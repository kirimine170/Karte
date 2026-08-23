import { BaseComponent } from './component-base';
import { useBoardStore, useDocStore, useUIStore } from '../stores/index';
import type { BoardCard, BoardDocument, BoardEdgeRecord, ResourceKind, ResourceSearchItem, WailsAppAPI } from '../types/wails-api';
import { eventLogger } from '../utils/event-logger';
import {
    LEGACY_RESOURCE_SEARCH_MAX_ITEMS,
    RESOURCE_SEARCH_PAGE_LIMIT,
    ResourceSearchClient,
} from '../utils/resource-search';

const RELATIONS = [
    'contains',
    'references',
    'quotes',
    'cites',
    'supports',
    'contradicts',
    'derived_from',
    'summarizes',
    'expands',
    'answers',
    'depends_on',
    'related_to',
] as const;

const RELATION_LABELS: Record<(typeof RELATIONS)[number], string> = {
    references: '参照',
    quotes: '引用',
    cites: '根拠引用',
    contains: '内包',
    depends_on: '依存',
    derived_from: '導出元',
    supports: '支持',
    contradicts: '矛盾',
    related_to: '関連',
    summarizes: '要約',
    expands: '詳細化',
    answers: '回答',
};

const CARD_TYPES = ['resource', 'board', 'quote', 'thought', 'llm-note', 'summary', 'claim', 'question', 'task'] as const;

type BoardMode = 'select' | 'link';

type LinkModeState = {
    fromCardId?: string;
};

type EdgeValidationResult =
    | { kind: 'valid' }
    | { kind: 'duplicate'; edgeId: string }
    | { kind: 'invalid'; reason: 'self' | 'missing-card' | 'relation' };

type EdgeCreationResult =
    | { kind: 'created'; board: BoardDocument; edge: BoardEdgeRecord }
    | Exclude<EdgeValidationResult, { kind: 'valid' }>;

type LinkUndoEntry = {
    boardPath: string;
    edge: BoardEdgeRecord;
};

type DragState = {
    cardId: string;
    boardPath: string;
    startX: number;
    startY: number;
    originX: number;
    originY: number;
    currentX: number;
    currentY: number;
    zoom: number;
    moved: boolean;
    incidentEdges: Array<Pick<BoardEdgeRecord, 'id' | 'from' | 'to'>>;
} | null;

type ViewportDragState = {
    boardPath: string;
    startX: number;
    startY: number;
    originX: number;
    originY: number;
    currentX: number;
    currentY: number;
    zoom: number;
    moved: boolean;
} | null;

type BoardEdgeElements = {
    line: SVGLineElement;
    hitbox: SVGLineElement;
    label: SVGTextElement;
};

type MaterialFilter = 'all' | 'markdown' | 'image' | 'csv';

type TrayMaterial = {
    path: string;
    title: string;
    kind: ResourceKind | 'other';
    description: string;
    cardType: 'resource' | 'board';
};

export class BoardView extends BaseComponent {
    private unsubscribe: (() => void)[] = [];
    private api: WailsAppAPI;
    private saveTimer: number | null = null;
    private dragState: DragState = null;
    private viewportDragState: ViewportDragState = null;
    private pointerAnimationFrame: number | null = null;
    private destroyed = false;
    private boardSceneElement: HTMLElement | null = null;
    private readonly cardElements = new Map<string, HTMLElement>();
    private readonly edgeElements = new Map<string, BoardEdgeElements>();
    private latestPersistRequestId = 0;
    private trayCollapsed = false;
    private trayFilter: MaterialFilter = 'all';
    private trayResources: ResourceSearchItem[] = [];
    private trayMaterials: TrayMaterial[] = [];
    private resourceSearch: ResourceSearchClient;
    private traySearchRequestId = 0;
    private traySearchPage = 0;
    private traySearchHasMore = false;
    private traySearchLoading = false;
    private draggedMaterialPath: string | null = null;
    private dragHoverCardId: string | null = null;
    private boardMode: BoardMode = 'select';
    private linkState: LinkModeState = {};
    private linkRelation: (typeof RELATIONS)[number] = 'related_to';
    private linkContinuous = false;
    private linkAnnouncement = '';
    private linkUndoStack: LinkUndoEntry[] = [];
    private pendingFocusKey: string | null = null;

    constructor(api: WailsAppAPI, parent?: HTMLElement) {
        super(parent);
        this.api = api;
        this.resourceSearch = new ResourceSearchClient(api);
    }

    init(): void {
        eventLogger.log('BoardView', 'init');
        this.destroyed = false;
        const boardTab = document.getElementById('board-tab');
        if (!boardTab) {
            return;
        }
        this.element = boardTab as HTMLElement;
        this.bindEvents();
        this.subscribeToStores();
        const currentBoard = useBoardStore.getState().board;
        if (currentBoard?.path) {
            void this.loadTrayMaterials(currentBoard.path);
        }
        this.render();
    }

