// Copyright (C) 2026 Techdelight BV

import { test, expect } from '@playwright/test';
import { mockProjects, interceptProjects, interceptAction, waitForProjects } from './fixtures';

test.describe('Dashboard', () => {
  test('table renders with 5 column headers', async ({ page }) => {
    await page.goto('/');
    await waitForProjects(page);

    const headers = page.locator('thead th');
    await expect(headers).toHaveCount(5);
    await expect(headers.nth(0)).toHaveText('Project');
    await expect(headers.nth(1)).toHaveText('Status');
    await expect(headers.nth(2)).toHaveText('Target');
    await expect(headers.nth(3)).toHaveText('Last Used');
    await expect(headers.nth(4)).toHaveText('Actions');
  });

  test('running project shows Attach and Stop buttons', async ({ page }) => {
    await page.goto('/');
    await waitForProjects(page);

    // alpha is the first project and is running
    const alphaRow = page.locator('#project-tbody tr').first();
    await expect(alphaRow.locator('.btn-attach')).toBeVisible();
    await expect(alphaRow.locator('.btn-stop')).toBeVisible();
    await expect(alphaRow.locator('.btn-start')).toHaveCount(0);
    await expect(alphaRow.locator('.btn-rename')).toHaveCount(0);
  });

  test('stopped project shows Start and Rename buttons', async ({ page }) => {
    await page.goto('/');
    await waitForProjects(page);

    // beta is the second project and is stopped
    const betaRow = page.locator('#project-tbody tr').nth(1);
    await expect(betaRow.locator('.btn-start')).toBeVisible();
    await expect(betaRow.locator('.btn-rename')).toBeVisible();
    await expect(betaRow.locator('.btn-attach')).toHaveCount(0);
    await expect(betaRow.locator('.btn-stop')).toHaveCount(0);
  });

  test('empty state shows message', async ({ page }) => {
    await interceptProjects(page, []);
    await page.goto('/');

    await expect(page.locator('#empty-state')).toBeVisible();
    await expect(page.locator('#empty-state')).toHaveText('No registered projects.');
  });

  test('version appears in heading', async ({ page }) => {
    await page.goto('/');

    const heading = page.locator('header h1');
    await expect(heading).toContainText('Daedalus [');
  });

  test('start action shows status message and refreshes list', async ({ page }) => {
    const projects = mockProjects();
    await interceptProjects(page, projects);
    await page.goto('/');
    await waitForProjects(page);

    // Intercept the start call
    await page.route('**/api/projects/beta/start', route => {
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ status: 'started', project: 'beta' }),
      });
    });

    // After start succeeds, fetchProjects is called — return beta as running
    const updated = projects.map(p =>
      p.name === 'beta' ? { ...p, running: true } : p
    );
    await page.unroute('**/api/projects');
    await interceptProjects(page, updated);

    await page.locator('#project-tbody tr').nth(1).locator('.btn-start').click();

    await expect(page.locator('#status-msg')).toContainText('Started beta');
  });

  test('start error displays error message', async ({ page }) => {
    const projects = mockProjects();
    await interceptProjects(page, projects);
    await page.goto('/');
    await waitForProjects(page);

    await page.route('**/api/projects/beta/start', route => {
      route.fulfill({
        status: 412,
        contentType: 'text/plain',
        body: 'image not found — run daedalus --build beta first',
      });
    });

    await page.locator('#project-tbody tr').nth(1).locator('.btn-start').click();

    await expect(page.locator('#status-msg')).toContainText('Error');
  });

  test('stop action shows status message', async ({ page }) => {
    const projects = mockProjects();
    await interceptProjects(page, projects);
    await page.goto('/');
    await waitForProjects(page);

    await page.route('**/api/projects/alpha/stop', route => {
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ status: 'stopped', project: 'alpha' }),
      });
    });

    await page.locator('#project-tbody tr').first().locator('.btn-stop').click();

    await expect(page.locator('#status-msg')).toContainText('Stopped alpha');
  });

  test('rename accepts prompt and shows status message', async ({ page }) => {
    const projects = mockProjects();
    await interceptProjects(page, projects);
    await page.goto('/');
    await waitForProjects(page);

    await page.route('**/api/projects/beta/rename', route => {
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ status: 'renamed', oldName: 'beta', newName: 'beta-new' }),
      });
    });

    page.on('dialog', async dialog => {
      expect(dialog.type()).toBe('prompt');
      await dialog.accept('beta-new');
    });

    await page.locator('#project-tbody tr').nth(1).locator('.btn-rename').click();

    await expect(page.locator('#status-msg')).toContainText('Renamed beta to beta-new');
  });

  test('rename dismiss does nothing', async ({ page }) => {
    const projects = mockProjects();
    await interceptProjects(page, projects);
    await page.goto('/');
    await waitForProjects(page);

    page.on('dialog', async dialog => {
      await dialog.dismiss();
    });

    await page.locator('#project-tbody tr').nth(1).locator('.btn-rename').click();

    // Brief wait then verify no rename status
    await page.waitForTimeout(500);
    await expect(page.locator('#status-msg')).not.toContainText('Renamed');
  });

  test('auto-refresh fires after ~5 seconds', async ({ page }) => {
    let fetchCount = 0;
    await page.route('**/api/projects', route => {
      fetchCount++;
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(mockProjects()),
      });
    });

    await page.goto('/');
    await waitForProjects(page);

    const initialCount = fetchCount;
    await page.waitForTimeout(6000);

    expect(fetchCount).toBeGreaterThan(initialCount);
  });
});
