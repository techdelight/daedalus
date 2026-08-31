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

        # --- the narrow layout -------------------------------------------------
        #
        # One screen at a time. The split pane is a desktop idea, and stacking
        # both panes at half height — what this used to do — is worse than
        # either alone: four rows of list above an entry too short to read.
        print("\n[5] on a phone, one screen at a time")
        phone = browser.new_context(viewport={"width": 390, "height": 844},
                                    is_mobile=True, has_touch=True,
                                    device_scale_factor=2)
        m = phone.new_page()
        m.goto(URL + "/ledger")
        m.wait_for_selector(".ledger-row", timeout=15000)
        m.wait_for_timeout(400)

        check("the device reports no hover", not m.evaluate("matchMedia('(hover: hover)').matches"))
        body = m.locator("#ledger-body")
        check("the list screen shows no entry pane",
              "is-entry" not in (body.get_attribute("class") or ""),
              body.get_attribute("class"))
        desc_visible = m.evaluate(
            "getComputedStyle(document.querySelector('.ledger-desc')).visibility")
        check("the entry pane is not on screen", desc_visible == "hidden", desc_visible)

        # A row is a 44px target and says it opens something.
        box = m.locator('.ledger-row[data-entry-id="T-1"]').bounding_box()
        check("a row is a 44px touch target", box["height"] >= 44, f"{box['height']}px")

        # Tapping navigates. It must be a real address, so the phone's own back
        # gesture works without the page knowing about it.
        m.tap('.ledger-row[data-entry-id="T-1"]')
        m.wait_for_timeout(500)
        check("tapping a row opens its own address", m.url.endswith("/ledger/T-1"), m.url)
        check("the entry screen is on", "is-entry" in (m.locator("#ledger-body").get_attribute("class") or ""))
        check("the entry pane is now visible",
              m.evaluate("getComputedStyle(document.querySelector('.ledger-desc')).visibility") == "visible")
        check("the back control is there", m.locator("#ledger-back").is_visible())

        # Two controls called Back, a thumb apart, is one too many.
        check("the header's Back is hidden while an entry is open",
              not m.locator(".ledger-header .btn-back").is_visible())

        # Back returns to the list AND to its address.
        m.tap("#ledger-back")
        m.wait_for_timeout(500)
        check("back returns to the list", m.url.endswith("/ledger"), m.url)
        check("the entry screen is off",
              "is-entry" not in (m.locator("#ledger-body").get_attribute("class") or ""))

        check("no sideways scrolling on a phone",
              m.evaluate("document.documentElement.scrollWidth <= document.documentElement.clientWidth + 1"))
        m.screenshot(path=os.environ.get("LEDGER_MOBILE_SHOT") or "/tmp/m-list.png")
        phone.close()

        # --- the terminal's copy path -----------------------------------------
        #
        # A URL printed by `claude /login` is long, and a phone's terminal is
        # narrow, so xterm stores it as several ROWS of one logical line. Two
        # things used to go wrong there and both only showed up on mobile:
        # nothing linkified the URL (no addon, so no anchor to tap, and no
        # drag-select fallback on touch), and the select overlay rebuilt the text
        # row by row joined with "\n" — turning the soft wrap into real newlines
        # and pasting a URL that would not resolve.
        #
        # The check that matters is the ROUND TRIP: the URL that comes out of
        # the overlay must equal the URL that went in, character for character.
        # Counting rows or newlines would pass on a terminal wide enough not to
        # wrap, which is exactly the case that never had the bug.
        print("\n[6] a long URL survives being copied off a phone")
        term_ctx = browser.new_context(viewport={"width": 390, "height": 844},
                                       is_mobile=True, has_touch=True)
        tp = term_ctx.new_page()
        tp.goto(URL + "/")
        tp.wait_for_function("typeof connectTerminal === 'function'", timeout=10000)

        # Show the terminal screen. Both views are display-driven by .active /
        # .hidden, and attaching for real needs a project and a runner this
        # server does not have — but every element under test is on this screen,
        # and a hidden one would let a visibility bug through unnoticed.
        tp.evaluate("""() => {
            document.getElementById('project-view').classList.add('hidden');
            document.getElementById('terminal-view').classList.add('active');
        }""")

        # The addon has to be ON THE PAGE. This is the whole of defect one: the
        # page loaded xterm and the fit addon and nothing else, so a URL in the
        # output was characters in a cell grid with no anchor anywhere. Link
        # ACTIVATION is not asserted — it needs xterm's hover-state internals,
        # and a flaky test is worse than an honest gap.
        check("the web-links addon is loaded",
              tp.evaluate("typeof WebLinksAddon !== 'undefined'"
                          " && typeof WebLinksAddon.WebLinksAddon === 'function'"))

        tp.evaluate("connectTerminal('copy-check')")
        tp.wait_for_function("typeof term === 'object' && term !== null", timeout=10000)

        # cols is forced rather than inherited from the viewport, so the fixture
        # has the property that made the bug possible no matter how the phone
        # container happens to lay out.
        tp.evaluate("term.resize(40, 20)")
        login_url = (
            "https://claude.ai/oauth/authorize?code=true"
            "&client_id=9d1c250a-e61b-44d9-88ed-5944d1962f5e&response_type=code"
            "&redirect_uri=https%3A%2F%2Fconsole.anthropic.com%2Foauth%2Fcode%2Fcallback"
            "&scope=org%3Acreate_api_key+user%3Aprofile&code_challenge=BsQ2i0Fk9Xr"
            "&code_challenge_method=S256&state=hV7pQ2mZ"
        )
        tp.evaluate("u => term.write('Use the url below to sign in:\\r\\n\\r\\n' + u + '\\r\\n')",
                    login_url)
        tp.wait_for_timeout(400)

        wrapped = tp.evaluate(
            "() => { const b = term.buffer.active; let n = 0;"
            " for (let i = 0; i < b.length; i++) { const l = b.getLine(i);"
            " if (l && l.isWrapped) n++; } return n; }")
        check("the URL really did wrap across rows", wrapped >= 3,
              f"{wrapped} wrapped row(s) at cols={tp.evaluate('term.cols')}")

        tp.evaluate("document.getElementById('mobile-select-btn').click()")
        tp.wait_for_timeout(200)
        copied = tp.evaluate(
            "() => document.getElementById('select-overlay-text').textContent")

        check("the copied text contains the URL unbroken", login_url in copied,
              repr(copied[copied.find("https://"):][:160]))
        # Said separately so a failure names the cause rather than just the miss.
        joined = "".join(copied.split())
        check("no newline or padding was inserted mid-URL",
              login_url in joined and login_url in copied,
              "characters survive but whitespace was injected"
              if login_url in joined else "the URL is not in the buffer at all")
        # --- the keys a soft keyboard cannot send ------------------------------
        #
        # On a phone `applyMobileMode` disables xterm's own stdin, so everything
        # arrives through #mobile-input. A soft keyboard has no Esc, its Tab
        # inserts a tab into the textarea and its Return adds a line break — so
        # Claude Code's cancel/cycle/confirm prompts had no reachable key at all.
        #
        # What is asserted is the BYTES ON THE WIRE, not that a button exists:
        # the socket is stubbed so each tap can be read back exactly. A button
        # wired to the wrong escape sequence looks identical from the DOM.
        print("\n[7] the keys a soft keyboard does not have")
        tp.evaluate("document.getElementById('select-done-btn').click()")
        tp.wait_for_timeout(150)

        for key in ("esc", "tab", "enter"):
            btn = tp.locator(f"#key-{key}-btn")
            box = btn.bounding_box()
            check(f"{key.upper()} is on screen as a 44px target",
                  btn.is_visible() and box and box["height"] >= 44,
                  f"visible={btn.is_visible()} height={box['height'] if box else None}")

        # Stub the socket. WebSocket.OPEN is 1; the real relay forwards any
        # non-control frame straight to the PTY, so a byte array here is exactly
        # what the shell would receive.
        #
        # The reconnect backoff has to be stopped FIRST. There is no runner
        # behind this server, so the real socket fails and `scheduleReconnect`
        # queues a reopen — which reassigns `ws` and silently replaces the stub
        # mid-test. That made this section fail on the ENTER tap roughly one run
        # in two, entirely as a function of where the 1s/2s/4s backoff happened
        # to land. `intentionalClose` is the flag the page's own teardown uses.
        tp.evaluate("""() => {
            intentionalClose = true;
            if (reconnectTimer) { clearTimeout(reconnectTimer); reconnectTimer = null; }
            window.__sent = [];
            ws = { readyState: 1,
                   send: function (d) {
                       window.__sent.push(typeof d === 'string' ? d : Array.from(d));
                   } };
        }""")

        tp.tap("#key-esc-btn")
        tp.tap("#key-tab-btn")
        tp.tap("#key-enter-btn")
        sent = tp.evaluate("() => window.__sent")

        check("ESC sends 0x1b", sent[0:1] == [[27]], repr(sent))
        check("TAB sends 0x09", sent[1:2] == [[9]], repr(sent))
        # Enter reuses the relay's control frame rather than a second way of
        # saying \r — runner_relay.go turns it into the write.
        check("ENTER sends the relay's enter frame",
              len(sent) > 2 and '"type":"enter"' in str(sent[2]).replace(" ", ""),
              repr(sent))
        check("nothing else went out", len(sent) == 3, repr(sent))

        # The reason the buttons bind touchend-with-preventDefault rather than
        # click: focus must not leave the textarea, or the soft keyboard
        # collapses on every keypress and the row is unusable.
        tp.evaluate("document.getElementById('mobile-input').focus()")
        tp.tap("#key-esc-btn")
        check("tapping a key leaves the keyboard up (focus stays in the input)",
              tp.evaluate("() => document.activeElement && document.activeElement.id") == "mobile-input",
              tp.evaluate("() => document.activeElement && document.activeElement.id"))

        tp.screenshot(path=os.environ.get("LEDGER_KEYS_SHOT") or "/tmp/m-keys.png")
        term_ctx.close()

        # --- item 95: no dead ends -------------------------------------------
        #
        # Two things, and the second is the one an operator feels.
        #
        # 1. The plates on screen are the plane's answer, not the page's. The
        #    expected set is READ FROM THE PLANE inside this test, so the check
        #    cannot pass by agreeing with a list somebody typed twice.
        # 2. A refused command leaves you holding something you can do. That is
        #    the whole of #95: the five dead ends of 2026-08-25 were all refusals
        #    that named no reachable action.
        print("\n[95] a refusal names a way forward")
        page.goto(URL + "/ledger/T-1")
        page.wait_for_selector("#control-view.active", timeout=10000)
        page.wait_for_function(
            "document.getElementById('ledger-desc-id').textContent === 'T-1'", timeout=10000)
        page.wait_for_selector("#ledger-commands .ff-cmd", timeout=10000)

        allowed = page.evaluate("""async () => {
            const r = await fetch('/api/control/operations');
            const d = await r.json();
            const out = {};
            (d.operations || []).forEach(o => { out[o.key] = o.states; });
            return out;
        }""")
        check("the plane served its operation table",
              bool(allowed) and "refine" in allowed, str(sorted(allowed))[:120])
        # T-1 is approval_required. Every plate on screen must be an operation the
        # plane admits from there; the page may narrow, never widen.
        check("refine is offered where the plane admits it",
              "approval_required" in allowed.get("refine", []) and
              page.locator('#ledger-commands .ff-cmd:text-is("Refine")').count() == 1)
        check("dispatch is NOT offered where the plane refuses it",
              "approval_required" not in allowed.get("dispatch", []) and
              page.locator('#ledger-commands .ff-cmd:text-is("Dispatch")').count() == 0)

        # Now the refusal. T-2's refine is declined by the fixture, with the
        # remedies the plane computed.
        page.goto(URL + "/ledger/T-2")
        page.wait_for_function(
            "document.getElementById('ledger-desc-id').textContent === 'T-2'", timeout=10000)
        page.wait_for_selector('#ledger-commands .ff-cmd:text-is("Refine")', timeout=10000)
        page.click('#ledger-commands .ff-cmd:text-is("Refine")')
        page.wait_for_selector("#ledger-prompt.is-open", timeout=5000)
        page.click('#ledger-prompt button:text-is("OK")')
        page.wait_for_function(
            "document.getElementById('ledger-message').textContent.indexOf('Refused') !== -1",
            timeout=10000)
        said = page.inner_text("#ledger-message")
        check("the refusal is shown as a refusal, with its reason",
              "Refused" in said and "attempts_exhausted" in said, said[:160])
        check("and it names what CAN be done from here",
              "You can:" in said, said[:200])
        check("the way out is in the page's own words, not a shell command",
              "Budget" in said and "daedalus task" not in said, said[:200])
        # THE BUG A SCREENSHOT FOUND. The first version of the exhausted-attempts
        # remedy list dropped dispatch, retry and replan and left REFINE in — and
        # refine spends an attempt like the other three, so the page told an
        # operator to refine a task whose refine had just been refused. #95's own
        # defect, inside the fix for #95.
        check("it does not offer the command that was just refused",
              "Refine" not in said.split("You can:")[-1], said[:200])
        check("the refusal and the way out are two sentences, not a run-on",
              "attempt(s). You can:" in said, said[:200])
        shot95 = os.environ.get("LEDGER_DEADEND_SHOT")
        if shot95:
            page.screenshot(path=shot95)
        # The corollary from docs/no-dead-ends.md: no operator action should
        # require destroying the task's history.
        check("cancel is not the only thing offered",
              said.count("·") >= 1 and said.strip().rstrip(".").split("You can:")[-1].strip() != "Cancel",
              said[:200])

        # --- page health ------------------------------------------------------
        print("\n[0] the page itself")
        # A 422 is the plane SAYING NO, which is it working — the browser logs it as
        # a failed resource because it cannot know that, and the check would
        # otherwise punish the suite for exercising a refusal on purpose ([95]).
        real = [e for e in errors if "favicon" not in e.lower()
                and "422" not in e]
        check("no uncaught JS errors", not real, "; ".join(real[:3]))
        # Two requests are allowed to fail: the deliberate lookup of an id the
        # plane does not hold, and the refusal [95] provokes. Anything else is a
        # route the page asks for and nothing answers, which is invisible from the
        # source.
        unexpected = [r for r in bad_responses
                      if "T-404" not in r and not r.startswith("422 ")]
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
