type PreviewWindow = Window & {
    mermaid?: {
        initialize: (config: Record<string, unknown>) => void;
        run: (options: { nodes: NodeListOf<Element> }) => Promise<void> | void;
    };
    katex?: {
        render: (math: string, element: Element, options: { throwOnError: boolean; displayMode: boolean }) => void;
    };
    __karteMermaidReady?: boolean;
    __karteKaTeXReady?: boolean;
};

const MERMAID_CDN = 'https://cdn.jsdelivr.net/npm/mermaid/dist/mermaid.min.js';
const KATEX_CSS = 'https://cdn.jsdelivr.net/npm/katex@latest/dist/katex.min.css';
const KATEX_JS = 'https://cdn.jsdelivr.net/npm/katex@latest/dist/katex.min.js';

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
