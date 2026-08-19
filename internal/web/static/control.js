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
  // The task the cursor is on. Kept across refreshes so a poll does not empty the
  // description window out from under someone mid-read.
  let current = null;

  function el(id) { return document.getElementById(id); }

  // Which sections carry a seal. The board's column keys are stable strings
  // (board.go says so), and this is the only place the Ledger cares which is
  // which — everything else renders whatever columns the plane sends, so a new
  // column appears here without a code change.
  const AWAITING_YOU = 'awaiting_approval';

  // The status word, coloured like a JRPG status effect. It replaced a note in
  // the section margin naming whose move it was: with a word on every row saying
  // RUNNING or AWAITING SEAL, the margin was repeating the row.
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
  function notesFor(c) {
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
    return { text: notes.join(' · '), stuck: stuck };
  }

  function markFor(columnKey) {
    return MARK[columnKey] || [columnKey.replace(/_/g, ' '), 'is-working'];
  }

  // A row is one line and never more. The objective is a paragraph — sometimes
  // several — so the row carries as much as fits and the description window
  // above carries the rest. Pointing at a row is what fills that window, which
  // is the same gesture as reading it.
  function entry(card, columnKey) {
    const row = document.createElement('button');
    row.type = 'button';
    row.className = 'ledger-row';
    row.dataset.taskId = card.taskId;

    row.appendChild(text('span', 'ledger-cursor', '\u25B6'));
    row.appendChild(text('span', 'ledger-row-id', card.taskId));
    row.appendChild(text('span', 'ledger-row-objective', card.objective));

    // Every row gets a status word, including the ones awaiting a decision. The
    // commands live in the entry window, where the objective they are deciding
    // on is legible — a row is one truncated line and nobody should approve
    // something on the strength of its first eight words.
    const mark = markFor(columnKey);
    row.appendChild(text('span', 'ledger-row-status ' + mark[1], mark[0]));

    const show = function () { describe(card, columnKey); };
    row.addEventListener('mouseenter', show);
    row.addEventListener('focus', show);
    row.addEventListener('click', show);
    return row;
  }

  // The description window. It is the only place an objective is readable in
  // full, so it is also the only place the plane's marginalia belongs.
  function describe(card, columnKey) {
    const id = el('ledger-desc-id');
    const project = el('ledger-desc-project');
    const status = el('ledger-desc-status');
    const body = el('ledger-desc-body');
    const note = el('ledger-desc-note');
    if (!id || !body) return;

    current = card ? card.taskId : null;
    document.querySelectorAll('.ledger-row').forEach(function (r) {
      r.classList.toggle('is-current', !!card && r.dataset.taskId === card.taskId);
    });

    if (!card) {
      id.textContent = '—';
      if (project) project.textContent = '';
      if (status) status.textContent = '';
      body.className = 'ledger-desc-body is-empty';
      body.textContent = 'Point at an entry to read it.';
      if (note) note.textContent = '';
      const none = el('ledger-commands');
      if (none) none.innerHTML = '';
      return;
    }

    const mark = markFor(columnKey);
    id.textContent = card.taskId;
    if (project) project.textContent = card.project;
    if (status) {
      status.textContent = mark[0];
      status.className = 'ledger-desc-status ledger-row-status ' + mark[1];
    }
    body.className = 'ledger-desc-body';
    body.textContent = card.objective;
    body.scrollTop = 0;
    if (note) {
      const n = notesFor(card);
      note.textContent = n.text;
      note.className = 'ledger-desc-note' + (n.stuck ? ' is-stuck' : '');
    }

    // Commands only where a decision is genuinely the next act, and only once
    // the approvals endpoint has confirmed it. The board alone is not enough: it
    // groups by state, and a task can sit in the approval column while the plane
    // is still settling it.
    const cmds = el('ledger-commands');
    if (cmds) {
      cmds.innerHTML = '';
      if (columnKey === AWAITING_YOU && approvable.has(card.taskId)) {
        cmds.appendChild(commands(card));
      }
    }
  }

  // A decision is a menu command, rendered into the entry window beside the
  // objective it decides on.
  function commands(card) {
    const wrap = document.createElement('span');
    wrap.className = 'ledger-command-group';

    const approve = document.createElement('button');
    approve.className = 'ff-cmd';
    approve.type = 'button';
    approve.textContent = 'Approve';
    // A screen reader hears the object, not just the verb: there may be several
    // of these on screen and "Approve" alone would not say which.
    approve.setAttribute('aria-label', 'Approve ' + card.taskId + ', ' + card.project);

    const refuse = document.createElement('button');
    refuse.className = 'ff-cmd is-refuse';
    refuse.type = 'button';
    refuse.textContent = 'Reject';
    refuse.setAttribute('aria-label', 'Reject ' + card.taskId + ', ' + card.project);

    approve.onclick = function () { decide(card.taskId, 'approve', approve, refuse); };
    refuse.onclick = function () { decide(card.taskId, 'reject', approve, refuse); };

    wrap.appendChild(approve);
    wrap.appendChild(refuse);
    return wrap;
  }

  function decide(id, action, approve, refuse) {
    // Both commands go down together so a double-click cannot send two
    // decisions; the refresh restores the true state either way.
    approve.disabled = true;
    refuse.disabled = true;
    fetch('/api/approvals/' + encodeURIComponent(id) + '/' + action, { method: 'POST' })
      .catch(function () { /* the refresh below reports the real state */ })
      .then(function () { refreshApprovals(); refreshBoard(); });
  }

  function renderBoard(data) {
    const list = el('ledger-list');
    const body = el('ledger-body');
    const closed = el('ledger-closed');
    const sub = el('ledger-subtitle');
    if (!list || !closed) return;

    // Unreachable is not empty. The CLI draws this distinction and so does the
    // page: an operator who cannot tell "nothing is running" from "I could not
    // ask" will trust the wrong one.
    if (!data || !data.available) {
      if (body) body.style.display = 'none';
      closed.style.display = '';
      if (sub) sub.textContent = '';
      return;
    }
    closed.style.display = 'none';
    if (body) body.style.display = '';

    if (sub) {
      const limit = data.globalLimit > 0 ? String(data.globalLimit) : '\u221E';
      sub.textContent = data.globalRunning + '/' + limit + ' running \u00B7 ' +
        data.pendingApprovals + ' awaiting you \u00B7 ' +
        data.pendingProposals + ' proposals pending';
    }

    list.innerHTML = '';
    let restored = null;
    (data.columns || []).forEach(function (col) {
      const section = document.createElement('div');
      section.className = 'ledger-section';

      const head = document.createElement('div');
      head.className = 'ledger-section-head';
      head.appendChild(text('span', 'ledger-section-title', col.title));
      head.appendChild(text('span', 'ledger-section-count', String(col.cards.length)));
      section.appendChild(head);

      if (!col.cards.length) {
        section.appendChild(text('div', 'ledger-none', 'Nothing.'));
      } else {
        col.cards.forEach(function (c) {
          section.appendChild(entry(c, col.key));
          if (c.taskId === current) restored = { card: c, key: col.key };
        });
      }
      list.appendChild(section);
    });

    // Put the cursor back where it was. If that task has moved on, the window
    // says so by going blank rather than showing a stale entry as if it were live.
    if (restored) {
      describe(restored.card, restored.key);
    } else {
      describe(null, null);
    }
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
