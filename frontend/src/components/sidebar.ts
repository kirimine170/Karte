import { BaseComponent } from './component-base';
import { useUIStore, useDocStore, useCustomCssStore } from '../stores/index';
import { filterFilesByQuery, buildFileDisplayLabel, type FileItem } from '../logic';
import type { ResourceSearchItem, WailsAppAPI } from '../types/wails-api';
import { eventLogger } from '../utils/event-logger';
import { applyCustomCssToHtml } from '../utils/custom-css';
import { renderMarkdownPreview } from '../utils/preview-renderer';
import { convertTimestampsToLinks } from '../utils/preview-audio';
import {
    beginDocumentTransition,
    cancelDocumentTransition,
    commitBoardDocumentTransition,
    commitDocumentPreview,
    commitEditorDocumentTransition,
    isBoardDocumentPath,
    isDocumentTransitionActive,
    isPdfDocumentPath,
    type DocumentTransition,
} from '../utils/document-transition';
import {
    LEGACY_RESOURCE_SEARCH_MAX_ITEMS,
    RESOURCE_SEARCH_PAGE_LIMIT,
    ResourceSearchClient,
} from '../utils/resource-search';

export class Sidebar extends BaseComponent {
    private unsubscribe: (() => void)[] = [];
    private api: WailsAppAPI;
    private destroyed = true;
    private fileSelectionRequestId = 0;
    private resourceSearch: ResourceSearchClient;
    private resourceSearchRequestId = 0;
    private resourceSearchItems: ResourceSearchItem[] = [];
    private resourceSearchQuery = '';
    private resourceSearchPage = 0;
    private resourceSearchHasMore = false;
    private resourceSearchLoading = false;
    private documentTransition: DocumentTransition | null = null;

    // DOM要素
    private searchInput: HTMLInputElement | null = null;
    private tree: HTMLElement | null = null;

    constructor(api: WailsAppAPI, parent?: HTMLElement) {
        super(parent);
        this.api = api;
        this.resourceSearch = new ResourceSearchClient(api);
    }

    init(): void {
        if (!this.destroyed) {
            return;
        }
        eventLogger.log('Sidebar', 'init');

        const side = document.querySelector('.side');
        if (!side) {
            console.error('Sidebar: .side element not found');
            return;
        }
        this.destroyed = false;
        this.element = side as HTMLElement;

        // DOM要素の取得
        this.searchInput = document.getElementById('q') as HTMLInputElement;
        this.tree = document.getElementById('tree');

        // イベントリスナーの設定
        this.setupEventListeners();

        // 状態の購読
        this.subscribeToStores();

        // 初期状態の反映
        const uiStore = useUIStore.getState();
        const mainContainer = document.getElementById('mainContainer');
        if (mainContainer) {
            this.toggleClass(mainContainer, 'sidebar-hidden', !uiStore.sidebarVisible);
        }

        // empty queryも共通のpage APIで読み込む
        this.startResourceSearch(useDocStore.getState().searchQuery);
    }

    private setupEventListeners(): void {
        const docStore = useDocStore.getState();

        // 検索入力
        if (this.searchInput) {
            this.unsubscribe.push(
                this.addEventListener(this.searchInput, 'input', (e) => {
                    const target = e.target as HTMLInputElement;
                    eventLogger.log('Sidebar', 'search-input', { query: target.value });
                    docStore.setSearchQuery(target.value);
                })
            );
        }

        if (this.tree) {
            this.unsubscribe.push(
                this.addEventListener(this.tree, 'click', (event) => {
                    if (event.target instanceof Element && event.target.closest('[data-action="load-more-resources"]')) {
                        void this.loadNextResourcePage();
                        return;
                    }
                    const item = this.fileItemFromEvent(event);
                    const path = item?.dataset.path;
                    if (path) {
                        void this.handleFileSelect(path);
                    }
                })
            );
        }
    }

    private subscribeToStores(): void {
        // UI Store - サイドバーの表示/非表示
        this.unsubscribe.push(
            useUIStore.subscribe((state) => {
                const mainContainer = document.getElementById('mainContainer');
                if (mainContainer) {
                    this.toggleClass(mainContainer, 'sidebar-hidden', !state.sidebarVisible);
                }
            })
        );

        // Doc Store - current pathと共有検索クエリ
        this.unsubscribe.push(
            useDocStore.subscribe(
                (state) => ({
                    searchQuery: state.searchQuery,
                    currentPath: state.currentPath,
                }),
                ({ searchQuery, currentPath }, previous) => {
                    if (searchQuery !== previous.searchQuery) {
                        this.startResourceSearch(searchQuery);
                    }
                    if (this.searchInput && this.searchInput.value !== searchQuery) {
                        this.searchInput.value = searchQuery;
                    }
                    this.renderFileList(currentPath);
                },
                {
                    equalityFn: (current, previous) =>
                        current.searchQuery === previous.searchQuery &&
                        current.currentPath === previous.currentPath,
                }
            )
        );
    }

