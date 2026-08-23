/// <reference types="vite/client" />

import { setupTimestampLinkHandlers } from './preview-audio';

type MermaidRenderer = {
    initialize: (config: Record<string, unknown>) => void;
    run: (options: { nodes: NodeListOf<Element> }) => Promise<void> | void;
};

type KaTeXRenderer = {
    render: (
        math: string,
        element: Element,
        options: { throwOnError: boolean; displayMode: boolean }
    ) => void;
};

type PreviewWindow = Window & {
    mermaid?: MermaidRenderer;
    katex?: KaTeXRenderer;
};

export type PreviewAssetLoadContext = {
    document: Document;
    window: PreviewWindow;
    signal: AbortSignal;
};

export type PreviewEnhancerLoaders = {
    loadMermaid: (context: PreviewAssetLoadContext) => Promise<MermaidRenderer>;
    loadKaTeX: (context: PreviewAssetLoadContext) => Promise<KaTeXRenderer>;
    loadKaTeXStyles: (context: PreviewAssetLoadContext) => Promise<string>;
};

export type PreviewFrameOptions = {
    loaders?: Partial<PreviewEnhancerLoaders>;
    loadTimeoutMs?: number;
};

export type PreviewFrameHandle = {
    done: Promise<void>;
    cancel: () => void;
};

const DEFAULT_LOAD_TIMEOUT_MS = 5000;
const BASE_STYLE_ID = 'karte-mermaid-katex-style';
const KATEX_STYLE_ID = 'karte-katex-local-style';
const MERMAID_SCRIPT_ID = 'karte-mermaid-local-script';
const KATEX_SCRIPT_ID = 'karte-katex-local-script';

const previewLifecycles = new WeakMap<HTMLIFrameElement, PreviewLifecycle>();

const defaultLoaders: PreviewEnhancerLoaders = {
    async loadMermaid(context): Promise<MermaidRenderer> {
        if (context.window.mermaid) {
            return context.window.mermaid;
        }
        const asset = await import('mermaid/dist/mermaid.min.js?url');
        throwIfAborted(context.signal);
        await loadLocalScript(context.document, MERMAID_SCRIPT_ID, asset.default, context.signal);
        if (!context.window.mermaid) {
            throw new Error('The local Mermaid asset loaded without exposing its renderer');
        }
        return context.window.mermaid;
    },

    async loadKaTeX(context): Promise<KaTeXRenderer> {
        if (context.window.katex) {
            return context.window.katex;
        }
        const asset = await import('katex/dist/katex.min.js?url');
        throwIfAborted(context.signal);
        await loadLocalScript(context.document, KATEX_SCRIPT_ID, asset.default, context.signal);
        if (!context.window.katex) {
            throw new Error('The local KaTeX asset loaded without exposing its renderer');
        }
        return context.window.katex;
    },

    async loadKaTeXStyles(context): Promise<string> {
        const asset = await import('katex/dist/katex.min.css?inline');
        throwIfAborted(context.signal);
        return asset.default;
    },
};

export function writePreviewFrame(
    iframe: HTMLIFrameElement,
    html: string,
    options: PreviewFrameOptions = {}
): PreviewFrameHandle {
    disposePreviewFrame(iframe);

    const doc = iframe.contentDocument;
    const win = iframe.contentWindow as PreviewWindow | null;
    if (!doc || !win) {
        return { done: Promise.resolve(), cancel: () => undefined };
    }

    const lifecycle = new PreviewLifecycle(win);
    previewLifecycles.set(iframe, lifecycle);

    const onLoad = (): void => {
        if (lifecycle.active) {
            setupTimestampLinkHandlers(iframe);
        }
    };
    iframe.addEventListener('load', onLoad, { once: true });
    lifecycle.addCleanup(() => iframe.removeEventListener('load', onLoad));

    doc.open();
    doc.write(stripLegacyRemotePreviewAssets(html));
    doc.close();
    ensureMermaidKaTeXStyles(doc);

    const loaders = { ...defaultLoaders, ...options.loaders };
    const timeoutMs = normalizeTimeout(options.loadTimeoutMs);
    const done = Promise.resolve()
        .then(() => enhancePreview(doc, win, lifecycle, loaders, timeoutMs))
        .catch(() => undefined);

    const cancel = (): void => {
        if (previewLifecycles.get(iframe) === lifecycle) {
            previewLifecycles.delete(iframe);
        }
        lifecycle.cancel();
    };
    return { done, cancel };
}

