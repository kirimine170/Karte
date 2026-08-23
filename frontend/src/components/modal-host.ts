import { BaseComponent } from './component-base';
import { useModalStore, useDocStore, useUIStore, useCustomCssStore } from '../stores/index';
import type { WailsAppAPI } from '../types/wails-api';
import type { ConflictResolutionStrategy } from '../types/wails-api';
import { eventLogger } from '../utils/event-logger';
import { applyCustomCssToHtml } from '../utils/custom-css';
import { renderMarkdownPreview } from '../utils/preview-renderer';
import { convertTimestampsToLinks } from '../utils/preview-audio';
import { CsvPageClient, CSV_PAGE_LIMIT, saveCsvPage } from '../utils/csv-page';

export class ModalHost extends BaseComponent {
    private unsubscribe: (() => void)[] = [];
    private api: WailsAppAPI;
    private readonly csvPageClient: CsvPageClient;
    private destroyed = false;
    private csvSaveGeneration = 0;
    private csvSaveInFlight = false;
    private csvPageIdentity = '';
    private csvPageBaseline = '';

    // モーダル要素
    private filenameModal: HTMLElement | null = null;
    private filenameInput: HTMLInputElement | null = null;
    private createFileBtn: HTMLButtonElement | null = null;
    private cancelFileBtn: HTMLButtonElement | null = null;

    private renameFileModal: HTMLElement | null = null;
    private renameFileInput: HTMLInputElement | null = null;
    private confirmRenameBtn: HTMLButtonElement | null = null;
    private cancelRenameBtn: HTMLButtonElement | null = null;

    private unsavedConfirmModal: HTMLElement | null = null;
    private unsavedSaveBtn: HTMLButtonElement | null = null;
    private unsavedDiscardBtn: HTMLButtonElement | null = null;
    private unsavedCancelBtn: HTMLButtonElement | null = null;

    private customCssModal: HTMLElement | null = null;
    private customCssTextarea: HTMLTextAreaElement | null = null;
    private saveCustomCssBtn: HTMLButtonElement | null = null;
    private clearCustomCssBtn: HTMLButtonElement | null = null;
    private cancelCustomCssBtn: HTMLButtonElement | null = null;

    private webClipModal: HTMLElement | null = null;
    private webClipUrlInput: HTMLInputElement | null = null;
    private importWebClipBtn: HTMLButtonElement | null = null;
    private cancelWebClipBtn: HTMLButtonElement | null = null;
    private webClipWarnings: HTMLElement | null = null;

    private csvEditModal: HTMLElement | null = null;
    private csvEditFileName: HTMLElement | null = null;
    private csvEditTableHead: HTMLElement | null = null;
    private csvEditTableBody: HTMLElement | null = null;
    private csvAddRowBtn: HTMLButtonElement | null = null;
    private csvAddColBtn: HTMLButtonElement | null = null;
    private csvDeleteRowBtn: HTMLButtonElement | null = null;
    private csvDeleteColBtn: HTMLButtonElement | null = null;
    private csvPrevPageBtn: HTMLButtonElement | null = null;
    private csvNextPageBtn: HTMLButtonElement | null = null;
    private csvPageLabel: HTMLElement | null = null;
    private csvSaveBtn: HTMLButtonElement | null = null;
    private csvCancelBtn: HTMLButtonElement | null = null;
    private csvSelectedRow: number | null = null;
    private csvSelectedCol: number | null = null;

    private conflictModal: HTMLElement | null = null;
    private conflictFilePath: HTMLElement | null = null;
    private diffLocal: HTMLElement | null = null;
    private diffRemote: HTMLElement | null = null;
    private resolveConflictBtn: HTMLButtonElement | null = null;
    private cancelConflictBtn: HTMLButtonElement | null = null;

    private imagePreviewModal: HTMLElement | null = null;
    private imagePreviewOverlay: HTMLElement | null = null;
    private imagePreviewImg: HTMLImageElement | null = null;
    private imagePreviewName: HTMLElement | null = null;
    private imagePreviewPath: HTMLElement | null = null;
    private imagePreviewClose: HTMLButtonElement | null = null;
    private imageMetadataEditor: HTMLTextAreaElement | null = null;
    private imageMetadataSaveBtn: HTMLButtonElement | null = null;
    private imageMetadataStatus: HTMLElement | null = null;
    private imageSystemMetadataEditor: HTMLTextAreaElement | null = null;
    private imageSystemMetadataSaveBtn: HTMLButtonElement | null = null;
    private imageSystemMetadataStatus: HTMLElement | null = null;

    constructor(api: WailsAppAPI, parent?: HTMLElement) {
        super(parent);
        this.api = api;
        this.csvPageClient = new CsvPageClient(api);
    }

