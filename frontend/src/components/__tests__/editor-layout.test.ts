import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { EditorLayout } from '../editor-layout';
import { useUIStore, useDocStore, useASRStore } from '../../stores/index';
import type { BoardDocument } from '../../types/wails-api';
import { clearLogs, expectLogContainsSequence } from '../../test-support/log-verifier';
import {
    activateDocumentTab,
    beginDocumentTransition,
    commitBoardDocumentTransition,
    commitDocumentPreview,
    commitEditorDocumentTransition,
} from '../../utils/document-transition';

// Wails APIのモック
const mockApi = {
    SaveFile: vi.fn().mockResolvedValue(undefined),
    PreviewMarkdown: vi.fn().mockResolvedValue('<p>Preview</p>'),
    GetASRStatus: vi.fn().mockResolvedValue({ initialized: true, initializing: false }),
    StartRecording: vi.fn().mockResolvedValue(undefined),
    StopRecording: vi.fn().mockResolvedValue('audio.wav'),
    GetAudioFileURL: vi.fn().mockResolvedValue('http://localhost/audio.wav'),
} as any;

describe('EditorLayout', () => {
    let editorLayout: EditorLayout;

    beforeEach(() => {
        clearLogs();
        vi.clearAllMocks();
        mockApi.PreviewMarkdown.mockReset().mockResolvedValue('<p>Preview</p>');
        
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
                                <div id="micLevelFillFooter"></div>
                                <div id="realtimeTranscript">
                                    <div id="realtimeTranscriptContent"></div>
                                </div>
                            </div>
                        </div>
                    </div>
                </div>
                <div class="preview-pane">
                    <div class="preview-pane-header">
                        <h3>プレビュー</h3>
                        <div id="previewPageControls" hidden>
                            <button id="previewPrevBtn"></button>
                            <span id="previewPageInfo">-</span>
                            <button id="previewNextBtn"></button>
                        </div>
                    </div>
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

        editorLayout = new EditorLayout(mockApi);
    });

    afterEach(() => {
        editorLayout.destroy();
        vi.useRealTimers();
        vi.restoreAllMocks();
    });

    it('should initialize and log init event', () => {
        clearLogs();
        editorLayout.init();

        expectLogContainsSequence([
            { component: 'EditorLayout', action: 'init' }
        ]);
    });

    it('should log editor input events', () => {
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

        editorLayout.init();
        const recordingBtnFooter = document.getElementById('recordingBtnFooter') as HTMLButtonElement;
        recordingBtnFooter.click();

        await vi.waitFor(() => expect(mockApi.StartRecording).toHaveBeenCalled());
        expect(useASRStore.getState().status).toEqual({ initialized: true, initializing: false });
    });

    it('keeps hardwrap enabled after saving with Ctrl+S', async () => {
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
    });

    it('renders ASR text as inert DOM text instead of HTML', () => {
        editorLayout.init();
        const injected = '<img src=x onerror="window.__asrInjected=true"><script>window.__asrInjected=true</script>';
        useASRStore.setState({
            isRecording: true,
            realtimeTranscript: {
                final: [injected],
                partial: '<svg onload="window.__asrInjected=true">',
            },
        });

        const content = document.getElementById('realtimeTranscriptContent')!;
        expect(content.querySelector('img, script, svg')).toBeNull();
        expect(content.textContent).toContain(injected);
        expect(content.textContent).toContain('<svg onload=');
    });

    it('debounces consecutive editor input before rendering the latest content', async () => {
        vi.useFakeTimers();
        editorLayout.init();

        const editor = document.getElementById('editor') as HTMLTextAreaElement;
        const input = (content: string) => {
            editor.value = content;
            editor.dispatchEvent(new Event('input', { bubbles: true }));
        };

        input('first');
        await vi.advanceTimersByTimeAsync(100);
        input('second');
        await vi.advanceTimersByTimeAsync(100);
        input('latest');
        await vi.advanceTimersByTimeAsync(199);

        expect(mockApi.PreviewMarkdown).not.toHaveBeenCalled();

        await vi.advanceTimersByTimeAsync(1);

        expect(mockApi.PreviewMarkdown).toHaveBeenCalledTimes(1);
        expect(mockApi.PreviewMarkdown).toHaveBeenCalledWith('latest');
    });

    it('does not let an older render response overwrite the latest preview', async () => {
        vi.useFakeTimers();
        const firstResponse = createDeferred<string>();
        const latestResponse = createDeferred<string>();
        mockApi.PreviewMarkdown.mockImplementation((content: string) => {
            return content === 'first' ? firstResponse.promise : latestResponse.promise;
        });
        editorLayout.init();

        const editor = document.getElementById('editor') as HTMLTextAreaElement;
        editor.value = 'first';
        editor.dispatchEvent(new Event('input', { bubbles: true }));
        await vi.advanceTimersByTimeAsync(200);

        editor.value = 'latest';
        editor.dispatchEvent(new Event('input', { bubbles: true }));
        await vi.advanceTimersByTimeAsync(200);

        expect(mockApi.PreviewMarkdown).toHaveBeenCalledTimes(2);

        latestResponse.resolve('<p>latest response</p>');
        await Promise.resolve();
        await Promise.resolve();
        expect(useDocStore.getState().previewHtml).toContain('latest response');

        firstResponse.resolve('<p>first response</p>');
        await Promise.resolve();
        await Promise.resolve();
        expect(useDocStore.getState().previewHtml).toContain('latest response');
        expect(useDocStore.getState().previewHtml).not.toContain('first response');
    });

    it('writes each preview commit once without redrawing stale HTML during editor updates', async () => {
        vi.useFakeTimers();
        editorLayout.init();

        const preview = document.getElementById('preview') as HTMLIFrameElement;
        const previewDocument = preview.contentDocument!;
        const openSpy = vi.spyOn(previewDocument, 'open');
        const writeSpy = vi.spyOn(previewDocument, 'write');
        const closeSpy = vi.spyOn(previewDocument, 'close');

        useDocStore.getState().setPreviewHtml('<p>old preview</p>');
        expect(openSpy).toHaveBeenCalledTimes(1);
        expect(writeSpy).toHaveBeenCalledTimes(1);
        expect(closeSpy).toHaveBeenCalledTimes(1);

        const editor = document.getElementById('editor') as HTMLTextAreaElement;
        editor.value = 'new markdown';
        editor.dispatchEvent(new Event('input', { bubbles: true }));

        expect(writeSpy).toHaveBeenCalledTimes(1);

        await vi.advanceTimersByTimeAsync(200);

        expect(mockApi.PreviewMarkdown).toHaveBeenCalledTimes(1);
        expect(openSpy).toHaveBeenCalledTimes(2);
        expect(writeSpy).toHaveBeenCalledTimes(2);
        expect(closeSpy).toHaveBeenCalledTimes(2);
        expect(writeSpy).toHaveBeenLastCalledWith(expect.stringContaining('<p>Preview</p>'));
    });

    it('clears the old iframe and preview session before exposing board source in Editor', () => {
        editorLayout.init();
        const oldPreview = createMarpPreview(['old-one', 'old-two']);
        useDocStore.getState().setPreviewHtml(oldPreview);
        const preview = document.getElementById('preview') as HTMLIFrameElement;
        const controls = document.getElementById('previewPageControls')!;
        expect(preview.contentDocument?.body.textContent).toContain('old-one');
        expect(controls.hidden).toBe(false);

        const board = createBoardDocument();
        const transition = beginDocumentTransition(board.path);
        expect(commitBoardDocumentTransition(transition, board)).toBe(true);

        expect(preview.contentDocument?.body.textContent).toBe('');
        expect(controls.hidden).toBe(true);
        expect(useDocStore.getState()).toMatchObject({
            currentPath: board.path,
            markdownContent: board.rawContent,
            previewHtml: '',
        });
        expect((document.getElementById('editor') as HTMLTextAreaElement).value).toBe(board.rawContent);

        activateDocumentTab('editor');

        expect(useUIStore.getState().activeTab).toBe('editor');
        expect(preview.contentDocument?.body.textContent).toBe('');
    });

    it('does not commit an in-flight editor preview after destroy', async () => {
        vi.useFakeTimers();
        const response = createDeferred<string>();
        mockApi.PreviewMarkdown.mockReturnValue(response.promise);
        editorLayout.init();
        const editor = document.getElementById('editor') as HTMLTextAreaElement;
        editor.value = 'pending preview';
        editor.dispatchEvent(new Event('input', { bubbles: true }));
        await vi.advanceTimersByTimeAsync(200);
        expect(mockApi.PreviewMarkdown).toHaveBeenCalledOnce();

        editorLayout.destroy();
        response.resolve('<p>late preview</p>');
        await Promise.resolve();
        await Promise.resolve();

        expect(useDocStore.getState().previewHtml).toBe('');
    });

    it('keeps the iframe empty from Board until the target Markdown preview commits', () => {
        editorLayout.init();
        const preview = document.getElementById('preview') as HTMLIFrameElement;
        useDocStore.getState().setPreviewHtml('<p>old Markdown preview</p>');
        const board = createBoardDocument();
        const boardTransition = beginDocumentTransition(board.path);
        commitBoardDocumentTransition(boardTransition, board);

        const markdownTransition = beginDocumentTransition('content/target.md');
        expect(commitEditorDocumentTransition(markdownTransition, '# Target')).toBe(true);

        expect(useDocStore.getState()).toMatchObject({
            currentPath: 'content/target.md',
            markdownContent: '# Target',
            previewHtml: '',
        });
        expect(preview.contentDocument?.body.textContent).toBe('');

        expect(commitDocumentPreview(markdownTransition, '<p>target Markdown preview</p>')).toBe(true);
        expect(preview.contentDocument?.body.textContent).toContain('target Markdown preview');
        expect(preview.contentDocument?.body.textContent).not.toContain('old Markdown preview');
    });

    it('updates an existing Markdown preview immediately when the theme changes', () => {
        editorLayout.init();

        const preview = document.getElementById('preview') as HTMLIFrameElement;
        const writeSpy = vi.spyOn(preview.contentDocument!, 'write');
        useDocStore.getState().setPreviewHtml(
            '<!doctype html><html data-theme="light"><head>' +
            '<style id="karte-custom-css">old theme</style></head><body>' +
            '<a href="#" class="timestamp-link" data-timestamp="5">[00:05]</a>' +
            '</body></html>'
        );
        expect(writeSpy).toHaveBeenCalledTimes(1);

        useUIStore.getState().setTheme('dark');

        const themedHtml = useDocStore.getState().previewHtml;
        expect(themedHtml).toContain('data-theme="dark"');
        expect(themedHtml.match(/class="timestamp-link"/g)).toHaveLength(1);
        expect(writeSpy).toHaveBeenCalledTimes(2);
        expect(mockApi.PreviewMarkdown).not.toHaveBeenCalled();
    });

    it('uses the same controls across Marp and finite Markdown while infinite and PDF stay unpaged', () => {
        editorLayout.init();
        const controls = document.getElementById('previewPageControls')!;
        const pageInfo = document.getElementById('previewPageInfo')!;
        const previous = document.getElementById('previewPrevBtn') as HTMLButtonElement;
        const next = document.getElementById('previewNextBtn') as HTMLButtonElement;
        const preview = document.getElementById('preview') as HTMLIFrameElement;

        useDocStore.getState().setPreviewHtml(createMarpPreview(['intro', 'details']));
        expect(controls.hidden).toBe(false);
        expect(pageInfo.textContent).toBe('1 / 2');
        next.click();
        expect(pageInfo.textContent).toBe('2 / 2');
        expect(preview.contentDocument?.querySelector('.slide.active')?.getAttribute('data-slide-id')).toBe('details');

        useDocStore.getState().setPreviewHtml(createFinitePreview(['First', 'Second', 'Third']));
        expect(controls.hidden).toBe(false);
        expect(pageInfo.textContent).toBe('1 / 3');
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true }));
        expect(pageInfo.textContent).toBe('2 / 3');
        previous.click();
        expect(pageInfo.textContent).toBe('1 / 3');

        useDocStore.getState().setPreviewHtml(
            '<!doctype html><html data-printout="infinite"><body><article>Scroll</article></body></html>'
        );
        expect(controls.hidden).toBe(true);
        expect(pageInfo.textContent).toBe('-');

        useDocStore.getState().setCurrentPath('content/document.pdf');
        expect(controls.hidden).toBe(true);

        useDocStore.getState().setCurrentPath('content/other.md');
        useDocStore.getState().setPreviewHtml(createMarpPreview(['other-one', 'other-two']));
        expect(controls.hidden).toBe(false);
        expect(pageInfo.textContent).toBe('1 / 2');
    });
});

