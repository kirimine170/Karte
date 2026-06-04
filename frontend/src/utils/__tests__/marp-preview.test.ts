import { describe, expect, it } from 'vitest';
import { applyCustomCssToHtml, isMarpMarkdown, prepareCustomCssForInjection } from '../custom-css';
import { renderMarpPreview } from '../marp-preview';

describe('Marp preview rendering', () => {
    it('detects Marp frontmatter', () => {
        expect(isMarpMarkdown('---\nmarp: true\n---\n# Slide')).toBe(true);
        expect(isMarpMarkdown('---\nmarp: "true"\n---\n# Slide')).toBe(true);
        expect(isMarpMarkdown('---\ntitle: Note\n---\n# Note')).toBe(false);
    });

    it('renders a navigable Marp document with official Marp output', async () => {
        const html = await renderMarpPreview('---\nmarp: true\npaginate: true\ntitle: Deck\n---\n# A\n---\n# B');

        expect(html).toContain('data-marp-preview="true"');
        expect(html).toContain('data-marpit-svg');
        expect(html).toContain('karteMarpNext');
        expect(html).toContain('<title>Deck</title>');
        expect(html).toContain('background: #ffffff');
    });

    it('rewrites local image paths for the app media handler', async () => {
        const html = await renderMarpPreview('---\nmarp: true\n---\n![sample](data/image/sample.png)\n\n![bg](bg.jpg)');

        expect(html).toContain('src="/image/data/image/sample.png"');
        expect(html).toContain('url("bg.jpg")');
    });

    it('adds SVG background rects for WebKit foreignObject painting', async () => {
        const html = await renderMarpPreview(
            '---\nmarp: true\n---\n# Default\n\n---\n<!-- _class: invert -->\n# Invert'
        );

        expect(html).toContain('class="karte-marp-svg-background"');
        expect(html).toContain('fill="#ffffff"');
        expect(html).toContain('fill="#2a1835"');
    });

    it('injects Karte default and custom CSS into Marp preview HTML', async () => {
        const html = await renderMarpPreview('---\nmarp: true\n---\n# Styled');
        const styled = applyCustomCssToHtml(
            '---\nmarp: true\n---\n# Styled',
            html,
            'section h1 { text-decoration: underline; }',
            'light'
        );

        expect(styled).toContain('id="karte-custom-css"');
        expect(styled).toContain('--color-highlight: #96368f');
        expect(styled).toContain(':where(html[data-marp-preview="true"] div.marpit > svg > foreignObject > section)');
        expect(styled).toContain('width: 1280px !important');
        expect(styled).toContain('[data-marpit-advanced-background-container="true"]');
        expect(styled).toContain('section h1 { text-decoration: underline; }');
    });

    it('prepares Marp theme CSS for post-render preview injection', () => {
        const css = [
            '/* @theme hacksick */',
            "@import 'gaia';",
            "@import url('https://fonts.googleapis.com/css2?family=Noto+Sans+JP:wght@400;700&display=swap');",
            '@size 16:9 1280px 720px;',
            'section h1 { color: #7c3aed; }',
        ].join('\n');

        const prepared = prepareCustomCssForInjection(css);

        expect(prepared.imports).toContain('https://fonts.googleapis.com');
        expect(prepared.body).toContain('section h1 { color: #7c3aed; }');
        expect(prepared.body).not.toContain("@import 'gaia'");
        expect(prepared.body).not.toContain('@size 16:9');
    });

    it('keeps Marp theme imports before preview defaults', async () => {
        const html = await renderMarpPreview('---\nmarp: true\n---\n# Styled');
        const styled = applyCustomCssToHtml(
            '---\nmarp: true\n---\n# Styled',
            html,
            [
                "@import url('https://fonts.googleapis.com/css2?family=Noto+Sans+JP:wght@400;700&display=swap');",
                'section h1 { color: #7c3aed; }',
            ].join('\n'),
            'light'
        );

        const importIndex = styled.indexOf('@import url(');
        const defaultsIndex = styled.indexOf(':root{');
        const themeRuleIndex = styled.indexOf('section h1 { color: #7c3aed; }');

        expect(importIndex).toBeGreaterThanOrEqual(0);
        expect(defaultsIndex).toBeGreaterThan(importIndex);
        expect(themeRuleIndex).toBeGreaterThan(defaultsIndex);
    });
});
