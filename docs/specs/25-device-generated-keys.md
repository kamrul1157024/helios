# 25 — Device-Generated Keys & Unified `helios start` TUI

## Problem

Two problems to solve together:

### 1. Private Key in QR Code

The current auth flow transmits the **Ed25519 private key seed** through the QR code (`keypair.go:38`):

```
helios://setup?key=<PRIVATE_KEY_SEED>
```

- If anyone photographs or intercepts the QR, they have the full private key
- The `key_already_claimed` check is a race condition, not a security guarantee
- No device confirmation — any device with the key auto-activates

### 2. Too Many Commands

Current CLI has `start` (non-blocking), `setup` (TUI wizard), `show`, `pair` confusion. Users don't know which to run.

## Solution

### Device-Generated Keys

The private key **never leaves the device**. The QR code carries only a short-lived, single-use **pairing token**. Device generates its own Ed25519 keypair locally.

### Unified `helios start`

One command: `helios start`. Blocking TUI that:
- Starts daemon if not running
- Starts tunnel if configured and not running
- Shows status + devices + landing page link
- Shows pairing QR with auto-refreshing token
- Device confirmation prompt (y/n)
- Back to main screen after approval (ready for more devices)
- **q quits the TUI** — daemon + tunnel keep running

Remove `helios setup` and `helios show` entirely.

---

## Pairing Flow Diagram

```
┌─────────────┐                              ┌─────────────┐
│  CLI TUI    │                              │  Mobile App │
│  (Server)   │                              │  (Client)   │
└──────┬──────┘                              └──────┬──────┘
       │                                            │
       │  1. Generate pairing token                 │
       │     (32-byte random, expires 120s)         │
       │     Store in DB with status=pending        │
       │                                            │
       │  2. Show QR in TUI                         │
       │                                            │
       │         ┌──────────┐                       │
       │         │ QR Code  │    3. Scan QR         │
       │────────>│ ╔══════╗ │──────────────────────>│
       │         │ ║██  ██║ │                       │
       │         │ ╚══════╝ │   helios://pair       │
       │         └──────────┘   ?url=<SERVER_URL>   │
       │                        &token=<PAIRING>    │
       │                                            │
       │                        4. Generate own     │
       │                           Ed25519 keypair  │
       │                           LOCALLY          │
       │                                            │
       │    5. POST /api/auth/pair                  │
       │       {token, public_key, kid}             │
       │<───────────────────────────────────────────│
       │                                            │
       │    6. Validate token:                      │
       │       - exists? not expired? not used?     │
       │       ✓ → burn token, store public_key     │
       │         device status = "pending"          │
       │       ✗ → 401                              │
       │───────────────────────────────────────────>│
       │                                            │
       │                        7. Sign JWT with    │
       │                           private key      │
       │                                            │
       │    8. POST /api/auth/login                 │
       │       {token: <signed JWT>}                │
       │<───────────────────────────────────────────│
       │                                            │
       │    9. Verify JWT, set cookie               │
       │       device stays "pending"               │
       │       (NO auto-activate)                   │
       │───────────────────────────────────────────>│
       │                                            │
       │   10. TUI detects pending device           │  Mobile shows:
       │       shows confirmation prompt            │  "Waiting for
       │       "Allow this device? [y/n]"           │   approval..."
       │                                            │
       │   11. User presses y                       │
       │       POST /internal/device/activate       │
       │       device status → "active"             │
       │                                            │
       │   12. TUI returns to main screen           │  Mobile polls
       │       with updated device list             │  /api/auth/device/me
       │                                            │  sees status=active
       │                                            │  → proceeds to dashboard
       ▼                                            ▼
```

---

## Threat Comparison

