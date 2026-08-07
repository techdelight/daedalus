// Copyright (C) 2026 Techdelight BV

// Guild Hall — a Secret-of-Mana-style JRPG party screen.
//
// Data flow (unchanged): GET /api/guild every 3s, diff-update cards without
// flicker. Each project renders as a distinct pixel-art hero whose ARCHETYPE
// and PALETTE are both derived deterministically from the project name, and
// whose animation is bound to the REAL activity state (busy/idle/sleeping).

let guildTimer = null;

// --- Deterministic hashing -------------------------------------------------

// Primary hash — drives the colour hue (kept identical to the historical
// nameToHue so existing projects keep a familiar tint).
function nameHash(name) {
    let hash = 0;
    for (let i = 0; i < name.length; i++) {
        hash = ((hash << 5) - hash + name.charCodeAt(i)) | 0;
    }
    return Math.abs(hash);
}

function nameToHue(name) {
    return nameHash(name) % 360;
}

// Secondary, independent hash — drives the archetype choice, so hero shape and
// colour vary independently and a roster reads as a varied party.
function archHash(name) {
    let h = 7;
    for (let i = 0; i < name.length; i++) {
        h = (h * 131 + name.charCodeAt(i) * (i + 1)) | 0;
    }
    return Math.abs(h);
}

// --- Palette ---------------------------------------------------------------

const HAIRS = ['#2b1d0e', '#5a3210', '#7a4a12', '#d9a441', '#1a1a22', '#9aa0ac'];
const SKINS = ['#f1c9a5', '#e6b48c', '#c68642', '#8d5524'];

// Derive a coherent five-slot palette from the project name. Same name always
// yields the same colours.
function heroPalette(name) {
    const hue = nameToHue(name);
    const h = nameHash(name);
    return {
        primary: 'hsl(' + hue + ' 55% 47%)',
        primaryDark: 'hsl(' + hue + ' 50% 30%)',
        accent: 'hsl(' + ((hue + 150) % 360) + ' 72% 60%)',
        hair: HAIRS[(h >> 3) % HAIRS.length],
        skin: SKINS[(h >> 6) % SKINS.length],
    };
}

// Fixed slots shared by every hero (steel, ink, highlight).
const FIXED = {
    M: '#c7cbd6',   // steel light (blades, plate)
    G: '#5b6070',   // steel dark  (hafts, boots)
    E: '#14151f',   // ink (eyes, outline)
    W: '#f4f6ff',   // white highlight / wings / gem shine
};

function colorFor(ch, pal) {
    switch (ch) {
        case 'S': return pal.skin;
        case 'H': return pal.hair;
        case 'P': return pal.primary;
        case 'D': return pal.primaryDark;
        case 'A': return pal.accent;
        case 'M': return FIXED.M;
        case 'G': return FIXED.G;
        case 'E': return FIXED.E;
        case 'W': return FIXED.W;
        default: return null; // space / '.' → transparent
    }
}

// --- Archetypes ------------------------------------------------------------
//
// Each archetype is authored as a 16-wide × 20-tall pixel grid (one char per
// cell). `body` is the figure; `tool` is the weapon/implement, drawn on a
// separate SVG group so it can be animated ("work" motion) independently.
// Class `arch-<key>` on the card selects the busy animation for that tool.

