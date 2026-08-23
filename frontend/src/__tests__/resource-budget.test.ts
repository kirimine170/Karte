// @ts-ignore -- Node types are intentionally outside the browser tsconfig；Vitest provides this module．
import { readFileSync, writeFileSync } from 'node:fs';
// @ts-ignore -- Node types are intentionally outside the browser tsconfig；Vitest provides this module．
import { resolve } from 'node:path';
import { afterAll, beforeAll, expect, it, vi } from 'vitest';
import type {
    PDFDocumentLoadingTask,
    PDFDocumentProxy,
    RenderTask,
} from 'pdfjs-dist/legacy/build/pdf.mjs';
import GraphD3Module from '../graph-d3';
import { EditorLayout } from '../components/editor-layout';
import { useASRStore, useDocStore, useUIStore } from '../stores/index';
import { eventLogger } from '../utils/event-logger';

declare const process: {
    cwd: () => string;
    env: Record<string, string | undefined>;
    version: string;
};

const d3Mocks = vi.hoisted(() => ({
    forceSimulation: vi.fn(),
}));

vi.mock('d3', async (importOriginal) => {
    const actual = await importOriginal<typeof import('d3')>();
    d3Mocks.forceSimulation.mockImplementation((nodes: unknown[]) => {
        const handlers = new Map<string, (() => void) | null>();
        const forces = new Map<string, unknown>();
        let alphaValue = 1;
        const simulation: any = {
            stop: vi.fn(() => simulation),
            restart: vi.fn(() => simulation),
            nodes: vi.fn(() => nodes),
            force: vi.fn(function (name: string, force?: unknown) {
                if (arguments.length === 1) return forces.get(name);
                forces.set(name, force);
                return simulation;
            }),
            on: vi.fn((name: string, handler: (() => void) | null) => {
                if (handler === null) handlers.delete(name);
                else handlers.set(name, handler);
                return simulation;
            }),
            alpha: vi.fn((value?: number) => {
                if (value === undefined) return alphaValue;
                alphaValue = value;
                return simulation;
            }),
            alphaTarget: vi.fn(() => simulation),
        };
        return simulation;
    });
    return {
        ...actual,
        forceSimulation: d3Mocks.forceSimulation,
    };
});

interface SamplingPolicy {
    warmup: number;
    samples: number;
    aggregation: 'median';
    gomaxprocs: number;
    latencyClock: 'monotonic';
}

interface BaselineMetric {
    id: string;
    source: 'backend' | 'frontend';
    unit: string;
    statistic: 'median';
    comparison: 'lte' | 'gte' | 'eq' | 'observe';
    limit: number;
    gate: boolean;
    required: boolean;
}

interface ResourceBaseline {
    schemaVersion: number;
    suite: string;
    policy: SamplingPolicy;
    metrics: BaselineMetric[];
}

interface ResourceMeasurement {
    id: string;
    unit: string;
    statistic: 'median';
    value: number;
    samples: number[];
}

const baselinePath = resolve(process.cwd(), '../resource-budget/baseline.json');
const baseline = JSON.parse(readFileSync(baselinePath, 'utf8')) as ResourceBaseline;
const realNow = globalThis.performance.now.bind(globalThis.performance);

beforeAll(() => {
    expect(baseline.schemaVersion).toBe(1);
    expect(baseline.policy).toMatchObject({
        warmup: 2,
        samples: 5,
        aggregation: 'median',
        latencyClock: 'monotonic',
    });
});

afterAll(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    eventLogger.clearLogs();
    document.body.replaceChildren();
});

it('enforces the deterministic frontend resource budget', async () => {
    const measurements = [
        ...await collectScenario(baseline.policy, measureContinuousInput),
        ...await collectScenario(baseline.policy, measurePdf100),
        ...await collectScenario(baseline.policy, measureGraph1000),
    ].sort((left, right) => left.id.localeCompare(right.id));

    const report = {
        schemaVersion: baseline.schemaVersion,
        suite: baseline.suite,
        source: 'frontend',
        policy: baseline.policy,
        environment: {
            node: process.version,
            runner: 'vitest-jsdom',
        },
        measurements,
    };

    // Preserve raw samples before applying limits so failed gates remain diagnosable．
    const reportPath = process.env.KARTE_RESOURCE_REPORT;
    if (reportPath) {
        writeFileSync(reportPath, `${JSON.stringify(report, null, 2)}\n`, { mode: 0o600 });
    }

    assertFrontendGate(baseline, measurements);
}, 120_000);

