package resident

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// DefaultTimezone is used when a schedule names none.
const DefaultTimezone = "Asia/Tokyo"

// Schedule kinds.
const (
	ScheduleManual = "manual" // only manual runs, follow-ups and self wake-ups
	ScheduleOnce   = "once"
	ScheduleDaily  = "daily"
	ScheduleWeekly = "weekly"
	ScheduleCron   = "cron"
)

// Schedule says when a task runs on its own. Times are wall-clock times in
// Timezone; instants are stored in UTC.
type Schedule struct {
	Kind string `json:"kind"`
	// At is the instant of a one-off run (RFC 3339).
	At *time.Time `json:"at,omitempty"`
	// Time is "HH:MM" for daily and weekly schedules.
	Time string `json:"time,omitempty"`
	// Days are weekdays ("mon".."sun") for weekly schedules.
	Days []string `json:"days,omitempty"`
	// Cron is a five-field cron expression (minute hour day month weekday).
	Cron     string `json:"cron,omitempty"`
	Timezone string `json:"timezone,omitempty"`
}

var errSchedule = errors.New("invalid schedule")

// ScheduleError explains why a schedule was refused.
func scheduleErr(format string, a ...any) error {
	return fmt.Errorf("%w: %s", errSchedule, fmt.Sprintf(format, a...))
}

var weekdayNames = map[string]time.Weekday{
	"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
	"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
}

// compiled is a validated schedule ready to compute occurrences.
type compiled struct {
	s    Schedule
	loc  *time.Location
	cron *cronExpr
}

// Normalize validates s and fills its defaults.
func (s Schedule) Normalize() (Schedule, error) {
	c, err := compile(s)
	if err != nil {
		return Schedule{}, err
	}
	return c.s, nil
}

func compile(s Schedule) (*compiled, error) {
	if s.Timezone == "" {
		s.Timezone = DefaultTimezone
	}
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil || s.Timezone == "Local" {
		return nil, scheduleErr("unknown timezone %q (use an IANA name such as Asia/Tokyo)", s.Timezone)
	}
	c := &compiled{loc: loc}
	switch s.Kind {
	case ScheduleManual:
		s = Schedule{Kind: ScheduleManual, Timezone: s.Timezone}
	case ScheduleOnce:
		if s.At == nil || s.At.IsZero() {
			return nil, scheduleErr("a one-off schedule needs at (RFC 3339)")
		}
		at := s.At.UTC().Truncate(time.Minute)
		s = Schedule{Kind: ScheduleOnce, At: &at, Timezone: s.Timezone}
	case ScheduleDaily, ScheduleWeekly:
		if _, _, err := parseClock(s.Time); err != nil {
			return nil, err
		}
		if s.Kind == ScheduleDaily {
			s = Schedule{Kind: ScheduleDaily, Time: s.Time, Timezone: s.Timezone}
			break
		}
		if len(s.Days) == 0 {
			return nil, scheduleErr("a weekly schedule needs at least one day")
		}
		var days []string
		for _, d := range s.Days {
			d = strings.ToLower(strings.TrimSpace(d))
			if len(d) > 3 {
				d = d[:3]
			}
			if _, ok := weekdayNames[d]; !ok {
				return nil, scheduleErr("unknown weekday %q (use mon..sun)", d)
			}
			if !slices.Contains(days, d) {
				days = append(days, d)
			}
		}
		slices.SortFunc(days, func(a, b string) int { return int(weekdayNames[a]) - int(weekdayNames[b]) })
		s = Schedule{Kind: ScheduleWeekly, Time: s.Time, Days: days, Timezone: s.Timezone}
	case ScheduleCron:
		e, err := parseCron(s.Cron)
		if err != nil {
			return nil, err
		}
		c.cron = e
		s = Schedule{Kind: ScheduleCron, Cron: strings.Join(strings.Fields(s.Cron), " "), Timezone: s.Timezone}
	case "":
		return nil, scheduleErr("kind is required (manual, once, daily, weekly or cron)")
	default:
		return nil, scheduleErr("unknown kind %q (manual, once, daily, weekly or cron)", s.Kind)
	}
	c.s = s
	return c, nil
}