```
 Attack                  Current (QR=privkey)   Proposed (QR=token)
┌───────────────────────┬──────────────────────┬──────────────────────┐
│ QR photographed       │ CRITICAL             │ Safe                 │
│                       │ attacker gets full   │ token expires 120s,  │
│                       │ private key          │ single-use, no key   │
├───────────────────────┼──────────────────────┼──────────────────────┤
│ Database leaked       │ Safe                 │ Safe                 │
│                       │ only public keys     │ only public keys     │
├───────────────────────┼──────────────────────┼──────────────────────┤
│ MITM races pairing    │ Can replay the       │ Token single-use +   │
│                       │ private key          │ TUI confirmation     │
│                       │                      │ blocks activation    │
├───────────────────────┼──────────────────────┼──────────────────────┤
│ Server compromised    │ Can't sign           │ Can't sign           │
├───────────────────────┼──────────────────────┼──────────────────────┤
│ Stolen phone          │ Revoke device        │ Revoke device        │
└───────────────────────┴──────────────────────┴──────────────────────┘
```

---

## CLI Command Changes

### Before

```
helios start      Non-blocking. Start daemon, print status + QR, exit.
helios setup      Blocking TUI wizard (tunnel + QR + wait).
helios show       Print status, devices, QR. Exit.
```

### After

```
helios start      Blocking TUI. Start daemon + tunnel, show status,
                  show landing page link, show pairing QR, device
                  confirmation. q quits (daemon stays running).

helios stop       Stop daemon (tunnel dies with it as child process).

helios devices    Blocking TUI. List, detail, revoke. (unchanged)
```

**Removed:** `helios setup`, `helios show`

**Unchanged:** `helios stop`, `helios devices`, `helios daemon *`, `helios auth *`, `helios hooks *`, `helios logs`, `helios cleanup`

---

## `helios start` TUI Screens

### Main Screen (single screen with everything)

```
┌─────────────────────────────────────────┐
│  helios                                 │
│                                         │
│  ✓ Daemon running (7654/7655)           │
│  ✓ Tunnel: https://abc-xyz... (cf)      │
│  ✓ Hooks installed                      │
│                                         │
│  Devices:                               │
│  * Android — Helios App  push:on  2m    │
│                                         │
│  Download app:                          │
│      ┌───────────────────────┐          │
│      │      ▄▄▄ █▄█ ▄▄▄     │  ← landing page QR
│      │      █▄█ ▄▄▄ █▄█     │    (tunnel URL, no secret)
│      └───────────────────────┘          │
│  https://abc-xyz.trycloudflare.com      │
│                                         │
│  Pair a new device:                     │
│      ┌───────────────────────┐          │
│      │      ▄▄▄ █▄█ ▄▄▄     │  ← pairing QR
│      │      █▄█ ▄▄▄ █▄█     │    (helios://pair?url=...&token=...)
│      └───────────────────────┘          │
│  Expires in 1:47                        │
│                                         │
│  q quit                                 │
└─────────────────────────────────────────┘
```

Two QR codes:
1. **Download QR** — just the tunnel URL (landing page with app download links). No secret, never expires. Safe to photograph.
2. **Pairing QR** — `helios://pair?url=...&token=...`. Ephemeral token, auto-refreshes every 120s.

### First-Time (no tunnel)

If no tunnel is configured, show tunnel selection before the main screen (same as current setup flow: tunnel select → binary check → tunnel starting → main screen).

### Device Confirmation (interrupts main screen)

When a pending device is detected:

```
┌─────────────────────────────────────────┐
│  helios — New Device                    │
│                                         │
│  A device wants to pair:                │
│                                         │
│    Name:     Android — Helios App       │
│    Platform: Android                    │
│    KID:      a1b2c3d4-...              │
│                                         │
│  Allow this device?                     │
│                                         │
│  y approve    n reject                  │
└─────────────────────────────────────────┘
```

- **y** → `POST /internal/device/activate` → back to main screen with updated device list + fresh QR
- **n** → `POST /internal/device/revoke` → back to main screen with fresh QR

### After Approval (returns to main screen)

