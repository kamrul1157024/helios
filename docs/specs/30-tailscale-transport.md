# 30 — Tailscale as a First-Class, Recommended Tunnel Provider

**Status:** Draft — awaiting approval
**Revision:** 3. Revision 1 proposed replacing *all* tunnel providers with Tailscale; that is
withdrawn. Revision 2 made the change **purely additive** — nothing deleted, no existing user
migrated. Revision 3 splits the two modes by scheme: **Serve runs plain HTTP** inside the
WireGuard-encrypted tailnet and needs no certificate, while **Funnel keeps HTTPS** for public
clients. This removes the certificate prerequisite and the Certificate-Transparency disclosure from
the recommended path entirely (§3.1, §6.1).
**Supersedes:** nothing. `spec-zrok-tunnel-provider.md`, `spec-localtunnel-provider.md`,
`spec-localxpose-provider.md`, `spec-localhostrun-provider.md` and `spec-tunnel-decoupling.md` all
remain in force.
**Related:** `20-remote-access-and-auth.md`, `22-setup-and-security.md`,
`09-prerequisites-and-health-checks.md`, `multi-host-spec.md`

---

## 1. Goal

Make Tailscale a properly working, **recommended** tunnel provider alongside the existing eight,
and fix three latent defects that the work exposes. Users who are happy with Cloudflare keep
working with zero changes.

### 1.1 Why additive beats replacement

Revision 1 hinged on a single unverified assumption — that `tailscale serve` proxies SSE without
buffering. If that assumption failed, the entire refactor was dead, because every other transport
had been deleted. The additive framing removes that dependency:

| | Rev 1 (replace) | Rev 2 (add) |
|---|---|---|
| If Serve buffers SSE | Design invalid, work wasted | Don't recommend Serve; Cloudflare unaffected |
| Existing paired devices | All break; needs `updateHostUrl` to recover | Untouched |
| Enabling HTTPS (CT-log disclosure, §6.2) | Project-wide prerequisite | Per-user opt-in |
| Blast radius of a mistake | Whole remote-access path | One provider nobody defaults to yet |

The cost is that ~2,377 lines of provider code stay, and "which provider should I use?" becomes a
curation problem rather than one answered by deletion. §5 is that curation.

### 1.2 Non-goals

- Removing or deprecating any existing provider.
- Changing the Ed25519 device-pairing / JWT auth model. Tailscale is transport only (§6.3).
- Forcing existing users off their current provider.

---

## 2. Current State (verified)

### 2.1 The existing Tailscale provider is broken

`internal/tunnel/cloudflare.go:163-220` already contains a `TailscaleTunnel`. It must be rewritten,
not extended:

- `t.url = fmt.Sprintf("https://%s:%d", dnsName, localPort)` — **wrong.** Funnel terminates on 443,
  never on the local port. Any user who selected this provider got an unreachable URL.
- Uses `funnel` (public internet) with no way to choose `serve` (tailnet-only).
- Extracts the DNS name by regex-scraping the first `"DNSName"` match out of
  `tailscale status --json`. It hits `Self` only by accident of JSON field ordering, and will
  silently return a *peer's* hostname if that ordering changes.
- `exec.LookPath("tailscale")` misses the macOS bundle at
  `/Applications/Tailscale.app/Contents/MacOS/Tailscale`.
- Sleeps 2s and declares success without checking the serve config actually took effect.

### 2.2 Three latent defects this work exposes

**(a) PID-0 providers are never adopted across daemon restarts.**
`Manager.Start` unconditionally persists `PID: t.PID()` (`tunnel.go:189`). For `local`, `custom` —
and Tailscale, which owns no process — that PID is `0`. On restart, `Adopt()` calls
`IsProcessAlive(0)` (`tunnel.go:113` → `state.go:63`), which returns **false**: Go's
`os.FindProcess(0).Signal(0)` yields `os: process not initialized`. Adopt therefore logs
`stale state file (PID 0 dead), removing`, deletes the state, and reports no active tunnel.

Confirmed empirically on Go 1.26.5/darwin-arm64. It is not a crash — `killProcess(0)` also returns
early and harmlessly — but it is a real functional gap today for `local` and `custom`.

For Tailscale it is worse and inverted: the serve config lives in `tailscaled` and **is still
live** after a helios restart. Only helios has forgotten it. The TUI would show no tunnel and
offer no QR while the URL is actually serving fine.

**(b) `ServerConfig.Bind` is dead config.** It defaults to `"localhost"` and is assigned from a CLI
flag at `cmd/helios/main.go:99` — and is then **never read**. `server.go:215` hardcodes
`0.0.0.0:%d`. The public API is therefore reachable by anyone on the same LAN regardless of
configuration. This predates Tailscale and affects every provider.