export function disposePreviewFrame(iframe: HTMLIFrameElement | null): void {
    if (!iframe) {
        return;
    }
    const lifecycle = previewLifecycles.get(iframe);
    if (!lifecycle) {
        return;
    }
    previewLifecycles.delete(iframe);
    lifecycle.cancel();
}

export function clearPreviewFrame(iframe: HTMLIFrameElement | null): void {
    if (!iframe) {
        return;
    }
    disposePreviewFrame(iframe);
    const doc = iframe.contentDocument;
    if (!doc) {
        iframe.removeAttribute('srcdoc');
        return;
    }
    doc.open();
    doc.write('<!doctype html><html><head></head><body></body></html>');
    doc.close();
}

async function enhancePreview(
    doc: Document,
    win: PreviewWindow,
    lifecycle: PreviewLifecycle,
    loaders: PreviewEnhancerLoaders,
    timeoutMs: number
): Promise<void> {
    if (!lifecycle.active) {
        return;
    }

    convertMermaidCodeBlocks(doc);
    const mermaidNodes = doc.querySelectorAll('.mermaid:not([data-processed])');
    const hasInitialMath = hasPendingKaTeX(doc);
    const katexLoad = hasInitialMath
        ? loadKaTeXAssets(doc, win, lifecycle, loaders, timeoutMs, true, true).then((loaded) => {
            if (loaded && lifecycle.active) {
                applyKaTeXAssets(doc, loaded);
            }
            return loaded;
        })
        : null;

    let renderedMermaid = false;
    if (mermaidNodes.length > 0) {
        const renderer = await runBounded(
            lifecycle,
            timeoutMs,
            (signal) => loaders.loadMermaid({ document: doc, window: win, signal })
        );
        if (renderer && lifecycle.active) {
            renderedMermaid = await renderMermaid(renderer, mermaidNodes, lifecycle, timeoutMs);
        }
    }

    const needsKaTeXRenderer = hasPendingKaTeX(doc);
    const needsKaTeXStyles = needsKaTeXRenderer || doc.querySelector('.katex') !== null;
    const katexAssets = katexLoad ?? (needsKaTeXStyles
        ? loadKaTeXAssets(
            doc,
            win,
            lifecycle,
            loaders,
            timeoutMs,
            needsKaTeXRenderer,
            needsKaTeXStyles
        )
        : null);
    if (katexAssets) {
        const loaded = await katexAssets;
        if (loaded && lifecycle.active) {
            applyKaTeXAssets(doc, loaded);
        }
    }

    if (renderedMermaid && lifecycle.active) {
        lifecycle.requestAnimationFrame(() => resizeMermaidLabels(doc));
    }
}

async function loadKaTeXAssets(
    doc: Document,
    win: PreviewWindow,
    lifecycle: PreviewLifecycle,
    loaders: PreviewEnhancerLoaders,
    timeoutMs: number,
    needsRenderer: boolean,
    needsStyles: boolean
): Promise<{ renderer: KaTeXRenderer | null; styles: string | null } | null> {
    const [renderer, styles] = await Promise.all([
        needsRenderer ? runBounded(
            lifecycle,
            timeoutMs,
            (signal) => loaders.loadKaTeX({ document: doc, window: win, signal })
        ) : Promise.resolve(null),
        needsStyles ? runBounded(
            lifecycle,
            timeoutMs,
            (signal) => loaders.loadKaTeXStyles({ document: doc, window: win, signal })
        ) : Promise.resolve(null),
    ]);
    if (!lifecycle.active) {
        return null;
    }
    return { renderer, styles };
}

function applyKaTeXAssets(
    doc: Document,
    loaded: { renderer: KaTeXRenderer | null; styles: string | null }
): void {
    if (loaded.styles !== null) {
        ensureKaTeXStyles(doc, loaded.styles);
    }
    if (loaded.renderer) {
        renderKaTeX(doc, loaded.renderer);
    }
}

