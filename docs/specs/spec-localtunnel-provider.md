# Spec: localtunnel Tunnel Provider

## Summary

Add localtunnel as a tunnel provider for Helios. localtunnel is a lightweight, open-source tunneling tool that exposes local servers to the internet via the `lt` CLI. It's the simplest tunneling option — no account, no signup, no configuration required.

## Why localtunnel?

| Feature | localtunnel | Cloudflare | ngrok | zrok |
|---------|-------------|-----------|-------|------|
| Account required | No | No | Yes | Yes |
| Install | `npm i -g localtunnel` or `brew install localtunnel` | `brew install cloudflared` | Download binary | `brew install openziti/tap/zrok` |
| Setup steps | 0 | 0 | 1 (auth) | 2 (invite + enable) |
| Custom subdomain | Yes (free, best-effort) | No | Yes (paid) | Yes (reserved) |
| Self-hostable server | Yes | No | No | Yes |
| Open source | Yes (MIT) | No | No | Yes (Apache 2.0) |
| URL stability | Via `--subdomain` (best-effort) | Random each time | Stable (paid) | Via reserved shares |
| Dependencies | Node.js / npm | None | None | None |

Key advantages:
- **Zero setup**: No account, no token, no enrollment — just install and go
- **Custom subdomains**: Request a specific subdomain for free (e.g., `https://my-helios.loca.lt`)
- **Self-hostable**: Run your own localtunnel server for full control
- **Lightweight**: Simple Node.js tool, minimal resource usage
- **Auto-reconnect**: Client reconnects automatically if the local server restarts

---

## localtunnel Concepts

### Basic Usage
```bash
lt --port 8000
# Output: your url is: https://flkajsfljas.loca.lt
```

The tunnel remains active as long as the `lt` process runs. HTTPS is always enabled.

### Custom Subdomain
```bash
lt --port 8000 --subdomain my-helios
# Output: your url is: https://my-helios.loca.lt
```

Subdomain availability is best-effort — if the requested subdomain is taken, a random one is assigned. This provides semi-stable URLs without any account or payment.

### Custom Server
```bash
lt --port 8000 --host https://lt.mycompany.com
```

Users can run their own localtunnel server (via `localtunnel/server` on GitHub) and point the client to it.

### Local Host Override
```bash
lt --port 8000 --local-host 192.168.1.10
```

Proxy to a non-localhost address. Not relevant for Helios (always localhost).

---

## Design

### Provider Configuration

Extend `TunnelConfig` to support localtunnel-specific options:

```yaml
# ~/.helios/config.yaml
tunnel:
  provider: localtunnel
  localtunnel:
    subdomain: ""           # requested subdomain (empty = random)
    host: ""                # custom localtunnel server URL (empty = default loca.lt)
```

### URL Stability Strategy

localtunnel supports a `--subdomain` flag that requests a specific subdomain. While not guaranteed (another client could claim it), it provides good URL stability in practice:

```
First start with provider=localtunnel:
  1. If subdomain is configured → use it
  2. If subdomain is empty → start without subdomain, parse the assigned one, save it to config
  3. Subsequent starts reuse the saved subdomain for URL stability

If subdomain is unavailable:
  - localtunnel assigns a random subdomain
  - Parse the actual URL from output, update saved subdomain
  - Mobile devices may need to reconnect
```

---

## Implementation

### 1. Config Changes

**`internal/daemon/config.go`**

```go
type LocaltunnelConfig struct {
    Subdomain string `yaml:"subdomain"` // requested subdomain (empty = random)
    Host      string `yaml:"host"`      // custom server URL (empty = default)
}

type TunnelConfig struct {
    Provider    string             `yaml:"provider"`
    CustomURL   string             `yaml:"custom_url"`
    Zrok        ZrokConfig         `yaml:"zrok"`
    Localtunnel LocaltunnelConfig  `yaml:"localtunnel"`
}
```

### 2. LocaltunnelTunnel Provider

**`internal/tunnel/localtunnel.go`** (new file)

```go
// LocaltunnelTunnel uses `lt` CLI for simple tunneling.
type LocaltunnelTunnel struct {
    cmd       *exec.Cmd
    url       string
    subdomain string
    host      string
    onSubdomainAssigned func(subdomain string) // callback to persist subdomain
}

func (t *LocaltunnelTunnel) Provider() string { return "localtunnel" }
func (t *LocaltunnelTunnel) URL() string      { return t.url }

func (t *LocaltunnelTunnel) PID() int {
    if t.cmd != nil && t.cmd.Process != nil {
        return t.cmd.Process.Pid
    }
    return 0
}

func (t *LocaltunnelTunnel) Start(localPort int) error {
    // Try `lt` first, then `npx localtunnel`
    binary, err := exec.LookPath("lt")
    if err != nil {
        // Check if npx is available as fallback
        npx, npxErr := exec.LookPath("npx")
        if npxErr != nil {
            return fmt.Errorf("localtunnel not found: install with 'npm install -g localtunnel' or 'brew install localtunnel'")
        }
        return t.startWithNpx(npx, localPort)
    }
    return t.startWithBinary(binary, localPort)
}

func (t *LocaltunnelTunnel) startWithBinary(binary string, localPort int) error {
    args := []string{"--port", fmt.Sprintf("%d", localPort)}

    if t.subdomain != "" {
        args = append(args, "--subdomain", t.subdomain)
    }
    if t.host != "" {
        args = append(args, "--host", t.host)
    }

    t.cmd = exec.Command(binary, args...)
    t.cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

    return t.startAndParseURL()
}

func (t *LocaltunnelTunnel) startWithNpx(npxBinary string, localPort int) error {
    args := []string{"localtunnel", "--port", fmt.Sprintf("%d", localPort)}

    if t.subdomain != "" {
        args = append(args, "--subdomain", t.subdomain)
    }
    if t.host != "" {
        args = append(args, "--host", t.host)
    }

    t.cmd = exec.Command(npxBinary, args...)
    t.cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

    return t.startAndParseURL()
}
```

