import { setupTimestampLinkHandlers } from './preview-audio';
import { eventLogger } from './event-logger';

type PreviewWindow = Window & {
    __karteCreatePrintoutPagination?: (doc?: Document) => {
        buildPages(): void;
    };
    __karteRunPrintoutPagination?: (doc?: Document) => unknown;
    mermaid?: {
        initialize: (config: Record<string, unknown>) => void;
        run: (options: { nodes: NodeListOf<Element> }) => Promise<void> | void;
    };
    katex?: {
        render: (math: string, element: Element, options: { throwOnError: boolean; displayMode: boolean }) => void;
    };
    __karteMermaidReady?: boolean;
    __karteKaTeXReady?: boolean;
    __kartePrintoutDebug?: string;
};

const MERMAID_CDN = 'https://cdn.jsdelivr.net/npm/mermaid/dist/mermaid.min.js';
const KATEX_CSS = 'https://cdn.jsdelivr.net/npm/katex@latest/dist/katex.min.css';
const KATEX_JS = 'https://cdn.jsdelivr.net/npm/katex@latest/dist/katex.min.js';
const PREVIEW_PRINTOUT_STATUS_ID = 'previewPrintoutStatus';
const statusPollingTimers = new WeakMap<HTMLIFrameElement, number>();
const lastPrintoutSignatures = new WeakMap<HTMLIFrameElement, string>();
const zeroPageRetryCount = new WeakMap<HTMLIFrameElement, number>();

export function writePreviewFrame(iframe: HTMLIFrameElement, html: string): void {
    const doc = iframe.contentDocument;
    if (!doc) {
        return;
    }
    doc.open();
    doc.write(html);
    doc.close();
    ensureMermaidKaTeXStyles(doc);
    const win = iframe.contentWindow as PreviewWindow | null;
    if (win) {
        win.__karteMermaidReady = false;
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
    // Let synchronous scripts attach first.
    setTimeout(() => {
        ensureKaTeX(doc, win);
        ensureMermaid(doc, win);
        rerunPrintoutPagination(iframe);
        updatePreviewPrintoutStatus(iframe);
    }, 0);
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
    if (!doc.querySelector(`link[href*="katex"]`)) {
        const link = doc.createElement('link');
        link.rel = 'stylesheet';
        link.href = KATEX_CSS;
        doc.head?.appendChild(link);
    }

    if (win.katex) {
        renderKaTeX(doc, win);
        return;
    }

    if (!doc.querySelector(`script[src*="katex"]`)) {
        const script = doc.createElement('script');
        script.src = KATEX_JS;
        script.async = true;
        script.onload = () => renderKaTeX(doc, win);
        doc.head?.appendChild(script);
    } else {
        waitForKaTeX(win, () => renderKaTeX(doc, win));
    }
}

function waitForKaTeX(win: PreviewWindow, callback: () => void): void {
    if (win.katex) {
        callback();
        return;
    }
    setTimeout(() => waitForKaTeX(win, callback), 50);
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
        rerunPrintoutPagination(iframe);
        updatePreviewPrintoutStatus(iframe);
    }
}

function ensureMermaid(doc: Document, win: PreviewWindow): void {
    if (win.mermaid) {
        renderMermaid(doc, win);
        return;
    }

    if (!doc.querySelector(`script[src*="mermaid"]`)) {
        const script = doc.createElement('script');
        script.src = MERMAID_CDN;
        script.async = true;
        script.onload = () => renderMermaid(doc, win);
        doc.head?.appendChild(script);
    } else {
        waitForMermaid(win, () => renderMermaid(doc, win));
    }
}

function waitForMermaid(win: PreviewWindow, callback: () => void): void {
    if (win.mermaid) {
        callback();
        return;
    }
    setTimeout(() => waitForMermaid(win, callback), 50);
}

function renderMermaid(doc: Document, win: PreviewWindow): void {
    if (!win.mermaid || win.__karteMermaidReady) {
        return;
    }
    win.__karteMermaidReady = true;

    convertMermaidCodeBlocks(doc);
    const nodes = doc.querySelectorAll('.mermaid:not([data-processed])');
    if (nodes.length === 0) {
        return;
    }
    win.mermaid.initialize({
        startOnLoad: false,
        securityLevel: 'loose',
        htmlLabels: true,
        flowchart: { htmlLabels: true },
        sequence: { htmlLabels: true },
    });
    Promise.resolve(win.mermaid.run({ nodes })).finally(() => {
        // KaTeX might appear inside Mermaid HTML labels.
        renderKaTeX(doc, win);
        requestAnimationFrame(() => {
            resizeMermaidLabels(doc);
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
        container.textContent = code.textContent || '';
        pre.replaceWith(container);
    });
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
