package store

import "testing"

// A malformed setting must fall back to the default, not to "no limit". The
// unsafe direction is silently disabling the budget.
func TestMemoryBudgetFraction(t *testing.T) {
	s := setupTestStore(t)

	if got := s.MemoryBudgetFraction(); got != DefaultBudgetFraction {
		t.Errorf("unset = %v, want %v", got, DefaultBudgetFraction)
	}

	for _, tc := range []struct {
		raw  string
		want float64
	}{
		{"0.5", 0.5},
		{"1", 1},
		// Zero is a real choice — "no limit" — rather than a parse failure.
		{"0", 0},
		{"nonsense", DefaultBudgetFraction},
		{"-0.5", DefaultBudgetFraction},
		{"12", DefaultBudgetFraction},
		{"", DefaultBudgetFraction},
	} {
		if err := s.SetSetting(SettingBudgetFraction, tc.raw); err != nil {
			t.Fatalf("set %q: %v", tc.raw, err)
		}
		if got := s.MemoryBudgetFraction(); got != tc.want {
			t.Errorf("%q = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

func TestEvictionEnabled(t *testing.T) {
	s := setupTestStore(t)

	if !s.EvictionEnabled() {
		t.Error("eviction is off by default; it should be on")
	}
	if err := s.SetSetting(SettingEvictEnabled, "false"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if s.EvictionEnabled() {
		t.Error("eviction stayed on after being turned off")
	}
}
