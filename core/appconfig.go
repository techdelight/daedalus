// Copyright (C) 2026 Techdelight BV

package core

// AppConfig holds optional application-level configuration loaded from config.json.
// Pointer fields distinguish "not set" (nil) from zero values.
type AppConfig struct {
	Version     *string `json:"version,omitempty"`
	DataDir     *string `json:"data-dir,omitempty"`
	Debug       *bool   `json:"debug,omitempty"`
	NoTmux      *bool   `json:"no-tmux,omitempty"`
	ImagePrefix *string `json:"image-prefix,omitempty"`
	LogFile     *string `json:"log-file,omitempty"`
	Runner      *string `json:"runner,omitempty"`
	Persona     *string `json:"persona,omitempty"`
	Agent       *string `json:"agent,omitempty"` // legacy: maps to Runner for backward compat
	AuthToken   *string `json:"auth-token,omitempty"`
	AuthExpiry  *int    `json:"auth-expiry,omitempty"` // session expiry in hours
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
	if cfg.LogFile == "" && app.LogFile != nil {
		cfg.LogFile = *app.LogFile
	}
	if cfg.Runner == "" && app.Runner != nil {
		cfg.Runner = *app.Runner
	}
	if cfg.Persona == "" && app.Persona != nil {
		cfg.Persona = *app.Persona
	}
	// Legacy: "agent" in config.json maps to Runner for backward compat
	if cfg.Runner == "" && app.Agent != nil {
		cfg.Runner = *app.Agent
	}
	if cfg.AuthToken == "" && app.AuthToken != nil {
		cfg.AuthToken = *app.AuthToken
	}
	if cfg.AuthExpiry == 0 && app.AuthExpiry != nil {
		cfg.AuthExpiry = *app.AuthExpiry
	}
}
