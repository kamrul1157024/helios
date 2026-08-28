package store

import (
	"strings"
	"testing"
)

// mustGroup creates a group or fails the test, returning its key.
func mustGroup(t *testing.T, s *Store, name string) string {
	t.Helper()
	g, err := s.CreateGroup(name)
	if err != nil {
		t.Fatalf("create group %q: %v", name, err)
	}
	return g.Key
}

// mustSession inserts a session in cwd and gives it groups.
func mustSession(t *testing.T, s *Store, id, cwd string, keys ...string) {
	t.Helper()
	if err := s.UpsertSession(&Session{SessionID: id, Source: "claude", CWD: cwd, Status: "idle"}); err != nil {
		t.Fatalf("upsert %s: %v", id, err)
	}
	if len(keys) > 0 {
		if err := s.SetSessionGroups(id, keys); err != nil {
			t.Fatalf("set groups on %s: %v", id, err)
		}
	}
}

// groupsOf returns the keys a session holds, in order.
func groupsOf(t *testing.T, s *Store, id string) []string {
	t.Helper()
	sessions, err := s.SearchSessions(SessionQuery{Grouped: true})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, sess := range sessions {
		if sess.SessionID != id {
			continue
		}
		keys := make([]string, 0, len(sess.Groups))
		for _, g := range sess.Groups {
			keys = append(keys, g.Key)
		}
		return keys
	}
	t.Fatalf("no session %s", id)
	return nil
}

func TestCreateGroup_AppendsToTheOrder(t *testing.T) {
	s := setupTestStore(t)

	first := mustGroup(t, s, "Work")
	second := mustGroup(t, s, "Side")

	groups, err := s.ListGroups()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("got %d groups, want 2", len(groups))
	}
	if groups[0].Key != first || groups[0].Position != 0 {
		t.Errorf("first = %+v, want %s at 0", groups[0], first)
	}
	if groups[1].Key != second || groups[1].Position != 1 {
		t.Errorf("second = %+v, want %s at 1", groups[1], second)
	}
}

func TestCreateGroup_RefusesAnEmptyName(t *testing.T) {
	s := setupTestStore(t)
	if _, err := s.CreateGroup("   "); err == nil {
		t.Fatal("accepted a group with no name")
	}
}

// A rename must not disturb the arrangement: the key is what every session and
// every position refers to.
func TestRenameGroup_KeepsKeyAndPosition(t *testing.T) {
	s := setupTestStore(t)
	work := mustGroup(t, s, "Work")
	mustGroup(t, s, "Side")
	mustSession(t, s, "s1", "/x/a", work)

	if err := s.RenameGroup(work, "Client work"); err != nil {
		t.Fatalf("rename: %v", err)
	}

	groups, _ := s.ListGroups()
	if groups[0].Key != work || groups[0].Name != "Client work" || groups[0].Position != 0 {
		t.Errorf("after rename: %+v", groups[0])
	}
	if got := groupsOf(t, s, "s1"); len(got) != 1 || got[0] != work {
		t.Errorf("session lost its group: %v", got)
	}
}

func TestSetGroupOrder_RenumbersFromZero(t *testing.T) {
	s := setupTestStore(t)
	work := mustGroup(t, s, "Work")
	side := mustGroup(t, s, "Side")
	spike := mustGroup(t, s, "Spike")

	if err := s.SetGroupOrder([]string{spike, work, side}); err != nil {
		t.Fatalf("order: %v", err)
	}

	groups, _ := s.ListGroups()
	want := []string{spike, work, side}
	for i, g := range groups {
		if g.Key != want[i] || g.Position != i {
			t.Errorf("position %d = %+v, want %s", i, g, want[i])
		}
	}
}

