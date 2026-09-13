package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// A channel is several sessions and the person, with one conversation running
// through it. See docs/specs/60-group-chat.md.
//
// Everything here is host-local: a channel belongs to the daemon that holds it,
// because a conversation spanning two daemons has no owner and its messages
// would have to cross a tunnel to be read.

// GeneralChannel is the channel every session on this daemon is in. It is
// created on demand and never deleted.
const GeneralChannel = "general"

// AuthorUser is who a message from a person is from. Anything else is
// "session:<id>".
const AuthorUser = "user"

type Channel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Session ids, in the order they joined.
	Members   []string `json:"members"`
	CreatedBy string   `json:"created_by"`
	CreatedAt string   `json:"created_at"`
	// Closed: readable, but it takes no more messages and delivers nothing.
	Archived bool `json:"archived"`
}

type ChannelMessage struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	// "user", or "session:<id>".
	Author    string `json:"author"`
	Body      string `json:"body"`
	Urgent    bool   `json:"urgent,omitempty"`
	CreatedAt string `json:"created_at"`
}

func newID(prefix string) (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	return prefix + hex.EncodeToString(buf), nil
}

// SessionAuthor is how a session names itself as the author of a message.
func SessionAuthor(sessionID string) string {
	return "session:" + sessionID
}

// AuthorSession is the session an author names, or "" for a person.
func AuthorSession(author string) string {
	return strings.TrimPrefix(author, "session:")
}

