"""Drive the Ledger in a real browser.

Run by internal/web/ledger_browser_test.go, which serves the real page over the
real routes against a scripted control plane and passes its address in
LEDGER_URL.

    RUN_PLAYWRIGHT_LEDGER=1 PLAYWRIGHT_PYTHON=/path/to/python \
      go test ./internal/web -run TestLedgerInABrowser -count=1 -v

PLAYWRIGHT_PYTHON must be an interpreter with `playwright` installed and a
browser downloaded (`playwright install chromium`). PLAYWRIGHT_SCRIPT overrides
this file.

WHY PYTHON, next to e2e/web-ui.spec.ts which is TypeScript: this one is meant to
be RUNNABLE. The TypeScript suite needs node and the Playwright test runner, and
went three sprints asserting against routes that had been deleted because
nothing in the development environment could execute it. A checked-in test
nobody can run is a comment.

Every check prints PASS or FAIL and the script exits non-zero if any failed, so
the Go test's pass/fail is the whole suite's.
"""

import os
import sys

from playwright.sync_api import sync_playwright

URL = os.environ["LEDGER_URL"]

failures = []
notes = []


def check(name, ok, detail=""):
    if ok:
        print(f"  PASS  {name}")
    else:
        print(f"  FAIL  {name}{(' — ' + detail) if detail else ''}")
        failures.append(name)


def note(text):
    notes.append(text)
    print(f"  ....  {text}")


def open_ledger(page, path="/ledger"):
    page.goto(URL + path)
    page.wait_for_selector("#control-view.active", timeout=10000)
    page.wait_for_selector(".ledger-row", timeout=10000)


