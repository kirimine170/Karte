import { defineConfig } from 'vite';
import path from 'path';
import fs from 'fs';

const appModulePath = path.resolve(__dirname, 'wailsjs/wailsjs/go/main/App.js');
const runtimeModulePath = path.resolve(__dirname, 'wailsjs/wailsjs/runtime/runtime.js');
const hasWailsBindings = fs.existsSync(appModulePath) && fs.existsSync(runtimeModulePath);
const useStubModules = process.env.VITE_USE_WAILS_STUBS === 'true' || !hasWailsBindings;

const alias = useStubModules
  ? {
      '../../wailsjs/wailsjs/go/main/App': path.resolve(__dirname, 'src/test-support/wails-stubs.ts'),
      '../../wailsjs/wailsjs/runtime/runtime': path.resolve(
        __dirname,
        'src/test-support/wails-runtime-stub.ts'
      ),
    }
  : {};

export default defineConfig({
  server: {
    host: true
  },
  resolve: {
    alias: {
      ...alias,
      '@': path.resolve(__dirname, './src'),
    },
    extensions: ['.ts', '.tsx', '.js', '.jsx', '.json'],
  }
});
