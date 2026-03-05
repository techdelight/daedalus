// Copyright (C) 2026 Techdelight BV

package core

import (
	"fmt"
	"time"
)

// NowUTC returns the current time in UTC as an ISO 8601 string.
func NowUTC() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05Z")
}

// ParseUTC parses an ISO 8601 UTC timestamp.
func ParseUTC(iso string) (time.Time, error) {
	return time.Parse("2006-01-02T15:04:05Z", iso)
}

// RelativeTime converts an ISO 8601 timestamp to a human-friendly relative string.
func RelativeTime(iso string) string {
	t, err := time.Parse("2006-01-02T15:04:05Z", iso)
	if err != nil {
		return iso // fallback to raw string
	}

	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1 min ago"
		}
		return fmt.Sprintf("%d min ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", h)
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	}
}
