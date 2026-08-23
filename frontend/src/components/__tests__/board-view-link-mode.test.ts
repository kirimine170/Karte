import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { BoardView } from '../board-view';
import { useBoardStore, useDocStore, useUIStore } from '../../stores/index';
import type {
    BoardDocument,
    BoardEdgeRecord,
    ResourceSearchRequest,
    WailsAppAPI,
} from '../../types/wails-api';

const BOARD_PATH = 'content/boards/link-mode.board.md';

const mockApi = {
    SearchResources: vi.fn(async (request: ResourceSearchRequest) => ({
        items: [],
        query: request.query,
        kinds: request.kinds,
        page: request.page,
        limit: request.limit,
        total: 0,
        hasMore: false,
    })),
    LoadBoard: vi.fn(async () => createBoard()),
    SaveBoard: vi.fn(async (_path: string, board: BoardDocument) => board),
} as unknown as WailsAppAPI;

describe('BoardView link mode interaction contract', () => {
    let view: BoardView | null = null;

    beforeEach(() => {
        vi.useFakeTimers();
        vi.clearAllMocks();
        useUIStore.setState({
            activeTab: 'board',
            statusMessage: '',
            statusClearTimer: null,
        });
        useDocStore.setState({
            files: [],
            currentPath: BOARD_PATH,
            markdownContent: '',
            previewHtml: '',
            hasUnsavedChanges: false,
            searchQuery: '',
        });
        useBoardStore.getState().clear();
        document.body.innerHTML = '<div id="board-tab"></div>';
    });

    afterEach(() => {
        view?.destroy();
        view = null;
        const statusTimer = useUIStore.getState().statusClearTimer;
        if (statusTimer) {
            window.clearTimeout(statusTimer);
        }
        useUIStore.setState({ statusMessage: '', statusClearTimer: null });
        vi.clearAllTimers();
        vi.useRealTimers();
        document.body.innerHTML = '';
    });

    it('uses one accessible activation path for keyboard selection and preserves native Tab', async () => {
        await initView(createBoard());

        expect(document.querySelector('.board-canvas')?.getAttribute('role')).toBe('list');
        expect(cards()).toHaveLength(3);
        cards().forEach((card) => {
            expect(card.tabIndex).toBe(0);
            expect(card.getAttribute('role')).toBe('button');
            expect(card.getAttribute('aria-pressed')).toBe('false');
            expect(card.getAttribute('aria-label')).toContain('EnterまたはSpace');
        });
        const firstCardContainer = card('card:a').closest<HTMLElement>('.board-card')!;
        const deleteButton = firstCardContainer.querySelector<HTMLButtonElement>('[data-action="delete-card"]')!;
        expect(firstCardContainer.getAttribute('role')).toBe('listitem');
        expect(card('card:a').contains(deleteButton)).toBe(false);

        action('add-edge').click();
        card('card:a').focus();
        const arrowEvent = key(card('card:a'), 'ArrowRight');
        expect(arrowEvent.defaultPrevented).toBe(true);
        expect((document.activeElement as HTMLElement).dataset.cardId).toBe('card:b');

        const tabEvent = key(card('card:b'), 'Tab');
        expect(tabEvent.defaultPrevented).toBe(false);
        expect(useBoardStore.getState().selectedCardId).toBeNull();

        key(card('card:a'), 'Enter', { repeat: true });
        expect(useBoardStore.getState().selectedCardId).toBeNull();
        key(card('card:a'), 'Enter');
        expect(useBoardStore.getState().selectedCardId).toBe('card:a');
        expect(card('card:a').getAttribute('aria-pressed')).toBe('true');
        expect(card('card:b').getAttribute('aria-pressed')).toBe('false');
        expect(linkStatus()).toContain('接続元: Alpha');
        expect(linkStatus()).toContain('接続先: 未選択');
        expect(linkStatus()).toContain('relation: 関連');

        const boardBeforeTarget = useBoardStore.getState().board;
        key(card('card:b'), 'Enter', { repeat: true });
        expect(useBoardStore.getState().board).toBe(boardBeforeTarget);
        key(card('card:b'), 'Enter');

        expect(useBoardStore.getState().board?.edges).toEqual([
            expect.objectContaining({ from: 'card:a', to: 'card:b', relation: 'related_to' }),
        ]);
        expect(document.activeElement).toBe(action('add-edge'));
        expect(action('add-edge').getAttribute('aria-pressed')).toBe('false');

        await vi.advanceTimersByTimeAsync(250);
        expect(mockApi.SaveBoard).toHaveBeenCalledOnce();
    });

    it('keeps continuous pointer and keyboard creation ordered with explicit endpoints', async () => {
        await initView(createBoard());
        action('add-edge').click();

        const relation = document.querySelector<HTMLSelectElement>('select[name="linkRelation"]')!;
        relation.value = 'supports';
        relation.dispatchEvent(new Event('input', { bubbles: true }));
        const continuous = document.querySelector<HTMLInputElement>('input[name="linkContinuous"]')!;
        continuous.checked = true;
        continuous.dispatchEvent(new Event('change', { bubbles: true }));

        card('card:a').click();
        card('card:b').click();
        expect(document.querySelector('select[name="linkRelation"]')).not.toBeNull();
        expect(useBoardStore.getState().selectedCardId).toBe('card:b');
        expect(document.activeElement).toBe(card('card:b'));

        card('card:c').focus();
        key(card('card:c'), ' ');

        expect(useBoardStore.getState().board?.edges).toEqual([
            expect.objectContaining({ from: 'card:a', to: 'card:b', relation: 'supports' }),
            expect.objectContaining({ from: 'card:b', to: 'card:c', relation: 'supports' }),
        ]);
        expect(useBoardStore.getState().selectedCardId).toBe('card:c');
        expect(linkStatus()).toContain('BetaからGammaへ');
        expect(linkStatus()).toContain('支持');
        expect(document.activeElement).toBe(card('card:c'));

        await vi.advanceTimersByTimeAsync(250);
        expect(mockApi.SaveBoard).toHaveBeenCalledOnce();
    });

    it('undoes continuous links one committed edge at a time in LIFO order', async () => {
        await initView(createBoard());
        action('add-edge').click();
        const continuous = document.querySelector<HTMLInputElement>('input[name="linkContinuous"]')!;
        continuous.checked = true;
        continuous.dispatchEvent(new Event('change', { bubbles: true }));
        card('card:a').click();
        card('card:b').click();
        card('card:c').click();
        await vi.advanceTimersByTimeAsync(250);
        expect(mockApi.SaveBoard).toHaveBeenCalledOnce();

        let boardCommits = 0;
        const unsubscribe = useBoardStore.subscribe((state, previous) => {
            if (state.board !== previous.board) boardCommits += 1;
        });

        action('undo-link-creation').click();
        expect(useBoardStore.getState().board?.edges).toEqual([
            expect.objectContaining({ from: 'card:a', to: 'card:b' }),
        ]);
        expect(boardCommits).toBe(1);
        await vi.advanceTimersByTimeAsync(250);
        expect(mockApi.SaveBoard).toHaveBeenCalledTimes(2);
        expect(vi.mocked(mockApi.SaveBoard).mock.calls[1]?.[1].edges).toHaveLength(1);

        action('undo-link-creation').click();
        expect(useBoardStore.getState().board?.edges).toHaveLength(0);
        expect(boardCommits).toBe(2);
        await vi.advanceTimersByTimeAsync(250);
        expect(mockApi.SaveBoard).toHaveBeenCalledTimes(3);
        expect(vi.mocked(mockApi.SaveBoard).mock.calls[2]?.[1].edges).toHaveLength(0);

        const thirdUndo = key(action('add-edge'), 'z', { ctrlKey: true });
        expect(thirdUndo.defaultPrevented).toBe(false);
        expect(boardCommits).toBe(2);
        await vi.advanceTimersByTimeAsync(300);
        expect(mockApi.SaveBoard).toHaveBeenCalledTimes(3);
        unsubscribe();
    });

    it('restores focus to relation and continuous controls after their rerenders', async () => {
        await initView(createBoard());
        action('add-edge').click();

        const relation = document.querySelector<HTMLSelectElement>('select[name="linkRelation"]')!;
        relation.focus();
        relation.value = 'depends_on';
        relation.dispatchEvent(new Event('input', { bubbles: true }));
        expect(document.activeElement).toBe(document.querySelector('select[name="linkRelation"]'));
        expect(linkStatus()).toContain('relation: 依存');

        const continuous = document.querySelector<HTMLInputElement>('input[name="linkContinuous"]')!;
        continuous.focus();
        continuous.checked = true;
        continuous.dispatchEvent(new Event('change', { bubbles: true }));
        expect(document.activeElement).toBe(document.querySelector('input[name="linkContinuous"]'));
    });

    it('cancels only the pending source when that source is activated again', async () => {
        await initView(createBoard());
        action('add-edge').click();
        card('card:a').click();
        const boardWithPendingSource = useBoardStore.getState().board;
        let boardMutations = 0;
        const unsubscribe = useBoardStore.subscribe((state, previous) => {
            if (state.board !== previous.board) boardMutations += 1;
        });

        card('card:a').click();

        expect(useBoardStore.getState().board).toBe(boardWithPendingSource);
        expect(useBoardStore.getState().selectedCardId).toBeNull();
        expect(document.querySelector('select[name="linkRelation"]')).not.toBeNull();
        expect(linkStatus()).toContain('接続元の選択を取り消しました');
        expect(boardMutations).toBe(0);
        expect(action('undo-link-creation').disabled).toBe(true);
        await vi.advanceTimersByTimeAsync(300);
        expect(mockApi.SaveBoard).not.toHaveBeenCalled();
        unsubscribe();
    });

    it('moves focus in four spatial directions with a stable card-ID tie break', async () => {
        await initView(createDirectionalBoard());
        const center = card('card:center');

        center.focus();
        key(center, 'ArrowLeft');
        expect((document.activeElement as HTMLElement).dataset.cardId).toBe('card:left');
        center.focus();
        key(center, 'ArrowRight');
        expect((document.activeElement as HTMLElement).dataset.cardId).toBe('card:right-a');
        center.focus();
        key(center, 'ArrowUp');
        expect((document.activeElement as HTMLElement).dataset.cardId).toBe('card:up');
        center.focus();
        key(center, 'ArrowDown');
        expect((document.activeElement as HTMLElement).dataset.cardId).toBe('card:down');
    });

    it('rejects a duplicate target without board，selection，history，or save changes', async () => {
        const board = createBoard([
            edge('edge:existing', 'card:a', 'card:b', 'related_to'),
        ]);
        await initView(board);
        action('add-edge').click();
        card('card:a').click();

        const boardBeforeTarget = useBoardStore.getState().board;
        const selectionBeforeTarget = selectionSnapshot();
        let boardMutations = 0;
        const unsubscribe = useBoardStore.subscribe((state, previous) => {
            if (state.board !== previous.board) boardMutations += 1;
        });

        card('card:b').click();

        expect(useBoardStore.getState().board).toBe(boardBeforeTarget);
        expect(selectionSnapshot()).toEqual(selectionBeforeTarget);
        expect(boardMutations).toBe(0);
        expect(action('undo-link-creation').disabled).toBe(true);
        expect(linkStatus()).toContain('すでに存在します');
        await vi.advanceTimersByTimeAsync(300);
        expect(mockApi.SaveBoard).not.toHaveBeenCalled();
        unsubscribe();
    });

    it('rejects self，missing，duplicate inspector edits and invalid reverse with zero side effects', async () => {
        const board = createBoard([
            edge('edge:forward', 'card:a', 'card:b', 'supports'),
            edge('edge:reverse', 'card:b', 'card:a', 'supports'),
            edge('edge:reference', 'card:a', 'card:b', 'references'),
        ]);
        await initView(board);
        let boardMutations = 0;
        const unsubscribe = useBoardStore.subscribe((state, previous) => {
            if (state.board !== previous.board) boardMutations += 1;
        });

        selectEdge('edge:forward');
        assertRejectedMutation(() => action('reverse-edge').click(), 'edge:forward');

        selectEdge('edge:forward');
        assertRejectedMutation(() => {
            const to = edgeField<HTMLSelectElement>('to');
            to.value = 'card:a';
            to.dispatchEvent(new Event('input', { bubbles: true }));
        }, 'edge:forward');

        selectEdge('edge:reference');
        assertRejectedMutation(() => {
            const relation = edgeField<HTMLSelectElement>('relation');
            relation.value = 'supports';
            relation.dispatchEvent(new Event('input', { bubbles: true }));
        }, 'edge:reference');

        selectEdge('edge:forward');
        assertRejectedMutation(() => {
            const from = edgeField<HTMLSelectElement>('from');
            from.append(new Option('Missing', 'card:missing'));
            from.value = 'card:missing';
            from.dispatchEvent(new Event('input', { bubbles: true }));
        }, 'edge:forward');

        expect(boardMutations).toBe(0);
        expect(action('undo-link-creation').disabled).toBe(true);
        await vi.advanceTimersByTimeAsync(300);
        expect(mockApi.SaveBoard).not.toHaveBeenCalled();
        unsubscribe();
    });

    it('cancels in two Escape steps and restores focus safely when the source disappears', async () => {
        await initView(createBoard());
        action('add-edge').click();
        card('card:a').focus();
        key(card('card:a'), 'Enter');

        key(card('card:a'), 'Escape');
        expect(document.querySelector('select[name="linkRelation"]')).not.toBeNull();
        expect(useBoardStore.getState().selectedCardId).toBeNull();
        expect(document.activeElement).toBe(card('card:a'));

        key(card('card:a'), 'Escape');
        expect(document.querySelector('select[name="linkRelation"]')).toBeNull();
        expect(document.activeElement).toBe(action('add-edge'));

        action('add-edge').click();
        card('card:a').focus();
        key(card('card:a'), 'Enter');
        const withoutSource = removeCard(useBoardStore.getState().board!, 'card:a');
        useBoardStore.setState({ board: withoutSource });

        expect(document.activeElement).toBe(card('card:b'));
        const boardBeforeMissingTarget = useBoardStore.getState().board;
        const selectionBeforeMissingTarget = selectionSnapshot();
        key(card('card:b'), 'Enter');
        expect(useBoardStore.getState().board).toBe(boardBeforeMissingTarget);
        expect(selectionSnapshot()).toEqual(selectionBeforeMissingTarget);
        expect(linkStatus()).toContain('見つかりません');
        expect(action('undo-link-creation').disabled).toBe(true);
        await vi.advanceTimersByTimeAsync(300);
        expect(mockApi.SaveBoard).not.toHaveBeenCalled();
    });

    it('does not steal editable undo and ignores repeated link undo shortcuts', async () => {
        await initView(createBoard());
        createLinkByClick('card:a', 'card:b');

        const label = edgeField<HTMLInputElement>('label');
        label.focus();
        const editableUndo = key(label, 'z', { ctrlKey: true });
        expect(editableUndo.defaultPrevented).toBe(false);
        expect(useBoardStore.getState().board?.edges).toHaveLength(1);

        action('add-edge').focus();
        key(action('add-edge'), 'z', { ctrlKey: true, repeat: true });
        expect(useBoardStore.getState().board?.edges).toHaveLength(1);
        expect(action('undo-link-creation').disabled).toBe(false);

        key(action('add-edge'), 'z', { ctrlKey: true });
        expect(useBoardStore.getState().board?.edges).toHaveLength(0);
        expect(action('undo-link-creation').disabled).toBe(true);
        expect(document.activeElement).toBe(action('add-edge'));

        await vi.advanceTimersByTimeAsync(250);
        expect(mockApi.SaveBoard).toHaveBeenCalledOnce();
        expect(vi.mocked(mockApi.SaveBoard).mock.calls[0]?.[1].edges).toHaveLength(0);
    });

    it('refuses to delete a same-ID edge whose identity changed externally', async () => {
        await initView(createBoard());
        createLinkByClick('card:a', 'card:b');
        await vi.advanceTimersByTimeAsync(250);
        vi.mocked(mockApi.SaveBoard).mockClear();

        const current = useBoardStore.getState().board!;
        const externallyChanged: BoardDocument = {
            ...current,
            edges: current.edges.map((item) => ({ ...item, label: 'external change' })),
        };
        useBoardStore.setState({ board: externallyChanged });
        action('add-edge').focus();
        key(action('add-edge'), 'z', { metaKey: true });

        expect(useBoardStore.getState().board).toBe(externallyChanged);
        expect(useBoardStore.getState().board?.edges).toEqual([
            expect.objectContaining({ id: 'edge:001', label: 'external change' }),
        ]);
        expect(action('undo-link-creation').disabled).toBe(true);
        expect(linkStatus()).toContain('変更されています');
        await vi.advanceTimersByTimeAsync(300);
        expect(mockApi.SaveBoard).not.toHaveBeenCalled();
    });

    it('scopes link undo to the board path', async () => {
        await initView(createBoard());
        createLinkByClick('card:a', 'card:b');
        await vi.advanceTimersByTimeAsync(250);
        const originalWithLink = structuredClone(useBoardStore.getState().board) as BoardDocument;

        const otherBoard = createBoard();
        otherBoard.path = 'content/boards/other.board.md';
        useBoardStore.setState({ board: otherBoard });
        useBoardStore.setState({ board: originalWithLink });

        expect(action('undo-link-creation').disabled).toBe(true);
        const shortcut = key(action('add-edge'), 'z', { ctrlKey: true });
        expect(shortcut.defaultPrevented).toBe(false);
        expect(useBoardStore.getState().board?.edges).toHaveLength(1);
    });

    it('removes delegated and window side effects on destroy，including a pending save', async () => {
        await initView(createBoard());
        createLinkByClick('card:a', 'card:b');
        const root = document.getElementById('board-tab')!;
        const staleUndo = action('undo-link-creation');
        const staleCard = card('card:c');
        const boardBeforeDestroy = useBoardStore.getState().board;
        const selectionBeforeDestroy = selectionSnapshot();
        vi.mocked(mockApi.SaveBoard).mockClear();
        let boardMutations = 0;
        const unsubscribe = useBoardStore.subscribe((state, previous) => {
            if (state.board !== previous.board) boardMutations += 1;
        });

        view!.destroy();
        view = null;
        staleUndo.click();
        staleCard.click();
        key(root, 'z', { ctrlKey: true });
        window.dispatchEvent(new MouseEvent('pointerup', { bubbles: true }));
        await vi.advanceTimersByTimeAsync(300);

        expect(useBoardStore.getState().board).toBe(boardBeforeDestroy);
        expect(selectionSnapshot()).toEqual(selectionBeforeDestroy);
        expect(boardMutations).toBe(0);
        expect(mockApi.SaveBoard).not.toHaveBeenCalled();
        unsubscribe();
    });

    async function initView(board: BoardDocument): Promise<void> {
        useBoardStore.getState().setBoard(board);
        view = new BoardView(mockApi);
        view.init();
        await flushMicrotasks();
        vi.mocked(mockApi.SaveBoard).mockClear();
    }
});