async function renderMermaid(
    renderer: MermaidRenderer,
    nodes: NodeListOf<Element>,
    lifecycle: PreviewLifecycle,
    timeoutMs: number
): Promise<boolean> {
    try {
        renderer.initialize({
            startOnLoad: false,
            securityLevel: 'loose',
            htmlLabels: true,
            flowchart: { htmlLabels: true },
            sequence: { htmlLabels: true },
        });
    } catch {
        return false;
    }

    const completed = await runBounded(lifecycle, timeoutMs, async () => {
        await Promise.resolve(renderer.run({ nodes }));
        return true;
    });
    return completed === true && lifecycle.active;
}

function renderKaTeX(doc: Document, renderer: KaTeXRenderer): void {
    renderKaTeXNodes(doc, renderer, '.katex-inline', false);
    renderKaTeXNodes(doc, renderer, '.katex-block', true);
}

function renderKaTeXNodes(
    doc: Document,
    renderer: KaTeXRenderer,
    selector: string,
    displayMode: boolean
): void {
    doc.querySelectorAll(selector).forEach((element) => {
        if (element.querySelector('.katex')) {
            return;
        }
        const math = (element.textContent || '').trim();
        if (!math) {
            return;
        }
        try {
            renderer.render(math, element, { throwOnError: false, displayMode });
        } catch {
            // Keep the original expression visible when one expression cannot be rendered.
        }
    });
}

function hasPendingKaTeX(doc: Document): boolean {
    return Array.from(doc.querySelectorAll('.katex-inline, .katex-block'))
        .some((element) => !element.querySelector('.katex'));
}

