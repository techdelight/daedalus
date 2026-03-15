# Homebrew Installation Plan

## Problem

Daedalus is currently installed via `curl | bash` using `install.sh`. macOS users
expect `brew install` as the standard installation method. Adding Homebrew support
makes installation, upgrades, and uninstalls seamless on macOS.

## Key Constraint

`os.Executable()` → `ScriptDir` resolution (`internal/config/config.go:122-126`)
requires runtime files to live next to the binary:

```go
exe, err := os.Executable()
// ...
cfg.ScriptDir, err = filepath.Abs(filepath.Dir(exe))
```

The binary locates `Dockerfile`, `docker-compose.yml`, `entrypoint.sh`,
`claude.json`, `settings.json`, `logo.txt`, and `config.json` relative to its own
path. A plain Homebrew `bin/` symlink would break this resolution.

## Solution: `libexec/` + Shell Wrapper (Zero Go Changes)

Homebrew's standard pattern for binaries that need co-located files:

1. Install the real binary and all runtime files into `libexec/daedalus/`.
2. Place a thin shell wrapper in `bin/daedalus` that does:
   ```bash
   #!/bin/bash
   exec "$(brew --prefix)/libexec/daedalus/daedalus" "$@"
   ```
3. `os.Executable()` returns the `libexec/` path → `ScriptDir` resolves correctly.

No Go code changes required.

## Tap Repository

Create `techdelight/homebrew-tap` on GitHub:

```
homebrew-tap/
├── Formula/
│   └── daedalus.rb
└── README.md
```

Users install with:

```bash
brew tap techdelight/tap
brew install daedalus
```

## Formula Design

```ruby
class Daedalus < Formula
  desc "Docker-based development environment for Claude Code"
  homepage "https://github.com/techdelight/daedalus"
  version "VERSION"
  license "Apache-2.0"

  on_macos do
    on_arm do
      url "https://github.com/techdelight/daedalus/releases/download/vVERSION/daedalus-darwin-arm64"
      sha256 "SHA256_DARWIN_ARM64"
    end
    on_intel do
      url "https://github.com/techdelight/daedalus/releases/download/vVERSION/daedalus-darwin-amd64"
      sha256 "SHA256_DARWIN_AMD64"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/techdelight/daedalus/releases/download/vVERSION/daedalus-linux-arm64"
      sha256 "SHA256_LINUX_ARM64"
    end
    on_intel do
      url "https://github.com/techdelight/daedalus/releases/download/vVERSION/daedalus-linux-amd64"
      sha256 "SHA256_LINUX_AMD64"
    end
  end

  # Runtime files downloaded as resources
  %w[claude.json docker-compose.yml Dockerfile entrypoint.sh settings.json logo.txt config.json].each do |f|
    resource f do
      url "https://github.com/techdelight/daedalus/releases/download/vVERSION/#{f}"
      sha256 "SHA256_#{f}"
    end
  end

  depends_on "docker" => :recommended

  def install
    libexec_dir = libexec/"daedalus"
    libexec_dir.mkpath

    # Install the binary
    bin_name = Dir["daedalus-*"].first
    libexec_dir.install bin_name => "daedalus"
    (libexec_dir/"daedalus").chmod 0755

    # Install runtime files
    %w[claude.json docker-compose.yml Dockerfile entrypoint.sh settings.json logo.txt config.json].each do |f|
      resource(f).stage { libexec_dir.install f }
    end

    # Patch config.json to use Homebrew-managed data directory
    inreplace libexec_dir/"config.json", /"data-dir"\s*:\s*"[^"]*"/, "\"data-dir\": \"#{var}/daedalus\""

    # Shell wrapper
    (bin/"daedalus").write <<~SH
      #!/bin/bash
      exec "#{libexec_dir}/daedalus" "$@"
    SH
  end

  def post_install
    (var/"daedalus").mkpath
  end

  def caveats
    <<~EOS
      Daedalus requires Docker Desktop (or a compatible Docker daemon).
      Ensure Docker is running before using Daedalus.

      Project data is stored in:
        #{var}/daedalus
    EOS
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/daedalus --version")
  end
end
```

## Automation

### Formula Generator: `scripts/generate-formula.sh`

A template script that:

1. Reads `VERSION` from the repo root.
2. Downloads (or receives) release asset checksums.
3. Substitutes version and SHA256 values into the formula template.
4. Outputs `daedalus.rb` ready for commit to the tap repo.

```bash
#!/usr/bin/env bash
set -euo pipefail

VERSION=$(cat VERSION)
TAG="v${VERSION}"
BASE_URL="https://github.com/techdelight/daedalus/releases/download/${TAG}"

# Compute SHA256 for each asset
sha256_for() {
    curl -fsSL "${BASE_URL}/$1" | shasum -a 256 | cut -d' ' -f1
}

# ... generate formula from template with computed checksums
```

### CI Job: `update-homebrew` in `.github/workflows/release.yml`

Add a new job after `publish` that:

1. Checks out the tap repository (`techdelight/homebrew-tap`).
2. Runs `scripts/generate-formula.sh` to produce the updated formula.
3. Commits and pushes the formula to the tap repo.

```yaml
update-homebrew:
  name: Update Homebrew Formula
  needs: publish
  runs-on: ubuntu-latest
  steps:
    - name: Checkout main repo
      uses: actions/checkout@v4

    - name: Checkout tap repo
      uses: actions/checkout@v4
      with:
        repository: techdelight/homebrew-tap
        token: ${{ secrets.HOMEBREW_TAP_TOKEN }}
        path: homebrew-tap

    - name: Generate formula
      run: bash scripts/generate-formula.sh > homebrew-tap/Formula/daedalus.rb

    - name: Commit and push
      working-directory: homebrew-tap
      run: |
        git config user.name "github-actions[bot]"
        git config user.email "github-actions[bot]@users.noreply.github.com"
        git add Formula/daedalus.rb
        git commit -m "Update daedalus to ${GITHUB_REF_NAME#v}"
        git push
```

## Manual Prerequisites

Before the first release with Homebrew support:

1. **Create tap repository**: `techdelight/homebrew-tap` on GitHub with a
   `Formula/` directory and a README.
2. **Create GitHub secret**: `HOMEBREW_TAP_TOKEN` — a personal access token with
   `repo` scope for the tap repository.
3. **Write `scripts/generate-formula.sh`**: implement the full template
   substitution script.
4. **Add `update-homebrew` job**: extend `.github/workflows/release.yml`.

## Installation Methods After Implementation

| Method | Command |
|--------|---------|
| Homebrew (new) | `brew tap techdelight/tap && brew install daedalus` |
| curl (existing) | `curl -fsSL https://raw.githubusercontent.com/techdelight/daedalus/master/install.sh \| bash` |
| From source | `go build ./cmd/daedalus` |
