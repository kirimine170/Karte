import { setupTimestampLinkHandlers } from './preview-audio';
import { eventLogger } from './event-logger';

export type PreviewFocusTarget =
    | {
        type: 'text-caret';
        startOffset?: number;
        endOffset?: number;
        lineStart?: number;
        lineEnd?: number;
        lineText: string;
        previousLineText: string;
        nextLineText: string;
        headingText: string;
        scrollRatio: number;
    }
    | {
        type: 'inserted-image';
        path: string;
        alt: string;
        title: string;
        startOffset?: number;
        endOffset?: number;
        lineStart?: number;
        lineEnd?: number;
        pageTextSignature?: string;
        pageHeadingText?: string;
        scrollRatio: number;
    }
    | {
        type: 'inserted-csv';
        path: string;
        label: string;
        startOffset?: number;
        endOffset?: number;
        lineStart?: number;
        lineEnd?: number;
        pageTextSignature?: string;
        pageHeadingText?: string;
        scrollRatio: number;
    }
    | {
        type: 'dropped-anchor';
        tagName: string;
        textSignature: string;
        headingText: string;
        pageTextSignature?: string;
        pageHeadingText?: string;
        scrollRatio: number;
    }
    | {
        type: 'scroll-ratio-fallback';
        scrollRatio: number;
    };

type PreviewFrameOptions = {
    focusTarget?: PreviewFocusTarget | null;
};

type PreviewWindow = Window & {
    __karteCreatePrintoutPagination?: (doc?: Document) => {
        buildPages(): void;
    };
    __karteRunPrintoutPagination?: (doc?: Document) => unknown;
    __karteRunRenderEnhancers?: () => void;
    mermaid?: {
        initialize: (config: Record<string, unknown>) => void;
        run: (options: { nodes: NodeListOf<Element> }) => Promise<void> | void;
    };
    katex?: {
        render: (math: string, element: Element, options: { throwOnError: boolean; displayMode: boolean }) => void;
    };
    __karteMermaidReady?: boolean;
    __karteMermaidRendering?: boolean;
    __karteKaTeXReady?: boolean;
    __kartePrintoutDebug?: string;
};

const MERMAID_CDN = 'https://cdn.jsdelivr.net/npm/mermaid/dist/mermaid.min.js';
const KATEX_CSS = 'https://cdn.jsdelivr.net/npm/katex@latest/dist/katex.min.css';
const KATEX_JS = 'https://cdn.jsdelivr.net/npm/katex@latest/dist/katex.min.js';
const PREVIEW_MERMAID_SCRIPT_ID = 'karte-preview-mermaid-script';
const PREVIEW_KATEX_SCRIPT_ID = 'karte-preview-katex-script';
const PREVIEW_KATEX_CSS_ID = 'karte-preview-katex-css';
const PREVIEW_PRINTOUT_STATUS_ID = 'previewPrintoutStatus';
const statusPollingTimers = new WeakMap<HTMLIFrameElement, number>();
const lastPrintoutSignatures = new WeakMap<HTMLIFrameElement, string>();
const zeroPageRetryCount = new WeakMap<HTMLIFrameElement, number>();
const enhancerRetryTimers = new WeakMap<HTMLIFrameElement, number>();
const pendingFocusTargets = new WeakMap<HTMLIFrameElement, PreviewFocusTarget>();
const focusRetryTimers = new WeakMap<HTMLIFrameElement, number>();

