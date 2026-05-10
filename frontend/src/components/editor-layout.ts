import { BaseComponent } from './component-base';
import { useUIStore, useDocStore, useASRStore, useOverlayStore, useCustomCssStore } from '../stores/index';
import type { WailsAppAPI } from '../types/wails-api';
import { eventLogger } from '../utils/event-logger';
import { applyCustomCssToHtml } from '../utils/custom-css';
import { prepareMarkdownForPreview } from '../utils/preview-content';
import { writePreviewFrame } from '../utils/preview-frame';
import { convertTimestampsToLinks, updateAudioPlayerFromContent } from '../utils/preview-audio';
import { GlobalWorkerOptions, getDocument, type PDFDocumentProxy } from 'pdfjs-dist/legacy/build/pdf.mjs';
import pdfWorkerUrl from 'pdfjs-dist/legacy/build/pdf.worker.min.mjs?url';

GlobalWorkerOptions.workerSrc = pdfWorkerUrl;

export class EditorLayout extends BaseComponent {
    private unsubscribe: (() => void)[] = [];
    private api: WailsAppAPI;

    // DOM要素
    private editor: HTMLTextAreaElement | null = null;
    private preview: HTMLIFrameElement | null = null;
    private previewBody: HTMLElement | null = null;
    private previewDropTarget: HTMLElement | null = null;
    private previewDropHandlersBound = false;
    private previewDragDepth = 0;
    private lastPreviewDropTs = 0;
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
    private lastPath = '';
    private pdfDoc: PDFDocumentProxy | null = null;
    private pdfSourceUrl = '';
    private pdfPageCount = 0;
    private pdfViewMode: 'single' | 'spread' | 'scroll' = 'single';
    private pdfCoverEnabled = true;
    private pdfBinding: 'rtl' | 'ltr' = 'ltr';
    private pdfPageNumber = 1;
    private pdfSpreadIndex = 0;
    private pdfRenderRequestId = 0;
    private pdfCanvasContainer: HTMLElement | null = null;
    private pdfCanvasLeft: HTMLCanvasElement | null = null;
    private pdfCanvasRight: HTMLCanvasElement | null = null;
    private pdfScrollContainer: HTMLElement | null = null;
    private pdfEmpty: HTMLElement | null = null;
    private pdfPageInfo: HTMLElement | null = null;
    private pdfPrevBtn: HTMLButtonElement | null = null;
    private pdfNextBtn: HTMLButtonElement | null = null;
    private pdfViewModeBtn: HTMLButtonElement | null = null;
    private pdfViewModeMenu: HTMLElement | null = null;
    private pdfCoverToggle: HTMLInputElement | null = null;
    private pdfBindingBtn: HTMLButtonElement | null = null;
    private pdfBindingMenu: HTMLElement | null = null;

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
        this.previewBody = document.querySelector('.preview-pane-body') as HTMLElement;
        this.previewDropTarget = document.getElementById('previewDropTarget');
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
        this.pdfCanvasContainer = document.getElementById('pdfCanvasContainer');
        this.pdfCanvasLeft = document.getElementById('pdfCanvasLeft') as HTMLCanvasElement;
        this.pdfCanvasRight = document.getElementById('pdfCanvasRight') as HTMLCanvasElement;
        this.pdfScrollContainer = document.getElementById('pdfScrollContainer');
        this.pdfEmpty = document.getElementById('pdfEmpty');
        this.pdfPageInfo = document.getElementById('pdfPageInfo');
        this.pdfPrevBtn = document.getElementById('pdfPrevBtn') as HTMLButtonElement;
        this.pdfNextBtn = document.getElementById('pdfNextBtn') as HTMLButtonElement;
        this.pdfViewModeBtn = document.getElementById('pdfViewModeBtn') as HTMLButtonElement;
        this.pdfViewModeMenu = document.getElementById('pdfViewModeMenu');
        this.pdfCoverToggle = document.getElementById('pdfCoverToggle') as HTMLInputElement;
        this.pdfBindingBtn = document.getElementById('pdfBindingBtn') as HTMLButtonElement;
        this.pdfBindingMenu = document.getElementById('pdfBindingMenu');

