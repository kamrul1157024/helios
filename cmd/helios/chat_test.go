package main

import (
	"testing"

	"github.com/kamrul1157024/helios/internal/terminal"
)

// `helios chat post api-redesign "the response shape is {items, next} now"`
// is the shape the joining prompt tells every member to type, so the channel
// and the message have to survive being positional.
func TestTheChannelAndTheMessageAreWhatIsLeftOver(t *testing.T) {
	opts := chatFlags([]string{"api-redesign", "the shape changed", "--urgent"})

	if len(opts.rest) != 2 || opts.rest[0] != "api-redesign" {
		t.Fatalf("rest = %v, want the channel then the message", opts.rest)
	}
	if opts.rest[1] != "the shape changed" {
		t.Errorf("message = %q", opts.rest[1])
	}
	if !opts.urgent {
		t.Error("--urgent was not read")
	}
}

// A message that starts with a dash is a message, not a flag somebody got
// wrong — but only --message can say so.
func TestMessageFlagBeatsPosition(t *testing.T) {
	opts := chatFlags([]string{"api-redesign", "--message", "--rebase is fine now"})
	if opts.message != "--rebase is fine now" {
		t.Errorf("message = %q", opts.message)
	}
}

func TestMembersAreSplitAndTrimmed(t *testing.T) {
	opts := chatFlags([]string{"--with", "s2, s7 ,,s9"})
	if len(opts.with) != 3 || opts.with[0] != "s2" || opts.with[2] != "s9" {
		t.Errorf("with = %v, want three ids and no blanks", opts.with)
	}
}

// The environment is the only thing that says which session a command is
// running in, and getting it wrong files the message against another agent —
// or sends a session its own message back as a prompt.
func TestASessionSignsWithItself(t *testing.T) {
	t.Setenv(terminal.SessionEnv, "sess-2")
	if got := chatAuthor(""); got != "session:sess-2" {
		t.Errorf("chatAuthor = %q", got)
	}
	// Named explicitly, it wins: one session may act for another.
	if got := chatAuthor("sess-9"); got != "session:sess-9" {
		t.Errorf("chatAuthor(sess-9) = %q", got)
	}
}

// Run from a person's own shell there is no session, and the daemon files the
// message as the user — which is what decides it goes out in full.
func TestAPersonSignsAsNobody(t *testing.T) {
	t.Setenv(terminal.SessionEnv, "")
	if got := chatAuthor(""); got != "" {
		t.Errorf("chatAuthor = %q, want empty so the daemon calls it the user", got)
	}
}

func TestTheReaderIsTheSessionYouAreIn(t *testing.T) {
	t.Setenv(terminal.SessionEnv, "sess-2")
	if got := chatFlags(nil).reader().Get("session"); got != "sess-2" {
		t.Errorf("reader = %q", got)
	}
	if got := chatFlags([]string{"--session", "sess-9"}).reader().Get("session"); got != "sess-9" {
		t.Errorf("reader with --session = %q", got)
	}
}

// An unnamed channel has no name to print, and `helios chat read ch_8f21a0`
// has to be a command somebody can run.
func TestAnUnnamedChannelIsLabelledByItsID(t *testing.T) {
	if got := chatLabel(wireChannel{ID: "ch_8f21a0"}); got != "ch_8f21a0" {
		t.Errorf("label = %q", got)
	}
	if got := chatLabel(wireChannel{ID: "ch_8f21a0", Name: "api-redesign"}); got != "api-redesign" {
		t.Errorf("label = %q", got)
	}
}
