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

async function flushAsync(): Promise<void> {
    await Promise.resolve();
    await new Promise((resolve) => setTimeout(resolve, 0));
}
