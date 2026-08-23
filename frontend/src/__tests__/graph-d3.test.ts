import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import GraphD3Module from '../graph-d3';

const d3Mocks = vi.hoisted(() => ({
    simulations: [] as any[],
    forceSimulation: vi.fn(),
}));

vi.mock('d3', async (importOriginal) => {
    const actual = await importOriginal<typeof import('d3')>();
    d3Mocks.forceSimulation.mockImplementation((nodes: any[]) => {
        const handlers = new Map<string, (() => void) | null>();
        const forces = new Map<string, unknown>();
        let alphaValue = 1;
        const simulation: any = {
            inputNodes: nodes,
            handlers,
            forces,
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
        d3Mocks.simulations.push(simulation);
        return simulation;
    });
    return {
        ...actual,
        forceSimulation: d3Mocks.forceSimulation,
    };
});

class ControlledResizeObserver {
    static instances: ControlledResizeObserver[] = [];
    readonly observe = vi.fn();
    readonly disconnect = vi.fn();

    constructor(private readonly callback: ResizeObserverCallback) {
        ControlledResizeObserver.instances.push(this);
    }

    emit(): void {
        this.callback([], this as unknown as ResizeObserver);
    }
}

describe('GraphD3Module lifecycle', () => {
    let graph: GraphD3Module | null;
    let documentHidden: boolean;
    let originalHiddenDescriptor: PropertyDescriptor | undefined;

    beforeEach(() => {
        vi.useFakeTimers();
        vi.clearAllMocks();
        d3Mocks.simulations.length = 0;
        ControlledResizeObserver.instances.length = 0;
        vi.stubGlobal('ResizeObserver', ControlledResizeObserver);
        vi.spyOn(console, 'log').mockImplementation(() => undefined);
        vi.spyOn(console, 'warn').mockImplementation(() => undefined);
        documentHidden = false;
        originalHiddenDescriptor = Object.getOwnPropertyDescriptor(document, 'hidden');
        Object.defineProperty(document, 'hidden', {
            configurable: true,
            get: () => documentHidden,
        });

        document.body.innerHTML = '<div id="graph-container"></div>';
        const container = document.getElementById('graph-container')!;
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
        graph = null;
    });

    afterEach(() => {
        graph?.destroy();
        vi.clearAllTimers();
        vi.useRealTimers();
        vi.unstubAllGlobals();
        vi.restoreAllMocks();
        if (originalHiddenDescriptor) {
            Object.defineProperty(document, 'hidden', originalHiddenDescriptor);
        } else {
            Reflect.deleteProperty(document, 'hidden');
        }
    });

    it('stops the old simulation before rerender and excludes hidden tag nodes and edges', () => {
        graph = new GraphD3Module('graph-container');
        graph.setData(createGraphData());
        const firstSimulation = d3Mocks.simulations[0];
        expect(firstSimulation.restart).toHaveBeenCalledOnce();

        graph.setData(createGraphData());
        expect(firstSimulation.stop).toHaveBeenCalledTimes(2);

        graph.toggleTagNodes();
        const hiddenTagSimulation = d3Mocks.simulations[2];
        expect(hiddenTagSimulation.inputNodes.map((node: any) => node.id)).toEqual(['doc:/one.md', 'doc:/two.md']);
        const linkForce = hiddenTagSimulation.forces.get('link') as { links: () => any[] };
        expect(linkForce.links()).toHaveLength(1);
        expect(document.querySelectorAll('#graph-container circle')).toHaveLength(2);
    });

    it('does not restart or apply ticks while hidden or inactive and resumes only when eligible', async () => {
        graph = new GraphD3Module('graph-container');
        graph.setActive(false);
        const data = createGraphData();
        graph.setData(data);
        const simulation = d3Mocks.simulations[0];
        const tick = simulation.handlers.get('tick') as () => void;
        const firstCircle = document.querySelector('circle')!;

        expect(simulation.restart).not.toHaveBeenCalled();
        expect(vi.getTimerCount()).toBe(0);
        graph.setActive(true);
        expect(simulation.restart).toHaveBeenCalledOnce();

        documentHidden = true;
        document.dispatchEvent(new Event('visibilitychange'));
        const restartCountWhileHidden = simulation.restart.mock.calls.length;
        data.nodes[0].x = 999;
        tick();
        expect(firstCircle.getAttribute('cx')).toBeNull();

        (graph as any).handleResize();
        expect(vi.getTimerCount()).toBe(0);
        await vi.advanceTimersByTimeAsync(50);
        expect(simulation.restart).toHaveBeenCalledTimes(restartCountWhileHidden);

        graph.setActive(false);
        documentHidden = false;
        document.dispatchEvent(new Event('visibilitychange'));
        expect(simulation.restart).toHaveBeenCalledTimes(restartCountWhileHidden);

        graph.setActive(true);
        expect(simulation.restart).toHaveBeenCalledTimes(restartCountWhileHidden + 1);

        simulation.handlers.get('end.lifecycle')?.();
        const restartCountAfterCooling = simulation.restart.mock.calls.length;
        documentHidden = true;
        document.dispatchEvent(new Event('visibilitychange'));
        documentHidden = false;
        document.dispatchEvent(new Event('visibilitychange'));
        expect(simulation.restart).toHaveBeenCalledTimes(restartCountAfterCooling);
    });

    it('releases timers, observer, tooltip, and listeners so captured callbacks are inert after destroy', () => {
        const windowRemoveSpy = vi.spyOn(window, 'removeEventListener');
        const documentRemoveSpy = vi.spyOn(document, 'removeEventListener');
        const timeoutSpy = vi.spyOn(window, 'setTimeout');
        graph = new GraphD3Module('graph-container');
        const data = createGraphData();
        graph.setData(data);
        graph.setFocus({ roots: ['doc:/one.md'], depth: 3 });
        const graphInternals = graph as any;
        const resizeListener = graphInternals.windowResizeHandler as () => void;
        const visibilityListener = graphInternals.visibilityChangeHandler as () => void;
        const observer = ControlledResizeObserver.instances[0]!;
        observer.emit();

        const timerCallbacks = timeoutSpy.mock.calls
            .filter(([, delay]) => delay === 50 || delay === 100 || delay === 500)
            .map(([callback]) => callback)
            .filter((callback): callback is () => void => typeof callback === 'function');
        expect(timerCallbacks).toHaveLength(3);

        const simulation = d3Mocks.simulations[0];
        const tick = simulation.handlers.get('tick') as () => void;
        const tooltipListener = graphInternals.nodeElements.on('mouseover') as (event: unknown, node: unknown) => void;
        const circle = document.querySelector('circle')!;
        const centerViewSpy = vi.spyOn(graph, 'centerView');
        const updateSizeSpy = vi.spyOn(graph, 'updateSimulationSize');
        const timeoutCallCount = timeoutSpy.mock.calls.length;
        const restartCallCount = simulation.restart.mock.calls.length;

        graph.destroy();
        graph = null;
        data.nodes[0].x = 1234;
        timerCallbacks.forEach((callback) => callback());
        resizeListener();
        visibilityListener();
        observer.emit();
        tick();
        tooltipListener({ pageX: 10, pageY: 10 }, data.nodes[0]);

        expect(centerViewSpy).not.toHaveBeenCalled();
        expect(updateSizeSpy).not.toHaveBeenCalled();
        expect(timeoutSpy).toHaveBeenCalledTimes(timeoutCallCount);
        expect(simulation.restart).toHaveBeenCalledTimes(restartCallCount);
        expect(circle.getAttribute('cx')).toBeNull();
        expect(document.getElementById('graph-tooltip')).toBeNull();
        expect(observer.disconnect).toHaveBeenCalledOnce();
        expect(windowRemoveSpy).toHaveBeenCalledWith('resize', resizeListener);
        expect(documentRemoveSpy).toHaveBeenCalledWith('visibilitychange', visibilityListener);
        expect(vi.getTimerCount()).toBe(0);
    });
});

function createGraphData(): { nodes: any[]; edges: any[] } {
    return {
        nodes: [
            { id: 'doc:/one.md', kind: 'note', label: 'One', exists: true, x: 10, y: 10 },
            { id: 'doc:/two.md', kind: 'note', label: 'Two', exists: true, x: 20, y: 20 },
            { id: 'tag:/topic', kind: 'tag', label: '#topic', exists: true, x: 30, y: 30 },
        ],
        edges: [
            { id: 'note-edge', kind: 'wikilink', source: 'doc:/one.md', target: 'doc:/two.md' },
            { id: 'tag-edge', kind: 'tag', source: 'doc:/one.md', target: 'tag:/topic' },
            { id: 'hidden-endpoint-edge', kind: 'wikilink', source: 'doc:/two.md', target: 'tag:/topic' },
        ],
    };
}
