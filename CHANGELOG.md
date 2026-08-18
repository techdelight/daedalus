# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

### Added
- **`daedalus task checks <id> --set '<cmd>'` — amendable per-task checks.** A
  check is written by a human before the work exists, and a wrong one — aimed at
  the wrong file, or asserting something the objective never asked for — could
  never be corrected: every attempt ran the same broken command, `retry` re-ran
  it against new work and `reverify` re-ran it against the same work, so the Task
  could never pass however good the artifact was. The only escape was to abandon
  the Task and recreate it, losing its history and its budget to a typo.
  - **It does not touch what the freeze protects.** The project's policy lives in
    a committed `.daedalus/verify.json`, is read at `base_sha` and is hashed onto
    the Task precisely so nobody can lower the bar after seeing the work — that is
    untouched. Per-task checks were deliberately kept *outside* that hash when
    they were built, so amending one changes nothing the freeze covers.
  - **Human-only, and not proposable.** Validation and the caller rule come from
    the same `resolveTaskChecks` the create path uses, so there is one copy of
    "the party being graded does not choose the commands run inside the verifier".
    There is deliberately no operation name and no tier entry: with only
    "execute" and "propose" available, a proposal would let an agent author a
    command and have a human wave it into the verifier.
  - **It withdraws the free re-verify.** A replay is uncharged because a verdict
    from a broken harness judged nothing; once a check has moved, the next
    grading is a *new* grading against a *different* oracle and is charged like
    any other. Without that, softening a check and replaying for free could be
    repeated until something passed, with the budget never noticing.
- **`daedalus task verify <id> --ignore-result` — waive a failing result.** For
  when you have read the failure and are proceeding anyway. The verifier still
  runs, the failure is still recorded, the artifact still carries `verify=fail`,
  and the Task moves to the approval gate on the named human's authority.
  - **It never records a pass.** `verified` means "the plane applied its own
    oracle and the artifact passed", and approval, integration and dependency
    satisfaction all read it that way — so a waived artifact never reaches that
    state. The log gets two facts in order: the finding, then the override. What
    a waiver changes is not the finding but who is answerable for proceeding past
    it, which is what an operator overriding a check is actually doing.
  - **Agents may not ask for it**, and not as a proposal either: a proposal would
    put the graded party's own waiver in front of a human as a routine-looking
    confirmation. An agent that could waive its own grading has no oracle at all.
- **`daedalus task integrate <id> --into-branch`, and an honest default message.**
  Integration advances the plane-owned target, which is projected into the repo as
  `refs/daedalus/target` — a ref nobody checks out, deliberately, so landing can
  never disturb a working tree. The design reason is structural (keying the target
  by canonical repo path rather than a branch ref is what closed the Sprint-59
  oracle-laundering hole, because a linked worktree *can* write branch refs), but
  the consequence appeared in no document and no command output: the first honest
  read of a successful landing was "nothing happened". `task integrate` now says
  in plain words that your branch was not changed and how to adopt the commit, and
  `--into-branch` opts into a **guarded fast-forward**. It refuses rather than
  resolves — no merge commit, no rebase, no stash, no `--force` — on a detached
  HEAD, on a dirty working tree (the one case where winding forward would touch
  files somebody is editing), and on a branch that has diverged. The branch step
  runs *after* the integration transaction, so a refusal can never unland the
  work; every outcome is reported as a note rather than an error. A confirmed
  agent **proposal** never advances a branch: the human confirming it is not
  necessarily sitting in that checkout, and a surprise fast-forward is exactly the
  side effect a proposal must not carry. Filed as backlog #79 rather than closed —
  the Web UI and TUI still say "integrated" with no equivalent explanation, and
  there is no way to ask for the fast-forward after the fact.

- **`daedalus task reverify <id>` — re-grade an artifact without re-running the
  Job (M19, Sprint 65).** A verdict can be wrong for reasons that say nothing
  about the work: a verifier that never ran the check it reported on, or an
  acceptance policy that fails on an advisory finding. Until now the only remedy
  was `retry`, which dispatches a fresh Job and discards an artifact that was
  never in question — so a defect in the *grading* cost an attempt and the work
  along with it. Verification is a function of `(artifact, policy, environment)`
  and the artifact is immutable and content-addressed, so re-grading it should be
  cheap; the only thing missing was the door.
  - **The same verification path grades it.** `reverify` returns the Task to
    `candidate` and calls the one `VerifyTask`, with every gate intact. A second
    grading path was the obvious implementation and the wrong one: two paths
    drift, and the weaker becomes the oracle.
  - **A harness fault is not charged a review cycle**, on the same principle
    `CountReviewCycles` already applied to an interrupted verification — entering
    `verifying` is not being verified. An `--amended` re-grade *is* charged,
    because the oracle changed and a real grading happened. No mode can spend an
    attempt: re-verification creates no Job, and attempts are counted from Jobs.
  - **It must never become an appeal.** The integrity gate and the null-agent
    floor are refused outright (`unappealable`) — both are findings about the
    artifact, and re-grading an integrity-gate rejection would let a self-grading
    diff through on the second ask. `verify_failed` is appealable, because from
    outside it cannot be distinguished from a broken oracle; that ambiguity is
    answered by tiering the operation (agents propose, humans confirm) and
    recording every re-grading against the verdict it set aside.
  - **`--amended` re-freezes the policy and records the lineage** (old hash → new
    hash), for the case that motivated the milestone: correcting a bad oracle
    means committing to the repo, which moves the target and makes the artifact's
    base stale, so a plain replay cannot help. A verdict produced under a policy
    amended after the artifact existed is weaker than one produced under the
    policy the artifact faced, and the event log is the only place that survives.
  - **An unreachable artifact is refused, not failed** (`artifact_gone`). Without
    the check the operator got `git diff --name-only: exit status 128`; recording
    a verification *failure* for work that was never examined would put a false
    verdict in an append-only log.
  - Prior art this follows: SWE-bench grades a saved patch from a prediction file
    and re-grades via a separate command, never reading the trajectory; Argo's
    `retry --restart-successful` re-runs chosen nodes of the same workflow
    object; LangGraph names *replay* and *fork* as distinct operations. The common
    shape is a durable addressable artifact plus a separately invocable grading
    step.

