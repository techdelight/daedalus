// Copyright (C) 2026 Techdelight BV
//
// Playwright end-to-end tests for the Daedalus Web UI.
// Tests use the API request context (no browser rendering required)
// for maximum portability across CI environments.

import { test, expect } from '@playwright/test';

test.describe('Web UI — Static Assets', () => {
  test('serves index.html at root with Daedalus in body', async ({ request }) => {
    const resp = await request.get('/');
    expect(resp.status()).toBe(200);
    const html = await resp.text();
    expect(html).toContain('Daedalus');
    expect(html).toContain('<!DOCTYPE html>');
  });

  test('injects version into page title', async ({ request }) => {
    const resp = await request.get('/');
    const html = await resp.text();
    // Version is injected as ">Daedalus [vX.Y.Z]<"
    expect(html).toMatch(/Daedalus \[.*\]/);
  });

  test('serves favicon.svg with correct content type', async ({ request }) => {
    const resp = await request.get('/static/favicon.svg');
    expect(resp.status()).toBe(200);
    const ct = resp.headers()['content-type'];
    expect(ct).toMatch(/svg/);
    const body = await resp.text();
    expect(body).toContain('<svg');
  });

  test('serves style.css', async ({ request }) => {
    const resp = await request.get('/static/style.css');
    expect(resp.status()).toBe(200);
    const body = await resp.text();
    expect(body).toContain('body');
  });

  test('serves terminal.js', async ({ request }) => {
    const resp = await request.get('/static/terminal.js');
    expect(resp.status()).toBe(200);
    const body = await resp.text();
    expect(body).toContain('Copyright');
  });

  test('returns 404 for unknown static paths', async ({ request }) => {
    const resp = await request.get('/static/nonexistent.txt', { failOnStatusCode: false });
    expect(resp.status()).toBe(404);
  });
});

test.describe('Web UI — HTML Structure', () => {
  test('index.html contains project list table', async ({ request }) => {
    const html = await (await request.get('/')).text();
    expect(html).toContain('id="project-tbody"');
    expect(html).toContain('<th>Project</th>');
    expect(html).toContain('<th>Status</th>');
    expect(html).toContain('<th>Target</th>');
    expect(html).toContain('<th>Actions</th>');
  });

  test('index.html contains dashboard view', async ({ request }) => {
    const html = await (await request.get('/')).text();
    expect(html).toContain('id="dashboard-view"');
  });

  test('index.html contains terminal view', async ({ request }) => {
    const html = await (await request.get('/')).text();
    expect(html).toContain('id="terminal-view"');
  });

  test('index.html includes favicon link', async ({ request }) => {
    const html = await (await request.get('/')).text();
    expect(html).toContain('href="/static/favicon.svg"');
  });

  test('index.html has filter toggle button', async ({ request }) => {
    const html = await (await request.get('/')).text();
    expect(html).toContain('id="filter-active-btn"');
    expect(html).toContain('Active Only');
  });
});

test.describe('Web UI — Project API', () => {
  test('GET /api/projects returns JSON array', async ({ request }) => {
    const resp = await request.get('/api/projects');
    expect(resp.status()).toBe(200);
    expect(resp.headers()['content-type']).toContain('application/json');
    const body = await resp.json();
    expect(Array.isArray(body)).toBe(true);
  });

  test('GET /api/projects/nonexistent/dashboard returns 404', async ({ request }) => {
    const resp = await request.get('/api/projects/nonexistent/dashboard', { failOnStatusCode: false });
    expect(resp.status()).toBe(404);
  });

  test('GET /api/projects/nonexistent/state returns 404', async ({ request }) => {
    const resp = await request.get('/api/projects/nonexistent/state', { failOnStatusCode: false });
    expect(resp.status()).toBe(404);
  });

  test('GET /api/projects/nonexistent/roadmap returns 404', async ({ request }) => {
    const resp = await request.get('/api/projects/nonexistent/roadmap', { failOnStatusCode: false });
    expect(resp.status()).toBe(404);
  });

  test('POST /api/projects/nonexistent/start returns 404', async ({ request }) => {
    const resp = await request.post('/api/projects/nonexistent/start', { failOnStatusCode: false });
    expect(resp.status()).toBe(404);
  });

  test('POST /api/projects/nonexistent/stop returns error', async ({ request }) => {
    const resp = await request.post('/api/projects/nonexistent/stop', { failOnStatusCode: false });
    expect([404, 500]).toContain(resp.status());
  });

  test('POST /api/projects/nonexistent/enter returns 404', async ({ request }) => {
    const resp = await request.post('/api/projects/nonexistent/enter', { failOnStatusCode: false });
    expect(resp.status()).toBe(404);
  });

  test('POST /api/projects/nonexistent/rename with body returns 404', async ({ request }) => {
    const resp = await request.post('/api/projects/nonexistent/rename', {
      data: { newname: 'new-name' },
      failOnStatusCode: false,
    });
    expect(resp.status()).toBe(404);
  });
});

