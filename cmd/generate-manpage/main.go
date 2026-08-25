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
	b.WriteString(".B daedalus init\n")
	b.WriteString("[\\fB\\-\\-force\\fR] [\\fB\\-\\-no\\-scaffold\\fR] [\\fIdir\\fR]\n")
	b.WriteString(".br\n")
	b.WriteString(".B daedalus docs\n")
	b.WriteString("[\\fBlint\\fR | \\fBscaffold\\fR] [\\fIdir\\fR]\n")
	b.WriteString(".br\n")
	b.WriteString(".B daedalus version\n")
	b.WriteString("[\\fBlist\\fR | \\fBuse\\fR \\fIversion\\fR | \\fBrollback\\fR | \\fBprune\\fR]\n")
	b.WriteString(".br\n")
	b.WriteString(".B daedalus programmes\n")
	b.WriteString("[\\fBlist\\fR | \\fBshow\\fR | \\fBstatus\\fR | \\fBcreate\\fR | \\fBadd\\-project\\fR | \\fBadd\\-dep\\fR | \\fBremove\\fR]\n")
	b.WriteString(".br\n")
	b.WriteString(".B daedalus task\n")
	b.WriteString("<\\fIsubcommand\\fR>\n")
	b.WriteString(".br\n")
	b.WriteString(".B daedalus coordinator\n")
	b.WriteString("[\\fBstart\\fR | \\fBstop\\fR | \\fBstatus\\fR]\n")
	b.WriteString(".br\n")
	b.WriteString(".B daedalus control\n")
	b.WriteString("[\\fBstart\\fR | \\fBstop\\fR | \\fBrestart\\fR | \\fBstatus\\fR]\n")
	b.WriteString(".br\n")
	b.WriteString(".B daedalus guild-master\n")
	b.WriteString(".br\n")
	b.WriteString(".B daedalus build\n")
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
	b.WriteString("Sessions run under an in-container runner that multiplexes one PTY to many\n")
	b.WriteString("clients, supporting detach/reattach across the CLI, TUI, and web dashboard.\n")
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
		"\\fBskills\\fR [\\fBadd\\fR \\fIfile\\fR | \\fBremove\\fR \\fIname\\fR | \\fBshow\\fR \\fIname\\fR]",
		"List, add, remove, or show skills in the shared skill catalog.")

	writeCommand(b,
		"\\fBrunners\\fR [\\fBlist\\fR | \\fBshow\\fR \\fIname\\fR]",
		"List or show built-in runner profiles (claude, copilot).")

	writeCommand(b,
		"\\fBpersonas\\fR [\\fBlist\\fR | \\fBshow\\fR \\fIname\\fR | \\fBcreate\\fR \\fIname\\fR | \\fBremove\\fR \\fIname\\fR]",
		"List, show, create, or remove named persona configurations.")

	writeCommand(b,
		"\\fBbuild\\fR",
		"Rebuild the Docker image for every registered project. Equivalent to \\fB\\-\\-build\\fR without a project name.")

	writeCommand(b,
		"\\fBinit\\fR [\\fB\\-\\-force\\fR] [\\fB\\-\\-no\\-scaffold\\fR] [\\fIdir\\fR]",
		"Scaffold the required project documents (VISION, ROADMAP, SPRINTS, BACKLOG and the rest) into \\fIdir\\fR and print a getting-started guide. \\fB\\-\\-no\\-scaffold\\fR prints the guidance without writing files.")

	writeCommand(b,
		"\\fBdocs\\fR [\\fBlint\\fR [\\fB\\-\\-ci\\fR] [\\fIdir\\fR] | \\fBscaffold\\fR [\\fB\\-\\-force\\fR] [\\fIdir\\fR]]",
		"Check a project's ROADMAP.md and SPRINTS.md against the structured-document format, or write conformant skeletons. \\fBlint\\fR exits non-zero on errors; \\fB\\-\\-ci\\fR makes warnings fatal too.")

	writeCommand(b,
		"\\fBversion\\fR [\\fBlist\\fR | \\fBuse\\fR \\fIversion\\fR | \\fBrollback\\fR | \\fBprune\\fR [\\fB\\-\\-keep\\fR \\fIn\\fR]]",
		"Manage side-by-side installed versions. Installs live under \\fIversions/<version>/\\fR with a \\fIcurrent\\fR symlink, so switching takes effect immediately and never re-downloads. \\fBprune\\fR never removes the active version.")

	writeCommand(b,
		"\\fBprogrammes\\fR [\\fBlist\\fR | \\fBshow\\fR \\fIname\\fR | \\fBstatus\\fR \\fIname\\fR [\\fB\\-\\-suggest\\-deps\\fR] | \\fBcreate\\fR \\fIname\\fR \\fIdescription\\fR | \\fBadd\\-project\\fR \\fIprogramme\\fR \\fIproject\\fR | \\fBadd\\-dep\\fR \\fIprogramme\\fR \\fIupstream\\fR \\fIdownstream\\fR | \\fBremove\\fR \\fIname\\fR]",
		"Manage programmes \\(em the shared intent several projects serve. \\fBstatus\\fR rolls up the tasks serving a programme, what it is waiting on outside itself, and where its declared project order and the task graph that actually gates disagree. \\fBadd\\-dep\\fR declares an order; it gates nothing (see \\fBtask depends\\fR). Requires the control plane, which is started on demand.")

	writeCommand(b,
		"\\fBtask\\fR <\\fIsubcommand\\fR>",
		"Host-side control-plane work: \\fBcreate\\fR, \\fBlist\\fR, \\fBstatus\\fR, \\fBdispatch\\fR, \\fBverify\\fR, \\fBreview\\fR, \\fBreverify\\fR, \\fBretry\\fR, \\fBreplan\\fR, \\fBrefine\\fR, \\fBchecks\\fR, \\fBbudget\\fR, \\fBdepends\\fR, \\fBsteer\\fR, \\fBapprove\\fR, \\fBreject\\fR, \\fBintegrate\\fR, \\fBcancel\\fR, \\fBboard\\fR, \\fBapprovals\\fR, \\fBproposals\\fR, \\fBtarget\\fR and \\fBevents\\fR. Ids are prefixed by kind: \\fIT\\-n\\fR task, \\fIJ\\-n\\fR job, \\fIA\\-n\\fR artifact, \\fIRV\\-n\\fR review, \\fIP\\-n\\fR proposal, \\fIPR\\-n\\fR programme, \\fIS\\-n\\fR steering.")

	writeCommand(b,
		"\\fBcoordinator\\fR [\\fBstart\\fR | \\fBstop\\fR | \\fBstatus\\fR]",
		"Manage the host-side session daemon that owns container session lifecycles. Started on demand; stop it to pick up a rebuilt image or binary.")

	writeCommand(b,
		"\\fBcontrol\\fR [\\fBstart\\fR | \\fBstop\\fR | \\fBrestart\\fR | \\fBstatus\\fR]",
		"Manage the control-plane daemon, the single owner of \\fIcontrol.db\\fR. Started on demand by \\fBtask\\fR and \\fBprogrammes\\fR, so this is needed only to stop it or to \\fBrestart\\fR it after an upgrade \\(em a running daemon serves the routes it was built with, so a newly added operation fails against an old one. \\fBstatus\\fR reports that case.")

	writeCommand(b,
		"\\fBguild-master\\fR",
		"Open the built-in cross-project overseer. Every registered project is mounted read-only for it; it can read across the guild, create bounded tasks, and \\fIpropose\\fR programmes and other consequential operations for a human to confirm. It cannot act on them itself.")

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

	writeOption(b, "\\fB\\-\\-debug\\fR",
		"Enable Claude Code debug mode.")

	writeOption(b, "\\fB\\-\\-dind\\fR",
		"Mount the host Docker socket into the container. WARNING: this grants the container full access to the host Docker daemon.")

	writeOption(b, "\\fB\\-\\-display\\fR",
		"Forward the host X11 or Wayland display into the container, allowing GUI applications to render on the host screen. Requires a running display server on the host.")

	writeOption(b, "\\fB\\-\\-runner\\fR \\fIname\\fR",
		"AI runner to use inside the container. Accepted values: \\fBclaude\\fR (default), \\fBcopilot\\fR. Can also be set per-project via \\fBdaedalus config <name> \\-\\-set runner=copilot\\fR.")

	writeOption(b, "\\fB\\-\\-persona\\fR \\fIname\\fR",
		"Named persona configuration to use. User-defined personas layer system prompts and tool permissions on top of a built-in runner.")

	writeOption(b, "\\fB\\-\\-force\\fR",
		"Force deletion in non-interactive mode for \\fBprune\\fR and \\fBremove\\fR commands.")

	writeOption(b, "\\fB\\-\\-no\\-color\\fR",
		"Disable colored output. Also honors the \\fBNO_COLOR\\fR environment variable.")

	writeOption(b, "\\fB\\-\\-port\\fR \\fIport\\fR",
		"Port for the web UI server. Default: 3000.")

	writeOption(b, "\\fB\\-\\-host\\fR \\fIhost\\fR",
		"Host address for the web UI server to bind to. Default: 127.0.0.1, or 0.0.0.0 when WSL2 is detected.")

	writeOption(b, "\\fB\\-\\-auth\\fR, \\fB\\-\\-no\\-auth\\fR",
		"Enable or disable token authentication for the web UI. Authentication is on by default. Note that the web UI serves the control plane, so \\fB\\-\\-no\\-auth\\fR on a reachable address gives away the ability to dispatch jobs and land code, not merely a dashboard.")

	writeOption(b, "\\fB\\-\\-container\\-log\\fR",
		"Write container output to \\fI<data-dir>/<project>/container.log\\fR.")
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
	b.WriteString("\\fBimage-prefix\\fR (string)\n")
	b.WriteString("Docker image prefix. Default: techdelight/claude-runner.\n")
	b.WriteString(".TP\n")
	b.WriteString("\\fBrunner\\fR (string)\n")
	b.WriteString("Default AI runner: claude (default) or copilot.\n")
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
	b.WriteString("Use Copilot CLI instead of Claude Code:\n")
	b.WriteString(".PP\n")
	b.WriteString(".RS\n")
	b.WriteString(".nf\n")
	b.WriteString("daedalus \\-\\-runner copilot my\\-app /path/to/project\n")
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
	b.WriteString(".TP\n")
	b.WriteString("\\fB3\\fR\n")
	b.WriteString("A \\fBtask\\fR request was refused by control-plane policy (over budget, attempts exhausted, concurrency exceeded). Distinct from \\fB1\\fR so a script can tell a policy refusal from a failure; the machine-readable reason is printed on standard error and recorded in \\fBdaedalus task events\\fR.\n")
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
	b.WriteString(".TP\n")
	b.WriteString(".I <data-dir>/.daedalus/control.db\n")
	b.WriteString("The control plane's database: tasks, jobs, artifacts, reviews, proposals, programmes and the append-only event log. The \\fBdaedalus\\-control\\fR daemon is its only writer.\n")
	b.WriteString(".TP\n")
	b.WriteString(".I <data-dir>/.daedalus/control.sock\n")
	b.WriteString("The control plane's human socket. \\fBcontrol\\-agent.sock\\fR sits beside it: the restricted socket mounted into the Guild Master, whose caller class is decided by which socket a request arrives on. \\fBcontrol.pid\\fR and \\fBcontrol.log\\fR are alongside.\n")
	b.WriteString(".TP\n")
	b.WriteString(".I <data-dir>/.daedalus/jobs/<job-id>.log\n")
	b.WriteString("One log per control-plane job: the agent's own output for that attempt, which is the only account of what it did.\n")
}

// writeSeeAlso writes the SEE ALSO section.
func writeSeeAlso(b *strings.Builder) {
	b.WriteString(".SH SEE ALSO\n")
	b.WriteString(".BR docker (1),\n")
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
