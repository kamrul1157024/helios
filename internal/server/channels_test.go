package server

import (
	"strings"
	"testing"

	"github.com/kamrul1157024/helios/internal/store"
)

func testChannel(name string) *store.Channel {
	return &store.Channel{ID: "ch_abc123", Name: name, Members: []string{"s2", "s7"}}
}

// A person writing to a channel is addressing the agents and expects them to
// act on it, so what they wrote is what arrives.
func TestAPersonsMessageArrivesWhole(t *testing.T) {
	ch := testChannel("api-redesign")
	body := strings.Repeat("x", 300)
	got := deliverable("user", ch, &store.ChannelMessage{Author: store.AuthorUser, Body: body})

	if !strings.Contains(got, body) {
		t.Error("the message was cut; a person's message is not a snippet")
	}
	if strings.Contains(got, "chat read") {
		t.Error("nothing to go and fetch, so nothing should tell the agent to")
	}
	if !strings.Contains(got, "api-redesign") {
		t.Error("the prompt should say which channel it came from")
	}
}

// Four copies of one agent's paragraph in four context windows is the cost the
// snippet exists to avoid.
func TestASessionsMessageArrivesAsASnippet(t *testing.T) {
	ch := testChannel("api-redesign")
	body := strings.Repeat("y", 500)
	got := deliverable("Split the orders migration", ch, &store.ChannelMessage{
		Author: store.SessionAuthor("s2"), Body: body,
	})

	if len(got) > 400 {
		t.Errorf("snippet is %d characters; the point is that it is short", len(got))
	}
	if strings.Contains(got, body) {
		t.Error("the whole message went out")
	}
	if !strings.Contains(got, "Split the orders migration") {
		t.Error("the snippet must say who it is from, by title")
	}
	if !strings.Contains(got, "helios chat read api-redesign --since-last") {
		t.Error("the snippet must say how to read the rest")
	}
}

func TestAnUnnamedChannelIsNamedByItsID(t *testing.T) {
	ch := testChannel("")
	got := deliverable("user", ch, &store.ChannelMessage{Author: store.AuthorUser, Body: "hello"})
	if !strings.Contains(got, "ch_abc123") {
		t.Errorf("a channel with no name should be referred to by id: %q", got)
	}
}

func TestUrgentSaysSo(t *testing.T) {
	ch := testChannel("api-redesign")
	got := deliverable("user", ch, &store.ChannelMessage{
		Author: store.AuthorUser, Body: "stop, the migration is wrong", Urgent: true,
	})
	if !strings.Contains(got, "urgent") {
		t.Errorf("an interrupted agent should be told why: %q", got)
	}
}

// A pasted design document is not a prompt.
func TestAVeryLongMessageIsCutWithSomewhereToGo(t *testing.T) {
	ch := testChannel("api-redesign")
	got := deliverable("user", ch, &store.ChannelMessage{
		Author: store.AuthorUser, Body: strings.Repeat("z", maxUserMessage+500),
	})
	if len(got) > maxUserMessage+300 {
		t.Errorf("length %d, want it cut near %d", len(got), maxUserMessage)
	}
	if !strings.Contains(got, "chat read") {
		t.Error("a cut message must say where the rest is")
	}
}

func TestTheJoiningPromptSaysWhatAndWho(t *testing.T) {
	got := joiningPrompt("api-redesign", []string{`"Port the client"`, "user"}, "", 0)

	for _, want := range []string{
		"group chat \"api-redesign\"",
		`"Port the client"`,
		"user",
		"helios chat read api-redesign --since-last",
		"helios chat post api-redesign",
		"Do not narrate",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the joining prompt is missing %q:\n%s", want, got)
		}
	}
}

// Creating a channel with a message is the common case: making the members run
// a command to find out what it was would be a round trip for nothing.
func TestTheJoiningPromptCarriesTheOpeningMessage(t *testing.T) {
	got := joiningPrompt("api-redesign", []string{"user"}, "align on {items, next}", 0)
	if !strings.Contains(got, "user: align on {items, next}") {
		t.Errorf("the opening message should be in the joining prompt:\n%s", got)
	}
}

// Fourteen messages pasted into a newcomer's first prompt is the bill this
// design exists to avoid.
func TestALateJoinerIsToldTheCountNotTheThread(t *testing.T) {
	got := joiningPrompt("api-redesign", []string{"user"}, "", 14)
	if !strings.Contains(got, "14 messages") {
		t.Errorf("want the count of what was missed:\n%s", got)
	}
	if !strings.Contains(got, "helios chat read api-redesign") {
		t.Error("want the command that would show it")
	}
}

func TestAnOpeningMessageBeatsTheMissedCount(t *testing.T) {
	// A channel created with a message has exactly one message in it — its
	// own — and saying "1 message you missed" under it would be nonsense.
	got := joiningPrompt("api-redesign", []string{"user"}, "the opening line", 1)
	if strings.Contains(got, "before you joined") {
		t.Errorf("the opening message is not something the newcomer missed:\n%s", got)
	}
}
