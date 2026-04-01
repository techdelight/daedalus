// Copyright (C) 2026 Techdelight BV

package core

import (
	"testing"
	"time"
)

func TestNowUTC(t *testing.T) {
	result := NowUTC()
	_, err := time.Parse("2006-01-02T15:04:05Z", result)
	if err != nil {
		t.Errorf("NowUTC() = %q, not valid ISO 8601: %v", result, err)
	}
}

func TestRelativeTime_JustNow(t *testing.T) {
	ts := time.Now().UTC().Add(-30 * time.Second).Format("2006-01-02T15:04:05Z")
	got := RelativeTime(ts)
	if got != "just now" {
		t.Errorf("RelativeTime(%q) = %q, want %q", ts, got, "just now")
	}
}

func TestRelativeTime_Minutes(t *testing.T) {
	ts := time.Now().UTC().Add(-5 * time.Minute).Format("2006-01-02T15:04:05Z")
	got := RelativeTime(ts)
	if got != "5 min ago" {
		t.Errorf("RelativeTime(%q) = %q, want %q", ts, got, "5 min ago")
	}
}

func TestRelativeTime_OneMinute(t *testing.T) {
	ts := time.Now().UTC().Add(-90 * time.Second).Format("2006-01-02T15:04:05Z")
	got := RelativeTime(ts)
	if got != "1 min ago" {
		t.Errorf("RelativeTime(%q) = %q, want %q", ts, got, "1 min ago")
	}
}

func TestRelativeTime_Hours(t *testing.T) {
	ts := time.Now().UTC().Add(-3 * time.Hour).Format("2006-01-02T15:04:05Z")
	got := RelativeTime(ts)
	if got != "3 hours ago" {
		t.Errorf("RelativeTime(%q) = %q, want %q", ts, got, "3 hours ago")
	}
}

func TestRelativeTime_OneHour(t *testing.T) {
	ts := time.Now().UTC().Add(-90 * time.Minute).Format("2006-01-02T15:04:05Z")
	got := RelativeTime(ts)
	if got != "1 hour ago" {
		t.Errorf("RelativeTime(%q) = %q, want %q", ts, got, "1 hour ago")
	}
}

func TestRelativeTime_Days(t *testing.T) {
	ts := time.Now().UTC().Add(-48 * time.Hour).Format("2006-01-02T15:04:05Z")
	got := RelativeTime(ts)
	if got != "2 days ago" {
		t.Errorf("RelativeTime(%q) = %q, want %q", ts, got, "2 days ago")
	}
}

func TestRelativeTime_OneDay(t *testing.T) {
	ts := time.Now().UTC().Add(-36 * time.Hour).Format("2006-01-02T15:04:05Z")
	got := RelativeTime(ts)
	if got != "1 day ago" {
		t.Errorf("RelativeTime(%q) = %q, want %q", ts, got, "1 day ago")
	}
}

func TestParseUTC(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		wantY   int
		wantM   time.Month
		wantD   int
	}{
		{"valid timestamp", "2025-06-15T10:30:00Z", false, 2025, time.June, 15},
		{"epoch", "1970-01-01T00:00:00Z", false, 1970, time.January, 1},
		{"empty string", "", true, 0, 0, 0},
		{"invalid format", "not-a-date", true, 0, 0, 0},
		{"missing Z suffix", "2025-06-15T10:30:00", true, 0, 0, 0},
		{"date only", "2025-06-15", true, 0, 0, 0},
		{"wrong separator", "2025-06-15 10:30:00Z", true, 0, 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			got, err := ParseUTC(tc.input)

			// Assert
			if tc.wantErr {
				if err == nil {
					t.Errorf("ParseUTC(%q) = %v, want error", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseUTC(%q) returned unexpected error: %v", tc.input, err)
			}
			if got.Year() != tc.wantY || got.Month() != tc.wantM || got.Day() != tc.wantD {
				t.Errorf("ParseUTC(%q) = %v, want year=%d month=%v day=%d", tc.input, got, tc.wantY, tc.wantM, tc.wantD)
			}
		})
	}
}

func TestRelativeTime_InvalidFormat(t *testing.T) {
	got := RelativeTime("not-a-date")
	if got != "not-a-date" {
		t.Errorf("RelativeTime(invalid) = %q, want %q", got, "not-a-date")
	}
}
