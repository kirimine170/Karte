import { beforeEach, describe, expect, it, vi } from 'vitest';
import { Sidebar } from '../sidebar';
import { useDocStore, useUIStore } from '../../stores/index';
import type { FileItem, FileSearchResult } from '../../types/wails-api';

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

const files: FileItem[] = [
    { path: 'content/alpha.md', title: 'Alpha title', size: 10 },
    { path: 'content/beta.md', title: 'Beta title', size: 20 },
    { path: 'content/gamma.md', title: 'Gamma title', size: 30 },
];

describe('Sidebar backend content search', () => {
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

    it('keeps title search immediate and merges every backend content-search page', async () => {
        const staleTitleSearch = deferred<FileSearchResult>();
        const searchFiles = vi.fn((query: string, page: number, limit: number) => {
            if (query === 'alpha') {
                return staleTitleSearch.promise;
            }
            if (page === 1) {
                return Promise.resolve({
                    items: [files[1]!],
                    page,
                    limit,
                    total: 2,
                    hasMore: true,
                });
            }
            return Promise.resolve({
                items: [files[2]!],
                page,
                limit,
                total: 2,
                hasMore: false,
            });
        });
        const sidebar = new Sidebar({
            GetFileList: vi.fn().mockResolvedValue(files),
            SearchFiles: searchFiles,
        } as any);
        sidebar.init();
        await vi.waitFor(() => {
            expect(document.querySelectorAll('#tree .item')).toHaveLength(3);
        });

        useDocStore.getState().setSearchQuery('alpha');
        expect(document.querySelector('.item[data-path="content/alpha.md"]')).not.toBeNull();
        expect(document.querySelectorAll('#tree .item')).toHaveLength(1);

        useDocStore.getState().setSearchQuery('needle-in-body');
        await vi.waitFor(() => {
            expect(document.querySelector('.item[data-path="content/beta.md"]')).not.toBeNull();
            expect(document.querySelector('.item[data-path="content/gamma.md"]')).not.toBeNull();
        });
        expect(document.querySelector('.item[data-path="content/alpha.md"]')).toBeNull();
        expect(searchFiles).toHaveBeenCalledWith('needle-in-body', 1, 100);
        expect(searchFiles).toHaveBeenCalledWith('needle-in-body', 2, 100);

        staleTitleSearch.resolve({
            items: [files[0]!],
            page: 1,
            limit: 100,
            total: 1,
            hasMore: false,
        });
        await Promise.resolve();
        await Promise.resolve();
        expect(document.querySelector('.item[data-path="content/alpha.md"]')).toBeNull();
        expect(document.querySelectorAll('#tree .item')).toHaveLength(2);
        sidebar.destroy();
    });

    it('does not render a content-search result that settles after destroy', async () => {
        const pendingSearch = deferred<FileSearchResult>();
        const sidebar = new Sidebar({
            GetFileList: vi.fn().mockResolvedValue(files),
            SearchFiles: vi.fn().mockReturnValue(pendingSearch.promise),
        } as any);
        const tree = document.getElementById('tree') as HTMLElement;
        sidebar.init();
        await vi.waitFor(() => {
            expect(tree.querySelectorAll('.item')).toHaveLength(3);
        });
        useDocStore.getState().setSearchQuery('body-only');
        const htmlBeforeDestroy = tree.innerHTML;
        sidebar.destroy();

        pendingSearch.resolve({
            items: [files[1]!],
            page: 1,
            limit: 100,
            total: 1,
            hasMore: false,
        });
        await Promise.resolve();
        await Promise.resolve();

        expect(tree.innerHTML).toBe(htmlBeforeDestroy);
    });
});
