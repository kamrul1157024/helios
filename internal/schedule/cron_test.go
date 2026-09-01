package schedule

import (
	"testing"
	"time"
)

func mustParse(t *testing.T, expr string) *Cron {
	t.Helper()
	c, err := Parse(expr)
	if err != nil {
		t.Fatalf("Parse(%q): %v", expr, err)
	}
	return c
}

func at(t *testing.T, loc *time.Location, s string) time.Time {
	t.Helper()
	when, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
	if err != nil {
		t.Fatalf("bad time %q: %v", s, err)
	}
	return when
}

func TestParseRejects(t *testing.T) {
	for _, expr := range []string{
		"",
		"* * * *",          // four fields
		"* * * * * *",      // six
		"60 * * * *",       // minute out of range
		"* 24 * * *",       // hour out of range
		"* * 0 * *",        // day of month starts at 1
		"* * * 13 *",       // month out of range
		"* * * * 8",        // day of week out of range
		"5-1 * * * *",      // backwards range
		"*/0 * * * *",      // zero step
		"* * * jan-dec 99", // day of week out of range with names
		"nonsense * * * *", // not a number
		"1,,2 * * * *",     // empty item
	} {
		if _, err := Parse(expr); err == nil {
			t.Errorf("Parse(%q) should have failed", expr)
		}
	}
}

func TestParseAccepts(t *testing.T) {
	for _, expr := range []string{
		"* * * * *",
		"0 9 * * 1-5",
		"*/15 * * * *",
		"0 0,12 * * *",
		"30 8 1,15 * *",
		"0 4 * jan,jul MON",
		"0 0 * * 7", // Sunday, the other way of writing it
		"@daily",
		"@HOURLY",
	} {
		if _, err := Parse(expr); err != nil {
			t.Errorf("Parse(%q): %v", expr, err)
		}
	}
}

func TestNextBasics(t *testing.T) {
	loc := time.UTC
	cases := []struct {
		expr, from, want string
	}{
		{"0 9 * * *", "2026-03-02 08:59", "2026-03-02 09:00"},
		// Exclusive: standing on a firing minute finds the next one.
		{"0 9 * * *", "2026-03-02 09:00", "2026-03-03 09:00"},
		{"*/15 * * * *", "2026-03-02 09:01", "2026-03-02 09:15"},
		{"0 9 * * 1-5", "2026-03-07 10:00", "2026-03-09 09:00"}, // Sat → Mon
		{"30 8 1 * *", "2026-03-02 00:00", "2026-04-01 08:30"},
		{"0 0 29 2 *", "2026-03-01 00:00", "2028-02-29 00:00"}, // next leap year
		{"@daily", "2026-03-02 12:00", "2026-03-03 00:00"},
	}
	for _, tc := range cases {
		got, ok := mustParse(t, tc.expr).Next(at(t, loc, tc.from))
		if !ok {
			t.Errorf("%s from %s: reported never", tc.expr, tc.from)
			continue
		}
		if want := at(t, loc, tc.want); !got.Equal(want) {
			t.Errorf("%s from %s: got %s, want %s", tc.expr, tc.from, got, want)
		}
	}
}

// Both day fields restricted means OR, which is the rule everyone meets once.
func TestNextDayOfMonthOrDayOfWeek(t *testing.T) {
	loc := time.UTC
	c := mustParse(t, "0 9 13 * 5") // the 13th, and every Friday

	// 2026-03-09 is a Monday. The next Friday is the 13th, which is both.
	got, _ := c.Next(at(t, loc, "2026-03-09 00:00"))
	if want := at(t, loc, "2026-03-13 09:00"); !got.Equal(want) {
		t.Fatalf("got %s, want %s", got, want)
	}

	// From the 14th: the next hit is Friday the 20th, by day of week alone.
	got, _ = c.Next(at(t, loc, "2026-03-14 00:00"))
	if want := at(t, loc, "2026-03-20 09:00"); !got.Equal(want) {
		t.Fatalf("got %s, want %s", got, want)
	}

	// April the 13th is a Monday, and still fires — by day of month alone.
	got, _ = c.Next(at(t, loc, "2026-04-11 00:00"))
	if want := at(t, loc, "2026-04-13 09:00"); !got.Equal(want) {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// One field restricted means that field alone decides.
func TestNextSingleDayFieldIsAnd(t *testing.T) {
	loc := time.UTC
	// Day of month only: every 13th whatever the weekday.
	got, _ := mustParse(t, "0 9 13 * *").Next(at(t, loc, "2026-04-01 00:00"))
	if want := at(t, loc, "2026-04-13 09:00"); !got.Equal(want) {
		t.Fatalf("dom only: got %s, want %s", got, want)
	}
	// Day of week only: the next Friday.
	got, _ = mustParse(t, "0 9 * * 5").Next(at(t, loc, "2026-04-01 00:00"))
	if want := at(t, loc, "2026-04-03 09:00"); !got.Equal(want) {
		t.Fatalf("dow only: got %s, want %s", got, want)
	}
}

func TestNextNever(t *testing.T) {
	// February the thirtieth. Rejected as a schedule, and the parser is what
	// says so — the expression itself is well formed.
	c := mustParse(t, "0 0 30 2 *")
	if when, ok := c.Next(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)); ok {
		t.Fatalf("30 February should never fire, got %s", when)
	}
}

// Spring forward: 02:30 does not exist on that morning in New York, so a daily
// 02:30 schedule must skip to the next day rather than fire at 03:30.
func TestNextSpringForward(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tzdata")
	}
	// 2026-03-08 is the US spring forward: 02:00 jumps to 03:00.
	got, ok := mustParse(t, "30 2 * * *").Next(at(t, loc, "2026-03-07 12:00"))
	if !ok {
		t.Fatal("reported never")
	}
	if want := at(t, loc, "2026-03-09 02:30"); !got.Equal(want) {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// Autumn back: 01:30 happens twice. It must fire once, on the first pass.
func TestNextFallBackFiresOnce(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tzdata")
	}
	// 2026-11-01 is the US fall back: 02:00 returns to 01:00.
	first, ok := mustParse(t, "30 1 * * *").Next(at(t, loc, "2026-11-01 00:00"))
	if !ok {
		t.Fatal("reported never")
	}
	if h := first.Hour(); h != 1 {
		t.Fatalf("got hour %d, want 01:30", h)
	}
	// The next one is the following day, not the repeat of the same wall time.
	second, _ := mustParse(t, "30 1 * * *").Next(first)
	if second.Day() == first.Day() {
		t.Fatalf("fired twice on the same day: %s then %s", first, second)
	}
}

func TestNextEveryMinuteWalksForward(t *testing.T) {
	loc := time.UTC
	c := mustParse(t, "* * * * *")
	from := at(t, loc, "2026-03-02 23:59")
	got, _ := c.Next(from)
	if want := at(t, loc, "2026-03-03 00:00"); !got.Equal(want) {
		t.Fatalf("got %s, want %s", got, want)
	}
}
