import { defineConfig } from 'vite';
import path from 'path';
import fs from 'fs';

const wailsDir = path.resolve(__dirname, 'wailsjs');
const useStubModules = process.env.VITE_USE_WAILS_STUBS === 'true' || !fs.existsSync(wailsDir);

const alias = useStubModules
  ? {
      '../wailsjs/wailsjs/go/main/App': path.resolve(__dirname, 'src/test-support/wails-stubs.js'),
      '../wailsjs/wailsjs/runtime/runtime': path.resolve(
        __dirname,
        'src/test-support/wails-runtime-stub.js'
      ),
    }
  : {};

export default defineConfig({
  server: {
    host: true
  },
  resolve: {
    alias
  }
});
