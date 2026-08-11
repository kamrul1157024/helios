# 30 — Tailscale as the Sole Remote Transport

**Status:** Draft — awaiting approval
**Supersedes:** `spec-tunnel-decoupling.md`, `spec-zrok-tunnel-provider.md`, `spec-localtunnel-provider.md`, `spec-localxpose-provider.md`, `spec-localhostrun-provider.md`
**Related:** `20-remote-access-and-auth.md`, `22-setup-and-security.md`, `25-device-generated-keys.md`, `multi-host-spec.md`

---

## 1. Motivation

The entire `internal/tunnel` package (2,817 lines across 15 files, 9 providers) exists to solve
exactly one problem: **give the mobile app a stable, TLS-terminated URL to reach the daemon.**

Every provider solves it badly in the same way — by renting a random hostname from a third party
that changes on every restart. Nearly all the accidental complexity in the package is scar tissue
from that instability:

| Mechanism | Why it exists | Survives under Tailscale? |
|---|---|---|
| `state.go` (PID/URL on disk) | Re-discover the URL after daemon restart | No — URL is a constant |
| `Manager.Adopt()` | Re-attach to an orphaned tunnel process so the URL survives | No — no process to adopt |
| Tunnel outlives `helios stop` | Killing the daemon would burn the URL | No — nothing to preserve |
| `OnZrokTokenCreated`, `OnLocaltunnelSubdomainAssigned` callbacks | Persist an assigned name back into config | No |
| 9-way provider switch + TUI picker | No single provider is reliable enough | No — one transport |

Tailscale removes the root cause. A MagicDNS name is permanent, tied to the machine, and its TLS
certificate is issued and renewed by Tailscale. The URL is knowable *before* the daemon starts.

The decision is already effectively made by deployment reality: **Tailscale will be installed on
both the phone and the desktop.** Once that is true, every third-party tunnel is strictly worse —
more moving parts, weaker auth, public exposure, and a URL that rots.

### 1.1 Non-goals

- Supporting access from devices *not* on the tailnet. This is an explicit capability regression;
  see §7.1.
- Replacing the existing Ed25519 device-pairing/JWT auth. Tailscale is a transport change, not an
  auth change. See §5.3.
- Multi-user / shared-tailnet access control. Out of scope.

---

## 2. Current State

### 2.1 Two servers

| Server | Bind | Auth | Purpose |
|---|---|---|---|
| Internal | `127.0.0.1:7654` (`server.go:92`) | none | Claude hooks, CLI admin |
| Public | `0.0.0.0:7655` (`server.go:215`) | Ed25519 JWT bearer + IP rate limit | Mobile app |

The public server binds `0.0.0.0` today. That is already a latent LAN-exposure bug independent of
this migration — anyone on the same Wi-Fi can reach the API and attempt pairing.

### 2.2 The tunnel package

```
cloudflare.go   292   CloudflareTunnel, NgrokTunnel, TailscaleTunnel,
                      LocalTunnel, CustomTunnel, killProcess   <- grab-bag file
tunnel.go       231   Tunnel interface, Manager, provider switch, adoptedTunnel
zrok.go         206
localhostrun.go 165
localxpose.go   150
localtunnel.go  119
state.go         88   PID/URL persistence
net.go           27   getLANIP (used only by LocalTunnel)
+ 6 test files 1,357
```

### 2.3 The existing Tailscale provider is broken

`cloudflare.go:163-220` already has a `TailscaleTunnel`. It does not work and must be rewritten,
not extended:

- `t.url = fmt.Sprintf("https://%s:%d", dnsName, localPort)` — wrong. Funnel terminates on 443,
  never on the local port.
- Uses `funnel` (public internet) rather than `serve` (tailnet-only).
- Scrapes the first `"DNSName"` match out of `tailscale status --json` with a regex; it happens to
  hit `Self` only because of JSON field ordering. It will silently return a *peer's* name if that
  ordering changes.
- `exec.LookPath("tailscale")` misses the macOS app bundle path
  (`/Applications/Tailscale.app/Contents/MacOS/Tailscale`).
- Sleeps 2s and declares success without verifying the serve config took effect.

---

## 3. Proposed Design

### 3.1 Transport: `tailscale serve`, not `funnel`