    init(): void {
        this.destroyed = false;
        eventLogger.log('ModalHost', 'init');
        
        // モーダル要素の取得
        this.filenameModal = document.getElementById('filenameModal');
        this.filenameInput = document.getElementById('filenameInput') as HTMLInputElement;
        this.createFileBtn = document.getElementById('createFileBtn') as HTMLButtonElement;
        this.cancelFileBtn = document.getElementById('cancelFileBtn') as HTMLButtonElement;

        this.renameFileModal = document.getElementById('renameFileModal');
        this.renameFileInput = document.getElementById('renameFileInput') as HTMLInputElement;
        this.confirmRenameBtn = document.getElementById('confirmRenameBtn') as HTMLButtonElement;
        this.cancelRenameBtn = document.getElementById('cancelRenameBtn') as HTMLButtonElement;

        this.unsavedConfirmModal = document.getElementById('unsavedConfirmModal');
        this.unsavedSaveBtn = document.getElementById('unsavedSaveBtn') as HTMLButtonElement;
        this.unsavedDiscardBtn = document.getElementById('unsavedDiscardBtn') as HTMLButtonElement;
        this.unsavedCancelBtn = document.getElementById('unsavedCancelBtn') as HTMLButtonElement;

        this.customCssModal = document.getElementById('customCssModal');
        this.customCssTextarea = document.getElementById('customCssTextarea') as HTMLTextAreaElement;
        this.saveCustomCssBtn = document.getElementById('saveCustomCssBtn') as HTMLButtonElement;
        this.clearCustomCssBtn = document.getElementById('clearCustomCssBtn') as HTMLButtonElement;
        this.cancelCustomCssBtn = document.getElementById('cancelCustomCssBtn') as HTMLButtonElement;

        this.webClipModal = document.getElementById('webClipModal');
        this.webClipUrlInput = document.getElementById('webClipUrlInput') as HTMLInputElement;
        this.importWebClipBtn = document.getElementById('importWebClipBtn') as HTMLButtonElement;
        this.cancelWebClipBtn = document.getElementById('cancelWebClipBtn') as HTMLButtonElement;
        this.webClipWarnings = document.getElementById('webClipWarnings');

        this.csvEditModal = document.getElementById('csvEditModal');
        this.csvEditFileName = document.getElementById('csvEditFileName');
        this.csvEditTableHead = document.getElementById('csvEditTableHead');
        this.csvEditTableBody = document.getElementById('csvEditTableBody');
        this.csvAddRowBtn = document.getElementById('csvAddRowBtn') as HTMLButtonElement;
        this.csvAddColBtn = document.getElementById('csvAddColBtn') as HTMLButtonElement;
        this.csvDeleteRowBtn = document.getElementById('csvDeleteRowBtn') as HTMLButtonElement;
        this.csvDeleteColBtn = document.getElementById('csvDeleteColBtn') as HTMLButtonElement;
        this.csvPrevPageBtn = document.getElementById('csvPrevPageBtn') as HTMLButtonElement;
        this.csvNextPageBtn = document.getElementById('csvNextPageBtn') as HTMLButtonElement;
        this.csvPageLabel = document.getElementById('csvPageLabel');
        this.csvSaveBtn = document.getElementById('csvSaveBtn') as HTMLButtonElement;
        this.csvCancelBtn = document.getElementById('csvCancelBtn') as HTMLButtonElement;

        this.conflictModal = document.getElementById('conflictModal');
        this.conflictFilePath = document.getElementById('conflictFilePath');
        this.diffLocal = document.getElementById('diffLocal');
        this.diffRemote = document.getElementById('diffRemote');
        this.resolveConflictBtn = document.getElementById('resolveConflictBtn') as HTMLButtonElement;
        this.cancelConflictBtn = document.getElementById('cancelConflictBtn') as HTMLButtonElement;

        this.imagePreviewModal = document.getElementById('imagePreviewModal');
        this.imagePreviewOverlay = this.imagePreviewModal?.querySelector('.image-preview-overlay') as HTMLElement | null;
        this.imagePreviewImg = document.getElementById('imagePreviewImg') as HTMLImageElement;
        this.imagePreviewName = document.getElementById('imagePreviewName');
        this.imagePreviewPath = document.getElementById('imagePreviewPath');
        this.imagePreviewClose = document.getElementById('imagePreviewClose') as HTMLButtonElement;
        this.imageMetadataEditor = document.getElementById('imageMetadataEditor') as HTMLTextAreaElement;
        this.imageMetadataSaveBtn = document.getElementById('imageMetadataSaveBtn') as HTMLButtonElement;
        this.imageMetadataStatus = document.getElementById('imageMetadataStatus');
        this.imageSystemMetadataEditor = document.getElementById('imageSystemMetadataEditor') as HTMLTextAreaElement;
        this.imageSystemMetadataSaveBtn = document.getElementById('imageSystemMetadataSaveBtn') as HTMLButtonElement;
        this.imageSystemMetadataStatus = document.getElementById('imageSystemMetadataStatus');

        // イベントリスナーの設定
        this.setupEventListeners();

        // 状態の購読
        this.subscribeToStores();
    }

