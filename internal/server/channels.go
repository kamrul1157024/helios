package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/kamrul1157024/helios/internal/store"
)

/*
Channels: several sessions and the person, with one conversation running
through them. See docs/specs/60-group-chat.md.

Delivery is SendPrompt and nothing else. A channel does not get its own idea of
what a prompt is: a member that is busy queues behind its turn, a cold one
wakes, and a terminated one answers 409 and is skipped. The only thing this
file decides is *what text* each member is sent.

Three kinds of text go out:

  - a joining prompt, once, when a session is added — it does not know channels
    exist until something says so;
  - a person's message, in full, because a person writing to a channel is
    addressing the agents and expects them to act;
  - a snippet of a session's message, because four copies of one agent's
    paragraph in four context windows is the cost this design exists to avoid.
*/

// How much of a session's message its members are shown before they have to
// ask for the rest.
const snippetLength = 200

// How much of a person's message goes out in full. A pasted design document is
// not a prompt.
const maxUserMessage = 4000

// How long a title may be where it names an author. Long enough to say what a
// session is for, short enough to leave room for the message.
const maxTitle = 48

type channelBody struct {
	Name     string   `json:"name"`
	Members  []string `json:"members"`
	Message  string   `json:"message"`
	Author   string   `json:"author"`
	Urgent   bool     `json:"urgent"`
	Session  string   `json:"session"`
	Muted    bool     `json:"muted"`
	AfterMsg string   `json:"after"`
}

// channelRoute is the one entry point, as the schedules routes are: the paths
// under /api/channels are few and this keeps them beside each other.
func (sh *Shared) channelRoute(w http.ResponseWriter, r *http.Request, prefix string) {
	rest := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, prefix), "/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if parts[0] == "" {
		parts = nil
	}

	switch {
	case len(parts) == 0 && r.Method == http.MethodGet:
		sh.listChannels(w, r)
	case len(parts) == 0 && r.Method == http.MethodPost:
		sh.createChannel(w, r)
	case len(parts) == 1 && r.Method == http.MethodGet:
		sh.readChannel(w, r, parts[0])
	case len(parts) == 1 && r.Method == http.MethodDelete:
		sh.deleteChannel(w, parts[0])
	case len(parts) == 2 && parts[1] == "messages" && r.Method == http.MethodGet:
		sh.readMessages(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "messages" && r.Method == http.MethodPost:
		sh.postMessage(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "members" && r.Method == http.MethodPost:
		sh.addMember(w, r, parts[0])
	case len(parts) == 3 && parts[1] == "members" && r.Method == http.MethodDelete:
		sh.removeMember(w, parts[0], parts[2])
	case len(parts) == 2 && parts[1] == "read" && r.Method == http.MethodPost:
		sh.markRead(w, r, parts[0])
	default:
		jsonError(w, "no such channel route", http.StatusNotFound)
	}
}

func (sh *Shared) listChannels(w http.ResponseWriter, r *http.Request) {
	if err := sh.DB.EnsureGeneral(); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	channels, err := sh.DB.Channels()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	reader := readerOf(r)
	out := make([]map[string]any, 0, len(channels))
	for _, ch := range channels {
		unread, err := sh.DB.Unread(ch.ID, reader)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out = append(out, map[string]any{
			"id": ch.ID, "name": ch.Name, "members": ch.Members,
			"created_by": ch.CreatedBy, "created_at": ch.CreatedAt,
			"unread": unread, "titles": sh.titles(ch.Members),
		})
	}
	jsonResponse(w, http.StatusOK, map[string]any{"channels": out})
}

func (sh *Shared) readChannel(w http.ResponseWriter, r *http.Request, id string) {
	ch, _, err := sh.DB.Channel(id)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if ch == nil {
		jsonError(w, "no such channel", http.StatusNotFound)
		return
	}
	unread, _ := sh.DB.Unread(ch.ID, readerOf(r))
	jsonResponse(w, http.StatusOK, map[string]any{
		"channel": map[string]any{
			"id": ch.ID, "name": ch.Name, "members": ch.Members,
			"created_by": ch.CreatedBy, "created_at": ch.CreatedAt,
			"unread": unread, "titles": sh.titles(ch.Members),
		},
	})
}

/*
createChannel makes one and, if it was given a message, says it.

The reply says whether the channel is one that already existed, because that
decides what the client shows: a reused channel is opened, not announced, and
nobody in it was sent a joining prompt — nobody joined.
*/
func (sh *Shared) createChannel(w http.ResponseWriter, r *http.Request) {
	var body channelBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "bad request", http.StatusBadRequest)
		return
	}
	author := body.Author
	if author == "" {
		author = store.AuthorUser
	}

	ch, reused, err := sh.DB.CreateChannel(body.Name, author, body.Members)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	if !reused {
		for _, member := range ch.Members {
			sh.sendJoining(ch, member, strings.TrimSpace(body.Message))
		}
	}

	// The message is recorded whether or not the channel is new, and delivered
	// to anyone who was not just told it in their joining prompt.
	if strings.TrimSpace(body.Message) != "" {
		skipJoiners := ch.Members
		if reused {
			skipJoiners = nil
		}
		if _, err := sh.say(ch, author, body.Message, body.Urgent, skipJoiners); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	sh.SSE.Broadcast(SSEEvent{Type: "channel_created", Data: map[string]any{"id": ch.ID}})
	jsonResponse(w, http.StatusOK, map[string]any{
		"channel": map[string]any{
			"id": ch.ID, "name": ch.Name, "members": ch.Members, "titles": sh.titles(ch.Members),
		},
		"existing": reused,
	})
}

