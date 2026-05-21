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
