// Copyright (C) 2026 Techdelight BV

// terminal.js — xterm.js + WebSocket connection for Daedalus web UI
//
// The terminal bridges to the in-container daedalus-runner over a Unix-socket
// relay (?mode=runner is the only mode). The runner replays its screen on
// attach via its hello frame, so there is no client-driven scrollback/capture
// protocol: any non-resize text frame is forwarded straight to the PTY as
// input.

let term = null;
let ws = null;
let fitAddon = null;
let cleanupListeners = null;

// #29 mobile-WebSocket resilience: a dropped socket (Wi-Fi/cellular handoff,
// backgrounded/throttled tab) auto-reconnects with backoff. The server keeps
// the session alive across a drop and replays the screen on re-attach (the
// runner hello frame), so a reconnect repaints for free. An intentional close
// (navigation via disconnectTerminal) suppresses reconnect.
let intentionalClose = false;
let reconnectAttempts = 0;
let reconnectTimer = null;
let currentProject = null;
let reopenSocket = null;

function scheduleReconnect() {
    if (intentionalClose || reconnectTimer || !reopenSocket) return;
    reconnectAttempts++;
    var delay = Math.min(1000 * Math.pow(2, reconnectAttempts - 1), 15000);
    if (term) term.write('\r\n\x1b[33m[Connection lost — reconnecting…]\x1b[0m\r\n');
    reconnectTimer = setTimeout(function() {
        reconnectTimer = null;
        reopenSocket();
    }, delay);
}

// Reconnect immediately when a backgrounded tab returns or the network comes
// back, if the socket isn't already open/connecting. Resets the backoff.
function maybeReconnect() {
    if (intentionalClose || !reopenSocket || !term) return;
    if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) return;
    if (reconnectTimer) { clearTimeout(reconnectTimer); reconnectTimer = null; }
    reconnectAttempts = 0;
    reopenSocket();
}

document.addEventListener('visibilitychange', function() {
    if (document.visibilityState === 'visible') maybeReconnect();
});
window.addEventListener('online', maybeReconnect);

function isMobileView() {
    return window.matchMedia('(max-width: 768px)').matches;
}

