// Copyright (C) 2026 Techdelight BV

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWaitForSocket_AppearsInTime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sock")
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = os.WriteFile(path, []byte{}, 0644)
	}()
	if err := waitForSocket(path, 2*time.Second); err != nil {
		t.Errorf("waitForSocket: %v", err)
	}
}

func TestWaitForSocket_Timeout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing")
	start := time.Now()
	err := waitForSocket(path, 200*time.Millisecond)
	if err == nil {
		t.Fatal("waitForSocket of missing socket returned nil error")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("waitForSocket took %v, expected ~200ms timeout", elapsed)
	}
}