**(c) The pairing rate limiter keys on the wrong address.** `middleware.go:72` uses
`ip := r.RemoteAddr`. Every tunnel provider — cloudflared, ngrok, zrok, localtunnel, localxpose,
localhost.run and Tailscale — connects to helios over loopback, so `RemoteAddr` is `127.0.0.1` for
effectively all remote traffic. The 5/min per-IP pairing limit is in practice a single global
bucket: one device pairing can lock out every other, and no two clients are distinguishable.

### 2.3 The TUI picker

`internal/tui/start.go:42-55` is a static list of nine providers. `(recommended)` is hardcoded text
in Cloudflare's label (`:46`); Tailscale's label (`:52`) is bare. Availability is **not** shown in
the list — it is checked only *after* selection (`:469-480`) via a single
`exec.LookPath(providerBinary(id))`, falling through to a `screenBinaryMissing` screen with an
install hint (`:839`, `:864`).

For Tailscale, "binary exists" is insufficient — the daemon must also be running, logged in, and
HTTPS-enabled — and `LookPath` misses the macOS bundle path.

### 2.4 Environment (dev machine, verified)

| Property | Value | Implication |
|---|---|---|
| Version | 1.102.2 | — |
| `BackendState` | `Running` | logged in |
| `Self.DNSName` | `mds-macbook-air.tail20015d.ts.net.` | note the trailing dot; must be trimmed |
| `tailscale serve status --json` | supported, returns `{}` | `Reconcile` parses JSON, not scraped text |
| serve config | none | teardown after testing restores the machine exactly |
| `MagicDNSSuffix` | `tail20015d.ts.net` | MagicDNS active — the hostname resolves inside the tailnet |
| `Self.TailscaleIPs` | `100.98.93.19`, `fd7a:115c:a1e0::5934:5d14` | reachable on the tailnet today |
| **`CertDomains`** | **`null`** | **HTTPS certificates are NOT enabled on this tailnet** |
| Node capabilities | includes `cap/is-admin`, `cap/is-owner` | the operator can enable HTTPS themselves |
| `tailscale serve --http <port>` | **supported** (verified in `serve --help`) | Serve does **not** require certificates |

**`CertDomains: null` gates Funnel only.** `tailscale serve` defaults to `--https`, which needs a
certificate — but it also accepts `--http <port>`, which does not. Because Serve is reachable only
from inside the tailnet, and every tailnet packet is already WireGuard-encrypted end-to-end,
terminating plain HTTP there loses nothing (§6.1). So:

- **Serve** works right now, on this machine, with **zero admin-console action** and **zero public
  disclosure**.
- **Funnel** genuinely requires certificates, because it terminates TLS for clients on the public
  internet that have no WireGuard tunnel.

This retires the previous revision's claim that HTTPS was a blocking precondition for the whole
feature. It is a precondition for the *unrecommended* mode only, which means the recommended path
has no prerequisites at all — see §12.

`Detect()` must still report certificate readiness as its own state (§3.4), but it now gates one
mode rather than the feature. It remains precisely the case a naive "is the binary installed?"
check (as the TUI does today at `start.go:470-480`) reports as healthy.

Funnel availability is **unverified**. It is gated by tailnet policy (`nodeAttrs`), and the
node-capabilities list is deprecated upstream, so its absence there is not conclusive either way.

---

## 3. Design

### 3.1 Two modes, not one

Because the other providers stay, Tailscale does not have to serve every audience by itself. It
should expose both of its exposure models:

```
Mode "serve"  (recommended)
  phone ──WireGuard (encrypted)──► tailscaled :7655 ──► 127.0.0.1:7655
  http://<machine>.<tailnet>.ts.net:7655
  Requires the Tailscale VPN *active* on the client. Never touches the public internet.
  No certificate, no admin-console action, no public disclosure.

Mode "funnel"
  anyone ──public TLS──► tailscaled :443|8443|10000 ──► 127.0.0.1:7655
  https://<machine>.<tailnet>.ts.net
  No client-side Tailscale. Public, but with a stable hostname and a real cert.
  Requires HTTPS certificates enabled on the tailnet (§6.2).
```

**The two modes deliberately use different schemes.** Serve runs `--http`, not `--https`:
confidentiality on the tailnet is provided by WireGuard at the transport layer, so TLS inside the
tunnel would be redundant encryption bought at the price of a Certificate-Transparency disclosure
(§6.2) and an admin-console prerequisite. Funnel must use `--https`, because its clients arrive
over the public internet with no tunnel to inherit encryption from.

