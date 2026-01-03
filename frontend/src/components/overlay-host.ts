import { BaseComponent } from './component-base';
import { useOverlayStore, useExportStore, useASRStore } from '../stores/index';
import { eventLogger } from '../utils/event-logger';

export class OverlayHost extends BaseComponent {
    private unsubscribe: (() => void)[] = [];
    private dropOverlay: HTMLElement | null = null;
    private transcriptionProgress: HTMLElement | null = null;
    private transcriptionProgressFill: HTMLElement | null = null;
    private transcriptionProgressText: HTMLElement | null = null;
    private pdfExportProgress: HTMLElement | null = null;
    private pdfExportProgressFill: HTMLElement | null = null;
    private pdfExportProgressText: HTMLElement | null = null;
    private asrStatusProgress: HTMLElement | null = null;
    private asrStatusProgressFill: HTMLElement | null = null;
    private asrStatusProgressText: HTMLElement | null = null;
    private isTranscriptionVisible = false;
    private isPdfVisible = false;
    private isAsrVisible = false;

    init(): void {
        eventLogger.log('OverlayHost', 'init');

        // オーバーレイ要素の取得
        this.dropOverlay = document.getElementById('dropOverlay');
        this.transcriptionProgress = document.getElementById('transcriptionProgress');
        this.transcriptionProgressFill = this.transcriptionProgress?.querySelector('.transcription-progress-fill') as HTMLElement;
        this.transcriptionProgressText = this.transcriptionProgress?.querySelector('.transcription-progress-text') as HTMLElement;
        this.pdfExportProgress = document.getElementById('pdfExportProgress');
        this.pdfExportProgressFill = this.pdfExportProgress?.querySelector('.transcription-progress-fill') as HTMLElement;
        this.pdfExportProgressText = this.pdfExportProgress?.querySelector('.transcription-progress-text') as HTMLElement;

        // ASRステータス進捗バー
        this.asrStatusProgress = document.getElementById('asrStatusProgress');
        this.asrStatusProgressFill = this.asrStatusProgress?.querySelector('.transcription-progress-fill') as HTMLElement;
        this.asrStatusProgressText = this.asrStatusProgress?.querySelector('.transcription-progress-text') as HTMLElement;

        // イベントリスナーの設定
        this.setupEventListeners();

        // 状態の購読
        this.subscribeToStores();

        // 初期状態でASRステータスを更新
        const asrStore = useASRStore.getState();
        this.updateASRStatus(asrStore.status, asrStore.isRecording);
    }

    private setupEventListeners(): void {
        // ドラッグ&ドロップイベント
        this.unsubscribe.push(
            this.addEventListener(document, 'dragover', (e) => {
                e.preventDefault();
                eventLogger.log('OverlayHost', 'drag-over');
                useOverlayStore.getState().showDropOverlay();
            })
        );

        this.unsubscribe.push(
            this.addEventListener(document, 'dragleave', (e) => {
                e.preventDefault();
                eventLogger.log('OverlayHost', 'drag-leave');
                useOverlayStore.getState().hideDropOverlay();
            })
        );

        this.unsubscribe.push(
            this.addEventListener(document, 'drop', (e) => {
                e.preventDefault();
                eventLogger.log('OverlayHost', 'drop');
                useOverlayStore.getState().hideDropOverlay();
                // ファイルドロップ処理は別のコンポーネントで実装
            })
        );
    }

    private subscribeToStores(): void {
        // オーバーレイストア
        this.unsubscribe.push(
            useOverlayStore.subscribe((state) => {
                if (this.dropOverlay) {
                    this.toggleClass(this.dropOverlay, 'visible', state.dropOverlay.visible);
                }
            })
        );

        // エクスポートストア
        this.unsubscribe.push(
            useExportStore.subscribe((state) => {
                // トランスクリプション進捗
                if (this.transcriptionProgress) {
                    this.transcriptionProgress.style.display = state.transcriptionProgress.visible ? 'flex' : 'none';
                }
                this.isTranscriptionVisible = state.transcriptionProgress.visible;
                if (this.transcriptionProgressFill) {
                    this.transcriptionProgressFill.style.width = `${state.transcriptionProgress.progress}%`;
                }
                if (this.transcriptionProgressText) {
                    this.transcriptionProgressText.textContent = state.transcriptionProgress.message;
                }

                // PDFエクスポート進捗
                if (this.pdfExportProgress) {
                    this.pdfExportProgress.style.display = state.pdfExportProgress.visible ? 'flex' : 'none';
                }
                this.isPdfVisible = state.pdfExportProgress.visible;
                if (this.pdfExportProgressFill) {
                    this.pdfExportProgressFill.style.width = `${state.pdfExportProgress.progress}%`;
                }
                if (this.pdfExportProgressText) {
                    this.pdfExportProgressText.textContent = state.pdfExportProgress.message;
                }

                this.updateStatusBarReservation();
            })
        );

        // ASR Store - ステータス表示
        this.unsubscribe.push(
            useASRStore.subscribe((state) => {
                this.updateASRStatus(state.status, state.isRecording);
            })
        );
    }

    private updateASRStatus(status: { initialized: boolean; initializing: boolean }, isRecording: boolean): void {
        if (!this.asrStatusProgress || !this.asrStatusProgressFill || !this.asrStatusProgressText) {
            return;
        }

        let statusText = 'ASR: 無効';
        let showProgress = status.initializing || isRecording;

        if (status.initializing) {
            statusText = 'ASR: 初期化中...';
            // 初期化中はインデターミネートバーを表示
            this.asrStatusProgressFill.classList.add('indeterminate');
            this.asrStatusProgressFill.style.width = '30%';
        } else if (status.initialized) {
            statusText = isRecording ? 'ASR: 録音中' : 'ASR: 準備完了';
            // 準備完了時はバーを非表示（テキストのみ）
            this.asrStatusProgressFill.classList.remove('indeterminate');
            this.asrStatusProgressFill.style.width = '0%';
        } else {
            // 無効時はバーを非表示
            this.asrStatusProgressFill.classList.remove('indeterminate');
            this.asrStatusProgressFill.style.width = '0%';
        }

        // ステータス表示の表示/非表示
        this.asrStatusProgress.style.display = showProgress ? 'flex' : 'none';
        this.asrStatusProgressText.textContent = statusText;
        this.isAsrVisible = showProgress;
        this.updateStatusBarReservation();
    }

    private updateStatusBarReservation(): void {
        const shouldReserve = this.isTranscriptionVisible || this.isPdfVisible || this.isAsrVisible;
        document.body.classList.toggle('status-bar-visible', shouldReserve);
    }

    destroy(): void {
        this.unsubscribe.forEach((unsub) => unsub());
        this.unsubscribe = [];
    }
}