```
┌─────────────────────────────────────────┐
│  helios                                 │
│                                         │
│  ✓ Daemon running (7654/7655)           │
│  ✓ Tunnel: https://abc-xyz... (cf)      │
│  ✓ Hooks installed                      │
│                                         │
│  Devices:                               │
│  * Android — Helios App  push:on  2m    │
│  * iPhone — Helios App   push:on  now   │
│                                         │
│  Download app:                          │
│      ┌───────────────────────┐          │
│      │      ▄▄▄ █▄█ ▄▄▄     │          │
│      │      █▄█ ▄▄▄ █▄█     │          │
│      └───────────────────────┘          │
│  https://abc-xyz.trycloudflare.com      │
│                                         │
│  Pair a new device:                     │
│      ┌───────────────────────┐          │
│      │      ▄▄▄ █▄█ ▄▄▄     │          │
│      │      █▄█ ▄▄▄ █▄█     │          │
│      └───────────────────────┘          │
│  Expires in 1:58                        │
│                                         │
│  q quit                                 │
└─────────────────────────────────────────┘
```

---

## Backend Changes

### 1. New DB Table: `pairing_tokens`

```sql
CREATE TABLE IF NOT EXISTS pairing_tokens (
    token      TEXT PRIMARY KEY,
    status     TEXT NOT NULL DEFAULT 'pending',   -- pending | claimed | expired
    claimed_by TEXT,                               -- kid of device that claimed it
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_pairing_tokens_status ON pairing_tokens(status);
```

### 2. New Store Methods (`internal/store/pairing_tokens.go`)

```go
type PairingToken struct {
    Token     string  `json:"token"`
    Status    string  `json:"status"`
    ClaimedBy *string `json:"claimed_by,omitempty"`
    ExpiresAt string  `json:"expires_at"`
    CreatedAt string  `json:"created_at"`
}

// CreatePairingToken stores a new token with expiry.
func (s *Store) CreatePairingToken(token string, expiresAt time.Time) error

// ClaimPairingToken atomically validates and claims a token.
// Returns error if token doesn't exist, is expired, or already claimed.
func (s *Store) ClaimPairingToken(token, kid string) error

// CleanExpiredPairingTokens removes tokens older than their expiry.
func (s *Store) CleanExpiredPairingTokens() error
```

**`ClaimPairingToken` implementation (atomic):**
```go
func (s *Store) ClaimPairingToken(token, kid string) error {
    result, err := s.db.Exec(
        `UPDATE pairing_tokens
         SET status = 'claimed', claimed_by = ?
         WHERE token = ? AND status = 'pending' AND expires_at > datetime('now')`,
        kid, token,
    )
    if err != nil {
        return fmt.Errorf("claim token: %w", err)
    }
    rows, _ := result.RowsAffected()
    if rows == 0 {
        return fmt.Errorf("token invalid, expired, or already claimed")
    }
    return nil
}
```

### 3. New Internal Endpoint: `POST /internal/device/activate`

```go
func (s *InternalServer) handleDeviceActivate(w http.ResponseWriter, r *http.Request) {
    var req struct {
        KID string `json:"kid"`
    }
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.KID == "" {
        jsonError(w, "missing kid", http.StatusBadRequest)
        return
    }

    if err := s.shared.DB.ActivateDevice(req.KID); err != nil {
        jsonError(w, "failed to activate device", http.StatusInternalServerError)
        return
    }

    jsonResponse(w, http.StatusOK, map[string]interface{}{
        "activated": true,
    })
}
```

Register in `NewInternalServer`:
```go
mux.HandleFunc("POST /internal/device/activate", s.handleDeviceActivate)
```

### 4. Update `internal/auth/keypair.go`

Remove `SetupPayload()` and `PrivateKeyBase64()`. Add:

```go
// GeneratePairingToken creates a cryptographically random token string.
func GeneratePairingToken() (string, error) {
    b := make([]byte, 32)
    if _, err := rand.Read(b); err != nil {
        return "", fmt.Errorf("generate pairing token: %w", err)
    }
    return base64.RawURLEncoding.EncodeToString(b), nil
}
```

### 5. Update `POST /api/auth/pair` (`internal/server/api.go`)

Now requires `token` field. Validates pairing token before accepting public key:

```go
func (s *PublicServer) handlePair(w http.ResponseWriter, r *http.Request) {
    var req struct {
        Token     string `json:"token"`
        KID       string `json:"kid"`
        PublicKey string `json:"public_key"`
    }
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Token == "" || req.KID == "" || req.PublicKey == "" {
        jsonError(w, "missing token, kid, or public_key", http.StatusBadRequest)
        return
    }

    if _, err := auth.PublicKeyFromBase64(req.PublicKey); err != nil {
        jsonError(w, "invalid public key format", http.StatusBadRequest)
        return
    }

    if err := s.shared.DB.ClaimPairingToken(req.Token, req.KID); err != nil {
        jsonResponse(w, http.StatusUnauthorized, map[string]interface{}{
            "success": false,
            "error":   "invalid_token",
            "message": "Pairing token is invalid, expired, or already used. Generate a new QR from the terminal.",
        })
        return
    }

    if err := s.shared.DB.UpsertDevice(req.KID, req.PublicKey); err != nil {
        jsonError(w, "failed to register device", http.StatusInternalServerError)
        return
    }

    jsonResponse(w, http.StatusOK, map[string]interface{}{
        "success": true,
        "kid":     req.KID,
    })
}
```

### 6. Update `POST /api/auth/login`

Remove auto-activation. Device stays `pending` until CLI confirms:

```go
// Remove this line from handleLogin:
// s.shared.DB.ActivateDevice(kid)

// Login accepts pending devices (for JWT/cookie) but doesn't activate.
// The cookie works, but cookieAuthMiddleware only allows "active" devices
// for protected endpoints. Pending devices can only hit /api/auth/device/me.
```

Add `/api/auth/device/me` to a new "pending-ok" middleware group so pending devices can poll their own status.

### 7. Update `POST /internal/device/create`

Returns pairing token instead of private key:

```go
func (s *InternalServer) handleDeviceCreate(w http.ResponseWriter, r *http.Request) {
    token, err := auth.GeneratePairingToken()
    if err != nil {
        jsonError(w, "failed to generate pairing token", http.StatusInternalServerError)
        return
    }

    expiresAt := time.Now().Add(2 * time.Minute)
    if err := s.shared.DB.CreatePairingToken(token, expiresAt); err != nil {
        jsonError(w, "failed to store pairing token", http.StatusInternalServerError)
        return
    }

    setupURL := ""
    if TunnelManager != nil {
        status := TunnelManager.Status()
        if url, ok := status["public_url"].(string); ok && url != "" {
            setupURL = fmt.Sprintf("helios://pair?url=%s&token=%s", url, token)
        }
    }

    jsonResponse(w, http.StatusOK, map[string]interface{}{
        "token":      token,
        "expires_in": 120,
        "setup_url":  setupURL,
    })
}
```

### 8. Update `POST /internal/device/rekey`

Same pattern — generate a pairing token instead of a keypair.

### 9. Pending Device Middleware

The current `cookieAuthMiddleware` requires `active` status. We need pending devices to poll `/api/auth/device/me` to check when they've been approved. Two options:

**Option A:** Add `/api/auth/device/me` GET to the "no auth" group, but protect it with the cookie (just allow `pending` status too).

**Option B (simpler):** Create a `pendingOrActiveAuthMiddleware` that accepts both statuses, use it only for `/api/auth/device/me` GET.

Go with Option B:

```go
// In NewPublicServer, pull /api/auth/device/me GET out of the protected group
// and into its own group with pending-ok middleware:
pendingAuth := pendingOrActiveAuthMiddleware(shared.DB)
pendingMux := http.NewServeMux()
pendingMux.HandleFunc("GET /api/auth/device/me", s.handleDeviceMe)
mux.Handle("/api/auth/device/", pendingAuth(pendingMux))
```

### 10. Migration

Add to `store.go` migrations:

```go
`CREATE TABLE IF NOT EXISTS pairing_tokens (
    token      TEXT PRIMARY KEY,
    status     TEXT NOT NULL DEFAULT 'pending',
    claimed_by TEXT,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
)`,
`CREATE INDEX IF NOT EXISTS idx_pairing_tokens_status ON pairing_tokens(status)`,
```

