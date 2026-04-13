// Copyright (C) 2026 Techdelight BV

// Guild Hall — JRPG-themed project view

let guildTimer = null;

// Deterministic hue from project name for unique avatar colors.
function nameToHue(name) {
    let hash = 0;
    for (let i = 0; i < name.length; i++) {
        hash = ((hash << 5) - hash + name.charCodeAt(i)) | 0;
    }
    return Math.abs(hash) % 360;
}

// Color palettes keyed by hue range — maps to CSS custom properties.
function avatarColors(name) {
    const hue = nameToHue(name);
    // Predefined palettes for visual variety
    const palettes = [
        { hat: '#7c3aed', accent: '#fbbf24', robe: '#6d28d9', robeDark: '#4c1d95' },  // purple
        { hat: '#2563eb', accent: '#fbbf24', robe: '#1d4ed8', robeDark: '#1e3a8a' },  // blue
        { hat: '#dc2626', accent: '#fbbf24', robe: '#b91c1c', robeDark: '#7f1d1d' },  // red
        { hat: '#059669', accent: '#fbbf24', robe: '#047857', robeDark: '#064e3b' },  // green
        { hat: '#d97706', accent: '#fef3c7', robe: '#b45309', robeDark: '#78350f' },  // amber
        { hat: '#db2777', accent: '#fbbf24', robe: '#be185d', robeDark: '#831843' },  // pink
        { hat: '#0891b2', accent: '#fbbf24', robe: '#0e7490', robeDark: '#164e63' },  // cyan
        { hat: '#7c3aed', accent: '#f472b6', robe: '#6d28d9', robeDark: '#4c1d95' },  // violet
    ];
    return palettes[hue % palettes.length];
}

// State labels in JRPG style.
const stateLabels = {
    busy: 'On Quest',
    idle: 'Awaiting Orders',
    sleeping: 'Resting',
};

function createMemberCard(member) {
    const card = document.createElement('div');
    card.className = 'guild-card state-' + member.activity;
    card.dataset.name = member.name;
    card.onclick = function () { showDashboard(member.name); };
    card.title = member.vision || member.name;

    const colors = avatarColors(member.name);
    card.style.setProperty('--hat', colors.hat);
    card.style.setProperty('--accent', colors.accent);
    card.style.setProperty('--robe', colors.robe);
    card.style.setProperty('--robe-dark', colors.robeDark);

    // Avatar
    const avatarContainer = document.createElement('div');
    avatarContainer.className = 'avatar-container';

    const sprite = document.createElement('div');
    sprite.className = 'avatar-sprite';
    avatarContainer.appendChild(sprite);

    // Particles (visible only when busy)
    const particles = document.createElement('div');
    particles.className = 'particles';
    for (let i = 0; i < 3; i++) {
        const p = document.createElement('div');
        p.className = 'particle';
        particles.appendChild(p);
    }
    avatarContainer.appendChild(particles);

    // ZZZ (visible only when sleeping)
    const zzz = document.createElement('div');
    zzz.className = 'zzz';
    zzz.innerHTML = '<span>z</span><span>z</span><span>z</span>';
    avatarContainer.appendChild(zzz);

    card.appendChild(avatarContainer);

    // Name
    const nameEl = document.createElement('div');
    nameEl.className = 'guild-name';
    nameEl.textContent = member.name;
    card.appendChild(nameEl);

    // State label
    const stateEl = document.createElement('div');
    stateEl.className = 'guild-state state-' + member.activity;
    stateEl.textContent = stateLabels[member.activity] || member.activity;
    card.appendChild(stateEl);

    // HP bar (progress)
    const hpBar = document.createElement('div');
    hpBar.className = 'guild-hp';
    const hpFill = document.createElement('div');
    hpFill.className = 'guild-hp-fill';
    hpFill.style.width = member.progressPct + '%';
    // Position gradient based on fill percentage
    hpFill.style.backgroundPosition = (100 - member.progressPct) + '% 0';
    hpBar.appendChild(hpFill);
    card.appendChild(hpBar);

    // HP text
    const hpText = document.createElement('div');
    hpText.className = 'guild-hp-text';
    hpText.textContent = 'HP ' + member.progressPct + '%';
    card.appendChild(hpText);

    // Target
    const targetEl = document.createElement('div');
    targetEl.className = 'guild-target';
    targetEl.textContent = member.target;
    card.appendChild(targetEl);

    return card;
}

function updateMemberCard(card, member) {
    // Update state class
    card.className = 'guild-card state-' + member.activity;

    // Update state label
    const stateEl = card.querySelector('.guild-state');
    if (stateEl) {
        stateEl.className = 'guild-state state-' + member.activity;
        stateEl.textContent = stateLabels[member.activity] || member.activity;
    }

    // Update HP
    const hpFill = card.querySelector('.guild-hp-fill');
    if (hpFill) {
        hpFill.style.width = member.progressPct + '%';
        hpFill.style.backgroundPosition = (100 - member.progressPct) + '% 0';
    }

    const hpText = card.querySelector('.guild-hp-text');
    if (hpText) {
        hpText.textContent = 'HP ' + member.progressPct + '%';
    }
}

function renderGuildMembers(members) {
    const container = document.getElementById('guild-members');
    const empty = document.getElementById('guild-empty');

    if (!members || members.length === 0) {
        container.innerHTML = '';
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

    // Update subtitle
    const subtitle = document.getElementById('guild-subtitle');
    if (subtitle) {
        const active = members.filter(function (m) { return m.activity !== 'sleeping'; }).length;
        subtitle.textContent = active + ' active / ' + members.length + ' total';
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
    document.getElementById('foreman-view').classList.remove('active');

    // Show guild
    document.getElementById('guild-view').classList.add('active');
    document.title = 'Guild Hall \u2014 Daedalus';

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
