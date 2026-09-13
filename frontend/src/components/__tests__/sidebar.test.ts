import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
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
    let sidebar: Sidebar;

    beforeEach(() => {
        clearLogs();
        vi.clearAllMocks();
        mockApi.GetFileList.mockReset().mockResolvedValue([
            { path: 'content/test1.md', name: 'test1.md' },
            { path: 'content/test2.md', name: 'test2.md' },
        ]);
        
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
            currentPath: null,
            markdownContent: '',
            previewHtml: '',
            hasUnsavedChanges: false,
            lastSavedContent: '',
            searchQuery: '',
        });

        document.body.innerHTML = `
            <aside class="side">
                <button id="fileListRefreshBtn" type="button">一覧を更新</button>
                <div class="search">
                    <input id="q" placeholder="ファイル検索 (content/)" />
                </div>
                <p id="fileListStatus" role="status" hidden></p>
                <div id="tree"></div>
            </aside>
            <div id="mainContainer"></div>
        `;
        sidebar = new Sidebar(mockApi);
    });

    afterEach(() => {
        sidebar.destroy();
        useUIStore.getState().clearStatusMessage();
        vi.restoreAllMocks();
    });

    it('should initialize and log init event', () => {
        clearLogs();
        sidebar.init();

        expectLogContainsSequence([
            { component: 'Sidebar', action: 'init' }
        ]);
    });

    it('should log search input events', () => {
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
        sidebar.init();
        await vi.waitFor(() => expect(document.querySelector('.item[data-path="content/test1.md"]')).not.toBeNull());
        clearLogs();

        // ファイルアイテムをクリック（実際のDOM構造に合わせて調整が必要な場合あり）
        const fileItem = document.querySelector('.item[data-path="content/test1.md"]') as HTMLElement;
        fileItem.click();
        await vi.waitFor(() => expect(mockApi.LoadFile).toHaveBeenCalledWith('content/test1.md'));
        expectLogContainsSequence([{ component: 'Sidebar', action: 'file-select' }]);
    });

    it('reflects external additions and deletions without replacing unsaved edits or the search', async () => {
        sidebar.init();
        const button = document.getElementById('fileListRefreshBtn') as HTMLButtonElement;
        await vi.waitFor(() => expect(button.disabled).toBe(false));
        useDocStore.setState({
            currentPath: 'content/test1.md', markdownContent: 'Unsaved local edit',
            previewHtml: '<p>Local preview</p>', hasUnsavedChanges: true, searchQuery: 'new',
        });
        const files = [{ path: 'content/new.md', title: 'New recording' }];
        mockApi.GetFileList.mockResolvedValue(files);

        button.click();
        button.click();
        expect(button.disabled).toBe(true);
        await vi.waitFor(() => expect(button.disabled).toBe(false));

        expect(mockApi.GetFileList).toHaveBeenCalledTimes(2);
        expect(document.querySelector('.item[data-path="content/new.md"]')).not.toBeNull();
        expect(document.querySelector('.item[data-path="content/test1.md"]')).toBeNull();
        expect(useDocStore.getState()).toMatchObject({
            files, currentPath: 'content/test1.md', markdownContent: 'Unsaved local edit',
            previewHtml: '<p>Local preview</p>', hasUnsavedChanges: true, searchQuery: 'new',
        });
        expect((document.getElementById('q') as HTMLInputElement).value).toBe('new');
        expect(mockApi.LoadFile).not.toHaveBeenCalled();
        expect(document.getElementById('fileListStatus')?.textContent).toBe('一覧を更新しました（全1件）');
    });

    it('keeps the previous list on failure and allows an explicit retry', async () => {
        vi.spyOn(console, 'error').mockImplementation(() => {});
        sidebar.init();
        const button = document.getElementById('fileListRefreshBtn') as HTMLButtonElement;
        await vi.waitFor(() => expect(button.disabled).toBe(false));
        const previous = useDocStore.getState().files;
        mockApi.GetFileList.mockRejectedValueOnce(new Error('unavailable'));

        button.click();
        await vi.waitFor(() => expect(button.disabled).toBe(false));
        expect(useDocStore.getState().files).toEqual(previous);
        expect(document.getElementById('fileListStatus')?.dataset.state).toBe('error');
        expect(document.getElementById('fileListStatus')?.hidden).toBe(false);

        mockApi.GetFileList.mockResolvedValueOnce([]);
        button.click();
        await vi.waitFor(() => expect(useDocStore.getState().files).toEqual([]));
        expect(document.getElementById('fileListStatus')?.textContent).toBe('一覧を更新しました（全0件）');
    });

    it('does not revert a newer list when an earlier refresh finishes late', async () => {
        sidebar.init();
        await vi.waitFor(() => expect(useDocStore.getState().files).toHaveLength(2));
        let finishEarlier!: (files: unknown[]) => void;
        mockApi.GetFileList.mockReturnValueOnce(new Promise((resolve) => { finishEarlier = resolve; }));
        const earlier = sidebar.refreshFileList(true);
        const newest = [{ path: 'content/latest.md', title: 'Latest' }];
        mockApi.GetFileList.mockResolvedValueOnce(newest);
        await sidebar.refreshFileList();
        finishEarlier([{ path: 'content/stale.md', title: 'Stale' }]);
        await earlier;

        expect(useDocStore.getState().files).toEqual(newest);
        expect(document.getElementById('tree')?.getAttribute('aria-busy')).toBe('false');
        expect(document.getElementById('fileListStatus')?.textContent).toBe('一覧を更新しました（全1件）');
    });
});
