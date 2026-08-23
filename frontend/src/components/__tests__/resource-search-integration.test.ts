import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Sidebar } from '../sidebar';
import { BoardView } from '../board-view';
import { useBoardStore, useDocStore, useUIStore } from '../../stores/index';
import type {
    BoardDocument,
    ResourceKind,
    ResourceSearchItem,
    ResourceSearchRequest,
    WailsAppAPI,
} from '../../types/wails-api';

const board: BoardDocument = {
    path: 'content/current.board.md',
    title: 'Current',
    docId: 'board:current',
    type: 'karte-board',
    version: 1,
    created: '2026-01-01',
    updated: '2026-01-01',
    tags: [],
    cards: [],
    edges: [],
    layout: { cards: {}, viewport: { x: 0, y: 0, zoom: 1 } },
    notes: '',
    rawContent: '# Current',
};

function resource(path: string, kind: ResourceKind): ResourceSearchItem {
    const name = path.split('/').pop() || path;
    return {
        path,
        kind,
        title: name,
        metadata: {
            name,
            extension: kind === 'image' ? '.webp' : kind === 'csv' ? '.csv' : kind === 'pdf' ? '.pdf' : '.md',
            size: 1,
            modTime: '2026-01-01T00:00:00.000Z',
        },
    };
}

describe('shared typed resource search UI', () => {
    beforeEach(() => {
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
        useBoardStore.getState().setBoard(structuredClone(board));
        document.body.innerHTML = `
            <aside class="side"><input id="q"><div id="tree"></div></aside>
            <div id="mainContainer"></div>
            <div id="board-tab"></div>
        `;
    });

    afterEach(() => {
        document.body.innerHTML = '';
    });

    it('uses the same backend Markdown hit and bounds a 1000-item response to 50 DOM nodes per consumer', async () => {
        const thousand = Array.from({ length: 1_000 }, (_, index) =>
            resource(`content/item-${String(index).padStart(4, '0')}.md`, 'markdown'));
        const shared = resource('content/shared-result.md', 'markdown');
        const searchResources = vi.fn(async (request: ResourceSearchRequest) => {
            const items = request.query === 'shared' ? [shared] : thousand;
            return {
                items,
                query: request.query,
                kinds: request.kinds,
                page: request.page,
                limit: request.limit,
                total: request.query === 'shared' ? 1 : 1_000,
                hasMore: request.query !== 'shared',
            };
        });
        const getFileList = vi.fn();
        const getImageList = vi.fn();
        const getCsvList = vi.fn();
        const getBoardResourceCandidates = vi.fn();
        const api = {
            SearchResources: searchResources,
            GetFileList: getFileList,
            GetImageList: getImageList,
            GetCsvList: getCsvList,
            GetBoardResourceCandidates: getBoardResourceCandidates,
        } as unknown as WailsAppAPI;
        const sidebar = new Sidebar(api);
        const boardView = new BoardView(api);
        sidebar.init();
        boardView.init();

        await vi.waitFor(() => {
            expect(document.querySelectorAll('#tree .item')).toHaveLength(50);
            expect(document.querySelectorAll('.board-tray-item')).toHaveLength(50);
        });
        expect(document.querySelector('.sidebar-search-more')).not.toBeNull();
        expect(document.querySelector('.board-tray-more')).not.toBeNull();

        useDocStore.getState().setSearchQuery('shared');
        await vi.waitFor(() => {
            expect(document.querySelectorAll('#tree .item')).toHaveLength(1);
            expect(document.querySelectorAll('.board-tray-item')).toHaveLength(1);
        });
        expect(document.querySelector('#tree .item')?.getAttribute('data-path')).toBe(shared.path);
        expect(document.querySelector('.board-tray-item')?.getAttribute('data-material-path')).toBe(shared.path);
        expect(searchResources).toHaveBeenCalledWith(expect.objectContaining({ kinds: ['markdown', 'pdf'], query: 'shared' }));
        expect(searchResources).toHaveBeenCalledWith(expect.objectContaining({ kinds: ['markdown', 'image', 'csv'], query: 'shared' }));
        expect(getFileList).not.toHaveBeenCalled();
        expect(getImageList).not.toHaveBeenCalled();
        expect(getCsvList).not.toHaveBeenCalled();
        expect(getBoardResourceCandidates).not.toHaveBeenCalled();

        sidebar.destroy();
        boardView.destroy();
    });

    it('sends image and CSV tray filters to the backend instead of filtering full lists locally', async () => {
        const searchResources = vi.fn(async (request: ResourceSearchRequest) => {
            const kind = request.kinds[0]!;
            const items = request.kinds.length === 1 && kind === 'image'
                ? [resource('data/image/cover.webp', 'image')]
                : request.kinds.length === 1 && kind === 'csv'
                    ? [resource('data/csv/table.csv', 'csv')]
                    : [];
            return {
                items,
                query: request.query,
                kinds: request.kinds,
                page: request.page,
                limit: request.limit,
                total: items.length,
                hasMore: false,
            };
        });
        const getImageList = vi.fn();
        const getCsvList = vi.fn();
        const boardView = new BoardView({
            SearchResources: searchResources,
            GetImageList: getImageList,
            GetCsvList: getCsvList,
        } as unknown as WailsAppAPI);
        boardView.init();
        await vi.waitFor(() => expect(searchResources).toHaveBeenCalled());

        document.querySelector<HTMLButtonElement>('[data-filter="image"]')?.click();
        await vi.waitFor(() => {
            expect(document.querySelector('.board-tray-item')?.getAttribute('data-material-path')).toBe('data/image/cover.webp');
        });
        expect(searchResources).toHaveBeenLastCalledWith(expect.objectContaining({ kinds: ['image'] }));

        document.querySelector<HTMLButtonElement>('[data-filter="csv"]')?.click();
        await vi.waitFor(() => {
            expect(document.querySelector('.board-tray-item')?.getAttribute('data-material-path')).toBe('data/csv/table.csv');
        });
        expect(searchResources).toHaveBeenLastCalledWith(expect.objectContaining({ kinds: ['csv'] }));
        expect(getImageList).not.toHaveBeenCalled();
        expect(getCsvList).not.toHaveBeenCalled();
        boardView.destroy();
    });
});
