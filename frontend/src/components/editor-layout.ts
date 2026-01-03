import { BaseComponent } from './component-base';
import { useUIStore, useDocStore, useASRStore, useOverlayStore } from '../stores/index';
import type { WailsAppAPI } from '../types/wails-api';
import { eventLogger } from '../utils/event-logger';

export class EditorLayout extends BaseComponent {
    private unsubscribe: (() => void)[] = [];
    private api: WailsAppAPI;

    // DOM要素
    private editor: HTMLTextAreaElement | null = null;
    private preview: HTMLIFrameElement | null = null;
    private recordingBtn: HTMLButtonElement | null = null;
    private recordingBtnFooter: HTMLButtonElement | null = null;
    private recordingIndicator: HTMLElement | null = null;
    private recordingIndicatorFooter: HTMLElement | null = null;
    private micLevelFill: HTMLElement | null = null;
    private micLevelFillFooter: HTMLElement | null = null;
    private audioPlayerContainer: HTMLElement | null = null;
    private audioPlayer: HTMLAudioElement | null = null;
    private realtimeTranscript: HTMLElement | null = null;
    private realtimeTranscriptContent: HTMLElement | null = null;
    private pdfPane: HTMLElement | null = null;
    private pdfPreview: HTMLIFrameElement | null = null;
    private lastPath = '';

    constructor(api: WailsAppAPI, parent?: HTMLElement) {
        super(parent);
        this.api = api;
    }

    init(): void {
        eventLogger.log('EditorLayout', 'init');

        const contentArea = document.getElementById('contentArea');
        if (!contentArea) {
            console.error('EditorLayout: #contentArea element not found');
            return;
        }
        this.element = contentArea as HTMLElement;

        // DOM要素の取得
        this.editor = document.getElementById('editor') as HTMLTextAreaElement;
        this.preview = document.getElementById('preview') as HTMLIFrameElement;
        this.recordingBtn = document.querySelector('.tabs #recordingBtn') as HTMLButtonElement;
        this.recordingBtnFooter = document.getElementById('recordingBtnFooter') as HTMLButtonElement;
        this.recordingIndicator = document.getElementById('recordingIndicator');
        this.recordingIndicatorFooter = document.getElementById('recordingIndicatorFooter');
        this.micLevelFill = document.getElementById('micLevelFill');
        this.micLevelFillFooter = document.getElementById('micLevelFillFooter');
        this.audioPlayerContainer = document.getElementById('audioPlayerContainer');
        this.audioPlayer = document.getElementById('audioPlayer') as HTMLAudioElement;
        this.realtimeTranscript = document.getElementById('realtimeTranscript');
        this.realtimeTranscriptContent = document.getElementById('realtimeTranscriptContent');
        this.pdfPane = document.getElementById('pdfPane');
        this.pdfPreview = document.getElementById('pdfPreview') as HTMLIFrameElement;

        // イベントリスナーの設定
        this.setupEventListeners();

        // 状態の購読
        this.subscribeToStores();

        // 初期状態の反映
        this.updateUI();
    }