    private setupEventListeners(): void {
        // ファイル名モーダル
        if (this.createFileBtn) {
            this.unsubscribe.push(
                this.addEventListener(this.createFileBtn, 'click', async () => {
                    eventLogger.log('ModalHost', 'filename-modal-create-click');
                    await this.handleFileCreation();
                })
            );
        }
        if (this.filenameInput) {
            this.unsubscribe.push(
                this.addEventListener(this.filenameInput, 'input', (e) => {
                    const target = e.target as HTMLInputElement;
                    useModalStore.getState().setFilenameModalValue(target.value);
                })
            );
        }
        if (this.cancelFileBtn) {
            this.unsubscribe.push(
                this.addEventListener(this.cancelFileBtn, 'click', () => {
                    eventLogger.log('ModalHost', 'filename-modal-cancel-click');
                    useModalStore.getState().hideFilenameModal();
                })
            );
        }

        // リネームモーダル
        if (this.confirmRenameBtn) {
            this.unsubscribe.push(
                this.addEventListener(this.confirmRenameBtn, 'click', async () => {
                    eventLogger.log('ModalHost', 'rename-modal-confirm-click');
                    await this.handleRenameFile();
                })
            );
        }
        if (this.renameFileInput) {
            this.unsubscribe.push(
                this.addEventListener(this.renameFileInput, 'input', (e) => {
                    const target = e.target as HTMLInputElement;
                    useModalStore.getState().setRenameFileModalValue(target.value);
                })
            );
        }
        if (this.cancelRenameBtn) {
            this.unsubscribe.push(
                this.addEventListener(this.cancelRenameBtn, 'click', () => {
                    eventLogger.log('ModalHost', 'rename-modal-cancel-click');
                    useModalStore.getState().hideRenameFileModal();
                })
            );
        }

        // 未保存確認モーダル
        if (this.unsavedSaveBtn) {
            this.unsubscribe.push(
                this.addEventListener(this.unsavedSaveBtn, 'click', async () => {
                    eventLogger.log('ModalHost', 'unsaved-confirm-save-click');
                    const modalStore = useModalStore.getState();
                    await modalStore.unsavedConfirmModal.onSave();
                    modalStore.hideUnsavedConfirmModal();
                })
            );
        }
        if (this.unsavedDiscardBtn) {
            this.unsubscribe.push(
                this.addEventListener(this.unsavedDiscardBtn, 'click', () => {
                    eventLogger.log('ModalHost', 'unsaved-confirm-discard-click');
                    const modalStore = useModalStore.getState();
                    modalStore.unsavedConfirmModal.onDiscard();
                    modalStore.hideUnsavedConfirmModal();
                })
            );
        }
        if (this.unsavedCancelBtn) {
            this.unsubscribe.push(
                this.addEventListener(this.unsavedCancelBtn, 'click', () => {
                    eventLogger.log('ModalHost', 'unsaved-confirm-cancel-click');
                    useModalStore.getState().hideUnsavedConfirmModal();
                })
            );
        }

        // カスタムCSSモーダル
        if (this.saveCustomCssBtn) {
            this.unsubscribe.push(
                this.addEventListener(this.saveCustomCssBtn, 'click', async () => {
                    eventLogger.log('ModalHost', 'custom-css-save-click');
                    await this.handleSaveCustomCSS();
                })
            );
        }
        if (this.customCssTextarea) {
            this.unsubscribe.push(
                this.addEventListener(this.customCssTextarea, 'input', (e) => {
                    const target = e.target as HTMLTextAreaElement;
                    useModalStore.getState().setCustomCssModalValue(target.value);
                })
            );
        }
        if (this.clearCustomCssBtn) {
            this.unsubscribe.push(
                this.addEventListener(this.clearCustomCssBtn, 'click', async () => {
                    eventLogger.log('ModalHost', 'custom-css-clear-click');
                    await this.handleClearCustomCSS();
                })
            );
        }
        if (this.cancelCustomCssBtn) {
            this.unsubscribe.push(
                this.addEventListener(this.cancelCustomCssBtn, 'click', () => {
                    eventLogger.log('ModalHost', 'custom-css-cancel-click');
                    useModalStore.getState().hideCustomCssModal();
                })
            );
        }

        if (this.webClipUrlInput) {
            this.unsubscribe.push(
                this.addEventListener(this.webClipUrlInput, 'input', (e) => {
                    const target = e.target as HTMLInputElement;
                    useModalStore.getState().setWebClipModalUrl(target.value);
                })
            );
            this.unsubscribe.push(
                this.addEventListener(this.webClipUrlInput, 'keydown', async (e) => {
                    const keyboardEvent = e as KeyboardEvent;
                    if (keyboardEvent.key === 'Enter') {
                        keyboardEvent.preventDefault();
                        await this.handleWebClipImport();
                    }
                })
            );
        }
        if (this.importWebClipBtn) {
            this.unsubscribe.push(
                this.addEventListener(this.importWebClipBtn, 'click', async () => {
                    eventLogger.log('ModalHost', 'web-clip-import-click');
                    await this.handleWebClipImport();
                })
            );
        }
        if (this.cancelWebClipBtn) {
            this.unsubscribe.push(
                this.addEventListener(this.cancelWebClipBtn, 'click', () => {
                    eventLogger.log('ModalHost', 'web-clip-cancel-click');
                    useModalStore.getState().hideWebClipModal();
                })
            );
        }

        // CSV編集モーダル
        if (this.csvSaveBtn) {
            this.unsubscribe.push(
                this.addEventListener(this.csvSaveBtn, 'click', async () => {
                    eventLogger.log('ModalHost', 'csv-edit-save-click');
                    await this.handleSaveCsv();
                })
            );
        }
        if (this.csvAddRowBtn) {
            this.unsubscribe.push(
                this.addEventListener(this.csvAddRowBtn, 'click', () => {
                    eventLogger.log('ModalHost', 'csv-edit-add-row');
                    this.handleCsvAddRow();
                })
            );
        }
        if (this.csvAddColBtn) {
            this.unsubscribe.push(
                this.addEventListener(this.csvAddColBtn, 'click', () => {
                    eventLogger.log('ModalHost', 'csv-edit-add-col');
                    this.handleCsvAddCol();
                })
            );
        }
        if (this.csvDeleteRowBtn) {
            this.unsubscribe.push(
                this.addEventListener(this.csvDeleteRowBtn, 'click', () => {
                    eventLogger.log('ModalHost', 'csv-edit-delete-row');
                    this.handleCsvDeleteRow();
                })
            );
        }
        if (this.csvDeleteColBtn) {
            this.unsubscribe.push(
                this.addEventListener(this.csvDeleteColBtn, 'click', () => {
                    eventLogger.log('ModalHost', 'csv-edit-delete-col');
                    this.handleCsvDeleteCol();
                })
            );
        }
        if (this.csvPrevPageBtn) {
            this.unsubscribe.push(
                this.addEventListener(this.csvPrevPageBtn, 'click', () => {
                    void this.loadCsvEditPage(-1);
                })
            );
        }
        if (this.csvNextPageBtn) {
            this.unsubscribe.push(
                this.addEventListener(this.csvNextPageBtn, 'click', () => {
                    void this.loadCsvEditPage(1);
                })
            );
        }
        if (this.csvCancelBtn) {
            this.unsubscribe.push(
                this.addEventListener(this.csvCancelBtn, 'click', () => {
                    eventLogger.log('ModalHost', 'csv-edit-cancel-click');
                    this.csvPageClient.cancel();
                    useModalStore.getState().hideCsvEditModal();
                })
            );
        }

        if (this.csvEditTableHead) {
            this.unsubscribe.push(
                this.addEventListener(this.csvEditTableHead, 'click', (e) => {
                    const target = e.target as HTMLElement;
                    if (!(target instanceof HTMLTableCellElement)) {
                        return;
                    }
                    const col = target.dataset.col;
                    if (col === undefined) {
                        return;
                    }
                    this.csvSelectedCol = Number(col);
                    this.csvSelectedRow = null;
                    this.applyCsvSelection();
                })
            );
        }
        if (this.csvEditTableBody) {
            this.unsubscribe.push(
                this.addEventListener(this.csvEditTableBody, 'click', (e) => {
                    const target = e.target as HTMLElement;
                    if (!(target instanceof HTMLTableCellElement)) {
                        return;
                    }
                    const row = target.dataset.row;
                    const col = target.dataset.col;
                    if (row === undefined || col === undefined) {
                        return;
                    }
                    this.csvSelectedRow = Number(row);
                    this.csvSelectedCol = Number(col);
                    this.applyCsvSelection();
                })
            );
        }

        // コンフリクト解決モーダル
        if (this.resolveConflictBtn) {
            this.unsubscribe.push(
                this.addEventListener(this.resolveConflictBtn, 'click', async () => {
                    await this.handleResolveConflict();
                })
            );
        }
        if (this.cancelConflictBtn) {
            this.unsubscribe.push(
                this.addEventListener(this.cancelConflictBtn, 'click', () => {
                    useModalStore.getState().hideConflictModal();
                })
            );
        }

        // 画像プレビューモーダル
        if (this.imagePreviewClose) {
            this.unsubscribe.push(
                this.addEventListener(this.imagePreviewClose, 'click', () => {
                    useModalStore.getState().hideImagePreviewModal();
                })
            );
        }
        if (this.imagePreviewOverlay) {
            this.unsubscribe.push(
                this.addEventListener(this.imagePreviewOverlay, 'click', () => {
                    useModalStore.getState().hideImagePreviewModal();
                })
            );
        }
        if (this.imageMetadataSaveBtn) {
            this.unsubscribe.push(
                this.addEventListener(this.imageMetadataSaveBtn, 'click', async () => {
                    await this.handleSaveImageMetadata();
                })
            );
        }
        if (this.imageMetadataEditor) {
            this.unsubscribe.push(
                this.addEventListener(this.imageMetadataEditor, 'input', () => {
                    useModalStore.getState().setImagePreviewModalMetadata(this.imageMetadataEditor?.value || '');
                })
            );
        }
        if (this.imageSystemMetadataSaveBtn) {
            this.unsubscribe.push(
                this.addEventListener(this.imageSystemMetadataSaveBtn, 'click', async () => {
                    await this.handleSaveImageSystemMetadata();
                })
            );
        }
        if (this.imageSystemMetadataEditor) {
            this.unsubscribe.push(
                this.addEventListener(this.imageSystemMetadataEditor, 'input', () => {
                    useModalStore.getState().setImagePreviewModalSystemMetadata(this.imageSystemMetadataEditor?.value || '');
                })
            );
        }
    }

