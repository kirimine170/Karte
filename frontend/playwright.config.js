import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './tests',
  timeout: 120000,
  use: {
    baseURL: 'http://127.0.0.1:4173',
    headless: true,
    trace: 'on-first-retry'
  },
  webServer: {
    command: 'npm run dev -- --host --port 4173 --clearScreen false',
    url: 'http://127.0.0.1:4173',
    reuseExistingServer: !process.env.CI,
    timeout: 120000,
    env: {
      VITE_USE_WAILS_STUBS: 'true'
    }
  }
});
