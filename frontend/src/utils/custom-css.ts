import type { Theme } from '../types/ui-state';

const THEME_VAR_NAMES = [
    '--main-background',
    '--text-color',
    '--browsername-color',
    '--backgroundcolor',
    '--backgroundcolor-unhover',
    '--opened-tab-backgroundcolor',
    '--border-color',
    '--border-color-unhover',
    '--borderline-color',
    '--shadow-color',
    '--shadow-color-unhover',
    '--input-color-unhover',
    '--loading-color',
    '--closebutton-color',
];

export function getThemeVariablesCSS(): string {
    const cs = getComputedStyle(document.documentElement);
    const lines = THEME_VAR_NAMES.map((name) => {
        const value = cs.getPropertyValue(name).trim();
        return value ? `${name}: ${value};` : '';
    }).filter(Boolean);
    return `:root{${lines.join('')}}`;
}

export function getBasePreviewCSS(): string {
    return `
      body{margin:16px; background: var(--main-background); color: var(--text-color);}
      a{color: var(--loading-color);}
      pre,code{font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace}
      pre{background: var(--backgroundcolor-unhover); padding:12px; border-radius:8px; overflow:auto}
      h1,h2,h3{margin-top:1.2em;}
      table{border-collapse:collapse}
      th,td{border:1px solid var(--border-color); padding:6px 10px}
    `;
}

export function getMarpPreviewCSS(): string {
    return `
      html[data-marp-preview="true"] {
        --color-background: #ffffff;
        --color-foreground: #363636;
        --color-highlight: #96368f;
        --color-sub-background: #e3cafa;
      }
      html[data-marp-preview="true"] div.marpit > svg > foreignObject > section {
        background: var(--color-background);
        color: var(--color-foreground);
        font-family: "メイリオ", "Hiragino Kaku Gothic ProN", system-ui, sans-serif;
      }
      html[data-marp-preview="true"] div.marpit > svg > foreignObject > section :is(h1, h2, h3, h4, h5, h6) {
        color: var(--color-highlight);
      }
      html[data-marp-preview="true"] div.marpit > svg > foreignObject > section :is(a, strong) {
        color: var(--color-highlight);
      }
      html[data-marp-preview="true"] div.marpit > svg > foreignObject > section :is(code, pre) {
        background: var(--color-sub-background);
      }
      html[data-marp-preview="true"] div.marpit > svg > foreignObject > section blockquote {
        border-left: 0.28em solid var(--color-highlight);
        background: rgba(227, 202, 250, 0.32);
      }
      html[data-marp-preview="true"] div.marpit > svg > foreignObject > section table :is(th, td) {
        border-color: rgba(150, 54, 143, 0.45);
      }
      html[data-marp-preview="true"] div.marpit > svg > foreignObject > section table th {
        background: var(--color-sub-background);
      }
      html[data-marp-preview="true"] div.marpit > svg > foreignObject > section.invert,
      html[data-marp-preview="true"] div.marpit > svg > foreignObject > section[data-marpit-advanced-background="background"] {
        --color-background: #2a1835;
        --color-foreground: #fbf7ff;
        --color-highlight: #e3cafa;
        --color-sub-background: #4b2a63;
      }
      html[data-marp-preview="true"] div.marpit > svg > foreignObject > section.lead {
        display: flex;
        flex-direction: column;
        justify-content: center;
        text-align: center;
      }
    `;
}

export function isMarpMarkdown(content: string): boolean {
    if (!content.startsWith('---')) {
        return false;
    }
    const frontmatterMatch = content.match(/^---\s*\n([\s\S]*?)\n---(?:\s*\n|$)/);
    if (!frontmatterMatch) {
        return false;
    }
    const yamlContent = frontmatterMatch[1];
    const marpMatch = yamlContent.match(/^marp:\s*['"]?(true|false)['"]?\s*$/m);
    if (marpMatch && marpMatch[1] === 'true') {
        return true;
    }
    const hasHeader = yamlContent.match(/^header:\s*["']?/m);
    const hasFooter = yamlContent.match(/^footer:\s*["']?/m);
    const hasPaginate = yamlContent.match(/^paginate:\s*['"]?(true|false)['"]?\s*$/m);
    const hasMarpSize = yamlContent.match(/^size:\s*["']?(16:9|4:3|A4|letter|[0-9]+:[0-9]+)["']?\s*$/m);
    return Boolean(hasHeader || hasFooter || hasPaginate || hasMarpSize);
}

export function injectCustomCSS(html: string, customCss: string, theme: Theme): string {
    const themeVars = getThemeVariablesCSS();
    const baseCSS = isRenderedMarpHtml(html) ? getMarpPreviewCSS() : getBasePreviewCSS();
    let cssToInject = themeVars + baseCSS;
    if (customCss) {
        cssToInject += `\n${customCss}`;
    }

    const customStyleRegex = /<style[^>]*id="karte-custom-css"[^>]*>[\s\S]*?<\/style>/i;
    if (customStyleRegex.test(html)) {
        html = html.replace(customStyleRegex, `<style id="karte-custom-css">${cssToInject}</style>`);
    } else if (html.includes('</head>')) {
        html = html.replace('</head>', `<style id="karte-custom-css">${cssToInject}</style></head>`);
    } else if (html.includes('<head>')) {
        html = html.replace('<head>', `<head><style id="karte-custom-css">${cssToInject}</style>`);
    } else if (html.includes('<html')) {
        html = html.replace(/<html([^>]*)>/i, (match, attrs) => {
            const normalized = attrs.replace(/\s*data-theme="[^"]*"/i, '');
            return `<html${normalized} data-theme="${theme}"><head><style id="karte-custom-css">${cssToInject}</style></head>`;
        });
    } else if (html.includes('<!doctype html>')) {
        if (html.includes('</style>')) {
            const lastStyleEnd = html.lastIndexOf('</style>');
            if (lastStyleEnd !== -1) {
                html =
                    html.slice(0, lastStyleEnd + 8) +
                    `\n<style id="karte-custom-css">${cssToInject}</style>` +
                    html.slice(lastStyleEnd + 8);
            }
        } else if (html.includes('<body>')) {
            html = html.replace('<body>', `<style id="karte-custom-css">${cssToInject}</style>\n<body>`);
        } else {
            html = html.replace('<!doctype html>', `<!doctype html>\n<style id="karte-custom-css">${cssToInject}</style>`);
        }
    } else {
        html = `<style id="karte-custom-css">${cssToInject}</style>\n${html}`;
    }

    if (html.includes('<html')) {
        html = html.replace(/<html([^>]*)>/i, (match, attrs) => {
            const normalized = attrs.replace(/\s*data-theme="[^"]*"/i, '');
            return `<html${normalized} data-theme="${theme}">`;
        });
    }

    return html;
}

function isRenderedMarpHtml(html: string): boolean {
    return /data-marp-preview=["']true["']/.test(html) || /data-marpit-svg/.test(html);
}

export function applyCustomCssToHtml(content: string, html: string, customCss: string, theme: Theme): string {
    return injectCustomCSS(html, customCss, theme);
}