    private subscribeToStores(): void {
        // モーダルの表示/非表示
        this.unsubscribe.push(
            useModalStore.subscribe((state) => {
                // ファイル名モーダル
                if (this.filenameModal) {
                    const wasVisible = this.filenameModal.style.display === 'flex';
                    this.filenameModal.style.display = state.filenameModal.visible ? 'flex' : 'none';
                    if (state.filenameModal.visible && !wasVisible) {
                        eventLogger.log('ModalHost', 'filename-modal-show');
                    } else if (!state.filenameModal.visible && wasVisible) {
                        eventLogger.log('ModalHost', 'filename-modal-hide');
                    }
                    if (state.filenameModal.visible && this.filenameInput) {
                        this.filenameInput.value = state.filenameModal.value;
                        this.filenameInput.focus();
                    }
                }

                // リネームモーダル
                if (this.renameFileModal) {
                    this.renameFileModal.style.display = state.renameFileModal.visible ? 'flex' : 'none';
                    if (state.renameFileModal.visible && this.renameFileInput) {
                        this.renameFileInput.value = state.renameFileModal.value;
                        this.renameFileInput.focus();
                    }
                }

                // 未保存確認モーダル
                if (this.unsavedConfirmModal) {
                    this.unsavedConfirmModal.style.display = state.unsavedConfirmModal.visible ? 'flex' : 'none';
                }

                // カスタムCSSモーダル
                if (this.customCssModal) {
                    this.customCssModal.style.display = state.customCssModal.visible ? 'flex' : 'none';
                    if (state.customCssModal.visible && this.customCssTextarea) {
                        this.customCssTextarea.value = state.customCssModal.value;
                    }
                }

                if (this.webClipModal) {
                    this.webClipModal.style.display = state.webClipModal.visible ? 'flex' : 'none';
                    if (state.webClipModal.visible && this.webClipUrlInput) {
                        this.webClipUrlInput.value = state.webClipModal.url;
                        this.webClipUrlInput.focus();
                    }
                    if (this.importWebClipBtn) {
                        this.importWebClipBtn.disabled = state.webClipModal.importing;
                        this.importWebClipBtn.textContent = state.webClipModal.importing ? '取り込み中...' : '取り込む';
                    }
                    if (this.cancelWebClipBtn) {
                        this.cancelWebClipBtn.disabled = state.webClipModal.importing;
                    }
                    if (this.webClipWarnings) {
                        if (state.webClipModal.warnings.length > 0) {
                            this.webClipWarnings.style.display = 'block';
                            this.webClipWarnings.textContent = state.webClipModal.warnings.join('\n');
                        } else {
                            this.webClipWarnings.style.display = 'none';
                            this.webClipWarnings.textContent = '';
                        }
                    }
                }

                // CSV編集モーダル
                if (this.csvEditModal) {
                    const wasVisible = this.csvEditModal.style.display === 'flex';
                    this.csvEditModal.style.display = state.csvEditModal.visible ? 'flex' : 'none';
                    if (state.csvEditModal.visible) {
                        const identity = this.csvModalIdentity(state.csvEditModal);
                        if (!wasVisible || identity !== this.csvPageIdentity) {
                            this.csvPageIdentity = identity;
                            this.csvPageBaseline = JSON.stringify(state.csvEditModal.data);
                        }
                        this.csvSelectedRow = null;
                        this.csvSelectedCol = null;
                        this.renderCsvEditTable(state.csvEditModal.data);
                        if (this.csvEditFileName) {
                            this.csvEditFileName.textContent = state.csvEditModal.filePath;
                        }
                        this.updateCsvPageControls();
                    } else {
                        if (wasVisible) {
                            this.csvSaveGeneration += 1;
                        }
                        this.csvPageIdentity = '';
                        this.csvPageBaseline = '';
                        this.csvPageClient.cancel();
                    }
                }

                // コンフリクトモーダル
                if (this.conflictModal) {
                    this.conflictModal.style.display = state.conflictModal.visible ? 'flex' : 'none';
                    if (state.conflictModal.visible && state.conflictModal.conflictInfo) {
                        const info = state.conflictModal.conflictInfo;
                        if (this.conflictFilePath) {
                            this.conflictFilePath.textContent = info.path;
                        }
                        if (this.diffLocal) {
                            this.diffLocal.textContent = info.localContent;
                        }
                        if (this.diffRemote) {
                            this.diffRemote.textContent = info.remoteContent;
                        }
                    }
                }

                // 画像プレビューモーダル
                if (this.imagePreviewModal) {
                    this.imagePreviewModal.style.display = state.imagePreviewModal.visible ? 'flex' : 'none';
                    if (state.imagePreviewModal.visible) {
                        this.updateImagePreview(state.imagePreviewModal);
                    }
                }
            })
        );
    }

