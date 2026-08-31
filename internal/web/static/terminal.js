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

// #56 chat font size. The A−/A+ header buttons scale ONLY the terminal text —
// not the surrounding UI — and the terminal container keeps its dimensions:
// changing term.options.fontSize then re-fitting recomputes the cols/rows grid
// to fill the same fixed box (and forwards the new size to the PTY via
// onResize), so a bigger font means fewer cells, never a bigger window. The
// choice is persisted across reconnects and page loads.
var FONT_SIZE_KEY = 'daedalus.chatFontSize';
var FONT_SIZE_DEFAULT = 14;
var FONT_SIZE_MIN = 8; // floor only — no upper cap, the user can grow the text freely

function loadChatFontSize() {
    var n = parseInt(localStorage.getItem(FONT_SIZE_KEY), 10);
    if (isNaN(n)) return FONT_SIZE_DEFAULT;
    return Math.max(FONT_SIZE_MIN, n);
}

function saveChatFontSize(n) {
    try { localStorage.setItem(FONT_SIZE_KEY, String(n)); } catch (e) { /* private mode: keep session-local */ }
}

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
        fontSize: loadChatFontSize(),
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

    // LINKS. Without this a URL in the output is just characters in a cell grid
    // — there is no anchor to tap, on any device. It matters most on a phone,
    // where the drag-select-and-copy fallback a desktop has is not available:
    // `claude /login` prints an OAuth URL and there was no way to follow it.
    // Guarded because it is not essential to the terminal working, and a CDN
    // that fails to serve it should cost links rather than the whole session.
    if (window.WebLinksAddon && WebLinksAddon.WebLinksAddon) {
        term.loadAddon(new WebLinksAddon.WebLinksAddon());
    }

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
    // scrollback. We move xterm's own .xterm-viewport by the finger delta in
    // real pixels — driving it directly means the finger-to-content ratio uses
    // xterm's true cell height. (A rows-based estimate — container height ÷
    // term.rows — runs slightly large because the container has a sub-row
    // leftover strip, and that error compounds the further you scroll, which
    // reads as scrolling that degrades as more output builds up.) preventDefault
    // stops the page from scrolling too; `touch-action: none` on
    // #terminal-container (mobile CSS) hands us the gesture cleanly. The
    // listeners are inert on desktop, where there are no touch events.
    var touchLastY = 0;
    var xtermViewport = null;
    function terminalViewport() {
        // Resolved lazily and re-resolved if detached: term.open() builds
        // .xterm-viewport inside the container, and a reconnect rebuilds it.
        if (!xtermViewport || !xtermViewport.isConnected) {
            xtermViewport = container.querySelector('.xterm-viewport');
        }
        return xtermViewport;
    }
    function onTerminalTouchStart(e) {
        if (e.touches.length !== 1) return;
        touchLastY = e.touches[0].clientY;
    }
    function onTerminalTouchMove(e) {
        if (e.touches.length !== 1) return;
        var vp = terminalViewport();
        if (!vp) return;
        var y = e.touches[0].clientY;
        // Finger up (y decreasing) → positive delta → scrollTop grows → scroll
        // toward newer lines; finger down scrolls back toward older output.
        vp.scrollTop += touchLastY - y;
        touchLastY = y;
        e.preventDefault();
    }
    container.addEventListener('touchstart', onTerminalTouchStart, { passive: true });
    container.addEventListener('touchmove', onTerminalTouchMove, { passive: false });

    // Mobile select mode
    var mobileSelectBtn = document.getElementById('mobile-select-btn');
    var selectOverlay = document.getElementById('select-overlay');
    var selectOverlayText = document.getElementById('select-overlay-text');
    var selectDoneBtn = document.getElementById('select-done-btn');

    // WHAT THE READER COPIES IS A LOGICAL LINE, NOT A ROW.
    //
    // This used to be `lines.push(line.translateToString())` over every row,
    // joined with "\n", and it broke long output in two separate ways.
    //
    // 1. A line longer than the terminal is stored as SEVERAL ROWS, and every
    //    row after the first carries `isWrapped`. Joining those with a newline
    //    turns a soft wrap into a real newline character — so an OAuth URL
    //    copied off a phone came back with breaks through the middle of it and
    //    would not paste. The narrower the terminal the worse it got, which is
    //    why it showed up on mobile and not on a desktop.
    // 2. `translateToString()` defaults `trimRight` to false, so every row is
    //    padded out to `cols` with spaces, and those spaces are copied too.
    //
    // Measured at cols=40 on a 312-character URL: 7 newlines and a run of
    // padding. Every character of the URL was present — the buffer was fine and
    // only the extraction was wrong — which is what `e2e/ledger.py` asserts by
    // round-tripping the URL rather than by counting rows.
    function getBufferText() {
        if (!term) return '';
        var buf = term.buffer.active;
        var lines = [];
        for (var i = 0; i < buf.length; i++) {
            var line = buf.getLine(i);
            if (!line) continue;
            var text = line.translateToString(true);
            if (lines.length > 0 && line.isWrapped) {
                lines[lines.length - 1] += text; // continuation of the row above
            } else {
                lines.push(text);
            }
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

    // Chat font-size controls (#56). Adjust term.options.fontSize within the
    // clamp, then re-fit so the container keeps its size (fewer/more cells, same
    // box) and the PTY is resized via onResize. Persisted for next time.
    var fontDecreaseBtn = document.getElementById('font-decrease-btn');
    var fontIncreaseBtn = document.getElementById('font-increase-btn');

    function setChatFontSize(size) {
        var clamped = Math.max(FONT_SIZE_MIN, size);
        if (!term || clamped === term.options.fontSize) return;
        term.options.fontSize = clamped;
        saveChatFontSize(clamped);
        if (fitAddon) fitAddon.fit();
    }
    function onFontDecrease() { if (term) setChatFontSize(term.options.fontSize - 1); }
    function onFontIncrease() { if (term) setChatFontSize(term.options.fontSize + 1); }

    if (fontDecreaseBtn) fontDecreaseBtn.addEventListener('click', onFontDecrease);
    if (fontIncreaseBtn) fontIncreaseBtn.addEventListener('click', onFontIncrease);

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

    // THE KEY ROW — Esc, Tab, Enter.
    //
    // On a phone `applyMobileMode` sets `disableStdin` and disables xterm's own
    // textarea, so the ONLY way in is #mobile-input. That is fine for
    // characters and useless for keys that are not characters: a soft keyboard
    // has no Esc at all, its Tab inserts a tab into the textarea, and its
    // Return adds a line break. So Claude Code's prompts — cancel, cycle mode,
    // confirm — were unreachable from a phone.
    //
    // These send the key to the PTY and DO NOT touch the textarea. Send stays
    // "post what I typed, then Enter", which is a different act: Enter here
    // answers a dialog, Send submits a message. Tab completion of half-typed
    // text is not possible either way, because the PTY never sees the textarea
    // until Send flushes it.
    //
    // The relay forwards any non-control frame straight to the PTY
    // (runner_relay.go readWebSocket), so a raw byte is the whole protocol.
    // Enter reuses the relay's own `enter` control frame — the one Send uses —
    // rather than a second way of saying \r.
    var keyEscBtn = document.getElementById('key-esc-btn');
    var keyTabBtn = document.getElementById('key-tab-btn');
    var keyEnterBtn = document.getElementById('key-enter-btn');

    function sendBytes(s) {
        if (ws && ws.readyState === WebSocket.OPEN) {
            ws.send(new TextEncoder().encode(s));
        }
    }
    function sendEsc() { sendBytes('\x1b'); }
    function sendTab() { sendBytes('\t'); }
    function sendEnter() {
        if (ws && ws.readyState === WebSocket.OPEN) {
            ws.send(JSON.stringify({ type: 'enter' }));
        }
    }

    // touchend + preventDefault is the file's existing idiom for a mobile
    // button, and it earns its place twice here: it suppresses the synthesized
    // click (so the key is not sent twice) AND it stops focus leaving
    // #mobile-input, which would collapse the soft keyboard on every keypress.
    function onEscTouch(e) { e.preventDefault(); sendEsc(); }
    function onTabTouch(e) { e.preventDefault(); sendTab(); }
    function onEnterTouch(e) { e.preventDefault(); sendEnter(); }

    mobileSendBtn.addEventListener('touchend', onMobileSendTouch);
    mobileSendBtn.addEventListener('click', onMobileSendClick);
    mobileInput.addEventListener('keydown', onMobileKeydown);
    mobileInput.addEventListener('input', onMobileInput);
    keyEscBtn.addEventListener('touchend', onEscTouch);
    keyEscBtn.addEventListener('click', sendEsc);
    keyTabBtn.addEventListener('touchend', onTabTouch);
    keyTabBtn.addEventListener('click', sendTab);
    keyEnterBtn.addEventListener('touchend', onEnterTouch);
    keyEnterBtn.addEventListener('click', sendEnter);

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
        if (fontDecreaseBtn) fontDecreaseBtn.removeEventListener('click', onFontDecrease);
        if (fontIncreaseBtn) fontIncreaseBtn.removeEventListener('click', onFontIncrease);
        mobileSendBtn.removeEventListener('touchend', onMobileSendTouch);
        mobileSendBtn.removeEventListener('click', onMobileSendClick);
        mobileInput.removeEventListener('keydown', onMobileKeydown);
        mobileInput.removeEventListener('input', onMobileInput);
        keyEscBtn.removeEventListener('touchend', onEscTouch);
        keyEscBtn.removeEventListener('click', sendEsc);
        keyTabBtn.removeEventListener('touchend', onTabTouch);
        keyTabBtn.removeEventListener('click', sendTab);
        keyEnterBtn.removeEventListener('touchend', onEnterTouch);
        keyEnterBtn.removeEventListener('click', sendEnter);
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