function connectTerminal(projectName) {
    const container = document.getElementById('terminal-container');
    container.innerHTML = '';

    term = new Terminal({
        cursorBlink: true,
        fontSize: 14,
        scrollback: 10000,
        fontFamily: "'SF Mono', 'Monaco', 'Inconsolata', 'Fira Code', monospace",
        theme: {
            background: '#1a1b26',
            foreground: '#c0caf5',
            cursor: '#c0caf5',
            selectionBackground: '#33467c',
            black: '#15161e',
            red: '#f7768e',
            green: '#9ece6a',
            yellow: '#e0af68',
            blue: '#7aa2f7',
            magenta: '#bb9af7',
            cyan: '#7dcfff',
            white: '#a9b1d6',
            brightBlack: '#414868',
            brightRed: '#f7768e',
            brightGreen: '#9ece6a',
            brightYellow: '#e0af68',
            brightBlue: '#7aa2f7',
            brightMagenta: '#bb9af7',
            brightCyan: '#7dcfff',
            brightWhite: '#c0caf5'
        }
    });

    fitAddon = new FitAddon.FitAddon();
    term.loadAddon(fitAddon);

    term.open(container);
    fitAddon.fit();
    requestAnimationFrame(function() { if (fitAddon) fitAddon.fit(); });

    currentProject = projectName;
    intentionalClose = false;
    reconnectAttempts = 0;

    // openSocket is (re)called on every (re)connect; it rebinds the module
    // `ws`. term.onData/onResize below reference module `ws`, so they keep
    // working across reconnects without rebinding.
    function openSocket() {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${proto}//${location.host}/api/projects/${encodeURIComponent(projectName)}/terminal`;
    ws = new WebSocket(wsUrl);
    ws.binaryType = 'arraybuffer';

    ws.onopen = function() {
        reconnectAttempts = 0;
        // Send initial size. The runner replays the screen on attach via its
        // hello frame, so nothing else needs requesting here.
        ws.send(JSON.stringify({
            type: 'resize',
            cols: term.cols,
            rows: term.rows
        }));
    };

    ws.onmessage = function(event) {
        if (event.data instanceof ArrayBuffer) {
            term.write(new Uint8Array(event.data));
        } else if (typeof event.data === 'string') {
            try {
                var msg = JSON.parse(event.data);
                // Server-pushed git branch: sent on attach and whenever the
                // branch changes, so the header needs no polling. Guarded
                // because terminal.js is also loaded by pages without the
                // session header.
                if (msg.type === 'branch') {
                    if (typeof setGitBranch === 'function') setGitBranch(msg.branch);
                    return;
                }
            } catch (e) { /* not JSON, treat as terminal data */ }
            term.write(event.data);
        }
    };

    ws.onclose = function() {
        if (intentionalClose) {
            if (term) term.write('\r\n\x1b[33m[Connection closed]\x1b[0m\r\n');
            return;
        }
        scheduleReconnect();
    };

    ws.onerror = function() {
        // A close event always follows an error; scheduleReconnect runs there.
    };
    }

    reopenSocket = openSocket;
    openSocket();

    // Forward input to WebSocket
    term.onData(function(data) {
        if (ws && ws.readyState === WebSocket.OPEN) {
            ws.send(new TextEncoder().encode(data));
        }
    });

    // Handle resize
    term.onResize(function(size) {
        if (ws && ws.readyState === WebSocket.OPEN) {
            ws.send(JSON.stringify({
                type: 'resize',
                cols: size.cols,
                rows: size.rows
            }));
        }
    });

    // Named handlers for cleanup
    function applyMobileMode(mobile) {
        term.options.disableStdin = mobile;
        if (term.textarea) {
            term.textarea.disabled = mobile;
        }
    }

    function onWindowResize() {
        if (fitAddon) {
            fitAddon.fit();
        }
        if (term) {
            applyMobileMode(isMobileView());
        }
    }

    window.addEventListener('resize', onWindowResize);

    // Touch scrolling. xterm's viewport does not reliably scroll via touch on
    // phones (notably iOS Safari), and on mobile stdin is disabled (input goes
    // through the Send box), so touch on the terminal is free to drive
    // scrollback. Translate single-finger vertical drags into scrollLines and
    // preventDefault so the page never scrolls instead. `touch-action: none`
    // on #terminal-container (mobile CSS) hands us the gesture cleanly; the
    // listeners are inert on desktop, where there are no touch events.
    var touchLastY = 0;
    var touchAccum = 0;
    function onTerminalTouchStart(e) {
        if (e.touches.length !== 1) return;
        touchLastY = e.touches[0].clientY;
        touchAccum = 0;
    }
    function onTerminalTouchMove(e) {
        if (!term || e.touches.length !== 1) return;
        var y = e.touches[0].clientY;
        // Finger moving up (y decreasing) accumulates positive → scroll toward
        // newer lines; moving down scrolls back toward older output.
        touchAccum += touchLastY - y;
        touchLastY = y;
        var rowPx = (container.clientHeight / (term.rows || 24)) || 20;
        var lines = Math.trunc(touchAccum / rowPx);
        if (lines !== 0) {
            term.scrollLines(lines);
            touchAccum -= lines * rowPx;
        }
        e.preventDefault();
    }
    container.addEventListener('touchstart', onTerminalTouchStart, { passive: true });
    container.addEventListener('touchmove', onTerminalTouchMove, { passive: false });

    // Mobile select mode
    var mobileSelectBtn = document.getElementById('mobile-select-btn');
    var selectOverlay = document.getElementById('select-overlay');
    var selectOverlayText = document.getElementById('select-overlay-text');
    var selectDoneBtn = document.getElementById('select-done-btn');

    function getBufferText() {
        if (!term) return '';
        var buf = term.buffer.active;
        var lines = [];
        for (var i = 0; i < buf.length; i++) {
            var line = buf.getLine(i);
            if (line) lines.push(line.translateToString());
        }
        // Trim trailing empty lines
        while (lines.length > 0 && lines[lines.length - 1].trim() === '') {
            lines.pop();
        }
        return lines.join('\n');
    }

    function enterSelectMode() {
        selectOverlayText.textContent = getBufferText();
        selectOverlay.classList.add('active');
        mobileSelectBtn.classList.add('active');
    }

    function exitSelectMode() {
        selectOverlay.classList.remove('active');
        mobileSelectBtn.classList.remove('active');
        selectOverlayText.textContent = '';
    }

    function toggleSelectMode() {
        if (selectOverlay.classList.contains('active')) {
            exitSelectMode();
        } else {
            enterSelectMode();
        }
    }

    function onSelectTouch(e) { e.preventDefault(); toggleSelectMode(); }
    function onDoneTouch(e) { e.preventDefault(); exitSelectMode(); }

    mobileSelectBtn.addEventListener('touchend', onSelectTouch);
    mobileSelectBtn.addEventListener('click', toggleSelectMode);
    selectDoneBtn.addEventListener('touchend', onDoneTouch);
    selectDoneBtn.addEventListener('click', exitSelectMode);

    // Milestones overlay (mobile). The ⚑ header button opens a full-screen
    // list; Done closes it. The list itself is populated by loadMilestones
    // (shared with the desktop sidebar), so opening is instant.
    var mobileMilestonesBtn = document.getElementById('mobile-milestones-btn');
    var milestonesOverlay = document.getElementById('milestones-overlay');
    var milestonesDoneBtn = document.getElementById('milestones-done-btn');

    function openMilestones() { if (milestonesOverlay) milestonesOverlay.classList.add('active'); }
    function closeMilestones() { if (milestonesOverlay) milestonesOverlay.classList.remove('active'); }
    function onMilestonesTouch(e) { e.preventDefault(); openMilestones(); }
    function onMilestonesDoneTouch(e) { e.preventDefault(); closeMilestones(); }

    mobileMilestonesBtn.addEventListener('touchend', onMilestonesTouch);
    mobileMilestonesBtn.addEventListener('click', openMilestones);
    milestonesDoneBtn.addEventListener('touchend', onMilestonesDoneTouch);
    milestonesDoneBtn.addEventListener('click', closeMilestones);

    // Mobile input wiring
    var mobileInput = document.getElementById('mobile-input');
    var mobileSendBtn = document.getElementById('mobile-send-btn');

    function sendMobileInput() {
        var text = mobileInput.value;
        if (text.length === 0) return;
        if (ws && ws.readyState === WebSocket.OPEN) {
            // Text first, then Enter as its own frame on the same socket.
            // Frames are ordered, so the submit cannot overtake the text.
            // Claude Code treats a chunk ending in a newline as a paste and
            // inserts a line break instead of submitting, so the Enter must
            // arrive as its own frame; the relay turns it into a \r write.
            ws.send(new TextEncoder().encode(text));
            ws.send(JSON.stringify({ type: 'enter' }));
        }
        mobileInput.value = '';
        mobileInput.style.height = 'auto';
    }

    function onMobileSendClick() {
        sendMobileInput();
    }

    function onMobileSendTouch(e) {
        e.preventDefault();
        sendMobileInput();
    }

    function onMobileKeydown(e) {
        if (e.ctrlKey && e.key === 'Enter') {
            e.preventDefault();
            sendMobileInput();
        }
    }

    function onMobileInput() {
        this.style.height = 'auto';
        this.style.height = Math.min(this.scrollHeight, 120) + 'px';
    }

    mobileSendBtn.addEventListener('touchend', onMobileSendTouch);
    mobileSendBtn.addEventListener('click', onMobileSendClick);
    mobileInput.addEventListener('keydown', onMobileKeydown);
    mobileInput.addEventListener('input', onMobileInput);

    if (isMobileView()) {
        applyMobileMode(true);
    }

    // Store cleanup function for disconnectTerminal
    cleanupListeners = function() {
        window.removeEventListener('resize', onWindowResize);
        container.removeEventListener('touchstart', onTerminalTouchStart);
        container.removeEventListener('touchmove', onTerminalTouchMove);
        exitSelectMode();
        mobileSelectBtn.removeEventListener('touchend', onSelectTouch);
        mobileSelectBtn.removeEventListener('click', toggleSelectMode);
        selectDoneBtn.removeEventListener('touchend', onDoneTouch);
        selectDoneBtn.removeEventListener('click', exitSelectMode);
        closeMilestones();
        mobileMilestonesBtn.removeEventListener('touchend', onMilestonesTouch);
        mobileMilestonesBtn.removeEventListener('click', openMilestones);
        milestonesDoneBtn.removeEventListener('touchend', onMilestonesDoneTouch);
        milestonesDoneBtn.removeEventListener('click', closeMilestones);
        mobileSendBtn.removeEventListener('touchend', onMobileSendTouch);
        mobileSendBtn.removeEventListener('click', onMobileSendClick);
        mobileInput.removeEventListener('keydown', onMobileKeydown);
        mobileInput.removeEventListener('input', onMobileInput);
    };
}

function disconnectTerminal() {
    intentionalClose = true;
    if (reconnectTimer) { clearTimeout(reconnectTimer); reconnectTimer = null; }
    currentProject = null;
    reopenSocket = null;
    if (cleanupListeners) {
        cleanupListeners();
        cleanupListeners = null;
    }
    if (ws) {
        ws.close();
        ws = null;
    }
    if (term) {
        term.dispose();
        term = null;
    }
    fitAddon = null;

    var mobileInput = document.getElementById('mobile-input');
    if (mobileInput) {
        mobileInput.value = '';
        mobileInput.style.height = 'auto';
    }
}
