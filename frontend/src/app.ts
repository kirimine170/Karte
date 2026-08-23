// メインアプリケーション
import { Topbar } from './components/topbar';
import { Sidebar } from './components/sidebar';
import { MainTabs } from './components/main-tabs';
import { EditorLayout } from './components/editor-layout';
import { GraphView } from './components/graph-view';
import { BoardView } from './components/board-view';
import { ModalHost } from './components/modal-host';
import { OverlayHost } from './components/overlay-host';
import { ImageGallery } from './components/image-gallery';
import { CsvGallery } from './components/csv-gallery';
import { getWailsAppAPI, getWailsRuntimeAPI } from './api/wails-api';
import { useUIStore, useDocStore, useASRStore, useExportStore, useModalStore, useCustomCssStore, useBoardStore } from './stores/index';
import { getASRStatusPollingDecision, markASRInitializationStopped } from './utils/asr-status';
import type { BoardDocument, WailsAppAPI, WailsRuntimeAPI } from './types/wails-api';
import type { Theme } from './types/ui-state';
import { eventLogger } from './utils/event-logger';
import { applyCustomCssToHtml } from './utils/custom-css';
import { renderMarkdownPreview } from './utils/preview-renderer';
import { convertTimestampsToLinks } from './utils/preview-audio';
import {
    beginDocumentTransition,
    cancelDocumentTransition,
    captureDocumentPreviewGuard,
    commitBoardDocumentTransition,
    commitDocumentPreview,
    commitEditorDocumentTransition,
    commitGuardedDocumentPreview,
    isBoardDocumentPath,
    isDocumentTransitionActive,
    isPdfDocumentPath,
    invalidateDocumentTransitions,
    type DocumentTransition,
} from './utils/document-transition';

