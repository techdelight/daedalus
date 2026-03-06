# ── Stage 1: base ────────────────────────────────────────────────────────────
# Minimal Debian with Claude CLI, git, and networking tools.
FROM debian:bookworm-slim AS base

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      ca-certificates curl git openssh-client jq && \
    rm -rf /var/lib/apt/lists/*

ARG CLAUDE_UID=1000
RUN useradd -m -u "$CLAUDE_UID" -s /bin/bash claude
RUN mkdir -p /workspace && chown claude:claude /workspace

USER claude

# WARNING: This downloads and executes an unverified script from claude.ai.
# The URL is not version-pinned and has no checksum verification.
# If supply-chain integrity is a concern, audit /tmp/install.sh before building.
RUN curl -fsSL https://claude.ai/install.sh > /tmp/install.sh && \
    chmod u+x /tmp/install.sh && cd /tmp && ./install.sh

USER root
RUN mv /home/claude/.local /opt/claude && \
    mkdir -p /opt/claude/defaults && \
    chown -R claude:claude /opt/claude

COPY --chown=claude:claude .claude.json /opt/claude/defaults/.claude.json
COPY --chown=claude:claude settings.json /opt/claude/defaults/settings.json
COPY --chown=claude:claude entrypoint.sh /opt/claude/bin/entrypoint.sh
RUN chmod +x /opt/claude/bin/entrypoint.sh

ENV PATH="$PATH:/opt/claude/bin"
ENV CLAUDE_CONFIG_DIR="/home/claude/.claude-config"

USER claude
WORKDIR /workspace
ENTRYPOINT ["/opt/claude/bin/entrypoint.sh"]

# ── Stage 2: utils ───────────────────────────────────────────────────────────
# Shared utilities needed by both dev and godot stages.
FROM base AS utils

USER root
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      unzip wget build-essential && \
    rm -rf /var/lib/apt/lists/*

USER claude

# ── Stage 3: dev ─────────────────────────────────────────────────────────────
# Full development environment: Go, Python 3, OpenJDK 17, Maven, Kotlin.
FROM utils AS dev

ARG KOTLIN_VERSION=2.1.10

USER root

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      golang-go \
      python3 python3-pip python3-venv \
      openjdk-17-jdk-headless maven \
      docker.io && \
    rm -rf /var/lib/apt/lists/*

RUN wget -q "https://github.com/JetBrains/kotlin/releases/download/v${KOTLIN_VERSION}/kotlin-compiler-${KOTLIN_VERSION}.zip" \
      -O /tmp/kotlin-compiler.zip && \
    unzip -q /tmp/kotlin-compiler.zip -d /opt && \
    rm /tmp/kotlin-compiler.zip && \
    ln -s /opt/kotlinc/bin/kotlin /usr/local/bin/kotlin && \
    ln -s /opt/kotlinc/bin/kotlinc /usr/local/bin/kotlinc

RUN usermod -aG docker claude

USER claude

# ── Stage 4: godot ───────────────────────────────────────────────────────────
# Godot 4.x engine for headless use (game CI, exports, tests).
FROM utils AS godot

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
