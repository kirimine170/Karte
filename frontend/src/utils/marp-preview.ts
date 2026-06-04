type MarpRenderer = {
    render(content: string): {
        html: string;
        css: string;
    };
};

const MARP_WIDTH = 1920;
const MARP_HEIGHT = 1080;

let marpPromise: Promise<MarpRenderer> | null = null;

async function getMarpRenderer(): Promise<MarpRenderer> {
    if (!marpPromise) {
        marpPromise = import('@marp-team/marp-core').then(({ Marp }) => new Marp({
            html: true,
            inlineSVG: true,
        }));
    }
    return marpPromise;
}

function escapeHtml(value: string): string {
    return value
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;')
        .replace(/'/g, '&#39;');
}

function extractFrontmatter(content: string): string {
    if (!content.startsWith('---')) {
        return '';
    }
    const match = content.match(/^---\s*\n([\s\S]*?)\n---(?:\s*\n|$)/);
    return match?.[1] || '';
}

function extractTitle(content: string): string {
    const yaml = extractFrontmatter(content);
    const title = yaml.match(/^title:\s*(?:"([^"]*)"|'([^']*)'|(.+))\s*$/m);
    return (title?.[1] || title?.[2] || title?.[3] || 'Presentation').trim();
}

function rewriteAssetPath(path: string): string {
    const trimmed = path.trim()
        .replace(/&quot;/g, '"')
        .replace(/&#34;/g, '"')
        .replace(/&#39;/g, "'")
        .replace(/^['"]|['"]$/g, '');
    if (
        !trimmed
        || trimmed.startsWith('http://')
        || trimmed.startsWith('https://')
        || trimmed.startsWith('data:')
        || trimmed.startsWith('blob:')
        || trimmed.startsWith('/image/')
        || trimmed.startsWith('#')
    ) {
        return trimmed;
    }
    if (trimmed.startsWith('data/image/')) {
        return `/image/${trimmed}`;
    }
    return trimmed;
}

function rewriteMarpAssetPaths(html: string): string {
    let output = html.replace(/\s(src|href)=("([^"]*)"|'([^']*)')/gi, (match, attr, quoted, doubleValue, singleValue) => {
        const value = doubleValue ?? singleValue ?? '';
        const rewritten = rewriteAssetPath(value);
        const quote = quoted.startsWith("'") ? "'" : '"';
        return ` ${attr}=${quote}${escapeHtml(rewritten)}${quote}`;
    });

    output = output.replace(/url\(([^)]+)\)/gi, (match, rawPath) => {
        const rewritten = rewriteAssetPath(rawPath);
        return `url("${rewritten.replace(/"/g, '\\"')}")`;
    });

    return output;
}

function normalizeMarpDimensionsCSS(css: string): string {
    return css
        .replace(/\b1280px\b/g, `${MARP_WIDTH}px`)
        .replace(/\b720px\b/g, `${MARP_HEIGHT}px`)
        .replace(/(@page\s*\{\s*size:\s*)1280px\s+720px/gi, `$1${MARP_WIDTH}px ${MARP_HEIGHT}px`);
}

function normalizeMarpDimensionsHTML(html: string): string {
    return html
        .replace(/viewBox="0 0 1280 720"/g, `viewBox="0 0 ${MARP_WIDTH} ${MARP_HEIGHT}"`)
        .replace(/\bwidth="1280"\s+height="720"/g, `width="${MARP_WIDTH}" height="${MARP_HEIGHT}"`);
}

function getSlideBackgroundColor(svgHtml: string): string {
    if (/class="[^"]*\binvert\b[^"]*"/.test(svgHtml) || /data-class="[^"]*\binvert\b[^"]*"/.test(svgHtml)) {
        return '#2a1835';
    }
    return '#ffffff';
}

function stabilizeMarpSvgBackgrounds(html: string): string {
    return html.replace(/<svg\b([^>]*)data-marpit-svg=""([^>]*)>/g, (match, beforeAttrs, afterAttrs, offset, source) => {
        const svgEnd = source.indexOf('</svg>', offset);
        const svgHtml = svgEnd === -1 ? '' : source.slice(offset, svgEnd);
        const fill = getSlideBackgroundColor(svgHtml);
        return `<svg${beforeAttrs}data-marpit-svg=""${afterAttrs}><rect class="karte-marp-svg-background" x="0" y="0" width="${MARP_WIDTH}" height="${MARP_HEIGHT}" fill="${fill}"></rect>`;
    });
}

export async function renderMarpPreview(content: string): Promise<string> {
    const marp = await getMarpRenderer();
    const result = marp.render(content);
    const renderedHtml = stabilizeMarpSvgBackgrounds(normalizeMarpDimensionsHTML(rewriteMarpAssetPaths(result.html)));
    const renderedCss = normalizeMarpDimensionsCSS(result.css);
    const title = escapeHtml(extractTitle(content));

    return `<!doctype html>
<html lang="ja" data-marp-preview="true" data-printout="marp">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>${title}</title>
  <style>${renderedCss}</style>
  <style>
    html, body {
      width: 100%;
      height: 100%;
      margin: 0;
      background: #ffffff;
      color: #111827;
      overflow: hidden;
    }
    body {
      display: flex;
      flex-direction: column;
      align-items: center;
      justify-content: center;
      font-family: system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
    }
    .karte-marp-stage {
      width: 100%;
      height: calc(100vh - 56px);
      display: flex;
      align-items: center;
      justify-content: center;
      overflow: hidden;
      background: #ffffff;
    }
    .marpit {
      width: 100%;
      height: 100%;
      display: flex;
      align-items: center;
      justify-content: center;
    }
    .marpit > svg[data-marpit-svg] {
      display: none;
      width: min(100vw, calc((100vh - 56px) * 16 / 9));
      max-width: 100vw;
      max-height: calc(100vh - 56px);
      height: auto;
      background: #fff;
      box-shadow: 0 18px 48px rgba(0, 0, 0, 0.35);
    }
    .marpit > svg[data-marpit-svg].karte-marp-active {
      display: block;
    }
    .karte-marp-overview {
      height: auto;
      min-height: 100%;
      overflow: auto;
      justify-content: flex-start;
      padding: 24px 0 64px;
      box-sizing: border-box;
    }
    .karte-marp-overview .karte-marp-stage {
      height: auto;
      overflow: visible;
    }
    .karte-marp-overview .marpit {
      height: auto;
      min-height: auto;
      flex-direction: column;
      gap: 24px;
    }
    .karte-marp-overview .marpit > svg[data-marpit-svg] {
      display: block;
      width: min(92vw, 960px);
      max-height: none;
    }
    .karte-marp-controls {
      position: fixed;
      left: 50%;
      bottom: 12px;
      transform: translateX(-50%);
      display: flex;
      align-items: center;
      gap: 8px;
      z-index: 10;
      padding: 8px;
      border-radius: 8px;
      background: rgba(17, 24, 39, 0.86);
      color: #f9fafb;
      box-shadow: 0 8px 24px rgba(0, 0, 0, 0.28);
    }
    .karte-marp-controls button {
      min-width: 36px;
      height: 32px;
      border: 1px solid rgba(249, 250, 251, 0.24);
      border-radius: 6px;
      background: rgba(255, 255, 255, 0.08);
      color: inherit;
      cursor: pointer;
    }
    .karte-marp-controls button:disabled {
      opacity: 0.45;
      cursor: default;
    }
    .karte-marp-counter {
      min-width: 72px;
      text-align: center;
      font-size: 13px;
      font-variant-numeric: tabular-nums;
    }
    html[data-export-target="pdf"],
    html[data-export-target="pdf"] body {
      height: auto;
      overflow: visible;
      background: #fff;
    }
    html[data-export-target="pdf"] .karte-marp-stage,
    html[data-export-target="pdf"] .marpit {
      display: block;
      height: auto;
      overflow: visible;
    }
    html[data-export-target="pdf"] .marpit > svg[data-marpit-svg] {
      display: block !important;
      width: 100vw;
      max-width: none;
      max-height: none;
      box-shadow: none;
      page-break-after: always;
      break-after: page;
    }
    html[data-export-target="pdf"] .karte-marp-controls {
      display: none;
    }
  </style>
</head>
<body>
  <main class="karte-marp-stage">
    ${renderedHtml}
  </main>
  <nav class="karte-marp-controls" aria-label="Marp slide navigation">
    <button type="button" id="karteMarpPrev" title="Previous slide">‹</button>
    <span class="karte-marp-counter"><span id="karteMarpCurrent">1</span> / <span id="karteMarpTotal">1</span></span>
    <button type="button" id="karteMarpNext" title="Next slide">›</button>
    <button type="button" id="karteMarpOverview" title="Toggle overview">▦</button>
  </nav>
  <script>
    (function () {
      var slides = Array.prototype.slice.call(document.querySelectorAll('.marpit > svg[data-marpit-svg]'));
      var current = 0;
      var total = slides.length || 1;
      var currentLabel = document.getElementById('karteMarpCurrent');
      var totalLabel = document.getElementById('karteMarpTotal');
      var prev = document.getElementById('karteMarpPrev');
      var next = document.getElementById('karteMarpNext');
      var overview = document.getElementById('karteMarpOverview');
      function show(index) {
        current = Math.max(0, Math.min(index, total - 1));
        slides.forEach(function (slide, slideIndex) {
          slide.classList.toggle('karte-marp-active', slideIndex === current);
          slide.setAttribute('aria-hidden', slideIndex === current ? 'false' : 'true');
        });
        if (currentLabel) currentLabel.textContent = String(current + 1);
        if (totalLabel) totalLabel.textContent = String(total);
        if (prev) prev.disabled = current === 0;
        if (next) next.disabled = current >= total - 1;
        document.documentElement.dataset.marpSlide = String(current + 1);
      }
      function setOverview(enabled) {
        document.body.classList.toggle('karte-marp-overview', enabled);
        slides.forEach(function (slide) {
          slide.classList.toggle('karte-marp-active', enabled ? true : slides.indexOf(slide) === current);
          slide.setAttribute('aria-hidden', enabled ? 'false' : (slides.indexOf(slide) === current ? 'false' : 'true'));
        });
      }
      if (prev) prev.addEventListener('click', function () { show(current - 1); });
      if (next) next.addEventListener('click', function () { show(current + 1); });
      if (overview) overview.addEventListener('click', function () { setOverview(!document.body.classList.contains('karte-marp-overview')); });
      document.addEventListener('keydown', function (event) {
        if (event.key === 'ArrowRight' || event.key === 'PageDown' || event.key === ' ') {
          event.preventDefault();
          show(current + 1);
        } else if (event.key === 'ArrowLeft' || event.key === 'PageUp') {
          event.preventDefault();
          show(current - 1);
        } else if (event.key === 'Home') {
          event.preventDefault();
          show(0);
        } else if (event.key === 'End') {
          event.preventDefault();
          show(total - 1);
        } else if (event.key.toLowerCase() === 'o') {
          setOverview(!document.body.classList.contains('karte-marp-overview'));
        }
      });
      window.__karteMarpShowSlide = show;
      show(0);
    })();
  </script>
</body>
</html>`;
}
