import { BaseComponent } from './component-base';
import { useUIStore, useDocStore, useASRStore } from '../stores/index';
import { convertMarkdownToHtml } from '../logic';
import type { WailsAppAPI } from '../types/wails-api';
import { eventLogger } from '../utils/event-logger';

export class EditorLayout extends BaseComponent {
    private unsubscribe: (() => void)[] = [];
    private api: WailsAppAPI;

    // DOM要素
    private editor: HTMLTextAreaElement | null = null;
    private preview: HTMLIFrameElement | null = null;
    private recordingBtn: HTMLButtonElement | null = null;
    private recordingIndicator: HTMLElement | null = null;
    private micLevelFill: HTMLElement | null = null;
    private audioPlayerContainer: HTMLElement | null = null;
    private audioPlayer: HTMLAudioElement | null = null;
    private realtimeTranscript: HTMLElement | null = null;
    private realtimeTranscriptContent: HTMLElement | null = null;

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

        // イベントリスナーの設定
        this.setupEventListeners();

        // 状態の購読
        this.subscribeToStores();

        // 初期状態の反映
        this.updateUI();
    }

    private setupEventListeners(): void {
        const docStore = useDocStore.getState();
        const asrStore = useASRStore.getState();

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
        this.unsubscribe.push(
            this.addEventListener(document, 'keydown', async (e) => {
                if ((e.ctrlKey || e.metaKey) && e.key === 's') {
                    e.preventDefault();
                    eventLogger.log('EditorLayout', 'keyboard-shortcut-save');
                    await this.handleSave();
                }
            })
        );
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
                if (this.editor && this.editor.value !== state.markdownContent) {
                    this.editor.value = state.markdownContent;
                }
                if (state.previewHtml) {
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
        
        if (galleryArea) {
            if (shouldShowGalleryArea) {
                // galleryAreaを先に表示（これがないと子要素が表示されない）
                contentArea.classList.remove('gallery-hidden');
                galleryArea.style.display = 'flex';
                eventLogger.log('EditorLayout', 'gallery-area-show');
            } else {
                // 両方非表示の場合は、galleryAreaも非表示
                contentArea.classList.add('gallery-hidden');
                galleryArea.style.display = 'none';
                eventLogger.log('EditorLayout', 'gallery-area-hide');
            }
        }

        // 個別のギャラリーの表示/非表示（galleryAreaが表示されている状態で制御）
        if (imageGallery) {
            if (uiState.imageGalleryVisible) {
                contentArea.classList.remove('image-gallery-hidden');
                imageGallery.style.display = 'flex';
                eventLogger.log('EditorLayout', 'image-gallery-show');
            } else {
                contentArea.classList.add('image-gallery-hidden');
                imageGallery.style.display = 'none';
                eventLogger.log('EditorLayout', 'image-gallery-hide');
            }
        }

        if (csvGallery) {
            if (uiState.csvGalleryVisible) {
                contentArea.classList.remove('csv-gallery-hidden');
                csvGallery.style.display = 'flex';
                eventLogger.log('EditorLayout', 'csv-gallery-show');
            } else {
                contentArea.classList.add('csv-gallery-hidden');
                csvGallery.style.display = 'none';
                eventLogger.log('EditorLayout', 'csv-gallery-hide');
            }
        }

        eventLogger.log('EditorLayout', 'layout-update', {
            imageGalleryVisible: uiState.imageGalleryVisible,
            csvGalleryVisible: uiState.csvGalleryVisible,
            galleryAreaVisible: shouldShowGalleryArea
        });
    }

    private async updatePreview(content: string): Promise<void> {
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
            try {
                await this.api.StartRecording();
                asrStore.setIsRecording(true);
                asrStore.clearRealtimeTranscript();
                eventLogger.log('EditorLayout', 'recording-start-success');
            } catch (error) {
                console.error('Failed to start recording:', error);
                eventLogger.log('EditorLayout', 'recording-start-error', { error: String(error) });
                useUIStore.getState().setStatusMessage('録音の開始に失敗しました', 3000);
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

