package terminal

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Defaults for the warm pool. max_warm is 3 rather than 5 because a warm
// Claude Code measures ~380 MB RSS; re-warming an evicted session costs under
// 5s via `claude --resume`, so memory is the binding constraint, not latency.
const (
	DefaultMaxWarm  = 3
	DefaultIdleTTL  = 2 * time.Hour
	DefaultMaxWarmR = 0 // 0 means "derive from system memory"
)

// SpawnFunc launches a detached ptyhost for a session. An empty command means
// "resume this session's agent", which is the warm-pool path; a non-empty
// command is typed into a fresh login shell, which is how a new session
// starts.
type SpawnFunc func(sessionID, cwd, command string) error

// Registry maps session IDs to live terminal hosts and owns the warm pool.
// It holds no PTYs itself: each host is a separate process, so a daemon
// restart does not SIGHUP live sessions.
type Registry struct {
	heliosDir string
	spawn     SpawnFunc

	mu      sync.Mutex
	entries map[string]*entry

	MaxWarm    int
	IdleTTL    time.Duration
	MaxWarmRSS int64 // bytes; 0 disables the memory ceiling
}

type entry struct {
	sessionID  string
	socket     string
	cwd        string
	pid        int
	lastActive time.Time
}

// NewRegistry returns a Registry rooted at heliosDir.
func NewRegistry(heliosDir string, spawn SpawnFunc) *Registry {
	return &Registry{
		heliosDir:  heliosDir,
		spawn:      spawn,
		entries:    make(map[string]*entry),
		MaxWarm:    DefaultMaxWarm,
		IdleTTL:    DefaultIdleTTL,
		MaxWarmRSS: defaultMaxWarmRSS(),
	}
}

// Recover rebuilds the registry from the run directory after a daemon
// restart. Sockets that fail to dial are stale and are cleaned up.
func (r *Registry) Recover() (alive int, cleaned int, err error) {
	cars, err := ListSidecars(r.heliosDir)
	if err != nil {
		return 0, 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range cars {
		sock := SocketPath(r.heliosDir, s.SessionID)
		if Probe(sock) {
			r.entries[s.SessionID] = &entry{
				sessionID:  s.SessionID,
				socket:     sock,
				cwd:        s.Cwd,
				pid:        s.PID,
				lastActive: time.Now(),
			}
			alive++
			continue
		}
		RemoveHostFiles(r.heliosDir, s.SessionID)
		cleaned++
	}
	return alive, cleaned, nil
}

// IsWarm reports whether a session currently has a live host.
func (r *Registry) IsWarm(sessionID string) bool {
	r.mu.Lock()
	e, ok := r.entries[sessionID]
	r.mu.Unlock()
	if !ok {
		return false
	}
	return Probe(e.socket)
}

// Socket returns the socket path for a warm session.
func (r *Registry) Socket(sessionID string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[sessionID]
	if !ok {
		return "", false
	}
	return e.socket, true
}

// Wake ensures a session has a live host, spawning one if needed, and returns
// its socket path. It is idempotent, which is what lets the mobile app call
// it on every session-detail open.
func (r *Registry) Wake(sessionID, cwd string) (string, error) {
	return r.start(sessionID, cwd, "")
}

// Start launches a host that runs command in a login shell. Unlike Wake it
// never adopts an existing host: starting a session that is already running
// would give the user two agents in one terminal.
func (r *Registry) Start(sessionID, cwd, command string) (string, error) {
	if r.IsWarm(sessionID) {
		return "", fmt.Errorf("terminal: session %s already has a live host", sessionID)
	}
	return r.start(sessionID, cwd, command)
}

func (r *Registry) start(sessionID, cwd, command string) (string, error) {
	sock := SocketPath(r.heliosDir, sessionID)

	r.mu.Lock()
	e, known := r.entries[sessionID]
	r.mu.Unlock()

	// Adopting a live host only makes sense when resuming. With an explicit
	// command the caller wants a fresh terminal.
	if command == "" {
		if known && Probe(e.socket) {
			r.touch(sessionID)
			return e.socket, nil
		}
		// Another daemon or a previous run may have left a live host behind.
		if Probe(sock) {
			r.adopt(sessionID, sock, cwd)
			return sock, nil
		}
	}

	RemoveHostFiles(r.heliosDir, sessionID)
	if r.spawn == nil {
		return "", fmt.Errorf("terminal: no spawn function configured")
	}
	// Make room before adding, so the ceiling is respected at all times.
	r.evictForRoom()

	if err := r.spawn(sessionID, cwd, command); err != nil {
		return "", fmt.Errorf("spawn terminal host for %s: %w", sessionID, err)
	}
	if !WaitForSocket(sock, 15*time.Second) {
		return "", fmt.Errorf("terminal host for %s did not come up", sessionID)
	}
	r.adopt(sessionID, sock, cwd)
	return sock, nil
}

// Adopt records an already-running host, which is how `helios wrap` binds a
// terminal the user started by hand.
func (r *Registry) Adopt(sessionID, cwd string) (string, error) {
	sock := SocketPath(r.heliosDir, sessionID)
	if !Probe(sock) {
		return "", fmt.Errorf("terminal: no live host for session %s", sessionID)
	}
	r.adopt(sessionID, sock, cwd)
	return sock, nil
}

func (r *Registry) adopt(sessionID, sock, cwd string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	pid := 0
	if s, err := ReadSidecar(SidecarPath(r.heliosDir, sessionID)); err == nil {
		pid = s.PID
	}
	r.entries[sessionID] = &entry{
		sessionID:  sessionID,
		socket:     sock,
		cwd:        cwd,
		pid:        pid,
		lastActive: time.Now(),
	}
}

func (r *Registry) touch(sessionID string) {
	r.mu.Lock()
	if e, ok := r.entries[sessionID]; ok {
		e.lastActive = time.Now()
	}
	r.mu.Unlock()
}

// Forget drops a session from the registry without touching its host. The
// host keeps running and can be adopted again later, which is what makes this
// safe to call when a session record is deleted but its terminal is not.
func (r *Registry) Forget(sessionID string) {
	r.mu.Lock()
	delete(r.entries, sessionID)
	r.mu.Unlock()
}

// Evict shuts a host down. The session is not lost: it returns to cold and is
// re-warmed on demand via `claude --resume`.
func (r *Registry) Evict(sessionID string) error {
	r.mu.Lock()
	e, ok := r.entries[sessionID]
	delete(r.entries, sessionID)
	r.mu.Unlock()
	if !ok {
		return nil
	}
	if e.pid > 0 {
		if p, err := os.FindProcess(e.pid); err == nil {
			p.Signal(syscall.SIGTERM)
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !Probe(e.socket) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if e.pid > 0 && Probe(e.socket) {
		if p, err := os.FindProcess(e.pid); err == nil {
			p.Signal(syscall.SIGKILL)
		}
	}
	RemoveHostFiles(r.heliosDir, sessionID)
	return nil
}

// Warm returns live session IDs, most recently active first.
func (r *Registry) Warm() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.entries))
	for id := range r.entries {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool {
		return r.entries[out[i]].lastActive.After(r.entries[out[j]].lastActive)
	})
	return out
}