function createBoard(edges: BoardEdgeRecord[] = []): BoardDocument {
    return {
        path: BOARD_PATH,
        title: 'Link mode board',
        docId: 'board:link-mode',
        type: 'karte-board',
        version: 1,
        created: '2026-08-23',
        updated: '2026-08-23',
        tags: ['test'],
        cards: [
            { id: 'card:a', type: 'thought', title: 'Alpha', body: '', meta: {} },
            { id: 'card:b', type: 'thought', title: 'Beta', body: '', meta: {} },
            { id: 'card:c', type: 'thought', title: 'Gamma', body: '', meta: {} },
        ],
        edges,
        layout: {
            cards: {
                'card:a': { x: 100, y: 100, width: 240, height: 140 },
                'card:b': { x: 500, y: 100, width: 240, height: 140 },
                'card:c': { x: 500, y: 420, width: 240, height: 140 },
            },
            viewport: { x: 0, y: 0, zoom: 1 },
        },
        notes: '',
        rawContent: '# Link mode board',
    };
}

function createDirectionalBoard(): BoardDocument {
    const board = createBoard();
    board.cards = [
        { id: 'card:center', type: 'thought', title: 'Center', body: '' },
        { id: 'card:left', type: 'thought', title: 'Left', body: '' },
        { id: 'card:right-b', type: 'thought', title: 'Right B', body: '' },
        { id: 'card:right-a', type: 'thought', title: 'Right A', body: '' },
        { id: 'card:up', type: 'thought', title: 'Up', body: '' },
        { id: 'card:down', type: 'thought', title: 'Down', body: '' },
    ];
    board.layout.cards = {
        'card:center': { x: 500, y: 500, width: 100, height: 100 },
        'card:left': { x: 100, y: 500, width: 100, height: 100 },
        'card:right-b': { x: 900, y: 500, width: 100, height: 100 },
        'card:right-a': { x: 900, y: 500, width: 100, height: 100 },
        'card:up': { x: 500, y: 100, width: 100, height: 100 },
        'card:down': { x: 500, y: 900, width: 100, height: 100 },
    };
    return board;
}

