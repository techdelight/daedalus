// Copyright (C) 2026 Techdelight BV

package hooks

import (
	"encoding/json"
	"strings"

	"github.com/techdelight/daedalus/core"
)

// hookEntry is a single hook configuration within a settings.json file.
type hookEntry struct {
	Matcher string     `json:"matcher"`
	Hooks   []hookItem `json:"hooks"`
}

// hookItem is a single hook action (command execution).
type hookItem struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// settingsJSON is the top-level structure of a Claude Code settings.json.
type settingsJSON struct {
	Hooks map[string][]hookEntry `json:"hooks,omitempty"`
}

// GenerateSettings produces the runner-specific settings.json content.
// activityFile is the absolute path to the activity.json file inside the container.
// Hook command templates may contain {{.ActivityFile}} which is replaced with activityFile.
func GenerateSettings(cfg core.HookConfig, activityFile string) ([]byte, error) {
	if len(cfg.Hooks) == 0 {
		return []byte("{}\n"), nil
	}

	settings := settingsJSON{
		Hooks: make(map[string][]hookEntry, len(cfg.Hooks)),
	}

	for eventName, cmdTemplate := range cfg.Hooks {
		cmd := strings.ReplaceAll(cmdTemplate, "{{.ActivityFile}}", activityFile)
		settings.Hooks[eventName] = []hookEntry{
			{
				Matcher: "",
				Hooks: []hookItem{
					{Type: "command", Command: cmd},
				},
			},
		}
	}

	return json.MarshalIndent(settings, "", "  ")
}
