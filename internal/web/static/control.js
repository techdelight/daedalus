// Copyright (C) 2026 Techdelight BV
//
// The Ledger — the control plane's own view, sibling to the Guild Hall.
//
// It supersedes the two panels that used to sit squeezed under the project
// list (approvals.js, board.js). Those answered the same two questions in two
// places, which board.js itself argued against: one surface per question. This
// is that one surface — what is happening, what is waiting on you, and now what
// to do about it.
//
// THE WRITES. Every operation `daedalus task` can perform is here, on the same
// routes under /api/control, through the same client, with the same authority —
// the Web UI is a client of control.sock exactly like the CLI. The earlier
// version deliberately offered only approve and reject, on the argument that a
// ledger which could also dispatch would be a second place for those decisions
// to be made. That was wrong in one direction: a surface that shows you a
// refused task and cannot retry it does not avoid the second place, it sends you
// to a terminal to finish the thought — and the two then disagree about where
// work is driven from. There is still one surface; it is just no longer a
// read-only one.
//
// WHO DECIDES WHAT IS LEGAL. The plane does. COMMANDS below names the states in
// which each command is worth OFFERING, which is a question about the menu, not
// about authority: the guards live in internal/control and a command the plane
// refuses comes back as a typed refusal and is printed as one. The table can be
// out of date and the worst that happens is a command that says no; it can never
// let something through.

