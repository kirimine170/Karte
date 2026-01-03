// メインアプリケーション
import { Topbar } from './components/topbar';
import { Sidebar } from './components/sidebar';
import { MainTabs } from './components/main-tabs';
import { EditorLayout } from './components/editor-layout';
import { GraphView } from './components/graph-view';
import { ModalHost } from './components/modal-host';
import { OverlayHost } from './components/overlay-host';
import { ImageGallery } from './components/image-gallery';
import { getWailsAppAPI, getWailsRuntimeAPI } from './api/wails-api';
import { useUIStore, useDocStore, useASRStore } from './stores/index';
import type { WailsAppAPI, WailsRuntimeAPI } from './types/wails-api';
import { eventLogger } from './utils/event-logger';

export class App {
    private api: WailsAppAPI | null = null;
    private runtime: WailsRuntimeAPI | null = null;
    private components: {
        topbar: Topbar | null;
        sidebar: Sidebar | null;
        mainTabs: MainTabs | null;
        editorLayout: EditorLayout | null;
        graphView: GraphView | null;
        modalHost: ModalHost | null;
        overlayHost: OverlayHost | null;
        imageGallery: ImageGallery | null;
    } = {
            topbar: null,
            sidebar: null,
            mainTabs: null,
            editorLayout: null,
            graphView: null,
            modalHost: null,
            overlayHost: null,
            imageGallery: null,
        };

    async init(): Promise<void> {
        console.log('Initializing Karte application...');

        try {
            // Wails APIの取得
            this.api = await getWailsAppAPI();
            this.runtime = await getWailsRuntimeAPI();

            // EventLoggerにAPIを設定
            if (this.api) {
                eventLogger.setApi({
                    SaveEventLogs: async (logsJson: string) => {
                        try {
                            await this.api!.SaveEventLogs(logsJson);
                            return true;
                        } catch (error) {
                            console.error('Failed to save event logs:', error);
                            return false;
                        }
                    }
                });
                // 自動保存を開始（60秒ごと）
                eventLogger.startAutoSave(60000);
            }

            // コンポーネントの初期化
            await this.initComponents();

            // Wailsイベントの設定
            this.setupWailsEvents();

            // ASRステータスの確認
            await this.updateASRStatus();

            console.log('Initialization completed successfully');
        } catch (error) {
            console.error('Failed to initialize:', error);
            useUIStore.getState().setStatusMessage('初期化に失敗しました', 5000);
        }
    }

    private async initComponents(): Promise<void> {
        if (!this.api) {
            throw new Error('Wails API not initialized');
        }

        // コンポーネントの初期化
        this.components.topbar = new Topbar(this.api);
        this.components.topbar.init();

        this.components.sidebar = new Sidebar(this.api);
        this.components.sidebar.init();

        this.components.mainTabs = new MainTabs();
        this.components.mainTabs.init();

        this.components.editorLayout = new EditorLayout(this.api);
        this.components.editorLayout.init();

        this.components.graphView = new GraphView(this.api);
        this.components.graphView.init();

        this.components.modalHost = new ModalHost(this.api);
        this.components.modalHost.init();

        this.components.overlayHost = new OverlayHost();
        this.components.overlayHost.init();

        this.components.imageGallery = new ImageGallery(this.api);
        this.components.imageGallery.init();

        // 初期ファイルの読み込み
        await this.loadInitialFile();
    }

    private async loadInitialFile(): Promise<void> {
        if (!this.api) {
            return;
        }

        try {
            const files = await this.api.GetFileList();
            useDocStore.getState().setFiles(files);

            if (files.length > 0) {
                const firstFile = files[0];
                const content = await this.api.LoadFile(firstFile.path);
                useDocStore.getState().setCurrentPath(firstFile.path);
                useDocStore.getState().setMarkdownContent(content);
                useDocStore.getState().clearUnsavedChanges();

                // プレビューを更新
                const html = await this.api.PreviewMarkdown(content);
                useDocStore.getState().setPreviewHtml(html);
            }
        } catch (error) {
            console.error('Failed to load initial file:', error);
        }
    }