export function writePreviewFrame(iframe: HTMLIFrameElement, html: string, options: PreviewFrameOptions = {}): void {
    const doc = iframe.contentDocument;
    if (!doc) {
        return;
    }
    if (options.focusTarget) {
        pendingFocusTargets.set(iframe, options.focusTarget);
    } else {
        pendingFocusTargets.delete(iframe);
    }
    const prevEnhancerTimer = enhancerRetryTimers.get(iframe);
    if (prevEnhancerTimer !== undefined) {
        window.clearTimeout(prevEnhancerTimer);
        enhancerRetryTimers.delete(iframe);
    }
    doc.open();
    doc.write(html);
    doc.close();
    ensureMermaidKaTeXStyles(doc);
    const win = iframe.contentWindow as PreviewWindow | null;
    if (win) {
        win.__karteMermaidReady = false;
        win.__karteMermaidRendering = false;
        win.__karteKaTeXReady = false;
    }
    schedulePreviewEnhancers(iframe);
    attachPreviewAssetLoadHandlers(iframe);
    rerunPrintoutPagination(iframe);
    attemptPreviewFocusRestore(iframe);
    schedulePreviewFocusRetry(iframe, 0);
    startPreviewPrintoutStatusPolling(iframe);
    iframe.addEventListener('load', () => {
        setupTimestampLinkHandlers(iframe);
        rerunPrintoutPagination(iframe);
        updatePreviewPrintoutStatus(iframe);
        attemptPreviewFocusRestore(iframe);
        schedulePreviewFocusRetry(iframe, 0);
    }, { once: true });
}

function schedulePreviewEnhancers(iframe: HTMLIFrameElement): void {
    const win = iframe.contentWindow as PreviewWindow | null;
    const doc = iframe.contentDocument;
    if (!win || !doc) {
        return;
    }
    kickPreviewEnhancers(iframe, 0);
}

function kickPreviewEnhancers(iframe: HTMLIFrameElement, attempt: number): void {
    const win = iframe.contentWindow as PreviewWindow | null;
    const doc = iframe.contentDocument;
    if (!win || !doc) {
        return;
    }

    try {
        win.__karteRunRenderEnhancers?.();
    } catch (error) {
        console.warn('[preview-frame] shared render enhancers failed', error);
    }
    ensureKaTeX(doc, win);
    ensureMermaid(doc, win);
    rerunPrintoutPagination(iframe);
    updatePreviewPrintoutStatus(iframe);
    attemptPreviewFocusRestore(iframe);
    schedulePreviewFocusRetry(iframe, attempt);

    const hasMermaidSource = doc.querySelector('.mermaid, pre > code.language-mermaid, pre > code.lang-mermaid');
    const hasRenderedMermaid = doc.querySelector('.mermaid svg');
    if (!hasMermaidSource || hasRenderedMermaid || attempt >= 40) {
        enhancerRetryTimers.delete(iframe);
        return;
    }

    const delayMs = Math.min(100 + attempt * 25, 500);
    const timer = window.setTimeout(() => kickPreviewEnhancers(iframe, attempt + 1), delayMs);
    enhancerRetryTimers.set(iframe, timer);
}

function ensureMermaidKaTeXStyles(doc: Document): void {
    if (doc.getElementById('karte-mermaid-katex-style')) {
        return;
    }
    const style = doc.createElement('style');
    style.id = 'karte-mermaid-katex-style';
    style.textContent = `
      .mermaid foreignObject { overflow: visible; }
      .mermaid .label,
      .mermaid .nodeLabel,
      .mermaid .labelText {
        display: flex;
        align-items: center;
        justify-content: center;
        line-height: 1.1;
        width: 100%;
        height: 100%;
      }
      .mermaid .katex {
        display: inline-flex;
        align-items: center;
        vertical-align: middle;
      }
      .mermaid .katex-display {
        margin: 0;
      }
    `;
    doc.head?.appendChild(style);
}

function ensureKaTeX(doc: Document, win: PreviewWindow): void {
    if (!doc.getElementById(PREVIEW_KATEX_CSS_ID) && !doc.querySelector(`link[href*="katex"]`)) {
        const link = doc.createElement('link');
        link.id = PREVIEW_KATEX_CSS_ID;
        link.rel = 'stylesheet';
        link.href = KATEX_CSS;
        doc.head?.appendChild(link);
    }

    if (win.katex) {
        renderKaTeX(doc, win);
        return;
    }

    if (!doc.getElementById(PREVIEW_KATEX_SCRIPT_ID)) {
        const script = doc.createElement('script');
        script.id = PREVIEW_KATEX_SCRIPT_ID;
        script.src = KATEX_JS;
        script.async = true;
        script.onload = () => renderKaTeX(doc, win);
        doc.head?.appendChild(script);
    } else {
        waitForKaTeX(win, () => renderKaTeX(doc, win), doc, true);
    }
}

