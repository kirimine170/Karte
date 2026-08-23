import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { BoardView } from '../board-view';
import { useBoardStore, useDocStore, useUIStore } from '../../stores/index';
import type { BoardDocument, WailsAppAPI } from '../../types/wails-api';

const boardDocument: BoardDocument = {
    path: 'content/clips/tv-kamitsubaki-studio-cg-1.board.md',
    title: 'tv-kamitsubaki-studio-cg-1',
    docId: 'board:test',
    type: 'karte-board',
    version: 1,
    created: '2026-06-08',
    updated: '2026-06-08',
    tags: ['corkboard'],
    cards: [
        {
            id: 'card:resource-001',
            type: 'resource',
            title: 'tv-kamitsubaki-studio-cg-1',
            source: 'content/clips/tv-kamitsubaki-studio-cg-1.md',
            tags: [],
            createdBy: 'user',
            body: 'Linked resource',
            meta: {},
        },
        {
            id: 'card:resource-002',
            type: 'resource',
            title: 'tv-kamitsubaki-studio-cg-2',
            source: 'content/clips/tv-kamitsubaki-studio-cg-2.md',
            tags: [],
            createdBy: 'user',
            body: 'Second resource',
            meta: {},
        },
    ],
    edges: [],
    layout: {
        cards: {
            'card:resource-001': { x: 120, y: 80, width: 280, height: 160 },
            'card:resource-002': { x: 520, y: 180, width: 280, height: 160 },
        },
        viewport: { x: 0, y: 0, zoom: 1 },
    },
    notes: '',
    rawContent: '# Board',
};

const mockApi: Pick<WailsAppAPI, 'GetBoardResourceCandidates' | 'GetImageList' | 'GetCsvList' | 'LoadBoard' | 'SaveBoard'> = {
    GetBoardResourceCandidates: vi.fn().mockResolvedValue([]),
    GetImageList: vi.fn().mockResolvedValue([]),
    GetCsvList: vi.fn().mockResolvedValue([]),
    LoadBoard: vi.fn().mockResolvedValue(boardDocument),
    SaveBoard: vi.fn().mockImplementation(async (_path: string, board: BoardDocument) => board),
};

