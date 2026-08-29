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

// How long a resident-set reading is trusted. Measuring shells out to ps and
// pgrep once per process in every host's tree, and clients ask on every poll.
const usageTTL = 30 * time.Second

// SpawnFunc launches a detached ptyhost for a session. Empty argv means
// "resume this session's agent", which is the warm-pool path; otherwise argv
// is the command the host executes, which is how a new session starts.
type SpawnFunc func(sessionID, cwd string, argv []string, env map[string]string) error

// Registry maps session IDs to live terminal hosts and owns the warm pool.
// It holds no PTYs itself: each host is a separate process, so a daemon
// restart does not SIGHUP live sessions.
type Registry struct {
	heliosDir string
	spawn     SpawnFunc

	mu      sync.Mutex
	entries map[string]*entry

	usageMu   sync.Mutex
	usage     map[string]int64
	usageRead time.Time
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
		heliosDir: heliosDir,
		spawn:     spawn,
		entries:   make(map[string]*entry),
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
	return r.start(sessionID, cwd, nil, nil)
}

// Start launches a host that runs argv. Unlike Wake it never adopts an
// existing host: starting a session that is already running would give the
// user two agents in one terminal.
func (r *Registry) Start(sessionID, cwd string, argv []string) (string, error) {
	return r.StartWithEnv(sessionID, cwd, argv, nil)
}

// StartWithEnv is Start with extra environment for the child process.
func (r *Registry) StartWithEnv(sessionID, cwd string, argv []string, env map[string]string) (string, error) {
	if r.IsWarm(sessionID) {
		return "", fmt.Errorf("terminal: session %s already has a live host", sessionID)
	}
	return r.start(sessionID, cwd, argv, env)
}

func (r *Registry) start(sessionID, cwd string, argv []string, env map[string]string) (string, error) {
	sock := SocketPath(r.heliosDir, sessionID)

	r.mu.Lock()
	e, known := r.entries[sessionID]
	r.mu.Unlock()

	// Adopting a live host only makes sense when resuming. With an explicit
	// command the caller wants a fresh terminal.
	if len(argv) == 0 {
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

	if err := r.spawn(sessionID, cwd, argv, env); err != nil {
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

// Touch records that a session is active, which is what orders Warm.
//
// Callers should throttle: this takes the registry lock, and screen activity
// arrives many times a second from a redrawing TUI.
func (r *Registry) Touch(sessionID string) { r.touch(sessionID) }

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
	// A host adopted before its sidecar landed has no pid on record. Reading it
	// now is the last chance: RemoveHostFiles below deletes the sidecar, and
	// without a pid the process is unreachable — no socket to find it by, no
	// entry to list it from, running until the machine stops.
	pid := e.pid
	if pid == 0 {
		if s, err := ReadSidecar(SidecarPath(r.heliosDir, sessionID)); err == nil {
			pid = s.PID
		}
	}
	if pid > 0 {
		if p, err := os.FindProcess(pid); err == nil {
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
	if pid > 0 && Probe(e.socket) {
		if p, err := os.FindProcess(pid); err == nil {
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

// Sweep forgets hosts whose socket has gone away.
//
// It kills nothing of its own accord. A session that has sat untouched for a
// week is still a session the user did not close, and only the user closes
// one: the pool has no ceiling to enforce and no idle TTL.
func (r *Registry) Sweep() {
	r.mu.Lock()
	var stale []string
	for id, e := range r.entries {
		if !Probe(e.socket) {
			stale = append(stale, id)
		}
	}
	r.mu.Unlock()

	for _, id := range stale {
		r.mu.Lock()
		delete(r.entries, id)
		r.mu.Unlock()
		RemoveHostFiles(r.heliosDir, id)
	}
}

// Usage reports resident bytes per warm session, so clients can show what a
// session costs and the user can decide which to close. Readings are cached
// for usageTTL because measuring is a fork per process in every host's tree.
func (r *Registry) Usage() map[string]int64 {
	r.usageMu.Lock()
	defer r.usageMu.Unlock()
	if r.usage != nil && time.Since(r.usageRead) < usageTTL {
		return r.usage
	}

	r.mu.Lock()
	pids := make(map[string]int, len(r.entries))
	for id, e := range r.entries {
		pids[id] = e.pid
	}
	r.mu.Unlock()

	usage := make(map[string]int64, len(pids))
	for id, pid := range pids {
		if rss := processTreeRSS(pid); rss > 0 {
			usage[id] = rss
		}
	}
	r.usage = usage
	r.usageRead = time.Now()
	return usage
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
