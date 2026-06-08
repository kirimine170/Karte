import { BaseComponent } from './component-base';
import { useBoardStore, useDocStore, useUIStore } from '../stores/index';
import { filterFilesByQuery } from '../logic';
import type { BoardCard, BoardDocument, BoardEdgeRecord, CsvInfo, FileItem, ImageInfo, WailsAppAPI } from '../types/wails-api';
import { eventLogger } from '../utils/event-logger';

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
    toCardId?: string;
};

type DragState = {
    cardId: string;
    startX: number;
    startY: number;
    originX: number;
    originY: number;
} | null;

type ViewportDragState = {
    startX: number;
    startY: number;
    originX: number;
    originY: number;
} | null;

type MaterialFilter = 'all' | 'markdown' | 'image' | 'csv' | 'board';

type TrayMaterial = {
    path: string;
    title: string;
    kind: MaterialFilter | 'other';
    description: string;
    cardType: 'resource' | 'board';
    modTime: string;
    searchText: string;
};

export class BoardView extends BaseComponent {
    private unsubscribe: (() => void)[] = [];
    private api: WailsAppAPI;
    private saveTimer: number | null = null;
    private dragState: DragState = null;
    private viewportDragState: ViewportDragState = null;
    private latestPersistRequestId = 0;
    private trayCollapsed = false;
    private trayFilter: MaterialFilter = 'all';
    private trayMaterials: TrayMaterial[] = [];
    private trayImages: ImageInfo[] = [];
    private trayCsvs: CsvInfo[] = [];
    private draggedMaterialPath: string | null = null;
    private dragHoverCardId: string | null = null;
    private boardMode: BoardMode = 'select';
    private linkState: LinkModeState = {};
    private linkRelation: (typeof RELATIONS)[number] = 'related_to';
    private linkContinuous = false;

    constructor(api: WailsAppAPI, parent?: HTMLElement) {
        super(parent);
        this.api = api;
    }

    init(): void {
        eventLogger.log('BoardView', 'init');
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
                    if (this.boardMode === 'link') {
                        this.handleLinkCardSelection(cardEl.dataset.cardId);
                        return;
                    }
                    useBoardStore.getState().setSelectedCardId(cardEl.dataset.cardId);
                    useBoardStore.getState().setSelectedEdgeId(null);
                    this.render();
                    return;
                }