```
Phone (Tailscale on)                    Desktop (Tailscale on)
   │                                       │
   │  https://<machine>.<tailnet>.ts.net   │
   ├──────── WireGuard, tailnet-only ─────►│  tailscaled :443 (TLS terminated here)
                                           │        │
                                           │        └─► 127.0.0.1:7655  helios public server
                                           │
                                           └─────────  127.0.0.1:7654  helios internal server
                                                                       (hooks, CLI — unchanged)
```

**Serve, not Funnel.** Funnel exposes the port to the public internet; Serve keeps it inside the
tailnet. Since Tailscale is on both ends, Funnel buys nothing and costs a public attack surface.

Concretely:

```
tailscale serve --bg --https=443 http://127.0.0.1:7655
```

Properties this buys us:

- **Stable URL** — `https://<machine>.<tailnet>.ts.net`, computable before startup.
- **Real TLS** — Tailscale-issued cert, auto-renewed. No self-signed cert warnings, no
  `badCertificateCallback` in the Flutter client.
- **No child process** — serve config lives in `tailscaled`, not a helios-owned PID. This is what
  deletes `state.go` and `Adopt()`.
- **Network-level authz** — tailnet ACLs gate reachability before a byte hits helios.

### 3.2 Free plan is sufficient

Verified against current Tailscale docs: Serve, Funnel, MagicDNS, HTTPS certificates, ACL editing
and `tsnet` are all available on the Free plan (6 users, 100 devices). Plan tier is **not** a
discriminator for this design.

The macOS App Store build of Tailscale is sandboxed and cannot share *files/directories*, but it
**can share ports** — which is all helios needs. Both the CLI at `/usr/local/bin/tailscale` and the
bundle at `/Applications/Tailscale.app/Contents/MacOS/Tailscale` are present on the dev machine.

### 3.3 CLI shell-out vs `tsnet`

| | Shell out to `tailscale serve` | Embed `tsnet` |
|---|---|---|
| Node identity | Reuses the user's existing node | Helios becomes its own tailnet node |
| Setup | Zero — user already logged in | Needs an auth key / separate login |
| Device count | 0 extra | +1 device per machine |
| Failure mode | Legible (`tailscale serve status`) | Opaque, in-process |
| Dependency | none | `tailscale.com` Go module (large) |

**Recommendation: shell out.** `tsnet` is the better long-term story if helios ever wants its own
identity and ACL tag, but it front-loads auth-key management onto the user for no benefit today.
Revisit only if the shell-out proves brittle.

### 3.4 New package shape

Replace `internal/tunnel` with a much smaller `internal/tailscale`:

```go
// internal/tailscale/tailscale.go
type Transport struct { ... }

// Binary resolves the tailscale CLI, checking PATH then the macOS bundle path.
func Binary() (string, error)

// Status reports whether tailscaled is running, logged in, and HTTPS-capable.
func Status(ctx context.Context) (Status, error)

// DNSName returns Self.DNSName parsed from `tailscale status --json`
// via encoding/json — NOT a regex.
func DNSName(ctx context.Context) (string, error)

// Serve publishes 127.0.0.1:localPort at https://<dnsname>/ and returns the URL.
func (t *Transport) Serve(ctx context.Context, localPort int) (string, error)

// Reset removes the serve config for our port only.
func (t *Transport) Reset(ctx context.Context) error
```

Estimated ~250 lines including error handling, replacing 2,817.

`Status` must distinguish and report distinctly, because each has a different user remedy:
tailscaled not running · not logged in · HTTPS/MagicDNS disabled · serve already bound by another
process.

---

## 4. Changes by Component

### 4.1 Deletions

| File | Lines | Action |
|---|---|---|
| `zrok.go` + `zrok_test.go` | 397 | delete |
| `localhostrun.go` + test | 429 | delete |
| `localxpose.go` + test | 373 | delete |
| `localtunnel.go` + test | 315 | delete |
| `state.go` + `state_test.go` | 246 | delete |
| `providers_test.go` | 325 | delete |
| `cloudflare.go` | 292 | **split, don't delete** — see below |
| `tunnel.go` | 231 | rewrite → thin wrapper or remove |
| `net.go` | 27 | delete with `LocalTunnel` |

**~2,377 lines deleted, ~250 added.**

