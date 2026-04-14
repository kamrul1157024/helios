package server

import (
	"testing"
)

func TestPendingPaneMap_AddAndRemoveByCWD(t *testing.T) {
	m := NewPendingPaneMap()
	m.Add("%5", "/tmp/project")

	paneID := m.RemoveByCWD("/tmp/project")
	if paneID != "%5" {
		t.Errorf("RemoveByCWD = %q, want %%5", paneID)
	}

	// Should be gone after removal.
	paneID = m.RemoveByCWD("/tmp/project")
	if paneID != "" {
		t.Errorf("RemoveByCWD after removal = %q, want empty", paneID)
	}
}

func TestPendingPaneMap_RemoveByCWD_NoMatch(t *testing.T) {
	m := NewPendingPaneMap()
	m.Add("%5", "/tmp/project-a")

	paneID := m.RemoveByCWD("/tmp/project-b")
	if paneID != "" {
		t.Errorf("RemoveByCWD mismatched CWD = %q, want empty", paneID)
	}

	// Original should still be there.
	paneID = m.RemoveByCWD("/tmp/project-a")
	if paneID != "%5" {
		t.Errorf("RemoveByCWD original = %q, want %%5", paneID)
	}
}

func TestPendingPaneMap_MultiplePanes(t *testing.T) {
	m := NewPendingPaneMap()
	m.Add("%1", "/tmp/project-a")
	m.Add("%2", "/tmp/project-b")
	m.Add("%3", "/tmp/project-a") // same CWD as %1

	// Should return one of the panes with matching CWD.
	paneID := m.RemoveByCWD("/tmp/project-a")
	if paneID != "%1" && paneID != "%3" {
		t.Errorf("RemoveByCWD = %q, want %%1 or %%3", paneID)
	}

	// Second call should return the other one.
	paneID2 := m.RemoveByCWD("/tmp/project-a")
	if paneID2 == "" {
		t.Error("RemoveByCWD second call = empty, want remaining pane")
	}
	if paneID2 == paneID {
		t.Errorf("RemoveByCWD returned same pane %q twice", paneID)
	}
}

func TestContainsTrustPrompt(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"Do you trust this folder? Yes, I trust this folder", true},
		{"Quick safety check for workspace", true},
		{"normal output without trust prompt", false},
		{"", false},
	}

	for _, tt := range tests {
		got := containsTrustPrompt(tt.input)
		if got != tt.want {
			t.Errorf("containsTrustPrompt(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
