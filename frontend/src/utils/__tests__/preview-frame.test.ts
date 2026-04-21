import { describe, expect, it, vi } from 'vitest';
import { restorePreviewFocus, type PreviewFocusTarget } from '../preview-frame';

function createPreviewIframe(html: string): HTMLIFrameElement {
    const iframe = document.createElement('iframe');
    document.body.appendChild(iframe);
    const doc = iframe.contentDocument;
    if (!doc) {
        throw new Error('iframe document is not available');
    }
    doc.open();
    doc.write(html);
    doc.close();
    return iframe;
}

describe('restorePreviewFocus', () => {
    it('scrolls to the matching image for inserted-image targets', () => {
        const iframe = createPreviewIframe('<body><p>Intro</p><img id="target" src="/image/data/image/mock-1.png" alt="mock-1"></body>');
        const image = iframe.contentDocument?.getElementById('target') as HTMLElement;
        (image as HTMLElement & { scrollIntoView: () => void }).scrollIntoView = () => {};
        const spy = vi.spyOn(image, 'scrollIntoView').mockImplementation(() => {});

        const restored = restorePreviewFocus(iframe, {
            type: 'inserted-image',
            path: 'data/image/mock-1.png',
            alt: 'mock-1',
            title: 'mock-1.png',
            startOffset: 10,
            endOffset: 20,
            lineStart: 2,
            lineEnd: 2,
            scrollRatio: 0,
        });

        expect(restored).toBe(true);
        expect(spy).toHaveBeenCalled();
    });

    it('scrolls the containing print page for paginated image targets', () => {
        const iframe = createPreviewIframe('<body><section class="karte-print-page" id="page"><div class="karte-print-page-content"><img id="target" src="/image/data/image/mock-1.png" alt="mock-1"></div></section></body>');
        const page = iframe.contentDocument?.getElementById('page') as HTMLElement;
        const image = iframe.contentDocument?.getElementById('target') as HTMLElement;
        (page as HTMLElement & { scrollIntoView: () => void }).scrollIntoView = () => {};
        (image as HTMLElement & { scrollIntoView: () => void }).scrollIntoView = () => {};
        const pageSpy = vi.spyOn(page, 'scrollIntoView').mockImplementation(() => {});
        const imageSpy = vi.spyOn(image, 'scrollIntoView').mockImplementation(() => {});

        const restored = restorePreviewFocus(iframe, {
            type: 'inserted-image',
            path: 'data/image/mock-1.png',
            alt: 'mock-1',
            title: 'mock-1.png',
            startOffset: 10,
            endOffset: 20,
            lineStart: 2,
            lineEnd: 2,
            pageTextSignature: 'mock-1',
            pageHeadingText: '',
            scrollRatio: 0,
        });

        expect(restored).toBe(true);
        expect(pageSpy).toHaveBeenCalled();
        expect(imageSpy).toHaveBeenCalled();
    });

    it('finds a paragraph from text-caret signatures', () => {
        const iframe = createPreviewIframe('<body><article><h2>Section A</h2><p id="target">Beta line content here</p></article></body>');
        const target = iframe.contentDocument?.getElementById('target') as HTMLElement;
        (target as HTMLElement & { scrollIntoView: () => void }).scrollIntoView = () => {};
        const spy = vi.spyOn(target, 'scrollIntoView').mockImplementation(() => {});

        const restored = restorePreviewFocus(iframe, {
            type: 'text-caret',
            startOffset: 10,
            endOffset: 10,
            lineStart: 3,
            lineEnd: 3,
            lineText: 'Beta line content here',
            previousLineText: 'Alpha',
            nextLineText: 'Gamma',
            headingText: 'Section A',
            scrollRatio: 0,
        });

        expect(restored).toBe(true);
        expect(spy).toHaveBeenCalled();
    });

    it('falls back to the scroll ratio when no target matches', () => {
        const iframe = createPreviewIframe('<body><div style="height: 2000px">Long content</div></body>');
        const doc = iframe.contentDocument;
        const scrollRoot = doc?.scrollingElement || doc?.documentElement || doc?.body;
        if (!doc || !scrollRoot) {
            throw new Error('missing scroll root');
        }

        Object.defineProperty(scrollRoot, 'scrollHeight', { value: 2000, configurable: true });
        Object.defineProperty(scrollRoot, 'clientHeight', { value: 500, configurable: true });
        scrollRoot.scrollTop = 0;

        const restored = restorePreviewFocus(iframe, {
            type: 'dropped-anchor',
            tagName: 'p',
            textSignature: 'does-not-exist',
            headingText: 'missing',
            scrollRatio: 0.5,
        } satisfies PreviewFocusTarget);

        expect(restored).toBe(true);
        expect(scrollRoot.scrollTop).toBe(750);
    });
});
