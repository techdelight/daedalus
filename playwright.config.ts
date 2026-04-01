// Copyright (C) 2026 Techdelight BV

import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './e2e',
  timeout: 30_000,
  retries: 0,
  use: {
    baseURL: 'http://127.0.0.1:3111',
    headless: true,
  },
  projects: [
    {
      name: 'chromium',
      use: { browserName: 'chromium' },
    },
  ],
  webServer: {
    command: './daedalus web --port 3111 --no-auth',
    port: 3111,
    timeout: 15_000,
    reuseExistingServer: false,
  },
});
