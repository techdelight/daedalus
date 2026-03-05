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

func TestRelativeTime_InvalidFormat(t *testing.T) {
	got := RelativeTime("not-a-date")
	if got != "not-a-date" {
		t.Errorf("RelativeTime(invalid) = %q, want %q", got, "not-a-date")
	}
}
