import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { PreviewSession, type PreviewSessionState } from '../preview-session';

describe('PreviewSession', () => {
    let iframe: HTMLIFrameElement;
    let states: PreviewSessionState[];
    let session: PreviewSession;

    beforeEach(() => {
        document.body.innerHTML = '<iframe id="preview"></iframe>';
        iframe = document.getElementById('preview') as HTMLIFrameElement;
        states = [];
        session = new PreviewSession(iframe, (state) => states.push(state));
    });

    afterEach(() => {
        session.destroy();
        vi.restoreAllMocks();
    });

    it('uses one Marp adapter and preserves the current slide by stable ID after editing', () => {
        renderPreview(iframe, marpHtml([
            ['intro', 'Intro'],
            ['details', 'Details'],
        ]));
        session.rendered('content/slides.md');

        expect(session.currentState).toMatchObject({
            kind: 'marp',
            currentPage: 1,
            pageCount: 2,
        });
        expect(session.next()).toBe(true);
        expect(session.currentState.currentPage).toBe(2);

        renderPreview(iframe, marpHtml([
            ['cover', 'Cover'],
            ['intro', 'Intro'],
            ['details', 'Details edited'],
        ]));
        session.rendered('content/slides.md');

        expect(session.currentState.currentPage).toBe(3);
        expect(activeMarpSlide(iframe)?.dataset.slideId).toBe('details');
    });

    it('uses an index fallback for finite Markdown and detects asynchronously created pages', async () => {
        renderPreview(iframe, finiteHtml(['One', 'Two']));
        session.rendered('content/report.md');
        session.next();
        expect(session.currentState).toMatchObject({
            kind: 'finite-markdown',
            currentPage: 2,
            pageCount: 2,
        });

        renderPreview(iframe, '<!doctype html><html data-printout="A4"><body></body></html>');
        session.rendered('content/report.md');
        expect(session.currentState).toMatchObject({
            kind: 'finite-markdown',
            paged: false,
        });

        const pages = iframe.contentDocument!.createElement('div');
        pages.innerHTML = [
            '<section class="karte-print-page">One edited</section>',
            '<section class="karte-print-page">Two edited</section>',
            '<section class="karte-print-page">Three</section>',
        ].join('');
        iframe.contentDocument!.body.appendChild(pages);
        await flushMutationObserver();

        expect(session.currentState).toMatchObject({
            kind: 'finite-markdown',
            paged: true,
            currentPage: 2,
            pageCount: 3,
        });
    });

    it('keeps infinite Markdown in scroll mode and resets a different document to page one', () => {
        renderPreview(iframe, finiteHtml(['One', 'Two']));
        session.rendered('content/first.md');
        session.next();

        renderPreview(iframe, finiteHtml(['Other one', 'Other two']));
        session.rendered('content/second.md');
        expect(session.currentState.currentPage).toBe(1);

        renderPreview(
            iframe,
            '<!doctype html><html data-printout="infinite"><body><article>Scrollable</article></body></html>'
        );
        session.rendered('content/second.md');

        expect(session.currentState).toEqual({
            kind: 'infinite',
            paged: false,
            currentPage: 0,
            pageCount: 0,
            canGoPrevious: false,
            canGoNext: false,
        });
        expect(session.next()).toBe(false);
    });

    it('does not duplicate iframe keyboard handling across redraws and removes it on destroy', () => {
        renderPreview(iframe, marpHtml([
            ['one', 'One'],
            ['two', 'Two'],
            ['three', 'Three'],
        ]));
        session.rendered('content/slides.md');
        session.rendered('content/slides.md');

        const firstArrow = iframeKeyboardEvent('ArrowRight');
        iframe.contentDocument!.dispatchEvent(firstArrow);
        expect(firstArrow.defaultPrevented).toBe(true);
        expect(session.currentState.currentPage).toBe(2);

        session.destroy();
        const secondArrow = iframeKeyboardEvent('ArrowRight');
        iframe.contentDocument!.dispatchEvent(secondArrow);

        expect(secondArrow.defaultPrevented).toBe(false);
        expect(activeMarpSlide(iframe)?.dataset.slideId).toBe('two');
        expect(states[states.length - 1]?.kind).toBe('inactive');
    });
});

function renderPreview(iframe: HTMLIFrameElement, html: string): void {
    const document = iframe.contentDocument!;
    document.open();
    document.write(html);
    document.close();
}

function marpHtml(slides: [id: string, label: string][]): string {
    return `<!doctype html>
        <html><body>
            <div id="presentation"><div class="slide-container">
                ${slides.map(([id, label], index) => `
                    <section class="slide${index === 0 ? ' active' : ''}"
                        data-slide-index="${index}" data-slide-id="${id}">${label}</section>
                `).join('')}
            </div></div>
            <span id="current-slide">1</span><span id="total-slides">${slides.length}</span>
            <button id="prev-btn"></button><button id="next-btn"></button>
        </body></html>`;
}

function finiteHtml(pages: string[]): string {
    return `<!doctype html>
        <html data-printout="A4"><body><div class="karte-print-pages">
            ${pages.map((page) => `<section class="karte-print-page">${page}</section>`).join('')}
        </div></body></html>`;
}

function activeMarpSlide(iframe: HTMLIFrameElement): HTMLElement | null {
    return iframe.contentDocument!.querySelector<HTMLElement>('section.slide.active');
}

function iframeKeyboardEvent(key: string): KeyboardEvent {
    return new KeyboardEvent('keydown', {
        key,
        bubbles: true,
        cancelable: true,
    });
}

async function flushMutationObserver(): Promise<void> {
    await Promise.resolve();
    await Promise.resolve();
}