describe('BoardView tray search', () => {
    beforeEach(() => {
        vi.clearAllMocks();
        useUIStore.setState({
            sidebarVisible: true,
            imageGalleryVisible: true,
            csvGalleryVisible: true,
            workspaceMode: false,
            activeTab: 'board',
            theme: 'light',
            hardWrap: false,
            statusMessage: '',
            statusClearTimer: null,
        });

        useDocStore.setState({
            files: [
                {
                    path: 'content/clips/tv-kamitsubaki-studio-cg-1.md',
                    title: 'TVアニメ『神椿市建設中。』 KAMITSUBAKI STUDIO原作のオリジナルCGアニメに迫る〜（1）キャラクター篇',
                    searchText: '本文にも神椿が含まれる',
                    modTime: '2026-06-08T00:00:00.000Z',
                },
                {
                    path: 'content/clips/tv-kamitsubaki-studio-cg-2.md',
                    title: 'TVアニメ『神椿市建設中。』 KAMITSUBAKI STUDIO原作のオリジナルCGアニメに迫る〜（2）リギング篇',
                    searchText: '別記事',
                    modTime: '2026-06-07T00:00:00.000Z',
                },
                {
                    path: 'content/notes/other.md',
                    title: 'Other note',
                    searchText: 'no hit',
                    modTime: '2026-06-06T00:00:00.000Z',
                },
            ],
            currentPath: boardDocument.path,
            markdownContent: boardDocument.rawContent,
            previewHtml: '',
            hasUnsavedChanges: false,
            searchQuery: '',
        });

        useBoardStore.getState().clear();
        useBoardStore.getState().setBoard(structuredClone(boardDocument));

        document.body.innerHTML = `
            <div id="board-tab"></div>
        `;
    });

    afterEach(() => {
        document.body.innerHTML = '';
    });

    it('renders markdown tray items from doc store files even when board candidate API is empty', async () => {
        const boardView = new BoardView(mockApi as WailsAppAPI);
        boardView.init();
        await flushAsync();

        const trayItems = Array.from(document.querySelectorAll('.board-tray-item h4')).map((node) => node.textContent?.trim());
        expect(trayItems).toContain('TVアニメ『神椿市建設中。』 KAMITSUBAKI STUDIO原作のオリジナルCGアニメに迫る〜（1）キャラクター篇');
        expect(trayItems).toContain('TVアニメ『神椿市建設中。』 KAMITSUBAKI STUDIO原作のオリジナルCGアニメに迫る〜（2）リギング篇');
        expect(mockApi.GetBoardResourceCandidates).toHaveBeenCalledTimes(1);

        boardView.destroy();
    });

    it('filters markdown tray items by the board search input', async () => {
        const boardView = new BoardView(mockApi as WailsAppAPI);
        boardView.init();
        await flushAsync();

        const filterButton = document.querySelector<HTMLButtonElement>('.board-tray-filter[data-filter="markdown"]');
        filterButton?.click();

        const searchInput = document.querySelector<HTMLInputElement>('input[name="boardTraySearch"]');
        expect(searchInput).toBeTruthy();
        searchInput!.value = '神椿';
        searchInput!.dispatchEvent(new Event('input', { bubbles: true }));
        await flushAsync();

        const trayItems = Array.from(document.querySelectorAll('.board-tray-item h4')).map((node) => node.textContent?.trim());
        expect(trayItems).toHaveLength(2);
        expect(document.querySelector('.board-tray-list .board-empty-inline')).toBeNull();

        const refreshedSearchInput = document.querySelector<HTMLInputElement>('input[name="boardTraySearch"]');
        refreshedSearchInput!.value = '（1）キャラクター篇';
        refreshedSearchInput!.dispatchEvent(new Event('input', { bubbles: true }));
        await flushAsync();

        const narrowedItems = Array.from(document.querySelectorAll('.board-tray-item h4')).map((node) => node.textContent?.trim());
        expect(narrowedItems).toEqual([
            'TVアニメ『神椿市建設中。』 KAMITSUBAKI STUDIO原作のオリジナルCGアニメに迫る〜（1）キャラクター篇',
        ]);

        boardView.destroy();
    });

    it('creates an edge through link mode card selection', async () => {
        const boardView = new BoardView(mockApi as WailsAppAPI);
        boardView.init();
        await flushAsync();

        const linkButton = Array.from(document.querySelectorAll<HTMLButtonElement>('.board-toolbar .btn'))
            .find((button) => button.textContent?.trim() === 'Link');
        linkButton?.click();
        await flushAsync();

        const relationSelect = document.querySelector<HTMLSelectElement>('select[name="linkRelation"]');
        expect(relationSelect).toBeTruthy();
        relationSelect!.value = 'supports';
        relationSelect!.dispatchEvent(new Event('input', { bubbles: true }));
        await flushAsync();

        const cards = document.querySelectorAll<HTMLElement>('.board-card');
        expect(cards).toHaveLength(2);
        cards[0].click();
        await flushAsync();

        const refreshedCards = document.querySelectorAll<HTMLElement>('.board-card');
        refreshedCards[1].click();
        await flushAsync();

        const board = useBoardStore.getState().board;
        expect(board?.edges).toHaveLength(1);
        expect(board?.edges[0]).toMatchObject({
            from: 'card:resource-001',
            to: 'card:resource-002',
            relation: 'supports',
        });
        expect(document.querySelector('select[name="linkRelation"]')).toBeNull();

        boardView.destroy();
    });
});

