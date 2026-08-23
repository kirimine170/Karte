import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const distDirectory = new URL('../dist/', import.meta.url);
const assetsDirectory = new URL('assets/', distDirectory);

if (!existsSync(assetsDirectory)) {
    throw new Error('Preview asset verification requires a completed Vite build');
}

const javascriptAssets = readdirSync(assetsDirectory)
    .filter((name) => name.endsWith('.js'))
    .map((name) => ({
        name,
        source: readFileSync(new URL(name, assetsDirectory), 'utf8'),
    }));

const katexStyleChunk = javascriptAssets.find(({ source }) =>
    source.includes('@font-face') && source.includes('KaTeX_')
);
if (!katexStyleChunk) {
    throw new Error('Vite did not emit the demand-loaded KaTeX stylesheet chunk');
}

const fontURLs = Array.from(katexStyleChunk.source.matchAll(/url\(([^)]+)\)/g), (match) =>
    (match[1] ?? '').replace(/^['"]|['"]$/g, '')
);
if (fontURLs.length === 0) {
    throw new Error('The emitted KaTeX stylesheet does not contain font references');
}
for (const fontURL of fontURLs) {
    if (fontURL.startsWith('data:')) {
        continue;
    }
    if (!fontURL.startsWith('/assets/')) {
        throw new Error(`KaTeX emitted a non-local or iframe-relative font URL: ${fontURL}`);
    }
    const fontPath = join(fileURLToPath(assetsDirectory), fontURL.slice('/assets/'.length));
    if (!existsSync(fontPath)) {
        throw new Error(`KaTeX emitted a missing font asset: ${fontURL}`);
    }
}

const mermaidRuntime = javascriptAssets.find(({ source }) =>
    /globalThis\[(['"])mermaid\1\]\s*=/.test(source)
);
if (!mermaidRuntime) {
    throw new Error('The local Mermaid UMD asset does not expose window.mermaid');
}

const remotePreviewAssetPattern = /https?:\/\/(?:cdn\.jsdelivr\.net|unpkg\.com|cdnjs\.cloudflare\.com|esm\.sh|cdn\.skypack\.dev)\/[^'"`\s]*(?:mermaid|katex)/i;
const remoteAssetOwner = javascriptAssets.find(({ source }) => remotePreviewAssetPattern.test(source));
if (remoteAssetOwner) {
    throw new Error(`A remote Mermaid or KaTeX asset remains in ${remoteAssetOwner.name}`);
}

console.log(
    `Verified local preview assets: ${fontURLs.length} KaTeX font URLs and ${mermaidRuntime.name}`
);