async function collectScenario(
    policy: SamplingPolicy,
    measure: () => Promise<Record<string, number>>,
): Promise<ResourceMeasurement[]> {
    const samplesByMetric = new Map<string, number[]>();
    const unitByMetric = new Map<string, string>();
    for (let iteration = 0; iteration < policy.warmup + policy.samples; iteration += 1) {
        const values = await measure();
        expect(Object.keys(values).length).toBeGreaterThan(0);
        if (iteration < policy.warmup) continue;
        for (const [encodedID, value] of Object.entries(values)) {
            const separator = encodedID.lastIndexOf('#');
            expect(separator).toBeGreaterThan(0);
            const id = encodedID.slice(0, separator);
            const unit = encodedID.slice(separator + 1);
            expect(unit).not.toBe('');
            expect(Number.isFinite(value)).toBe(true);
            expect(unitByMetric.get(id) ?? unit).toBe(unit);
            unitByMetric.set(id, unit);
            const samples = samplesByMetric.get(id) ?? [];
            samples.push(value);
            samplesByMetric.set(id, samples);
        }
    }

    return [...samplesByMetric.entries()].map(([id, samples]) => {
        expect(samples).toHaveLength(policy.samples);
        return {
            id,
            unit: unitByMetric.get(id)!,
            statistic: 'median',
            value: median(samples),
            samples,
        };
    });
}

async function measureContinuousInput(): Promise<Record<string, number>> {
    vi.useFakeTimers();
    eventLogger.clearLogs();
    resetEditorStores('content/resource-budget.md');
    document.body.innerHTML = createEditorDOM();
    const previewMarkdown = vi.fn().mockResolvedValue('<p>latest</p>');
    const editorLayout = new EditorLayout({
        PreviewMarkdown: previewMarkdown,
        SaveFile: vi.fn().mockResolvedValue(undefined),
    } as any);
    editorLayout.init();
    const editor = document.getElementById('editor') as HTMLTextAreaElement;
    const startedAt = realNow();
    for (let index = 0; index < 10_000; index += 1) {
        editor.value = `resource-${index}`;
        editor.dispatchEvent(new Event('input', { bubbles: true }));
    }
    const pendingTimers = vi.getTimerCount();
    await vi.advanceTimersByTimeAsync(200);
    await Promise.resolve();
    await Promise.resolve();
    const latency = realNow() - startedAt;
    const domNodes = document.body.querySelectorAll('*').length;
    const previewCalls = previewMarkdown.mock.calls.length;
    expect(previewMarkdown).toHaveBeenCalledWith('resource-9999');
    editorLayout.destroy();
    eventLogger.clearLogs();
    vi.clearAllTimers();
    vi.useRealTimers();

    return {
        'frontend.continuous_input.events#operations': 10_000,
        'frontend.continuous_input.pending_timers#count': pendingTimers,
        'frontend.continuous_input.preview_calls#count': previewCalls,
        'frontend.continuous_input.dom_nodes#count': domNodes,
        'frontend.continuous_input.latency_ms#milliseconds': latency,
    };
}

