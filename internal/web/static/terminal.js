// Copyright (C) 2026 Techdelight BV

// terminal.js — xterm.js + WebSocket connection for Daedalus web UI

let term = null;
let ws = null;
let fitAddon = null;
let cleanupListeners = null;

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

    // Connect WebSocket
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${proto}//${location.host}/api/projects/${encodeURIComponent(projectName)}/terminal`;
    ws = new WebSocket(wsUrl);
    ws.binaryType = 'arraybuffer';

    ws.onopen = function() {
        // Send initial size
        ws.send(JSON.stringify({
            type: 'resize',
            cols: term.cols,
            rows: term.rows
        }));
    };

    ws.onmessage = function(event) {
        if (event.data instanceof ArrayBuffer) {
            term.write(new Uint8Array(event.data));
        } else {
            term.write(event.data);
        }
    };

    ws.onclose = function() {
        term.write('\r\n\x1b[33m[Connection closed]\x1b[0m\r\n');
    };

    ws.onerror = function() {
        term.write('\r\n\x1b[31m[Connection error]\x1b[0m\r\n');
    };

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

    // Mobile input wiring
    var mobileInput = document.getElementById('mobile-input');
    var mobileSendBtn = document.getElementById('mobile-send-btn');

    function sendMobileInput() {
        var text = mobileInput.value;
        if (text.length === 0) return;
        if (ws && ws.readyState === WebSocket.OPEN) {
            ws.send(new TextEncoder().encode(text));
            setTimeout(function() {
                ws.send(new TextEncoder().encode('\r'));
            }, 50);
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
        mobileSendBtn.removeEventListener('touchend', onMobileSendTouch);
        mobileSendBtn.removeEventListener('click', onMobileSendClick);
        mobileInput.removeEventListener('keydown', onMobileKeydown);
        mobileInput.removeEventListener('input', onMobileInput);
    };
}

function disconnectTerminal() {
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