func parseClock(v string) (int, int, error) {
	h, m, ok := strings.Cut(v, ":")
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if !ok || err1 != nil || err2 != nil || len(m) != 2 || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, 0, scheduleErr("time must be HH:MM (00:00-23:59), got %q", v)
	}
	return hh, mm, nil
}

// Next returns the first occurrence strictly after after, or false when
// there is none (manual, or a one-off in the past).
func (s Schedule) Next(after time.Time) (time.Time, bool) {
	c, err := compile(s)
	if err != nil {
		return time.Time{}, false
	}
	return c.next(after)
}

// NextN returns up to n occurrences after after.
func (s Schedule) NextN(after time.Time, n int) []time.Time {
	c, err := compile(s)
	if err != nil {
		return nil
	}
	var out []time.Time
	for len(out) < n {
		t, ok := c.next(after)
		if !ok {
			break
		}
		out = append(out, t)
		after = t
	}
	return out
}

// searchDays bounds the search for the next occurrence (a cron expression
// such as "0 0 31 2 *" never matches).
const searchDays = 366 * 5

func (c *compiled) next(after time.Time) (time.Time, bool) {
	after = after.UTC()
	switch c.s.Kind {
	case ScheduleOnce:
		if c.s.At.After(after) {
			return *c.s.At, true
		}
		return time.Time{}, false
	case ScheduleDaily, ScheduleWeekly:
		hh, mm, _ := parseClock(c.s.Time)
		day := after.In(c.loc)
		y, mo, d := day.Date()
		for i := -1; i < searchDays; i++ {
			date := time.Date(y, mo, d+i, 12, 0, 0, 0, c.loc)
			if c.s.Kind == ScheduleWeekly && !slices.Contains(c.s.Days, weekdayKey(date.Weekday())) {
				continue
			}
			if t := resolveWall(date.Year(), date.Month(), date.Day(), hh, mm, c.loc); t.After(after) {
				return t, true
			}
		}
	case ScheduleCron:
		return c.cron.next(after, c.loc)
	}
	return time.Time{}, false
}

func weekdayKey(w time.Weekday) string {
	for k, v := range weekdayNames {
		if v == w {
			return k
		}
	}
	return ""
}

// resolveWall turns a wall-clock time in loc into an instant.
//
// Daylight saving time: a time that occurs twice (the clocks go back) is the
// first of the two; a time that does not exist (the clocks go forward) is
// moved later by the length of the gap (02:30 becomes 03:30), as
// java.time does.
func resolveWall(y int, mo time.Month, d, hh, mm int, loc *time.Location) time.Time {
	wall := time.Date(y, mo, d, hh, mm, 0, 0, time.UTC)
	var offsets []int
	for _, probe := range []time.Duration{-36 * time.Hour, -12 * time.Hour, 0, 12 * time.Hour, 36 * time.Hour} {
		_, off := wall.Add(probe).In(loc).Zone()
		if !slices.Contains(offsets, off) {
			offsets = append(offsets, off)
		}
	}
	var best time.Time
	for _, off := range offsets {
		cand := wall.Add(-time.Duration(off) * time.Second)
		l := cand.In(loc)
		if l.Year() == y && l.Month() == mo && l.Day() == d && l.Hour() == hh && l.Minute() == mm {
			if best.IsZero() || cand.Before(best) {
				best = cand
			}
		}
	}
	if !best.IsZero() {
		return best.UTC()
	}
	// In a gap: read the wall time with the offset in force before it.
	_, before := wall.Add(-36 * time.Hour).In(loc).Zone()
	return wall.Add(-time.Duration(before) * time.Second).UTC()
}

// --- cron ---

type cronExpr struct {
	minutes, hours, doms, months, dows []bool
	domStar, dowStar                   bool
}

