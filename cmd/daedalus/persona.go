// Copyright (C) 2026 Techdelight BV

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/color"
	"github.com/techdelight/daedalus/internal/logging"
	"github.com/techdelight/daedalus/internal/personas"
)

// resolvePersonaOverlay checks if a persona is selected and, if so, writes
// the overlay files to the cache directory and returns the paths for container
// volume mounts. Also sets cfg.Runner to the persona's BaseRunner so the
// correct binary and Docker image are used. Returns nil when no persona is set.
func resolvePersonaOverlay(cfg *core.Config) (*core.OverlayPaths, error) {
	if cfg.Persona == "" {
		return nil, nil
	}
	store := personas.New(cfg.PersonasDir())
	personaCfg, err := store.Read(cfg.Persona)
	if err != nil {
		return nil, fmt.Errorf("reading persona %q: %w", cfg.Persona, err)
	}
	// Set the runner to the persona's base so the correct binary and image are used.
	if cfg.Runner == "" {
		cfg.Runner = personaCfg.BaseRunner
	}

	overlayDir := filepath.Join(cfg.CacheDir(), "persona-overlay")
	if err := os.MkdirAll(overlayDir, 0755); err != nil {
		return nil, fmt.Errorf("creating overlay directory: %w", err)
	}

	var paths core.OverlayPaths
	if personaCfg.ClaudeMd != "" {
		p := filepath.Join(overlayDir, "CLAUDE.md")
		if err := os.WriteFile(p, []byte(personaCfg.ClaudeMd), 0644); err != nil {
			return nil, fmt.Errorf("writing CLAUDE.md overlay: %w", err)
		}
		paths.ClaudeMdPath = p
	}
	if len(personaCfg.Settings) > 0 {
		p := filepath.Join(overlayDir, "settings.json")
		if err := os.WriteFile(p, personaCfg.Settings, 0644); err != nil {
			return nil, fmt.Errorf("writing settings.json overlay: %w", err)
		}
		paths.SettingsPath = p
	}
	if len(personaCfg.Env) > 0 {
		paths.Env = personaCfg.Env
	}

	logging.Info("resolved persona overlay for " + cfg.Persona + " (base: " + personaCfg.BaseRunner + ")")
	return &paths, nil
}

// managePersonas handles the "personas" subcommand for managing user-defined
// persona configurations.
func managePersonas(cfg *core.Config) error {
	store := personas.New(cfg.PersonasDir())
	args := cfg.PersonasArgs

	if len(args) == 0 || args[0] == "list" {
		return listPersonas(store)
	}

	switch args[0] {
	case "show":
		if len(args) < 2 {
			return fmt.Errorf("usage: daedalus personas show <name>")
		}
		return showPersona(store, args[1])
	case "create":
		if len(args) < 2 {
			return fmt.Errorf("usage: daedalus personas create <name>")
		}
		return createPersona(cfg, store, args[1])
	case "remove":
		if len(args) < 2 {
			return fmt.Errorf("usage: daedalus personas remove <name>")
		}
		return removePersona(store, args[1])
	default:
		return fmt.Errorf("unknown personas command %q\n%s available: list, show, create, remove", args[0], color.Cyan("Hint:"))
	}
}

// listPersonas prints all user-defined persona configurations.
func listPersonas(store *personas.Store) error {
	configs, err := store.List()
	if err != nil {
		return fmt.Errorf("listing personas: %w", err)
	}

	if len(configs) == 0 {
		fmt.Println("No personas defined. Use 'daedalus personas create <name>' to create one.")
		return nil
	}

	nameW := 4
	baseW := 4
	for _, c := range configs {
		if len(c.Name) > nameW {
			nameW = len(c.Name)
		}
		if len(c.BaseRunner) > baseW {
			baseW = len(c.BaseRunner)
		}
	}

	fmt.Printf("%-*s  %-*s  %s\n", nameW, color.Bold("NAME"), baseW, color.Bold("BASE"), color.Bold("DESCRIPTION"))
	fmt.Printf("%-*s  %-*s  %s\n", nameW, strings.Repeat("-", nameW), baseW, strings.Repeat("-", baseW), "-----------")
	for _, c := range configs {
		fmt.Printf("%-*s  %-*s  %s\n", nameW, c.Name, baseW, c.BaseRunner, c.Description)
	}
	return nil
}

// showPersona prints the full JSON configuration for a named persona.
func showPersona(store *personas.Store, name string) error {
	if core.IsBuiltinRunner(name) {
		return fmt.Errorf("%q is a built-in runner, not a persona — use 'daedalus --runner %s' to select it", name, name)
	}
	cfg, err := store.Read(name)
	if err != nil {
		return fmt.Errorf("reading persona: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("formatting persona config: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

// createPersona interactively creates a new persona configuration.
func createPersona(cfg *core.Config, store *personas.Store, name string) error {
	if err := core.ValidatePersonaName(name); err != nil {
		return err
	}

	scanner := bufio.NewScanner(os.Stdin)

	// Prompt for base runner
	fmt.Printf("Base runner (claude, copilot) [claude]: ")
	baseRunner := "claude"
	if scanner.Scan() {
		if text := strings.TrimSpace(scanner.Text()); text != "" {
			baseRunner = text
		}
	}
	if !core.IsBuiltinRunner(baseRunner) {
		return fmt.Errorf("base runner must be a built-in runner (claude, copilot), got %q", baseRunner)
	}

	// Prompt for description
	fmt.Printf("Description: ")
	var description string
	if scanner.Scan() {
		description = strings.TrimSpace(scanner.Text())
	}

	// Prompt for CLAUDE.md content (inline or file path)
	fmt.Printf("CLAUDE.md content (enter text, or @filepath to read from file, or empty to skip):\n> ")
	var claudeMd string
	if scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(text, "@") {
			path := strings.TrimPrefix(text, "@")
			data, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("reading CLAUDE.md file: %w", err)
			}
			claudeMd = string(data)
		} else {
			claudeMd = text
		}
	}

	personaCfg := core.PersonaConfig{
		Name:        name,
		Description: description,
		BaseRunner:  baseRunner,
		ClaudeMd:    claudeMd,
	}

	if err := os.MkdirAll(cfg.PersonasDir(), 0755); err != nil {
		return fmt.Errorf("creating personas directory: %w", err)
	}
	if err := store.Create(personaCfg); err != nil {
		return fmt.Errorf("creating persona: %w", err)
	}
	fmt.Printf("%s persona '%s' created (base: %s).\n", color.Green("OK:"), name, baseRunner)
	return nil
}

// removePersona deletes a user-defined persona configuration.
func removePersona(store *personas.Store, name string) error {
	if core.IsBuiltinRunner(name) {
		return fmt.Errorf("cannot remove built-in runner %q", name)
	}
	if err := store.Remove(name); err != nil {
		return fmt.Errorf("removing persona: %w", err)
	}
	fmt.Printf("%s persona '%s' removed.\n", color.Green("OK:"), name)
	return nil
}
