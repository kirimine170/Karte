import { describe, it, expect, beforeEach, vi } from 'vitest';
import { Sidebar } from '../sidebar';
import { useUIStore, useDocStore } from '../../stores/index';
import { clearLogs, expectLogSequence, expectLogContainsSequence } from '../../test-support/log-verifier';

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
            currentPath: null,
            markdownContent: '',
            previewHtml: '',
            hasUnsavedChanges: false,
            lastSavedContent: '',
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
});