function waitForKaTeX(win: PreviewWindow, callback: () => void, doc?: Document, retryLoad = false, attempts = 0): void {
    if (win.katex) {
        callback();
        return;
    }
    if (retryLoad && doc && attempts === 20 && !doc.getElementById(PREVIEW_KATEX_SCRIPT_ID)) {
        ensureKaTeX(doc, win);
        return;
    }
    setTimeout(() => waitForKaTeX(win, callback, doc, retryLoad, attempts + 1), 50);
}

function renderKaTeX(doc: Document, win: PreviewWindow): void {
    if (!win.katex) {
        return;
    }

    doc.querySelectorAll('.katex-inline').forEach((el) => {
        if (el.querySelector('.katex')) {
            return;
        }
        const math = (el.textContent || '').trim();
        if (!math) {
            return;
        }
        win.katex?.render(math, el, { throwOnError: false, displayMode: false });
    });

    doc.querySelectorAll('.katex-block').forEach((el) => {
        if (el.querySelector('.katex')) {
            return;
        }
        const math = (el.textContent || '').trim();
        if (!math) {
            return;
        }
        win.katex?.render(math, el, { throwOnError: false, displayMode: true });
    });

    const iframe = doc.defaultView?.frameElement;
    if (iframe instanceof HTMLIFrameElement) {
        syncPrintoutSourceSnapshot(doc);
        rerunPrintoutPagination(iframe);
        updatePreviewPrintoutStatus(iframe);
        attemptPreviewFocusRestore(iframe);
        schedulePreviewFocusRetry(iframe, 0);
    }
}

function ensureMermaid(doc: Document, win: PreviewWindow): void {
    if (win.mermaid) {
        renderMermaid(doc, win);
        return;
    }

    if (!doc.getElementById(PREVIEW_MERMAID_SCRIPT_ID)) {
        const script = doc.createElement('script');
        script.id = PREVIEW_MERMAID_SCRIPT_ID;
        script.src = MERMAID_CDN;
        script.async = true;
        script.onload = () => renderMermaid(doc, win);
        doc.head?.appendChild(script);
    } else {
        waitForMermaid(win, () => renderMermaid(doc, win), doc, true);
    }
}

function waitForMermaid(win: PreviewWindow, callback: () => void, doc?: Document, retryLoad = false, attempts = 0): void {
    if (win.mermaid) {
        callback();
        return;
    }
    if (retryLoad && doc && attempts === 20 && !doc.getElementById(PREVIEW_MERMAID_SCRIPT_ID)) {
        ensureMermaid(doc, win);
        return;
    }
    setTimeout(() => waitForMermaid(win, callback, doc, retryLoad, attempts + 1), 50);
}

function renderMermaid(doc: Document, win: PreviewWindow): void {
    if (!win.mermaid || win.__karteMermaidRendering) {
        return;
    }
    convertMermaidCodeBlocks(doc);
    const nodes = prepareMermaidNodes(doc);
    if (nodes.length === 0) {
        win.__karteMermaidReady = true;
        return;
    }
    win.__karteMermaidReady = false;
    win.__karteMermaidRendering = true;
    win.mermaid.initialize({
        startOnLoad: false,
        securityLevel: 'loose',
        htmlLabels: true,
        flowchart: { htmlLabels: true },
        sequence: { htmlLabels: true },
    });
    Promise.resolve(win.mermaid.run({ nodes: nodes as unknown as NodeListOf<Element> })).finally(() => {
        win.__karteMermaidRendering = false;
        win.__karteMermaidReady = prepareMermaidNodes(doc).length === 0;
        // KaTeX might appear inside Mermaid HTML labels.
        renderKaTeX(doc, win);
        requestAnimationFrame(() => {
            resizeMermaidLabels(doc);
            syncPrintoutSourceSnapshot(doc);
            const iframe = doc.defaultView?.frameElement;
            if (iframe instanceof HTMLIFrameElement) {
                rerunPrintoutPagination(iframe);
                updatePreviewPrintoutStatus(iframe);
                attemptPreviewFocusRestore(iframe);
                schedulePreviewFocusRetry(iframe, 0);
            }
        });
    });
}