#### URL Parsing

localtunnel prints the URL to stdout in the format: `your url is: https://xxxxx.loca.lt`

```go
func (t *LocaltunnelTunnel) startAndParseURL() error {
    stdout, err := t.cmd.StdoutPipe()
    if err != nil {
        return fmt.Errorf("create stdout pipe: %w", err)
    }

    if err := t.cmd.Start(); err != nil {
        return fmt.Errorf("start localtunnel: %w", err)
    }

    go t.cmd.Wait()

    urlCh := make(chan string, 1)
    go func() {
        scanner := bufio.NewScanner(stdout)
        re := regexp.MustCompile(`https://([a-zA-Z0-9-]+)\.(loca\.lt|localtunnel\.me|[a-zA-Z0-9.-]+)`)
        for scanner.Scan() {
            line := scanner.Text()
            if match := re.FindStringSubmatch(line); match != nil {
                urlCh <- match[0]
                // Extract and save subdomain for future reuse
                if t.onSubdomainAssigned != nil {
                    t.onSubdomainAssigned(match[1])
                }
                return
            }
        }
    }()

    select {
    case url := <-urlCh:
        t.url = url
        return nil
    case <-time.After(30 * time.Second):
        killProcess(t.cmd.Process.Pid)
        return fmt.Errorf("timeout waiting for localtunnel URL")
    }
}

func (t *LocaltunnelTunnel) Stop() error {
    if t.cmd != nil && t.cmd.Process != nil {
        return killProcess(t.cmd.Process.Pid)
    }
    return nil
}
```

### 3. Manager Integration

**`internal/tunnel/tunnel.go`** — add localtunnel to the provider switch:

```go
case "localtunnel":
    t = &LocaltunnelTunnel{
        subdomain: ltConfig.Subdomain,
        host:      ltConfig.Host,
        onSubdomainAssigned: func(subdomain string) {
            if onLocaltunnelSubdomainAssigned != nil {
                onLocaltunnelSubdomainAssigned(subdomain)
            }
        },
    }
```

### 4. API Changes

**`POST /internal/tunnel/start`** — extend the request body:

```json
{
    "provider": "localtunnel",
    "localtunnel": {
        "subdomain": "my-helios",
        "host": ""
    }
}
```

Response unchanged:

```json
{
    "public_url": "https://my-helios.loca.lt"
}
```

### 5. Node.js Dependency Consideration

localtunnel requires Node.js/npm. This is the only Helios tunnel provider with a runtime dependency beyond a single binary. Options:

1. **Require Node.js** (recommended): Most developers have Node.js installed. Error message guides installation.
2. **Go alternative**: Use the Go client `go-localtunnel` as a library instead of shelling out to `lt`. This eliminates the Node.js dependency but adds a Go dependency.
3. **npx fallback**: If `lt` is not globally installed but `npx` is available, use `npx localtunnel` as a fallback (slower first start due to download).

**Recommendation**: Shell out to `lt` with `npx` fallback. If neither is available, suggest installation. Consider the Go client (`go-localtunnel`) as a future optimization to eliminate the Node.js dependency entirely.

---

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `internal/tunnel/localtunnel.go` | **New** | LocaltunnelTunnel struct, Start/Stop/URL/PID, subdomain logic |
| `internal/tunnel/tunnel.go` | Modify | Add "localtunnel" case to provider switch |
| `internal/daemon/config.go` | Modify | Add LocaltunnelConfig struct, extend TunnelConfig |
| `internal/server/api.go` | Modify | Pass localtunnel config to tunnel start |
| `internal/daemon/daemon.go` | Modify | Wire localtunnel config + subdomain persistence callback |
| `cmd/helios/main.go` | Modify | Add "localtunnel" to help text |

---

## Edge Cases

### `lt` Binary Not Found
Try `npx localtunnel` as fallback. If neither available:
```
localtunnel not found: install with 'npm install -g localtunnel' or 'brew install localtunnel'
```

### Subdomain Unavailable
If the requested subdomain is taken, localtunnel assigns a random one. The parser detects the actual subdomain from the output and updates the saved config. Log a warning:
```
localtunnel: requested subdomain "my-helios" unavailable, assigned "random-xyz" instead
```

### localtunnel Server Down
The default `loca.lt` server may occasionally be unavailable (it's community-maintained). If the tunnel fails to start:
```
localtunnel: failed to connect to server. Try again later or use a custom server with tunnel.localtunnel.host
```

### First-Time Visitor Warning Page
localtunnel shows a "click to continue" interstitial page on first visit from each IP. This is a known limitation of the free service. Users can:
- Self-host the localtunnel server (no interstitial)
- Use a different provider for production use

This does **not** affect the Helios mobile app after the first visit, as the cookie persists.

---

## Testing Plan

1. **Unit tests** (`internal/tunnel/localtunnel_test.go`):
   - Parse URL from various `lt` output formats
   - Extract subdomain from URL
   - Config serialization/deserialization with LocaltunnelConfig

2. **Integration tests** (manual, requires Node.js):
   - `provider=localtunnel` → tunnel starts, URL accessible
   - `provider=localtunnel, subdomain=test-xyz` → custom subdomain requested
   - Subdomain persistence → restart uses same subdomain
   - npx fallback → works when `lt` not globally installed

3. **Adoption test**:
   - Start localtunnel, kill daemon, restart daemon → tunnel adopted via PID
