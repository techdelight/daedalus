// Copyright (C) 2026 Techdelight BV

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/color"
	"github.com/techdelight/daedalus/internal/docker"
	"github.com/techdelight/daedalus/internal/executor"
	"github.com/techdelight/daedalus/internal/logging"
	"github.com/techdelight/daedalus/internal/registry"
)

// buildSpec describes a single Docker image build: the Dockerfile stage name
// and the resulting image tag.
type buildSpec struct {
	dockerTarget string // Dockerfile stage (e.g. "dev", "copilot-dev")
	imageName    string // full image tag (e.g. "techdelight/copilot-runner:dev")
}

// buildAllProjects rebuilds Docker images for all registered projects.
// When --target is explicitly provided, only that target is rebuilt.
// Otherwise, each unique target from the registry is rebuilt.
func buildAllProjects(cfg *core.Config) error {
	exec := &executor.RealExecutor{}
	d := docker.NewDocker(exec, filepath.Join(cfg.ScriptDir, "docker-compose.yml"))

	reg := registry.NewRegistry(cfg.RegistryPath())
	if err := reg.Init(); err != nil {
		return fmt.Errorf("initializing registry: %w", err)
	}

	entries, err := reg.GetProjectEntries()
	if err != nil {
		return fmt.Errorf("reading projects: %w", err)
	}

	if len(entries) == 0 {
		return fmt.Errorf("no registered projects\n%s register a project first with: daedalus <name> <path>", color.Cyan("Hint:"))
	}

	// Collect unique (agent, target) build specs
	specs := collectBuildSpecs(cfg, entries)

	uid := strconv.Itoa(os.Getuid())
	fmt.Printf("Rebuilding %d image(s) for %d registered project(s)...\n\n", len(specs), len(entries))

	checksumPath := filepath.Join(cfg.DataDir, "build-checksum")

	for _, spec := range specs {
		if cfg.Debug {
			printBuildDebugInfo(cfg, spec.dockerTarget, spec.imageName)
		}
		if err := d.Build(spec.dockerTarget, spec.imageName, uid, cfg.ScriptDir); err != nil {
			return fmt.Errorf("building image %s: %w", spec.imageName, err)
		}
		fmt.Println()
	}

	if err := updateBuildChecksum(cfg.ScriptDir, checksumPath); err != nil {
		fmt.Fprintf(os.Stderr, "%s could not update build checksum: %v\n", color.Yellow("Warning:"), err)
	}

	fmt.Printf("%s all images rebuilt.\n", color.Green("Done:"))
	return nil
}

// ensureImageBuilt builds the Docker image if needed (explicit --build, missing
// image, or changed runtime files). It also updates the build checksum afterward.
func ensureImageBuilt(cfg *core.Config, d *docker.Docker) error {
	image := cfg.Image()
	checksumPath := filepath.Join(cfg.DataDir, "build-checksum")

	if cfg.Build {
		logging.Info("building image: " + image)
		if cfg.Debug {
			printBuildDebugInfo(cfg, cfg.BuildTarget(), image)
		}
		if err := buildImage(cfg, d, image); err != nil {
			return err
		}
	} else if !d.ImageExists(image) {
		logging.Info("building image: " + image + " (missing)")
		fmt.Printf(color.Yellow("Warning:")+" image %s missing, building...\n", image)
		if err := buildImage(cfg, d, image); err != nil {
			return err
		}
	} else if docker.NeedsRebuild(cfg.ScriptDir, checksumPath) {
		logging.Info("runtime files changed, rebuilding image: " + image)
		fmt.Printf("%s runtime files changed, rebuilding image %s...\n", color.Yellow("Notice:"), image)
		uid := strconv.Itoa(os.Getuid())
		if err := d.Build(cfg.BuildTarget(), image, uid, cfg.ScriptDir); err != nil {
			logging.Error("auto-rebuild failed: " + err.Error())
			return fmt.Errorf("auto-rebuilding image: %w", err)
		}
	} else {
		return nil
	}

	if err := updateBuildChecksum(cfg.ScriptDir, checksumPath); err != nil {
		fmt.Fprintf(os.Stderr, "%s could not update build checksum: %v\n", color.Yellow("Warning:"), err)
	}
	return nil
}

// buildImage builds the Docker image for the configured target. Used by
// ensureImageBuilt for explicit builds and missing-image builds.
func buildImage(cfg *core.Config, d *docker.Docker, image string) error {
	uid := strconv.Itoa(os.Getuid())
	if err := d.Build(cfg.BuildTarget(), image, uid, cfg.ScriptDir); err != nil {
		logging.Error("build failed: " + err.Error())
		return fmt.Errorf("building image: %w\n%s check Docker is running and try: daedalus --build %s", err, color.Cyan("Hint:"), cfg.ProjectName)
	}
	return nil
}

// updateBuildChecksum computes and stores the checksum of build-relevant files.
func updateBuildChecksum(scriptDir, checksumPath string) error {
	content, err := docker.ReadBuildFilesContent(scriptDir)
	if err != nil {
		return fmt.Errorf("reading build files: %w", err)
	}
	checksum := core.ComputeBuildChecksum(content)
	if err := docker.WriteChecksum(checksumPath, checksum); err != nil {
		return fmt.Errorf("writing checksum: %w", err)
	}
	return nil
}

// printBuildDebugInfo prints diagnostic information before a Docker build when
// both --debug and --build are set. It prints resolved paths, target, image,
// and all environment variables sorted alphabetically.
func printBuildDebugInfo(cfg *core.Config, target, image string) {
	fmt.Println(color.Dim("--- Build Debug Info ---"))
	fmt.Printf("  Dockerfile:       %s\n", filepath.Join(cfg.ScriptDir, "Dockerfile"))
	fmt.Printf("  Compose file:     %s\n", filepath.Join(cfg.ScriptDir, "docker-compose.yml"))
	fmt.Printf("  Target:           %s\n", target)
	fmt.Printf("  Image:            %s\n", image)
	fmt.Println()
	fmt.Println(color.Dim("  Environment variables:"))
	envVars := os.Environ()
	sort.Strings(envVars)
	for _, env := range envVars {
		fmt.Printf("    %s\n", env)
	}
	fmt.Println(color.Dim("--- End Build Debug Info ---"))
	fmt.Println()
}

// collectBuildSpecs returns the deduplicated, sorted list of images to build.
// If --target was explicitly set, only the current config's spec is returned.
// Otherwise, unique (agent, target) pairs are collected from all registered
// projects to produce the correct Dockerfile stage and image name per agent.
func collectBuildSpecs(cfg *core.Config, entries []core.ProjectInfo) []buildSpec {
	if cfg.TargetOverride {
		return []buildSpec{{
			dockerTarget: cfg.BuildTarget(),
			imageName:    cfg.Image(),
		}}
	}
	seen := make(map[string]bool)
	var specs []buildSpec
	for _, e := range entries {
		runner := e.Entry.DefaultFlags["runner"]
		if runner == "" {
			runner = e.Entry.DefaultFlags["agent"] // legacy fallback
		}
		tmpCfg := &core.Config{
			ImagePrefix: cfg.ImagePrefix,
			Target:      e.Entry.Target,
			Runner:      runner,
		}
		img := tmpCfg.Image()
		if !seen[img] {
			seen[img] = true
			specs = append(specs, buildSpec{
				dockerTarget: tmpCfg.BuildTarget(),
				imageName:    img,
			})
		}
	}
	sort.Slice(specs, func(i, j int) bool {
		return specs[i].imageName < specs[j].imageName
	})
	return specs
}
