package store

import (
	"errors"
	"testing"
)

// fork inserts a session that begins with another's conversation. forkedAt is
// explicit because sibling order depends on it and a test that relied on clock
// resolution would be flaky rather than wrong.
func fork(t *testing.T, s *Store, id, parent, forkedAt string) {
	t.Helper()
	sess := &Session{
		SessionID:     id,
		Source:        "claude",
		CWD:           "/tmp/" + id,
		Status:        "idle",
		ForkedFrom:    parent,
		ForkedAt:      &forkedAt,
		ForkWorkspace: "worktree",
	}
	if err := s.UpsertSession(sess); err != nil {
		t.Fatalf("upsert fork %s: %v", id, err)
	}
}

func ids(sessions []Session) []string {
	out := make([]string, len(sessions))
	for i, sess := range sessions {
		out[i] = sess.SessionID
	}
	return out
}

func TestForkColumnsSurviveARoundTrip(t *testing.T) {
	s := setupTestStore(t)
	mustSession(t, s, "root", "/tmp/root", "")
	fork(t, s, "child", "root", "2026-09-18T10:00:00Z")

	got, err := s.GetSession("child")
	if err != nil || got == nil {
		t.Fatalf("get fork: %v", err)
	}
	if got.ForkedFrom != "root" {
		t.Errorf("forked_from = %q, want root", got.ForkedFrom)
	}
	if got.ForkedAt == nil || *got.ForkedAt != "2026-09-18T10:00:00Z" {
		t.Errorf("forked_at = %v, want the time it was taken", got.ForkedAt)
	}
	if got.ForkWorkspace != "worktree" {
		t.Errorf("fork_workspace = %q, want worktree", got.ForkWorkspace)
	}
	if !got.IsFork() {
		t.Error("IsFork is false for a session with a parent")
	}
}

func TestForkCountAndRootAreDerived(t *testing.T) {
	s := setupTestStore(t)
	mustSession(t, s, "root", "/tmp/root", "")
	fork(t, s, "a", "root", "2026-09-18T10:00:00Z")
	fork(t, s, "b", "root", "2026-09-18T11:00:00Z")
	fork(t, s, "deep", "a", "2026-09-18T12:00:00Z")

	cases := map[string]struct {
		count int
		root  string
	}{
		"root": {count: 2, root: "root"},
		"a":    {count: 1, root: "root"},
		"b":    {count: 0, root: "root"},
		"deep": {count: 0, root: "root"},
	}
	for id, want := range cases {
		got, err := s.GetSession(id)
		if err != nil || got == nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if got.ForkCount != want.count {
			t.Errorf("%s: fork_count = %d, want %d", id, got.ForkCount, want.count)
		}
		if got.RootSessionID != want.root {
			t.Errorf("%s: root = %q, want %q", id, got.RootSessionID, want.root)
		}
	}
}

// A fork of a fork needs no new concept, and the count must not turn into a
// descendant count on the way.
func TestForkCountIsDirectChildrenOnly(t *testing.T) {
	s := setupTestStore(t)
	mustSession(t, s, "root", "/tmp/root", "")
	fork(t, s, "a", "root", "2026-09-18T10:00:00Z")
	fork(t, s, "b", "a", "2026-09-18T11:00:00Z")
	fork(t, s, "c", "b", "2026-09-18T12:00:00Z")

	root, _ := s.GetSession("root")
	if root.ForkCount != 1 {
		t.Errorf("root fork_count = %d, want 1 — a chain is not a fan", root.ForkCount)
	}
	c, _ := s.GetSession("c")
	if c.RootSessionID != "root" {
		t.Errorf("three levels down, root = %q, want root", c.RootSessionID)
	}
}

func TestTreeOrderPutsForksUnderTheirParent(t *testing.T) {
	sessions := []Session{
		{SessionID: "r1"},
		{SessionID: "r2"},
		{SessionID: "b", ForkedFrom: "r1", ForkedAt: strPtr("2026-09-18T11:00:00Z")},
		{SessionID: "a", ForkedFrom: "r1", ForkedAt: strPtr("2026-09-18T10:00:00Z")},
		{SessionID: "deep", ForkedFrom: "a", ForkedAt: strPtr("2026-09-18T12:00:00Z")},
	}

	got := ids(treeOrder(sessions))
	want := []string{"r1", "a", "deep", "b", "r2"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("tree order = %v, want %v", got, want)
		}
	}
}

// A search or the 1000-row limit can hand back a child whose parent did not
// make the window. Dropping it would hide a session that exists.
func TestTreeOrderDrawsAnOrphanAsARoot(t *testing.T) {
	sessions := []Session{
		{SessionID: "visible"},
		{SessionID: "orphan", ForkedFrom: "somewhere-else"},
	}

	got := ids(treeOrder(sessions))
	if len(got) != 2 {
		t.Fatalf("tree order = %v, want both sessions", got)
	}
}

