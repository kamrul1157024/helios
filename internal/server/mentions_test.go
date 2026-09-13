package server

import (
	"strings"
	"testing"

	"github.com/kamrul1157024/helios/internal/store"
)

// The real titles from a live channel on this machine, truncation and all. A
// slug scheme that works on invented names and not on these is no use.
const (
	titleSSH       = "[INFRA] Debug SSH authentication kamul-dev integ…"
	titleRetention = "[INFRA] Check production log retention configura…"
	titleVM        = "[INFRA] Force stop and restart unresponsive VM"
)

func TestSlugDropsTheTagEveryTitleShares(t *testing.T) {
	// Three sessions all tagged [INFRA]: keeping it would make every handle
	// start with the one word that separates none of them.
	for title, want := range map[string]string{
		titleSSH:                     "debug-ssh",
		titleRetention:               "check-production",
		titleVM:                      "force-stop",
		"Port the client":            "port-client",
		"  [ui]  Fold a turn  ":      "fold-turn",
		"Split the orders migration": "split-orders",
	} {
		if got := slugFor(title); got != want {
			t.Errorf("slugFor(%q) = %q, want %q", title, got, want)
		}
	}
}

// A title with nothing usable in it has no handle, and the id stands in. A slug
// of "go" would name a language rather than a session.
func TestATitleWithNothingInItHasNoSlug(t *testing.T) {
	for _, title := range []string{"", "   ", "[INFRA]", "[INFRA] Go", "…"} {
		if got := slugFor(title); got != "" {
			t.Errorf("slugFor(%q) = %q, want no slug", title, got)
		}
	}
}

// Two sessions whose titles reduce to the same handle must both fall back. One
// of them quietly taking the name is how a mention wakes the wrong agent.
func TestACollidingSlugIsGivenToNeither(t *testing.T) {
	members := []string{"aaaa1111-2222", "bbbb3333-4444", "cccc5555-6666"}
	titles := map[string]string{
		members[0]: "[INFRA] Debug SSH authentication on one box",
		members[1]: "[INFRA] Debug SSH authentication on another",
		members[2]: "Port the client",
	}

	slugs := slugsFor(members, titles)
	if slugs[members[0]] == slugs[members[1]] {
		t.Fatalf("both took %q", slugs[members[0]])
	}
	for _, member := range members[:2] {
		if slugs[member] != handleFor(member) {
			t.Errorf("%s got %q, want its id after the collision", member, slugs[member])
		}
	}
	// The one that did not collide keeps its readable handle.
	if slugs[members[2]] != "port-client" {
		t.Errorf("uncontested slug = %q", slugs[members[2]])
	}
}

func TestMentionsAreReadInOrderAndOnlyOnce(t *testing.T) {
	got := parseMentions("@port-client and @orders, then @port-client again")
	if len(got) != 2 || got[0] != "port-client" || got[1] != "orders" {
		t.Errorf("tokens = %v, want each named session once, in the order written", got)
	}
	// The comma is punctuation, not part of the handle.
	if got[1] != "orders" {
		t.Errorf("trailing punctuation was kept: %q", got[1])
	}
}

// An @ inside a snippet is not an address. Agents paste code constantly, and a
// decorator that woke somebody for `@param` would make mentions untrustworthy.
func TestCodeIsNotAnAddress(t *testing.T) {
	body := "look at `@decorator` and\n\n```go\n// @author nobody\nfunc f() {}\n```\n\nbut @port-client should hear"

	got := parseMentions(body)
	if len(got) != 1 || got[0] != "port-client" {
		t.Errorf("tokens = %v, want only the one written in prose", got)
	}
}

func TestAnAddressResolvesByHandleOrById(t *testing.T) {
	members := []string{"aaaa1111-2222-3333", "bbbb4444-5555-6666"}
	titles := map[string]string{members[0]: "Port the client", members[1]: "Split the orders"}
	slugs := slugsFor(members, titles)

	// By handle, and in author form: that is what a reader is everywhere else.
	want := store.SessionAuthor(members[0])
	if got := resolveMentions([]string{"port-client"}, members, slugs); len(got) != 1 || got[0] != want {
		t.Errorf("by handle = %v, want %q", got, want)
	}
	// By the head of the id, which works whatever the title says.
	if got := resolveMentions([]string{"aaaa1111"}, members, slugs); len(got) != 1 || got[0] != want {
		t.Errorf("by id = %v", got)
	}
	// The person is a reader like any other.
	if got := resolveMentions([]string{store.AuthorUser}, members, slugs); len(got) != 1 ||
		got[0] != store.AuthorUser {
		t.Errorf("user = %v", got)
	}
}

// Waking the wrong agent is worse than waking none, so a token that names
// nobody is dropped rather than guessed at.
func TestAnAddressToNobodyWakesNobody(t *testing.T) {
	members := []string{"aaaa1111-2222"}
	slugs := slugsFor(members, map[string]string{members[0]: "Port the client"})

	if got := resolveMentions([]string{"nobody", "port"}, members, slugs); len(got) != 0 {
		t.Errorf("resolved %v, want nothing — neither names a member", got)
	}
}

func TestTheSameSessionNamedTwiceIsWokenOnce(t *testing.T) {
	members := []string{"aaaa1111-2222"}
	slugs := slugsFor(members, map[string]string{members[0]: "Port the client"})

	got := resolveMentions(parseMentions("@port-client and also @aaaa1111"), members, slugs)
	if len(got) != 1 {
		t.Errorf("resolved %v, want the one session once", got)
	}
}

// The handle has to survive being printed beside a name in a terminal.
func TestAHandleIsTypeable(t *testing.T) {
	for _, title := range []string{titleSSH, titleRetention, titleVM} {
		slug := slugFor(title)
		if strings.ContainsAny(slug, " \t[]()\"'`…") {
			t.Errorf("slugFor(%q) = %q, which needs quoting", title, slug)
		}
	}
}