    private async handleFileCreation(): Promise<void> {
        const modalStore = useModalStore.getState();
        const filename = modalStore.filenameModal.value.trim();

        if (!filename) {
            eventLogger.log('ModalHost', 'file-creation-error', { error: 'no-filename' });
            alert('ファイル名を入力してください');
            return;
        }

        eventLogger.log('ModalHost', 'file-creation-start', { filename });
        modalStore.hideFilenameModal();

        try {
            await this.api.CreateNewFile(filename);
            eventLogger.log('ModalHost', 'file-creation-success', { filename });
            
            // ファイルリストを再読み込み
            const files = await this.api.GetFileList();
            useDocStore.getState().setFiles(files);

            // 新規ファイルを読み込む
            const newFilePath = `content/${filename}.md`;
            const content = await this.api.LoadFile(newFilePath);
            useDocStore.getState().setCurrentPath(newFilePath);
            useDocStore.getState().setMarkdownContent(content);
            useDocStore.getState().clearUnsavedChanges();

            const { prepared, html } = await renderMarkdownPreview(content, this.api, newFilePath);
            const theme = useUIStore.getState().theme;
            const customCss = useCustomCssStore.getState().customCss;
            const withCss = applyCustomCssToHtml(prepared, html, customCss, theme);
            useDocStore.getState().setPreviewHtml(convertTimestampsToLinks(withCss));

            useUIStore.getState().setStatusMessage('新規ファイルを作成しました', 2000);
        } catch (error) {
            console.error('Failed to create file:', error);
            const { setStatusMessage } = useUIStore.getState();
            setStatusMessage('ファイルの作成に失敗しました', 3000);
        }
    }

    private async handleRenameFile(): Promise<void> {
        const modalStore = useModalStore.getState();
        const newFilename = modalStore.renameFileModal.value.trim();

        if (!newFilename) {
            alert('ファイル名を入力してください');
            return;
        }

        const oldPath = modalStore.renameFileModal.currentPath;
        const newPath = `content/${newFilename}.md`;

        try {
            if (this.api.RenameFile) {
                await this.api.RenameFile(oldPath, newPath);
                modalStore.hideRenameFileModal();

                // ファイルリストを再読み込み
                const files = await this.api.GetFileList();
                useDocStore.getState().setFiles(files);

                // 現在のファイルパスを更新
                if (useDocStore.getState().currentPath === oldPath) {
                    useDocStore.getState().setCurrentPath(newPath);
                }

                useUIStore.getState().setStatusMessage('ファイル名を変更しました', 2000);
            }
        } catch (error) {
            console.error('Failed to rename file:', error);
            useUIStore.getState().setStatusMessage('ファイル名の変更に失敗しました', 3000);
        }
    }

    private async handleSaveCustomCSS(): Promise<void> {
        const modalStore = useModalStore.getState();
        const css = modalStore.customCssModal.value;

        try {
            await this.api.SetCustomCSS(css);
            useCustomCssStore.getState().setCustomCss(css);
            modalStore.hideCustomCssModal();
            useUIStore.getState().setStatusMessage('カスタムCSSを保存しました', 2000);
            await this.refreshPreviewWithCustomCss(css);
        } catch (error) {
            console.error('Failed to save custom CSS:', error);
            useUIStore.getState().setStatusMessage('カスタムCSSの保存に失敗しました', 3000);
        }
    }

    private async handleClearCustomCSS(): Promise<void> {
        try {
            await this.api.ClearCustomCSS();
            useModalStore.getState().setCustomCssModalValue('');
            useCustomCssStore.getState().setCustomCss('');
            useUIStore.getState().setStatusMessage('カスタムCSSをクリアしました', 2000);
            await this.refreshPreviewWithCustomCss('');
        } catch (error) {
            console.error('Failed to clear custom CSS:', error);
            useUIStore.getState().setStatusMessage('カスタムCSSのクリアに失敗しました', 3000);
        }
    }