`cloudflare.go` is a grab-bag: it holds `CloudflareTunnel`, `NgrokTunnel`, `TailscaleTunnel`,
`LocalTunnel`, `CustomTunnel` **and** the shared `killProcess` helper used by `state.go` and
`tunnel.go`. It cannot be deleted wholesale. `killProcess` becomes dead once `state.go` and the
process-spawning providers go.

**Open question (§9.1):** keep `custom` as an escape hatch? It spawns no process and costs ~15
lines, and it is the only path left for a user behind their own reverse proxy.

### 4.2 `internal/server`

- `server.go:215` — `0.0.0.0` → `127.0.0.1`. Serve connects over loopback, so this both works and
  closes the pre-existing LAN exposure.
- `server.go:35` — update the stale comment.
- `middleware.go:72` — **required, silent-failure bug.** `ip := r.RemoteAddr` becomes `127.0.0.1`
  for *every* request behind Serve. The pairing rate limiter (5/min per IP) silently degrades into
  a single global bucket: one device pairing locks out all others, and an attacker on the tailnet
  is indistinguishable from a legitimate device. Must parse `X-Forwarded-For` (Serve sets it),
  trusting it **only** when `RemoteAddr` is loopback.

### 4.3 `internal/daemon`

- `daemon.go:77-121` — delete provider-config wiring and both `SaveConfig` callbacks.
- `daemon.go:139,142` — log the actual bind and the tailnet URL.
- `config.go` — delete `ZrokConfig`, `LocaltunnelConfig`, `LocalhostRunConfig`, `LocalxposeConfig`
  (~35 lines). Reduce `TunnelConfig` to `{ Enabled bool }`.
- **Config migration:** existing `~/.helios/config.json` files carry a `tunnel.provider` key. Must
  be tolerated and ignored, not rejected — a hard parse error on upgrade would brick the daemon.

### 4.4 TUI

`internal/tui/start.go` — replace the 9-way provider picker with a status panel:

```
Remote access
  Tailscale      ● connected   mds-macbook-air.tailXXXX.ts.net
  HTTPS certs    ● enabled
  Serving        ● https://mds-macbook-air.tailXXXX.ts.net → 127.0.0.1:7655

  [QR to pair]   [Stop serving]
```

When Tailscale is missing/logged out/HTTPS-disabled, show the specific remedy rather than a generic
failure. QR payload is unchanged: `helios://pair?url=%s&token=%s`.

### 4.5 CLI

`handleTunnel` in `cmd/helios/main.go`: keep `helios tunnel status` and `helios tunnel stop`,
drop provider arguments. Consider aliasing to `helios remote` (non-breaking, `tunnel` retained).

### 4.6 Mobile

**This is the migration's sharpest edge.** `HostConnection` exposes only `updateHostLabel` and
`updateHostColor` (`host_manager.dart:364,373`). **`serverUrl` is fixed at pairing time and cannot
be changed.**

So every existing paired host breaks on cutover with no in-app recovery. Two options:

1. **Add `updateHostUrl(hostId, newUrl)`** — preserves the Ed25519 keypair and device registration;
   user edits the URL in host detail. ~30 lines. **Recommended.**
2. **Force re-pair** — user deletes and re-adds each host. Zero code, worse UX, and it orphans the
   device row on the daemon side.

Also required: a clear error when the phone's Tailscale is off. Today a dead `serverUrl` surfaces
as a generic "Could not reach server" (`host_manager.dart:219`), which will be the single most
common support question after this change.

---

## 5. Security

### 5.1 Net improvement

Public exposure goes from "a guessable `*.trycloudflare.com` hostname on the open internet,
protected only by app-layer JWT" to "reachable only from devices already authenticated to the
tailnet." Tailnet ACLs become a second, network-level gate.

### 5.2 Certificate Transparency disclosure

Enabling HTTPS publishes `<machine>.<tailnet>.ts.net` to public CT logs — machine names and the
tailnet name become publicly enumerable. It does not expose any service. **This is the user's
call** and is a prerequisite, not a consequence, of this work.

### 5.3 Keep the JWT layer

Tailscale identity (`Tailscale-User-Login` header / `WhoIs()`) is available and tempting, but:

- Tailnet identity is per-*user*, not per-*device*. Helios's approval model is per-device.
- Dropping JWT would make the daemon fully trust anything that reaches loopback, including any
  other local process — a real weakening.

