package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// MaxGroupDepth is how many groups one session may hold. The sidebar enforces
// it too; the store enforces it because a client is not the last word on what
// goes in the database.
const MaxGroupDepth = 3

// Group is one grouping a person made. Position is the group's place among all
// groups, wherever it happens to be nested — a group has one position, not one
// per parent.
type Group struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Position int    `json:"position"`
}

// SessionGroup is a group as it reaches a client: the key it is stored under,
// its name, and where it sorts. Sent on the session rather than looked up,
// so the client needs no table of its own.
type SessionGroup struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Position int    `json:"position"`
}

func newGroupKey() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate group key: %w", err)
	}
	return "g_" + hex.EncodeToString(buf), nil
}

// CreateGroup adds a group at the end of the order.
func (s *Store) CreateGroup(name string) (*Group, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("a group needs a name")
	}
	key, err := newGroupKey()
	if err != nil {
		return nil, err
	}

	var next int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(position), -1) + 1 FROM groups`).Scan(&next); err != nil {
		return nil, fmt.Errorf("next group position: %w", err)
	}
	if _, err := s.db.Exec(`INSERT INTO groups (key, name, position) VALUES (?, ?, ?)`, key, name, next); err != nil {
		return nil, fmt.Errorf("create group %q: %w", name, err)
	}
	return &Group{Key: key, Name: name, Position: next}, nil
}

// RenameGroup changes a group's name. The key does not move, so every
// arrangement that mentions the group survives the rename.
func (s *Store) RenameGroup(key, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("a group needs a name")
	}
	res, err := s.db.Exec(`UPDATE groups SET name = ? WHERE key = ?`, name, key)
	if err != nil {
		return fmt.Errorf("rename group %s: %w", key, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no group %s", key)
	}
	return nil
}

// DeleteGroup removes a group and drops it from every session holding it, in
// one transaction: a session left pointing at a group that no longer exists
// would render under a blank header.
func (s *Store) DeleteGroup(key string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM groups WHERE key = ?`, key); err != nil {
		return fmt.Errorf("delete group %s: %w", key, err)
	}
	// json_remove needs the index of the element, so the rewrite happens in Go
	// over the few rows that actually mention the group.
	rows, err := tx.Query(`SELECT session_id, groups FROM sessions
	    WHERE groups IS NOT NULL
	      AND EXISTS (SELECT 1 FROM json_each(sessions.groups) WHERE value = ?)`, key)
	if err != nil {
		return fmt.Errorf("find sessions in group %s: %w", key, err)
	}
	type change struct {
		id   string
		keys []string
	}
	var changes []change
	for rows.Next() {
		var id string
		var raw sql.NullString
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return err
		}
		kept := make([]string, 0, MaxGroupDepth)
		for _, held := range decodeGroups(raw) {
			if held != key {
				kept = append(kept, held)
			}
		}
		changes = append(changes, change{id: id, keys: kept})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, c := range changes {
		encoded, err := encodeGroups(c.keys)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE sessions SET groups = ? WHERE session_id = ?`, encoded, c.id); err != nil {
			return fmt.Errorf("clear group %s from %s: %w", key, c.id, err)
		}
	}
	return tx.Commit()
}

// ListGroups returns every group in display order.
func (s *Store) ListGroups() ([]Group, error) {
	rows, err := s.db.Query(`SELECT key, name, position FROM groups ORDER BY position, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.Key, &g.Name, &g.Position); err != nil {
			return nil, err
		}
		result = append(result, g)
	}
	return result, rows.Err()
}

// SetGroupOrder writes a hand-arranged order, first key first.
//
// The whole list at once, like SetSessionOrder: dragging one header shifts
// every header it passed, so the client already knows the arrangement it wants.
// Groups the client did not mention keep their relative order and follow the
// ones it did — a group whose sessions are all hidden behind the terminated
// filter is missing from the client's list, and dropping it to unranked would
// lose an arrangement the user never touched.
func (s *Store) SetGroupOrder(keys []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	named := make(map[string]bool, len(keys))
	for _, key := range keys {
		named[key] = true
	}

	rows, err := tx.Query(`SELECT key FROM groups ORDER BY position, name`)
	if err != nil {
		return err
	}
	ordered := append([]string(nil), keys...)
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return err
		}
		if !named[key] {
			ordered = append(ordered, key)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	stmt, err := tx.Prepare(`UPDATE groups SET position = ? WHERE key = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for position, key := range ordered {
		if _, err := stmt.Exec(position, key); err != nil {
			return fmt.Errorf("order group %s: %w", key, err)
		}
	}
	return tx.Commit()
}

// SetSessionGroups replaces a session's groups with keys, outermost first.
//
// The list must be dense, free of duplicates, no deeper than MaxGroupDepth, and
// every key must name a group that exists. An empty list clears the session.
func (s *Store) SetSessionGroups(sessionID string, keys []string) error {
	if len(keys) > MaxGroupDepth {
		return fmt.Errorf("a session may hold %d groups, not %d", MaxGroupDepth, len(keys))
	}
	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		if key == "" {
			return fmt.Errorf("a group key cannot be empty")
		}
		if seen[key] {
			return fmt.Errorf("a session belongs to a group once, and %s is listed twice", key)
		}
		seen[key] = true

		var exists int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM groups WHERE key = ?`, key).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return fmt.Errorf("no group %s", key)
		}
	}

	encoded, err := encodeGroups(keys)
	if err != nil {
		return err
	}
	res, err := s.db.Exec(`UPDATE sessions SET groups = ? WHERE session_id = ?`, encoded, sessionID)
	if err != nil {
		return fmt.Errorf("set groups on %s: %w", sessionID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no session %s", sessionID)
	}
	return nil
}

// groupsForCWD returns the groups the newest session in dir holds, which is
// what a session starting there inherits. Empty when the directory is new.
func (s *Store) groupsForCWD(cwd string) (sql.NullString, error) {
	var raw sql.NullString
	if cwd == "" {
		return raw, nil
	}
	err := s.db.QueryRow(`SELECT groups FROM sessions
	    WHERE cwd = ? AND groups IS NOT NULL
	    ORDER BY COALESCE(last_event_at, created_at) DESC LIMIT 1`, cwd).Scan(&raw)
	if err == sql.ErrNoRows {
		return sql.NullString{}, nil
	}
	return raw, err
}

// encodeGroups renders keys as the JSON array the column holds. An empty list
// is NULL rather than "[]", so "has no groups" has one representation.
func encodeGroups(keys []string) (interface{}, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	encoded, err := json.Marshal(keys)
	if err != nil {
		return nil, fmt.Errorf("encode groups: %w", err)
	}
	return string(encoded), nil
}

// decodeGroups reads the column back. A malformed value reads as no groups: it
// is a display concern, and refusing to list a session because its grouping is
// unreadable would hide the session itself.
func decodeGroups(raw sql.NullString) []string {
	if !raw.Valid || raw.String == "" {
		return nil
	}
	var keys []string
	if err := json.Unmarshal([]byte(raw.String), &keys); err != nil {
		return nil
	}
	return keys
}
