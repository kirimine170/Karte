export type PreviewSessionKind = 'inactive' | 'marp' | 'finite-markdown' | 'infinite';

export interface PreviewSessionState {
    kind: PreviewSessionKind;
    paged: boolean;
    currentPage: number;
    pageCount: number;
    canGoPrevious: boolean;
    canGoNext: boolean;
}

type PagedPreviewKind = 'marp' | 'finite-markdown';

interface DetectedPages {
    kind: PagedPreviewKind | 'infinite';
    pages: HTMLElement[];
}

interface PageCursor {
    kind: PagedPreviewKind | 'infinite';
    index: number;
    id: string | null;
}

type PreviewSessionStateListener = (state: PreviewSessionState) => void;

const INACTIVE_STATE: PreviewSessionState = {
    kind: 'inactive',
    paged: false,
    currentPage: 0,
    pageCount: 0,
    canGoPrevious: false,
    canGoNext: false,
};

/**
 * iframe内のrenderer固有DOMをPageAdapterへ閉じ込め，親側へ共通のページ状態だけを公開する．
 * T-079のrenderer contract導入前に使う暫定adapterである．
 */
export class PreviewSession {
    private readonly iframe: HTMLIFrameElement;
    private readonly onStateChange: PreviewSessionStateListener;
    private readonly iframeLoadHandler: () => void;
    private active = false;
    private destroyed = false;
    private sessionKey = '';
    private cursor: PageCursor = { kind: 'infinite', index: 0, id: null };
    private adapter: PageAdapter | null = null;
    private observedDocument: Document | null = null;
    private observer: MutationObserver | null = null;
    private reconcileScheduled = false;
    private state: PreviewSessionState = INACTIVE_STATE;

    constructor(iframe: HTMLIFrameElement, onStateChange: PreviewSessionStateListener) {
        this.iframe = iframe;
        this.onStateChange = onStateChange;
        this.iframeLoadHandler = () => {
            if (this.active && !this.destroyed) {
                this.bindCurrentDocument();
            }
        };
        this.iframe.addEventListener('load', this.iframeLoadHandler);
    }

    get currentState(): PreviewSessionState {
        return this.state;
    }

    get isPaged(): boolean {
        return this.state.paged;
    }

    rendered(sessionKey: string): void {
        if (this.destroyed) {
            return;
        }
        if (sessionKey !== this.sessionKey) {
            this.cursor = { kind: 'infinite', index: 0, id: null };
        }
        this.sessionKey = sessionKey;
        this.active = true;
        this.bindCurrentDocument();
    }

    suspend(): void {
        if (this.destroyed) {
            return;
        }
        this.active = false;
        this.detachDocument();
        this.adapter = null;
        this.emitState(INACTIVE_STATE);
    }

    previous(): boolean {
        return this.goTo(this.cursor.index - 1);
    }

    next(): boolean {
        return this.goTo(this.cursor.index + 1);
    }

    destroy(): void {
        if (this.destroyed) {
            return;
        }
        this.destroyed = true;
        this.active = false;
        this.detachDocument();
        this.adapter = null;
        this.iframe.removeEventListener('load', this.iframeLoadHandler);
        this.emitState(INACTIVE_STATE);
    }

    private bindCurrentDocument(): void {
        this.detachDocument();
        const document = this.iframe.contentDocument || this.iframe.contentWindow?.document;
        if (!document) {
            this.adapter = null;
            this.emitState(INACTIVE_STATE);
            return;
        }

        this.observedDocument = document;
        document.addEventListener('keydown', this.handleDocumentKeydown, true);
        this.reconcileAdapter();

        if (typeof MutationObserver !== 'undefined') {
            this.observer = new MutationObserver(() => this.scheduleReconcile());
            const observationRoot = document.documentElement || document;
            this.observer.observe(observationRoot, {
                childList: true,
                subtree: true,
                attributes: true,
                attributeFilter: ['class', 'data-printout'],
            });
        }
    }

    private detachDocument(): void {
        this.observer?.disconnect();
        this.observer = null;
        if (this.observedDocument) {
            this.observedDocument.removeEventListener('keydown', this.handleDocumentKeydown, true);
        }
        this.observedDocument = null;
        this.reconcileScheduled = false;
    }

