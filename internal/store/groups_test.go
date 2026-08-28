package store

import (
	"strings"
	"testing"
)

func mustGroup(t *testing.T, s *Store, name, parent string) string {
	t.Helper()
	g, err := s.CreateGroup(name, parent)
	if err != nil {
		t.Fatalf("create group %q under %q: %v", name, parent, err)
	}
	return g.Key
}

func mustSession(t *testing.T, s *Store, id, cwd, group string) {
	t.Helper()
	if err := s.UpsertSession(&Session{SessionID: id, Source: "claude", CWD: cwd, Status: "idle"}); err != nil {
		t.Fatalf("upsert %s: %v", id, err)
	}
	if group != "" {
		if err := s.SetSessionGroup(id, group); err != nil {
			t.Fatalf("file %s: %v", id, err)
		}
	}
}

// pathNames returns the names of a session's path, outermost first.
func pathNames(t *testing.T, s *Store, id string) []string {
	t.Helper()
	sessions, err := s.SearchSessions(SessionQuery{Grouped: true})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, sess := range sessions {
		if sess.SessionID != id {
			continue
		}
		names := make([]string, 0, len(sess.GroupPath))
		for _, g := range sess.GroupPath {
			names = append(names, g.Name)
		}
		return names
	}
	t.Fatalf("no session %s", id)
	return nil
}

func groupKeyOf(t *testing.T, s *Store, id string) string {
	t.Helper()
	sessions, _ := s.SearchSessions(SessionQuery{Grouped: true})
	for _, sess := range sessions {
		if sess.SessionID == id {
			return sess.GroupKey
		}
	}
	t.Fatalf("no session %s", id)
	return ""
}

func TestCreateGroup_NestsUnderItsParent(t *testing.T) {
	s := setupTestStore(t)
	work := mustGroup(t, s, "Work", "")
	opal := mustGroup(t, s, "opal-app", work)
	mustSession(t, s, "s1", "/x/a", opal)

	if got := pathNames(t, s, "s1"); len(got) != 2 || got[0] != "Work" || got[1] != "opal-app" {
		t.Fatalf("path = %v, want [Work opal-app]", got)
	}
}

func TestCreateGroup_RefusesAnUnknownParent(t *testing.T) {
	s := setupTestStore(t)
	if _, err := s.CreateGroup("orphan", "g_nope"); err == nil {
		t.Fatal("accepted a parent that does not exist")
	}
}

// Identity is the key, so the same label in two places is two nodes. The
// earlier model folded them into one.
func TestCreateGroup_AllowsTheSameNameTwice(t *testing.T) {
	s := setupTestStore(t)
	opal := mustGroup(t, s, "opal-app", "")
	helios := mustGroup(t, s, "helios", "")
	one := mustGroup(t, s, "backend", opal)
	two := mustGroup(t, s, "backend", helios)

	if one == two {
		t.Fatal("two groups named backend share a key")
	}
	mustSession(t, s, "s1", "/x/a", one)
	mustSession(t, s, "s2", "/x/b", two)
	if got := pathNames(t, s, "s1"); got[0] != "opal-app" {
		t.Errorf("s1 path = %v", got)
	}
	if got := pathNames(t, s, "s2"); got[0] != "helios" {
		t.Errorf("s2 path = %v", got)
	}
}

// Position is among siblings, so two roots and two children can share numbers
// without meaning anything by it.
func TestCreateGroup_PositionsAreAmongSiblings(t *testing.T) {
	s := setupTestStore(t)
	work := mustGroup(t, s, "Work", "")
	side := mustGroup(t, s, "Side", "")
	child := mustGroup(t, s, "opal-app", work)

	byKey := map[string]Group{}
	groups, _ := s.ListGroups()
	for _, g := range groups {
		byKey[g.Key] = g
	}
	if byKey[work].Position != 0 || byKey[side].Position != 1 {
		t.Errorf("roots = %d,%d want 0,1", byKey[work].Position, byKey[side].Position)
	}
	if byKey[child].Position != 0 {
		t.Errorf("first child = %d, want 0", byKey[child].Position)
	}
}

func TestListGroups_ReturnsParentsBeforeChildren(t *testing.T) {
	s := setupTestStore(t)
	work := mustGroup(t, s, "Work", "")
	opal := mustGroup(t, s, "opal-app", work)
	mustGroup(t, s, "backend", opal)
	mustGroup(t, s, "Side", "")

	groups, err := s.ListGroups()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var names []string
	for _, g := range groups {
		names = append(names, g.Name)
	}
	want := []string{"Work", "opal-app", "backend", "Side"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("order = %v, want %v", names, want)
		}
	}
}

