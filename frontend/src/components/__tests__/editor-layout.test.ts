import { describe, it, expect, beforeEach, vi } from 'vitest';
import { EditorLayout } from '../editor-layout';
import { useUIStore, useDocStore, useASRStore } from '../../stores/index';
import { clearLogs, expectLogSequence, expectLogContainsSequence } from '../../test-support/log-verifier';

// Wails APIのモック
const mockApi = {
    SaveFile: vi.fn().mockResolvedValue(undefined),
    PreviewMarkdown: vi.fn().mockResolvedValue('<p>Preview</p>'),
    StartRecording: vi.fn().mockResolvedValue(undefined),
    StopRecording: vi.fn().mockResolvedValue('audio.wav'),
    GetAudioFileURL: vi.fn().mockResolvedValue('http://localhost/audio.wav'),
    GetASRStatus: vi.fn().mockResolvedValue({ initialized: true, initializing: false }),
} as any;

describe('EditorLayout', () => {
    beforeEach(() => {
        vi.clearAllMocks();
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
            currentPath: 'content/test.md',
            markdownContent: '# Test',
            previewHtml: '',
            hasUnsavedChanges: false,
            lastSavedContent: '',
            searchQuery: '',
        });

        useASRStore.setState({
            isRecording: false,
            micLevel: 0,
            status: { initialized: true, initializing: false },
            realtimeTranscript: { partial: '', final: [] },
        });

        document.body.innerHTML = `
            <div id="contentArea">
                <div class="editor-pane-wrapper">
                    <div class="tabs"></div>
                    <div class="tab-content active" id="editor-tab">
                        <div class="editor-pane">
                            <div class="editor-pane-body">
                                <textarea id="editor"></textarea>
                            </div>
                            <div id="editorFooter">
                                <button id="recordingBtnFooter">🎤 録音</button>
                                <div id="recordingIndicatorFooter" style="display: none;"></div>
                            </div>
                        </div>
                    </div>
                </div>
                <div class="preview-pane">
                    <div class="preview-pane-body">
                        <iframe id="preview"></iframe>
                    </div>
                </div>
                <div id="galleryArea">
                    <div id="imageGalleryContainer"></div>
                    <div id="csvGalleryContainer"></div>
                </div>
            </div>
        `;
    });

    it('should initialize and log init event', () => {
        const editorLayout = new EditorLayout(mockApi);
        clearLogs();
        editorLayout.init();

        expectLogContainsSequence([
            { component: 'EditorLayout', action: 'init' }
        ]);
    });

    it('should log editor input events', () => {
        const editorLayout = new EditorLayout(mockApi);
        editorLayout.init();
        clearLogs();

        const editor = document.getElementById('editor') as HTMLTextAreaElement;
        editor.value = 'test content';
        editor.dispatchEvent(new Event('input', { bubbles: true }));

        expectLogContainsSequence([
            { component: 'EditorLayout', action: 'editor-input' }
        ]);
    });

    it('keeps document-relative report images resolved after editing', async () => {
        const currentPath = 'content/Reports/Quarterly.md';
        const content = '# Updated results\n\n![Analysis state](assets/state.png)';
        const resolvedImage = '/image/content/Reports/assets/state.png';
        const api = {
            ...mockApi,
            PreviewMarkdownForPath: vi.fn().mockResolvedValue(`<img src="${resolvedImage}" alt="Analysis state">`),
        };
        useDocStore.setState({ currentPath });
        const editorLayout = new EditorLayout(api);
        editorLayout.init();

        try {
            const editor = document.getElementById('editor') as HTMLTextAreaElement;
            editor.value = content;
            editor.dispatchEvent(new Event('input', { bubbles: true }));

            await vi.waitFor(() => {
                expect(api.PreviewMarkdownForPath).toHaveBeenCalledWith(currentPath, content);
                expect(useDocStore.getState().previewHtml).toContain(`src="${resolvedImage}"`);
            });
            expect(mockApi.PreviewMarkdown).not.toHaveBeenCalled();
            expect(useDocStore.getState().markdownContent).toBe(content);
            expect(useDocStore.getState().hasUnsavedChanges).toBe(true);
        } finally {
            editorLayout.destroy();
        }
    });

    it.each(['content/Reports/Quarterly.md', ''])('previews edits with the legacy API for path %j', async (currentPath) => {
        useDocStore.setState({ currentPath });
        const editorLayout = new EditorLayout(mockApi);
        editorLayout.init();

        try {
            const editor = document.getElementById('editor') as HTMLTextAreaElement;
            editor.value = '# Updated report';
            editor.dispatchEvent(new Event('input', { bubbles: true }));

            await vi.waitFor(() => {
                expect(mockApi.PreviewMarkdown).toHaveBeenCalledWith('# Updated report');
                expect(useDocStore.getState().previewHtml).toContain('<p>Preview</p>');
            });
        } finally {
            editorLayout.destroy();
        }
    });

    it('should log recording start/stop events', async () => {
        const editorLayout = new EditorLayout(mockApi);
        editorLayout.init();
        clearLogs();

        const recordingBtnFooter = document.getElementById('recordingBtnFooter') as HTMLButtonElement;
        recordingBtnFooter.click();
        await new Promise(resolve => setTimeout(resolve, 100));

        expectLogContainsSequence([
            { component: 'EditorLayout', action: 'recording-start' }
        ]);
    });

    it('synchronizes the ASR status before recording', async () => {
        useASRStore.setState({
            status: { initialized: false, initializing: true },
        });
        mockApi.GetASRStatus.mockResolvedValueOnce({ initialized: true, initializing: false });

        const editorLayout = new EditorLayout(mockApi);
        editorLayout.init();
        const recordingBtnFooter = document.getElementById('recordingBtnFooter') as HTMLButtonElement;
        recordingBtnFooter.click();

        await vi.waitFor(() => expect(mockApi.StartRecording).toHaveBeenCalled());
        expect(useASRStore.getState().status).toEqual({ initialized: true, initializing: false });
        editorLayout.destroy();
    });

    it('keeps hardwrap enabled after saving with Ctrl+S', async () => {
        const editorLayout = new EditorLayout(mockApi);
        editorLayout.init();
        useUIStore.getState().setHardWrap(true);

        document.dispatchEvent(new KeyboardEvent('keydown', {
            key: 's',
            ctrlKey: true,
            bubbles: true,
            cancelable: true,
        }));

        await vi.waitFor(() => expect(mockApi.SaveFile).toHaveBeenCalledWith('content/test.md', '# Test'));
        expect(useUIStore.getState().hardWrap).toBe(true);
        editorLayout.destroy();
    });
});