async function measurePdf100(): Promise<Record<string, number>> {
    resetEditorStores('content/resource-budget.md');
    eventLogger.clearLogs();
    vi.stubGlobal('IntersectionObserver', undefined);
    document.body.innerHTML = createEditorDOM();
    const canvasContainer = document.getElementById('pdfCanvasContainer')!;
    Object.defineProperty(canvasContainer, 'clientWidth', { configurable: true, value: 800 });
    Object.defineProperty(canvasContainer, 'clientHeight', { configurable: true, value: 600 });
    const canvasContext = vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockImplementation(
        () => ({ clearRect: vi.fn() }) as any,
    );
    const pdfDocument = createMockPdfDocument(100);
    const editorLayout = new EditorLayout({
        GetPdfFileURL: vi.fn(async (path: string) => `/pdf/${path}`),
        PreviewMarkdown: vi.fn().mockResolvedValue('<p>preview</p>'),
        SaveFile: vi.fn().mockResolvedValue(undefined),
    } as any);
    installPdfLoader(editorLayout, pdfDocument.proxy);

    const startedAt = realNow();
    editorLayout.init();
    useDocStore.getState().setCurrentPath('content/resource-budget.pdf');
    await vi.waitFor(() => expect(pdfDocument.getPage).toHaveBeenCalled());
    document.querySelector<HTMLButtonElement>('#pdfViewModeMenu button[data-value="scroll"]')?.click();
    await vi.waitFor(() => {
        expect(document.querySelectorAll('.pdf-scroll-page')).toHaveLength(100);
        expect(document.querySelectorAll('.pdf-scroll-canvas').length).toBeGreaterThan(0);
    });
    let activeCanvases = document.querySelectorAll('.pdf-scroll-canvas').length;
    canvasContainer.scrollTop = 50 * 1113;
    canvasContainer.dispatchEvent(new Event('scroll'));
    await vi.waitFor(() => {
        expect(document.querySelector('.pdf-scroll-page[data-page-number="50"] canvas')).not.toBeNull();
    });
    activeCanvases = Math.max(activeCanvases, document.querySelectorAll('.pdf-scroll-canvas').length);
    const latency = realNow() - startedAt;
    const values = {
        'frontend.pdf_100.page_slots#count': document.querySelectorAll('.pdf-scroll-page').length,
        'frontend.pdf_100.active_canvases#count': activeCanvases,
        'frontend.pdf_100.page_requests#operations': pdfDocument.getPage.mock.calls.length,
        'frontend.pdf_100.dom_nodes#count': document.body.querySelectorAll('*').length,
        'frontend.pdf_100.latency_ms#milliseconds': latency,
    };
    editorLayout.destroy();
    canvasContext.mockRestore();
    vi.unstubAllGlobals();
    eventLogger.clearLogs();
    return values;
}

async function measureGraph1000(): Promise<Record<string, number>> {
    class ResourceResizeObserver {
        observe(): void {}
        disconnect(): void {}
    }
    vi.stubGlobal('ResizeObserver', ResourceResizeObserver);
    const log = vi.spyOn(console, 'log').mockImplementation(() => undefined);
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => undefined);
    document.body.innerHTML = '<div id="resource-graph"></div>';
    const container = document.getElementById('resource-graph')!;
    Object.defineProperty(container, 'clientWidth', { configurable: true, value: 800 });
    Object.defineProperty(container, 'clientHeight', { configurable: true, value: 600 });
    container.getBoundingClientRect = () => ({
        x: 0,
        y: 0,
        top: 0,
        left: 0,
        right: 800,
        bottom: 600,
        width: 800,
        height: 600,
        toJSON: () => ({}),
    });
    const graphData = createGraphData(1_000);
    const startedAt = realNow();
    const graph = new GraphD3Module('resource-graph');
    graph.setData(graphData);
    const latency = realNow() - startedAt;
    const values = {
        'frontend.graph_1000.nodes#count': document.querySelectorAll('#resource-graph circle').length,
        'frontend.graph_1000.edges#count': document.querySelectorAll('#resource-graph line').length,
        'frontend.graph_1000.dom_nodes#count': document.body.querySelectorAll('*').length,
        'frontend.graph_1000.canvas_elements#count': document.querySelectorAll('canvas').length,
        'frontend.graph_1000.latency_ms#milliseconds': latency,
    };
    graph.destroy();
    log.mockRestore();
    warn.mockRestore();
    vi.unstubAllGlobals();
    return values;
}

function installPdfLoader(editorLayout: EditorLayout, document: PDFDocumentProxy): void {
    const loadingTask = {
        promise: Promise.resolve(document),
        destroy: vi.fn().mockResolvedValue(undefined),
    } as unknown as PDFDocumentLoadingTask;
    (editorLayout as unknown as { createPdfLoadingTask: () => PDFDocumentLoadingTask }).createPdfLoadingTask = vi.fn(() => loadingTask);
}

function createMockPdfDocument(numPages: number) {
    const cleanup = vi.fn().mockResolvedValue(undefined);
    const destroy = vi.fn().mockResolvedValue(undefined);
    const getPage = vi.fn(async () => ({
        getViewport: ({ scale }: { scale: number }) => ({ width: 600 * scale, height: 800 * scale }),
        render: () => ({
            promise: Promise.resolve(),
            cancel: vi.fn(),
        } as unknown as RenderTask),
    }));
    return {
        getPage,
        proxy: { numPages, cleanup, destroy, getPage } as unknown as PDFDocumentProxy,
    };
}