function normalizePreviewText(value: string | null | undefined): string {
    return (value || '').replace(/\s+/g, ' ').trim();
}

function buildTextSignature(value: string | null | undefined, maxLength = 100): string {
    return normalizePreviewText(value).slice(0, maxLength);
}

function isFinitePrintout(doc: Document | null | undefined): boolean {
    const mode = (doc?.documentElement?.getAttribute('data-printout') || '').trim().toLowerCase();
    return Boolean(mode && mode !== 'infinite');
}

function hasPendingImageLoads(doc: Document): boolean {
    return Array.from(doc.images).some((img) => !img.complete);
}

function getPreviewScrollRatio(iframe: HTMLIFrameElement): number {
    const doc = iframe.contentDocument;
    if (!doc) {
        return 0;
    }
    const scrollRoot = doc.scrollingElement || doc.documentElement || doc.body;
    if (!scrollRoot) {
        return 0;
    }
    const maxScroll = Math.max(0, scrollRoot.scrollHeight - scrollRoot.clientHeight);
    if (maxScroll <= 0) {
        return 0;
    }
    return Math.max(0, Math.min(1, scrollRoot.scrollTop / maxScroll));
}

function restorePreviewScrollRatio(iframe: HTMLIFrameElement, ratio: number): boolean {
    const doc = iframe.contentDocument;
    if (!doc) {
        return false;
    }
    const scrollRoot = doc.scrollingElement || doc.documentElement || doc.body;
    if (!scrollRoot) {
        return false;
    }
    const maxScroll = Math.max(0, scrollRoot.scrollHeight - scrollRoot.clientHeight);
    scrollRoot.scrollTop = Math.round(maxScroll * Math.max(0, Math.min(1, ratio)));
    return true;
}

function getCandidateElements(doc: Document): HTMLElement[] {
    return Array.from(
        doc.querySelectorAll<HTMLElement>('h1, h2, h3, h4, h5, h6, p, li, blockquote, pre, table, img, figure')
    );
}

function getPrintPageCandidates(doc: Document): HTMLElement[] {
    return Array.from(doc.querySelectorAll<HTMLElement>('section.karte-print-page'));
}

function scoreTextCandidate(
    candidate: HTMLElement,
    textTarget: string,
    headingTarget: string,
    secondaryTargets: string[]
): number {
    let score = 0;
    const ownText = buildTextSignature(candidate.textContent, 120);
    const headingText = buildTextSignature(candidate.closest('section, article, main, div')?.querySelector('h1, h2, h3, h4, h5, h6')?.textContent, 100);

    if (textTarget && ownText.includes(textTarget)) {
        score += 6;
    }
    if (headingTarget && headingText.includes(headingTarget)) {
        score += 3;
    }
    for (const value of secondaryTargets) {
        if (value && ownText.includes(value)) {
            score += 2;
        }
    }
    if (/^H[1-6]$/.test(candidate.tagName) && textTarget && ownText === textTarget) {
        score += 2;
    }
    return score;
}

function findBestTextCandidate(
    doc: Document,
    textTarget: string,
    headingTarget: string,
    secondaryTargets: string[]
): HTMLElement | null {
    let best: HTMLElement | null = null;
    let bestScore = 0;
    for (const candidate of getCandidateElements(doc)) {
        const score = scoreTextCandidate(candidate, textTarget, headingTarget, secondaryTargets);
        if (score > bestScore) {
            best = candidate;
            bestScore = score;
        }
    }
    return bestScore > 0 ? best : null;
}

function scrollTargetIntoView(target: Element): boolean {
    if (!('scrollIntoView' in target) || typeof target.scrollIntoView !== 'function') {
        return false;
    }
    const page = target.closest?.('section.karte-print-page');
    if (page && 'scrollIntoView' in page && typeof page.scrollIntoView === 'function') {
        page.scrollIntoView({ block: 'center', inline: 'nearest' });
    }
    target.scrollIntoView({ block: 'center', inline: 'nearest' });
    return true;
}

