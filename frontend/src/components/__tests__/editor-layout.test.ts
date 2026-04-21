import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import type { PreviewFocusTarget } from '../../utils/preview-frame';

const { writePreviewFrameMock } = vi.hoisted(() => ({
    writePreviewFrameMock: vi.fn(),
}));

vi.mock('../../utils/preview-frame', async () => {
    const actual = await vi.importActual<typeof import('../../utils/preview-frame')>('../../utils/preview-frame');
    return {
        ...actual,
        writePreviewFrame: writePreviewFrameMock,
    };
});

import { EditorLayout } from '../editor-layout';
import { useUIStore, useDocStore, useASRStore } from '../../stores/index';
import { clearLogs, expectLogContainsSequence, getLogsByAction } from '../../test-support/log-verifier';

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
    const instances: EditorLayout[] = [];

    beforeEach(() => {
        clearLogs();
        writePreviewFrameMock.mockReset();

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

    afterEach(() => {
        while (instances.length > 0) {
            instances.pop()?.destroy();
        }
    });

    it('should initialize and log init event', () => {
        const editorLayout = new EditorLayout(mockApi);
        instances.push(editorLayout);
        clearLogs();
        editorLayout.init();

        expectLogContainsSequence([
            { component: 'EditorLayout', action: 'init' }
        ]);
    });

    it('should log editor input events', () => {
        const editorLayout = new EditorLayout(mockApi);
        instances.push(editorLayout);
        editorLayout.init();
        clearLogs();

        const editor = document.getElementById('editor') as HTMLTextAreaElement;
        editor.value = 'test content';
        editor.dispatchEvent(new Event('input', { bubbles: true }));

        expectLogContainsSequence([
            { component: 'EditorLayout', action: 'editor-input' }
        ]);
    });

    it('captures a text-caret focus target on input', async () => {
        const editorLayout = new EditorLayout(mockApi);
        instances.push(editorLayout);
        editorLayout.init();

        const editor = document.getElementById('editor') as HTMLTextAreaElement;
        const content = '# Section\n\nAlpha line\nBeta line\nGamma line';
        editor.value = content;
        editor.setSelectionRange(content.indexOf('Beta'), content.indexOf('Beta'));
        editor.dispatchEvent(new Event('input', { bubbles: true }));
        await Promise.resolve();

        const focusTarget = (editorLayout as any).pendingPreviewFocusTarget as PreviewFocusTarget | null;
        expect(focusTarget).not.toBeNull();
        expect(focusTarget?.type).toBe('text-caret');
        if (focusTarget?.type === 'text-caret') {
            expect(focusTarget.lineText).toContain('Beta line');
            expect(focusTarget.headingText).toBe('Section');
        }
    });

    it('prioritizes inserted-image focus target on image insertion', () => {
        const editorLayout = new EditorLayout(mockApi);
        instances.push(editorLayout);
        editorLayout.init();

        (editorLayout as any).insertImageAtCursor('data/image/mock-1.png', 'mock-1.png');

        const focusTarget = (editorLayout as any).pendingPreviewFocusTarget as PreviewFocusTarget | null;
        expect(focusTarget).not.toBeNull();
        expect(focusTarget?.type).toBe('inserted-image');
        if (focusTarget?.type === 'inserted-image') {
            expect(focusTarget.path).toBe('data/image/mock-1.png');
            expect(focusTarget.alt).toBe('mock-1');
        }
    });

    it('writes the preview frame only once after setPreviewHtml', () => {
        const editorLayout = new EditorLayout(mockApi);
        instances.push(editorLayout);
        editorLayout.init();
        writePreviewFrameMock.mockClear();

        useDocStore.getState().setPreviewHtml('<p>Rendered once</p>');

        expect(writePreviewFrameMock).toHaveBeenCalledTimes(1);
    });

    it('logs a fallback when preview drop position resolves to a generic container', () => {
        const editorLayout = new EditorLayout(mockApi);
        instances.push(editorLayout);
        editorLayout.init();
        clearLogs();

        const position = (editorLayout as any).findMarkdownPositionFromElement(document.body, '# Title\n\nParagraph');

        expect(position).toBe(-1);
        const logs = getLogsByAction('preview-drop-position-fallback');
        expect(logs).toHaveLength(1);
        expect(logs[0]?.state?.elementTag).toBe('BODY');
        expect(logs[0]?.state?.isGenericContainer).toBe(true);
    });

    it('logs a block-text match when preview drop position resolves from paragraph text', () => {
        const editorLayout = new EditorLayout(mockApi);
        instances.push(editorLayout);
        editorLayout.init();
        clearLogs();

        const paragraph = document.createElement('p');
        paragraph.textContent = 'Alpha Beta Gamma';
        const position = (editorLayout as any).findMarkdownPositionFromElement(
            paragraph,
            '# Title\n\nAlpha Beta Gamma\nNext line'
        );

        expect(position).toBeGreaterThan(0);
        const logs = getLogsByAction('preview-drop-position-match');
        expect(logs.some((log) => log.state?.strategy === 'block-text')).toBe(true);
    });

    it('matches nested list items using only the direct list item text', () => {
        const editorLayout = new EditorLayout(mockApi);
        instances.push(editorLayout);
        editorLayout.init();
        clearLogs();

        const parent = document.createElement('li');
        parent.appendChild(document.createTextNode('第1章：複雑になり続けるストレージ'));
        const nestedList = document.createElement('ul');
        const child = document.createElement('li');
        child.textContent = 'VSCode拡張での限界';
        nestedList.appendChild(child);
        parent.appendChild(nestedList);

        const markdown = [
            '## 目次',
            '- 第1章：複雑になり続けるストレージ',
            '  - VSCode拡張での限界',
            '',
        ].join('\n');

        const position = (editorLayout as any).findMarkdownPositionFromElement(parent, markdown);

        expect(position).toBeGreaterThan(0);
        const logs = getLogsByAction('preview-drop-position-match');
        const listItemMatch = logs.find((log) => log.state?.strategy === 'list-item');
        expect(listItemMatch?.state?.textSignature).toBe('第1章：複雑になり続けるストレージ');
    });

    it('should log recording start/stop events', async () => {
        const editorLayout = new EditorLayout(mockApi);
        instances.push(editorLayout);
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
