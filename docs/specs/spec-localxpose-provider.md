# Spec: localxpose Tunnel Provider

## Summary

Add localxpose (loclx) as a tunnel provider for Helios. localxpose is a feature-rich tunneling platform with HTTP/TLS/TCP/UDP support, reserved domains, endpoint reservations, regional server selection, built-in basic auth, and a background service mode. It's the most full-featured commercial tunnel option.

## Why localxpose?

| Feature | localxpose | Cloudflare | ngrok | zrok | localhost.run |
|---------|-----------|-----------|-------|------|---------------|
| Account required | Yes (free tier) | No | Yes | Yes | No |
| Reserved domains | Yes (free) | No | Yes (paid) | Yes (free) | Yes ($9/mo) |
| Regional servers | Yes (us/eu/ap) | No | Yes (paid) | No | No |
| TCP/UDP tunnels | Yes | No | Yes | Yes | TLS passthru |
| Built-in auth | Yes (basic auth) | No | Yes (paid) | No | No |
| Background service | Yes (native) | No | No | Agent mode | No |
| Wildcard domains | Yes | No | Yes (paid) | No | No |
| Raw/headless mode | Yes (`--raw-mode`) | No | No | `--headless` | N/A (SSH) |

Key advantages:
- **Reserved domains for free**: Stable URLs via `loclx domain reserve`
- **Regional servers**: Route through US, EU, or Asia-Pacific for lower latency
- **Raw mode**: `--raw-mode` flag disables TUI — perfect for headless/daemon use
- **Background service**: `loclx service install` runs as a system service
- **Built-in basic auth**: `--basic-auth user:pass` adds auth without extra middleware
- **Multi-protocol**: HTTP, TLS, TCP, UDP tunnels from one tool

---

## localxpose Concepts

### Account & Authentication
```bash
# Get access token from https://localxpose.io/dashboard/access
loclx account login
# Enter access token
```

Or via environment variable: `ACCESS_TOKEN=xxx loclx tunnel http`

### Basic HTTP Tunnel
```bash
loclx tunnel http --port 8080
```
- Random subdomain assigned
- TLS terminated by localxpose servers
- `X-Forwarded-For` and `X-Real-Ip` headers added

### Custom Subdomain (Ephemeral)
```bash
loclx tunnel http --subdomain my-helios
```
- Temporary subdomain, released when tunnel stops

### Reserved Domain (Persistent)
```bash
# Reserve once
loclx domain reserve --subdomain my-helios

# Use in tunnels (stable URL)
loclx tunnel http --reserved-domain my-helios.loclx.io
```
- Persists across restarts
- Can also reserve custom domains with DNS verification

### Regional Selection
```bash
loclx tunnel http --region eu
```
- `us` (default), `eu`, `ap` available

### Raw Mode (Headless)
```bash
loclx tunnel http --raw-mode
```
- Disables the interactive TUI
- Outputs plain text to stdout — essential for Helios integration

---

## Design

### Provider Configuration

```yaml
# ~/.helios/config.yaml
tunnel:
  provider: localxpose
  localxpose:
    subdomain: ""             # ephemeral subdomain request
    reserved_domain: ""       # reserved domain (e.g., "my-helios.loclx.io")
    region: ""                # us | eu | ap (empty = default/us)
    basic_auth: ""            # "user:pass" for built-in auth (optional)
    access_token: ""          # access token (optional, overrides loclx account login)
```

### URL Stability Strategy

Similar to zrok's reserved shares, localxpose offers domain reservations:

```
First start with provider=localxpose:
  1. If reserved_domain is configured → use --reserved-domain
  2. If subdomain is configured → use --subdomain (ephemeral)
  3. If neither → start without subdomain, parse assigned URL

Recommended flow:
  - User runs `loclx domain reserve --subdomain my-helios` once (outside Helios)
  - Configures reserved_domain: "my-helios.loclx.io" in Helios config
  - All subsequent starts use the stable URL
```

Unlike zrok, we don't auto-reserve domains because localxpose reservations are account-level resources that may have limits. The user should manage reservations explicitly.

---

## Implementation

### 1. Config Changes

**`internal/daemon/config.go`**

```go
type LocalxposeConfig struct {
    Subdomain      string `yaml:"subdomain"`       // ephemeral subdomain
    ReservedDomain string `yaml:"reserved_domain"`  // reserved domain
    Region         string `yaml:"region"`           // us | eu | ap
    BasicAuth      string `yaml:"basic_auth"`       // user:pass
    AccessToken    string `yaml:"access_token"`     // override token
}
```

### 2. LocalxposeTunnel Provider

**`internal/tunnel/localxpose.go`** (new file)

```go
// LocalxposeTunnel uses `loclx` CLI for tunneling.
type LocalxposeTunnel struct {
    cmd            *exec.Cmd
    url            string
    subdomain      string
    reservedDomain string
    region         string
    basicAuth      string
    accessToken    string
}

func (t *LocalxposeTunnel) Provider() string { return "localxpose" }
func (t *LocalxposeTunnel) URL() string      { return t.url }

func (t *LocalxposeTunnel) PID() int {
    if t.cmd != nil && t.cmd.Process != nil {
        return t.cmd.Process.Pid
    }
    return 0
}
```

#### Start Flow

