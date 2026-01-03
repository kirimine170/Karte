import { BaseComponent } from './component-base';
import { useUIStore, useDocStore, useCustomCssStore } from '../stores/index';
import { filterFilesByQuery, buildFileDisplayLabel, type FileItem } from '../logic';
import type { WailsAppAPI } from '../types/wails-api';
import { eventLogger } from '../utils/event-logger';
import { applyCustomCssToHtml } from '../utils/custom-css';

export class Sidebar extends BaseComponent {
    private unsubscribe: (() => void)[] = [];
    private api: WailsAppAPI;

    // DOM要素
    private searchInput: HTMLInputElement | null = null;
    private tree: HTMLElement | null = null;

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
        this.loadFileList();
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

    private async loadFileList(): Promise<void> {
        try {
            eventLogger.log('Sidebar', 'load-file-list-start');
            const files = await this.api.GetFileList();
            useDocStore.getState().setFiles(files);
            eventLogger.log('Sidebar', 'load-file-list-success', { count: files.length });
        } catch (error) {
            console.error('Failed to load file list:', error);
            eventLogger.log('Sidebar', 'load-file-list-error', { error: String(error) });
            useUIStore.getState().setStatusMessage('ファイルリストの読み込みに失敗しました', 3000);
        }
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

            // クリックイベント
            this.unsubscribe.push(
                this.addEventListener(item, 'click', async () => {
                    await this.handleFileSelect(file.path);
                })
            );

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
                await this.updatePreview(content);
            }
        } catch (error) {
            console.error('Failed to load file:', error);
            eventLogger.log('Sidebar', 'file-load-error', { path, error: String(error) });
            useUIStore.getState().setStatusMessage('ファイルの読み込みに失敗しました', 3000);
        }
    }

    private async updatePreview(content: string): Promise<void> {
        try {
            const html = await this.api.PreviewMarkdown(content);
            const finalHtml = this.buildPreviewHtml(content, html);
            useDocStore.getState().setPreviewHtml(finalHtml);

            // iframeに反映
            const preview = document.getElementById('preview') as HTMLIFrameElement;
            if (preview && preview.contentDocument) {
                preview.contentDocument.open();
                preview.contentDocument.write(finalHtml);
                preview.contentDocument.close();
            }
        } catch (error) {
            console.error('Failed to update preview:', error);
        }
    }

    private buildPreviewHtml(content: string, html: string): string {
        const customCss = useCustomCssStore.getState().customCss;
        const theme = useUIStore.getState().theme;
        return applyCustomCssToHtml(content, html, customCss, theme);
    }

    destroy(): void {
        this.unsubscribe.forEach((unsub) => unsub());
        this.unsubscribe = [];
    }
}