export class App {
    private api: WailsAppAPI | null = null;
    private runtime: WailsRuntimeAPI | null = null;
    private documentTransition: DocumentTransition | null = null;
    private components: {
        topbar: Topbar | null;
        sidebar: Sidebar | null;
        mainTabs: MainTabs | null;
        editorLayout: EditorLayout | null;
        graphView: GraphView | null;
        boardView: BoardView | null;
        modalHost: ModalHost | null;
        overlayHost: OverlayHost | null;
        imageGallery: ImageGallery | null;
        csvGallery: CsvGallery | null;
    } = {
            topbar: null,
            sidebar: null,
            mainTabs: null,
            editorLayout: null,
            graphView: null,
            boardView: null,
            modalHost: null,
            overlayHost: null,
            imageGallery: null,
            csvGallery: null,
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
                    SaveEventLogs: (logsJson: string) => this.api!.SaveEventLogs(logsJson)
                });
                // 自動保存を開始（60秒ごと）
                eventLogger.startAutoSave(60000);
            }

            // コンポーネントの初期化
            await this.initComponents();
            this.setupThemeSelector();

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

        this.components.boardView = new BoardView(this.api);
        this.components.boardView.init();

        this.components.modalHost = new ModalHost(this.api);
        this.components.modalHost.init();

        this.components.overlayHost = new OverlayHost(this.api);
        this.components.overlayHost.init();

        this.components.imageGallery = new ImageGallery(this.api);
        this.components.imageGallery.init();

        this.components.csvGallery = new CsvGallery(this.api);
        this.components.csvGallery.init();

        await this.loadCustomCss();

        // 初期ファイルの読み込み
        await this.loadInitialFile();
    }

    private setupThemeSelector(): void {
        const select = document.getElementById('theme') as HTMLSelectElement | null;
        if (!select) {
            return;
        }
        const applyTheme = () => {
            const theme = select.value as Theme;
            useUIStore.getState().setTheme(theme);
        };
        select.value = useUIStore.getState().theme;
        select.addEventListener('change', applyTheme);
        select.addEventListener('input', applyTheme);
        useUIStore.subscribe((state) => {
            if (select.value !== state.theme) {
                select.value = state.theme;
            }
        });
    }

    private async loadInitialFile(): Promise<void> {
        if (!this.api) {
            return;
        }

        try {
            const files = await this.api.GetFileList();
            useDocStore.getState().setFiles(files);

            if (files.length > 0) {
                await this.loadFileByPath(files[0].path);
            }
        } catch (error) {
            console.error('Failed to load initial file:', error);
        }
    }

    private async refreshFileList(): Promise<void> {
        if (!this.api) {
            return;
        }
        const files = await this.api.GetFileList();
        useDocStore.getState().setFiles(files);
    }

    private async loadFileByPath(path: string): Promise<void> {
        if (!this.api) {
            return;
        }
        const transition = beginDocumentTransition(path);
        this.documentTransition = transition;
        try {
            if (isBoardDocumentPath(path)) {
                const board = await this.api.LoadBoard(path);
                commitBoardDocumentTransition(transition, board);
                return;
            }
            const content = await this.api.LoadFile(path);
            if (!commitEditorDocumentTransition(transition, content) || isPdfDocumentPath(path)) {
                return;
            }
            const { prepared, html } = await renderMarkdownPreview(content, this.api, path);
            if (!isDocumentTransitionActive(transition)) {
                return;
            }
            commitDocumentPreview(transition, this.buildPreviewHtml(prepared, html));
        } finally {
            if (this.documentTransition === transition) {
                this.documentTransition = null;
            }
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
            const label = (data as { original_name?: string; path?: string })?.original_name
                || (data as { original_name?: string; path?: string })?.path
                || 'audio';
            useUIStore.getState().setStatusMessage(`音声ファイルを保存しました: ${label}`, 3000);
        });

        // オーディオトランスクリプションイベント
        this.runtime.EventsOn('audio-transcribed', (data: unknown) => {
            console.log('Audio transcribed:', data);
            const payload = data as { error?: string; transcriptPath?: string };
            if (payload?.error) {
                useExportStore.getState().setTranscriptionProgress(false);
                useUIStore.getState().setStatusMessage(`文字起こしに失敗: ${payload.error}`, 5000);
                return;
            }
            useExportStore.getState().setTranscriptionProgress(false);
            useUIStore.getState().setStatusMessage('文字起こしが完了しました', 3000);
            if (payload?.transcriptPath) {
                this.refreshFileList().catch((error) => {
                    console.error('Failed to refresh file list after transcription:', error);
                });
                this.loadFileByPath(payload.transcriptPath).catch((error) => {
                    console.error('Failed to load transcript:', error);
                });
            } else {
                this.refreshFileList().catch((error) => {
                    console.error('Failed to refresh file list after transcription:', error);
                });
            }
        });

        // オーディオトランスクリプション進捗イベント
        this.runtime.EventsOn('audio-transcribe-progress', (data: unknown) => {
            console.log('Audio transcribe progress:', data);
            const payload = data as { progress?: number; message?: string };
            const progress = typeof payload?.progress === 'number' ? payload.progress : 0;
            const message = payload?.message || '文字起こし中...';
            useExportStore.getState().setTranscriptionProgress(true, progress, message);
        });

        this.runtime.EventsOn('recording-transcript-final', (data: unknown) => {
            console.log('Recording transcript final:', data);
            const payload = data as { text?: string; transcriptPath?: string };
            if (payload?.text) {
                useASRStore.getState().setRealtimeTranscript('', [payload.text]);
            }
            if (payload?.transcriptPath) {
                useASRStore.getState().setRecordingTranscriptPath(payload.transcriptPath);
            }
        });

        this.runtime.EventsOn('recording-transcript-partial', (data: unknown) => {
            const payload = data as { text?: string; transcriptPath?: string };
            if (typeof payload?.text === 'string') {
                useASRStore.getState().setRealtimeTranscript(payload.text);
            }
            if (payload?.transcriptPath) {
                useASRStore.getState().setRecordingTranscriptPath(payload.transcriptPath);
            }
        });

        this.runtime.EventsOn('recording-input-level', (data: unknown) => {
            const level = Number((data as { level?: unknown })?.level);
            if (Number.isFinite(level)) {
                useASRStore.getState().setMicLevel(Math.min(100, Math.max(0, level * 100)));
            }
        });

        this.runtime.EventsOn('recording-stopped', (data: unknown) => {
            console.log('Recording stopped:', data);
            const payload = data as { error?: string; audioPath?: string; transcriptPath?: string };
            const asrStore = useASRStore.getState();
            asrStore.setIsRecording(false);
            asrStore.setMicLevel(0);
            asrStore.setRealtimeTranscript('');
            if (payload?.transcriptPath) {
                asrStore.setRecordingTranscriptPath(payload.transcriptPath);
            }

            if (payload?.error) {
                useUIStore.getState().setStatusMessage(`録音の停止に失敗しました: ${payload.error}`, 5000);
                return;
            }

            if (payload?.transcriptPath) {
                this.refreshFileList().catch((error) => {
                    console.error('Failed to refresh file list after recording:', error);
                });
                this.loadFileByPath(payload.transcriptPath).catch((error) => {
                    console.error('Failed to load recording transcript:', error);
                });
                useUIStore.getState().setStatusMessage('録音と文字起こしが完了しました', 3000);
            } else {
                this.refreshFileList().catch((error) => {
                    console.error('Failed to refresh file list after recording:', error);
                });
                useUIStore.getState().setStatusMessage('録音が完了しました', 3000);
            }
        });

        // 画像インポートイベント
        this.runtime.EventsOn('image-imported', (data: unknown) => {
            console.log('Image imported:', data);
            if (this.components.imageGallery) {
                this.components.imageGallery.refresh();
            }
        });

        // CSVインポートイベント
        this.runtime.EventsOn('csv-imported', (data: unknown) => {
            console.log('CSV imported:', data);
            if (this.components.csvGallery) {
                this.components.csvGallery.refresh();
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
            useExportStore.getState().setPdfExportProgress(false);
            useUIStore.getState().setStatusMessage('PDFエクスポートが完了しました', 3000);
        });

        // PDF表示エラーイベント
        this.runtime.EventsOn('pdf-open-error', (data: unknown) => {
            console.log('PDF open error:', data);
            const message = (data as { error?: string })?.error || 'PDFの表示に失敗しました';
            useUIStore.getState().setStatusMessage(message, 4000);
        });

        // PDFエクスポートエラーイベント
        this.runtime.EventsOn('pdf-export-error', (data: unknown) => {
            console.log('PDF export error:', data);
            useExportStore.getState().setPdfExportProgress(false);
            const payload = data as { message?: string; error?: string };
            const message = payload?.message || payload?.error || 'PDFエクスポートに失敗しました';
            useUIStore.getState().setStatusMessage(message, 4000);
        });

        this.setupCloseGuard();
    }

    private setupCloseGuard(): void {
        if (!this.runtime || !this.api) {
            return;
        }

        this.runtime.EventsOn('check-unsaved-before-close', () => {
            const docStore = useDocStore.getState();
            if (!docStore.hasUnsavedChanges) {
                this.api?.AllowClose();
                return;
            }

            const modalStore = useModalStore.getState();
            if (modalStore.unsavedConfirmModal.visible) {
                return;
            }

            modalStore.showUnsavedConfirmModal(
                async () => {
                    const saved = await this.saveCurrentFile();
                    if (saved) {
                        this.api?.AllowClose();
                    }
                },
                () => {
                    docStore.clearUnsavedChanges();
                    this.api?.AllowClose();
                }
            );
        });

        if (typeof window !== 'undefined' && !(window as any).go) {
            window.addEventListener('beforeunload', (event) => {
                if (useDocStore.getState().hasUnsavedChanges) {
                    event.preventDefault();
                    event.returnValue = '';
                }
            });
        }
    }

    private async saveCurrentFile(): Promise<boolean> {
        if (!this.api) {
            return false;
        }

        const docStore = useDocStore.getState();
        if (!docStore.currentPath) {
            useUIStore.getState().setStatusMessage('ファイルが選択されていません', 2000);
            return false;
        }

        if (docStore.currentPath.toLowerCase().endsWith('.pdf')) {
            useUIStore.getState().setStatusMessage('PDF閲覧中は保存できません', 2000);
            return false;
        }

        try {
            await this.api.SaveFile(docStore.currentPath, docStore.markdownContent);
            docStore.clearUnsavedChanges();
            useUIStore.getState().setStatusMessage('保存しました', 2000);
            return true;
        } catch (error) {
            console.error('Save failed:', error);
            useUIStore.getState().setStatusMessage('保存に失敗しました', 3000);
            return false;
        }
    }

    private async updateASRStatus(): Promise<void> {
        if (!this.api) {
            return;
        }

        try {
            const status = await this.api.GetASRStatus();
            useASRStore.getState().setStatus(status);

            // 初期化されていない場合は定期的にチェック
            if (status.initializing) {
                const startedAt = Date.now();
                const interval = setInterval(async () => {
                    try {
                        const newStatus = await this.api!.GetASRStatus();
                        const decision = getASRStatusPollingDecision(newStatus, Date.now() - startedAt);
                        if (decision === 'timeout') {
                            useASRStore.getState().setStatus(markASRInitializationStopped(newStatus));
                            useUIStore.getState().setStatusMessage('ASRの初期化がタイムアウトしました', 5000);
                            clearInterval(interval);
                            return;
                        }
                        useASRStore.getState().setStatus(newStatus);
                        if (decision === 'complete') {
                            clearInterval(interval);
                        }
                    } catch (error) {
                        const currentStatus = useASRStore.getState().status;
                        useASRStore.getState().setStatus(markASRInitializationStopped(currentStatus));
                        useUIStore.getState().setStatusMessage('ASRの初期化状態を取得できませんでした', 5000);
                        clearInterval(interval);
                        console.error('Failed to poll ASR status:', error);
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
        if (isPdfDocumentPath(docStore.currentPath) || isBoardDocumentPath(docStore.currentPath)) {
            return;
        }

        const previewGuard = captureDocumentPreviewGuard(docStore.currentPath);
        try {
            const content = await this.api.LoadFile(docStore.currentPath);
            const { prepared, html } = await renderMarkdownPreview(content, this.api, docStore.currentPath);
            commitGuardedDocumentPreview(previewGuard, this.buildPreviewHtml(prepared, html));
        } catch (error) {
            console.error('Failed to refresh preview:', error);
        }
    }

    private async loadCustomCss(): Promise<void> {
        if (!this.api) {
            return;
        }
        try {
            const css = await this.api.GetCustomCSS();
            useCustomCssStore.getState().setCustomCss(css);
        } catch (error) {
            console.error('Failed to load custom CSS:', error);
        }
    }

    private buildPreviewHtml(content: string, html: string): string {
        const customCss = useCustomCssStore.getState().customCss;
        const theme = useUIStore.getState().theme;
        const withCss = applyCustomCssToHtml(content, html, customCss, theme);
        return convertTimestampsToLinks(withCss);
    }

    private async refreshGraph(): Promise<void> {
        if (this.components.graphView) {
            await this.components.graphView.refresh();
        }
    }

    destroy(): void {
        cancelDocumentTransition(this.documentTransition);
        this.documentTransition = null;
        invalidateDocumentTransitions();
        // Stop the timer before component cleanup，then flush the final stable snapshot．
        eventLogger.stopAutoSave();

        // コンポーネントのクリーンアップ
        Object.values(this.components).forEach((component) => {
            if (component) {
                component.destroy();
            }
        });

        void eventLogger.destroy();
    }
}
