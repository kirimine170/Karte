import { describe, it, expect, beforeEach, vi } from 'vitest';
import { Sidebar } from '../sidebar';
import { useUIStore, useDocStore } from '../../stores/index';
import { clearLogs, expectLogContainsSequence } from '../../test-support/log-verifier';

// Wails APIのモック
const mockApi = {
    GetFileList: vi.fn().mockResolvedValue([
        { path: 'content/test1.md', name: 'test1.md' },
        { path: 'content/test2.md', name: 'test2.md' },
    ]),
    LoadFile: vi.fn().mockResolvedValue('# Test Content'),
    PreviewMarkdown: vi.fn().mockResolvedValue('<h1>Test Content</h1>'),
} as any;

describe('Sidebar', () => {
    beforeEach(() => {
        clearLogs();
        
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
            currentPath: '',
            markdownContent: '',
            previewHtml: '',
            hasUnsavedChanges: false,
            searchQuery: '',
        });

        document.body.innerHTML = `
            <aside class="side">
                <div class="search">
                    <input id="q" placeholder="ファイル検索 (content/)" />
                </div>
                <div id="tree"></div>
            </aside>
            <div id="mainContainer"></div>
        `;
    });

    it('should initialize and log init event', () => {
        const sidebar = new Sidebar(mockApi);
        clearLogs();
        sidebar.init();

        expectLogContainsSequence([
            { component: 'Sidebar', action: 'init' }
        ]);
    });

    it('should log search input events', () => {
        const sidebar = new Sidebar(mockApi);
        sidebar.init();
        clearLogs();

        const searchInput = document.getElementById('q') as HTMLInputElement;
        searchInput.value = 'test';
        searchInput.dispatchEvent(new Event('input', { bubbles: true }));

        expectLogContainsSequence([
            { component: 'Sidebar', action: 'search-input' }
        ]);
    });

    it('should log file selection events', async () => {
        const sidebar = new Sidebar(mockApi);
        sidebar.init();
        await new Promise(resolve => setTimeout(resolve, 100)); // ファイルリスト読み込み待機
        clearLogs();

        // ファイルアイテムをクリック（実際のDOM構造に合わせて調整が必要な場合あり）
        const fileItem = document.querySelector('.item[data-path="content/test1.md"]') as HTMLElement;
        if (fileItem) {
            fileItem.click();
            await new Promise(resolve => setTimeout(resolve, 100));

            expectLogContainsSequence([
                { component: 'Sidebar', action: 'file-select' }
            ]);
        }
    });

    it('does not rebuild the file list while Markdown content changes', async () => {
        const sidebar = new Sidebar(mockApi);
        sidebar.init();
        await vi.waitFor(() => {
            expect(document.querySelector('.item[data-path="content/test1.md"]')).not.toBeNull();
        });
        const initialItem = document.querySelector('.item[data-path="content/test1.md"]');

        useDocStore.getState().setMarkdownContentAndMarkUnsaved('# Updated');

        expect(document.querySelector('.item[data-path="content/test1.md"]')).toBe(initialItem);

        useDocStore.getState().setSearchQuery('test1');
        expect(document.querySelector('.item[data-path="content/test1.md"]')).not.toBe(initialItem);
        sidebar.destroy();
    });

    it('opens Markdown from Board with matching content and a blank preview until render completes', async () => {
        const preview = createDeferred<string>();
        const api = {
            GetFileList: vi.fn().mockResolvedValue([
                { path: 'content/next.md', name: 'next.md' },
            ]),
            LoadFile: vi.fn().mockResolvedValue('# Next document'),
            PreviewMarkdown: vi.fn().mockReturnValue(preview.promise),
        } as any;
        useUIStore.setState({ activeTab: 'board' });
        useDocStore.setState({
            currentPath: 'content/example.board.md',
            markdownContent: '---\ntype: karte-board\n---',
            previewHtml: '',
        });
        const sidebar = new Sidebar(api);
        sidebar.init();
        await vi.waitFor(() => {
            expect(document.querySelector('.item[data-path="content/next.md"]')).not.toBeNull();
        });

        document.querySelector<HTMLElement>('.item[data-path="content/next.md"]')?.click();
        await vi.waitFor(() => expect(api.PreviewMarkdown).toHaveBeenCalledOnce());

        expect(useDocStore.getState()).toMatchObject({
            currentPath: 'content/next.md',
            markdownContent: '# Next document',
            previewHtml: '',
        });
        expect(useUIStore.getState().activeTab).toBe('editor');

        preview.resolve('<p>Next preview</p>');
        await vi.waitFor(() => {
            expect(useDocStore.getState().previewHtml).toContain('Next preview');
        });
        sidebar.destroy();
    });

    it('ignores an older preview response that resolves after a later file selection', async () => {
        const olderPreview = createDeferred<string>();
        const newerPreview = createDeferred<string>();
        const api = {
            GetFileList: vi.fn().mockResolvedValue([
                { path: 'content/older.md', name: 'older.md' },
                { path: 'content/newer.md', name: 'newer.md' },
            ]),
            LoadFile: vi.fn((path: string) => Promise.resolve(
                path.endsWith('older.md') ? '# Older' : '# Newer'
            )),
            PreviewMarkdown: vi.fn((content: string) => (
                content === '# Older' ? olderPreview.promise : newerPreview.promise
            )),
        } as any;
        const sidebar = new Sidebar(api);
        sidebar.init();
        await vi.waitFor(() => {
            expect(document.querySelector('.item[data-path="content/older.md"]')).not.toBeNull();
        });

        document.querySelector<HTMLElement>('.item[data-path="content/older.md"]')?.click();
        await vi.waitFor(() => expect(api.PreviewMarkdown).toHaveBeenCalledTimes(1));
        document.querySelector<HTMLElement>('.item[data-path="content/newer.md"]')?.click();
        await vi.waitFor(() => expect(api.PreviewMarkdown).toHaveBeenCalledTimes(2));

        newerPreview.resolve('<p>Newer preview</p>');
        await vi.waitFor(() => {
            expect(useDocStore.getState().previewHtml).toContain('Newer preview');
        });
        olderPreview.resolve('<p>Older preview</p>');
        await Promise.resolve();
        await Promise.resolve();

        expect(useDocStore.getState()).toMatchObject({
            currentPath: 'content/newer.md',
            markdownContent: '# Newer',
        });
        expect(useDocStore.getState().previewHtml).toContain('Newer preview');
        expect(useDocStore.getState().previewHtml).not.toContain('Older preview');
        sidebar.destroy();
    });
});

function createDeferred<T>(): { promise: Promise<T>; resolve: (value: T) => void } {
    let resolve!: (value: T) => void;
    const promise = new Promise<T>((resolvePromise) => {
        resolve = resolvePromise;
    });
    return { promise, resolve };
}