    private setupWailsEvents(): void {
        if (!this.runtime || !this.api) {
            return;
        }

        // ファイル変更イベント
        this.runtime.EventsOn('file-changed', (path: unknown) => {
            console.log('File changed:', path);
            this.refreshPreview();
            this.refreshGraph();
        });

        // リンク更新イベント
        this.runtime.EventsOn('link-updated', (data: unknown) => {
            console.log('Link updated:', data);
            this.refreshPreview();
        });

        // コンフリクト検出イベント
        this.runtime.EventsOn('conflict-detected', (conflictInfo: unknown) => {
            console.log('Conflict detected:', conflictInfo);
            // TODO: コンフリクトモーダルを表示
        });

        // 自動マージ成功イベント
        this.runtime.EventsOn('auto-merge-success', (data: unknown) => {
            console.log('Auto-merge succeeded:', data);
            // TODO: ファイルを再読み込み
        });

        // オーディオインポートイベント
        this.runtime.EventsOn('audio-imported', (data: unknown) => {
            console.log('Audio imported:', data);
            // TODO: オーディオプレーヤーを更新
        });

        // オーディオトランスクリプションイベント
        this.runtime.EventsOn('audio-transcribed', (data: unknown) => {
            console.log('Audio transcribed:', data);
            // TODO: トランスクリプトをエディタに追加
        });

        // オーディオトランスクリプション進捗イベント
        this.runtime.EventsOn('audio-transcribe-progress', (data: unknown) => {
            console.log('Audio transcribe progress:', data);
            // TODO: 進捗バーを更新
        });

        // 画像インポートイベント
        this.runtime.EventsOn('image-imported', (data: unknown) => {
            console.log('Image imported:', data);
            if (this.components.imageGallery) {
                this.components.imageGallery.refresh();
            }
        });

        // PDFエクスポート進捗イベント
        this.runtime.EventsOn('pdf-export-progress', (data: unknown) => {
            console.log('PDF export progress:', data);
            // TODO: 進捗バーを更新
        });

        // PDFエクスポート完了イベント
        this.runtime.EventsOn('pdf-export-completed', (data: unknown) => {
            console.log('PDF export completed:', data);
            // TODO: 完了メッセージを表示
        });

        // PDFエクスポートエラーイベント
        this.runtime.EventsOn('pdf-export-error', (data: unknown) => {
            console.log('PDF export error:', data);
            // TODO: エラーメッセージを表示
        });
    }

    private async updateASRStatus(): Promise<void> {
        if (!this.api) {
            return;
        }

        try {
            const status = await this.api.GetASRStatus();
            useASRStore.getState().setStatus(status);

            // 初期化されていない場合は定期的にチェック
            if (!status.initialized && !status.initializing) {
                const interval = setInterval(async () => {
                    const newStatus = await this.api!.GetASRStatus();
                    useASRStore.getState().setStatus(newStatus);
                    if (newStatus.initialized) {
                        clearInterval(interval);
                    }
                }, 2000);
            }
        } catch (error) {
            console.error('Failed to update ASR status:', error);
        }
    }

    private async refreshPreview(): Promise<void> {
        const docStore = useDocStore.getState();
        if (!docStore.currentPath || !this.api) {
            return;
        }

        try {
            const content = await this.api.LoadFile(docStore.currentPath);
            const html = await this.api.PreviewMarkdown(content);
            useDocStore.getState().setPreviewHtml(html);
        } catch (error) {
            console.error('Failed to refresh preview:', error);
        }
    }

    private async refreshGraph(): Promise<void> {
        if (this.components.graphView) {
            await this.components.graphView.refresh();
        }
    }

    destroy(): void {
        // 最終的なログを保存
        if (this.api) {
            eventLogger.saveToBackend().catch(err => {
                console.error('Failed to save logs on destroy:', err);
            });
        }

        // 自動保存を停止
        eventLogger.stopAutoSave();

        // コンポーネントのクリーンアップ
        Object.values(this.components).forEach((component) => {
            if (component) {
                component.destroy();
            }
        });
    }
}

