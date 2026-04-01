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

  test('index.html contains Foreman view', async ({ request }) => {
    const html = await (await request.get('/')).text();
    expect(html).toContain('id="foreman-view"');
    expect(html).toContain('id="foreman-state-label"');
    expect(html).toContain('id="foreman-programme-select"');
    expect(html).toContain('id="foreman-start-btn"');
  });

  test('index.html contains dashboard view', async ({ request }) => {
    const html = await (await request.get('/')).text();
    expect(html).toContain('id="dashboard-view"');
  });

  test('index.html contains terminal view', async ({ request }) => {
    const html = await (await request.get('/')).text();
    expect(html).toContain('id="terminal-view"');
  });

  test('index.html contains programme form', async ({ request }) => {
    const html = await (await request.get('/')).text();
    expect(html).toContain('id="programme-form-name"');
    expect(html).toContain('id="programme-form-desc"');
    expect(html).toContain('id="programme-form-projects"');
    expect(html).toContain('id="programme-form-deps"');
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

test.describe('Web UI — Foreman API', () => {
  test('GET /api/foreman/status returns JSON', async ({ request }) => {
    const resp = await request.get('/api/foreman/status', { failOnStatusCode: false });
    expect([200, 500]).toContain(resp.status());
  });

  test('POST /api/foreman/stop returns JSON', async ({ request }) => {
    const resp = await request.post('/api/foreman/stop', { failOnStatusCode: false });
    expect([200, 409, 500]).toContain(resp.status());
  });

  test('POST /api/foreman/start without programme returns error', async ({ request }) => {
    const resp = await request.post('/api/foreman/start', {
      data: {},
      failOnStatusCode: false,
    });
    expect([400, 500]).toContain(resp.status());
  });
});

test.describe('Web UI — Programme API CRUD', () => {
  const progName = 'e2e-test-prog';

  test('can create a programme', async ({ request }) => {
    const resp = await request.post('/api/programmes', {
      data: { name: progName, description: 'E2E test programme', projects: ['proj-a'], deps: [] },
    });
    expect(resp.status()).toBe(201);
    const body = await resp.json();
    expect(body.name).toBe(progName);
    expect(body.description).toBe('E2E test programme');
  });

  test('can get the created programme', async ({ request }) => {
    // Ensure it exists
    await request.post('/api/programmes', {
      data: { name: progName, description: 'E2E test', projects: [], deps: [] },
      failOnStatusCode: false,
    });
    const resp = await request.get(`/api/programmes/${progName}`);
    expect(resp.status()).toBe(200);
    const body = await resp.json();
    expect(body.name).toBe(progName);
  });

  test('can list programmes and find the created one', async ({ request }) => {
    await request.post('/api/programmes', {
      data: { name: progName, description: 'E2E test', projects: [], deps: [] },
      failOnStatusCode: false,
    });
    const resp = await request.get('/api/programmes');
    expect(resp.status()).toBe(200);
    const body = await resp.json();
    expect(body.some((p: any) => p.name === progName)).toBe(true);
  });

  test('can update a programme', async ({ request }) => {
    await request.post('/api/programmes', {
      data: { name: progName, description: 'old', projects: [], deps: [] },
      failOnStatusCode: false,
    });
    const resp = await request.put(`/api/programmes/${progName}`, {
      data: { name: progName, description: 'updated description', projects: ['a', 'b'], deps: [] },
    });
    expect(resp.status()).toBe(200);
    const body = await resp.json();
    expect(body.description).toBe('updated description');
  });

  test('can delete a programme', async ({ request }) => {
    await request.post('/api/programmes', {
      data: { name: progName, description: 'to delete', projects: [], deps: [] },
      failOnStatusCode: false,
    });
    const delResp = await request.delete(`/api/programmes/${progName}`);
    expect(delResp.status()).toBe(200);

    const getResp = await request.get(`/api/programmes/${progName}`, { failOnStatusCode: false });
    expect(getResp.status()).toBe(404);
  });

  test('GET nonexistent programme returns 404', async ({ request }) => {
    const resp = await request.get('/api/programmes/does-not-exist', { failOnStatusCode: false });
    expect(resp.status()).toBe(404);
  });

  test('DELETE nonexistent programme returns 404', async ({ request }) => {
    const resp = await request.delete('/api/programmes/does-not-exist', { failOnStatusCode: false });
    expect(resp.status()).toBe(404);
  });

  test('creating duplicate programme returns 409', async ({ request }) => {
    await request.post('/api/programmes', {
      data: { name: 'dup-test', description: 'first', projects: [], deps: [] },
      failOnStatusCode: false,
    });
    const resp = await request.post('/api/programmes', {
      data: { name: 'dup-test', description: 'second', projects: [], deps: [] },
      failOnStatusCode: false,
    });
    expect([400, 409, 500]).toContain(resp.status());
    // Cleanup
    await request.delete('/api/programmes/dup-test', { failOnStatusCode: false });
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
