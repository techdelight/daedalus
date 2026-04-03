// Copyright (C) 2026 Techdelight BV

// terminal.js — xterm.js + WebSocket connection for Daedalus web UI

let term = null;
let ws = null;
let fitAddon = null;
let cleanupListeners = null;
let inHistoryMode = false;

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

    // Connect WebSocket — use control mode for scrollback support
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${proto}//${location.host}/api/projects/${encodeURIComponent(projectName)}/terminal?mode=control`;
    ws = new WebSocket(wsUrl);
    ws.binaryType = 'arraybuffer';

    ws.onopen = function() {
        // Send initial size
        ws.send(JSON.stringify({
            type: 'resize',
            cols: term.cols,
            rows: term.rows
        }));
        // Capture current pane content so the terminal is never blank on connect
        ws.send(JSON.stringify({ type: 'live-capture' }));
    };

    ws.onmessage = function(event) {
        if (event.data instanceof ArrayBuffer) {
            term.write(new Uint8Array(event.data));
        } else if (typeof event.data === 'string') {
            try {
                var msg = JSON.parse(event.data);
                if (msg.type === 'scrollback-response' && msg.content) {
                    term.write('\x1b[2J\x1b[H'); // clear + home
                    term.write(msg.content);
                    enterHistoryMode();
                    return;
                }
                if (msg.type === 'live-capture-response' && msg.content) {
                    term.write('\x1b[2J\x1b[H');
                    term.write(msg.content);
                    return;
                }
            } catch (e) { /* not JSON, treat as terminal data */ }
            term.write(event.data);
        }
    };

    ws.onclose = function() {
        if (inHistoryMode) {
            inHistoryMode = false;
            var banner = document.getElementById('history-banner');
            if (banner) banner.classList.remove('active');
            var btn = document.querySelector('.btn-history');
            if (btn) btn.classList.remove('active');
        }
        term.write('\r\n\x1b[33m[Connection closed]\x1b[0m\r\n');
    };

    ws.onerror = function() {
        if (inHistoryMode) {
            inHistoryMode = false;
            var banner = document.getElementById('history-banner');
            if (banner) banner.classList.remove('active');
            var btn = document.querySelector('.btn-history');
            if (btn) btn.classList.remove('active');
        }
        term.write('\r\n\x1b[31m[Connection error]\x1b[0m\r\n');
    };

    // Intercept Esc to exit history mode
    term.onKey(function(ev) {
        if (inHistoryMode && ev.domEvent.key === 'Escape') {
            ev.domEvent.preventDefault();
            exitHistoryMode();
        }
    });

    // Forward input to WebSocket
    term.onData(function(data) {
        if (inHistoryMode) {
            exitHistoryMode();
            return; // consume the keystroke that exits history
        }
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

    // Mobile input wiring
    var mobileInput = document.getElementById('mobile-input');
    var mobileSendBtn = document.getElementById('mobile-send-btn');

    function sendMobileInput() {
        var text = mobileInput.value;
        if (text.length === 0) return;
        if (ws && ws.readyState === WebSocket.OPEN) {
            ws.send(new TextEncoder().encode(text));
            fetch('/api/projects/' + encodeURIComponent(projectName) + '/enter', {
                method: 'POST'
            });
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
        exitSelectMode();
        mobileSelectBtn.removeEventListener('touchend', onSelectTouch);
        mobileSelectBtn.removeEventListener('click', toggleSelectMode);
        selectDoneBtn.removeEventListener('touchend', onDoneTouch);
        selectDoneBtn.removeEventListener('click', exitSelectMode);
        mobileSendBtn.removeEventListener('touchend', onMobileSendTouch);
        mobileSendBtn.removeEventListener('click', onMobileSendClick);
        mobileInput.removeEventListener('keydown', onMobileKeydown);
        mobileInput.removeEventListener('input', onMobileInput);
    };
}

function enterHistoryMode() {
    if (inHistoryMode) return;
    inHistoryMode = true;
    var banner = document.getElementById('history-banner');
    if (banner) banner.classList.add('active');
    var btn = document.querySelector('.btn-history');
    if (btn) btn.classList.add('active');
}

function exitHistoryMode() {
    if (!inHistoryMode) return;
    inHistoryMode = false;
    var banner = document.getElementById('history-banner');
    if (banner) banner.classList.remove('active');
    var btn = document.querySelector('.btn-history');
    if (btn) btn.classList.remove('active');
    // Request live terminal content to restore the viewport
    if (ws && ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'live-capture' }));
    }
}

function requestScrollback(lines) {
    if (ws && ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'scrollback', lines: lines || 500 }));
    }
}

function disconnectTerminal() {
    inHistoryMode = false;
    var banner = document.getElementById('history-banner');
    if (banner) banner.classList.remove('active');
    var btn = document.querySelector('.btn-history');
    if (btn) btn.classList.remove('active');
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
