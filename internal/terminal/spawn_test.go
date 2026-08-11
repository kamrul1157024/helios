package terminal

import (
	"slices"
	"testing"
)

// Empty argv is the resume path: the host picks the agent and its --resume
// flag itself, so nothing is passed down.
func TestHostArgs_NoCommandMeansResume(t *testing.T) {
	got := hostArgs("sess-1", "/work", nil)

	want := []string{"ptyhost", "sess-1", "--cwd", "/work"}
	if !slices.Equal(got, want) {
		t.Errorf("args = %q, want %q", got, want)
	}
}

func TestHostArgs_CommandBecomesOneFlagPerWord(t *testing.T) {
	got := hostArgs("sess-1", "/work", []string{"/bin/claude", "--session-id", "sess-1"})

	want := []string{
		"ptyhost", "sess-1", "--cwd", "/work",
		"--cmd", "/bin/claude", "--arg", "--session-id", "--arg", "sess-1",
	}
	if !slices.Equal(got, want) {
		t.Errorf("args = %q, want %q", got, want)
	}
}

// One flag per word is what makes quoting unnecessary: a prompt keeps its
// spaces, quotes and backticks all the way to the agent.
func TestHostArgs_ArgumentsSurviveWhole(t *testing.T) {
	prompt := "fix `git log` and say \"hi\", it's $HOME"

	got := hostArgs("sess-1", "", []string{"claude", prompt})

	want := []string{"ptyhost", "sess-1", "--cmd", "claude", "--arg", prompt}
	if !slices.Equal(got, want) {
		t.Errorf("args = %q, want %q", got, want)
	}
}