const ARCHETYPES = [
    {
        key: 'knight',
        label: 'Knight',
        body: [
            '                ',
            '      AA        ',
            '     MMMM       ',
            '    MMMMMM      ',
            '    MEEEEM      ',
            '    MMMMMM      ',
            '    PMMMMP      ',
            '   PPMMMMPP     ',
            '   PMMAAMMP     ',
            '   PMMMMMMP     ',
            '   PMMMMMMP     ',
            '    MMMMMM      ',
            '    MMDDMM      ',
            '    PPPPPP      ',
            '    PPPPPP      ',
            '    PPDDPP      ',
            '    MM  MM      ',
            '    MM  MM      ',
            '    GG  GG      ',
            '   GGG  GGG     ',
        ],
        tool: [
            '                ',
            '                ',
            '                ',
            '                ',
            '                ',
            '           M    ',
            '           M    ',
            '           M    ',
            '           M    ',
            '          AAA   ',
            '           G    ',
            '           G    ',
            '                ',
            '                ',
            '                ',
            '                ',
            '                ',
            '                ',
            '                ',
            '                ',
        ],
    },
    {
        key: 'mage',
        label: 'Mage',
        body: [
            '       P        ',
            '      PP        ',
            '      PPP       ',
            '     PPPP       ',
            '    PPPPPP      ',
            '    AAAAAA      ',
            '     SSSS       ',
            '     SEES       ',
            '     SSSS       ',
            '     HHHH       ',
            '    PPPPPP      ',
            '   PPPPPPPP     ',
            '   PPPAAPPP     ',
            '   PPPPPPPP     ',
            '   PPPPPPPP     ',
            '   DPPPPPPD     ',
            '   DPPPPPPD     ',
            '   DDPPPPDD     ',
            '   DDDDDDDD     ',
            '                ',
        ],
        tool: [
            '                ',
            '                ',
            '                ',
            '                ',
            '          WA    ',
            '          AA    ',
            '           G    ',
            '           G    ',
            '           G    ',
            '           G    ',
            '           G    ',
            '           G    ',
            '           G    ',
            '           G    ',
            '                ',
            '                ',
            '                ',
            '                ',
            '                ',
            '                ',
        ],
    },
    {
        key: 'archer',
        label: 'Archer',
        body: [
            '                ',
            '         A      ',
            '        AA      ',
            '     PPPPP      ',
            '     PPPPP      ',
            '     SSSS       ',
            '     SEES       ',
            '     SSSS       ',
            '      HH        ',
            '    PPPPPP      ',
            '    PAAAAP      ',
            '    PPPPPP      ',
            '    PPDDPP      ',
            '    PPPPPP      ',
            '    PP  PP      ',
            '    DD  DD      ',
            '    DD  DD      ',
            '    DD  DD      ',
            '    GG  GG      ',
            '   GGG  GGG     ',
        ],
        tool: [
            '                ',
            '                ',
            '                ',
            '                ',
            '                ',
            '    M           ',
            '   M            ',
            '   M            ',
            '   M            ',
            '   M            ',
            '   M            ',
            '   M            ',
            '    M           ',
            '                ',
            '                ',
            '                ',
            '                ',
            '                ',
            '                ',
            '                ',
        ],
    },
    {
        key: 'rogue',
        label: 'Rogue',
        body: [
            '                ',
            '     PPPP       ',
            '    PPPPPP      ',
            '    PPPPPP      ',
            '    PSSSSP      ',
            '    PSEESP      ',
            '    PPSSPP      ',
            '    DDDDDD      ',
            '   DDDDDDDD     ',
            '   DDPPPPDD     ',
            '   DDPPPPDD     ',
            '   DDAAAADD     ',
            '    DDDDDD      ',
            '    DDPPDD      ',
            '    PP  PP      ',
            '    DD  DD      ',
            '    DD  DD      ',
            '    DD  DD      ',
            '    GG  GG      ',
            '   GGG  GGG     ',
        ],
        tool: [
            '                ',
            '                ',
            '                ',
            '                ',
            '                ',
            '                ',
            '                ',
            '                ',
            '           M    ',
            '           M    ',
            '           A    ',
            '           G    ',
            '                ',
            '                ',
            '                ',
            '                ',
            '                ',
            '                ',
            '                ',
            '                ',
        ],
    },
    {
        key: 'cleric',
        label: 'Cleric',
        body: [
            '      AA        ',
            '      AA        ',
            '     AAAA       ',
            '      PP        ',
            '     PPPP       ',
            '     SSSS       ',
            '     SEES       ',
            '     SSSS       ',
            '    PPPPPP      ',
            '   PPPPPPPP     ',
            '   PPPAAPPP     ',
            '   PPPPPPPP     ',
            '   PPPPPPPP     ',
            '   PPPAAPPP     ',
            '   DPPPPPPD     ',
            '   DPPPPPPD     ',
            '   DDPPPPDD     ',
            '   DDDDDDDD     ',
            '    DDDDDD      ',
            '                ',
        ],
        tool: [
            '                ',
            '                ',
            '                ',
            '          AAA   ',
            '           A    ',
            '           A    ',
            '           G    ',
            '           G    ',
            '           G    ',
            '           G    ',
            '           G    ',
            '           G    ',
            '           G    ',
            '                ',
            '                ',
            '                ',
            '                ',
            '                ',
            '                ',
            '                ',
        ],
    },
    {
        key: 'sprite',
        label: 'Sprite',
        body: [
            '                ',
            '      HHHH      ',
            '     HHHHHH     ',
            '    SSSSSSSS    ',
            '    SEESSEES    ',
            '     SSSSSS     ',
            '      HHHH      ',
            '   WW PPPP WW   ',
            '   W  PPPPPP  W ',
            '     PPAAPP     ',
            '     PPPPPP     ',
            '     PPPPPP     ',
            '     DPPPPD     ',
            '      P  P      ',
            '      S  S      ',
            '      S  S      ',
            '     GG  GG     ',
            '                ',
            '                ',
            '                ',
        ],
        tool: [
            '                ',
            '                ',
            '                ',
            '                ',
            '                ',
            '                ',
            '           A    ',
            '           G    ',
            '           G    ',
            '                ',
            '                ',
            '                ',
            '                ',
            '                ',
            '                ',
            '                ',
            '                ',
            '                ',
            '                ',
            '                ',
        ],
    },
];

