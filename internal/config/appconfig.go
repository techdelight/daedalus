// Copyright (C) 2026 Techdelight BV

package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/techdelight/daedalus/core"
)

// LoadAppConfig reads config.json from the given directory.
// Returns a zero AppConfig if the file does not exist.
// Returns an error only on read or parse failure.
func LoadAppConfig(dir string) (core.AppConfig, error) {
	path := filepath.Join(dir, "config.json")
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return core.AppConfig{}, nil
		}
		return core.AppConfig{}, fmt.Errorf("reading config file: %w", err)
	}
	var cfg core.AppConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return core.AppConfig{}, fmt.Errorf("parsing config file: %w", err)
	}
	return cfg, nil
}
