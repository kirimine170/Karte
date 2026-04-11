import { setupTimestampLinkHandlers } from './preview-audio';
import { eventLogger } from './event-logger';

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

export function writePreviewFrame(iframe: HTMLIFrameElement, html: string): void {
    const doc = iframe.contentDocument;
    if (!doc) {
        return;
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
    rerunPrintoutPagination(iframe);
    startPreviewPrintoutStatusPolling(iframe);
    iframe.addEventListener('load', () => {
        setupTimestampLinkHandlers(iframe);
        rerunPrintoutPagination(iframe);
        updatePreviewPrintoutStatus(iframe);
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
            }
        });
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