This is the single most important consequence of the additive framing: **the recommended mode is
also the one with no setup and no disclosure.** Users are not asked to publish their machine name
to a public log in order to take the advice.

Funnel deserves more weight than Revision 1 gave it. For a user who cannot install Tailscale on
their phone, Funnel is a **strict upgrade over Cloudflare quick tunnels**: a permanent hostname
instead of a random `*.trycloudflare.com` that rots on every restart. That stability is what makes
`state.go` and `Adopt()` unnecessary for this provider, and it is available without asking the
client to install anything.

Both modes yield a URL computable before the daemon starts, from `Self.DNSName` plus the mode and
port — no output scraping, no waiting.

**Topology note.** Tailscale is a *mesh*, not a hub-and-spoke VPN: in Serve mode the phone and the
desktop connect **peer-to-peer** via NAT traversal, with no gateway in the path. When direct
traversal fails, traffic falls back to a Tailscale DERP relay, which forwards only
WireGuard-encrypted bytes and cannot read the session. There is consequently no gateway to
configure — subnet routers and exit nodes are unrelated features that helios does not need. The
practical consequences are no central bottleneck and no single point of failure.

Funnel is restricted to ports **443, 8443, 10000**. The public/private boundary is **per-port, not
per-path**, so a single port cannot be half-public.

### 3.2 Free plan is sufficient

Serve, Funnel, MagicDNS, HTTPS certificates and ACL editing are all available on the Free plan
(6 users, 100 devices). Plan tier is not a discriminator. The macOS App Store build is sandboxed
and cannot share *files/directories* but **can share ports**, which is all helios needs.

### 3.3 Shell out, don't embed `tsnet`

| | Shell out to `tailscale` | Embed `tsnet` |
|---|---|---|
| Node identity | Reuses the user's node | Helios becomes its own node |
| Setup | None — user already logged in | Needs an auth key |
| Device count | +0 | +1 per machine |
| Diagnosability | `tailscale serve status` | Opaque, in-process |
| Dependency | none | large Go module |

**Shell out.** `tsnet` front-loads auth-key management for no benefit today. Revisit only if the
shell-out proves brittle.

### 3.4 New package: `internal/tailscale`

```go
// Binary resolves the CLI: $PATH, then /Applications/Tailscale.app/Contents/MacOS/Tailscale.
func Binary() (string, error)

// State is the detection result driving both the recommendation and error messages.
type State struct {
    Installed  bool
    Running    bool   // tailscaled up
    LoggedIn   bool   // BackendState == "Running"
    MagicDNS   bool   // MagicDNSSuffix non-empty; required by BOTH modes
    CertsReady bool   // CertDomains non-empty; required by FUNNEL ONLY
    DNSName    string // Self.DNSName, trailing dot trimmed
    ServeInUse bool   // an existing serve config we did not create
}


func Detect(ctx context.Context) (State, error)

type Mode string // "serve" | "funnel"

type Tunnel struct{ mode Mode; port int }

func (t *Tunnel) Start(localPort int) error   // publishes, verifies, returns
func (t *Tunnel) Stop() error                 // removes only our mapping
func (t *Tunnel) URL() string
func (t *Tunnel) Provider() string            // "tailscale"
func (t *Tunnel) PID() int                    // 0 — owns no process
func (t *Tunnel) Reconcile(ctx context.Context) (string, bool, error)
```

`DNSName` is parsed with `encoding/json` from `tailscale status --json`, reading `Self.DNSName`
explicitly — never a regex over the whole document.

`Detect` must distinguish each failure mode, because each has a different remedy: not installed ·
tailscaled not running · not logged in · MagicDNS off · certificates disabled · port already
claimed by a foreign serve config. A single "tailscale unavailable" error is not acceptable here.

**`CertsReady` gates mode selection, not availability.** `CertsReady == false` — the dev machine's
current state (§2.4) — must still offer Serve as recommended, and must present Funnel as *needing
one admin-console step* rather than as unavailable or broken. The prior revision conflated these
and would have refused the feature outright on a perfectly usable machine.

Estimated ~250 lines, replacing the 58 broken ones at `cloudflare.go:163-220`.

### 3.5 Fixing adoption with an optional interface

Rather than special-casing Tailscale inside `Manager.Adopt()`:

```go
// Reconciler is implemented by providers whose liveness lives outside a helios-owned
// process. Adopt() type-asserts for it before falling back to PID liveness.
type Reconciler interface {
    Reconcile(ctx context.Context) (url string, active bool, err error)
}
```