    private bindEvents(): void {
        if (!this.element) {
            return;
        }

        this.unsubscribe.push(
            this.addEventListener(this.element, 'click', (event) => {
                const target = event.target as HTMLElement;
                const actionEl = target.closest<HTMLElement>('[data-action]');
                if (actionEl) {
                    void this.handleAction(actionEl.dataset.action || '', actionEl);
                    return;
                }

                const cardEl = target.closest<HTMLElement>('.board-card');
                if (cardEl?.dataset.cardId) {
                    this.pendingFocusKey = this.cardFocusKey(cardEl.dataset.cardId);
                    this.activateBoardCard(cardEl.dataset.cardId);
                    return;
                }

                const edgeEl = target.closest<SVGElement>('.board-edge-hitbox');
                if (edgeEl?.dataset.edgeId) {
                    useBoardStore.setState({
                        selectedEdgeId: edgeEl.dataset.edgeId,
                        selectedCardId: null,
                    });
                }
            })
        );

        this.unsubscribe.push(
            this.addEventListener(this.element, 'input', (event) => {
                const target = event.target as HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement;
                if (!target.name) {
                    return;
                }
                if (target.name === 'boardTraySearch') {
                    useDocStore.getState().setSearchQuery(target.value);
                    eventLogger.log('BoardView', 'tray-search-input', {
                        query: target.value,
                        filter: this.trayFilter,
                    });
                    return;
                }
                if (target.closest('.board-card-editor')) {
                    this.updateSelectedCardField(target.name, target.value);
                    return;
                }
                if (target.closest('.board-edge-editor')) {
                    this.updateSelectedEdgeField(target.name, target.value);
                    return;
                }
                if (target.name === 'linkRelation') {
                    if (!this.isLinkRelation(target.value)) {
                        return;
                    }
                    this.linkRelation = target.value;
                    this.linkAnnouncement = '';
                    this.render();
                }
            })
        );

        this.unsubscribe.push(
            this.addEventListener(this.element, 'change', (event) => {
                const target = event.target as HTMLInputElement | HTMLSelectElement;
                if (target.name === 'zoom') {
                    this.updateViewportZoom(Number(target.value));
                    return;
                }
                if (target.name === 'reviewed') {
                    this.updateSelectedCardField('reviewed', target.checked ? 'true' : 'false');
                    return;
                }
                if (target.name === 'linkContinuous') {
                    this.linkContinuous = target.checked;
                    this.render();
                }
            })
        );

        this.unsubscribe.push(
            this.addEventListener(this.element, 'keydown', (event) => {
                const target = event.target as HTMLElement;
                const key = event.key;

                if ((event.metaKey || event.ctrlKey) && !event.shiftKey && key.toLowerCase() === 'z') {
                    if (this.isEditableElement(target) || !this.hasLinkUndoForCurrentBoard()) {
                        return;
                    }
                    event.preventDefault();
                    if (!event.repeat) {
                        this.undoLastCreatedLink();
                    }
                    return;
                }

                if (key === 'Escape' && this.boardMode === 'link') {
                    event.preventDefault();
                    if (!event.repeat) {
                        this.cancelLinkStep();
                    }
                    return;
                }

                const activationEl = target.closest<HTMLElement>('[data-board-card-activation]');
                const cardEl = activationEl?.closest<HTMLElement>('.board-card');
                if (!activationEl || !cardEl?.dataset.cardId || target !== activationEl) {
                    return;
                }
                if (key === 'Enter' || key === ' ') {
                    event.preventDefault();
                    if (!event.repeat) {
                        this.pendingFocusKey = this.cardFocusKey(cardEl.dataset.cardId);
                        this.activateBoardCard(cardEl.dataset.cardId);
                    }
                    return;
                }
                if (key === 'ArrowLeft' || key === 'ArrowRight' || key === 'ArrowUp' || key === 'ArrowDown') {
                    event.preventDefault();
                    this.focusAdjacentCard(cardEl.dataset.cardId, key);
                }
            })
        );

        this.unsubscribe.push(
            this.addEventListener(this.element, 'pointerdown', (event) => {
                const target = event.target as HTMLElement;
                const cardEl = target.closest<HTMLElement>('.board-card');
                if (!cardEl?.dataset.cardId) {
                    if (
                        target.closest('.board-toolbar') ||
                        target.closest('.board-inspector') ||
                        target.closest('.board-edge-hitbox') ||
                        target.closest('.board-edge-label') ||
                        !target.closest('.board-canvas-wrapper')
                    ) {
                        return;
                    }
                    const board = useBoardStore.getState().board;
                    if (!board) {
                        return;
                    }
                    this.viewportDragState = {
                        boardPath: board.path,
                        startX: event.clientX,
                        startY: event.clientY,
                        originX: board.layout.viewport.x || 0,
                        originY: board.layout.viewport.y || 0,
                        currentX: board.layout.viewport.x || 0,
                        currentY: board.layout.viewport.y || 0,
                        zoom: board.layout.viewport.zoom || 1,
                        moved: false,
                    };
                    return;
                }
                if (this.boardMode !== 'select') {
                    return;
                }
                if (target.closest('.board-card-actions')) {
                    return;
                }
                const board = useBoardStore.getState().board;
                const layout = board?.layout.cards[cardEl.dataset.cardId];
                if (!board || !layout) {
                    return;
                }
                this.dragState = {
                    cardId: cardEl.dataset.cardId,
                    boardPath: board.path,
                    startX: event.clientX,
                    startY: event.clientY,
                    originX: layout.x,
                    originY: layout.y,
                    currentX: layout.x,
                    currentY: layout.y,
                    zoom: board.layout.viewport.zoom || 1,
                    moved: false,
                    incidentEdges: board.edges
                        .filter((edge) => edge.from === cardEl.dataset.cardId || edge.to === cardEl.dataset.cardId)
                        .map((edge) => ({ id: edge.id, from: edge.from, to: edge.to })),
                };
                useBoardStore.getState().setSelectedCardId(cardEl.dataset.cardId);
            })
        );

        this.unsubscribe.push(
            this.addEventListener(this.element, 'dragstart', (event) => {
                const target = event.target as HTMLElement;
                const materialEl = target.closest<HTMLElement>('.board-tray-item');
                if (!materialEl?.dataset.materialPath || !event.dataTransfer) {
                    return;
                }
                this.draggedMaterialPath = materialEl.dataset.materialPath;
                this.dragHoverCardId = null;
                event.dataTransfer.effectAllowed = 'copy';
                event.dataTransfer.setData('text/plain', materialEl.dataset.materialPath);
            })
        );

        this.unsubscribe.push(
            this.addEventListener(this.element, 'dragend', () => {
                this.draggedMaterialPath = null;
                this.dragHoverCardId = null;
                this.render();
            })
        );

        const moveHandler = (event: PointerEvent) => {
            if (!this.dragState) {
                if (!this.viewportDragState) {
                    return;
                }
                const dx = event.clientX - this.viewportDragState.startX;
                const dy = event.clientY - this.viewportDragState.startY;
                const nextX = Math.round(this.viewportDragState.originX + dx);
                const nextY = Math.round(this.viewportDragState.originY + dy);
                if (nextX === this.viewportDragState.currentX && nextY === this.viewportDragState.currentY) {
                    return;
                }
                this.viewportDragState.currentX = nextX;
                this.viewportDragState.currentY = nextY;
                this.viewportDragState.moved = nextX !== this.viewportDragState.originX || nextY !== this.viewportDragState.originY;
                this.schedulePointerFrame();
                return;
            }
            const dx = event.clientX - this.dragState.startX;
            const dy = event.clientY - this.dragState.startY;
            const nextX = Math.round(this.dragState.originX + dx / this.dragState.zoom);
            const nextY = Math.round(this.dragState.originY + dy / this.dragState.zoom);
            if (nextX === this.dragState.currentX && nextY === this.dragState.currentY) {
                return;
            }
            this.dragState.currentX = nextX;
            this.dragState.currentY = nextY;
            this.dragState.moved = nextX !== this.dragState.originX || nextY !== this.dragState.originY;
            this.schedulePointerFrame();
        };

        const upHandler = () => {
            if (!this.dragState) {
                if (!this.viewportDragState) {
                    return;
                }
                this.flushPointerFrame();
                const completedDrag = this.viewportDragState;
                this.viewportDragState = null;
                this.commitViewportDrag(completedDrag);
                this.scheduleSave();
                return;
            }
            this.flushPointerFrame();
            const completedDrag = this.dragState;
            this.dragState = null;
            this.commitCardDrag(completedDrag);
            this.scheduleSave();
        };

        window.addEventListener('pointermove', moveHandler);
        window.addEventListener('pointerup', upHandler);
        this.unsubscribe.push(() => window.removeEventListener('pointermove', moveHandler));
        this.unsubscribe.push(() => window.removeEventListener('pointerup', upHandler));

        this.unsubscribe.push(
            this.addEventListener(this.element, 'wheel', (event) => {
                const target = event.target as HTMLElement;
                if (!target.closest('.board-canvas-wrapper')) {
                    return;
                }
                event.preventDefault();
                const board = useBoardStore.getState().board;
                const wrapper = this.element?.querySelector<HTMLElement>('.board-canvas-wrapper');
                if (!board || !wrapper) {
                    return;
                }
                const rect = wrapper.getBoundingClientRect();
                const currentZoom = board.layout.viewport.zoom || 1;
                const wheelFactor = event.ctrlKey ? 0.0035 : 0.0022;
                const scaleFactor = Math.exp(-event.deltaY * wheelFactor);
                const nextZoom = clamp(currentZoom * scaleFactor, 0.35, 3);
                const pointerX = event.clientX - rect.left;
                const pointerY = event.clientY - rect.top;
                const worldX = (pointerX - (board.layout.viewport.x || 0)) / currentZoom;
                const worldY = (pointerY - (board.layout.viewport.y || 0)) / currentZoom;

                const nextBoard = structuredClone(board) as BoardDocument;
                nextBoard.layout.viewport.zoom = nextZoom;
                nextBoard.layout.viewport.x = Math.round(pointerX - worldX * nextZoom);
                nextBoard.layout.viewport.y = Math.round(pointerY - worldY * nextZoom);
                useBoardStore.getState().setBoard(nextBoard);
                this.scheduleSave();
            }, { passive: false })
        );

        this.unsubscribe.push(
            this.addEventListener(this.element, 'dragover', (event) => {
                const target = event.target as HTMLElement;
                if (!target.closest('.board-canvas-wrapper')) {
                    return;
                }
                event.preventDefault();
                const cardEl = target.closest<HTMLElement>('.board-card');
                const nextHoverCardId = cardEl?.dataset.cardId || null;
                if (nextHoverCardId !== this.dragHoverCardId) {
                    this.dragHoverCardId = nextHoverCardId;
                    this.render();
                }
            })
        );

        this.unsubscribe.push(
            this.addEventListener(this.element, 'dragleave', (event) => {
                const target = event.target as HTMLElement;
                if (!target.closest('.board-canvas-wrapper')) {
                    return;
                }
                const relatedTarget = event.relatedTarget as HTMLElement | null;
                if (relatedTarget?.closest('.board-canvas-wrapper')) {
                    return;
                }
                if (this.dragHoverCardId) {
                    this.dragHoverCardId = null;
                    this.render();
                }
            })
        );

        this.unsubscribe.push(
            this.addEventListener(this.element, 'drop', (event) => {
                const target = event.target as HTMLElement;
                if (!target.closest('.board-canvas-wrapper')) {
                    return;
                }
                event.preventDefault();
                void this.handleMaterialDrop(event as DragEvent);
            })
        );
    }

    private schedulePointerFrame(): void {
        if (this.destroyed || this.pointerAnimationFrame !== null) {
            return;
        }
        this.pointerAnimationFrame = window.requestAnimationFrame(() => {
            this.pointerAnimationFrame = null;
            if (!this.destroyed) {
                this.applyPointerFrame();
            }
        });
    }

    private flushPointerFrame(): void {
        if (this.pointerAnimationFrame === null) {
            return;
        }
        window.cancelAnimationFrame(this.pointerAnimationFrame);
        this.pointerAnimationFrame = null;
        if (!this.destroyed) {
            this.applyPointerFrame();
        }
    }