// parseCron reads a standard five-field expression: minute hour
// day-of-month month day-of-week, with *, lists, ranges, steps and month /
// weekday names. Day 7 is Sunday too. When both day fields are restricted a
// day matching either runs, as in Vixie cron.
func parseCron(expr string) (*cronExpr, error) {
	f := strings.Fields(expr)
	if len(f) != 5 {
		return nil, scheduleErr("cron needs 5 fields (minute hour day month weekday), got %d", len(f))
	}
	var e cronExpr
	var err error
	if e.minutes, _, err = cronField(f[0], 0, 59, nil, "minute"); err != nil {
		return nil, err
	}
	if e.hours, _, err = cronField(f[1], 0, 23, nil, "hour"); err != nil {
		return nil, err
	}
	if e.doms, e.domStar, err = cronField(f[2], 1, 31, nil, "day of month"); err != nil {
		return nil, err
	}
	months := map[string]int{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12}
	if e.months, _, err = cronField(f[3], 1, 12, months, "month"); err != nil {
		return nil, err
	}
	days := map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}
	if e.dows, e.dowStar, err = cronField(f[4], 0, 7, days, "day of week"); err != nil {
		return nil, err
	}
	if e.dows[7] {
		e.dows[0] = true
	}
	return &e, nil
}

func cronField(field string, lo, hi int, names map[string]int, what string) ([]bool, bool, error) {
	set := make([]bool, hi+1)
	star := field == "*" || strings.HasPrefix(field, "*/")
	value := func(v string) (int, error) {
		if n, ok := names[strings.ToLower(v)]; ok {
			return n, nil
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < lo || n > hi {
			return 0, scheduleErr("cron %s %q is out of range %d-%d", what, v, lo, hi)
		}
		return n, nil
	}
	for _, part := range strings.Split(field, ",") {
		rng, stepS, hasStep := strings.Cut(part, "/")
		step := 1
		if hasStep {
			n, err := strconv.Atoi(stepS)
			if err != nil || n < 1 || n > hi {
				return nil, false, scheduleErr("cron %s step %q is invalid", what, stepS)
			}
			step = n
		}
		var from, to int
		switch {
		case rng == "*":
			from, to = lo, hi
		case strings.Contains(rng, "-"):
			a, b, _ := strings.Cut(rng, "-")
			var err error
			if from, err = value(a); err != nil {
				return nil, false, err
			}
			if to, err = value(b); err != nil {
				return nil, false, err
			}
			if from > to {
				return nil, false, scheduleErr("cron %s range %q is backwards", what, rng)
			}
		case rng == "":
			return nil, false, scheduleErr("cron %s %q is empty", what, field)
		default:
			n, err := value(rng)
			if err != nil {
				return nil, false, err
			}
			from, to = n, n
			if hasStep {
				to = hi
			}
		}
		for v := from; v <= to; v += step {
			set[v] = true
		}
	}
	return set, star, nil
}

func (e *cronExpr) dayMatches(t time.Time) bool {
	dom, dow := e.doms[t.Day()], e.dows[int(t.Weekday())]
	switch {
	case e.domStar && e.dowStar:
		return true
	case e.domStar:
		return dow
	case e.dowStar:
		return dom
	default:
		return dom || dow
	}
}

func (e *cronExpr) next(after time.Time, loc *time.Location) (time.Time, bool) {
	start := after.In(loc)
	y, mo, d := start.Date()
	for i := -1; i < searchDays; i++ {
		date := time.Date(y, mo, d+i, 12, 0, 0, 0, loc)
		if !e.months[int(date.Month())] || !e.dayMatches(date) {
			continue
		}
		var best time.Time
		for h := 0; h < 24; h++ {
			if !e.hours[h] {
				continue
			}
			for m := 0; m < 60; m++ {
				if !e.minutes[m] {
					continue
				}
				t := resolveWall(date.Year(), date.Month(), date.Day(), h, m, loc)
				if t.After(after) && (best.IsZero() || t.Before(best)) {
					best = t
				}
			}
		}
		if !best.IsZero() {
			return best, true
		}
	}
	return time.Time{}, false
}