function findBestPrintPage(doc: Document, pageTextSignature?: string, pageHeadingText?: string): HTMLElement | null {
    const textTarget = buildTextSignature(pageTextSignature, 120);
    const headingTarget = buildTextSignature(pageHeadingText, 100);
    if (!textTarget && !headingTarget) {
        return null;
    }

    let best: HTMLElement | null = null;
    let bestScore = 0;
    for (const page of getPrintPageCandidates(doc)) {
        const pageText = buildTextSignature(page.textContent, 180);
        const pageHeading = buildTextSignature(page.querySelector('h1, h2, h3, h4, h5, h6')?.textContent, 100);
        let score = 0;
        if (textTarget && pageText.includes(textTarget)) {
            score += 5;
        }
        if (headingTarget && pageHeading.includes(headingTarget)) {
            score += 3;
        }
        if (score > bestScore) {
            best = page;
            bestScore = score;
        }
    }
    return bestScore > 0 ? best : null;
}

export function restorePreviewFocus(iframe: HTMLIFrameElement, focusTarget: PreviewFocusTarget): boolean {
    const doc = iframe.contentDocument;
    if (!doc) {
        return false;
    }

    if (focusTarget.type === 'inserted-image') {
        const matchedPage = findBestPrintPage(doc, focusTarget.pageTextSignature, focusTarget.pageHeadingText);
        if (matchedPage) {
            scrollTargetIntoView(matchedPage);
        }
        const candidates = Array.from(doc.querySelectorAll<HTMLImageElement>('img'));
        const path = focusTarget.path;
        const alt = buildTextSignature(focusTarget.alt, 100);
        const title = buildTextSignature(focusTarget.title, 100);
        const match = candidates.find((img) => {
            const src = img.getAttribute('src') || '';
            const imgAlt = buildTextSignature(img.getAttribute('alt'), 100);
            const imgTitle = buildTextSignature(img.getAttribute('title'), 100);
            return src.includes(path) || (alt && imgAlt.includes(alt)) || (title && imgTitle.includes(title));
        });
        if (match) {
            return scrollTargetIntoView(match);
        }
        return restorePreviewScrollRatio(iframe, focusTarget.scrollRatio);
    }

    if (focusTarget.type === 'inserted-csv') {
        const matchedPage = findBestPrintPage(doc, focusTarget.pageTextSignature, focusTarget.pageHeadingText);
        if (matchedPage) {
            scrollTargetIntoView(matchedPage);
        }
        const tables = Array.from(doc.querySelectorAll<HTMLElement>('figure, table, figcaption, p, li'));
        const label = buildTextSignature(focusTarget.label, 100);
        const path = focusTarget.path;
        const match = tables.find((candidate) => {
            const text = buildTextSignature(candidate.textContent, 120);
            return text.includes(path) || (label && text.includes(label));
        });
        if (match) {
            return scrollTargetIntoView(match.tagName === 'FIGCAPTION' ? (match.closest('figure') || match) : match);
        }
        return restorePreviewScrollRatio(iframe, focusTarget.scrollRatio);
    }

    if (focusTarget.type === 'dropped-anchor') {
        const matchedPage = findBestPrintPage(doc, focusTarget.pageTextSignature, focusTarget.pageHeadingText);
        if (matchedPage) {
            scrollTargetIntoView(matchedPage);
        }
        const target = findBestTextCandidate(
            doc,
            buildTextSignature(focusTarget.textSignature, 100),
            buildTextSignature(focusTarget.headingText, 100),
            [buildTextSignature(focusTarget.tagName, 40)]
        );
        if (target) {
            return scrollTargetIntoView(target);
        }
        return restorePreviewScrollRatio(iframe, focusTarget.scrollRatio);
    }

    if (focusTarget.type === 'text-caret') {
        const target = findBestTextCandidate(
            doc,
            buildTextSignature(focusTarget.lineText, 100),
            buildTextSignature(focusTarget.headingText, 100),
            [
                buildTextSignature(focusTarget.previousLineText, 80),
                buildTextSignature(focusTarget.nextLineText, 80),
            ]
        );
        if (target) {
            return scrollTargetIntoView(target);
        }
        return restorePreviewScrollRatio(iframe, focusTarget.scrollRatio);
    }

    return restorePreviewScrollRatio(iframe, focusTarget.scrollRatio);
}