    private setupEventListeners(): void {
        const docStore = useDocStore.getState();

        // エディタの入力イベント
        if (this.editor) {
            this.unsubscribe.push(
                this.addEventListener(this.editor, 'input', (e) => {
                    const target = e.target as HTMLTextAreaElement;
                    const content = target.value;
                    eventLogger.log('EditorLayout', 'editor-input', { contentLength: content.length });
                    docStore.setMarkdownContent(content);
                    docStore.setHasUnsavedChanges(true);
                    this.updatePreview(content);
                })
            );
        }

        if (this.editor) {
            this.unsubscribe.push(
                this.addEventListener(this.editor, 'dragover', (e) => {
                    const types = Array.from(e.dataTransfer?.types || []);
                    if (types.includes('application/json') || types.includes('text/plain')) {
                        e.preventDefault();
                        e.dataTransfer.dropEffect = 'copy';
                    }
                })
            );

            this.unsubscribe.push(
                this.addEventListener(this.editor, 'drop', (e) => {
                    e.preventDefault();
                    const jsonData = e.dataTransfer?.getData('application/json');
                    if (jsonData) {
                        try {
                            const payload = JSON.parse(jsonData) as { type?: string; path?: string };
                            if (payload.type === 'csv' && payload.path) {
                                this.insertCsvAtCursor(payload.path);
                            }
                        } catch (error) {
                            console.error('Failed to parse drag data:', error);
                        }
                        return;
                    }

                    const path = e.dataTransfer?.getData('text/plain');
                    if (path) {
                        const csvItem = document.querySelector(`.csv-item[data-csv-path="${path}"]`);
                        if (csvItem) {
                            this.insertCsvAtCursor(path);
                        }
                    }
                })
            );
        }

        if (this.pdfPreview) {
            this.unsubscribe.push(
                this.addEventListener(this.pdfPreview, 'dragover', (e) => {
                    e.preventDefault();
                    useOverlayStore.getState().showDropOverlay();
                })
            );
            this.unsubscribe.push(
                this.addEventListener(this.pdfPreview, 'dragleave', () => {
                    useOverlayStore.getState().hideDropOverlay();
                })
            );
            this.unsubscribe.push(
                this.addEventListener(this.pdfPreview, 'drop', (e) => {
                    e.preventDefault();
                    useOverlayStore.getState().hideDropOverlay();
                    const files = Array.from(e.dataTransfer?.files || []);
                    if (files.length === 0) {
                        return;
                    }
                    window.dispatchEvent(new CustomEvent('karte-file-drop', { detail: { files } }));
                })
            );
        }

        // 録音ボタン（タブ内）
        if (this.recordingBtn) {
            this.unsubscribe.push(
                this.addEventListener(this.recordingBtn, 'click', async () => {
                    await this.toggleRecording();
                })
            );
        }

        // 録音ボタン（フッター）
        if (this.recordingBtnFooter) {
            this.unsubscribe.push(
                this.addEventListener(this.recordingBtnFooter, 'click', async () => {
                    await this.toggleRecording();
                })
            );
        }

        // キーボードショートカット（Ctrl/Cmd+S）
        const keydownHandler = async (e: KeyboardEvent) => {
            if ((e.ctrlKey || e.metaKey) && e.key === 's') {
                e.preventDefault();
                eventLogger.log('EditorLayout', 'keyboard-shortcut-save');
                await this.handleSave();
            }
        };
        document.addEventListener('keydown', keydownHandler);
        this.unsubscribe.push(() => {
            document.removeEventListener('keydown', keydownHandler);
        });
    }

    private subscribeToStores(): void {
        // UI Store - レイアウトの表示/非表示
        this.unsubscribe.push(
            useUIStore.subscribe((state) => {
                this.updateLayout(state);
            })
        );

        // Doc Store - マークダウンコンテンツ
        this.unsubscribe.push(
            useDocStore.subscribe((state) => {
                if (state.currentPath !== this.lastPath) {
                    this.lastPath = state.currentPath;
                    const isPdf = state.currentPath.toLowerCase().endsWith('.pdf');
                    this.setPdfMode(isPdf);
                    if (isPdf) {
                        this.updatePdfPreview(state.currentPath);
                    }
                }
                if (this.editor && this.editor.value !== state.markdownContent) {
                    this.editor.value = state.markdownContent;
                }
                if (state.previewHtml && !state.currentPath.toLowerCase().endsWith('.pdf')) {
                    this.updatePreviewFrame(state.previewHtml);
                }
            })
        );

        // ASR Store - 録音状態
        this.unsubscribe.push(
            useASRStore.subscribe((state) => {
                this.updateRecordingUI(state.isRecording, state.micLevel, state.realtimeTranscript);
            })
        );
    }

