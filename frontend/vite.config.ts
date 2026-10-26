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
  build: { target: 'esnext', rollupOptions: { input: { main: path.resolve(__dirname, 'index.html'), researchWall: path.resolve(__dirname, 'research-wall.html') } } },
  plugins: [{
    name: 'research-wall-pdf-assets',
    generateBundle() {
      const root = path.resolve(__dirname, 'node_modules/pdfjs-dist');
      const copy = (dir: string) => {
        for (const entry of fs.readdirSync(path.join(root, dir), { withFileTypes: true })) {
          const rel = `${dir}/${entry.name}`;
          if (entry.isDirectory()) copy(rel);
          else this.emitFile({ type: 'asset', fileName: `pdfjs/${rel}`, source: fs.readFileSync(path.join(root, rel)) });
        }
      };
      for (const dir of ['cmaps', 'standard_fonts', 'wasm']) copy(dir);
    },
    configureServer(server) {
      server.middlewares.use('/pdfjs', (req, res, next) => {
        const rel = decodeURIComponent((req.url || '').split('?')[0] || '');
        if (!/^\/(cmaps|standard_fonts|wasm)\/[a-zA-Z0-9_.-]+$/.test(rel)) return next();
        const file = path.resolve(__dirname, 'node_modules/pdfjs-dist', '.' + rel);
        if (!fs.existsSync(file)) return next();
        res.setHeader('Content-Type', file.endsWith('.wasm') ? 'application/wasm' : 'application/octet-stream');
        fs.createReadStream(file).pipe(res);
      });
    },
  }],
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
