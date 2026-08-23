import { beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from '../app';
import { useDocStore, useUIStore } from '../stores/index';

vi.mock('../api/wails-api', () => ({
    getWailsAppAPI: vi.fn(),
    getWailsRuntimeAPI: vi.fn(),
}));

describe('App document navigation', () => {
    beforeEach(() => {
        useDocStore.setState({
            files: [],
            currentPath: 'content/current.board.md',
            markdownContent: 'board source',
            previewHtml: '',
            hasUnsavedChanges: false,
            searchQuery: '',
        });
        useUIStore.setState({ activeTab: 'board', workspaceMode: false });
    });

    it('keeps the latest path，content，preview，and tab when App loads resolve out of order', async () => {
        const olderPreview = deferred<string>();
        const newerPreview = deferred<string>();
        const api = {
            LoadFile: vi.fn((path: string) => Promise.resolve(
                path.endsWith('older.md') ? '# Older app document' : '# Newer app document'
            )),
            PreviewMarkdown: vi.fn((content: string) => (
                content.includes('Older') ? olderPreview.promise : newerPreview.promise
            )),
        };
        const app = new App();
        Reflect.set(app, 'api', api);
        const loadFileByPath = Reflect.get(app, 'loadFileByPath') as (
            this: App,
            path: string
        ) => Promise<void>;

        const olderLoad = loadFileByPath.call(app, 'content/older.md');
        await vi.waitFor(() => expect(api.PreviewMarkdown).toHaveBeenCalledTimes(1));
        const newerLoad = loadFileByPath.call(app, 'content/newer.md');
        await vi.waitFor(() => expect(api.PreviewMarkdown).toHaveBeenCalledTimes(2));

        expect(useDocStore.getState()).toMatchObject({
            currentPath: 'content/newer.md',
            markdownContent: '# Newer app document',
            previewHtml: '',
        });
        expect(useUIStore.getState().activeTab).toBe('editor');

        newerPreview.resolve('<p>Newer app preview</p>');
        await newerLoad;
        olderPreview.resolve('<p>Older app preview</p>');
        await olderLoad;

        expect(useDocStore.getState()).toMatchObject({
            currentPath: 'content/newer.md',
            markdownContent: '# Newer app document',
        });
        expect(useDocStore.getState().previewHtml).toContain('Newer app preview');
        expect(useDocStore.getState().previewHtml).not.toContain('Older app preview');
        expect(useUIStore.getState().activeTab).toBe('editor');
    });
});

function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void } {
    let resolve!: (value: T) => void;
    const promise = new Promise<T>((resolvePromise) => {
        resolve = resolvePromise;
    });
    return { promise, resolve };
}