    private updateUI(): void {
        const uiStore = useUIStore.getState();
        const docStore = useDocStore.getState();

        this.lastPath = docStore.currentPath;
        const isPdf = docStore.currentPath.toLowerCase().endsWith('.pdf');
        this.setPdfMode(isPdf);
        if (isPdf && docStore.currentPath) {
            this.updatePdfPreview(docStore.currentPath);
        }
        this.updateLayout(uiStore);
        if (this.editor) {
            this.editor.value = docStore.markdownContent;
        }
    }

    private updateLayout(uiState: ReturnType<typeof useUIStore.getState>): void {
        if (!this.element) return;

        const contentArea = this.element as HTMLElement;
        const galleryArea = document.getElementById('galleryArea');
        const imageGallery = document.getElementById('imageGalleryContainer');
        const csvGallery = document.getElementById('csvGalleryContainer');

        // まず、galleryAreaの表示/非表示を決定
        const shouldShowGalleryArea = uiState.imageGalleryVisible || uiState.csvGalleryVisible;

        // 現在のgalleryAreaの表示状態を確認
        const currentGalleryAreaVisible = galleryArea ? galleryArea.style.display !== 'none' : false;

        if (galleryArea) {
            if (shouldShowGalleryArea) {
                // galleryAreaを先に表示（これがないと子要素が表示されない）
                if (!currentGalleryAreaVisible) {
                    contentArea.classList.remove('gallery-hidden');
                    galleryArea.style.display = 'flex';
                    eventLogger.log('EditorLayout', 'gallery-area-show', {
                        imageGalleryVisible: uiState.imageGalleryVisible,
                        csvGalleryVisible: uiState.csvGalleryVisible
                    });
                }
            } else {
                // 両方非表示の場合は、galleryAreaも非表示
                if (currentGalleryAreaVisible) {
                    contentArea.classList.add('gallery-hidden');
                    galleryArea.style.display = 'none';
                    eventLogger.log('EditorLayout', 'gallery-area-hide', {
                        imageGalleryVisible: uiState.imageGalleryVisible,
                        csvGalleryVisible: uiState.csvGalleryVisible
                    });
                }
            }
        }

        // 個別のギャラリーの表示/非表示（galleryAreaが表示されている状態で制御）
        if (imageGallery) {
            const currentImageGalleryVisible = imageGallery.style.display !== 'none';
            if (uiState.imageGalleryVisible) {
                if (!currentImageGalleryVisible) {
                    contentArea.classList.remove('image-gallery-hidden');
                    imageGallery.style.display = 'flex';
                    eventLogger.log('EditorLayout', 'image-gallery-show');
                }
            } else {
                if (currentImageGalleryVisible) {
                    contentArea.classList.add('image-gallery-hidden');
                    imageGallery.style.display = 'none';
                    eventLogger.log('EditorLayout', 'image-gallery-hide');
                }
            }
        }

        if (csvGallery) {
            const currentCsvGalleryVisible = csvGallery.style.display !== 'none';
            if (uiState.csvGalleryVisible) {
                if (!currentCsvGalleryVisible) {
                    contentArea.classList.remove('csv-gallery-hidden');
                    csvGallery.style.display = 'flex';
                    eventLogger.log('EditorLayout', 'csv-gallery-show');
                }
            } else {
                if (currentCsvGalleryVisible) {
                    contentArea.classList.add('csv-gallery-hidden');
                    csvGallery.style.display = 'none';
                    eventLogger.log('EditorLayout', 'csv-gallery-hide');
                }
            }
        }

        eventLogger.log('EditorLayout', 'layout-update', {
            imageGalleryVisible: uiState.imageGalleryVisible,
            csvGalleryVisible: uiState.csvGalleryVisible,
            galleryAreaVisible: shouldShowGalleryArea
        });
    }

    private async updatePreview(content: string): Promise<void> {
        if (useDocStore.getState().currentPath.toLowerCase().endsWith('.pdf')) {
            return;
        }
        try {
            const html = await this.api.PreviewMarkdown(content);
            useDocStore.getState().setPreviewHtml(html);
            this.updatePreviewFrame(html);
        } catch (error) {
            console.error('Failed to update preview:', error);
        }
    }

