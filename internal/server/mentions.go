package server

import (
	"regexp"
	"strings"

	"github.com/kamrul1157024/helios/internal/store"
)

/*
Who a message named.

A session has no short name of its own. Its title is generated, long, and cut to
48 characters where it crosses the wire — "[INFRA] Debug SSH authentication
kamul-dev integ…" — so it cannot be typed, and its id is a UUID, which can be
typed but not remembered. So a channel hands out a handle per member: a slug off
the title where that names exactly one of them, and the head of the id where it
does not.

Resolution happens once, when the message is posted, and the readers it produces
are stored. Nothing downstream re-derives them. Two parsers that disagreed —
this one for delivery and the renderer's for decoration — would eventually show
a message addressed to somebody it never woke, which is worse than having no
mentions at all.
*/

// How much of a session id stands in for the whole of it. Short enough to type,
// long enough that two sessions colliding is not something to plan around.
const handleLength = 8

// The shortest slug worth offering. Below this the title has said nothing —
// "[INFRA] Go" would give "go", which names a language rather than a session.
const minSlugLength = 3

// How many words of the title a slug keeps. Two is enough to separate the
// sessions on one machine and short enough to type without looking.
const slugWords = 2

// A leading "[TAG]", which several titles carry and none are distinguished by.
var titleTag = regexp.MustCompile(`^\s*\[[^\]]*\]\s*`)

// Words that carry no meaning in a handle. Without this "Port the client"
// becomes @port-the, which names the session after a word every other title
// also has — the two slots are worth spending on the words that differ.
var filler = map[string]bool{
	"a": true, "an": true, "and": true, "for": true, "from": true, "in": true,
	"of": true, "on": true, "or": true, "the": true, "to": true, "with": true,
}

// A mention is @ followed by a run of the characters a slug or an id can hold.
// The trailing punctuation of a sentence is left out of the token: "@s2, are
// you there" names s2, not "s2,".
var mentionToken = regexp.MustCompile(`@([A-Za-z0-9][A-Za-z0-9_-]*)`)

// Fenced blocks and inline spans, so an @ inside a snippet is not an address.
var (
	fencedCode = regexp.MustCompile("(?s)```.*?```")
	inlineCode = regexp.MustCompile("`[^`\n]*`")
)

/*
slugFor is the readable handle for a title, or "" when the title has nothing
worth using in it.

The leading tag goes because it is shared, the words are lowercased and joined,
and anything that is not a letter or a digit becomes a separator — a handle that
needs quoting is not a handle.
*/
func slugFor(title string) string {
	cleaned := titleTag.ReplaceAllString(title, "")
	all := strings.FieldsFunc(strings.ToLower(cleaned), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	})

	words := []string{}
	for _, word := range all {
		if filler[word] {
			continue
		}
		words = append(words, word)
		if len(words) == slugWords {
			break
		}
	}
	slug := strings.Join(words, "-")
	if len(slug) < minSlugLength {
		return ""
	}
	return slug
}

/*
slugsFor is the handle each member of a channel answers to.

Unique within the channel, which is the only scope a mention is resolved in. Two
members whose titles reduce to the same slug both fall back to their id, rather
than one of them quietly taking the name: a handle that sometimes means the
other session is worse than one that is ugly.
*/
func slugsFor(members []string, titles map[string]string) map[string]string {
	taken := map[string]int{}
	for _, member := range members {
		if slug := slugFor(titles[member]); slug != "" {
			taken[slug]++
		}
	}

	out := map[string]string{}
	for _, member := range members {
		slug := slugFor(titles[member])
		if slug == "" || taken[slug] > 1 {
			out[member] = handleFor(member)
			continue
		}
		out[member] = slug
	}
	return out
}

// handleFor is the fallback: the head of the session id.
func handleFor(sessionID string) string {
	if len(sessionID) <= handleLength {
		return sessionID
	}
	return sessionID[:handleLength]
}

// parseMentions is the @tokens in a body, in the order written and without
// repeats. Code is stripped first: `@param` in a snippet addresses nobody.
func parseMentions(body string) []string {
	prose := inlineCode.ReplaceAllString(fencedCode.ReplaceAllString(body, " "), " ")

	seen := map[string]bool{}
	out := []string{}
	for _, match := range mentionToken.FindAllStringSubmatch(prose, -1) {
		token := strings.ToLower(match[1])
		if seen[token] {
			continue
		}
		seen[token] = true
		out = append(out, token)
	}
	return out
}

/*
resolveMentions turns the tokens in a body into the readers they name.

A token is matched against the handles first, then against the head of a session
id — so a handle stays usable after a title changes under it, and an id always
works. A token naming nobody is dropped rather than guessed at: waking the wrong
agent is worse than waking none.

The result is in author form — "session:<id>", or "user" for the person —
because that is what a reader is everywhere else: receipts, unread counts and
the author column all speak it, and a mention recorded as a bare id would be
counted against a reader that never matches.
*/
func resolveMentions(tokens, members []string, slugs map[string]string) []string {
	byHandle := map[string]string{}
	for _, member := range members {
		if slug, ok := slugs[member]; ok {
			byHandle[slug] = member
		}
		byHandle[handleFor(member)] = member
	}

	seen := map[string]bool{}
	out := []string{}
	add := func(reader string) {
		if seen[reader] {
			return
		}
		seen[reader] = true
		out = append(out, reader)
	}

	for _, token := range tokens {
		if token == store.AuthorUser {
			add(store.AuthorUser)
			continue
		}
		if member, ok := byHandle[token]; ok {
			add(store.SessionAuthor(member))
		}
	}
	return out
}
