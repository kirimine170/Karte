import { BaseComponent } from './component-base';
import { useOverlayStore, useExportStore, useASRStore, useUIStore, useDocStore, useCustomCssStore } from '../stores/index';
import type { WailsAppAPI } from '../types/wails-api';
import { eventLogger } from '../utils/event-logger';
import { applyCustomCssToHtml } from '../utils/custom-css';
import { renderMarkdownPreview } from '../utils/preview-renderer';
import { convertTimestampsToLinks } from '../utils/preview-audio';

export class OverlayHost extends BaseComponent {
    private unsubscribe: (() => void)[] = [];
    private api: WailsAppAPI;
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
    private dragDepth = 0;

    constructor(api: WailsAppAPI, parent?: HTMLElement) {
        super(parent);
        this.api = api;
    }

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
        const isFileDrag = (event: DragEvent): boolean => {
            if (!event.dataTransfer) {
                return false;
            }
            const types = Array.from(event.dataTransfer.types || []);
            return types.includes('Files');
        };

        this.unsubscribe.push(
            this.addEventListener(window, 'dragenter', (event) => {
                if (!isFileDrag(event)) {
                    return;
                }
                event.preventDefault();
                this.dragDepth += 1;
                eventLogger.log('OverlayHost', 'drag-enter');
                useOverlayStore.getState().showDropOverlay();
            })
        );

        this.unsubscribe.push(
            this.addEventListener(window, 'dragover', (event) => {
                if (!isFileDrag(event)) {
                    return;
                }
                event.preventDefault();
                eventLogger.log('OverlayHost', 'drag-over');
            })
        );

        this.unsubscribe.push(
            this.addEventListener(window, 'dragleave', (event) => {
                if (!isFileDrag(event)) {
                    return;
                }
                this.dragDepth = Math.max(0, this.dragDepth - 1);
                if (this.dragDepth === 0) {
                    eventLogger.log('OverlayHost', 'drag-leave');
                    useOverlayStore.getState().hideDropOverlay();
                }
            })
        );

        this.unsubscribe.push(
            this.addEventListener(window, 'drop', (event) => {
                if (!isFileDrag(event)) {
                    return;
                }
                event.preventDefault();
                this.dragDepth = 0;
                eventLogger.log('OverlayHost', 'drop');
                useOverlayStore.getState().hideDropOverlay();
                const files = Array.from(event.dataTransfer?.files || []);
                if (files.length === 0) {
                    return;
                }
                this.handleFileDrop(files).catch((error) => {
                    console.error('handleFileDrop failed:', error);
                });
            })
        );