// Only a bug can make a cycle, and a bug that hides sessions is worse than one
// that draws them flat.
func TestTreeOrderSurvivesACycle(t *testing.T) {
	sessions := []Session{
		{SessionID: "a", ForkedFrom: "b"},
		{SessionID: "b", ForkedFrom: "a"},
	}

	got := ids(treeOrder(sessions))
	if len(got) != 2 {
		t.Fatalf("tree order = %v, want both sessions drawn", got)
	}
}

func TestRootOfStopsOnACycle(t *testing.T) {
	parentOf := map[string]string{"a": "b", "b": "a"}
	if got := rootOf("a", parentOf); got == "" {
		t.Error("rootOf returned nothing on a cycle")
	}
}

// A fork's group comes from its root, so the fork's own key is never written —
// not even the one groupForCWD would have inherited from its directory.
func TestForkDoesNotInheritAGroupFromItsDirectory(t *testing.T) {
	s := setupTestStore(t)
	key := mustGroup(t, s, "helios", "")
	mustSession(t, s, "neighbour", "/tmp/shared", key)

	sess := &Session{
		SessionID:  "forked",
		Source:     "claude",
		CWD:        "/tmp/shared",
		Status:     "idle",
		ForkedFrom: "neighbour",
	}
	if err := s.UpsertSession(sess); err != nil {
		t.Fatalf("upsert fork: %v", err)
	}

	var stored string
	err := s.db.QueryRow(
		`SELECT COALESCE(group_key, '') FROM sessions WHERE session_id = ?`, "forked").Scan(&stored)
	if err != nil {
		t.Fatalf("read group_key: %v", err)
	}
	if stored != "" {
		t.Errorf("fork stored group_key %q, want none — its group is its root's", stored)
	}
}

// Storing nothing is only right if reading gives the root's group back.
func TestForkRendersUnderItsRootsGroup(t *testing.T) {
	s := setupTestStore(t)
	key := mustGroup(t, s, "helios", "")
	mustSession(t, s, "root", "/tmp/root", key)
	fork(t, s, "child", "root", "2026-09-18T10:00:00Z")

	sessions, err := s.SearchSessions(SessionQuery{Grouped: true})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, sess := range sessions {
		if sess.SessionID != "child" {
			continue
		}
		if sess.GroupKey != key {
			t.Errorf("fork group = %q, want its root's %q", sess.GroupKey, key)
		}
		if len(sess.GroupPath) != 1 || sess.GroupPath[0].Name != "helios" {
			t.Errorf("fork group path = %v, want [helios]", sess.GroupPath)
		}
		return
	}
	t.Fatal("fork missing from the list")
}

func TestSetSessionGroupRefusesAFork(t *testing.T) {
	s := setupTestStore(t)
	key := mustGroup(t, s, "helios", "")
	mustSession(t, s, "root", "/tmp/root", "")
	fork(t, s, "child", "root", "2026-09-18T10:00:00Z")

	err := s.SetSessionGroup("child", key)
	var notFileable *ErrForkNotFileable
	if !errors.As(err, &notFileable) {
		t.Fatalf("SetSessionGroup on a fork = %v, want ErrForkNotFileable", err)
	}
	if notFileable.Root != "root" {
		t.Errorf("error names root %q, want root", notFileable.Root)
	}
}

// Deleting the session a branch came from is not a decision about the branch.
func TestDeleteSessionLiftsForksOneLevel(t *testing.T) {
	s := setupTestStore(t)
	mustSession(t, s, "root", "/tmp/root", "")
	fork(t, s, "middle", "root", "2026-09-18T10:00:00Z")
	fork(t, s, "leaf", "middle", "2026-09-18T11:00:00Z")

	if err := s.DeleteSession("middle"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	leaf, err := s.GetSession("leaf")
	if err != nil || leaf == nil {
		t.Fatalf("get leaf: %v", err)
	}
	if leaf.ForkedFrom != "root" {
		t.Errorf("leaf forked_from = %q, want root", leaf.ForkedFrom)
	}
	if leaf.RootSessionID != "root" {
		t.Errorf("leaf root = %q, want root", leaf.RootSessionID)
	}
}

func TestDeleteRootMakesItsForksRoots(t *testing.T) {
	s := setupTestStore(t)
	mustSession(t, s, "root", "/tmp/root", "")
	fork(t, s, "child", "root", "2026-09-18T10:00:00Z")

	if err := s.DeleteSession("root"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	child, err := s.GetSession("child")
	if err != nil || child == nil {
		t.Fatalf("get child: %v", err)
	}
	if child.IsFork() {
		t.Errorf("child forked_from = %q, want empty", child.ForkedFrom)
	}
	if child.RootSessionID != "child" {
		t.Errorf("child root = %q, want itself", child.RootSessionID)
	}
}

func TestRootsFilterDropsForks(t *testing.T) {
	s := setupTestStore(t)
	mustSession(t, s, "root", "/tmp/root", "")
	fork(t, s, "child", "root", "2026-09-18T10:00:00Z")

	sessions, err := s.SearchSessions(SessionQuery{Roots: true})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if got := ids(sessions); len(got) != 1 || got[0] != "root" {
		t.Errorf("roots = %v, want [root]", got)
	}
}
