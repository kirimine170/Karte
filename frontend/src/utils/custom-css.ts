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

export function isMarpMarkdown(content: string): boolean {
    if (!content.startsWith('---')) {
        return false;
    }
    const fmEnd = content.indexOf('\n---\n');
    if (fmEnd <= 0) {
        return false;
    }
    const yamlContent = content.substring(4, fmEnd);
    const marpMatch = yamlContent.match(/^marp:\s*(true|false)\s*$/m);
    if (marpMatch && marpMatch[1] === 'true') {
        return true;
    }
    const hasHeader = yamlContent.match(/^header:\s*["']?/m);
    const hasFooter = yamlContent.match(/^footer:\s*["']?/m);
    const hasPaginate = yamlContent.match(/^paginate:\s*(true|false)\s*$/m);
    return Boolean(hasHeader || hasFooter || hasPaginate);
}

export function injectCustomCSS(html: string, customCss: string, theme: Theme): string {
    const themeVars = getThemeVariablesCSS();
    const baseCSS = getBasePreviewCSS();
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

export function applyCustomCssToHtml(content: string, html: string, customCss: string, theme: Theme): string {
    if (isMarpMarkdown(content)) {
        return html;
    }
    return injectCustomCSS(html, customCss, theme);
}