    private cancelPointerFrame(): void {
        if (this.pointerAnimationFrame !== null) {
            window.cancelAnimationFrame(this.pointerAnimationFrame);
            this.pointerAnimationFrame = null;
        }
    }

    private applyPointerFrame(): void {
        const board = useBoardStore.getState().board;
        if (!board) {
            return;
        }
        if (this.dragState) {
            if (board.path !== this.dragState.boardPath) {
                return;
            }
            this.updateDraggedCardDOM(board, this.dragState);
            return;
        }
        if (this.viewportDragState && board.path === this.viewportDragState.boardPath) {
            this.updateViewportDOM(this.viewportDragState);
        }
    }

    private updateViewportDOM(drag: NonNullable<ViewportDragState>): void {
        if (!this.boardSceneElement) {
            return;
        }
        this.boardSceneElement.style.transform =
            `translate(${drag.currentX}px, ${drag.currentY}px) scale(${drag.zoom})`;
    }

    private updateDraggedCardDOM(board: BoardDocument, drag: NonNullable<DragState>): void {
        const cardLayout = board.layout.cards[drag.cardId];
        if (!cardLayout) {
            return;
        }
        const cardElement = this.cardElements.get(drag.cardId);
        if (cardElement) {
            cardElement.style.left = `${drag.currentX}px`;
            cardElement.style.top = `${drag.currentY}px`;
        }

        const draggedLayout = { ...cardLayout, x: drag.currentX, y: drag.currentY };
        drag.incidentEdges.forEach((edge) => {
            const from = edge.from === drag.cardId ? draggedLayout : board.layout.cards[edge.from];
            const to = edge.to === drag.cardId ? draggedLayout : board.layout.cards[edge.to];
            const elements = this.edgeElements.get(edge.id);
            if (!from || !to || !elements) {
                return;
            }
            const x1 = from.x + from.width / 2;
            const y1 = from.y + from.height / 2;
            const x2 = to.x + to.width / 2;
            const y2 = to.y + to.height / 2;
            this.setLineCoordinates(elements.line, x1, y1, x2, y2);
            this.setLineCoordinates(elements.hitbox, x1, y1, x2, y2);
            elements.label.setAttribute('x', `${(x1 + x2) / 2}`);
            elements.label.setAttribute('y', `${(y1 + y2) / 2 - 8}`);
        });
    }

    private setLineCoordinates(
        line: SVGLineElement,
        x1: number,
        y1: number,
        x2: number,
        y2: number
    ): void {
        line.setAttribute('x1', `${x1}`);
        line.setAttribute('y1', `${y1}`);
        line.setAttribute('x2', `${x2}`);
        line.setAttribute('y2', `${y2}`);
    }

    private commitCardDrag(drag: NonNullable<DragState>): void {
        if (!drag.moved) {
            return;
        }
        const board = useBoardStore.getState().board;
        const layout = board?.layout.cards[drag.cardId];
        if (!board || board.path !== drag.boardPath || !layout) {
            return;
        }
        const nextBoard: BoardDocument = {
            ...board,
            layout: {
                ...board.layout,
                cards: {
                    ...board.layout.cards,
                    [drag.cardId]: {
                        ...layout,
                        x: drag.currentX,
                        y: drag.currentY,
                    },
                },
            },
        };
        useBoardStore.getState().setBoard(nextBoard);
    }

    private commitViewportDrag(drag: NonNullable<ViewportDragState>): void {
        if (!drag.moved) {
            return;
        }
        const board = useBoardStore.getState().board;
        if (!board || board.path !== drag.boardPath) {
            return;
        }
        const nextBoard: BoardDocument = {
            ...board,
            layout: {
                ...board.layout,
                viewport: {
                    ...board.layout.viewport,
                    x: drag.currentX,
                    y: drag.currentY,
                },
            },
        };
        useBoardStore.getState().setBoard(nextBoard);
    }

    private subscribeToStores(): void {
        this.unsubscribe.push(
            useBoardStore.subscribe((state, prev) => {
                if (state.board?.path !== prev.board?.path) {
                    this.boardMode = 'select';
                    this.linkState = {};
                    this.linkAnnouncement = '';
                    this.linkUndoStack = [];
                    this.pendingFocusKey = null;
                    if (state.board?.path) {
                        void this.loadTrayMaterials(state.board.path);
                    }
                }
                this.render();
            })
        );
        this.unsubscribe.push(
            useUIStore.subscribe((state) => {
                if (state.activeTab === 'board') {
                    this.render();
                }
            })
        );
        this.unsubscribe.push(
            useDocStore.subscribe(
                (state) => state.searchQuery,
                (state, prev) => {
                    const board = useBoardStore.getState().board;
                    if (board?.path && state !== prev) {
                        this.startTraySearch(board.path);
                    }
                    if (useUIStore.getState().activeTab === 'board') {
                        this.render();
                    }
                }
            )
        );
    }

    private async loadTrayMaterials(boardPath: string): Promise<void> {
        this.startTraySearch(boardPath);
    }

    private startTraySearch(boardPath: string): void {
        this.resourceSearch.cancel();
        this.traySearchRequestId += 1;
        this.trayResources = [];
        this.trayMaterials = [];
        this.traySearchPage = 0;
        this.traySearchHasMore = false;
        this.traySearchLoading = false;
        this.render();
        void this.loadTrayPage(boardPath, 1, true);
    }

    private async loadNextTrayPage(): Promise<void> {
        const boardPath = useBoardStore.getState().board?.path;
        if (!boardPath || this.traySearchLoading || !this.traySearchHasMore ||
            this.trayMaterials.length >= LEGACY_RESOURCE_SEARCH_MAX_ITEMS) {
            return;
        }
        await this.loadTrayPage(boardPath, this.traySearchPage + 1, false);
    }

    private async loadTrayPage(boardPath: string, page: number, replace: boolean): Promise<void> {
        const requestId = ++this.traySearchRequestId;
        const query = useDocStore.getState().searchQuery;
        this.traySearchLoading = true;
        this.render();
        eventLogger.log('BoardView', 'tray-load-start', { boardPath, query, page, filter: this.trayFilter });
        try {
            const result = await this.resourceSearch.search({
                query,
                kinds: this.traySearchKinds(),
                excludePaths: [boardPath],
                page,
                limit: RESOURCE_SEARCH_PAGE_LIMIT,
            }, {
                boardPath,
                documentItems: useDocStore.getState().files,
            });
            if (!result || !this.isTraySearchActive(requestId, boardPath, query)) {
                return;
            }
            const resources = mergeTrayResources(
                replace ? [] : this.trayResources,
                result.items,
                LEGACY_RESOURCE_SEARCH_MAX_ITEMS,
            );
            this.trayResources = resources;
            this.trayMaterials = resources.map((item) => this.buildTrayMaterial(item));
            this.traySearchPage = result.page;
            this.traySearchHasMore = result.hasMore && this.trayMaterials.length < LEGACY_RESOURCE_SEARCH_MAX_ITEMS;
            useBoardStore.getState().setCandidateResources(
                resources
                    .filter((item) => item.kind === 'markdown')
                    .map((item) => ({
                        path: item.path,
                        title: item.title,
                        modTime: item.metadata.modTime,
                        size: item.metadata.size,
                    })),
            );
            eventLogger.log('BoardView', 'tray-load-success', {
                boardPath,
                page: result.page,
                pageCount: result.items.length,
                retainedCount: this.trayMaterials.length,
                total: result.total,
            });
        } catch (error) {
            if (!this.isTraySearchActive(requestId, boardPath, query)) {
                return;
            }
            console.error('Failed to search board resources:', error);
            eventLogger.log('BoardView', 'tray-load-error', { boardPath, error: String(error) });
            useUIStore.getState().setStatusMessage('素材トレイの検索に失敗しました', 2500);
        } finally {
            if (this.isTraySearchActive(requestId, boardPath, query)) {
                this.traySearchLoading = false;
                this.render();
            }
        }
    }

    private traySearchKinds(): ResourceKind[] {
        if (this.trayFilter === 'all') {
            return ['markdown', 'image', 'csv'];
        }
        return [this.trayFilter];
    }

    private isTraySearchActive(requestId: number, boardPath: string, query: string): boolean {
        return !this.destroyed && requestId === this.traySearchRequestId &&
            useBoardStore.getState().board?.path === boardPath && useDocStore.getState().searchQuery === query;
    }