// Sweep evicts hosts that are idle past the TTL and any whose socket has gone
// away, then enforces the count and memory ceilings.
func (r *Registry) Sweep() {
	r.mu.Lock()
	var stale, idle []string
	for id, e := range r.entries {
		if !Probe(e.socket) {
			stale = append(stale, id)
			continue
		}
		if r.IdleTTL > 0 && time.Since(e.lastActive) > r.IdleTTL {
			idle = append(idle, id)
		}
	}
	r.mu.Unlock()

	for _, id := range stale {
		r.mu.Lock()
		delete(r.entries, id)
		r.mu.Unlock()
		RemoveHostFiles(r.heliosDir, id)
	}
	for _, id := range idle {
		r.Evict(id)
	}
	r.evictForRoom()
}

// evictForRoom enforces both ceilings: at most MaxWarm hosts, and total warm
// RSS under MaxWarmRSS. Least-recently-active goes first.
func (r *Registry) evictForRoom() {
	for {
		r.mu.Lock()
		if len(r.entries) == 0 {
			r.mu.Unlock()
			return
		}
		overCount := r.MaxWarm > 0 && len(r.entries) >= r.MaxWarm
		var overMem bool
		if r.MaxWarmRSS > 0 {
			var total int64
			for _, e := range r.entries {
				total += processTreeRSS(e.pid)
			}
			overMem = total > r.MaxWarmRSS
		}
		if !overCount && !overMem {
			r.mu.Unlock()
			return
		}
		var oldest *entry
		for _, e := range r.entries {
			if oldest == nil || e.lastActive.Before(oldest.lastActive) {
				oldest = e
			}
		}
		id := oldest.sessionID
		r.mu.Unlock()

		if err := r.Evict(id); err != nil {
			return
		}
	}
}

// WaitForSocket blocks until a socket accepts connections or the timeout
// elapses.
func WaitForSocket(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if Probe(path) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// processTreeRSS returns resident bytes for a pid and its descendants. Claude
// Code spawns helpers, so the parent alone understates the real cost.
func processTreeRSS(pid int) int64 {
	if pid <= 0 {
		return 0
	}
	var total int64
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err == nil {
		if kb, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64); err == nil {
			total += kb * 1024
		}
	}
	kids, err := exec.Command("pgrep", "-P", strconv.Itoa(pid)).Output()
	if err != nil {
		return total
	}
	for _, f := range strings.Fields(string(kids)) {
		if child, err := strconv.Atoi(f); err == nil {
			total += processTreeRSS(child)
		}
	}
	return total
}

// defaultMaxWarmRSS budgets a quarter of physical memory for warm sessions.
func defaultMaxWarmRSS() int64 {
	out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
	if err != nil {
		return 0
	}
	total, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil || total <= 0 {
		return 0
	}
	return total / 4
}