function attemptPreviewFocusRestore(iframe: HTMLIFrameElement): void {
    const focusTarget = pendingFocusTargets.get(iframe);
    if (!focusTarget) {
        return;
    }
    const restored = restorePreviewFocus(iframe, focusTarget);
    if (!restored) {
        return;
    }

    const doc = iframe.contentDocument;
    const shouldRetainForImageSettle =
        focusTarget.type === 'inserted-image'
        && Boolean(doc)
        && isFinitePrintout(doc)
        && hasPendingImageLoads(doc);

    if (!shouldRetainForImageSettle) {
        pendingFocusTargets.delete(iframe);
    }
}

function schedulePreviewFocusRetry(iframe: HTMLIFrameElement, attempt: number): void {
    if (!pendingFocusTargets.has(iframe) || attempt >= 20) {
        return;
    }
    const existing = focusRetryTimers.get(iframe);
    if (existing !== undefined) {
        window.clearTimeout(existing);
    }
    const timer = window.setTimeout(() => {
        focusRetryTimers.delete(iframe);
        if (!pendingFocusTargets.has(iframe)) {
            return;
        }
        const doc = iframe.contentDocument;
        if (!doc) {
            return;
        }
        if (isFinitePrintout(doc)) {
            rerunPrintoutPagination(iframe);
            updatePreviewPrintoutStatus(iframe);
        }
        attemptPreviewFocusRestore(iframe);
        if (pendingFocusTargets.has(iframe)) {
            schedulePreviewFocusRetry(iframe, attempt + 1);
        }
    }, Math.min(80 + attempt * 30, 300));
    focusRetryTimers.set(iframe, timer);
}

function attachPreviewAssetLoadHandlers(iframe: HTMLIFrameElement): void {
    const doc = iframe.contentDocument;
    if (!doc) {
        return;
    }

    const flaggedDoc = doc as Document & { __kartePreviewAssetLoadSetup?: boolean };
    if (flaggedDoc.__kartePreviewAssetLoadSetup) {
        return;
    }
    flaggedDoc.__kartePreviewAssetLoadSetup = true;

    const onAssetLoad = () => {
        rerunPrintoutPagination(iframe);
        updatePreviewPrintoutStatus(iframe);
        attemptPreviewFocusRestore(iframe);
        schedulePreviewFocusRetry(iframe, 0);
    };

    doc.querySelectorAll('img').forEach((img) => {
        if (img.complete) {
            return;
        }
        img.addEventListener('load', onAssetLoad, { once: true });
        img.addEventListener('error', onAssetLoad, { once: true });
    });
}

function convertMermaidCodeBlocks(doc: Document): void {
    const codeBlocks = doc.querySelectorAll('pre > code.language-mermaid, pre > code.lang-mermaid');
    codeBlocks.forEach((code) => {
        const pre = code.parentElement;
        if (!pre) {
            return;
        }
        const container = doc.createElement('div');
        container.className = 'mermaid';
        const source = code.textContent || '';
        container.dataset.mermaidSource = source;
        container.textContent = source;
        pre.replaceWith(container);
    });
}

function prepareMermaidNodes(doc: Document): HTMLElement[] {
    return Array.from(doc.querySelectorAll('.mermaid')).filter((node): node is HTMLElement => node instanceof HTMLElement).filter((node) => {
        const renderedSvg = node.querySelector('svg');
        if (renderedSvg) {
            return false;
        }

        const source = node.dataset.mermaidSource || node.textContent || '';
        if (source.trim() === '') {
            return false;
        }

        if (!node.dataset.mermaidSource) {
            node.dataset.mermaidSource = source;
        }

        if (node.getAttribute('data-processed') === 'true' || node.childElementCount > 0) {
            node.textContent = node.dataset.mermaidSource;
            node.removeAttribute('data-processed');
        }

        return true;
    });
}

