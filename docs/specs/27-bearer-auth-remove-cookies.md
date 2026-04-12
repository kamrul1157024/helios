# 27 — Bearer Token Auth: Remove Cookies, Client-Signed JWTs

## Problem

The current auth flow is broken and overly complex:

1. **Token lifetime mismatch** — The mobile client signs a JWT with 1-hour `exp` (`host_manager.dart:479`). The server stores this exact JWT as the cookie value (`api.go:308`) with `MaxAge: 30 days`. After 1 hour, `jwt.Parse` in the middleware rejects it — every API call returns 401.

2. **No recovery** — The mobile client treats 401 the same as a network error (`_consecutiveFailures++`), showing a generic offline banner instead of re-authenticating.

3. **Cookie complexity** — Cookies are a browser mechanism. The mobile client manually constructs `Cookie: helios_token=...` headers, parses `Set-Cookie` responses, and stores the value in secure storage. This is unnecessary indirection — the client already holds the Ed25519 private key and can sign JWTs directly.

## Design

Replace cookie-based auth with `Authorization: Bearer <jwt>` headers. The mobile client signs a JWT with 1-hour expiry, caches it in memory, and re-signs only when expired or rejected. The server validates the signature against the device's public key (already stored in DB).

### Why this works

- The client **owns the private key** — it can mint a valid JWT anytime without server interaction
- The server **has the public key** — it can validate without shared secrets or session state
- No cookies, no login endpoint, no token storage, no refresh flow
- A lost/stolen device is revoked by setting `status = 'revoked'` in the DB — the middleware already checks device status on every request

### JWT structure (client-signed, unchanged)

```
Header:  { "alg": "EdDSA", "typ": "JWT", "kid": "<device-id>" }
Payload: { "iat": <unix-timestamp>, "exp": <iat + 3600>, "sub": "helios-client" }
```

The `kid` in the header identifies which device's public key to use for verification.

### Token caching strategy

Signing Ed25519 on every request is wasteful. The client caches the JWT in memory and only re-signs when:

1. **Proactive refresh** — The cached token's `exp` is within 5 minutes of now (sign before it expires)
2. **Reactive refresh** — A request returns 401 (token was rejected — sign a fresh one and retry once)

This means ~1 sign per hour during active use, not 1 per request.

## Changes

### Server

#### `internal/server/middleware.go`

Replace `jwtCookieMiddleware` with `bearerAuthMiddleware`:

- Read token from `Authorization: Bearer <token>` header instead of cookie
- Keep the existing `auth.ValidateJWT` + device status check logic
- Remove all cookie references (`cookieName`, `r.Cookie()`)

```go
func bearerAuthMiddleware(db *store.Store, allowPending bool) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            authHeader := r.Header.Get("Authorization")
            if !strings.HasPrefix(authHeader, "Bearer ") {
                // respond 401
            }
            token := strings.TrimPrefix(authHeader, "Bearer ")
            kid, err := auth.ValidateJWT(token, func(kid string) (ed25519.PublicKey, error) {
                // same device lookup as today
            })
            // ...
        })
    }
}
```

#### `internal/server/api.go`

- **Remove `handleLogin`** — no longer needed. The client authenticates on every request via Bearer token.
- **Update `handlePair`** — keep as-is (no auth required, public endpoint).

#### `internal/server/server.go`

- Remove `POST /api/auth/login` route
- Rename `cookieAuth` → `bearerAuth`, `pendingAuth` uses same new middleware
- Remove `cookieName` constant

### Mobile

#### `mobile/lib/services/daemon_api_service.dart`

Replace cookie-based `_authHeaders()` with Bearer token + in-memory cache:

- Remove `_cookie` field, `setCookie()`, `cookie` getter
- Add `_privateKey` (Ed25519 seed) and `_deviceId` fields
- Add `_cachedToken` / `_tokenExpiresAt` fields for in-memory cache
- Add `_getToken()` — returns cached JWT if still valid (>5 min until `exp`), otherwise signs a fresh one
- `_authHeaders()` calls `_getToken()` and returns `{'Authorization': 'Bearer <jwt>'}`
- Add 401 retry logic to `_authGet`, `_authPost`, `_authPatch`, `_authDelete` — on 401, invalidate cache, re-sign, retry once
- SSE connection (`_connect`) uses the same Bearer header; on 401 response, invalidate cache and reconnect

```dart
String? _cachedToken;
DateTime? _tokenExpiresAt;

Future<String> _getToken() async {
    final now = DateTime.now().toUtc();
    if (_cachedToken != null &&
        _tokenExpiresAt != null &&
        _tokenExpiresAt!.isAfter(now.add(const Duration(minutes: 5)))) {
        return _cachedToken!;
    }
    _cachedToken = await _signJWT();
    _tokenExpiresAt = now.add(const Duration(hours: 1));
    return _cachedToken!;
}

void _invalidateToken() {
    _cachedToken = null;
    _tokenExpiresAt = null;
}

Future<Map<String, String>> _authHeaders() async {
    final token = await _getToken();
    return {'Authorization': 'Bearer $token'};
}
```

#### `mobile/lib/services/host_manager.dart`

- `addHost`: After pairing + approval, store the private key seed (already does). Remove cookie extraction, `/api/auth/login` call, and cookie storage.
- `_startServiceFor`: Pass private key + device ID to `DaemonAPIService` instead of cookie.
- `_waitForApproval`: Use Bearer header instead of cookie.
- `_updateDeviceMetadata`: Use Bearer header instead of cookie.
- `_fetchAndSetHostname`: Use Bearer header instead of cookie.
- Remove `helios_host_${id}_cookie` from secure storage.
- Remove legacy cookie migration key.

#### `mobile/lib/services/narration_service.dart`

- `ReporterHost`: Replace `cookie` field with a `getToken` callback (`Future<String> Function()`).
- `_ReporterConnection`: Call `getToken()` for SSE request headers.

#### `mobile/lib/screens/home_screen.dart` (line 264)

- Pass `getToken` callback from `DaemonAPIService` to `ReporterHost` instead of `cookie`.

#### `mobile/lib/screens/session_detail_screen.dart` (line 517)

- Same as above.

### What gets deleted

| File | What |
|---|---|
| `middleware.go` | `cookieName` const, `r.Cookie()` reads, `jwtCookieMiddleware` |
| `api.go` | `handleLogin` handler, `http.SetCookie` call |
| `server.go` | `POST /api/auth/login` route |
| `host_manager.dart` | Cookie extraction from `Set-Cookie`, `/api/auth/login` call, `helios_host_${id}_cookie` secure storage, `_legacyCookieKey` |
| `daemon_api_service.dart` | `_cookie` field, `setCookie()`, `cookie` getter |
| `narration_service.dart` | `cookie` field on `ReporterHost` and `_ReporterConnection` |

### What stays the same

- `auth.ValidateJWT` — unchanged, validates Ed25519 signature + `exp`
- `auth.CreateTestJWT` — unchanged, used by `helios auth verify`
- Device DB schema — unchanged
- Pairing flow (`/api/auth/pair`) — unchanged
- Device approval flow (`/api/auth/device/me`) — unchanged, just uses Bearer instead of cookie
- Device revocation — unchanged, middleware checks status on every request

## Migration

This is a breaking change for paired devices. Existing stored cookies will stop working since the server no longer reads cookies. Devices must re-pair.

This is acceptable because:
1. The current auth is already broken (1-hour JWT expiry)
2. The migration path is simple — remove the old host and scan a new QR code