```go
func (t *LocalxposeTunnel) Start(localPort int) error {
    binary, err := exec.LookPath("loclx")
    if err != nil {
        return fmt.Errorf("localxpose not found: install from https://localxpose.io or 'npm install -g loclx'")
    }

    args := []string{"tunnel", "http",
        "--port", fmt.Sprintf("%d", localPort),
        "--raw-mode", // essential: disable TUI for headless operation
    }

    if t.reservedDomain != "" {
        args = append(args, "--reserved-domain", t.reservedDomain)
    } else if t.subdomain != "" {
        args = append(args, "--subdomain", t.subdomain)
    }

    if t.region != "" {
        args = append(args, "--region", t.region)
    }

    if t.basicAuth != "" {
        args = append(args, "--basic-auth", t.basicAuth)
    }

    t.cmd = exec.Command(binary, args...)
    t.cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

    // Pass access token via environment if configured
    if t.accessToken != "" {
        t.cmd.Env = append(os.Environ(), "ACCESS_TOKEN="+t.accessToken)
    }

    return t.startAndParseURL()
}
```

#### URL Parsing

In `--raw-mode`, loclx outputs the tunnel URL to stdout. Parse it:

```go
func (t *LocalxposeTunnel) startAndParseURL() error {
    stdout, err := t.cmd.StdoutPipe()
    if err != nil {
        return fmt.Errorf("create stdout pipe: %w", err)
    }
    stderr, err := t.cmd.StderrPipe()
    if err != nil {
        return fmt.Errorf("create stderr pipe: %w", err)
    }

    if err := t.cmd.Start(); err != nil {
        return fmt.Errorf("start localxpose: %w", err)
    }

    go t.cmd.Wait()

    urlCh := make(chan string, 1)
    // Match localxpose URLs: https://xxx.loclx.io or custom domains
    re := regexp.MustCompile(`https://[a-zA-Z0-9.-]+\.(loclx\.io|[a-zA-Z0-9.-]+)`)

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
        return fmt.Errorf("timeout waiting for localxpose tunnel URL")
    }
}

func (t *LocalxposeTunnel) Stop() error {
    if t.cmd != nil && t.cmd.Process != nil {
        return killProcess(t.cmd.Process.Pid)
    }
    return nil
}
```

### 3. Manager Integration

**`internal/tunnel/tunnel.go`** — add to provider switch:

```go
case "localxpose":
    t = &LocalxposeTunnel{
        subdomain:      lxConfig.Subdomain,
        reservedDomain: lxConfig.ReservedDomain,
        region:         lxConfig.Region,
        basicAuth:      lxConfig.BasicAuth,
        accessToken:    lxConfig.AccessToken,
    }
```

### 4. Authentication Check

Before starting, verify the user is authenticated:

```go
func checkLocalxposeAuth(binary string) error {
    out, err := exec.Command(binary, "account", "status").CombinedOutput()
    if err != nil {
        return fmt.Errorf("localxpose auth check failed: run 'loclx account login' first: %w", err)
    }
    if strings.Contains(strings.ToLower(string(out)), "not logged") {
        return fmt.Errorf("localxpose not authenticated: run 'loclx account login' with your access token")
    }
    return nil
}
```

---

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `internal/tunnel/localxpose.go` | **New** | LocalxposeTunnel struct, raw-mode tunneling |
| `internal/tunnel/tunnel.go` | Modify | Add "localxpose" case to provider switch |
| `internal/daemon/config.go` | Modify | Add LocalxposeConfig struct |
| `internal/server/api.go` | Modify | Pass localxpose config to tunnel start |
| `internal/daemon/daemon.go` | Modify | Wire localxpose config |
| `cmd/helios/main.go` | Modify | Add "localxpose" to help text |

---

## Edge Cases

### Not Authenticated
If `loclx account status` fails:
```
localxpose not authenticated: run 'loclx account login' with your access token from https://localxpose.io/dashboard/access
```

### Reserved Domain Not Found
If `--reserved-domain` specifies a domain that hasn't been reserved:
- loclx will error
- Surface the error: `"reserved domain 'xxx' not found: run 'loclx domain reserve --subdomain xxx' first"`

### Raw Mode Output Format
The `--raw-mode` output format may vary across loclx versions. The URL regex should be broad enough to match various formats. If parsing fails, fall back to checking `loclx tunnel list` for active tunnel URLs.

### Access Token in Config
The `access_token` field in config.yaml is a credential. It should:
- Be optional (prefer `loclx account login` which stores it securely)
- Be documented as an alternative for automated/CI setups
- Config file permissions should be 0600 (existing Helios behavior)

### Region Validation
Valid regions: `us`, `eu`, `ap`. Invalid region → let loclx handle the error and surface it.

---

## Testing Plan

1. **Unit tests** (`internal/tunnel/localxpose_test.go`):
   - Build command args for various configs (subdomain, reserved, region, auth)
   - Parse URL from raw-mode output
   - Config serialization/deserialization

2. **Integration tests** (manual, requires localxpose account):
   - Basic tunnel → starts, URL accessible
   - Subdomain → custom subdomain used
   - Reserved domain → stable URL across restarts
   - Region selection → connects to correct region
   - Raw mode → headless output parsed correctly
   - Auth check → clear error when not logged in