function archetypeFor(name) {
    return ARCHETYPES[archHash(name) % ARCHETYPES.length];
}

// Render a 16×20 char grid into a string of <rect> pixels.
function pixelRects(grid, pal) {
    let rects = '';
    for (let y = 0; y < grid.length; y++) {
        const row = grid[y];
        for (let x = 0; x < row.length; x++) {
            const col = colorFor(row[x], pal);
            if (!col) continue;
            rects += '<rect x="' + x + '" y="' + y + '" width="1" height="1" fill="' + col + '"/>';
        }
    }
    return rects;
}

// Build the inline SVG sprite. Vector rects on an integer grid render as crisp
// pixels; shape-rendering="crispEdges" guarantees no anti-aliased seams.
function buildSprite(name) {
    const arch = archetypeFor(name);
    const pal = heroPalette(name);
    return '<svg class="fig" viewBox="0 0 16 20" shape-rendering="crispEdges" '
        + 'xmlns="http://www.w3.org/2000/svg" aria-hidden="true">'
        + '<g class="fig-body">' + pixelRects(arch.body, pal) + '</g>'
        + '<g class="fig-tool">' + pixelRects(arch.tool, pal) + '</g>'
        + '</svg>';
}

// --- State labels ----------------------------------------------------------

const stateLabels = {
    busy: 'On Quest',
    idle: 'Awaiting Orders',
    sleeping: 'Resting',
};

// --- Action ribbon (JRPG flavour from member.detail) -----------------------
//
// When a hero is `busy`, member.detail carries the agent's current activity
// (usually a tool name). Map the common ones to JRPG flavour; fall back to the
// raw detail string when unmapped. Keyed by the lower-cased base tool name.
const ACTION_FLAVOR = {
    edit: 'Casting Edit…',
    multiedit: 'Casting Edit…',
    update: 'Casting Edit…',
    write: 'Inscribing runes…',
    notebookedit: 'Inscribing runes…',
    read: 'Reading the runes…',
    notebookread: 'Reading the runes…',
    bash: 'Forging…',
    grep: 'Scouting…',
    glob: 'Scouting…',
    search: 'Scouting…',
    ls: 'Scouting…',
    webfetch: 'Gazing afar…',
    websearch: 'Gazing afar…',
    fetch: 'Gazing afar…',
    task: 'Summoning allies…',
    agent: 'Summoning allies…',
    tool_use: 'Channelling…',
    thinking: 'Pondering…',
    think: 'Pondering…',
    stop: 'Catching breath…',
    waiting: 'Catching breath…',
};

// Resolve the ribbon {text, mood} for a member, or null when it should hide.
//   busy  -> action flavour (mapped, else raw detail, else generic)
//   idle  -> a quiet at-ease line
//   sleep -> hidden
function actionRibbon(member) {
    if (member.activity === 'busy') {
        const raw = (member.detail || '').trim();
        const key = raw.toLowerCase();
        const flavor = ACTION_FLAVOR[key];
        if (flavor) return { text: flavor, mood: 'busy' };
        if (raw) return { text: raw, mood: 'busy' };  // unmapped → raw detail
        return { text: 'Channelling…', mood: 'busy' };
    }
    if (member.activity === 'idle') {
        return { text: 'At ease…', mood: 'idle' };
    }
    return null; // sleeping → no ribbon
}