(function () {
  // The board changes on the scale of a Job, not a keystroke, and building it
  // resolves a repository path per project. Approvals are cheaper and more
  // urgent, so they are polled harder — the same split the old panels used.
  const BOARD_MS = 15000;
  const APPROVALS_MS = 5000;

  let timers = [];
  let approvable = new Set();
  // What the cursor is on, kept across refreshes so a poll does not empty the
  // entry window out from under someone mid-read.
  let current = null;         // {kind: 'task'|'proposal', id}
  let currentCard = null;     // the board card, when the entry is a task
  let currentKey = null;      // its column key
  let detail = null;          // StatusView for the selected task, once loaded
  let tab = 'entry';          // entry | terms | record
  let message = null;         // {text, kind} — the last thing that happened
  let proposals = [];
  let targets = [];
  let archive = [];           // terminal tasks; only fetched when asked for
  let showArchive = false;
  let busy = false;           // one command at a time
  // True while the operator is MID-INTERACTION: a prompt window is open, or a
  // command is asking "are you sure" in place.
  //
  // It exists because the board polls, and polling repaints the entry — including
  // the command row. A confirmation rendered into that row was therefore being
  // destroyed under the operator's hand within fifteen seconds of appearing, and
  // the Yes they then clicked landed on a rebuilt Confirm. It looked exactly like
  // confirming did not work, which is what it was reported as.
  //
  // The list keeps refreshing while this is true; only the ENTRY is left alone,
  // because the entry is the thing being interacted with.
  let awaiting = false;

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

  // The archive is the one section the board does not supply, so its rows are
  // marked from the Task's own state rather than a column key.
  const STATE_MARK = {
    integrated: ['landed', 'is-passed'],
    cancelled: ['cancelled', 'is-refused'],
    failed: ['failed', 'is-refused'],
    expired: ['expired', 'is-refused'],
  };

  function text(tag, cls, value) {
    const node = document.createElement(tag);
    if (cls) node.className = cls;
    node.textContent = value;
    return node;
  }

  function short(sha) { return sha ? sha.slice(0, 7) : ''; }

  // --- talking to the plane -------------------------------------------------

  // send performs one command and turns whatever comes back into a message.
  //
  // A REFUSAL IS NOT A FAILURE, and this is the only place that distinction gets
  // made on the page: the plane answers 422 with a machine-readable reason for
  // "I understood and declined", and anything else for "something broke". Told
  // apart here, an operator reads "over_budget — no attempts left" instead of a
  // red box that could mean the daemon died.
  function send(method, path, body) {
    const init = { method: method };
    if (body !== undefined) {
      init.headers = { 'Content-Type': 'application/json' };
      init.body = JSON.stringify(body);
    }
    return fetch('/api/control' + path, init).then(function (res) {
      return res.json().catch(function () { return {}; }).then(function (data) {
        if (res.ok) return data;
        const err = new Error((data && data.error) || ('the plane answered ' + res.status));
        err.reason = data && data.reason;
        err.refused = res.status === 422;
        err.conflict = res.status === 409;
        throw err;
      });
    });
  }

  function get(path) {
    return fetch('/api/control' + path).then(function (res) { return res.json(); });
  }

  function say(text, kind) {
    message = { text: text, kind: kind || '' };
    paintMessage();
  }

  // --- the command table ----------------------------------------------------
  //
  // `states` is where the command is worth OFFERING. The flag variants are
  // separate commands rather than a modifier on one, because each is a different
  // act with different consequences — `retry --rebase` re-freezes the acceptance
  // oracle at a new commit, and burying that in a checkbox would make the more
  // consequential option the easier one to reach by accident.

  const ANY_ACTIVE = ['planned', 'blocked', 'queued', 'working', 'input_required',
    'candidate', 'verifying', 'verified', 'rejected', 'approval_required', 'approved'];

  const COMMANDS = [
    {
      key: 'dispatch', label: 'Dispatch', states: ['planned', 'queued', 'rejected'],
      run: function (id) { return send('POST', '/tasks/' + enc(id) + '/dispatch'); },
      done: function (r) {
        return 'Job ' + (r.job ? r.job.id : '?') + ' ' +
          (r.job ? r.job.state : '') + (r.artifact ? ' · artifact ' + r.artifact.id : '');
      },
    },
    {
      key: 'verify', label: 'Verify', states: ['candidate'],
      run: function (id) { return send('POST', '/tasks/' + enc(id) + '/verify', {}); },
      done: verdict,
    },
    {
      // Named for what it does to the RECORD, not for what it does to the gate:
      // the verifier still runs, the failure is still recorded, and the artifact
      // still carries verify=fail. What is waived is the consequence, and the
      // human doing it is the one answerable for that.
      key: 'waive', label: 'Verify · waive', states: ['candidate'], confirm: true, danger: true,
      hint: 'Runs the verifier, records the true result, and moves on anyway — on your authority.',
      run: function (id) { return send('POST', '/tasks/' + enc(id) + '/verify', { ignoreResult: true }); },
      done: verdict,
    },
    {
      key: 'retry', label: 'Retry', states: ['rejected'],
      hint: 'A fresh attempt from the same base. Costs an attempt.',
      run: function (id) { return send('POST', '/tasks/' + enc(id) + '/retry', {}); },
      done: function (r) {
        return 'Attempt ' + r.attempt + (r.maxAttempts ? ' of ' + r.maxAttempts : '') +
          ' · job ' + (r.dispatch && r.dispatch.job ? r.dispatch.job.id : '?');
      },
    },
    {
      key: 'retry-rebase', label: 'Retry · rebase', states: ['rejected'], confirm: true,
      hint: 'Re-pins the task to the project tip and RE-FREEZES its acceptance policy there.',
      run: function (id) { return send('POST', '/tasks/' + enc(id) + '/retry', { rebase: true }); },
      done: function (r) {
        return 'Attempt ' + r.attempt + ' from ' + short(r.baseSha) + (r.rebased ? ' (rebased)' : '');
      },
    },
    {
      key: 'reverify', label: 'Reverify', states: ['rejected'],
      hint: 'Grade the SAME artifact again. Costs no attempt.',
      run: function (id) { return send('POST', '/tasks/' + enc(id) + '/reverify', {}); },
      done: regrade,
    },
    {
      key: 'reverify-amended', label: 'Reverify · amended', states: ['rejected'], confirm: true,
      hint: 'Re-grade under a policy that has CHANGED: re-pins to the tip and re-freezes the oracle.',
      run: function (id) { return send('POST', '/tasks/' + enc(id) + '/reverify', { amended: true }); },
      done: regrade,
    },
    {
      key: 'replan', label: 'Replan', states: ['rejected'],
      prompt: {
        title: 'Replan', label: 'A new objective for this task',
        multiline: true, fill: function (t) { return t.objective; },
      },
      run: function (id, value) { return send('POST', '/tasks/' + enc(id) + '/replan', { objective: value }); },
      done: function () { return 'Objective replaced; the task is planned again.'; },
    },
    {
      // Its own plate, like `retry · rebase`: re-pinning adopts a newer acceptance
      // oracle, which is a governance act rather than a detail of rewording an
      // objective, and burying it in the same button would make the more
      // consequential option the easier one to reach.
      key: 'replan-rebase', label: 'Replan · rebase', states: ['rejected'], confirm: true,
      hint: 'New objective AND re-pin to the project tip, re-freezing the acceptance policy there.',
      prompt: {
        title: 'Replan and rebase', label: 'A new objective for this task',
        multiline: true, fill: function (t) { return t.objective; },
      },
      run: function (id, value) {
        return send('POST', '/tasks/' + enc(id) + '/replan', { objective: value, rebase: true });
      },
      done: function (t) { return 'Objective replaced and re-pinned to ' + short(t.baseSha) + '.'; },
    },
    {
      key: 'checks', label: 'Checks', states: ['planned', 'blocked', 'queued', 'candidate', 'rejected'],
      hint: 'Per-task acceptance commands, appended to the project policy. One per line; empty clears.',
      prompt: {
        title: 'Acceptance checks', label: 'One command per line',
        multiline: true, allowEmpty: true,
        fill: function (t) { return (t.checks || []).join('\n'); },
      },
      run: function (id, value) {
        const lines = value.split('\n').map(function (s) { return s.trim(); })
          .filter(function (s) { return s.length; });
        return send('POST', '/tasks/' + enc(id) + '/checks', { checks: lines });
      },
      done: function (t) {
        const n = (t.checks || []).length;
        return n ? n + ' check' + (n === 1 ? '' : 's') + ' on this task.' : 'Checks cleared.';
      },
    },
    {
      // Offered wherever there is an artifact to read — including `candidate`
      // (nobody has graded it yet) and `rejected` (the machine oracle said no,
      // which is the case that most needs a second opinion). Review moves
      // nothing, so there is nothing to protect by withholding it; gating it
      // behind the oracle passing made the second opinion available only when
      // the first one already agreed.
      key: 'review', label: 'Review',
      hint: 'Send an agent to read the change against what it promised. Advisory — it moves nothing.',
      states: ['candidate', 'rejected', 'verified', 'approval_required', 'approved'],
      run: function (id) { return send('POST', '/tasks/' + enc(id) + '/review'); },
      // This used to say "Review passed." or "Review failed — " and nothing else.
      // Both were wrong after M20: a review FAILS nothing (it moves no state), and
      // `reason` is now always empty, so a reading with real findings in it showed
      // up as one bland half-sentence. The findings are the entire point, and they
      // were a tab away with no reason to look. Say what was found, then GO THERE.
      done: function (r) {
        const n = (r.findings || []).length;
        const blocking = (r.findings || []).filter(function (f) {
          return f.severity === 'blocking';
        }).length;
        const who = r.reviewer || 'an unattributed reviewer';
        const verdict = r.passed ? 'found no blocker' : 'had concerns';
        // Land on the record, where the reading is. A judgement nobody reads is
        // the same as no judgement, and this is the one command whose whole
        // output lives somewhere other than this line.
        tab = 'record';
        paintEntry();
        if (!n) {
          return who + ' ' + verdict + ', with no findings — see RECORD. ' + (r.detail || '');
        }
        return who + ' ' + verdict + ': ' + n + ' finding' + (n === 1 ? '' : 's') +
          (blocking ? ', ' + blocking + ' blocking' : '') + ' — see RECORD below.';
      },
    },
    {
      key: 'approve', label: 'Approve', states: ['verified', 'approval_required'], seal: true,
      prompt: { title: 'Approve', label: 'A note for the record (optional)', allowEmpty: true },
      run: function (id, note) { return send('POST', '/tasks/' + enc(id) + '/approve', { note: note }); },
      done: function (t) { return t.id + ' is ' + t.state + '.'; },
    },
    {
      key: 'reject', label: 'Reject', states: ['verified', 'approval_required'], danger: true,
      prompt: { title: 'Reject', label: 'Why (optional)', allowEmpty: true },
      run: function (id, note) { return send('POST', '/tasks/' + enc(id) + '/reject', { note: note }); },
      done: function (t) { return t.id + ' is ' + t.state + '.'; },
    },
    {
      key: 'integrate', label: 'Integrate', states: ['verified', 'approved'], confirm: true,
      hint: 'Rebase onto the target, re-verify the MERGED result, and land it.',
      run: function (id) { return send('POST', '/tasks/' + enc(id) + '/integrate', {}); },
      done: landed,
    },
    {
      key: 'integrate-branch', label: 'Integrate · branch', states: ['verified', 'approved'], confirm: true,
      hint: 'Lands it, then fast-forwards the project checkout’s current branch to the landed commit.',
      run: function (id) { return send('POST', '/tasks/' + enc(id) + '/integrate', { intoBranch: true }); },
      done: landed,
    },
    {
      key: 'steer', label: 'Steer', states: ['queued', 'working', 'input_required'],
      hint: 'Inject an instruction into the running Job.',
      needsJob: true,
      prompt: { title: 'Steer', label: 'What should the Job do differently', multiline: true },
      run: function (id, value, jobID) {
        return send('POST', '/jobs/' + enc(jobID) + '/steer', { instruction: value });
      },
      done: function (s) { return s.id + ' is ' + s.state + (s.detail ? ' — ' + s.detail : ''); },
    },
    {
      key: 'depends', label: 'Depends on', states: ANY_ACTIVE,
      hint: 'This task may not LAND until the named one has.',
      prompt: { title: 'Dependency', label: 'The task this one waits for, e.g. T-3' },
      run: function (id, value) {
        return send('POST', '/tasks/' + enc(id) + '/dependencies', { dependsOn: value.trim() });
      },
      done: function (e) { return e.taskId + ' now waits for ' + e.dependsOn + '.'; },
    },
    {
      key: 'cancel', label: 'Cancel', states: ANY_ACTIVE, confirm: true, danger: true,
      hint: 'Ends the task and any running Job. Terminal — there is no way back.',
      run: function (id) { return send('DELETE', '/tasks/' + enc(id)); },
      done: function (t) { return t.id + ' is ' + t.state + '.'; },
    },
  ];

  function enc(s) { return encodeURIComponent(s); }

  function verdict(r) {
    if (r.verified) return 'Verified.';
    if (r.waived) return 'Waived — recorded as ' + (r.reason || 'failed') + '; ' + (r.detail || '');
    return 'Refused — ' + (r.reason || '') + ' ' + (r.detail || '');
  }

  function regrade(r) {
    return 'Set aside ' + (r.previousReason || 'the verdict') +
      (r.amended ? ' under an amended policy at ' + short(r.base_sha || r.baseSha) : '') +
      ' — ' + verdict(r.verify || {});
  }

  function landed(r) {
    let out = 'Landed ' + short(r.mergedSha) + ' onto the target' +
      (r.attempts > 1 ? ' after ' + r.attempts + ' rounds' : '') + '.';
    if (r.branchNote) out += ' Branch: ' + r.branchNote;
    return out;
  }

  // --- the list -------------------------------------------------------------

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
  // several — so the row carries as much as fits and the entry window beside it
  // carries the rest. Pointing at a row is what fills that window, which is the
  // same gesture as reading it.
  function entry(id, label, mark, onShow) {
    const row = document.createElement('button');
    row.type = 'button';
    row.className = 'ledger-row';
    row.dataset.entryId = id;

    row.appendChild(text('span', 'ledger-cursor', '▶'));
    row.appendChild(text('span', 'ledger-row-id', id));
    row.appendChild(text('span', 'ledger-row-objective', label));
    row.appendChild(text('span', 'ledger-row-status ' + mark[1], mark[0]));

    row.addEventListener('mouseenter', onShow);
    row.addEventListener('focus', onShow);
    row.addEventListener('click', onShow);
    return row;
  }

  function section(list, title, count, rows) {
    const sec = document.createElement('div');
    sec.className = 'ledger-section';
    const head = document.createElement('div');
    head.className = 'ledger-section-head';
    head.appendChild(text('span', 'ledger-section-title', title));
    head.appendChild(text('span', 'ledger-section-count', String(count)));
    sec.appendChild(head);
    if (!rows.length) {
      sec.appendChild(text('div', 'ledger-none', 'Nothing.'));
    } else {
      rows.forEach(function (r) { sec.appendChild(r); });
    }
    list.appendChild(sec);
    return sec;
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
      const limit = data.globalLimit > 0 ? String(data.globalLimit) : '∞';
      sub.textContent = data.globalRunning + '/' + limit + ' running · ' +
        data.pendingApprovals + ' awaiting you · ' +
        data.pendingProposals + ' proposals pending';
    }

    list.innerHTML = '';
    let restored = null;
    (data.columns || []).forEach(function (col) {
      const rows = col.cards.map(function (c) {
        if (current && current.kind === 'task' && c.taskId === current.id) {
          restored = { card: c, key: col.key };
        }
        return entry(c.taskId, c.objective, markFor(col.key), function () { selectTask(c, col.key); });
      });
      section(list, col.title, col.cards.length, rows);
    });

    // Proposals are entries in the same ledger: an agent asked for something and
    // a human has to answer. Kept out of the board columns because they are not
    // work in a state — they are a question about work.
    if (proposals.length) {
      section(list, 'Proposed — awaiting your word', proposals.length,
        proposals.map(function (p) {
          // Re-point at the FRESH proposal, not the one selected a poll ago: a
          // proposal that has just been confirmed elsewhere must not still be
          // offering its Confirm plate.
          if (current && current.kind === 'proposal' && p.id === current.id) {
            detail = p;
            restored = { proposal: p };
          }
          return entry(p.id, p.operation.replace(/_/g, ' ') + (p.taskId ? ' · ' + p.taskId : ''),
            ['proposed', 'is-waiting'], function () { selectProposal(p); });
        }));
    }

    // The archive is off by default: the board is about what is in flight, and a
    // list that grows forever would bury it. It is one command away because the
    // record of a landed task is exactly what you want when asking what happened.
    if (showArchive) {
      section(list, 'Closed', archive.length, archive.map(function (t) {
        const card = { taskId: t.id, project: t.project, objective: t.objective, state: t.state };
        if (current && current.kind === 'task' && t.id === current.id) restored = { card: card, key: null };
        return entry(t.id, t.objective, STATE_MARK[t.state] || [t.state, 'is-working'],
          function () { selectTask(card, null); });
      }));
    }

    // Never repaint the entry out from under an interaction. The cursor is
    // re-marked so the list still shows where you are, but the command row — which
    // may be holding a confirmation — is left exactly as the operator left it.
    if (awaiting || busy) {
      document.querySelectorAll('.ledger-row').forEach(function (r) {
        r.classList.toggle('is-current', !!current && r.dataset.entryId === current.id);
      });
      return;
    }

    // Put the cursor back where it was. If that entry has moved on, the window
    // says so by going blank rather than showing a stale one as if it were live.
    if (restored && restored.card) {
      currentCard = restored.card;
      currentKey = restored.key;
      paintEntry();
    } else if (restored && restored.proposal) {
      paintEntry();
    } else if (current) {
      clearEntry();
    } else {
      paintEntry();
    }

    // AND RE-READ THE OPEN ENTRY. Without this the board was live and the entry
    // beside it was frozen: `detail` — the jobs, the artifacts, the terms, the
    // reviews — was fetched once when a row was selected and never again, so
    // anything that happened OUTSIDE this page never appeared. Run `task review`
    // in a terminal, or let an agent finish, and the Ledger showed the old
    // reading indefinitely while the list two inches to its left updated. That is
    // a page that looks live and is not, which is worse than one that plainly
    // is not. Guarded by the early return above, so it never lands mid-interaction.
    if (current && current.kind === 'task') {
      loadDetail(current.id);
    }
  }

  // --- the entry window -----------------------------------------------------

  function selectTask(card, columnKey) {
    const same = current && current.kind === 'task' && current.id === card.taskId;
    current = { kind: 'task', id: card.taskId };
    currentCard = card;
    currentKey = columnKey;
    if (!same) {
      detail = null;
      message = null;
      tab = 'entry';
    }
    paintEntry();
    loadDetail(card.taskId);
  }

  function selectProposal(p) {
    current = { kind: 'proposal', id: p.id };
    currentCard = null;
    currentKey = null;
    detail = p;
    message = null;
    tab = 'entry';
    paintEntry();
  }

  function clearEntry() {
    current = null;
    currentCard = null;
    currentKey = null;
    detail = null;
    paintEntry();
  }

  // loadDetail fetches the full status of the selected task. It is a SECOND
  // request on top of the board because the board is a summary by design — the
  // jobs, the artifacts, the budget and the frozen policy are what the commands
  // are decided against, and none of them are on a card.
  function loadDetail(id) {
    get('/tasks/' + enc(id)).then(function (view) {
      if (!current || current.kind !== 'task' || current.id !== id) return; // moved on
      if (view && view.task) {
        detail = view;
        paintEntry();
      }
    }).catch(function () { /* the entry renders from the card alone */ });
  }

  function paintEntry() {
    const head = el('ledger-desc-id');
    const project = el('ledger-desc-project');
    const status = el('ledger-desc-status');
    const bodyEl = el('ledger-desc-body');
    const note = el('ledger-desc-note');
    const tabs = el('ledger-tabs');
    if (!head || !bodyEl) return;

    document.querySelectorAll('.ledger-row').forEach(function (r) {
      r.classList.toggle('is-current', !!current && r.dataset.entryId === current.id);
    });

    if (!current) {
      head.textContent = '—';
      if (project) project.textContent = '';
      if (status) status.textContent = '';
      if (tabs) tabs.innerHTML = '';
      bodyEl.className = 'ledger-desc-body is-empty';
      bodyEl.textContent = 'Point at an entry to read it.';
      if (note) note.textContent = '';
      el('ledger-commands').innerHTML = '';
      paintFoot();
      return;
    }

    if (current.kind === 'proposal') return paintProposal();

    const task = detail && detail.task;
    const state = task ? task.state : (currentCard ? currentCard.state : '');
    const mark = currentKey ? markFor(currentKey) : (STATE_MARK[state] || [state, 'is-working']);

    head.textContent = current.id;
    if (project) project.textContent = currentCard ? currentCard.project : (task ? task.project : '');
    if (status) {
      status.textContent = mark[0];
      status.className = 'ledger-desc-status ledger-row-status ' + mark[1];
    }
    paintTabs(['entry', 'terms', 'record']);
    bodyEl.className = 'ledger-desc-body';
    bodyEl.innerHTML = '';
    if (tab === 'entry') paintObjective(bodyEl);
    else if (tab === 'terms') paintTerms(bodyEl);
    else paintRecord(bodyEl);

    if (note) {
      const n = currentCard ? notesFor(currentCard) : { text: '', stuck: false };
      note.textContent = n.text;
      note.className = 'ledger-desc-note' + (n.stuck ? ' is-stuck' : '');
    }
    paintCommands(state);
    paintMessage();
    paintFoot();
  }

  function paintTabs(names) {
    const tabs = el('ledger-tabs');
    if (!tabs) return;
    tabs.innerHTML = '';
    names.forEach(function (name) {
      const b = document.createElement('button');
      b.type = 'button';
      b.className = 'ff-tab' + (tab === name ? ' is-current' : '');
      b.textContent = name;
      b.setAttribute('aria-pressed', tab === name ? 'true' : 'false');
      b.onclick = function () { tab = name; paintEntry(); };
      tabs.appendChild(b);
    });
  }

  function paintObjective(host) {
    const task = detail && detail.task;
    host.appendChild(text('div', 'ledger-prose', task ? task.objective :
      (currentCard ? currentCard.objective : '')));
    // Say that a reading exists. The findings live on RECORD, and an operator
    // with no reason to look there would never learn an agent had read their
    // work — which is indistinguishable from the review not having happened.
    const reviews = (detail && detail.reviews) || [];
    if (reviews.length) {
      const last = reviews[reviews.length - 1];
      const blocking = (last.findings || []).filter(function (f) {
        return f.severity === 'blocking';
      }).length;
      const line = text('div', 'ledger-review-flag',
        '▶ ' + reviews.length + ' review' + (reviews.length === 1 ? '' : 's') + ' — ' +
        (last.reviewer || 'unattributed') + ' ' + (last.passed ? 'found no blocker' : 'had concerns') +
        (blocking ? ', ' + blocking + ' blocking' : '') + '. See RECORD.');
      line.title = 'Open the record page to read the findings';
      host.appendChild(line);
    }

    const deps = detail && detail.dependencies;
    if (deps && ((deps.dependsOn || []).length || (deps.dependents || []).length)) {
      const facts = document.createElement('dl');
      facts.className = 'ledger-facts';
      if ((deps.dependsOn || []).length) fact(facts, 'waits for', deps.dependsOn.join(' '));
      if ((deps.dependents || []).length) fact(facts, 'waited on by', deps.dependents.join(' '));
      if (deps.status && deps.status.unsatisfiable && deps.status.unsatisfiable.length) {
        fact(facts, 'unsatisfiable', deps.status.unsatisfiable.join(' '));
      }
      host.appendChild(facts);
    }
  }

  function fact(list, key, value) {
    list.appendChild(text('dt', 'ledger-fact-k', key));
    list.appendChild(text('dd', 'ledger-fact-v', value));
  }

  // TERMS is what this task is graded against and bounded by — the two things a
  // command like `reverify --amended` or `checks` actually changes. They were
  // invisible on the old page, which meant the operator could not see what the
  // oracle was before deciding to move it.
  function paintTerms(host) {
    const task = detail && detail.task;
    if (!task) {
      host.appendChild(text('div', 'ledger-prose is-empty', 'Reading…'));
      return;
    }
    const facts = document.createElement('dl');
    facts.className = 'ledger-facts';
    fact(facts, 'base', short(task.baseSha) || '—');
    fact(facts, 'policy hash', short(task.acceptanceHash) || 'the project default');
    fact(facts, 'image', task.imageDigest ? short(task.imageDigest.replace('sha256:', '')) : 'not pinned yet');
    if (task.acceptanceRef) fact(facts, 'acceptance note', task.acceptanceRef);
    const b = task.budget || {};
    fact(facts, 'wall clock', b.wallClockSeconds ? b.wallClockSeconds + 's' : 'unbounded');
    fact(facts, 'attempts', (detail.jobs || []).length + ' used' +
      (b.maxAttempts ? ' of ' + b.maxAttempts : ' of unbounded'));
    fact(facts, 'review cycles', b.maxReviewCycles ? 'max ' + b.maxReviewCycles : 'unbounded');
    if (b.concurrency) fact(facts, 'concurrency', String(b.concurrency));
    host.appendChild(facts);

    host.appendChild(text('div', 'ledger-sub', 'Per-task checks'));
    const checks = task.checks || [];
    if (!checks.length) {
      host.appendChild(text('div', 'ledger-prose is-empty',
        'None. This task is graded on the project policy alone.'));
    } else {
      const ul = document.createElement('ul');
      ul.className = 'ledger-checks';
      checks.forEach(function (c) { ul.appendChild(text('li', 'ledger-check', c)); });
      host.appendChild(ul);
    }

    const s = detail.scheduling;
    if (s && (s.queuedForCapacity || s.running)) {
      host.appendChild(text('div', 'ledger-sub', 'With the scheduler'));
      const sf = document.createElement('dl');
      sf.className = 'ledger-facts';
      if (s.running) fact(sf, 'running', 'yes');
      if (s.queuedForCapacity) fact(sf, 'queued', 'position ' + (s.queuePosition || '?'));
      fact(sf, 'in flight', s.globalRunning + '/' +
        (s.limits && s.limits.global ? s.limits.global : '∞') + ' globally');
      host.appendChild(sf);
    }
  }

  // RECORD is the attempts and the append-only log, in that order: what was
  // tried, then what the plane wrote down about it.
  function paintRecord(host) {
    const jobs = (detail && detail.jobs) || [];
    host.appendChild(text('div', 'ledger-sub', 'Attempts'));
    if (!jobs.length) {
      host.appendChild(text('div', 'ledger-prose is-empty', 'Nothing has been attempted yet.'));
    } else {
      jobs.forEach(function (jv) {
        const j = jv.job;
        const row = document.createElement('div');
        row.className = 'ledger-log-row';
        row.appendChild(text('span', 'ledger-log-id', j.id));
        row.appendChild(text('span', 'ledger-log-what',
          j.state + (j.executionResult ? ' · ' + j.executionResult : '') +
          (j.outputSnapshot ? ' · ' + short(j.outputSnapshot) : '')));
        host.appendChild(row);
        (jv.artifacts || []).forEach(function (a) {
          const ar = document.createElement('div');
          ar.className = 'ledger-log-row is-sub';
          ar.appendChild(text('span', 'ledger-log-id', a.id));
          ar.appendChild(text('span', 'ledger-log-what',
            'verify ' + a.verify + ' · review ' + a.review + ' · ' + a.branch +
            (a.integratedSha ? ' → ' + short(a.integratedSha) : '')));
          host.appendChild(ar);
        });
        if (j.logPath) {
          const lp = text('div', 'ledger-logpath', j.logPath);
          lp.title = 'This Job’s own log on the host';
          host.appendChild(lp);
        }
        paintSteering(host, j.id);
      });
    }

    // The judgements, before the log. A review is evidence for the decision the
    // commands below are about, so it belongs where the deciding happens rather
    // than a click away — and it is ADVISORY, which the page says every time
    // because the plane used to act on it and an operator may remember that.
    const reviews = (detail && detail.reviews) || [];
    if (reviews.length) {
      host.appendChild(text('div', 'ledger-sub', 'Reviews'));
      reviews.forEach(function (r) {
        const head = document.createElement('div');
        head.className = 'ledger-log-row';
        head.appendChild(text('span', 'ledger-log-id', r.id));
        head.appendChild(text('span', 'ledger-log-what',
          (r.passed ? 'no blocker' : 'had concerns') + ' · ' + (r.reviewer || 'unattributed')));
        host.appendChild(head);
        if (r.reasoning) host.appendChild(text('div', 'ledger-log-note', r.reasoning));
        (r.findings || []).forEach(function (f) {
          const row = document.createElement('div');
          row.className = 'ledger-finding is-' + (f.severity || 'note');
          const where = f.file ? (f.line ? f.file + ':' + f.line : f.file) : '';
          row.appendChild(text('span', 'ledger-finding-sev', f.severity || 'note'));
          row.appendChild(text('span', 'ledger-finding-what',
            (where ? where + ' — ' : '') + f.what));
          host.appendChild(row);
          if (f.why) host.appendChild(text('div', 'ledger-log-note', f.why));
        });
      });
      host.appendChild(text('div', 'ledger-advisory',
        'Advisory. The plane acts on none of this — you decide.'));
    }

    host.appendChild(text('div', 'ledger-sub', 'The record'));
    const log = text('div', 'ledger-prose is-empty', 'Reading…');
    host.appendChild(log);
    get('/tasks/' + enc(current.id) + '/events').then(function (events) {
      if (!Array.isArray(events)) return;
      log.remove();
      if (!events.length) {
        host.appendChild(text('div', 'ledger-prose is-empty', 'Empty.'));
        return;
      }
      events.forEach(function (e) {
        const row = document.createElement('div');
        row.className = 'ledger-log-row';
        row.appendChild(text('span', 'ledger-log-id', e.entityId));
        const what = e.kind + (e.from || e.to ? ' · ' + (e.from || '—') + ' → ' + e.to : '') +
          (e.reason ? ' · ' + e.reason : '');
        row.appendChild(text('span', 'ledger-log-what', what));
        host.appendChild(row);
        if (e.note) host.appendChild(text('div', 'ledger-log-note', e.note));
      });
    }).catch(function () { log.textContent = 'The record could not be read.'; });
  }

  // A Job's steering history, under the Job it was aimed at. It is here rather
  // than beside the Steer command because an instruction's INTERESTING part is
  // what became of it: the shipped runner has no steering boundary, so most of
  // these read `undeliverable`, and a page that showed only "sent" would be
  // making the claim the subsystem deliberately refuses to make.
  //
  // Withdrawing is offered only on a PENDING one, which is the same window the
  // CLI's `task steer --withdraw` has: once an instruction is delivered there is
  // nothing left to take back.
  function paintSteering(host, jobID) {
    const anchor = text('div', 'ledger-logpath', '');
    host.appendChild(anchor);
    get('/jobs/' + enc(jobID) + '/steering').then(function (events) {
      if (!Array.isArray(events) || !events.length) {
        anchor.remove();
        return;
      }
      events.forEach(function (s) {
        const row = document.createElement('div');
        row.className = 'ledger-log-row is-sub';
        row.appendChild(text('span', 'ledger-log-id', s.id));
        row.appendChild(text('span', 'ledger-log-what',
          s.state + ' · ' + (s.issuedBy || '') + ' · ' + s.instruction));
        host.insertBefore(row, anchor);
        if (s.state === 'pending') {
          const wrap = document.createElement('div');
          wrap.className = 'ledger-commands is-inline';
          wrap.appendChild(plate('Withdraw ' + s.id, 'refuse', function () {
            runCommand({
              key: 'withdraw', label: 'Withdraw ' + s.id, confirm: true, danger: true,
              hint: 'Takes back an instruction that has not been delivered yet.',
              run: function () { return send('DELETE', '/steering/' + enc(s.id)); },
              done: function (r) { return r.id + ' is ' + r.state + '.'; },
            }, s.id);
          }));
          host.insertBefore(wrap, anchor);
        }
      });
      anchor.remove();
    }).catch(function () { anchor.remove(); });
  }

  function paintProposal() {
    const p = detail;
    const bodyEl = el('ledger-desc-body');
    el('ledger-desc-id').textContent = p.id;
    el('ledger-desc-project').textContent = p.taskId || '';
    const status = el('ledger-desc-status');
    status.textContent = p.state;
    status.className = 'ledger-desc-status ledger-row-status ' +
      (p.state === 'pending' ? 'is-waiting' : p.state === 'confirmed' ? 'is-passed' : 'is-refused');
    el('ledger-tabs').innerHTML = '';
    bodyEl.className = 'ledger-desc-body';
    bodyEl.innerHTML = '';
    bodyEl.appendChild(text('div', 'ledger-prose',
      'The ' + p.proposedBy + ' asked to ' + p.operation.replace(/_/g, ' ') +
      (p.taskId ? ' on ' + p.taskId : '') + '.'));
    const facts = document.createElement('dl');
    facts.className = 'ledger-facts';
    fact(facts, 'operation', p.operation);
    if (p.argument) fact(facts, 'argument', p.argument);
    fact(facts, 'proposed by', p.proposedBy);
    fact(facts, 'at', p.createdAt);
    if (p.detail) fact(facts, 'detail', p.detail);
    bodyEl.appendChild(facts);
    el('ledger-desc-note').textContent = p.state === 'pending'
      ? 'Confirming runs the operation. An agent cannot confirm its own proposal — that is what this queue is for.'
      : '';

    const cmds = el('ledger-commands');
    cmds.innerHTML = '';
    if (p.state === 'pending') {
      cmds.appendChild(plate('Confirm', 'seal', function () {
        runCommand({
          // No second confirmation: the prompt window IS the deliberate step
          // (click Confirm, then click OK), and stacking a Yes/No on top of it
          // only widened the window a poll could land in.
          key: 'confirm', label: 'Confirm',
          prompt: { title: 'Confirm', label: 'A note for the record (optional)', allowEmpty: true },
          run: function (id, note) { return send('POST', '/proposals/' + enc(id) + '/confirm', { note: note }); },
          done: function (r) { return r.id + ' is ' + r.state + '.'; },
        }, p.id);
      }));
      cmds.appendChild(plate('Deny', 'refuse', function () {
        runCommand({
          key: 'deny', label: 'Deny', danger: true,
          prompt: { title: 'Deny', label: 'Why (optional)', allowEmpty: true },
          run: function (id, note) { return send('POST', '/proposals/' + enc(id) + '/deny', { note: note }); },
          done: function (r) { return r.id + ' is ' + r.state + '.'; },
        }, p.id);
      }));
    }
    paintMessage();
    paintFoot();
  }

  // --- commands -------------------------------------------------------------

  function plate(label, kind, onClick) {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'ff-cmd' + (kind === 'refuse' ? ' is-refuse' : '') + (kind === 'seal' ? ' is-seal' : '');
    b.textContent = label;
    b.onclick = onClick;
    return b;
  }

  function paintCommands(state) {
    const host = el('ledger-commands');
    if (!host) return;
    host.innerHTML = '';
    if (!state) return;

    const runningJob = jobToSteer();
    COMMANDS.forEach(function (cmd) {
      if (cmd.states.indexOf(state) === -1) return;
      // Approve and reject are the one pair the board has a second opinion about:
      // a task can sit in the approval column while the plane is still settling
      // it, and the approvals endpoint is what confirms a human is genuinely the
      // next actor.
      if ((cmd.key === 'approve' || cmd.key === 'reject') &&
        currentKey === AWAITING_YOU && !approvable.has(current.id)) return;
      if (cmd.needsJob && !runningJob) return;

      const b = plate(cmd.label, cmd.danger ? 'refuse' : cmd.seal ? 'seal' : '', function () {
        runCommand(cmd, current.id, runningJob);
      });
      // The screen reader hears the object, not just the verb: there are a dozen
      // of these and "Retry" alone would not say what is being retried.
      b.setAttribute('aria-label', cmd.label + ' ' + current.id);
      if (cmd.hint) b.title = cmd.hint;
      b.disabled = busy;
      host.appendChild(b);
    });
  }

  // The Job a steer would reach. Steering is aimed at work that is RUNNING, so
  // this looks for a running Job rather than the newest one — a task can be
  // `working` with an older Job long since finished.
  function jobToSteer() {
    const jobs = (detail && detail.jobs) || [];
    for (let i = jobs.length - 1; i >= 0; i--) {
      const s = jobs[i].job.state;
      if (s === 'working' || s === 'queued' || s === 'input_required') return jobs[i].job.id;
    }
    return null;
  }

  // runCommand walks the three gates a command can have, in order: ask for a
  // value, ask whether you meant it, then do it. Each is optional and most
  // commands have none.
  function runCommand(cmd, id, jobID) {
    // Silence here was a trap. `busy` is cleared in a .then, so any throw between
    // raising it and that callback used to strand the ENTIRE command surface:
    // every later click returned quietly and only a page reload cured it, which
    // reads exactly like "the buttons do nothing". Both halves are fixed — this
    // says what it is doing, and execute() can no longer leave the flag raised.
    if (busy) {
      say('A command is already running; wait for it to answer.', 'is-refused');
      return;
    }
    if (cmd.prompt) {
      // A prompt that PRE-FILLS from the task must not open before the task has
      // been read. `checks` fills with the current checks and sends back exactly
      // what is in the box — opened early it would show an empty field, and an
      // operator pressing OK would clear checks they never saw.
      if (cmd.prompt.fill && !(detail && detail.task)) {
        say('Still reading ' + id + ' — try again in a moment.', '');
        return;
      }
      const fill = cmd.prompt.fill ? cmd.prompt.fill(detail.task) : '';
      askFor(cmd.prompt, fill, function (value) {
        if (!cmd.prompt.allowEmpty && !value.trim()) {
          say(cmd.label + ' needs something to act on.', 'is-refused');
          return;
        }
        maybeConfirm(cmd, function () { execute(cmd, id, value, jobID); });
      });
      return;
    }
    maybeConfirm(cmd, function () { execute(cmd, id, undefined, jobID); });
  }

  // A confirmation is a command plate turning into a question, in place. A
  // browser confirm() would work and would be the only thing on the page that
  // did not belong to it; more to the point, the question needs to name the
  // consequence, and confirm() gives one line with no room to.
  function maybeConfirm(cmd, then) {
    if (!cmd.confirm) return then();
    awaiting = true;
    const host = el('ledger-commands');
    host.innerHTML = '';
    const q = document.createElement('span');
    q.className = 'ledger-confirm';
    q.appendChild(text('span', 'ledger-confirm-q', cmd.label + '?'));
    if (cmd.hint) q.appendChild(text('span', 'ledger-confirm-why', cmd.hint));
    host.appendChild(q);
    const yes = plate('Yes', cmd.danger ? 'refuse' : '', function () { awaiting = false; then(); });
    const no = plate('No', '', function () { awaiting = false; paintEntry(); });
    host.appendChild(yes);
    host.appendChild(no);
    yes.focus();
  }

  function execute(cmd, id, value, jobID) {
    busy = true;
    paintCommands(currentState());
    say('…' + cmd.label, '');
    let started;
    try {
      started = cmd.run(id, value, jobID);
    } catch (e) {
      // A command whose run threw before it ever reached the network. Report it
      // and release the surface; the alternative is a page that has quietly
      // stopped accepting input.
      busy = false;
      say(cmd.label + ' could not be sent: ' + (e && e.message ? e.message : e), 'is-bad');
      paintEntry();
      return;
    }
    if (!started || typeof started.then !== 'function') {
      busy = false;
      say(cmd.label + ' returned nothing to wait on — this is a bug in the page.', 'is-bad');
      paintEntry();
      return;
    }
    started.then(function (result) {
      say(cmd.done ? cmd.done(result) : 'Done.', 'is-good');
    }).catch(function (err) {
      // A refusal is the plane working. Said differently from a failure, and
      // labelled with the reason code, because the reason is what tells you which
      // command to reach for next.
      if (err.refused) say('Refused · ' + (err.reason || '') + ' — ' + err.message, 'is-refused');
      else if (err.conflict) say('Not now — ' + err.message, 'is-refused');
      else say(err.message, 'is-bad');
    }).then(function () {
      busy = false;
      // Re-enable the commands from what we already know, then go and find out
      // what is actually true. Waiting for the round trip would leave every plate
      // greyed out for as long as a verify takes.
      paintEntry();
      if (current && current.kind === 'task') loadDetail(current.id);
      refreshApprovals();
      refreshProposals().then(refreshBoard);
    });
  }

  function currentState() {
    if (detail && detail.task) return detail.task.state;
    return currentCard ? currentCard.state : '';
  }

  function paintMessage() {
    const host = el('ledger-message');
    if (!host) return;
    host.className = 'ledger-message ' + (message ? message.kind : '');
    host.textContent = message ? message.text : '';
  }

  // --- the prompt window ----------------------------------------------------

  function askFor(spec, fill, then) {
    const overlay = el('ledger-prompt');
    const field = el('ledger-prompt-field');
    if (!overlay || !field) {
      say('This page is missing its prompt window — reload it (the assets may be stale).', 'is-bad');
      return;
    }
    el('ledger-prompt-title').textContent = spec.title;
    el('ledger-prompt-label').textContent = spec.label;
    field.innerHTML = '';

    const input = document.createElement(spec.multiline ? 'textarea' : 'input');
    input.className = 'ledger-input';
    if (!spec.multiline) input.type = 'text';
    if (spec.multiline) input.rows = 6;
    input.value = fill || '';
    field.appendChild(input);

    const cmds = el('ledger-prompt-commands');
    cmds.innerHTML = '';
    const accept = function () { close(); then(input.value); };
    const close = function () {
      awaiting = false;
      overlay.classList.remove('is-open');
      document.removeEventListener('keydown', onKey);
    };
    cmds.appendChild(plate('OK', 'seal', accept));
    cmds.appendChild(plate('Cancel', '', function () { close(); paintEntry(); }));

    // Escape closes, and Enter accepts a single-line field. A textarea keeps
    // Enter for what it is for; Ctrl+Enter accepts it, which is the convention
    // everywhere else a multi-line box has a submit.
    const onKey = function (e) {
      if (e.key === 'Escape') { close(); paintEntry(); }
      else if (e.key === 'Enter' && (!spec.multiline || e.ctrlKey || e.metaKey)) {
        e.preventDefault();
        accept();
      }
    };
    document.addEventListener('keydown', onKey);
    awaiting = true;
    overlay.classList.add('is-open');
    input.focus();
    input.select();
  }

  // --- creating a task ------------------------------------------------------

  // The one entry that does not exist yet. It gets its own window rather than a
  // command plate, because everything else on this page acts on a selected row
  // and this acts on nothing.
  function openNewTask() {
    const overlay = el('ledger-new');
    if (!overlay) return;
    overlay.classList.add('is-open');
    const project = el('new-project');
    // Projects come from the registry, not from the plane: a task can be created
    // for a project that has never had one.
    fetch('/api/projects').then(function (r) { return r.json(); }).then(function (list) {
      project.innerHTML = '';
      (list || []).forEach(function (p) {
        const opt = document.createElement('option');
        opt.value = p.name;
        opt.textContent = p.name;
        project.appendChild(opt);
      });
      if (current && current.kind === 'task' && currentCard) project.value = currentCard.project;
    }).catch(function () { /* the field stays empty and the plane will say so */ });
    el('new-objective').value = '';
    el('new-checks').value = '';
    el('new-wall-clock').value = '';
    el('new-attempts').value = '';
    el('new-review-cycles').value = '';
    el('new-error').textContent = '';
    el('new-objective').focus();
  }

  function closeNewTask() {
    const overlay = el('ledger-new');
    if (overlay) overlay.classList.remove('is-open');
  }

  function submitNewTask() {
    const objective = el('new-objective').value.trim();
    const err = el('new-error');
    if (!objective) {
      err.textContent = 'An objective is what the task IS; there is nothing to create without one.';
      return;
    }
    const req = { project: el('new-project').value, objective: objective };
    const checks = el('new-checks').value.split('\n')
      .map(function (s) { return s.trim(); }).filter(function (s) { return s.length; });
    if (checks.length) req.checks = checks;

    // A budget axis is sent only when it was typed. An empty field means "inherit
    // the project ceiling", and sending 0 for it would mean something else
    // entirely — 0 reads as unbounded on some axes and is refused on others.
    const budget = {};
    const num = function (id, key) {
      const v = el(id).value.trim();
      if (v !== '') budget[key] = parseInt(v, 10);
    };
    num('new-wall-clock', 'wallClockSeconds');
    num('new-attempts', 'maxAttempts');
    num('new-review-cycles', 'maxReviewCycles');
    if (Object.keys(budget).length) req.budget = budget;

    err.textContent = '';
    send('POST', '/tasks', req).then(function (task) {
      closeNewTask();
      say(task.id + ' created for ' + task.project + '.', 'is-good');
      refreshBoard();
    }).catch(function (e) {
      err.textContent = e.refused ? (e.reason || 'refused') + ' — ' + e.message : e.message;
    });
  }

  // --- the foot: the message, and the target the work would land on ---------

  function paintFoot() {
    const host = el('ledger-target');
    if (!host) return;
    host.innerHTML = '';
    const project = currentCard ? currentCard.project :
      (detail && detail.task ? detail.task.project : null);
    if (!project) return;

    const target = targets.filter(function (t) {
      return (t.projects || []).indexOf(project) !== -1;
    })[0];
    if (!target) return;

    host.appendChild(text('span', 'ledger-target-label', 'target'));
    host.appendChild(text('span', 'ledger-target-value',
      short(target.sha) + ' · ' + (target.repoPath || target.queueId)));
    host.appendChild(plate('Sync', '', function () {
      runCommand({
        key: 'sync', label: 'Sync target', confirm: true,
        hint: 'Re-points the integration target at the project checkout’s current HEAD.',
        run: function () { return send('POST', '/targets/' + enc(project) + '/sync', {}); },
        done: function (t) { return 'Target for ' + project + ' is now ' + short(t.sha) + '.'; },
      }, project);
    }));
  }

  // --- polling --------------------------------------------------------------

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
    return get('/board').then(renderBoard).catch(function () { renderBoard(null); });
  }

  function refreshApprovals() {
    return get('/approvals').then(renderApprovals).catch(function () { renderApprovals(null); });
  }

  function refreshProposals() {
    return get('/proposals?state=pending').then(function (data) {
      proposals = (data && data.available && data.proposals) || [];
    }).catch(function () { proposals = []; });
  }

  function refreshTargets() {
    return get('/targets').then(function (data) {
      targets = (data && data.available && data.targets) || [];
      paintFoot();
    }).catch(function () { targets = []; });
  }

  function refreshArchive() {
    if (!showArchive) return Promise.resolve();
    return get('/tasks').then(function (data) {
      const terminal = { integrated: 1, cancelled: 1, failed: 1, expired: 1 };
      archive = ((data && data.available && data.tasks) || [])
        .filter(function (t) { return terminal[t.state]; })
        .reverse();
    }).catch(function () { archive = []; });
  }

  function toggleArchive() {
    showArchive = !showArchive;
    const btn = el('ledger-archive-btn');
    if (btn) {
      btn.setAttribute('aria-pressed', showArchive ? 'true' : 'false');
      btn.classList.toggle('is-current', showArchive);
    }
    refreshArchive().then(refreshBoard);
  }

  // Polling starts when the view opens and stops when it closes: the Ledger is
  // one view among several and a hidden page should not keep asking the daemon
  // questions nobody is reading.
  function start() {
    stop();
    const cycle = function () {
      return Promise.all([refreshProposals(), refreshArchive()]).then(refreshBoard);
    };
    refreshApprovals();
    refreshTargets();
    cycle();
    timers.push(setInterval(cycle, BOARD_MS));
    timers.push(setInterval(refreshApprovals, APPROVALS_MS));
    timers.push(setInterval(refreshTargets, BOARD_MS));
  }

  function stop() {
    timers.forEach(clearInterval);
    timers = [];
  }

  // A page that breaks should SAY it broke.
  //
  // Everything on this screen is driven by click handlers, and an exception in
  // one is swallowed by the browser: the button appears to do nothing, and the
  // operator has no way to tell "nothing happened" from "something failed". That
  // ambiguity cost a whole round of diagnosis, and it is the same ambiguity the
  // control plane spends so much effort on elsewhere — an unreachable plane is
  // not an empty one, a refusal is not a failure. The page should hold itself to
  // the standard it holds the plane to.
  //
  // Bound once, and only speaks while the Ledger is the visible view, so it never
  // reports another screen's problem in this screen's message line.
  function reportPageError(what) {
    const view = el('control-view');
    if (!view || !view.classList.contains('active')) return;
    busy = false;   // whatever broke, do not leave the surface locked
    awaiting = false;
    say('The page hit an error: ' + what + ' — please report it; a reload will unstick it.', 'is-bad');
  }

  window.addEventListener('error', function (e) {
    reportPageError((e && e.message) || 'unknown error');
  });
  window.addEventListener('unhandledrejection', function (e) {
    const r = e && e.reason;
    reportPageError((r && r.message) || String(r || 'unknown rejection'));
  });

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
    closeNewTask();
    // Leaving the view abandons whatever was half-answered. Clearing the flag
    // matters: carried into the next visit it would freeze the entry forever,
    // and a stuck page is a worse bug than the one this guard fixes.
    awaiting = false;
    const overlay = el('ledger-prompt');
    if (overlay) overlay.classList.remove('is-open');
    stop();
  };

  window.ledgerNewTask = openNewTask;
  window.ledgerCloseNewTask = closeNewTask;
  window.ledgerSubmitNewTask = submitNewTask;
  window.ledgerToggleArchive = toggleArchive;
})();