    private renderFileList(currentPath: string): void {
        const tree = this.tree;
        if (!tree) {
            return;
        }

        // ファイルリストをクリア
        tree.innerHTML = '';

        // ファイルアイテムをレンダリング
        this.resourceSearchItems.forEach((resource) => {
            const file: FileItem = {
                path: resource.path,
                title: resource.title,
                modTime: resource.metadata.modTime,
                size: resource.metadata.size,
            };
            const item = this.createElement('div', 'item');
            item.dataset.path = file.path;

            // 現在のファイルかどうか
            if (file.path === currentPath) {
                item.classList.add('active');
            }

            // 未保存インジケーター（将来実装）
            const unsavedDot = this.createElement('span', 'unsaved-dot');
            item.appendChild(unsavedDot);

            // ファイル名
            const label = buildFileDisplayLabel(file);
            item.textContent = label;

            tree.appendChild(item);
        });
        if (this.resourceSearchHasMore && this.resourceSearchItems.length < LEGACY_RESOURCE_SEARCH_MAX_ITEMS) {
            const more = this.createElement('button', 'sidebar-search-more');
            more.dataset.action = 'load-more-resources';
            more.textContent = this.resourceSearchLoading ? '読み込み中…' : 'さらに表示';
            (more as HTMLButtonElement).disabled = this.resourceSearchLoading;
            tree.appendChild(more);
        }
    }

    private startResourceSearch(query: string): void {
        const normalizedQuery = this.normalizeSearchQuery(query);
        this.resourceSearch.cancel();
        this.resourceSearchRequestId += 1;
        this.resourceSearchQuery = normalizedQuery;
        this.resourceSearchItems = this.legacyImmediateItems(normalizedQuery);
        this.resourceSearchPage = 0;
        this.resourceSearchHasMore = false;
        this.resourceSearchLoading = false;
        this.renderFileList(useDocStore.getState().currentPath);
        void this.loadResourcePage(1, true);
    }

    private async loadNextResourcePage(): Promise<void> {
        if (this.resourceSearchLoading || !this.resourceSearchHasMore ||
            this.resourceSearchItems.length >= LEGACY_RESOURCE_SEARCH_MAX_ITEMS) {
            return;
        }
        await this.loadResourcePage(this.resourceSearchPage + 1, false);
    }

    private async loadResourcePage(page: number, replace: boolean): Promise<void> {
        const requestId = ++this.resourceSearchRequestId;
        const query = this.resourceSearchQuery;
        this.resourceSearchLoading = true;
        this.renderFileList(useDocStore.getState().currentPath);
        try {
            const result = await this.resourceSearch.search({
                query,
                kinds: ['markdown', 'pdf'],
                page,
                limit: RESOURCE_SEARCH_PAGE_LIMIT,
            });
            if (!result || !this.isResourceSearchRequestActive(requestId, query)) {
                return;
            }
            this.resourceSearchItems = mergeResourceSearchItems(
                replace ? [] : this.resourceSearchItems,
                result.items,
                LEGACY_RESOURCE_SEARCH_MAX_ITEMS,
            );
            this.resourceSearchPage = result.page;
            this.resourceSearchHasMore = result.hasMore && this.resourceSearchItems.length < LEGACY_RESOURCE_SEARCH_MAX_ITEMS;
            const files = this.resourceSearchItems.map((item): FileItem => ({
                path: item.path,
                title: item.title,
                modTime: item.metadata.modTime,
                size: item.metadata.size,
            }));
            useDocStore.getState().setFiles(files);
            eventLogger.log('Sidebar', 'resource-search-success', {
                query,
                page: result.page,
                pageCount: result.items.length,
                retainedCount: this.resourceSearchItems.length,
                total: result.total,
            });
        } catch (error) {
            if (!this.isResourceSearchRequestActive(requestId, query)) {
                return;
            }
            console.error('Failed to search resources:', error);
            eventLogger.log('Sidebar', 'resource-search-error', { error: String(error) });
            useUIStore.getState().setStatusMessage('ファイル検索に失敗しました', 3000);
        } finally {
            if (this.isResourceSearchRequestActive(requestId, query)) {
                this.resourceSearchLoading = false;
                this.renderFileList(useDocStore.getState().currentPath);
            }
        }
    }

    private legacyImmediateItems(query: string): ResourceSearchItem[] {
        if (typeof this.api.SearchResources === 'function') {
            return [];
        }
        return filterFilesByQuery(
            useDocStore.getState().files.slice(0, LEGACY_RESOURCE_SEARCH_MAX_ITEMS),
            query,
        ).map((file) => ({
            kind: file.path.toLowerCase().endsWith('.pdf') ? 'pdf' : 'markdown',
            path: file.path,
            title: file.title || file.path.split('/').pop() || file.path,
            metadata: {
                name: file.path.split('/').pop() || file.path,
                extension: file.path.toLowerCase().endsWith('.pdf') ? '.pdf' : '.md',
                size: typeof file.size === 'number' ? file.size : 0,
                modTime: typeof file.modTime === 'string' ? file.modTime : '',
            },
        }));
    }