function edge(id: string, from: string, to: string, relation: string): BoardEdgeRecord {
    return { id, from, to, relation, label: '', description: '' };
}

function removeCard(board: BoardDocument, cardId: string): BoardDocument {
    const layouts = { ...board.layout.cards };
    delete layouts[cardId];
    return {
        ...board,
        cards: board.cards.filter((item) => item.id !== cardId),
        edges: board.edges.filter((item) => item.from !== cardId && item.to !== cardId),
        layout: { ...board.layout, cards: layouts },
    };
}

function action(name: string): HTMLButtonElement {
    const button = document.querySelector<HTMLButtonElement>(`[data-action="${name}"]`);
    if (!button) throw new Error(`Missing action ${name}`);
    return button;
}

function card(cardId: string): HTMLElement {
    const element = document.querySelector<HTMLElement>(`[data-board-card-activation][data-card-id="${cardId}"]`);
    if (!element) throw new Error(`Missing card ${cardId}`);
    return element;
}

function cards(): HTMLElement[] {
    return Array.from(document.querySelectorAll<HTMLElement>('[data-board-card-activation]'));
}

function edgeField<T extends HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>(name: string): T {
    const field = document.querySelector<T>(`.board-edge-editor [name="${name}"]`);
    if (!field) throw new Error(`Missing edge field ${name}`);
    return field;
}