// Apply the ribbon to a card's .guild-action element (create + update share
// this; it only touches text/visibility, never the sprite).
function applyActionRibbon(card, member) {
    const el = card.querySelector('.guild-action');
    if (!el) return;
    const ribbon = actionRibbon(member);
    if (!ribbon) {
        el.style.display = 'none';
        el.textContent = '';
        return;
    }
    el.style.display = '';
    el.className = 'guild-action mood-' + ribbon.mood;
    if (el.textContent !== ribbon.text) {
        el.textContent = ribbon.text;
    }
}

// --- Card construction (sprite built once; keyed by immutable name) ---------

function createMemberCard(member) {
    const arch = archetypeFor(member.name);

    const card = document.createElement('div');
    card.className = 'guild-card arch-' + arch.key + ' state-' + member.activity;
    card.dataset.name = member.name;
    card.onclick = function () { showDashboard(member.name); };
    card.title = member.vision || member.name;

    // Ornate class-tag ribbon (top-left of frame).
    const tag = document.createElement('div');
    tag.className = 'guild-class';
    tag.textContent = arch.label;
    card.appendChild(tag);

    // Action ribbon (speech bubble over the sprite). Created once; its text and
    // visibility are updated live on each poll — the sprite is never rebuilt.
    const action = document.createElement('div');
    action.className = 'guild-action';
    card.appendChild(action);

    // Avatar (SVG sprite + activity overlays).
    const avatarContainer = document.createElement('div');
    avatarContainer.className = 'avatar-container';
    avatarContainer.innerHTML = buildSprite(member.name);

    // Particles (busy: work sparks / spell motes).
    const particles = document.createElement('div');
    particles.className = 'particles';
    for (let i = 0; i < 3; i++) {
        const p = document.createElement('div');
        p.className = 'particle';
        particles.appendChild(p);
    }
    avatarContainer.appendChild(particles);

    // ZZZ (sleeping).
    const zzz = document.createElement('div');
    zzz.className = 'zzz';
    zzz.innerHTML = '<span>z</span><span>z</span><span>z</span>';
    avatarContainer.appendChild(zzz);

    card.appendChild(avatarContainer);

    // Name plate (with a small "Lv" pip from sessionCount).
    const nameEl = document.createElement('div');
    nameEl.className = 'guild-name';

    const nameText = document.createElement('span');
    nameText.className = 'guild-name-text';
    nameText.textContent = member.name;
    nameEl.appendChild(nameText);

    const levelEl = document.createElement('span');
    levelEl.className = 'guild-level';
    levelEl.textContent = 'Lv ' + (member.sessionCount || 0);
    nameEl.appendChild(levelEl);

    card.appendChild(nameEl);

    // State label.
    const stateEl = document.createElement('div');
    stateEl.className = 'guild-state state-' + member.activity;
    stateEl.textContent = stateLabels[member.activity] || member.activity;
    card.appendChild(stateEl);

    // HP gauge (bound to progressPct).
    const hpRow = document.createElement('div');
    hpRow.className = 'guild-hp-row';

    const hpLabel = document.createElement('span');
    hpLabel.className = 'guild-hp-label';
    hpLabel.textContent = 'HP';
    hpRow.appendChild(hpLabel);

    const hpBar = document.createElement('div');
    hpBar.className = 'guild-hp';
    const hpFill = document.createElement('div');
    hpFill.className = 'guild-hp-fill';
    hpFill.style.width = member.progressPct + '%';
    hpBar.appendChild(hpFill);
    hpRow.appendChild(hpBar);

    const hpText = document.createElement('span');
    hpText.className = 'guild-hp-text';
    hpText.textContent = member.progressPct + '%';
    hpRow.appendChild(hpText);

    card.appendChild(hpRow);

    // Target line.
    const targetEl = document.createElement('div');
    targetEl.className = 'guild-target';
    targetEl.textContent = member.target || '';
    card.appendChild(targetEl);

    // Initial action ribbon.
    applyActionRibbon(card, member);

    return card;
}

