import { BaseComponent } from './component-base';
import { useUIStore, useDocStore, useCustomCssStore, useBoardStore } from '../stores/index';
import { filterFilesByQuery, buildFileDisplayLabel, type FileItem } from '../logic';
import type { BoardDocument, WailsAppAPI } from '../types/wails-api';
import { eventLogger } from '../utils/event-logger';
import { applyCustomCssToHtml } from '../utils/custom-css';
import { renderMarkdownPreview } from '../utils/preview-renderer';
import { writePreviewFrame } from '../utils/preview-frame';
import { convertTimestampsToLinks } from '../utils/preview-audio';

export class Sidebar extends BaseComponent {
    private unsubscribe: (() => void)[] = [];
    private api: WailsAppAPI;
    private refreshRevision = 0;
    private announceRefresh = false;

    // DOM要素
    private searchInput: HTMLInputElement | null = null;
    private tree: HTMLElement | null = null;
    private refreshButton: HTMLButtonElement | null = null;
    private refreshStatus: HTMLElement | null = null;

    constructor(api: WailsAppAPI, parent?: HTMLElement) {
        super(parent);
        this.api = api;
    }

    init(): void {
        eventLogger.log('Sidebar', 'init');
        
        const side = document.querySelector('.side');
        if (!side) {
            console.error('Sidebar: .side element not found');
            return;
        }
        this.element = side as HTMLElement;

        // DOM要素の取得
        this.searchInput = document.getElementById('q') as HTMLInputElement;
        this.tree = document.getElementById('tree');
        this.refreshButton = document.getElementById('fileListRefreshBtn') as HTMLButtonElement | null;
        this.refreshStatus = document.getElementById('fileListStatus');

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

        // ファイルリストの読み込み
        void this.refreshFileList();
    }

    private setupEventListeners(): void {
        const docStore = useDocStore.getState();

        if (this.refreshButton) {
            this.unsubscribe.push(this.addEventListener(this.refreshButton, 'click', () => {
                void this.refreshFileList(true);
            }));
        }
        if (this.tree) {
            this.unsubscribe.push(this.addEventListener(this.tree, 'click', (event) => {
                const item = event.target instanceof Element ? event.target.closest<HTMLElement>('.item[data-path]') : null;
                if (item?.dataset.path && this.tree?.contains(item)) {
                    void this.handleFileSelect(item.dataset.path);
                }
            }));
        }

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

        // Doc Store - ファイルリストと検索クエリ
        this.unsubscribe.push(
            useDocStore.subscribe((state) => {
                if (this.searchInput && this.searchInput.value !== state.searchQuery) {
                    this.searchInput.value = state.searchQuery;
                }
                this.renderFileList(state.files, state.searchQuery, state.currentPath);
            })
        );
    }

    // Shared by the explicit button and application events．Only the list changes．
    async refreshFileList(announce = false): Promise<void> {
        const revision = ++this.refreshRevision;
        this.announceRefresh ||= announce;
        this.setRefreshing(true);
        if (this.announceRefresh) this.setRefreshStatus('一覧を更新中．．．', 'loading');
        try {
            eventLogger.log('Sidebar', 'load-file-list-start');
            const files = await this.api.GetFileList();
            if (revision !== this.refreshRevision) return;
            useDocStore.getState().setFiles(files);
            eventLogger.log('Sidebar', 'load-file-list-success', { count: files.length });
            if (this.announceRefresh || this.refreshStatus?.hidden === false) {
                this.setRefreshStatus(`一覧を更新しました（全${files.length}件）`, 'success');
            }
        } catch (error) {
            if (revision !== this.refreshRevision) return;
            console.error('Failed to load file list:', error);
            eventLogger.log('Sidebar', 'load-file-list-error');
            this.setRefreshStatus('一覧を更新できませんでした．もう一度お試しください．', 'error');
            useUIStore.getState().setStatusMessage('ファイルリストの読み込みに失敗しました', 3000);
        } finally {
            if (revision === this.refreshRevision) {
                this.announceRefresh = false;
                this.setRefreshing(false);
            }
        }
    }

    private setRefreshing(refreshing: boolean): void {
        if (this.refreshButton) {
            this.refreshButton.disabled = refreshing;
            this.refreshButton.textContent = refreshing ? '更新中．．．' : '一覧を更新';
        }
        this.tree?.setAttribute('aria-busy', String(refreshing));
    }

    private setRefreshStatus(message: string, state: string): void {
        if (!this.refreshStatus) return;
        this.refreshStatus.textContent = message;
        this.refreshStatus.dataset.state = state;
        this.refreshStatus.hidden = false;
    }

    private renderFileList(files: FileItem[], query: string, currentPath: string): void {
        if (!this.tree) {
            return;
        }

        // 検索でフィルタリング
        const filteredFiles = filterFilesByQuery(files, query);

        // ファイルリストをクリア
        this.tree.innerHTML = '';

        // ファイルアイテムをレンダリング
        filteredFiles.forEach((file) => {
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

            this.tree.appendChild(item);
        });
    }

    private async handleFileSelect(path: string): Promise<void> {
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

        try {
            eventLogger.log('Sidebar', 'file-load-start', { path });
            if (path.toLowerCase().endsWith('.board.md')) {
                const board = await this.api.LoadBoard(path);
                this.applyBoardDocument(board);
                eventLogger.log('Sidebar', 'board-load-success', { path });
                return;
            }
            // ファイルを読み込む
            const content = await this.api.LoadFile(path);
            docStore.setCurrentPath(path);
            if (path.toLowerCase().endsWith('.pdf')) {
                docStore.setMarkdownContent('');
                docStore.setPreviewHtml('');
                docStore.clearUnsavedChanges();
            } else {
                docStore.setMarkdownContent(content);
                docStore.clearUnsavedChanges();
                eventLogger.log('Sidebar', 'file-load-success', { path, contentLength: content.length });

                // プレビューを更新
                await this.updatePreview(content, path);
            }
        } catch (error) {
            console.error('Failed to load file:', error);
            eventLogger.log('Sidebar', 'file-load-error', { path, error: String(error) });
            useUIStore.getState().setStatusMessage('ファイルの読み込みに失敗しました', 3000);
        }
    }

    private async updatePreview(content: string, path: string): Promise<void> {
        try {
            const { prepared, html } = await renderMarkdownPreview(content, this.api, path);
            const finalHtml = this.buildPreviewHtml(prepared, html);
            useDocStore.getState().setPreviewHtml(finalHtml);

            // iframeに反映
            const preview = document.getElementById('preview') as HTMLIFrameElement;
            if (preview) {
                writePreviewFrame(preview, finalHtml);
            }
        } catch (error) {
            console.error('Failed to update preview:', error);
        }
    }

    private buildPreviewHtml(content: string, html: string): string {
        const customCss = useCustomCssStore.getState().customCss;
        const theme = useUIStore.getState().theme;
        const withCss = applyCustomCssToHtml(content, html, customCss, theme);
        return convertTimestampsToLinks(withCss);
    }

    private applyBoardDocument(board: BoardDocument): void {
        useBoardStore.getState().setBoard(board);
        useDocStore.getState().setCurrentPath(board.path);
        useDocStore.getState().setMarkdownContent(board.rawContent);
        useDocStore.getState().setPreviewHtml('');
        useDocStore.getState().clearUnsavedChanges();
        useUIStore.getState().setActiveTab('board');
    }

    destroy(): void {
        this.refreshRevision++;
        this.unsubscribe.forEach((unsub) => unsub());
        this.unsubscribe = [];
    }
}