        this.unsubscribe.push(
            this.addEventListener(window, 'karte-file-drop' as keyof HTMLElementEventMap, (event) => {
                const customEvent = event as CustomEvent<{ files?: File[] }>;
                const files = customEvent.detail?.files;
                if (files && files.length > 0) {
                    this.handleFileDrop(files).catch((error) => {
                        console.error('handleFileDrop failed:', error);
                    });
                }
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

    private async handleFileDrop(files: File[]): Promise<void> {
        const hasAudio = files.some((file) => this.isSupportedAudioFile(file.name));
        const hasImage = files.some((file) => this.isSupportedImageFile(file.name));
        const hasPdf = files.some((file) => this.isSupportedPdfFile(file.name));
        const hasCsv = files.some((file) => this.isSupportedCsvFile(file.name));

        const tasks: Promise<void>[] = [];
        if (hasAudio) {
            tasks.push(this.handleAudioDrop(files));
        }
        if (hasImage) {
            tasks.push(this.handleImageDrop(files));
        }
        if (hasPdf) {
            tasks.push(this.handlePdfDrop(files));
        }
        if (hasCsv) {
            tasks.push(this.handleCsvDrop(files));
        }

        if (tasks.length === 0) {
            useUIStore.getState().setStatusMessage('対応していないファイル形式です', 3000);
            return;
        }

        await Promise.all(tasks);
    }

    private isSupportedAudioFile(name = ''): boolean {
        const lower = name.toLowerCase();
        return ['.wav', '.mp3', '.m4a'].some((ext) => lower.endsWith(ext));
    }

    private isSupportedImageFile(name = ''): boolean {
        const lower = name.toLowerCase();
        return ['.jpg', '.jpeg', '.png', '.gif', '.webp'].some((ext) => lower.endsWith(ext));
    }

    private isSupportedPdfFile(name = ''): boolean {
        return name.toLowerCase().endsWith('.pdf');
    }

    private isSupportedCsvFile(name = ''): boolean {
        return name.toLowerCase().endsWith('.csv');
    }

    private getFilePath(file: File): string | null {
        const fileWithPath = file as File & { path?: string };
        return fileWithPath.path || null;
    }

    private async handleAudioDrop(files: File[]): Promise<void> {
        const audioFiles = files.filter((file) => this.isSupportedAudioFile(file.name));
        if (audioFiles.length === 0) {
            useUIStore.getState().setStatusMessage('対応していない音声形式です (wav/mp3/m4a)', 3000);
            return;
        }

        for (const file of audioFiles) {
            try {
                useUIStore.getState().setStatusMessage(`音声を取り込み中: ${file.name}`);
                const path = this.getFilePath(file);
                if (path) {
                    await this.api.ImportAudioFile(path);
                } else {
                    const base64 = await this.readFileAsBase64(file);
                    if (base64) {
                        await this.api.ImportAudioBase64(file.name || `audio-${Date.now()}.wav`, base64);
                    } else {
                        useUIStore.getState().setStatusMessage('音声データにアクセスできませんでした', 4000);
                    }
                }
            } catch (error) {
                console.error('ImportAudioFile failed', error);
                useUIStore.getState().setStatusMessage(`音声取り込みに失敗: ${this.formatError(error)}`, 4000);
            }
        }

        await this.refreshFileList();
        useUIStore.getState().setStatusMessage('音声ファイルを保存しました。文字起こしを開始します…', 3500);
    }

    private async handleImageDrop(files: File[]): Promise<void> {
        const imageFiles = files.filter((file) => this.isSupportedImageFile(file.name));
        if (imageFiles.length === 0) {
            useUIStore.getState().setStatusMessage('対応していない画像形式です (jpg/png/gif/webp)', 3000);
            return;
        }

        for (const file of imageFiles) {
            try {
                useUIStore.getState().setStatusMessage(`画像を取り込み中: ${file.name}`);
                const path = this.getFilePath(file);
                if (path) {
                    await this.api.ImportImageFile(path);
                } else {
                    const base64 = await this.readFileAsBase64(file);
                    if (base64) {
                        await this.api.ImportImageBase64(file.name || `image-${Date.now()}.png`, base64);
                    } else {
                        useUIStore.getState().setStatusMessage('画像データにアクセスできませんでした', 4000);
                    }
                }
            } catch (error) {
                console.error('ImportImageFile failed', error);
                useUIStore.getState().setStatusMessage(`画像取り込みに失敗: ${this.formatError(error)}`, 4000);
            }
        }

        useUIStore.getState().setStatusMessage('画像ファイルを保存しました。', 3000);
    }

    private async handleCsvDrop(files: File[]): Promise<void> {
        const csvFiles = files.filter((file) => this.isSupportedCsvFile(file.name));
        if (csvFiles.length === 0) {
            useUIStore.getState().setStatusMessage('対応していないファイル形式です (csv)', 3000);
            return;
        }

        for (const file of csvFiles) {
            try {
                useUIStore.getState().setStatusMessage(`CSVを取り込み中: ${file.name}`);
                const path = this.getFilePath(file);
                if (path) {
                    await this.api.ImportCsvFile(path);
                } else {
                    const base64 = await this.readFileAsBase64(file);
                    if (base64) {
                        await this.api.ImportCsvBase64(file.name || `data-${Date.now()}.csv`, base64);
                    } else {
                        useUIStore.getState().setStatusMessage('CSVデータにアクセスできませんでした', 4000);
                    }
                }
            } catch (error) {
                console.error('ImportCsvFile failed', error);
                useUIStore.getState().setStatusMessage(`CSV取り込みに失敗: ${this.formatError(error)}`, 4000);
            }
        }

        await this.refreshFileList();
        useUIStore.getState().setStatusMessage('CSVファイルを保存しました。', 3000);
    }

    private async handlePdfDrop(files: File[]): Promise<void> {
        const pdfFiles = files.filter((file) => this.isSupportedPdfFile(file.name));
        if (pdfFiles.length === 0) {
            useUIStore.getState().setStatusMessage('対応していないファイル形式です (pdf)', 3000);
            return;
        }

        for (const file of pdfFiles) {
            try {
                useUIStore.getState().setStatusMessage(`PDFを取り込み中: ${file.name}`);
                const path = this.getFilePath(file);
                if (path) {
                    const pdfPath = await this.api.ImportPdfFile(path);
                    await this.loadFile(pdfPath);
                } else {
                    const base64 = await this.readFileAsBase64(file);
                    if (base64) {
                        const pdfPath = await this.api.ImportPdfBase64(file.name || `document-${Date.now()}.pdf`, base64);
                        await this.loadFile(pdfPath);
                    } else {
                        useUIStore.getState().setStatusMessage('PDFデータにアクセスできませんでした', 4000);
                    }
                }
            } catch (error) {
                console.error('ImportPdfFile failed', error);
                useUIStore.getState().setStatusMessage(`PDF取り込みに失敗: ${this.formatError(error)}`, 4000);
            }
        }

        await this.refreshFileList();
        useUIStore.getState().setStatusMessage('PDFファイルを保存しました。', 3000);
    }

    private async loadFile(path: string): Promise<void> {
        if (!path) {
            return;
        }
        try {
            const content = await this.api.LoadFile(path);
            const docStore = useDocStore.getState();
            docStore.setCurrentPath(path);
            if (path.toLowerCase().endsWith('.pdf')) {
                docStore.setMarkdownContent('');
                docStore.setPreviewHtml('');
                docStore.clearUnsavedChanges();
                return;
            }

            docStore.setMarkdownContent(content);
            docStore.clearUnsavedChanges();

            const { prepared, html } = await renderMarkdownPreview(content, this.api, path);
            const finalHtml = this.buildPreviewHtml(prepared, html);
            docStore.setPreviewHtml(finalHtml);
        } catch (error) {
            console.error('Failed to load file after import:', error);
        }
    }

    private buildPreviewHtml(content: string, html: string): string {
        const customCss = useCustomCssStore.getState().customCss;
        const theme = useUIStore.getState().theme;
        const withCss = applyCustomCssToHtml(content, html, customCss, theme);
        return convertTimestampsToLinks(withCss);
    }

    private async refreshFileList(): Promise<void> {
        try {
            const files = await this.api.GetFileList();
            useDocStore.getState().setFiles(files);
        } catch (error) {
            console.error('Failed to refresh file list:', error);
        }
    }

    private async readFileAsBase64(file: File): Promise<string | null> {
        if (!file.arrayBuffer) {
            return null;
        }
        const buffer = await file.arrayBuffer();
        return this.arrayBufferToBase64(buffer);
    }

    private arrayBufferToBase64(buffer: ArrayBuffer): string {
        const bytes = new Uint8Array(buffer);
        let binary = '';
        const chunkSize = 0x8000;
        for (let i = 0; i < bytes.length; i += chunkSize) {
            const chunk = bytes.subarray(i, i + chunkSize);
            binary += String.fromCharCode.apply(null, chunk as unknown as number[]);
        }
        return btoa(binary);
    }

    private formatError(error: unknown): string {
        if (error instanceof Error) {
            return error.message;
        }
        return String(error);
    }

    destroy(): void {
        this.unsubscribe.forEach((unsub) => unsub());
        this.unsubscribe = [];
    }
}
