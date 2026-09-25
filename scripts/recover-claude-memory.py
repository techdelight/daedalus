#!/usr/bin/env python3
# Copyright (C) 2026 Techdelight BV
"""Rebuild a lost Claude memory/ directory from the session transcripts.

The memory files live at

    <project home>/.claude-config/projects/-workspace/memory/

and the session transcripts (*.jsonl) live in the directory ABOVE it. Every
memory file was created by a Write tool call and amended by Edit tool calls,
and those calls are recorded verbatim in the transcripts. So as long as the
transcripts survived, the memory content did too -- it just has to be replayed.

Usage:

    recover-claude-memory.py <projects/-workspace dir> [-o OUTDIR] [--apply]

Without --apply it writes the reconstruction to OUTDIR (default:
./memory-recovered) and touches nothing else. With --apply it writes straight
into <dir>/memory/, refusing to clobber any file already there.
"""

import argparse
import json
import os
import pathlib
import sys


def entries(jsonl_paths):
    """Yield (timestamp, tool_name, input_dict) for every Write/Edit aimed at memory/."""
    for path in jsonl_paths:
        with open(path, errors="replace") as fh:
            for seq, line in enumerate(fh):
                try:
                    obj = json.loads(line)
                except ValueError:
                    continue
                message = obj.get("message") or {}
                content = message.get("content")
                if not isinstance(content, list):
                    continue
                stamp = (obj.get("timestamp") or "", str(path), seq)
                for block in content:
                    if not isinstance(block, dict) or block.get("type") != "tool_use":
                        continue
                    name = block.get("name")
                    if name not in ("Write", "Edit", "MultiEdit"):
                        continue
                    params = block.get("input") or {}
                    target = params.get("file_path", "")
                    if "/memory/" not in target:
                        continue
                    yield stamp, name, params


def replay(events):
    """Fold the events into {basename: final content}, in timestamp order."""
    files = {}
    skipped = []
    for stamp, name, params in sorted(events, key=lambda e: e[0]):
        base = os.path.basename(params["file_path"])
        if name == "Write":
            files[base] = params.get("content", "")
            continue
        edits = params.get("edits") if name == "MultiEdit" else [params]
        for edit in edits or []:
            old, new = edit.get("old_string", ""), edit.get("new_string", "")
            body = files.get(base)
            if body is None:
                # An Edit with no preceding Write: the file predates the oldest
                # surviving transcript. The new_string is still real content, so
                # keep it as a fragment rather than dropping it on the floor.
                files[base] = new
                skipped.append((base, "edit with no base text -- fragment only"))
            elif old in body:
                count = 1 if not edit.get("replace_all") else body.count(old)
                files[base] = body.replace(old, new, count)
            else:
                skipped.append((base, f"edit did not apply at {stamp[0]}"))
    return files, skipped


def rebuild_index(files):
    """Rebuild MEMORY.md from the recovered files' own frontmatter.

    MEMORY.md cannot be replayed: it is only ever Edited, never Written, so
    there is no base text in the transcripts to apply those edits to. It is
    also injected into each session through the system prompt, which is not
    recorded. But it is only an index -- one line per memory -- and every line
    it needs is sitting in the frontmatter of the files we just recovered.
    """
    lines = []
    for base, body in sorted(files.items()):
        if base == "MEMORY.md":
            continue
        desc = ""
        for line in body.splitlines()[:12]:
            if line.startswith("description:"):
                desc = line.split(":", 1)[1].strip().strip('"')
                break
        title = base[:-3].replace("-", " ").replace("_", " ").capitalize()
        lines.append(f"- [{title}]({base})" + (f" — {desc}" if desc else ""))
    return "\n".join(lines) + "\n"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("dir", help="the projects/-workspace directory holding the *.jsonl transcripts")
    ap.add_argument("-o", "--out", default="memory-recovered", help="where to write the reconstruction")
    ap.add_argument("--apply", action="store_true",
                    help="write into <dir>/memory/ instead, skipping files that already exist")
    args = ap.parse_args()

    root = pathlib.Path(args.dir)
    transcripts = sorted(root.glob("*.jsonl"))
    if not transcripts:
        sys.exit(f"no *.jsonl transcripts under {root} -- nothing to recover from")

    files, skipped = replay(entries(transcripts))
    if not files:
        sys.exit(f"found no memory writes in {len(transcripts)} transcript(s)")

    files["MEMORY.md"] = rebuild_index(files)

    out = (root / "memory") if args.apply else pathlib.Path(args.out)
    out.mkdir(parents=True, exist_ok=True)

    written = kept = 0
    for base, body in sorted(files.items()):
        dest = out / base
        if args.apply and dest.exists():
            print(f"  keep    {base} (already present)")
            kept += 1
            continue
        dest.write_text(body)
        print(f"  write   {base}  ({len(body)} bytes)")
        written += 1

    print(f"\n{written} file(s) written to {out}, {kept} left alone, "
          f"from {len(transcripts)} transcript(s).")
    if skipped:
        print("\nNot everything replayed cleanly -- these need an eyeball:")
        for base, why in skipped:
            print(f"  {base}: {why}")


if __name__ == "__main__":
    main()
