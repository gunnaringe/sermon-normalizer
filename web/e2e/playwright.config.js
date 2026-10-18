import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './tests',
  timeout: 120_000,
  workers: 1, // one server, one encode at a time
  reporter: [['list']],
  use: {
    baseURL: process.env.BASE_URL || 'http://localhost:8080/',
    viewport: { width: 1280, height: 900 },
    launchOptions: { args: ['--autoplay-policy=no-user-gesture-required'] },
    trace: 'retain-on-failure',
  },
});
