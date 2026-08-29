package terminal

import (
	"os/exec"
	"slices"
	"testing"
	"time"
)

func TestSplitID(t *testing.T) {
	tests := []struct {
		id     string
		parent string
		shell  bool
	}{
		{"sess-1", "sess-1", false},
		{"sess-1:sh1", "sess-1", true},
		{"sess-1:sh12", "sess-1", true},
		// A uuid contains no marker, and a session whose id happens to end in
		// something marker-shaped but unnumbered is still an agent.
		{"7d4c-11ee-8c90", "7d4c-11ee-8c90", false},
		{"sess-1:shell", "sess-1:shell", false},
		{":sh1", ":sh1", false},
	}

	for _, tt := range tests {
		parent, shell := SplitID(tt.id)
		if parent != tt.parent || shell != tt.shell {
			t.Errorf("SplitID(%q) = (%q, %v), want (%q, %v)", tt.id, parent, shell, tt.parent, tt.shell)
		}
	}
}

func TestShellIDRoundTrips(t *testing.T) {
	id := ShellID("sess-1", 3)
	if id != "sess-1:sh3" {
		t.Fatalf("ShellID = %q", id)
	}
	if parent, shell := SplitID(id); parent != "sess-1" || !shell {
		t.Errorf("SplitID(%q) = (%q, %v)", id, parent, shell)
	}
}

// Closing the middle shell of three should not push the next one to 4: the
// index is a label the user reads, not a counter.
func TestNextShellIDFillsTheGap(t *testing.T) {
	e := newRegistryEnv(t)
	e.add("sess-1", 0)
	e.add(ShellID("sess-1", 1), 0)
	e.add(ShellID("sess-1", 2), 0)

	if got := e.reg.nextShellID("sess-1"); got != "sess-1:sh3" {
		t.Errorf("nextShellID = %q, want sess-1:sh3", got)
	}

	if err := e.reg.Evict(ShellID("sess-1", 1)); err != nil {
		t.Fatalf("evict: %v", err)
	}
	if got := e.reg.nextShellID("sess-1"); got != "sess-1:sh1" {
		t.Errorf("nextShellID after a close = %q, want sess-1:sh1", got)
	}
}

func TestTerminalsListsTheAgentFirst(t *testing.T) {
	e := newRegistryEnv(t)
	e.add("sess-1", 0)
	e.add(ShellID("sess-1", 2), 0)
	e.add(ShellID("sess-1", 1), 0)
	e.add("sess-2", 0)
	e.add(ShellID("sess-2", 1), 0)

	got := e.reg.Terminals("sess-1")
	ids := make([]string, 0, len(got))
	for _, term := range got {
		ids = append(ids, term.ID)
	}
	if !slices.Equal(ids, []string{"sess-1", "sess-1:sh1", "sess-1:sh2"}) {
		t.Errorf("terminals = %v, want the agent then its shells in order", ids)
	}
	if got[0].Kind != "agent" || got[1].Kind != "shell" {
		t.Errorf("kinds = %q, %q", got[0].Kind, got[1].Kind)
	}
}

func TestKillShellsLeavesTheAgent(t *testing.T) {
	e := newRegistryEnv(t)
	e.add("sess-1", 0)
	e.add(ShellID("sess-1", 1), 0)
	e.add(ShellID("sess-1", 2), 0)

	e.reg.KillShells("sess-1")

	warm := e.warm()
	if !warm["sess-1"] {
		t.Error("the agent should have survived its session's shells being killed")
	}
	if warm[ShellID("sess-1", 1)] || warm[ShellID("sess-1", 2)] {
		t.Errorf("warm = %v, want no shells", warm)
	}
}

// A host adopted before its sidecar landed has pid 0 on record. Evict has to
// go back to the sidecar for it, because it is about to delete that file:
// without the pid the process survives with no socket to reach it by and no
// entry to list it from, which is how closing a shell tab leaves a shell
// running until the machine stops.
func TestEvictFindsThePidInTheSidecar(t *testing.T) {
	e := newRegistryEnv(t)
	id := ShellID("sess-1", 1)
	e.add(id, 0)

	// A sleeping process stands in for the host, so the test can tell whether
	// Evict actually found a pid to signal. Its command line has to name the
	// session: Evict signals a recorded pid only once it has confirmed the pid
	// still belongs to that host, since pids are recycled.
	victim := exec.Command("/bin/sh")
	// Two commands, so sh does not exec-replace itself with sleep and drop
	// the argv this check reads.
	victim.Args = []string{"helios", "-c", "sleep 30; :", "ptyhost", id}
	if err := victim.Start(); err != nil {
		t.Fatalf("start victim: %v", err)
	}
	for i := 0; i < 50 && !IsHostProcess(victim.Process.Pid, id); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	t.Cleanup(func() { victim.Process.Kill() })

	sidecar := Sidecar{SessionID: id, PID: victim.Process.Pid, Socket: SocketPath(e.dir, id)}
	if err := WriteSidecar(e.dir, sidecar); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
	e.reg.mu.Lock()
	e.reg.entries[id].pid = 0 // what adopting before the sidecar leaves behind
	e.reg.mu.Unlock()

	if err := e.reg.Evict(id); err != nil {
		t.Fatalf("evict: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- victim.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("the process outlived its eviction: Evict never found its pid")
	}
}

func TestLoginShellArgvUsesTheUsersShell(t *testing.T) {
	t.Setenv("SHELL", "/bin/fish")
	if got := LoginShellArgv(); !slices.Equal(got, []string{"/bin/fish", "-l"}) {
		t.Errorf("argv = %v", got)
	}

	t.Setenv("SHELL", "")
	if got := LoginShellArgv(); !slices.Equal(got, []string{"/bin/sh", "-l"}) {
		t.Errorf("argv without SHELL = %v", got)
	}
}
