// Copyright (C) 2026 Techdelight BV

import { test, expect } from '@playwright/test';
import { mockProjects, interceptProjects, waitForProjects } from './fixtures';

test.describe('Responsive Layout — Desktop', () => {
  test.beforeEach(({}, testInfo) => {
    test.skip(testInfo.project.name !== 'desktop-chromium', 'desktop-only');
  });

  test('thead is visible on desktop', async ({ page }) => {
    await interceptProjects(page, mockProjects());
    await page.goto('/');
    await waitForProjects(page);

    await expect(page.locator('thead')).toBeVisible();
  });

  test('all 5 columns are visible on desktop', async ({ page }) => {
    await interceptProjects(page, mockProjects());
    await page.goto('/');
    await waitForProjects(page);

    const firstRow = page.locator('#project-tbody tr').first();
    const cells = firstRow.locator('td');
    await expect(cells).toHaveCount(5);

    for (let i = 0; i < 5; i++) {
      await expect(cells.nth(i)).toBeVisible();
    }
  });

  test('rows use table-row display on desktop', async ({ page }) => {
    await interceptProjects(page, mockProjects());
    await page.goto('/');
    await waitForProjects(page);

    const row = page.locator('#project-tbody tr').first();
    const display = await row.evaluate(el => getComputedStyle(el).display);
    expect(display).toBe('table-row');
  });
});

test.describe('Responsive Layout — Mobile', () => {
  test.beforeEach(({}, testInfo) => {
    test.skip(testInfo.project.name !== 'mobile-chrome', 'mobile-only');
  });

  test('thead is hidden on mobile', async ({ page }) => {
    await interceptProjects(page, mockProjects());
    await page.goto('/');
    await waitForProjects(page);

    await expect(page.locator('thead')).not.toBeVisible();
  });

  test('Target and Last Used columns are hidden on mobile', async ({ page }) => {
    await interceptProjects(page, mockProjects());
    await page.goto('/');
    await waitForProjects(page);

    const firstRow = page.locator('#project-tbody tr').first();

    // 3rd column (Target) and 4th column (Last Used) should be hidden
    await expect(firstRow.locator('td').nth(2)).not.toBeVisible();
    await expect(firstRow.locator('td').nth(3)).not.toBeVisible();
  });

  test('card layout with border-radius on mobile', async ({ page }) => {
    await interceptProjects(page, mockProjects());
    await page.goto('/');
    await waitForProjects(page);

    const row = page.locator('#project-tbody tr').first();
    const borderRadius = await row.evaluate(el => getComputedStyle(el).borderRadius);
    expect(borderRadius).toBe('8px');
  });
});

test.describe('Responsive Layout — Viewport Resize', () => {
  test.beforeEach(({}, testInfo) => {
    test.skip(testInfo.project.name !== 'desktop-chromium', 'desktop-only');
  });

  test('resizing viewport toggles layout dynamically', async ({ page }) => {
    await interceptProjects(page, mockProjects());
    await page.goto('/');
    await waitForProjects(page);

    // Desktop: thead visible
    await expect(page.locator('thead')).toBeVisible();

    // Resize to mobile width
    await page.setViewportSize({ width: 390, height: 720 });

    // Mobile: thead hidden, card layout
    await expect(page.locator('thead')).not.toBeVisible();
    const row = page.locator('#project-tbody tr').first();
    const borderRadius = await row.evaluate(el => getComputedStyle(el).borderRadius);
    expect(borderRadius).toBe('8px');

    // Resize back to desktop
    await page.setViewportSize({ width: 1280, height: 720 });

    await expect(page.locator('thead')).toBeVisible();
    const display = await row.evaluate(el => getComputedStyle(el).display);
    expect(display).toBe('table-row');
  });
});