func (sh *Shared) deleteChannel(w http.ResponseWriter, id string) {
	if err := sh.DB.DeleteChannel(id); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	sh.SSE.Broadcast(SSEEvent{Type: "channel_deleted", Data: map[string]any{"id": id}})
	jsonResponse(w, http.StatusOK, map[string]any{"success": true})
}

func (sh *Shared) readMessages(w http.ResponseWriter, r *http.Request, id string) {
	after := r.URL.Query().Get("after")
	reader := readerOf(r)
	// "Since I last looked" is the common ask, and doing it here saves every
	// caller from holding a receipt of its own.
	if r.URL.Query().Get("since_last") == "1" {
		at, err := sh.DB.LastRead(id, reader)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		after = at
	}

	messages, err := sh.DB.Messages(id, after, 0)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if r.URL.Query().Get("mark_read") != "0" {
		if err := sh.DB.MarkRead(id, reader); err != nil {
			log.Printf("channel: mark read %s: %v", id, err)
		}
	}

	out := make([]map[string]any, 0, len(messages))
	for _, m := range messages {
		out = append(out, map[string]any{
			"id": m.ID, "author": m.Author, "from": sh.authorName(m.Author),
			"body": m.Body, "urgent": m.Urgent, "created_at": m.CreatedAt,
		})
	}
	jsonResponse(w, http.StatusOK, map[string]any{"messages": out})
}

func (sh *Shared) postMessage(w http.ResponseWriter, r *http.Request, id string) {
	var body channelBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "bad request", http.StatusBadRequest)
		return
	}
	ch, _, err := sh.DB.Channel(id)
	if err != nil || ch == nil {
		jsonError(w, "no such channel", http.StatusNotFound)
		return
	}
	author := body.Author
	if author == "" {
		author = store.AuthorUser
	}

	message, err := sh.say(ch, author, body.Message, body.Urgent, nil)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"message": map[string]any{
		"id": message.ID, "author": message.Author, "from": sh.authorName(message.Author),
		"body": message.Body, "urgent": message.Urgent, "created_at": message.CreatedAt,
	}})
}

func (sh *Shared) addMember(w http.ResponseWriter, r *http.Request, id string) {
	var body channelBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Session == "" {
		jsonError(w, "missing session", http.StatusBadRequest)
		return
	}
	ch, _, err := sh.DB.Channel(id)
	if err != nil || ch == nil {
		jsonError(w, "no such channel", http.StatusNotFound)
		return
	}

	added, err := sh.DB.AddMember(id, body.Session)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if added {
		// Reread, so the joining prompt names everybody including the newcomer.
		if fresh, _, err := sh.DB.Channel(id); err == nil && fresh != nil {
			ch = fresh
		}
		sh.sendJoining(ch, body.Session, "")
		sh.SSE.Broadcast(SSEEvent{Type: "channel_updated", Data: map[string]any{"id": id}})
	}
	jsonResponse(w, http.StatusOK, map[string]any{"success": true, "added": added})
}

func (sh *Shared) removeMember(w http.ResponseWriter, id, session string) {
	if err := sh.DB.RemoveMember(id, session); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sh.SSE.Broadcast(SSEEvent{Type: "channel_updated", Data: map[string]any{"id": id}})
	jsonResponse(w, http.StatusOK, map[string]any{"success": true})
}

func (sh *Shared) markRead(w http.ResponseWriter, r *http.Request, id string) {
	if err := sh.DB.MarkRead(id, readerOf(r)); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"success": true})
}

/*
say records a message and delivers it.

`skip` is who has already been told — the members that were sent a joining
prompt in this same request, which carried the message inside it. Telling them
again would be the same sentence twice in one turn.
*/
func (sh *Shared) say(
	ch *store.Channel, author, body string, urgent bool, skip []string,
) (*store.ChannelMessage, error) {
	message, err := sh.DB.PostMessage(ch.ID, author, body, urgent)
	if err != nil {
		return nil, err
	}

	// The author reads its own message in the transcript it typed it into; the
	// muted asked not to be interrupted.
	members, err := sh.DB.Unmuted(ch.ID, store.AuthorSession(author))
	if err != nil {
		return nil, err
	}

	text := deliverable(sh.authorName(author), ch, message)
	for _, member := range members {
		if contains(skip, member) {
			continue
		}
		if urgent {
			// The point of urgent is that a queued prompt is no use: whatever
			// the agent is doing is the thing being interrupted.
			if err := sh.Backend.Interrupt(member); err != nil {
				log.Printf("channel: interrupt %s: %v", member, err)
			}
		}
		if _, err := sh.SendPrompt(member, text); err != nil {
			// One member being dead or unreachable is not a failed post: the
			// message is recorded, and the others still get it.
			log.Printf("channel: deliver to %s: %v", member, err)
		}
	}

	sh.SSE.Broadcast(SSEEvent{Type: "channel_message", Data: map[string]any{
		"channel_id": ch.ID, "id": message.ID, "author": message.Author,
	}})
	return message, nil
}