    private readonly handleDocumentKeydown = (event: KeyboardEvent): void => {
        if (
            !this.active ||
            !this.adapter ||
            !this.state.paged ||
            (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') ||
            isEditableEventTarget(event.target)
        ) {
            return;
        }
        event.preventDefault();
        event.stopImmediatePropagation();
        if (event.key === 'ArrowLeft') {
            this.previous();
        } else {
            this.next();
        }
    };

    private scheduleReconcile(): void {
        if (this.reconcileScheduled || !this.active || this.destroyed) {
            return;
        }
        this.reconcileScheduled = true;
        const expectedDocument = this.observedDocument;
        queueMicrotask(() => {
            this.reconcileScheduled = false;
            if (!this.active || this.destroyed || expectedDocument !== this.observedDocument) {
                return;
            }
            this.reconcileAdapter();
        });
    }

    private reconcileAdapter(): void {
        const document = this.observedDocument;
        if (!document || !this.active) {
            return;
        }

        const detected = detectPages(document);
        if (this.adapter?.matches(detected.kind, detected.pages)) {
            this.syncCurrentPageFromAdapter();
            return;
        }

        const shouldPreserveCursor = this.cursor.kind === detected.kind;
        const previousCursor = shouldPreserveCursor
            ? this.cursor
            : { kind: detected.kind, index: 0, id: null };
        this.adapter = detected.pages.length > 0
            ? new PageAdapter(detected.kind as PagedPreviewKind, detected.pages, document.defaultView)
            : null;

        if (!this.adapter) {
            if (!shouldPreserveCursor) {
                this.cursor = previousCursor;
            }
            this.emitState({
                kind: detected.kind,
                paged: false,
                currentPage: 0,
                pageCount: 0,
                canGoPrevious: false,
                canGoNext: false,
            });
            return;
        }

        const idIndex = previousCursor.id ? this.adapter.findPageIndex(previousCursor.id) : -1;
        const targetIndex = idIndex >= 0
            ? idIndex
            : clampPageIndex(previousCursor.index, this.adapter.pageCount);
        this.adapter.show(targetIndex);
        this.setCursorFromAdapter(targetIndex);
        this.emitPagedState();
    }

    private syncCurrentPageFromAdapter(): void {
        if (!this.adapter) {
            return;
        }
        const domIndex = this.adapter.readCurrentIndex();
        if (domIndex >= 0 && domIndex !== this.cursor.index) {
            this.setCursorFromAdapter(domIndex);
        }
        this.emitPagedState();
    }

    private goTo(index: number): boolean {
        if (!this.active || !this.adapter || this.adapter.pageCount === 0) {
            return false;
        }
        const targetIndex = clampPageIndex(index, this.adapter.pageCount);
        if (targetIndex === this.cursor.index) {
            return false;
        }
        this.adapter.show(targetIndex);
        this.setCursorFromAdapter(targetIndex);
        this.emitPagedState();
        return true;
    }

    private setCursorFromAdapter(index: number): void {
        if (!this.adapter) {
            return;
        }
        this.cursor = {
            kind: this.adapter.kind,
            index,
            id: this.adapter.getPageId(index),
        };
    }

    private emitPagedState(): void {
        if (!this.adapter) {
            return;
        }
        const pageCount = this.adapter.pageCount;
        const index = clampPageIndex(this.cursor.index, pageCount);
        this.emitState({
            kind: this.adapter.kind,
            paged: pageCount > 0,
            currentPage: pageCount > 0 ? index + 1 : 0,
            pageCount,
            canGoPrevious: index > 0,
            canGoNext: index + 1 < pageCount,
        });
    }

    private emitState(state: PreviewSessionState): void {
        if (sameState(this.state, state)) {
            return;
        }
        this.state = state;
        this.onStateChange(state);
    }
}

class PageAdapter {
    readonly kind: PagedPreviewKind;
    readonly pages: HTMLElement[];
    private readonly previewWindow: Window | null;

    constructor(kind: PagedPreviewKind, pages: HTMLElement[], previewWindow: Window | null) {
        this.kind = kind;
        this.pages = pages;
        this.previewWindow = previewWindow;
    }

    get pageCount(): number {
        return this.pages.length;
    }

    matches(kind: DetectedPages['kind'], pages: HTMLElement[]): boolean {
        return this.kind === kind &&
            pages.length === this.pages.length &&
            pages.every((page, index) => page === this.pages[index]);
    }

