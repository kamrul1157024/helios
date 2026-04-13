# Spec: zrok Tunnel Provider

## Summary

Add zrok as a tunnel provider for Helios, alongside cloudflare, ngrok, tailscale, local, and custom. zrok is an open-source, self-hostable sharing platform built on OpenZiti that provides zero-trust tunneling with end-to-end encryption, no open ports, and no NAT traversal tricks.

## Why zrok?

| Feature | Cloudflare | ngrok | Tailscale | zrok |
|---------|-----------|-------|-----------|------|
| Free tier | Yes (random URL) | Yes (limited) | Yes (100 devices) | Yes (zrok.io hosted) |
| Self-hostable | No | No | Partial (Headscale) | Yes (full) |
| Open source | No | No | Partial | Yes (Apache 2.0) |
| E2E encryption | TLS termination | TLS termination | WireGuard | OpenZiti (true E2E) |
| No inbound ports | Yes | Yes | No (funnel needs 443) | Yes |
| Reserved URLs | No (quick tunnels) | Yes (paid) | Via DNS | Yes (free) |
| Private sharing | No | No (IP policy, paid) | Yes (tailnet) | Yes (native) |

Key advantages:
- **Self-hostable**: Users can run their own zrok instance for full control
- **Reserved shares**: Persistent URLs that survive restarts (free, unlike ngrok)
- **Private shares**: Share to specific zrok users without public exposure
- **True E2E encryption**: Traffic is encrypted even from the zrok server itself
- **Peer-to-peer**: Direct connections when possible, reducing latency

---

## zrok Concepts (Relevant to Helios)

### Account & Environment
- Users create an account on a zrok instance (zrok.io or self-hosted)
- `zrok enable <token>` enrolls the local machine as an **environment**
- Environment state stored in `~/.zrok/` (managed by zrok, not Helios)

### Shares
- **Public share**: `zrok share public <port>` — exposes HTTP on a random public URL
- **Private share**: `zrok share private <port>` — only accessible to other zrok users via `zrok access private <shareToken>`
- Shares are **ephemeral** by default — they stop when the command exits

### Reserved Shares
- `zrok reserve public --backend-mode proxy localhost:<port>` — creates a persistent share token
- `zrok share reserved <token>` — activates a reserved share (URL stays the same across restarts)
- Reserved shares persist across machine reboots, daemon restarts, etc.
- This is the key feature for Helios: **stable URLs that survive tunnel restarts**

### The zrok Agent
- `zrok agent start` — runs a background agent that can manage multiple shares
- `zrok agent share public <port>` — creates a share through the agent
- The agent is optional; direct `zrok share` works fine for single-share use

---

## Design

### Provider Configuration

Extend `TunnelConfig` to support zrok-specific options:

```yaml
# ~/.helios/config.yaml
tunnel:
  provider: zrok
  zrok:
    share_mode: public          # public | private | reserved
    share_token: ""             # only for reserved shares (populated after first reserve)
    backend_mode: proxy         # proxy (default) | tcpTunnel | caddy
    api_endpoint: ""            # custom zrok instance URL (empty = zrok.io default)
```

### Share Mode Strategy

Helios will use **reserved shares** by default when possible, falling back to ephemeral public shares:

```
First start with provider=zrok:
  1. Run `zrok reserve public --backend-mode proxy localhost:<port>`
  2. Get back a share token (e.g., "abc123xyz")
  3. Save share token in config.yaml → tunnel.zrok.share_token
  4. Run `zrok share reserved <token>` to activate
  5. Parse URL from stdout/stderr

Subsequent starts (share_token exists):
  1. Run `zrok share reserved <token>` directly
  2. Same URL as before — mobile devices stay connected

If reserved share fails (token expired/revoked):
  1. Log warning, clear stale token from config
  2. Create new reservation
  3. New URL — mobile devices need to reconnect
```

This gives Helios the **best possible URL stability** for free.

### Share Mode: `public` (Ephemeral)

For users who don't want persistent reservations:

```
zrok share public <port>
```

- Random URL each time (like Cloudflare quick tunnels)
- No setup/reservation needed
- Good for testing or single-session use

### Share Mode: `private`

For users on the same zrok network:

```
zrok share private <port>
```