// deliverable is what a member is sent: a person's message in full, and a
// session's as a snippet with the command that would show the rest.
func deliverable(from string, ch *store.Channel, m *store.ChannelMessage) string {
	name := channelName(ch)
	if m.Author == store.AuthorUser {
		body := m.Body
		if len(body) > maxUserMessage {
			body = body[:maxUserMessage] + fmt.Sprintf(
				"\n… cut here. `helios chat read %s` for the rest.", name)
		}
		if m.Urgent {
			return fmt.Sprintf("[helios · %s · urgent] user:\n\n%s", name, body)
		}
		return fmt.Sprintf("[helios · %s] user:\n\n%s", name, body)
	}

	return fmt.Sprintf(
		"[helios] New message in \"%s\" from %q:\n%q\n— `helios chat read %s --since-last` for the thread.",
		name, from, truncate(m.Body, snippetLength), name,
	)
}

// sendJoining tells a session it is in a channel, which it has no other way of
// finding out. `withMessage` is the message the channel was created with, said
// in full here so the newcomer does not have to fetch it.
func (sh *Shared) sendJoining(ch *store.Channel, session, withMessage string) {
	name := channelName(ch)

	others := []string{}
	for _, member := range ch.Members {
		if member == session {
			continue
		}
		others = append(others, fmt.Sprintf("%q", sh.sessionTitle(member)))
	}
	others = append(others, "user")

	if _, err := sh.SendPrompt(session, joiningPrompt(name, others, withMessage, sh.missed(ch.ID))); err != nil {
		log.Printf("channel: joining prompt for %s: %v", session, err)
	}
}

/*
joiningPrompt is the only thing a session is ever told about channels.

It has to carry four things and no more: that it is in one, who else is, the
two commands, and when to bother. Everything past that is context spent on a
feature the session may never use.

A conversation that started earlier is summarised as a count rather than
pasted: fourteen messages in a newcomer's first prompt is the bill this whole
design exists to avoid.
*/
func joiningPrompt(name string, others []string, withMessage string, missed int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[helios] You are now in the group chat %q.\n\n", name)
	fmt.Fprintf(&b, "Members: %s.\n\n", strings.Join(others, ", "))
	fmt.Fprintf(&b, "  helios chat read %s --since-last     the conversation since you last looked\n", name)
	fmt.Fprintf(&b, "  helios chat post %s \"…\"              say something to everyone in it\n\n", name)
	b.WriteString("Post when you have changed something another member would trip over — an\n")
	b.WriteString("interface, a schema, a file you both touch. Do not narrate your work; your own\n")
	b.WriteString("transcript already has it. You will be told when there is a new message, with\n")
	b.WriteString("the first line of it; read the thread only when it looks like yours.")

	if withMessage != "" {
		fmt.Fprintf(&b, "\n\nuser: %s", truncate(withMessage, maxUserMessage))
		return b.String()
	}
	if missed > 0 {
		fmt.Fprintf(&b,
			"\n\nThis conversation started before you joined — %d messages.\n  helios chat read %s        all of it",
			missed, name)
	}
	return b.String()
}

func (sh *Shared) missed(channelID string) int {
	messages, err := sh.DB.Messages(channelID, "", 0)
	if err != nil {
		return 0
	}
	return len(messages)
}

// channelName is what the CLI would be given: the name where there is one, and
// the id where there is not.
func channelName(ch *store.Channel) string {
	if ch.Name != "" {
		return ch.Name
	}
	return ch.ID
}

// authorName is how a message is signed: a person is "user", and a session is
// its title, which is the one line that says what it is for.
func (sh *Shared) authorName(author string) string {
	if author == store.AuthorUser {
		return store.AuthorUser
	}
	return sh.sessionTitle(store.AuthorSession(author))
}

func (sh *Shared) sessionTitle(sessionID string) string {
	session, err := sh.DB.GetSession(sessionID)
	if err != nil || session == nil {
		return sessionID
	}
	if session.Title != nil {
		if title := strings.TrimSpace(*session.Title); title != "" {
			return truncate(title, maxTitle)
		}
	}
	if project := strings.TrimSpace(session.Project); project != "" {
		return truncate(project, maxTitle)
	}
	return sessionID
}

func (sh *Shared) titles(members []string) map[string]string {
	out := map[string]string{}
	for _, member := range members {
		out[member] = sh.sessionTitle(member)
	}
	return out
}

// readerOf is whose unread count and receipt a request is about: a session when
// it says so, and the person otherwise.
func readerOf(r *http.Request) string {
	if session := r.URL.Query().Get("session"); session != "" {
		return store.SessionAuthor(session)
	}
	return store.AuthorUser
}

func contains(list []string, want string) bool {
	for _, one := range list {
		if one == want {
			return true
		}
	}
	return false
}