    private async handleAction(action: string, element: HTMLElement): Promise<void> {
        const boardStore = useBoardStore.getState();
        const board = boardStore.board;
        switch (action) {
            case 'refresh-board':
                if (board?.path) {
                    const fresh = await this.api.LoadBoard(board.path);
                    this.syncSavedBoard(fresh);
                }
                return;
            case 'toggle-tray':
                this.trayCollapsed = !this.trayCollapsed;
                this.render();
                return;
            case 'set-tray-filter':
                this.trayFilter = (element.dataset.filter as MaterialFilter) || 'all';
                if (board?.path) {
                    this.startTraySearch(board.path);
                } else {
                    this.render();
                }
                return;
            case 'load-more-tray-resources':
                void this.loadNextTrayPage();
                return;
            case 'pan-left':
            case 'pan-right':
            case 'pan-up':
            case 'pan-down':
                this.panViewport(action);
                return;
            case 'add-edge':
                this.enterLinkMode();
                return;
            case 'select-mode':
                this.pendingFocusKey = 'action:select-mode';
                this.setBoardMode('select');
                return;
            case 'cancel-link-mode':
                this.pendingFocusKey = 'action:add-edge';
                this.setBoardMode('select');
                return;
            case 'undo-link-creation':
                this.undoLastCreatedLink();
                return;
            case 'reverse-edge':
                this.reverseSelectedEdge();
                return;
            case 'delete-card':
                if (element.dataset.cardId) {
                    this.deleteCard(element.dataset.cardId);
                }
                return;
            case 'delete-edge':
                if (element.dataset.edgeId) {
                    this.deleteEdge(element.dataset.edgeId);
                }
                return;
            default:
                return;
        }
    }

    private panViewport(action: string): void {
        const board = useBoardStore.getState().board;
        if (!board) {
            return;
        }
        const nextBoard = structuredClone(board) as BoardDocument;
        const amount = 60;
        if (action === 'pan-left') nextBoard.layout.viewport.x -= amount;
        if (action === 'pan-right') nextBoard.layout.viewport.x += amount;
        if (action === 'pan-up') nextBoard.layout.viewport.y -= amount;
        if (action === 'pan-down') nextBoard.layout.viewport.y += amount;
        useBoardStore.getState().setBoard(nextBoard);
        this.scheduleSave();
    }

    private updateViewportZoom(value: number): void {
        const board = useBoardStore.getState().board;
        if (!board) {
            return;
        }
        const nextBoard = structuredClone(board) as BoardDocument;
        nextBoard.layout.viewport.zoom = value;
        useBoardStore.getState().setBoard(nextBoard);
        this.scheduleSave();
    }

    private async handleMaterialDrop(event: DragEvent): Promise<void> {
        const board = useBoardStore.getState().board;
        const wrapper = this.element?.querySelector<HTMLElement>('.board-canvas-wrapper');
        if (!board || !wrapper) {
            return;
        }
        const materialPath = this.draggedMaterialPath || event.dataTransfer?.getData('text/plain');
        const material = this.trayMaterials.find((item) => item.path === materialPath);
        this.draggedMaterialPath = null;
        this.dragHoverCardId = null;
        if (!material) {
            this.render();
            return;
        }
        const position = this.getBoardPositionFromClient(event.clientX, event.clientY, wrapper, board);
        const targetCardId = (event.target as HTMLElement)?.closest<HTMLElement>('.board-card')?.dataset.cardId || null;
        const nextBoard = structuredClone(board) as BoardDocument;
        const { cardId, edgeId } = this.createCardFromMaterial(nextBoard, material, position.x, position.y, targetCardId);
        useBoardStore.getState().setBoard(nextBoard);
        useBoardStore.getState().setSelectedCardId(cardId);
        if (edgeId) {
            useBoardStore.getState().setSelectedEdgeId(edgeId);
        }
        await this.persistBoard(nextBoard);
        this.render();
    }

    private createCardFromMaterial(
        board: BoardDocument,
        material: TrayMaterial,
        x: number,
        y: number,
        linkToCardId: string | null = null
    ): { cardId: string; edgeId: string | null } {
        const cardId = this.nextCardId(board, material.cardType);
        board.cards.push({
            id: cardId,
            type: material.cardType,
            title: material.title,
            source: material.path,
            tags: [],
            createdBy: 'user',
            body: '',
            meta: {},
        });
        board.layout.cards[cardId] = {
            x,
            y,
            width: 300,
            height: 180,
        };

        if (!linkToCardId || !board.cards.some((card) => card.id === linkToCardId)) {
            return { cardId, edgeId: null };
        }

        const edgeId = this.nextEdgeId(board);
        board.edges.push({
            id: edgeId,
            from: cardId,
            to: linkToCardId,
            relation: 'references',
            label: '',
            description: '',
        });
        return { cardId, edgeId };
    }

    private nextCardId(board: BoardDocument, cardType: 'resource' | 'board'): string {
        let index = board.cards.length + 1;
        let cardId = `card:${cardType}-${String(index).padStart(3, '0')}`;
        while (board.cards.some((card) => card.id === cardId)) {
            index += 1;
            cardId = `card:${cardType}-${String(index).padStart(3, '0')}`;
        }
        return cardId;
    }

    private nextEdgeId(board: BoardDocument): string {
        let index = board.edges.length + 1;
        let edgeId = `edge:${String(index).padStart(3, '0')}`;
        while (board.edges.some((edge) => edge.id === edgeId)) {
            index += 1;
            edgeId = `edge:${String(index).padStart(3, '0')}`;
        }
        return edgeId;
    }

    private getBoardPositionFromClient(clientX: number, clientY: number, wrapper: HTMLElement, board: BoardDocument): { x: number; y: number } {
        const rect = wrapper.getBoundingClientRect();
        const zoom = board.layout.viewport.zoom || 1;
        const viewportX = board.layout.viewport.x || 0;
        const viewportY = board.layout.viewport.y || 0;
        const worldX = (clientX - rect.left - viewportX) / zoom;
        const worldY = (clientY - rect.top - viewportY) / zoom;
        return {
            x: Math.round(clamp(worldX - 150, 24, 1280)),
            y: Math.round(clamp(worldY - 90, 24, 980)),
        };
    }

    private setBoardMode(mode: BoardMode): void {
        this.boardMode = mode;
        if (mode === 'select') {
            this.linkState = {};
            this.linkAnnouncement = 'リンク作成を終了しました';
        }
        this.render();
    }

    private enterLinkMode(): void {
        const { board, selectedCardId } = useBoardStore.getState();
        if (!board || board.cards.length < 2) {
            useUIStore.getState().setStatusMessage('Edge を追加するにはカードが2枚以上必要です', 2000);
            return;
        }
        const initialCardId = selectedCardId && board.cards.some((card) => card.id === selectedCardId)
            ? selectedCardId
            : undefined;
        this.boardMode = 'link';
        this.linkState = initialCardId ? { fromCardId: initialCardId } : {};
        this.linkAnnouncement = initialCardId
            ? `接続元を${this.cardTitle(board, initialCardId)}に設定しました`
            : '接続元カードを選択してください';
        this.pendingFocusKey = initialCardId ? this.cardFocusKey(initialCardId) : 'action:add-edge';
        useBoardStore.setState({
            selectedCardId: initialCardId ?? null,
            selectedEdgeId: null,
        });
    }

    private handleLinkCardSelection(cardId: string): void {
        const board = useBoardStore.getState().board;
        if (!board || !board.cards.some((card) => card.id === cardId)) {
            this.reportInvalidEdge({ kind: 'invalid', reason: 'missing-card' });
            return;
        }
        if (!this.linkState.fromCardId) {
            this.linkState = { fromCardId: cardId };
            this.linkAnnouncement = `接続元を${this.cardTitle(board, cardId)}に設定しました`;
            this.pendingFocusKey = this.cardFocusKey(cardId);
            useBoardStore.setState({ selectedCardId: cardId, selectedEdgeId: null });
            return;
        }
        if (this.linkState.fromCardId === cardId) {
            this.linkState = {};
            this.linkAnnouncement = '接続元の選択を取り消しました';
            this.pendingFocusKey = this.cardFocusKey(cardId);
            useBoardStore.setState({ selectedCardId: null, selectedEdgeId: null });
            return;
        }

        const fromCardId = this.linkState.fromCardId;
        const result = this.createEdge(board, fromCardId, cardId, this.linkRelation);
        if (result.kind !== 'created') {
            this.reportInvalidEdge(result);
            return;
        }

        this.pushLinkUndo(board.path, result.edge);
        this.linkAnnouncement = `${this.cardTitle(board, fromCardId)}から${this.cardTitle(board, cardId)}へ「${this.relationLabel(result.edge.relation)}」を作成しました`;
        if (this.linkContinuous) {
            this.linkState = { fromCardId: cardId };
            this.pendingFocusKey = this.cardFocusKey(cardId);
            useBoardStore.setState({
                board: result.board,
                selectedCardId: cardId,
                selectedEdgeId: null,
            });
        } else {
            this.boardMode = 'select';
            this.linkState = {};
            this.pendingFocusKey = 'action:add-edge';
            useBoardStore.setState({
                board: result.board,
                selectedCardId: null,
                selectedEdgeId: result.edge.id,
            });
        }
        this.scheduleSave();
    }

