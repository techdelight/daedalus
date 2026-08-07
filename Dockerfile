# Daedalus runner image — multi-stage build.
#
# Layer strategy (#51): the frequently-rebuilt Daedalus Go binaries
# (skill-catalog-mcp, project-mgmt-mcp, guild-mcp, daedalus-runner) are COPY'd LAST,
# in a thin per-target leaf stage, so a Daedalus version bump invalidates
# only that final layer and leaves the expensive toolchain download layers
# (Go, SDKMAN, Godot, Copilot) cached. The *-base stages below are the
# stable parents; the buildable targets (base/utils/dev/godot/copilot-*)
# are the leaves at the bottom.

# ── Parent: agent-base ───────────────────────────────────────────────────────
# Minimal Debian with Claude CLI, git, config files, and the entrypoint.
# Holds no Daedalus Go binaries (see leaf targets below).
FROM debian:bookworm-slim AS agent-base

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      ca-certificates curl git openssh-client jq && \
    rm -rf /var/lib/apt/lists/*

ARG CLAUDE_UID=1000
RUN useradd -m -u "$CLAUDE_UID" -s /bin/bash claude
RUN mkdir -p /workspace && chown claude:claude /workspace

USER claude

# Claude CLI, pinned to a specific version (#51, supply-chain). The bootstrap
# script takes the version/channel as its first argument; passing a fixed
# CLAUDE_VERSION makes the build reproducible instead of tracking bleeding-edge
# `latest`, and the installer itself verifies the downloaded binary's SHA-256
# against Anthropic's release manifest.json (so the binary is checksum-verified;
# the bootstrap script is still fetched fresh, the residual, lower risk).
# Claude auto-updates at runtime via the shared versions cache, so this is a
# floor, not a freeze. Bump with --build-arg CLAUDE_VERSION=X.Y.Z (or 'stable').
ARG CLAUDE_VERSION=2.1.221
RUN curl -fsSL https://claude.ai/install.sh > /tmp/install.sh && \
    chmod u+x /tmp/install.sh && cd /tmp && ./install.sh "${CLAUDE_VERSION}"

USER root
RUN mv /home/claude/.local /opt/claude && \
    mkdir -p /opt/claude/defaults && \
    ln -sf "$(readlink /opt/claude/bin/claude | sed 's|/home/claude/.local|/opt/claude|')" /opt/claude/bin/claude && \
    chown -R claude:claude /opt/claude

COPY --chown=claude:claude claude.json /opt/claude/defaults/.claude.json
COPY --chown=claude:claude settings.json /opt/claude/defaults/settings.json
COPY --chown=claude:claude entrypoint.sh /opt/claude/bin/entrypoint.sh
RUN chmod +x /opt/claude/bin/entrypoint.sh

# Per-project persistent tools prefix (#27): bind-mounted at /opt/tools at
# runtime. Create the mount point + put its bin on PATH so tools the agent
# installs there survive restarts and are found.
RUN mkdir -p /opt/tools/bin && chown -R claude:claude /opt/tools

ENV PATH="$PATH:/opt/claude/bin:/opt/tools/bin"
ENV CLAUDE_CONFIG_DIR="/home/claude/.claude-config"

USER claude
WORKDIR /workspace
ENTRYPOINT ["/opt/claude/bin/entrypoint.sh"]

# ── Parent: utils-base ───────────────────────────────────────────────────────
# Shared utilities needed by both dev and godot.
FROM agent-base AS utils-base

USER root
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      unzip wget build-essential && \
    rm -rf /var/lib/apt/lists/*

USER claude

# ── Parent: dev-base ─────────────────────────────────────────────────────────
# Full development environment: Go, Python 3, JDK, Maven, Kotlin.
# JVM tooling (Java, Maven, Kotlin) installed via SDKMAN instead of apt.
FROM utils-base AS dev-base

ARG GO_VERSION=1.25.0

USER root

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      zip curl \
      python3 python3-pip python3-venv \
      docker.io && \
    rm -rf /var/lib/apt/lists/*

RUN curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" | tar -C /usr/local -xz
ENV PATH="/usr/local/go/bin:$PATH"

RUN usermod -aG docker claude

USER claude

# Install SDKMAN and JVM tooling as the claude user
RUN curl -s "https://get.sdkman.io" | bash
SHELL ["/bin/bash", "-c"]
RUN source "$HOME/.sdkman/bin/sdkman-init.sh" && \
    sdk install java 21.0.6-tem && \
    sdk install maven && \
    sdk install kotlin
ENV SDKMAN_DIR="/home/claude/.sdkman"
ENV PATH="$SDKMAN_DIR/candidates/java/current/bin:$SDKMAN_DIR/candidates/maven/current/bin:$SDKMAN_DIR/candidates/kotlin/current/bin:$PATH"

# ── Parent: godot-base ───────────────────────────────────────────────────────
# Godot 4.x engine for headless use (game CI, exports, tests).
FROM utils-base AS godot-base

ARG GODOT_VERSION=4.4.1

USER root

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      libx11-6 libxcursor1 libxinerama1 libxrandr2 libxi6 \
      libgl1 libasound2 libpulse0 libdbus-1-3 libfontconfig1 && \
    rm -rf /var/lib/apt/lists/*

RUN wget -q "https://github.com/godotengine/godot/releases/download/${GODOT_VERSION}-stable/Godot_v${GODOT_VERSION}-stable_linux.x86_64.zip" \
      -O /tmp/godot.zip && \
    unzip -q /tmp/godot.zip -d /tmp && \
    mv /tmp/Godot_v${GODOT_VERSION}-stable_linux.x86_64 /usr/local/bin/godot && \
    chmod +x /usr/local/bin/godot && \
    rm /tmp/godot.zip

USER claude

# ── Parent: copilot-base-base ────────────────────────────────────────────────
# Minimal base with Copilot CLI instead of Claude CLI.
FROM agent-base AS copilot-base-base

USER claude
# Copilot CLI, pinned to a specific version (#51, supply-chain). The installer
# reads the VERSION env var (default `latest`); pinning it makes the build
# reproducible, and the installer verifies the downloaded binary's SHA-256
# against the release's SHA256SUMS.txt. Bump with --build-arg COPILOT_VERSION=vX.Y.Z.
ARG COPILOT_VERSION=v1.0.78
RUN echo 'n' | curl -fsSL https://gh.io/copilot-install | VERSION="${COPILOT_VERSION}" bash

USER root
RUN mv /home/claude/.local/bin/copilot /usr/local/bin/copilot

USER claude
ENV RUNNER="copilot"

# ── Parent: copilot-dev-base ─────────────────────────────────────────────────
# Copilot with full development environment: Go, Python 3, JDK, Maven, Kotlin.
FROM copilot-base-base AS copilot-dev-base

ARG GO_VERSION=1.25.0

USER root

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      unzip wget zip curl build-essential \
      python3 python3-pip python3-venv \
      docker.io && \
    rm -rf /var/lib/apt/lists/*

RUN curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" | tar -C /usr/local -xz
ENV PATH="/usr/local/go/bin:$PATH"

RUN usermod -aG docker claude

USER claude

# Install SDKMAN and JVM tooling as the claude user
RUN curl -s "https://get.sdkman.io" | bash
SHELL ["/bin/bash", "-c"]
RUN source "$HOME/.sdkman/bin/sdkman-init.sh" && \
    sdk install java 21.0.6-tem && \
    sdk install maven && \
    sdk install kotlin
ENV SDKMAN_DIR="/home/claude/.sdkman"
ENV PATH="$SDKMAN_DIR/candidates/java/current/bin:$SDKMAN_DIR/candidates/maven/current/bin:$SDKMAN_DIR/candidates/kotlin/current/bin:$PATH"

# ── Daedalus artifact layer (#51) ────────────────────────────────────────────
# Appended LAST to every buildable target so a `build.sh` binary rewrite
# invalidates only this thin final layer, leaving all toolchain download
# layers above cached. Each target `FROM`s its stable *-base parent and adds
# the three Daedalus Go binaries. The COPY runs as the build daemon regardless
# of the current USER; --chown sets ownership.

FROM agent-base AS base
COPY --chown=claude:claude skill-catalog-mcp /usr/local/bin/skill-catalog-mcp
COPY --chown=claude:claude project-mgmt-mcp /usr/local/bin/project-mgmt-mcp
COPY --chown=claude:claude guild-mcp /usr/local/bin/guild-mcp
COPY --chown=claude:claude daedalus-runner /usr/local/bin/daedalus-runner

FROM utils-base AS utils
COPY --chown=claude:claude skill-catalog-mcp /usr/local/bin/skill-catalog-mcp
COPY --chown=claude:claude project-mgmt-mcp /usr/local/bin/project-mgmt-mcp
COPY --chown=claude:claude guild-mcp /usr/local/bin/guild-mcp
COPY --chown=claude:claude daedalus-runner /usr/local/bin/daedalus-runner

FROM dev-base AS dev
COPY --chown=claude:claude skill-catalog-mcp /usr/local/bin/skill-catalog-mcp
COPY --chown=claude:claude project-mgmt-mcp /usr/local/bin/project-mgmt-mcp
COPY --chown=claude:claude guild-mcp /usr/local/bin/guild-mcp
COPY --chown=claude:claude daedalus-runner /usr/local/bin/daedalus-runner

FROM godot-base AS godot
COPY --chown=claude:claude skill-catalog-mcp /usr/local/bin/skill-catalog-mcp
COPY --chown=claude:claude project-mgmt-mcp /usr/local/bin/project-mgmt-mcp
COPY --chown=claude:claude guild-mcp /usr/local/bin/guild-mcp
COPY --chown=claude:claude daedalus-runner /usr/local/bin/daedalus-runner

FROM copilot-base-base AS copilot-base
COPY --chown=claude:claude skill-catalog-mcp /usr/local/bin/skill-catalog-mcp
COPY --chown=claude:claude project-mgmt-mcp /usr/local/bin/project-mgmt-mcp
COPY --chown=claude:claude guild-mcp /usr/local/bin/guild-mcp
COPY --chown=claude:claude daedalus-runner /usr/local/bin/daedalus-runner

FROM copilot-dev-base AS copilot-dev
COPY --chown=claude:claude skill-catalog-mcp /usr/local/bin/skill-catalog-mcp
COPY --chown=claude:claude project-mgmt-mcp /usr/local/bin/project-mgmt-mcp
COPY --chown=claude:claude guild-mcp /usr/local/bin/guild-mcp
COPY --chown=claude:claude daedalus-runner /usr/local/bin/daedalus-runner
