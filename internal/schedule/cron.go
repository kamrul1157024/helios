// Cron expressions, parsed and asked when they next fire.
//
// Written rather than imported because the two rules that actually bite are
// rules worth having tests for in our own tree:
//
//   - Day-of-month and day-of-week are OR when both are restricted. `0 9 13 * 5`
//     is the thirteenth *and* every Friday, which surprises everyone once.
//   - Local time is what a person means by "nine", so the walk below steps
//     through wall-clock components rather than adding durations. On the spring
//     forward a wall time that does not exist is skipped; on the autumn back a
//     wall time that happens twice fires once.
package schedule

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Cron is a parsed five-field expression.
//
// Each field is a bitmask over its own range, which makes matching a shift and
// an AND rather than a search.
type Cron struct {
	minutes uint64 // 0-59
	hours   uint64 // 0-23
	doms    uint64 // 1-31
	months  uint64 // 1-12
	dows    uint64 // 0-6, Sunday is 0

	// Whether each day field was narrowed, which is what decides between AND
	// and OR below.
	domRestricted bool
	dowRestricted bool

	expr string
}

func (c *Cron) String() string { return c.expr }

// How far Next will look before calling an expression impossible. Four years
// clears a leap day from any starting point.
const horizon = 4 * 366 * 24 * time.Hour

var shorthands = map[string]string{
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
	"@monthly":  "0 0 1 * *",
	"@weekly":   "0 0 * * 0",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@hourly":   "0 * * * *",
}

var monthNames = map[string]int{
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
}

var dayNames = map[string]int{
	"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
}

// Parse reads a five-field expression, or one of the @shorthands.
func Parse(expr string) (*Cron, error) {
	raw := strings.TrimSpace(expr)
	if raw == "" {
		return nil, fmt.Errorf("empty expression")
	}
	if expanded, ok := shorthands[strings.ToLower(raw)]; ok {
		raw = expanded
	}

	fields := strings.Fields(raw)
	if len(fields) != 5 {
		return nil, fmt.Errorf("want 5 fields (minute hour day-of-month month day-of-week), got %d", len(fields))
	}

	c := &Cron{expr: strings.TrimSpace(expr)}
	var err error
	if c.minutes, _, err = parseField(fields[0], 0, 59, nil); err != nil {
		return nil, fmt.Errorf("minute: %w", err)
	}
	if c.hours, _, err = parseField(fields[1], 0, 23, nil); err != nil {
		return nil, fmt.Errorf("hour: %w", err)
	}
	if c.doms, c.domRestricted, err = parseField(fields[2], 1, 31, nil); err != nil {
		return nil, fmt.Errorf("day of month: %w", err)
	}
	if c.months, _, err = parseField(fields[3], 1, 12, monthNames); err != nil {
		return nil, fmt.Errorf("month: %w", err)
	}
	if c.dows, c.dowRestricted, err = parseField(fields[4], 0, 6, dayNames); err != nil {
		return nil, fmt.Errorf("day of week: %w", err)
	}
	return c, nil
}

// parseField turns one field into a bitmask, and reports whether it narrowed
// anything — `*` and `*/1` cover everything and do not.
func parseField(field string, min, max int, names map[string]int) (uint64, bool, error) {
	var mask uint64
	restricted := false

	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return 0, false, fmt.Errorf("empty item in %q", field)
		}

		step := 1
		if slash := strings.Index(part, "/"); slash >= 0 {
			var err error
			step, err = strconv.Atoi(part[slash+1:])
			if err != nil || step < 1 {
				return 0, false, fmt.Errorf("bad step in %q", part)
			}
			part = part[:slash]
			if step > 1 {
				restricted = true
			}
		}

		lo, hi := min, max
		switch {
		case part == "*":
			// The whole range, already set above.
		case strings.Contains(part, "-"):
			bounds := strings.SplitN(part, "-", 2)
			var err error
			if lo, err = parseValue(bounds[0], min, max, names); err != nil {
				return 0, false, err
			}
			if hi, err = parseValue(bounds[1], min, max, names); err != nil {
				return 0, false, err
			}
			if lo > hi {
				return 0, false, fmt.Errorf("range %q runs backwards", part)
			}
			restricted = true
		default:
			v, err := parseValue(part, min, max, names)
			if err != nil {
				return 0, false, err
			}
			lo, hi = v, v
			restricted = true
		}

		for v := lo; v <= hi; v += step {
			mask |= 1 << uint(v)
		}
	}

	if mask == 0 {
		return 0, false, fmt.Errorf("%q matches nothing", field)
	}
	return mask, restricted, nil
}

