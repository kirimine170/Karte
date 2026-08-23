import type {
    PDFDocumentLoadingTask,
    PDFDocumentProxy,
    RenderTask,
} from 'pdfjs-dist/legacy/build/pdf.mjs';

type LoadingTaskFactory = () => PDFDocumentLoadingTask;

export class PdfLifecycleManager {
    private documentGeneration = 0;
    private loadingTask: PDFDocumentLoadingTask | null = null;
    private document: PDFDocumentProxy | null = null;
    private renderTasks = new Set<RenderTask>();
    private renderTaskCanvases = new Map<RenderTask, HTMLCanvasElement>();
    private canvases = new Set<HTMLCanvasElement>();
    private renderDrain: Promise<void> = Promise.resolve();
    private documentDisposal: Promise<void> = Promise.resolve();

    get currentDocument(): PDFDocumentProxy | null {
        return this.document;
    }

    get retainedDocumentCount(): number {
        return this.document ? 1 : 0;
    }

    get retainedCanvasCount(): number {
        return this.canvases.size;
    }

    get hasLoadingTask(): boolean {
        return this.loadingTask !== null;
    }

    beginDocumentTransition(): number {
        const generation = ++this.documentGeneration;
        this.queueCurrentResourcesForDisposal();
        return generation;
    }

    isCurrentGeneration(generation: number): boolean {
        return generation === this.documentGeneration;
    }

    async loadDocument(generation: number, createLoadingTask: LoadingTaskFactory): Promise<PDFDocumentProxy | null> {
        await this.documentDisposal;
        if (!this.isCurrentGeneration(generation)) {
            return null;
        }

        let loadingTask: PDFDocumentLoadingTask;
        try {
            loadingTask = createLoadingTask();
        } catch (error) {
            if (this.isCurrentGeneration(generation)) {
                throw error;
            }
            return null;
        }

        if (!this.isCurrentGeneration(generation)) {
            await callSafely(() => loadingTask.destroy());
            return null;
        }
        this.loadingTask = loadingTask;

        try {
            const document = await loadingTask.promise;
            if (this.loadingTask === loadingTask) {
                this.loadingTask = null;
            }
            if (!this.isCurrentGeneration(generation)) {
                await disposeDocument(document);
                return null;
            }
            this.document = document;
            return document;
        } catch (error) {
            const ownsLoadingTask = this.loadingTask === loadingTask;
            if (ownsLoadingTask) {
                this.loadingTask = null;
                await callSafely(() => loadingTask.destroy());
            }
            if (this.isCurrentGeneration(generation)) {
                throw error;
            }
            return null;
        }
    }

    async beginRenderCycle(): Promise<void> {
        const canvases = this.takeCanvases();
        const renderDrain = this.cancelTrackedRenderTasks();
        releaseCanvases(canvases);
        await renderDrain;
    }

    trackCanvas(canvas: HTMLCanvasElement): void {
        this.canvases.add(canvas);
    }

    releaseCanvas(canvas: HTMLCanvasElement): void {
        this.canvases.delete(canvas);
        const canvasRenderTasks = Array.from(this.renderTasks).filter(
            (renderTask) => this.renderTaskCanvases.get(renderTask) === canvas
        );
        this.cancelRenderTasks(canvasRenderTasks);
        releaseCanvases([canvas]);
    }

    trackRenderTask(renderTask: RenderTask, canvas?: HTMLCanvasElement): void {
        this.renderTasks.add(renderTask);
        if (canvas) {
            this.renderTaskCanvases.set(renderTask, canvas);
        }
        void renderTask.promise.then(
            () => this.forgetRenderTask(renderTask),
            () => this.forgetRenderTask(renderTask)
        );
    }

    async destroy(): Promise<void> {
        this.beginDocumentTransition();
        await this.documentDisposal;
    }

    private queueCurrentResourcesForDisposal(): void {
        const loadingTask = this.loadingTask;
        const document = this.document;
        const canvases = this.takeCanvases();
        this.loadingTask = null;
        this.document = null;

        const renderDrain = this.cancelTrackedRenderTasks();
        const loadingTaskDisposal = loadingTask
            ? callSafely(() => loadingTask.destroy())
            : Promise.resolve();
        releaseCanvases(canvases);

        const previousDisposal = this.documentDisposal;
        this.documentDisposal = (async () => {
            await previousDisposal;
            await Promise.all([renderDrain, loadingTaskDisposal]);
            if (document) {
                await disposeDocument(document);
            }
        })();
    }

    private cancelTrackedRenderTasks(): Promise<void> {
        const renderTasks = Array.from(this.renderTasks);
        return this.cancelRenderTasks(renderTasks);
    }

    private cancelRenderTasks(renderTasks: RenderTask[]): Promise<void> {
        renderTasks.forEach((renderTask) => {
            this.renderTasks.delete(renderTask);
            this.renderTaskCanvases.delete(renderTask);
            try {
                renderTask.cancel();
            } catch {
                // taskがすでに完了または破棄されている場合は無視する．
            }
        });

        const previousDrain = this.renderDrain;
        const currentDrain = Promise.all([
            previousDrain,
            Promise.allSettled(renderTasks.map((renderTask) => renderTask.promise)),
        ]).then(() => undefined);
        this.renderDrain = currentDrain;
        return currentDrain;
    }

    private forgetRenderTask(renderTask: RenderTask): void {
        this.renderTasks.delete(renderTask);
        this.renderTaskCanvases.delete(renderTask);
    }

    private takeCanvases(): HTMLCanvasElement[] {
        const canvases = Array.from(this.canvases);
        this.canvases.clear();
        return canvases;
    }
}

async function disposeDocument(document: PDFDocumentProxy): Promise<void> {
    await callSafely(() => document.cleanup());
    await callSafely(() => document.destroy());
}

async function callSafely(action: () => void | Promise<unknown>): Promise<void> {
    try {
        await action();
    } catch {
        // 個別の解放失敗後も，残りの解放処理を継続する．
    }
}

function releaseCanvases(canvases: HTMLCanvasElement[]): void {
    canvases.forEach((canvas) => {
        canvas.width = 0;
        canvas.height = 0;
    });
}
