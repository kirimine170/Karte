import { beforeEach, describe, expect, it, vi } from 'vitest';
import { GraphView } from '../graph-view';
import { useDocStore, useUIStore } from '../../stores/index';

const graphModuleMocks = vi.hoisted(() => ({
    on: vi.fn(),
    setData: vi.fn(),
    setFocus: vi.fn(),
    setActive: vi.fn(),
    toggleTagNodes: vi.fn(),
    areTagNodesVisible: vi.fn(() => true),
    destroy: vi.fn(),
}));

vi.mock('../../graph-d3', () => ({
    default: class MockGraphModule {
        readonly on = graphModuleMocks.on;
        readonly setData = graphModuleMocks.setData;
        readonly setFocus = graphModuleMocks.setFocus;
        readonly setActive = graphModuleMocks.setActive;
        readonly toggleTagNodes = graphModuleMocks.toggleTagNodes;
        readonly areTagNodesVisible = graphModuleMocks.areTagNodesVisible;
        readonly destroy = graphModuleMocks.destroy;
    },
}));

const mockApi = {
    GetGraphData: vi.fn().mockResolvedValue({ nodes: [], links: [] }),
} as any;

describe('GraphView store subscription', () => {
    beforeEach(() => {
        vi.clearAllMocks();
        useDocStore.setState({
            files: [],
            currentPath: 'content/test.md',
            markdownContent: '# Test',
            previewHtml: '',
            hasUnsavedChanges: false,
            searchQuery: '',
        });
        useUIStore.setState({ activeTab: 'editor' });
        document.body.innerHTML = '<div id="graph-container"></div>';
    });

    it('does not refocus the graph while Markdown content changes', async () => {
        const graphView = new GraphView(mockApi);
        graphView.init();
        await vi.waitFor(() => {
            expect(graphModuleMocks.setData).toHaveBeenCalledTimes(1);
        });
        graphModuleMocks.setFocus.mockClear();

        useDocStore.getState().setMarkdownContentAndMarkUnsaved('# Updated');

        expect(graphModuleMocks.setFocus).not.toHaveBeenCalled();

        useDocStore.getState().setCurrentPath('content/other.md');
        expect(graphModuleMocks.setFocus).toHaveBeenCalledOnce();
        expect(graphModuleMocks.setFocus).toHaveBeenCalledWith({
            roots: ['doc:/other.md'],
            depth: 3,
        });
        graphView.destroy();
    });

    it('runs the graph only while the graph tab is active and unsubscribes on destroy', async () => {
        const graphView = new GraphView(mockApi);
        graphView.init();
        await vi.waitFor(() => {
            expect(graphModuleMocks.setData).toHaveBeenCalledTimes(1);
        });

        expect(graphModuleMocks.setActive).toHaveBeenCalledWith(false);
        graphModuleMocks.setActive.mockClear();

        useUIStore.getState().setTheme('dark');
        expect(graphModuleMocks.setActive).not.toHaveBeenCalled();

        useUIStore.getState().setActiveTab('graph');
        expect(graphModuleMocks.setActive).toHaveBeenLastCalledWith(true);
        useUIStore.getState().setActiveTab('editor');
        expect(graphModuleMocks.setActive).toHaveBeenLastCalledWith(false);

        graphView.destroy();
        graphModuleMocks.setActive.mockClear();
        useUIStore.getState().setActiveTab('graph');

        expect(graphModuleMocks.setActive).not.toHaveBeenCalled();
        expect(graphModuleMocks.destroy).toHaveBeenCalledOnce();
    });

    it('opens a graph Markdown node through the coherent Editor transition', async () => {
        const preview = deferred<string>();
        const api = {
            GetGraphData: vi.fn().mockResolvedValue({ nodes: [], links: [] }),
            LoadFile: vi.fn().mockResolvedValue('# Graph target'),
            PreviewMarkdown: vi.fn().mockReturnValue(preview.promise),
        } as any;
        useUIStore.setState({ activeTab: 'board' });
        useDocStore.setState({
            currentPath: 'content/current.board.md',
            markdownContent: 'board source',
            previewHtml: '',
        });
        const graphView = new GraphView(api);
        graphView.init();
        await vi.waitFor(() => expect(api.GetGraphData).toHaveBeenCalledOnce());
        const nodeClick = graphModuleMocks.on.mock.calls.find(
            ([eventName]) => eventName === 'node:click'
        )?.[1] as ((data: { id: string }) => void) | undefined;

        nodeClick?.({ id: 'doc:/graph-target.md' });
        await vi.waitFor(() => expect(api.PreviewMarkdown).toHaveBeenCalledOnce());

        expect(useDocStore.getState()).toMatchObject({
            currentPath: 'content/graph-target.md',
            markdownContent: '# Graph target',
            previewHtml: '',
        });
        expect(useUIStore.getState().activeTab).toBe('editor');

        preview.resolve('<p>Graph target preview</p>');
        await vi.waitFor(() => {
            expect(useDocStore.getState().previewHtml).toContain('Graph target preview');
        });
        graphView.destroy();
    });
});

function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void } {
    let resolve!: (value: T) => void;
    const promise = new Promise<T>((resolvePromise) => {
        resolve = resolvePromise;
    });
    return { promise, resolve };
}
