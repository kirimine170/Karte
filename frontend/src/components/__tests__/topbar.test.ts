import { describe, it, expect, beforeEach, vi } from 'vitest';
import { Topbar } from '../topbar';
import { useUIStore } from '../../stores/ui-store';
import { useModalStore } from '../../stores/modal-store';
import { useDocStore } from '../../stores/doc-store';
import { eventLogger } from '../../utils/event-logger';
import { clearLogs, expectLogSequence, expectLogContainsSequence } from '../../test-support/log-verifier';

// Wails APIのモック
const mockApi = {
    SaveFile: vi.fn(),
    GetFileList: vi.fn(),
    LoadFile: vi.fn(),
    PreviewMarkdown: vi.fn(),
    ExportPDF: vi.fn(),
    GetCustomCSS: vi.fn(),
    SetCustomCSS: vi.fn(),
} as any;

describe('Topbar', () => {
    beforeEach(() => {
		vi.clearAllMocks();
        // ログをクリア
        clearLogs();
        
        // ストアをリセット
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
        useModalStore.setState({
            filenameModal: { visible: false, value: '' },
            renameFileModal: { visible: false, value: '', currentPath: '' },
            unsavedConfirmModal: { visible: false, onSave: () => {}, onDiscard: () => {} },
            customCssModal: { visible: false, value: '' },
            webClipModal: { visible: false, url: '', importing: false, warnings: [] },
            csvEditModal: { visible: false, filePath: '', data: [] },
            conflictModal: { visible: false, conflictInfo: null },
            imagePreviewModal: { visible: false, imagePath: '', imageName: '', metadata: '', systemMetadata: '' },
        });
		useDocStore.setState({
			currentPath: 'content/projects/ephy/note/2026-09/context.md',
			markdownContent: '# Context',
			previewHtml: '<h1>Context</h1>',
			hasUnsavedChanges: false,
		});

        document.body.innerHTML = `
            <div class="bar">
                <button id="sidebarToggle">📁</button>
                <button id="galleryToggle">🖼️</button>
                <button id="csvToggle">📊</button>
                <button id="workspaceToggle">🖥️</button>
                <select id="theme">
                    <option value="light">Light</option>
                    <option value="dark">Dark</option>
                </select>
                <input id="hardwrap" type="checkbox" />
                <button id="saveBtn">保存</button>
                <button id="webClipBtn">Web Clip</button>
                <div id="status"></div>
            </div>
        `;
    });

    it('should initialize and set up event listeners', () => {
        const topbar = new Topbar(mockApi);
        topbar.init();

        const sidebarBtn = document.getElementById('sidebarToggle');
        expect(sidebarBtn).toBeTruthy();
    });

    it('should toggle sidebar visibility on button click', () => {
        const topbar = new Topbar(mockApi);
        topbar.init();

        const sidebarBtn = document.getElementById('sidebarToggle') as HTMLButtonElement;
        const initialState = useUIStore.getState().sidebarVisible;
        
        // クリックイベントを発火
        sidebarBtn.dispatchEvent(new MouseEvent('click', { bubbles: true }));
        
        // 状態が変更されたか確認
        expect(useUIStore.getState().sidebarVisible).toBe(!initialState);
    });

    it('should toggle image gallery visibility on button click', () => {
        const topbar = new Topbar(mockApi);
        topbar.init();

        const galleryBtn = document.getElementById('galleryToggle') as HTMLButtonElement;
        const initialState = useUIStore.getState().imageGalleryVisible;

        galleryBtn.dispatchEvent(new MouseEvent('click', { bubbles: true }));
        expect(useUIStore.getState().imageGalleryVisible).toBe(!initialState);
    });

    it('should toggle CSV gallery visibility on button click', () => {
        const topbar = new Topbar(mockApi);
        topbar.init();

        const csvBtn = document.getElementById('csvToggle') as HTMLButtonElement;
        const initialState = useUIStore.getState().csvGalleryVisible;

        csvBtn.dispatchEvent(new MouseEvent('click', { bubbles: true }));
        expect(useUIStore.getState().csvGalleryVisible).toBe(!initialState);
    });

    it('should show web clip modal on button click', () => {
        const topbar = new Topbar(mockApi);
        topbar.init();

        const webClipBtn = document.getElementById('webClipBtn') as HTMLButtonElement;
        webClipBtn.click();

        expect(useModalStore.getState().webClipModal.visible).toBe(true);
    });

    it('should log events in correct order when initializing and clicking buttons', () => {
        const topbar = new Topbar(mockApi);
        clearLogs();
        
        topbar.init();
        
        const sidebarBtn = document.getElementById('sidebarToggle') as HTMLButtonElement;
        sidebarBtn.onclick = () => {
            // onclickで直接呼び出し
            (sidebarBtn as any).__onclick?.();
        };
        sidebarBtn.click();

        // ログの順序を検証
        expectLogContainsSequence([
            { component: 'Topbar', action: 'init' },
            { component: 'Topbar', action: 'sidebar-toggle' }
        ]);
    });

    it('should log all button clicks in sequence', () => {
        const topbar = new Topbar(mockApi);
        topbar.init();
        clearLogs();

        const sidebarBtn = document.getElementById('sidebarToggle') as HTMLButtonElement;
        const galleryBtn = document.getElementById('galleryToggle') as HTMLButtonElement;
        const csvBtn = document.getElementById('csvToggle') as HTMLButtonElement;

        sidebarBtn.click();
        galleryBtn.click();
        csvBtn.click();

        // ログの順序を検証
        expectLogSequence([
            { component: 'Topbar', action: 'sidebar-toggle' },
            { component: 'Topbar', action: 'gallery-toggle' },
            { component: 'Topbar', action: 'csv-toggle' }
        ]);
    });

	it('passes canonical document identity to the export policy boundary', async () => {
		mockApi.ExportPDF.mockResolvedValue('/mock/context.pdf');
		const topbar = new Topbar(mockApi);
		await (topbar as any).handleExportPDF();
		expect(mockApi.ExportPDF).toHaveBeenCalledWith(
			'content/projects/ephy/note/2026-09/context.md',
		);
	});

	it('saves unsaved canonical content before exporting it', async () => {
		mockApi.SaveFile.mockResolvedValue(undefined);
		mockApi.ExportPDF.mockResolvedValue('/mock/context.pdf');
		useDocStore.setState({ hasUnsavedChanges: true });
		const topbar = new Topbar(mockApi);
		await (topbar as any).handleExportPDF();
		expect(mockApi.SaveFile).toHaveBeenCalledWith(
			'content/projects/ephy/note/2026-09/context.md',
			'# Context',
		);
		expect(mockApi.SaveFile.mock.invocationCallOrder[0]).toBeLessThan(
			mockApi.ExportPDF.mock.invocationCallOrder[0],
		);
	});
});
