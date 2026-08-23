import { existsSync, readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { JSDOM } from 'jsdom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
    clearPreviewFrame,
    disposePreviewFrame,
    writePreviewFrame,
} from '../preview-frame';

const frames: HTMLIFrameElement[] = [];

afterEach(() => {
    frames.forEach((frame) => disposePreviewFrame(frame));
    frames.length = 0;
    document.body.replaceChildren();
    vi.useRealTimers();
});

describe('preview frame enhancers', () => {
    it('renders Mermaid and KaTeX with demand-loaded assets and strips legacy CDN tags', async () => {
        const iframe = createFrame();
        const initialize = vi.fn();
        const run = vi.fn(async ({ nodes }: { nodes: NodeListOf<Element> }) => {
            nodes.forEach((node) => {
                node.setAttribute('data-processed', 'true');
                node.innerHTML = '<svg aria-label="diagram"></svg>';
            });
        });
        const render = vi.fn((
            math: string,
            element: Element,
            _options: { throwOnError: boolean; displayMode: boolean }
        ) => {
            const output = element.ownerDocument.createElement('span');
            output.className = 'katex';
            output.textContent = `rendered:${math}`;
            element.replaceChildren(output);
        });
        const loadMermaid = vi.fn(async () => ({ initialize, run }));
        const loadKaTeX = vi.fn(async () => ({ render }));
        const loadKaTeXStyles = vi.fn(async () => '.katex { font-family: KaTeX_Local; }');

        const handle = writePreviewFrame(
            iframe,
            `<!doctype html><html><head>
                <script src="https://cdn.jsdelivr.net/npm/mermaid/dist/mermaid.min.js"></script>
                <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/katex@latest/dist/katex.min.css">
                <script src="https://unpkg.com/katex/dist/katex.min.js"></script>
                <script>mermaid.initialize({startOnLoad:true});</script>
            </head><body>
                <pre><code class="language-mermaid">graph TD; A--&gt;B</code></pre>
                <span class="katex-inline">x^2</span>
                <div class="katex-block">y^2</div>
            </body></html>`,
            { loaders: { loadMermaid, loadKaTeX, loadKaTeXStyles } }
        );

        await handle.done;

        const doc = requiredDocument(iframe);
        expect(loadMermaid).toHaveBeenCalledOnce();
        expect(loadKaTeX).toHaveBeenCalledOnce();
        expect(loadKaTeXStyles).toHaveBeenCalledOnce();
        expect(initialize).toHaveBeenCalledWith(expect.objectContaining({ startOnLoad: false }));
        expect(run).toHaveBeenCalledOnce();
        expect(doc.querySelector('.mermaid svg')).not.toBeNull();
        expect(doc.querySelector('.katex-inline .katex')?.textContent).toBe('rendered:x^2');
        expect(doc.querySelector('.katex-block .katex')?.textContent).toBe('rendered:y^2');
        expect(render.mock.calls.map((call) => call[2]?.displayMode)).toEqual([false, true]);
        expect(doc.getElementById('karte-katex-local-style')?.textContent).toContain('KaTeX_Local');
        expect(doc.querySelector('[src^="http://"], [src^="https://"], [href^="http://"], [href^="https://"]')).toBeNull();
        expect(doc.querySelector('script:not([src])')).toBeNull();
    });

    it('does not load either renderer for a plain preview', async () => {
        const iframe = createFrame();
        const loadMermaid = vi.fn(async () => { throw new Error('unexpected Mermaid load'); });
        const loadKaTeX = vi.fn(async () => { throw new Error('unexpected KaTeX load'); });
        const loadKaTeXStyles = vi.fn(async () => { throw new Error('unexpected KaTeX style load'); });

        const handle = writePreviewFrame(
            iframe,
            '<p>plain preview</p>',
            { loaders: { loadMermaid, loadKaTeX, loadKaTeXStyles } }
        );
        await handle.done;

        expect(requiredDocument(iframe).body.textContent).toContain('plain preview');
        expect(requiredDocument(iframe).compatMode).toBe('CSS1Compat');
        expect(loadMermaid).not.toHaveBeenCalled();
        expect(loadKaTeX).not.toHaveBeenCalled();
        expect(loadKaTeXStyles).not.toHaveBeenCalled();
    });

    it('loads only KaTeX styles when Mermaid already emitted rendered math', async () => {
        const iframe = createFrame();
        const loadKaTeX = vi.fn(async () => { throw new Error('unexpected KaTeX renderer load'); });
        const loadKaTeXStyles = vi.fn(async () => '.katex { font-family: KaTeX_Local; }');
        const handle = writePreviewFrame(
            iframe,
            '<!doctype html><html><body><div class="mermaid">graph TD; math</div></body></html>',
            {
                loaders: {
                    loadMermaid: async () => ({
                        initialize: vi.fn(),
                        run: async ({ nodes }) => {
                            nodes[0]?.replaceChildren(nodes[0].ownerDocument.createElement('span'));
                            nodes[0]?.firstElementChild?.classList.add('katex');
                        },
                    }),
                    loadKaTeX,
                    loadKaTeXStyles,
                },
            }
        );

        await handle.done;

        expect(loadKaTeX).not.toHaveBeenCalled();
        expect(loadKaTeXStyles).toHaveBeenCalledOnce();
        expect(requiredDocument(iframe).getElementById('karte-katex-local-style')).not.toBeNull();
    });

    it('anchors emitted KaTeX font URLs to the app even when preview HTML changes its base URL', async () => {
        const iframe = createFrame();
        const fontPath = '/assets/KaTeX_Main-Regular-test.woff2';
        const handle = writePreviewFrame(
            iframe,
            '<!doctype html><html><head><base href="https://remote.invalid/"></head>' +
                '<body><span class="katex-inline">x</span></body></html>',
            {
                loaders: {
                    loadKaTeX: async () => ({
                        render: (_math, element) => {
                            const rendered = element.ownerDocument.createElement('span');
                            rendered.className = 'katex';
                            element.replaceChildren(rendered);
                        },
                    }),
                    loadKaTeXStyles: async () => `@font-face { src: url(${fontPath}); }`,
                },
            }
        );

        await handle.done;

        const styles = requiredDocument(iframe).getElementById('karte-katex-local-style')?.textContent;
        expect(styles).toContain(new URL(fontPath, window.location.href).href);
        expect(styles).not.toContain('remote.invalid/assets');
    });

    it('ships every KaTeX stylesheet font reference in the local package', () => {
        const stylesheetPath = resolve(process.cwd(), 'node_modules/katex/dist/katex.min.css');
        const styles = readFileSync(stylesheetPath, 'utf8');
        const fontUrls = Array.from(styles.matchAll(/url\(([^)]+)\)/g), (match) =>
            (match[1] ?? '').replace(/^['"]|['"]$/g, '')
        );

        expect(fontUrls.length).toBeGreaterThan(0);
        fontUrls.forEach((fontUrl) => {
            expect(fontUrl).not.toMatch(/^https?:/i);
            expect(existsSync(resolve(dirname(stylesheetPath), fontUrl))).toBe(true);
        });
    });

    it('uses UMD assets that expose Mermaid and render KaTeX in the iframe realm', () => {
        const runtime = new JSDOM('<!doctype html><html><head></head><body></body></html>', {
            pretendToBeVisual: true,
            runScripts: 'dangerously',
            url: 'http://localhost/',
        });
        try {
            const mermaidScript = runtime.window.document.createElement('script');
            mermaidScript.textContent = readFileSync(
                resolve(process.cwd(), 'node_modules/mermaid/dist/mermaid.min.js'),
                'utf8'
            );
            runtime.window.document.head.appendChild(mermaidScript);

            const katexScript = runtime.window.document.createElement('script');
            katexScript.textContent = readFileSync(
                resolve(process.cwd(), 'node_modules/katex/dist/katex.min.js'),
                'utf8'
            );
            runtime.window.document.head.appendChild(katexScript);

            const mermaid = (runtime.window as unknown as {
                mermaid?: { initialize?: unknown; run?: unknown };
            }).mermaid;
            const katex = (runtime.window as unknown as {
                katex?: { render?: (math: string, element: Element) => void };
            }).katex;
            expect(typeof mermaid?.initialize).toBe('function');
            expect(typeof mermaid?.run).toBe('function');
            const math = runtime.window.document.createElement('span');
            katex?.render?.('x^2', math);
            expect(math.querySelector('.katex')).not.toBeNull();
        } finally {
            runtime.window.close();
        }
    });

    it('cancels a stale asset load when the preview is replaced', async () => {
        const iframe = createFrame();
        const pending = createDeferred<{
            initialize: (config: Record<string, unknown>) => void;
            run: (options: { nodes: NodeListOf<Element> }) => Promise<void>;
        }>();
        const initialize = vi.fn();
        const run = vi.fn(async () => undefined);
        let staleSignal: AbortSignal | undefined;

        const stale = writePreviewFrame(
            iframe,
            '<!doctype html><html><body><div class="mermaid">graph TD; stale</div></body></html>',
            {
                loaders: {
                    loadMermaid: ({ signal }) => {
                        staleSignal = signal;
                        return pending.promise;
                    },
                },
            }
        );
        await waitForLoaderStart(() => staleSignal);

        const current = writePreviewFrame(
            iframe,
            '<!doctype html><html><body><p>current preview</p></body></html>'
        );

        expect(staleSignal?.aborted).toBe(true);
        await stale.done;
        pending.resolve({ initialize, run });
        await Promise.resolve();
        await current.done;

        expect(run).not.toHaveBeenCalled();
        expect(requiredDocument(iframe).body.textContent).toContain('current preview');
    });

    it('cancels an in-flight load when the preview frame is disposed', async () => {
        const iframe = createFrame();
        let loadSignal: AbortSignal | undefined;
        const handle = writePreviewFrame(
            iframe,
            '<!doctype html><html><body><div class="mermaid">graph TD; destroy</div></body></html>',
            {
                loaders: {
                    loadMermaid: ({ signal }) => {
                        loadSignal = signal;
                        return new Promise(() => undefined);
                    },
                },
            }
        );
        await waitForLoaderStart(() => loadSignal);

        disposePreviewFrame(iframe);

        expect(loadSignal?.aborted).toBe(true);
        await handle.done;
    });

    it('cancels an in-flight load and removes old document content when cleared', async () => {
        const iframe = createFrame();
        let loadSignal: AbortSignal | undefined;
        const handle = writePreviewFrame(
            iframe,
            '<!doctype html><html><body><div class="mermaid">graph TD; old</div></body></html>',
            {
                loaders: {
                    loadMermaid: ({ signal }) => {
                        loadSignal = signal;
                        return new Promise(() => undefined);
                    },
                },
            }
        );
        await waitForLoaderStart(() => loadSignal);

        clearPreviewFrame(iframe);

        expect(loadSignal?.aborted).toBe(true);
        expect(requiredDocument(iframe).body.textContent).toBe('');
        expect(requiredDocument(iframe).querySelector('.mermaid')).toBeNull();
        await handle.done;
    });

    it('removes the default local script request when disposal cancels it', async () => {
        const iframe = createFrame();
        const handle = writePreviewFrame(
            iframe,
            '<!doctype html><html><head><base href="https://remote.invalid/"></head>' +
                '<body><div class="mermaid">graph TD; cancel</div></body></html>'
        );

        await vi.waitFor(() => {
            expect(requiredDocument(iframe).getElementById('karte-mermaid-local-script')).not.toBeNull();
        });
        const script = requiredDocument(iframe).getElementById(
            'karte-mermaid-local-script'
        ) as HTMLScriptElement;
        expect(script.src).not.toContain('remote.invalid');

        disposePreviewFrame(iframe);

        expect(requiredDocument(iframe).getElementById('karte-mermaid-local-script')).toBeNull();
        await handle.done;
    });

    it('allows an in-flight Mermaid render to mutate only its detached old nodes', async () => {
        const iframe = createFrame();
        const renderFinished = createDeferred<void>();
        let staleNode: Element | undefined;
        const run = vi.fn(({ nodes }: { nodes: NodeListOf<Element> }) => {
            staleNode = nodes[0];
            return renderFinished.promise;
        });
        const stale = writePreviewFrame(
            iframe,
            '<!doctype html><html><body><div class="mermaid">graph TD; stale</div></body></html>',
            {
                loaders: {
                    loadMermaid: async () => ({ initialize: vi.fn(), run }),
                },
            }
        );
        await waitForCondition(() => run.mock.calls.length > 0);

        const current = writePreviewFrame(
            iframe,
            '<!doctype html><html><body><div id="current">current preview</div></body></html>'
        );
        expect(staleNode?.isConnected).toBe(false);

        staleNode?.replaceChildren(staleNode.ownerDocument.createElement('svg'));
        renderFinished.resolve();
        await stale.done;
        await current.done;

        const doc = requiredDocument(iframe);
        expect(doc.getElementById('current')?.textContent).toBe('current preview');
        expect(doc.querySelector('svg')).toBeNull();
    });

    it('finishes within the configured bound when an asset loader never settles', async () => {
        vi.useFakeTimers();
        const iframe = createFrame();
        let loadSignal: AbortSignal | undefined;
        const handle = writePreviewFrame(
            iframe,
            '<!doctype html><html><body><div class="mermaid">graph TD; timeout</div></body></html>',
            {
                loadTimeoutMs: 100,
                loaders: {
                    loadMermaid: ({ signal }) => {
                        loadSignal = signal;
                        return new Promise(() => undefined);
                    },
                },
            }
        );
        await waitForLoaderStart(() => loadSignal);

        await vi.advanceTimersByTimeAsync(100);
        await handle.done;

        expect(loadSignal?.aborted).toBe(true);
        expect(vi.getTimerCount()).toBe(0);
        expect(requiredDocument(iframe).querySelector('.mermaid')?.textContent).toContain('timeout');
    });
});

function createFrame(): HTMLIFrameElement {
    const iframe = document.createElement('iframe');
    document.body.appendChild(iframe);
    frames.push(iframe);
    return iframe;
}

function requiredDocument(iframe: HTMLIFrameElement): Document {
    const doc = iframe.contentDocument;
    if (!doc) {
        throw new Error('iframe document was not created');
    }
    return doc;
}

async function waitForLoaderStart(readSignal: () => AbortSignal | undefined): Promise<void> {
    await waitForCondition(() => Boolean(readSignal()));
    expect(readSignal()).toBeDefined();
}

async function waitForCondition(condition: () => boolean): Promise<void> {
    for (let attempt = 0; attempt < 10 && !condition(); attempt += 1) {
        await Promise.resolve();
    }
    expect(condition()).toBe(true);
}

function createDeferred<T>(): {
    promise: Promise<T>;
    resolve: (value: T) => void;
} {
    let resolve!: (value: T) => void;
    const promise = new Promise<T>((resolvePromise) => {
        resolve = resolvePromise;
    });
    return { promise, resolve };
}