    private async handleWebClipImport(): Promise<void> {
        const modalStore = useModalStore.getState();
        const url = modalStore.webClipModal.url.trim();

        if (!url) {
            modalStore.setWebClipWarnings(['URLを入力してください']);
            return;
        }

        try {
            new URL(url);
        } catch {
            modalStore.setWebClipWarnings(['有効なURLを入力してください']);
            return;
        }

        modalStore.setWebClipImporting(true);
        modalStore.setWebClipWarnings([]);
        useUIStore.getState().setStatusMessage('Web Clipを取り込んでいます...', 10000);

        try {
            const result = await this.api.ClipURL({
                url,
                mode: 'article',
                imageMode: 'download',
                outputDir: '',
            });

            const files = await this.api.GetFileList();
            useDocStore.getState().setFiles(files);

            const content = await this.api.LoadFile(result.markdownPath);
            useDocStore.getState().setCurrentPath(result.markdownPath);
            useDocStore.getState().setMarkdownContent(content);
            useDocStore.getState().clearUnsavedChanges();
            const { prepared, html } = await renderMarkdownPreview(content, this.api, result.markdownPath);
            const theme = useUIStore.getState().theme;
            const customCss = useCustomCssStore.getState().customCss;
            const withCss = applyCustomCssToHtml(prepared, html, customCss, theme);
            useDocStore.getState().setPreviewHtml(convertTimestampsToLinks(withCss));

            modalStore.hideWebClipModal();
            const warningSuffix = result.warnings.length > 0 ? `（警告 ${result.warnings.length} 件）` : '';
            useUIStore.getState().setStatusMessage(`Web Clipを取り込みました${warningSuffix}`, 3000);
            eventLogger.log('ModalHost', 'web-clip-import-success', {
                path: result.markdownPath,
                warnings: result.warnings.length,
            });
        } catch (error) {
            console.error('Failed to import web clip:', error);
            const message = error instanceof Error ? error.message : 'Web Clipの取り込みに失敗しました';
            modalStore.setWebClipWarnings([message]);
            useUIStore.getState().setStatusMessage('Web Clipの取り込みに失敗しました', 3000);
            eventLogger.log('ModalHost', 'web-clip-import-error', { error: message });
        } finally {
            useModalStore.getState().setWebClipImporting(false);
        }
    }

    private async refreshPreviewWithCustomCss(customCss: string): Promise<void> {
        const docStore = useDocStore.getState();
        if (!docStore.currentPath || docStore.currentPath.toLowerCase().endsWith('.pdf')) {
            return;
        }
        try {
            const { prepared, html } = await renderMarkdownPreview(docStore.markdownContent, this.api, docStore.currentPath);
            const theme = useUIStore.getState().theme;
            const withCss = applyCustomCssToHtml(prepared, html, customCss, theme);
            const finalHtml = convertTimestampsToLinks(withCss);
            docStore.setPreviewHtml(finalHtml);
        } catch (error) {
            console.error('Failed to refresh preview after custom CSS update:', error);
        }
    }

    private renderCsvEditTable(data: string[][]): void {
        if (!this.csvEditTableHead || !this.csvEditTableBody) {
            return;
        }

        this.csvEditTableHead.innerHTML = '';
        this.csvEditTableBody.innerHTML = '';

        if (data.length === 0) {
            return;
        }

        // ヘッダー行
        const headerRow = document.createElement('tr');
        data[0].forEach((cell, colIndex) => {
            const th = document.createElement('th');
            th.contentEditable = 'true';
            th.textContent = cell;
            th.dataset.col = String(colIndex);
            headerRow.appendChild(th);
        });
        this.csvEditTableHead.appendChild(headerRow);

        // データ行
        for (let i = 1; i < data.length; i++) {
            const row = document.createElement('tr');
            data[i].forEach((cell, colIndex) => {
                const td = document.createElement('td');
                td.contentEditable = 'true';
                td.textContent = cell;
                td.dataset.row = String(i - 1);
                td.dataset.col = String(colIndex);
                row.appendChild(td);
            });
            this.csvEditTableBody.appendChild(row);
        }

        this.applyCsvSelection();
    }

    private collectCsvTableData(): string[][] {
        const data: string[][] = [];
        if (!this.csvEditTableHead || !this.csvEditTableBody) {
            return data;
        }
        const headerRow = this.csvEditTableHead.querySelector('tr');
        if (headerRow) {
            const headerCells = Array.from(headerRow.querySelectorAll('th'));
            data.push(headerCells.map((th) => th.textContent || ''));
        }

        const rows = this.csvEditTableBody.querySelectorAll('tr');
        rows.forEach((row) => {
            const cells = Array.from(row.querySelectorAll('td'));
            data.push(cells.map((td) => td.textContent || ''));
        });
        return data;
    }

    private applyCsvSelection(): void {
        if (!this.csvEditTableHead || !this.csvEditTableBody) {
            return;
        }
        this.csvEditTableHead.querySelectorAll('th').forEach((cell) => {
            cell.classList.remove('selected');
        });
        this.csvEditTableBody.querySelectorAll('td').forEach((cell) => {
            cell.classList.remove('selected');
        });

        if (this.csvSelectedCol !== null) {
            this.csvEditTableHead.querySelectorAll(`th[data-col="${this.csvSelectedCol}"]`).forEach((cell) => {
                cell.classList.add('selected');
            });
            this.csvEditTableBody.querySelectorAll(`td[data-col="${this.csvSelectedCol}"]`).forEach((cell) => {
                cell.classList.add('selected');
            });
        }

        if (this.csvSelectedRow !== null) {
            this.csvEditTableBody.querySelectorAll(`td[data-row="${this.csvSelectedRow}"]`).forEach((cell) => {
                cell.classList.add('selected');
            });
        }
    }

    private handleCsvAddRow(): void {
        const modal = useModalStore.getState().csvEditModal;
        if (modal.hasMore) {
            useUIStore.getState().setStatusMessage('行数は最終ページでのみ変更できます', 2500);
            return;
        }
        const data = this.collectCsvTableData();
        const limit = modal.limit ?? CSV_PAGE_LIMIT;
        if (Math.max(data.length - 1, 0) >= limit) {
            useUIStore.getState().setStatusMessage(`1ページには${limit}行まで追加できます`, 2500);
            return;
        }
        const columnCount = Math.max(data[0]?.length ?? 0, 1);
        const newRow = Array.from({ length: columnCount }, () => '');

        if (data.length === 0) {
            data.push(Array.from({ length: columnCount }, (_, idx) => `列${idx + 1}`));
        }
        const insertIndex = this.csvSelectedRow !== null ? this.csvSelectedRow + 2 : data.length;
        data.splice(insertIndex, 0, newRow);

        this.csvSelectedRow = this.csvSelectedRow !== null ? this.csvSelectedRow + 1 : data.length - 2;
        useModalStore.getState().setCsvEditModalData(data);
    }

