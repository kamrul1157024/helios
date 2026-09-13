package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kamrul1157024/helios/internal/store"
)

/*
The whole path, once: HTTP in, a real store, and a backend that records what
each member was typed.

The unit tests next door check the wording of what goes out. What they cannot
check is the part that decides who gets anything at all — the author rule, the
joining prompt firing exactly once per member, and the receipt that makes
`--since-last` mean something. All three are only visible from outside.
*/

// liveChannelTest gives every named session a socket and an agent that answers
// the moment it is typed into, so a fan-out is measured rather than waited on.
func liveChannelTest(t *testing.T, sessions ...string) (*Shared, *sendBackend) {
	t.Helper()
	_, shared, be := newSendTest(t)

	for _, id := range sessions {
		seedSessionWithStatus(t, shared.DB, id, "idle")
		be.handles[id] = "sock-" + id
	}
	be.onSend = func() {
		for _, id := range sessions {
			shared.Signals.Fire(SignalPromptSubmitted, id)
		}
	}
	return shared, be
}

func channelCall(t *testing.T, sh *Shared, method, path, body string) map[string]any {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, "/internal/channels"+path, reader)
	rec := httptest.NewRecorder()

	sh.channelRoute(rec, req, "/internal/channels")

	if rec.Code >= 400 {
		t.Fatalf("%s %s = %d: %s", method, path, rec.Code, rec.Body.String())
	}
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}

func madeChannel(t *testing.T, out map[string]any) string {
	t.Helper()
	channel, _ := out["channel"].(map[string]any)
	id, _ := channel["id"].(string)
	if id == "" {
		t.Fatalf("no channel id in %v", out)
	}
	return id
}

func countContaining(texts []string, want string) int {
	n := 0
	for _, text := range texts {
		if strings.Contains(text, want) {
			n++
		}
	}
	return n
}

// Creating a channel with a message is the common case, and it must not cost
// the members two prompts: the joining prompt carries the message inside it.
func TestCreatingAChannelTellsEachMemberOnce(t *testing.T) {
	shared, be := liveChannelTest(t, "s2", "s7")

	channelCall(t, shared, http.MethodPost, "",
		`{"name":"api-redesign","members":["s2","s7"],"message":"align on {items, next}"}`)

	sent := be.sentTexts()
	if len(sent) != 2 {
		t.Fatalf("sent %d prompts for two members: %v", len(sent), sent)
	}
	if got := countContaining(sent, "You are now in the group chat"); got != 2 {
		t.Errorf("%d joining prompts, want one each", got)
	}
	if got := countContaining(sent, "align on {items, next}"); got != 2 {
		t.Errorf("%d members were told the opening message, want both", got)
	}
}

// A session that posts reads its own words in the transcript it typed them
// into. Sending them back would be the same sentence twice.
func TestAPostGoesToEveryoneButItsAuthor(t *testing.T) {
	shared, be := liveChannelTest(t, "s2", "s7")
	id := madeChannel(t, channelCall(t, shared, http.MethodPost, "",
		`{"name":"api-redesign","members":["s2","s7"]}`))

	be.sent = nil
	channelCall(t, shared, http.MethodPost, "/"+id+"/messages",
		`{"author":"session:s2","message":"the response shape changed"}`)

	sent := be.sentTexts()
	if len(sent) != 1 {
		t.Fatalf("sent %d prompts, want only the one that did not write it: %v", len(sent), sent)
	}
	if !strings.Contains(sent[0], "New message in") {
		t.Errorf("a session's message should arrive as a snippet: %q", sent[0])
	}
}

// The person is not a member and gets nothing typed at them, but everyone else
// does — in full, because a person writing to a channel expects to be acted on.
func TestAPersonsPostGoesToEveryMember(t *testing.T) {
	shared, be := liveChannelTest(t, "s2", "s7")
	id := madeChannel(t, channelCall(t, shared, http.MethodPost, "",
		`{"name":"api-redesign","members":["s2","s7"]}`))

	be.sent = nil
	channelCall(t, shared, http.MethodPost, "/"+id+"/messages",
		`{"message":"does the client cope with it?"}`)

	sent := be.sentTexts()
	if len(sent) != 2 {
		t.Fatalf("sent %d prompts, want both members: %v", len(sent), sent)
	}
	if got := countContaining(sent, "does the client cope with it?"); got != 2 {
		t.Errorf("%d members got the message whole, want both", got)
	}
}