                const edgeEl = target.closest<SVGElement>('.board-edge-hitbox');
                if (edgeEl?.dataset.edgeId) {
                    useBoardStore.getState().setSelectedEdgeId(edgeEl.dataset.edgeId);
                    useBoardStore.getState().setSelectedCardId(null);
                    this.render();
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
                    const hitCount = this.getFilteredTrayMaterials().length;
                    eventLogger.log('BoardView', 'tray-search-input', {
                        query: target.value,
                        filter: this.trayFilter,
                        hitCount,
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
                    this.linkRelation = target.value as (typeof RELATIONS)[number];
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
                        startX: event.clientX,
                        startY: event.clientY,
                        originX: board.layout.viewport.x || 0,
                        originY: board.layout.viewport.y || 0,
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
                    startX: event.clientX,
                    startY: event.clientY,
                    originX: layout.x,
                    originY: layout.y,
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
                const board = useBoardStore.getState().board;
                if (!board) {
                    return;
                }
                const dx = event.clientX - this.viewportDragState.startX;
                const dy = event.clientY - this.viewportDragState.startY;
                const nextBoard = structuredClone(board) as BoardDocument;
                nextBoard.layout.viewport.x = Math.round(this.viewportDragState.originX + dx);
                nextBoard.layout.viewport.y = Math.round(this.viewportDragState.originY + dy);
                useBoardStore.getState().setBoard(nextBoard);
                return;
            }
            const dx = event.clientX - this.dragState.startX;
            const dy = event.clientY - this.dragState.startY;
            const board = useBoardStore.getState().board;
            if (!board) {
                return;
            }
            const zoom = board.layout.viewport.zoom || 1;
            const nextBoard = structuredClone(board) as BoardDocument;
            const layout = nextBoard.layout.cards[this.dragState.cardId];
            if (!layout) {
                return;
            }
            layout.x = Math.round(this.dragState.originX + dx / zoom);
            layout.y = Math.round(this.dragState.originY + dy / zoom);
            useBoardStore.getState().setBoard(nextBoard);
            this.render();
        };

        const upHandler = () => {
            if (!this.dragState) {
                if (!this.viewportDragState) {
                    return;
                }
                this.viewportDragState = null;
                this.scheduleSave();
                return;
            }
            this.dragState = null;
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

    private subscribeToStores(): void {
        this.unsubscribe.push(
            useBoardStore.subscribe((state, prev) => {
                if (state.board?.path !== prev.board?.path) {
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
            useDocStore.subscribe((state, prev) => {
                if (state.searchQuery !== prev.searchQuery || state.files !== prev.files) {
                    const board = useBoardStore.getState().board;
                    if (board?.path && state.files !== prev.files) {
                        this.refreshTrayMaterials(board.path);
                    }
                    if (useUIStore.getState().activeTab === 'board') {
                        this.render();
                    }
                }
            })
        );
    }

    private async loadTrayMaterials(boardPath: string): Promise<void> {
        eventLogger.log('BoardView', 'tray-load-start', { boardPath });
        const results = await Promise.allSettled([
            this.api.GetBoardResourceCandidates(boardPath),
            this.api.GetImageList(),
            this.api.GetCsvList(),
        ]);
        const [candidatesResult, imagesResult, csvsResult] = results;
        if (candidatesResult.status !== 'fulfilled') {
            console.error('Failed to load board resource candidates:', candidatesResult.reason);
            eventLogger.log('BoardView', 'tray-load-error', {
                boardPath,
                source: 'candidates',
                error: String(candidatesResult.reason),
            });
            useUIStore.getState().setStatusMessage('素材トレイのファイル候補取得に失敗しました', 2500);
            return;
        }
        const candidates = Array.isArray(candidatesResult.value) ? candidatesResult.value : [];
        const images = imagesResult.status === 'fulfilled' && Array.isArray(imagesResult.value) ? imagesResult.value : [];
        const csvs = csvsResult.status === 'fulfilled' && Array.isArray(csvsResult.value) ? csvsResult.value : [];
        if (imagesResult.status !== 'fulfilled') {
            console.error('Failed to load board image candidates:', imagesResult.reason);
            eventLogger.log('BoardView', 'tray-load-error', {
                boardPath,
                source: 'images',
                error: String(imagesResult.reason),
            });
        }
        if (csvsResult.status !== 'fulfilled') {
            console.error('Failed to load board CSV candidates:', csvsResult.reason);
            eventLogger.log('BoardView', 'tray-load-error', {
                boardPath,
                source: 'csvs',
                error: String(csvsResult.reason),
            });
        }
        this.trayImages = images;
        this.trayCsvs = csvs;
        this.refreshTrayMaterials(boardPath, candidates);
        eventLogger.log('BoardView', 'tray-load-success', {
            boardPath,
            candidateCount: candidates.length,
            imageCount: images.length,
            csvCount: csvs.length,
            docFileCount: useDocStore.getState().files.length,
            trayCount: this.trayMaterials.length,
        });
        this.render();
    }

    private refreshTrayMaterials(boardPath: string, fallbackCandidates?: FileItem[]): void {
        const docFiles = useDocStore.getState().files;
        const candidates = docFiles.length > 0
            ? docFiles.filter((file) => file.path !== boardPath)
            : (fallbackCandidates || []).filter((file) => file.path !== boardPath);
        useBoardStore.getState().setCandidateResources(candidates);
        this.trayMaterials = this.buildTrayMaterials(candidates, this.trayImages, this.trayCsvs);
        eventLogger.log('BoardView', 'tray-refresh', {
            boardPath,
            docFileCount: docFiles.length,
            fallbackCount: fallbackCandidates?.length || 0,
            candidateCount: candidates.length,
            trayCount: this.trayMaterials.length,
        });
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
                this.render();
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
                this.setBoardMode('select');
                return;
            case 'cancel-link-mode':
                this.setBoardMode('select');
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
        }
        this.render();
    }

    private enterLinkMode(): void {
        const board = useBoardStore.getState().board;
        if (!board || board.cards.length < 2) {
            useUIStore.getState().setStatusMessage('Edge を追加するにはカードが2枚以上必要です', 2000);
            return;
        }
        this.boardMode = 'link';
        this.linkState = {};
        useBoardStore.getState().setSelectedEdgeId(null);
        this.render();
    }

    private handleLinkCardSelection(cardId: string): void {
        const board = useBoardStore.getState().board;
        if (!board) {
            return;
        }
        if (!this.linkState.fromCardId) {
            this.linkState = { fromCardId: cardId };
            useBoardStore.getState().setSelectedCardId(cardId);
            this.render();
            return;
        }
        if (this.linkState.fromCardId === cardId) {
            useUIStore.getState().setStatusMessage('接続先には別のカードを選択してください', 1800);
            return;
        }
        const edgeId = this.createEdge(this.linkState.fromCardId, cardId, this.linkRelation);
        if (!edgeId) {
            return;
        }
        useBoardStore.getState().setSelectedEdgeId(edgeId);
        useBoardStore.getState().setSelectedCardId(null);
        if (this.linkContinuous) {
            this.linkState = { fromCardId: cardId };
        } else {
            this.boardMode = 'select';
            this.linkState = {};
        }
        this.scheduleSave();
        this.render();
    }

    private createEdge(fromCardId: string, toCardId: string, relation: string): string | null {
        const board = useBoardStore.getState().board;
        if (!board) {
            return null;
        }
        if (fromCardId === toCardId) {
            return null;
        }
        if (!board.cards.some((card) => card.id === fromCardId) || !board.cards.some((card) => card.id === toCardId)) {
            return null;
        }
        const existingEdge = board.edges.find((edge) => edge.from === fromCardId && edge.to === toCardId && edge.relation === relation);
        if (existingEdge) {
            useUIStore.getState().setStatusMessage('同じ関係の Edge はすでに存在します', 1800);
            return existingEdge.id;
        }
        const nextBoard = structuredClone(board) as BoardDocument;
        const edgeId = this.nextEdgeId(nextBoard);
        nextBoard.edges.push({
            id: edgeId,
            from: fromCardId,
            to: toCardId,
            relation,
            label: '',
            description: '',
        });
        useBoardStore.getState().setBoard(nextBoard);
        return edgeId;
    }

    private reverseSelectedEdge(): void {
        const board = useBoardStore.getState().board;
        const selectedEdgeId = useBoardStore.getState().selectedEdgeId;
        if (!board || !selectedEdgeId) {
            return;
        }
        const nextBoard = structuredClone(board) as BoardDocument;
        const edge = nextBoard.edges.find((item) => item.id === selectedEdgeId);
        if (!edge) {
            return;
        }
        const from = edge.from;
        edge.from = edge.to;
        edge.to = from;
        useBoardStore.getState().setBoard(nextBoard);
        this.scheduleSave();
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
        const nextBoard = structuredClone(board) as BoardDocument;
        const edge = nextBoard.edges.find((item) => item.id === selectedEdgeId);
        if (!edge) {
            return;
        }
        if (name === 'from') edge.from = value;
        if (name === 'to') edge.to = value;
        if (name === 'relation') edge.relation = value;
        if (name === 'label') edge.label = value;
        if (name === 'description') edge.description = value;
        useBoardStore.getState().setBoard(nextBoard);
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
            if (requestId !== this.latestPersistRequestId) {
                return;
            }
            this.syncSavedBoard(saved);
            useUIStore.getState().setStatusMessage('コルクボードを保存しました', 1500);
        } catch (error) {
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

    private buildTrayMaterials(candidates: FileItem[], images: ImageInfo[], csvs: CsvInfo[]): TrayMaterial[] {
        const materialMap = new Map<string, TrayMaterial>();
        candidates.forEach((candidate) => {
            const kind = classifyPath(candidate.path);
            materialMap.set(candidate.path, {
                path: candidate.path,
                title: candidate.title || candidate.path.split('/').pop() || candidate.path,
                kind,
                description: kind === 'board' ? '既存のコルクボード' : candidate.path,
                cardType: kind === 'board' ? 'board' : 'resource',
                modTime: normalizeTimestamp(candidate.modTime),
                searchText: `${candidate.title || ''} ${candidate.path} ${candidate.searchText || ''}`.trim(),
            });
        });
        images.forEach((image) => {
            materialMap.set(image.path, {
                path: image.path,
                title: image.name || image.path.split('/').pop() || image.path,
                kind: 'image',
                description: image.path,
                cardType: 'resource',
                modTime: normalizeTimestamp(image.modTime),
                searchText: `${image.name || ''} ${image.path}`.trim(),
            });
        });
        csvs.forEach((csv) => {
            materialMap.set(csv.path, {
                path: csv.path,
                title: csv.name || csv.path.split('/').pop() || csv.path,
                kind: 'csv',
                description: csv.path,
                cardType: 'resource',
                modTime: normalizeTimestamp(csv.modTime),
                searchText: `${csv.name || ''} ${csv.path}`.trim(),
            });
        });
        return Array.from(materialMap.values()).sort(compareTrayMaterials);
    }

    private getFilteredTrayMaterials(): TrayMaterial[] {
        const query = useDocStore.getState().searchQuery;
        const filteredByQuery = filterFilesByQuery(
            this.trayMaterials.map((material) => ({
                path: material.path,
                title: `${material.title} ${material.description} ${material.searchText}`,
            })),
            query
        );
        const allowedPaths = new Set(filteredByQuery.map((item) => item.path));
        return this.trayMaterials.filter((material) => {
            if (!allowedPaths.has(material.path)) {
                return false;
            }
            if (this.trayFilter === 'all') {
                return true;
            }
            return material.kind === this.trayFilter;
        }).sort(compareTrayMaterials);
    }

    private render(): void {
        if (!this.element) {
            return;
        }
        const activeElement = document.activeElement as HTMLElement | null;
        const searchInput = activeElement?.closest('input[name="boardTraySearch"]') ? activeElement as HTMLInputElement : null;
        const selectionStart = searchInput?.selectionStart ?? null;
        const selectionEnd = searchInput?.selectionEnd ?? null;
        const { board, selectedCardId, selectedEdgeId } = useBoardStore.getState();
        if (!board) {
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
                        <button class="btn${this.boardMode === 'select' ? ' is-active' : ''}" data-action="select-mode">Select</button>
                        <button class="btn${this.boardMode === 'link' ? ' is-active' : ''}" data-action="add-edge">Link</button>
                        ${this.boardMode === 'link' ? '<button class="btn" data-action="cancel-link-mode">終了</button>' : ''}
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
                        ${this.boardMode === 'link' ? `
                            <label class="board-link-label">relation
                                <select name="linkRelation">
                                    ${RELATIONS.map((relation) => `<option value="${relation}"${relation === this.linkRelation ? ' selected' : ''}>${escapeHtml(RELATION_LABELS[relation])}</option>`).join('')}
                                </select>
                            </label>
                            <label class="board-checkbox">
                                <input type="checkbox" name="linkContinuous"${this.linkContinuous ? ' checked' : ''}>
                                連続作成
                            </label>
                        ` : ''}
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
                                ${this.renderTrayFilterButton('board', 'Board')}
                            </div>
                            <div class="board-tray-list">
                                ${trayMaterials.length > 0
                                    ? trayMaterials.map((material) => this.renderTrayMaterial(material)).join('')
                                    : '<div class="board-empty-inline">該当する素材がありません</div>'}
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
                            <div class="board-canvas">
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
        if (searchInput) {
            const nextInput = this.element.querySelector<HTMLInputElement>('input[name="boardTraySearch"]');
            nextInput?.focus();
            if (nextInput && selectionStart !== null && selectionEnd !== null) {
                nextInput.setSelectionRange(selectionStart, selectionEnd);
            }
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
            return 'リンク作成中: 接続元カードを選択してください';
        }
        const fromCard = board.cards.find((card) => card.id === fromCardId);
        return `リンク作成中: 接続先カードを選択してください / 接続元: ${fromCard?.title || fromCardId}`;
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
                style="left:${box.x}px;top:${box.y}px;width:${box.width}px;height:${box.height}px;"
            >
                <div class="board-card-header">
                    <span class="board-card-type">${escapeHtml(card.type)}</span>
                    <div class="board-card-actions">
                        <button class="btn btn-small" data-action="delete-card" data-card-id="${escapeHtml(card.id)}">削除</button>
                    </div>
                </div>
                <h4>${escapeHtml(card.title || card.id)}</h4>
                <p>${escapeHtml(card.body || card.source || '')}</p>
                ${card.source ? `<div class="board-card-source">${escapeHtml(card.source)}</div>` : ''}
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
                <g class="board-edge-group">
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
            <label>from<select name="from">${options}</select></label>
            <label>to<select name="to">${toOptions}</select></label>
            <label>relation<select name="relation">${RELATIONS.map((relation) => `<option value="${relation}"${edge.relation === relation ? ' selected' : ''}>${escapeHtml(RELATION_LABELS[relation])}</option>`).join('')}</select></label>
            <label>label<input name="label" value="${escapeHtml(edge.label || '')}"></label>
            <label>description<textarea name="description" rows="4">${escapeHtml(edge.description || '')}</textarea></label>
            <button class="btn" data-action="reverse-edge">向きを反転</button>
            <button class="btn" data-action="delete-edge" data-edge-id="${escapeHtml(edge.id)}">Edge削除</button>
        `;
    }

    destroy(): void {
        if (this.saveTimer) {
            window.clearTimeout(this.saveTimer);
        }
        this.unsubscribe.forEach((unsub) => unsub());
        this.unsubscribe = [];
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

function classifyPath(path: string): TrayMaterial['kind'] {
    const normalized = path.toLowerCase();
    if (normalized.endsWith('.board.md')) {
        return 'board';
    }
    if (normalized.endsWith('.md')) {
        return 'markdown';
    }
    return 'other';
}

function normalizeTimestamp(value: unknown): string {
    if (typeof value !== 'string' || !value) {
        return '';
    }
    return value;
}

function compareTrayMaterials(a: TrayMaterial, b: TrayMaterial): number {
    const timeDelta = parseMaterialTime(b.modTime) - parseMaterialTime(a.modTime);
    if (timeDelta !== 0) {
        return timeDelta;
    }
    return a.title.localeCompare(b.title, 'ja');
}

function parseMaterialTime(value: string): number {
    if (!value) {
        return 0;
    }
    const parsed = Date.parse(value);
    return Number.isNaN(parsed) ? 0 : parsed;
}