### 11. Periodic Cleanup

In daemon startup:

```go
go func() {
    ticker := time.NewTicker(5 * time.Minute)
    defer ticker.Stop()
    for range ticker.C {
        db.CleanExpiredPairingTokens()
    }
}()
```

---

## TUI Changes

### Remove `helios setup` TUI

Delete the setup-specific TUI wizard. All its functionality moves into `helios start`.

### New `helios start` TUI (`internal/tui/start.go`)

Replaces both the old non-blocking `handleStart()` in `main.go` and the old `setup.go` TUI.

**Screens:**

```go
const (
    startScreenLoading      screen = iota  // checking daemon, starting if needed
    startScreenTunnelSelect                // first time only: pick tunnel provider
    startScreenBinaryMissing               // tunnel binary not found
    startScreenTunnelStarting              // starting tunnel...
    startScreenCustomURL                   // custom URL input
    startScreenMain                        // main dashboard: status + devices + QR
    startScreenConfirmDevice               // "Allow this device? y/n"
    startScreenError                       // error
)
```

**Model fields (new/changed):**
```go
type StartModel struct {
    // ... existing fields from SetupModel ...

    // Pairing token
    pairingToken    string
    tokenExpiresAt  time.Time

    // Device confirmation
    pendingDevice   *deviceInfo

    // Landing page URL (= tunnel URL)
    landingURL      string

    // Device list (refreshed after each approval)
    devices         []deviceInfo
}
```

**Key behaviors:**

1. **Token countdown** — tick every second, display "Expires in M:SS". When hits 0, auto-call `createDevice` → new token → new QR. No user action needed.

2. **Pending device poll** — poll `/internal/device/list` every 2 seconds. If a new `pending` device appears → switch to `startScreenConfirmDevice`.

3. **Device confirmation** — `y` calls `POST /internal/device/activate`, `n` calls `POST /internal/device/revoke`. Both return to `startScreenMain` with refreshed device list and fresh QR.

4. **Landing page link** — display tunnel URL above the QR so users can visit it to download the app.

5. **q quits** — only from `startScreenMain`. Daemon stays running.

### Update TUI Client (`internal/tui/client.go`)

```go
// Updated response — no more Key field
type deviceCreateResponse struct {
    Token     string `json:"token"`
    ExpiresIn int    `json:"expires_in"`
    SetupURL  string `json:"setup_url"`
}

// New method
func (c *client) deviceActivate(kid string) error {
    body, _ := json.Marshal(map[string]string{"kid": kid})
    resp, err := c.httpClient.Post(
        c.baseURL+"/internal/device/activate",
        "application/json",
        bytes.NewReader(body),
    )
    if err != nil {
        return err
    }
    resp.Body.Close()
    return nil
}
```

---

## `cmd/helios/main.go` Changes

### Remove

- `handleStart()` — replaced by TUI
- `handleSetup()` — removed entirely
- `handleShow()` — removed entirely
- `showPairingQR()` — no longer needed
- `printDevices()` — moved into TUI
- `case "setup":` — removed
- `case "show":` — removed

### Update `handleStart()`

```go
func handleStart() {
    cfg, err := daemon.LoadConfig()
    if err != nil {
        fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
        os.Exit(1)
    }
    if err := tui.RunStart(cfg.Server.InternalPort, cfg.Server.PublicPort); err != nil {
        fmt.Fprintf(os.Stderr, "Error: %v\n", err)
        os.Exit(1)
    }
}
```

### Update Usage

```go
func printUsage() {
    fmt.Println(`helios - orchestrates AI coding agents on your machine

Usage:
  helios <command> [options]

Commands:
  start                 Start helios (daemon + tunnel + device pairing TUI)
  stop                  Stop daemon and tunnel
  devices               Device management (TUI)

  daemon start [flags]  Start the helios daemon directly
  daemon stop           Stop the running daemon
  daemon status         Show daemon status

  auth init             Generate pairing QR (non-interactive)
  auth devices          List trusted devices
  auth revoke <kid>     Revoke a device

  logs [flags]          Show daemon and device logs
  hooks install         Install Claude Code hooks
  hooks show            Print hook config JSON
  hooks remove          Remove helios hooks
  cleanup [target]      Remove helios data

  version               Show version
  help                  Show this help`)
}
```