func TestMoveGroup_TakesTheSubtreeWithIt(t *testing.T) {
	s := setupTestStore(t)
	work := mustGroup(t, s, "Work", "")
	side := mustGroup(t, s, "Side", "")
	opal := mustGroup(t, s, "opal-app", work)
	backend := mustGroup(t, s, "backend", opal)
	mustSession(t, s, "s1", "/x/a", backend)

	if err := s.MoveGroup(opal, side); err != nil {
		t.Fatalf("move: %v", err)
	}
	if got := pathNames(t, s, "s1"); len(got) != 3 || got[0] != "Side" {
		t.Fatalf("path = %v, want it to start at Side", got)
	}
}

func TestMoveGroup_RefusesACycle(t *testing.T) {
	s := setupTestStore(t)
	work := mustGroup(t, s, "Work", "")
	opal := mustGroup(t, s, "opal-app", work)
	backend := mustGroup(t, s, "backend", opal)

	for name, parent := range map[string]string{"itself": work, "its child": opal, "its grandchild": backend} {
		t.Run(name, func(t *testing.T) {
			if err := s.MoveGroup(work, parent); err == nil {
				t.Fatal("accepted a move that makes a loop")
			}
		})
	}
}

func TestMoveGroup_ToRoot(t *testing.T) {
	s := setupTestStore(t)
	work := mustGroup(t, s, "Work", "")
	opal := mustGroup(t, s, "opal-app", work)
	mustSession(t, s, "s1", "/x/a", opal)

	if err := s.MoveGroup(opal, ""); err != nil {
		t.Fatalf("move: %v", err)
	}
	if got := pathNames(t, s, "s1"); len(got) != 1 || got[0] != "opal-app" {
		t.Fatalf("path = %v, want just [opal-app]", got)
	}
}

// The rule the whole delete story rests on: nothing is orphaned, and nothing
// but the node itself is lost.
func TestDeleteGroup_LiftsChildrenAndSessionsOneLevel(t *testing.T) {
	s := setupTestStore(t)
	work := mustGroup(t, s, "Work", "")
	opal := mustGroup(t, s, "opal-app", work)
	backend := mustGroup(t, s, "backend", opal)
	mustSession(t, s, "onNode", "/x/a", opal)
	mustSession(t, s, "below", "/x/b", backend)

	if err := s.DeleteGroup(opal); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if got := pathNames(t, s, "onNode"); len(got) != 1 || got[0] != "Work" {
		t.Errorf("session on the deleted node = %v, want [Work]", got)
	}
	if got := pathNames(t, s, "below"); len(got) != 2 || got[0] != "Work" || got[1] != "backend" {
		t.Errorf("session below it = %v, want [Work backend]", got)
	}
}

func TestDeleteGroup_ARootLeavesEverythingUnassigned(t *testing.T) {
	s := setupTestStore(t)
	work := mustGroup(t, s, "Work", "")
	opal := mustGroup(t, s, "opal-app", work)
	mustSession(t, s, "onRoot", "/x/a", work)
	mustSession(t, s, "below", "/x/b", opal)

	if err := s.DeleteGroup(work); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if got := pathNames(t, s, "onRoot"); len(got) != 0 {
		t.Errorf("session on the deleted root = %v, want unassigned", got)
	}
	// Its child became a root, so anything under it keeps one level.
	if got := pathNames(t, s, "below"); len(got) != 1 || got[0] != "opal-app" {
		t.Errorf("session below = %v, want [opal-app]", got)
	}
}

func TestSetGroupOrder_ArrangesOneParentsChildren(t *testing.T) {
	s := setupTestStore(t)
	work := mustGroup(t, s, "Work", "")
	a := mustGroup(t, s, "a", work)
	b := mustGroup(t, s, "b", work)
	c := mustGroup(t, s, "c", work)

	if err := s.SetGroupOrder(work, []string{c, a, b}); err != nil {
		t.Fatalf("order: %v", err)
	}
	byKey := map[string]int{}
	groups, _ := s.ListGroups()
	for _, g := range groups {
		byKey[g.Key] = g.Position
	}
	if byKey[c] != 0 || byKey[a] != 1 || byKey[b] != 2 {
		t.Errorf("positions = c:%d a:%d b:%d, want 0,1,2", byKey[c], byKey[a], byKey[b])
	}
}

// A child hidden behind the terminated filter is missing from the client's
// list, and dropping it to the end would lose an arrangement nobody touched.
func TestSetGroupOrder_KeepsSiblingsTheClientDidNotMention(t *testing.T) {
	s := setupTestStore(t)
	work := mustGroup(t, s, "Work", "")
	a := mustGroup(t, s, "a", work)
	hidden := mustGroup(t, s, "hidden", work)
	b := mustGroup(t, s, "b", work)

	if err := s.SetGroupOrder(work, []string{b, a}); err != nil {
		t.Fatalf("order: %v", err)
	}
	byKey := map[string]int{}
	groups, _ := s.ListGroups()
	for _, g := range groups {
		byKey[g.Key] = g.Position
	}
	if byKey[b] != 0 || byKey[a] != 1 || byKey[hidden] != 2 {
		t.Errorf("hidden sibling not appended: b:%d a:%d hidden:%d", byKey[b], byKey[a], byKey[hidden])
	}
}