function ensureMermaidKaTeXStyles(doc: Document): void {
    if (doc.getElementById(BASE_STYLE_ID)) {
        return;
    }
    const style = doc.createElement('style');
    style.id = BASE_STYLE_ID;
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

function ensureKaTeXStyles(doc: Document, styles: string): void {
    if (!styles || doc.getElementById(KATEX_STYLE_ID)) {
        return;
    }
    const style = doc.createElement('style');
    style.id = KATEX_STYLE_ID;
    style.textContent = resolveKaTeXFontURLs(styles);
    doc.head?.appendChild(style);
}

function resolveKaTeXFontURLs(styles: string): string {
    return styles.replace(/url\(([^)]+)\)/g, (match, value: string) => {
        const assetURL = value.trim().replace(/^['"]|['"]$/g, '');
        if (!assetURL.startsWith('/assets/')) {
            return match;
        }
        return `url("${resolveLocalAssetURL(assetURL)}")`;
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
    foreignObjects.forEach((foreignObject) => {
        const label = foreignObject.querySelector('.label, .nodeLabel, .labelText') as HTMLElement | null;
        if (!label) {
            return;
        }
        const contentWidth = Math.ceil(label.scrollWidth);
        const contentHeight = Math.ceil(label.scrollHeight);
        if (!contentWidth || !contentHeight) {
            return;
        }

        const currentWidth = parseFloat(foreignObject.getAttribute('width') || '0');
        const currentHeight = parseFloat(foreignObject.getAttribute('height') || '0');
        foreignObject.setAttribute('width', `${Math.max(currentWidth, contentWidth + 4)}`);
        foreignObject.setAttribute('height', `${Math.max(currentHeight, contentHeight + 2)}`);
    });
}

async function runBounded<T>(
    lifecycle: PreviewLifecycle,
    timeoutMs: number,
    operation: (signal: AbortSignal) => Promise<T>
): Promise<T | null> {
    if (!lifecycle.active) {
        return null;
    }

    const operationController = new AbortController();
    const abortOperation = (): void => operationController.abort();
    lifecycle.signal.addEventListener('abort', abortOperation, { once: true });
    const timeout = lifecycle.setTimeout(abortOperation, timeoutMs);

    const task = Promise.resolve()
        .then(() => operation(operationController.signal))
        .then((value) => value, () => null);
    const aborted = new Promise<null>((resolve) => {
        if (operationController.signal.aborted) {
            resolve(null);
            return;
        }
        operationController.signal.addEventListener('abort', () => resolve(null), { once: true });
    });

    try {
        return await Promise.race([task, aborted]);
    } finally {
        lifecycle.clearTimeout(timeout);
        lifecycle.signal.removeEventListener('abort', abortOperation);
    }
}

function loadLocalScript(
    doc: Document,
    id: string,
    source: string,
    signal: AbortSignal
): Promise<void> {
    return new Promise((resolve, reject) => {
        if (signal.aborted) {
            reject(createAbortError());
            return;
        }

        const script = doc.createElement('script');
        script.id = id;
        script.src = resolveLocalAssetURL(source);
        script.async = true;

        const cleanup = (): void => {
            script.onload = null;
            script.onerror = null;
            signal.removeEventListener('abort', onAbort);
        };
        const onAbort = (): void => {
            cleanup();
            script.removeAttribute('src');
            script.remove();
            reject(createAbortError());
        };
        script.onload = (): void => {
            cleanup();
            resolve();
        };
        script.onerror = (): void => {
            cleanup();
            script.remove();
            reject(new Error(`Failed to load local preview asset: ${source}`));
        };
        signal.addEventListener('abort', onAbort, { once: true });
        (doc.head ?? doc.documentElement).appendChild(script);
    });
}

function resolveLocalAssetURL(source: string): string {
    try {
        return new URL(source, window.location.href).href;
    } catch {
        return source;
    }
}

function stripLegacyRemotePreviewAssets(html: string): string {
    try {
        const parsed = new DOMParser().parseFromString(html, 'text/html');
        parsed.querySelectorAll('script[src], link[href]').forEach((element) => {
            const source = element.getAttribute('src') ?? element.getAttribute('href');
            if (source && isLegacyRemotePreviewAsset(source)) {
                element.remove();
            }
        });
        parsed.querySelectorAll('script:not([src])').forEach((script) => {
            if (/^\s*mermaid\.initialize\(\{\s*startOnLoad\s*:\s*true\s*}\)\s*;?\s*$/s.test(script.textContent ?? '')) {
                script.remove();
            }
        });
        return `<!doctype html>${parsed.documentElement.outerHTML}`;
    } catch {
        return html;
    }
}

function isLegacyRemotePreviewAsset(source: string): boolean {
    try {
        const url = new URL(source, window.location.href);
        if (url.protocol !== 'https:' && url.protocol !== 'http:') {
            return false;
        }
        return /(?:^|[/@._-])(?:mermaid|katex)(?:$|[/@._-])/i.test(url.pathname);
    } catch {
        return false;
    }
}

function normalizeTimeout(timeoutMs: number | undefined): number {
    if (timeoutMs === undefined || !Number.isFinite(timeoutMs)) {
        return DEFAULT_LOAD_TIMEOUT_MS;
    }
    return Math.max(0, timeoutMs);
}

function throwIfAborted(signal: AbortSignal): void {
    if (signal.aborted) {
        throw createAbortError();
    }
}

function createAbortError(): DOMException {
    return new DOMException('Preview enhancement was cancelled', 'AbortError');
}

class PreviewLifecycle {
    private readonly controller = new AbortController();
    private readonly timers = new Set<ReturnType<typeof setTimeout>>();
    private readonly cleanups = new Set<() => void>();
    private readonly animationFrames = new Set<number>();

    constructor(private readonly win: PreviewWindow) {}

    get active(): boolean {
        return !this.controller.signal.aborted;
    }

    get signal(): AbortSignal {
        return this.controller.signal;
    }

    addCleanup(cleanup: () => void): void {
        this.cleanups.add(cleanup);
    }

    setTimeout(callback: () => void, timeoutMs: number): ReturnType<typeof setTimeout> {
        const timer = setTimeout(() => {
            this.timers.delete(timer);
            callback();
        }, timeoutMs);
        this.timers.add(timer);
        return timer;
    }

    clearTimeout(timer: ReturnType<typeof setTimeout>): void {
        clearTimeout(timer);
        this.timers.delete(timer);
    }

    requestAnimationFrame(callback: () => void): void {
        if (!this.active) {
            return;
        }
        if (typeof this.win.requestAnimationFrame !== 'function') {
            callback();
            return;
        }
        const animationFrame = this.win.requestAnimationFrame(() => {
            this.animationFrames.delete(animationFrame);
            if (this.active) {
                callback();
            }
        });
        this.animationFrames.add(animationFrame);
    }

    cancel(): void {
        if (!this.active) {
            return;
        }
        this.controller.abort();
        this.timers.forEach((timer) => clearTimeout(timer));
        this.timers.clear();
        this.animationFrames.forEach((animationFrame) => this.win.cancelAnimationFrame(animationFrame));
        this.animationFrames.clear();
        this.cleanups.forEach((cleanup) => cleanup());
        this.cleanups.clear();
    }
}
