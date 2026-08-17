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
//
// An empty path means "no log file configured" and disables logging rather than
// failing. Without this the empty path splits confusingly — filepath.Dir("") is
// ".", so MkdirAll succeeds on the cwd and only the OpenFile fails, producing
// `opening log file "": open : no such file or directory` with nothing between
// the colon and the message. Callers treat an Init error as a warning and carry
// on, so the only effect was noise on stderr for a config that never asked for a
// log in the first place.
func Init(path string, debugMode bool) error {
	mu.Lock()
	defer mu.Unlock()

	if path == "" {
		enabled = false
		return nil
	}

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
