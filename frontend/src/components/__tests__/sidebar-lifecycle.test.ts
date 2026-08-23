import { beforeEach, describe, expect, it, vi } from 'vitest';
import { Sidebar } from '../sidebar';
import { useDocStore, useUIStore } from '../../stores/index';
import type { FileItem } from '../../types/wails-api';

function deferred<T>(): {
    promise: Promise<T>;
    resolve: (value: T) => void;
} {
    let resolve!: (value: T) => void;
    const promise = new Promise<T>((promiseResolve) => {
        resolve = promiseResolve;
    });
    return { promise, resolve };
}

function lifecycleCleanupCount(component: object): number {
    return (component as { unsubscribe: Array<() => void> }).unsubscribe.length;
}

describe('Sidebar lifecycle', () => {
    beforeEach(() => {
        useUIStore.setState({ sidebarVisible: true, statusMessage: '', statusClearTimer: null });
        useDocStore.setState({
            files: [],
            currentPath: '',
            markdownContent: '',
            previewHtml: '',
            hasUnsavedChanges: false,
            searchQuery: '',
        });
        document.body.innerHTML = `
            <aside class="side">
                <input id="q" />
                <div id="tree"></div>
            </aside>
            <div id="mainContainer"></div>
        `;
    });

    it('uses one tree listener across 100 file-list rerenders', async () => {
        const files: FileItem[] = [
            { path: 'content/alpha.md', title: 'Alpha' },
            { path: 'content/beta.md', title: 'Beta' },
        ];
        const api = {
            GetFileList: vi.fn().mockResolvedValue(files),
        } as any;
        const tree = document.getElementById('tree') as HTMLElement;
        const addEventListener = vi.spyOn(tree, 'addEventListener');
        const removeEventListener = vi.spyOn(tree, 'removeEventListener');
        const sidebar = new Sidebar(api);

        sidebar.init();
        await vi.waitFor(() => {
            expect(tree.querySelectorAll('.item')).toHaveLength(2);
        });
        const initialCleanupCount = lifecycleCleanupCount(sidebar);

        for (let index = 0; index < 100; index += 1) {
            useDocStore.getState().setSearchQuery(index % 2 === 0 ? 'alpha' : '');
        }

        expect(addEventListener).toHaveBeenCalledTimes(1);
        expect(lifecycleCleanupCount(sidebar)).toBe(initialCleanupCount);
        expect(tree.querySelectorAll('.item')).toHaveLength(2);

        sidebar.destroy();
        expect(removeEventListener).toHaveBeenCalledTimes(1);
    });

    it('ignores a file-list result that settles after destroy', async () => {
        const pendingFiles = deferred<FileItem[]>();
        const api = {
            GetFileList: vi.fn().mockReturnValue(pendingFiles.promise),
        } as any;
        const originalFiles: FileItem[] = [{ path: 'content/existing.md', title: 'Existing' }];
        useDocStore.getState().setFiles(originalFiles);
        const tree = document.getElementById('tree') as HTMLElement;
        const sidebar = new Sidebar(api);

        sidebar.init();
        expect(api.GetFileList).toHaveBeenCalledTimes(1);
        const htmlBeforeDestroy = tree.innerHTML;
        sidebar.destroy();

        pendingFiles.resolve([{ path: 'content/late.md', title: 'Late' }]);
        await Promise.resolve();
        await Promise.resolve();

        expect(useDocStore.getState().files).toBe(originalFiles);
        expect(tree.innerHTML).toBe(htmlBeforeDestroy);
    });

    it('does not commit a document load that settles after destroy', async () => {
        const pendingContent = deferred<string>();
        const api = {
            GetFileList: vi.fn().mockResolvedValue([
                { path: 'content/late.md', title: 'Late' },
            ]),
            LoadFile: vi.fn().mockReturnValue(pendingContent.promise),
            PreviewMarkdown: vi.fn(),
        } as any;
        useUIStore.setState({ activeTab: 'board' });
        useDocStore.setState({
            currentPath: 'content/current.board.md',
            markdownContent: 'board source',
            previewHtml: '',
        });
        const sidebar = new Sidebar(api);
        sidebar.init();
        await vi.waitFor(() => {
            expect(document.querySelector('.item[data-path="content/late.md"]')).not.toBeNull();
        });
        document.querySelector<HTMLElement>('.item[data-path="content/late.md"]')?.click();
        await vi.waitFor(() => expect(api.LoadFile).toHaveBeenCalledOnce());

        sidebar.destroy();
        pendingContent.resolve('# Late');
        await Promise.resolve();
        await Promise.resolve();

        expect(useDocStore.getState()).toMatchObject({
            currentPath: 'content/current.board.md',
            markdownContent: 'board source',
            previewHtml: '',
        });
        expect(useUIStore.getState().activeTab).toBe('board');
        expect(api.PreviewMarkdown).not.toHaveBeenCalled();
    });
});