    private updatePreviewFrame(html: string): void {
        if (!this.preview || !this.preview.contentDocument) {
            return;
        }

        this.preview.contentDocument.open();
        this.preview.contentDocument.write(html);
        this.preview.contentDocument.close();
    }

    private async updatePdfPreview(path: string): Promise<void> {
        if (!this.pdfPreview) {
            return;
        }
        try {
            const pdfUrl = await this.api.GetPdfFileURL(path);
            const pdfHtml = this.buildPdfHtml(pdfUrl);
            this.pdfPreview.srcdoc = pdfHtml;
        } catch (error) {
            console.error('Failed to load PDF:', error);
            this.pdfPreview.srcdoc = `<html><body><p>PDFの読み込みに失敗しました: ${String(error)}</p></body></html>`;
        }
    }

    private buildPdfHtml(pdfUrl: string): string {
        return `<!DOCTYPE html>
<html>
<head>
    <meta charset="utf-8">
    <title>PDF Viewer</title>
    <style>
        html, body {
            margin: 0;
            padding: 0;
            height: 100%;
            overflow: hidden;
            background: #525252;
        }
        embed {
            width: 100%;
            height: 100%;
            border: none;
        }
    </style>
</head>
<body>
    <embed src="${pdfUrl}" type="application/pdf" />
</body>
</html>`;
    }

    private setPdfMode(isPdf: boolean): void {
        if (!this.element) {
            return;
        }
        this.element.classList.toggle('pdf-mode', isPdf);
        if (this.pdfPane) {
            this.pdfPane.style.display = isPdf ? 'flex' : 'none';
        }
    }

    private updateRecordingUI(isRecording: boolean, micLevel: number, transcript: { partial: string; final: string[] }): void {
        // タブ内の録音ボタン
        if (this.recordingBtn) {
            if (isRecording) {
                this.recordingBtn.classList.add('recording');
                const label = this.recordingBtn.querySelector('.recording-label');
                if (label) label.textContent = '録音中';
            } else {
                this.recordingBtn.classList.remove('recording');
                const label = this.recordingBtn.querySelector('.recording-label');
                if (label) label.textContent = '録音';
            }
        }

        // フッターの録音ボタン
        if (this.recordingBtnFooter) {
            if (isRecording) {
                this.recordingBtnFooter.classList.add('recording');
                const label = this.recordingBtnFooter.querySelector('span:last-child');
                if (label) label.textContent = '録音中';
            } else {
                this.recordingBtnFooter.classList.remove('recording');
                const label = this.recordingBtnFooter.querySelector('span:last-child');
                if (label) label.textContent = '録音';
            }
        }

        if (this.recordingIndicator) {
            this.recordingIndicator.style.display = isRecording ? 'flex' : 'none';
        }

        if (this.recordingIndicatorFooter) {
            this.recordingIndicatorFooter.style.display = isRecording ? 'flex' : 'none';
        }

        if (this.micLevelFill) {
            this.micLevelFill.style.width = `${micLevel}%`;
        }

        if (this.micLevelFillFooter) {
            this.micLevelFillFooter.style.width = `${micLevel}%`;
        }

        if (this.realtimeTranscript) {
            this.realtimeTranscript.style.display = isRecording ? 'flex' : 'none';
        }

        if (this.realtimeTranscriptContent) {
            const finalText = transcript.final.join('\n');
            const partialText = transcript.partial ? `<div class="transcript-partial">${transcript.partial}</div>` : '';
            this.realtimeTranscriptContent.innerHTML = finalText + partialText;
        }
    }

    private insertCsvAtCursor(path: string): void {
        if (!this.editor) {
            return;
        }

        const cursorPos = this.editor.selectionStart ?? this.editor.value.length;
        const csvMarkdown = `@import(type="csv", path="${path}")\n`;
        const currentValue = this.editor.value;
        const nextValue = currentValue.slice(0, cursorPos) + csvMarkdown + currentValue.slice(cursorPos);
        this.editor.value = nextValue;
        const nextCursor = cursorPos + csvMarkdown.length;
        this.editor.setSelectionRange(nextCursor, nextCursor);

        const docStore = useDocStore.getState();
        docStore.setMarkdownContent(nextValue);
        docStore.setHasUnsavedChanges(true);
        this.updatePreview(nextValue);
    }