    private handleCsvAddCol(): void {
        useUIStore.getState().setStatusMessage('ページ編集では列数を変更できません', 2500);
    }

    private handleCsvDeleteRow(): void {
        const modal = useModalStore.getState().csvEditModal;
        if (modal.hasMore) {
            useUIStore.getState().setStatusMessage('行数は最終ページでのみ変更できます', 2500);
            return;
        }
        const data = this.collectCsvTableData();
        const bodyLength = Math.max(data.length - 1, 0);
        if (this.csvSelectedRow === null || bodyLength === 0) {
            useUIStore.getState().setStatusMessage('削除する行を選択してください', 2000);
            return;
        }
        const removeIndex = this.csvSelectedRow + 1;
        if (removeIndex >= data.length) {
            return;
        }
        data.splice(removeIndex, 1);
        this.csvSelectedRow = null;
        useModalStore.getState().setCsvEditModalData(data);
    }

    private handleCsvDeleteCol(): void {
        useUIStore.getState().setStatusMessage('ページ編集では列数を変更できません', 2500);
    }

    private updateCsvPageControls(): void {
        const modal = useModalStore.getState().csvEditModal;
        const page = modal.page ?? 1;
        const limit = modal.limit ?? CSV_PAGE_LIMIT;
        const totalRows = modal.totalRows ?? Math.max(modal.data.length - 1, 0);
        const lastPage = Math.max(1, Math.ceil(totalRows / limit));
        if (this.csvPrevPageBtn) {
            this.csvPrevPageBtn.disabled = page <= 1;
        }
        if (this.csvNextPageBtn) {
            this.csvNextPageBtn.disabled = !modal.hasMore;
        }
        if (this.csvPageLabel) {
            this.csvPageLabel.textContent = `${page} / ${lastPage}（全${totalRows}行）`;
        }
        if (this.csvAddRowBtn) {
            this.csvAddRowBtn.disabled = Boolean(modal.hasMore) ||
                Math.max(modal.data.length - 1, 0) >= limit;
        }
        if (this.csvDeleteRowBtn) {
            this.csvDeleteRowBtn.disabled = Boolean(modal.hasMore);
        }
        if (this.csvAddColBtn) {
            this.csvAddColBtn.disabled = true;
        }
        if (this.csvDeleteColBtn) {
            this.csvDeleteColBtn.disabled = true;
        }
    }

    private async loadCsvEditPage(direction: -1 | 1): Promise<void> {
        const modal = useModalStore.getState().csvEditModal;
        if (!modal.visible) {
            return;
        }
        if (this.csvPageIsDirty()) {
            useUIStore.getState().setStatusMessage('ページ移動前に変更を保存または取消してください', 3000);
            return;
        }
        const currentPage = modal.page ?? 1;
        const targetPage = currentPage + direction;
        if (targetPage < 1 || (direction > 0 && !modal.hasMore)) {
            return;
        }
        try {
            const page = await this.csvPageClient.load({
                path: modal.filePath,
                page: targetPage,
                limit: modal.limit ?? CSV_PAGE_LIMIT,
            });
            if (!page || this.destroyed) {
                return;
            }
            const current = useModalStore.getState().csvEditModal;
            if (!current.visible || current.filePath !== modal.filePath) {
                return;
            }
            useModalStore.getState().setCsvEditPage(page);
        } catch (error) {
            if (this.destroyed) {
                return;
            }
            console.error('Failed to load CSV page:', error);
            useUIStore.getState().setStatusMessage('CSVページの読み込みに失敗しました', 3000);
        }
    }

    private async handleSaveCsv(): Promise<void> {
        if (this.destroyed || this.csvSaveInFlight) {
            return;
        }
        const modalStore = useModalStore.getState();
        const modal = modalStore.csvEditModal;
        const filePath = modal.filePath;

        // テーブルからデータを取得
        const data: string[][] = [];
        if (this.csvEditTableHead && this.csvEditTableBody) {
            const headerRow = this.csvEditTableHead.querySelector('tr');
            if (headerRow) {
                const headerCells = Array.from(headerRow.querySelectorAll('th'));
                data.push(headerCells.map((th) => th.textContent || ''));
            }

            const rows = this.csvEditTableBody.querySelectorAll('tr');
            rows.forEach((row) => {
                const cells = Array.from(row.querySelectorAll('td'));
                data.push(cells.map((td) => td.textContent || ''));
            });
        }

        const inputSnapshot = JSON.stringify(data);
        const generation = ++this.csvSaveGeneration;
        this.csvSaveInFlight = true;
        if (this.csvSaveBtn) {
            this.csvSaveBtn.disabled = true;
        }
        try {
            if (data.length === 0) {
                throw new Error('CSV header is missing');
            }
            const result = await saveCsvPage(this.api, {
                path: filePath,
                revision: modal.revision ?? '',
                page: modal.page ?? 1,
                limit: modal.limit ?? CSV_PAGE_LIMIT,
                header: data[0],
                rows: data.slice(1),
            }, {
                totalRows: modal.totalRows ?? Math.max(data.length - 1, 0),
            });
            if (this.destroyed) {
                return;
            }
            if (!this.isCsvSaveIdentityCurrent(generation, modal)) {
                return;
            }
            const currentData = this.collectCsvTableData();
            if (JSON.stringify(currentData) !== inputSnapshot) {
                const savedRows = Math.max(data.length - 1, 0);
                const currentRows = Math.max(currentData.length - 1, 0);
                const adjustedTotalRows = Math.max(0, result.totalRows + currentRows - savedRows);
                modalStore.rebaseCsvEditRevision(result.revision, adjustedTotalRows, currentData);
                this.csvPageIdentity = this.csvModalIdentity(useModalStore.getState().csvEditModal);
                this.csvPageBaseline = inputSnapshot;
                return;
            }
            modalStore.hideCsvEditModal();
            useUIStore.getState().setStatusMessage('CSVファイルを保存しました', 2000);
        } catch (error) {
            if (!this.isCsvSaveIdentityCurrent(generation, modal)) {
                return;
            }
            console.error('Failed to save CSV:', error);
            const message = String(error).toLowerCase();
            if (message.includes('commit completed') || message.includes('durability is unconfirmed')) {
                useUIStore.getState().setStatusMessage(
                    'CSVは反映済みですが永続化を確認できません．自動再試行せず再読み込みしてください',
                    6000,
                );
            } else {
                useUIStore.getState().setStatusMessage('CSVファイルの保存に失敗しました', 3000);
            }
        } finally {
            // A newer save cannot start while this flag is set，so the owner
            // operation always releases it even when hide/destroy invalidated
            // its UI generation．
            this.csvSaveInFlight = false;
            if (this.csvSaveBtn && !this.destroyed) {
                this.csvSaveBtn.disabled = false;
            }
        }
    }