function syncPrintoutSourceSnapshot(doc: Document): void {
    const flowRoot = doc.querySelector<HTMLElement>('.karte-print-flow-root, article, main.container, main, .container');
    if (!flowRoot) {
        return;
    }

    const pageContents = Array.from(doc.querySelectorAll<HTMLElement>('section.karte-print-page > .karte-print-page-content'));
    if (pageContents.length > 0) {
        const wrapper = doc.createElement('div');
        pageContents.forEach((content) => {
            Array.from(content.childNodes).forEach((node) => {
                wrapper.appendChild(node.cloneNode(true));
            });
        });
        flowRoot.dataset.kartePrintOriginalHtml = wrapper.innerHTML;
        return;
    }

    if (!flowRoot.querySelector('.karte-print-pages')) {
        flowRoot.dataset.kartePrintOriginalHtml = flowRoot.innerHTML;
    }
}

function resizeMermaidLabels(doc: Document): void {
    const foreignObjects = doc.querySelectorAll('svg foreignObject');
    foreignObjects.forEach((fo) => {
        const label = fo.querySelector('.label, .nodeLabel, .labelText') as HTMLElement | null;
        if (!label) {
            return;
        }
        // Measure after KaTeX has rendered.
        const contentWidth = Math.ceil(label.scrollWidth);
        const contentHeight = Math.ceil(label.scrollHeight);
        if (!contentWidth || !contentHeight) {
            return;
        }

        const currentWidth = parseFloat(fo.getAttribute('width') || '0');
        const currentHeight = parseFloat(fo.getAttribute('height') || '0');
        const nextWidth = Math.max(currentWidth, contentWidth + 4);
        const nextHeight = Math.max(currentHeight, contentHeight + 2);
        fo.setAttribute('width', `${nextWidth}`);
        fo.setAttribute('height', `${nextHeight}`);
    });
}

function rerunPrintoutPagination(iframe: HTMLIFrameElement): void {
    const doc = iframe.contentDocument;
    const win = iframe.contentWindow as PreviewWindow | null;
    if (!doc || !win) {
        return;
    }

    try {
        if (typeof win.__karteCreatePrintoutPagination === 'function') {
            const controller = win.__karteCreatePrintoutPagination(doc);
            controller?.buildPages();
            return;
        }
        if (typeof win.__karteRunPrintoutPagination === 'function') {
            win.__karteRunPrintoutPagination(doc);
        }
    } catch (error) {
        console.warn('[preview-frame] failed to rerun printout pagination', error);
    }
}

