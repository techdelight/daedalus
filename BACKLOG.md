# Backlog

| # | Item |
|---|------|
| 6 | Shell toggle — switch between Claude Code and a regular project shell inside the container |
| 8 | Bundle release assets — package runtime files into a single tarball on the GitHub Release page instead of individual files |
| 9 | Side-by-side versions — install a new version alongside the existing one, allowing rollback or A/B comparison before switching |
| 11 | Homebrew installation (`brew install daedalus`) — add Homebrew tap, formula generator, and CI automation. See [docs/homebrew-plan.md](docs/homebrew-plan.md) for full plan |
| 16 | ACP integration — use the Agent Client Protocol to communicate with the Claude Code CLI, enabling Daedalus to observe agent state (thinking, tool use, idle, error) in real time |
| 21 | Shared Maven `.m2` repository — mount a host-side `.m2/repository` into containers so dependencies are shared across projects. Investigate overlay/merge strategy: a stable global repo (read-only base) combined with a per-container local repo for builds/downloads/installs, so containers benefit from cached artifacts without polluting the shared cache |
| 25 | Webdev container — move Node.js out of the regular `dev` stage into a dedicated `webdev` build target for web/frontend projects. Keeps the default dev image lean |
| 27 | Decouple tooling from agent runner images — keep base agent containers minimal and let the agent install additional tools at runtime. Provide container snapshotting so customized environments persist across restarts |
| 29 | Mobile WebSocket stability — investigate and fix regular disconnects on mobile web clients (possible causes: browser background tab throttling, network switches between Wi-Fi and cellular, WebSocket ping/pong timeout tuning, reconnect logic) |
| 31 | Rename skill store — rename "skill catalog" / "skill" terminology to avoid confusion with Claude Code's built-in skill repository. Candidate: "focus" (focus catalog, focus files). Open to alternatives |
| 33 | tmux control mode integration — use `tmux -C` control mode instead of raw PTY for terminal interaction. Enables native scrollback access, clean session disconnect/reconnect, and machine-parseable event notifications for agent observability |
| 34 | Project detail roadmap not found — when viewing project details the roadmap panel shows "not found" even though vision loads correctly. Investigate roadmap file detection / API endpoint for the project detail view |
| 37 | Shared Claude versions volume — Claude CLI stores its versions in `~/.local/share/claude/versions` inside each container, consuming significant disk space per project. Create a shared Docker volume for this path and mount it into all containers that use a Claude runner |
| 38 | Web UI hangs on trust prompt — when attaching to a container/tmux session where Claude CLI is showing the "trust this folder" security prompt, the Web UI hangs instead of rendering the prompt interactively |
| 39 | Add Maven to dev container — the `dev` build target does not include `mvn`. Install Maven via SDKMAN! in the Dockerfile so Java/Maven projects work out of the box |
| 40 | Fix PATH on container startup — Claude Code reports PATH issues inside the container on startup. Investigate entrypoint.sh and shell profile sourcing |
| 41 | Auto-build missing Docker image — when the Docker image is missing, automatically trigger a build instead of failing. Detect missing image in the launch flow and run `build.sh` |
| 42 | Fix Java in dev container — Java isn't working in the dev build target. Investigate SDKMAN!/JDK installation, JAVA_HOME, and PATH in the Dockerfile dev stage |
| 43 | Multi-repo submodule support — support projects consisting of several services in separate repos via git submodules, so they can be managed as a single Daedalus project with shared skills and configuration |
| 44 | Local LLM support (Ollama) — add a local runner that connects Claude Code to Ollama for companies that cannot use remote LLMs. Investigate Claude Code's Ollama integration and create a `local` runner profile |
| 45 | Post-install onboarding — after download and install, users don't know what to do next. Add a first-run wizard, `daedalus init` command, or getting-started guide that appears after installation |
| 46 | Clarify value proposition — improve README, landing page, and first-run messaging to clearly explain what Daedalus does and why users need it. Focus on the "hands-off AI coding in a safe container" pitch |
