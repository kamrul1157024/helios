# Spec: localhost.run Tunnel Provider

## Summary

Add localhost.run as a tunnel provider for Helios. localhost.run is unique among tunnel providers: it requires **zero client installation** — it uses SSH reverse tunnels, which are built into every OS. This makes it the lowest-friction tunnel option available.

## Why localhost.run?

| Feature | localhost.run | Cloudflare | ngrok | localtunnel | zrok |
|---------|--------------|-----------|-------|-------------|------|
| Client install | **None (SSH)** | Binary | Binary | npm | Binary |
| Account required | No (free) | No | Yes | No | Yes |
| Setup steps | 0 | 0 | 1 | 1 (npm) | 2 |
| Custom domains | Yes ($9/mo) | No | Yes (paid) | No | Yes (reserved) |
| URL stability | With SSH key | Random | Paid | Best-effort | Reserved |
| Self-hostable | No | No | No | Yes | Yes |
| HTTPS | Yes | Yes | Yes | Yes | Yes |

Key advantages:
- **Zero install**: Uses SSH, which is already on every macOS, Linux, and Windows (10+) machine
- **Zero signup for free tier**: `ssh -R 80:localhost:PORT nokey@localhost.run` — that's it
- **SSH key stability**: Adding an SSH key to the admin console gives the free domain longer persistence
- **Custom domains**: `lhr.rocks` subdomains or bring your own domain ($9/mo)
- **No dependencies**: No Node.js, no binary, no package manager — just SSH

---

## localhost.run Concepts

### Free Tunnel (No Key)
```bash
ssh -R 80:localhost:8080 nokey@localhost.run
```
- Random subdomain assigned (e.g., `https://abc123.localhost.run`)
- Domain changes regularly (anti-phishing measure)
- Speed-limited
- No account needed

### Free Tunnel (With SSH Key)
```bash
ssh -R 80:localhost:8080 localhost.run
```
- Register at admin console, upload SSH key
- Domain persists longer
- Still free

### Custom Domain ($9/mo)
```bash
ssh -R myapp.example.com:80:localhost:8080 plan@localhost.run
```
- Stable domain, priority bandwidth
- DNS setup required (CNAME to `cd.localhost.run` or A records)
- Max 5 concurrent tunnels
- `lhr.rocks` subdomains available (no DNS setup)

### Connection Stability
SSH tunnels can drop on network changes. Solutions:
- `ssh -o ServerAliveInterval=60` — keepalive packets
- `autossh -M 0 -o ServerAliveInterval=60` — auto-reconnect wrapper

---

## Design

### Provider Configuration

```yaml
# ~/.helios/config.yaml
tunnel:
  provider: localhostrun
  localhostrun:
    ssh_user: ""              # "" (default/key-based) | "nokey" (anonymous) | "plan" (custom domain)
    custom_domain: ""         # custom domain (e.g., "myapp.lhr.rocks" or "tunnel.example.com")
    keepalive_interval: 60    # ServerAliveInterval in seconds (default: 60)
    use_autossh: false        # use autossh for auto-reconnect if available
```

### Process Model

Unlike other providers that run a dedicated binary, localhost.run uses SSH:

```
daemon (PID 100)         ssh (PID 101, independent process group via Setsid)
  │                        │
  └── manages via PID ─────┘
```

This follows the exact same pattern as cloudflared/ngrok — spawn a process, parse output for URL, manage via PID.

### URL Parsing

SSH prints the tunnel URL to stdout when the connection is established:

```
$ ssh -R 80:localhost:8080 nokey@localhost.run
Connect to http://localhost.run for more options. Use https to len TLS.

** your connection id is abc123-def456 **

https://abc123def456.localhost.run -- https://abc123def456.localhost.run
```

Parse the URL using regex from stdout.

---

## Implementation

### 1. Config Changes

**`internal/daemon/config.go`**

```go
type LocalhostRunConfig struct {
    SSHUser           string `yaml:"ssh_user"`           // "" | "nokey" | "plan"
    CustomDomain      string `yaml:"custom_domain"`      // custom domain or lhr.rocks subdomain
    KeepaliveInterval int    `yaml:"keepalive_interval"` // ServerAliveInterval (default: 60)
    UseAutossh        bool   `yaml:"use_autossh"`        // use autossh if available
}
```

### 2. LocalhostRunTunnel Provider

**`internal/tunnel/localhostrun.go`** (new file)

```go
// LocalhostRunTunnel uses SSH reverse tunnels via localhost.run.
type LocalhostRunTunnel struct {
    cmd          *exec.Cmd
    url          string
    sshUser      string
    customDomain string
    keepalive    int
    useAutossh   bool
}

func (t *LocalhostRunTunnel) Provider() string { return "localhostrun" }
func (t *LocalhostRunTunnel) URL() string      { return t.url }

func (t *LocalhostRunTunnel) PID() int {
    if t.cmd != nil && t.cmd.Process != nil {
        return t.cmd.Process.Pid
    }
    return 0
}
```

#### Start Flow