// A group whose sessions are all hidden behind the terminated filter is missing
// from the list the client posts. Dropping it to the end would lose an
// arrangement the user never touched, so the daemon completes the list itself.
func TestSetGroupOrder_KeepsGroupsTheClientDidNotMention(t *testing.T) {
	s := setupTestStore(t)
	work := mustGroup(t, s, "Work")
	hidden := mustGroup(t, s, "Hidden")
	side := mustGroup(t, s, "Side")

	if err := s.SetGroupOrder([]string{side, work}); err != nil {
		t.Fatalf("order: %v", err)
	}

	groups, _ := s.ListGroups()
	if len(groups) != 3 {
		t.Fatalf("got %d groups, want 3", len(groups))
	}
	if groups[0].Key != side || groups[1].Key != work {
		t.Errorf("named groups not honoured: %+v", groups)
	}
	if groups[2].Key != hidden {
		t.Errorf("unmentioned group = %s, want %s appended", groups[2].Key, hidden)
	}
}

func TestSetSessionGroups_RoundTripsInOrder(t *testing.T) {
	s := setupTestStore(t)
	work := mustGroup(t, s, "Work")
	opal := mustGroup(t, s, "opal-app")
	mustSession(t, s, "s1", "/x/a", work, opal)

	got := groupsOf(t, s, "s1")
	if len(got) != 2 || got[0] != work || got[1] != opal {
		t.Fatalf("groups = %v, want [%s %s] in that order", got, work, opal)
	}
}

