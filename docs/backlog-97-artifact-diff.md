# Backlog #97 — artifact diff in the Ledger

**Status: DESIGN. Implementation-ready.**

This design closes the remaining review-context gap in the Ledger: the reviewer
agent receives the artifact diff, while the human who decides whether to approve
the change cannot inspect that diff without leaving Daedalus.

The change is read-only. It does not move a Task, create an event, require a
network connection, or introduce credentials. The source of truth is the
artifact already stored in the host repository: its `BaseSHA`, `HeadSHA`, and
owning Job and Task.

---

## Outcome

A human reviewing a Task can:

1. open a **CHANGE** tab in the Ledger;
2. scan the files changed by an artifact;
3. identify acceptance-policy files and files named by blocking findings;
4. expand only the files worth reading;
5. jump from a review finding to the corresponding diff line; and
6. switch between attempts without losing their place.

The CLI provides the same evidence and an uncapped escape hatch for patches too
large for the Ledger.

---

## 1. Plane API

Add one read-only resource:

```text
GET /tasks/{taskID}/artifacts/{artifactID}/diff
GET /tasks/{taskID}/artifacts/{artifactID}/diff?path={path}
GET /tasks/{taskID}/artifacts/{artifactID}/diff?format=patch[&path={path}]
```

The forms have distinct purposes:

- no query parameters returns the structured file summary;
- `path` returns a structured, capped patch for one changed file; and
- `format=patch` streams a raw unified patch, optionally restricted to one file,
  for the CLI.

`artifactID` is mandatory on the wire. The server never silently selects the
latest artifact. The Ledger already knows which artifact its selected pill
represents, and the CLI resolves its default through `TaskStatus` before making
the diff request.

The plane resolves the repository, base SHA, and head SHA from its own records.
None of them is accepted from the client.

Diffs do **not** become part of `StatusView`. `TaskStatus` is polled every fifteen
seconds by the Ledger; putting Git work on that path would repeatedly compute a
diff when nobody is reading the CHANGE tab.

### Summary response

```go
type DiffSummary struct {
	TaskID     string         `json:"taskId"`
	JobID      string         `json:"jobId"`
	ArtifactID string         `json:"artifactId"`
	BaseSHA    string         `json:"baseSha"`
	HeadSHA    string         `json:"headSha"`
	Files      []DiffFileView `json:"files"`
}

type DiffFileView struct {
	Path       string `json:"path"`
	Status     string `json:"status"` // A, M, D, T, U
	Additions  int    `json:"additions"`
	Deletions  int    `json:"deletions"`
	Binary     bool   `json:"binary,omitempty"`
	Acceptance bool   `json:"acceptance,omitempty"`
	Blocking   int    `json:"blocking,omitempty"`
}
```

`Blocking` counts findings from the latest recorded review of the selected
artifact. It does not add together repeated readings or findings against older
attempts.

### File response

```go
type DiffFilePatch struct {
	ArtifactID   string       `json:"artifactId"`
	File         DiffFileView `json:"file"`
	Hunks        []DiffHunk   `json:"hunks"`
	Truncated    bool         `json:"truncated"`
	TotalLines   int          `json:"totalLines"`
	OmittedLines int          `json:"omittedLines,omitempty"`
}

type DiffHunk struct {
	Header string     `json:"header"`
	Lines  []DiffLine `json:"lines"`
}

type DiffLine struct {
	Kind    string `json:"kind"` // add, delete, context
	Text    string `json:"text"`
	OldLine int    `json:"oldLine,omitempty"`
	NewLine int    `json:"newLine,omitempty"`
}
```

The plane parses unified hunks. The browser does not parse patch syntax. That
keeps the handwritten JavaScript small and gives finding links stable new-file
line numbers to target.

### Errors

- `404` — the Task or Artifact does not exist;
- `404` — the Artifact exists but does not belong to the named Task;
- `400` — `path` is not an exact path in this artifact's changed-file summary;
- `409` — the Artifact has no usable base or head commit; and
- `500` — the stored commits or repository cannot be read.