    findPageIndex(id: string): number {
        return this.pages.findIndex((_, index) => this.getPageId(index) === id);
    }

    getPageId(index: number): string | null {
        const page = this.pages[index];
        return page ? readStablePageId(page, this.kind) : null;
    }

    readCurrentIndex(): number {
        if (this.kind !== 'marp') {
            return -1;
        }
        return this.pages.findIndex((page) => page.classList.contains('active'));
    }

    show(index: number): void {
        const page = this.pages[index];
        if (!page) {
            return;
        }
        if (this.kind === 'marp') {
            this.showMarpSlide(index);
            return;
        }
        if (typeof page.scrollIntoView === 'function') {
            page.scrollIntoView({ behavior: 'auto', block: 'start' });
        }
    }

    private showMarpSlide(index: number): void {
        const marpWindow = this.previewWindow as (Window & { showSlide?: (index: number) => void }) | null;
        try {
            marpWindow?.showSlide?.(index);
        } catch {
            // 現rendererのscriptが利用できない場合はDOM操作へfallbackする．
        }
        if (!this.pages[index]?.classList.contains('active')) {
            this.pages.forEach((page, pageIndex) => page.classList.toggle('active', pageIndex === index));
        }
        const document = this.pages[index]?.ownerDocument;
        const current = document?.getElementById('current-slide');
        const total = document?.getElementById('total-slides');
        const previous = document?.getElementById('prev-btn') as HTMLButtonElement | null;
        const next = document?.getElementById('next-btn') as HTMLButtonElement | null;
        if (current) current.textContent = String(index + 1);
        if (total) total.textContent = String(this.pageCount);
        if (previous) previous.disabled = index === 0;
        if (next) next.disabled = index + 1 === this.pageCount;
    }
}

function detectPages(document: Document): DetectedPages {
    const finitePages = Array.from(
        document.querySelectorAll<HTMLElement>('section.karte-print-page')
    );
    if (finitePages.length > 0) {
        return { kind: 'finite-markdown', pages: finitePages };
    }

    const marpPages = Array.from(
        document.querySelectorAll<HTMLElement>(
            '#presentation .slide-container > section.slide, section.slide[data-slide-index]'
        )
    ).filter((page, index, pages) => pages.indexOf(page) === index);
    if (marpPages.length > 0) {
        return { kind: 'marp', pages: marpPages };
    }

    const printoutMode = document.documentElement?.getAttribute('data-printout')?.toLowerCase();
    if (printoutMode && printoutMode !== 'infinite') {
        return { kind: 'finite-markdown', pages: [] };
    }
    return { kind: 'infinite', pages: [] };
}

function readStablePageId(page: HTMLElement, kind: PagedPreviewKind): string | null {
    const attributes = kind === 'marp'
        ? ['data-slide-id', 'data-page-id', 'id']
        : ['data-page-id', 'data-karte-page-id', 'id'];
    for (const attribute of attributes) {
        const value = page.getAttribute(attribute)?.trim();
        if (value) {
            return `${attribute}:${value}`;
        }
    }

    if (kind === 'finite-markdown') {
        const anchor = page.querySelector<HTMLElement>('[data-sourcepos], h1[id], h2[id], h3[id]');
        const sourcePosition = anchor?.getAttribute('data-sourcepos')?.trim();
        if (sourcePosition) {
            return `data-sourcepos:${sourcePosition}`;
        }
        if (anchor?.id) {
            return `anchor:${anchor.id}`;
        }
    }
    return null;
}

function clampPageIndex(index: number, pageCount: number): number {
    if (pageCount <= 0) {
        return 0;
    }
    return Math.min(pageCount - 1, Math.max(0, index));
}

function isEditableEventTarget(target: EventTarget | null): boolean {
    const element = target as HTMLElement | null;
    const tagName = element?.tagName?.toLowerCase();
    return tagName === 'input' ||
        tagName === 'textarea' ||
        tagName === 'select' ||
        element?.isContentEditable === true;
}

function sameState(left: PreviewSessionState, right: PreviewSessionState): boolean {
    return left.kind === right.kind &&
        left.paged === right.paged &&
        left.currentPage === right.currentPage &&
        left.pageCount === right.pageCount &&
        left.canGoPrevious === right.canGoPrevious &&
        left.canGoNext === right.canGoNext;
}