    private createEdge(
        board: BoardDocument,
        fromCardId: string,
        toCardId: string,
        relation: string
    ): EdgeCreationResult {
        const validation = this.validateEdgeCandidate(board, fromCardId, toCardId, relation);
        if (validation.kind !== 'valid') {
            return validation;
        }
        const edge: BoardEdgeRecord = {
            id: this.nextEdgeId(board),
            from: fromCardId,
            to: toCardId,
            relation,
            label: '',
            description: '',
        };
        return {
            kind: 'created',
            board: { ...board, edges: [...board.edges, edge] },
            edge,
        };
    }

    private reverseSelectedEdge(): void {
        const board = useBoardStore.getState().board;
        const selectedEdgeId = useBoardStore.getState().selectedEdgeId;
        if (!board || !selectedEdgeId) {
            return;
        }
        const edge = board.edges.find((item) => item.id === selectedEdgeId);
        if (!edge) {
            return;
        }
        const nextEdge = { ...edge, from: edge.to, to: edge.from };
        const validation = this.validateEdgeCandidate(
            board,
            nextEdge.from,
            nextEdge.to,
            nextEdge.relation,
            edge.id
        );
        if (validation.kind !== 'valid') {
            this.reportInvalidEdge(validation);
            return;
        }
        this.updateLinkUndoIdentity(board.path, edge, nextEdge);
        useBoardStore.setState({
            board: {
                ...board,
                edges: board.edges.map((item) => item.id === edge.id ? nextEdge : item),
            },
        });
        this.scheduleSave();
    }

    private validateEdgeCandidate(
        board: BoardDocument,
        fromCardId: string,
        toCardId: string,
        relation: string,
        excludedEdgeId?: string
    ): EdgeValidationResult {
        if (fromCardId === toCardId) {
            return { kind: 'invalid', reason: 'self' };
        }
        if (!board.cards.some((card) => card.id === fromCardId) ||
            !board.cards.some((card) => card.id === toCardId)) {
            return { kind: 'invalid', reason: 'missing-card' };
        }
        if (!this.isLinkRelation(relation)) {
            return { kind: 'invalid', reason: 'relation' };
        }
        const duplicate = board.edges.find((edge) =>
            edge.id !== excludedEdgeId &&
            edge.from === fromCardId &&
            edge.to === toCardId &&
            edge.relation === relation
        );
        if (duplicate) {
            return { kind: 'duplicate', edgeId: duplicate.id };
        }
        return { kind: 'valid' };
    }

    private reportInvalidEdge(result: Exclude<EdgeValidationResult, { kind: 'valid' }>): void {
        let message = 'Edge を作成できませんでした';
        if (result.kind === 'duplicate') {
            message = '同じ向き・関係の Edge はすでに存在します';
        } else if (result.reason === 'self') {
            message = '同じカード同士は接続できません';
        } else if (result.reason === 'missing-card') {
            message = '接続元または接続先のカードが見つかりません';
        } else if (result.reason === 'relation') {
            message = '未対応の relation は保存できません';
        }
        this.linkAnnouncement = message;
        useUIStore.getState().setStatusMessage(message, 2200);
        this.render();
    }

    private pushLinkUndo(boardPath: string, edge: BoardEdgeRecord): void {
        this.linkUndoStack.push({ boardPath, edge: { ...edge } });
        if (this.linkUndoStack.length > 50) {
            this.linkUndoStack.shift();
        }
    }

    private hasLinkUndoForCurrentBoard(): boolean {
        const boardPath = useBoardStore.getState().board?.path;
        const latest = this.linkUndoStack.at(-1);
        return Boolean(boardPath && latest?.boardPath === boardPath);
    }

    private undoLastCreatedLink(): void {
        const { board, selectedEdgeId, selectedCardId } = useBoardStore.getState();
        const entry = this.linkUndoStack.at(-1);
        if (!board || !entry || entry.boardPath !== board.path) {
            return;
        }

        const currentEdge = board.edges.find((edge) => edge.id === entry.edge.id);
        this.linkUndoStack.pop();
        if (!currentEdge || !this.sameEdgeIdentity(currentEdge, entry.edge)) {
            const message = 'リンク作成専用Undoを中止しました．作成後にEdgeが変更されています';
            this.linkAnnouncement = message;
            useUIStore.getState().setStatusMessage(message, 2600);
            this.render();
            return;
        }

        this.linkAnnouncement = `${this.cardTitle(board, currentEdge.from)}から${this.cardTitle(board, currentEdge.to)}へのリンク作成を戻しました`;
        this.pendingFocusKey = this.boardMode === 'link' && this.linkState.fromCardId
            ? this.cardFocusKey(this.linkState.fromCardId)
            : 'action:add-edge';
        useBoardStore.setState({
            board: {
                ...board,
                edges: board.edges.filter((edge) => edge.id !== currentEdge.id),
            },
            selectedEdgeId: selectedEdgeId === currentEdge.id ? null : selectedEdgeId,
            selectedCardId,
        });
        this.scheduleSave();
    }

    private updateLinkUndoIdentity(
        boardPath: string,
        previous: BoardEdgeRecord,
        next: BoardEdgeRecord
    ): void {
        for (let index = this.linkUndoStack.length - 1; index >= 0; index -= 1) {
            const entry = this.linkUndoStack[index];
            if (entry.boardPath === boardPath && this.sameEdgeIdentity(entry.edge, previous)) {
                this.linkUndoStack[index] = { boardPath, edge: { ...next } };
                return;
            }
        }
    }

    private sameEdgeIdentity(left: BoardEdgeRecord, right: BoardEdgeRecord): boolean {
        return left.id === right.id &&
            left.from === right.from &&
            left.to === right.to &&
            left.relation === right.relation &&
            (left.label ?? '') === (right.label ?? '') &&
            (left.description ?? '') === (right.description ?? '');
    }

    private cancelLinkStep(): void {
        if (this.linkState.fromCardId) {
            const cancelledCardId = this.linkState.fromCardId;
            this.linkState = {};
            this.linkAnnouncement = '接続元の選択を取り消しました';
            this.pendingFocusKey = this.cardFocusKey(cancelledCardId);
            useBoardStore.setState({ selectedCardId: null, selectedEdgeId: null });
            return;
        }
        this.boardMode = 'select';
        this.linkAnnouncement = 'リンク作成を終了しました';
        this.pendingFocusKey = 'action:add-edge';
        this.render();
    }

    private activateBoardCard(cardId: string): void {
        if (this.boardMode === 'link') {
            this.handleLinkCardSelection(cardId);
            return;
        }
        useBoardStore.setState({ selectedCardId: cardId, selectedEdgeId: null });
    }

    private focusAdjacentCard(
        cardId: string,
        direction: 'ArrowLeft' | 'ArrowRight' | 'ArrowUp' | 'ArrowDown'
    ): void {
        const board = useBoardStore.getState().board;
        const origin = board?.layout.cards[cardId];
        if (!board || !origin) {
            return;
        }
        const originX = origin.x + origin.width / 2;
        const originY = origin.y + origin.height / 2;
        const candidates = board.cards.flatMap((card) => {
            if (card.id === cardId) {
                return [];
            }
            const layout = board.layout.cards[card.id];
            if (!layout) {
                return [];
            }
            const dx = layout.x + layout.width / 2 - originX;
            const dy = layout.y + layout.height / 2 - originY;
            const primary = direction === 'ArrowLeft' ? -dx
                : direction === 'ArrowRight' ? dx
                    : direction === 'ArrowUp' ? -dy
                        : dy;
            if (primary <= 0) {
                return [];
            }
            const perpendicular = direction === 'ArrowLeft' || direction === 'ArrowRight' ? Math.abs(dy) : Math.abs(dx);
            return [{
                cardId: card.id,
                angle: perpendicular / primary,
                distance: Math.hypot(dx, dy),
            }];
        });
        candidates.sort((left, right) =>
            left.angle - right.angle || left.distance - right.distance || left.cardId.localeCompare(right.cardId)
        );
        const nextCardId = candidates[0]?.cardId;
        if (nextCardId) {
            this.cardElements.get(nextCardId)
                ?.querySelector<HTMLElement>('[data-board-card-activation]')
                ?.focus();
        }
    }

    private cardFocusKey(cardId: string): string {
        return `card:${cardId}`;
    }

