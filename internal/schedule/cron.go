// Package schedule implements timezone-aware cron parsing, DST-safe next-tick
// calculation, overlap/catch-up rules, and transactional schedule dispatch.
package schedule

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CronExpr represents a parsed 5-field cron expression (minute hour dom month dow).
type CronExpr struct {
	Raw     string
	Minutes map[int]bool
	Hours   map[int]bool
	DOMs    map[int]bool
	Months  map[int]bool
	DOWs    map[int]bool
}

// ParseCron parses a standard 5-field cron expression.
func ParseCron(expr string) (CronExpr, error) {
	fields := strings.Fields(strings.TrimSpace(expr))
	if len(fields) != 5 {
		return CronExpr{}, fmt.Errorf("cron expression must have 5 fields, got %d", len(fields))
	}
	mins, err := parseField(fields[0], 0, 59)
	if err != nil {
		return CronExpr{}, fmt.Errorf("minute: %w", err)
	}
	hrs, err := parseField(fields[1], 0, 23)
	if err != nil {
		return CronExpr{}, fmt.Errorf("hour: %w", err)
	}
	doms, err := parseField(fields[2], 1, 31)
	if err != nil {
		return CronExpr{}, fmt.Errorf("day-of-month: %w", err)
	}
	months, err := parseField(fields[3], 1, 12)
	if err != nil {
		return CronExpr{}, fmt.Errorf("month: %w", err)
	}
	dows, err := parseField(fields[4], 0, 6)
	if err != nil {
		return CronExpr{}, fmt.Errorf("day-of-week: %w", err)
	}
	return CronExpr{
		Raw:     expr,
		Minutes: mins,
		Hours:   hrs,
		DOMs:    doms,
		Months:  months,
		DOWs:    dows,
	}, nil
}

// Next returns the first UTC instant strictly after after that matches c in loc.
// Advancing in UTC minutes and evaluating local wall-clock fields handles
// DST spring-forward and fall-back transitions without skipping or duplicating instants.
func (c CronExpr) Next(after time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	t := after.UTC().Truncate(time.Minute).Add(time.Minute)
	limit := t.Add(366 * 24 * time.Hour)
	for t.Before(limit) {
		lt := t.In(loc)
		if c.Months[int(lt.Month())] &&
			c.DOMs[lt.Day()] &&
			c.DOWs[int(lt.Weekday())] &&
			c.Hours[lt.Hour()] &&
			c.Minutes[lt.Minute()] {
			return t.UTC()
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}
}

// Describe returns a plain-language summary of the cron expression.
func (c CronExpr) Describe() string {
	fields := strings.Fields(c.Raw)
	if len(fields) != 5 {
		return c.Raw
	}
	if fields[0] == "*" && fields[1] == "*" && fields[2] == "*" && fields[3] == "*" && fields[4] == "*" {
		return "Every minute"
	}
	if strings.HasPrefix(fields[0], "*/") && fields[1] == "*" && fields[2] == "*" && fields[3] == "*" && fields[4] == "*" {
		return "Every " + strings.TrimPrefix(fields[0], "*/") + " minutes"
	}
	if fields[2] == "*" && fields[3] == "*" && fields[4] == "*" {
		if m, err1 := strconv.Atoi(fields[0]); err1 == nil {
			if h, err2 := strconv.Atoi(fields[1]); err2 == nil {
				return fmt.Sprintf("Daily at %02d:%02d", h, m)
			}
		}
	}
	return "Cron: " + c.Raw
}

func parseField(field string, minVal, maxVal int) (map[int]bool, error) {
	out := make(map[int]bool)
	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("empty field segment")
		}
		base, stepStr, hasStep := strings.Cut(part, "/")
		step := 1
		if hasStep {
			n, err := strconv.Atoi(stepStr)
			if err != nil || n <= 0 {
				return nil, fmt.Errorf("invalid step %q", stepStr)
			}
			step = n
		}
		var lo, hi int
		if base == "*" {
			lo, hi = minVal, maxVal
		} else if a, b, isRange := strings.Cut(base, "-"); isRange {
			var err error
			lo, err = strconv.Atoi(a)
			if err != nil {
				return nil, err
			}
			hi, err = strconv.Atoi(b)
			if err != nil {
				return nil, err
			}
		} else {
			v, err := strconv.Atoi(base)
			if err != nil {
				return nil, err
			}
			lo, hi = v, v
		}
		if lo < minVal || hi > maxVal || lo > hi {
			return nil, fmt.Errorf("value out of range [%d,%d]: %q", minVal, maxVal, part)
		}
		for v := lo; v <= hi; v += step {
			out[v] = true
		}
	}
	return out, nil
}
