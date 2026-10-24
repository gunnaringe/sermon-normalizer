import { defineConfig } from '@playwright/test';

// The website (site/), served as files by serve.js: see run.sh.
export default defineConfig({
  testDir: '.',
  outputDir: './test-results', // its own, so it can run beside the other browser tests
  timeout: 60_000,
  workers: 2,
  reporter: [['line']],
  use: {
    baseURL: 'http://127.0.0.1:8090/',
    viewport: { width: 1280, height: 900 },
    trace: 'retain-on-failure',
  },
  webServer: { command: 'node serve.js', url: 'http://127.0.0.1:8090/', reuseExistingServer: false },
});
