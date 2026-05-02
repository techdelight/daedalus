// Copyright (C) 2026 Techdelight BV

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/catalog"
	"github.com/techdelight/daedalus/internal/color"
	"github.com/techdelight/daedalus/internal/logging"
)

// initSkillsCatalog ensures the shared skill catalog directory exists.
// On first run (directory absent), it seeds it with starter skills.
func initSkillsCatalog(cfg *core.Config) error {
	dir := cfg.SkillsDir()
	if _, err := os.Stat(dir); err == nil {
		return nil // already exists
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating skills directory: %w", err)
	}
	starters, err := core.StarterSkills()
	if err != nil {
		return fmt.Errorf("reading starter skills: %w", err)
	}
	for name, data := range starters {
		skillName := strings.TrimSuffix(name, ".md")
		skillDir := filepath.Join(dir, skillName)
		if err := os.MkdirAll(skillDir, 0755); err != nil {
			return fmt.Errorf("creating skill directory %s: %w", skillName, err)
		}
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), data, 0644); err != nil {
			return fmt.Errorf("writing starter skill %s: %w", skillName, err)
		}
	}
	logging.Info("initialized skill catalog with " + strconv.Itoa(len(starters)) + " starter skills")
	return nil
}

// manageSkills handles the "skills" subcommand for host-side catalog management.
func manageSkills(cfg *core.Config) error {
	if err := initSkillsCatalog(cfg); err != nil {
		return err
	}

	cat := catalog.New(cfg.SkillsDir(), "")
	args := cfg.SkillsArgs

	if len(args) == 0 {
		return listSkills(cat)
	}

	switch args[0] {
	case "add":
		if len(args) < 2 {
			return fmt.Errorf("usage: daedalus skills add <file.md>")
		}
		return addSkill(cat, args[1])
	case "remove":
		if len(args) < 2 {
			return fmt.Errorf("usage: daedalus skills remove <name>")
		}
		return removeSkill(cat, args[1])
	case "show":
		if len(args) < 2 {
			return fmt.Errorf("usage: daedalus skills show <name>")
		}
		return showSkill(cat, args[1])
	default:
		return fmt.Errorf("unknown skills command %q\n%s available: add, remove, show (or no args to list)", args[0], color.Cyan("Hint:"))
	}
}

// listSkills prints all skills in the catalog.
func listSkills(cat *catalog.Catalog) error {
	skills, err := cat.List()
	if err != nil {
		return fmt.Errorf("listing skills: %w", err)
	}
	if len(skills) == 0 {
		fmt.Println("No skills in catalog.")
		return nil
	}
	nameW := 4
	for _, s := range skills {
		if len(s.Name) > nameW {
			nameW = len(s.Name)
		}
	}
	fmt.Printf("%-*s  %s\n", nameW, color.Bold("NAME"), color.Bold("DESCRIPTION"))
	fmt.Printf("%-*s  %s\n", nameW, strings.Repeat("-", nameW), "-----------")
	for _, s := range skills {
		fmt.Printf("%-*s  %s\n", nameW, s.Name, s.Description)
	}
	return nil
}

// addSkill copies a local .md file into the catalog.
func addSkill(cat *catalog.Catalog, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading file: %w", err)
	}
	name := strings.TrimSuffix(filepath.Base(path), ".md")
	if err := cat.Create(name, string(data)); err != nil {
		return fmt.Errorf("adding skill: %w", err)
	}
	fmt.Printf("%s skill '%s' added to catalog.\n", color.Green("OK:"), name)
	return nil
}

// removeSkill deletes a skill from the catalog.
func removeSkill(cat *catalog.Catalog, name string) error {
	name = strings.TrimSuffix(name, ".md")
	if err := cat.Remove(name); err != nil {
		return fmt.Errorf("removing skill: %w", err)
	}
	fmt.Printf("%s skill '%s' removed from catalog.\n", color.Green("OK:"), name)
	return nil
}

// showSkill prints a skill's content.
func showSkill(cat *catalog.Catalog, name string) error {
	name = strings.TrimSuffix(name, ".md")
	content, err := cat.Read(name)
	if err != nil {
		return fmt.Errorf("reading skill: %w", err)
	}
	fmt.Print(content)
	return nil
}