- No public URL — share token must be accessed via `zrok access private <token>`
- Not suitable for general mobile access (mobile app would need zrok SDK)
- Listed for completeness; not the default path

### Share Mode: `reserved` (Default)

```
zrok reserve public --backend-mode proxy localhost:<port>
# returns share token

zrok share reserved <shareToken>
```

- Stable URL across restarts
- Share token persisted in config
- Best for Helios's mobile-pairing use case

---

## Implementation

### 1. Config Changes

**`internal/daemon/config.go`**

```go
type ZrokConfig struct {
    ShareMode   string `yaml:"share_mode"`    // public | reserved (default: reserved)
    ShareToken  string `yaml:"share_token"`   // reserved share token (auto-populated)
    BackendMode string `yaml:"backend_mode"`  // proxy (default)
    APIEndpoint string `yaml:"api_endpoint"`  // custom zrok instance URL (empty = default)
}

type TunnelConfig struct {
    Provider  string     `yaml:"provider"`
    CustomURL string     `yaml:"custom_url"`
    Zrok      ZrokConfig `yaml:"zrok"`
}
```

### 2. ZrokTunnel Provider

**`internal/tunnel/cloudflare.go`** (add to existing file, or extract to `internal/tunnel/zrok.go`)

```go
// ZrokTunnel uses `zrok share` for zero-trust tunneling.
type ZrokTunnel struct {
    cmd        *exec.Cmd
    url        string
    shareToken string
    shareMode  string
    onTokenCreated func(token string) // callback to persist reserved token
}

func (t *ZrokTunnel) Provider() string { return "zrok" }
func (t *ZrokTunnel) URL() string      { return t.url }

func (t *ZrokTunnel) PID() int {
    if t.cmd != nil && t.cmd.Process != nil {
        return t.cmd.Process.Pid
    }
    return 0
}
```

#### Start Flow

```go
func (t *ZrokTunnel) Start(localPort int) error {
    binary, err := exec.LookPath("zrok")
    if err != nil {
        return fmt.Errorf("zrok not found: install from https://zrok.io")
    }

    // Check that zrok environment is enabled
    if err := checkZrokEnabled(binary); err != nil {
        return err
    }

    switch t.shareMode {
    case "reserved":
        return t.startReserved(binary, localPort)
    case "public":
        return t.startPublic(binary, localPort)
    default:
        return t.startReserved(binary, localPort) // default to reserved
    }
}

func (t *ZrokTunnel) startReserved(binary string, localPort int) error {
    // If no share token, create a reservation first
    if t.shareToken == "" {
        token, err := t.createReservation(binary, localPort)
        if err != nil {
            // Fall back to ephemeral public share
            log.Printf("zrok: reservation failed, falling back to public share: %v", err)
            return t.startPublic(binary, localPort)
        }
        t.shareToken = token
        if t.onTokenCreated != nil {
            t.onTokenCreated(token)
        }
    }

    // Activate the reserved share
    t.cmd = exec.Command(binary, "share", "reserved", t.shareToken, "--headless")
    t.cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

    return t.startAndParseURL()
}

func (t *ZrokTunnel) createReservation(binary string, localPort int) (string, error) {
    target := fmt.Sprintf("localhost:%d", localPort)
    out, err := exec.Command(binary, "reserve", "public",
        "--backend-mode", "proxy",
        target,
    ).CombinedOutput()
    if err != nil {
        return "", fmt.Errorf("zrok reserve: %s: %w", string(out), err)
    }

    // Parse the share token from output
    // Expected output contains the token, e.g., "reserved share token is abc123xyz"
    token := parseShareToken(string(out))
    if token == "" {
        return "", fmt.Errorf("could not parse share token from: %s", string(out))
    }
    return token, nil
}

func (t *ZrokTunnel) startPublic(binary string, localPort int) error {
    t.cmd = exec.Command(binary, "share", "public",
        "--headless",
        fmt.Sprintf("localhost:%d", localPort),
    )
    t.cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

    return t.startAndParseURL()
}
```

#### URL Parsing

zrok prints the share URL to stdout/stderr when the share becomes active. We parse it similarly to cloudflared:

```go
func (t *ZrokTunnel) startAndParseURL() error {
    // Capture both stdout and stderr
    stdout, err := t.cmd.StdoutPipe()
    if err != nil {
        return fmt.Errorf("create stdout pipe: %w", err)
    }
    stderr, err := t.cmd.StderrPipe()
    if err != nil {
        return fmt.Errorf("create stderr pipe: %w", err)
    }

    if err := t.cmd.Start(); err != nil {
        return fmt.Errorf("start zrok: %w", err)
    }

    go t.cmd.Wait()

    // Parse output for the share URL
    // zrok outputs something like: https://<token>.share.zrok.io
    urlCh := make(chan string, 1)
    re := regexp.MustCompile(`https://[a-zA-Z0-9-]+\.[a-zA-Z0-9.-]+\.[a-z]{2,}`)

    scanForURL := func(r io.Reader) {
        scanner := bufio.NewScanner(r)
        for scanner.Scan() {
            line := scanner.Text()
            if match := re.FindString(line); match != "" {
                select {
                case urlCh <- match:
                default:
                }
                return
            }
        }
    }

    go scanForURL(stdout)
    go scanForURL(stderr)

    select {
    case url := <-urlCh:
        t.url = url
        return nil
    case <-time.After(30 * time.Second):
        killProcess(t.cmd.Process.Pid)
        return fmt.Errorf("timeout waiting for zrok share URL")
    }
}
```

#### Environment Check

```go
func checkZrokEnabled(binary string) error {
    out, err := exec.Command(binary, "status").CombinedOutput()
    if err != nil {
        return fmt.Errorf("zrok not enabled: run 'zrok enable <token>' first: %w", err)
    }
    if strings.Contains(string(out), "not enabled") {
        return fmt.Errorf("zrok environment not enabled: run 'zrok enable <token>' to set up")
    }
    return nil
}
```

### 3. Manager Integration

**`internal/tunnel/tunnel.go`** — add zrok to the provider switch:

```go
case "zrok":
    t = &ZrokTunnel{
        shareMode:  zrokConfig.ShareMode,
        shareToken: zrokConfig.ShareToken,
        onTokenCreated: func(token string) {
            // Callback to persist the reserved share token
            if onZrokTokenCreated != nil {
                onZrokTokenCreated(token)
            }
        },
    }
```

The `Manager.Start()` signature needs a small extension to accept zrok config:

```go
func (m *Manager) Start(provider string, customURL string, localPort int, zrokCfg ZrokConfig) (string, error)
```

Or, use an options struct:

```go
type StartOptions struct {
    Provider  string
    CustomURL string
    LocalPort int
    Zrok      ZrokConfig
}

func (m *Manager) Start(opts StartOptions) (string, error)
```

### 4. API Changes

**`POST /internal/tunnel/start`** — extend the request body:

```json
{
    "provider": "zrok",
    "zrok": {
        "share_mode": "reserved",
        "share_token": "",
        "backend_mode": "proxy",
        "api_endpoint": ""
    }
}
```

Response unchanged:

```json
{
    "public_url": "https://abc123.share.zrok.io"
}
```

**`GET /internal/tunnel/status`** — add zrok-specific fields when provider is zrok:

```json
{
    "active": true,
    "provider": "zrok",
    "public_url": "https://abc123.share.zrok.io",
    "zrok": {
        "share_mode": "reserved",
        "share_token": "abc123"
    }
}
```

### 5. State Persistence

The existing `tunnel.state` file works as-is. The zrok share token is **additionally** persisted in `config.yaml` so it survives tunnel state cleanup:

```
tunnel.state    → PID, URL, provider (ephemeral, removed on tunnel stop)
config.yaml     → zrok.share_token (persistent, survives restarts)
```

This means:
- `helios tunnel stop` kills the process and removes tunnel.state
- Next `helios daemon start` reads config.yaml, finds share_token, reuses it
- The URL stays the same

### 6. Reserved Share Cleanup

Add a cleanup mechanism for reserved shares:

**`helios tunnel release`** (new CLI command):

```
$ helios tunnel release

Reserved zrok share: abc123xyz
URL: https://abc123.share.zrok.io

Release this reservation? This will:
  - Stop the tunnel if running
  - Release the reserved share token
  - Mobile devices will need to reconnect with a new URL

