# Persisting tools across restarts (`/opt/tools`)

Daedalus containers are **ephemeral** — when a session stops, the container is
removed and its writable filesystem layer is discarded. Only two locations
survive a restart, because they are host bind mounts:

- `/workspace` — the project source
- `/home/claude` — the per-project home (config, credentials, caches)

Anything installed **outside** those — a `sudo apt-get install`, a binary
dropped in `/usr/local/bin`, a system `pip install` — is **lost** on the next
start.

## The persistent tools prefix

To let a tool the agent installs at runtime survive restarts, Daedalus mounts a
**per-project persistent directory at `/opt/tools`** (Backlog #27), with
`/opt/tools/bin` on `PATH`. It is backed on the host by
`<DataDir>/tools/<project>/` and, unlike `/home/claude`, is dedicated to
executables so it stays small and inspectable.

**Install user-space tools into `/opt/tools`** and they persist:

```bash
# a standalone binary
curl -fsSL https://example.com/mytool -o /opt/tools/bin/mytool && chmod +x /opt/tools/bin/mytool

# a Go tool
GOBIN=/opt/tools/bin go install example.com/cmd/tool@latest

# anything that takes a prefix
pip install --prefix /opt/tools some-package   # then ensure /opt/tools/bin is used
```

Because `/opt/tools/bin` is on `PATH`, the tool is found in every later session.

## What does *not* persist (by design)

System package installs — `apt-get install`, or anything that writes to
`/usr/`, `/etc/`, `/var/` — are **not** persisted. This is deliberate: Daedalus
keeps base images **reproducible** (isolation-first), so the container's system
layer is disposable rather than a mutable, un-rebuildable snapshot. If a project
genuinely needs a system package on every start, add it to the appropriate
build stage in the `Dockerfile` (declared as code), not as a runtime install.

## Housekeeping

The tools directory is per-project on the host at `<DataDir>/tools/<project>/`.
Delete that directory to reset a project's persisted tools; it is recreated
empty on the next start.