`Adopt()` gains one branch: if the persisted provider's implementation satisfies `Reconciler`, ask
it; otherwise use the existing `IsProcessAlive` path. Tailscale implements it via
`tailscale serve status --json`.

Additive, no change to the `Tunnel` interface, no behaviour change for the seven process-backed
providers — and it fixes `custom` for free, which has defect (a) today.

---

## 4. Changes by Component

### 4.1 `internal/tunnel`

- Delete `TailscaleTunnel` from `cloudflare.go:163-220`; the provider now delegates to
  `internal/tailscale`.
- `tunnel.go`: add the `Reconciler` type assertion in `Adopt()`; wire `TailscaleProviderConfig`
  into the provider switch alongside the existing four.
- **No other provider is touched. No files are deleted.**

### 4.2 `internal/daemon/config.go`

```go
type TailscaleConfig struct {
    Mode string `yaml:"mode"` // serve | funnel        (default: serve)
    Port int    `yaml:"port"` // serve:  any free port (default: 7655)
                              // funnel: 443|8443|10000 (default: 443)
}
```

Added to `TunnelConfig` next to `Zrok`/`Localtunnel`/`LocalhostRun`/`Localxpose`. Existing config
files are unaffected — the zero value means `serve` on 7655.

The port rules differ by mode and must be validated as such: Funnel is restricted to **443, 8443,
10000**, while Serve accepts any port. Serve defaults to **7655** so the tailnet URL carries the
same port number as the local public server, which keeps the two mental models aligned. Rejecting a
Funnel port at config-parse time is far better than discovering it when `tailscale funnel` fails.

### 4.3 `internal/server` — the two cross-cutting fixes

These are provider-agnostic and fix bugs that exist today. They are in scope here because Tailscale
makes both materially worse if left alone.

- **Honour `cfg.Server.Bind`** (defect **b**). `server.go:215` reads the config instead of
  hardcoding. Default `127.0.0.1`. The `local` provider needs LAN reachability, so binding is
  derived: `local` ⇒ `0.0.0.0`, everything else ⇒ `127.0.0.1`, with the explicit CLI flag
  overriding. Also fix the stale comment at `server.go:35`.
- **Trust-scope `X-Forwarded-For`** (defect **c**). `middleware.go:72` parses the left-most
  `X-Forwarded-For` entry **only when `RemoteAddr` is loopback**, else keeps `RemoteAddr`. Correct
  for every proxying provider; automatically safe for `local`/`custom`, where `RemoteAddr` is a
  genuine remote address and the header is therefore ignored.

**Why the `Bind` fix is a hard dependency of Serve mode, not a nicety.** `server.go:215` hardcodes
`0.0.0.0`, and the tailnet interface is one of the interfaces that covers. So today, on this
machine, helios is *already* reachable at `http://mds-macbook-air.tail20015d.ts.net:7655` with no
serve config at all — but it is equally reachable on every LAN the laptop joins, which is exactly
what the user did not ask for. Serve is the better mechanism precisely because tailscaled listens
on the tailnet interface *only*; that advantage evaporates unless helios itself retreats to
loopback. Serve mode therefore requires `Bind = 127.0.0.1`, and the derivation in the bullet above
already yields that.

Whether tailscaled's Serve listener and a stray `0.0.0.0:7655` bind actually collide is
**unverified** — on macOS the GUI client handles tailnet traffic in a network extension rather than
an ordinary kernel socket, so they may silently coexist with confusing precedence. This is a §8
validation item, and a reason to land the `Bind` fix first regardless.

### 4.4 TUI — the recommendation surface

Three changes to `internal/tui/start.go`:

1. **Reorder and relabel** `tunnelProviders` (`:42-55`): Tailscale first and carrying
   `(recommended)`; remove that suffix from Cloudflare's label (`:46`).
2. **Show availability inline** rather than only after selection. The list gains a status column
   computed once on entry to the screen:
   ```
   ▸ Tailscale            (recommended)   ● ready · mds-macbook-air.tail20015d.ts.net
     Cloudflare Tunnel                    ● cloudflared installed
     zrok (open-source, stable URLs)      ○ not installed
     Tailscale                            ○ logged out → tailscale up
   ```
3. **Provider-specific readiness** for Tailscale, replacing the bare `exec.LookPath` at `:470-480`
   with `tailscale.Detect`, so the `screenBinaryMissing` screen can say *"Tailscale is installed
   but not logged in — run `tailscale up`"* instead of claiming the binary is missing.

After a successful start, the screen shows the mode and its exposure in plain words — "reachable
only from your tailnet" vs "reachable from the public internet" — because that distinction is the
whole security argument and must not be buried in config.

