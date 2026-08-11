package tui

import (
	"strings"
	"testing"
)

func collect(stream string) []sessSSEUpdate {
	var got []sessSSEUpdate
	readSessionEvents(strings.NewReader(stream), func(u sessSSEUpdate) { got = append(got, u) })
	return got
}

// The daemon puts the type on the `event:` line and the bare update on `data:`.
// Parsing the payload as a typed envelope matches nothing, which silently
// leaves the list refreshing only on its poll tick.
func TestReadSessionEvents_ReadsTheDaemonWireFormat(t *testing.T) {
	got := collect(": connected\n\n" +
		"event: session_status\ndata: {\"session_id\":\"sess-1\",\"status\":\"active\"}\n\n")

	if len(got) != 1 {
		t.Fatalf("got %d updates, want 1: %+v", len(got), got)
	}
	if got[0].SessionID != "sess-1" || got[0].Status != "active" {
		t.Errorf("update = %+v, want sess-1 active", got[0])
	}
}

func TestReadSessionEvents_CarriesTheWholeUpdate(t *testing.T) {
	got := collect("event: session_status\n" +
		`data: {"session_id":"sess-1","status":"idle","last_user_message":"hi","cwd":"/tmp/proj"}` + "\n\n")

	if len(got) != 1 {
		t.Fatalf("got %d updates, want 1", len(got))
	}
	if got[0].LastUserMessage != "hi" || got[0].CWD != "/tmp/proj" {
		t.Errorf("update = %+v, want the message and cwd carried", got[0])
	}
}

// Other event types share the stream, and a heartbeat arrives as a bare
// comment: neither should be mistaken for a status update.
func TestReadSessionEvents_IgnoresOtherEventsAndHeartbeats(t *testing.T) {
	got := collect(": heartbeat\n\n" +
		"event: notification\ndata: {\"session_id\":\"sess-9\"}\n\n" +
		"event: narration\ndata: {\"text\":\"hello\"}\n\n")

	if len(got) != 0 {
		t.Errorf("got %+v, want nothing", got)
	}
}

// A frame ends at the blank line, so the type must not leak into the next one.
func TestReadSessionEvents_TypeDoesNotLeakAcrossFrames(t *testing.T) {
	got := collect("event: session_status\ndata: {\"session_id\":\"sess-1\",\"status\":\"idle\"}\n\n" +
		"data: {\"session_id\":\"sess-2\",\"status\":\"active\"}\n\n")

	if len(got) != 1 {
		t.Fatalf("got %d updates, want only the typed one: %+v", len(got), got)
	}
	if got[0].SessionID != "sess-1" {
		t.Errorf("update = %+v, want sess-1", got[0])
	}
}

func TestReadSessionEvents_SkipsUndecodablePayloads(t *testing.T) {
	got := collect("event: session_status\ndata: not json\n\n" +
		"event: session_status\ndata: {\"session_id\":\"sess-2\",\"status\":\"idle\"}\n\n")

	if len(got) != 1 || got[0].SessionID != "sess-2" {
		t.Errorf("got %+v, want just sess-2", got)
	}
}
