// Copyright (C) 2026 Techdelight BV

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/color"
	"github.com/techdelight/daedalus/internal/personas"
	"github.com/techdelight/daedalus/internal/platform"
)

// IsHeadless returns true if running without interactive input.
func IsHeadless(cfg *core.Config) bool {
	if cfg.Prompt != "" {
		return true
	}
	fi, err := os.Stdin.Stat()
	if err != nil {
		return true
	}
	return fi.Mode()&os.ModeCharDevice == 0
}

// ParseArgs parses CLI arguments into a Config.
// Flags can appear in any position; 0/1/2 positional args are accepted.
//
// Subcommand detection:
//   - --help / -h → HelpRequested, and Subcommand names WHOSE help to print:
//     the subcommand's own when it has one (see helpAwareSubcommands), else
//     "help" for the global usage
//   - 0 positional args → Subcommand = "help"
//   - "list" as first positional → Subcommand = "list"
//   - 1 positional arg → ProjectName set, ProjectDir left empty (resolved later)
//   - 2 positional args → ProjectName and ProjectDir set
func ParseArgs(args []string) (*core.Config, error) {
	cfg := &core.Config{
		ImagePrefix: "techdelight/claude-runner",
		Target:      "dev",
	}

	var positional []string
	webHost := "127.0.0.1"
	webPort := "3000"
	hostOverride := false
	noAuthExplicit := false
	helpRequested := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--help", "-h":
			// Recorded, NOT returned on. Returning here used to skip the path
			// resolution below (leaving LogFile empty, which every --help then
			// warned about) and, worse, skip subcommand routing entirely — so
			// `daedalus task --help` printed the global usage and the per-command
			// help at task.go/version.go/docs.go was unreachable by the flag form
			// people actually type.
			helpRequested = true
		case "--build":
			cfg.Build = true
		case "--target":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--target requires a stage name (dev, godot, base, utils)")
			}
			i++
			cfg.Target = args[i]
			cfg.TargetOverride = true
		case "--resume":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--resume requires a session id")
			}
			i++
			cfg.Resume = args[i]
		case "-p":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("-p requires a prompt string")
			}
			i++
			cfg.Prompt = args[i]
		case "--debug":
			cfg.Debug = true
		case "--dind":
			cfg.DinD = true
		case "--display":
			cfg.Display = true
		case "--force":
			cfg.Force = true
		case "--no-color":
			cfg.NoColor = true
		case "--container-log":
			cfg.ContainerLog = true
		case "--auth":
			cfg.Auth = true
		case "--no-auth":
			cfg.Auth = false
			noAuthExplicit = true
		case "--set":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--set requires a key=value pair")
			}
			i++
			if !strings.Contains(args[i], "=") {
				return nil, fmt.Errorf("--set requires format key=value, got %q", args[i])
			}
			cfg.ConfigSet = append(cfg.ConfigSet, args[i])
		case "--unset":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--unset requires a key name")
			}
			i++
			cfg.ConfigUnset = append(cfg.ConfigUnset, args[i])
		case "--port":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--port requires a port number")
			}
			i++
			webPort = args[i]
			port, err := strconv.Atoi(webPort)
			if err != nil || port < 1 || port > 65535 {
				return nil, fmt.Errorf("--port requires a valid port number (1-65535), got %q", webPort)
			}
		case "--host":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--host requires an address")
			}
			i++
			webHost = args[i]
			if strings.TrimSpace(webHost) == "" {
				return nil, fmt.Errorf("--host requires a non-empty address")
			}
			hostOverride = true
		case "--runner":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--runner requires a runner name")
			}
			i++
			cfg.Runner = args[i]
		case "--persona":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--persona requires a persona name")
			}
			i++
			cfg.Persona = args[i]
		case "--agent":
			// Legacy: --agent maps to --runner for backward compat
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--agent requires a name")
			}
			i++
			cfg.Runner = args[i]
		default:
			positional = append(positional, args[i])
		}
	}

	// Resolve script directory (follow symlinks so runtime files are found
	// when the binary is invoked via a symlink, e.g. ~/.local/bin/daedalus)
	// A help request must survive a broken installation: printing usage needs no
	// script dir, and a user whose exe path cannot be resolved is exactly the one
	// who needs to read the help. On error under --help the script dir is left
	// empty (logging.Init treats the resulting empty LogFile as "no log").
	exe, err := resolveExecutable()
	if err != nil && !helpRequested {
		return nil, err
	}
	if err == nil {
		cfg.ScriptDir, err = filepath.Abs(filepath.Dir(exe))
		if err != nil && !helpRequested {
			return nil, fmt.Errorf("cannot resolve script directory: %w", err)
		}
	}

	// Help wins over everything below it: no data dir, no log file, no flag
	// validation. `daedalus --runner bogus --help` should print help rather than
	// an error, and a usage print must not create a log directory — that is what
	// made --help warn inside the project image, where the exe dir is not
	// writable by the container user. LogFile stays empty and logging.Init
	// disables itself, so help remains a pure print with no side effects.
	if helpRequested {
		cfg.HelpRequested = true
		cfg.Subcommand = "help"
		if len(positional) > 0 && helpAwareSubcommands[positional[0]] {
			if handler, ok := collectorSubcommands[positional[0]]; ok {
				cfg.Subcommand = positional[0]
				handler(cfg, []string{positional[0], "help"})
			}
		}
		return cfg, nil
	}

	// Resolve data directory: env var > config file > default
	if cfg.DataDir == "" {
		cfg.DataDir = os.Getenv("DAEDALUS_DATA_DIR")
	}
	appCfg, err := LoadAppConfig(cfg.ScriptDir)
	if err != nil {
		return nil, err
	}
	core.ApplyAppConfig(cfg, appCfg)
	if cfg.DataDir == "" {
		cfg.DataDir = filepath.Join(cfg.ScriptDir, ".cache")
	}
	if cfg.LogFile == "" {
		cfg.LogFile = filepath.Join(cfg.DataDir, "daedalus.log")
	}

	// Validate --runner against built-in names
	if cfg.Runner != "" {
		if err := validateRunnerName(cfg.Runner); err != nil {
			return nil, err
		}
	}

	// Validate --persona against user-defined personas
	if cfg.Persona != "" {
		if err := validatePersonaName(cfg.Persona, cfg.PersonasDir()); err != nil {
			return nil, err
		}
	}

	if len(positional) > 0 {
		if handler, ok := collectorSubcommands[positional[0]]; ok {
			cfg.Subcommand = positional[0]
			handler(cfg, positional)
			return cfg, nil
		}
	}

	switch len(positional) {
	case 0:
		if cfg.Build {
			cfg.Subcommand = "build"
			return cfg, nil
		}
		cfg.Subcommand = "help"
		return cfg, nil
	case 1:
		if isBareSubcommand(positional[0]) {
			cfg.Subcommand = positional[0]
			if positional[0] == "web" {
				applyWebDefaults(cfg, webHost, webPort, hostOverride, noAuthExplicit)
			}
			return cfg, nil
		}
		cfg.ProjectName = positional[0]
		// ProjectDir left empty — resolved later via registry or cwd
	case 2:
		cfg.ProjectName = positional[0]
		cfg.ProjectDir, err = filepath.Abs(positional[1])
		if err != nil {
			return nil, fmt.Errorf("cannot resolve project directory: %w", err)
		}
	default:
		return nil, fmt.Errorf("too many arguments (expected at most 2, got %d)\n%s run 'daedalus --help' for usage", len(positional), color.Cyan("Hint:"))
	}

	core.NormalizeRunnerTarget(cfg)
	return cfg, nil
}