**Keep bearer auth as-is.** Tailnet identity may later be used to *auto-approve* pairing requests
from the owner's own account, but that is a follow-up.

---

## 6. Phasing

**Phase 0 — Validate (gate; nothing merges before this passes).**
The single load-bearing unknown is whether `tailscale serve` proxies **Server-Sent Events** without
buffering, and whether it tolerates **long-lived blocking requests**. Both are core to helios:
`sse.go:79-91` holds streams open indefinitely with a 30s heartbeat, and `waitForDecision`
(`hooks.go:576`) holds a Claude hook request open for **up to 5 minutes** awaiting a human decision.

If Serve buffers SSE, approvals and narration break and the whole design is invalid.

Test in isolation, no helios changes:
1. Toy Go server on `:9999` emitting an SSE tick every 2s and one endpoint that sleeps 5 min.
2. `tailscale serve --bg --https=8443 http://127.0.0.1:9999`
3. From another tailnet device: `curl -N https://<host>.ts.net:8443/events` — ticks must arrive
   incrementally, not in one flush at the end.
4. `curl -m 400 https://<host>.ts.net:8443/slow` — must not be cut off before 5 min.
5. `tailscale serve --https=8443 off`

~10 minutes. **Requires explicit permission — `tailscale serve` mutates global `tailscaled` state.**

**Phase 1 — Add alongside.** New `internal/tailscale` package; register as a provider next to the
existing ones. Bind public server to loopback. Fix the `X-Forwarded-For` limiter. Nothing deleted
yet — fully reversible.

**Phase 2 — Migrate UX.** TUI status panel, CLI simplification, mobile `updateHostUrl`, config
migration tolerance. Dogfood on the real phone.

**Phase 3 — Delete.** Remove the 8 other providers, `state.go`, `Adopt()`, callbacks, config
structs. Single commit, easy to revert.

---

## 7. Risks

### 7.1 Capability regression: no non-tailnet access

Today a `trycloudflare.com` URL can be handed to anyone. After this, the client must be on the
tailnet. Accepted per the deployment decision, but it removes any "quick share" or demo path.
Mitigation if ever needed: Funnel on 8443 alongside Serve on 443 — the public/private boundary is
**per-port, not per-path**, and Funnel is restricted to 443/8443/10000.

### 7.2 SSE buffering — see Phase 0. Invalidates the design if it fails.

### 7.3 Mobile VPN reliability

Both iOS and Android can tear down the VPN under memory pressure or battery optimisation. Combined
with `flutter_foreground_task` for the notification service, expect reconnect churn. Needs explicit
"Tailscale appears to be off" handling.

### 7.4 Serve config drift

`tailscale serve` state is global to `tailscaled` and survives helios entirely. If a user manually
configures serve on 443, helios must detect the conflict and refuse rather than clobber it. `Reset`
must remove only our own mapping.

### 7.5 Sudden loss of the "it's just HTTP" escape hatch

With `0.0.0.0` binding gone, debugging from a laptop on the same LAN stops working. Keep a documented
`--bind` override for development.

---

## 8. Testing

- Unit: `DNSName` JSON parsing incl. the multi-peer case the old regex got wrong; `Binary()`
  resolution incl. macOS bundle path; `Status` classification of each failure mode.
- Unit: `X-Forwarded-For` parsing — must **not** trust the header when `RemoteAddr` is non-loopback.
- Integration: config file containing `tunnel.provider: "zrok"` still loads.
- Manual: SSE narration, blocking approval end-to-end from the phone over the tailnet, daemon
  restart (URL must be identical), phone Tailscale toggled off mid-SSE.

---

## 9. Open Questions

1. **Keep `custom` provider?** ~15 lines, no process, only remaining path for a user-run reverse
   proxy. Recommend keep.
2. **`helios remote` rename** or keep `tunnel` terminology?
3. **Mobile: `updateHostUrl` vs force re-pair** (§4.6). Recommend `updateHostUrl`.
4. **Auto-enable HTTPS?** Helios can detect HTTPS-disabled and print the admin-console URL, but
   should not enable it — CT-log disclosure is the user's decision (§5.2).

---

## 10. Prerequisites (user action, before Phase 1)

1. Enable HTTPS certificates in the Tailscale admin console — understanding §5.2.
2. Grant permission for the Phase 0 `tailscale serve` validation test.