Release? [y/N]: y
Share released.
```

Implementation:
```bash
zrok release <shareToken>
```

This cleans up the reservation on the zrok server and removes the token from config.yaml.

### 7. Mobile App Changes

No mobile app changes needed. The mobile app connects via the tunnel URL, which is provider-agnostic. The QR code pairing flow works identically:

```
QR code: helios://pair?url=https://abc123.share.zrok.io&token=...
```

### 8. TUI Changes

The TUI tunnel provider selector should include "zrok" as an option. If zrok is selected and not enabled, show a setup hint:

```
zrok requires initial setup:
  1. Install: brew install openziti/tap/zrok
  2. Create account: zrok invite (or sign up at zrok.io)
  3. Enable: zrok enable <token>
```

---

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `internal/tunnel/zrok.go` | **New** | ZrokTunnel struct, Start/Stop/URL/PID, reservation logic |
| `internal/tunnel/tunnel.go` | Modify | Add "zrok" case to provider switch, extend Start signature |
| `internal/daemon/config.go` | Modify | Add ZrokConfig struct, extend TunnelConfig |
| `internal/server/api.go` | Modify | Pass zrok config to tunnel start, include in status response |
| `internal/daemon/daemon.go` | Modify | Wire zrok config + token persistence callback |
| `cmd/helios/main.go` | Modify | Add "zrok" to help text, add `tunnel release` command |

---

## Edge Cases

### zrok Not Enabled
If `zrok status` indicates the environment is not enabled:
- Return clear error: `"zrok environment not enabled: run 'zrok enable <token>' to set up"`
- Do not attempt to share

### Reserved Share Token Expired/Invalid
If `zrok share reserved <token>` fails:
1. Log warning with error details
2. Clear stale token from config
3. Attempt to create a new reservation
4. If new reservation succeeds, persist new token
5. Mobile devices will need to reconnect (new URL)

### zrok Binary Not Found
Return: `"zrok not found: install with 'brew install openziti/tap/zrok' or from https://zrok.io"`

### Self-Hosted zrok Instance
Users with a self-hosted instance set `api_endpoint` in config:
```yaml
tunnel:
  provider: zrok
  zrok:
    api_endpoint: "https://zrok.mycompany.com"
```

The provider passes this via `ZROK_API_ENDPOINT` env var to the zrok process:
```go
t.cmd.Env = append(os.Environ(), "ZROK_API_ENDPOINT="+apiEndpoint)
```

### Concurrent Access
The existing mutex in `Manager` handles concurrent access. No additional synchronization needed for zrok.

---

## Testing Plan

1. **Unit tests** (`internal/tunnel/zrok_test.go`):
   - Parse share token from various zrok output formats
   - Parse URL from share output
   - Config serialization/deserialization with ZrokConfig

2. **Integration tests** (manual, requires zrok account):
   - `provider=zrok, share_mode=public` → ephemeral share starts, URL accessible
   - `provider=zrok, share_mode=reserved` → reservation created, token persisted, URL stable across restart
   - Daemon restart with existing reservation → same URL reused
   - `helios tunnel release` → share released, token cleared
   - Invalid token → fallback to new reservation

3. **Adoption test**:
   - Start zrok tunnel, kill daemon, restart daemon → tunnel adopted via PID, same URL

---

## Open Questions

1. **`--headless` flag**: zrok may use `--headless` for non-interactive output. Need to verify exact flag name and output format with the installed version. If `--headless` is not available, parse the TUI-style output instead.

2. **zrok agent vs direct share**: Should Helios use the zrok agent (`zrok agent start` + `zrok agent share`) or direct `zrok share`? Direct share is simpler and matches the existing provider pattern (one process per tunnel). The agent adds complexity but enables multi-share support. **Recommendation: start with direct `zrok share`, consider agent integration later.**

3. **Private share support**: Private shares require the mobile app to have zrok access capabilities (`zrok access private <token>`). This is a significant mobile-side change. **Recommendation: defer private share support to a follow-up spec. Focus on public/reserved for now.**

4. **zrok CLI version**: The docs reference both `zrok` and `zrok2` binaries. Need to check which is current. Try `zrok` first, fall back to `zrok2`.

---

## Rollout

1. **Phase 1**: Implement ZrokTunnel with public ephemeral shares (simplest, no reservation logic)
2. **Phase 2**: Add reserved share support with token persistence
3. **Phase 3**: Add `helios tunnel release` CLI command
4. **Phase 4** (future): Private share support, zrok agent integration
