import { describe, expect, it, vi } from 'vitest';
import type {
    PDFDocumentLoadingTask,
    PDFDocumentProxy,
    RenderTask,
} from 'pdfjs-dist/legacy/build/pdf.mjs';
import { PdfLifecycleManager } from '../pdf-lifecycle';

describe('PdfLifecycleManager', () => {
    it('cancels an old loading task and disposes a stale document during a switch race', async () => {
        const manager = new PdfLifecycleManager();
        const firstDocument = createMockDocument();
        const secondDocument = createMockDocument();
        const firstResult = createDeferred<PDFDocumentProxy>();
        const firstLoadingTask = createLoadingTask(firstResult.promise);

        const firstGeneration = manager.beginDocumentTransition();
        const firstLoad = manager.loadDocument(firstGeneration, () => firstLoadingTask.task);
        await vi.waitFor(() => {
            expect(manager.hasLoadingTask).toBe(true);
        });

        const secondGeneration = manager.beginDocumentTransition();
        expect(firstLoadingTask.destroy).toHaveBeenCalledOnce();
        const secondLoadingTask = createLoadingTask(Promise.resolve(secondDocument.proxy));
        const secondLoad = manager.loadDocument(secondGeneration, () => secondLoadingTask.task);

        firstResult.resolve(firstDocument.proxy);

        await expect(firstLoad).resolves.toBeNull();
        await expect(secondLoad).resolves.toBe(secondDocument.proxy);
        expect(firstDocument.cleanup).toHaveBeenCalledOnce();
        expect(firstDocument.destroy).toHaveBeenCalledOnce();
        expect(manager.currentDocument).toBe(secondDocument.proxy);

        await manager.destroy();
    });

    it('cancels render tasks and releases canvases before document cleanup on destroy', async () => {
        const manager = new PdfLifecycleManager();
        const document = createMockDocument();
        const generation = manager.beginDocumentTransition();
        await manager.loadDocument(generation, () => createLoadingTask(Promise.resolve(document.proxy)).task);

        const canvas = documentElementCanvas(640, 480);
        manager.trackCanvas(canvas);
        const render = createPendingRenderTask();
        manager.trackRenderTask(render.task);

        const destruction = manager.destroy();

        expect(render.cancel).toHaveBeenCalledOnce();
        expect(canvas.width).toBe(0);
        expect(canvas.height).toBe(0);

        await destruction;
        expect(document.cleanup).toHaveBeenCalledOnce();
        expect(document.destroy).toHaveBeenCalledOnce();
        expect(manager.retainedDocumentCount).toBe(0);
        expect(manager.retainedCanvasCount).toBe(0);
    });

    it('cancels only the render task owned by an evicted canvas', async () => {
        const manager = new PdfLifecycleManager();
        const firstCanvas = documentElementCanvas(640, 480);
        const secondCanvas = documentElementCanvas(640, 480);
        const firstRender = createPendingRenderTask();
        const secondRender = createPendingRenderTask();
        manager.trackCanvas(firstCanvas);
        manager.trackCanvas(secondCanvas);
        manager.trackRenderTask(firstRender.task, firstCanvas);
        manager.trackRenderTask(secondRender.task, secondCanvas);

        manager.releaseCanvas(firstCanvas);

        expect(firstRender.cancel).toHaveBeenCalledOnce();
        expect(secondRender.cancel).not.toHaveBeenCalled();
        expect(firstCanvas.width).toBe(0);
        expect(firstCanvas.height).toBe(0);
        expect(manager.retainedCanvasCount).toBe(1);

        await manager.destroy();
        expect(secondRender.cancel).toHaveBeenCalledOnce();
    });

    it('destroys a failed loading task and still destroys a document when cleanup fails', async () => {
        const manager = new PdfLifecycleManager();
        const loadFailure = createDeferred<PDFDocumentProxy>();
        const failedLoadingTask = createLoadingTask(loadFailure.promise);
        const failedGeneration = manager.beginDocumentTransition();
        const failedLoad = manager.loadDocument(failedGeneration, () => failedLoadingTask.task);
        await vi.waitFor(() => {
            expect(manager.hasLoadingTask).toBe(true);
        });

        loadFailure.reject(new Error('load failed'));

        await expect(failedLoad).rejects.toThrow('load failed');
        expect(failedLoadingTask.destroy).toHaveBeenCalledOnce();

        const document = createMockDocument();
        document.cleanup.mockRejectedValueOnce(new Error('cleanup failed'));
        const recoveryGeneration = manager.beginDocumentTransition();
        await manager.loadDocument(
            recoveryGeneration,
            () => createLoadingTask(Promise.resolve(document.proxy)).task
        );

        await manager.destroy();
        expect(document.cleanup).toHaveBeenCalledOnce();
        expect(document.destroy).toHaveBeenCalledOnce();
    });

    it('keeps retained documents and canvases bounded across twenty switches', async () => {
        const manager = new PdfLifecycleManager();
        const documents: ReturnType<typeof createMockDocument>[] = [];
        const canvases: HTMLCanvasElement[] = [];

        for (let index = 0; index < 20; index += 1) {
            const generation = manager.beginDocumentTransition();
            const document = createMockDocument();
            documents.push(document);
            await manager.loadDocument(
                generation,
                () => createLoadingTask(Promise.resolve(document.proxy)).task
            );

            const canvas = documentElementCanvas(320 + index, 240 + index);
            canvases.push(canvas);
            manager.trackCanvas(canvas);

            expect(manager.retainedDocumentCount).toBe(1);
            expect(manager.retainedCanvasCount).toBe(1);
            if (index > 0) {
                expect(documents[index - 1]?.cleanup).toHaveBeenCalledOnce();
                expect(documents[index - 1]?.destroy).toHaveBeenCalledOnce();
                expect(canvases[index - 1]?.width).toBe(0);
                expect(canvases[index - 1]?.height).toBe(0);
            }
        }

        expect(documents.slice(0, -1).every((document) => document.destroy.mock.calls.length === 1)).toBe(true);
        await manager.destroy();
        expect(documents.every((document) => document.cleanup.mock.calls.length === 1)).toBe(true);
        expect(documents.every((document) => document.destroy.mock.calls.length === 1)).toBe(true);
        expect(canvases.every((canvas) => canvas.width === 0 && canvas.height === 0)).toBe(true);
    });
});