// Diff-update: mutate only what changed (state, HP, target). The sprite is
// keyed by the immutable project name and is never rebuilt → no flicker.
function updateMemberCard(card, member) {
    const arch = archetypeFor(member.name);
    const nextClass = 'guild-card arch-' + arch.key + ' state-' + member.activity;
    if (card.className !== nextClass) {
        card.className = nextClass;
    }

    const stateEl = card.querySelector('.guild-state');
    if (stateEl) {
        stateEl.className = 'guild-state state-' + member.activity;
        stateEl.textContent = stateLabels[member.activity] || member.activity;
    }

    const hpFill = card.querySelector('.guild-hp-fill');
    if (hpFill) {
        hpFill.style.width = member.progressPct + '%';
    }

    const hpText = card.querySelector('.guild-hp-text');
    if (hpText) {
        hpText.textContent = member.progressPct + '%';
    }

    const targetEl = card.querySelector('.guild-target');
    if (targetEl) {
        targetEl.textContent = member.target || '';
    }

    const levelEl = card.querySelector('.guild-level');
    if (levelEl) {
        const lv = 'Lv ' + (member.sessionCount || 0);
        if (levelEl.textContent !== lv) {
            levelEl.textContent = lv;
        }
    }

    // Live action ribbon (text + visibility only — no sprite rebuild).
    applyActionRibbon(card, member);

    const title = member.vision || member.name;
    if (card.title !== title) {
        card.title = title;
    }
}

function renderGuildMembers(members) {
    const container = document.getElementById('guild-members');
    const empty = document.getElementById('guild-empty');

    if (!members || members.length === 0) {
        container.innerHTML = '';
        empty.textContent = 'The guild hall stands empty — register a project to begin.';
        empty.style.display = 'block';
        return;
    }
    empty.style.display = 'none';

    // Diff-update: reuse existing cards to avoid flicker
    const existingCards = {};
    container.querySelectorAll('.guild-card').forEach(function (card) {
        existingCards[card.dataset.name] = card;
    });

    const memberNames = new Set();
    members.forEach(function (member) {
        memberNames.add(member.name);
        const existing = existingCards[member.name];
        if (existing) {
            updateMemberCard(existing, member);
        } else {
            container.appendChild(createMemberCard(member));
        }
    });

    // Remove cards for projects that no longer exist
    container.querySelectorAll('.guild-card').forEach(function (card) {
        if (!memberNames.has(card.dataset.name)) {
            card.remove();
        }
    });

    // Update subtitle (party roster header)
    const subtitle = document.getElementById('guild-subtitle');
    if (subtitle) {
        const active = members.filter(function (m) { return m.activity !== 'sleeping'; }).length;
        subtitle.textContent = 'Party — ' + active + ' active / ' + members.length + ' heroes';
    }
}

async function fetchGuildData() {
    try {
        const resp = await fetch('/api/guild');
        if (!resp.ok) return;
        const members = await resp.json();
        renderGuildMembers(members);
    } catch (e) {
        // Silently ignore fetch errors during polling
    }
}

function showGuildView() {
    // Stop project list polling
    if (typeof refreshTimer !== 'undefined' && refreshTimer) {
        clearInterval(refreshTimer);
        refreshTimer = null;
    }

    // Hide other views
    document.getElementById('project-view').classList.add('hidden');
    document.getElementById('dashboard-view').classList.remove('active');
    document.getElementById('terminal-view').classList.remove('active');

    // Show guild
    document.getElementById('guild-view').classList.add('active');
    document.title = 'Guild Hall — Daedalus';

    // Sensible first-load state until the first poll resolves.
    const empty = document.getElementById('guild-empty');
    const container = document.getElementById('guild-members');
    if (empty && container && container.querySelectorAll('.guild-card').length === 0) {
        empty.textContent = 'Summoning the guild…';
        empty.style.display = 'block';
    }

    // Fetch immediately, then poll
    fetchGuildData();
    guildTimer = setInterval(fetchGuildData, 3000);
}

function hideGuildView() {
    if (guildTimer) {
        clearInterval(guildTimer);
        guildTimer = null;
    }
    document.getElementById('guild-view').classList.remove('active');
}
