// Copyright (C) 2026 Techdelight BV

import { Page } from '@playwright/test';

export interface Project {
  name: string;
  directory: string;
  target: string;
  lastUsed: string;
  running: boolean;
  sessionCount: number;
}

export function mockProjects(): Project[] {
  return [
    { name: 'alpha', directory: '/path/alpha', target: 'dev', lastUsed: new Date().toISOString(), running: true, sessionCount: 1 },
    { name: 'beta', directory: '/path/beta', target: 'dev', lastUsed: new Date().toISOString(), running: false, sessionCount: 0 },
    { name: 'gamma', directory: '/path/gamma', target: 'godot', lastUsed: new Date().toISOString(), running: false, sessionCount: 0 },
  ];
}

export async function interceptProjects(page: Page, projects: Project[]) {
  await page.route('**/api/projects', route => {
    if (route.request().method() === 'GET') {
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(projects),
      });
    } else {
      route.continue();
    }
  });
}

export async function interceptAction(page: Page, name: string, action: string, response: { status: number; body: object }) {
  await page.route(`**/api/projects/${name}/${action}`, route => {
    route.fulfill({
      status: response.status,
      contentType: response.status >= 400 ? 'text/plain' : 'application/json',
      body: response.status >= 400 ? String(response.body) : JSON.stringify(response.body),
    });
  });
}

export async function waitForProjects(page: Page) {
  await page.waitForSelector('#project-tbody tr');
}
