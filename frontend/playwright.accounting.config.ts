import { defineConfig } from '@playwright/test';

export default defineConfig({
    testDir: './tests', testMatch: 'accounting.spec.ts', timeout: 30000,
    use: { baseURL: 'http://127.0.0.1:4182', headless: true, viewport: { width: 1400, height: 1000 }, trace: 'retain-on-failure' },
    webServer: {
        command: 'npm run dev -- --host 127.0.0.1 --port 4182 --strictPort',
        url: 'http://127.0.0.1:4182', reuseExistingServer: !process.env.CI,
        env: { VITE_USE_WAILS_STUBS: 'true' },
    },
});