**The QR and pairing flow are unchanged.** Both modes resolve to a URL derived from `Self.DNSName`,
so the existing payload `helios://pair?url=%s&token=%s` simply carries a different URL string —
`http://…:7655` for Serve, `https://…` for Funnel. The mobile side is agnostic to both scheme and
port: `Uri.parse('$serverUrl/api/auth/pair')` (`host_manager.dart:209`) interpolates whatever it is
given, and there is no scheme validation anywhere in `host_manager.dart` (verified). No QR
generation, payload, or pairing-endpoint change is in scope for either mode.

The one platform caveat is client-side TLS policy, not payload shape — see §4.7.

The Serve prerequisite — the phone's VPN being switched on — is surfaced once, at the moment of
choice in §4.5, which is where it is actionable. It is deliberately not repeated on the QR screen.

### 4.5 First-run prompt (not a silent default)

`DefaultConfig()` leaves `Tunnel.Provider` as `""`. The tempting move is to silently default to
Tailscale Serve whenever `Detect()` reports ready. **That is wrong**, and the reason is worth
stating plainly:

> `Detect()` runs on the desktop and can only ever see the *desktop's* Tailscale.
> Whether Serve is usable depends on a **client-side** fact — is the phone's VPN on? — that the
> daemon has no way to observe.

Defaulting on a desktop-side signal to answer a phone-side question strands the user: helios picks
Serve, the QR encodes a `ts.net` URL, the phone cannot resolve it, and pairing fails with the
generic "Could not reach server" (`host_manager.dart:219`) with no hint that the remedy is on the
phone.

So it is not a default at all — it is a **one-time question**, asked of the only party who knows:

```
  Tailscale detected on this machine.

▸ Use Tailscale Serve   (recommended)
    Private to your tailnet. Requires the Tailscale VPN to be
    switched ON on your phone — not just installed.

  Use Tailscale Funnel
    Works on any phone, no Tailscale needed. Publicly reachable
    on the internet, like Cloudflare, but with a stable hostname.
    ⚠ Needs HTTPS certificates enabled once in the admin console,
      which publishes this machine's name to a public log.

  Choose another provider…
```

Serve is pre-selected and labelled recommended. The wording asks about the **VPN being switched
on**, not about the app being installed — an installed-but-inactive profile is the exact state that
produces the confusing failure above.

The Funnel warning line is shown only when `Detect().CertsReady` is false, and choosing Funnel in
that state leads to a screen with the admin-console link and the §6.2 explanation rather than a
failed start. Serve, by contrast, has no such branch — there is nothing to warn about.

Asked only when `Tunnel.Provider` is empty and `Detect()` reports ready. An existing `provider:`
value is always honoured, and the answer is persisted so the question is asked once.

### 4.6 CLI

- `helios tunnel status` reports mode and exposure, not just the URL.
- `helios setup` gains a Tailscale readiness check feeding
  `09-prerequisites-and-health-checks.md`.
- No flags removed; `handleTunnel` keeps its current shape.

### 4.7 Mobile — optional, recommended

`HostConnection` exposes only `updateHostLabel` / `updateHostColor`
(`host_manager.dart:364,373`); **`serverUrl` is immutable after pairing**. Under Rev 1 this was a
blocking migration problem. Here it is not — nobody is forced to move — but it is why switching
providers today requires deleting and re-pairing a host, losing the Ed25519 keypair and orphaning
the device row on the daemon.

Adding `updateHostUrl(hostId, newUrl)` (~30 lines) makes provider switching a first-class action
for *all* providers. Recommended, not required, and independently useful.

Also worth a targeted error: when the phone's Tailscale is off, a dead `serverUrl` currently
surfaces as the generic "Could not reach server" (`host_manager.dart:219`).

**Cleartext policy — the one required mobile change, and only off-Android.** Serve mode hands the
client an `http://` URL, so each platform's TLS policy applies. Verified state:

| Platform | Status | Action |
|---|---|---|
| Android | `android:usesCleartextTraffic="true"` already set (`AndroidManifest.xml:17`) | **none** |
| iOS | `ios/Runner/Info.plist` has **no** `NSAppTransportSecurity` key | needs an ATS exception |
| macOS | `macos/Runner/Info.plist` likewise | needs an ATS exception |

Android is already unblocked — the `local` provider ("Local Network, no HTTPS") required the same
thing, so Serve inherits a permission the app has carried all along. For iOS/macOS, add an
`NSExceptionDomains` entry for `ts.net` with `NSIncludesSubdomains` and
`NSExceptionAllowsInsecureHTTPLoads`, scoped to that domain rather than a blanket
`NSAllowsArbitraryLoads`. Note that `NSAllowsLocalNetworking` does **not** help: `ts.net` is a real
public domain, not a `.local` or link-local name.

