package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
)

// Group is one node of the grouping tree. Position is its place among its own
// siblings, not among every group: moving one is a write to the handful that
// share its parent.
type Group struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	Parent string `json:"parent,omitempty"`
	// Position among the groups sharing this one's parent.
	Position int `json:"position"`
}

// SessionGroup is one step of a session's path, as it reaches a client.
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

// CreateGroup adds a group at the end of its parent's children. An empty parent
// makes it a root.
//
// Two siblings may share a name, and so may two groups anywhere in the tree:
// identity is the key. "backend" under one project and "backend" under another
// are two nodes that happen to read the same.
func (s *Store) CreateGroup(name, parent string) (*Group, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("a group needs a name")
	}
	if parent != "" {
		if err := s.mustExist(parent); err != nil {
			return nil, err
		}
	}
	key, err := newGroupKey()
	if err != nil {
		return nil, err
	}

	var next int
	if err := s.db.QueryRow(
		`SELECT COALESCE(MAX(position), -1) + 1 FROM groups WHERE COALESCE(parent_key,'') = ?`, parent,
	).Scan(&next); err != nil {
		return nil, fmt.Errorf("next position under %q: %w", parent, err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO groups (key, name, parent_key, position) VALUES (?, ?, ?, ?)`,
		key, name, nullable(parent), next,
	); err != nil {
		return nil, fmt.Errorf("create group %q: %w", name, err)
	}
	return &Group{Key: key, Name: name, Parent: parent, Position: next}, nil
}

// RenameGroup changes a group's name. The key does not move, so every session
// filed under it and every child hanging off it are undisturbed.
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

// MoveGroup re-parents a group, and the whole subtree with it. An empty parent
// makes it a root.
//
// Nothing beneath the group records its own depth, so one row moves all of it.
func (s *Store) MoveGroup(key, parent string) error {
	if err := s.mustExist(key); err != nil {
		return err
	}
	if key == parent {
		return fmt.Errorf("a group cannot be its own parent")
	}
	if parent != "" {
		if err := s.mustExist(parent); err != nil {
			return err
		}
		// A node moved under its own descendant makes a loop that no walk of the
		// tree terminates on, so the ancestors are walked before the write.
		ancestors, err := s.ancestorsOf(parent)
		if err != nil {
			return err
		}
		for _, up := range ancestors {
			if up == key {
				return fmt.Errorf("a group cannot be moved inside itself")
			}
		}
	}

	var next int
	if err := s.db.QueryRow(
		`SELECT COALESCE(MAX(position), -1) + 1 FROM groups WHERE COALESCE(parent_key,'') = ?`, parent,
	).Scan(&next); err != nil {
		return err
	}
	_, err := s.db.Exec(`UPDATE groups SET parent_key = ?, position = ? WHERE key = ?`,
		nullable(parent), next, key)
	return err
}

// DeleteGroup removes one node and lifts everything under it one level.
//
// Children take the deleted node's parent, and so do the sessions filed on it;
// when it was a root, both end up unassigned. Nothing is destroyed but the node
// itself — a delete is a change to the shape of the tree, not a decision about
// anyone's sessions.
func (s *Store) DeleteGroup(key string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var parent sql.NullString
	err = tx.QueryRow(`SELECT parent_key FROM groups WHERE key = ?`, key).Scan(&parent)
	if err == sql.ErrNoRows {
		return fmt.Errorf("no group %s", key)
	}
	if err != nil {
		return err
	}

	// Appended after whatever already sits under the new parent, so the promoted
	// children do not collide with their new siblings' positions.
	var next int
	if err := tx.QueryRow(
		`SELECT COALESCE(MAX(position), -1) + 1 FROM groups WHERE COALESCE(parent_key,'') = ?`,
		parent.String,
	).Scan(&next); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT key FROM groups WHERE parent_key = ? ORDER BY position`, key)
	if err != nil {
		return err
	}
	var children []string
	for rows.Next() {
		var child string
		if err := rows.Scan(&child); err != nil {
			rows.Close()
			return err
		}
		children = append(children, child)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for i, child := range children {
		if _, err := tx.Exec(`UPDATE groups SET parent_key = ?, position = ? WHERE key = ?`,
			parent, next+i, child); err != nil {
			return fmt.Errorf("reparent %s: %w", child, err)
		}
	}

	if _, err := tx.Exec(`UPDATE sessions SET group_key = ? WHERE group_key = ?`, parent, key); err != nil {
		return fmt.Errorf("reparent sessions of %s: %w", key, err)
	}
	if _, err := tx.Exec(`DELETE FROM groups WHERE key = ?`, key); err != nil {
		return fmt.Errorf("delete group %s: %w", key, err)
	}
	return tx.Commit()
}

// ListGroups returns every group, parents before children, siblings in order.
func (s *Store) ListGroups() ([]Group, error) {
	rows, err := s.db.Query(
		`SELECT key, name, COALESCE(parent_key, ''), position FROM groups ORDER BY position, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var flat []Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.Key, &g.Name, &g.Parent, &g.Position); err != nil {
			return nil, err
		}
		flat = append(flat, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Depth-first, so a client can render the list without sorting it again.
	byParent := make(map[string][]Group, len(flat))
	for _, g := range flat {
		byParent[g.Parent] = append(byParent[g.Parent], g)
	}
	var walk func(parent string) []Group
	walk = func(parent string) []Group {
		var out []Group
		for _, g := range byParent[parent] {
			out = append(out, g)
			out = append(out, walk(g.Key)...)
		}
		return out
	}
	return walk(""), nil
}

// SetGroupOrder arranges one parent's children, first key first.
//
// Position is among siblings, so ordering is always a question about one
// parent. Children the caller did not mention keep their relative order and
// follow the ones it did: a group whose sessions are all hidden behind the
// terminated filter is missing from the client's list, and dropping it to the
// end would lose an arrangement nobody touched.
func (s *Store) SetGroupOrder(parent string, keys []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	named := make(map[string]bool, len(keys))
	for _, key := range keys {
		named[key] = true
	}

	rows, err := tx.Query(
		`SELECT key FROM groups WHERE COALESCE(parent_key,'') = ? ORDER BY position, name`, parent)
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

	stmt, err := tx.Prepare(
		`UPDATE groups SET position = ? WHERE key = ? AND COALESCE(parent_key,'') = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for position, key := range ordered {
		if _, err := stmt.Exec(position, key, parent); err != nil {
			return fmt.Errorf("order group %s: %w", key, err)
		}
	}
	return tx.Commit()
}

// SetSessionGroup files a session under one group. An empty key unassigns it.
func (s *Store) SetSessionGroup(sessionID, key string) error {
	if key != "" {
		if err := s.mustExist(key); err != nil {
			return err
		}
	}
	res, err := s.db.Exec(`UPDATE sessions SET group_key = ? WHERE session_id = ?`,
		nullable(key), sessionID)
	if err != nil {
		return fmt.Errorf("set group on %s: %w", sessionID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no session %s", sessionID)
	}
	return nil
}

// pathOf returns a group and its ancestors, outermost first. A key naming a
// group that is gone yields nothing rather than a broken path.
func pathOf(key string, byKey map[string]Group) []SessionGroup {
	var reversed []SessionGroup
	seen := make(map[string]bool)
	for key != "" && !seen[key] {
		seen[key] = true
		g, ok := byKey[key]
		if !ok {
			return nil
		}
		reversed = append(reversed, SessionGroup{Key: g.Key, Name: g.Name, Position: g.Position})
		key = g.Parent
	}
	path := make([]SessionGroup, 0, len(reversed))
	for i := len(reversed) - 1; i >= 0; i-- {
		path = append(path, reversed[i])
	}
	return path
}

// ancestorsOf walks from a group up to its root. The seen set is a guard
// against a cycle that some earlier bug let through: a walk that cannot
// terminate is worse than one that stops early.
func (s *Store) ancestorsOf(key string) ([]string, error) {
	groups, err := s.ListGroups()
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]Group, len(groups))
	for _, g := range groups {
		byKey[g.Key] = g
	}
	var up []string
	seen := make(map[string]bool)
	for key != "" && !seen[key] {
		seen[key] = true
		g, ok := byKey[key]
		if !ok {
			break
		}
		up = append(up, g.Key)
		key = g.Parent
	}
	return up, nil
}

// descendantsOf returns a group and everything beneath it, which is what asking
// for a branch means.
func (s *Store) descendantsOf(key string) ([]string, error) {
	groups, err := s.ListGroups()
	if err != nil {
		return nil, err
	}
	children := make(map[string][]string, len(groups))
	for _, g := range groups {
		children[g.Parent] = append(children[g.Parent], g.Key)
	}
	out := []string{key}
	for i := 0; i < len(out); i++ {
		out = append(out, children[out[i]]...)
	}
	return out, nil
}

func (s *Store) mustExist(key string) error {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM groups WHERE key = ?`, key).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("no group %s", key)
	}
	return nil
}

// groupForCWD returns the group the newest session in dir was filed under,
// which is what a session starting there inherits.
func (s *Store) groupForCWD(cwd string) (sql.NullString, error) {
	var key sql.NullString
	if cwd == "" {
		return key, nil
	}
	err := s.db.QueryRow(`SELECT group_key FROM sessions
	    WHERE cwd = ? AND group_key IS NOT NULL
	    ORDER BY COALESCE(last_event_at, created_at) DESC LIMIT 1`, cwd).Scan(&key)
	if err == sql.ErrNoRows {
		return sql.NullString{}, nil
	}
	return key, err
}

// nullable renders an empty key as SQL NULL, so "no parent" and "no group" have
// one representation rather than two.
func nullable(key string) interface{} {
	if key == "" {
		return nil
	}
	return key
}
