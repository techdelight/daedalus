// Copyright (C) 2026 Techdelight BV

package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	mu      sync.Mutex
	file    *os.File
	debug   bool
	enabled bool
)

// Init opens or creates the log file at path in append mode.
// Parent directories are created if they do not exist.
// When debugMode is true, Debug() writes to the log; otherwise Debug() is silent.
func Init(path string, debugMode bool) error {
	mu.Lock()
	defer mu.Unlock()

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating log directory %q: %w", dir, err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("opening log file %q: %w", path, err)
	}

	file = f
	debug = debugMode
	enabled = true
	return nil
}

// Close flushes and closes the log file.
func Close() {
	mu.Lock()
	defer mu.Unlock()

	if file != nil {
		file.Close()
		file = nil
	}
	enabled = false
}

// Info writes an informational log line with an [INFO] prefix.
func Info(msg string) {
	write("INFO", msg)
}

// Error writes an error log line with an [ERROR] prefix.
func Error(msg string) {
	write("ERROR", msg)
}

// Debug writes a debug log line with a [DEBUG] prefix.
// The message is only written when debug mode was enabled in Init().
func Debug(msg string) {
	mu.Lock()
	skip := !debug
	mu.Unlock()

	if skip {
		return
	}
	write("DEBUG", msg)
}

// write formats and writes a single log line.
func write(level, msg string) {
	mu.Lock()
	defer mu.Unlock()

	if !enabled || file == nil {
		return
	}

	ts := time.Now().UTC().Format(time.RFC3339)
	line := fmt.Sprintf("%s [%s] %s\n", ts, level, msg)
	_, _ = file.WriteString(line)
}