- **Every Job now has a log of its own, so a failed Job can be diagnosed (#77).**
  What the database kept about a failed run was `err.Error()` — "exit status 1" —
  while the agent's actual output went to the daemon's stdout and from there to
  the single shared `control.log`, interleaved with every concurrent Job's and
  with the plane's own logging, keyed by nothing. Present, and unreadable. A Job's
  output is now tee'd to `<data-dir>/.daedalus/jobs/<job-id>.log` as it runs, and
  the path is recorded on the Job row, so `task status` prints it under the Job.
  - **The service chooses the path, the runner writes the file.** One place
    decides where a log lives, so every runner writes where the plane will later
    look. `StubRunner` honours it too, which is what keeps the whole chain
    exercised on the only path this environment can run — no Docker required.
  - **The path is recorded only once the file exists**, and existence rather than
    the run's outcome is deliberately the test: it is the one signal that survives
    every exit path. A wall-clock timeout abandons the runner goroutine mid-write,
    a cancellation returns before the runner does, and an open failure is reported
    by log line only — in all three the file on disk still answers "is there
    something to read" correctly. A row pointing at a path that resolves to
    nothing would be worse than an empty one, because it sends a reader looking.
  - **stdout and stderr share one writer, on purpose.** os/exec special-cases the
    two being the same value by handing the child a single pipe, which preserves
    the child's own interleaving and leaves exactly one copying goroutine — two
    separate `MultiWriter`s would have lost the ordering and raced on the file.
    The child's stderr therefore arrives on the daemon's stdout, which is a
    non-event: the daemon sends both to the same place.
  - **Mode `0600`, in a `0700` directory**, rather than the `0644`/`0755` the
    daemon's own logs use. This file holds raw agent output, which is exactly
    where a leaked token or credential would show up.
  - `jobs.log_path` is an additive, idempotent migration. A Job that ran before
    this reads back with no log — which is not a default standing in for the
    truth, it *is* the truth.
  - **Two limits remain open and are not claimed as fixed.** The agent's own
    session transcript still lives in the throwaway job home and is still deleted
    on every exit path including failure, so the log captures what the agent
    printed rather than what it thought; and nothing prunes these logs yet, so
    they accumulate one file per Job.
- **Per-task acceptance checks: `task create --check <cmd>` (repeatable).** The
  project's `.daedalus/verify.json` is task-independent — it answers "does this
  artifact still meet the standing bar", and cannot answer "did this task deliver
  what it promised", having been committed before the task existed. Until now the
  only answer to the second question was a human reading the diff at the approval
  gate. A `--check` is recorded on the Task and run by the verifier, so the
  objective gets a machine-checkable definition of done, stated at the same moment
  as the objective itself.
  - **Appended, never substituted, and run after the project's checks.** A Task
    can only raise its own bar. The ordering is load-bearing rather than tidy: the
    verifier's checkout is writable and checks run in sequence, so running the
    project's checks first means a task-supplied command can only sabotage itself.
  - **Human callers only.** A check is a command executed inside the verifier, and
    the party being graded does not choose those; an agent asking is refused with
    a typed `forbidden`. Task creation *without* checks stays a bounded write the
    Guild Master may perform.
  - **Outside the frozen `acceptance_hash`**, which covers the project's policy
    alone — so a Task with its own checks does not read as policy drift.
  - Stored via an idempotent `task_checks` migration; a task written before this
    reads back with none. Shown by `task create` and `task status`, and a task
    created *without* any now says so, because "graded by the project policy
    alone" is a thing worth knowing at the moment you create the work.
  - Related clarification: `--acceptance <note>` is free text and is **not**
    executed. `task status` and `--help` now say so — it had the shape of this
    feature with none of the mechanism, which is exactly the wrong thing for a
    flag called "acceptance" to have.

### Fixed
- **Reconcile now settles work that was merged outside the plane.** Daedalus does
  not own the repository: a human can merge a branch it rejected, and people do,
  most often when the check rather than the work was wrong. The database then
  carried a claim anyone could see was false — a Task recorded as rejected and
  never landed, whose commits are demonstrably in the tree — and everything
  downstream reads that claim, so a Task waiting on this one would block forever
  and the board would show shipped work as failed. Reconcile now asks, for each
  rejected Task, whether its artifact's commits are contained in the integration
  target, reusing `ArtifactIsLanded` — the same containment test the integration
  transaction runs, so it recognises a rebased landing that shares no sha as well
  as a fast-forward one. It is not a bypass: nothing becomes `verified`, the
  rejection and its reason stay in the log, and the Task walks the approval gate
  rather than jumping — there is no `rejected → integrated` edge and there must
  not be one, since a single hop from a refusal to a landing is the shape of
  every laundering bug the transition table exists to prevent.
- **A Job reaped by reconcile no longer destroys its Task.** When reconcile finds
  a `working` Job whose session has vanished it fails the Job — correctly, the
  attempt is over — but it used to drive the **Task** to `failed` as well, which
  is terminal. `dispatch`, `retry`, `replan` and `reverify` all refuse a `failed`
  Task and no transition leaves that state, so a single liveness reading
  destroyed the objective, its budget and every recovery command at once. The
  reading can be wrong in ordinary ways: a control daemon restarted mid-run
  reads exactly like a dead session, and so does a container removed by hand.
  The Task now goes to `rejected` — the state the retry ladder already
  understands and `prepareDispatch` already accepts — so the remedy is
  `task dispatch <id>`. This restores a path rather than inventing one; the
  precedent is two cases up in the same function, where a Task whose dispatch
  died before any Job existed is returned to `rejected` on the reasoning that
  nothing was ever attempted. A reaped Job is that situation with one more row in
  the database. The attempt is still charged against `max-attempts`: the plane
  cannot distinguish a Job killed by a daemon bounce from one that died on its
  own, and silently refunding on a reading it is unsure of is the worse error.
  Found the hard way — a real Task went `working → failed` with
  "reconcile: the job's session is gone" and had no way back.
- **`task board` columns now say whose move it is, not which phase the work is
  in.** Reported from a real board: "In verification" held work needing
  **approval**, and "Awaiting approval" held work needing **integration**. Both
  titles were true of the lifecycle and false about what to do, which is the
  wrong trade for a board — it is read to answer "whose move is it, and what is
  the move", and a column titled for a phase buries that under work the plane is
  still handling. `in_review` splits into **Being verified** (`candidate`,
  `verifying` — the plane is working, nothing is being asked of anyone),
  **Rejected — needs a decision** (`rejected`, its own column because it is one
  command from running again but nothing will move it until a human chooses), and
  **Awaiting your approval** (`verified`, `approval_required`); `approved` moves
  to **Approved — ready to land**, since filing it under "awaiting approval"
  described something that had already happened. Nothing consumed the old column
  keys by name — the CLI and Web render whatever columns the response carries.
- **The integrity gate and the null-agent floor now measure against the Job's own
  base, not the Task's.** Both ask what *this Job did*, and a Job's diff is
  defined relative to the commit it was checked out at. The two values are
  normally identical — a Job is created at the Task's base, and `retry --rebase`
  moves the Task and then dispatches a fresh Job — so the difference was latent
  until `reverify --amended` became the first operation to re-pin a Task while
  keeping an existing artifact. Measured against the Task's base the diff no
  longer describes the Job at all: it describes the divergence between two trees,
  and every file the new base added reads as a file the Job deleted. An amended
  re-grade whose corrected policy was itself `.daedalus/verify.json` would trip
  the integrity gate on the very commit that fixed the oracle. Using the Job's
  base weakens nothing — the gate still catches every acceptance file the Job
  touched, measured from where the Job actually started.

- **The built-in acceptance policy no longer rejects a Task for a warning about
  the repository it was handed.** `DefaultAcceptancePolicy` ran `daedalus docs
  lint --ci`, and `--ci` treats a warning as a failure. A roadmap between
  milestones — a deliberate, supported state the linter itself exits 0 on —
  emits exactly one warning and no errors, so the built-in oracle returned
  `verify_failed` on every artifact from every project that had declared no
  `.daedalus/verify.json`, whatever the work had been. Measured on a real host:
  T-8 ("restyle the Web UI sidebar") was rejected on `no milestone is marked (In
  Progress)` having touched nothing outside `internal/web`.
  - **This was the first verify in the project's history to actually execute a
    check command**, the entrypoint bypass having landed hours earlier — and the
    first genuine verdict the plane ever produced was a statement about the
    repository rather than about the change it was asked to judge. Worth
    recording rather than quietly fixing: the defect was invisible for as long as
    the checks were not running, and became visible the moment they did.
  - The default now gates on what is **broken**, not on what is merely remarked
    upon. A project that wants warnings fatal declares `--ci` in its own
    `verify.json` — one line, and an explicit choice rather than an inherited
    one.
  - Pinned by a test that **derives** the requirement instead of asserting a
    remembered string: it lints the real `ROADMAP.md`/`SPRINTS.md`, and only if
    they carry warnings and no errors does it assert the default check has no
    warnings-fatal flag. If this repository's documents ever acquire a genuine
    error the premise fails and the test stands down on its own, because the
    default policy *should* reject that.
- **A project can now commit a verify policy at all.** `.gitignore` excluded
  `.daedalus/` as a directory, and git does not descend into an excluded
  directory, so `!.daedalus/verify.json` would have been inert — the ignore had
  to become `.daedalus/*` plus the negation. Until now a project could write a
  policy, see it sitting in the working tree, and have the plane freeze the
  built-in default instead with nothing to indicate it: the freeze reads `git
  show <base_sha>:.daedalus/verify.json`, and a file that was never committed
  does not exist at any sha. daedalus now declares its own policy — a single
  `daedalus docs lint`, because the hermetic verifier ships no Go toolchain and
  the module cache problem (backlog #74) is still open, so the docs linter
  remains the only check it can genuinely run.
- **A Job inherits its project's login even when the daemon's data dir is not the
  CLI's default.** Seeding a Job's home was already the fix for `Not logged in`,
  but it wrote to `<daemon's DataDir>/daedalus-job-<id>/` while the CLI the daemon
  spawns to launch the agent resolved its OWN data dir from scratch —
  `DAEDALUS_DATA_DIR`, then `config.json`, then `<its own scriptDir>/.cache`
  (`internal/config/config.go:196-207`) — and, unlike `daedalus-control`, has no
  `--data-dir` flag to be told otherwise. The daemon is *always* spawned with an
  explicit `--data-dir` (`internal/control/bootstrap.go:114`), so the two agreed
  only by coincidence. Where they diverged, the credentials landed in a home
  nothing mounted: the container got a different `/home/claude`, the Job died in
  about two seconds on `Not logged in`, and the seeding step above it in
  `control.log` reported success — the failure and its cause in different
  directories, which is the hardest shape of this bug to read.
  - Both spawns are now pinned via `DAEDALUS_DATA_DIR`: the launch, so the seeded
    home is the mounted one, and the deregistration, so cleanup edits the registry
    the launch actually wrote to rather than a different one (where it would find
    no such project and leak the entry it exists to remove).
  - `DAEDALUS_DATA_DIR` is the correct lever because it is the CLI's
    highest-precedence source — read before `config.json` loads, and
    `ApplyAppConfig` only fills a still-empty value (`core/appconfig.go:24`) — so a
    project-local `config.json` cannot quietly win against the plane's own choice.
  - An adapter with no `DataDir` pins nothing rather than exporting an empty value,
    which the CLI would take as an explicit `""` and, being highest-precedence,
    would beat `config.json` — turning a missing setting into a wrong one.
  - `executor.Call` now records the extra environment a `RunWithEnv` carried; the
    `MockExecutor` discarded it, so no test could tell a pinned spawn from an
    unpinned one.
- **`--help` reaches the subcommand's own help, and stops warning about a log
  file.** `ParseArgs` returned from inside its flag loop the moment it saw
  `--help`, before resolving any paths and before any subcommand routing. Two
  consequences, one cosmetic and one not. The cosmetic one: `LogFile` was never
  defaulted, so every `--help` called `logging.Init("")` and printed `opening log
  file "": open : no such file or directory` — a confusing shape, since
  `filepath.Dir("")` is `"."` and so only the `OpenFile` failed. The real one:
  `--help` is matched anywhere in argv, so no subcommand could ever see it, and
  the dedicated usage in `task.go`, `version.go` and `docs.go` was reachable only
  by the bare word `help`. `daedalus task --help` printed the global usage, and
  the `"--help"` branches in those files were dead code from the CLI — the
  documented help was the help nobody found.
  - `--help` is now recorded rather than returned on, and routed to the
    subcommand's own help via an **allow-list of the commands that implement
    one**. Injecting `help` everywhere would be worse than the bug: a subcommand
    without a help action reads it as an ordinary argument, and `daedalus remove
    --help` must never be understood as "remove the project named `help`". That
    case is pinned by a test.
  - **Help still wins over flag validation** — someone typing `--runner bogus
    --help` is asking what the valid runners are, and answering with the error
    they are looking up has the order backwards. It also survives an unresolvable
    executable path, since that user especially needs the usage.
  - **A usage print stays a pure print**: no data dir, no log file, and no Guild
    Master bootstrap. `logging.Init` now treats an empty path as "no log
    configured" rather than an error, which also matters inside the image, where
    the exe dir is not writable by the container user — defaulting a log path for
    `--help` would have traded this warning for a different one.
  - `daedalus version help` no longer needs a resolvable install layout: it
    answered before, only after `ResolvePrefix` had already failed for the person
    asking. And `--no-color` is applied before any warning can print, instead of
    eight lines after.
- **The clean verifier overrides the image entrypoint, so a check command is
  actually executed.** The verifier ran `docker run <image> sh -c '<check>'` with
  no `--entrypoint`, but the project image's `ENTRYPOINT` is `entrypoint.sh`,
  which has no `exec "$@"` passthrough — it seeds runner config, patches the
  trust keys, injects MCP servers, and then execs the *agent* with whatever
  arguments it was handed. So every check arrived as argv to `claude`, in a
  container with no network and no credentials, and no check command has ever
  run. The verdict was a statement about a container that never did the work.
  Now `--entrypoint sh` with the command as `-c <check>`; the clean room wanted
  none of that startup anyway, since verification is a decision about a checkout
  rather than a session. Two regression tests, both verified by reverting the
  fix: one pins the argv shape (override present, and exactly `-c <check>` after
  the image, so the pre-fix stray `sh` positional cannot come back), the other
  *derives* the requirement by reading `entrypoint.sh` — it demands the override
  only while the script still execs the agent with `"$@"`, and stands down if a
  real `exec "$@"` passthrough is ever added.
- **The `daedalus` CLI ships in the image, so the built-in acceptance policy can
  actually run.** A project that declares no `.daedalus/verify.json` is graded by
  the default policy, whose only check is `daedalus docs lint --ci` — run inside
  the clean verifier, which mounts nothing but the checkout, so every command has
  to come from the image. The image received `daedalus-runner` and the three MCP
  servers but never the CLI itself, so that check could not have run even once
  the container reached a shell. (Correction: this entry first said the check
  "could only ever exit 127". That was inference, not observation — the
  entrypoint defect fixed above means the check never reached a shell at all, so
  a `command not found` from inside `sh -c` is not where that came from. Both
  defects had to be fixed before the built-in policy could run.) Added to all
  six buildable stages, in the same thin final layer as the other binaries so the
  toolchain layers stay cached. `TestDefaultPolicyCommandShipsInTheImage` fails if
  a stage that takes `daedalus-runner` does not also take the CLI — verified by
  deleting one COPY, where it reports `6 stage(s) COPY daedalus-runner but 5 COPY
  the daedalus CLI`.
  - Note for existing tasks: a Task pins its image digest **once**, so a task
    created before this change keeps grading against the old image. Rebuild
    (`daedalus --build`), then create a new task.
- **A Job's container inherits its project's Claude credentials — before this, no
  Job could ever run.** Every Job launches as a throwaway project
  (`daedalus-job-<id>`), and the container's home is `<DataDir>/<project>/`, so
  each Job got a brand-new home seeded from the image defaults with **no
  login**. Headless `claude -p` therefore exited 1 within two seconds on
  `Not logged in · Please run /login`, every time, and the plane recorded it
  faithfully as `execution_result=failed`. Found on a real host on 2026-08-16,
  in `control.log`, after three consecutive Tasks failed identically.
  - **`SeedJobHome` copies an allow-list** — `.claude.json`, `settings.json` and
    `.credentials.json` under the container's `CLAUDE_CONFIG_DIR`, plus a
    `~/.claude/.credentials.json` fallback — from the owning project's home into
    the Job's, at 0600. A copy rather than a shared mount, so concurrent Jobs on
    one project cannot race each other's config writes and the Job's home dies
    with the Job. Nothing outside the allow-list travels: a fresh attempt does
    not inherit session transcripts or caches.
  - Seeding failure never blocks a dispatch (a project may authenticate by API
    key), but it logs the symptom and the human fix — "log in once with
    `daedalus <project>`" — because the alternative is an operator staring at
    `exit status 1`.
  - `TestJobHomeSeedPathsMatchTheDockerfile` pins the host-side path to the
    Dockerfile's `ENV CLAUDE_CONFIG_DIR`; if they drift, seeding would write
    where the agent never looks and this failure would return silently.
  - **Worth stating plainly: `CoordinatorRunner` had never successfully executed
    a Job on a real host.** It is the seam M13 marked HOST-ONLY, and every green
    run — the suite, the M13/M14 verify scripts, CI — uses `StubRunner` or
    `DAEDALUS_CONTROL_FAKE_RUNNER`, none of which needs credentials. The control
    plane around it was sound; the substrate had never been exercised.
- **`--force` is honoured by `remove` and `prune` (it was ignored precisely where
  it was needed).** `--force` was consulted only in the *headless* branch, and
  headlessness is guessed from stdin being a non-character device. The control
  plane's own cleanup runs `daedalus remove daedalus-job-<id> --force` from the
  daemon, whose children get stdin on `/dev/null` — **which is a character
  device** — so the CLI decided a human was present, printed a prompt into a log
  file, read EOF and aborted. Every Job stranded a `daedalus-job-*` registry
  entry, defeating the deferred remove written to prevent exactly that. `--force`
  now wins outright and never reads stdin; a single `confirmDestructive` helper
  answers for both commands. The regression test reproduces the daemon's exact
  conditions (stdin on `/dev/null`, no `--prompt`) and was **verified by
  reverting the fix**, where it reproduces the production line
  `Remove project 'daedalus-job-J-7'? [Y/n]: … aborted`.
- **The Guild Master can finally act — the restricted control-plane socket is
  mounted into its container (#72).** `cmd/guild-control-mcp` has shipped since
  M15 and `entrypoint.sh` gated it correctly on the socket being present, but
  nothing on the host ever mounted it, so the gate was always false, the tool was
  never wired, and "the Guild Master joins as a gated client" had never once been
  reachable. `core.GuildControlSocketMount` now supplies it, and every rule in it
  is a refusal:
  - not the Guild Master → no mount; no ordinary project's agent reaches the
    control plane at all;
  - not an existing **socket** → no mount, so a stopped plane cannot have Docker
    create a directory at the target;
  - basename not exactly `control-agent.sock` → no mount. This is the guard that
    matters: caller class comes from *which file* is mounted, so mounting the
    human `control.sock` there would silently promote the agent to full
    authority. A caller that computes the wrong path gets **no tool** rather than
    an unlimited one.
  `DAEDALUS_CONTROL_AGENT_SOCKET` is set only alongside a real mount, so the host
  mount and the in-container gate cannot disagree. `daedalus guild-master` starts
  `daedalus-control` first — a bind-mount source must exist at `docker run` — and
  a plane that will not start is a warning plus the read-only overseer, never a
  failed launch.

### Added
- **`scripts/verify-guild-control.sh` + `docs/guild-control-verification.md`.**
  The `static` phase proves the host half with no Docker (15 assertions) by
  driving the **real** sockets rather than a fake: an agent caller creates a task
  (allowed — creation cannot exceed policy), has its cancel turned into a 422
  proposal with the task still alive afterwards, and is refused when it tries to
  confirm that proposal itself; the same confirm on the human socket executes.
  The `real` phase inspects a running Guild Master container for the mount, the
  socket, the connectability (a uid mismatch is the likely failure and the
  runbook says so), and the wired tool — plus the negative check that an ordinary
  project's container gets none of it.

## [0.53.0] - 2026-08-15

Milestone 18 (Control-Plane Hardening): the post-arc external review of the
M13–M17 control plane, acted on. Three correctness defects fixed, one claim the
code contradicted two lines below itself corrected, and the review's two
milestone-sized findings written into the backlog rather than quietly absorbed.

### Fixed
- **A steering handoff is bounded even when the adapter ignores its context.** The
  delivery call was handed a deadline context but invoked synchronously, so an
  adapter that never checks cancellation would block its caller forever and
  `steerTimeout` bounded nothing it claimed to bound. It is now *raced* against the
  deadline on a buffered channel — the shape `runUnderWallClock` already uses for
  the wall-clock budget, for the same reason: a context is a request, and an
  adapter is runner-specific code the plane cannot vouch for. The honest limit is
  unchanged and now stated: this bounds how long the **plane waits**, not how long
  the adapter runs. Dormant in the shipped runner, which still has no steering
  boundary — fixed before the first real adapter could inherit it.
- **Replacing a steering instruction is atomic.** Superseding the pending
  instruction and inserting its replacement were two transactions; a failure
  between them left the Job with **zero** pending instructions, silently discarding
  a valid one the operator believed was still standing. The comment promised there
  would never be *two* — it bought that by permitting the opposite. `Store`
  gains `ReplacePendingSteering`, which does both in one transaction and replaces
  `SupersedePendingSteering`/`CreateSteering` (each had exactly one caller).
- **A dependency declared after a Task left `planned` is no longer inert.** The
  graph's enforcement lived only in the `planned ⇄ blocked` refresh, so an edge
  added to a `working`, `candidate`, `verified` or `approved` Task was recorded,
  rendered in `task depends` and on the programme board, and gated nothing — the
  Task verified and landed with its dependency still sitting unstarted. The
  integration transaction now refuses to land a Task whose dependencies have not
  landed, with the typed `dependencies_unmet` rejection naming what it is waiting
  on, and distinguishing *unmet* from *can never be satisfied* because those need
  different actions from an operator.
  - **Landing is gated, not grading.** `base_sha` is frozen at task creation and
    only `retry --rebase` moves it, so a dependent that merely *starts* after its
    dependency landed still runs against a tree that predates it — admission
    ordering alone never puts B's work under A's. The integration
    rebase-and-re-verify is where the two are genuinely combined. Verification is
    deliberately untouched: that verdict is about the artifact against its own
    frozen oracle, and blocking it would spend a review cycle to learn nothing.
  - **A terminal Task cannot acquire a dependency**, refused in the same store
    transaction as the insert: once a Task is `integrated`/`failed`/`cancelled`/
    `expired` there is no dispatch left to block and no landing left to gate, so
    the edge could only ever be decoration.
  - An in-flight Task with an unmet dependency is **not** moved to `blocked` —
    that pair of transitions connects `planned` and `blocked` only, and a Task
    whose worker is mid-flight claiming to be waiting would be a worse
    misstatement than the one being fixed. It carries the edge and meets it at the
    landing gate.
  - Honest cost, now documented: a dependent can be verified *and approved* and
    still unable to land, and if its upstream fails the only route out remains
    cancel-and-recreate — discarding work already graded and signed off. That
    raises `RemoveDependency` (backlog #68) from a convenience to something closer
    to a missing escape hatch.
- **The milestone status marker is replaced rather than appended (#65).** The doc
  writer's heading regex listed `Done|In Progress|Paused` while the parser's listed
  those plus `Planned`, so a heading already carrying `(Planned)` had nothing
  stripped and the new status was appended — `### Milestone 15: … (V2) (Planned)
  (In Progress)`. This was silent corruption, not a failure: the parser reads the
  *last* marker, so a doubled heading parses to a perfectly valid milestone while
  its title quietly accumulates markers. The two lists are now the same list, and
  the stripping is iterative, so a heading already corrupted is repaired by the next
  status write instead of growing a third marker.
- **`ValidateWrite` refuses a heading carrying two status markers (#65).** The
  backstop for the above, and the only layer positioned to catch it — no other check
  can see the corruption, because the document parses cleanly. A title whose trailing
  parenthetical is *not* a status ("Rework (Phase 2)") is untouched.
- **`add_sprint` no longer leaves the between-sprints placeholder behind (#66).**
  Opening a sprint inserted it *above* the `_No active sprint…_` note, so
  `## Current Sprint` held both a real sprint and a paragraph saying there was not
  one — hand-deleted when opening Sprint 58, and again at Sprint 64. The section's
  body is now replaced when it holds no sprint. The rule is deliberately narrow:
  prose is only dropped when there is no sprint heading in the section, so a note
  written to sit *alongside* a live sprint survives.
- **`add_sprint`'s description no longer claims items start `Pending` (#66).** It
  writes an empty status cell, which is what Pending means in this format — the
  literal word is a status `docs lint` rejects. The docstring was the wrong half.

### Added
- **`docs/using-daedalus-control.md` — a usage guide for the Guild Master and the
  control plane.** The two subsystems had extensive design documentation and no
  page that said what to type: an end-to-end walkthrough (create → dispatch →
  verify → approve → integrate), how to write a `.daedalus/verify.json`
  acceptance policy and how to change tests without tripping the integrity gate,
  budgets and the host-side policy file, the typed refusals and which of
  retry/`--rebase`/replan answers each, cross-project dependencies, steering,
  approving from the TUI (`A`) and Web, and where state lives. README gains a
  Control Plane section and `task`/`guild-master` entries in the command table;
  `ARCHITECTURE.md` gains the control-plane component, its two sockets and its
  data files, none of which it had described.
  - It also documents two capabilities as **dormant rather than available**,
    which is the point of writing it: steering delivers nothing on the shipped
    runner, and the Guild Master's control client is never wired because nothing
    mounts `control-agent.sock` into its container (new **BACKLOG #72** — the
    env gate and the MCP server both exist and are correct; the host-side mount
    does not). Found while writing the guide, by trying to describe the workflow
    end to end.
- **`add_sprint` writes a `Goal:` line** when given one. Every sprint in `SPRINTS.md`
  carries a goal above its item table, but the writer emitted none, so each opening
  needed a hand-edit — the same class of gap as #66 and found the same way, by
  opening a sprint with the tool and reading what it produced.

### Changed
- **`Service.TargetFor` is split into a query and a command (CQS).** It was named
  and typed as a query — it returned a `Target` — but on first call it also wrote a
  database row and a git ref. `CONTRIBUTING.md` § Command-Query Separation forbids
  exactly that, and this was the worst place in the package to break it: the read
  decides which commit the acceptance oracle is frozen at, so every caller that
  merely wanted to *know* the target was one missing row away from *creating* one
  out of the worker-writable checkout `HEAD`.
  - `Service.Target(project)` is now a **pure query** — no adoption, no projection
    ref, no writes — returning an `ErrNotFound`-wrapped error when a repository has
    no target.
  - `Service.ensureTarget(project)` is the **adoption command**: idempotent,
    unexported so no route or CLI verb can be wired to it, and called from
    `CreateTask` and nowhere else — the one moment a project legitimately has no
    target yet.
  - The other three call sites now **fail rather than adopt**. For `retry --rebase`
    that is a strict improvement: `--rebase` re-freezes the acceptance policy at the
    tip, so silently adopting the checkout `HEAD` there would have re-opened oracle
    laundering on the very path Sprint 59 closed it on. For `integrate` and the
    staleness check, adopting mid-transaction would have handed the trunk to
    whoever can write the repository's refs.
  - Behaviour is otherwise unchanged: trust-on-first-use still happens, and the
    `errors.Is(err, ErrNotFound)` discipline (only a genuine "no target yet" may
    adopt; any other read failure surfaces) moved intact to the command. The
    `TestAttack_*` suite passes with **no assertion changed**.
- **The wall-clock budget is described as what it is: not a kill.** The comment on
  `runUnderWallClock` called it the first "strongly enforceable" axis and said the
  Job "is terminated" — two lines above the honest paragraph explaining that the
  plane cannot guarantee the death of a process it did not fork and that the
  container may outlive the verdict. Both could not be true; the honest one was.
  The comment and `docs/control-plane.md`'s budget table now say the Job and Task
  **rows** go terminal on time whether or not the runner cooperates, and that what
  is enforced is the plane's own bookkeeping plus a cancellation *request*. No
  behaviour changed — the budget did exactly this before, and only the description
  of it was wrong, which is the point: the same defect class the M15 audit found
  five times and the M17 close corrected once. Real termination is now backlog #69
  rather than an implied property.
- **The two findings this release deliberately does not fix are written down.**
  BACKLOG **#69** (real execution termination: a persisted execution handle,
  idempotent `Stop`/`Kill`, capacity released only on confirmed death) and **#70**
  (a durable scheduler: persisted queue entries plus a dispatch loop, replacing
  retry-driven admission — today the `waiting` map is in-memory and a restart
  erases queue order). Both are milestone-sized execution-substrate work, and the
  external review's verdict is that they are the real remaining gap: the control
  plane is more trustworthy than the machinery it controls. **#71** records the
  hostile black-box scenarios the review asked for, minus the three now covered by
  tests, so the list is a to-do rather than a critique.

## [0.52.0] - 2026-08-09

### Added
- **Typed steering, the programme board, and the M13–M17 arc close (Sprint 63).**
  - **`steer_job` is a typed, audited control-plane operation.** An instruction
    aimed at a running Job, recorded as plane-owned state with its issuer, its
    timestamp and an explicit **delivery state** — a worker can neither forge one
    nor replay one, which is the whole difference between this and typing into a
    terminal. `daedalus task steer <job-id> --instruction <text>`, `POST
    /jobs/{id}/steer`, and a `request_steering` MCP tool.
  - **Steering changes what the worker is told, never what counts as done.** A
    steered Job still reaches `candidate` and is still verified against the
    acceptance policy frozen at the plane-owned target. M17 adds **no state and no
    transition** — nothing to `legalTransitions`, nothing to `workerReachable` —
    and touches neither the acceptance hash, `base_sha`, the budget, nor the
    objective. Two tests hold that shut, because if steering could influence
    acceptance it would re-open M14's and M15's argument through a new door.
  - **Honest failure is the point.** Delivery state is `pending` (a runner took
    custody; the boundary has not arrived), `delivered` (it reached the Job),
    `undeliverable` (it did not and will not), `superseded` (a newer instruction
    replaced it) or `cancelled` (a human withdrew it). A steering op that reported
    success without delivering would be **worse than one that refuses** — an
    operator would go on believing they had redirected a Job that never heard them
    — so `undeliverable` is a first-class outcome, printed in red, and carried on
    the board.
  - **Delivery sits behind an optional `SteeringDeliverer` seam**, so the authority
    path stays runner-agnostic (§9) and the logic is host-testable with no Docker.
    A runner that does not implement it is not broken; it is a runner with no
    steering boundary.
  - **Agent callers are proposal-tier.** An instruction injected into work that is
    already running is at least as consequential as cancelling it, and rather more
    subtle — the Job carries on and the change of direction shows up only in the
    log. Withdrawing an instruction is tiered with issuing it, since an agent that
    could cancel a human's pending steer would have the same control by
    subtraction.
  - **The programme board.** `daedalus task board`, `GET /board`, a Web panel, a
    TUI header and a `programme_board` MCP tool: one cross-project view of what is
    running, queued, blocked (**and on what**), in verification, awaiting approval,
    landed, and closed without landing. **Derived, not stored** — a projection of
    the same rows every other surface reads, so it cannot disagree with them. It
    reuses the Sprint-59 approvals queue, the Sprint-62 dependency status and the
    Sprint-61 plane status, and the agent-facing projection carries Sprint-60
    **opaque queue ids** and no host paths. Every state maps to exactly one column,
    asserted by a test, so work can never silently vanish from the board.
  - **The arc's closing summary** in `docs/control-plane.md`: what M13–M17
    guarantee, what they explicitly do not, and the standing limits collected in
    one place — heuristic liveness, wall-clock as bookkeeping rather than a process
    kill, tests as an incomplete oracle, and callers as a class rather than an
    identity. Three properties the arc earned but had never claimed are now stated:
    **no refusal is silent** (every "no" carries a typed reason *and* an event row;
    five audits found no silent drop), **recovery is unattended** (every wedge found
    across the arc is repaired by reconcile without a human — Sprint 58 alone
    shipped three permanent ones, none of which survives), and the acceptance oracle
    survives commit rewriting for a **stronger reason than ancestry checking**: the
    plane's integration target is not a git ref at all, so there is no ref an agent
    could rewrite that the oracle is ever read from.
  - **One guarantee gained its missing qualifier.** "Capacity cannot be starved or
    leaked" is true *where per-Job liveness is available*; without a
    `JobSessionObserver` the heuristic may answer "I don't know" and leave a Job
    alone, which is a held slot. The limits section already said so twenty lines
    below — the two could not both be unconditionally true.

### Changed
- **The plan's demotion of M17 is recorded as correct, not justified away.** The
  shipped `CoordinatorRunner` has no steering boundary — `SteeringDeliverer` has no
  implementation anywhere, and `claude --print -p` is a one-shot invocation that
  takes its prompt from the flag and exits — so every instruction against it is
  recorded `undeliverable`. **Cancel + redispatch remains the working remedy for
  short Jobs.** What M17 genuinely bought is an audited record of each instruction
  and its fate, a refusal an operator can read instead of a silent no-op, a seam
  ready for a runner that does have a boundary, and — largest of the four — **the
  verdict itself**: a judgement made in advance and then tested, which is worth more
  than the feature would have been. Written down in `ROADMAP.md`,
  `docs/guild-master-plan.md` and `docs/control-plane.md` rather than smoothed over.

### Fixed
- **`scripts/verify-m13.sh` had been reporting a false failure since v0.51.0.** Its
  guardrail section still asserted the one-active-Task-per-project invariant that
  **Sprint 61 deliberately removed**, so the host-side self-check reported 16
  passed / 1 failed on a healthy tree — and this is the script the arc summary names
  as the way the host-only container paths get exercised. A self-check that cries
  wolf gets ignored. The assertion now checks the thing that actually changed (the
  plane no longer refuses the second Task; genuine concurrency is proven under
  `-race` in `parallel_test.go`, a far better oracle than a shell script), and a
  programme-board smoke was added. Now 18 passed / 0 failed.
- **A wrong justification in the M17 assessment, corrected in place rather than
  deleted.** The first draft argued the shipped runner has no steering boundary
  because "the container is launched with stdin closed", citing `cmd.Stdin = nil` in
  `internal/coordinator/bootstrap.go`. That line launches the **coordinator daemon**,
  not the agent container: `RealExecutor.Run` sets `cmd.Stdin = os.Stdin` and
  `docker compose run --rm` attaches stdin without `-T`, so a pipe does exist
  structurally. The conclusion survives on better evidence — `claude --print -p` is
  one-shot and does not read stdin as a channel, and decisively `SteeringDeliverer`
  has no implementation at all. The error was verifying a *fact* and not the
  *inference* drawn from it, and the correction is kept in the document as the
  worked example.
- `docs/control-plane.md` claimed the prompt-injection surface did not exist
  because there was no agent client. That stopped being true in Sprint 60, when
  `guild-control-mcp` shipped. The surface exists and is answered by tiered
  authority and the proposal flow; the sentence is corrected rather than left
  standing as a guarantee the plane no longer makes.

## [0.51.0] - 2026-08-09

### Added
- **Per-Job liveness, the cross-project task graph, and the M16 close (Sprint 62).**
  - **Reconciliation repaired — liveness was asking the wrong question.**
    `HasSession` takes a *project*, but a control-plane Job runs under
    `daedalus-job-<jobID>`, which is what the coordinator keys its session by. The
    plane asked about `app` while the Job's session was `daedalus-job-J-7`, so the
    answer was only accidentally related to the Job being judged — false while a
    Job ran happily, true for every Job of a project somebody had a session open
    on. Survivable at one Job per project; a **capacity denial-of-service** once
    several share one, since a ghost Job stays `working`, is counted by the
    scheduler, denied against, and holds its worktree until the project can never
    dispatch again. `JobSessionObserver.HasSessionForJob` fixes it and **needed no
    coordinator change** — the per-Job key has existed since M13.
  - **A labelled heuristic** for deployments without per-Job liveness: a `working`
    Job whose worktree is gone, or which is far past its own wall-clock budget, is
    reaped. It is documented in code, docs and its own event as a **guess** — it
    cannot distinguish a crashed Job from a slow one whose budget was set too low,
    and the margin changes how often it is wrong rather than whether it can be. A
    per-Job observer always wins where available.
  - **Reconcile now sweeps Tasks as well as Jobs.** A Task wedged `working` with
    **no Job at all** (a crash between the transition and the Job insert) was
    invisible to a Job-only census — not dispatchable, retryable or replannable,
    with only cancel to escape. It is returned to `rejected`, the state the
    retry/replan ladder already understands.
  - **The cross-project task graph.** Task→Task dependencies spanning projects,
    with a new plane-only `blocked` state (`planned ⇄ blocked`, absent from
    `workerReachable`). A dependency is satisfied only when the upstream Task
    reaches `integrated` — verified is not enough, since the work exists but has
    not landed. **Cycles are refused at creation**, naming the path, rather than
    discovered at dispatch where they would be a wedged graph. The edge is
    plane-owned state in `control.db`, never read from a project checkout, and
    declaring one is a **proposal** for an agent caller: "what must happen before
    this is graded" is as load-bearing as "what grades it".
  - **Unsatisfiable dependencies are handled by kind.** A `failed` upstream leaves
    dependents `blocked` and marked unsatisfiable — failure is an outcome, and a
    human may retry the work as a new Task and keep them. A `cancelled` upstream
    **cancels its dependents transitively** — cancellation is a decision that the
    work will not happen, so leaving them waiting forever is the stranding.
  - **The wake path has the same liveness discipline as the queue lease.** A
    landing wakes its dependents directly, and every reconcile pass re-evaluates
    every blocked Task, because a wake that only happens on an event is missed when
    the process dies mid-event. `daedalus task depends <id> [--on <other>]`, with
    the graph shown in `task status`.

### Fixed
- **A documented justification that was not true.** The docs explained leaving
  dependents of a *failed* Task blocked-and-unsatisfiable on the grounds that "a
  human may retry the work and keep the dependents". They cannot: `failed` is
  terminal with no outgoing edge, and there is no operation to remove a dependency
  edge, so a dependent blocked on failed work cannot be rescued in place. Nothing
  is silently stranded — the state, the status and the CLI all say so — but the
  reasoning was wrong, and the CLI was offering advice that could not work. The
  docs, the code comment and the CLI hint now say plainly that a failed dependency
  is **permanent** and its dependents must be cancelled and recreated. (Removing a
  dependency edge is tracked as backlog, deliberately not part of this milestone.)
- **An overclaim in the M16 description.** "Adds *only* concurrency and
  scheduling" is true of the execution path — no runner, worktree, verifier or
  container code changed — but a reader would take it to mean the state machine was
  untouched, and it was not: M16 added the `blocked` state, three plane-only edges
  and a plane-owned dependency table. Now "adds concurrency, scheduling and a
  dependency graph — no new execution machinery", with the narrower claim spelled
  out where it could otherwise be over-read.
- **The per-Job observer type assertion checked the wrong thing in the wrong
  order.** `s.sessions.(JobSessionObserver); ok && s.sessions != nil` puts a
  redundant nil check after the assertion (a nil interface already fails it) while
  missing the hazard that is real: a **non-nil interface holding a nil pointer**
  whose method set satisfies the interface, which asserts successfully and then
  panics on the first dereference. Extracted into a guard that sees through the
  interface, with a test that panics on the naive form.
- **`core/milestone_test.go` restated the document instead of testing it.** It
  pinned each milestone's status by hand, carried no information ROADMAP.md did not
  already carry, and broke on **two consecutive milestone openings** in one working
  session. It now asserts structural invariants that hold for any valid roadmap —
  the parser sees every milestone the document declares (derived from the document,
  so adding M18 needs no edit), numbering is contiguous from 1, nothing is
  nameless, and at most one milestone is In Progress.
- **The milestone parser did not recognise an explicit `(Planned)` marker**, found
  by the new structural assertion: the tooling writes it, the regex did not match
  it, so it fell through into the *title* and M10 and M17 rendered as "Homebrew
  Distribution (Planned)" everywhere a title is shown. The parser now recognises
  and strips it, and a new check catches a heading carrying **two** status markers
  — the silent document corruption recorded in BACKLOG #65.

### Added
- **Concurrent Jobs and the scheduler (Sprint 61, opening M16).** The **one active
  Job per project** invariant that had held since M13 is lifted, and the M15 merge
  queue becomes load-bearing instead of insurance.
  - **What actually changed.** A *Task* still has at most one Job in flight (the
    state machine guarantees it), so every per-Task singular lookup stays correct
    unchanged; what was forbidden and is now allowed is several *Tasks* active on
    one project. The service lock was already released across long calls since
    Sprint 58, so dispatches genuinely overlap the moment two Tasks exist — no
    execution machinery was added. The audit's load-bearing finding: `withClaim` is
    keyed by **Task** while concurrency is per **project**; those were the same
    sentence under the old invariant and are not any more, so the per-project limit
    had to come from the scheduler rather than the claim set, which would otherwise
    have been a limiter that silently never fired.
  - **The scheduler** admits Jobs under a **global** limit, a **per-project** limit,
    and the per-Task budget `concurrency` axis — the tightest binds. This is what
    finally makes that budget axis fire at all (Sprint 58 audit finding 11), and its
    default moved from `1` to *unset*, because a Task-level default of 1 would
    silently override the operator's per-project limit and make parallelism
    impossible to switch on. Limits come from the host-side governance file, and the
    defaults (`perProject: 1`) **preserve the previous behaviour**: lifting the
    invariant changes what the plane can do, not what an existing installation does.
  - **Fairness.** When capacity frees, only the **oldest waiter** may take it — a
    newer Task is refused with `queued_behind` and told which Task it yields to.
    Without it, a project dispatching in a loop would starve the Task that asked
    first, forever, since a refusal is a retryable typed rejection. Fairness is per
    project (freeing A's slot does nothing for B), except for the shared global
    limit. Every admission decision, allowed or refused, is a typed `schedule`
    event: a scheduler that quietly declines is indistinguishable from a broken one.
  - **Concurrency correctness**, proven with real goroutines under `-race` rather
    than simulated interleaving: three Jobs live on one project each with their own
    worktree and artifact; Jobs on separate projects not contending; reconcile
    adopting/settling/reclaiming nothing while N Jobs run; cancellation hitting
    exactly one Job and leaving its siblings running; concurrent worktree
    create/remove leaving git's own view clean; and **competing landings on one
    queue** — two Tasks verified against the same target racing to integrate, where
    the compare-and-swap decides and the loser rebases onto the winner so neither
    change is lost.
  - **Observability.** `task list` shows the plane-wide and per-project running
    counts and names what is queued; `task status` shows a Task's scheduling
    standing, making a Task **queued for capacity** visibly distinct from one that
    is working. New `GET /status` and `GET /api/plane-status`, with the same summary
    in the TUI.
  - **Bounded containers, not disk:** the scheduler limits running Jobs, but
    `candidate`/`verifying`/`rejected` Jobs each keep a worktree, so N Tasks per
    project means N simultaneous candidate worktrees where there was at most one.
    Bounded by unfinished work rather than leaking, but it now scales with Tasks in
    flight.
  - **Honest limitation:** session liveness is observed per *project*, not per
    *Job*, so reconcile cannot tell which of a project's Jobs a live session belongs
    to. It errs conservatively — a crashed Job among healthy siblings is adopted
    rather than failed — which leaks a stale Job rather than destroying a live one.
    Per-Job liveness is the fix.

### Fixed
- **An abandoned queue ticket blocked every younger Task indefinitely** (found by
  the Sprint 61 audit). Fairness was implemented without liveness: a Task refused
  for capacity kept its place while sitting in `planned`, and nothing woke it —
  dispatch is synchronous, so the queue only advanced if a human re-issued
  dispatch for that exact Task. One abandoned attempt bricked a project's
  parallelism, and an abandoned **global** waiter stalled every project at once,
  reachable by ordinary use: dispatch, get refused, walk away. A ticket is now a
  **lease**: it is renewed when its owner re-asks, spends a *passover* each time it
  blocks someone else, and lapses after a TTL. The busy case heals in a few
  attempts by the Tasks being blocked; the quiet case heals on the clock. Re-asking
  renews without losing place, so a live waiter cannot be aged out by its
  competitors.
- **The `concurrency` budget axis silently accepted an over-ask.** With its
  default now unset (= unbounded), the generic ceiling check could never refuse
  one, so a request for `concurrency: 1000` was stored and echoed back by `task
  status` as though it were the limit, while the real bound was the operator's
  per-project setting. It is now refused against `concurrency.perProject`, out
  loud, like every other axis.
- **The stub runner's marker file is job-scoped.** A fixed filename made every
  concurrent Job write the same path, so any two artifacts landing on one queue
  collided — the integration *conflict* path was being reached by accident and the
  *clean-rebase* path was not being reached at all. Both are now exercised
  deliberately, with a test each.
- **`core` milestone test broke when M16 was opened**, the same way it did for M15:
  it hard-asserted M16–M17 were both `Planned`. It now asserts M16 `In Progress`
  and M17 `Planned`.

## [0.50.0] - 2026-08-09

### Added
- **The Guild Master joins as a gated client — `guild-control-mcp`, transport-derived
  caller identity and tiered authority (Sprint 60, closing M15).**
  - **Caller identity from the transport.** The daemon now listens on two sockets:
    `control.sock` (human) and `control-agent.sock` (agent). Which socket a
    request arrived on *is* the caller class — not a request field (a client that
    can name its own actor can name "human") and not peer credentials (the socket
    is `srwxr-xr-x` and the agent runs as the same uid, so `SO_PEERCRED` separates
    users, not caller classes). The agent's container is given exactly one socket,
    so the class is fixed by the mount namespace before a byte is parsed. Every
    event now carries a caller class the caller could not forge.
  - **Tiered authority.** Reads and bounded task creation execute; verification and
    review execute because they apply the *plane's own* oracle; dispatch, retry,
    replan, cancel, integrate, approve/reject and target-resync are **recorded as
    proposals** for a human to confirm. Confirming runs the operation as the
    confirming human; denying does nothing. An agent **cannot confirm its own
    proposal** — confirmation is refused for agent callers and is not itself
    proposable — which makes "the Guild Master cannot approve its own work" true by
    construction. An unknown operation fails closed to "ask a human", and a table
    test fails if a mutating operation has no explicit tier.
  - **`cmd/guild-control-mcp`.** Intent-level tools only (`list_tasks`, `get_task`,
    `task_events`, `create_task`, `request_verification`, and the proposal ops);
    **never** `run_shell` / `docker_run` / `mount` / `git_exec` /
    `start_container`, and never `coordinator.sock`. Env-gated into the
    guild-master container as `guild-mcp` is, and wired only when the restricted
    socket is actually mounted — no socket, no tool.
  - **`daedalus task proposals list|confirm|deny`**, plus the `proposals` view
    alongside the Sprint-59 approvals surface.
  - **Opaque queue ids.** `GET /targets` listed every queue with its absolute host
    path, and the target operations wrote those paths into the **append-only**
    event log as entity ids — a cross-tenant disclosure that would have become
    historical and unerasable once an agent could read the log. Queues now carry a
    stable `sha256(canonical path)` id, used as the event entity id and shown to
    agents; the path is rendered to human callers only. An agent can still tell
    that two projects share a queue, and learns nothing about host layout.
  - **Carried Sprint-59 audit items.** Queue identity now resolves a **linked
    worktree** to its parent repository (`--git-common-dir`), so a Job's checkout
    cannot get a merge queue of its own; the shared-queue view is derived from the
    **registry** rather than from tasks, so a queue shared with a project that has
    no tasks yet no longer looks unshared; and both mutation survivors
    (`errors.Is(ErrNotFound)` adoption gating, `EvalSymlinks` aliasing) now have
    regression tests.
  - **Distribution chain** for the new binary — `Dockerfile`, `build.sh`,
    `setup.sh`, `scripts/package-release.sh`, both release workflows,
    `entrypoint.sh` injection, and **both test sims**. The bundle sim caught a real
    mistake (the expected-contents list is `LC_ALL=C` sorted, so `guild-control-mcp`
    precedes `guild-mcp`), which is exactly what it is for.
- **The plane-owned integration target, the race-safe integration transaction,
  human approval and the independent reviewer (Sprint 59, M15).**
  - **Plane-owned target ref — closes the acceptance-oracle laundering hole
    structurally.** Each project now has a target commit held in `control.db` that
    **only a completed integration transaction advances** (plus an explicit human
    resync). `CreateTask` and `retry --rebase` freeze the acceptance policy at
    *that* commit rather than the project checkout's mutable `HEAD`, and staleness
    is measured against it. This dissolves the whole class the Sprint-58 audit
    documented but could not patch: cherry-pick/`commit-tree`/`format-patch|am`
    laundering (a weakened policy under a fresh SHA with no ancestry link) and the
    unguarded-`CreateTask` path ("poison the branch and wait for the next Task")
    both change nothing, because none of the repository's refs is what work is
    based on or graded against. Regression tests stage each attack, assert it
    still defeats an ancestry check, then assert it has no effect. The Sprint-58
    ancestry guard survives as **defence in depth**, not as the mechanism.
    `refs/daedalus/target` is written as a non-authoritative projection so the
    landed commit stays visible and reachable; a worker overwriting it is asserted
    to change nothing. Adoption is trust-on-first-use and the resync is manual and
    logged — both documented rather than glossed.
  - **The integration transaction.** `daedalus task integrate <id>` serializes per
    project, rebases the artifact onto the current target, **re-verifies the
    MERGED result** through the existing `VerifyRunner` seam (not the pre-merge
    branch — this is the semantic-conflict fix), then **compare-and-swaps** the
    target, recomputing against the new tip if another integration landed
    meanwhile. Every failure leaves the target untouched and the Task recoverable
    via retry/replan, with typed reasons (`merge_conflict`,
    `merged_verify_failed`, `integration_raced`). Host-tested against real temp
    repos: a genuine N-goroutine compare-and-swap race with exactly one winner, a
    target that moves *during* re-verification forcing a real retry, and a
    semantic conflict that passes pre-merge and fails merged.
  - **Human approval.** `verified → approval_required → approved → integrated`
    with `daedalus task approve|reject <id> [--note]`, opt-in per project via the
    host-side governance file. Every edge on the tail is absent from
    `workerReachable`, so no worker-driven request can approve anything. A project
    that does not require approval still *walks* the states, with an event
    recording that policy — not oversight — is why no human was asked. The §6
    limit is stated rather than implied: the state machine stops a *worker*, not
    an agent client, so "the Guild Master cannot approve its own work" rests on the
    Sprint-60 socket boundary.
  - **Independent reviewer.** An injectable `ReviewRunner` (stub for now, as
    `VerifyRunner` was in Sprint 56) producing the Artifact's `review` status,
    gating integration when configured, bounded by `maxReviewCycles` — the same
    limit as verification cycles, counted separately and not summed. A failed
    review routes to `rejected` with `review_failed`.
  - **Web/TUI approval surface.** `GET /api/approvals` + approve/reject in the
    dashboard, and `[A]` in the TUI. Neither spawns the control daemon, and both
    say the plane is unreachable rather than rendering an empty queue.
  - **Claim leaks made unrepresentable.** The Sprint-58 residual: five correct
    hand-written `defer` sites were still one bare statement from a sixth bug. The
    release now lives in exactly one place — `Service.withClaim` /
    `unlockedDuring` — and a test fails the moment a second appears.
- **Governance core — budgets, typed rejection, retry/replan and the
  control-plane-managed event log (Sprint 58, opening M15).** The control plane
  can now say **no**. Every Task carries a **budget** captured at create and
  stored authoritatively (`budget`, new column + idempotent migration): the plane
  enforces the axes it genuinely can — **wall-clock** (a Job is raced against a
  deadline; an overrun is `execution_result=timeout`), **max-attempts**,
  **max-review-cycles** (counted from the event log), and **concurrency** — while
  **turn/token/cost remain policy in the plane, explicitly NOT enforced**, because
  Daedalus takes process exit as the Job boundary and cannot measure them.
  Defaults are per-project-overridable from a **host-side**
  `<data-dir>/control/budgets.json` — never a project checkout, so an agent cannot
  raise its own ceiling — and `task create` flags may only ever *narrow* it.
  **Typed rejection** gives every "no" a machine-readable `RejectionReason`:
  refusals (`over_budget`, `attempts_exhausted`, `review_cycles_exhausted`,
  `concurrency_exceeded`) change no state, are recorded, and surface as HTTP 422 +
  **CLI exit code 3** so a client can tell *refused by policy* from *failed*;
  verdicts (`stale_base`, `null_agent_floor`, `policy_drift`, `integrity_gate`,
  `verify_failed`) ride on the transition event and `VerifyResult.Reason`. New
  **stale-base** detection rejects a candidate whose `base_sha` is no longer the
  project's target tip — before the integrity gate or the verifier — and names the
  remedy. New `daedalus task retry <id> [--rebase]` creates a **fresh Job** with
  the attempt counter advanced and the budget re-checked, **preserving the whole
  Job chain** (attempt history is never overwritten); `--rebase` re-pins the Task
  to the project tip and re-freezes the acceptance policy there (opt-in, because
  it adopts a newer oracle). New `daedalus task replan <id> --objective <text>`
  returns a rejected Task to `planned` with a revised objective in one atomic
  transition, **without** resetting the attempt counter. New `daedalus task events
  <id>` renders the **control-plane-managed event log** — every transition, budget
  decision, rejection and verification outcome as a typed event (`kind`, `reason`,
  `actor`) across the Task, its Jobs and its Artifacts. The log is **immutable
  *through the API*** — `INSERT` is the only statement the package runs against
  `events`, the `TaskAPI` exposes one read-only event method, and the route is
  `GET`-only (all four asserted by tests, including a source scan) — and is
  deliberately **not** claimed to be cryptographically tamper-proof. No state or
  transition edge was added: retry reuses `rejected → queued`, replan reuses
  `rejected → planned`, so the worker-vs-plane transition-table invariant is
  untouched. Pure Go + git throughout, fully host-tested without Docker
  (`CGO_ENABLED=0`); a v0.49.0 `control.db` migrates additively and keeps working.
  See `docs/control-plane.md`.

### Fixed
- **Authority hardening after the final M15 audit.**
  - **`TierFor` failed OPEN on any unrecognised caller class (blocking).** It was
    written `if class != CallerAgent { return TierAllowed }`, which reads
    identically to the correct rule and is catastrophically different: a
    zero-valued `Caller` — and `Caller` is exported with an exported field — got
    **full human authority**, silently, with no error and no log line. This was
    the exact inverse of `parseCallerClass`'s own stated principle two files away.
    Inverted to grant direct execution only to an explicitly human class, and the
    zero value's three inconsistent answers (`IsAgent()=false`, `Actor()="system"`,
    `String()="human"`) now all derive from one `effectiveClass()` and all say
    *agent*.
  - **`ProposalFailed` was unreachable**, found while fixing the 500 below: the
    fail-marking reused the pending-only compare-and-swap, so a confirmed-then-
    failed proposal stayed `confirmed` forever. New `MarkProposalFailed` moves
    `confirmed → failed` and nothing else, so the human's decision still stands
    and only the outcome is appended.
  - **A confirmed-but-inapplicable proposal returned HTTP 500.** It is a correctly
    handled case — recorded `failed`, nothing mutated — so it now maps to 409.
  - **`TestProposal_ConfirmIsSingleUse` passed for the wrong reason.** It confirmed
    a `retry` on a verified task, so the first confirm failed anyway and the second
    errored for an unrelated reason; both enforcement layers could be deleted with
    the package still green. Rewritten around an operation that *succeeds*, with
    the error identity and the proposal's final state as the load-bearing
    assertions, plus a new concurrent-confirm test that pins the store's
    pending-only CAS specifically — a serial test cannot distinguish it from the
    service guard in front of it. Verified by mutation: removing the service guard
    fails the serial test, removing the CAS fails the concurrent one, removing both
    fails both.
  - **Dead code removed:** `proposalOnly` was never reached (the real refusal is
    two layers up, both tested), so it no longer reads as load-bearing.
  - **Doc overstatements cut:** an agent cannot confirm its own proposal "at two
    independent layers, both tested" rather than "by construction"; the socket
    boundary is described as shipped rather than as something a future sprint
    introduces; and the integration section now says plainly that with one active
    Task per project the merge queue is **insurance for M16's parallel Jobs**, not
    yet load-bearing.
- **Pre-existing: `dev-release.yml` never built `daedalus-control`.** `release.yml`
  has built it since v0.48.0; the dev workflow built 7 binaries, not 8 — so every
  dev release since then shipped a bundle whose `daedalus task` CLI would try to
  spawn a daemon that was not there. The M13 chain fix covered `build.sh`,
  `setup.sh`, `package-release.sh`, `release.yml` and both sims, but not
  `dev-release.yml`, and the sims cannot catch it because they exercise the local
  packaging path. Added to both the build and upload lists; the two workflows now
  diff clean against each other.
- **Integration hardening after an adversarial audit of the Sprint 59 core.**
  - **The approval gate failed OPEN when the governance file was unreadable
    (blocking).** With a corrupt or half-written `budgets.json` and no
    last-known-good — the state at daemon boot — `RequiresApproval` returned
    false, so the plane auto-approved and logged that policy said no human was
    needed. Nothing had said anything. The approval axis now fails closed by
    **requiring** a human, and the auto-approval event says only that the
    configured policy source did not request one. (The budget axis keeps failing
    closed toward the narrower ceiling — the two directions differ deliberately
    and are now documented as such.)
  - **Two projects on one repository got two uncoordinated merge queues.** The
    integration target was keyed by project name, so a clone registered twice — or
    a project registered on a subdirectory of another — produced independent
    target rows, each rebasing onto its own notion of the trunk and swapping a row
    the other never read. Targets are now keyed by **canonical repository path**
    (`git rev-parse --show-toplevel` + symlink resolution), so those projects
    share one queue and serialize against each other; `daedalus task target` shows
    which projects share a queue.
  - **`TargetFor` swallowed every `GetTarget` error, not just not-found**, then
    fell through to trust-on-first-use adoption of the worker-writable checkout
    `HEAD`. Only a genuine `ErrNotFound` may reach the adoption path now — this is
    the single most security-relevant read in the package, and its safety was
    accidental rather than asserted.
  - **The claim-leak guard was defeatable.** `beginOp` was a plain method, so a
    claim could be taken outside `withClaim`'s scope and never released. It now
    requires an unexported witness only `withClaim` constructs, and the source
    scan additionally forbids constructing that witness elsewhere — the type
    system for the accident, the test for the deliberate act. (Go cannot make a
    type unconstructible within its own package; the comment now says so instead
    of overstating.)
  - **`RebaseOnto`'s scratch-worktree cleanup was unpinned** — the sole mutation
    survivor of the audit. Deleting the deferred `worktree remove` left the suite
    green while leaking a worktree per failed integration. Now tested on the
    success path, the conflict path, and over a leftover from a crashed attempt.
  - **A Job could be stranded as a ghost in the census.** A Job whose bookkeeping
    failed after its Task settled stayed non-terminal forever: `ListActiveJobs`
    returned it on every pass and the other reconcile checks skipped it.
    `Reconcile` now settles a Job whose Task is already terminal.
  - **A post-CAS failure could double-land.** The compare-and-swap commits before
    the Task transition, so a failure in that window left the target advanced with
    the Task still `approved`, and re-integrating would replay the same commits.
    Re-integration is now idempotent: it detects that the artifact is already
    contained in the target — by ancestry for a fast-forward landing, by patch id
    for a rebased one — and settles the Task instead of landing twice.
  - **`--no-auth` documentation.** The web UI now carries write authority over
    human approval, and WSL2 auto-detection binds `0.0.0.0`; `--no-auth` therefore
    hands the approve button to anyone who can reach the port. Auth is on by
    default and the handlers are behind the middleware, so this is a documentation
    fix, in `README.md`, `docs/control-plane.md` and `--help`.
- **Governance hardening after an adversarial audit of the Sprint 58 core.** Six
  holes found by a hostile review of the governance implementation, each with a
  permanent regression test:
  - **A negative budget widened every enforceable axis (critical).** `0` means
    "unbounded" and every enforcement site guards `> 0`, so a requested `-1`
    sailed past the ceiling check and disabled that axis entirely — `maxAttempts:
    50` was refused, `maxAttempts: -1` ran unbounded. The CLI rejected negatives,
    but **the CLI is not the security boundary**: the socket API is, and an agent
    client joins it in Sprint 60. Negative axes are now rejected as malformed
    input (`invalid_budget`, HTTP 422) in the **service**, at policy-file load,
    and on the row scan as a backstop against a hand-edited `control.db`.
  - **A Task could wedge permanently in `verifying` (major).** The transition into
    `verifying` was committed before the verifier ran, so any error, panic or
    daemon restart in that window stranded the Task — verify/retry/replan/dispatch
    all refuse a `verifying` Task, only `cancel` escaped, and the review cycle was
    burned permanently because it was counted from the append-only log. Now:
    configuration is checked before anything moves; an aborted verify rolls back;
    `Reconcile` returns a stranded `verifying` to `candidate` (a new **plane-only**
    `verifying → candidate` edge, deliberately not worker-reachable); and review
    cycles are counted as entries into `verifying` **minus** recoveries, so a
    verification that never ran costs nothing.
  - **The service lock was held across `runner.Run` (major).** `task cancel` and
    the reconcile loop were inert for the entire wall-clock budget — up to an hour
    on the default. The lock is now held for DB bookkeeping only and released
    across `runner.Run` and `verifier.Verify`, with an explicit **in-flight set**
    replacing what the over-held lock provided by accident: a second operation on
    a Task is refused immediately (`operation_in_flight`), reconcile skips work
    this process is running, and a cancellation that lands mid-run is preserved
    rather than fought. The concurrency axis now genuinely fires, where before the
    serialisation meant a second dispatch never saw the first one running.
  - **`--rebase` could adopt an agent-authored acceptance oracle (major).** A
    linked worktree shares the parent repository's **refs**, not just its objects,
    so a Job can point the target branch at its own commit — which makes the base
    look stale, and the plane then recommended `--rebase`, the command that
    re-freezes the acceptance policy at the attacker's commit. `retry --rebase` now
    **refuses** (`unsafe_rebase`) when the new tip is reachable from any of the
    Task's own Job commits (an unverifiable check is treated as unsafe), the
    `stale_base` message no longer offers a copy-pasteable rebase, and the false
    "a Job's worktree never moves this tip" claim is replaced with what actually
    holds.
  - **A corrupt `budgets.json` failed open (major).** A parse error fell back to
    the built-in default, which is *wider* than any stricter operator policy — a
    non-atomic editor's partial write was a live widening window. It now fails
    closed onto the last known-good policy.
  - **`ReadHeadSHA` could not resolve a branch in a linked worktree (minor).**
    `refs/heads/*` lives in the git **common** dir, not `.git/worktrees/<id>`, so a
    healthy worktree checkout was misreported as an unborn branch.
- **Leaked locks and leaked claims on the panic paths** (a second audit pass; the
  same class as the fixes above, one layer down). Releasing a resource with a bare
  statement rather than a `defer` means a panic unwinds straight past it:
  `VerifyTask` leaked its in-flight claim (wedging the Task as permanently
  un-verifiable — `candidate`'s only forward move is verify), `prepareDispatch`
  leaked its claim through the same mistake, and the locked phases of
  `DispatchTask`/`RetryTask` used bare `Unlock()` calls that would **deadlock the
  entire control plane** — with `net/http` recovering the handler panic so the
  daemon stayed up and silently stopped answering. All four now release by
  `defer`. `Service.budgetCeiling` also now sanitizes its `BudgetSource`, since
  `BudgetSource` is exported and a negative *ceiling* would make the over-budget
  check permit everything.
- **State-precondition failures returned HTTP 500 instead of 409.** Retrying a
  task that was never rejected (and the equivalent verify/replan/dispatch guards)
  reported a server error rather than the documented state conflict. New
  `ErrWrongState` sentinel, mapped to 409 alongside `ErrConflict` and
  `ErrIllegalTransition`.
- **Documented the rebase guard's real limits** (`docs/control-plane.md`,
  `git.go`). The `unsafe_rebase` ancestry check catches naive ref moves and
  merge/descendant commits, but **not** content-preserving rewrites
  (`cherry-pick` / `commit-tree` / `format-patch|am` reproduce the same weakened
  policy under a new SHA with no ancestry link), and **`task create` is unguarded
  entirely** — an attacker that can move the target ref can simply wait for the
  next Task. No ancestry test can close either; the real fix is a plane-owned
  target ref the agent cannot write, which arrives with the Sprint 59 integration
  transaction. Written down rather than papered over.
- **Registry leak: one dead `daedalus-job-*` entry per Job.** The real
  `CoordinatorRunner` registers a throwaway project to launch the agent headless
  and never removed it, so the registry accumulated an entry per Job pointing at a
  worktree that gets reclaimed. It is now deregistered on every exit path.
- **`core` milestone test broke when M15 was opened.** `TestParseMilestonesAgainstRealRoadmap`
  hard-asserted M15–M17 were all `Planned`; opening Milestone 15 in `ROADMAP.md`
  turned that into a red suite. It now asserts M15 is `In Progress` and M16–M17
  `Planned`.

## [0.49.0] - 2026-08-08

**Milestone 14: Independent Verification (V1).** "Done" is now decided by the
control plane checking a committed artifact against an oracle the worker can't
edit — never by self-report. A project declares its checks + acceptance files in a
committed `.daedalus/verify.json`; the policy is frozen + hashed at `base_sha`; a
**test-integrity gate** rejects any Job whose diff touches the frozen test files; a
**null-agent floor** rejects an empty change; and only the control plane performs
`candidate → verified`, by checking out the Artifact's commit into a fresh,
**digest-pinned** verifier container (network off, no credentials, no `/opt/tools`)
and running the frozen checks. An *independently reproducible verification result* —
not a proof of correctness. See `docs/control-plane.md`.

### Added
- **Independent verification — the real clean verifier, digest pinning, env
  policy, and the null-agent floor (Sprint 57, closing M14).** `daedalus task
  verify` now runs a real `CleanVerifier` by default: it checks out the artifact's
  `head_sha` into a **fresh, separate clean worktree** (never the Job's mutable
  one) and runs the frozen `policy.checks` in a container built from the project's
  image, failing on the first non-zero check. The project image is **pinned by
  `sha256:` digest** (`docker image inspect`) at task create — or lazily at first
  verify — and recorded on the Task (`image_digest`, new column + idempotent
  migration), so the artifact is verified in the environment it was authored
  against; capture is behind an injectable `ImageDigester` seam. The verifier's
  **environment policy** is explicit and hermetic-ish — `--network none`, no
  ambient credentials, no inherited `/opt/tools`, only the clean checkout mounted
  at `/workspace`, `--rm` — expressed as a pure `VerifierEnvPolicy.DockerRunArgs`
  that a host test asserts leaks nothing. A **null-agent floor** rejects any Job
  whose `head_sha == base_sha` (no change at all) with an "empty change" note,
  before the gate or verifier, so a do-nothing job can never earn a vacuous pass.
  The `CleanVerifier` + `dockerImageDigester` are the only Docker-dependent pieces
  (host-only); everything else (floor, gate, freeze, digest plumbing, env-policy
  args, transitions) is host-tested with a fake, and `DAEDALUS_CONTROL_FAKE_VERIFY`
  still selects the stub so tests and the verify scripts stay Docker-free. New
  `scripts/verify-m14.sh` exercises the whole flow (fake phase: 15 host-checkable
  assertions; real phase: the clean verifier container). The verification result is
  framed honestly as *reproducible*, not a *proof of correctness*. Pure-Go
  throughout (`CGO_ENABLED=0`).
- **Independent verification — acceptance contract, frozen oracle, test-integrity
  gate, and plane-owned verify transitions (Sprint 56, the host-testable half of
  M14).** Projects declare a verify policy in a committed `.daedalus/verify.json`
  (`checks` = the commands the clean verifier will run; `acceptanceGlobs` = paths
  whose edits invalidate a Job); `control.ReadAcceptancePolicy` reads it from a
  checkout, with a documented language-agnostic default (`daedalus docs lint --ci`
  plus conventional test/fixture globs). At `task create` the policy is read **as
  committed at `base_sha`** and a stable hash of the normalized (commands + globs)
  is **frozen** on the Task (`acceptance_hash`, new column + idempotent migration)
  — a later working-tree edit cannot change it, pinning the acceptance oracle
  outside the agent's reach. A pure `DiffTouchesAcceptanceFiles` (git
  `--no-renames --name-only` diff + a `**`-aware glob matcher) powers the
  **test-integrity gate**. A new `daedalus task verify <id>` (client→daemon route)
  drives the plane-owned flow behind an injectable `VerifyRunner`: confirm the
  frozen hash, run the **integrity gate first** (a diff touching a frozen
  acceptance file → straight to `rejected`, the verifier is **never called**),
  else `candidate → verifying → verified | rejected`; a rejection reclaims the
  worktree and a `task dispatch` retry is accepted (`rejected → queued → working`).
  Only the control plane performs `candidate → verified` (structural). The real
  clean-verifier **container** — checkout into a clean image, `sha256:` digest
  pinning, network/credentials policy, null-agent floor — is **Sprint 57**; here
  the runner is a stub (`StubVerifyRunner`; `DAEDALUS_CONTROL_FAKE_VERIFY=fail` to
  force a rejection), so the gate, freeze, and transitions are fully host-tested
  without Docker. Pure-Go throughout (`CGO_ENABLED=0`).

## [0.48.0] - 2026-08-08

**Milestone 13: Control Plane Foundation (V1).** The first step of the controlling
Guild Master arc — a host-side control plane, human-CLI-first. A `daedalus task`
CLI + `daedalus-control` daemon over `control.sock` own a Task/Job/Artifact model
in a pure-Go SQLite store; a dispatched Task runs as a headless Job in an isolated
Git worktree (process-exit boundary; only success → a candidate Artifact); and a
reconcile-on-boot/periodic loop keeps state consistent across crashes. No agent
client and no verification yet (M14/M15). See `docs/control-plane.md` and
`docs/guild-master-plan.md`.

### Added
- **Control-plane execution — the `daedalus-control` daemon, isolated-worktree
  headless Jobs, and reconciliation (Sprint 55, completing M13).** A new
  `cmd/daedalus-control` daemon becomes the single owner of `control.db` and
  serves an HTTP-over-Unix-socket API at `<data-dir>/.daedalus/control.sock`
  (`create`/`list`/`status`/`dispatch`/`cancel`); the `daedalus task` CLI was
  refactored into a **thin client** that auto-spawns and reuses the daemon (ssh-
  agent style, like `daedalus coordinator`), so there is never a second SQLite
  writer. A new `daedalus task dispatch <id>` runs **one headless Job attempt**:
  it drives the Task to `working`, creates a `Job`, and `git worktree add`s a
  clean checkout at the Task's `base_sha` on branch `daedalus/<task>/<job>` at the
  deterministic path `<data-dir>/control/worktrees/<job>` (never the developer's
  checkout), then runs the agent through an injectable `AgentRunner` taking
  **process exit as the boundary**. The wrapper auto-commits and captures the tree
  as `output_snapshot` (even on failure, as a salvage snapshot), sets
  `execution_result`, and **promotes only a `success` run** to a Job `candidate` +
  candidate `Artifact` (commit-exists never implies job-succeeded); failure/
  timeout/cancel are terminal and reclaim the worktree. The daemon **reconciles on
  boot and on a 30s tick** (the level-triggered controller pattern, the dual-write
  fix): a `working` Job whose coordinator session has vanished is failed and
  cleaned; a live one is adopted; an orphaned worktree with no live DB Job is
  removed — all idempotent via deterministic names, and liveness that can't be
  verified is left untouched. The whole control-plane logic lives in a host-tested
  `control.Service` (both it and the socket `Client` implement one `TaskAPI`); the
  coordinator/Docker dependency sits behind interfaces (`AgentRunner`,
  `SessionObserver`) so everything is tested with fakes — only the real
  `CoordinatorRunner` needs Docker and is host-only. Pure-Go throughout
  (`CGO_ENABLED=0`). See [`docs/control-plane.md`](docs/control-plane.md).
- **Control-plane foundation — Task/Job/Artifact model, SQLite store, and a
  `daedalus task` CLI (Sprint 54, the start of M13).** A new `internal/control`
  package defines the host-side, authoritative control-plane data model — `Task`
  (project, objective, acceptance_ref, base_sha, state), `Job` (base_sha, runner,
  budget, `execution_result` vs `output_snapshot`), and `Artifact` (base_sha,
  head_sha, branch, verify/review status) — plus the control-plane-owned state
  machine (`planned → queued → working → candidate → verifying → verified |
  rejected → approval_required → approved → integrated`; terminal: failed /
  cancelled / expired / integrated). The load-bearing invariant is structural: a
  *worker* may only reach `candidate`, and **only the control plane** performs
  `candidate → verified` — enforced via two transition entry points
  (`WorkerCanTransition` vs `CanTransition`) rather than convention. State is
  persisted in a pure-Go SQLite database (`modernc.org/sqlite`, so release builds
  stay `CGO_ENABLED=0`) at `<data-dir>/control.db` (`Config.ControlDBPath()`),
  with atomic optimistic transitions (`UPDATE … WHERE id=? AND state=?`) that each
  append an immutable row to an `events` log in the same SQL transaction. A new
  human-driven `daedalus task create|list|status|cancel` CLI drives the store
  in-process: `create` resolves the project through the registry, requires it to
  be a **Git repo**, captures the current `base_sha` from HEAD, and enforces one
  active task per project. (At Sprint 54 this drove the store in-process; Sprint
  55 above moved the CLI behind the `daedalus-control` daemon and added execution.
  The independent verifier and Guild Master client remain in M14/M15.) See
  [`docs/control-plane.md`](docs/control-plane.md).

## [0.47.0] - 2026-08-07

**Milestone 12: The Guild Master.** An always-present, un-removable `guild-master`
project whose agent reads across every project's documents — a read-only programme
overseer (a supervisor by visibility, not command).

### Added
- **Guild Master — read visibility across every project.** When the built-in
  Guild Master launches, every *other* registered project's directory is mounted
  **read-only** into its container at `/guild/<name>`, and a new in-container
  `guild-mcp` server exposes visibility-only tools over that tree:
  `list_guild_projects` (each project + a one-line milestone/sprint state),
  `read_project_doc` (read a named document, path-traversal rejected), and
  `guild_overview` (parsed milestones/sprints/progress per project). It can see
  every project and never write another's files. The mounts and the `guild-mcp`
  server are gated to the Guild Master alone (via a launch-set
  `DAEDALUS_GUILD_MASTER` env that `entrypoint.sh` keys the MCP entry on) — a
  normal project's agent gets neither. This is visibility only: it does not, and
  cannot, control or dispatch other agents. The Guild Master's workspace is
  seeded with a role `CLAUDE.md` framing it as the read-only programme overseer.
  Cross-project mounts are a launch-time snapshot: a project registered later
  appears on the Guild Master's next launch.

## [0.46.0] - 2026-08-07

### Changed
- **Guild Hall — hero levels now mean progress (Web UI).** A project's "Lv N" is
  now its **milestones completed** (parsed from `ROADMAP.md`), falling back to
  shipped-sprint count for a young project with no milestones yet, and `Lv 0` for
  one with neither — replacing the old "level = session count". So a hero's level
  reflects real accomplishment, not how often the project was launched. Derived
  host-side in a pure, unit-tested `guildProgression` helper via `/api/guild`.

### Added
- **Guild Hall — achievement badges (Web UI).** Hero cards now show earned
  badges derived from the project's own docs: 🏆 First Release (shipped a
  version), 🎖️ Milestone Master (5+ milestones done), 🧭 Trailblazer (a milestone
  underway), 🏃 Sprinter (10+ sprints shipped), ⭐ Veteran (10+ sessions). Badges
  refresh live on the existing 3s poll via the no-flicker diff-update.

## [0.45.0] - 2026-08-07

**Milestone 11: The Guild Hall, Reforged.** The Web UI's Guild view is now a
Secret-of-Mana-style party screen — every project is a distinct pixel-art hero
whose animation reflects its real activity (busy → working, idle → at ease,
sleeping → resting), riding the existing `/api/guild` busy/idle/sleeping signal.

### Changed
- **Guild Hall reforge, Sprint 51 (Web UI).** Finished the Secret-of-Mana-style
  party screen. Busy heroes now surface a themed **action ribbon** derived from
  the agent's live `detail` (e.g. `Edit`→"Casting Edit…", `Bash`→"Forging…",
  `Read`→"Reading the runes…"), falling back to the raw `detail` string when
  unmapped; idle heroes show a quiet "At ease…" line and sleeping heroes none.
  The ribbon updates live on the existing 3s poll via the no-flicker
  diff-update, without rebuilding the sprite. Added a small "Lv N" pip from
  `sessionCount`. The roster now reflows cleanly under the 768px breakpoint with
  no horizontal page scroll, honours `prefers-reduced-motion: reduce` (looping
  animations freeze to a clean static pose; state stays legible via colour and
  labels), and gained a framed JRPG empty/first-load state. `guild-preview.html`
  refreshed to showcase all three states plus a busy action ribbon. All
  frontend — no API or JSON-shape change.

## [0.44.0] - 2026-08-06

**Milestone 9: Release Bundling & Safe Upgrades.** Upgrading is now effortless
and reversible: a single checksum-verified release archive replaces the ~27
individual assets, and installs land side by side so a new version can be tried
and rolled back.

**Sprint 49: Side-by-side versioned installs.** A new install lands alongside
the current one instead of clobbering it, and switching or falling back is one
command.

### Added
- **Versioned install layout** — `setup.sh` now installs the payload into
  `$PREFIX/versions/<version>/` and maintains `$PREFIX/current` (the active
  version) and `$PREFIX/previous` (the rollback target) symlinks; the PATH
  symlink resolves through `current`, so a switch is a single symlink flip.
  Upgrades install alongside prior versions and flip `current` instead of
  overwriting. The shared project registry stays at `$PREFIX/.cache`, untouched
  by switches. A legacy flat install is transparently migrated into
  `versions/<old>/` on the first versioned upgrade.
- **`daedalus version` subcommand** — `list` (installed versions, marking the
  current one), `use <version>` (switch the active version, recording the prior
  as `previous`), `rollback` (return to the previous version), and
  `prune [--keep N]` (remove old versions, keeping the most-recent N — default 3
  — plus the current version, which is never removed). The install prefix is
  derived from the running binary (`os.Executable`), with a `DAEDALUS_PREFIX`
  override. Wired into `--help`, usage, and bash/zsh/fish completions.

### Changed
- **Uninstall handles the versioned layout** — removes all installed versions,
  the `current`/`previous` links, and the PATH symlink (and cleans up any
  leftover legacy flat files).

---

**Sprint 48: Bundled release archive.** Replaces the ~27 individual GitHub
Release assets with one self-contained, checksum-verified archive per platform.

### Added
- **`scripts/package-release.sh`** — the single source of truth for release
  packaging. Given a staging directory of per-platform binaries plus the shared
  runtime files and a version, it produces one `daedalus-<os>-<arch>.tar.gz` per
  platform (linux/darwin × amd64/arm64) — each containing that platform's five
  binaries renamed to their install names (`daedalus`, `skill-catalog-mcp`,
  `project-mgmt-mcp`, `daedalus-coordinator`, `daedalus-runner`) plus exactly the
  runtime files `setup.sh` installs — and a `SHA256SUMS.txt` over all archives.
  Archives are flat (no top-level directory) and deterministic (fixed mtime,
  sorted entries, `gzip -n`). Both the release workflows and the local
  simulation invoke it, so the packaging logic is genuinely exercised in tests.
- **`scripts/test-release-bundle.sh`** — a no-network end-to-end proof: builds
  the host binaries, packages them, then drives `install.sh`'s real
  verify + extract + `setup.sh` path against the produced archive into a
  throwaway prefix, asserting the installed tree, the symlink, checksum
  verification, and rejection of a corrupted archive.

### Changed
- **Release assets are now a single bundled archive per platform.** A release
  publishes **6** assets (4 `daedalus-<os>-<arch>.tar.gz` + `SHA256SUMS.txt` +
  `install.sh`) instead of ~27 individual files. The dev-release workflow uses
  the same packager.
- **`install.sh` downloads one archive** for the detected platform, **verifies
  its SHA-256 against `SHA256SUMS.txt`** (failing loudly on a missing or
  mismatched checksum), extracts it, and hands the result to `setup.sh` exactly
  as before. Every existing flag and the `latest`-vs-pinned tag resolution are
  preserved. The version is now baked into the packaged `config.json` at package
  time rather than patched in during install. A `DAEDALUS_ARCHIVE_DIR` hook lets
  the test suite install from a local archive without touching GitHub.

## [0.43.0] - 2026-08-06

**Milestone 8: Onboarding & Adoption.** Get a new user from install to first
productive session, and a new project from an empty tree to a valid roadmap arc.

### Added
- **`daedalus init [dir] [--force] [--no-scaffold]`** — the first-run entry point.
  Scaffolds the required project docs (reusing `core.ScaffoldDocs`, so a fresh
  project passes `daedalus docs lint` out of the box) and prints a short
  getting-started guide: register & start a project, reattach, the TUI and web
  dashboards, and the docs gate. Idempotent — existing docs are skipped unless
  `--force`; `--no-scaffold` prints the guide without writing anything.
- **Post-install next steps** — `install.sh`/`setup.sh` now point a new user at
  `daedalus init` and then `daedalus <name> <dir>` on a successful install.
- **`daedalus docs scaffold [dir] [--force]`** — writes conformant skeletons for
  the eight required project documents (`README`, `VISION`, `ARCHITECTURE`,
  `ROADMAP`, `BACKLOG`, `SPRINTS`, `CHANGELOG`, `CONTRIBUTING`) into a directory
  (default: current). The `ROADMAP.md` and `SPRINTS.md` skeletons already satisfy
  the structured-docs contract, so `daedalus docs lint --ci` passes on the fresh
  output — a new project starts with a valid roadmap arc instead of an empty tree.
  Existing files are skipped (never overwritten) unless `--force` is given. Backed
  by `core.ScaffoldDocs`, whose templates are the single source of truth for the
  skeleton bodies.

### Changed
- **Sharper value proposition** — the README hero and the CLI's no-args/`--help`
  banner now lead with the one-line pitch ("hands-off AI coding in a safe,
  isolated container") so a new user sees what Daedalus is and why immediately.

## [0.42.0] - 2026-08-05

**Milestone 7: Project-Management Tools & File-Derived State.** The in-container
agent gets proper MCP tools to manage the project's roadmap, and Daedalus derives
the rest of the project's state from its files instead of the agent self-reporting.

### Added
- **Lifecycle MCP tools** on `project-mgmt-mcp` — the agent evolves the roadmap
  through validated transitions instead of hand-editing: `add`/`remove`/`start`/
  `finish`/`pause_milestone` and `add`/`remove`/`move`/`start`/`finish`/`pause_sprint`.
  Each edits `ROADMAP.md`/`SPRINTS.md` and refuses a write that would break an
  invariant (exactly one milestone In Progress; a sprint finishes only once its
  items are Done; a milestone finishes only with no open sprint under it).
- **A `Paused` lifecycle state** — a milestone `(Paused)` heading and a sprint
  `Status: Paused` line, distinct from Done / In Progress / Planned.
- **Prose-preserving document writer** (`core/docwriter.go`) — structural edits to
  the roadmap docs that never re-serialize, so every hand-written line survives
  byte-for-byte. (This release's own milestone was closed by these functions.)

### Changed
- **Project state is derived from files (#52)** — version from `VERSION`, vision
  from `VISION.md`, progress from the current sprint's item statuses. What Daedalus
  shows always matches the files, with nothing self-reported.

### Removed
- **The self-report write tools** `report_progress` / `set_vision` / `set_version`
  and the `.daedalus/progress.json` store — replaced by the derived read state.

### Notes
- Daedalus *offers* these tools; it can't *gate* the agent (it launches the CLI and
  provides MCP servers — it is not the agent's harness), so this is capability, not
  enforcement. `move_milestone` was deferred (no reorder need yet).

## [0.41.0] - 2026-08-05

**Milestone 6: Roadmap Hierarchy, Made Visible.** The session sidebar now shows
the active milestone and its sprints, framed by how agentic development actually
flows — verified batches that cut a release — rather than by calendar time.

### Added
- **Sprint ship-pipeline in the session sidebar.** For the active (In Progress)
  milestone, its sprints render grouped by phase: **Building → Ready → Shipped**
  (+ optional **Proposed**). The **Ready** phase — work complete but not yet
  released — is first-class: it surfaces the verify/ship gate that a
  past/current/future view hides. An active milestone with no sprints is a valid
  view ("no sprints yet").
- **`core.PhaseOf`** derives a sprint's phase from its `Version` + item statuses
  (no schema change): Shipped = cut a release, Ready = all items done but no
  version, Building = work in flight, Proposed = declared/not-started. Plus a
  `SprintProgress` (done/total) helper.
- **`GET /api/projects/{name}/milestone-sprints`** returns the active milestone
  and its phased sprints. Sprints without an active milestone → an empty view.
- **`## Planned Sprints` convention** — a sprint header + `Milestone: N` line
  with no item table parses as a **Proposed** sprint (documented in
  `docs/structured-docs.md`).

### Notes
- Desktop sidebar only; a mobile sprints overlay is deferred.
- The full "sprint → batch/increment" vocabulary rename was considered and
  deferred — the light reframe (keep "sprint", derive phase, surface Ready)
  captures the agentic value at a fraction of the churn.

## [0.40.0] - 2026-08-04

**Milestone 4 (Layered Runner/Coordinator Architecture) endgame, structured
project documents, and Milestone 5 (Self-Sustaining Operations).** The runner
path — a `daedalus-runner` PID-1 process per container, its PTY fanned over a
Unix socket by the host-side `daedalus-coordinator` daemon — is now the **only**
launch path for all three UIs (CLI, TUI, Web); the classic tmux path has been
removed. The Web-UI-hangs-on-trust-prompt gap (#38) is resolved. Milestone 5
(below) lands and is **verified end-to-end on real Docker + a device** (Sprint
43): image builds + cache win, coordinator mounts and the shared/tools volumes
with the uid/permission check, pinned + checksum-verified installers, trust-
prompt idempotency, and mobile WebSocket resilience.

### Added
- **Runner path is the launch path** for CLI/TUI/Web — a `daedalus-runner`
  PID-1 process per container, its PTY fanned over a Unix socket by the
  host-side `daedalus-coordinator` daemon.
- **Repaint-on-attach (smart replay-from-boundary)** — a one-shot full-screen
  prompt (trust dialog, `--resume` picker) reconstructs for a late, second, or
  same-size viewer by replaying scrollback from the last screen boundary
  (`ScreenSnapshot`). The core of the #38 fix; covered by `e2e/run-repaint.sh`.
- **Workspace trust pre-seeded** in the default `claude.json`
  (`hasTrustDialogAccepted` + onboarding keys) so Claude's "trust this folder?"
  dialog never fires — the container is already the trust boundary (non-root,
  all caps dropped, `no-new-privileges`).
- **`daedalus-runner --cols/--rows`** (default 80×24): the PTY is sized at
  startup, so a one-shot startup prompt renders into a real terminal instead of
  creack/pty's 0×0 void. Routed through the hub; covered by hub tests.
- **Structured project documents → a file-derived dashboard.** `ROADMAP.md`
  milestones parse (`core.ParseMilestones`) and sprints link to them;
  `GET /api/projects/{name}/overview` serves the whole Purpose → Arc → Backlog
  journey in one fetch, which the per-project dashboard now renders.
- **`daedalus docs lint [--ci]`** gates the parseable docs — cross-file
  `ValidateDocs` (contradictions) + raw-text `LintHeadings` (silently-dropped
  headings).

### Changed
- **All three UIs go through the coordinator daemon.** The TUI was migrated off
  tmux to the runner path (runner-only); the CLI and Web already were. `daedalus
  web` offers an "Open" action that autostarts a session via the coordinator —
  no CLI pre-launch.
- **The per-project dashboard is the project's journey** (Purpose → Arc →
  Backlog), replacing the five-KPI grid that read agent-self-reported data.
- **`PROJECT-INIT.md` reconciled** to the structured-docs model and moved under
  `docs/`.
- **Installers are version-pinned** (#51, supply-chain) — the Dockerfile pins
  the Claude and Copilot CLIs via `CLAUDE_VERSION` / `COPILOT_VERSION` build
  args instead of unpinned `latest` `curl | bash`. Both installers verify the
  downloaded binary's SHA-256 (Claude against the release `manifest.json`,
  Copilot against `SHA256SUMS.txt`). Claude auto-updates at runtime via the
  shared versions cache, so the pin is an install floor, not a freeze.
- **Build-uid permission preflight** (#55/#27 ops) — daedalus records the host
  uid an image was built with (`<DataDir>/build-uid`), and the coordinator logs
  a clear warning when it later runs as a different uid, since the container's
  `claude` user (baked at build uid) then can't write the shared caches / tools
  dirs created at the run uid. Turns the cryptic "Permission denied" into a
  named cause + fix (`daedalus --build` as the current user).

### Fixed
- **#38 — the Web UI no longer hangs on the trust prompt** on the runner path;
  verified end-to-end on real Docker + Claude in the Sprint 41 parity pass.
- **`./test.sh` under the root golang container** — two tests that assumed a
  non-root user now pass (a `chmod` root bypasses; git "dubious ownership"
  during a `go build` VCS stamp, fixed with `-buildvcs=false`).
- **A runner-path integration-bug chain** surfaced while dogfooding (the
  container reaped before the runner bound its socket; CLI re-attach blocked by
  the tmux guard; stale sessions handed back to callers).
- **Mobile terminal scrollback** — the Web UI terminal now scrolls its output on
  phones. xterm's viewport doesn't reliably scroll via touch (notably iOS), and
  with mobile input routed through the Send box (`disableStdin`), single-finger
  vertical drags now drive the viewport by real pixels (`touch-action: none` on
  the container). This restores touch scroll-back after the tmux "History"
  button was removed with the tmux path.
- **Trust force-set is now crash-safe** — the entrypoint's idempotent trust /
  onboarding patch of `~/.claude.json` no longer aborts container startup under
  `set -e` if the cached file is malformed or unreadable (it is left untouched
  and boot continues; the worst case is a one-time dialog, not a crash). The jq
  transform is now covered by `scripts/test-trust-idempotency.sh` (in CI):
  old-cache fixtures assert the trust keys are forced true, MCP servers merged,
  user data preserved, and the patch idempotent.

### Removed
- **The classic tmux launch path is retired.** The `internal/session` package
  (tmux session + control-mode wire protocol), the `core.UseRunner()` seam and
  its `DAEDALUS_USE_TMUX` / `DAEDALUS_USE_RUNNER` env toggles, the tmux command
  builders (`BuildTmuxCommand`, `BuildControlSendKeys`, `BuildSessionCommand`),
  and the Web tmux/control terminal relays and Start endpoint are gone. The
  runner path is now the only path.
- **The `--no-tmux` flag and the `no-tmux` / `tmux-prefix` config.json fields**
  are removed (from the CLI parser, usage, shell completions, man page, and the
  install/setup scripts). Existing config files keep loading — the retired keys
  are simply ignored.

### Milestone 5 — Self-Sustaining Operations (verified — Sprint 43)

Less per-project disk and more runtime resilience. Built in an environment
without a Docker daemon or device, then **verified end-to-end on a real host in
Sprint 43** (see Verification below and
[docs/m5-verification.md](docs/m5-verification.md)).

#### Added
- **Shared, host-visible caches** under `<DataDir>/shared/`, bind-mounted into
  every runner container so projects stop re-downloading them: the Claude CLI
  version store (#37) and the Maven `.m2` repository (#21).
- **Per-project persistent tools prefix** at `/opt/tools`
  (`<DataDir>/tools/<project>`, on `PATH`), so tools the agent installs at
  runtime survive restarts (#27). See
  [docs/tool-persistence.md](docs/tool-persistence.md). System `apt` installs
  are intentionally **not** persisted (base images stay reproducible).
- **WebSocket keepalive + client auto-reconnect** for the web terminal (#29):
  server ping + read deadline, client exponential-backoff reconnect gated by
  an intentional-close flag, and `visibilitychange`/`online` listeners — so a
  mobile Wi-Fi/cellular handoff or a backgrounded tab reconnects and repaints
  instead of dying. The session already survived the drop server-side.

#### Changed
- **Dockerfile layer efficiency** (#51): split into stable `*-base` parent
  stages + thin leaf targets that `COPY` the frequently-rebuilt Daedalus
  binaries last, so a version bump no longer busts the Go/SDKMAN/Godot/Copilot
  download layers.
- **Runner volume mounts centralized** in `core.RunnerVolumeArgs`, used by the
  coordinator launch path.
- **Trust/onboarding keys are force-set idempotently on every container boot**
  (previously seeded write-once), so a project cache predating those keys can
  no longer trigger the "trust this folder?" dialog.

#### Fixed
- **Coordinator runner path was missing bind mounts** (#55): the now-default
  path never mounted the shared skill catalog (`/opt/skills`) or the
  project-mgmt progress dir (`/workspace/.daedalus`) that the legacy path
  added, so skills and MCP progress reporting were unavailable on it.

#### Verification (Sprint 43, on a real Docker host)
- **Image builds + cache win.** All six Dockerfile targets
  (base/utils/dev/godot/copilot-base/copilot-dev) build; a Daedalus-binary
  change reuses the toolchain-download layers (only the final `COPY`s re-run).
  Runnable via [`scripts/verify-m5.sh`](scripts/verify-m5.sh).
- **Container uid (the top risk) — clear.** With the same user building and
  running, the build/run/container uids all matched and every shared/tools mount
  was writable. Daedalus now records the build uid (`<DataDir>/build-uid`) and
  the coordinator warns clearly if a later run's uid differs (the exact
  permission cause), so the mismatch case fails loud instead of cryptically.
- **Mounts + nested bind mounts confirmed** — all five runner mounts present and
  writable; the shared caches at subpaths under `/home/claude` are not masked by
  the home mount.
- **Installers pinned + checksum-verified** (#51): `CLAUDE_VERSION=2.1.221`,
  `COPILOT_VERSION=v1.0.78` via Dockerfile build args (was unpinned `latest`);
  both installers verify the binary SHA-256 (Claude vs. the release
  `manifest.json`, Copilot vs. `SHA256SUMS.txt`). The `TODO(#51)` markers are
  gone.
- **Trust idempotency confirmed** — an older cache (trust keys dropped) fires no
  "trust this folder?" dialog after a restart; the force-set is hardened to be
  non-fatal on a malformed cache, and covered by
  `scripts/test-trust-idempotency.sh`.
- **Mobile (#29) confirmed on a real phone** — reconnect + repaint across a
  backgrounded tab and a Wi-Fi/cellular switch; terminal touch-scroll fixed.
- **Deferred:** the Maven read-only-base + per-project overlay (#21) — the single
  shared writable `.m2` is standard practice; revisit only if pollution appears.

## [0.39.0] - 2026-07-11

Sprint 40 (Coordinator-as-Daemon) — the second Milestone 4 slice.
Promotes the in-process `internal/coordinator` from v0.38.0 into a
long-lived host daemon with an HTTP-over-Unix-socket API, a Go
client, ssh-agent-style auto-spawn, and persistent sessions across
daemon restarts. Both the CLI (`launchProjectViaRunner`) and Web
(`?mode=runner`) now go through the daemon, so a session started
from one is visible from the other. Still opt-in via
`DAEDALUS_USE_RUNNER=1`; the tmux path is unchanged.

### Added
- **`daedalus-coordinator` daemon binary** (`cmd/daedalus-coordinator`).
  Reads config.json for defaults, binds an HTTP handler on a Unix
  socket, handles SIGINT/SIGTERM for graceful shutdown, writes a
  pidfile when given `--pid-file`. Flags: `--socket`, `--compose`,
  `--data-dir`, `--pid-file`.
- **`daedalus coordinator start|stop|status`** CLI subcommand.
  `start` forks the daemon in a new session (Setsid), streams
  stdout/stderr to `<DataDir>/.daedalus/coordinator.log`, writes a
  pidfile at `<DataDir>/.daedalus/coordinator.pid`, waits up to 5s
  for the socket to appear. `stop` SIGTERMs the daemon and polls for
  exit. `status` reports PID + socket + calls the daemon's List for
  the tracked session summary.
- **`internal/coordinator/daemon.go`** — HTTP-over-UDS server.
  `POST /sessions` (Start), `GET /sessions` (List), `GET /sessions/{name}`
  (Get), `DELETE /sessions/{name}` (Stop). 4xx/5xx errors carry a
  JSON envelope; `List` always returns `[]`, never `null`.
- **`internal/coordinator/client.go`** — Go client wrapping the wire
  API with the same method shape as the in-process `Coordinator`.
  Domain sentinels survive the wire crossing: 409 → `ErrAlreadyRunning`,
  404 → `ErrNotFound`. Transport failures become non-sentinel wrapped
  errors so callers can distinguish "not found" from "server
  unreachable".
- **`internal/coordinator/bootstrap.go`** — `EnsureRunning(opts)`
  returns a Client, spawning the daemon detached if a fast liveness
  check (pidfile alive + socket dialable) fails. `DefaultLayout` and
  `DefaultSessionsFile` give every UI process one place to compute
  the standard `<DataDir>/.daedalus/` paths.
- **`sessions.json` persistence** — `Coordinator.Options.SessionsFile`
  turns on write-on-change (atomic temp-file + rename) plus
  load-and-reconcile at construction. On startup the coordinator
  runs `docker ps --format {{.Names}}` and drops any recorded
  session whose container is no longer running, then rewrites the
  file so a subsequent boot doesn't reinherit dead state.
- **`contrib/systemd/daedalus-coordinator.service`** — user-scope
  systemd unit for the daemon.
- **`contrib/launchd/dev.techdelight.daedalus-coordinator.plist`** —
  per-user launchd agent plist for the daemon (edit `$HOME` first).
- **Real-daemon integration test**
  (`cmd/daedalus-coordinator/integration_test.go`) — builds the
  daemon binary, spins up a POSIX-sh mock `docker` on PATH, drives
  the full stack via `coordinator.NewClient` through
  Start → List → Get → duplicate-Start (ErrAlreadyRunning) → Stop →
  post-Stop-Get (ErrNotFound), then verifies `sessions.json` ends
  up empty. Skipped under `-short` and on non-Unix.

### Changed
- **`launchProjectViaRunner` now uses the daemon** — the CLI runner
  path no longer constructs an in-process `Coordinator`; it calls
  `ensureCoordinatorClient(cfg)` and drives `client.Start(cfg)`.
  A second CLI invocation for the same project sees the existing
  session via the shared daemon.
- **Web `?mode=runner` handler now uses the daemon** — instead of
  stat'ing the runner socket file, `handleTerminalRunner` calls
  `coordinator.EnsureRunning` then `client.Get(name)`. Stale socket
  files left over from crashed prior containers no longer produce a
  misleading 200; a missing session cleanly returns 404 with a hint
  to start one via `DAEDALUS_USE_RUNNER=1 daedalus <name>`.
- **`coordinator` package documentation** — now describes both
  deployment modes: in-memory (test-only / no `SessionsFile`) and
  daemon-mode (persistent + reconciled). Points at daemon.go +
  client.go as the daemon-mode surfaces.
- **CLI dispatcher** — new `coordinator` subcommand registered in
  `internal/config/config.go`'s `collectorSubcommands` and dispatched
  from `cmd/daedalus/main.go`. `core.Config` gains `CoordinatorArgs`
  to receive the positional after "coordinator".

### Fixed
- **Web runner-mode 404 truthfulness** — the pre-Sprint-40 handler
  returned 200 whenever the socket file existed on disk, even if
  the container behind it had crashed. Going through the daemon
  makes "session is tracked" the source of truth.

### Infrastructure
- **`daedalus-coordinator` staged into `PREFIX` by `setup.sh`**
  (mirrors the existing `daedalus-runner` staging) and downloaded
  best-effort by `install.sh` — a 404 for older releases skips
  cleanly without aborting the install.
- **Release workflows** (`release.yml`, `dev-release.yml`) now build
  `daedalus-coordinator` in the 4-arch matrix and publish it as a
  release asset. Narrowed the previously overly-broad `daedalus-*`
  glob so `daedalus-coordinator-*` doesn't get double-copied.

## [0.38.0] - 2026-07-11

Foundation release for Milestone 4 (Layered Runner / Coordinator
Architecture). A new `daedalus-runner` PID-1 binary runs inside the
project container and speaks a socket-based wire protocol to the
host; CLI and Web can both attach through it. The runner path is
opt-in via `DAEDALUS_USE_RUNNER=1` — the default launch path is
unchanged. Foreman was removed to clear the way. Ships alongside
large refactors of `main.go`, `web.go`, `tui/`, and `registry.go`
into topic files, plus support for parallel test installs.

### Added
- **`daedalus-runner`** — standalone PID-1 binary that owns the PTY
  inside the project container and fans its output out to any number
  of connected UNIX-socket clients. Installed into the container
  image at build time.
- **`internal/runproto`** — host ↔ runner wire protocol: `Hello`,
  `Output`, `Input`, `Resize`, length-prefixed on a single Unix
  socket.
- **`internal/runclient`** — host-side socket client. Dials the
  runner, replays scrollback from the hello frame, and exposes
  `Read` / `Write` / `Resize` / `Detach`.
- **Per-runner adapter layer (`internal/runner`)** — `Adapter`
  interface with `claude` and `copilot` implementations. Decouples
  the runner binary from the specific coding agent it launches.
- **`internal/coordinator`** — host-side lifecycle owner for
  runner-attached containers. `Coordinator.{Start,Get,List,Stop}`
  prepares the socket directory, runs `docker compose run --rm
  --detach` with `DAEDALUS_RUNNER=1`, waits for the bind-mounted
  socket to appear, and tracks live sessions in-process (no daemon,
  no persistence yet). Replaces the "where does this session live"
  role tmux used to play. Covered by 12 unit tests including a real
  `net.Listen("unix", …)` smoke test.
- **CLI runner path** — `DAEDALUS_USE_RUNNER=1` short-circuits
  `launchProject` to `launchProjectViaRunner`: the coordinator
  spawns the container, the host attaches through runclient. tmux is
  not involved.
- **Web terminal `?mode=runner`** — third terminal mode alongside
  the default PTY relay and `?mode=control` (tmux control mode).
  Dials the project's `daedalus-runner` Unix socket via
  `internal/runclient` and bridges it with the WebSocket through
  `runnerRelay`. Requires the project to have been started with the
  runner path; the handler returns 404 otherwise. Concurrent CLI +
  Web attach works because the runner fans out.
- **Parallel test installs** — `install.sh` and `setup.sh` accept
  `--link-name`, `--container-prefix`, `--tmux-prefix`, and
  `--image-prefix`. Populates `container-prefix` / `tmux-prefix` /
  `image-prefix` keys in `config.json`; `core.Config` honours them
  via `ContainerName()` and `TmuxSession()`. `setup.sh` also stages
  `daedalus-runner` into `PREFIX` so the Dockerfile COPY succeeds
  when building the image from a custom location. See CONTRIBUTING.md
  "Parallel Test Installs".
- **`/sprints`, `/backlog`, `/strategic-roadmap` Web endpoints** —
  three REST handlers matching the post doc-split frontend, which had
  been calling these URLs since v0.37 even though only the legacy
  `/roadmap` was registered. `/sprints` reads `SPRINTS.md` with
  fallback to `ROADMAP.md`, `/backlog` parses `BACKLOG.md` via
  `core.ParseBacklog`, `/strategic-roadmap` returns the raw
  `ROADMAP.md` content. `/roadmap` remains as an alias with the same
  SPRINTS-first fallback.
- **`core.Config.RunnerSocketPath()`** — single source of truth for
  the runner socket path; CLI and Web share it.
- **`.daedalus/` runtime state** — now covered by `.gitignore`.

### Changed
- **`launchProjectViaRunner` delegates to coordinator** — the
  inline compose env, `--detach`, and socket-poll are gone from
  `cmd/daedalus/launch.go`; the lifecycle lives in
  `internal/coordinator` in one testable place.
- **Split `cmd/daedalus/main.go` dispatcher** — 1674-line file with
  all 13 subcommand handlers inlined → 12 topic files
  (`build.go`, `launch.go`, `resolve.go`, `clone.go`,
  `config_cmd.go`, `usage.go`, `list.go`, `persona.go`, `runners.go`,
  `programmes.go`, `skills.go`, `foreman.go`) plus a 171-line
  dispatcher. No behaviour change. (Backlog #50)
- **Split `internal/web/web.go` god-object** — 1196 lines / 31
  methods across 6 unrelated domains → topic files
  (`projects.go`, `dashboard.go`, `roadmap.go`, `programmes.go`,
  `terminal.go`, `control_relay.go`, `runner_relay.go`). `web.go` is
  now a 169-line orchestrator listing every route in one place.
  No behaviour change. (Backlog #49)
- **Extracted `controlRelay` from `handleTerminalControl`** — the
  ~200-line WebSocket handler that mixed protocol handling with the
  tmux control-mode relay became `controlRelay` in
  `internal/web/control_relay.go` with focused methods.
  `handleTerminalControl` is now ~40 lines. No behaviour change.
- **Split `internal/tui/tui.go` and `core/registry.go`** — the same
  topic-file treatment for the TUI (`commands.go`, `model.go`,
  `view.go`, mode files, `styles.go`) and registry. No behaviour
  change.
- **Deduplicated `ShellQuote`** — removed
  `internal/session.ShellQuote` (a copy of `core.ShellQuote`) and
  routed `internal/session` and `internal/web` through
  `core.ShellQuote`. Per ARCHITECTURE/CONTRIBUTING, command builders
  belong in `core/`.

### Fixed
- **Runner-attach race on `DAEDALUS_USE_RUNNER=1`** —
  `runclient.Dial` did not retry, so a slow container start could
  fail the attach. `coordinator.Start` now waits for the socket to
  appear (30s poll) before returning.
- **Large paste kills WebSocket** — pasting text containing newlines
  (or any multiline input on mobile) terminated the tmux control-mode
  `send-keys` command at the first `\n`, desyncing the response
  queue and dropping the WebSocket connection. Added
  `core.BuildControlSendKeys` which translates newlines to `Enter`
  keystrokes and uses `send-keys -l` (literal) for non-newline
  content, keeping the resulting command on one line.
  (Backlog #47, #48)
- **Project-detail roadmap panels stayed empty** — after the v0.37
  doc split, the project-detail view fetched `/sprints`, `/backlog`,
  and `/strategic-roadmap`, all of which 404'd because only the old
  `/roadmap` route was wired up. Adding the three handlers restores
  the panels. (Backlog #34)
- **`install.sh` recorded `"version": "unknown"`** — the shipped
  `config.json` template had an empty `"version"` field and no code
  patched it before handing off to `setup.sh`. `install.sh` now
  sed's the release tag (with the leading `v` stripped) into
  `config.json` before invoking `setup.sh`.

### Removed
- **Foreman** — the in-process AI project manager
  (`internal/foreman/`, `core/foreman.go`,
  `internal/web/foreman.go`, `cmd/daedalus/foreman.go`) and the
  surrounding cascade machinery were removed wholesale. The
  `daedalus foreman` CLI subcommand, `/api/foreman/*` HTTP routes,
  `daedalus programmes cascade` subcommand, `CascadeStrategy` type,
  and `DependencyEdge.Strategy` field are all gone. The Web UI's
  Foreman view (and the programme-management form embedded in it) is
  removed; programme CRUD remains via CLI and `/api/programmes/*`.
  Done to clear the way for the runner-adapter / daedalus-runner /
  coordinator architecture.

## [0.37.0] - 2026-04-18

### Added
- **Document structure split** — separated monolithic `ROADMAP.md` into three purpose-specific files: `ROADMAP.md` (strategic milestones), `BACKLOG.md` (prioritised work items), `SPRINTS.md` (sprint execution history).
- **Backlog parser** — `core/backlog.go` with `BacklogItem` type and `ParseBacklog()` function for parsing `BACKLOG.md` tables.
- **New MCP tools** — `get_sprints` (parse sprints from `SPRINTS.md`), `get_backlog` (parse backlog items), `get_strategic_roadmap` (raw roadmap content). All sprint tools fall back to `ROADMAP.md` for backward compatibility.
- **Muse starter skill** — new `muse.md` starter skill in the skill catalog.

### Changed
- **`ParseRoadmap` renamed to `ParseSprints`** — reflects that sprint data now lives in `SPRINTS.md` rather than `ROADMAP.md`.
- **`get_roadmap` / `get_current_sprint` MCP tools** — now read from `SPRINTS.md` first, falling back to `ROADMAP.md` for projects that haven't migrated.
- **MCP client** — updated methods to use new tool names and added methods for backlog and strategic roadmap queries.

## [0.36.0] - 2026-04-12

### Added
- **JRPG Guild Hall UI** — new web view where each project is a pixel-art mage avatar with state-based animations. Avatars bounce with particles when busy, float gently when idle, and dim with floating "zzz" when sleeping. Each project gets a unique color palette derived from its name. Click an avatar to navigate to the project dashboard.
- **Runner-agnostic activity detection** — new `internal/activity/` package with `RunnerActivityDetector` interface, `DetectorRegistry` for mapping runner names to detectors, and `NullDetector` fallback. Adding a new runner requires only a detector implementation and one `Register()` call.
- **Claude Code `Stop` hook** — the definitive "finished processing" signal. Previously idle detection relied on `Notification` + 30-second staleness timeout; the `Stop` hook fires after ALL processing completes.
- **Three new Claude Code hooks** — `Stop` (idle), `PostToolUse` (sustained busy), and `UserPromptSubmit` (transition to busy), bringing total activity hooks to 6.
- **`HookConfig` in `RunnerProfile`** — each runner profile carries its activity hook definitions, connecting hook configuration to runner identity.
- **Settings generation** — `internal/hooks/` package renders runner-specific `settings.json` from `HookConfig` templates with placeholder substitution.
- **`GET /api/guild`** — REST endpoint returning all projects with unified three-state activity (busy/idle/sleeping), progress, and metadata for the guild hall view.

### Changed
- **`GET /api/projects/{name}/state`** — now returns activity-level state (`busy`/`idle`/`sleeping`) via `activityResolver` instead of raw container state. Response includes `containerState` field for backward compatibility.
- **Resolver is runner-aware** — `Resolve(containerName, projectDir, runnerName)` selects the correct detector per project based on its runner configuration.

### Fixed
- **Removed broken tests** — deleted test functions referencing unimplemented handlers (`handleSprints`, `handleBacklog`, `handleStrategicRoadmap`) that caused CI build failures.

## [0.35.0] - 2026-04-04

### Fixed
- **WebSocket resize race condition** — the tmux reader goroutine and WebSocket handler goroutine were both calling `ReadMessage()` on the same `bufio.Reader`, racing for command responses. Refactored `handleTerminalControl()` so a single reader goroutine consumes all tmux control-mode output, with a `sendTracked`/`dequeueType` queue to match `%begin/%end` responses to commands.
- **No terminal refresh after resize** — resize messages produced no visible response because `%layout-change` events were silently dropped. The reader goroutine now auto-captures visible pane content on `%layout-change` and sends a `live-capture-response` back to the client.
- **Terminal staircase formatting** — captured pane content was joined with `\n` (LF only), causing xterm.js to indent each line progressively. Changed to `\r\n` (CRLF) so the cursor returns to column 0 on each line.
- **Discarded errors in web handlers** — fixed 8 silently discarded errors in `web.go`: PTY cleanup (`Close`, `Wait`, `Signal`), PTY relay (`Setsize`, `Write`), progress read, index HTML write, and WebSocket response write. All now either log or return on error per contributing guidelines.

### Changed
- `shellQuote` exported as `ShellQuote` in `internal/session` package (required by the refactored `handleTerminalControl` which builds tmux commands directly).
- **Pipeline version in config.json** — release workflow patches `config.json` with the semantic version (e.g. `0.34.0`); dev-release workflow patches it with `dev_{timestamp}` (e.g. `dev_20260404120000`). Version patching removed from `install.sh` — now handled entirely at build time. (Backlog #43)

## [0.34.0] - 2026-04-03

### Fixed
- **Blank terminal on attach** — terminal now sends a `live-capture` request immediately after WebSocket connect and resize, so the current pane content is displayed right away instead of waiting for new tmux output. Especially impactful on mobile where the blank + disabled-input terminal appeared broken. (Backlog #42)
- **Foreman roadmap display** — `showDashboard()` now resets the roadmap panel and auto-loads the roadmap via `loadRoadmap()` when opening a project from the Foreman or project list. Previously the roadmap panel could show stale data or remain empty. (Backlog #41)

### Added
- `CaptureVisible()` and `CaptureVisible_Error` unit tests for the control session package.
- Backlog items 41–42 added to ROADMAP.md.

## [0.33.0] - 2026-04-02

### Added
- **History mode visual indicator** — blue "HISTORY MODE" banner with hint text appears between terminal header and content when viewing scrollback. History button highlights with active state. (Backlog #35)
- **History mode exit** — press Esc, any key, or click the Exit button in the banner to leave history mode. Exiting sends a `live-capture` request to restore the current visible pane content. (Backlog #36)
- **Live capture endpoint** — new `CaptureVisible()` method on `ControlSession` captures only the visible pane (no scrollback depth). New `live-capture` WebSocket message type in `handleTerminalControl()`.
- **Scroll recovery after crash/disconnect** — history mode state resets automatically on WebSocket close, error, or terminal disconnect, preventing stale scroll viewport. (Backlog #40)
- Backlog items 34–40 added to ROADMAP.md.

## [0.32.0] - 2026-04-01

### Added
- **tmux control mode web terminal** — `handleTerminalControl()` in `internal/web/web.go` uses `ControlSession` (tmux `-C`) instead of raw PTY relay. Activated via `?mode=control` query parameter on the terminal WebSocket endpoint. Both modes coexist.
- **Scrollback support** — "History" button in the terminal header requests the last 1000 lines of pane scrollback via `CapturePane()`. Client sends `{"type":"scrollback","lines":N}`, server responds with `{"type":"scrollback-response","content":"..."}`.
- Web UI terminal now defaults to control mode for scrollback access.

## [0.31.0] - 2026-04-01

### Added
- **tmux control mode session** — `ControlSession` in `internal/session/control.go` spawns `tmux -C attach-session` and provides structured message I/O: `SendKeys()`, `CapturePane()`, `ResizeWindow()`, `ReadMessage()`, `Close()`.
- **Control message parser** — `ParseControlLine()` in `internal/session/controlparser.go` parses all `%`-prefixed tmux control mode messages: `%output`, `%begin`, `%end`, `%error`, `%layout-change`, `%session-changed`, `%window-renamed`, `%pane-mode-changed`.
- 19 unit tests covering all message types, edge cases (empty input, special characters, spaces), `shellQuote()`, and `safeIndex()`.

## [0.30.0] - 2026-04-01

### Added
- **tmux control mode design document** (`docs/tmux-control-mode.md`) — research spike covering current PTY relay architecture, tmux `-C` protocol analysis, component impact assessment, phased implementation plan (4 phases, ~850 lines, 3 sprints), and migration strategy. Approved for implementation.

## [0.29.1] - 2026-04-01

### Fixed
- Playwright e2e: scoped `.btn-back` selector to `#terminal-view` to fix strict mode violation when multiple Back buttons exist.

### Added
- Playwright e2e test suite (34 tests) covering static assets, HTML structure, all REST API endpoints, programme CRUD lifecycle, and auth modes.
- Go test coverage improvements: `core` 98%, `cmd/skill-catalog-mcp` from 0% to tested, `internal/web` 60.6%.
- `new-and-improved.md` — summary of all changes from v0.8.2 to v0.29.0.

### Changed
- README and ARCHITECTURE synced with codebase: auth config fields, skill directory structure, `/login` route, `ValidTargets()`, `UpdateProjectTarget()`, GitHub URL parsing.

## [0.29.0] - 2026-04-01

### Added
- **Switch target for existing project** — `daedalus config <name> --set target=<stage>` changes the build target without re-registering. Validates against known targets (dev, godot, base, utils).
- `UpdateProjectTarget()` method in registry package.
- `ValidTargets()` and `IsValidTarget()` functions in core package.
- **GitHub repo projects** — pass a GitHub URL or `owner/repo` shorthand as the project name to clone and register in one step. E.g., `daedalus https://github.com/user/repo` or `daedalus user/repo`.
- Tests for target switching, GitHub URL parsing, and registry `UpdateProjectTarget`.

## [0.28.0] - 2026-04-01

### Added
- **Web UI authentication** — token-based login protects the dashboard when exposed on a network. A cryptographically random access token is generated on first launch and stored in `config.json`.
- `--auth` / `--no-auth` flags for `daedalus web` — authentication is enabled by default; use `--no-auth` to disable.
- Login page at `/login` with token input form, styled to match the Daedalus theme.
- Session cookie (`daedalus_session`) with configurable expiry (default 24 hours, `auth-expiry` in `config.json`).
- WebSocket authentication via session cookie (automatic) or `token` query parameter (fallback).
- `internal/auth` package with 12 unit tests covering token generation, middleware, login flow, cookie handling, and query parameter auth.
- Shell completions for `--auth` and `--no-auth` flags in bash, zsh, and fish.

## [0.27.0] - 2026-04-01

### Added
- **Favicon** — SVG favicon with labyrinth motif added to the Web UI, visible in browser tabs.
- **Foreman UI project navigation** — clicking a project card in the Foreman view now navigates to that project's detail/dashboard view.

### Changed
- **Skill catalog directory structure** — skills are now stored as `{name}/SKILL.md` directories instead of flat `{name}.md` files. Applies to both the shared catalog and per-project installed skills. Starter skills are seeded in the new format on first run.

## [0.26.2] - 2026-04-01

### Added
- Backlog item #32: Foreman UI project navigation — clicking a project opens its detail view.
- Backlog item #33: tmux control mode integration — native scrollback, clean disconnect/reconnect, event notifications.

## [0.26.1] - 2026-03-30

### Added
- **MCP server reconciliation on startup** — entrypoint now ensures daedalus-specific MCP servers (`skill-catalog`, `project-mgmt`) are present in the runner's config. Missing entries are added from defaults; existing entries and user-added servers are preserved.

### Fixed
- `project-mgmt-mcp` panic on startup caused by `google/jsonschema-go` v0.4.2 rejecting `description=` prefixed struct tags. Tags now use plain description strings.
- Added 12 tests for `project-mgmt-mcp` covering all MCP tools, error handling, and version fallback.

## [0.26.0] - 2026-03-30

### Added
- **Foreman web frontend** — dedicated view accessible from the main Daedalus page for managing the Foreman and its programmes.
  - Foreman status panel with live state indicator, programme selector, and Start/Stop controls.
  - Active plan display with project cards showing progress bars, agent state badges, and current sprint info.
  - Cascade event log with color-coded action badges (propagate/notify/skip).
  - Full programme CRUD: create, edit, and delete programmes with project lists and dependency edges.
- REST API endpoints for programme management: `GET/POST /api/programmes`, `GET/PUT/DELETE /api/programmes/{name}`.

### Changed
- Dev and copilot-dev Docker targets now install Go 1.25 from the official tarball instead of Debian's `golang-go` package (was Go 1.19).
- `build.sh` and `test.sh` updated to use `golang:1.25-bookworm` image.

## [0.25.1] - 2026-03-30

### Fixed
- Foreman `Start()` race condition — hold lock through goroutine launch to prevent TOCTOU between state check and `go f.run()`.
- Foreman `Stop()` double-close panic — added guard flag to prevent closing `stopCh` twice.
- `mcpclient.GetProjectStatus()` now propagates errors instead of silently returning partial data.
- `agentstate.ContainerObserver` returns `StateUnknown` (not `StateStopped`) when Docker is unreachable, preventing false "stopped" reports.
- Foreman planner and monitor now check registry and MCP client errors instead of silently ignoring them.
- `programme.Store.List()` returns error on corrupt JSON files instead of silently skipping them.
- Extracted shared `buildSummary()` function to eliminate duplication between planner and monitor.
- Added missing tests: `DefaultObserver` (GetState, IsActive), Foreman web handlers (status, start, stop), `programme.Store.Update()`, `agentstate` state constants and additional container states.

## [0.25.0] - 2026-03-30

### Added
- **Programme-level cascade orchestration** — when an upstream project completes, the Foreman evaluates which downstream projects need work based on the dependency graph and cascade strategy.
- `CascadeStrategy` type on `DependencyEdge` — `auto` (Foreman acts), `notify` (flag for human approval), `manual` (skip). Defaults to `notify`.
- `daedalus programmes cascade <name> [--dry-run]` — preview cascade propagation for a programme. Shows which downstream projects would be affected, with color-coded actions.
- Cascade event log in Foreman status API response (`cascadeLog` field).
- `EvaluateCascade()` function evaluates cascade actions for completed projects.

## [0.24.0] - 2026-03-30

### Added
- **The Foreman** — AI-driven project manager that monitors programmes. Reads roadmaps, builds plans, monitors agent state, and reports through the Web UI. Runs as a background goroutine inside `daedalus web`.
- `daedalus foreman` CLI subcommand — `start`, `stop`, `status` commands (delegates to Web UI API).
- Foreman REST API — `POST /api/foreman/start` (starts Foreman for a programme), `POST /api/foreman/stop`, `GET /api/foreman/status` (returns state, plan, and message).
- Foreman status indicator in Web UI header — shows "Foreman: monitoring" when active.
- `internal/foreman` package — `Foreman` (main loop), `Planner` (builds plans from programme data), `Monitor` (polls project and agent state).
- `core/foreman.go` — `ForemanConfig`, `ForemanState`, `ForemanPlan`, `ForemanProject`, `ForemanStatus` pure types.
- Shell completions for `foreman` subcommand in bash, zsh, and fish.

## [0.23.0] - 2026-03-30

### Added
- **Agent observability** — `internal/agentstate` package with `Observer` interface and `ContainerObserver` implementation that determines agent state from Docker container status.
- `GET /api/projects/{name}/state` REST endpoint returning current agent state (running, stopped, idle, error, unknown).
- Pulsing animation on running project status dots in the Web UI.
- `internal/foreman/observer.go` — `AgentObserver` interface and `DefaultObserver` wrapper for use in the Foreman loop.

## [0.22.0] - 2026-03-30

### Added
- `internal/mcpclient` package — host-side MCP client that reads project progress and roadmap data from bind-mounted files. Provides `ReadProgress()`, `ReadRoadmap()`, `GetCurrentSprint()`, and `GetProjectStatus()` methods.
- `daedalus programmes show <name>` now displays aggregated member project status — progress percentage, version, and current sprint for each project in the programme.

### Changed
- `programmes show` output changed from raw JSON dump to a formatted display with programme header, dependency graph, and per-project status table.

## [0.21.0] - 2026-03-30

### Added
- **Roadmap parsing** — `ParseRoadmap()` in `core/roadmap.go` parses Daedalus-native ROADMAP.md files into structured `Sprint` and `SprintItem` data. Detects current vs historical sprints.
- `GET /api/projects/{name}/roadmap` REST endpoint returning parsed sprint data from the project's ROADMAP.md.
- **Roadmap panel in Web UI** — click "Show Roadmap" in the project dashboard to see all sprints with items, statuses, goals, and version tags. Current sprints are highlighted.
- `get_roadmap` and `get_current_sprint` MCP tools in `project-mgmt-mcp` — agents can query the project's ROADMAP.md for sprint data.
- `core/sprint.go` — `Sprint`, `SprintItem`, `SprintStatus` pure types.

## [0.20.0] - 2026-03-30

### Added
- **Multi-project programmes** — declare named collections of related projects with dependency relationships. Foundation for programme-level orchestration.
- `daedalus programmes` CLI subcommand — `list` (shows all programmes), `show <name>` (prints full config), `create <name>` (creates empty programme), `add-project <programme> <project>` (adds project to programme), `add-dep <programme> <upstream> <downstream>` (declares dependency), `remove <name>` (deletes programme).
- `core/programme.go` — `Programme`, `DependencyEdge`, `DependencyGraph` types with topological sort, cycle detection, upstream/downstream queries (pure functions, zero I/O).
- `internal/programme` package — `Store` with `List`, `Read`, `Create`, `Update`, `Remove`, `AddProject`, `AddDep` operations, persisted as JSON files in `<data-dir>/programmes/`.
- Shell completions for `programmes` subcommand in bash, zsh, and fish.
- `ProgrammesDir()` method on `Config` for programme storage path.

## [0.19.0] - 2026-03-30

### Added
- **Project management MCP server** (`project-mgmt-mcp`) — runs inside each container, providing 4 tools via MCP stdio: `report_progress` (set completion %), `set_vision`, `set_version`, `get_progress`. Claude Code can use these tools to report project status back to Daedalus in real time.
- `internal/progress` package — read/write operations for `.daedalus/progress.json` files with partial-update semantics.
- `.daedalus/` directory mounted into containers for progress data exchange between agent and host.
- Dashboard endpoint now reads `.daedalus/progress.json` from the project directory, preferring real-time MCP-reported data over registry data.

### Changed
- `BuildExtraArgs` now mounts the project's `.daedalus/` directory into containers at `/workspace/.daedalus`.
- `build.sh` now builds three binaries: `daedalus`, `skill-catalog-mcp`, and `project-mgmt-mcp`.
- `claude.json` registers the `project-mgmt` MCP server alongside `skill-catalog`.
- `entrypoint.sh` ensures `/workspace/.daedalus/` directory exists on container startup.
- `Dockerfile` copies `project-mgmt-mcp` binary into the image.

## [0.18.0] - 2026-03-30

### Added
- **Project management dashboard** — click any project name in the Web UI to see a detail panel with progress bar, version, total session time, session count, and vision statement.
- `GET /api/projects/{name}/dashboard` REST endpoint returning full project dashboard data (progress percentage, vision, project version, total session time, session count, running status).
- `UpdateProjectProgress(name, pct, vision, projectVersion)` registry method for updating project progress metadata. Supports partial updates (only non-zero/non-empty values applied) and clamps percentage to 0-100.
- `ProgressPct`, `Vision`, and `ProjectVersion` fields on `ProjectEntry` for storing per-project progress metadata.

### Changed
- Registry schema upgraded from v2 to v3 (automatic migration on first read). New fields default to zero values — no data loss.

## [0.17.0] - 2026-03-29

### Added
- `daedalus runners` CLI subcommand — `list` (shows built-in runners with binary paths), `show <name>` (prints runner profile details).
- Shell completions for `runners` subcommand in bash, zsh, and fish.

### Changed
- Persona CLAUDE.md content is now stored in a companion `<name>.md` file alongside the `<name>.json` config, instead of being embedded in JSON. Easier to edit and version.
- Skill installation target changed from `~/.claude/commands/` to `/workspace/.claude/skills/` — the correct project-scoped location where Claude Code discovers skills.
- `daedalus personas list` now shows only user-defined personas, not built-in runners.
- `daedalus personas show <builtin>` now returns an error instead of printing runner details — use `daedalus runners show` instead.

### Fixed
- `resolvePersonaOverlay` now uses `cfg.Persona` instead of `cfg.Runner` to look up persona configurations. Previously the persona name was never read, so overlays were silently skipped.
- `resolvePersonaOverlay` now sets `cfg.Runner` from the persona's `BaseRunner` when no explicit `--runner` is given, ensuring the correct binary and Docker image are used.
- `--runner` flag now strictly accepts only built-in runner names (`claude`, `copilot`). Previously it also accepted persona names, blurring the runner/persona boundary.
- `--persona` flag now validated at parse time — rejects built-in runner names (use `--runner` instead) and nonexistent persona names.
- `collectDefaultFlags` now saves the `persona` key alongside `runner` for per-project defaults.
- Dev release workflow: replaced `softprops/action-gh-release` with `gh release create` to fix silent release creation failures.

## [0.16.0] - 2026-03-26

### Added
- **Named persona configurations** — users can define custom personas that layer system prompts, tool permissions, and environment variables on top of a built-in runner (Claude, Copilot). Configs stored as JSON in `<data-dir>/personas/`.
- `daedalus personas` CLI subcommand — `list` (shows built-in runners + user-defined personas), `show <name>` (prints full config), `create <name>` (interactive setup), `remove <name>` (deletes config).
- `--persona <name>` flag to select a user-defined persona.
- `--runner <name>` flag to select the runtime binary (`claude` or `copilot`), replacing the overloaded `--agent` flag.
- `core/persona.go` — `PersonaConfig` struct, `PersonaOverlay` struct, `PersonasDir()` method, `ValidatePersonaName()`.
- `core/runner.go` — `RunnerProfile` struct, `LookupRunner()`, `LookupBuiltinRunner()`, `ValidRunnerNames()`, `ResolveRunnerName()`, `IsBuiltinRunner()`, `BuiltinRunnerNames()`.
- `internal/personas` package — `Store` with `List`, `Read`, `Create`, `Update`, `Remove` operations for persona CRUD.
- `OverlayPaths` struct in `core/command.go` for injecting custom CLAUDE.md, settings.json, and environment variables into containers via volume mounts.
- Shell completions for `personas` subcommand and `--runner`/`--persona` flags in bash, zsh, and fish.
- Legacy `--agent` flag accepted as deprecated alias for `--runner`.
- Auto-migration of `<data-dir>/agents/` directory to `<data-dir>/personas/`.

### Changed
- **Terminology split**: "agent" is now two distinct concepts — **runner** (claude/copilot binary) and **persona** (user-defined configuration overlay).
- `BuildAgentArgs()` renamed to `BuildRunnerArgs()` (`BuildClaudeArgs()` kept as deprecated alias).
- `AGENT` environment variable renamed to `RUNNER` in docker-compose.yml, entrypoint.sh, and Dockerfile.
- `Config.Agent` field split into `Config.Runner` and `Config.Persona`.
- `config.json` field `"agent"` renamed to `"runner"` (legacy `"agent"` key still accepted).
- Help text updated with `personas` subcommand and `--runner`/`--persona` documentation.

## [0.15.0] - 2026-03-25

### Added
- Active project filter — Web UI "Active Only" button and TUI `[f]` keybinding toggle the project list to show only running projects. Helps users focus when the project list grows large.
- Web UI filter state persisted in `localStorage` so it survives page reloads.
- TUI title shows "(active only)" indicator when filter is active.
- Contextual empty-state messages: "No running projects." when filtered, "No registered projects." when unfiltered.

## [0.14.0] - 2026-03-25

### Added
- Mobile Select mode — replaces the Copy button with a Select toggle that overlays the terminal buffer as plain selectable HTML text, enabling native mobile text selection via long-press. Tap "Done" to dismiss the overlay and return to the live terminal.

### Changed
- Mobile Copy button removed in favour of Select mode, which gives users fine-grained native text selection instead of copying the entire buffer.

## [0.13.0] - 2026-03-22

### Added
- Mobile-friendly web UI — the dashboard is now usable on phones and tablets.
- Scrollable terminal output with 10 000-line scrollback buffer (touch-scroll works natively in xterm.js v5).
- Multi-line mobile input area below the terminal — textarea with Send button; Enter inserts newlines, Ctrl+Enter or Send button submits to the PTY. xterm.js stdin is disabled on mobile so the on-screen keyboard targets the textarea.
- Card-based project list on mobile — hides Target and Last Used columns, wraps each project as a card with larger touch targets for action buttons.
- JavaScript test suite for mobile terminal input (`internal/web/testdata/terminal_test.html`) — 16 tests covering send, keyboard shortcuts, `disableStdin`, focus prevention, event listener leak, and cleanup.

### Fixed
- Mobile terminal input not working — xterm.js's internal helper textarea was still focusable with `disableStdin: true`, stealing on-screen keyboard focus from the mobile input area. Tapping the terminal opened the keyboard but all typed characters were silently dropped. Fix: disable the xterm helper textarea on mobile and re-enable on resize back to desktop.
- Mobile input area hidden on real phones — `100vh` includes the browser chrome (URL bar, bottom navigation) on mobile browsers, pushing the input area off the visible viewport. Fix: override to `100dvh` (dynamic viewport height) on mobile with `-webkit-fill-available` fallback.

## [0.12.1] - 2026-03-24

### Fixed
- macOS install: portable `sed -i` for BSD/GNU compatibility.
- macOS install: resolve symlink in `ScriptDir` to find runtime files.
- macOS install: handle empty `FORWARD_ARGS` on bash 3.2.

### Added
- Install test for no-flags invocation covering bash 3.2.

## [0.12.0] - 2026-03-22

### Added
- `--container-log` CLI flag — tees all container stdout/stderr to `<data-dir>/<project>/container.log` for post-session debugging. Works in both direct and tmux modes (uses `io.MultiWriter` for direct, `tmux pipe-pane` for tmux sessions). Log path is printed at startup when enabled.
- `--verbose` flag for `install.sh` — enables shell tracing (`set -x`) for debugging installation issues.

### Fixed
- Copilot agent now uses agent-specific Docker image (`copilot-runner:dev`) and Dockerfile stage (`copilot-dev`) instead of always using `claude-runner:dev`.
- Agent-prefixed targets (e.g. `--target copilot-dev`) now auto-detect the agent and normalize the target, so `copilot-dev` becomes agent=`copilot` + target=`dev`. Fixes container launching Claude CLI instead of Copilot CLI when using composite targets.
- Auto-rebuild path (`NeedsRebuild`) now uses `BuildTarget()` instead of raw `Target`, ensuring the correct Dockerfile stage is built for non-claude agents.
- Copilot binary moved from `/home/claude/.local/bin/copilot` to `/usr/local/bin/copilot` — the `${CACHE_DIR}:/home/claude` volume mount was wiping the binary at container start.
- Copilot images now set `ENV AGENT="copilot"` so `docker run` without explicit AGENT env var launches the Copilot CLI instead of Claude.

### Changed
- Split `install.sh` into two scripts: `install.sh` (thin downloader) and `setup.sh` (installer). The downloader resolves the release tag, detects platform, downloads assets to a temp dir, and execs `setup.sh`. The installer handles file copy, config merge, symlink, and uninstall. `setup.sh` is uploaded as a release asset.
- `install.sh` now uses a `__RELEASE_TAG__` placeholder (falls back to `"latest"` when unpatched), replacing the previous `RELEASE_TAG="latest"` default. Pipelines sed-replace the placeholder before uploading.
- Dev and stable release workflows now patch `install.sh` with the correct release tag during build.
- Dev install URL points to release asset (`releases/download/dev/install.sh`) instead of raw source.
- Release workflows now build `skill-catalog-mcp` per-platform alongside `daedalus`. Install script downloads the platform-specific MCP binary.

## [0.11.0] - 2026-03-22

### Added
- **Copilot CLI support** — GitHub Copilot CLI can now be used as an alternative AI agent alongside Claude Code, selectable per-project via `--agent copilot` or `daedalus config <name> --set agent=copilot`.
- `core/agent.go` — `AgentProfile` struct and `LookupAgent()`, `ValidAgentNames()`, `ResolveAgentName()` functions for agent abstraction (pure logic, zero I/O).
- `--agent <name>` CLI flag with validation (accepts `claude` or `copilot`).
- `BuildAgentArgs()` — agent-aware argument builder that uses agent profiles to emit correct flags per agent.
- `Agent` field in `Config`, `AppConfig`, and per-project default flags (`applyDefaultFlags`).
- `AGENT` environment variable exported in `BuildTmuxCommand` and passed via `docker-compose.yml`.
- `copilot-base` and `copilot-dev` Dockerfile stages with Copilot CLI installed via the [gh.io installer](https://gh.io/copilot-install).
- Agent dispatch in `entrypoint.sh` — reads `$AGENT` env var to launch the correct binary (`claude` or `copilot`).
- Shell completions for `--agent` flag in bash, zsh, and fish with `claude copilot` value suggestions.

### Changed
- `BuildClaudeArgs()` is now a deprecated alias for `BuildAgentArgs()` — no breakage for existing callers.
- `cmd/daedalus/main.go`, `internal/tui/tui.go`, and `internal/web/web.go` now use `BuildAgentArgs()`.
- Help text and usage examples updated with `--agent` flag documentation.

## [0.10.0] - 2026-03-21

### Added
- **Skill Catalog** — a shared skill repository on the host filesystem, mounted into every container. Skills are Claude Code slash commands (`.md` files) that can be browsed, installed, created, and shared across projects.
- `skill-catalog-mcp` — an MCP server (using the official `github.com/modelcontextprotocol/go-sdk`) running inside containers, exposing 8 tools: `list_skills`, `read_skill`, `install_skill`, `uninstall_skill`, `create_skill`, `update_skill`, `remove_skill`, `list_installed`.
- `daedalus skills` CLI subcommand for host-side catalog management: `daedalus skills` (list), `daedalus skills add <file>`, `daedalus skills remove <name>`, `daedalus skills show <name>`.
- Starter skills (`commit.md`, `review.md`) seeded via `go:embed` on first run when the catalog directory does not exist.
- `SkillsDir()` method on `Config` for the shared catalog path (`<data-dir>/skills/`).
- Skills volume mount (`<data-dir>/skills:/opt/skills`) automatically added to every container via `BuildExtraArgs`.
- `internal/catalog` package with pure catalog operations and 21 unit tests.
- `skill-catalog-mcp` binary added to Dockerfile, `build.sh`, and `install.sh`.
- MCP server entry in `claude.json` for automatic discovery by Claude Code.
- `~/.claude/commands/` directory creation in `entrypoint.sh` for skill installation target.

### Changed
- Go module minimum version bumped to 1.25.0 (required by `github.com/modelcontextprotocol/go-sdk`).
- `build.sh` now builds both `daedalus` and `skill-catalog-mcp` binaries.

## [0.9.2] - 2026-03-21

### Fixed
- tmux sessions on macOS no longer become unreachable after opening a new terminal window. All tmux commands now use a stable socket path via `TMUX_TMPDIR=/tmp`.

### Added
- `ExecWithEnv` method on `Executor` interface for process replacement with extra environment variables.

## [0.9.1] - 2026-03-20

### Fixed
- Web UI and TUI now apply per-project default flags (display, dind) when starting containers. Previously only the CLI applied registry defaults.
- Display forwarding test no longer depends on host `DISPLAY` environment variable, fixing CI failures in headless environments.

### Changed
- Dev release workflow triggers on pushes to the `development` branch instead of `master`.

## [0.9.0] - 2026-03-20

### Added
- `--display` CLI flag to forward the host X11/Wayland display into Docker containers, enabling GUI application rendering on the host screen
- Per-project `display` default flag stored in `projects.json`, configurable via `daedalus config <name> --set display=true`
- Shell completions and man page documentation for `--display`
- `internal/platform/display.go` — pure `DisplayArgs()` function for resolving X11/Wayland Docker arguments
- X11 forwarding via `/tmp/.X11-unix` socket mount and `DISPLAY` environment variable
- Wayland forwarding via `$XDG_RUNTIME_DIR/$WAYLAND_DISPLAY` socket mount
- Interactive prompt during first project registration to enable display forwarding (default: no)

## [0.8.3] - 2026-03-20

### Added
- Unit tests for `MockExecutor` in `internal/executor/executor_test.go` covering call recording, result lookup, and query helpers

### Fixed
- Updated 13 stale `"0.8.1"` version strings to `"0.8.2"` in `cmd/generate-manpage/main_test.go` test fixtures

### Changed
- Moved `PrintBanner()` from `core/` to `cmd/daedalus/` to restore the zero-I/O invariant in the core package
- Refactored `run()` in `cmd/daedalus/main.go` — extracted `ensureImageBuilt()`, `buildImage()`, and `launchProject()` to reduce the function from ~197 lines to ~60 lines

## [0.8.2] - 2026-03-18

### Added
- Browser tab title reflects the active project name when attached to a terminal session, resets to "Daedalus — Web Dashboard" on return to the project list

## [0.8.1] - 2026-03-16

### Added
- Auto-detect WSL2 and bind web UI to `0.0.0.0` for Windows host accessibility
- Print WSL2 VM IP address at web UI startup for easy browser access

## [0.8.0] - 2026-03-15

### Added
- Standalone `--build` flag — run `daedalus --build` without a project name to rebuild Docker images for all registered projects. Supports `--target` to limit to a specific build target.
- Verbose `--debug --build` output — when both flags are set, prints resolved Dockerfile and docker-compose.yml paths, build target, image name, and all environment variables (sorted) before the build starts.
- File logging — runtime logs are written to a persistent log file for post-mortem debugging. Default location: `<data-dir>/daedalus.log`. Configurable via `log-file` in `config.json`. Logs include timestamps, levels (`INFO`/`DEBUG`/`ERROR`), and key events (startup, subcommands, builds, errors).
- `internal/logging` package — thread-safe file logger with `Init()`, `Close()`, `Info()`, `Debug()`, `Error()` functions.
- Auto-rebuild after install/upgrade — stores a SHA-256 checksum of build-relevant files (Dockerfile, entrypoint.sh, docker-compose.yml, settings.json, claude.json) after each build. On next project start, compares the current checksum to detect changes and triggers an automatic rebuild when runtime files have been updated.
- Curated release changelog — GitHub Releases now display the version-specific section from CHANGELOG.md instead of auto-generated commit notes. Extraction script at `scripts/extract-changelog.sh`.
- Install script test harness — `scripts/test-install.sh` validates install, upgrade, and uninstall flows using mocked downloads and temp directories (34 assertions across 7 test cases).
- `log-file` field in `config.json` and `AppConfig` struct for configurable log file path.
- `core/checksum.go` — pure `ComputeBuildChecksum()` and `BuildFiles()` functions (zero I/O).
- `internal/docker/checksum.go` — I/O functions for reading build files, storing/comparing checksums.

### Changed
- `--build` flag description in help text updated to reflect standalone rebuild capability.
- ARCHITECTURE.md updated with `logging` package in the dependency graph.
- Release workflow (`.github/workflows/release.yml`) now extracts changelog from CHANGELOG.md for the release body.

## [0.7.8] - 2026-03-10

### Fixed
- Dockerfile: fix Claude CLI symlink rewrite — use `readlink` + `sed` to resolve the actual target instead of an unresolved glob pattern.

## [0.7.7] - 2026-03-10

### Fixed
- Dockerfile: fix broken Claude CLI symlink after moving `/home/claude/.local` to `/opt/claude`. The symlink is now re-created to point to the correct `/opt/claude/share/claude/versions/*/claude` path.

## [0.7.6] - 2026-03-10

### Changed
- Renamed `.claude.json` to `claude.json` in the repo. The Dockerfile copies it as `.claude.json` into the image at build time, avoiding dotfile glob issues in releases and installs.

## [0.7.5] - 2026-03-10

### Fixed
- Release workflow glob `release-assets/*` did not match dotfiles — `.claude.json` was missing from GitHub Release assets.

## [0.7.4] - 2026-03-10

### Added
- README: zsh `source ~/.zshrc` note for macOS users after installation.
- README: "Creating a New Target" section with example and guidelines.
- ROADMAP: shell toggle and target switching backlog items.

### Fixed
- Install script and release workflow now include `.claude.json` in runtime files and release assets.

## [0.7.3] - 2026-03-08

### Fixed
- TUI delete confirmation prompt showed twice — once in the status area and once in the help area. Now only displays in the help area.

## [0.7.2] - 2026-03-08

### Added
- TUI `Del` key removes the selected project from the registry with inline Y/n confirmation prompt. Running projects cannot be removed. Help bar now shows `[del] remove`.

## [0.7.1] - 2026-03-08

### Changed
- TUI kill shortcut changed from `Del` key to `x`. Help bar now shows `[x] kill` instead of `[del]ete`.

## [0.7.0] - 2026-03-08

### Added
- TUI returns to the dashboard after tmux detach or session end, instead of exiting to the shell. Normal quit (`q`/`Ctrl-C`) still exits.
- `AttachWait()` method on `Session` — attaches to a tmux session via fork-wait (`Run`) instead of process replacement (`Exec`), allowing the caller to continue after detach.
- GitHub Pages landing page in `/docs`.

## [0.6.0] - 2026-03-08

### Added
- TUI create mode — press `n` to register a new project directly from the TUI with an interactive directory browser. Step 1: enter project name (validated, duplicate-checked). Step 2: browse filesystem with j/k navigation, Enter to descend, Backspace to go up, `s` to select directory, `c` to create a new subdirectory. Target defaults to `dev`. Esc cancels at any step.

## [0.5.7] - 2026-03-08

### Added
- TUI viewport scrolling — projects beyond the terminal height are now reachable via cursor keys. Scrollbar indicator (`█` thumb / `░` track) appears on the right when the list exceeds the viewport.
- Dark-themed scrollbar styling for the web UI project list, matching the Tokyo Night color palette (Webkit and Firefox).
- Version displayed in brackets after the title in both TUI and web UI (`Daedalus [0.5.7]`).

### Changed
- Version is now baked into the binary at compile time via `-ldflags` instead of reading a VERSION file at runtime.

### Fixed
- Release workflow was not injecting version into binaries via `-ldflags`, causing `unknown` to appear in titles.
- Web scrollbar not appearing — `#project-view` was missing flex layout, preventing `.project-list` from having a constrained height to overflow.
- Renaming a project via the web UI could corrupt the cache directory on WSL2/bind-mounted filesystems. Replaced `os.Rename` with copy+remove for directory renames.
- Cache directory copy failed on dangling symlinks (e.g. `.claude-config/debug/latest`). Symlinks are now recreated instead of followed.

## [0.5.2] - 2026-03-08

### Added
- Colored CLI output — errors in red, warnings in yellow, success in green, hints in cyan, section headers in bold. Respects `NO_COLOR` environment variable convention.
- `--no-color` flag to disable colored output.
- `daedalus config <name>` subcommand to view per-project configuration (directory, target, sessions, default flags).
- `daedalus config <name> --set key=value` and `--unset key` to modify per-project default flags.
- `UpdateDefaultFlags` registry method — single read-modify-write to merge set/unset changes.
- `daedalus completion <bash|zsh|fish>` subcommand to print shell completion scripts. Covers all subcommands, flags, and dynamic project name completion.
- Input validation for `--port` (must be 1-65535) and `--host` (must be non-empty) at parse time (#21).
- Actionable hint messages on 6 key errors: missing credentials, missing project directory, already running, image build failure, project not found, too many arguments.
- Configurable data directory via `DAEDALUS_DATA_DIR` environment variable. Allows storing registry and per-project caches on a different drive or following XDG conventions. Default remains `.cache` next to the binary (backward compatible).
- `RegistryPath()` method on `Config` to eliminate duplicated registry path construction.
- `install.sh` deployment script — builds the binary, copies runtime files to a configurable `--prefix` directory (default: `~/.local/share/daedalus`), and creates a PATH symlink. Validates Docker as a prerequisite.
- Application configuration file (`config.json`) — optional JSON config file next to the binary for persistent settings. Supports `data-dir`, `debug`, `no-tmux`, and `image-prefix`. Precedence: env vars > config file > defaults.
- `--uninstall` flag for `install.sh` — removes binary, runtime files, and symlink. Prompts before deleting project data in `.cache/`.
- Documentation for MCP server configuration and container restrictions.
- Documentation for sharing skills and instructions across projects.
- End-to-end integration test suite — 9 test functions covering full project lifecycle, config precedence, registry lifecycle, Docker command construction, Web API, headless mode detection, and shell completions.
- GitHub Actions CI workflow — runs `go vet` and `go test -race` on push/PR to master.
- GitHub Actions release workflow — cross-compiles binaries for Linux and macOS (amd64/arm64) on version tags, creates GitHub Release with all assets.
- Man page generator (`cmd/generate-manpage/`) — produces `daedalus.1` roff man page with all commands, flags, environment variables, configuration, examples, and exit codes.
- Pre-built `daedalus.1` man page.
- `NewWebServerForTest()` constructor and exported handler wrappers (`HandleListProjects`, `HandleStartProject`, `HandleStopProject`) for cross-package integration testing.
- Startup banner — `PrintBanner()` displays the Techdelight logo, version, and build timestamp when launching `daedalus web` or `daedalus tui`.
- Upgrade-aware installer — `install.sh` now detects an existing installation via the `version` field in `config.json`. On upgrade, it preserves user settings (data-dir, debug, no-tmux, image-prefix), replaces the binary and runtime files, and updates the version.
- `version` field in `config.json` and `AppConfig` struct.
- `daedalus rename <old-name> <new-name>` CLI subcommand to rename registered projects.
- `POST /api/projects/{name}/rename` web API endpoint with JSON body `{"newName": "..."}`.
- Rename button in the web dashboard for stopped projects (uses `prompt()` for new name).
- F2 key in TUI to rename the selected project via inline text input (Enter to confirm, Esc to cancel).
- `ValidateProjectName()` pure validation function — names must start with alphanumeric and contain only `[a-zA-Z0-9._-]`.
- `RenameProject()` registry method — atomic rename of registry key with best-effort cache directory rename.
- Shell completions for `rename` subcommand (bash, zsh, fish).
- Man page entry for `rename` command with synopsis and example.

### Changed
- `CacheDir()` now derives from `DataDir` instead of `ScriptDir`.
- **Rebrand**: Renamed project from `agentenv` to `Daedalus` across all source files, Go module path, binary name, shell completions, documentation, and build scripts.
- Copyright holder changed from "David Stibbe" to "Techdelight BV" in all source file headers.
- Apache-2.0 license added (`LICENSE` file).
- Documentation restructured: `README.md` is now end-user focused, `CONTRIBUTING.md` expanded with coding standards and Definition of Done, `ARCHITECTURE.md` created with module breakdown, component diagram, and data flow.
- `install.sh` now downloads pre-built binaries from the latest GitHub Release instead of downloading source and building via Docker. Docker is no longer required for installation (still required at runtime).
- TUI kill shortcut changed from `K` (shift+k) to the `Del` (Delete) key.

### Fixed
- `install.sh` `sed -i` command now portable across Linux and macOS (replaced with `sed` + temp file).
- TUI kill (`K`) and web UI stop did not stop containers — `executor.Run` attached stdout/stderr/stdin to the subprocess, conflicting with bubbletea's alt-screen terminal. Replaced with `executor.Output` which captures output without terminal interference (#27).
- Registry migration did not reject future schema versions — a registry file with a version newer than the binary could be silently accepted. Now returns an error with both the file version and the supported version.
- Docker compose command and environment exports no longer visible in the terminal when starting a container via TUI or Web UI. The tmux command now clears the screen before execution.
- `docker image inspect` output no longer leaks to the terminal when starting a container from the web interface. `ImageExists()` now uses `Output()` instead of `Run()`.

### Removed
- `--data-dir` CLI flag — data directory is now configured via `config.json` or the `DAEDALUS_DATA_DIR` environment variable.
- Host credential linking — `ClaudeConfigDir`, `CredSourcePath()`, and `CRED_PATH` env var removed from config, command builder, and compose environment. Users now run `claude /login` inside the container; credentials persist in the per-project cache directory.
- Credential prerequisite check from `install.sh` — Claude credentials are no longer required on the host.
- `/opt/claude/credentials/` directory from Dockerfile and credential symlink logic from `entrypoint.sh`.
- `.claude.json` from `install.sh` runtime files — it is baked into the Docker image and not included in release assets.
- `start.sh` from release workflow assets — it is a development helper not needed by end users.

## [0.5.0] - 2026-03-02

### Added
- Session history tracking per project — `StartSession`/`EndSession` record session IDs, timestamps, durations, and optional resume IDs. Capped at 50 entries per project. Sessions column in `list`, TUI, and web API.
- Per-project default flags — flags like `--dind`, `--debug`, `--no-tmux` are captured at first registration and automatically applied on subsequent runs. CLI flags always override. New `SetDefaultFlags` registry method.
- `daedalus remove <name> [name...]` subcommand to explicitly delete named projects from the registry with interactive confirmation (or `--force` for headless mode).
- Batch `RemoveProjects` registry method — single read-modify-write cycle for removing multiple projects, replacing N+1 pattern in `pruneProjects` (#24).
- Registry schema versioning and migration framework — `CurrentRegistryVersion` constant, `migrate()` with per-version upgrade functions, auto-migration on read with immediate persistence.
- `RemoveProject` now cleans up the per-project cache directory after registry deletion (#23).

### Changed
- `ComposeRun` now delegates to `ComposeRunCommand` internally, eliminating duplicated arg construction (#20).
- `pruneProjects` uses batch `RemoveProjects` for atomic removal instead of per-item loop.
- New registries are created at schema version 2 (up from 1).

## [0.4.1] - 2026-03-02

### Fixed
- **Critical**: `extraArgs` (e.g. `-v` for DinD socket mount) placed after service name in `ComposeRun`/`ComposeRunCommand` — flags were interpreted as container command instead of `docker compose run` flags (#15).
- `claude` user not in `docker` group — socket permission denied inside container (#16).
- `docker.io` installed in `utils` stage, bloating `godot` image — moved to `dev` stage only (#17).
- Headless `prune` auto-deleted without confirmation — now requires `--force` flag (#19, #25).

### Added
- Runtime stderr warning when `--dind` is used, documenting that the host Docker socket is mounted (#18).
- `--force` flag for non-interactive `prune` deletion.
- Unit tests for `pruneProjects` (no-stale, with-stale, headless-without-force) (#22).

## [0.4.0] - 2026-03-02

### Added
- `--dind` flag to mount the host Docker socket into the container for Docker-in-Docker workflows. Docker CLI installed in the `utils` stage. Security warning: grants host Docker access.
- `daedalus prune` subcommand to remove registry entries whose project directories no longer exist on disk. Interactive confirmation in TTY mode, auto-remove in headless mode.
- `--debug` flag to opt-in to Claude Code debug mode (previously hardcoded as always-on).
- `RemoveProject` method on the registry for programmatic project deletion.
- Container resource limits: `mem_limit: 4g`, `cpus: 2.0`, `pids_limit: 512`.

### Fixed
- `--debug` flag no longer hardcoded — now opt-in via CLI flag (#7).
- Volume paths in `docker-compose.yml` are now quoted to handle paths with spaces (#10).
- Dead `ln -sfr` symlink removed from Dockerfile — binary already on PATH via `ENV PATH` (#13).
- Redundant `mkdir -p` in `entrypoint.sh` consolidated to a single unconditional call (#14).

### Changed
- Dockerfile install script now has a supply-chain warning comment documenting the unverified curl-pipe-sh pattern (#12).

## [0.3.0] - 2026-03-02

### Added
- Web UI dashboard (`daedalus web`) — browser-based project management with REST API and embedded xterm.js terminal connected to tmux sessions via WebSocket + PTY relay. Static assets embedded in binary via `go:embed`. Binds to `localhost:3000` by default with `--port` and `--host` flags.
- Interactive TUI dashboard (`daedalus tui`) for managing projects — start, attach, kill, and monitor containers with keyboard-driven navigation (bubbletea + lipgloss)
- TUI auto-attaches to the tmux session after starting a project, matching the non-TUI flow
- `entrypoint.sh` wrapper that seeds config defaults on first run and symlinks credentials
- Project registry (`.cache/projects.json`) tracking directory, target, and timestamps per project
- Auto-migration of existing `.cache/*/` directories into registry on first run
- Project existence check — interactive prompt for unregistered projects, auto-register in headless mode
- Container naming (`claude-run-<project-name>`) for easy identification in `docker ps`
- Duplicate container detection — prevents launching a project that's already running
- `jq` dependency check at script startup with install hint
- `ROADMAP.md` with language evaluation (bash vs Go vs Zig vs C++)
- Single multi-stage `Dockerfile` with four targets: `base`, `utils`, `dev`, `godot`
- Godot 4.x stage for headless game CI, exports, and tests
- `--target` flag in `run.sh` to select build stage (default: `dev`)
- `--resume` flag in `run.sh` to resume a previous Claude Code session
- Home directory persistence via `.cache/<project-name>/` bind-mounted as `/home/claude`
- `update-login.sh` script to refresh credentials for running agents
- MCP server support via Claude Code settings
- `--debug` flag passed to Claude Code by default

### Fixed
- TUI tmux attach no longer garbles the terminal. Previously, `syscall.Exec` was called from within bubbletea, skipping alt-screen and raw-mode cleanup. Now the TUI quits cleanly first, then attaches to tmux after the terminal is restored.
- Session resume (`--resume`) now works across container runs. `CLAUDE_CONFIG_DIR` moved from `/opt/claude/config` (ephemeral) to `/home/claude/.claude-config` (persistent volume), so session transcripts survive container removal.

### Changed
- `run.sh` positional arguments (`project-name`, `project-dir`) are now optional. Zero args defaults to current directory name and path; one arg defaults project-dir to current directory.
- Makefile Go Docker image bumped from `golang:1.21-bookworm` to `golang:1.24-bookworm` to match `go.mod`
- Merged `Dockerfile.base` and `Dockerfile.dev` into a single multi-stage `Dockerfile`
- Credentials now bind-mounted read-only at runtime instead of only baked into the image
- `docker-compose.yml` uses `TARGET` env var for image tag selection
- Container auto-removed on exit since home directory is now persisted
- Container user UID matched to caller's UID via `CLAUDE_UID` build arg
- Claude CLI installed to `/opt/claude` with defaults at `/opt/claude/defaults/`; runtime config at `/home/claude/.claude-config` (persistent volume)
- Credentials mount moved from `/opt/claude/config/.credentials.json` to `/opt/claude/credentials/.credentials.json`
- `jq` added to base Dockerfile stage

### Removed
- Separate `Dockerfile.base` and `Dockerfile.dev` (replaced by single multi-stage `Dockerfile`)
- Named Docker volume for Claude config (replaced by host bind mount)

## [0.1.0] - 2026-02-15

### Added
- Dockerfile with Node 22, Python 3, git, build tools, and Claude Code CLI
- `entrypoint.sh` launching Claude Code with `--dangerously-skip-permissions`
- `docker-compose.yml` with security hardening (read-only FS, dropped capabilities, no-new-privileges)
- `.claude/settings.json` pre-approving all Claude Code tools
- `.env.example` for API key configuration
- `CLAUDE.md` with project guidance for Claude Code