        // イベントリスナーの設定
        this.setupEventListeners();
        this.setupPreviewDropHandlers();

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
                            const payload = JSON.parse(jsonData) as { type?: string; path?: string; name?: string };
                            if (payload.type === 'csv' && payload.path) {
                                this.insertCsvAtCursor(payload.path);
                                return;
                            }
                            if (payload.path) {
                                const name = payload.name || this.getNameFromPath(payload.path);
                                this.insertImageAtCursor(payload.path, name);
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
                            return;
                        }
                        const imageItem = document.querySelector(`.image-thumbnail[data-image-path="${path}"]`);
                        if (imageItem) {
                            const name = imageItem.getAttribute('data-image-name') || this.getNameFromPath(path);
                            this.insertImageAtCursor(path, name);
                        }
                    }
                })
            );
        }

        if (this.pdfCanvasContainer) {
            this.unsubscribe.push(
                this.addEventListener(this.pdfCanvasContainer, 'dragover', (e) => {
                    e.preventDefault();
                    useOverlayStore.getState().showDropOverlay();
                })
            );
            this.unsubscribe.push(
                this.addEventListener(this.pdfCanvasContainer, 'dragleave', () => {
                    useOverlayStore.getState().hideDropOverlay();
                })
            );
            this.unsubscribe.push(
                this.addEventListener(this.pdfCanvasContainer, 'drop', (e) => {
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

        if (this.pdfPrevBtn) {
            this.unsubscribe.push(
                this.addEventListener(this.pdfPrevBtn, 'click', () => {
                    this.handlePdfArrow('left');
                })
            );
        }

        if (this.pdfNextBtn) {
            this.unsubscribe.push(
                this.addEventListener(this.pdfNextBtn, 'click', () => {
                    this.handlePdfArrow('right');
                })
            );
        }

        if (this.pdfViewModeBtn && this.pdfViewModeMenu) {
            this.unsubscribe.push(
                this.addEventListener(this.pdfViewModeBtn, 'click', (e) => {
                    e.stopPropagation();
                    this.togglePdfSelect(this.pdfViewModeBtn);
                })
            );

            this.unsubscribe.push(
                this.addEventListener(this.pdfViewModeMenu, 'click', (e) => {
                    const target = e.target as HTMLElement;
                    if (target instanceof HTMLButtonElement) {
                        const value = target.dataset.value;
                        if (value === 'spread') {
                            this.pdfViewMode = 'spread';
                        } else if (value === 'scroll') {
                            this.pdfViewMode = 'scroll';
                        } else {
                            this.pdfViewMode = 'single';
                        }
                        this.syncPdfPagingFromModeChange();
                        this.renderPdfPages();
                        this.updatePdfControlsState();
                        this.updatePdfSelectLabels();
                        this.closePdfSelects();
                    }
                })
            );
        }

        if (this.pdfCoverToggle) {
            this.unsubscribe.push(
                this.addEventListener(this.pdfCoverToggle, 'change', (e) => {
                    const target = e.target as HTMLInputElement;
                    this.pdfCoverEnabled = target.checked;
                    this.syncPdfPagingFromModeChange();
                    this.renderPdfPages();
                    this.updatePdfControlsState();
                })
            );
        }

        if (this.pdfBindingBtn && this.pdfBindingMenu) {
            this.unsubscribe.push(
                this.addEventListener(this.pdfBindingBtn, 'click', (e) => {
                    e.stopPropagation();
                    this.togglePdfSelect(this.pdfBindingBtn);
                })
            );
            this.unsubscribe.push(
                this.addEventListener(this.pdfBindingMenu, 'click', (e) => {
                    const target = e.target as HTMLElement;
                    if (target instanceof HTMLButtonElement) {
                        const value = target.dataset.value;
                        this.pdfBinding = value === 'ltr' ? 'ltr' : 'rtl';
                        this.renderPdfPages();
                        this.updatePdfSelectLabels();
                        this.closePdfSelects();
                    }
                })
            );
        }

        this.unsubscribe.push(
            this.addEventListener(window, 'resize', () => {
                if (this.pdfPane && this.pdfPane.style.display !== 'none') {
                    this.renderPdfPages();
                }
            })
        );

        this.unsubscribe.push(
            this.addEventListener(document, 'click', () => {
                this.closePdfSelects();
            })
        );

        this.unsubscribe.push(
            this.addEventListener(window, 'karte-timestamp-click', (event) => {
                const detail = (event as CustomEvent<{ timestamp: number }>).detail;
                const timestamp = detail?.timestamp;
                if (!this.audioPlayer || typeof timestamp !== 'number' || Number.isNaN(timestamp)) {
                    return;
                }
                this.audioPlayer.currentTime = timestamp;
                if (this.audioPlayer.paused) {
                    this.audioPlayer.play().catch((error) => {
                        console.error('Failed to play audio:', error);
                    });
                }
            })
        );

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
                return;
            }
            if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
                const active = document.activeElement;
                if (
                    active instanceof HTMLInputElement ||
                    active instanceof HTMLTextAreaElement ||
                    (active instanceof HTMLElement && active.isContentEditable)
                ) {
                    return;
                }
                if (!this.pdfDoc || !this.pdfPane || this.pdfPane.style.display === 'none') {
                    return;
                }
                e.preventDefault();
                this.handlePdfArrow(e.key === 'ArrowLeft' ? 'left' : 'right');
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
                    this.updateAudioPlayer(state.markdownContent);
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
        if (this.pdfCoverToggle) {
            this.pdfCoverToggle.checked = this.pdfCoverEnabled;
        }
        this.updatePdfSelectLabels();
        this.updatePdfControlsState();
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
            const prepared = await prepareMarkdownForPreview(content, this.api);
            const html = await this.api.PreviewMarkdown(prepared);
            const finalHtml = this.buildPreviewHtml(prepared, html);
            useDocStore.getState().setPreviewHtml(finalHtml);
            this.updatePreviewFrame(finalHtml);
            this.updateAudioPlayer(content);
        } catch (error) {
            console.error('Failed to update preview:', error);
        }
    }

    private updatePreviewFrame(html: string): void {
        if (!this.preview) {
            return;
        }
        writePreviewFrame(this.preview, html);
        this.setupPreviewDropHandlers();
    }

    private setupPreviewDropHandlers(): void {
        if (!this.preview) {
            return;
        }

        if (!this.previewDropHandlersBound) {
            this.previewDropHandlersBound = true;
            this.preview.addEventListener('load', () => this.setupPreviewDropHandlersInternal());
            this.preview.addEventListener('dragover', (e) => this.handlePreviewDragOver(e));
            this.preview.addEventListener('drop', (e) => this.handlePreviewDrop(e, 'iframe-element'));
            if (this.previewBody) {
                this.previewBody.addEventListener('dragover', (e) => this.handlePreviewDragOver(e));
                this.previewBody.addEventListener('drop', (e) => this.handlePreviewDrop(e, 'iframe-element'));
            }
            this.bindPreviewDragState();
        }

        if (this.preview.contentDocument || this.preview.contentWindow?.document) {
            this.setupPreviewDropHandlersInternal();
        }
    }

    private setupPreviewDropHandlersInternal(): void {
        const iframeDoc = this.preview?.contentDocument || this.preview?.contentWindow?.document;
        if (!iframeDoc) {
            return;
        }
        const flaggedDoc = iframeDoc as Document & { __kartePreviewDropSetup?: boolean };
        if (flaggedDoc.__kartePreviewDropSetup) {
            return;
        }
        flaggedDoc.__kartePreviewDropSetup = true;

        iframeDoc.addEventListener('dragover', (e) => this.handlePreviewDragOver(e));
        iframeDoc.addEventListener('drop', (e) => this.handlePreviewDrop(e, 'iframe-doc'));
    }

    private buildPreviewHtml(content: string, html: string): string {
        const customCss = useCustomCssStore.getState().customCss;
        const theme = useUIStore.getState().theme;
        const withCss = applyCustomCssToHtml(content, html, customCss, theme);
        return convertTimestampsToLinks(withCss);
    }

    private updateAudioPlayer(content: string): void {
        updateAudioPlayerFromContent(this.api, content, this.audioPlayerContainer, this.audioPlayer).catch((error) => {
            console.error('Failed to update audio player:', error);
        });
    }

    private async updatePdfPreview(path: string): Promise<void> {
        if (!this.pdfCanvasContainer) {
            return;
        }
        try {
            const pdfUrl = await this.api.GetPdfFileURL(path);
            const resolvedUrl = this.resolvePdfUrl(pdfUrl);
            if (resolvedUrl !== this.pdfSourceUrl) {
                await this.loadPdfDocument(resolvedUrl);
            } else {
                this.renderPdfPages();
            }
        } catch (error) {
            console.error('Failed to load PDF:', error);
            this.showPdfEmpty('PDFの読み込みに失敗しました');
        }
    }

    private resolvePdfUrl(pdfUrl: string): string {
        try {
            return new URL(pdfUrl, window.location.href).toString();
        } catch {
            return pdfUrl;
        }
    }

    private async loadPdfDocument(url: string): Promise<void> {
        if (!this.pdfCanvasContainer) {
            return;
        }
        this.showPdfEmpty('PDFを読み込み中...');
        const loadingTask = getDocument({
            url,
            cMapUrl: '/pdfjs/cmaps/',
            cMapPacked: true,
            standardFontDataUrl: '/pdfjs/standard_fonts/',
        });
        this.pdfDoc = await loadingTask.promise;
        this.pdfSourceUrl = url;
        this.pdfPageCount = this.pdfDoc.numPages;
        this.pdfPageNumber = 1;
        this.pdfSpreadIndex = 0;
        this.pdfViewMode = this.pdfPageCount <= 1 ? 'scroll' : 'single';
        this.syncPdfPagingFromModeChange();
        this.updatePdfSelectLabels();
        this.updatePdfControlsState();
        this.renderPdfPages();
    }

    private renderPdfPages(): void {
        if (!this.pdfDoc || !this.pdfCanvasContainer || !this.pdfCanvasLeft || !this.pdfCanvasRight) {
            return;
        }

        const requestId = ++this.pdfRenderRequestId;
        const isSingle = this.pdfViewMode === 'single';
        const isScroll = this.pdfViewMode === 'scroll';
        const isCoverOnly = this.pdfViewMode === 'spread' && this.pdfCoverEnabled && this.pdfSpreadIndex === 0;

        this.pdfCanvasContainer.classList.toggle('single', isSingle);
        this.pdfCanvasContainer.classList.toggle('scroll', isScroll);
        this.pdfCanvasContainer.classList.toggle('cover-only', isCoverOnly);
        this.hidePdfEmpty();

        if (isScroll) {
            this.renderScrollPages(requestId);
            return;
        }

        if (isSingle) {
            this.renderSinglePage(requestId);
        } else {
            this.renderSpreadPages(requestId, isCoverOnly);
        }
    }

    private async renderSinglePage(requestId: number): Promise<void> {
        if (!this.pdfDoc || !this.pdfCanvasContainer || !this.pdfCanvasLeft || !this.pdfCanvasRight) {
            return;
        }

        const pageNumber = Math.min(Math.max(1, this.pdfPageNumber), this.pdfPageCount);
        this.pdfPageNumber = pageNumber;
        this.updatePdfPageInfo(`${pageNumber} / ${this.pdfPageCount}`);

        this.pdfCanvasRight.style.display = 'none';
        const containerWidth = this.pdfCanvasContainer.clientWidth;
        const containerHeight = this.pdfCanvasContainer.clientHeight;
        await this.renderPdfPageToCanvas(pageNumber, this.pdfCanvasLeft, containerWidth, containerHeight, requestId);
    }

    private async renderScrollPages(requestId: number): Promise<void> {
        if (!this.pdfDoc || !this.pdfCanvasContainer || !this.pdfScrollContainer) {
            return;
        }

        this.pdfScrollContainer.innerHTML = '';
        const containerWidth = this.pdfCanvasContainer.clientWidth;

        for (let pageNumber = 1; pageNumber <= this.pdfPageCount; pageNumber += 1) {
            if (requestId !== this.pdfRenderRequestId) {
                return;
            }
            const canvas = document.createElement('canvas');
            canvas.className = 'pdf-scroll-canvas';
            this.pdfScrollContainer.appendChild(canvas);
            await this.renderPdfPageToCanvas(pageNumber, canvas, containerWidth, Number.MAX_SAFE_INTEGER, requestId);
        }

        this.updatePdfPageInfo(`全${this.pdfPageCount}ページ`);
    }

    private async renderSpreadPages(requestId: number, coverOnly: boolean): Promise<void> {
        if (!this.pdfDoc || !this.pdfCanvasContainer || !this.pdfCanvasLeft || !this.pdfCanvasRight) {
            return;
        }

        if (coverOnly) {
            const placement = this.pdfBinding === 'rtl' ? 'left' : 'right';
            const containerWidth = this.pdfCanvasContainer.clientWidth;
            const containerHeight = this.pdfCanvasContainer.clientHeight;

            if (placement === 'right') {
                this.pdfCanvasLeft.style.display = 'none';
                this.pdfCanvasRight.style.display = '';
                this.pdfCanvasRight.style.gridColumn = '2';
                await this.renderPdfPageToCanvas(1, this.pdfCanvasRight, containerWidth / 2, containerHeight, requestId);
            } else {
                this.pdfCanvasLeft.style.display = '';
                this.pdfCanvasRight.style.display = 'none';
                this.pdfCanvasLeft.style.gridColumn = '1';
                await this.renderPdfPageToCanvas(1, this.pdfCanvasLeft, containerWidth / 2, containerHeight, requestId);
            }

            this.updatePdfPageInfo(`1 / ${this.pdfPageCount}`);
            return;
        }

        this.pdfCanvasLeft.style.gridColumn = 'auto';
        this.pdfCanvasRight.style.gridColumn = 'auto';

        const startPage = this.pdfCoverEnabled
            ? 2 + (this.pdfSpreadIndex - 1) * 2
            : 1 + this.pdfSpreadIndex * 2;

        const { leftPage, rightPage } = this.getSpreadPages(startPage);
        const containerWidth = this.pdfCanvasContainer.clientWidth;
        const containerHeight = this.pdfCanvasContainer.clientHeight;
        const pageWidth = containerWidth / 2;

        if (leftPage) {
            this.pdfCanvasLeft.style.display = '';
            await this.renderPdfPageToCanvas(leftPage, this.pdfCanvasLeft, pageWidth, containerHeight, requestId);
        } else {
            this.pdfCanvasLeft.style.display = 'none';
        }

        if (rightPage) {
            this.pdfCanvasRight.style.display = '';
            await this.renderPdfPageToCanvas(rightPage, this.pdfCanvasRight, pageWidth, containerHeight, requestId);
        } else {
            this.pdfCanvasRight.style.display = 'none';
        }

        if (leftPage && rightPage) {
            this.updatePdfPageInfo(`${leftPage}-${rightPage} / ${this.pdfPageCount}`);
        } else if (leftPage) {
            this.updatePdfPageInfo(`${leftPage} / ${this.pdfPageCount}`);
        } else if (rightPage) {
            this.updatePdfPageInfo(`${rightPage} / ${this.pdfPageCount}`);
        }
    }

    private getSpreadPages(startPage: number): { leftPage: number | null; rightPage: number | null } {
        const pageA = startPage;
        const pageB = startPage + 1 <= this.pdfPageCount ? startPage + 1 : null;

        if (!pageB) {
            if (this.pdfBinding === 'rtl') {
                return { leftPage: null, rightPage: pageA };
            }
            return { leftPage: pageA, rightPage: null };
        }

        const oddPage = pageA % 2 === 1 ? pageA : pageB;
        const evenPage = pageA % 2 === 0 ? pageA : pageB;

        if (this.pdfBinding === 'rtl') {
            return { leftPage: evenPage, rightPage: oddPage };
        }
        return { leftPage: oddPage, rightPage: evenPage };
    }

    private async renderPdfPageToCanvas(
        pageNumber: number,
        canvas: HTMLCanvasElement,
        maxWidth: number,
        maxHeight: number,
        requestId: number
    ): Promise<void> {
        if (!this.pdfDoc) {
            return;
        }
        const page = await this.pdfDoc.getPage(pageNumber);
        if (requestId !== this.pdfRenderRequestId) {
            return;
        }
        const viewport = page.getViewport({ scale: 1 });
        const scale = Math.min(maxWidth / viewport.width, maxHeight / viewport.height);
        const scaledViewport = page.getViewport({ scale: Math.max(scale, 0.1) });
        canvas.width = Math.floor(scaledViewport.width);
        canvas.height = Math.floor(scaledViewport.height);
        const context = canvas.getContext('2d');
        if (!context) {
            return;
        }
        context.clearRect(0, 0, canvas.width, canvas.height);
        await page.render({ canvasContext: context, viewport: scaledViewport }).promise;
    }

    private handlePdfArrow(direction: 'left' | 'right'): void {
        if (this.pdfViewMode === 'scroll') {
            return;
        }
        const shouldGoNext =
            this.pdfViewMode === 'single' ? direction === 'right' : this.pdfBinding === 'rtl' ? direction === 'left' : direction === 'right';
        if (shouldGoNext) {
            this.goToNextPdfPage();
        } else {
            this.goToPreviousPdfPage();
        }
    }

    private goToPreviousPdfPage(): void {
        if (!this.pdfDoc) {
            return;
        }
        if (this.pdfViewMode === 'scroll') {
            return;
        }
        if (this.pdfViewMode === 'single') {
            this.pdfPageNumber = Math.max(1, this.pdfPageNumber - 1);
        } else {
            if (this.pdfCoverEnabled && this.pdfSpreadIndex === 0) {
                return;
            }
            this.pdfSpreadIndex = Math.max(0, this.pdfSpreadIndex - 1);
        }
        this.renderPdfPages();
    }

    private goToNextPdfPage(): void {
        if (!this.pdfDoc) {
            return;
        }
        if (this.pdfViewMode === 'scroll') {
            return;
        }
        if (this.pdfViewMode === 'single') {
            this.pdfPageNumber = Math.min(this.pdfPageCount, this.pdfPageNumber + 1);
        } else {
            const maxIndex = this.getMaxSpreadIndex();
            if (this.pdfSpreadIndex < maxIndex) {
                this.pdfSpreadIndex += 1;
            }
        }
        this.renderPdfPages();
    }

    private getMaxSpreadIndex(): number {
        if (!this.pdfDoc) {
            return 0;
        }
        if (this.pdfCoverEnabled) {
            const remaining = Math.max(0, this.pdfPageCount - 1);
            return Math.ceil(remaining / 2);
        }
        return Math.max(0, Math.ceil(this.pdfPageCount / 2) - 1);
    }

    private syncPdfPagingFromModeChange(): void {
        if (this.pdfViewMode === 'scroll') {
            return;
        }
        if (this.pdfViewMode === 'single') {
            const current = this.getCurrentSpreadPrimaryPage();
            this.pdfPageNumber = current || 1;
            return;
        }

        if (this.pdfCoverEnabled && this.pdfPageNumber === 1) {
            this.pdfSpreadIndex = 0;
            return;
        }

        const startPage = Math.max(this.pdfCoverEnabled ? 2 : 1, this.pdfPageNumber);
        this.pdfSpreadIndex = this.pdfCoverEnabled
            ? Math.floor((startPage - 2) / 2) + 1
            : Math.floor((startPage - 1) / 2);

        const maxIndex = this.getMaxSpreadIndex();
        if (this.pdfSpreadIndex > maxIndex) {
            this.pdfSpreadIndex = maxIndex;
        }
    }

    private getCurrentSpreadPrimaryPage(): number | null {
        if (this.pdfViewMode === 'single') {
            return this.pdfPageNumber;
        }
        if (this.pdfCoverEnabled && this.pdfSpreadIndex === 0) {
            return 1;
        }
        const startPage = this.pdfCoverEnabled
            ? 2 + (this.pdfSpreadIndex - 1) * 2
            : 1 + this.pdfSpreadIndex * 2;
        return Math.min(startPage, this.pdfPageCount);
    }

    private togglePdfSelect(trigger: HTMLButtonElement): void {
        const wrapper = trigger.closest('.pdf-select');
        if (!wrapper) {
            return;
        }
        const isOpen = wrapper.classList.contains('open');
        this.closePdfSelects();
        if (!isOpen) {
            wrapper.classList.add('open');
        }
    }

    private closePdfSelects(): void {
        document.querySelectorAll('.pdf-select.open').forEach((element) => {
            element.classList.remove('open');
        });
    }

    private updatePdfSelectLabels(): void {
        if (this.pdfViewModeBtn) {
            const label =
                this.pdfViewMode === 'spread'
                    ? '見開き'
                    : this.pdfViewMode === 'scroll'
                      ? 'スクロール'
                      : '単ページ';
            this.pdfViewModeBtn.textContent = label;
        }
        if (this.pdfBindingBtn) {
            this.pdfBindingBtn.textContent = this.pdfBinding === 'ltr' ? '左開き' : '右開き';
        }
    }

    private updatePdfPageInfo(label: string): void {
        if (this.pdfPageInfo) {
            this.pdfPageInfo.textContent = label;
        }
    }

    private updatePdfControlsState(): void {
        const hasPdf = !!this.pdfDoc;
        const isScroll = this.pdfViewMode === 'scroll';
        const isSpread = this.pdfViewMode === 'spread';

        if (this.pdfPrevBtn) {
            this.pdfPrevBtn.disabled = !hasPdf || isScroll || this.pdfPageCount <= 1;
        }
        if (this.pdfNextBtn) {
            this.pdfNextBtn.disabled = !hasPdf || isScroll || this.pdfPageCount <= 1;
        }
        if (this.pdfCoverToggle) {
            this.pdfCoverToggle.disabled = !hasPdf || !isSpread;
        }
        if (this.pdfViewModeBtn) {
            this.pdfViewModeBtn.disabled = !hasPdf;
        }
        if (this.pdfBindingBtn) {
            this.pdfBindingBtn.disabled = !hasPdf || !isSpread;
        }
    }

    private showPdfEmpty(message: string): void {
        if (this.pdfEmpty) {
            this.pdfEmpty.textContent = message;
            this.pdfEmpty.style.display = 'block';
        }
    }

    private hidePdfEmpty(): void {
        if (this.pdfEmpty) {
            this.pdfEmpty.style.display = 'none';
        }
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

    private insertCsvAfterElement(path: string, element: Element): void {
        if (!this.editor) {
            return;
        }
        const markdownContent = this.editor.value;
        const position = this.findMarkdownPositionFromElement(element, markdownContent);
        const csvMarkdown = `\n\n@import(type="csv", path="${path}")\n`;
        const insertAt = position === -1 ? markdownContent.length : position;
        const nextValue = markdownContent.slice(0, insertAt) + csvMarkdown + markdownContent.slice(insertAt);
        this.editor.value = nextValue;
        const nextCursor = insertAt + csvMarkdown.length;
        this.editor.setSelectionRange(nextCursor, nextCursor);
        this.editor.focus();

        const docStore = useDocStore.getState();
        docStore.setMarkdownContent(nextValue);
        docStore.setHasUnsavedChanges(true);
        this.updatePreview(nextValue);
    }

    private insertImageAtCursor(path: string, name: string): void {
        if (!this.editor) {
            return;
        }
        const cursorPos = this.editor.selectionStart ?? this.editor.value.length;
        const nameWithoutExt = name.replace(/\.[^/.]+$/, '');
        const imageMarkdown = `![${nameWithoutExt}](${path} "${name}")`;
        const currentValue = this.editor.value;
        const nextValue = currentValue.slice(0, cursorPos) + imageMarkdown + currentValue.slice(cursorPos);
        this.editor.value = nextValue;
        const nextCursor = cursorPos + imageMarkdown.length;
        this.editor.setSelectionRange(nextCursor, nextCursor);

        const docStore = useDocStore.getState();
        docStore.setMarkdownContent(nextValue);
        docStore.setHasUnsavedChanges(true);
        this.updatePreview(nextValue);
    }

    private async insertImageAfterElement(path: string, name: string, element: Element): Promise<void> {
        if (!this.editor) {
            return;
        }
        const markdownContent = this.editor.value;
        const position = this.findMarkdownPositionFromElement(element, markdownContent);
        const nameWithoutExt = name.replace(/\.[^/.]+$/, '');
        const imageMarkdown = `\n\n![${nameWithoutExt}](${path} "${name}")\n`;
        const insertAt = position === -1 ? markdownContent.length : position;
        const nextValue = markdownContent.slice(0, insertAt) + imageMarkdown + markdownContent.slice(insertAt);
        this.editor.value = nextValue;

        const nextCursor = insertAt + imageMarkdown.length;
        this.editor.setSelectionRange(nextCursor, nextCursor);
        this.editor.focus();

        const docStore = useDocStore.getState();
        docStore.setMarkdownContent(nextValue);
        docStore.setHasUnsavedChanges(true);
        this.updatePreview(nextValue);
    }

    private findMarkdownPositionFromElement(element: Element, markdownContent: string): number {
        if (!element || !markdownContent) {
            return -1;
        }

        let currentElement: Element | null = element;
        let attempts = 0;
        const maxAttempts = 10;

        while (currentElement && attempts < maxAttempts) {
            if (currentElement.tagName && /^H[1-6]$/.test(currentElement.tagName)) {
                const level = parseInt(currentElement.tagName.charAt(1), 10);
                const headingText = currentElement.textContent?.trim();
                if (headingText) {
                    const escapedText = headingText.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
                    const headingPattern = new RegExp(`^#{${level}}\\s+${escapedText}`, 'm');
                    const match = markdownContent.match(headingPattern);
                    if (match) {
                        const headingEnd = markdownContent.indexOf('\n', match.index + match[0].length);
                        return headingEnd !== -1 ? headingEnd : markdownContent.length;
                    }
                }
            }

            if (currentElement.tagName === 'P' || currentElement.tagName === 'DIV') {
                const elementText = currentElement.textContent?.trim();
                if (elementText) {
                    const searchText = elementText.substring(0, Math.min(100, elementText.length));
                    const escapedSearch = searchText.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
                    const regex = new RegExp(escapedSearch.replace(/\s+/g, '\\s+'), 'm');
                    const match = markdownContent.match(regex);
                    if (match) {
                        const lineEnd = markdownContent.indexOf('\n', match.index);
                        return lineEnd !== -1 ? lineEnd : markdownContent.length;
                    }
                }
            }

            if (currentElement.tagName === 'LI') {
                const itemText = currentElement.textContent?.trim();
                if (itemText) {
                    const escapedText = itemText.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
                    const listPattern = new RegExp(`^[\\s\\-*+\\d\\.]+\\s+${escapedText.substring(0, 50)}`, 'm');
                    const match = markdownContent.match(listPattern);
                    if (match) {
                        const lineEnd = markdownContent.indexOf('\n', match.index);
                        return lineEnd !== -1 ? lineEnd : markdownContent.length;
                    }
                }
            }

            currentElement = currentElement.parentElement;
            attempts += 1;
        }

        return markdownContent.length;
    }

    private getNameFromPath(path: string): string {
        const name = path.split('/').pop();
        return name || 'image';
    }

    private handlePreviewDragOver(event: DragEvent): void {
        const types = Array.from(event.dataTransfer?.types || []);
        if (types.includes('Files') || types.includes('application/json') || types.includes('text/plain')) {
            event.preventDefault();
            if (event.dataTransfer) {
                event.dataTransfer.dropEffect = 'copy';
            }
        }
    }

    private async handlePreviewDrop(
        event: DragEvent,
        source: 'iframe-doc' | 'iframe-element'
    ): Promise<void> {
        const now = Date.now();
        if (now - this.lastPreviewDropTs < 150) {
            return;
        }
        this.lastPreviewDropTs = now;
        event.preventDefault();
        event.stopPropagation();

        const files = Array.from(event.dataTransfer?.files || []);
        if (files.length > 0) {
            window.dispatchEvent(new CustomEvent('karte-file-drop', { detail: { files } }));
            return;
        }

        const dragItem =
            this.getDragItemFromDataTransfer(event.dataTransfer) ??
            this.getDragItemFromGlobals() ??
            this.getDragItemFromDom();
        if (!dragItem) {
            console.error('Failed to get item data from drag');
            return;
        }
        this.clearGlobalDragData();

        const iframeDoc = this.preview?.contentDocument || this.preview?.contentWindow?.document;
        if (!iframeDoc || !this.preview) {
            if (dragItem.type === 'csv') {
                this.insertCsvAtCursor(dragItem.path);
            } else {
                this.insertImageAtCursor(dragItem.path, dragItem.name);
            }
            return;
        }

        try {
            let element: Element | null = null;
            if (source === 'iframe-doc') {
                element = iframeDoc.elementFromPoint(event.clientX, event.clientY);
            } else {
                const rect = this.preview.getBoundingClientRect();
                element = iframeDoc.elementFromPoint(event.clientX - rect.left, event.clientY - rect.top);
            }

            if (dragItem.type === 'csv') {
                if (element) {
                    this.insertCsvAfterElement(dragItem.path, element);
                } else {
                    this.insertCsvAtCursor(dragItem.path);
                }
                return;
            }

            if (element) {
                await this.insertImageAfterElement(dragItem.path, dragItem.name, element);
            } else {
                this.insertImageAtCursor(dragItem.path, dragItem.name);
            }
        } catch (error) {
            console.error('Failed to handle drop in preview:', error);
        }
    }

    private getDragItemFromDataTransfer(
        dataTransfer: DataTransfer | null
    ): { type: 'csv' | 'image'; path: string; name: string } | null {
        if (!dataTransfer) {
            return null;
        }
        const json = dataTransfer.getData('application/json');
        if (json) {
            try {
                const payload = JSON.parse(json) as { type?: string; path?: string; name?: string };
                if (payload.type === 'csv' && payload.path) {
                    return { type: 'csv', path: payload.path, name: payload.name || this.getNameFromPath(payload.path) };
                }
                if (payload.path) {
                    return { type: 'image', path: payload.path, name: payload.name || this.getNameFromPath(payload.path) };
                }
            } catch (error) {
                console.error('Failed to parse drag data:', error);
            }
        }
        const text = dataTransfer.getData('text/plain');
        if (text) {
            const isCsv = text.toLowerCase().endsWith('.csv');
            if (isCsv) {
                return { type: 'csv', path: text, name: this.getNameFromPath(text) };
            }
            return { type: 'image', path: text, name: this.getNameFromPath(text) };
        }
        return null;
    }

    private bindPreviewDragState(): void {
        if (!this.previewBody) {
            return;
        }

        const isPotentialDrag = (event: DragEvent): boolean => {
            const types = Array.from(event.dataTransfer?.types || []);
            return types.length === 0 || types.includes('Files') || types.includes('application/json') || types.includes('text/plain');
        };

        const show = () => {
            if (!this.previewBody) {
                return;
            }
            this.previewBody.classList.add('preview-drop-active');
        };

        const hide = () => {
            if (!this.previewBody) {
                return;
            }
            this.previewBody.classList.remove('preview-drop-active');
        };

        const onDragEnter = (event: DragEvent) => {
            if (!isPotentialDrag(event)) {
                return;
            }
            this.previewDragDepth += 1;
            show();
        };

        const onDragLeave = (event: DragEvent) => {
            if (!isPotentialDrag(event)) {
                return;
            }
            this.previewDragDepth = Math.max(0, this.previewDragDepth - 1);
            if (this.previewDragDepth === 0) {
                hide();
            }
        };

        const onDrop = (event: DragEvent) => {
            if (!isPotentialDrag(event)) {
                return;
            }
            this.previewDragDepth = 0;
            hide();
        };

        window.addEventListener('dragenter', onDragEnter);
        window.addEventListener('dragleave', onDragLeave);
        window.addEventListener('drop', onDrop);
        window.addEventListener('dragend', onDrop);

        this.unsubscribe.push(() => {
            window.removeEventListener('dragenter', onDragEnter);
            window.removeEventListener('dragleave', onDragLeave);
            window.removeEventListener('drop', onDrop);
            window.removeEventListener('dragend', onDrop);
        });
    }

    private getDragItemFromGlobals(): { type: 'csv' | 'image'; path: string; name: string } | null {
        const win = window as any;
        if (win.currentDragCsvData?.path && win.currentDragCsvData?.name) {
            return { type: 'csv', path: win.currentDragCsvData.path, name: win.currentDragCsvData.name };
        }
        if (win.currentDragImageData?.path && win.currentDragImageData?.name) {
            return { type: 'image', path: win.currentDragImageData.path, name: win.currentDragImageData.name };
        }
        return null;
    }

    private getDragItemFromDom(): { type: 'csv' | 'image'; path: string; name: string } | null {
        const csvSource = document.querySelector('.csv-item[style*="opacity: 0.5"]');
        if (csvSource) {
            const path = csvSource.getAttribute('data-csv-path') || '';
            const name = csvSource.getAttribute('data-csv-name') || this.getNameFromPath(path);
            if (path) {
                return { type: 'csv', path, name };
            }
        }
        const imageSource = document.querySelector('.image-thumbnail[style*="opacity: 0.5"]');
        if (imageSource) {
            const path = imageSource.getAttribute('data-image-path') || '';
            const name = imageSource.getAttribute('data-image-name') || this.getNameFromPath(path);
            if (path) {
                return { type: 'image', path, name };
            }
        }
        return null;
    }

    private clearGlobalDragData(): void {
        const win = window as any;
        win.currentDragImageData = null;
        win.currentDragCsvData = null;
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