func parseValue(s string, min, max int, names map[string]int) (int, error) {
	s = strings.TrimSpace(s)
	if names != nil {
		if v, ok := names[strings.ToLower(s)]; ok {
			return v, nil
		}
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", s)
	}
	// Sunday is both 0 and 7 in every cron anyone has used.
	if max == 6 && v == 7 {
		v = 0
	}
	if v < min || v > max {
		return 0, fmt.Errorf("%d is outside %d-%d", v, min, max)
	}
	return v, nil
}

func has(mask uint64, v int) bool { return mask&(1<<uint(v)) != 0 }

// matchesDay applies the rule that catches people out: when both day fields are
// narrowed the expression fires on *either*, not on both.
func (c *Cron) matchesDay(t time.Time) bool {
	dom := has(c.doms, t.Day())
	dow := has(c.dows, int(t.Weekday()))
	switch {
	case c.domRestricted && c.dowRestricted:
		return dom || dow
	case c.domRestricted:
		return dom
	case c.dowRestricted:
		return dow
	default:
		return true
	}
}

// Next returns the first firing strictly after `after`, in that time's own
// location, and false when the expression can never fire again.
//
// The walk is over wall-clock components rather than added durations, which is
// what makes it right across a daylight-saving change: `time.Date` normalises a
// local time that does not exist — 02:30 on a spring-forward morning becomes
// 03:30 — and a normalised result is not the minute we asked for, so it is
// skipped rather than fired at the wrong hour.
func (c *Cron) Next(after time.Time) (time.Time, bool) {
	loc := after.Location()
	limit := after.Add(horizon)

	// Start at the next whole minute: "after" is exclusive, and a schedule that
	// fires at 09:00 must not fire again at 09:00:30.
	t := after.Truncate(time.Minute).Add(time.Minute)
	y, mo, d := t.Date()
	year, month, day := y, int(mo), d
	hour, minute := t.Hour(), t.Minute()

	for {
		when := time.Date(year, time.Month(month), day, hour, minute, 0, 0, loc)
		if when.After(limit) {
			return time.Time{}, false
		}

		// Normalisation moved it: either a day that does not exist in this
		// month (31 September) or a local time the clocks skipped.
		normalised := when.Year() != year || int(when.Month()) != month ||
			when.Day() != day || when.Hour() != hour || when.Minute() != minute

		switch {
		case normalised && (when.Year() != year || int(when.Month()) != month || when.Day() != day):
			// Past the end of the month. Start the next one.
			year, month, day, hour, minute = rollMonth(year, month)
		case normalised:
			// A skipped wall clock hour. Try the next minute.
			year, month, day, hour, minute = rollMinute(year, month, day, hour, minute, loc)
		case !has(c.months, month):
			year, month, day, hour, minute = rollMonth(year, month)
		case !c.matchesDay(when):
			year, month, day, hour, minute = rollDay(year, month, day, loc)
		case !has(c.hours, hour):
			hour, minute = hour+1, 0
			if hour > 23 {
				year, month, day, hour, minute = rollDay(year, month, day, loc)
			}
		case !has(c.minutes, minute):
			year, month, day, hour, minute = rollMinute(year, month, day, hour, minute, loc)
		default:
			return when, true
		}
	}
}

func rollMonth(year, month int) (int, int, int, int, int) {
	month++
	if month > 12 {
		year, month = year+1, 1
	}
	return year, month, 1, 0, 0
}

func rollDay(year, month, day int, loc *time.Location) (int, int, int, int, int) {
	next := time.Date(year, time.Month(month), day, 12, 0, 0, 0, loc).AddDate(0, 0, 1)
	return next.Year(), int(next.Month()), next.Day(), 0, 0
}

func rollMinute(year, month, day, hour, minute int, loc *time.Location) (int, int, int, int, int) {
	minute++
	if minute <= 59 {
		return year, month, day, hour, minute
	}
	hour, minute = hour+1, 0
	if hour <= 23 {
		return year, month, day, hour, minute
	}
	return rollDay(year, month, day, loc)
}
