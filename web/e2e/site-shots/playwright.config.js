import { defineConfig } from '@playwright/test';

// The website's screenshots and video: see run.sh.
export default defineConfig({
  testDir: '.',
  outputDir: './test-results', // its own, so it can run beside the other browser tests
  timeout: 900_000,
  workers: 1,
  reporter: [['list']],
  use: {
    baseURL: 'http://localhost:8080/',
    viewport: { width: 1280, height: 800 },
    launchOptions: { args: ['--autoplay-policy=no-user-gesture-required'] },
    trace: 'retain-on-failure',
  },
});
