import http from 'node:http';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { readFile, stat } from 'node:fs/promises';
import { createRequire } from 'node:module';

const root = path.dirname(fileURLToPath(import.meta.url));
const require = createRequire(import.meta.url);
let pdfRoot;
for (const candidate of [root, path.resolve(root, '../../frontend')]) {
  try { pdfRoot = path.dirname(require.resolve('pdfjs-dist/package.json', { paths: [candidate] })); break; } catch {}
}
if (!pdfRoot) throw new Error('PDF.jsが必要です．このディレクトリで npm install を実行してください．');
const types = { '.html': 'text/html', '.mjs': 'text/javascript', '.js': 'text/javascript', '.css': 'text/css', '.svg': 'image/svg+xml', '.pdf': 'application/pdf', '.wav': 'audio/wav', '.md': 'text/plain', '.wasm': 'application/wasm', '.json': 'application/json' };
const publicFiles = new Set(['index.html', 'app.mjs', 'model.mjs', 'sample.mjs', 'style.css']);
const port = Number(process.env.PORT || 4180);
http.createServer(async (req, res) => {
  try {
    const pathname = decodeURIComponent(new URL(req.url, 'http://localhost').pathname);
    let base = root;
    let relative = pathname === '/' ? 'index.html' : pathname.slice(1);
    if (relative.startsWith('vendor/pdfjs/')) { base = pdfRoot; relative = relative.slice('vendor/pdfjs/'.length); }
    else if (!publicFiles.has(relative) && !relative.startsWith('assets/')) { res.writeHead(404).end(); return; }
    const file = path.resolve(base, relative);
    if (!file.startsWith(base + path.sep) || !(await stat(file)).isFile()) { res.writeHead(404).end(); return; }
    const bytes = await readFile(file);
    const range = req.headers.range?.match(/^bytes=(\d+)-(\d*)$/);
    res.setHeader('Content-Type', types[path.extname(file)] || 'application/octet-stream');
    res.setHeader('Cache-Control', 'no-cache');
    res.setHeader('X-Content-Type-Options', 'nosniff');
    res.setHeader('Accept-Ranges', 'bytes');
    if (range) {
      const start = Number(range[1]), end = Math.min(range[2] ? Number(range[2]) : bytes.length - 1, bytes.length - 1);
      if (start > end || start >= bytes.length) { res.writeHead(416, { 'Content-Range': `bytes */${bytes.length}` }).end(); return; }
      res.writeHead(206, { 'Content-Range': `bytes ${start}-${end}/${bytes.length}`, 'Content-Length': end - start + 1 });
      res.end(bytes.subarray(start, end + 1));
    } else { res.writeHead(200, { 'Content-Length': bytes.length }); res.end(bytes); }
  } catch { res.writeHead(404).end('Not found'); }
}).listen(port, '127.0.0.1', () => console.log(`Karte Research Wall: http://127.0.0.1:${port}/`));