function createMockDocument() {
    const cleanup = vi.fn().mockResolvedValue(undefined);
    const destroy = vi.fn().mockResolvedValue(undefined);
    return {
        cleanup,
        destroy,
        proxy: { cleanup, destroy } as unknown as PDFDocumentProxy,
    };
}

function createLoadingTask(promise: Promise<PDFDocumentProxy>) {
    const destroy = vi.fn().mockResolvedValue(undefined);
    return {
        destroy,
        task: { promise, destroy } as unknown as PDFDocumentLoadingTask,
    };
}

function createPendingRenderTask() {
    const result = createDeferred<void>();
    const cancel = vi.fn(() => {
        const cancellation = new Error('render cancelled');
        cancellation.name = 'RenderingCancelledException';
        result.reject(cancellation);
    });
    return {
        cancel,
        task: { promise: result.promise, cancel } as unknown as RenderTask,
    };
}

function documentElementCanvas(width: number, height: number): HTMLCanvasElement {
    const canvas = document.createElement('canvas');
    canvas.width = width;
    canvas.height = height;
    return canvas;
}

function createDeferred<T>(): {
    promise: Promise<T>;
    resolve: (value: T) => void;
    reject: (reason?: unknown) => void;
} {
    let resolve!: (value: T) => void;
    let reject!: (reason?: unknown) => void;
    const promise = new Promise<T>((resolvePromise, rejectPromise) => {
        resolve = resolvePromise;
        reject = rejectPromise;
    });
    return { promise, resolve, reject };
}
