// Copyright (C) 2026 Techdelight BV
//
// The programme board panel: the Web half of the cross-project control-plane view
// (docs/control-plane.md, M17).
//
// It is READ-ONLY. Every action a card might tempt you into — approve, cancel,
// integrate — already has a surface with the right confirmations attached, and a
// board that could also act would be a second place for those decisions to be
// made. This one answers "what is happening"; the approvals panel below it
// answers "what needs me".
//
// Like the approvals panel it HIDES itself when the control daemon is not
// running, rather than rendering an empty board — "nothing is running" and "I
// could not ask" are different answers and only one of them is reassuring.

(function () {
  // Slower than the approvals panel on purpose: building the board resolves each
  // registered project's canonical repository path, which is a git call per
  // project. The board changes on the scale of a Job, not a keystroke.
  const POLL_MS = 15000;

  function el(id) { return document.getElementById(id); }

  function card(c) {
    const row = document.createElement('div');
    row.className = 'board-card';

    const id = document.createElement('span');
    id.className = 'board-card-id';
    id.textContent = c.taskId + ' · ' + c.project;

    const what = document.createElement('span');
    what.className = 'board-card-objective';
    what.textContent = c.objective;

    row.appendChild(id);
    row.appendChild(what);

    const notes = [];
    if (c.blockedOn && c.blockedOn.length) {
      notes.push('waiting on ' + c.blockedOn.join(' '));
    }
    if (c.unsatisfiable && c.unsatisfiable.length) {
      // Said plainly: this one is not going to start by waiting.
      notes.push('stuck — ' + c.unsatisfiable.join(' ') + ' can never complete');
    }
    if (c.queuedForCapacity) {
      notes.push('holding a place in line for a free slot');
    }
    if (c.steering) {
      notes.push('steering ' + c.steering);
    }
    if (notes.length) {
      const note = document.createElement('div');
      note.className = 'board-card-note';
      note.textContent = notes.join(' · ');
      row.appendChild(note);
    }
    return row;
  }

  function render(data) {
    const panel = el('board-panel');
    const cols = el('board-columns');
    const summary = el('board-summary');
    if (!panel || !cols) return;

    if (!data.available || !data.columns || !data.columns.length) {
      panel.style.display = 'none';
      return;
    }
    panel.style.display = '';
    if (summary) {
      const limit = data.globalLimit > 0 ? String(data.globalLimit) : '∞';
      summary.textContent = data.globalRunning + '/' + limit + ' jobs running · ' +
        data.pendingApprovals + ' awaiting approval · ' +
        data.pendingProposals + ' proposals pending';
    }
    cols.innerHTML = '';
    data.columns.forEach(function (col) {
      const box = document.createElement('div');
      box.className = 'board-column';

      const head = document.createElement('div');
      head.className = 'board-column-title';
      head.textContent = col.title + ' (' + col.cards.length + ')';
      box.appendChild(head);

      if (!col.cards.length) {
        // Rendered, not skipped: "nothing is blocked" is information.
        const none = document.createElement('div');
        none.className = 'board-card-note';
        none.textContent = '—';
        box.appendChild(none);
      } else {
        col.cards.forEach(function (c) { box.appendChild(card(c)); });
      }
      cols.appendChild(box);
    });
  }

  function refresh() {
    fetch('/api/board')
      .then(function (res) { return res.json(); })
      .then(render)
      .catch(function () {
        const panel = el('board-panel');
        if (panel) panel.style.display = 'none';
      });
  }

  document.addEventListener('DOMContentLoaded', function () {
    refresh();
    setInterval(refresh, POLL_MS);
  });
})();