This is a pre-existing gap that Serve merely makes visible — `local` is equally unusable on
iOS/macOS today — so it should be scoped as its own small fix rather than allowed to gate this
work. Android is the only platform with a shipping build today (`make apk`).

---

## 5. What "Recommended" Means

Recommendation must be **conditional on detection**. Telling a user with no Tailscale installed
that it is recommended is noise.

| Mechanism | Adopt? |
|---|---|
| Docs — README + this spec | Yes |
| Picker: first position + `(recommended)` badge + inline live status | Yes |
| First-run prompt when unset and detected ready, Serve pre-selected (§4.5) | Yes |
| Silently defaulting to Serve on desktop-side detection | **No** — the deciding fact is on the phone (§4.5) |
| `helios setup` readiness check and guidance | Yes |
| Nagging existing Cloudflare users to switch | **No** — they made a choice |
| Deprecating any provider | **No** — explicitly out of scope |

The honest recommendation is two-tiered, and the TUI should say so:

- **Tailscale Serve** — best option if you can install Tailscale on your phone. Private to your
  tailnet, stable URL, encrypted by WireGuard, and nothing to set up.
- **Tailscale Funnel** — if you cannot. Public like Cloudflare, but with a stable hostname. Costs
  one admin-console step and a CT-log disclosure (§6.2).
- **Cloudflare and the rest** — unchanged, fully supported.

---

## 6. Security

### 6.1 Net improvement, opt-in

Serve mode moves remote access from "guessable public hostname protected only by app-layer JWT" to
"reachable only from devices already authenticated to the tailnet," with ACLs as a second,
network-level gate. Users who stay on Cloudflare are no worse off than today — and both fixes in
§4.3 improve their posture regardless of provider.

**On Serve carrying `http://`.** This is not a downgrade, and the reasoning should survive review:

- Every packet between phone and desktop is **WireGuard-encrypted end-to-end**, with per-peer keys
  and forward secrecy. TLS inside that tunnel would encrypt already-encrypted bytes.
- The DERP relay fallback (§3.1) forwards only WireGuard ciphertext and cannot read the session, so
  the relay path does not weaken the argument.
- The plaintext segment is `tailscaled → 127.0.0.1:7655` on the same host — the identical loopback
  hop every other provider already terminates into.
- Bearer JWT auth (§6.3) is unchanged and still applies to every request.

The genuine trade-off is *ecosystem*, not cryptography: `http://` URLs lose browser secure-context
features and trip platform TLS policy (§4.7). Neither applies to a native Flutter client on
Android. Users who want TLS on the tailnet anyway can set `mode: serve` with certificates enabled
and a `--https` port; that should remain configurable, just not the default.

### 6.2 Certificate Transparency disclosure

Enabling HTTPS publishes `<machine>.<tailnet>.ts.net` to public CT logs; machine and tailnet names
become publicly enumerable. No service is exposed by this. Under Rev 1 this was a project
prerequisite; here it is **a Funnel-only cost** — the recommended Serve path never triggers it,
because it never requests a certificate. Helios should **detect and explain** the disclosure at the
point a user chooses Funnel, and never enable certificates silently.

That the recommended mode and the zero-disclosure mode are the same mode is the strongest argument
for this design.

### 6.3 Keep bearer auth unchanged

Tailnet identity (`Tailscale-User-Login` / `WhoIs()`) is available but is per-*user*, while the
approval model is per-*device*. Dropping JWT would also make the daemon trust any local process
that reaches loopback. Keep it. Tailnet identity may later *auto-approve* pairing from the owner's
own account — a follow-up, not this spec.

---

## 7. Phasing

Each phase is independently shippable and independently revertable.

**Implementation status: all five phases complete.** Phase 3's validation ran on 2026-08-11 and
passed both properties (§8), so the recommendation Phase 4 assumed is now evidenced rather than
asserted.

**Phase 1 — Cross-cutting fixes.** `Bind` honoured, `X-Forwarded-For` trust-scoped, `Reconciler`
added and `Adopt()` taught to use it. No Tailscale code. Fixes real bugs for existing users and is
worth merging even if the rest is abandoned.

**Phase 2 — The provider.** `internal/tailscale`, both modes, `TailscaleConfig`, provider switch
wiring. Selectable but not yet recommended. Serve is fully testable in this phase; Funnel's
end-to-end test waits on certificates being enabled (§12.2), so Funnel ships behind its own
readiness check rather than blocking the phase.