function selectEdge(edgeId: string): void {
    const hitbox = document.querySelector<SVGElement>(`.board-edge-hitbox[data-edge-id="${edgeId}"]`);
    if (!hitbox) throw new Error(`Missing edge ${edgeId}`);
    hitbox.dispatchEvent(new MouseEvent('click', { bubbles: true }));
}

function createLinkByClick(fromCardId: string, toCardId: string): void {
    action('add-edge').click();
    card(fromCardId).click();
    card(toCardId).click();
}

function assertRejectedMutation(mutate: () => void, selectedEdgeId: string): void {
    const board = useBoardStore.getState().board;
    const selection = selectionSnapshot();
    mutate();
    expect(useBoardStore.getState().board).toBe(board);
    expect(selectionSnapshot()).toEqual(selection);
    expect(useBoardStore.getState().selectedEdgeId).toBe(selectedEdgeId);
}

function selectionSnapshot(): { card: string | null; edge: string | null } {
    const state = useBoardStore.getState();
    return { card: state.selectedCardId, edge: state.selectedEdgeId };
}

function linkStatus(): string {
    return document.getElementById('board-link-status')?.textContent || '';
}

function key(
    target: EventTarget,
    value: string,
    options: Pick<KeyboardEventInit, 'ctrlKey' | 'metaKey' | 'repeat'> = {}
): KeyboardEvent {
    const event = new KeyboardEvent('keydown', {
        bubbles: true,
        cancelable: true,
        key: value,
        ...options,
    });
    target.dispatchEvent(event);
    return event;
}

async function flushMicrotasks(): Promise<void> {
    await Promise.resolve();
    await Promise.resolve();
    await Promise.resolve();
}
