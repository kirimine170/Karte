import { defineConfig } from 'vitest/config';
import path from 'path';

export default defineConfig({
    test: {
        environment: 'jsdom',
        globals: true,
        setupFiles: ['./src/test-support/setup.ts'],
        exclude: [
            '**/node_modules/**',
            '**/dist/**',
            '**/tests/**',  // E2Eテストディレクトリを除外
            '**/*.e2e.spec.ts',  // E2Eテストファイルを除外
            '**/*.e2e.spec.js'
        ],
        coverage: {
            provider: 'v8',
            reporter: ['text', 'json', 'html'],
            exclude: [
                'node_modules/',
                'src/test-support/',
                '**/*.d.ts',
                '**/*.config.*',
                '**/dist/**'
            ]
        }
    },
    resolve: {
        alias: {
            '@': path.resolve(__dirname, './src'),
        },
    },
});