function createDeferred<T>(): { promise: Promise<T>; resolve: (value: T) => void } {
    let resolve!: (value: T) => void;
    const promise = new Promise<T>((resolvePromise) => {
        resolve = resolvePromise;
    });
    return { promise, resolve };
}

function createBoardDocument(): BoardDocument {
    return {
        path: 'content/example.board.md',
        title: 'Example board',
        docId: 'board:example',
        type: 'karte-board',
        version: 1,
        created: '2026-08-23',
        updated: '2026-08-23',
        tags: [],
        cards: [],
        edges: [],
        layout: { cards: {}, viewport: { x: 0, y: 0, zoom: 1 } },
        notes: '',
        rawContent: '---\ntype: karte-board\n---',
    };
}

function createMarpPreview(ids: string[]): string {
    return `<!doctype html><html><body>
        <div id="presentation"><div class="slide-container">
            ${ids.map((id, index) => `
                <section class="slide${index === 0 ? ' active' : ''}"
                    data-slide-index="${index}" data-slide-id="${id}">${id}</section>
            `).join('')}
        </div></div>
    </body></html>`;
}

function createFinitePreview(pages: string[]): string {
    return `<!doctype html><html data-printout="A4"><body>
        <div class="karte-print-pages">
            ${pages.map((page) => `<section class="karte-print-page">${page}</section>`).join('')}
        </div>
    </body></html>`;
}