    private async toggleRecording(): Promise<void> {
        const asrStore = useASRStore.getState();

        if (asrStore.isRecording) {
            // 録音停止
            eventLogger.log('EditorLayout', 'recording-stop-start');
            try {
                const audioPath = await this.api.StopRecording();
                asrStore.setIsRecording(false);
                asrStore.setRecordingTranscriptPath(audioPath);
                eventLogger.log('EditorLayout', 'recording-stop-success', { audioPath });

                // オーディオプレーヤーを表示
                if (this.audioPlayerContainer && this.audioPlayer) {
                    const audioURL = await this.api.GetAudioFileURL(audioPath);
                    this.audioPlayer.src = audioURL;
                    this.audioPlayerContainer.style.display = 'block';
                }
            } catch (error) {
                console.error('Failed to stop recording:', error);
                eventLogger.log('EditorLayout', 'recording-stop-error', { error: String(error) });
                useUIStore.getState().setStatusMessage('録音の停止に失敗しました', 3000);
            }
        } else {
            // 録音開始
            eventLogger.log('EditorLayout', 'recording-start');

            // ASRステータスを確認
            try {
                const asrStatus = await this.api.GetASRStatus();
                if (!asrStatus.initialized && !asrStatus.initializing) {
                    eventLogger.log('EditorLayout', 'recording-start-error', {
                        error: 'ASR service not ready',
                        status: asrStatus
                    });
                    useUIStore.getState().setStatusMessage(
                        'ASRサービスが利用できません。デスクトップアプリで実行してください。',
                        5000
                    );
                    return;
                }
            } catch (error) {
                console.error('Failed to get ASR status:', error);
                eventLogger.log('EditorLayout', 'recording-start-error', {
                    error: 'Failed to get ASR status',
                    details: String(error)
                });
                useUIStore.getState().setStatusMessage('ASRサービスの状態を取得できませんでした', 3000);
                return;
            }

            try {
                await this.api.StartRecording();
                asrStore.setIsRecording(true);
                asrStore.clearRealtimeTranscript();
                eventLogger.log('EditorLayout', 'recording-start-success');
            } catch (error) {
                console.error('Failed to start recording:', error);
                eventLogger.log('EditorLayout', 'recording-start-error', { error: String(error) });
                let errorMsg = '録音の開始に失敗しました';
                if (error) {
                    if (error instanceof Error) {
                        errorMsg = error.message;
                    } else {
                        errorMsg = String(error);
                    }
                }
                useUIStore.getState().setStatusMessage(errorMsg, 3000);
            }
        }
    }

    private async handleSave(): Promise<void> {
        const docStore = useDocStore.getState();
        if (!docStore.currentPath) {
            eventLogger.log('EditorLayout', 'save-error', { error: 'no-file-selected' });
            useUIStore.getState().setStatusMessage('ファイルが選択されていません', 2000);
            return;
        }

        try {
            eventLogger.log('EditorLayout', 'save-start', { path: docStore.currentPath });
            await this.api.SaveFile(docStore.currentPath, docStore.markdownContent);
            docStore.clearUnsavedChanges();
            eventLogger.log('EditorLayout', 'save-success', { path: docStore.currentPath });
            useUIStore.getState().setStatusMessage('保存しました', 2000);
        } catch (error) {
            console.error('Save failed:', error);
            eventLogger.log('EditorLayout', 'save-error', { error: String(error) });
            useUIStore.getState().setStatusMessage('保存に失敗しました', 3000);
        }
    }

    destroy(): void {
        this.unsubscribe.forEach((unsub) => unsub());
        this.unsubscribe = [];
    }
}