/*
CreateChannel makes one, or hands back the one that already holds exactly these
members.

An unnamed channel *is* its members: asking for [s2, s7] when a channel of
exactly [s2, s7] exists gives that channel, because the alternative is three
"untitled" channels holding the same two sessions, one of which has the thing
you are looking for. A named channel is a deliberate act and is always new —
naming is how the caller says "not that one".

The second return says whether this is a channel that already existed, which
the caller needs: a reused channel takes the message but sends nobody a joining
prompt, because nobody joined.
*/
func (s *Store) CreateChannel(name, createdBy string, members []string) (*Channel, bool, error) {
	name = strings.TrimSpace(name)
	members = dedupe(members)
	if len(members) == 0 {
		return nil, false, fmt.Errorf("a channel needs at least one session")
	}

	if name == "" {
		if existing, err := s.channelWithMembers(members); err != nil {
			return nil, false, err
		} else if existing != nil {
			return existing, true, nil
		}
	}

	id, err := newID("ch_")
	if err != nil {
		return nil, false, err
	}
	if createdBy == "" {
		createdBy = AuthorUser
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, false, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(
		`INSERT INTO channels (id, name, created_by, created_at) VALUES (?, ?, ?, ?)`,
		id, name, createdBy, time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		return nil, false, fmt.Errorf("insert channel: %w", err)
	}
	for _, member := range members {
		if _, err := tx.Exec(
			`INSERT INTO channel_members (channel_id, session_id) VALUES (?, ?)`, id, member,
		); err != nil {
			return nil, false, fmt.Errorf("insert member: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("commit: %w", err)
	}

	return s.Channel(id)
}

// EnsureGeneral creates the daemon's notice board if it is not there yet.
//
// Its membership is not stored: every session on the host is in it, and a table
// saying so would have to be kept in step with every session that starts.
func (s *Store) EnsureGeneral() error {
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO channels (id, name, created_by) VALUES (?, ?, ?)`,
		GeneralChannel, "general", AuthorUser,
	)
	if err != nil {
		return fmt.Errorf("ensure general: %w", err)
	}
	return nil
}

// channelWithMembers finds the open unnamed channel holding exactly this set.
//
// A closed one is skipped rather than handed back: archiving says the
// conversation is finished, and reopening it because somebody asked for the
// same two sessions again would undo that behind their back.
func (s *Store) channelWithMembers(members []string) (*Channel, error) {
	rows, err := s.db.Query(
		`SELECT id FROM channels WHERE name = '' AND archived = 0 AND id != ?`, GeneralChannel)
	if err != nil {
		return nil, fmt.Errorf("scan channels: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan channel: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scan channels: %w", err)
	}

	want := slices.Clone(members)
	sort.Strings(want)
	for _, id := range ids {
		held, err := s.ChannelMembers(id)
		if err != nil {
			return nil, err
		}
		got := slices.Clone(held)
		sort.Strings(got)
		if slices.Equal(want, got) {
			return s.channelRow(id)
		}
	}
	return nil, nil
}

func (s *Store) channelRow(id string) (*Channel, error) {
	var ch Channel
	var archived int
	err := s.db.QueryRow(
		`SELECT id, name, created_by, created_at, archived FROM channels WHERE id = ?`, id,
	).Scan(&ch.ID, &ch.Name, &ch.CreatedBy, &ch.CreatedAt, &archived)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read channel: %w", err)
	}
	ch.Archived = archived == 1
	members, err := s.ChannelMembers(id)
	if err != nil {
		return nil, err
	}
	ch.Members = members
	return &ch, nil
}

// Channel reads one, with its members. The bool matches CreateChannel's, so the
// two can be returned from the same line.
func (s *Store) Channel(id string) (*Channel, bool, error) {
	ch, err := s.channelRow(id)
	if err != nil || ch == nil {
		return nil, false, err
	}
	return ch, false, nil
}

// Channels lists them, newest first, with general pinned to the top and the
// closed ones after the open ones.
func (s *Store) Channels(includeArchived bool) ([]Channel, error) {
	where := `WHERE archived = 0`
	if includeArchived {
		where = ""
	}
	rows, err := s.db.Query(
		`SELECT id, name, created_by, created_at, archived FROM channels ` + where + `
		 ORDER BY (id = 'general') DESC, archived ASC, created_at DESC, id DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	defer rows.Close()

	var out []Channel
	for rows.Next() {
		var ch Channel
		var archived int
		if err := rows.Scan(&ch.ID, &ch.Name, &ch.CreatedBy, &ch.CreatedAt, &archived); err != nil {
			return nil, fmt.Errorf("scan channel: %w", err)
		}
		ch.Archived = archived == 1
		out = append(out, ch)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	for at := range out {
		members, err := s.ChannelMembers(out[at].ID)
		if err != nil {
			return nil, err
		}
		out[at].Members = members
	}
	return out, nil
}

/*
ChannelMembers is who is in a channel.

For general it is not read from channel_members at all — it is every session on
this daemon that has not ended, worked out from the sessions table each time.

That is what makes the rule free. A session that is terminated drops out of the
answer with no row to delete; one that is resumed comes back with no row to
add. A stored membership would have to be corrected on every start, stop and
resume, and the first one it missed would leave the notice board lying about
who is standing at it.
*/
func (s *Store) ChannelMembers(id string) ([]string, error) {
	query := `SELECT session_id FROM channel_members WHERE channel_id = ? ORDER BY joined_at, session_id`
	args := []any{id}
	if id == GeneralChannel {
		query = `SELECT session_id FROM sessions WHERE status != 'terminated' ORDER BY created_at, session_id`
		args = nil
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	defer rows.Close()

	members := []string{}
	for rows.Next() {
		var session string
		if err := rows.Scan(&session); err != nil {
			return nil, fmt.Errorf("scan member: %w", err)
		}
		members = append(members, session)
	}
	return members, rows.Err()
}

// AddMember puts a session in a channel. Already being in it is not an error:
// two agents adding the same session is a race, not a mistake.
//
// The bool says whether this call is what put it there, which decides whether
// it is sent a joining prompt.
func (s *Store) AddMember(channelID, sessionID string) (bool, error) {
	if channelID == GeneralChannel {
		return false, fmt.Errorf("every session is already in the general channel")
	}
	archived, err := s.archived(channelID)
	if err != nil {
		return false, err
	}
	if archived {
		return false, fmt.Errorf("that channel is closed")
	}

	result, err := s.db.Exec(
		`INSERT OR IGNORE INTO channel_members (channel_id, session_id) VALUES (?, ?)`,
		channelID, sessionID,
	)
	if err != nil {
		return false, fmt.Errorf("add member: %w", err)
	}
	added, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("add member: %w", err)
	}
	return added > 0, nil
}

func (s *Store) RemoveMember(channelID, sessionID string) error {
	if channelID == GeneralChannel {
		return fmt.Errorf("a session leaves the general channel by ending, not by being removed")
	}
	_, err := s.db.Exec(
		`DELETE FROM channel_members WHERE channel_id = ? AND session_id = ?`, channelID, sessionID,
	)
	if err != nil {
		return fmt.Errorf("remove member: %w", err)
	}
	return nil
}

func (s *Store) SetMuted(channelID, sessionID string, muted bool) error {
	flag := 0
	if muted {
		flag = 1
	}
	_, err := s.db.Exec(
		`UPDATE channel_members SET muted = ? WHERE channel_id = ? AND session_id = ?`,
		flag, channelID, sessionID,
	)
	if err != nil {
		return fmt.Errorf("mute member: %w", err)
	}
	return nil
}

/*
Unmuted is who a message is delivered to: the members, less the author, less
anyone who asked not to be interrupted.

Nobody, for general. It is a notice board: sessions read it, it does not read
them. A snippet per post per session is fine for a channel of three, but on a
daemon running thirty sessions one sentence would cost thirty prompts and
thirty context windows — and the cost grows with how useful the feature gets.
So the membership is everyone and the delivery is no one.
*/
func (s *Store) Unmuted(channelID, exceptSession string) ([]string, error) {
	if channelID == GeneralChannel {
		return []string{}, nil
	}

	rows, err := s.db.Query(
		`SELECT session_id FROM channel_members
		 WHERE channel_id = ? AND muted = 0 AND session_id != ?
		 ORDER BY joined_at, session_id`,
		channelID, exceptSession,
	)
	if err != nil {
		return nil, fmt.Errorf("list unmuted: %w", err)
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var session string
		if err := rows.Scan(&session); err != nil {
			return nil, fmt.Errorf("scan member: %w", err)
		}
		out = append(out, session)
	}
	return out, rows.Err()
}

// PostMessage records what was said. Delivering it is the server's business.
func (s *Store) PostMessage(channelID, author, body string, urgent bool) (*ChannelMessage, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, fmt.Errorf("a message needs a body")
	}
	archived, err := s.archived(channelID)
	if err != nil {
		return nil, err
	}
	if archived {
		return nil, fmt.Errorf("that channel is closed")
	}

	flag := 0
	if urgent {
		flag = 1
	}
	now := time.Now().UTC().Format(time.RFC3339)

	// The id is the sequence, zero-padded: a receipt is "everything up to this
	// id", compared as a string, so the ids have to sort the way the
	// conversation ran. A clock does not — two messages can share a
	// millisecond, and then their order is whatever the random suffix said.
	result, err := s.db.Exec(
		`INSERT INTO channel_messages (channel_id, author, body, urgent, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		channelID, author, body, flag, now,
	)
	if err != nil {
		return nil, fmt.Errorf("insert message: %w", err)
	}
	seq, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("insert message: %w", err)
	}
	id := fmt.Sprintf("m_%012d", seq)
	if _, err := s.db.Exec(`UPDATE channel_messages SET id = ? WHERE seq = ?`, id, seq); err != nil {
		return nil, fmt.Errorf("name message: %w", err)
	}
	return &ChannelMessage{
		ID: id, ChannelID: channelID, Author: author, Body: body, Urgent: urgent, CreatedAt: now,
	}, nil
}

// Messages reads a channel, oldest first. `after` is a message id: pass the
// reader's receipt to get what they have not seen.
func (s *Store) Messages(channelID, after string, limit int) ([]ChannelMessage, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.Query(
		`SELECT id, channel_id, author, body, urgent, created_at FROM channel_messages
		 WHERE channel_id = ? AND id > ? ORDER BY seq LIMIT ?`,
		channelID, after, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("read messages: %w", err)
	}
	defer rows.Close()

	out := []ChannelMessage{}
	for rows.Next() {
		var m ChannelMessage
		var urgent int
		if err := rows.Scan(&m.ID, &m.ChannelID, &m.Author, &m.Body, &urgent, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		m.Urgent = urgent == 1
		out = append(out, m)
	}
	return out, rows.Err()
}

// Unread counts what a reader has not seen. A reader is a session id or "user".
func (s *Store) Unread(channelID, reader string) (int, error) {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM channel_messages
		 WHERE channel_id = ?
		   AND id > COALESCE((SELECT last_read FROM channel_receipts
		                      WHERE channel_id = ? AND reader = ?), '')
		   AND author != ?`,
		channelID, channelID, reader, reader,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count unread: %w", err)
	}
	return count, nil
}

// MarkRead moves a reader's receipt to the newest message in the channel.
func (s *Store) MarkRead(channelID, reader string) error {
	_, err := s.db.Exec(
		`INSERT INTO channel_receipts (channel_id, reader, last_read)
		 VALUES (?, ?, COALESCE((SELECT id FROM channel_messages
		                          WHERE channel_id = ? ORDER BY seq DESC LIMIT 1), ''))
		 ON CONFLICT(channel_id, reader) DO UPDATE SET last_read = excluded.last_read`,
		channelID, reader, channelID,
	)
	if err != nil {
		return fmt.Errorf("mark read: %w", err)
	}
	return nil
}

// LastRead is where a reader got to, or "" for one who has never looked.
func (s *Store) LastRead(channelID, reader string) (string, error) {
	var at string
	err := s.db.QueryRow(
		`SELECT last_read FROM channel_receipts WHERE channel_id = ? AND reader = ?`,
		channelID, reader,
	).Scan(&at)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read receipt: %w", err)
	}
	return at, nil
}

// archived answers whether a channel is closed, and errors when there is no
// such channel — which is how the callers below tell a typo from a refusal.
func (s *Store) archived(id string) (bool, error) {
	var flag int
	err := s.db.QueryRow(`SELECT archived FROM channels WHERE id = ?`, id).Scan(&flag)
	if err == sql.ErrNoRows {
		return false, fmt.Errorf("no such channel")
	}
	if err != nil {
		return false, fmt.Errorf("read channel: %w", err)
	}
	return flag == 1, nil
}

/*
SetArchived closes a channel, or reopens it.

Closing is not hiding: the conversation stays readable, but it takes no more
messages and delivers nothing. That is the stronger promise, and the one worth
making — a channel somebody has finished with cannot come back a week later and
interrupt six agents.
*/
func (s *Store) SetArchived(id string, archived bool) error {
	if id == GeneralChannel {
		return fmt.Errorf("the general channel cannot be closed")
	}
	if _, err := s.archived(id); err != nil {
		return err
	}
	flag := 0
	if archived {
		flag = 1
	}
	if _, err := s.db.Exec(`UPDATE channels SET archived = ? WHERE id = ?`, flag, id); err != nil {
		return fmt.Errorf("archive channel: %w", err)
	}
	return nil
}

/*
RenameChannel gives a channel a name, or changes the one it has.

Naming an unnamed channel changes what it is, not only what it is called. An
unnamed channel *is* its member set — that is what channelWithMembers matches
on, and it matches only rows with no name. So the next ask for those same
sessions will make a new channel rather than find this one, which is the
existing rule rather than a new exception: naming is how somebody says "not
that one".

The name may not be cleared. An empty one would put the channel back into the
pool matched by member set, where it could collide with a channel that is
already there, and the caller has no way to say which of the two they meant.
*/
func (s *Store) RenameChannel(id, name string) error {
	if id == GeneralChannel {
		return fmt.Errorf("the general channel cannot be renamed")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("a channel needs a name to be renamed to")
	}
	if _, err := s.archived(id); err != nil {
		return err
	}
	if _, err := s.db.Exec(`UPDATE channels SET name = ? WHERE id = ?`, name, id); err != nil {
		return fmt.Errorf("rename channel: %w", err)
	}
	return nil
}

func (s *Store) DeleteChannel(id string) error {
	if id == GeneralChannel {
		return fmt.Errorf("the general channel cannot be deleted")
	}
	if _, err := s.db.Exec(`DELETE FROM channels WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete channel: %w", err)
	}
	// The rows in the other three tables reference this one, but foreign keys
	// are not enforced by default in SQLite, so they go by hand.
	for _, table := range []string{"channel_members", "channel_messages", "channel_receipts"} {
		if _, err := s.db.Exec(`DELETE FROM `+table+` WHERE channel_id = ?`, id); err != nil {
			return fmt.Errorf("delete %s: %w", table, err)
		}
	}
	return nil
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, one := range in {
		one = strings.TrimSpace(one)
		if one == "" || seen[one] {
			continue
		}
		seen[one] = true
		out = append(out, one)
	}
	return out
}