    private isCsvSaveIdentityCurrent(
        generation: number,
        expected: ReturnType<typeof useModalStore.getState>['csvEditModal'],
    ): boolean {
        if (this.destroyed || generation !== this.csvSaveGeneration) {
            return false;
        }
        const current = useModalStore.getState().csvEditModal;
        return current.visible && current.filePath === expected.filePath &&
            current.page === expected.page && current.revision === expected.revision;
    }

    private csvModalIdentity(modal: ReturnType<typeof useModalStore.getState>['csvEditModal']): string {
        return `${modal.filePath}\n${modal.page ?? 1}\n${modal.revision ?? ''}`;
    }

    private csvPageIsDirty(): boolean {
        return this.csvPageBaseline !== '' && JSON.stringify(this.collectCsvTableData()) !== this.csvPageBaseline;
    }

    private async handleResolveConflict(): Promise<void> {
        const modalStore = useModalStore.getState();
        if (!modalStore.conflictModal.conflictInfo) {
            return;
        }

        const strategyInput = document.querySelector('input[name="conflictResolution"]:checked') as HTMLInputElement;
        const strategy = (strategyInput?.value || 'merge') as ConflictResolutionStrategy;

        try {
            await this.api.ResolveConflict(modalStore.conflictModal.conflictInfo.path, strategy);
            modalStore.hideConflictModal();

            // ファイルを再読み込み
            const docStore = useDocStore.getState();
            if (docStore.currentPath) {
                const content = await this.api.LoadFile(docStore.currentPath);
                docStore.setMarkdownContent(content);
                docStore.clearUnsavedChanges();
            }

            useUIStore.getState().setStatusMessage('コンフリクトを解決しました', 2000);
        } catch (error) {
            console.error('Failed to resolve conflict:', error);
            useUIStore.getState().setStatusMessage('コンフリクトの解決に失敗しました', 3000);
        }
    }

    private updateImagePreview(state: { imagePath: string; imageName: string; metadata: string; systemMetadata: string }): void {
        if (this.imagePreviewImg) {
            this.api.GetImageFileURL(state.imagePath).then((url) => {
                if (this.imagePreviewImg) {
                    this.imagePreviewImg.src = url;
                }
            });
        }
        if (this.imagePreviewName) {
            this.imagePreviewName.textContent = state.imageName;
        }
        if (this.imagePreviewPath) {
            this.imagePreviewPath.textContent = state.imagePath;
        }
        if (this.imageMetadataEditor) {
            this.imageMetadataEditor.value = state.metadata;
        }
        if (this.imageSystemMetadataEditor) {
            this.imageSystemMetadataEditor.value = state.systemMetadata;
        }
    }

    private async handleSaveImageMetadata(): Promise<void> {
        const modalStore = useModalStore.getState();
        const imagePath = modalStore.imagePreviewModal.imagePath;
        const metadata = modalStore.imagePreviewModal.metadata;

        try {
            await this.api.SaveImageMetadata(imagePath, metadata);
            if (this.imageMetadataStatus) {
                this.imageMetadataStatus.textContent = '保存しました';
                this.imageMetadataStatus.className = 'image-metadata-status success';
            }
            useUIStore.getState().setStatusMessage('画像メタデータを保存しました', 2000);
        } catch (error) {
            console.error('Failed to save image metadata:', error);
            if (this.imageMetadataStatus) {
                this.imageMetadataStatus.textContent = '保存に失敗しました';
                this.imageMetadataStatus.className = 'image-metadata-status error';
            }
        }
    }

    private async handleSaveImageSystemMetadata(): Promise<void> {
        const modalStore = useModalStore.getState();
        const imagePath = modalStore.imagePreviewModal.imagePath;
        const systemMetadata = modalStore.imagePreviewModal.systemMetadata;

        try {
            await this.api.SaveImageSystemMetadata(imagePath, systemMetadata);
            if (this.imageSystemMetadataStatus) {
                this.imageSystemMetadataStatus.textContent = '保存しました';
                this.imageSystemMetadataStatus.className = 'image-metadata-status success';
            }
            useUIStore.getState().setStatusMessage('画像KMTDメタデータを保存しました', 2000);
        } catch (error) {
            console.error('Failed to save image system metadata:', error);
            if (this.imageSystemMetadataStatus) {
                this.imageSystemMetadataStatus.textContent = '保存に失敗しました';
                this.imageSystemMetadataStatus.className = 'image-metadata-status error';
            }
        }
    }

    destroy(): void {
        this.destroyed = true;
        this.csvSaveGeneration += 1;
        this.csvPageClient.destroy();
        this.unsubscribe.forEach((unsub) => unsub());
        this.unsubscribe = [];
    }
}
