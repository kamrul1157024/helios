package store

import "testing"

func TestReviewed_RoundTripAndToggleOff(t *testing.T) {
	s := setupTestStore(t)

	if err := s.MarkReviewed("/repo", "main", "a.go", true); err != nil {
		t.Fatalf("mark: %v", err)
	}
	if err := s.MarkReviewed("/repo", "main", "b.go", true); err != nil {
		t.Fatalf("mark: %v", err)
	}

	got, err := s.ReviewedFiles("/repo", "main")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 || got[0] != "a.go" || got[1] != "b.go" {
		t.Fatalf("got %v, want [a.go b.go]", got)
	}

	// Marking twice must not duplicate, and unmarking must actually forget.
	if err := s.MarkReviewed("/repo", "main", "a.go", true); err != nil {
		t.Fatalf("re-mark: %v", err)
	}
	if err := s.MarkReviewed("/repo", "main", "b.go", false); err != nil {
		t.Fatalf("unmark: %v", err)
	}
	got, _ = s.ReviewedFiles("/repo", "main")
	if len(got) != 1 || got[0] != "a.go" {
		t.Fatalf("got %v, want [a.go]", got)
	}
}

// The same file read against two bases is two different readings. Carrying one
// over would tell a reviewer they had seen a diff they have not.
func TestReviewed_IsPerBaseAndPerRepo(t *testing.T) {
	s := setupTestStore(t)

	if err := s.MarkReviewed("/repo", "main", "a.go", true); err != nil {
		t.Fatalf("mark: %v", err)
	}

	for _, tc := range []struct{ root, base string }{
		{"/repo", "release"},
		{"/other", "main"},
	} {
		got, err := s.ReviewedFiles(tc.root, tc.base)
		if err != nil {
			t.Fatalf("list %s %s: %v", tc.root, tc.base, err)
		}
		if len(got) != 0 {
			t.Errorf("%s@%s leaked %v", tc.root, tc.base, got)
		}
	}
}

func TestReviewed_ClearForgetsTheWholeReview(t *testing.T) {
	s := setupTestStore(t)

	s.MarkReviewed("/repo", "main", "a.go", true)
	s.MarkReviewed("/repo", "release", "a.go", true)
	if err := s.ClearReviewed("/repo", "main"); err != nil {
		t.Fatalf("clear: %v", err)
	}

	if got, _ := s.ReviewedFiles("/repo", "main"); len(got) != 0 {
		t.Errorf("main not cleared: %v", got)
	}
	if got, _ := s.ReviewedFiles("/repo", "release"); len(got) != 1 {
		t.Error("clearing one base cleared another")
	}
}
