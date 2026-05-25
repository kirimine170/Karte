import { describe, expect, it } from 'vitest';
import { isMarpMarkdown } from '../custom-css';
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
    });

    it('rewrites local image paths for the app media handler', async () => {
        const html = await renderMarpPreview('---\nmarp: true\n---\n![sample](data/image/sample.png)\n\n![bg](bg.jpg)');

        expect(html).toContain('src="/image/data/image/sample.png"');
        expect(html).toContain('url("bg.jpg")');
    });
});
