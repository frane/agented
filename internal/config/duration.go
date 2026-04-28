package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// ParseDuration parses durations in our extended format. It accepts Go's
// time.ParseDuration formats ("10m", "1h30m") and our extensions: "d" (days,
// = 24h) and "y" (years, = 365d). Multiple unit segments are supported, e.g.
// "1y2d3h". Returns an error for empty input or unrecognized units.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	// Fast path: pure Go duration like "10m" or "1h30m" with no d/y units.
	if !strings.ContainsAny(s, "dy") {
		d, err := time.ParseDuration(s)
		if err != nil {
			return 0, fmt.Errorf("parse duration %q: %w", s, err)
		}
		return d, nil
	}
	// Walk segments: number then unit. Units are y, d, h, m, s, ms, us, ns.
	var total time.Duration
	i := 0
	for i < len(s) {
		// Optional sign only at start.
		start := i
		if i == 0 && (s[i] == '+' || s[i] == '-') {
			i++
		}
		// Read number (allow decimal).
		for i < len(s) && (unicode.IsDigit(rune(s[i])) || s[i] == '.') {
			i++
		}
		if i == start || (i == 1 && (s[0] == '+' || s[0] == '-')) {
			return 0, fmt.Errorf("parse duration %q: missing number near %q", s, s[start:])
		}
		num, err := strconv.ParseFloat(s[start:i], 64)
		if err != nil {
			return 0, fmt.Errorf("parse duration %q: %w", s, err)
		}
		// Read unit.
		uStart := i
		for i < len(s) && unicode.IsLetter(rune(s[i])) {
			i++
		}
		if uStart == i {
			return 0, fmt.Errorf("parse duration %q: missing unit after %s", s, s[start:uStart])
		}
		unit := s[uStart:i]
		var mult time.Duration
		switch unit {
		case "ns":
			mult = time.Nanosecond
		case "us", "µs":
			mult = time.Microsecond
		case "ms":
			mult = time.Millisecond
		case "s":
			mult = time.Second
		case "m":
			mult = time.Minute
		case "h":
			mult = time.Hour
		case "d":
			mult = 24 * time.Hour
		case "y":
			mult = 365 * 24 * time.Hour
		default:
			return 0, fmt.Errorf("parse duration %q: unknown unit %q", s, unit)
		}
		total += time.Duration(num * float64(mult))
	}
	return total, nil
}

// FormatDuration renders a duration using our extended units. Output is the
// most natural single-unit form when possible (e.g. 24h -> "1d", 7*24h -> "7d").
func FormatDuration(d time.Duration) string {
	if d == 0 {
		return "0s"
	}
	abs := d
	sign := ""
	if d < 0 {
		abs = -d
		sign = "-"
	}
	year := 365 * 24 * time.Hour
	day := 24 * time.Hour
	switch {
	case abs%year == 0:
		return fmt.Sprintf("%s%dy", sign, abs/year)
	case abs%day == 0:
		return fmt.Sprintf("%s%dd", sign, abs/day)
	default:
		return d.String()
	}
}