---

## Mobile App Changes (`mobile/lib/services/auth_service.dart`)

### 1. Update `setup()` — Generate Keys Locally

```dart
Future<SetupResult> setup(String pairingToken, String serverUrl) async {
    try {
        // 1. Generate Ed25519 keypair LOCALLY
        final algorithm = Ed25519();
        _keyPair = await algorithm.newKeyPair();

        // 2. Device ID
        _deviceId = await _secureStorage.read(key: _deviceIdKey);
        if (_deviceId == null) {
            _deviceId = const Uuid().v4();
            await _secureStorage.write(key: _deviceIdKey, value: _deviceId);
        }

        _serverUrl = serverUrl;
        final prefs = await SharedPreferences.getInstance();
        await prefs.setString(_serverUrlKey, serverUrl);

        // 3. Get public key
        final publicKey = await _getPublicKeyBase64();

        // 4. Pair with token (NOT private key)
        final pairResp = await http.post(
            Uri.parse('$serverUrl/api/auth/pair'),
            headers: {'Content-Type': 'application/json'},
            body: jsonEncode({
                'token': pairingToken,
                'kid': _deviceId,
                'public_key': publicKey,
            }),
        );
        final pairData = jsonDecode(pairResp.body);
        if (pairData['success'] != true) {
            if (pairData['error'] == 'invalid_token') {
                return SetupResult.error(
                    'This QR code has expired or already been used. '
                    'Generate a new one from the terminal.',
                );
            }
            return SetupResult.error(pairData['message'] ?? 'Pairing failed');
        }

        // 5. Sign JWT and login
        final jwt = await _signJWT();
        final loginResp = await http.post(
            Uri.parse('$serverUrl/api/auth/login'),
            headers: {'Content-Type': 'application/json'},
            body: jsonEncode({'token': jwt}),
        );
        if (loginResp.statusCode != 200) {
            return SetupResult.error('Login failed');
        }

        // 6. Extract cookie
        final setCookie = loginResp.headers['set-cookie'];
        if (setCookie != null) {
            final match = RegExp(r'helios_token=([^;]+)').firstMatch(setCookie);
            if (match != null) {
                _cookie = match.group(1);
                await _secureStorage.write(key: _cookieKey, value: _cookie);
            }
        }
        _cookie ??= jwt;
        await _secureStorage.write(key: _cookieKey, value: _cookie);

        // 7. Store private key seed for persistence
        final seed = await _keyPair!.extractPrivateKeyBytes();
        final seedB64 = _base64urlEncode(Uint8List.fromList(seed.sublist(0, 32)));
        await _secureStorage.write(key: _keyStorageKey, value: seedB64);

        // 8. Update device metadata
        await _updateDeviceMetadata();

        // 9. Wait for CLI approval (device is "pending" until approved)
        final approved = await _waitForApproval();
        if (!approved) {
            return SetupResult.error('Device was rejected by the server.');
        }

        _isAuthenticated = true;
        notifyListeners();
        return SetupResult.success();
    } catch (e) {
        return SetupResult.error('Setup failed: $e');
    }
}
```

### 2. New Method: `_waitForApproval()`

Polls `/api/auth/device/me` until status changes from `pending` to `active` (or `revoked`):

```dart
/// Poll until CLI user approves or rejects this device.
Future<bool> _waitForApproval({Duration timeout = const Duration(minutes: 5)}) async {
    final deadline = DateTime.now().add(timeout);
    while (DateTime.now().isBefore(deadline)) {
        try {
            final resp = await authGet('/api/auth/device/me');
            if (resp.statusCode == 200) {
                final data = jsonDecode(resp.body);
                if (data['status'] == 'active') return true;
                if (data['status'] == 'revoked') return false;
            }
        } catch (_) {}
        await Future.delayed(const Duration(seconds: 2));
    }
    return false; // timeout
}
```