function updatePreviewPrintoutStatus(iframe: HTMLIFrameElement): { ready: boolean; hasError: boolean } {
    const badge = document.getElementById(PREVIEW_PRINTOUT_STATUS_ID);
    const doc = iframe.contentDocument;
    const win = iframe.contentWindow as PreviewWindow | null;
    if (!badge || !doc || !win) {
        return { ready: false, hasError: false };
    }

    const root = doc.documentElement;
    const mode = (root?.getAttribute('data-printout') || doc.querySelector<HTMLMetaElement>('meta[name="karte-printout"]')?.content || '').trim();
    const pages = (doc.querySelector<HTMLMetaElement>('meta[name="karte-printout-pages"]')?.content || '-').trim() || '-';
    const readyMeta = (doc.querySelector<HTMLMetaElement>('meta[name="karte-printout-ready"]')?.content || '').trim();
    const errorMeta = (doc.querySelector<HTMLMetaElement>('meta[name="karte-printout-error"]')?.content || '').trim();
    const debugMeta = (doc.querySelector<HTMLMetaElement>('meta[name="karte-printout-debug"]')?.content || '').trim();
    const readyState = win.__kartePrintoutReady === undefined ? readyMeta : String(win.__kartePrintoutReady);
    const errorText = win.__kartePrintoutError || errorMeta;
    const debugText = win.__kartePrintoutDebug || debugMeta;
    const hasError = readyState === 'error' || Boolean(errorText);
    const ready = readyState === 'true' || readyState === 'True' || readyState === 'TRUE';

    badge.classList.remove('printout-status-pending', 'printout-status-ready', 'printout-status-error');
    if (hasError) {
        badge.classList.add('printout-status-error');
        badge.textContent = `printout: ${mode || '-'} | pages: ${pages} | error: ${String(errorText)}`;
        logPrintoutDiagnostics(iframe, mode, pages, readyState, errorText, debugText);
    } else if (ready) {
        badge.classList.add('printout-status-ready');
        if (pages === '0') {
            badge.textContent = `printout: ${mode || '-'} | pages: ${pages} | ready(${debugText || 'no-pages'})`;
            logPrintoutDiagnostics(iframe, mode, pages, readyState, '', debugText);
            if (mode && mode.toLowerCase() !== 'infinite') {
                scheduleZeroPageRecovery(iframe, debugText || 'zero-pages');
            }
        } else {
            badge.textContent = `printout: ${mode || '-'} | pages: ${pages} | ready`;
            zeroPageRetryCount.delete(iframe);
        }
    } else {
        badge.classList.add('printout-status-pending');
        badge.textContent = `printout: ${mode || '-'} | pages: ${pages} | building`;
    }

    return { ready, hasError };
}

function logPrintoutDiagnostics(
    iframe: HTMLIFrameElement,
    mode: string,
    pages: string,
    readyState: string,
    errorText: string,
    debugText: string
): void {
    const signature = `${mode}|${pages}|${readyState}|${errorText}|${debugText}`;
    if (lastPrintoutSignatures.get(iframe) === signature) {
        return;
    }
    lastPrintoutSignatures.set(iframe, signature);

    const msg = `[PrintoutPreview] mode=${mode || '-'} pages=${pages} ready=${readyState || '-'} error=${errorText || '-'} debug=${debugText || '-'}`;
    eventLogger.log('PrintoutPreview', 'diagnostics', { mode, pages, readyState, errorText, debugText });
    try {
        const goApp = (window as Window & { go?: { main?: { App?: { LogJS?: (level: string, msg: string) => Promise<void> } } } }).go?.main?.App;
        goApp?.LogJS?.('INFO', msg).catch(() => {});
    } catch {
        // no-op
    }
}

function scheduleZeroPageRecovery(iframe: HTMLIFrameElement, reason: string): void {
    const tries = zeroPageRetryCount.get(iframe) || 0;
    if (tries >= 3) {
        return;
    }
    zeroPageRetryCount.set(iframe, tries + 1);

    const delayMs = 120 * (tries + 1);
    window.setTimeout(() => {
        eventLogger.log('PrintoutPreview', 'zero-page-recovery', { try: tries + 1, delayMs, reason });
        try {
            const goApp = (window as Window & { go?: { main?: { App?: { LogJS?: (level: string, msg: string) => Promise<void> } } } }).go?.main?.App;
            goApp?.LogJS?.('INFO', `[PrintoutPreview] zero-page-recovery try=${tries + 1} delayMs=${delayMs} reason=${reason}`).catch(() => {});
        } catch {
            // no-op
        }
        rerunPrintoutPagination(iframe);
        updatePreviewPrintoutStatus(iframe);
    }, delayMs);
}

function startPreviewPrintoutStatusPolling(iframe: HTMLIFrameElement): void {
    const prev = statusPollingTimers.get(iframe);
    if (prev !== undefined) {
        window.clearInterval(prev);
    }

    let ticks = 0;
    const timer = window.setInterval(() => {
        const { ready, hasError } = updatePreviewPrintoutStatus(iframe);
        ticks += 1;
        if (ready || hasError || ticks >= 100) {
            window.clearInterval(timer);
            statusPollingTimers.delete(iframe);
        }
    }, 80);

    statusPollingTimers.set(iframe, timer);
}