// Programmes, against the CONTROL PLANE (M20, Sprint 66).
//
// These tests used to drive `/api/programmes` — the file-backed CRUD that was
// DELETED when a programme became plane state. They followed the deleted routes
// instead of being deleted with them, so every one of them had been asserting a
// 200 from a handler that no longer exists. Nobody noticed because the e2e suite
// needs a browser and a running server, and neither is present in the
// development environment.
//
// They now drive `/api/control/programmes`, which needs the control daemon —
// which the Web UI deliberately never spawns. So each test SKIPS when the plane
// is down rather than failing: "I could not ask" is not "it is broken", which is
// the same distinction the page itself makes.
test.describe('Web UI — Programmes (control plane)', () => {
  const progName = 'e2e-test-prog';

  // Returns the programme list when the plane is reachable, or null when it is
  // not. The response is 200 either way; `available` is the field that answers.
  async function programmes(request: any): Promise<any[] | null> {
    const resp = await request.get('/api/control/programmes');
    if (resp.status() !== 200) return null;
    const body = await resp.json();
    return body.available ? (body.programmes || []) : null;
  }

  async function form(request: any, name: string, description: string): Promise<any> {
    const resp = await request.post('/api/control/programmes', {
      data: { name, description },
      failOnStatusCode: false,
    });
    return resp.status() === 201 || resp.status() === 200 ? await resp.json() : null;
  }

  // Programmes are keyed by ID, not by name — that is the whole reason they moved
  // into the plane, so the tests address them the way a client has to.
  async function dissolve(request: any, id: string) {
    await request.delete(`/api/control/programmes/${id}`, { failOnStatusCode: false });
  }

  test('a programme can be formed, read back, amended and dissolved', async ({ request }) => {
    const before = await programmes(request);
    test.skip(before === null, 'the control plane is not running');

    const formed = await form(request, progName, 'E2E test programme');
    expect(formed).not.toBeNull();
    expect(formed.id).toMatch(/^PR-\d+$/);
    expect(formed.name).toBe(progName);

    const read = await request.get(`/api/control/programmes/${formed.id}`);
    expect(read.status()).toBe(200);
    expect((await read.json()).description).toBe('E2E test programme');

    const listed = await programmes(request);
    expect((listed || []).some((p: any) => p.id === formed.id)).toBe(true);

    const amended = await request.post(`/api/control/programmes/${formed.id}`, {
      data: { name: progName, description: 'amended description' },
    });
    expect(amended.status()).toBe(200);
    expect((await amended.json()).description).toBe('amended description');

    await dissolve(request, formed.id);
    const gone = await request.get(`/api/control/programmes/${formed.id}`, { failOnStatusCode: false });
    expect(gone.status()).toBe(404);
  });

  test('the roll-up answers what a programme is waiting on', async ({ request }) => {
    const before = await programmes(request);
    test.skip(before === null, 'the control plane is not running');

    const formed = await form(request, `${progName}-status`, 'for the roll-up');
    expect(formed).not.toBeNull();

    const resp = await request.get(`/api/control/programmes/${formed.id}/status`);
    expect(resp.status()).toBe(200);
    const body = await resp.json();
    expect(body.programme.id).toBe(formed.id);
    // No work yet, so no tasks and nothing outside to wait on. The shape is what
    // is being asserted: the page reads `open`, `landed` and `external`.
    expect(body.open).toBe(0);
    expect(body.landed).toBe(0);

    await dissolve(request, formed.id);
  });

  test('an unknown programme is 404, not an empty programme', async ({ request }) => {
    const before = await programmes(request);
    test.skip(before === null, 'the control plane is not running');

    const resp = await request.get('/api/control/programmes/PR-99999', { failOnStatusCode: false });
    expect(resp.status()).toBe(404);
  });

  test('with the plane down the list says unavailable, and does not 500', async ({ request }) => {
    const resp = await request.get('/api/control/programmes');
    expect(resp.status()).toBe(200);
    const body = await resp.json();
    // Whichever way it goes, the page can render it: `programmes` is always an
    // array, and `available` says whether the emptiness means anything.
    expect(Array.isArray(body.programmes)).toBe(true);
    if (!body.available) expect(body.reason).toBeTruthy();
  });
});

test.describe('Web UI — Auth (no-auth mode)', () => {
  test('all endpoints accessible without token in no-auth mode', async ({ request }) => {
    // Server started with --no-auth, so everything should be accessible
    const resp = await request.get('/');
    expect(resp.status()).toBe(200);
    const apiResp = await request.get('/api/projects');
    expect(apiResp.status()).toBe(200);
  });

  test('/login is not registered in no-auth mode', async ({ request }) => {
    const resp = await request.get('/login', { failOnStatusCode: false });
    // Without auth, /login is not a registered route
    expect([404, 405]).toContain(resp.status());
  });
});