def main():
    with sync_playwright() as pw:
        browser = pw.chromium.launch()
        page = browser.new_page(viewport={"width": 1400, "height": 900})

        errors = []          # uncaught exceptions and console errors
        bad_responses = []   # every non-2xx the page asked for
        page.on("pageerror", lambda e: errors.append("pageerror: " + str(e)))
        page.on("console",
                lambda m: errors.append("console: " + m.text)
                if m.type == "error" and "status of 404" not in m.text else None)
        page.on("response",
                lambda r: bad_responses.append(f"{r.status} {r.url}") if r.status >= 400 else None)

        # --- item 2: the address --------------------------------------------
        print("\n[2] the Ledger has its own URL")
        open_ledger(page, "/ledger")
        check("GET /ledger opens the Ledger, not the project list",
              page.is_visible("#control-view.active"))
        check("the board rendered rows", page.locator(".ledger-row").count() >= 3,
              f"{page.locator('.ledger-row').count()} rows")

        page.goto(URL + "/ledger/T-1")
        page.wait_for_selector("#control-view.active", timeout=10000)
        page.wait_for_function("document.getElementById('ledger-desc-id').textContent === 'T-1'",
                               timeout=10000)
        check("GET /ledger/T-1 opens that entry",
              page.inner_text("#ledger-desc-id").strip() == "T-1",
              page.inner_text("#ledger-desc-id"))
        check("the deep-linked row is pinned",
              page.locator('.ledger-row[data-entry-id="T-1"].is-pinned').count() == 1)
        check("the title names the entry", "T-1" in page.title(), page.title())

        # clicking another row moves the address
        page.click('.ledger-row[data-entry-id="T-2"]')
        page.wait_for_timeout(300)
        check("clicking a row changes the URL",
              page.url.endswith("/ledger/T-2"), page.url)

        page.go_back()
        page.wait_for_timeout(500)
        check("Back returns to the previous entry",
              page.url.endswith("/ledger/T-1"), page.url)

        # hovering must NOT push history
        before = page.url
        page.hover('.ledger-row[data-entry-id="T-3"]')
        page.wait_for_timeout(400)
        check("hovering does not change the URL", page.url == before, page.url)

        # an id that is not there
        page.goto(URL + "/ledger/T-404")
        page.wait_for_selector("#control-view.active", timeout=10000)
        try:
            page.wait_for_function(
                "document.getElementById('ledger-message').textContent.includes('not in this ledger')",
                timeout=45000)
            check("an unknown id says so", True)
        except Exception:
            check("an unknown id says so", False,
                  "message was: " + page.inner_text("#ledger-message"))

        # --- item 4: deliverables -------------------------------------------
        print("\n[4] deliverables on the entry")
        open_ledger(page, "/ledger/T-1")
        page.wait_for_function("document.getElementById('ledger-desc-id').textContent === 'T-1'",
                               timeout=10000)
        page.wait_for_selector(".ledger-deliverable", timeout=10000)
        items = page.locator(".ledger-deliverable").all_inner_texts()
        check("the three deliverables render", len(items) == 3, str(items))
        check("their text is the task's",
              any("nextCursor" in i for i in items), str(items))

        page.click('.ledger-row[data-entry-id="T-2"]')
        page.wait_for_function("document.getElementById('ledger-desc-id').textContent === 'T-2'",
                               timeout=10000)
        page.wait_for_timeout(400)
        body = page.inner_text("#ledger-desc-body")
        check("a task with none says so", "No deliverables" in body, body[:160])

        # the new-entry form
        page.click("text=New entry")
        page.wait_for_selector("#ledger-new.is-open", timeout=5000)
        check("the form has a deliverables field", page.is_visible("#new-deliverables"))
        page.fill("#new-objective", "Add a --since flag to task list")
        page.fill("#new-deliverables", "--since filters by age\n\n--since is in the man page")
        page.click("text=Create")
        page.wait_for_timeout(800)
        msg = page.inner_text("#ledger-message")
        check("creating a task succeeds", "created" in msg.lower(), msg)
        page.click("text=Cancel") if page.is_visible("#ledger-new.is-open") else None

        # --- item 3: the review ---------------------------------------------
        print("\n[3] the review, rendered to be acted on")
        open_ledger(page, "/ledger/T-1")
        page.wait_for_function("document.getElementById('ledger-desc-id').textContent === 'T-1'",
                               timeout=10000)
        page.click("button.ff-tab:has-text('record')")
        page.wait_for_selector(".ledger-finding", timeout=10000)

        findings = page.locator(".ledger-finding")
        check("one element per finding", findings.count() == 3, str(findings.count()))
        first = findings.nth(0)
        check("blocking is first",
              "blocking" in first.get_attribute("class"), first.get_attribute("class"))
        check("the verdict line carries the tally",
              "1 blocking" in page.inner_text("#ledger-desc-body"),
              page.inner_text("#ledger-desc-body")[:200])

        # the why/fix must be hidden until asked for
        why = page.locator(".ledger-finding-why").first
        check("why is hidden until the finding is opened", not why.is_visible())
        first.locator("summary").click()
        page.wait_for_timeout(200)
        check("opening a finding reveals its why", why.is_visible())
        fix = first.locator(".ledger-finding-fix")
        check("and its fix", fix.is_visible() and "256 bytes" in fix.inner_text(),
              fix.inner_text() if fix.count() else "(none)")

        reasoning = page.locator(".ledger-reasoning")
        check("the reasoning is in its own disclosure", reasoning.count() == 1)
        check("and is closed by default",
              not page.locator(".ledger-reasoning .ledger-log-note").first.is_visible())

        # ONE LINE PER FINDING, measured. This is the whole complaint: a review
        # that reads as a wall of text. A summary row that wraps is a wall of
        # text one finding at a time.
        heights = [findings.nth(i).locator("summary").bounding_box()["height"]
                   for i in range(findings.count())]
        check("every closed finding is one line", all(h < 24 for h in heights), str(heights))

        # the anchor is abbreviated, with the full path a click away
        at = findings.nth(1).locator(".ledger-finding-where")
        check("the summary row carries the file name, not the path",
              at.inner_text().strip() == "items.go:140", at.inner_text())
        check("the full path is on the title",
              at.get_attribute("title") == "internal/api/items.go:140",
              str(at.get_attribute("title")))
        findings.nth(1).locator("summary").click()
        page.wait_for_timeout(200)
        check("and is shown in full when opened",
              "internal/api/items.go:140" in findings.nth(1).inner_text(),
              findings.nth(1).inner_text()[:120])

        # A REVIEW THAT COULD NOT BE OBTAINED. Its one finding has no file and no
        # fix, so its reason is the whole content — reported on real work: the
        # row said "no review judgement was produced" and the reason, which
        # names the failure and the log to read, was hidden behind a disclosure
        # with nothing suggesting it was worth opening.
        print("\n[3b] a review that did not happen says why, without being asked")
        open_ledger(page, "/ledger/T-2")
        page.wait_for_function("document.getElementById('ledger-desc-id').textContent === 'T-2'",
                               timeout=10000)
        page.click("button.ff-tab:has-text('record')")
        page.wait_for_selector(".ledger-finding", timeout=10000)
        failed = page.locator(".ledger-finding").first
        check("the harness failure is open without being clicked",
              failed.get_attribute("open") is not None)
        why = failed.locator(".ledger-finding-why")
        check("its reason is visible", why.is_visible())
        check("and names the log to read", "reviews/J-9.log" in why.inner_text(),
              why.inner_text())
        check("no invented fix line", failed.locator(".ledger-finding-fix").count() == 0,
              failed.inner_text())

        # WHAT THE READER OPENED MUST SURVIVE THE POLL.
        #
        # The record is rebuilt from scratch every BOARD_MS, so a <details> the
        # operator opened becomes a new, closed element a moment later —
        # reported as "each time the ledger refreshes it collapses everything",
        # while reading a review, which is the one screen somebody sits on for
        # minutes. Nothing about the source says whether `open` survives a
        # rebuild; only a browser does.
        print("\n[3c] an opened finding survives a repaint")
        open_ledger(page, "/ledger/T-1")
        page.wait_for_function("document.getElementById('ledger-desc-id').textContent === 'T-1'",
                               timeout=10000)
        page.click("button.ff-tab:has-text('record')")
        page.wait_for_selector(".ledger-finding", timeout=10000)
        target = page.locator(".ledger-finding").nth(1)
        target.locator("summary").click()
        page.wait_for_timeout(200)
        check("the finding opened", target.get_attribute("open") is not None)

        # A repaint by the fast route: leaving the tab and coming back rebuilds
        # the record through the same paintEntry the poll uses.
        page.click("button.ff-tab:has-text('entry')")
        page.wait_for_timeout(150)
        page.click("button.ff-tab:has-text('record')")
        page.wait_for_selector(".ledger-finding", timeout=10000)
        check("still open after a rebuild",
              page.locator(".ledger-finding").nth(1).get_attribute("open") is not None)

        # …and closing one must STAY closed, including the harness-failure row
        # that opens by default. A default that reasserts itself every fifteen
        # seconds is the same bug wearing the opposite sign.
        page.locator(".ledger-finding").nth(1).locator("summary").click()
        page.wait_for_timeout(150)
        page.click("button.ff-tab:has-text('entry')")
        page.wait_for_timeout(150)
        page.click("button.ff-tab:has-text('record')")
        page.wait_for_selector(".ledger-finding", timeout=10000)
        check("still closed after a rebuild",
              page.locator(".ledger-finding").nth(1).get_attribute("open") is None)

        # And the real thing: one whole poll cycle, unmocked.
        target = page.locator(".ledger-finding").nth(0)
        if target.get_attribute("open") is None:
            target.locator("summary").click()
            page.wait_for_timeout(200)
        page.wait_for_timeout(16000)   # BOARD_MS is 15s
        check("still open after a real poll",
              page.locator(".ledger-finding").nth(0).get_attribute("open") is not None)

        # --- item 1: the lock ------------------------------------------------
        print("\n[1] a command locks one entry, not the page")
        open_ledger(page, "/ledger/T-1")
        page.wait_for_function("document.getElementById('ledger-desc-id').textContent === 'T-1'",
                               timeout=10000)
        page.wait_for_selector("#ledger-commands button", timeout=10000)
        page.click("#ledger-commands button:has-text('Review')")
        page.wait_for_timeout(600)

        check("the running line names the review",
              "second agent" in page.inner_text("#ledger-message"),
              page.inner_text("#ledger-message"))
        disabled = page.locator("#ledger-commands button[disabled]").count()
        total = page.locator("#ledger-commands button").count()
        check("T-1's own plates are disabled", disabled == total and total > 0,
              f"{disabled}/{total}")
        check("T-1's row is marked busy",
              page.locator('.ledger-row[data-entry-id="T-1"].is-busy').count() == 1)

        # THE COMPLAINT: move to another task while the review runs.
        page.click('.ledger-row[data-entry-id="T-2"]')
        page.wait_for_function("document.getElementById('ledger-desc-id').textContent === 'T-2'",
                               timeout=10000)
        page.wait_for_timeout(300)
        t2_total = page.locator("#ledger-commands button").count()
        t2_disabled = page.locator("#ledger-commands button[disabled]").count()
        check("T-2's buttons stay usable while T-1 is reviewing",
              t2_total > 0 and t2_disabled == 0, f"{t2_disabled}/{t2_total} disabled")
        check("T-2's row is not marked busy",
              page.locator('.ledger-row[data-entry-id="T-2"].is-busy').count() == 0)

        # and the board must keep moving
        check("the board still has its rows while a command is out",
              page.locator(".ledger-row").count() >= 3)

        # a second command on a DIFFERENT task, while the first is still out
        approve = page.locator("#ledger-commands button:has-text('Approve')")
        if approve.count():
            approve.first.click()
            page.wait_for_timeout(300)
            if page.locator("#ledger-prompt.is-open").count():
                page.click("#ledger-prompt-commands .ff-cmd.is-seal")
            page.wait_for_timeout(800)
            m = page.inner_text("#ledger-message")
            check("a command on another task runs while the first is still out",
                  "T-2" in m or "approved" in m.lower(), m)
        else:
            note("no Approve plate offered for T-2 — skipped the concurrent-command check")

        # wait for the review to land
        page.wait_for_function(
            "!document.querySelector('.ledger-row[data-entry-id=\\\"T-1\\\"].is-busy')",
            timeout=20000)
        check("the review released T-1 when it answered",
              page.locator('.ledger-row[data-entry-id="T-1"].is-busy').count() == 0)
        page.wait_for_timeout(500)
        check("T-1 is usable again",
              page.locator('.ledger-row[data-entry-id="T-1"]').count() == 1)

        # --- page health ------------------------------------------------------
        print("\n[0] the page itself")
        real = [e for e in errors if "favicon" not in e.lower()]
        check("no uncaught JS errors", not real, "; ".join(real[:3]))
        # The only request allowed to fail is the deliberate lookup of an id the
        # plane does not hold. Anything else is a route the page asks for and
        # nothing answers, which is invisible from the source.
        unexpected = [r for r in bad_responses if "T-404" not in r]
        check("no unexpected failed requests", not unexpected, "; ".join(unexpected[:5]))
        check("the page never scrolled horizontally",
              page.evaluate("document.documentElement.scrollWidth <= document.documentElement.clientWidth + 1"))

        shot = os.environ.get("LEDGER_SCREENSHOT")
        if shot:
            page.screenshot(path=shot, full_page=True)
        browser.close()

    print()
    if failures:
        print(f"{len(failures)} FAILED: " + ", ".join(failures))
        sys.exit(1)
    print("all checks passed")


main()