    private cardTitle(board: BoardDocument, cardId: string): string {
        const card = board.cards.find((item) => item.id === cardId);
        return card?.title || cardId;
    }

    private relationLabel(relation: string): string {
        return this.isLinkRelation(relation) ? RELATION_LABELS[relation] : relation;
    }

    private isLinkRelation(value: string): value is (typeof RELATIONS)[number] {
        return (RELATIONS as readonly string[]).includes(value);
    }

    private isEditableElement(target: HTMLElement): boolean {
        return Boolean(target.closest('input, textarea, select, [contenteditable="true"], [contenteditable=""], [contenteditable="plaintext-only"]'));
    }

    private deleteCard(cardId: string): void {
        const board = useBoardStore.getState().board;
        if (!board) {
            return;
        }
        const nextBoard = structuredClone(board) as BoardDocument;
        nextBoard.cards = nextBoard.cards.filter((card) => card.id !== cardId);
        nextBoard.edges = nextBoard.edges.filter((edge) => edge.from !== cardId && edge.to !== cardId);
        delete nextBoard.layout.cards[cardId];
        useBoardStore.getState().setBoard(nextBoard);
        useBoardStore.getState().setSelectedCardId(null);
        this.scheduleSave();
    }

    private deleteEdge(edgeId: string): void {
        const board = useBoardStore.getState().board;
        if (!board) {
            return;
        }
        const nextBoard = structuredClone(board) as BoardDocument;
        nextBoard.edges = nextBoard.edges.filter((edge) => edge.id !== edgeId);
        useBoardStore.getState().setBoard(nextBoard);
        useBoardStore.getState().setSelectedEdgeId(null);
        this.scheduleSave();
    }

    private updateSelectedCardField(name: string, value: string): void {
        const board = useBoardStore.getState().board;
        const selectedCardId = useBoardStore.getState().selectedCardId;
        if (!board || !selectedCardId) {
            return;
        }
        const nextBoard = structuredClone(board) as BoardDocument;
        const card = nextBoard.cards.find((item) => item.id === selectedCardId);
        if (!card) {
            return;
        }
        if (name === 'tags') {
            card.tags = value.split(',').map((item) => item.trim()).filter(Boolean);
        } else if (name === 'reviewed') {
            card.reviewed = value === 'true';
        } else if (name === 'createdBy') {
            card.createdBy = value;
        } else if (name === 'updatedBy') {
            card.updatedBy = value;
        } else if (name === 'reviewedBy') {
            card.reviewedBy = value;
        } else if (name === 'source') {
            card.source = value;
        } else if (name === 'body') {
            card.body = value;
        } else if (name === 'title') {
            card.title = value;
        } else if (name === 'type') {
            card.type = value;
        }
        useBoardStore.getState().setBoard(nextBoard);
        this.scheduleSave();
    }

    private updateSelectedEdgeField(name: string, value: string): void {
        const board = useBoardStore.getState().board;
        const selectedEdgeId = useBoardStore.getState().selectedEdgeId;
        if (!board || !selectedEdgeId) {
            return;
        }
        const edge = board.edges.find((item) => item.id === selectedEdgeId);
        if (!edge) {
            return;
        }
        const nextEdge = { ...edge };
        if (name === 'from') nextEdge.from = value;
        else if (name === 'to') nextEdge.to = value;
        else if (name === 'relation') nextEdge.relation = value;
        else if (name === 'label') nextEdge.label = value;
        else if (name === 'description') nextEdge.description = value;
        else return;

        const validation = this.validateEdgeCandidate(
            board,
            nextEdge.from,
            nextEdge.to,
            nextEdge.relation,
            edge.id
        );
        if (validation.kind !== 'valid') {
            this.pendingFocusKey = `edge:${edge.id}:${name}`;
            this.reportInvalidEdge(validation);
            return;
        }
        this.updateLinkUndoIdentity(board.path, edge, nextEdge);
        useBoardStore.setState({
            board: {
                ...board,
                edges: board.edges.map((item) => item.id === edge.id ? nextEdge : item),
            },
        });
        this.scheduleSave();
    }

    private scheduleSave(): void {
        if (this.saveTimer) {
            window.clearTimeout(this.saveTimer);
        }
        this.saveTimer = window.setTimeout(() => {
            const board = useBoardStore.getState().board;
            if (board) {
                void this.persistBoard(board);
            }
        }, 250);
    }

    private async persistBoard(board: BoardDocument): Promise<void> {
        const requestId = ++this.latestPersistRequestId;
        try {
            const saved = await this.api.SaveBoard(board.path, board);
            if (this.destroyed || requestId !== this.latestPersistRequestId) {
                return;
            }
            this.syncSavedBoard(saved);
            useUIStore.getState().setStatusMessage('コルクボードを保存しました', 1500);
        } catch (error) {
            if (this.destroyed) {
                return;
            }
            console.error('Failed to save board:', error);
            useUIStore.getState().setStatusMessage('コルクボードの保存に失敗しました', 2500);
        }
    }

    private syncSavedBoard(board: BoardDocument): void {
        const selectedCardId = useBoardStore.getState().selectedCardId;
        const selectedEdgeId = useBoardStore.getState().selectedEdgeId;
        useBoardStore.getState().setBoard(board);
        if (selectedCardId && board.cards.some((card) => card.id === selectedCardId)) {
            useBoardStore.getState().setSelectedCardId(selectedCardId);
        } else if (selectedEdgeId && board.edges.some((edge) => edge.id === selectedEdgeId)) {
            useBoardStore.getState().setSelectedEdgeId(selectedEdgeId);
        }
        useDocStore.getState().setCurrentPath(board.path);
        useDocStore.getState().setMarkdownContent(board.rawContent);
        useDocStore.getState().setPreviewHtml('');
        useDocStore.getState().clearUnsavedChanges();
    }

    private buildTrayMaterial(resource: ResourceSearchItem): TrayMaterial {
        const isBoard = resource.path.toLowerCase().endsWith('.board.md');
        return {
            path: resource.path,
            title: resource.title || resource.metadata.name || resource.path.split('/').pop() || resource.path,
            kind: resource.kind,
            description: isBoard ? '既存のコルクボード' : resource.path,
            cardType: isBoard ? 'board' : 'resource',
        };
    }

    private getFilteredTrayMaterials(): TrayMaterial[] {
        return this.trayMaterials;
    }

    private cacheRenderedBoardElements(): void {
        this.cardElements.clear();
        this.edgeElements.clear();
        this.boardSceneElement = this.element?.querySelector<HTMLElement>('.board-scene') ?? null;
        this.element?.querySelectorAll<HTMLElement>('.board-card').forEach((cardElement) => {
            const cardId = cardElement.dataset.cardId;
            if (cardId) {
                this.cardElements.set(cardId, cardElement);
            }
        });
        this.element?.querySelectorAll<SVGGElement>('.board-edge-group[data-edge-id]').forEach((edgeGroup) => {
            const edgeId = edgeGroup.dataset.edgeId;
            const line = edgeGroup.querySelector<SVGLineElement>('.board-edge-line');
            const hitbox = edgeGroup.querySelector<SVGLineElement>('.board-edge-hitbox');
            const label = edgeGroup.querySelector<SVGTextElement>('.board-edge-label');
            if (edgeId && line && hitbox && label) {
                this.edgeElements.set(edgeId, { line, hitbox, label });
            }
        });
    }

    private clearRenderedBoardElements(): void {
        this.boardSceneElement = null;
        this.cardElements.clear();
        this.edgeElements.clear();
    }

    private restoreBoardFocus(focusKey: string | null): void {
        if (!focusKey || !this.element || this.destroyed) {
            return;
        }
        const candidates = Array.from(this.element.querySelectorAll<HTMLElement>('[data-board-focus-key]'));
        const requested = candidates.find((element) => element.dataset.boardFocusKey === focusKey);
        if (requested) {
            requested.focus();
            return;
        }
        if (focusKey.startsWith('card:')) {
            const fallback = this.boardMode === 'link'
                ? this.element.querySelector<HTMLElement>('[data-board-card-activation]')
                : candidates.find((element) => element.dataset.boardFocusKey === 'action:add-edge');
            fallback?.focus();
        }
    }

