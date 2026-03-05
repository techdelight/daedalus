// Copyright (C) 2026 Techdelight BV

package core

// AppConfig holds optional application-level configuration loaded from config.json.
// Pointer fields distinguish "not set" (nil) from zero values.
type AppConfig struct {
	DataDir     *string `json:"data-dir,omitempty"`
	Debug       *bool   `json:"debug,omitempty"`
	NoTmux      *bool   `json:"no-tmux,omitempty"`
	ImagePrefix *string `json:"image-prefix,omitempty"`
}

// ApplyAppConfig sets fields on cfg from app only when the cfg field is still
// at its zero value (CLI/env already won). Follows the applyDefaultFlags pattern.
func ApplyAppConfig(cfg *Config, app AppConfig) {
	if cfg.DataDir == "" && app.DataDir != nil {
		cfg.DataDir = *app.DataDir
	}
	if !cfg.Debug && app.Debug != nil {
		cfg.Debug = *app.Debug
	}
	if !cfg.NoTmux && app.NoTmux != nil {
		cfg.NoTmux = *app.NoTmux
	}
	if cfg.ImagePrefix == "techdelight/claude-runner" && app.ImagePrefix != nil {
		cfg.ImagePrefix = *app.ImagePrefix
	}
}
