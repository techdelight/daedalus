// Copyright (C) 2026 Techdelight BV

import { test, expect } from '@playwright/test';
import { mockProjects, interceptProjects, waitForProjects } from './fixtures';

test.describe('Terminal View', () => {
  test('attach switches to terminal view and updates title', async ({ page }) => {
    await interceptProjects(page, mockProjects());
    await page.goto('/');
    await waitForProjects(page);

    await page.locator('.btn-attach').first().click();

    await expect(page.locator('#terminal-view')).toHaveClass(/active/);
    await expect(page.locator('#project-view')).toHaveClass(/hidden/);
    await expect(page.locator('#terminal-project-name')).toHaveText('alpha');
    expect(await page.title()).toContain('alpha');
  });

  test('back button returns to project list and restores title', async ({ page }) => {
    await interceptProjects(page, mockProjects());
    await page.goto('/');
    await waitForProjects(page);

    await page.locator('.btn-attach').first().click();
    await expect(page.locator('#terminal-view')).toHaveClass(/active/);

    await page.locator('.btn-back').click();

    await expect(page.locator('#terminal-view')).not.toHaveClass(/active/);
    await expect(page.locator('#project-view')).not.toHaveClass(/hidden/);
    expect(await page.title()).toContain('Daedalus');
  });

  test('terminal container is present after attach', async ({ page }) => {
    await interceptProjects(page, mockProjects());
    await page.goto('/');
    await waitForProjects(page);

    await page.locator('.btn-attach').first().click();

    await expect(page.locator('#terminal-container')).toBeVisible();
  });

  test('auto-refresh stops in terminal view and resumes after back', async ({ page }) => {
    test.setTimeout(20000);

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

    // Attach to terminal — stops auto-refresh
    await page.locator('.btn-attach').first().click();
    const countAfterAttach = fetchCount;

    await page.waitForTimeout(6000);
    expect(fetchCount).toBe(countAfterAttach);

    // Go back — resumes auto-refresh
    await page.locator('.btn-back').click();
    const countAfterBack = fetchCount;

    await page.waitForTimeout(6000);
    expect(fetchCount).toBeGreaterThan(countAfterBack);
  });

  test('mobile input area visible on mobile viewport', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'mobile-chrome', 'mobile-only test');

    await interceptProjects(page, mockProjects());
    await page.goto('/');
    await waitForProjects(page);

    await page.locator('.btn-attach').first().click();

    await expect(page.locator('#mobile-input-area')).toBeVisible();
  });

  test('mobile input area hidden on desktop', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'desktop-chromium', 'desktop-only test');

    await interceptProjects(page, mockProjects());
    await page.goto('/');
    await waitForProjects(page);

    await page.locator('.btn-attach').first().click();

    await expect(page.locator('#mobile-input-area')).not.toBeVisible();
  });

  test('mobile textarea accepts text input', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'mobile-chrome', 'mobile-only test');

    await interceptProjects(page, mockProjects());
    await page.goto('/');
    await waitForProjects(page);

    await page.locator('.btn-attach').first().click();
    await expect(page.locator('#mobile-input-area')).toBeVisible();

    const textarea = page.locator('#mobile-input');
    await textarea.fill('ls -la');
    await expect(textarea).toHaveValue('ls -la');
  });

  test('Send button clears textarea after click', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'mobile-chrome', 'mobile-only test');

    await interceptProjects(page, mockProjects());
    await page.goto('/');
    await waitForProjects(page);

    await page.locator('.btn-attach').first().click();

    const textarea = page.locator('#mobile-input');
    await textarea.fill('echo hello');
    await expect(textarea).toHaveValue('echo hello');

    await page.locator('#mobile-send-btn').click();
    await expect(textarea).toHaveValue('');
  });

  test('Enter inserts newline and does not send', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'mobile-chrome', 'mobile-only test');

    await interceptProjects(page, mockProjects());
    await page.goto('/');
    await waitForProjects(page);

    await page.locator('.btn-attach').first().click();

    const textarea = page.locator('#mobile-input');
    await textarea.fill('line1');
    await textarea.press('Enter');
    await textarea.type('line2');

    const value = await textarea.inputValue();
    expect(value).toContain('line1');
    expect(value).toContain('line2');
  });

  test('Send button does nothing when textarea is empty', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'mobile-chrome', 'mobile-only test');

    await interceptProjects(page, mockProjects());
    await page.goto('/');
    await waitForProjects(page);

    await page.locator('.btn-attach').first().click();

    const textarea = page.locator('#mobile-input');
    await expect(textarea).toHaveValue('');

    // Click send on empty input — should not throw or change state
    await page.locator('#mobile-send-btn').click();
    await expect(textarea).toHaveValue('');
  });
});
