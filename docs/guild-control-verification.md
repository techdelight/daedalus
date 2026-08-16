# Host verification: the Guild Master's control-plane client

The runbook for BACKLOG #72 — the change that mounts the control plane's
**restricted agent socket** into the Guild Master's container, which is what lets
the Guild Master act at all (as a proposal-tier caller). Companion to
[`m5-verification.md`](m5-verification.md): same shape, same reason — the parts
that need a real Docker daemon cannot be proven in the development container.

Everything below is scripted in
[`../scripts/verify-guild-control.sh`](../scripts/verify-guild-control.sh); this
document says what each check means and what a failure is telling you.

## Status

| # | Check | Where | Status |
|---|---|---|---|
| 1 | The mount is refused in every unsafe shape (not the Guild Master, not a socket, not `control-agent.sock`) | Unit | **Verified** — `go test ./core -run TestGuildControlSocketMount` |
| 2 | Mounted for the Guild Master, never for an ordinary project; env set only with a real mount | Unit | **Verified** — `go test ./internal/coordinator -run 'TestStart_(GuildMaster\|NormalProject)'` |
| 3 | The plane offers both sockets; the agent one is mode 0660 | Host, no Docker | **Verified** — `verify-guild-control.sh static` |
| 4 | Caller class over the real sockets: create allowed, cancel → proposal, self-confirm refused, human confirm executes | Host, no Docker | **Verified** — `verify-guild-control.sh static` |
| 5 | The socket arrives inside the container and is a socket there | **Host, Docker** | Pending |
| 6 | The container user can *open* it (uid/permission) | **Host, Docker** | Pending |
| 7 | The entrypoint wires `guild-control` in the agent's MCP config | **Host, Docker** | Pending |
| 8 | An ordinary project's container gets none of it | **Host, Docker** | Pending |

Checks 1–4 run anywhere Go and `curl` exist. 5–8 are the container half.

## Part 1 — without Docker

```bash
bash scripts/verify-guild-control.sh static
```

Expect `15 passed, 0 failed`. What it proves, and why each line is there:

- **The contracts.** The mount function is fail-closed three ways, and the launch
  args are asserted rather than assumed. The `control.sock` refusal is the one
  worth reading the test for: caller class is decided by *which file* is mounted,
  so mounting the human socket at the agent's path would hand the Guild Master
  full authority silently. A wrong path yields **no tool**, never an unlimited one.
- **The distribution chain.** `guild-control-mcp` in `Dockerfile`, `build.sh` and
  `package-release.sh`. This class of gap has bitten three times (M12 `guild-mcp`,
  M13 `daedalus-control`, and the M15 `dev-release.yml` omission), which is why it
  is asserted rather than remembered.
- **The two sockets exist**, and the agent socket is `0660`. The mount namespace
  is the real boundary; the mode is just not leaving it world-writable.
- **The authority tiers over the real sockets**, which is the part worth running
  before you trust the feature. An agent caller creates a task (allowed — it
  cannot exceed policy), lists tasks (allowed), and asks to cancel: the answer is
  a **422 with a proposal**, not a silent success, and the task is still alive
  afterwards. It then tries to confirm its own proposal and is refused. The same
  confirm on the human socket executes, and only then does the task cancel.

## Part 2 — with Docker

```bash
daedalus guild-master                      # starts the plane, then the container
bash scripts/verify-guild-control.sh real  # inspects the running container
```

The launch order matters: a bind-mount source is resolved at `docker run`, so the
plane must be listening *before* the container starts. `daedalus guild-master`
does that for you — it calls the same auto-spawn `daedalus task` uses.

| Check | Command it runs | What a failure means |
|---|---|---|
| Mount present | `docker inspect -f '{{range .Mounts}}…'` | The plane was not running at launch (look for the coordinator's log line: *"no control-plane agent socket at …"*), or the launch did not go through `daedalus guild-master` |
| Human socket absent | same inspect | **Stop.** If `control.sock` is mounted into the container, the agent has human authority. Nothing in the plane will catch this — the class comes from the file |
| Socket inside | `docker exec … test -S /var/run/daedalus/control-agent.sock` | Mounted as a directory or a plain file: the source did not exist as a socket at launch |
| Can connect | a Node one-liner that opens the socket | **Almost always a uid mismatch.** The socket is `0660` and owned by the host user; the container runs as `claude` (uid 1000). Same-user flows are fine; a host uid ≠ 1000 cannot open it. Daedalus already logs a build-uid mismatch warning at launch — check for it |
| Tool wired | `jq .mcpServers["guild-control"] ~/.claude.json` | The entrypoint's `[ -S … ]` gate found no socket, i.e. the mount check above lied, or `jq` failed (the patch is deliberately non-fatal) |
| `guild-control-mcp` on the image | `docker exec … test -x /usr/local/bin/…` | The image predates the binary — rebuild (`daedalus --build <project>`) |

Then confirm the negative by hand, because a capability that leaks to ordinary
projects is worse than one that does not work:

```bash
daedalus my-app
docker inspect claude-run-my-app | grep control-agent   # expect NOTHING
docker exec claude-run-my-app sh -c 'jq ".mcpServers" ~/.claude.json'  # no guild-control
```

## Part 3 — end to end, by hand

Worth doing once, because it is the whole point of the feature:

1. Launch the Guild Master and ask its agent to create a bounded task for some
   registered project. It should succeed — creation is allowed, budget-clamped,
   and graded against an oracle frozen at the plane-owned target.
2. Ask it to cancel that task. It must come back saying it has **recorded a
   proposal**, not that it cancelled anything.
3. On the host: `daedalus task proposals list` shows the proposal with its
   originating caller class. `daedalus task proposals deny <id>` does nothing at
   all; `confirm` runs it as you.
4. Ask the Guild Master to confirm its own proposal. It must be refused — that
   refusal is what makes "the Guild Master cannot approve its own work" true in
   practice, and it is enforced at two independent layers.

If step 2 ever executes instead of proposing, the wrong socket is mounted. Check
`docker inspect` before anything else.