An empty diff is a successful response with `files: []`, not an error.

---

## 2. Artifact selection

The Ledger obtains Jobs and Artifacts from the existing `TaskStatus` response.

- Show CHANGE only when at least one usable artifact exists.
- Show artifact pills in chronological order.
- Label the ordinary case with the Job ID, for example `J-31`.
- Address and cache the selection by Artifact ID, not by Job ID.
- If a Job ever contains multiple artifacts, label them `J-31/A-42`.
- Select the newest usable artifact when CHANGE is first opened.
- Do not switch automatically if polling discovers a newer attempt while the
  operator is reading an older one.
- A finding belonging to another artifact switches to that artifact before
  opening its file.

An artifact is usable when it has non-empty, resolvable `BaseSHA` and `HeadSHA`
values. A broken artifact is not silently substituted with another attempt.

---

## 3. Git implementation

`--numstat` and `--name-status` are competing Git output formats and must not be
combined in one invocation. Build the summary from two NUL-delimited reads:

```text
git diff --no-renames --name-status -z <base>..<head>
git diff --no-renames --numstat -z <base>..<head>
```

Merge their records by exact path and preserve the ordering produced by
`--name-status`. NUL-delimited output prevents spaces, tabs, and newlines in a
filename from corrupting the parser.

Read one file with:

```text
git diff \
  --no-renames \
  --no-color \
  --no-ext-diff \
  --no-textconv \
  --unified=3 \
  <base>..<head> \
  -- <exact-path>
```

The implementation must:

- obtain both SHAs from the stored Artifact;
- confirm that the Artifact belongs to the named Task;
- accept `path` only when it exactly matches a path in the summary;
- always place `--` before a path argument;
- treat `-`/`-` numstat values as a binary file;
- never enable external diff drivers or text conversion; and
- render filenames and patch contents with DOM `textContent`, never HTML.

Renames deliberately appear as delete plus add. This matches the existing
acceptance-file restoration and reviewer behavior.

---

## 4. Acceptance and review annotations

Read the acceptance policy at `Artifact.BaseSHA`. Mark every changed path that
matches the frozen policy's `AcceptanceGlobs` with `⚠ acceptance`.

For findings:

- only reviews of the selected Artifact contribute row annotations;
- only the latest review of that Artifact contributes `▶ n blocking` and the
  default-open set;
- historical findings remain visible under RECORD; and
- clicking any historical finding uses the Review's `ArtifactID`, then opens the
  corresponding file in CHANGE.

The reviewer contract must be made explicit as part of #97:

> `line` is the 1-based line number in the artifact's head version. Use zero for
> a whole-file finding or when no head-side line exists.

If a finding's path or line cannot be resolved, CHANGE opens the file at its
first hunk and states that the exact reported line was unavailable. A model's
coordinate is evidence, not a trusted pointer.

---

## 5. Ledger behavior

Task tabs become:

```text
ENTRY · TERMS · CHANGE · RECORD
```

CHANGE contains:

1. the attempt/artifact pills;
2. the selected base and head SHAs;
3. one summary row per changed file; and
4. a `<details>` disclosure containing each fetched patch.

A summary row shows status, path, additions, deletions, binary state, acceptance
marker, and blocking-finding count. The layout remains unified at every viewport
width; each hunk scrolls horizontally inside itself so a long line never widens
the page.

Disclosure state uses the existing `remember()` and `disclosureOpen()` machinery,
with a composite key:

```text
diff:{artifactID}:{path}
```

A path alone is insufficient because two attempts can change the same file
differently.

Files named by blocking findings in the latest review open by default exactly
once. After that, the operator's own toggle wins across every repaint.

Successful summaries and file patches are cached in browser memory by immutable
Artifact ID and path. Failures are not cached. Every asynchronous response
captures its Task, Artifact, and path; if the operator has moved elsewhere before
it arrives, it is discarded rather than repainting the new selection.

Scroll position is stored by Task, tab, and Artifact. Switching from a long
RECORD to CHANGE must not inherit RECORD's scroll offset, and returning to an
artifact should restore the reader's place.