describe('BoardView pointer drag scheduling', () => {
    let boardView: BoardView | null = null;
    let nextAnimationFrameId = 1;
    let pendingAnimationFrames: Map<number, FrameRequestCallback>;
    let cancelledAnimationFrames: Map<number, FrameRequestCallback>;

    beforeEach(() => {
        vi.clearAllMocks();
        pendingAnimationFrames = new Map();
        cancelledAnimationFrames = new Map();
        nextAnimationFrameId = 1;
        vi.stubGlobal(
            'requestAnimationFrame',
            vi.fn((callback: FrameRequestCallback) => {
                const id = nextAnimationFrameId;
                nextAnimationFrameId += 1;
                pendingAnimationFrames.set(id, callback);
                return id;
            })
        );
        vi.stubGlobal(
            'cancelAnimationFrame',
            vi.fn((id: number) => {
                const callback = pendingAnimationFrames.get(id);
                if (callback) {
                    cancelledAnimationFrames.set(id, callback);
                }
                pendingAnimationFrames.delete(id);
            })
        );
    });

    afterEach(() => {
        boardView?.destroy();
        boardView = null;
        vi.useRealTimers();
        vi.unstubAllGlobals();
        vi.restoreAllMocks();
        document.body.innerHTML = '';
    });

    it('coalesces a large-board pointermove burst into one target-only frame and one commit', async () => {
        const largeBoard = createLargeBoard(250);
        largeBoard.edges = [{
            id: 'edge:drag-test',
            from: 'card:resource-001',
            to: 'card:resource-002',
            relation: 'related_to',
            label: '',
            description: '',
        }];
        boardView = await initPointerBoard(largeBoard);

        pointer(document.querySelector<HTMLElement>('[data-card-id="card:resource-001"]'), 'pointerdown', 100, 200);
        const shell = document.querySelector('.board-shell');
        const draggedCard = document.querySelector<HTMLElement>('[data-card-id="card:resource-001"]');
        const untouchedCards = Array.from(document.querySelectorAll<HTMLElement>('.board-card'))
            .filter((card) => card.dataset.cardId !== 'card:resource-001');
        const untouchedStyles = untouchedCards.map((card) => card.getAttribute('style'));
        const boardBeforeMove = useBoardStore.getState().board!;
        const committedBoards: BoardDocument[] = [];
        const unsubscribe = useBoardStore.subscribe((state, previous) => {
            if (state.board && state.board !== previous.board) {
                committedBoards.push(state.board as BoardDocument);
            }
        });
        const structuredCloneSpy = vi.spyOn(globalThis, 'structuredClone');

        for (let index = 1; index <= 50; index += 1) {
            pointer(window, 'pointermove', 100 + index, 200 + index * 2);
        }

        expect(requestAnimationFrame).toHaveBeenCalledOnce();
        expect(pendingAnimationFrames).toHaveLength(1);
        expect(structuredCloneSpy).not.toHaveBeenCalled();
        expect(useBoardStore.getState().board).toBe(boardBeforeMove);
        expect(draggedCard?.style.left).toBe('120px');

        runAllAnimationFrames(pendingAnimationFrames);

        expect(document.querySelector('.board-shell')).toBe(shell);
        expect(document.querySelector('[data-card-id="card:resource-001"]')).toBe(draggedCard);
        untouchedCards.forEach((card, index) => {
            expect(document.querySelector(`[data-card-id="${card.dataset.cardId}"]`)).toBe(card);
            expect(card.getAttribute('style')).toBe(untouchedStyles[index]);
        });
        expect(draggedCard?.style.left).toBe('170px');
        expect(draggedCard?.style.top).toBe('180px');
        expect(document.querySelector('.board-edge-line')?.getAttribute('x1')).toBe('310');
        expect(useBoardStore.getState().board).toBe(boardBeforeMove);
        expect(committedBoards).toHaveLength(0);

        pointer(window, 'pointerup', 150, 300);

        const committed = useBoardStore.getState().board!;
        expect(committedBoards).toHaveLength(1);
        expect(committed.layout.cards['card:resource-001']).toMatchObject({ x: 170, y: 180 });
        expect(committed.cards).toBe(boardBeforeMove.cards);
        expect(committed.layout.cards['card:resource-002']).toBe(
            boardBeforeMove.layout.cards['card:resource-002']
        );
        expect(structuredCloneSpy).not.toHaveBeenCalled();

        unsubscribe();
    });

    it('flushes the latest position on pointerup，persists it，and restores the start snapshot as undo', async () => {
        boardView = await initPointerBoard(structuredClone(boardDocument));
        const startSnapshot = structuredClone(useBoardStore.getState().board) as BoardDocument;
        const committedBoards: BoardDocument[] = [];
        const unsubscribe = useBoardStore.subscribe((state, previous) => {
            if (state.board && state.board !== previous.board) {
                committedBoards.push(state.board as BoardDocument);
            }
        });
        vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });

        pointer(document.querySelector<HTMLElement>('[data-card-id="card:resource-001"]'), 'pointerdown', 10, 20);
        pointer(window, 'pointermove', 70, 90);
        expect(pendingAnimationFrames).toHaveLength(1);

        pointer(window, 'pointerup', 70, 90);

        expect(pendingAnimationFrames).toHaveLength(0);
        expect(cancelledAnimationFrames).toHaveLength(1);
        expect(committedBoards).toHaveLength(1);
        expect(useBoardStore.getState().board?.layout.cards['card:resource-001']).toMatchObject({
            x: 180,
            y: 150,
        });

        await vi.advanceTimersByTimeAsync(250);

        expect(mockApi.SaveBoard).toHaveBeenCalledOnce();
        const savedBoard = vi.mocked(mockApi.SaveBoard).mock.calls[0]?.[1];
        expect(savedBoard?.layout.cards['card:resource-001']).toMatchObject({ x: 180, y: 150 });

        useBoardStore.getState().setBoard(startSnapshot);

        expect(useBoardStore.getState().board?.layout.cards['card:resource-001']).toMatchObject({
            x: 120,
            y: 80,
        });
        expect(document.querySelector<HTMLElement>('[data-card-id="card:resource-001"]')?.style.left).toBe('120px');

        unsubscribe();
    });

    it('coalesces viewport coordinates without rebuilding cards', async () => {
        boardView = await initPointerBoard(structuredClone(boardDocument));
        const wrapper = document.querySelector<HTMLElement>('.board-canvas-wrapper');
        pointer(wrapper, 'pointerdown', 10, 20);
        const scene = document.querySelector<HTMLElement>('.board-scene');
        const cards = Array.from(document.querySelectorAll<HTMLElement>('.board-card'));
        const boardBeforeMove = useBoardStore.getState().board;

        pointer(window, 'pointermove', 20, 30);
        pointer(window, 'pointermove', 40, 60);
        pointer(window, 'pointermove', 70, 100);

        expect(requestAnimationFrame).toHaveBeenCalledOnce();
        expect(useBoardStore.getState().board).toBe(boardBeforeMove);
        runAllAnimationFrames(pendingAnimationFrames);
        expect(document.querySelector('.board-scene')).toBe(scene);
        expect(scene?.style.transform).toBe('translate(60px, 80px) scale(1)');
        cards.forEach((card) => {
            expect(document.querySelector(`[data-card-id="${card.dataset.cardId}"]`)).toBe(card);
        });

        pointer(window, 'pointerup', 70, 100);

        expect(useBoardStore.getState().board?.layout.viewport).toMatchObject({ x: 60, y: 80, zoom: 1 });
    });

    it('cancels the pending frame and window listeners on destroy', async () => {
        boardView = await initPointerBoard(structuredClone(boardDocument));
        pointer(document.querySelector<HTMLElement>('[data-card-id="card:resource-001"]'), 'pointerdown', 10, 20);
        const draggedCard = document.querySelector<HTMLElement>('[data-card-id="card:resource-001"]');
        const boardBeforeMove = useBoardStore.getState().board;
        pointer(window, 'pointermove', 70, 90);
        const cancelledCallback = Array.from(pendingAnimationFrames.values())[0];
        const requestCountBeforeDestroy = vi.mocked(requestAnimationFrame).mock.calls.length;

        boardView.destroy();
        boardView = null;

        expect(pendingAnimationFrames).toHaveLength(0);
        expect(cancelledAnimationFrames).toHaveLength(1);
        cancelledCallback?.(0);
        pointer(window, 'pointermove', 90, 110);
        pointer(window, 'pointerup', 90, 110);

        expect(vi.mocked(requestAnimationFrame).mock.calls).toHaveLength(requestCountBeforeDestroy);
        expect(useBoardStore.getState().board).toBe(boardBeforeMove);
        expect(draggedCard?.style.left).toBe('120px');
        expect(mockApi.SaveBoard).not.toHaveBeenCalled();
    });
});

