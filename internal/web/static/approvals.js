// Copyright (C) 2026 Techdelight BV
//
// The pending-approvals panel: the Web half of the control plane's human
// approval gate (docs/control-plane.md).
//
// It is deliberately minimal and read-mostly. The panel is HIDDEN — not shown
// empty — when the control daemon is not running, because an empty queue and an
// unreachable queue mean very different things and only one of them means
// "nothing needs you".

(function () {
  const POLL_MS = 5000;

  function el(id) { return document.getElementById(id); }

  function render(data) {
    const panel = el('approvals-panel');
    const list = el('approvals-list');
    if (!panel || !list) return;

    if (!data.available || !data.tasks || data.tasks.length === 0) {
      panel.style.display = 'none';
      return;
    }
    panel.style.display = '';
    list.innerHTML = '';
    data.tasks.forEach(function (task) {
      const row = document.createElement('div');
      row.className = 'approval-row';

      const who = document.createElement('span');
      who.className = 'approval-id';
      who.textContent = task.id + ' · ' + task.project;

      const what = document.createElement('span');
      what.className = 'approval-objective';
      what.textContent = task.objective;

      const approve = document.createElement('button');
      approve.className = 'btn btn-approve';
      approve.textContent = 'Approve';
      approve.onclick = function () { decide(task.id, 'approve', approve); };

      const reject = document.createElement('button');
      reject.className = 'btn btn-reject';
      reject.textContent = 'Reject';
      reject.onclick = function () { decide(task.id, 'reject', reject); };

      row.appendChild(who);
      row.appendChild(what);
      row.appendChild(approve);
      row.appendChild(reject);
      list.appendChild(row);
    });
  }

  function decide(id, action, button) {
    // Disable both buttons in the row so a double-click cannot send two
    // decisions; the refresh below restores the true state either way.
    const row = button.parentElement;
    Array.prototype.forEach.call(row.querySelectorAll('button'), function (b) { b.disabled = true; });

    const note = window.prompt(
      action === 'approve' ? 'Note for the approval (optional):' : 'Why is this rejected? (optional)'
    );
    if (note === null) {
      Array.prototype.forEach.call(row.querySelectorAll('button'), function (b) { b.disabled = false; });
      return;
    }
    fetch('/api/approvals/' + encodeURIComponent(id) + '/' + action, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ note: note })
    }).then(function (res) {
      if (!res.ok) {
        return res.json().catch(function () { return {}; }).then(function (body) {
          // A 422 is the control plane refusing on policy grounds — a real answer,
          // not a failure, so it is shown as such.
          throw new Error(body.error || ('control plane returned ' + res.status));
        });
      }
      return null;
    }).catch(function (err) {
      window.alert(err.message);
    }).then(refresh);
  }

  function refresh() {
    fetch('/api/approvals')
      .then(function (res) { return res.json(); })
      .then(render)
      .catch(function () {
        // The dashboard must keep working when the control plane is not there.
        const panel = el('approvals-panel');
        if (panel) panel.style.display = 'none';
      });
  }

  document.addEventListener('DOMContentLoaded', function () {
    refresh();
    setInterval(refresh, POLL_MS);
  });
})();