func TestSetSessionGroups_Rejects(t *testing.T) {
	s := setupTestStore(t)
	work := mustGroup(t, s, "Work")
	a := mustGroup(t, s, "A")
	b := mustGroup(t, s, "B")
	mustSession(t, s, "s1", "/x/a")

	cases := map[string]struct {
		keys []string
		want string
	}{
		"a group twice":      {[]string{work, work}, "once"},
		"too deep":           {[]string{work, a, b, work}, "3 groups"},
		"unknown key":        {[]string{"g_nope"}, "no group"},
		"an empty key":       {[]string{""}, "cannot be empty"},
		"a hole in the list": {[]string{work, ""}, "cannot be empty"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := s.SetSessionGroups("s1", tc.keys)
			if err == nil {
				t.Fatalf("accepted %v", tc.keys)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestSetSessionGroups_EmptyClears(t *testing.T) {
	s := setupTestStore(t)
	work := mustGroup(t, s, "Work")
	mustSession(t, s, "s1", "/x/a", work)

	if err := s.SetSessionGroups("s1", nil); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got := groupsOf(t, s, "s1"); len(got) != 0 {
		t.Errorf("groups = %v, want none", got)
	}
}

// Deleting a group has to reach the sessions holding it, or they render under a
// header that no longer exists.
func TestDeleteGroup_ClearsItFromSessions(t *testing.T) {
	s := setupTestStore(t)
	work := mustGroup(t, s, "Work")
	opal := mustGroup(t, s, "opal-app")
	mustSession(t, s, "s1", "/x/a", work, opal)
	mustSession(t, s, "s2", "/x/b", opal)

	if err := s.DeleteGroup(opal); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if got := groupsOf(t, s, "s1"); len(got) != 1 || got[0] != work {
		t.Errorf("s1 groups = %v, want just %s", got, work)
	}
	if got := groupsOf(t, s, "s2"); len(got) != 0 {
		t.Errorf("s2 groups = %v, want none", got)
	}
}

// The whole point of manual grouping being bearable: assign a directory once,
// and every later agent started there joins on its own.
func TestUpsertSession_InheritsGroupsFromTheSameDirectory(t *testing.T) {
	s := setupTestStore(t)
	work := mustGroup(t, s, "Work")
	opal := mustGroup(t, s, "opal-app")
	mustSession(t, s, "first", "/x/opal", work, opal)

	mustSession(t, s, "second", "/x/opal")
	if got := groupsOf(t, s, "second"); len(got) != 2 || got[0] != work || got[1] != opal {
		t.Errorf("second did not inherit: %v", got)
	}

	mustSession(t, s, "elsewhere", "/x/other")
	if got := groupsOf(t, s, "elsewhere"); len(got) != 0 {
		t.Errorf("a new directory inherited %v", got)
	}
}

// Membership is a snapshot. Reorganising later must not rewrite the sessions
// that already ran.
func TestUpsertSession_DoesNotRewriteGroupsOnUpdate(t *testing.T) {
	s := setupTestStore(t)
	work := mustGroup(t, s, "Work")
	side := mustGroup(t, s, "Side")
	mustSession(t, s, "s1", "/x/a", work)

	// A later session in the same directory files it differently.
	mustSession(t, s, "s2", "/x/a")
	if err := s.SetSessionGroups("s2", []string{side}); err != nil {
		t.Fatalf("regroup: %v", err)
	}

	// Updating s1 must leave its own grouping alone.
	if err := s.UpsertSession(&Session{SessionID: "s1", Source: "claude", CWD: "/x/a", Status: "active"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if got := groupsOf(t, s, "s1"); len(got) != 1 || got[0] != work {
		t.Errorf("s1 groups = %v, want just %s", got, work)
	}
}

// json_each rather than a LIKE over the raw array: a substring match would find
// a key inside a longer one.
func TestSearchSessions_GroupKeyMatchesAtAnyDepthAndNotBySubstring(t *testing.T) {
	s := setupTestStore(t)
	work := mustGroup(t, s, "Work")
	opal := mustGroup(t, s, "opal-app")
	mustSession(t, s, "outer", "/x/a", work)
	mustSession(t, s, "inner", "/x/b", work, opal)
	mustSession(t, s, "neither", "/x/c")

	found, err := s.SearchSessions(SessionQuery{Grouped: true, GroupKey: opal})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(found) != 1 || found[0].SessionID != "inner" {
		t.Fatalf("group filter returned %d rows, want just inner", len(found))
	}

	// A prefix of a real key must match nothing.
	short, err := s.SearchSessions(SessionQuery{GroupKey: opal[:len(opal)-2]})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(short) != 0 {
		t.Errorf("a truncated key matched %d sessions", len(short))
	}
}

// Ungrouped is not a stored group, so nothing should claim it.
func TestSearchSessions_UngroupedCarriesNoGroups(t *testing.T) {
	s := setupTestStore(t)
	mustSession(t, s, "s1", "/x/a")

	sessions, err := s.SearchSessions(SessionQuery{Grouped: true})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(sessions) != 1 || sessions[0].Groups != nil {
		t.Errorf("groups = %+v, want nil", sessions[0].Groups)
	}
}

// A caller that did not ask for grouping is served exactly what it always was.
func TestSearchSessions_UngroupedQueryOmitsTheField(t *testing.T) {
	s := setupTestStore(t)
	work := mustGroup(t, s, "Work")
	mustSession(t, s, "s1", "/x/a", work)

	sessions, err := s.SearchSessions(SessionQuery{})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if sessions[0].Groups != nil {
		t.Errorf("groups served without being asked for: %+v", sessions[0].Groups)
	}
}

// Positions travel with the session so the client needs no lookup table.
func TestSearchSessions_ResolvesNamesAndPositions(t *testing.T) {
	s := setupTestStore(t)
	work := mustGroup(t, s, "Work")
	opal := mustGroup(t, s, "opal-app")
	mustSession(t, s, "s1", "/x/a", work, opal)

	sessions, err := s.SearchSessions(SessionQuery{Grouped: true})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	got := sessions[0].Groups
	if len(got) != 2 {
		t.Fatalf("groups = %+v, want 2", got)
	}
	if got[0].Name != "Work" || got[0].Position != 0 {
		t.Errorf("outermost = %+v, want Work at 0", got[0])
	}
	if got[1].Name != "opal-app" || got[1].Position != 1 {
		t.Errorf("inner = %+v, want opal-app at 1", got[1])
	}
}
