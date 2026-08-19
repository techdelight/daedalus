// Copyright (C) 2026 Techdelight BV
//
// The Ledger — the control plane's own view, sibling to the Guild Hall.
//
// It supersedes the two panels that used to sit squeezed under the project
// list (approvals.js, board.js). Those answered the same two questions in two
// places, which board.js itself argued against: one surface per question. This
// is that one surface — what is happening, and what is waiting on you.
//
// It reads three endpoints that already existed: /api/board (the grouped
// programme view), /api/approvals (what needs a human), and /api/plane-status
// (the daemon's own summary). The board is the source of truth for grouping;
// approvals only decides which entries get a seal.
//
// THE ONE WRITE. Approve and reject POST to /api/approvals/{id}/…, exactly as
// the old panel did. Nothing else here acts: cancel, retry, integrate and the
// rest have surfaces with the right confirmations attached, and a ledger that
// could also dispatch would be a second place for those decisions to be made.

(function () {
  // The board changes on the scale of a Job, not a keystroke, and building it
  // resolves a repository path per project. Approvals are cheaper and more
  // urgent, so they are polled harder — the same split the old panels used.
  const BOARD_MS = 15000;
  const APPROVALS_MS = 5000;

  let timers = [];
  let approvable = new Set();

  function el(id) { return document.getElementById(id); }

  // Which sections carry a seal. The board's column keys are stable strings
  // (board.go says so), and this is the only place the Ledger cares which is
  // which — everything else renders whatever columns the plane sends, so a new
  // column appears here without a code change.
  const AWAITING_YOU = 'awaiting_approval';

  // Whose move it is, in the margin — but ONLY for the sections whose title does
  // not already say. "Awaiting your approval" and "Rejected — needs a decision"
  // name their hand in the heading; repeating it beside them would be decoration.
  // "Queued", "Running" and "Being verified" do not, and there the fact that
  // nothing is being asked of you is the most useful thing on the row.
  const HAND = {
    queued: 'the plane has it',
    blocked: 'a dependency has it',
    running: 'the plane has it',
    verifying: 'the plane has it',
  };

  // The verdict mark's tone. Reuses the palette's HP ramp as a judgement ramp.
  const MARK = {
    queued: ['queued', 'is-working'],
    blocked: ['blocked', 'is-waiting'],
    running: ['running', 'is-working'],
    verifying: ['verifying', 'is-working'],
    needs_decision: ['refused', 'is-refused'],
    awaiting_approval: ['awaiting seal', 'is-waiting'],
    ready_to_land: ['sealed', 'is-passed'],
    landed: ['landed', 'is-passed'],
    withdrawn: ['closed', 'is-refused'],
  };

  function text(tag, cls, value) {
    const node = document.createElement(tag);
    if (cls) node.className = cls;
    node.textContent = value;
    return node;
  }

  // The plane's marginalia: why this entry is where it is. Same facts the CLI
  // board prints under a card, in the same words.
  function noteFor(c) {
    const notes = [];
    let stuck = false;
    if (c.blockedOn && c.blockedOn.length) {
      notes.push('waiting on ' + c.blockedOn.join(' '));
    }
    if (c.unsatisfiable && c.unsatisfiable.length) {
      notes.push('stuck — ' + c.unsatisfiable.join(' ') + ' can never complete');
      stuck = true;
    }
    if (c.queuedForCapacity) {
      notes.push('holding a place in line for a free slot');
    }
    if (c.steering) {
      notes.push('steering ' + c.steering);
    }
    if (!notes.length) return null;
    const node = text('div', 'ledger-entry-note' + (stuck ? ' is-stuck' : ''), notes.join(' · '));
    return node;
  }

  function entry(card, columnKey) {
    const row = document.createElement('div');
    row.className = 'ledger-entry';

    row.appendChild(text('span', 'ledger-entry-id', card.taskId));
    row.appendChild(text('span', 'ledger-entry-project', card.project));
    row.appendChild(text('span', 'ledger-entry-objective', card.objective));

    // A seal only where a seal is the actual next act, and only once the
    // approvals endpoint has confirmed this task is genuinely waiting on a
    // human. The board alone is not enough: it groups by state, and a task can
    // sit in the approval column while the plane is still settling it.
    if (columnKey === AWAITING_YOU && approvable.has(card.taskId)) {
      row.appendChild(decision(card));
    } else {
      const mark = MARK[columnKey] || [columnKey.replace(/_/g, ' '), 'is-working'];
      row.appendChild(text('span', 'ledger-mark ' + mark[1], mark[0]));
    }

    const note = noteFor(card);
    if (note) row.appendChild(note);
    return row;
  }

  function decision(card) {
    const wrap = document.createElement('div');
    wrap.className = 'ledger-decision';

    const seal = document.createElement('button');
    seal.className = 'seal';
    seal.type = 'button';
    seal.textContent = 'Approve';
    // The label a screen reader hears names the object, not just the verb —
    // there may be several seals on the page and "Approve" alone would not say
    // which one this is.
    seal.setAttribute('aria-label', 'Approve ' + card.taskId + ', ' + card.project);

    const refuse = document.createElement('button');
    refuse.className = 'ledger-refuse';
    refuse.type = 'button';
    refuse.textContent = 'Reject';
    refuse.setAttribute('aria-label', 'Reject ' + card.taskId + ', ' + card.project);

    seal.onclick = function () { decide(card.taskId, 'approve', seal, refuse); };
    refuse.onclick = function () { decide(card.taskId, 'reject', seal, refuse); };

    wrap.appendChild(seal);
    wrap.appendChild(refuse);
    return wrap;
  }

  function decide(id, action, seal, refuse) {
    // Both controls go down together so a double-click cannot send two
    // decisions; the refresh restores the true state either way.
    seal.disabled = true;
    refuse.disabled = true;
    if (action === 'approve') {
      seal.classList.add('is-stamping');
    }
    fetch('/api/approvals/' + encodeURIComponent(id) + '/' + action, { method: 'POST' })
      .catch(function () { /* the refresh below reports the real state */ })
      .then(function () {
        // Long enough to let the stamp land. A decision that vanished mid-press
        // would read as a page glitch rather than an act.
        setTimeout(function () { refreshApprovals(); refreshBoard(); }, 420);
      });
  }

  function renderBoard(data) {
    const leaf = el('ledger-leaf');
    const closed = el('ledger-closed');
    const sub = el('ledger-subtitle');
    if (!leaf || !closed) return;

    // Unreachable is not empty. The CLI draws this distinction and so does the
    // page: an operator who cannot tell "nothing is running" from "I could not
    // ask" will trust the wrong one.
    if (!data || !data.available) {
      leaf.style.display = 'none';
      closed.style.display = '';
      if (sub) sub.textContent = '';
      return;
    }
    closed.style.display = 'none';
    leaf.style.display = '';

    if (sub) {
      const limit = data.globalLimit > 0 ? String(data.globalLimit) : '∞';
      sub.textContent = data.globalRunning + '/' + limit + ' running · ' +
        data.pendingApprovals + ' awaiting your seal · ' +
        data.pendingProposals + ' proposals pending';
    }

    leaf.innerHTML = '';
    (data.columns || []).forEach(function (col) {
      const section = document.createElement('div');
      section.className = 'ledger-section';

      const head = document.createElement('div');
      head.className = 'ledger-section-head';
      head.appendChild(text('span', 'ledger-section-title', col.title));
      head.appendChild(text('span', 'ledger-section-count', String(col.cards.length)));
      const hand = HAND[col.key];
      if (hand) head.appendChild(text('span', 'ledger-section-hand', hand));
      section.appendChild(head);

      if (!col.cards.length) {
        section.appendChild(text('div', 'ledger-none', 'Nothing.'));
      } else {
        col.cards.forEach(function (c) { section.appendChild(entry(c, col.key)); });
      }
      leaf.appendChild(section);
    });
  }

  function renderApprovals(data) {
    const next = new Set();
    if (data && data.available && data.tasks) {
      data.tasks.forEach(function (t) { next.add(t.id); });
    }
    // Only redraw the board when the set actually changed — otherwise a seal
    // the operator is reaching for could be rebuilt out from under the cursor
    // every five seconds.
    const changed = next.size !== approvable.size ||
      Array.from(next).some(function (id) { return !approvable.has(id); });
    approvable = next;
    if (changed) refreshBoard();
  }

  function refreshBoard() {
    return fetch('/api/board')
      .then(function (res) { return res.json(); })
      .then(renderBoard)
      .catch(function () { renderBoard(null); });
  }

  function refreshApprovals() {
    return fetch('/api/approvals')
      .then(function (res) { return res.json(); })
      .then(renderApprovals)
      .catch(function () { renderApprovals(null); });
  }

  // Polling starts when the view opens and stops when it closes: the Ledger is
  // one view among several and a hidden page should not keep asking the daemon
  // questions nobody is reading.
  function start() {
    stop();
    refreshApprovals();
    refreshBoard();
    timers.push(setInterval(refreshBoard, BOARD_MS));
    timers.push(setInterval(refreshApprovals, APPROVALS_MS));
  }

  function stop() {
    timers.forEach(clearInterval);
    timers = [];
  }

  // Shown and hidden the same way the Guild Hall is — `.hidden` on the project
  // list, `.active` on every other view. A third pattern here would be a third
  // thing to keep in step.
  window.showControlView = function () {
    const view = el('control-view');
    if (!view) return;
    if (typeof refreshTimer !== 'undefined' && refreshTimer) {
      clearInterval(refreshTimer);
      refreshTimer = null;
    }
    const list = el('project-view');
    if (list) list.classList.add('hidden');
    ['dashboard-view', 'guild-view', 'terminal-view'].forEach(function (id) {
      const v = el(id);
      if (v) v.classList.remove('active');
    });
    view.classList.add('active');
    document.title = 'The Ledger — Daedalus';
    start();
  };

  window.hideControlView = function () {
    const view = el('control-view');
    if (view) view.classList.remove('active');
    stop();
  };
})();
