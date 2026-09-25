package timeparse

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseSince takes a relative duration string like "30m", "2h", or "1d"
// and returns the start time (now minus that duration) and end time (now).
func ParseSince(since string) (time.Time, time.Time, error) {
	now := time.Now().UTC()

	if strings.HasSuffix(since, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(since, "d"))
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid day format: %s", since)
		}
		start := now.Add(-time.Duration(days) * 24 * time.Hour)
		return start, now, nil
	}

	d, err := time.ParseDuration(since)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid duration: %s", since)
	}

	return now.Add(-d), now, nil
}

// Resolve returns the time range for a search: either the explicit
// start/end pair (RFC 3339, both required together) or the relative
// window given by since.
func Resolve(since, start, end string) (time.Time, time.Time, error) {
	if (start != "") != (end != "") {
		return time.Time{}, time.Time{}, fmt.Errorf("--start and --end must be used together")
	}

	if start == "" {
		startTime, endTime, err := ParseSince(since)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid --since value: %w", err)
		}
		return startTime, endTime, nil
	}

	startTime, err := time.Parse(time.RFC3339, start)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid --start format (expected ISO 8601): %w", err)
	}

	endTime, err := time.Parse(time.RFC3339, end)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid --end format (expected ISO 8601): %w", err)
	}

	if !endTime.After(startTime) {
		return time.Time{}, time.Time{}, fmt.Errorf("--end must be after --start")
	}

	return startTime, endTime, nil
}
