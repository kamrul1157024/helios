package terminal

import (
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// hostStartTimeout matches the wait the agent path uses: a shell that has not
// bound its socket by then is not coming up.
const hostStartTimeout = 15 * time.Second

// sidecarTimeout covers the gap between a host binding its socket and writing
// the file that names its pid. Short: the two happen in consecutive statements.
const sidecarTimeout = 2 * time.Second

// waitForSidecar blocks until a host's sidecar names a pid, or the timeout
// passes. A miss is not fatal — Evict re-reads the sidecar before giving up —
// so the caller has nothing to decide on the result.
func waitForSidecar(heliosDir, id string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s, err := ReadSidecar(SidecarPath(heliosDir, id)); err == nil && s.PID > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// shellMarker separates a session from the shell it owns. A terminal id is
// either a session id, naming that session's agent, or a session id followed
// by this and an index, naming one of the shells the user opened beside it.
//
// The structure lives in the id rather than in a table because everything
// downstream — the socket name, the sidecar, the restart adoption, the
// websocket relay — already treats the id as opaque. Encoding the parent here
// is what let all of that carry shells without being told about them.
const shellMarker = ":sh"

// ShellID names the nth shell of a session.
func ShellID(parent string, n int) string {
	return parent + shellMarker + strconv.Itoa(n)
}

// SplitID reports which session a terminal belongs to, and whether it is a
// shell rather than that session's agent.
func SplitID(id string) (parent string, shell bool) {
	i := strings.LastIndex(id, shellMarker)
	if i <= 0 {
		return id, false
	}
	if _, err := strconv.Atoi(id[i+len(shellMarker):]); err != nil {
		return id, false
	}
	return id[:i], true
}

// IsShell reports whether an id names a user shell.
func IsShell(id string) bool {
	_, shell := SplitID(id)
	return shell
}

// shellIndex is the number in a shell id, or 0 for anything else.
func shellIndex(id string) int {
	i := strings.LastIndex(id, shellMarker)
	if i <= 0 {
		return 0
	}
	n, err := strconv.Atoi(id[i+len(shellMarker):])
	if err != nil {
		return 0
	}
	return n
}

// LoginShellArgv is the command a user shell runs: their own login shell, so
// the prompt, aliases and PATH are the ones they have in a terminal of their
// own. Not the agent's command, and not a bare sh.
func LoginShellArgv() []string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	return []string{shell, "-l"}
}

// Terminal describes one live host: a session's agent, or a shell beside it.
type Terminal struct {
	ID     string `json:"id"`
	Parent string `json:"parent"`
	Kind   string `json:"kind"` // "agent" or "shell"
	Socket string `json:"socket"`
	Cwd    string `json:"cwd"`
	PID    int    `json:"pid"`
}

// StartShell opens a login shell for a session and returns the terminal it
// runs in. The session's own host is untouched: a shell is another process
// beside it, and killing one has nothing to do with the other.
func (r *Registry) StartShell(parent, cwd string) (Terminal, error) {
	if r.spawn == nil {
		return Terminal{}, fmt.Errorf("terminal: no spawn function configured")
	}

	id := r.nextShellID(parent)
	sock := SocketPath(r.heliosDir, id)
	RemoveHostFiles(r.heliosDir, id)

	if err := r.spawn(id, cwd, LoginShellArgv()); err != nil {
		return Terminal{}, fmt.Errorf("spawn shell for %s: %w", parent, err)
	}
	if !WaitForSocket(sock, hostStartTimeout) {
		return Terminal{}, fmt.Errorf("shell for %s did not come up", parent)
	}
	// The host binds its socket before it writes its sidecar, and the sidecar
	// is the only record of its pid. Adopting in that gap stores pid 0, and a
	// host with no pid is one that closing the tab cannot kill: the shell runs
	// on with no socket to reach it by and no entry to list it from.
	waitForSidecar(r.heliosDir, id, sidecarTimeout)
	r.adopt(id, sock, cwd)

	return Terminal{ID: id, Parent: parent, Kind: "shell", Socket: sock, Cwd: cwd, PID: r.pidOf(id)}, nil
}

// Terminals lists a session's live hosts, its agent first and its shells in
// the order they were opened.
func (r *Registry) Terminals(parent string) []Terminal {
	r.mu.Lock()
	defer r.mu.Unlock()

	var out []Terminal
	for id, e := range r.entries {
		owner, shell := SplitID(id)
		if owner != parent {
			continue
		}
		kind := "agent"
		if shell {
			kind = "shell"
		}
		out = append(out, Terminal{ID: id, Parent: owner, Kind: kind, Socket: e.socket, Cwd: e.cwd, PID: e.pid})
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Kind == "agent") != (out[j].Kind == "agent") {
			return out[i].Kind == "agent"
		}
		return shellIndex(out[i].ID) < shellIndex(out[j].ID)
	})
	return out
}

// KillShells shuts down every shell a session owns, for when the session
// itself is being deleted. The agent host is left alone: it has its own
// lifecycle, and this is not it.
func (r *Registry) KillShells(parent string) {
	for _, t := range r.Terminals(parent) {
		if t.Kind != "shell" {
			continue
		}
		if err := r.Evict(t.ID); err != nil {
			log.Printf("terminal: kill shell %s: %v", t.ID, err)
		}
	}
}

// nextShellID picks the lowest index this session is not already using, so
// closing shell 1 of three leaves the next one called 1 rather than 4.
func (r *Registry) nextShellID(parent string) string {
	r.mu.Lock()
	taken := make(map[int]bool)
	for id := range r.entries {
		if owner, shell := SplitID(id); shell && owner == parent {
			taken[shellIndex(id)] = true
		}
	}
	r.mu.Unlock()

	for n := 1; ; n++ {
		if !taken[n] {
			return ShellID(parent, n)
		}
	}
}

func (r *Registry) pidOf(id string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.entries[id]; ok {
		return e.pid
	}
	return 0
}