### Finding links

A finding with a file becomes a control. Activating it:

1. selects the Review's Artifact;
2. switches to CHANGE;
3. opens that file's disclosure;
4. fetches the patch if it is not cached; and
5. scrolls to the line whose `newLine` matches the finding.

The control remains keyboard-operable through its native button semantics. #97
does not introduce board-wide shortcut keys or line comments.

---

## 6. Truncation and binary files

The structured Ledger response is capped per file at:

- 500 rendered patch lines; or
- 512 KiB of retained patch text,

whichever is reached first.

The plane continues consuming the Git stream with bounded memory so it can state
`TotalLines` and `OmittedLines` without retaining the full patch. Truncation is
always explicit:

```text
Truncated — 3,200 more lines. Read the full patch with:
daedalus task diff T-28 --artifact A-42 --path internal/control/review.go
```

Binary files display `Binary file changed` and do not render an empty patch
panel.

The raw CLI response streams end to end and is not held in memory by the daemon,
web server, or client.

---

## 7. CLI

Add:

```text
daedalus task diff <task-id> [--artifact <artifact-id>] [--stat] [--path <path>]
```

Behavior:

- the default is the complete unified patch for the newest usable artifact;
- `--artifact` selects an exact Artifact;
- `--stat` prints the same file summary as CHANGE;
- `--path` restricts the patch to one exact changed path;
- stdout contains only the patch when raw output is requested;
- the selected Job, Artifact, base, and head are identified on stderr; and
- an invalid Task, Artifact relationship, or path returns a non-zero error rather
  than an empty diff.

Output has no colour when redirected, so this remains valid:

```text
daedalus task diff T-28 > review.patch
```

---

## 8. Access boundary

The Ledger and CLI reach this resource through the human control socket.

Do not automatically expose source patches through `control-agent.sock`.
`TaskStatus` currently gives an agent metadata; a diff returns repository source
and therefore widens read authority. The caller-scoped implementation returns
`403 Forbidden` to an agent caller unless source-reading authority is designed
and granted separately later.

No operation-table entry is added. Diff is a read, not a state-changing
operation, and it has no state-dependent legality beyond the existence of a
usable artifact.

---

## 9. Verification

### Plane and Git tests

- added, modified, deleted, type-changed, binary, and empty diffs;
- filenames containing spaces, tabs, newlines, and a leading dash;
- independent `--name-status` and `--numstat` parsing and merging;
- Artifact ownership by Task;
- rejection of caller-supplied or unresolved commit identities;
- exact-path validation and `--` option termination;
- external diff drivers and text conversion are not executed;
- acceptance markers use the Artifact's frozen policy;
- blocking markers use the latest Review for that Artifact;
- old- and new-side line numbers are parsed correctly;
- explicit truncation with accurate omitted-line counts; and
- binary files return a stated binary result.

### Daemon and client tests

- summary, structured-file, and streamed-patch forms;
- error status preservation through the Unix-socket client;
- raw patches are streamed rather than buffered; and
- agent-socket requests receive `403 Forbidden`.

### CLI tests

- newest-artifact default;
- explicit `--artifact`;
- `--stat` and `--path`;
- patch-only stdout suitable for redirection; and
- non-zero errors for an unknown Task, wrong Artifact, and unchanged path.

### Ledger browser tests

- CHANGE is absent without an Artifact;
- the newest Artifact is initially selected;
- selection, disclosures, and scroll survive polling;
- a newly appearing attempt does not displace the current selection;
- stale fetch responses cannot repaint another Artifact;
- finding links select the correct Artifact, open the file, and reach the
  reported head line;
- an invalid finding coordinate degrades visibly;
- binary and truncated files are stated rather than blank; and
- a narrow viewport never acquires horizontal page scrolling.

---

## Definition of done

#97 is complete when an operator can move from a blocking finding to the relevant
code on a phone, remain in place through a Ledger refresh, inspect every attempt,
and obtain the complete raw patch from the CLI — without a network call or any
mutation of control-plane state.
