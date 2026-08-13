package terminal

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Defaults for the warm pool.
//
// A warm Claude Code measures ~380 MB RSS, so memory is the binding constraint
// rather than latency: re-warming costs under 5s via `claude --resume`. That
// makes MaxWarmRSS the ceiling that should actually bind, and MaxWarm a
// backstop against pathological session counts rather than the everyday limit
// — hence 20 and not 3.
//
// There is deliberately no idle TTL. A session goes away when the user closes
// it, and at no other time: age is not evidence that nobody wants it, and an
// eviction costs the host's scrollback ring, which no `claude --resume` brings
// back. Sessions that outlive their usefulness sit idle until they are closed
// or until the pool needs the room.
const (
	DefaultMaxWarm  = 20
	DefaultMaxWarmR = 0 // 0 means "derive from system memory"
)

// SpawnFunc launches a detached ptyhost for a session. Empty argv means
// "resume this session's agent", which is the warm-pool path; otherwise argv
// is the command the host executes, which is how a new session starts.
type SpawnFunc func(sessionID, cwd string, argv []string) error

// Registry maps session IDs to live terminal hosts and owns the warm pool.
// It holds no PTYs itself: each host is a separate process, so a daemon
// restart does not SIGHUP live sessions.
type Registry struct {
	heliosDir string
	spawn     SpawnFunc

	mu      sync.Mutex
	entries map[string]*entry

	MaxWarm    int
	MaxWarmRSS int64 // bytes; 0 disables the memory ceiling

	// InUse reports whether a session is being watched right now. Sessions it
	// returns true for are never evicted to reclaim room, however old they
	// look: killing the terminal somebody has open on their phone is the one
	// eviction the user is guaranteed to notice.
	//
	// It is called without the registry lock held, because the implementation
	// lives in the backend and takes its own mutex before ours.
	InUse func(sessionID string) bool
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
	return r.start(sessionID, cwd, nil)
}

// Start launches a host that runs argv. Unlike Wake it never adopts an
// existing host: starting a session that is already running would give the
// user two agents in one terminal.
func (r *Registry) Start(sessionID, cwd string, argv []string) (string, error) {
	if r.IsWarm(sessionID) {
		return "", fmt.Errorf("terminal: session %s already has a live host", sessionID)
	}
	return r.start(sessionID, cwd, argv)
}

func (r *Registry) start(sessionID, cwd string, argv []string) (string, error) {
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
	// Make room before adding, so the ceiling is respected at all times.
	r.evictForRoom(1)

	if err := r.spawn(sessionID, cwd, argv); err != nil {
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

// Touch records that a session is active, so the LRU in evictForRoom reflects
// use rather than adoption order.
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

// Sweep forgets hosts whose socket has gone away, then enforces the count and
// memory ceilings.
//
// It kills nothing on account of age. A session that has sat untouched for a
// week is still a session the user did not close, and the only thing an
// eviction would buy is memory the ceilings already govern.
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
	// Nothing is waiting on a slot here, unlike the call in start: a sweep that
	// asked for headroom would evict a healthy session on every pass and hold
	// the pool one below its ceiling forever.
	r.evictForRoom(0)
}

// inUse consults the InUse predicate, if one is set. Never call it with the
// registry lock held.
func (r *Registry) inUse(sessionID string) bool {
	return r.InUse != nil && r.InUse(sessionID)
}

// evictForRoom enforces both ceilings: at most MaxWarm hosts, and total warm
// RSS under MaxWarmRSS. Least-recently-active goes first, and sessions with a
// live viewer are never chosen.
//
// headroom is the number of hosts the caller is about to add. start passes 1
// so the ceiling still holds once its host comes up; a caller that is only
// tidying passes 0. Getting this wrong is not harmless: with headroom always
// 1, every sweep evicts a healthy session to make room nobody asked for.
//
// Neither the RSS measurement nor the InUse check may run under the registry
// lock. processTreeRSS shells out to ps and pgrep once per process in the
// tree, and InUse calls back into the backend, which takes its own mutex
// before this one.
func (r *Registry) evictForRoom(headroom int) {
	for {
		r.mu.Lock()
		if len(r.entries) == 0 {
			r.mu.Unlock()
			return
		}
		overCount := r.MaxWarm > 0 && len(r.entries)+headroom > r.MaxWarm
		candidates := make([]*entry, 0, len(r.entries))
		for _, e := range r.entries {
			candidates = append(candidates, e)
		}
		measure := r.MaxWarmRSS
		r.mu.Unlock()

		var overMem bool
		if measure > 0 {
			var total int64
			for _, e := range candidates {
				total += processTreeRSS(e.pid)
			}
			overMem = total > measure
		}
		if !overCount && !overMem {
			return
		}

		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].lastActive.Before(candidates[j].lastActive)
		})
		victim := ""
		for _, e := range candidates {
			// Never a user's shell. An evicted agent comes back with
			// `claude --resume`; an evicted shell is a lost scrollback and a
			// job the user was running, with nothing to resume it from.
			if IsShell(e.sessionID) {
				continue
			}
			if !r.inUse(e.sessionID) {
				victim = e.sessionID
				break
			}
		}
		if victim == "" {
			// Everything warm is being watched. Going over the ceiling is the
			// better failure: the alternative is killing a terminal somebody
			// has open.
			log.Printf("registry: over warm ceiling (%d hosts) but every session has a viewer", len(candidates))
			return
		}
		if err := r.Evict(victim); err != nil {
			log.Printf("registry: evict %s for room: %v", victim, err)
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
//
// This is the ceiling that actually binds — MaxWarm is a backstop — so a
// platform where it returns 0 has no memory ceiling at all. Linux is read from
// /proc/meminfo rather than sysctl, which reports something unrelated there.
func defaultMaxWarmRSS() int64 {
	if runtime.GOOS == "linux" {
		return linuxMemTotal() / 4
	}
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

// linuxMemTotal returns physical memory in bytes from /proc/meminfo, or 0 if
// it cannot be read.
func linuxMemTotal() int64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		// "MemTotal:", value, unit — the kernel always reports kB here.
		if len(fields) < 2 {
			return 0
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || kb <= 0 {
			return 0
		}
		return kb * 1024
	}
	return 0
}