**Phase 3 — Validation.** Run §8. Decide, on evidence, whether Serve earns the "recommended" badge.

**Phase 4 — Recommendation UX.** Reorder/relabel the picker, inline status, the first-run prompt
(§4.5), `helios setup` check, README. Gated on Phase 3 passing. The QR and pairing flow are
explicitly untouched (§4.4).

**Phase 5 — Optional.** Mobile `updateHostUrl` + Tailscale-off error handling.

---

## 8. Validation (Phase 3)

Two properties decide whether Serve is recommendable. Both are core to helios and neither is
currently known:

- **SSE is not buffered.** `sse.go:79-91` holds streams open indefinitely with a 30s heartbeat.
  This carries narration, session events **and — since spec 29 — remote terminal output from the
  new PTY host**, which reaches the phone over this same endpoint (`api.go:1322`).
- **Long-lived requests are not cut off.** `waitForDecision` (`hooks.go:587`, 5-minute timer at
  `:588`) holds a Claude hook request open for up to **5 minutes** awaiting a human approval.

Procedure, in isolation, touching no helios code:

1. Toy server on `127.0.0.1:9999`: `/events` emits an SSE tick every 2s with explicit `Flush`;
   `/slow` holds for 330s.
2. Baseline over loopback — confirms the harness itself streams. **Already run: ticks arrive at
   2s intervals, incrementally.**
3. `tailscale serve --bg --http=8080 http://127.0.0.1:9999` — an unused port, leaving 443 and the
   helios port untouched.
4. `curl -N http://<node>.ts.net:8080/events` — ticks must arrive incrementally, not in one flush.
5. `curl -m 400 http://<node>.ts.net:8080/slow` — must survive past 5 minutes.
6. `tailscale serve --http=8080 off`.
7. Confirm `tailscale serve status` reports no config, restoring the machine's original state.

Additionally, once the daemon is involved: verify whether a Serve listener on 7655 collides with
helios's current `0.0.0.0:7655` bind (§4.3) or silently shadows it.

**Status: run and passed on 2026-08-11.** Both properties hold. Serve earns the recommendation.

| # | Step | Result |
|---|---|---|
| 1 | Toy server on `127.0.0.1:9999` | done |
| 2 | Loopback baseline | **passed** — ticks at 2s intervals, incrementally |
| 3 | `tailscale serve --bg --http=8080 http://127.0.0.1:9999` | published; `443` and `7655` untouched |
| 4 | SSE over `ts.net:8080` | **passed** — 6 ticks at 2s intervals, indistinguishable from loopback |
| 5 | `curl -m 400 …/slow` | **passed** — `survived 330s`, `HTTP=200`, elapsed 330s |
| 6 | `tailscale serve --http=8080 off` | mapping removed |
| 7 | Verify restoration | `:8080` gone |

**Neither failure mode materialised.** Step 4 rules out buffering: the same handler that streams
incrementally over loopback streams identically through the Serve proxy. Step 5 rules out an
intermediary timeout at 5½ minutes, comfortably past `waitForDecision`'s 5-minute ceiling.

The baseline in step 2 is what makes step 4 diagnostic — any batching would have been attributable
to Serve, since the handler flushes per tick.

**Certificates were never needed.** A prior revision listed "HTTPS certificates are not enabled" as
a blocker; switching Serve to `--http` (§3.1) removed it. The whole run completed with certificates
still disabled on this tailnet.

**Side-benefit: the JSON shape is now evidence, not assumption.** `tailscale serve status --json`
was captured verbatim mid-run and is pinned as a test fixture, confirming the `TCP` / `Web` /
`AllowFunnel` layout that `waitPublished` and `Reconcile` depend on, and that a Serve mapping never
sets `AllowFunnel`.

**End-to-end, beyond the harness.** A real Serve tunnel was subsequently started through the new
picker: config persisted `provider: tailscale` with `tailscale.mode: serve`, tailscaled published
`http://<node>.ts.net:7655 → 127.0.0.1:7655`, and `helios tunnel status` recovered it through
`Reconcile` — the PID-0 path that would previously have deleted the state file as stale.

Caveat, unchanged by the run: curling one's own `ts.net` name from the same machine exercises the
Serve proxy — which is the buffering question — but not the cross-device WireGuard path. A paired
phone is still what would give full confidence.

~~**If SSE is buffered:** Tailscale ships as a supported but *not* recommended provider, Phase 4 is
dropped, and §5 keeps Cloudflare first.~~ Not triggered.

---

## 9. Risks

