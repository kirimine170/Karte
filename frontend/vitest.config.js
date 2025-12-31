import { defineConfig } from 'vitest/config';

export default defineConfig({
    test: {
        environment: 'jsdom',
        globals: true,
        exclude: [
            '**/node_modules/**',
            '**/dist/**',
            '**/tests/**',  // E2Eテストディレクトリを除外
            '**/*.e2e.spec.ts',  // E2Eテストファイルを除外
            '**/*.e2e.spec.js'
        ]
    }
});