function createGraphData(size: number): { nodes: any[]; edges: any[] } {
    const nodes = Array.from({ length: size }, (_, index) => ({
        id: `doc:/resource/${index}.md`,
        kind: 'note',
        label: `Resource ${index}`,
        exists: true,
        degIn: index === 0 ? 0 : 1,
        degOut: index === size - 1 ? 0 : 1,
        x: index % 100,
        y: Math.floor(index / 100),
    }));
    const edges = Array.from({ length: size - 1 }, (_, index) => ({
        id: `resource-edge-${index}`,
        kind: 'wikilink',
        source: nodes[index]!.id,
        target: nodes[index + 1]!.id,
        weight: 1,
    }));
    return { nodes, edges };
}

function resetEditorStores(path: string): void {
    useUIStore.setState({
        sidebarVisible: true,
        imageGalleryVisible: true,
        csvGalleryVisible: true,
        workspaceMode: false,
        activeTab: 'editor',
        theme: 'light',
        hardWrap: false,
        statusMessage: '',
        statusClearTimer: null,
    });
    useDocStore.setState({
        files: [],
        currentPath: path,
        markdownContent: '# Resource fixture',
        previewHtml: '',
        hasUnsavedChanges: false,
        searchQuery: '',
    });
    useASRStore.setState({
        isRecording: false,
        micLevel: 0,
        status: { initialized: true, initializing: false },
        realtimeTranscript: { partial: '', final: [] },
    });
}

function createEditorDOM(): string {
    return `
        <div id="contentArea">
            <div class="tabs"></div>
            <textarea id="editor"></textarea>
            <div class="preview-pane-body"><iframe id="preview"></iframe></div>
            <div id="pdfPane" style="display: none">
                <button id="pdfPrevBtn"></button>
                <button id="pdfNextBtn"></button>
                <div class="pdf-select">
                    <button id="pdfViewModeBtn"></button>
                    <div id="pdfViewModeMenu">
                        <button data-value="single"></button>
                        <button data-value="spread"></button>
                        <button data-value="scroll"></button>
                    </div>
                </div>
                <input id="pdfCoverToggle" type="checkbox" checked />
                <button id="pdfBindingBtn"></button>
                <div id="pdfBindingMenu"></div>
                <div id="pdfCanvasContainer">
                    <canvas id="pdfCanvasLeft"></canvas>
                    <canvas id="pdfCanvasRight"></canvas>
                    <div id="pdfScrollContainer"></div>
                    <div id="pdfEmpty"></div>
                </div>
                <div id="pdfPageInfo"></div>
            </div>
            <div id="galleryArea"></div>
            <div id="imageGalleryContainer"></div>
            <div id="csvGalleryContainer"></div>
        </div>
    `;
}

function assertFrontendGate(resourceBaseline: ResourceBaseline, measurements: ResourceMeasurement[]): void {
    const budgets = resourceBaseline.metrics.filter((metric) => metric.source === 'frontend');
    const byID = new Map(measurements.map((measurement) => [measurement.id, measurement]));
    expect(byID.size).toBe(measurements.length);
    expect([...byID.keys()].sort()).toEqual(budgets.map((budget) => budget.id).sort());

    for (const budget of budgets) {
        const measurement = byID.get(budget.id);
        if (!measurement) {
            expect.fail(`missing required frontend metric ${budget.id}`);
        }
        expect(measurement.unit).toBe(budget.unit);
        expect(measurement.statistic).toBe(budget.statistic);
        expect(measurement.samples).toHaveLength(resourceBaseline.policy.samples);
        expect(measurement.samples.every(Number.isFinite)).toBe(true);
        expect(measurement.value).toBe(median(measurement.samples));
        if (!budget.gate || budget.comparison === 'observe') continue;
        const pass = budget.comparison === 'lte'
            ? measurement.value <= budget.limit
            : budget.comparison === 'gte'
                ? measurement.value >= budget.limit
                : measurement.value === budget.limit;
        expect(pass, `${budget.id}=${measurement.value} violates ${budget.comparison} ${budget.limit}`).toBe(true);
    }
}

function median(samples: number[]): number {
    const ordered = [...samples].sort((left, right) => left - right);
    const middle = Math.floor(ordered.length / 2);
    return ordered.length % 2 === 1
        ? ordered[middle]!
        : (ordered[middle - 1]! + ordered[middle]!) / 2;
}