// resolveExecutable returns the real path of the running binary, following
// symlinks so runtime files are found when invoked via one (e.g.
// ~/.local/bin/daedalus).
func resolveExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("cannot determine executable path: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("cannot resolve executable symlink: %w", err)
	}
	return exe, nil
}

// helpAwareSubcommands lists the subcommands that implement their own `help`
// action, so --help can be routed to them instead of printing the global usage.
// The allow-list is deliberate rather than injecting "help" into every
// subcommand: one without a help action would read the word as an ordinary
// argument, and `daedalus remove --help` turning into "remove the project named
// help" is exactly the destructive surprise a help flag must never produce.
var helpAwareSubcommands = map[string]bool{
	"docs":    true,
	"task":    true,
	"version": true,
}

// collectorSubcommands maps each "daedalus <name> [args...]" subcommand to a
// handler that stores the trailing positional args on the Config. These all
// follow the same shape: cfg.Subcommand is set to the keyword, the handler
// captures whatever positional args belong to it, and ParseArgs returns.
var collectorSubcommands = map[string]func(cfg *core.Config, positional []string){
	"remove": func(cfg *core.Config, p []string) { cfg.RemoveTargets = p[1:] },
	"rename": func(cfg *core.Config, p []string) {
		if len(p) > 1 {
			cfg.RenameOldName = p[1]
		}
		if len(p) > 2 {
			cfg.RenameNewName = p[2]
		}
	},
	"config": func(cfg *core.Config, p []string) {
		if len(p) > 1 {
			cfg.ConfigTarget = p[1]
		}
	},
	"completion": func(cfg *core.Config, p []string) {
		if len(p) > 1 {
			cfg.CompletionShell = p[1]
		}
	},
	"skills":      func(cfg *core.Config, p []string) { cfg.SkillsArgs = p[1:] },
	"personas":    func(cfg *core.Config, p []string) { cfg.PersonasArgs = p[1:] },
	"runners":     func(cfg *core.Config, p []string) { cfg.RunnersArgs = p[1:] },
	"programmes":  func(cfg *core.Config, p []string) { cfg.ProgrammesArgs = p[1:] },
	"coordinator": func(cfg *core.Config, p []string) { cfg.CoordinatorArgs = p[1:] },
	"control":     func(cfg *core.Config, p []string) { cfg.ControlArgs = p[1:] },
	"docs":        func(cfg *core.Config, p []string) { cfg.DocsArgs = p[1:] },
	"init":        func(cfg *core.Config, p []string) { cfg.InitArgs = p[1:] },
	"version":     func(cfg *core.Config, p []string) { cfg.VersionArgs = p[1:] },
	"task":        func(cfg *core.Config, p []string) { cfg.TaskArgs = p[1:] },
}

