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
} as any;

describe('EditorLayout', () => {
    beforeEach(() => {
        clearLogs();
        
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
});