```go
func (t *LocalhostRunTunnel) Start(localPort int) error {
    // Determine binary: prefer autossh if configured, fallback to ssh
    var binary string
    var args []string

    if t.useAutossh {
        autosshBin, err := exec.LookPath("autossh")
        if err != nil {
            // Fall back to plain ssh with keepalive
            log.Printf("localhostrun: autossh not found, falling back to ssh")
        } else {
            binary = autosshBin
            args = append(args, "-M", "0")
        }
    }

    if binary == "" {
        sshBin, err := exec.LookPath("ssh")
        if err != nil {
            return fmt.Errorf("ssh not found (this should not happen)")
        }
        binary = sshBin
    }

    // Build SSH arguments
    keepalive := t.keepalive
    if keepalive == 0 {
        keepalive = 60
    }
    args = append(args,
        "-o", fmt.Sprintf("ServerAliveInterval=%d", keepalive),
        "-o", "StrictHostKeyChecking=accept-new",
        "-o", "ExitOnForwardFailure=yes",
    )

    // Build the -R (reverse tunnel) argument
    remoteSpec := t.buildRemoteSpec(localPort)
    args = append(args, "-R", remoteSpec)

    // SSH user
    user := t.sshUser
    if user == "" {
        user = "nokey" // default to anonymous free tier
    }
    args = append(args, fmt.Sprintf("%s@localhost.run", user))

    t.cmd = exec.Command(binary, args...)
    t.cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

    return t.startAndParseURL()
}

func (t *LocalhostRunTunnel) buildRemoteSpec(localPort int) string {
    localTarget := fmt.Sprintf("localhost:%d", localPort)

    if t.customDomain != "" {
        // Custom domain: -R domain.com:80:localhost:PORT
        return fmt.Sprintf("%s:80:%s", t.customDomain, localTarget)
    }
    // Free tier: -R 80:localhost:PORT
    return fmt.Sprintf("80:%s", localTarget)
}
```

#### URL Parsing

```go
func (t *LocalhostRunTunnel) startAndParseURL() error {
    stdout, err := t.cmd.StdoutPipe()
    if err != nil {
        return fmt.Errorf("create stdout pipe: %w", err)
    }
    stderr, err := t.cmd.StderrPipe()
    if err != nil {
        return fmt.Errorf("create stderr pipe: %w", err)
    }

    if err := t.cmd.Start(); err != nil {
        return fmt.Errorf("start ssh tunnel: %w", err)
    }

    go t.cmd.Wait()

    urlCh := make(chan string, 1)
    // localhost.run outputs: https://xxxx.localhost.run
    re := regexp.MustCompile(`https://[a-zA-Z0-9.-]+\.(localhost\.run|lhr\.rocks|[a-zA-Z0-9.-]+)`)

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
        return fmt.Errorf("timeout waiting for localhost.run tunnel URL")
    }
}

func (t *LocalhostRunTunnel) Stop() error {
    if t.cmd != nil && t.cmd.Process != nil {
        return killProcess(t.cmd.Process.Pid)
    }
    return nil
}
```

### 3. Manager Integration

**`internal/tunnel/tunnel.go`** — add to provider switch:

```go
case "localhostrun":
    t = &LocalhostRunTunnel{
        sshUser:      lhrConfig.SSHUser,
        customDomain: lhrConfig.CustomDomain,
        keepalive:    lhrConfig.KeepaliveInterval,
        useAutossh:   lhrConfig.UseAutossh,
    }
```

### 4. SSH Host Key Verification

On first connection, SSH will prompt to accept the host key. We handle this with:
```
-o StrictHostKeyChecking=accept-new
```

This auto-accepts on first connection and verifies on subsequent ones — secure and non-interactive.

### 5. SSH Connection Resilience

SSH tunnels are inherently less stable than dedicated tunnel clients. Mitigations:

1. **ServerAliveInterval=60**: Detect dead connections within ~60s
2. **ExitOnForwardFailure=yes**: Fail fast if port forwarding fails
3. **autossh** (optional): Auto-reconnect wrapper. Helios uses it if available and configured
4. **Daemon-level recovery**: If the SSH process dies, daemon adoption detects it (PID dead), removes stale state, and can re-establish on next status check or manual restart

---

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `internal/tunnel/localhostrun.go` | **New** | LocalhostRunTunnel struct, SSH-based tunneling |
| `internal/tunnel/tunnel.go` | Modify | Add "localhostrun" case to provider switch |
| `internal/daemon/config.go` | Modify | Add LocalhostRunConfig struct |
| `internal/server/api.go` | Modify | Pass localhostrun config to tunnel start |
| `internal/daemon/daemon.go` | Modify | Wire localhostrun config |
| `cmd/helios/main.go` | Modify | Add "localhostrun" to help text |

---

## Edge Cases

### SSH Key Not Found
If using non-`nokey` user and no SSH key exists:
- SSH will fail or prompt for password (which hangs in non-interactive mode)
- Detect this: if `sshUser != "nokey"` and no key at `~/.ssh/id_rsa` or `~/.ssh/id_ed25519`, warn user

### SSH Host Key Changed
If localhost.run's host key changes (rare), SSH will refuse to connect. The error is distinctive:
```
WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED!
```
Detect this from stderr and surface a clear error.

### Connection Drop Mid-Session
SSH tunnel dies → PID goes away → daemon adoption detects dead PID on next check → stale state cleaned up. Mobile devices lose connectivity until tunnel is re-established.

### Free Tier Domain Rotation
Free domains change regularly. This means mobile devices will need to reconnect. Users who need URL stability should:
- Register an SSH key for longer persistence
- Use a custom domain ($9/mo)
- Or use a different provider (zrok reserved, ngrok paid)

---

## Testing Plan

1. **Unit tests** (`internal/tunnel/localhostrun_test.go`):
   - Build SSH command args for various configs (nokey, custom domain, autossh)
   - Parse URL from various ssh output formats
   - Remote spec construction

2. **Integration tests** (manual):
   - Free tier (`nokey`) → tunnel starts, URL accessible
   - With SSH key → tunnel starts, longer domain persistence
   - Custom domain → correct SSH user and -R flag
   - autossh → auto-reconnect after network blip
   - Keepalive → connection stays alive over idle periods