    private render(): void {
        if (!this.element) {
            return;
        }
        const activeElement = document.activeElement as HTMLElement | null;
        const searchInput = activeElement?.closest('input[name="boardTraySearch"]') ? activeElement as HTMLInputElement : null;
        const activeFocusKey = activeElement?.closest<HTMLElement>('[data-board-focus-key]')?.dataset.boardFocusKey || null;
        const focusKey = this.pendingFocusKey || activeFocusKey;
        this.pendingFocusKey = null;
        const selectionStart = searchInput?.selectionStart ?? null;
        const selectionEnd = searchInput?.selectionEnd ?? null;
        const { board, selectedCardId, selectedEdgeId } = useBoardStore.getState();
        if (!board) {
            this.clearRenderedBoardElements();
            this.element.innerHTML = `<div class="board-empty">グラフからコルクボードを開くと、ここに board UI を表示します。</div>`;
            return;
        }

        const selectedCard = board.cards.find((card) => card.id === selectedCardId) || null;
        const selectedEdge = board.edges.find((edge) => edge.id === selectedEdgeId) || null;
        const scale = board.layout.viewport.zoom || 1;
        const translateX = board.layout.viewport.x || 0;
        const translateY = board.layout.viewport.y || 0;
        const searchQuery = useDocStore.getState().searchQuery;
        const trayMaterials = this.getFilteredTrayMaterials();
        const modeHint = this.getModeHint(board);

        this.element.innerHTML = `
            <div class="board-shell">
                <div class="board-toolbar">
                    <div class="board-toolbar-group">
                        <button class="btn" data-action="refresh-board">再読込</button>
                        <button class="btn" data-action="toggle-tray">${this.trayCollapsed ? '素材を開く' : '素材を閉じる'}</button>
                        <button class="btn${this.boardMode === 'select' ? ' is-active' : ''}" data-action="select-mode" data-board-focus-key="action:select-mode" aria-pressed="${this.boardMode === 'select'}">Select</button>
                        <button class="btn${this.boardMode === 'link' ? ' is-active' : ''}" data-action="add-edge" data-board-focus-key="action:add-edge" aria-pressed="${this.boardMode === 'link'}" aria-controls="board-link-status">Link</button>
                        ${this.boardMode === 'link' ? '<button class="btn" data-action="cancel-link-mode" data-board-focus-key="action:cancel-link-mode">終了</button>' : ''}
                        <button class="btn" data-action="undo-link-creation" data-board-focus-key="action:undo-link-creation" aria-keyshortcuts="Control+Z Meta+Z"${this.hasLinkUndoForCurrentBoard() ? '' : ' disabled'}>リンク作成を1件戻す</button>
                        <button class="btn" data-action="pan-left">←</button>
                        <button class="btn" data-action="pan-right">→</button>
                        <button class="btn" data-action="pan-up">↑</button>
                        <button class="btn" data-action="pan-down">↓</button>
                        <label class="board-zoom-label">Zoom
                            <input type="range" min="0.6" max="1.8" step="0.1" name="zoom" value="${scale}">
                        </label>
                    </div>
                    <div class="board-toolbar-group">
                        <div class="board-toolbar-hint">${escapeHtml(modeHint)}</div>
                        <div class="board-toolbar-hint">リンク作成専用Undo（一般編集のUndoではありません）</div>
                        ${this.boardMode === 'link' ? `
                            <label class="board-link-label">relation
                                <select name="linkRelation" data-board-focus-key="link:relation" aria-label="リンクのrelation">
                                    ${RELATIONS.map((relation) => `<option value="${relation}"${relation === this.linkRelation ? ' selected' : ''}>${escapeHtml(RELATION_LABELS[relation])}</option>`).join('')}
                                </select>
                            </label>
                            <label class="board-checkbox">
                                <input type="checkbox" name="linkContinuous" data-board-focus-key="link:continuous"${this.linkContinuous ? ' checked' : ''}>
                                連続作成
                            </label>
                        ` : ''}
                        <div id="board-link-status" role="status" aria-live="polite" aria-atomic="true">${escapeHtml(this.linkAnnouncement ? `${modeHint} / ${this.linkAnnouncement}` : modeHint)}</div>
                    </div>
                </div>
                <div class="board-main">
                    <aside class="board-material-tray${this.trayCollapsed ? ' collapsed' : ''}">
                        <div class="board-tray-header">
                            <strong>素材</strong>
                            <span>${trayMaterials.length}件</span>
                        </div>
                        ${this.trayCollapsed ? `
                            <div class="board-tray-collapsed-label">Tray</div>
                        ` : `
                            <label class="board-tray-search">
                                <span>検索</span>
                                <input name="boardTraySearch" value="${escapeHtml(searchQuery)}" placeholder="ファイル検索を共有">
                            </label>
                            <div class="board-tray-filters">
                                ${this.renderTrayFilterButton('all', 'All')}
                                ${this.renderTrayFilterButton('markdown', 'Markdown')}
                                ${this.renderTrayFilterButton('image', 'Image')}
                                ${this.renderTrayFilterButton('csv', 'CSV')}
                            </div>
                            <div class="board-tray-list">
                                ${trayMaterials.length > 0
                                    ? trayMaterials.map((material) => this.renderTrayMaterial(material)).join('')
                                    : '<div class="board-empty-inline">該当する素材がありません</div>'}
                                ${this.traySearchHasMore && trayMaterials.length < LEGACY_RESOURCE_SEARCH_MAX_ITEMS
                                    ? `<button class="btn board-tray-more" data-action="load-more-tray-resources"${this.traySearchLoading ? ' disabled' : ''}>${this.traySearchLoading ? '読み込み中…' : 'さらに表示'}</button>`
                                    : ''}
                            </div>
                        `}
                    </aside>
                    <div class="board-canvas-wrapper board-mode-${this.boardMode}">
                        <div class="board-scene" style="transform: translate(${translateX}px, ${translateY}px) scale(${scale});">
                            <svg class="board-edge-layer" viewBox="0 0 1600 1200" preserveAspectRatio="none">
                                <defs>
                                    <marker id="board-edge-arrow" markerWidth="10" markerHeight="10" refX="8" refY="5" orient="auto" markerUnits="strokeWidth">
                                        <path d="M0,0 L10,5 L0,10 z" fill="rgba(64, 42, 15, 0.75)" />
                                    </marker>
                                    <marker id="board-edge-arrow-selected" markerWidth="10" markerHeight="10" refX="8" refY="5" orient="auto" markerUnits="strokeWidth">
                                        <path d="M0,0 L10,5 L0,10 z" fill="#9b5d1d" />
                                    </marker>
                                </defs>
                                ${this.renderEdges(board, selectedEdgeId)}
                            </svg>
                            <div class="board-canvas" role="list" aria-label="ボードカード．Tabで移動し，矢印キーで空間移動，EnterまたはSpaceで選択します">
                                ${board.cards.map((card) => this.renderCard(
                                    card,
                                    board.layout.cards[card.id],
                                    card.id === selectedCardId,
                                    card.id === this.dragHoverCardId
                                )).join('')}
                            </div>
                        </div>
                    </div>
                    <aside class="board-inspector">
                        <div class="board-inspector-section">
                            <h3>Board</h3>
                            <div class="board-meta-line"><strong>${escapeHtml(board.title)}</strong></div>
                            <div class="board-meta-line">${escapeHtml(board.path)}</div>
                            <div class="board-meta-line">cards: ${board.cards.length} / edges: ${board.edges.length}</div>
                        </div>
                        <div class="board-inspector-section board-card-editor">
                            <h3>Card</h3>
                            ${selectedCard ? this.renderCardEditor(selectedCard) : '<div class="board-empty-inline">カードを選択してください</div>'}
                        </div>
                        <div class="board-inspector-section board-edge-editor">
                            <h3>Edge</h3>
                            ${selectedEdge ? this.renderEdgeEditor(selectedEdge, board.cards) : '<div class="board-empty-inline">edge を選択してください</div>'}
                        </div>
                    </aside>
                </div>
            </div>
        `;
        this.cacheRenderedBoardElements();
        if (this.dragState?.moved || this.viewportDragState?.moved) {
            this.applyPointerFrame();
        }
        if (searchInput) {
            const nextInput = this.element.querySelector<HTMLInputElement>('input[name="boardTraySearch"]');
            nextInput?.focus();
            if (nextInput && selectionStart !== null && selectionEnd !== null) {
                nextInput.setSelectionRange(selectionStart, selectionEnd);
            }
        } else {
            this.restoreBoardFocus(focusKey);
        }
    }

    private renderTrayFilterButton(filter: MaterialFilter, label: string): string {
        const activeClass = this.trayFilter === filter ? ' active' : '';
        return `<button class="board-tray-filter${activeClass}" data-action="set-tray-filter" data-filter="${filter}">${label}</button>`;
    }