// Two agents adding the same session is a race, not a mistake, and the second
// one must not send it a second joining prompt.
func TestJoiningTwiceIsJoiningOnce(t *testing.T) {
	shared, be := liveChannelTest(t, "s2", "s9")
	id := madeChannel(t, channelCall(t, shared, http.MethodPost, "",
		`{"name":"api-redesign","members":["s2"]}`))

	be.sent = nil
	first := channelCall(t, shared, http.MethodPost, "/"+id+"/members", `{"session":"s9"}`)
	second := channelCall(t, shared, http.MethodPost, "/"+id+"/members", `{"session":"s9"}`)

	if first["added"] != true || second["added"] != false {
		t.Errorf("added = %v then %v, want true then false", first["added"], second["added"])
	}
	if got := countContaining(be.sentTexts(), "You are now in the group chat"); got != 1 {
		t.Errorf("%d joining prompts for one session", got)
	}
}

// `--since-last` is the whole point of the receipt: "something happened, show
// me what I missed" has to be one command with no bookkeeping.
func TestSinceLastShowsOnlyWhatArrivedAfter(t *testing.T) {
	shared, _ := liveChannelTest(t, "s2")
	id := madeChannel(t, channelCall(t, shared, http.MethodPost, "",
		`{"name":"api-redesign","members":["s2"]}`))

	channelCall(t, shared, http.MethodPost, "/"+id+"/messages", `{"message":"before"}`)
	// Reading marks it read, which is what moves the receipt.
	channelCall(t, shared, http.MethodGet, "/"+id+"/messages?session=s2", "")
	channelCall(t, shared, http.MethodPost, "/"+id+"/messages", `{"message":"after"}`)

	out := channelCall(t, shared, http.MethodGet, "/"+id+"/messages?session=s2&since_last=1", "")
	messages, _ := out["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("since the receipt: %d messages, want only the later one", len(messages))
	}
	first, _ := messages[0].(map[string]any)
	if first["body"] != "after" {
		t.Errorf("got %q, want \"after\"", first["body"])
	}
}

// An unread count that includes what you wrote yourself is a badge that never
// clears.
func TestTheListCountsWhatSomebodyElseSaid(t *testing.T) {
	shared, _ := liveChannelTest(t, "s2")
	id := madeChannel(t, channelCall(t, shared, http.MethodPost, "",
		`{"name":"api-redesign","members":["s2"]}`))

	channelCall(t, shared, http.MethodPost, "/"+id+"/messages", `{"message":"from the person"}`)
	channelCall(t, shared, http.MethodPost, "/"+id+"/messages",
		`{"author":"session:s2","message":"from the session"}`)

	out := channelCall(t, shared, http.MethodGet, "?session=s2", "")
	channels, _ := out["channels"].([]any)
	for _, one := range channels {
		ch, _ := one.(map[string]any)
		if ch["id"] != id {
			continue
		}
		if unread, _ := ch["unread"].(float64); unread != 1 {
			t.Errorf("unread = %v, want 1 — s2 wrote the other one", ch["unread"])
		}
		return
	}
	t.Fatalf("the channel was not in the list: %v", out)
}

// The notice board is not somebody's to delete, and the list must always have
// it: an agent told to post to #general has to find it there.
func TestGeneralIsInTheListAndStays(t *testing.T) {
	shared, _ := liveChannelTest(t)

	out := channelCall(t, shared, http.MethodGet, "", "")
	channels, _ := out["channels"].([]any)
	if len(channels) != 1 {
		t.Fatalf("got %d channels on an empty daemon, want general: %v", len(channels), out)
	}
	first, _ := channels[0].(map[string]any)
	if first["id"] != store.GeneralChannel {
		t.Errorf("first channel is %v, want general", first["id"])
	}

	req := httptest.NewRequest(http.MethodDelete, "/internal/channels/"+store.GeneralChannel, nil)
	rec := httptest.NewRecorder()
	shared.channelRoute(rec, req, "/internal/channels")
	if rec.Code < 400 {
		t.Errorf("deleting general answered %d, want a refusal", rec.Code)
	}
}