// isBareSubcommand reports whether name is a single-positional subcommand that
// takes no further arguments (list, tui, web, prune).
func isBareSubcommand(name string) bool {
	switch name {
	case "list", "tui", "web", "prune":
		return true
	}
	return false
}

// applyWebDefaults applies WSL2 host detection and the auth-on-by-default rule
// when the "web" subcommand was invoked without overrides.
func applyWebDefaults(cfg *core.Config, webHost, webPort string, hostOverride, noAuthExplicit bool) {
	if !hostOverride && platform.IsWSL2() {
		webHost = "0.0.0.0"
		cfg.WSL2Detected = true
	}
	cfg.WebAddr = webHost + ":" + webPort
	if !noAuthExplicit {
		cfg.Auth = true
	}
}

// validateRunnerName checks whether name is a valid built-in runner.
func validateRunnerName(name string) error {
	if core.IsBuiltinRunner(name) {
		return nil
	}
	return fmt.Errorf("unknown runner %q — valid runners: %s", name, strings.Join(core.ValidRunnerNames(), ", "))
}

// validatePersonaName checks whether name is a user-defined persona in the
// personas directory.
func validatePersonaName(name, personasDir string) error {
	if core.IsBuiltinRunner(name) {
		return fmt.Errorf("invalid persona %q — %q is a built-in runner, use --runner instead", name, name)
	}
	store := personas.New(personasDir)
	if _, err := store.Read(name); err == nil {
		return nil
	}
	return fmt.Errorf("unknown persona %q — use 'daedalus personas list' to see available personas", name)
}
