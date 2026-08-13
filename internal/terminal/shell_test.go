package terminal

import (
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

// A shell holds a scrollback and whatever the user was running. An agent
// evicted for room comes back with --resume; a shell has nothing to come back
// from, so the pool must take the room from somewhere else.
func TestEvictForRoomNeverTakesAShell(t *testing.T) {
	e := newRegistryEnv(t)
	e.reg.MaxWarm = 2
	e.add(ShellID("sess-1", 1), 5*time.Hour) // oldest, and the obvious victim
	e.add("sess-1", time.Hour)
	e.add("sess-2", 2*time.Hour)

	e.reg.Sweep()

	if warm := e.warm(); !warm[ShellID("sess-1", 1)] {
		t.Errorf("warm = %v, want the shell kept", warm)
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