| Risk | Severity | Mitigation |
|---|---|---|
| Serve buffers SSE | Medium | Phase 3 gates only the badge, not the feature |
| Funnel needs HTTPS enabled, a CT-log disclosure some users will refuse | Low | Downgraded from Medium: Serve uses `--http` and needs no certificate (§3.1), so this constrains the *unrecommended* mode only. `Detect().CertsReady` surfaces it as "Funnel needs one setup step", never as "Tailscale unavailable" |
| `http://` Serve URL blocked by platform TLS policy | Low | Android already permits cleartext (verified, §4.7); iOS/macOS need a scoped ATS exception, a pre-existing gap that also affects `local` |
| Reviewer reads `http://` as insecure and rejects the design | Medium | Argued explicitly in §6.1: WireGuard provides transport encryption, the plaintext hop is loopback-only, JWT is unchanged. Worth stating in the PR description, not just the spec |
| Serve config drift — user's own mapping on the same port | Medium | `Detect.ServeInUse`; refuse rather than clobber; `Stop` removes only our mapping |
| Mobile VPN torn down by OS battery/memory policy | Medium | Explicit "Tailscale appears to be off" error (§4.7) |
| Binding default flips to `127.0.0.1` and breaks a LAN debugging workflow | Low | `local` still binds `0.0.0.0`; CLI flag overrides; call out in release notes |
| Users with `provider: tailscale` today see behaviour change | Low | It is currently broken (§2.1); this is a fix. Note in release notes |
| Provider count grows, picker gets noisier | Low | Inline availability (§4.4) makes the list self-pruning in practice |

---

## 10. Testing

- Unit: `Self.DNSName` extraction incl. the multi-peer document the old regex got wrong.
- Unit: `Binary()` resolution incl. the macOS bundle path.
- Unit: `Detect` classification of each failure mode independently.
- Unit: `X-Forwarded-For` — trusted from loopback, **ignored** from a non-loopback `RemoteAddr`.
- Unit: `Adopt()` picks `Reconcile` for a `Reconciler` and `IsProcessAlive` otherwise; regression
  test that a PID-0 `custom` entry is no longer discarded.
- Integration: bind derivation per provider; existing config files load unchanged.
- Unit: `StateLiveness`/`KillTunnel` — the CLI-side counterpart of the adoption regression, so
  `helios tunnel status` cannot delete the state file of a working PID-0 tunnel.
- Unit: exposure wording per mode, derived from the URL scheme for a tunnel the CLI did not start.
- Unit (Dart): `isTailnetUrl` matches on the host component only, so a `ts.net` in a path or query
  cannot trigger the "switch Tailscale on" advice.
- Manual: SSE narration, blocking approval, and PTY-host terminal streaming from the phone over
  the tailnet; daemon restart with serve active (URL must be recovered, not dropped); Tailscale
  toggled off mid-stream.

---

## 11. Open Questions

1. **Both modes at once, or Serve first?** Recommend both — Funnel is where the easy adoption is
   and it reuses the same detection and teardown code.
2. ~~Smart default?~~ **Resolved:** no silent default. A one-time prompt with Serve pre-selected
   (§4.5), because the deciding fact — whether the phone's VPN is on — is not observable from the
   desktop. Asked only when no provider is configured.
3. **Version the `provider: tailscale` behaviour change (§2.1)?** Recommend no — release note is
   enough, since the current behaviour is an unreachable URL.
4. **Ship the mobile `updateHostUrl` (§4.7)?** Recommend yes, as its own change — it benefits all
   providers, not just this one.
5. **Are the two §4.3 fixes in scope here, or a separate PR?** Recommend a separate PR landing
   first: they are independent bug fixes and should not be gated on Tailscale review.

---

## 12. Prerequisites (user action)

**No blocking items remain.**

1. ~~**Permission to run the §8 validation.**~~ Granted and run on 2026-08-11; both properties
   passed and the test mapping was torn down (§8).
2. *(Deferred, Funnel only)* **Enable HTTPS certificates** in the admin console, accepting the CT-log
   disclosure in §6.2. Verified **not currently enabled** (§2.4). Needed to test and ship Funnel;
   **not** needed for Serve, for the §8 validation, or for the recommended path. The operator's node
   holds `cap/is-admin` and `cap/is-owner`, so no separate approval is required when the time comes.
3. *(Optional, for full confidence)* A paired phone, to exercise the cross-device WireGuard path
   rather than only same-host proxying.

**For end users, the recommended path now has no prerequisites at all** beyond having Tailscale
installed and logged in. Only users who deliberately choose Funnel pay the setup-and-disclosure
cost.