async function initPointerBoard(board: BoardDocument): Promise<BoardView> {
    useUIStore.setState({
        sidebarVisible: true,
        imageGalleryVisible: true,
        csvGalleryVisible: true,
        workspaceMode: false,
        activeTab: 'board',
        theme: 'light',
        hardWrap: false,
        statusMessage: '',
        statusClearTimer: null,
    });
    useDocStore.setState({
        files: [],
        currentPath: board.path,
        markdownContent: board.rawContent,
        previewHtml: '',
        hasUnsavedChanges: false,
        searchQuery: '',
    });
    useBoardStore.getState().clear();
    useBoardStore.getState().setBoard(board);
    document.body.innerHTML = '<div id="board-tab"></div>';
    const view = new BoardView(mockApi as WailsAppAPI);
    view.init();
    await flushAsync();
    vi.mocked(mockApi.SaveBoard).mockClear();
    vi.mocked(requestAnimationFrame).mockClear();
    vi.mocked(cancelAnimationFrame).mockClear();
    return view;
}

function createLargeBoard(cardCount: number): BoardDocument {
    const board = structuredClone(boardDocument);
    for (let index = board.cards.length; index < cardCount; index += 1) {
        const id = `card:resource-${String(index + 1).padStart(3, '0')}`;
        board.cards.push({
            id,
            type: 'resource',
            title: `Resource ${index + 1}`,
            source: `content/resource-${index + 1}.md`,
            tags: [],
            createdBy: 'user',
            body: '',
            meta: {},
        });
        board.layout.cards[id] = {
            x: 40 + (index % 20) * 40,
            y: 40 + Math.floor(index / 20) * 40,
            width: 280,
            height: 160,
        };
    }
    return board;
}

function pointer(
    target: EventTarget | null,
    type: 'pointerdown' | 'pointermove' | 'pointerup',
    clientX: number,
    clientY: number
): void {
    if (!target) {
        throw new Error(`Missing pointer target for ${type}`);
    }
    target.dispatchEvent(new MouseEvent(type, { bubbles: true, clientX, clientY }));
}

function runAllAnimationFrames(frames: Map<number, FrameRequestCallback>): void {
    const callbacks = Array.from(frames.values());
    frames.clear();
    callbacks.forEach((callback) => callback(0));
}

async function flushAsync(): Promise<void> {
    await Promise.resolve();
    await new Promise((resolve) => setTimeout(resolve, 0));
}
