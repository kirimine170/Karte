import { describe, it, expect, beforeEach, vi } from 'vitest';
import { Topbar } from '../topbar';
import { useUIStore } from '../../stores/ui-store';
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
        // ログをクリア
        clearLogs();
        
        // ストアをリセット
        useUIStore.setState({
            sidebarVisible: true,
            imageGalleryVisible: true,
            csvGalleryVisible: true,
            activeTab: 'editor',
            theme: 'light',
            hardWrap: false,
            statusMessage: '',
            statusClearTimer: null,
        });

        document.body.innerHTML = `
            <div class="bar">
                <button id="sidebarToggle">📁</button>
                <button id="galleryToggle">🖼️</button>
                <button id="csvToggle">📊</button>
                <select id="theme">
                    <option value="light">Light</option>
                    <option value="dark">Dark</option>
                </select>
                <input id="hardwrap" type="checkbox" />
                <button id="saveBtn">保存</button>
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
});