    private getModeHint(board: BoardDocument): string {
        if (this.boardMode !== 'link') {
            return '素材トレイからドラッグして追加';
        }
        const fromCardId = this.linkState.fromCardId;
        if (!fromCardId) {
            return `リンク作成中: 接続元: 未選択 / 接続先: 未選択 / relation: ${this.relationLabel(this.linkRelation)}`;
        }
        const fromCard = board.cards.find((card) => card.id === fromCardId);
        return `リンク作成中: 接続元: ${fromCard?.title || fromCardId} / 接続先: 未選択 / relation: ${this.relationLabel(this.linkRelation)}`;
    }

    private renderTrayMaterial(material: TrayMaterial): string {
        return `
            <article class="board-tray-item" draggable="true" data-material-path="${escapeHtml(material.path)}">
                <div class="board-tray-item-kind">${escapeHtml(material.kind)}</div>
                <h4>${escapeHtml(material.title)}</h4>
                <p>${escapeHtml(material.description)}</p>
            </article>
        `;
    }

    private renderCard(
        card: BoardCard,
        layout: BoardDocument['layout']['cards'][string] | undefined,
        selected: boolean,
        dropTarget: boolean
    ): string {
        const box = layout || { x: 80, y: 80, width: 280, height: 160 };
        return `
            <article
                class="board-card${selected ? ' selected' : ''}${dropTarget ? ' drop-target' : ''}${this.linkState.fromCardId === card.id ? ' link-source' : ''}"
                data-card-id="${escapeHtml(card.id)}"
                role="listitem"
                style="left:${box.x}px;top:${box.y}px;width:${box.width}px;height:${box.height}px;"
            >
                <div class="board-card-header">
                    <span class="board-card-type">${escapeHtml(card.type)}</span>
                    <div class="board-card-actions">
                        <button class="btn btn-small" data-action="delete-card" data-card-id="${escapeHtml(card.id)}">削除</button>
                    </div>
                </div>
                <div
                    class="board-card-activation"
                    data-board-card-activation
                    data-card-id="${escapeHtml(card.id)}"
                    data-board-focus-key="${escapeHtml(this.cardFocusKey(card.id))}"
                    tabindex="0"
                    role="button"
                    aria-pressed="${selected || this.linkState.fromCardId === card.id}"
                    aria-label="${escapeHtml(`${card.title || card.id}．EnterまたはSpaceで${this.boardMode === 'link' ? 'リンク対象に指定' : '選択'}します`)}"
                    aria-keyshortcuts="Enter Space ArrowLeft ArrowRight ArrowUp ArrowDown"
                    ${this.boardMode === 'link' ? 'aria-describedby="board-link-status"' : ''}
                >
                    <h4>${escapeHtml(card.title || card.id)}</h4>
                    <p>${escapeHtml(card.body || card.source || '')}</p>
                    ${card.source ? `<div class="board-card-source">${escapeHtml(card.source)}</div>` : ''}
                </div>
            </article>
        `;
    }

    private renderEdges(board: BoardDocument, selectedEdgeId: string | null): string {
        return board.edges.map((edge) => {
            const from = board.layout.cards[edge.from];
            const to = board.layout.cards[edge.to];
            if (!from || !to) {
                return '';
            }
            const x1 = from.x + from.width / 2;
            const y1 = from.y + from.height / 2;
            const x2 = to.x + to.width / 2;
            const y2 = to.y + to.height / 2;
            const labelX = (x1 + x2) / 2;
            const labelY = (y1 + y2) / 2 - 8;
            const active = edge.id === selectedEdgeId;
            const markerId = active ? 'board-edge-arrow-selected' : 'board-edge-arrow';
            const displayLabel = edge.label?.trim() || RELATION_LABELS[(edge.relation as (typeof RELATIONS)[number])] || edge.relation;
            return `
                <g class="board-edge-group" data-edge-id="${escapeHtml(edge.id)}">
                    <line class="board-edge-line${active ? ' selected' : ''}" x1="${x1}" y1="${y1}" x2="${x2}" y2="${y2}" marker-end="url(#${markerId})" />
                    <line class="board-edge-hitbox" data-edge-id="${escapeHtml(edge.id)}" x1="${x1}" y1="${y1}" x2="${x2}" y2="${y2}" />
                    <text x="${labelX}" y="${labelY}" text-anchor="middle" class="board-edge-label">${escapeHtml(displayLabel)}</text>
                </g>
            `;
        }).join('');
    }

    private renderCardEditor(card: BoardCard): string {
        return `
            <label>id<input name="id" value="${escapeHtml(card.id)}" disabled></label>
            <label>type<select name="type">${CARD_TYPES.map((type) => `<option value="${type}"${card.type === type ? ' selected' : ''}>${type}</option>`).join('')}</select></label>
            <label>title<input name="title" value="${escapeHtml(card.title || '')}"></label>
            <label>source<input name="source" value="${escapeHtml(card.source || '')}"></label>
            <label>tags<input name="tags" value="${escapeHtml((card.tags || []).join(', '))}"></label>
            <label>createdBy<input name="createdBy" value="${escapeHtml(card.createdBy || '')}"></label>
            <label>updatedBy<input name="updatedBy" value="${escapeHtml(card.updatedBy || '')}"></label>
            <label>reviewedBy<input name="reviewedBy" value="${escapeHtml(card.reviewedBy || '')}"></label>
            <label class="board-checkbox"><input type="checkbox" name="reviewed"${card.reviewed ? ' checked' : ''}> reviewed</label>
            <label>body<textarea name="body" rows="6">${escapeHtml(card.body || '')}</textarea></label>
        `;
    }

    private renderEdgeEditor(edge: BoardEdgeRecord, cards: BoardCard[]): string {
        const options = cards.map((card) => `<option value="${escapeHtml(card.id)}"${card.id === edge.from ? ' selected' : ''}>${escapeHtml(card.title || card.id)}</option>`).join('');
        const toOptions = cards.map((card) => `<option value="${escapeHtml(card.id)}"${card.id === edge.to ? ' selected' : ''}>${escapeHtml(card.title || card.id)}</option>`).join('');
        return `
            <label>id<input name="id" value="${escapeHtml(edge.id)}" disabled></label>
            <label>from<select name="from" data-board-focus-key="${escapeHtml(`edge:${edge.id}:from`)}">${options}</select></label>
            <label>to<select name="to" data-board-focus-key="${escapeHtml(`edge:${edge.id}:to`)}">${toOptions}</select></label>
            <label>relation<select name="relation" data-board-focus-key="${escapeHtml(`edge:${edge.id}:relation`)}">${RELATIONS.map((relation) => `<option value="${relation}"${edge.relation === relation ? ' selected' : ''}>${escapeHtml(RELATION_LABELS[relation])}</option>`).join('')}</select></label>
            <label>label<input name="label" data-board-focus-key="${escapeHtml(`edge:${edge.id}:label`)}" value="${escapeHtml(edge.label || '')}"></label>
            <label>description<textarea name="description" data-board-focus-key="${escapeHtml(`edge:${edge.id}:description`)}" rows="4">${escapeHtml(edge.description || '')}</textarea></label>
            <button class="btn" data-action="reverse-edge">向きを反転</button>
            <button class="btn" data-action="delete-edge" data-edge-id="${escapeHtml(edge.id)}">Edge削除</button>
        `;
    }

    destroy(): void {
        this.destroyed = true;
        this.traySearchRequestId += 1;
        this.resourceSearch.destroy();
        this.trayResources = [];
        this.trayMaterials = [];
        this.traySearchHasMore = false;
        this.traySearchLoading = false;
        this.cancelPointerFrame();
        this.dragState = null;
        this.viewportDragState = null;
        this.boardMode = 'select';
        this.linkState = {};
        this.linkAnnouncement = '';
        this.linkUndoStack = [];
        this.pendingFocusKey = null;
        this.latestPersistRequestId++;
        if (this.saveTimer) {
            window.clearTimeout(this.saveTimer);
            this.saveTimer = null;
        }
        this.unsubscribe.forEach((unsub) => unsub());
        this.unsubscribe = [];
        this.clearRenderedBoardElements();
    }
}

function escapeHtml(value: string): string {
    return value
        .replaceAll('&', '&amp;')
        .replaceAll('<', '&lt;')
        .replaceAll('>', '&gt;')
        .replaceAll('"', '&quot;')
        .replaceAll("'", '&#39;');
}

function clamp(value: number, min: number, max: number): number {
    return Math.min(max, Math.max(min, value));
}

function mergeTrayResources(
    existing: ResourceSearchItem[],
    incoming: ResourceSearchItem[],
    maximum: number,
): ResourceSearchItem[] {
    const byPath = new Map(existing.map((item) => [item.path, item]));
    for (const item of incoming) {
        if (!byPath.has(item.path) && byPath.size < maximum) {
            byPath.set(item.path, item);
        }
    }
    return Array.from(byPath.values());
}