### 3. Update QR Parsing

```dart
// Old: helios://setup?key=<PRIVATE_KEY>&server=<URL>
// New: helios://pair?url=<SERVER_URL>&token=<PAIRING_TOKEN>

final uri = Uri.parse(qrData);
final token = uri.queryParameters['token'];
final serverUrl = uri.queryParameters['url'];
await authService.setup(token!, serverUrl!);
```

### 4. Approval Waiting UI

The mobile app should show a "Waiting for approval..." screen with a spinner after the pairing step completes. This is shown between login and dashboard.

### 5. No Change To

- `_signJWT()` — same
- `_authHeaders()` — same
- `loadStoredCredentials()` — same
- `authGet()` / `authPost()` — same
- `logout()` — same

---

## QR Payload Change

**Before:**
```
helios://setup?key=<BASE64_PRIVATE_KEY_SEED>&server=<URL>
```

**After:**
```
helios://pair?url=<SERVER_URL>&token=<PAIRING_TOKEN>
```

---

## Files Changed

### Backend (Go)

| File | Change |
|------|--------|
| `internal/store/store.go` | Add `pairing_tokens` table migration |
| `internal/store/pairing_tokens.go` | **New** — `CreatePairingToken`, `ClaimPairingToken`, `CleanExpiredPairingTokens` |
| `internal/auth/keypair.go` | Remove `SetupPayload()`, `PrivateKeyBase64()`. Add `GeneratePairingToken()` |
| `internal/server/api.go` | Update `handlePair` (validate token), `handleDeviceCreate` (return token not key), `handleLogin` (remove auto-activate), `handleDeviceRekey` (use token) |
| `internal/server/server.go` | Add `POST /internal/device/activate` route. Add pending-ok middleware for `/api/auth/device/me` GET |
| `internal/server/middleware.go` | Add `pendingOrActiveAuthMiddleware` |
| `internal/daemon/daemon.go` | Add expired token cleanup goroutine |
| `internal/tui/start.go` | **New** — unified `helios start` TUI (replaces `setup.go`) |
| `internal/tui/setup.go` | **Delete** |
| `internal/tui/view.go` | Rewrite for new start TUI screens |
| `internal/tui/client.go` | Update `deviceCreateResponse` (token not key), add `deviceActivate()` |
| `cmd/helios/main.go` | Remove `handleSetup`, `handleShow`, `showPairingQR`, `printDevices`. Rewrite `handleStart` to launch TUI. Update usage. |

### Mobile (Dart/Flutter)

| File | Change |
|------|--------|
| `mobile/lib/services/auth_service.dart` | `setup()` generates keypair locally, accepts token. Add `_waitForApproval()`. |
| QR scan screen | Parse `helios://pair?url=...&token=...` |
| Setup screen | Add "Waiting for approval..." state between login and dashboard |

---

## Backwards Compatibility

Breaking change in pairing protocol. Existing paired devices continue to work. Only new pairing flows affected.

---

## Implementation Order

1. Add `pairing_tokens` table migration to `store.go`
2. Create `internal/store/pairing_tokens.go`
3. Add `GeneratePairingToken()` to `internal/auth/keypair.go`
4. Add `POST /internal/device/activate` endpoint
5. Add `pendingOrActiveAuthMiddleware` for device/me
6. Update `handleDeviceCreate` → return token
7. Update `handlePair` → validate pairing token
8. Update `handleLogin` → remove auto-activate
9. Update `handleDeviceRekey` → use token
10. Add cleanup goroutine in daemon
11. Rewrite TUI: new `start.go` + updated `view.go` + updated `client.go`
12. Delete `setup.go`
13. Update `main.go` — remove setup/show, rewrite start
14. Remove `SetupPayload()`, `PrivateKeyBase64()` from keypair.go
15. Update mobile `auth_service.dart` — local keygen + token + approval wait
16. Update mobile QR scan + approval waiting UI
