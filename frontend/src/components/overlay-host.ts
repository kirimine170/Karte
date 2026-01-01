import { BaseComponent } from './component-base';
import { useOverlayStore, useExportStore } from '../stores/index';
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

        // イベントリスナーの設定
        this.setupEventListeners();

        // 状態の購読
        this.subscribeToStores();
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
                if (this.pdfExportProgressFill) {
                    this.pdfExportProgressFill.style.width = `${state.pdfExportProgress.progress}%`;
                }
                if (this.pdfExportProgressText) {
                    this.pdfExportProgressText.textContent = state.pdfExportProgress.message;
                }
            })
        );
    }

    destroy(): void {
        this.unsubscribe.forEach((unsub) => unsub());
        this.unsubscribe = [];
    }
}

