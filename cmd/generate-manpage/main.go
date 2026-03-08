// Copyright (C) 2026 Techdelight BV

package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	version, err := readVersion()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(generateManpage(version, time.Now().Format("2006-01-02")))
}

// readVersion reads the VERSION file from the same directory tree as the binary.
// It walks up from the executable location looking for VERSION, falling back to
// the current working directory.
func readVersion() (string, error) {
	data, err := os.ReadFile("VERSION")
	if err != nil {
		return "", fmt.Errorf("reading VERSION: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// generateManpage returns a complete roff-formatted man page for daedalus(1).
func generateManpage(version string, date string) string {
	var b strings.Builder

	writeHeader(&b, version, date)
	writeName(&b)
	writeSynopsis(&b)
	writeDescription(&b)
	writeCommands(&b)
	writeOptions(&b)
	writeEnvironment(&b)
	writeConfiguration(&b)
	writeExamples(&b)
	writeExitStatus(&b)
	writeFiles(&b)
	writeSeeAlso(&b)
	writeAuthors(&b)
	writeCopyright(&b)

	return b.String()
}

// writeHeader writes the .TH title header macro.
func writeHeader(b *strings.Builder, version string, date string) {
	fmt.Fprintf(b, ".TH DAEDALUS 1 \"%s\" \"daedalus %s\" \"User Commands\"\n", date, version)
}

// writeName writes the NAME section.
func writeName(b *strings.Builder) {
	b.WriteString(".SH NAME\n")
	b.WriteString("daedalus \\- Docker environment for autonomous Claude Code\n")
}

// writeSynopsis writes the SYNOPSIS section.
func writeSynopsis(b *strings.Builder) {
	b.WriteString(".SH SYNOPSIS\n")
	b.WriteString(".B daedalus\n")
	b.WriteString("[\\fIflags\\fR] <\\fIproject-name\\fR> [\\fIproject-dir\\fR]\n")
	b.WriteString(".br\n")
	b.WriteString(".B daedalus list\n")
	b.WriteString(".br\n")
	b.WriteString(".B daedalus prune\n")
	b.WriteString(".br\n")
	b.WriteString(".B daedalus remove\n")
	b.WriteString("<\\fIname\\fR> [\\fIname\\fR...]\n")
	b.WriteString(".br\n")
	b.WriteString(".B daedalus rename\n")
	b.WriteString("<\\fIold-name\\fR> <\\fInew-name\\fR>\n")
	b.WriteString(".br\n")
	b.WriteString(".B daedalus config\n")
	b.WriteString("<\\fIname\\fR> [\\fB\\-\\-set\\fR \\fIkey=value\\fR] [\\fB\\-\\-unset\\fR \\fIkey\\fR]\n")
	b.WriteString(".br\n")
	b.WriteString(".B daedalus tui\n")
	b.WriteString(".br\n")
	b.WriteString(".B daedalus web\n")
	b.WriteString("[\\fB\\-\\-port\\fR \\fIPORT\\fR] [\\fB\\-\\-host\\fR \\fIHOST\\fR]\n")
	b.WriteString(".br\n")
	b.WriteString(".B daedalus completion\n")
	b.WriteString("<\\fBbash\\fR|\\fBzsh\\fR|\\fBfish\\fR>\n")
	b.WriteString(".br\n")
	b.WriteString(".B daedalus \\-\\-help\n")
}

// writeDescription writes the DESCRIPTION section.
func writeDescription(b *strings.Builder) {
	b.WriteString(".SH DESCRIPTION\n")
	b.WriteString(".B daedalus\n")
	b.WriteString("wraps Claude Code in a Docker container with\n")
	b.WriteString(".B \\-\\-dangerously\\-skip\\-permissions\\fR,\n")
	b.WriteString("providing isolation instead of interactive permission prompts.\n")
	b.WriteString("The container boundary replaces per-action approval with a single trust\n")
	b.WriteString("decision: Claude can do anything inside the container.\n")
	b.WriteString(".PP\n")
	b.WriteString("Each project gets its own named container with the project directory\n")
	b.WriteString("mounted read-write at\n")
	b.WriteString(".I /workspace\n")
	b.WriteString("and a persistent home directory at\n")
	b.WriteString(".IR /home/claude .\n")
	b.WriteString("Sessions are wrapped in tmux for detach/reattach support.\n")
	b.WriteString(".PP\n")
	b.WriteString("Three UI surfaces are provided: a command-line interface (CLI), an\n")
	b.WriteString("interactive terminal dashboard (TUI), and a browser-based web dashboard\n")
	b.WriteString("with an embedded terminal.\n")
}

// writeCommands writes the COMMANDS section.
func writeCommands(b *strings.Builder) {
	b.WriteString(".SH COMMANDS\n")

	writeCommand(b,
		"<\\fIproject-name\\fR>",
		"Open a registered project using its stored directory.")

	writeCommand(b,
		"<\\fIproject-name\\fR> <\\fIproject-dir\\fR>",
		"Register a new project and open it. The directory is stored in the registry for future use.")

	writeCommand(b,
		"\\fBlist\\fR",
		"List all registered projects with their directories, targets, session counts, and last-used timestamps.")

	writeCommand(b,
		"\\fBprune\\fR",
		"Remove registry entries whose project directories no longer exist on disk. Prompts for confirmation in interactive mode; use \\fB\\-\\-force\\fR for non-interactive mode.")

	writeCommand(b,
		"\\fBremove\\fR <\\fIname\\fR> [\\fIname\\fR...]",
		"Remove one or more named projects from the registry. Prompts for confirmation in interactive mode; use \\fB\\-\\-force\\fR for non-interactive mode.")

	writeCommand(b,
		"\\fBrename\\fR <\\fIold-name\\fR> <\\fInew-name\\fR>",
		"Rename a registered project. The project must be stopped. Updates the registry key and renames the per-project cache directory.")

	writeCommand(b,
		"\\fBconfig\\fR <\\fIname\\fR> [\\fB\\-\\-set\\fR \\fIkey=value\\fR] [\\fB\\-\\-unset\\fR \\fIkey\\fR]",
		"View or edit per-project default flags. Without \\fB\\-\\-set\\fR or \\fB\\-\\-unset\\fR, displays the current configuration.")

	writeCommand(b,
		"\\fBtui\\fR",
		"Launch the interactive terminal dashboard for managing all registered projects. Key bindings: j/Down move down, k/Up move up, s start, a attach, K kill, r refresh, q quit.")

	writeCommand(b,
		"\\fBweb\\fR [\\fB\\-\\-port\\fR \\fIPORT\\fR] [\\fB\\-\\-host\\fR \\fIHOST\\fR]",
		"Start the browser-based web dashboard with project management and an embedded terminal. Default: localhost:3000.")

	writeCommand(b,
		"\\fBcompletion\\fR <\\fBbash\\fR|\\fBzsh\\fR|\\fBfish\\fR>",
		"Print a shell completion script to stdout. Source the output in your shell profile.")

	writeCommand(b,
		"\\fB\\-\\-help\\fR, \\fB\\-h\\fR",
		"Show the usage message and exit.")
}

// writeCommand writes a single .TP command entry.
func writeCommand(b *strings.Builder, term string, desc string) {
	b.WriteString(".TP\n")
	fmt.Fprintf(b, "%s\n", term)
	fmt.Fprintf(b, "%s\n", desc)
}

// writeOptions writes the OPTIONS section.
func writeOptions(b *strings.Builder) {
	b.WriteString(".SH OPTIONS\n")

	writeOption(b, "\\fB\\-\\-build\\fR",
		"Force rebuild the Docker image before starting the container.")

	writeOption(b, "\\fB\\-\\-target\\fR \\fIstage\\fR",
		"Docker build target stage. Available targets: \\fBdev\\fR (default), \\fBgodot\\fR, \\fBbase\\fR, \\fButils\\fR.")

	writeOption(b, "\\fB\\-\\-resume\\fR \\fIid\\fR",
		"Resume a previous Claude Code session by its session ID.")

	writeOption(b, "\\fB\\-p\\fR \\fIprompt\\fR",
		"Run a headless single-prompt task. The container executes the prompt and exits without interactive input.")

	writeOption(b, "\\fB\\-\\-no\\-tmux\\fR",
		"Run without tmux session wrapping. The container runs directly in the current terminal.")

	writeOption(b, "\\fB\\-\\-debug\\fR",
		"Enable Claude Code debug mode.")

	writeOption(b, "\\fB\\-\\-dind\\fR",
		"Mount the host Docker socket into the container. WARNING: this grants the container full access to the host Docker daemon.")

	writeOption(b, "\\fB\\-\\-force\\fR",
		"Force deletion in non-interactive mode for \\fBprune\\fR and \\fBremove\\fR commands.")

	writeOption(b, "\\fB\\-\\-no\\-color\\fR",
		"Disable colored output. Also honors the \\fBNO_COLOR\\fR environment variable.")

	writeOption(b, "\\fB\\-\\-port\\fR \\fIport\\fR",
		"Port for the web UI server. Default: 3000.")

	writeOption(b, "\\fB\\-\\-host\\fR \\fIhost\\fR",
		"Host address for the web UI server to bind to. Default: 127.0.0.1.")
}

// writeOption writes a single .TP option entry.
func writeOption(b *strings.Builder, term string, desc string) {
	b.WriteString(".TP\n")
	fmt.Fprintf(b, "%s\n", term)
	fmt.Fprintf(b, "%s\n", desc)
}

// writeEnvironment writes the ENVIRONMENT section.
func writeEnvironment(b *strings.Builder) {
	b.WriteString(".SH ENVIRONMENT\n")
	b.WriteString(".TP\n")
	b.WriteString("\\fBDAEDALUS_DATA_DIR\\fR\n")
	b.WriteString("Base directory for the project registry and per-project caches. Defaults to\n")
	b.WriteString(".I .cache\n")
	b.WriteString("next to the daedalus binary.\n")
	b.WriteString(".TP\n")
	b.WriteString("\\fBNO_COLOR\\fR\n")
	b.WriteString("When set (to any value), disables colored output. See https://no-color.org/.\n")
}

// writeConfiguration writes the CONFIGURATION section.
func writeConfiguration(b *strings.Builder) {
	b.WriteString(".SH CONFIGURATION\n")
	b.WriteString("A JSON configuration file can be placed at\n")
	b.WriteString(".I <install-dir>/config.json\n")
	b.WriteString("(default:\n")
	b.WriteString(".IR ~/.local/share/daedalus/config.json ).\n")
	b.WriteString("All fields are optional.\n")
	b.WriteString(".PP\n")
	b.WriteString("Precedence (highest to lowest): CLI flags, environment variables, config.json, built-in defaults.\n")
	b.WriteString(".PP\n")
	b.WriteString("Supported fields:\n")
	b.WriteString(".TP\n")
	b.WriteString("\\fBdata-dir\\fR (string)\n")
	b.WriteString("Base directory for registry and per-project caches. Must be an absolute path.\n")
	b.WriteString(".TP\n")
	b.WriteString("\\fBdebug\\fR (bool)\n")
	b.WriteString("Enable Claude Code debug mode.\n")
	b.WriteString(".TP\n")
	b.WriteString("\\fBno-tmux\\fR (bool)\n")
	b.WriteString("Run without tmux session wrapping.\n")
	b.WriteString(".TP\n")
	b.WriteString("\\fBimage-prefix\\fR (string)\n")
	b.WriteString("Docker image prefix. Default: techdelight/claude-runner.\n")
}

// writeExamples writes the EXAMPLES section.
func writeExamples(b *strings.Builder) {
	b.WriteString(".SH EXAMPLES\n")

	b.WriteString("Open an existing project from the registry:\n")
	b.WriteString(".PP\n")
	b.WriteString(".RS\n")
	b.WriteString(".nf\n")
	b.WriteString("daedalus my\\-app\n")
	b.WriteString(".fi\n")
	b.WriteString(".RE\n")

	b.WriteString(".PP\n")
	b.WriteString("Register a new project with a directory:\n")
	b.WriteString(".PP\n")
	b.WriteString(".RS\n")
	b.WriteString(".nf\n")
	b.WriteString("daedalus my\\-app /path/to/project\n")
	b.WriteString(".fi\n")
	b.WriteString(".RE\n")

	b.WriteString(".PP\n")
	b.WriteString("Run a headless single-prompt task:\n")
	b.WriteString(".PP\n")
	b.WriteString(".RS\n")
	b.WriteString(".nf\n")
	b.WriteString("daedalus my\\-app \\-p \"Fix all linting errors\"\n")
	b.WriteString(".fi\n")
	b.WriteString(".RE\n")

	b.WriteString(".PP\n")
	b.WriteString("Force rebuild with a specific target:\n")
	b.WriteString(".PP\n")
	b.WriteString(".RS\n")
	b.WriteString(".nf\n")
	b.WriteString("daedalus \\-\\-build \\-\\-target godot my\\-game /path/to/game\n")
	b.WriteString(".fi\n")
	b.WriteString(".RE\n")

	b.WriteString(".PP\n")
	b.WriteString("Start the web UI on a custom port:\n")
	b.WriteString(".PP\n")
	b.WriteString(".RS\n")
	b.WriteString(".nf\n")
	b.WriteString("daedalus web \\-\\-port 8080\n")
	b.WriteString(".fi\n")
	b.WriteString(".RE\n")

	b.WriteString(".PP\n")
	b.WriteString("Rename a project:\n")
	b.WriteString(".PP\n")
	b.WriteString(".RS\n")
	b.WriteString(".nf\n")
	b.WriteString("daedalus rename my\\-app my\\-new\\-app\n")
	b.WriteString(".fi\n")
	b.WriteString(".RE\n")

	b.WriteString(".PP\n")
	b.WriteString("Set per-project defaults:\n")
	b.WriteString(".PP\n")
	b.WriteString(".RS\n")
	b.WriteString(".nf\n")
	b.WriteString("daedalus config my\\-app \\-\\-set dind=true\n")
	b.WriteString(".fi\n")
	b.WriteString(".RE\n")

	b.WriteString(".PP\n")
	b.WriteString("Generate and source shell completions:\n")
	b.WriteString(".PP\n")
	b.WriteString(".RS\n")
	b.WriteString(".nf\n")
	b.WriteString("eval \"$(daedalus completion bash)\"\n")
	b.WriteString(".fi\n")
	b.WriteString(".RE\n")

	b.WriteString(".PP\n")
	b.WriteString("Resume a previous Claude session:\n")
	b.WriteString(".PP\n")
	b.WriteString(".RS\n")
	b.WriteString(".nf\n")
	b.WriteString("daedalus \\-\\-resume <session\\-id> my\\-app\n")
	b.WriteString(".fi\n")
	b.WriteString(".RE\n")
}

// writeExitStatus writes the EXIT STATUS section.
func writeExitStatus(b *strings.Builder) {
	b.WriteString(".SH EXIT STATUS\n")
	b.WriteString(".TP\n")
	b.WriteString("\\fB0\\fR\n")
	b.WriteString("Success.\n")
	b.WriteString(".TP\n")
	b.WriteString("\\fB1\\fR\n")
	b.WriteString("An error occurred (invalid arguments, Docker failure, missing project, etc.).\n")
}

// writeFiles writes the FILES section.
func writeFiles(b *strings.Builder) {
	b.WriteString(".SH FILES\n")
	b.WriteString(".TP\n")
	b.WriteString(".I <install-dir>/config.json\n")
	b.WriteString("Application configuration file. See \\fBCONFIGURATION\\fR above.\n")
	b.WriteString(".TP\n")
	b.WriteString(".I .cache/projects.json\n")
	b.WriteString("Project registry file containing all registered projects, their directories, targets, session history, and timestamps.\n")
	b.WriteString(".TP\n")
	b.WriteString(".I .cache/<project>/\n")
	b.WriteString("Per-project persistent home directory, bind-mounted as \\fI/home/claude\\fR inside the container. Stores shell history, Claude session transcripts, tool caches, and per-project MCP/settings overrides.\n")
}

// writeSeeAlso writes the SEE ALSO section.
func writeSeeAlso(b *strings.Builder) {
	b.WriteString(".SH SEE ALSO\n")
	b.WriteString(".BR docker (1),\n")
	b.WriteString(".BR tmux (1),\n")
	b.WriteString(".BR claude (1)\n")
}

// writeAuthors writes the AUTHORS section.
func writeAuthors(b *strings.Builder) {
	b.WriteString(".SH AUTHORS\n")
	b.WriteString("Techdelight BV\n")
}

// writeCopyright writes the COPYRIGHT section.
func writeCopyright(b *strings.Builder) {
	b.WriteString(".SH COPYRIGHT\n")
	b.WriteString("Copyright (C) 2026 Techdelight BV. Licensed under the Apache License, Version 2.0.\n")
}