    private normalizeSearchQuery(query: string): string {
        return (query || '').trim().toLowerCase();
    }

    private fileItemFromEvent(event: Event): HTMLElement | null {
        if (!(event.target instanceof Element) || !this.tree) {
            return null;
        }
        const item = event.target.closest<HTMLElement>('.item[data-path]');
        return item && this.tree.contains(item) ? item : null;
    }

    private async handleFileSelect(path: string): Promise<void> {
        if (this.destroyed) {
            return;
        }
        const docStore = useDocStore.getState();

        eventLogger.log('Sidebar', 'file-select', { path });

        // 未保存の変更がある場合は確認
        if (docStore.hasUnsavedChanges) {
            eventLogger.log('Sidebar', 'file-select-unsaved-warning', { path });
            // TODO: 未保存確認モーダルを表示
            const confirmed = window.confirm('未保存の変更があります。保存せずに続行しますか？');
            if (!confirmed) {
                eventLogger.log('Sidebar', 'file-select-cancelled', { path });
                return;
            }
        }

        const requestId = ++this.fileSelectionRequestId;
        const transition = beginDocumentTransition(path);
        this.documentTransition = transition;
        try {
            eventLogger.log('Sidebar', 'file-load-start', { path });
            if (isBoardDocumentPath(path)) {
                const board = await this.api.LoadBoard(path);
                if (
                    !this.isFileSelectionRequestActive(requestId) ||
                    !commitBoardDocumentTransition(transition, board)
                ) {
                    return;
                }
                eventLogger.log('Sidebar', 'board-load-success', { path });
                return;
            }
            // ファイルを読み込む
            const content = await this.api.LoadFile(path);
            if (
                !this.isFileSelectionRequestActive(requestId) ||
                !commitEditorDocumentTransition(transition, content)
            ) {
                return;
            }
            if (isPdfDocumentPath(path)) {
                return;
            }
            eventLogger.log('Sidebar', 'file-load-success', { path, contentLength: content.length });

            // プレビューを更新
            await this.updatePreview(content, path, requestId, transition);
        } catch (error) {
            if (
                !this.isFileSelectionRequestActive(requestId) ||
                !isDocumentTransitionActive(transition)
            ) {
                return;
            }
            console.error('Failed to load file:', error);
            eventLogger.log('Sidebar', 'file-load-error', { path, error: String(error) });
            useUIStore.getState().setStatusMessage('ファイルの読み込みに失敗しました', 3000);
        } finally {
            if (this.documentTransition === transition) {
                this.documentTransition = null;
            }
        }
    }

    private async updatePreview(
        content: string,
        path: string,
        requestId: number,
        transition: DocumentTransition
    ): Promise<void> {
        try {
            const { prepared, html } = await renderMarkdownPreview(content, this.api, path);
            if (
                !this.isFileSelectionRequestActive(requestId) ||
                !isDocumentTransitionActive(transition)
            ) {
                return;
            }
            const finalHtml = this.buildPreviewHtml(prepared, html);
            commitDocumentPreview(transition, finalHtml);
        } catch (error) {
            if (
                !this.isFileSelectionRequestActive(requestId) ||
                !isDocumentTransitionActive(transition)
            ) {
                return;
            }
            console.error('Failed to update preview:', error);
        }
    }

    private buildPreviewHtml(content: string, html: string): string {
        const customCss = useCustomCssStore.getState().customCss;
        const theme = useUIStore.getState().theme;
        const withCss = applyCustomCssToHtml(content, html, customCss, theme);
        return convertTimestampsToLinks(withCss);
    }

    destroy(): void {
        if (this.destroyed) {
            return;
        }
        this.destroyed = true;
        this.fileSelectionRequestId += 1;
        cancelDocumentTransition(this.documentTransition);
        this.documentTransition = null;
        this.resourceSearchRequestId += 1;
        this.resourceSearch.destroy();
        this.resourceSearchItems = [];
        this.resourceSearchHasMore = false;
        this.resourceSearchLoading = false;
        this.unsubscribe.forEach((unsub) => unsub());
        this.unsubscribe = [];
        this.searchInput = null;
        this.tree = null;
        this.element = null;
    }

    private isFileSelectionRequestActive(requestId: number): boolean {
        return !this.destroyed && requestId === this.fileSelectionRequestId;
    }

    private isResourceSearchRequestActive(requestId: number, query: string): boolean {
        return !this.destroyed && requestId === this.resourceSearchRequestId && query === this.resourceSearchQuery;
    }
}

function mergeResourceSearchItems(
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
