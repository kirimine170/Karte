import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type {
    PDFDocumentLoadingTask,
    PDFDocumentProxy,
    RenderTask,
} from 'pdfjs-dist/legacy/build/pdf.mjs';
import { EditorLayout } from '../editor-layout';
import { useASRStore, useDocStore, useUIStore } from '../../stores/index';

const mockApi = {
    GetPdfFileURL: vi.fn(async (path: string) => `/pdf/${path}`),
    PreviewMarkdown: vi.fn().mockResolvedValue('<p>Preview</p>'),
    SaveFile: vi.fn().mockResolvedValue(undefined),
} as any;

describe('EditorLayout PDF virtualization', () => {
    let editorLayout: EditorLayout;

    beforeEach(() => {
        vi.clearAllMocks();
        vi.stubGlobal('IntersectionObserver', undefined);
        vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockImplementation(
            () => ({ clearRect: vi.fn() }) as any
        );
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
        document.body.innerHTML = createPdfEditorDom();
        const canvasContainer = document.getElementById('pdfCanvasContainer')!;
        Object.defineProperty(canvasContainer, 'clientWidth', { configurable: true, value: 800 });
        Object.defineProperty(canvasContainer, 'clientHeight', { configurable: true, value: 600 });
        editorLayout = new EditorLayout(mockApi);
    });

    afterEach(() => {
        editorLayout.destroy();
        vi.unstubAllGlobals();
        vi.restoreAllMocks();
    });

    it('renders only a bounded visible window for a 100-page PDF without IntersectionObserver', async () => {
        const pdfDocument = createMockPdfDocument(100);
        installPdfLoader(editorLayout, [pdfDocument.proxy]);
        editorLayout.init();
        useDocStore.getState().setCurrentPath('content/large.pdf');
        await vi.waitFor(() => {
            expect(pdfDocument.getPage).toHaveBeenCalled();
        });

        clickPdfMode('scroll');
        await vi.waitFor(() => {
            expect(document.querySelectorAll('.pdf-scroll-page')).toHaveLength(100);
            expect(document.querySelectorAll('.pdf-scroll-canvas').length).toBeGreaterThan(0);
        });

        expect(document.querySelectorAll('.pdf-scroll-canvas').length).toBeLessThanOrEqual(8);
        expect(pdfDocument.getPage.mock.calls.length).toBeLessThan(12);

        const canvasContainer = document.getElementById('pdfCanvasContainer')!;
        canvasContainer.scrollTop = 50 * 1113;
        canvasContainer.dispatchEvent(new Event('scroll'));

        await vi.waitFor(() => {
            expect(document.querySelector('.pdf-scroll-page[data-page-number="50"] canvas')).not.toBeNull();
        });
        expect(document.querySelectorAll('.pdf-scroll-canvas').length).toBeLessThanOrEqual(8);
        expect(document.querySelector('.pdf-scroll-page[data-page-number="1"] canvas')).toBeNull();
        expect(pdfDocument.getPage.mock.calls.length).toBeLessThan(20);
    });

    it('releases the virtual window across mode, page, PDF, and destroy transitions', async () => {
        const firstDocument = createMockPdfDocument(12);
        const secondDocument = createMockPdfDocument(6);
        installPdfLoader(editorLayout, [firstDocument.proxy, secondDocument.proxy]);
        editorLayout.init();
        useDocStore.getState().setCurrentPath('content/first.pdf');
        await vi.waitFor(() => {
            expect(firstDocument.getPage).toHaveBeenCalled();
        });

        clickPdfMode('scroll');
        await vi.waitFor(() => {
            expect(document.querySelectorAll('.pdf-scroll-canvas').length).toBeGreaterThan(0);
        });
        const virtualCanvases = Array.from(document.querySelectorAll<HTMLCanvasElement>('.pdf-scroll-canvas'));

        clickPdfMode('single');
        await vi.waitFor(() => {
            expect(document.querySelectorAll('.pdf-scroll-page')).toHaveLength(0);
        });
        expect(virtualCanvases.every((canvas) => canvas.width === 0 && canvas.height === 0)).toBe(true);

        const nextButton = document.getElementById('pdfNextBtn') as HTMLButtonElement;
        nextButton.click();
        await vi.waitFor(() => {
            expect(firstDocument.getPage).toHaveBeenCalledWith(2);
        });

        useDocStore.getState().setCurrentPath('content/second.pdf');
        await vi.waitFor(() => {
            expect(firstDocument.cleanup).toHaveBeenCalledOnce();
            expect(firstDocument.destroy).toHaveBeenCalledOnce();
            expect(secondDocument.getPage).toHaveBeenCalled();
        });

        editorLayout.destroy();
        await vi.waitFor(() => {
            expect(secondDocument.cleanup).toHaveBeenCalledOnce();
            expect(secondDocument.destroy).toHaveBeenCalledOnce();
        });
        const fixedCanvases = [
            document.getElementById('pdfCanvasLeft') as HTMLCanvasElement,
            document.getElementById('pdfCanvasRight') as HTMLCanvasElement,
        ];
        expect(fixedCanvases.every((canvas) => canvas.width === 0 && canvas.height === 0)).toBe(true);
    });
});