func TestSetSessionGroup_Rejects(t *testing.T) {
	s := setupTestStore(t)
	mustSession(t, s, "s1", "/x/a", "")
	if err := s.SetSessionGroup("s1", "g_nope"); err == nil {
		t.Fatal("accepted a group that does not exist")
	}
	if err := s.SetSessionGroup("nosuch", ""); err == nil {
		t.Fatal("accepted a session that does not exist")
	}
}

func TestSetSessionGroup_EmptyUnassigns(t *testing.T) {
	s := setupTestStore(t)
	work := mustGroup(t, s, "Work", "")
	mustSession(t, s, "s1", "/x/a", work)

	if err := s.SetSessionGroup("s1", ""); err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if got := pathNames(t, s, "s1"); len(got) != 0 {
		t.Errorf("path = %v, want none", got)
	}
}

// Asking for a group means asking for what is under it.
func TestSearchSessions_GroupKeyCoversTheWholeBranch(t *testing.T) {
	s := setupTestStore(t)
	work := mustGroup(t, s, "Work", "")
	opal := mustGroup(t, s, "opal-app", work)
	side := mustGroup(t, s, "Side", "")
	mustSession(t, s, "onWork", "/x/a", work)
	mustSession(t, s, "deep", "/x/b", opal)
	mustSession(t, s, "elsewhere", "/x/c", side)

	found, err := s.SearchSessions(SessionQuery{Grouped: true, GroupKey: work})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("got %d sessions, want the branch's 2", len(found))
	}

	leaf, _ := s.SearchSessions(SessionQuery{GroupKey: opal})
	if len(leaf) != 1 || leaf[0].SessionID != "deep" {
		t.Errorf("a leaf returned %d sessions", len(leaf))
	}
}

func TestSearchSessions_UngroupedQueryOmitsTheFields(t *testing.T) {
	s := setupTestStore(t)
	work := mustGroup(t, s, "Work", "")
	mustSession(t, s, "s1", "/x/a", work)

	sessions, err := s.SearchSessions(SessionQuery{})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if sessions[0].GroupPath != nil || sessions[0].GroupKey != "" {
		t.Errorf("grouping served without being asked for: %+v", sessions[0].GroupPath)
	}
}

// Assign a directory once and every later agent started there joins on its own.
func TestUpsertSession_InheritsTheGroupOfTheSameDirectory(t *testing.T) {
	s := setupTestStore(t)
	work := mustGroup(t, s, "Work", "")
	opal := mustGroup(t, s, "opal-app", work)
	mustSession(t, s, "first", "/x/opal", opal)

	mustSession(t, s, "second", "/x/opal", "")
	if got := groupKeyOf(t, s, "second"); got != opal {
		t.Errorf("second did not inherit: %q", got)
	}
	if got := pathNames(t, s, "second"); len(got) != 2 {
		t.Errorf("inherited path = %v, want two levels", got)
	}

	mustSession(t, s, "elsewhere", "/x/other", "")
	if got := groupKeyOf(t, s, "elsewhere"); got != "" {
		t.Errorf("a new directory inherited %q", got)
	}
}

func TestUpsertSession_DoesNotRefileOnUpdate(t *testing.T) {
	s := setupTestStore(t)
	work := mustGroup(t, s, "Work", "")
	side := mustGroup(t, s, "Side", "")
	mustSession(t, s, "s1", "/x/a", work)
	mustSession(t, s, "s2", "/x/a", side)

	if err := s.UpsertSession(&Session{SessionID: "s1", Source: "claude", CWD: "/x/a", Status: "active"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if got := groupKeyOf(t, s, "s1"); got != work {
		t.Errorf("s1 was refiled to %q", got)
	}
}

func TestCreateGroup_RefusesAnEmptyName(t *testing.T) {
	s := setupTestStore(t)
	if _, err := s.CreateGroup("   ", ""); err == nil {
		t.Fatal("accepted a group with no name")
	}
}

func TestRenameGroup_KeepsTheKeyAndTheTree(t *testing.T) {
	s := setupTestStore(t)
	work := mustGroup(t, s, "Work", "")
	opal := mustGroup(t, s, "opal-app", work)
	mustSession(t, s, "s1", "/x/a", opal)

	if err := s.RenameGroup(work, "Client work"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	got := pathNames(t, s, "s1")
	if len(got) != 2 || got[0] != "Client work" || got[1] != "opal-app" {
		t.Errorf("path = %v", got)
	}
	if err := s.RenameGroup("g_nope", "x"); err == nil || !strings.Contains(err.Error(), "no group") {
		t.Errorf("renaming a missing group gave %v", err)
	}
}