function installPdfLoader(editorLayout: EditorLayout, documents: PDFDocumentProxy[]): void {
    const loadingTasks = documents.map((document) => createLoadingTask(document));
    const loader = vi.fn(() => {
        const loadingTask = loadingTasks.shift();
        if (!loadingTask) {
            throw new Error('Unexpected PDF load');
        }
        return loadingTask;
    });
    (editorLayout as unknown as { createPdfLoadingTask: () => PDFDocumentLoadingTask }).createPdfLoadingTask = loader;
}

function createMockPdfDocument(numPages: number) {
    const cleanup = vi.fn().mockResolvedValue(undefined);
    const destroy = vi.fn().mockResolvedValue(undefined);
    const getPage = vi.fn(async () => ({
        getViewport: ({ scale }: { scale: number }) => ({ width: 600 * scale, height: 800 * scale }),
        render: () => {
            const renderTask = {
                promise: Promise.resolve(),
                cancel: vi.fn(),
            } as unknown as RenderTask;
            return renderTask;
        },
    }));
    return {
        cleanup,
        destroy,
        getPage,
        proxy: { numPages, cleanup, destroy, getPage } as unknown as PDFDocumentProxy,
    };
}

function createLoadingTask(document: PDFDocumentProxy): PDFDocumentLoadingTask {
    return {
        promise: Promise.resolve(document),
        destroy: vi.fn().mockResolvedValue(undefined),
    } as unknown as PDFDocumentLoadingTask;
}

function clickPdfMode(mode: 'single' | 'scroll'): void {
    const button = document.querySelector<HTMLButtonElement>(`#pdfViewModeMenu button[data-value="${mode}"]`);
    button?.click();
}

function createPdfEditorDom(): string {
    return `
        <div id="contentArea">
            <div class="tabs"></div>
            <textarea id="editor"></textarea>
            <div class="preview-pane-body"><iframe id="preview"></iframe></div>
            <div id="pdfPane" style="display: none">
                <button id="pdfPrevBtn"></button>
                <button id="pdfNextBtn"></button>
                <div class="pdf-select">
                    <button id="pdfViewModeBtn"></button>
                    <div id="pdfViewModeMenu">
                        <button data-value="single"></button>
                        <button data-value="spread"></button>
                        <button data-value="scroll"></button>
                    </div>
                </div>
                <input id="pdfCoverToggle" type="checkbox" checked />
                <button id="pdfBindingBtn"></button>
                <div id="pdfBindingMenu"></div>
                <div id="pdfCanvasContainer">
                    <canvas id="pdfCanvasLeft"></canvas>
                    <canvas id="pdfCanvasRight"></canvas>
                    <div id="pdfScrollContainer"></div>
                    <div id="pdfEmpty"></div>
                </div>
                <div id="pdfPageInfo"></div>
            </div>
            <div id="galleryArea"></div>
            <div id="imageGalleryContainer"></div>
            <div id="csvGalleryContainer"></div>
        </div>
    `;
}
