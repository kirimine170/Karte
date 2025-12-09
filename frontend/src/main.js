import * as AppModule from '../wailsjs/wailsjs/go/main/App';
const { GetFileList, LoadFile, SaveFile, PreviewMarkdown, GetGraphData, CreateNewFile, ExportPDF, ExportPreviewHTML, GetCustomCSS, SetCustomCSS, ClearCustomCSS, ResolveConflict, ImportAudioFile, ImportAudioBase64, ImportImageFile, ImportImageBase64, ImportPdfFile, ImportPdfBase64, GetASRStatus, GetAudioFileURL, GetImageFileURL, GetPdfFileURL, GetImageList, GetImageMetadata, SaveImageMetadata, StartRecording, StopRecording, IsRecording, LogJS, RenamePdfFile, CaptureScreenInteractive, AllowClose } = AppModule;
// RenameFile and UpdateLinkToLatest may not exist in generated bindings yet, so import them conditionally
const RenameFile = AppModule.RenameFile || null;
const UpdateLinkToLatest = AppModule.UpdateLinkToLatest || null;
import { EventsOn, BrowserOpenURL } from '../wailsjs/wailsjs/runtime/runtime';
import GraphModule from './graph-d3.js';

// Check if running in browser (no Wails backend)
const isBrowser = typeof window !== 'undefined' && !window.go;

// Mock functions for browser testing
const mockFunctions = {
    async GetFileList() {
        return [
            { path: 'content/README.md', title: 'README' },
            { path: 'content/Test.md', title: 'Test Document' }
        ];
    },

    async LoadFile(path) {
        return `# ${path.split('/').pop()}\n\nThis is a mock file content for testing in browser.\n\n## Features\n- Mock content\n- Browser testing\n- No backend required`;
    },

    async SaveFile(path, content) {
        console.log('Mock SaveFile called:', path, content.length);
        return true;
    },

    async PreviewMarkdown(content) {
        // Simple markdown to HTML conversion for testing
        return content
            .replace(/^# (.*$)/gim, '<h1>$1</h1>')
            .replace(/^## (.*$)/gim, '<h2>$1</h2>')
            .replace(/^### (.*$)/gim, '<h3>$1</h3>')
            .replace(/\*\*(.*)\*\*/gim, '<strong>$1</strong>')
            .replace(/\*(.*)\*/gim, '<em>$1</em>')
            .replace(/\n/gim, '<br>');
    },

    async GetGraphData() {
        return {
            nodes: [
                { id: 'doc:/README.md', label: 'README', kind: 'note', exists: true, degIn: 0, degOut: 1, tags: [] },
                { id: 'doc:/Test.md', label: 'Test Document', kind: 'note', exists: true, degIn: 1, degOut: 0, tags: [] }
            ],
            edges: [
                { id: 'e1', source: 'doc:/README.md', target: 'doc:/Test.md', kind: 'wikilink', weight: 1 }
            ],
            meta: { directed: true }
        };
    },

    async CreateNewFile(filename) {
        console.log('Mock CreateNewFile called:', filename);
        // Simulate file creation
        return true;
    },

    async ExportPDF(html) {
        console.log('Mock ExportPDF called, HTML length:', html.length);
        return '/mock/path/to/export.pdf';
    },

    async ExportPreviewHTML(html) {
        console.log('Mock ExportPreviewHTML called, HTML length:', html.length);
        return 'file:///mock/path/to/preview.html';
    },

    async GetCustomCSS() {
        console.log('Mock GetCustomCSS called');
        return '';
    },

    async SetCustomCSS(css) {
        console.log('Mock SetCustomCSS called, CSS length:', css.length);
        return true;
    },

    async ClearCustomCSS() {
        console.log('Mock ClearCustomCSS called');
        return true;
    },

    async ResolveConflict(path, strategy) {
        console.log('Mock ResolveConflict called:', path, strategy);
        return true;
    },

    async ImportAudioFile(path) {
        console.log('Mock ImportAudioFile called:', path);
        return `data/audio/mock-${Date.now()}.wav`;
    },

    async ImportAudioBase64(name, data) {
        console.log('Mock ImportAudioBase64 called:', name, data.length);
        return `data/audio/mock-${Date.now()}.wav`;
    },

    async ImportImageFile(path) {
        console.log('Mock ImportImageFile called:', path);
        return `data/image/mock-${Date.now()}.png`;
    },

    async ImportImageBase64(name, data) {
        console.log('Mock ImportImageBase64 called:', name, data.length);
        return `data/image/mock-${Date.now()}.png`;
    },

    async GetASRStatus() {
        console.log('Mock GetASRStatus called');
        return { initialized: false, initializing: false };
    },

    async GetAudioFileURL(audioPath) {
        console.log('Mock GetAudioFileURL called:', audioPath);
        return `/audio/${audioPath}`;
    },

    async GetImageFileURL(imagePath) {
        console.log('Mock GetImageFileURL called:', imagePath);
        return `/image/${imagePath}`;
    },

    async ImportPdfFile(path) {
        console.log('Mock ImportPdfFile called:', path);
        return `content/mock-${Date.now()}.pdf`;
    },

    async ImportPdfBase64(name, data) {
        console.log('Mock ImportPdfBase64 called:', name, data.length);
        return `content/mock-${Date.now()}.pdf`;
    },

    async GetPdfFileURL(pdfPath) {
        console.log('Mock GetPdfFileURL called:', pdfPath);
        return `/pdf/${pdfPath}`;
    },

    async RenamePdfFile(oldPath, newPath) {
        console.log('Mock RenamePdfFile called:', oldPath, '->', newPath);
        return true;
    },

    async GetImageList() {
        console.log('Mock GetImageList called');
        return [
            { path: 'data/image/mock-1.png', name: 'mock-1.png', size: 1024, modTime: new Date().toISOString() },
            { path: 'data/image/mock-2.jpg', name: 'mock-2.jpg', size: 2048, modTime: new Date().toISOString() }
        ];
    },

    async GetImageMetadata(path) {
        console.log('Mock GetImageMetadata called:', path);
        return 'title: mock image\nnotes: サンプルメタデータ';
    },

    async SaveImageMetadata(path, yaml) {
        console.log('Mock SaveImageMetadata called:', path, yaml);
        return true;
    },

    async StartRecording() {
        console.log('Mock StartRecording called');
        return true;
    },

    async StopRecording() {
        console.log('Mock StopRecording called');
        return 'data/audio/mock-recording.m4a';
    },

    async IsRecording() {
        console.log('Mock IsRecording called');
        return false;
    },

    async LogJS(level, msg) {
        console.log(`[Mock LogJS] ${level}: ${msg}`);
    },

    async RenameFile(oldPath, newPath) {
        console.log('Mock RenameFile called:', oldPath, '->', newPath);
        return true;
    },

    async UpdateLinkToLatest(sourceDocID, targetDocID) {
        console.log('Mock UpdateLinkToLatest called:', sourceDocID, targetDocID);
        return true;
    }
};

// Use mock functions if in browser, otherwise use real Wails functions
const api = isBrowser ? mockFunctions : {
    GetFileList,
    LoadFile,
    SaveFile,
    PreviewMarkdown,
    GetGraphData,
    CreateNewFile,
    ExportPDF,
    ExportPreviewHTML,
    GetCustomCSS,
    SetCustomCSS,
    ClearCustomCSS,
    ResolveConflict,
    ImportAudioFile,
    ImportAudioBase64,
    ImportImageFile,
    ImportImageBase64,
    ImportPdfFile,
    ImportPdfBase64,
    GetASRStatus,
    GetAudioFileURL,
    GetImageFileURL,
    GetPdfFileURL,
    GetImageList,
    GetImageMetadata,
    StartRecording,
    StopRecording,
    IsRecording,
    SaveImageMetadata,
    LogJS,
    RenameFile: RenameFile || (() => Promise.reject(new Error('RenameFile not available'))),
    RenamePdfFile: RenamePdfFile || (() => Promise.reject(new Error('RenamePdfFile not available'))),
    UpdateLinkToLatest: UpdateLinkToLatest || (() => Promise.reject(new Error('UpdateLinkToLatest not available')))
};

// Logging helper function that writes to app.log via Go backend
function jsLog(level, ...args) {
    const msg = args.map(arg => {
        if (typeof arg === 'object') {
            try {
                return JSON.stringify(arg);
            } catch (e) {
                return String(arg);
            }
        }
        return String(arg);
    }).join(' ');

    // Also log to browser console for immediate feedback
    if (level === 'ERROR' || level === 'ERR') {
        console.error(...args);
    } else if (level === 'WARN' || level === 'WARNING') {
        console.warn(...args);
    } else if (level === 'DEBUG') {
        console.debug(...args);
    } else {
        console.log(...args);
    }

    // Send to Go backend to write to app.log
    if (api.LogJS) {
        api.LogJS(level, msg).catch(err => {
            console.error('Failed to log to app.log:', err);
        });
    }
}

// Global variables
let currentPath = '';
let recordingTranscriptPath = null;
let files = [];
let graphModule = null;
let lastMarpSlideIndex = 0;
let statusClearTimer = null;
let currentMetadataImagePath = null;
let imageMetadataDirty = false;
let imageMetadataLoading = false;
let isDirty = false;
let lastSavedContent = '';
let isModalShowing = false;
let closeHandlerRegistered = false;

// DOM elements
const statusEl = document.getElementById('status');
const ta = document.getElementById('editor');
const pv = document.getElementById('preview');
const tree = document.getElementById('tree');
const inp = document.getElementById('q');
const saveBtn = document.getElementById('saveBtn');
const openBtn = document.getElementById('openBtn');
const newBtn = document.getElementById('newBtn');
const exportPdfBtn = document.getElementById('exportPdfBtn');
const themeSel = document.getElementById('theme');
const hardwrapChk = document.getElementById('hardwrap');
const tabs = document.querySelectorAll('.tab');
const tabContents = document.querySelectorAll('.tab-content');
const dropOverlay = document.getElementById('dropOverlay');
const recordingBtn = document.getElementById('recordingBtn');
const sidebarToggle = document.getElementById('sidebarToggle');
const galleryToggle = document.getElementById('galleryToggle');
const layout = document.querySelector('.layout');
const row = document.querySelector('.row');

// Image gallery elements
const imageGalleryContainer = document.getElementById('imageGalleryContainer');
const imageGalleryGrid = document.getElementById('imageGalleryGrid');
const imageGalleryEmpty = document.getElementById('imageGalleryEmpty');
const imagePreviewModal = document.getElementById('imagePreviewModal');
const imagePreviewImg = document.getElementById('imagePreviewImg');
const imagePreviewName = document.getElementById('imagePreviewName');
const imagePreviewPath = document.getElementById('imagePreviewPath');
const imagePreviewClose = document.getElementById('imagePreviewClose');
const imageMetadataEditor = document.getElementById('imageMetadataEditor');
const imageMetadataSaveBtn = document.getElementById('imageMetadataSaveBtn');
const imageMetadataStatus = document.getElementById('imageMetadataStatus');
const captureScreenBtn = document.getElementById('captureScreenBtn');

if (imageMetadataEditor) {
    imageMetadataEditor.disabled = true;
    imageMetadataEditor.addEventListener('input', () => {
        if (imageMetadataLoading) {
            return;
        }
        imageMetadataDirty = true;
        updateImageMetadataStatus('未保存の変更があります', 'warning');
    });
}

if (imageMetadataSaveBtn) {
    imageMetadataSaveBtn.disabled = true;
    imageMetadataSaveBtn.addEventListener('click', (event) => {
        event.preventDefault();
        saveCurrentImageMetadata();
    });
}

// Modal elements
const filenameModal = document.getElementById('filenameModal');
const filenameInput = document.getElementById('filenameInput');
const createFileBtn = document.getElementById('createFileBtn');
const cancelFileBtn = document.getElementById('cancelFileBtn');

// Rename file modal elements
const renameFileModal = document.getElementById('renameFileModal');
const renameFileInput = document.getElementById('renameFileInput');
const confirmRenameBtn = document.getElementById('confirmRenameBtn');
const cancelRenameBtn = document.getElementById('cancelRenameBtn');

// Unsaved changes confirmation modal elements
const unsavedConfirmModal = document.getElementById('unsavedConfirmModal');
const unsavedSaveBtn = document.getElementById('unsavedSaveBtn');
const unsavedDiscardBtn = document.getElementById('unsavedDiscardBtn');
const unsavedCancelBtn = document.getElementById('unsavedCancelBtn');

// Custom CSS elements
const customCssBtn = document.getElementById('customCssBtn');
const customCssModal = document.getElementById('customCssModal');
const customCssTextarea = document.getElementById('customCssTextarea');
const saveCustomCssBtn = document.getElementById('saveCustomCssBtn');
const clearCustomCssBtn = document.getElementById('clearCustomCssBtn');
const cancelCustomCssBtn = document.getElementById('cancelCustomCssBtn');
const customCssStatus = document.getElementById('customCssStatus');

const supportedAudioExt = ['.wav', '.mp3', '.m4a'];
const supportedImageExt = ['.jpg', '.jpeg', '.png', '.gif', '.webp'];

// Conflict modal elements
const conflictModal = document.getElementById('conflictModal');
const conflictFilePath = document.getElementById('conflictFilePath');
const diffLocal = document.getElementById('diffLocal');
const diffRemote = document.getElementById('diffRemote');
const resolveConflictBtn = document.getElementById('resolveConflictBtn');
const cancelConflictBtn = document.getElementById('cancelConflictBtn');

// ASR status and transcription progress elements
const asrStatusEl = document.getElementById('asrStatus');
const asrStatusIndicator = asrStatusEl?.querySelector('.asr-status-indicator');
const asrStatusText = asrStatusEl?.querySelector('.asr-status-text');
const transcriptionProgressEl = document.getElementById('transcriptionProgress');
const transcriptionProgressBar = transcriptionProgressEl?.querySelector('.transcription-progress-bar');
const transcriptionProgressFill = transcriptionProgressEl?.querySelector('.transcription-progress-fill');
const transcriptionProgressText = transcriptionProgressEl?.querySelector('.transcription-progress-text');
const pdfExportProgressEl = document.getElementById('pdfExportProgress');
const pdfExportProgressBar = pdfExportProgressEl?.querySelector('.transcription-progress-bar');
const pdfExportProgressFill = pdfExportProgressEl?.querySelector('.transcription-progress-fill');
const pdfExportProgressText = pdfExportProgressEl?.querySelector('.transcription-progress-text');

// Audio player elements
const audioPlayerContainer = document.getElementById('audioPlayerContainer');
const audioPlayer = document.getElementById('audioPlayer');

let customCssCache = '';
let currentConflictInfo = null;
let asrStatusCheckInterval = null;

function setDirtyState(flag) {
    if (isDirty === flag) {
        return;
    }
    isDirty = flag;
    updateUnsavedIndicator();
}

function updateUnsavedIndicator() {
    if (tree) {
        const items = tree.querySelectorAll('.item');
        items.forEach((item) => {
            const isCurrent = item.dataset.path === currentPath;
            item.classList.toggle('unsaved', isCurrent && isDirty);
        });
    }
    if (saveBtn) {
        saveBtn.classList.toggle('unsaved', isDirty);
    }
}

async function confirmNavigationIfDirty() {
    if (!isDirty) {
        return true;
    }

    // 既にモーダルが表示されている場合は、新しいモーダルを表示しない
    if (isModalShowing) {
        return false; // キャンセル扱い
    }

    return new Promise((resolve) => {
        if (!unsavedConfirmModal) {
            // フォールバック: モーダルが存在しない場合はconfirmを使用
            const wantsSave = window.confirm('未保存の変更があります。保存してから続行しますか？\nOK: 保存して続行 / キャンセル: 破棄するか選択');
            if (wantsSave) {
                save().then(() => resolve(true)).catch(() => resolve(false));
                return;
            }
            const discard = window.confirm('変更を破棄して続行しますか？');
            if (discard) {
                lastSavedContent = ta?.value || '';
                setDirtyState(false);
                resolve(true);
            } else {
                resolve(false);
            }
            return;
        }

        // モーダル表示中フラグを設定
        isModalShowing = true;

        // 既存のイベントリスナーをクリアするために、一度削除して再追加
        const hideModal = () => {
            unsavedConfirmModal.style.display = 'none';
            isModalShowing = false; // フラグをリセット
        };

        const handleSave = async () => {
            try {
                await save();
                // save()が完了してsetDirtyState(false)が呼ばれるまで、モーダルを表示したままにする
                // これにより、保存処理中に別のイベントが発生しても新しいモーダルが表示されない
                hideModal();
                resolve(true);
            } catch (error) {
                console.error('Save failed during navigation confirm:', error);
                hideModal();
                resolve(false);
            }
        };

        const handleDiscard = () => {
            hideModal();
            lastSavedContent = ta?.value || '';
            setDirtyState(false);
            resolve(true);
        };

        const handleCancel = () => {
            hideModal();
            resolve(false);
        };

        // 毎回最新の要素を取得（グローバル変数ではなく）
        const saveBtn = document.getElementById('unsavedSaveBtn');
        const discardBtn = document.getElementById('unsavedDiscardBtn');
        const cancelBtn = document.getElementById('unsavedCancelBtn');

        if (!saveBtn || !discardBtn || !cancelBtn) {
            console.error('Unsaved confirm modal buttons not found');
            isModalShowing = false; // フラグをリセット
            resolve(false);
            return;
        }

        // 既存のイベントリスナーを削除してから新しいものを追加
        const newSaveBtn = saveBtn.cloneNode(true);
        const newDiscardBtn = discardBtn.cloneNode(true);
        const newCancelBtn = cancelBtn.cloneNode(true);
        saveBtn.parentNode.replaceChild(newSaveBtn, saveBtn);
        discardBtn.parentNode.replaceChild(newDiscardBtn, discardBtn);
        cancelBtn.parentNode.replaceChild(newCancelBtn, cancelBtn);

        newSaveBtn.onclick = handleSave;
        newDiscardBtn.onclick = handleDiscard;
        newCancelBtn.onclick = handleCancel;

        // モーダル外クリックでキャンセル
        const handleModalClick = (e) => {
            if (e.target === unsavedConfirmModal) {
                handleCancel();
            }
        };
        unsavedConfirmModal.onclick = handleModalClick;

        unsavedConfirmModal.style.display = 'flex';
    });
}

// Initialize the application
async function init() {
    console.log('Initializing Karte application...');
    try {
        // Load file list
        console.log('Loading file list...');
        await loadFileList();
        console.log('File list loaded, files count:', files.length);

        // Load first file if available
        if (files && files.length > 0) {
            console.log('Loading first file:', files[0]);
            console.log('First file path:', files[0].path);
            console.log('First file title:', files[0].title);
            await loadFile(files[0].path);
        } else {
            console.log('No files available to load');
        }

        // Setup event listeners
        console.log('Setting up event listeners...');
        setupEventListeners();

        // Setup Wails events
        console.log('Setting up Wails events...');
        if (!isBrowser) {
            EventsOn('file-changed', (path) => {
                console.log('File changed:', path);
                updatePreview();
                updateGraph();
            });

            // Link updated event (when link is updated to latest version)
            EventsOn('link-updated', (data) => {
                console.log('Link updated:', data);
                updatePreview();
            });

            // Conflict detection events
            EventsOn('conflict-detected', (conflictInfo) => {
                console.log('Conflict detected:', conflictInfo);
                showConflictResolutionModal(conflictInfo);
            });

            EventsOn('auto-merge-success', (data) => {
                console.log('Auto-merge succeeded:', data);
                statusEl.textContent = `ファイル「${data.path}」の変更を自動的に統合しました`;
                setTimeout(() => {
                    statusEl.textContent = '';
                }, 3000);
                // Reload file to show merged content
                if (currentPath) {
                    loadFile(currentPath);
                }
            });

            EventsOn('audio-imported', handleAudioImportedEvent);
            EventsOn('audio-transcribed', handleAudioTranscribedEvent);
            EventsOn('audio-transcribe-progress', handleAudioTranscribeProgress);
            EventsOn('image-imported', handleImageImportedEvent);
            EventsOn('pdf-export-progress', handlePDFExportProgress);
            EventsOn('pdf-export-completed', (payload) => {
                hidePDFExportProgress();
                if (exportPdfBtn) {
                    exportPdfBtn.disabled = false;
                }
                const pdfPath = payload.pdfPath || payload.PdfPath || '';
                const size = payload.size || payload.Size || 0;
                statusEl.textContent = `PDF exported: ${pdfPath} (${(size / 1024).toFixed(1)} KB)`;
                if (pdfPath) {
                    BrowserOpenURL(pdfPath);
                }
            });
            EventsOn('pdf-export-error', (payload) => {
                hidePDFExportProgress();
                if (exportPdfBtn) {
                    exportPdfBtn.disabled = false;
                }
                const error = payload.error || payload.Error || 'PDF export failed';
                statusEl.textContent = error;
                alert(error);
            });

            // Initialize ASR status check
            updateASRStatus();
            // Check ASR status periodically (every 2 seconds) until initialized
            asrStatusCheckInterval = setInterval(async () => {
                const status = await updateASRStatus();
                if (status && status.initialized) {
                    clearInterval(asrStatusCheckInterval);
                    asrStatusCheckInterval = null;
                }
            }, 2000);
        } else {
            console.log('Running in browser mode - Wails events disabled');
        }

        // Initialize graph module
        console.log('Initializing graph module...');
        try {
            graphModule = new GraphModule('graph-container');
            graphModule.on('node:click', (data) => {
                console.log('Node clicked:', data);
                const nodeId = data.id || data.ID;
                if (nodeId && nodeId.startsWith('doc:/')) {
                    loadFile(nodeId);
                    switchToTab('editor');
                }
            });

            // Setup tag nodes toggle button
            const toggleTagNodesBtn = document.getElementById('toggleTagNodesBtn');
            if (toggleTagNodesBtn) {
                toggleTagNodesBtn.addEventListener('click', () => {
                    if (graphModule) {
                        graphModule.toggleTagNodes();
                        toggleTagNodesBtn.textContent = `タグノード表示: ${graphModule.showTagNodes ? 'ON' : 'OFF'}`;
                    }
                });
            }

            await updateGraph();
        } catch (error) {
            console.error('Failed to initialize graph module:', error);
        }

        console.log('Initialization completed successfully');
    } catch (error) {
        console.error('Failed to initialize:', error);
        statusEl.textContent = 'Initialization failed: ' + error.message;
    }
}

// Create new file
async function createNewFile() {
    console.log('Create new file function called');

    const ok = await confirmNavigationIfDirty();
    if (!ok) {
        return;
    }

    // Show modal instead of prompt
    showFilenameModal();
}

// Show filename input modal
function showFilenameModal() {
    filenameInput.value = '';
    filenameModal.style.display = 'flex';
    filenameInput.focus();
}

// Hide filename input modal
function hideFilenameModal() {
    filenameModal.style.display = 'none';
}

// Handle file creation from modal
async function handleFileCreation() {
    const filename = filenameInput.value.trim();

    if (!filename) {
        alert('ファイル名を入力してください');
        return;
    }

    hideFilenameModal();

    try {
        statusEl.textContent = 'Creating new file...';
        console.log('Calling CreateNewFile with filename:', filename);

        const result = await api.CreateNewFile(filename);
        console.log('CreateNewFile result:', result);

        // Reload file list
        await loadFileList();

        // Load the newly created file
        const newFilePath = `content/${filename}.md`;
        await loadFile(newFilePath);

        statusEl.textContent = 'New file created';
        console.log('New file created successfully:', newFilePath);

    } catch (error) {
        console.error('Failed to create new file:', error);
        const errorMessage = error?.message || error || 'Unknown error';
        statusEl.textContent = 'Failed to create file: ' + errorMessage;
        alert('Failed to create file: ' + errorMessage);
    }
}

// Load file list from backend
async function loadFileList() {
    try {
        console.log('Calling GetFileList...');
        const result = await api.GetFileList();
        console.log('GetFileList result:', result);

        if (result === null || result === undefined) {
            console.warn('GetFileList returned null/undefined, using empty array');
            files = [];
        } else if (Array.isArray(result)) {
            files = result;
        } else {
            console.warn('GetFileList returned non-array:', typeof result, result);
            files = [];
        }

        console.log('Files loaded:', files.length, 'files');
        renderFileList();
    } catch (error) {
        console.error('Failed to load file list:', error);
        files = [];
        statusEl.textContent = 'Failed to load file list: ' + error.message;
    }
}

// Render file list in sidebar
function renderFileList() {
    tree.innerHTML = '';
    const frag = document.createDocumentFragment();
    const qq = (inp.value || '').toLowerCase();

    console.log('Rendering file list, files:', files);

    for (const file of files) {
        console.log('Processing file:', file);

        // Check if file object is valid
        if (!file || typeof file !== 'object') {
            console.warn('Invalid file object:', file);
            continue;
        }

        // Check if path property exists
        if (!file.path) {
            console.warn('File missing path property:', file);
            continue;
        }

        if (qq && !(file.path.toLowerCase().includes(qq) || (file.title && file.title.toLowerCase().includes(qq)))) {
            continue;
        }

        const a = document.createElement('a');
        a.className = 'item' + (file.path === currentPath ? ' active' : '');
        a.dataset.path = file.path;
        a.textContent = (file.title || 'Untitled') + '  —  ' + file.path.replace(/^content\//, '');
        const dot = document.createElement('span');
        dot.className = 'unsaved-dot';
        a.prepend(dot);
        if (file.path === currentPath && isDirty) {
            a.classList.add('unsaved');
        }
        a.href = '#';
        a.onclick = async (e) => {
            e.preventDefault();
            console.log('File clicked:', file);
            console.log('File path:', file.path);
            loadFile(file.path);
        };

        // Add right-click context menu for rename
        a.oncontextmenu = (e) => {
            e.preventDefault();
            console.log('Context menu (rename) on file:', file.path);
            jsLog('INFO', 'rename: contextmenu-event', file.path);
            showRenameMenu(e, file);
        };

        // Double-click to rename
        a.ondblclick = (e) => {
            e.preventDefault();
            console.log('Double-click (rename) on file:', file.path);
            jsLog('INFO', 'rename: dblclick-event', file.path);
            showRenameMenu(e, file);
        };

        jsLog('DEBUG', 'rename: handlers-attached', file.path);

        frag.appendChild(a);
    }
    tree.appendChild(frag);
    updateUnsavedIndicator();
}

// ---- Rename (file name change) ----

// Store the file to be renamed (used by modal handlers)
let fileToRename = null;

// Show rename file modal
function showRenameFileModal(file) {
    if (!file || !file.path) {
        jsLog('ERROR', 'rename: invalid-file', 'file or file.path is missing');
        console.error('showRenameFileModal: file or file.path is missing');
        return;
    }

    fileToRename = file;
    const oldPath = file.path; // e.g. "content/A.md" or "content/A.pdf"
    const currentName = oldPath.replace(/^content\//, '');
    const isMarkdown = /\.md$/i.test(currentName);
    const isPdf = /\.pdf$/i.test(currentName);

    // Remove extension (.md or .pdf) for display
    let currentNameWithoutExt = currentName;
    if (isMarkdown || isPdf) {
        currentNameWithoutExt = currentName.replace(/\.[^.]+$/, '');
    }

    jsLog('INFO', 'rename: show-modal', 'oldPath=' + oldPath, 'currentName=' + currentName, 'displayName=' + currentNameWithoutExt);
    console.log('Showing rename modal for:', currentName, '(displaying without extension:', currentNameWithoutExt + ')');

    if (renameFileInput) {
        renameFileInput.value = currentNameWithoutExt;
        renameFileModal.style.display = 'flex';
        renameFileInput.focus();
        renameFileInput.select(); // Select all text for easy editing
    } else {
        jsLog('ERROR', 'rename: modal-element-missing', 'renameFileInput not found');
        console.error('renameFileInput element not found');
    }
}

// Hide rename file modal
function hideRenameFileModal() {
    if (renameFileModal) {
        renameFileModal.style.display = 'none';
    }
    fileToRename = null;
}

// Handle file rename from modal
async function handleFileRename() {
    if (!fileToRename || !fileToRename.path) {
        jsLog('ERROR', 'rename: no-file-to-rename', 'fileToRename is missing');
        console.error('handleFileRename: fileToRename is missing');
        return;
    }

    const newName = renameFileInput ? renameFileInput.value.trim() : '';
    const oldPath = fileToRename.path; // e.g. "content/A.md" or "content/A.pdf"
    const currentName = oldPath.replace(/^content\//, '');
    const isMarkdown = /\.md$/i.test(currentName);
    const isPdf = /\.pdf$/i.test(currentName);

    // Remove extension for comparison (since modal displays without extension)
    let currentNameWithoutExt = currentName;
    if (isMarkdown || isPdf) {
        currentNameWithoutExt = currentName.replace(/\.[^.]+$/, '');
    }

    jsLog('INFO', 'rename: handle-rename', 'oldPath=' + oldPath, 'currentName=' + currentName, 'newName=' + newName);

    if (!newName) {
        jsLog('INFO', 'rename: empty-input', 'User entered empty string');
        statusEl.textContent = 'ファイル名が空です。';
        return;
    }

    // Compare without extension (since extension will be added automatically)
    if (newName === currentNameWithoutExt) {
        jsLog('INFO', 'rename: no-change', currentNameWithoutExt, '->', newName);
        statusEl.textContent = 'ファイル名が変更されていません。';
        hideRenameFileModal();
        return;
    }

    hideRenameFileModal();

    try {
        // Determine extension based on current file type
        const targetExt = isPdf ? '.pdf' : '.md';

        // Ensure extension is correct (.md or .pdf)
        let finalName = newName;
        if (!finalName.toLowerCase().endsWith(targetExt)) {
            // Remove any existing extension and add targetExt
            const nameWithoutExt = finalName.replace(/\.[^.]*$/, '');
            finalName = nameWithoutExt + targetExt;
            jsLog('INFO', 'rename: added-extension', 'original=' + newName, 'ext=' + targetExt, 'final=' + finalName);
        }

        // Ensure path starts with content/
        const newPath = finalName.startsWith('content/')
            ? finalName
            : 'content/' + finalName;

        const newLabel = newPath.replace(/^content\//, '');
        statusEl.textContent = `Renaming ${currentName} → ${newLabel} ...`;
        console.log('Calling rename API:', oldPath, '->', newPath);
        jsLog('INFO', 'rename: call-api', oldPath, '->', newPath);

        if (isPdf) {
            await api.RenamePdfFile(oldPath, newPath);
        } else {
            await api.RenameFile(oldPath, newPath);
        }

        statusEl.textContent = 'リネームしました: ' + newLabel;
        jsLog('INFO', 'rename: success', oldPath, '->', newPath);

        // Reload file list and graph
        jsLog('INFO', 'rename: reloading-file-list');
        await loadFileList();
        // Explicitly render file list to ensure UI is updated
        renderFileList();
        jsLog('INFO', 'rename: file-list-rendered');

        try {
            await updateGraph();
            jsLog('INFO', 'rename: graph-updated');
        } catch (err) {
            console.warn('Failed to update graph after rename:', err);
            jsLog('WARN', 'rename: graph-update-failed', err?.message || String(err));
        }

        // Open the renamed file
        jsLog('INFO', 'rename: opening-renamed-file', newPath);
        currentPath = newPath;
        await loadFile(newPath);

        // Ensure sidebar is updated with active state after loading
        renderFileList();
        jsLog('INFO', 'rename: file-view-updated-complete');
    } catch (error) {
        console.error('Failed to rename file:', error);
        statusEl.textContent = 'リネームに失敗しました: ' + (error?.message || error);
        jsLog('ERROR', 'rename: failed', error?.message || String(error));
    }
}

// Common interactive rename handler (now uses modal)
async function renameFileInteractive(file) {
    jsLog('INFO', 'rename: function-called', file?.path || 'no file');
    console.log('renameFileInteractive called with file:', file);
    showRenameFileModal(file);
}

// Simple context-menu handler (currently just prompts for new name)
function showRenameMenu(e, file) {
    jsLog('INFO', 'rename: showRenameMenu-called', file?.path || 'no file');
    console.log('showRenameMenu called with file:', file, 'event:', e);

    // For now we don't render a custom menu; we just reuse the interactive prompt.
    // This keeps UIシンプル and avoids extra DOM elements.
    jsLog('INFO', 'rename: calling-renameFileInteractive');
    renameFileInteractive(file);
}

// Load a file
async function loadFile(path) {
    console.log('loadFile called with path:', path);
    if (isDirty) {
        const ok = await confirmNavigationIfDirty();
        if (!ok) {
            console.log('Navigation cancelled due to unsaved changes');
            return;
        }
    }
    try {
        statusEl.textContent = 'Loading...';
        console.log('Calling LoadFile with path:', path);
        const content = await api.LoadFile(path);
        console.log('LoadFile returned content, length:', content.length);

        // Check if this is a PDF file
        const isPdf = path.toLowerCase().endsWith('.pdf');

        if (isPdf) {
            // PDF閲覧モード: エディタを非表示、プレビューにPDFを表示
            ta.value = '';
            currentPath = path;
            lastSavedContent = '';
            setDirtyState(false);

            // Hide editor
            if (ta) {
                ta.style.display = 'none';
            }
            const row = document.querySelector('.row');
            if (row) {
                row.classList.add('pdf-mode');
            }

            // Update preview with PDF
            await updatePreview();
        } else {
            // 通常のマークダウンファイル
            ta.value = content;
            currentPath = path;
            lastSavedContent = content;
            setDirtyState(false);

            // Show editor
            if (ta) {
                ta.style.display = '';
            }
            const row = document.querySelector('.row');
            if (row) {
                row.classList.remove('pdf-mode');
            }

            // Update preview
            await updatePreview();

            // Also update audio player directly (in case updatePreview didn't call it)
            await updateAudioPlayer(content);
        }

        // Update active file in sidebar
        document.querySelectorAll('.item').forEach(item => {
            item.classList.remove('active');
        });
        // Find and activate the correct item
        const items = document.querySelectorAll('.item');
        for (const item of items) {
            if (item.textContent.includes(path.replace(/^content\//, ''))) {
                item.classList.add('active');
                break;
            }
        }

        statusEl.textContent = 'Loaded';
        console.log('File loaded successfully');
    } catch (error) {
        console.error('Failed to load file:', error);
        statusEl.textContent = 'Failed to load file: ' + error.message;
    }
}

// Save current file
async function save() {
    console.log('Save function called, currentPath:', currentPath);
    if (!currentPath) {
        statusEl.textContent = 'No file selected';
        return;
    }

    // Prevent accidentally overwriting PDF files with text buffer
    if (currentPath.toLowerCase().endsWith('.pdf')) {
        console.warn('Save skipped: current file is a PDF:', currentPath);
        statusEl.textContent = 'PDF閲覧中は保存できません';
        return;
    }

    try {
        statusEl.textContent = 'Saving...';
        console.log('Calling SaveFile with path:', currentPath, 'content length:', ta.value.length);
        await api.SaveFile(currentPath, ta.value);
        statusEl.textContent = 'Saved';
        console.log('File saved successfully');
        lastSavedContent = ta.value;
        setDirtyState(false);
    } catch (error) {
        console.error('Failed to save file:', error);
        statusEl.textContent = 'Save failed: ' + error.message;
    }
}

// Update preview
async function updatePreview() {
    try {
        // Check if current file is a PDF
        if (currentPath && currentPath.toLowerCase().endsWith('.pdf')) {
            try {
                const pdfUrl = await api.GetPdfFileURL(currentPath);
                // Create HTML with embedded PDF
                const pdfHtml = `<!DOCTYPE html>
<html>
<head>
    <meta charset="utf-8">
    <title>PDF Viewer</title>
    <style>
        body {
            margin: 0;
            padding: 0;
            overflow: hidden;
            background: #525252;
        }
        embed {
            width: 100%;
            height: 100vh;
            border: none;
        }
    </style>
</head>
<body>
    <embed src="${pdfUrl}" type="application/pdf" />
</body>
</html>`;
                pv.srcdoc = pdfHtml;
                return;
            } catch (error) {
                console.error('Failed to load PDF:', error);
                pv.srcdoc = `<html><body><p>PDFの読み込みに失敗しました: ${error.message}</p></body></html>`;
                return;
            }
        }

        const content = ta.value;
        const caretIndex = typeof ta.selectionStart === 'number' ? ta.selectionStart : 0;
        const caretLine = getCaretLineNumber(content, caretIndex);

        // Check if this is a Marp presentation
        let isMarp = false;
        if (content.startsWith('---')) {
            const fmEnd = content.indexOf('\n---\n');
            if (fmEnd > 0) {
                const yamlContent = content.substring(4, fmEnd);
                // Check for marp: true
                const marpMatch = yamlContent.match(/^marp:\s*(true|false)\s*$/m);
                if (marpMatch && marpMatch[1] === 'true') {
                    isMarp = true;
                }
                // Check for Marp-specific fields (header, footer, paginate)
                if (!isMarp) {
                    const hasHeader = yamlContent.match(/^header:\s*["']?/m);
                    const hasFooter = yamlContent.match(/^footer:\s*["']?/m);
                    const hasPaginate = yamlContent.match(/^paginate:\s*(true|false)\s*$/m);
                    if (hasHeader || hasFooter || hasPaginate) {
                        isMarp = true;
                    }
                }
            }
        }

        const mdHtml = await api.PreviewMarkdown(content);

        // For Marp presentations, use the HTML directly (it's already a complete HTML document)
        if (isMarp) {
            const slideInfo = computeMarpSlideInfo(content, caretLine);
            if (slideInfo) {
                const { index, total } = slideInfo;
                if (Number.isFinite(index)) {
                    lastMarpSlideIndex = Math.max(0, Math.min(index, Math.max(total - 1, 0)));
                }
            }
            const targetSlide = Math.max(0, lastMarpSlideIndex || 0);

            const handleMarpLoad = () => {
                try {
                    const applySlide = (attempt = 0) => {
                        const frame = pv.contentWindow;
                        if (frame && typeof frame.showSlide === 'function') {
                            frame.showSlide(targetSlide);
                        } else if (attempt < 5) {
                            setTimeout(() => applySlide(attempt + 1), 50);
                        }
                    };
                    applySlide();

                    // Initialize KaTeX for Marp presentations
                    const iframeDoc = pv.contentDocument || pv.contentWindow.document;
                    if (iframeDoc) {
                        const initKaTeX = () => {
                            const iframeWindow = pv.contentWindow;
                            if (typeof iframeWindow.katex === 'undefined') {
                                setTimeout(initKaTeX, 50);
                                return;
                            }

                            jsLog('DEBUG', '[Marp] KaTeX is available, starting rendering');

                            // Render inline math
                            const inlineElements = iframeDoc.querySelectorAll('.katex-inline');
                            jsLog('DEBUG', `[Marp] Found ${inlineElements.length} inline math elements`);
                            inlineElements.forEach(function (el) {
                                try {
                                    if (el.querySelector('.katex')) {
                                        jsLog('DEBUG', '[Marp] Skipping already rendered inline math');
                                        return;
                                    }
                                    const rawContent = el.textContent.trim();
                                    jsLog('DEBUG', `[Marp] Inline math rawContent: ${JSON.stringify(rawContent)}`);
                                    const math = rawContent;
                                    jsLog('DEBUG', `[Marp] Inline math final: ${JSON.stringify(math)}`);
                                    iframeWindow.katex.render(math, el, {
                                        throwOnError: false,
                                        displayMode: false
                                    });
                                    jsLog('DEBUG', '[Marp] Inline math rendered successfully');
                                } catch (e) {
                                    jsLog('ERROR', '[Marp] KaTeX inline rendering error:', e);
                                    console.error('KaTeX inline rendering error:', e);
                                }
                            });

                            // Render block math
                            const blockElements = iframeDoc.querySelectorAll('.katex-block');
                            jsLog('DEBUG', `[Marp] Found ${blockElements.length} block math elements`);
                            blockElements.forEach(function (el) {
                                try {
                                    if (el.querySelector('.katex')) {
                                        jsLog('DEBUG', '[Marp] Skipping already rendered block math');
                                        return;
                                    }
                                    const rawContent = el.textContent.trim();
                                    jsLog('DEBUG', `[Marp] Block math rawContent: ${JSON.stringify(rawContent)}`);
                                    const math = rawContent;
                                    jsLog('DEBUG', `[Marp] Block math final: ${JSON.stringify(math)}`);
                                    iframeWindow.katex.render(math, el, {
                                        throwOnError: false,
                                        displayMode: true
                                    });
                                    jsLog('DEBUG', '[Marp] Block math rendered successfully');
                                } catch (e) {
                                    jsLog('ERROR', '[Marp] KaTeX block rendering error:', e);
                                    console.error('KaTeX block rendering error:', e);
                                }
                            });
                        };
                        initKaTeX();
                    }
                } catch (err) {
                    console.warn('Failed to restore Marp slide position:', err);
                } finally {
                    pv.removeEventListener('load', handleMarpLoad);
                }
            };
            pv.addEventListener('load', handleMarpLoad);

            // Reset drop handler flag so it can be set up again after iframe reloads
            if (pv.contentDocument) {
                pv.contentDocument._imageDropHandlersSetup = false;
            }

            pv.srcdoc = mdHtml;

            // Setup drop handlers after iframe loads (for Marp)
            pv.addEventListener('load', () => {
                setupPreviewImageDrop();
                // Make updateLinkToLatest available in iframe context
                setupUpdateLinkToLatestHandler();
            }, { once: true });

            // Handle audio player even for Marp presentations
            await updateAudioPlayer(content);
            return;
        }

        // Regular markdown preview (with or without YAML frontmatter)
        // site.RenderMarkdown already returns a complete HTML document,
        // but we need to inject custom CSS and theme variables
        // Custom CSS is always applied to regular markdown, regardless of frontmatter
        let finalHtml = injectCustomCSS(mdHtml);

        // Convert timestamps to clickable links if audio is available
        const audioPath = extractAudioPath(content);
        if (audioPath) {
            finalHtml = convertTimestampsToLinks(finalHtml);
        }

        // Setup timestamp click handlers before setting srcdoc
        setupTimestampClickHandlers();

        // Reset drop handler flag so it can be set up again after iframe reloads
        if (pv.contentDocument) {
            pv.contentDocument._imageDropHandlersSetup = false;
        }

        pv.srcdoc = finalHtml;

        // Setup drop handlers after iframe loads
        pv.addEventListener('load', () => {
            setupPreviewImageDrop();
            // Make updateLinkToLatest available in iframe context
            setupUpdateLinkToLatestHandler();
        }, { once: true });

        // Handle audio player
        await updateAudioPlayer(content);
    } catch (error) {
        console.error('Failed to update preview:', error);
        const errorMsg = error?.message || error?.toString() || 'Unknown error';
        pv.srcdoc = `<p style="color: red; padding: 20px;">Preview failed to load<br><small>${escapeHtml(errorMsg)}</small></p>`;

        // Hide audio player on error
        if (audioPlayerContainer) {
            audioPlayerContainer.style.display = 'none';
        }
    }
}

// Convert timestamps in HTML to clickable links
function convertTimestampsToLinks(html) {
    // Match timestamps in format [HH:MM:SS.mmm] or [MM:SS.mmm]
    // Pattern: [HH:MM:SS.mmm] or [MM:SS.mmm] where H, M, S are digits and mmm is milliseconds (0-999)
    const timestampRegex = /\[(\d{1,2}):(\d{2})(?::(\d{2}))?(?:\.(\d{1,3}))?\]/g;

    return html.replace(timestampRegex, (match, hours, minutes, seconds, milliseconds) => {
        // Calculate total seconds with milliseconds
        let totalSeconds = 0;
        const ms = milliseconds ? parseInt(milliseconds.padEnd(3, '0')) : 0;

        if (seconds !== undefined) {
            // [HH:MM:SS.mmm] format
            totalSeconds = parseInt(hours) * 3600 + parseInt(minutes) * 60 + parseInt(seconds) + ms / 1000;
        } else {
            // [MM:SS.mmm] format
            totalSeconds = parseInt(hours) * 60 + parseInt(minutes) + ms / 1000;
        }

        // Create clickable link with data attribute
        return `<a href="#" class="timestamp-link" data-timestamp="${totalSeconds}">${match}</a>`;
    });
}

// Setup timestamp click handlers in preview iframe
function setupTimestampClickHandlers() {
    if (!pv || !audioPlayer) {
        return;
    }

    // Remove previous handler if exists
    if (pv._timestampLoadHandler) {
        pv.removeEventListener('load', pv._timestampLoadHandler);
        pv._timestampLoadHandler = null;
    }

    const handleTimestampLoad = () => {
        pv.removeEventListener('load', handleTimestampLoad);
        pv._timestampLoadHandler = null;

        // Setup timestamp click handlers
        try {
            const iframeDoc = pv.contentDocument || pv.contentWindow.document;
            if (!iframeDoc) {
                // Retry after a short delay
                setTimeout(() => setupTimestampClickHandlers(), 100);
                return;
            }

            const timestampLinks = iframeDoc.querySelectorAll('.timestamp-link');

            timestampLinks.forEach(link => {
                // Remove existing listeners to avoid duplicates
                const newLink = link.cloneNode(true);
                link.parentNode.replaceChild(newLink, link);

                newLink.addEventListener('click', (e) => {
                    e.preventDefault();
                    const timestamp = parseFloat(newLink.getAttribute('data-timestamp'));
                    if (!isNaN(timestamp) && audioPlayer) {
                        audioPlayer.currentTime = timestamp;
                        // If audio is paused, start playing
                        if (audioPlayer.paused) {
                            audioPlayer.play().catch(err => {
                                console.error('Failed to play audio:', err);
                            });
                        }
                    }
                });

                // Add hover effect
                newLink.style.cursor = 'pointer';
                newLink.style.color = 'var(--accent, #7c3aed)';
                newLink.style.textDecoration = 'underline';
            });

            // Helper function to decode HTML entities
            const decodeHtmlEntities = (text) => {
                const textarea = iframeDoc.createElement('textarea');
                textarea.innerHTML = text;
                return textarea.value;
            };

            // Load KaTeX dynamically if not already loaded
            const loadKaTeX = (callback) => {
                const iframeWindow = pv.contentWindow;
                const iframeDoc = pv.contentDocument || iframeWindow.document;

                if (!iframeDoc) {
                    jsLog('ERROR', 'iframeDoc not available');
                    return;
                }

                // Check if KaTeX is already loaded
                if (typeof iframeWindow.katex !== 'undefined') {
                    jsLog('DEBUG', 'KaTeX already loaded');
                    callback();
                    return;
                }

                // Check if KaTeX CSS is already loaded
                const katexCSSLoaded = iframeDoc.querySelector('link[href*="katex"]');
                if (!katexCSSLoaded) {
                    jsLog('DEBUG', 'Loading KaTeX CSS...');
                    const cssLink = iframeDoc.createElement('link');
                    cssLink.rel = 'stylesheet';
                    cssLink.href = 'https://cdn.jsdelivr.net/npm/katex@latest/dist/katex.min.css';
                    iframeDoc.head.appendChild(cssLink);
                }

                // Check if KaTeX script is already loading
                if (iframeDoc.querySelector('script[src*="katex"]')) {
                    jsLog('DEBUG', 'KaTeX script already loading, waiting...');
                    const checkInterval = setInterval(() => {
                        if (typeof iframeWindow.katex !== 'undefined') {
                            clearInterval(checkInterval);
                            jsLog('DEBUG', 'KaTeX loaded after waiting');
                            callback();
                        }
                    }, 50);
                    setTimeout(() => clearInterval(checkInterval), 10000); // Timeout after 10 seconds
                    return;
                }

                // Load KaTeX script dynamically
                jsLog('DEBUG', 'Loading KaTeX script dynamically...');
                const script = iframeDoc.createElement('script');
                script.src = 'https://cdn.jsdelivr.net/npm/katex@latest/dist/katex.min.js';
                script.onload = () => {
                    jsLog('DEBUG', 'KaTeX script loaded successfully');
                    callback();
                };
                script.onerror = () => {
                    jsLog('ERROR', 'Failed to load KaTeX script from CDN');
                };
                iframeDoc.head.appendChild(script);
            };

            // Initialize KaTeX for math rendering
            const initKaTeX = () => {
                jsLog('DEBUG', 'initKaTeX called');
                const iframeWindow = pv.contentWindow;
                if (!iframeWindow) {
                    jsLog('DEBUG', 'iframeWindow is not available yet');
                    setTimeout(initKaTeX, 50);
                    return;
                }

                // Load KaTeX if not already loaded
                loadKaTeX(() => {
                    const katex = iframeWindow.katex;
                    if (!katex) {
                        jsLog('ERROR', 'KaTeX object not available after loading');
                        return;
                    }

                    jsLog('DEBUG', 'KaTeX is available, starting rendering');

                    // Render inline math
                    const inlineElements = iframeDoc.querySelectorAll('.katex-inline');
                    jsLog('DEBUG', `Found ${inlineElements.length} inline math elements`);
                    inlineElements.forEach(function (el) {
                        try {
                            // Skip if already rendered (has .katex child)
                            if (el.querySelector('.katex')) {
                                jsLog('DEBUG', 'Skipping already rendered inline math');
                                return;
                            }
                            // Use textContent to get raw text (avoids HTML entity issues)
                            // textContent automatically decodes HTML entities, so we don't need decodeHtmlEntities
                            const rawContent = el.textContent.trim();
                            jsLog('DEBUG', `Inline math rawContent: ${JSON.stringify(rawContent)}`);
                            jsLog('DEBUG', `Inline math rawContent bytes: ${Array.from(rawContent).map(c => c.charCodeAt(0).toString(16)).join(' ')}`);
                            // textContent already decodes HTML entities, so use it directly
                            const math = rawContent;
                            jsLog('DEBUG', `Inline math final: ${JSON.stringify(math)}`);
                            katex.render(math, el, {
                                throwOnError: false,
                                displayMode: false
                            });
                            jsLog('DEBUG', 'Inline math rendered successfully');
                        } catch (e) {
                            jsLog('ERROR', 'KaTeX inline rendering error:', e);
                            console.error('KaTeX inline rendering error:', e);
                        }
                    });

                    // Render block math
                    const blockElements = iframeDoc.querySelectorAll('.katex-block');
                    jsLog('DEBUG', `Found ${blockElements.length} block math elements`);
                    blockElements.forEach(function (el) {
                        try {
                            // Skip if already rendered (has .katex child)
                            if (el.querySelector('.katex')) {
                                jsLog('DEBUG', 'Skipping already rendered block math');
                                return;
                            }
                            // Use textContent to get raw text (avoids HTML entity issues)
                            // textContent automatically decodes HTML entities, so we don't need decodeHtmlEntities
                            const rawContent = el.textContent.trim();
                            jsLog('DEBUG', `Block math rawContent: ${JSON.stringify(rawContent)}`);
                            jsLog('DEBUG', `Block math rawContent bytes: ${Array.from(rawContent.substring(0, 50)).map(c => c.charCodeAt(0).toString(16)).join(' ')}`);
                            // textContent already decodes HTML entities, so use it directly
                            const math = rawContent;
                            jsLog('DEBUG', `Block math final: ${JSON.stringify(math)}`);
                            katex.render(math, el, {
                                throwOnError: false,
                                displayMode: true
                            });
                            jsLog('DEBUG', 'Block math rendered successfully');
                        } catch (e) {
                            jsLog('ERROR', 'KaTeX block rendering error:', e);
                            console.error('KaTeX block rendering error:', e);
                        }
                    });
                });
            };

            // Wait for KaTeX to be loaded
            jsLog('DEBUG', 'Calling initKaTeX from setupTimestampClickHandlers');
            initKaTeX();
        } catch (err) {
            // Cross-origin or other error
            jsLog('ERROR', 'Could not access iframe document:', err);
            console.warn('Could not access iframe document:', err);
        }
    };

    pv._timestampLoadHandler = handleTimestampLoad;
    pv.addEventListener('load', handleTimestampLoad);
}

// Update audio player based on content
async function updateAudioPlayer(content) {
    jsLog('DEBUG', 'updateAudioPlayer: called with content length:', content ? content.length : 0);

    if (!audioPlayerContainer || !audioPlayer) {
        jsLog('ERROR', 'updateAudioPlayer: audioPlayerContainer or audioPlayer not found', {
            audioPlayerContainer: !!audioPlayerContainer,
            audioPlayer: !!audioPlayer
        });
        return;
    }

    try {
        const audioPath = extractAudioPath(content);
        jsLog('DEBUG', 'updateAudioPlayer: extracted audioPath:', audioPath);

        if (audioPath) {
            jsLog('INFO', 'updateAudioPlayer: calling GetAudioFileURL with:', audioPath);
            // Get audio file URL (HTTP path served by Wails)
            const audioURL = await api.GetAudioFileURL(audioPath);
            jsLog('INFO', 'updateAudioPlayer: got audioURL:', audioURL);

            // Update audio player source
            if (audioPlayer.src !== audioURL) {
                jsLog('INFO', 'updateAudioPlayer: updating audio src from', audioPlayer.src, 'to', audioURL);
                audioPlayer.src = audioURL;
                // Reset player state
                audioPlayer.load();
            } else {
                jsLog('DEBUG', 'updateAudioPlayer: audio src already set to', audioURL);
            }

            // Show audio player
            jsLog('INFO', 'updateAudioPlayer: showing audio player container');
            audioPlayerContainer.style.display = 'flex';
            jsLog('INFO', 'updateAudioPlayer: audio player shown, display:', audioPlayerContainer.style.display);
        } else {
            jsLog('DEBUG', 'updateAudioPlayer: no audio_path found, hiding player');
            // Hide audio player if no audio_path
            audioPlayerContainer.style.display = 'none';
            // Clear audio source
            audioPlayer.src = '';
        }
    } catch (error) {
        jsLog('ERROR', 'Failed to update audio player:', error, 'Error stack:', error.stack);
        // Hide audio player on error
        audioPlayerContainer.style.display = 'none';
        // Show error message in player
        if (audioPlayer) {
            audioPlayer.src = '';
        }
    }
}

function getCaretLineNumber(text, caretIndex) {
    if (caretIndex <= 0) return 0;
    const upToCaret = text.slice(0, Math.min(caretIndex, text.length));
    return upToCaret.split('\n').length - 1;
}

function splitFrontMatter(content) {
    if (!content.startsWith('---')) {
        return { body: content, offsetLines: 0 };
    }
    const frontMatterRegex = /^---\s*\n([\s\S]*?)\n---\s*\n?/;
    const match = content.match(frontMatterRegex);
    if (!match) {
        return { body: content, offsetLines: 0 };
    }
    const fmText = match[0];
    const offsetLines = fmText.split('\n').length;
    const body = content.slice(fmText.length);
    return { body, offsetLines };
}

// Extract audio_path from markdown frontmatter
function extractAudioPath(content) {
    jsLog('DEBUG', 'extractAudioPath: called with content length:', content ? content.length : 0);

    if (!content || !content.startsWith('---')) {
        jsLog('DEBUG', 'extractAudioPath: content does not start with ---');
        return null;
    }

    const frontMatterRegex = /^---\s*\n([\s\S]*?)\n---\s*\n?/;
    const match = content.match(frontMatterRegex);
    if (!match) {
        jsLog('DEBUG', 'extractAudioPath: no frontmatter found');
        return null;
    }

    const yamlContent = match[1];
    jsLog('DEBUG', 'extractAudioPath: yamlContent:', yamlContent);

    // Try to extract audio_path field
    // Support both "audio_path: value" and "audio_path: 'value'" and "audio_path: \"value\"" formats
    // Match: audio_path: "value" or audio_path: 'value' or audio_path: value
    // First try the simple regex that matches any audio_path line
    const simpleRegex = /^audio_path:\s*(.+?)\s*$/m;
    const simpleMatch = yamlContent.match(simpleRegex);
    jsLog('DEBUG', 'extractAudioPath: simpleMatch:', simpleMatch);

    if (simpleMatch && simpleMatch[1]) {
        let path = simpleMatch[1].trim();
        jsLog('DEBUG', 'extractAudioPath: extracted path (before quote removal):', path);
        // Remove quotes if present
        if ((path.startsWith('"') && path.endsWith('"')) || (path.startsWith("'") && path.endsWith("'"))) {
            path = path.slice(1, -1);
            jsLog('DEBUG', 'extractAudioPath: removed quotes, path:', path);
        }
        if (path) {
            jsLog('INFO', 'extractAudioPath: found audioPath (simple regex):', path);
            return path;
        }
    }

    // Fallback: try more specific regex
    const audioPathRegex = /^audio_path:\s*(?:"([^"]*)"|'([^']*)'|([^\n\r]+?))\s*$/m;
    const audioPathMatch = yamlContent.match(audioPathRegex);
    jsLog('DEBUG', 'extractAudioPath: audioPathMatch:', audioPathMatch);

    if (audioPathMatch) {
        // Return the first non-empty capture group (double-quoted, single-quoted, or unquoted)
        const audioPath = (audioPathMatch[1] || audioPathMatch[2] || audioPathMatch[3] || '').trim();
        if (audioPath) {
            jsLog('INFO', 'extractAudioPath: found audioPath (specific regex):', audioPath);
            return audioPath;
        }
    }

    jsLog('DEBUG', 'extractAudioPath: no audio_path found in frontmatter');
    return null;
}

function computeMarpSlideInfo(content, caretLine) {
    const { body, offsetLines } = splitFrontMatter(content);
    const lines = body.split('\n');

    const slideRanges = [];
    let currentStart = offsetLines;

    for (let i = 0; i < lines.length; i++) {
        const absoluteLine = offsetLines + i;
        const trimmed = lines[i].trim();
        if (trimmed === '---') {
            slideRanges.push({
                start: currentStart,
                end: Math.max(currentStart, absoluteLine - 1)
            });
            currentStart = absoluteLine + 1;
        }
    }

    // Add final slide
    const finalEnd = offsetLines + Math.max(lines.length - 1, 0);
    slideRanges.push({
        start: currentStart,
        end: Math.max(currentStart, finalEnd)
    });

    const totalSlides = Math.max(slideRanges.length, 1);

    if (caretLine < offsetLines) {
        return { index: 0, total: totalSlides };
    }

    for (let i = 0; i < slideRanges.length; i++) {
        const { start, end } = slideRanges[i];
        if (caretLine >= start && (caretLine <= end || i === slideRanges.length - 1)) {
            return { index: i, total: totalSlides };
        }
    }

    return { index: totalSlides - 1, total: totalSlides };
}

// Helper function to escape HTML
function escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}

// Setup event listeners
function setupEventListeners() {
    // Save button
    saveBtn.onclick = save;

    // New file button
    if (newBtn) {
        newBtn.onclick = createNewFile;
    }

    // Open external preview button
    if (openBtn) {
        openBtn.onclick = openExternalPreview;
    }
    if (exportPdfBtn) {
        exportPdfBtn.onclick = exportPdf;
    }

    // Keyboard shortcuts
    document.addEventListener('keydown', (e) => {
        if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') {
            e.preventDefault();
            save();
        }
        if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'n') {
            e.preventDefault();
            createNewFile();
        }
    });

    // Theme selector
    themeSel.addEventListener('change', (e) => {
        const theme = e.target.value;
        document.documentElement.setAttribute('data-theme', theme);
        localStorage.setItem('karte-theme', theme);
        // Re-render preview with updated theme variables
        updatePreview();
    });

    // Hard wrap checkbox
    hardwrapChk.addEventListener('change', () => {
        updatePreview();
    });

    // Search input
    inp.addEventListener('input', () => {
        renderFileList();
    });

    // Custom CSS initial load
    api.GetCustomCSS()
        .then((css) => { customCssCache = css || ''; updateCustomCssStatus(); updatePreview(); })
        .catch((e) => console.warn('GetCustomCSS failed', e));

    // Custom CSS modal events
    if (customCssBtn) customCssBtn.onclick = showCustomCssModal;
    if (saveCustomCssBtn) saveCustomCssBtn.onclick = async () => {
        await saveCustomCss(customCssTextarea.value || '');
        hideCustomCssModal();
        updatePreview();
    };
    if (clearCustomCssBtn) clearCustomCssBtn.onclick = async () => {
        await clearCustomCss();
        hideCustomCssModal();
        updatePreview();
    };
    if (cancelCustomCssBtn) cancelCustomCssBtn.onclick = hideCustomCssModal;
    if (customCssModal) customCssModal.onclick = (e) => { if (e.target === customCssModal) hideCustomCssModal(); };

    // Modal event listeners
    if (createFileBtn) {
        createFileBtn.onclick = handleFileCreation;
    }

    if (cancelFileBtn) {
        cancelFileBtn.onclick = hideFilenameModal;
    }

    // Close modal when clicking outside
    if (filenameModal) {
        filenameModal.onclick = (e) => {
            if (e.target === filenameModal) {
                hideFilenameModal();
            }
        };
    }

    // Handle Enter key in filename input
    if (filenameInput) {
        filenameInput.addEventListener('keydown', (e) => {
            if (e.key === 'Enter') {
                handleFileCreation();
            } else if (e.key === 'Escape') {
                hideFilenameModal();
            }
        });
    }

    // Rename file modal event listeners
    if (confirmRenameBtn) {
        confirmRenameBtn.onclick = handleFileRename;
    }

    if (cancelRenameBtn) {
        cancelRenameBtn.onclick = hideRenameFileModal;
    }

    // Close rename modal when clicking outside
    if (renameFileModal) {
        renameFileModal.onclick = (e) => {
            if (e.target === renameFileModal) {
                hideRenameFileModal();
            }
        };
    }

    if (renameFileInput) {
        renameFileInput.addEventListener('keydown', (e) => {
            if (e.key === 'Enter') {
                handleFileRename();
            } else if (e.key === 'Escape') {
                hideRenameFileModal();
            }
        });
    }

    // Editor input with debouncing
    let inputTimeout = null;
    ta.addEventListener('input', () => {
        clearTimeout(inputTimeout);
        inputTimeout = setTimeout(() => {
            updatePreview();
        }, 300);
        setDirtyState(currentPath && ta.value !== lastSavedContent);
    });

    // Setup editor drop handler for images
    if (ta) {
        ta.addEventListener('dragover', (e) => {
            // Check if dragging an image from gallery by checking dataTransfer types
            const types = Array.from(e.dataTransfer.types || []);
            if (types.includes('application/json') || types.includes('text/plain')) {
                // Check if it's an image drag by looking at the source element
                const dragSource = document.querySelector('.image-thumbnail[style*="opacity: 0.5"]');
                if (dragSource) {
                    e.preventDefault();
                    e.dataTransfer.dropEffect = 'copy';
                }
            }
        });

        ta.addEventListener('drop', async (e) => {
            e.preventDefault();
            const data = e.dataTransfer.getData('application/json');
            if (!data) {
                // Try text/plain as fallback
                const path = e.dataTransfer.getData('text/plain');
                if (path) {
                    // Try to get name from attribute
                    const dragSource = document.querySelector('.image-thumbnail[data-image-path="' + path + '"]');
                    const name = dragSource?.getAttribute('data-image-name') || path.split('/').pop();
                    insertImageAtCursor(path, name);
                }
                return;
            }

            try {
                const imageData = JSON.parse(data);
                if (imageData.path && imageData.name) {
                    insertImageAtCursor(imageData.path, imageData.name);
                }
            } catch (error) {
                console.error('Failed to parse image data:', error);
            }
        });
    }

    // Load saved theme
    const savedTheme = localStorage.getItem('karte-theme') || 'light';
    themeSel.value = savedTheme;
    document.documentElement.setAttribute('data-theme', savedTheme);

    // Tab switching
    tabs.forEach(tab => {
        tab.addEventListener('click', () => {
            const tabName = tab.dataset.tab;
            switchToTab(tabName);
        });
    });

    // Handle window close with custom modal (Wails OnBeforeClose)
    if (!isBrowser && !closeHandlerRegistered) {
        closeHandlerRegistered = true; // フラグを設定して重複登録を防ぐ
        EventsOn('check-unsaved-before-close', async () => {
            // 既にモーダルが表示されている場合は何もしない
            if (isModalShowing) {
                return;
            }

            if (isDirty) {
                const result = await confirmNavigationIfDirty();
                if (result) {
                    // User confirmed (saved or discarded), allow closing
                    if (AllowClose) {
                        AllowClose();
                    }
                }
                // If result is false, user cancelled, so window won't close
            } else {
                // No unsaved changes, allow closing
                if (AllowClose) {
                    AllowClose();
                }
            }
        });
    } else if (isBrowser) {
        // Browser mode: use beforeunload
        window.addEventListener('beforeunload', (e) => {
            if (isDirty) {
                e.preventDefault();
                e.returnValue = '';
            }
        });
    }

    setupDragAndDrop();
    setupRecording();
    setupImageGallery();
    setupPreviewImageDrop();
}

// Global variable to store current drag image data
let currentDragImageData = null;

// Setup preview iframe drop handler for images
function setupPreviewImageDrop() {
    if (!pv) {
        return;
    }

    // This function will be called whenever the iframe loads
    const setupDropHandlers = () => {
        try {
            const iframeDoc = pv.contentDocument || pv.contentWindow?.document;
            if (!iframeDoc) {
                return;
            }

            // Remove existing listeners to avoid duplicates
            const newDoc = iframeDoc.cloneNode(true);
            // Actually, we can't clone the document, so we'll use a flag to prevent duplicates
            if (iframeDoc._imageDropHandlersSetup) {
                return;
            }
            iframeDoc._imageDropHandlersSetup = true;

            // Setup dragover to allow drop
            iframeDoc.addEventListener('dragover', (e) => {
                // Check if dragging an image from gallery by checking types
                const types = Array.from(e.dataTransfer.types || []);
                if (types.includes('application/json') || types.includes('text/plain')) {
                    // Check if it's an image drag by looking at the source element
                    const dragSource = document.querySelector('.image-thumbnail[style*="opacity: 0.5"]');
                    if (dragSource) {
                        e.preventDefault();
                        e.dataTransfer.dropEffect = 'copy';
                    }
                }
            });

            // Setup drop handler
            iframeDoc.addEventListener('drop', async (e) => {
                e.preventDefault();

                // Get image data from global variable (set during dragstart)
                let imagePath = null;
                let imageName = null;

                // Try to get from global variable first
                if (currentDragImageData) {
                    imagePath = currentDragImageData.path;
                    imageName = currentDragImageData.name;
                } else {
                    // Fallback: try to get from parent window's drag source
                    const dragSource = document.querySelector('.image-thumbnail[style*="opacity: 0.5"]');
                    if (dragSource) {
                        imagePath = dragSource.getAttribute('data-image-path');
                        imageName = dragSource.getAttribute('data-image-name');
                    }
                }

                if (!imagePath || !imageName) {
                    console.error('Failed to get image data from drag');
                    return;
                }

                // Clear the global variable
                currentDragImageData = null;

                try {
                    // Find element at drop position
                    const element = iframeDoc.elementFromPoint(e.clientX, e.clientY);
                    if (element) {
                        await insertImageAfterElement(imagePath, imageName, element);
                    } else {
                        // Fallback: append to end
                        insertImageAtCursor(imagePath, imageName);
                    }
                } catch (error) {
                    console.error('Failed to handle image drop in preview:', error);
                }
            });
        } catch (error) {
            console.error('Failed to setup preview drop handler:', error);
        }
    };

    // Setup on initial load
    pv.addEventListener('load', setupDropHandlers);

    // Also setup immediately if iframe is already loaded
    if (pv.contentDocument || pv.contentWindow?.document) {
        setupDropHandlers();
    }
}

// Setup image gallery
function setupImageGallery() {
    // Load initial gallery
    loadImageGallery();

    // Setup capture screen button
    if (captureScreenBtn) {
        // Disable in pure browser mode (no backend)
        if (isBrowser || !CaptureScreenInteractive) {
            captureScreenBtn.disabled = true;
            captureScreenBtn.title = 'スクリーンショットはデスクトップアプリでのみ利用できます';
        } else {
            captureScreenBtn.addEventListener('click', async () => {
                if (captureScreenBtn.disabled) {
                    return;
                }
                try {
                    captureScreenBtn.disabled = true;
                    setStatusMessage('スクリーンショットを取得中...', 0);
                    const path = await CaptureScreenInteractive();
                    if (path && typeof path === 'string') {
                        console.log('Screenshot saved to:', path);
                        setStatusMessage('スクリーンショットを保存しました', 3000);
                    } else {
                        setStatusMessage('スクリーンショットが保存されませんでした', 3000);
                    }
                    await loadImageGallery();
                } catch (error) {
                    console.error('Failed to capture screenshot:', error);
                    const msg = (error && (error.message || String(error))) || 'スクリーンショットに失敗しました';
                    setStatusMessage(msg, 5000);
                } finally {
                    captureScreenBtn.disabled = false;
                }
            });
        }
    }

    // Setup toggle button in top bar
    if (galleryToggle) {
        galleryToggle.addEventListener('click', toggleImageGallery);
    }

    // Setup preview modal close
    if (imagePreviewClose) {
        imagePreviewClose.addEventListener('click', closeImagePreview);
    }

    if (imagePreviewModal) {
        const overlay = imagePreviewModal.querySelector('.image-preview-overlay');
        if (overlay) {
            overlay.addEventListener('click', closeImagePreview);
        }
    }

    // Setup sidebar toggle
    if (sidebarToggle) {
        sidebarToggle.addEventListener('click', toggleSidebar);
    }

    // Restore gallery hidden state from localStorage
    const isGalleryHidden = localStorage.getItem('imageGalleryHidden') === 'true';
    if (isGalleryHidden && row) {
        row.classList.add('gallery-hidden');
        if (imageGalleryContainer) {
            imageGalleryContainer.classList.add('collapsed');
        }
        if (galleryToggle) {
            galleryToggle.title = '画像ギャラリーを表示';
        }
    } else {
        if (galleryToggle) {
            galleryToggle.title = '画像ギャラリーを非表示';
        }
    }

    // Restore sidebar hidden state from localStorage
    const isSidebarHidden = localStorage.getItem('sidebarHidden') === 'true';
    if (isSidebarHidden && layout) {
        layout.classList.add('sidebar-hidden');
        if (sidebarToggle) {
            sidebarToggle.title = 'ファイルリストを表示';
        }
    } else {
        if (sidebarToggle) {
            sidebarToggle.title = 'ファイルリストを非表示';
        }
    }
}

let imageGalleryRequestId = 0;

// Load image gallery from backend
async function loadImageGallery() {
    if (!imageGalleryGrid || !api.GetImageList) {
        return;
    }

    const requestId = ++imageGalleryRequestId;

    try {
        const images = await api.GetImageList();
        if (requestId !== imageGalleryRequestId) {
            // A newer request finished first, so ignore this result
            return;
        }
        const dedupedImages = deduplicateImages(images);
        await renderImageGallery(dedupedImages);
    } catch (error) {
        console.error('Failed to load image gallery:', error);
        jsLog('ERROR', 'Failed to load image gallery:', error);
    }
}

function deduplicateImages(images) {
    if (!Array.isArray(images)) {
        return [];
    }
    const seen = new Map();
    for (const image of images) {
        if (!image || !image.path) {
            continue;
        }
        if (!seen.has(image.path)) {
            seen.set(image.path, image);
        }
    }
    return Array.from(seen.values());
}

// Render image gallery
async function renderImageGallery(images) {
    if (!imageGalleryGrid || !imageGalleryEmpty) {
        return;
    }

    imageGalleryGrid.innerHTML = '';

    if (!images || images.length === 0) {
        imageGalleryEmpty.style.display = 'block';
        imageGalleryGrid.style.display = 'none';
        return;
    }

    imageGalleryEmpty.style.display = 'none';
    imageGalleryGrid.style.display = 'grid';

    // Use for...of loop instead of forEach to properly handle async operations
    for (const image of images) {
        try {
            const imageURL = await api.GetImageFileURL(image.path);
            const thumbnail = document.createElement('img');
            thumbnail.className = 'image-thumbnail';
            thumbnail.src = imageURL;
            thumbnail.alt = image.name;
            thumbnail.title = image.name;

            // Store image data in attributes before adding event listeners
            thumbnail.setAttribute('data-image-path', image.path);
            thumbnail.setAttribute('data-image-name', image.name);

            thumbnail.addEventListener('click', () => {
                const path = thumbnail.getAttribute('data-image-path');
                const name = thumbnail.getAttribute('data-image-name');
                showImagePreview(path, name, imageURL);
            });

            // Make thumbnail draggable
            thumbnail.draggable = true;

            // Handle drag start - read from data attributes to avoid closure issues
            thumbnail.addEventListener('dragstart', (e) => {
                const path = thumbnail.getAttribute('data-image-path');
                const name = thumbnail.getAttribute('data-image-name');

                if (!path || !name) {
                    console.error('Missing image data attributes');
                    return;
                }

                // Store in global variable for iframe drop handler
                currentDragImageData = { path: path, name: name };

                e.dataTransfer.effectAllowed = 'copy';
                e.dataTransfer.setData('text/plain', path);
                e.dataTransfer.setData('application/json', JSON.stringify({
                    path: path,
                    name: name
                }));
                // Add visual feedback
                thumbnail.style.opacity = '0.5';
            });

            thumbnail.addEventListener('dragend', () => {
                thumbnail.style.opacity = '1';
                // Clear global variable when drag ends
                currentDragImageData = null;
            });
            imageGalleryGrid.appendChild(thumbnail);
        } catch (error) {
            console.error('Failed to load image thumbnail:', image.path, error);
        }
    }
}

// Toggle image gallery visibility
function toggleImageGallery() {
    if (!row) {
        return;
    }

    const isHidden = row.classList.contains('gallery-hidden');
    if (isHidden) {
        // Show gallery
        row.classList.remove('gallery-hidden');
        if (imageGalleryContainer) {
            imageGalleryContainer.classList.remove('collapsed');
        }
        if (galleryToggle) {
            galleryToggle.title = '画像ギャラリーを非表示';
        }
    } else {
        // Hide gallery
        row.classList.add('gallery-hidden');
        if (imageGalleryContainer) {
            imageGalleryContainer.classList.add('collapsed');
        }
        if (galleryToggle) {
            galleryToggle.title = '画像ギャラリーを表示';
        }
    }

    // Save state to localStorage
    localStorage.setItem('imageGalleryHidden', !isHidden);
}

// Toggle sidebar visibility
function toggleSidebar() {
    if (!layout || !sidebarToggle) {
        return;
    }

    const isHidden = layout.classList.contains('sidebar-hidden');
    if (isHidden) {
        layout.classList.remove('sidebar-hidden');
        sidebarToggle.title = 'ファイルリストを非表示';
    } else {
        layout.classList.add('sidebar-hidden');
        sidebarToggle.title = 'ファイルリストを表示';
    }

    // Save state to localStorage
    localStorage.setItem('sidebarHidden', !isHidden);
}

// Show image preview modal
async function showImagePreview(imagePath, imageName, imageURL = null) {
    if (!imagePreviewModal || !imagePreviewImg || !imagePreviewName || !imagePreviewPath) {
        console.error('Image preview modal elements not found');
        return;
    }

    try {
        // If imageURL is not provided, get it from API
        let finalImageURL = imageURL;
        if (!finalImageURL && imagePath && api.GetImageFileURL) {
            finalImageURL = await api.GetImageFileURL(imagePath);
        }

        if (!finalImageURL) {
            console.error('Failed to get image URL for:', imagePath);
            setStatusMessage('画像のURLを取得できませんでした', 3000);
            return;
        }

        imagePreviewImg.src = finalImageURL;
        imagePreviewName.textContent = imageName || '画像';
        imagePreviewPath.textContent = imagePath || '';
        imagePreviewModal.style.display = 'flex';
        imagePreviewModal.setAttribute('aria-hidden', 'false');

        // Handle image load error
        imagePreviewImg.onerror = () => {
            console.error('Failed to load image:', finalImageURL);
            setStatusMessage('画像の読み込みに失敗しました', 3000);
            closeImagePreview();
        };

        await loadImageMetadataForPreview(imagePath);
    } catch (error) {
        console.error('Error showing image preview:', error);
        setStatusMessage('画像プレビューの表示に失敗しました', 3000);
    }
}

// Close image preview modal
function closeImagePreview() {
    if (!imagePreviewModal) {
        return;
    }

    imagePreviewModal.style.display = 'none';
    imagePreviewModal.setAttribute('aria-hidden', 'true');
    if (imagePreviewImg) {
        imagePreviewImg.src = '';
    }
    resetImageMetadataUI();
}

function updateImageMetadataStatus(message = '', level = 'info') {
    if (!imageMetadataStatus) {
        return;
    }
    imageMetadataStatus.textContent = message || '';
    imageMetadataStatus.classList.remove('success', 'error', 'warning');
    if (message && level && level !== 'info') {
        imageMetadataStatus.classList.add(level);
    }
}

function resetImageMetadataUI() {
    currentMetadataImagePath = null;
    imageMetadataDirty = false;
    imageMetadataLoading = false;
    if (imageMetadataEditor) {
        imageMetadataEditor.value = '';
        imageMetadataEditor.disabled = true;
    }
    if (imageMetadataSaveBtn) {
        imageMetadataSaveBtn.disabled = true;
    }
    updateImageMetadataStatus('');
}

async function loadImageMetadataForPreview(imagePath) {
    if (!imagePath || !api.GetImageMetadata || !imageMetadataEditor) {
        return;
    }
    currentMetadataImagePath = imagePath;
    imageMetadataLoading = true;
    imageMetadataEditor.disabled = true;
    if (imageMetadataSaveBtn) {
        imageMetadataSaveBtn.disabled = true;
    }
    updateImageMetadataStatus('メタデータを読み込み中…');
    try {
        const yamlText = await api.GetImageMetadata(imagePath);
        imageMetadataEditor.value = yamlText || '{}\n';
        imageMetadataEditor.disabled = false;
        if (imageMetadataSaveBtn) {
            imageMetadataSaveBtn.disabled = false;
        }
        imageMetadataDirty = false;
        updateImageMetadataStatus('メタデータを読み込みました', 'success');
    } catch (error) {
        console.error('Failed to load image metadata:', error);
        imageMetadataEditor.value = '{}\n';
        updateImageMetadataStatus(`読み込み失敗: ${error?.message || error}`, 'error');
    } finally {
        imageMetadataLoading = false;
    }
}

async function saveCurrentImageMetadata() {
    if (!currentMetadataImagePath || !api.SaveImageMetadata || !imageMetadataEditor) {
        return;
    }
    if (imageMetadataSaveBtn) {
        imageMetadataSaveBtn.disabled = true;
    }
    updateImageMetadataStatus('保存中…');
    try {
        await api.SaveImageMetadata(currentMetadataImagePath, imageMetadataEditor.value);
        imageMetadataDirty = false;
        updateImageMetadataStatus('保存しました', 'success');
        setStatusMessage('画像メタデータを保存しました。', 2500);
    } catch (error) {
        console.error('Failed to save image metadata:', error);
        updateImageMetadataStatus(`保存に失敗: ${error?.message || error}`, 'error');
    } finally {
        if (imageMetadataSaveBtn) {
            imageMetadataSaveBtn.disabled = false;
        }
    }
}

function handleImageImportedEvent(payload) {
    jsLog('INFO', 'Image imported event:', payload);
    // Reload gallery when new image is imported
    loadImageGallery();
}

// Insert image at cursor position in editor
function insertImageAtCursor(imagePath, imageName) {
    if (!ta) {
        return;
    }

    const cursorPos = ta.selectionStart;
    const textBefore = ta.value.substring(0, cursorPos);
    const textAfter = ta.value.substring(cursorPos);

    // Get image name without extension for alt text
    const nameWithoutExt = imageName.replace(/\.[^/.]+$/, '');

    // Create Markdown image syntax: ![alt](path "title")
    const imageMarkdown = `![${nameWithoutExt}](${imagePath} "${imageName}")`;

    // Insert image markdown
    const newText = textBefore + imageMarkdown + textAfter;
    ta.value = newText;

    // Set cursor position after inserted image
    const newCursorPos = cursorPos + imageMarkdown.length;
    ta.setSelectionRange(newCursorPos, newCursorPos);
    ta.focus();

    // Update preview
    updatePreview();

    // Trigger input event to save
    ta.dispatchEvent(new Event('input', { bubbles: true }));
}

// Find Markdown position from preview element
function findMarkdownPositionFromElement(element, markdownContent) {
    if (!element || !markdownContent) {
        return -1;
    }

    // Walk up the DOM tree to find a meaningful element
    let currentElement = element;
    let attempts = 0;
    const maxAttempts = 10;

    while (currentElement && attempts < maxAttempts) {
        // Try to find by heading level first (most reliable)
        if (currentElement.tagName && currentElement.tagName.match(/^H[1-6]$/)) {
            const level = parseInt(currentElement.tagName.charAt(1));
            const headingText = currentElement.textContent?.trim();
            if (headingText) {
                // Escape special regex characters
                const escapedText = headingText.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
                const headingPattern = new RegExp(`^#{${level}}\\s+${escapedText}`, 'm');
                const match = markdownContent.match(headingPattern);
                if (match) {
                    // Find the end of the heading line
                    const headingEnd = markdownContent.indexOf('\n', match.index + match[0].length);
                    return headingEnd !== -1 ? headingEnd : markdownContent.length;
                }
            }
        }

        // Try to find by paragraph text
        if (currentElement.tagName === 'P' || currentElement.tagName === 'DIV') {
            const elementText = currentElement.textContent?.trim();
            if (elementText && elementText.length > 0) {
                // Use first meaningful part of text (avoid very long text)
                const searchText = elementText.substring(0, Math.min(100, elementText.length));
                // Escape special regex characters for search
                const escapedSearch = searchText.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
                const regex = new RegExp(escapedSearch.replace(/\s+/g, '\\s+'), 'm');
                const match = markdownContent.match(regex);
                if (match) {
                    // Find the end of the line or paragraph
                    const lineEnd = markdownContent.indexOf('\n', match.index);
                    return lineEnd !== -1 ? lineEnd : markdownContent.length;
                }
            }
        }

        // Try to find by list item
        if (currentElement.tagName === 'LI') {
            const itemText = currentElement.textContent?.trim();
            if (itemText) {
                // Look for list marker followed by text
                const escapedText = itemText.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
                const listPattern = new RegExp(`^[\\s\\-*+\\d\\.]+\\s+${escapedText.substring(0, 50)}`, 'm');
                const match = markdownContent.match(listPattern);
                if (match) {
                    const lineEnd = markdownContent.indexOf('\n', match.index);
                    return lineEnd !== -1 ? lineEnd : markdownContent.length;
                }
            }
        }

        // Move to parent element
        currentElement = currentElement.parentElement;
        attempts++;
    }

    // Last resort: append to end
    return markdownContent.length;
}

// Insert image after element in preview
async function insertImageAfterElement(imagePath, imageName, element) {
    if (!ta || !element) {
        return;
    }

    const markdownContent = ta.value;
    const position = findMarkdownPositionFromElement(element, markdownContent);

    if (position === -1) {
        // Fallback: append to end
        const nameWithoutExt = imageName.replace(/\.[^/.]+$/, '');
        const imageMarkdown = `\n\n![${nameWithoutExt}](${imagePath} "${imageName}")\n`;
        ta.value = markdownContent + imageMarkdown;
        ta.setSelectionRange(ta.value.length, ta.value.length);
    } else {
        const textBefore = markdownContent.substring(0, position);
        const textAfter = markdownContent.substring(position);

        const nameWithoutExt = imageName.replace(/\.[^/.]+$/, '');
        const imageMarkdown = `\n\n![${nameWithoutExt}](${imagePath} "${imageName}")\n`;

        const newText = textBefore + imageMarkdown + textAfter;
        ta.value = newText;

        const newCursorPos = position + imageMarkdown.length;
        ta.setSelectionRange(newCursorPos, newCursorPos);
    }

    ta.focus();
    updatePreview();
    ta.dispatchEvent(new Event('input', { bubbles: true }));
}

// Setup recording functionality
function setupRecording() {
    if (!recordingBtn) {
        return;
    }

    let isRecording = false;
    let recordingPreviewTimeout = null;

    function scheduleRecordingPreviewUpdate() {
        if (recordingPreviewTimeout) {
            clearTimeout(recordingPreviewTimeout);
        }
        recordingPreviewTimeout = setTimeout(() => {
            updatePreview().catch((error) => {
                console.error('Failed to update preview for recording changes:', error);
            });
        }, 150);
    }

    // Update recording button state
    async function updateRecordingState() {
        try {
            if (!isBrowser && api.IsRecording) {
                isRecording = await api.IsRecording();
                recordingBtn.classList.toggle('recording', isRecording);
                const label = recordingBtn.querySelector('.recording-label');
                if (label) {
                    label.textContent = isRecording ? '停止' : '録音';
                }
            }
        } catch (error) {
            console.error('Failed to check recording state:', error);
        }
    }

    // Recording button click handler
    recordingBtn.addEventListener('click', async () => {
        try {
            if (isRecording) {
                // Stop recording
                setStatusMessage('録音を停止中...', 0);
                const audioPath = await api.StopRecording();
                setStatusMessage('録音を停止しました', 3000);
                isRecording = false;
                recordingBtn.classList.remove('recording');
                const label = recordingBtn.querySelector('.recording-label');
                if (label) {
                    label.textContent = '録音';
                }
                // Reload file list to show new recording
                await loadFileList();
            } else {
                // Start recording
                setStatusMessage('録音を開始中...', 0);
                await api.StartRecording();
                setStatusMessage('録音中...', 0);
                isRecording = true;
                recordingBtn.classList.add('recording');
                const label = recordingBtn.querySelector('.recording-label');
                if (label) {
                    label.textContent = '停止';
                }
            }
        } catch (error) {
            console.error('Recording error:', error);
            setStatusMessage(`録音エラー: ${error?.message || error}`, 5000);
            isRecording = false;
            recordingBtn.classList.remove('recording');
            const label = recordingBtn.querySelector('.recording-label');
            if (label) {
                label.textContent = '録音';
            }
        }
    });

    // Listen to recording events
    if (!isBrowser) {
        const recordingIndicator = document.getElementById('recordingIndicator');
        const micLevelFill = document.getElementById('micLevelFill');
        const realtimeTranscript = document.getElementById('realtimeTranscript');
        const realtimeTranscriptContent = document.getElementById('realtimeTranscriptContent');
        let transcriptSegments = [];

        EventsOn('recording-error', (payload) => {
            if (payload && payload.error) {
                setStatusMessage(payload.error, 10000);
                if (payload.details) {
                    console.error('Recording error details:', payload.details);
                }
            }
        });

        EventsOn('recording-started', async (payload) => {
            // Switch to editor tab when recording starts
            switchToTab('editor');
            isRecording = true;
            recordingBtn.classList.add('recording');
            const label = recordingBtn.querySelector('.recording-label');
            if (label) {
                label.textContent = '停止';
            }
            if (recordingIndicator) {
                recordingIndicator.style.display = 'flex';
            }
            // Overlay is now hidden - transcription is written directly to editor
            // if (realtimeTranscript) {
            //     realtimeTranscript.style.display = 'block';
            //     transcriptSegments = [];
            //     realtimeTranscriptContent.textContent = '';
            // }
            setStatusMessage('録音中...', 0);

            recordingTranscriptPath = payload && payload.transcriptPath ? payload.transcriptPath : null;
            if (recordingTranscriptPath) {
                try {
                    await loadFile(recordingTranscriptPath);
                    setStatusMessage('録音用ファイルを開きました', 2000);
                } catch (error) {
                    console.error('Failed to open recording transcript file:', error);
                    setStatusMessage('録音用ファイルの読み込みに失敗しました', 5000);
                }
            }
        });

        EventsOn('recording-stopped', async (payload) => {
            isRecording = false;
            recordingBtn.classList.remove('recording');
            const label = recordingBtn.querySelector('.recording-label');
            if (label) {
                label.textContent = '録音';
            }
            if (recordingIndicator) {
                recordingIndicator.style.display = 'none';
            }
            if (micLevelFill) {
                micLevelFill.style.width = '0%';
            }
            recordingTranscriptPath = payload && payload.transcriptPath ? payload.transcriptPath : recordingTranscriptPath;

            if (recordingTranscriptPath) {
                setStatusMessage('録音と文字起こしが完了しました', 3000);
                loadFileList();
                try {
                    await loadFile(recordingTranscriptPath);
                    switchToTab('editor');
                    setStatusMessage('録音ファイルを開きました', 3000);
                } catch (error) {
                    console.error('Failed to open transcript file:', error);
                    setStatusMessage('録音ファイルの読み込みに失敗しました', 3000);
                }
            } else {
                setStatusMessage('録音を停止しました', 3000);
            }
            // Overlay is now hidden - transcription is written directly to editor
            // if (realtimeTranscript) {
            //     setTimeout(() => {
            //         realtimeTranscript.style.display = 'none';
            //     }, 5000);
            // }
            // Reset transcript tracking after stop
            recordingTranscriptPath = null;
        });

        EventsOn('recording-input-level', (payload) => {
            if (payload && micLevelFill) {
                // RMS level is typically 0-0.1 for normal speech
                // Scale to 0-100% for display (multiply by 2000 for better sensitivity)
                const rawLevel = payload.level || 0;
                const level = Math.min(100, Math.sqrt(rawLevel) * 200); // Use sqrt for better visual response
                micLevelFill.style.width = `${level}%`;

                // Color based on level (green -> yellow -> red)
                if (level < 20) {
                    micLevelFill.style.backgroundColor = '#4caf50'; // Green
                } else if (level < 60) {
                    micLevelFill.style.backgroundColor = '#ff9800'; // Orange
                } else {
                    micLevelFill.style.backgroundColor = '#f44336'; // Red
                }
            }
        });

        EventsOn('recording-transcript-partial', (payload) => {
            if (payload && payload.text) {
                // Show partial text in status bar
                setStatusMessage(`録音中: ${payload.text}`, 0);
                if (recordingTranscriptPath && currentPath === recordingTranscriptPath) {
                    updateEditorWithPartialText(payload.text);
                }
            }
        });

        EventsOn('recording-transcript-final', (payload) => {
            if (payload && payload.text) {
                // Show final transcript in status bar
                setStatusMessage(`確定: ${payload.text}`, 2000);
                if (recordingTranscriptPath && currentPath === recordingTranscriptPath) {
                    updateEditorWithFinalText(payload.text, payload.timestamp || 0, payload.segmentIndex || 0);
                }
            }
        });

        function updateEditorWithPartialText(text) {
            if (!ta || !recordingTranscriptPath || currentPath !== recordingTranscriptPath) {
                return;
            }

            const content = ta.value;
            const partialMarkerStart = '<!-- ASR_PARTIAL -->';
            const partialMarkerEnd = '<!-- /ASR_PARTIAL -->';

            // Find ## Transcript section
            const transcriptHeaderRegex = /^##\s+Transcript\s*$/m;
            const match = content.match(transcriptHeaderRegex);
            if (!match) {
                const newContent = content + '\n\n## Transcript\n\n' + partialMarkerStart + text + partialMarkerEnd + '\n';
                const cursorPos = ta.selectionStart;
                ta.value = newContent;
                scheduleRecordingPreviewUpdate();
                if (cursorPos <= content.length) {
                    ta.setSelectionRange(cursorPos, cursorPos);
                }
                return;
            }

            const headerIndex = match.index;
            const afterHeader = content.substring(headerIndex + match[0].length);

            const partialStartIndex = afterHeader.indexOf(partialMarkerStart);
            const partialEndIndex = afterHeader.indexOf(partialMarkerEnd);

            let newContent;
            const cursorPos = ta.selectionStart;

            if (partialStartIndex !== -1 && partialEndIndex !== -1 && partialEndIndex > partialStartIndex) {
                const beforePartial = content.substring(0, headerIndex + match[0].length + partialStartIndex + partialMarkerStart.length);
                const afterPartial = content.substring(headerIndex + match[0].length + partialEndIndex);
                newContent = beforePartial + text + partialMarkerEnd + afterPartial;
            } else {
                const nextHeaderMatch = afterHeader.match(/^##\s+/m);
                const sectionEnd = nextHeaderMatch ? nextHeaderMatch.index : afterHeader.length;
                const sectionContent = afterHeader.substring(0, sectionEnd);
                const trimmedSection = sectionContent.trimEnd();
                const newSectionContent = trimmedSection + (trimmedSection ? '\n\n' : '') + partialMarkerStart + text + partialMarkerEnd + '\n';
                newContent = content.substring(0, headerIndex + match[0].length) + '\n' + newSectionContent + afterHeader.substring(sectionEnd);
            }

            ta.value = newContent;
            scheduleRecordingPreviewUpdate();

            const transcriptStart = headerIndex;
            const transcriptEnd = newContent.indexOf('\n## ', transcriptStart + 1);
            const actualTranscriptEnd = transcriptEnd !== -1 ? transcriptEnd : newContent.length;
            if (cursorPos < transcriptStart || cursorPos > actualTranscriptEnd) {
                ta.setSelectionRange(cursorPos, cursorPos);
            }
        }

        function updateEditorWithFinalText(text, timestamp, segmentIndex) {
            if (!ta || !recordingTranscriptPath || currentPath !== recordingTranscriptPath) {
                return;
            }

            const content = ta.value;
            const partialMarkerStart = '<!-- ASR_PARTIAL -->';
            const partialMarkerEnd = '<!-- /ASR_PARTIAL -->';

            const minutes = Math.floor(timestamp / 60);
            const seconds = Math.floor(timestamp % 60);
            const timestampStr = `**${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')}**`;
            const finalLine = timestampStr + ' ' + text;

            const transcriptHeaderRegex = /^##\s+Transcript\s*$/m;
            const match = content.match(transcriptHeaderRegex);
            if (!match) {
                const newContent = content + '\n\n## Transcript\n\n' + finalLine + '\n';
                const cursorPos = ta.selectionStart;
                ta.value = newContent;
                scheduleRecordingPreviewUpdate();
                if (cursorPos <= content.length) {
                    ta.setSelectionRange(cursorPos, cursorPos);
                }
                return;
            }

            const headerIndex = match.index;
            const afterHeader = content.substring(headerIndex + match[0].length);

            const partialStartIndex = afterHeader.indexOf(partialMarkerStart);
            const partialEndIndex = afterHeader.indexOf(partialMarkerEnd);

            let newContent;
            const cursorPos = ta.selectionStart;

            if (partialStartIndex !== -1 && partialEndIndex !== -1 && partialEndIndex > partialStartIndex) {
                const beforePartial = content.substring(0, headerIndex + match[0].length + partialStartIndex);
                const afterPartial = content.substring(headerIndex + match[0].length + partialEndIndex + partialMarkerEnd.length);
                newContent = beforePartial + finalLine + '\n' + afterPartial;
            } else {
                const nextHeaderMatch = afterHeader.match(/^##\s+/m);
                const sectionEnd = nextHeaderMatch ? nextHeaderMatch.index : afterHeader.length;
                const sectionContent = afterHeader.substring(0, sectionEnd);
                const trimmedSection = sectionContent.trimEnd();
                const newSectionContent = trimmedSection + (trimmedSection ? '\n\n' : '') + finalLine + '\n';
                newContent = content.substring(0, headerIndex + match[0].length) + '\n' + newSectionContent + afterHeader.substring(sectionEnd);
            }

            const conflictRegex = new RegExp(`^\\*\\*${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')}\\*\\*\\s+.*$`, 'm');
            const existingMatch = newContent.match(conflictRegex);
            if (existingMatch && existingMatch[0] !== finalLine) {
                const conflictLine = existingMatch[0];
                const conflictIndex = existingMatch.index;
                const beforeConflict = newContent.substring(0, conflictIndex + conflictLine.length);
                const afterConflict = newContent.substring(conflictIndex + conflictLine.length);
                newContent = beforeConflict + '\n' + finalLine + afterConflict;
            }

            ta.value = newContent;
            scheduleRecordingPreviewUpdate();

            const transcriptStart = headerIndex;
            const transcriptEnd = newContent.indexOf('\n## ', transcriptStart + 1);
            const actualTranscriptEnd = transcriptEnd !== -1 ? transcriptEnd : newContent.length;
            if (cursorPos < transcriptStart || cursorPos > actualTranscriptEnd) {
                ta.setSelectionRange(cursorPos, cursorPos);
            }
        }
    }

    // Initial state check
    updateRecordingState();
}

function setupDragAndDrop() {
    if (!dropOverlay || isBrowser) {
        return;
    }

    let dragDepth = 0;

    const showOverlay = () => dropOverlay.classList.add('visible');
    const hideOverlay = () => {
        dragDepth = 0;
        dropOverlay.classList.remove('visible');
    };

    const isFileDrag = (event) => {
        if (!event || !event.dataTransfer) {
            return false;
        }
        const types = Array.from(event.dataTransfer.types || []);
        return types.includes('Files');
    };

    window.addEventListener('dragenter', (event) => {
        if (!isFileDrag(event)) {
            return;
        }
        event.preventDefault();
        dragDepth += 1;
        showOverlay();
    });

    window.addEventListener('dragover', (event) => {
        if (!isFileDrag(event)) {
            return;
        }
        event.preventDefault();
    });

    window.addEventListener('dragleave', (event) => {
        if (!isFileDrag(event)) {
            return;
        }
        dragDepth = Math.max(0, dragDepth - 1);
        if (dragDepth === 0) {
            hideOverlay();
        }
    });

    window.addEventListener('drop', (event) => {
        if (!isFileDrag(event)) {
            return;
        }
        event.preventDefault();
        hideOverlay();
        const files = Array.from(event.dataTransfer?.files || []);
        if (files.length === 0) {
            return;
        }

        const hasAudio = files.some((file) => isSupportedAudioFile(file.name));
        const hasImage = files.some((file) => isSupportedImageFile(file.name));
        const hasPdf = files.some((file) => isSupportedPdfFile(file.name));

        const tasks = [];
        if (hasAudio) {
            tasks.push(handleAudioDrop(files));
        }
        if (hasImage) {
            tasks.push(handleImageDrop(files));
        }
        if (hasPdf) {
            tasks.push(handlePdfDrop(files));
        }

        if (tasks.length === 0) {
            setStatusMessage('対応していないファイル形式です', 3000);
            return;
        }

        Promise.all(tasks).catch((error) => {
            console.error('handleDrop failed', error);
        });
    });
}

function isSupportedAudioFile(name = '') {
    const lower = (name || '').toLowerCase();
    return supportedAudioExt.some((ext) => lower.endsWith(ext));
}

function isSupportedImageFile(name = '') {
    const lower = (name || '').toLowerCase();
    return supportedImageExt.some((ext) => lower.endsWith(ext));
}

function isSupportedPdfFile(name = '') {
    const lower = (name || '').toLowerCase();
    return lower.endsWith('.pdf');
}

async function handleAudioDrop(fileList) {
    if (!fileList || fileList.length === 0) {
        return;
    }
    if (!api.ImportAudioFile) {
        console.warn('ImportAudioFile API is unavailable in this environment');
        return;
    }

    const files = Array.from(fileList).filter((file) => isSupportedAudioFile(file.name));
    if (files.length === 0) {
        setStatusMessage('対応していない音声形式です (wav/mp3/m4a)', 3000);
        return;
    }

    for (const file of files) {
        try {
            setStatusMessage(`音声を取り込み中: ${file.name}`);
            if (file.path) {
                await api.ImportAudioFile(file.path);
            } else if (api.ImportAudioBase64 && file.arrayBuffer) {
                const buffer = await file.arrayBuffer();
                const base64 = arrayBufferToBase64(buffer);
                await api.ImportAudioBase64(file.name || `audio-${Date.now()}.wav`, base64);
            } else {
                console.warn('Cannot access data for dropped file:', file.name);
                setStatusMessage('音声データにアクセスできませんでした', 4000);
            }
        } catch (error) {
            console.error('ImportAudioFile failed', error);
            setStatusMessage(`音声取り込みに失敗: ${error?.message || error}`, 4000);
        }
    }

    await loadFileList();
    setStatusMessage('音声ファイルを保存しました。文字起こしを開始します…', 3500);
}

async function handleImageDrop(fileList) {
    if (!fileList || fileList.length === 0) {
        return;
    }
    if (!api.ImportImageFile) {
        console.warn('ImportImageFile API is unavailable in this environment');
        return;
    }

    const files = Array.from(fileList).filter((file) => isSupportedImageFile(file.name));
    if (files.length === 0) {
        setStatusMessage('対応していない画像形式です (jpg/png/gif/webp)', 3000);
        return;
    }

    for (const file of files) {
        try {
            setStatusMessage(`画像を取り込み中: ${file.name}`);
            if (file.path) {
                await api.ImportImageFile(file.path);
            } else if (api.ImportImageBase64 && file.arrayBuffer) {
                const buffer = await file.arrayBuffer();
                const base64 = arrayBufferToBase64(buffer);
                await api.ImportImageBase64(file.name || `image-${Date.now()}.png`, base64);
            } else {
                console.warn('Cannot access data for dropped image:', file.name);
                setStatusMessage('画像データにアクセスできませんでした', 4000);
            }
        } catch (error) {
            console.error('ImportImageFile failed', error);
            setStatusMessage(`画像取り込みに失敗: ${error?.message || error}`, 4000);
        }
    }

    await loadFileList();
    setStatusMessage('画像ファイルを保存しました。', 3000);
    // Reload image gallery after import
    await loadImageGallery();
}

async function handlePdfDrop(fileList) {
    if (!fileList || fileList.length === 0) {
        return;
    }
    if (!api.ImportPdfFile) {
        console.warn('ImportPdfFile API is unavailable in this environment');
        return;
    }

    const files = Array.from(fileList).filter((file) => isSupportedPdfFile(file.name));
    if (files.length === 0) {
        setStatusMessage('対応していないファイル形式です (pdf)', 3000);
        return;
    }

    for (const file of files) {
        try {
            setStatusMessage(`PDFを取り込み中: ${file.name}`);
            if (file.path) {
                const pdfPath = await api.ImportPdfFile(file.path);
                // Load the imported PDF file
                await loadFile(pdfPath);
            } else if (api.ImportPdfBase64 && file.arrayBuffer) {
                const buffer = await file.arrayBuffer();
                const base64 = arrayBufferToBase64(buffer);
                const pdfPath = await api.ImportPdfBase64(file.name || `document-${Date.now()}.pdf`, base64);
                // Load the imported PDF file
                await loadFile(pdfPath);
            } else {
                console.warn('Cannot access data for dropped PDF:', file.name);
                setStatusMessage('PDFデータにアクセスできませんでした', 4000);
            }
        } catch (error) {
            console.error('ImportPdfFile failed', error);
            setStatusMessage(`PDF取り込みに失敗: ${error?.message || error}`, 4000);
        }
    }

    await loadFileList();
    setStatusMessage('PDFファイルを保存しました。', 3000);
}

function arrayBufferToBase64(buffer) {
    const bytes = new Uint8Array(buffer);
    let binary = '';
    const chunkSize = 0x8000;
    for (let i = 0; i < bytes.length; i += chunkSize) {
        const chunk = bytes.subarray(i, i + chunkSize);
        binary += String.fromCharCode.apply(null, chunk);
    }
    return btoa(binary);
}

function handleAudioImportedEvent(payload) {
    const label = payload?.original_name || payload?.path || 'audio';
    console.log('audio-imported', payload);
    setStatusMessage(`音声ファイルを保存しました: ${label}`, 3000);
}

function handleAudioTranscribedEvent(payload) {
    if (!payload) {
        return;
    }
    if (payload.error) {
        console.warn('audio-transcribed error', payload.error);
        setStatusMessage(`文字起こしに失敗: ${payload.error}`, 5000);
        hideTranscriptionProgress();
        return;
    }
    const transcriptPath = payload.transcriptPath;
    console.log('audio-transcribed', payload);
    setStatusMessage('文字起こしが完了しました', 3000);
    hideTranscriptionProgress();
    loadFileList();
    if (transcriptPath) {
        loadFile(transcriptPath);
        switchToTab('editor');
    }
}

// Update ASR status display
async function updateASRStatus() {
    if (!asrStatusEl || !asrStatusIndicator || !asrStatusText) {
        return null;
    }

    try {
        const status = await api.GetASRStatus();
        if (!status) {
            return null;
        }

        // Update indicator
        asrStatusIndicator.className = 'asr-status-indicator';
        if (status.initializing) {
            asrStatusIndicator.classList.add('initializing');
            asrStatusText.textContent = 'ASR: 初期化中...';
        } else if (status.initialized) {
            asrStatusIndicator.classList.add('ready');
            asrStatusText.textContent = 'ASR: 準備完了';
        } else {
            asrStatusIndicator.classList.add('disabled');
            asrStatusText.textContent = 'ASR: 無効';
        }

        return status;
    } catch (error) {
        console.error('Failed to get ASR status:', error);
        if (asrStatusIndicator && asrStatusText) {
            asrStatusIndicator.className = 'asr-status-indicator disabled';
            asrStatusText.textContent = 'ASR: エラー';
        }
        return null;
    }
}

// Handle transcription progress
let currentTranscriptionAudioPath = null;
let currentTranscriptionTotalSegments = 0;

function handleAudioTranscribeProgress(payload) {
    if (!payload) {
        return;
    }

    const audioPath = payload.audioPath || payload.AudioPath;
    const text = payload.text || payload.Text || '';
    const segmentIndex = payload.segmentIndex || payload.SegmentIndex || 0;
    const totalSegments = payload.totalSegments || payload.TotalSegments || 0;

    // Start progress if this is a new transcription
    if (audioPath !== currentTranscriptionAudioPath) {
        currentTranscriptionAudioPath = audioPath;
        currentTranscriptionTotalSegments = totalSegments;
        showTranscriptionProgress(audioPath, totalSegments);
    }

    // Update progress bar
    if (transcriptionProgressFill && totalSegments > 0) {
        const progress = Math.min(100, (segmentIndex / totalSegments) * 100);
        transcriptionProgressFill.style.width = `${progress}%`;
        transcriptionProgressFill.classList.remove('indeterminate');
    } else if (transcriptionProgressFill && totalSegments === 0) {
        // Fallback to indeterminate if total segments unknown
        transcriptionProgressFill.classList.add('indeterminate');
    }

    // Update progress text
    if (transcriptionProgressText) {
        const fileName = audioPath ? audioPath.split('/').pop() : '音声ファイル';
        if (totalSegments > 0) {
            transcriptionProgressText.textContent = `文字起こし中: ${fileName} (${segmentIndex}/${totalSegments}区間処理済み)`;
        } else {
            transcriptionProgressText.textContent = `文字起こし中: ${fileName} (${segmentIndex}区間処理済み)`;
        }
    }
}

function showTranscriptionProgress(audioPath, totalSegments) {
    if (!transcriptionProgressEl || !transcriptionProgressFill) {
        return;
    }

    transcriptionProgressEl.style.display = 'flex';

    // Reset progress bar
    if (totalSegments > 0) {
        transcriptionProgressFill.style.width = '0%';
        transcriptionProgressFill.classList.remove('indeterminate');
    } else {
        transcriptionProgressFill.classList.add('indeterminate');
    }

    const fileName = audioPath ? audioPath.split('/').pop() : '音声ファイル';
    if (transcriptionProgressText) {
        if (totalSegments > 0) {
            transcriptionProgressText.textContent = `文字起こし中: ${fileName} (0/${totalSegments}区間処理済み)`;
        } else {
            transcriptionProgressText.textContent = `文字起こし中: ${fileName} (0区間処理済み)`;
        }
    }
}

function hideTranscriptionProgress() {
    if (!transcriptionProgressEl || !transcriptionProgressFill) {
        return;
    }

    transcriptionProgressEl.style.display = 'none';
    transcriptionProgressFill.classList.remove('indeterminate');
    transcriptionProgressFill.style.width = '0%';
    currentTranscriptionAudioPath = null;
    currentTranscriptionTotalSegments = 0;
}

// Handle PDF export progress
let currentPDFExportTotalImages = 0;

function handlePDFExportProgress(payload) {
    if (!payload) {
        return;
    }

    const currentImage = payload.currentImage || payload.CurrentImage || 0;
    const totalImages = payload.totalImages || payload.TotalImages || 0;
    const htmlSize = payload.htmlSize || payload.HtmlSize || 0;
    const stage = payload.stage || payload.Stage || '';

    // Start progress if this is a new export
    if (totalImages !== currentPDFExportTotalImages) {
        currentPDFExportTotalImages = totalImages;
        showPDFExportProgress(totalImages, htmlSize);
    }

    // Update progress bar
    if (pdfExportProgressFill && totalImages > 0) {
        const progress = Math.min(100, (currentImage / totalImages) * 100);
        pdfExportProgressFill.style.width = `${progress}%`;
        pdfExportProgressFill.classList.remove('indeterminate');
    } else if (pdfExportProgressFill && totalImages === 0) {
        // Fallback to indeterminate if total images unknown
        pdfExportProgressFill.classList.add('indeterminate');
    }

    // Update progress text
    if (pdfExportProgressText) {
        let stageText = '';
        if (stage === 'converting-images') {
            stageText = '画像を変換中';
        } else if (stage === 'loading-webview') {
            stageText = 'WebViewを読み込み中';
        } else if (stage === 'generating-pdf') {
            stageText = 'PDFを生成中';
        } else {
            stageText = '処理中';
        }

        if (totalImages > 0) {
            pdfExportProgressText.textContent = `PDF出力中: ${stageText} (${currentImage}/${totalImages}枚の画像を処理済み)`;
        } else {
            pdfExportProgressText.textContent = `PDF出力中: ${stageText}`;
        }
    }
}

function showPDFExportProgress(totalImages, htmlSize) {
    if (!pdfExportProgressEl || !pdfExportProgressFill) {
        return;
    }

    pdfExportProgressEl.style.display = 'flex';

    // Reset progress bar
    if (totalImages > 0) {
        pdfExportProgressFill.style.width = '0%';
        pdfExportProgressFill.classList.remove('indeterminate');
    } else {
        pdfExportProgressFill.classList.add('indeterminate');
    }

    if (pdfExportProgressText) {
        if (totalImages > 0) {
            pdfExportProgressText.textContent = `PDF出力中: 画像を変換中 (0/${totalImages}枚の画像を処理済み)`;
        } else {
            pdfExportProgressText.textContent = 'PDF出力中: 処理中...';
        }
    }
}

function hidePDFExportProgress() {
    if (!pdfExportProgressEl || !pdfExportProgressFill) {
        return;
    }

    pdfExportProgressEl.style.display = 'none';
    pdfExportProgressFill.classList.remove('indeterminate');
    pdfExportProgressFill.style.width = '0%';
    currentPDFExportTotalImages = 0;
}

function setStatusMessage(message, durationMs = 0) {
    if (!statusEl) {
        return;
    }
    statusEl.textContent = message || '';
    if (statusClearTimer) {
        clearTimeout(statusClearTimer);
        statusClearTimer = null;
    }
    if (durationMs > 0) {
        statusClearTimer = setTimeout(() => {
            statusEl.textContent = '';
            statusClearTimer = null;
        }, durationMs);
    }
}
// Open external preview in system browser
async function openExternalPreview() {
    try {
        const content = ta.value || '';
        const html = await api.PreviewMarkdown(content);
        const url = 'data:text/html;charset=utf-8,' + encodeURIComponent(html);
        if (!isBrowser) {
            BrowserOpenURL(url);
        } else {
            window.open(url, '_blank', 'noopener');
        }
    } catch (error) {
        console.error('Failed to open external preview:', error);
        statusEl.textContent = 'Failed to open external preview';
    }
}

// ---------- Custom CSS & Themed Preview helpers ----------
function loadCustomCss() { return customCssCache || ''; }
async function saveCustomCss(css) {
    await api.SetCustomCSS(css);
    customCssCache = css;
    updateCustomCssStatus();
}
async function clearCustomCss() {
    await api.ClearCustomCSS();
    customCssCache = '';
    updateCustomCssStatus();
}

function updateCustomCssStatus() {
    if (!customCssStatus) return;
    const active = !!loadCustomCss();
    customCssStatus.textContent = active ? 'Custom CSS active' : '';
}

function showCustomCssModal() {
    if (!customCssModal) return;
    customCssTextarea.value = loadCustomCss();
    customCssModal.style.display = 'flex';
    customCssTextarea.focus();
}

function hideCustomCssModal() {
    if (!customCssModal) return;
    customCssModal.style.display = 'none';
}

function getThemeVariablesCSS() {
    const varNames = [
        '--main-background', '--text-color', '--browsername-color', '--backgroundcolor', '--backgroundcolor-unhover',
        '--opened-tab-backgroundcolor', '--border-color', '--border-color-unhover', '--borderline-color', '--shadow-color',
        '--shadow-color-unhover', '--input-color-unhover', '--loading-color', '--closebutton-color'
    ];
    const cs = getComputedStyle(document.documentElement);
    const lines = varNames.map(v => {
        const val = cs.getPropertyValue(v).trim();
        return val ? `${v}: ${val};` : '';
    }).filter(Boolean);
    return `:root{${lines.join('')}}`;
}

function getBasePreviewCSS() {
    return `
      body{margin:16px; background: var(--main-background); color: var(--text-color);}
      a{color: var(--loading-color);}
      pre,code{font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace}
      pre{background: var(--backgroundcolor-unhover); padding:12px; border-radius:8px; overflow:auto}
      h1,h2,h3{margin-top:1.2em;}
      table{border-collapse:collapse}
      th,td{border:1px solid var(--border-color); padding:6px 10px}
    `;
}

// Inject custom CSS and theme variables into a complete HTML document
function injectCustomCSS(html) {
    const custom = loadCustomCss();
    const theme = (themeSel && themeSel.value) || localStorage.getItem('karte-theme') || 'light';
    const themeVars = getThemeVariablesCSS();
    const baseCSS = getBasePreviewCSS();

    console.log('injectCustomCSS: custom CSS length:', custom ? custom.length : 0);
    console.log('injectCustomCSS: HTML has </head>:', html.includes('</head>'));
    console.log('injectCustomCSS: HTML has <head>:', html.includes('<head>'));
    console.log('injectCustomCSS: HTML has <html>:', html.includes('<html'));

    // Build CSS to inject
    let cssToInject = themeVars + baseCSS;
    if (custom) {
        cssToInject += '\n' + custom;
    }

    console.log('injectCustomCSS: CSS to inject length:', cssToInject.length);

    // Check if there's already a custom CSS style tag
    const customStyleRegex = /<style[^>]*id="karte-custom-css"[^>]*>[\s\S]*?<\/style>/i;
    if (customStyleRegex.test(html)) {
        // Replace existing custom CSS
        html = html.replace(customStyleRegex, `<style id="karte-custom-css">${cssToInject}</style>`);
        console.log('injectCustomCSS: Replaced existing custom CSS style tag');
    } else {
        // Try different insertion strategies based on HTML structure
        if (html.includes('</head>')) {
            // Standard HTML with </head> tag
            html = html.replace('</head>', `<style id="karte-custom-css">${cssToInject}</style></head>`);
            console.log('injectCustomCSS: Inserted CSS before </head>');
        } else if (html.includes('<head>')) {
            // Has <head> but no </head>
            html = html.replace('<head>', `<head><style id="karte-custom-css">${cssToInject}</style>`);
            console.log('injectCustomCSS: Inserted CSS after <head>');
        } else if (html.includes('<html')) {
            // Has <html> tag but no <head>
            html = html.replace(/<html([^>]*)>/i, (match, attrs) => {
                // Remove existing data-theme if present
                attrs = attrs.replace(/\s*data-theme="[^"]*"/i, '');
                return `<html${attrs} data-theme="${theme}"><head><style id="karte-custom-css">${cssToInject}</style></head>`;
            });
            console.log('injectCustomCSS: Added head section with CSS after <html>');
        } else if (html.includes('<!doctype html>')) {
            // preview.html template structure: <!doctype html> followed by meta tags
            // Insert after the first <style> tag or before <body>
            if (html.includes('</style>')) {
                // Find the last </style> tag and insert after it
                const lastStyleEnd = html.lastIndexOf('</style>');
                if (lastStyleEnd !== -1) {
                    html = html.slice(0, lastStyleEnd + 8) + `\n<style id="karte-custom-css">${cssToInject}</style>` + html.slice(lastStyleEnd + 8);
                    console.log('injectCustomCSS: Inserted CSS after last </style> tag');
                }
            } else if (html.includes('<body>')) {
                // Insert before <body>
                html = html.replace('<body>', `<style id="karte-custom-css">${cssToInject}</style>\n<body>`);
                console.log('injectCustomCSS: Inserted CSS before <body>');
            } else {
                // Fallback: insert after <!doctype html>
                html = html.replace('<!doctype html>', `<!doctype html>\n<style id="karte-custom-css">${cssToInject}</style>`);
                console.log('injectCustomCSS: Inserted CSS after <!doctype html>');
            }
        } else {
            // Fallback: try to add at the beginning
            html = `<style id="karte-custom-css">${cssToInject}</style>\n` + html;
            console.log('injectCustomCSS: Added CSS at the beginning');
        }
    }

    // Update data-theme attribute if <html> tag exists
    if (html.includes('<html')) {
        html = html.replace(/<html([^>]*)>/i, (match, attrs) => {
            // Remove existing data-theme if present
            attrs = attrs.replace(/\s*data-theme="[^"]*"/i, '');
            return `<html${attrs} data-theme="${theme}">`;
        });
    }

    console.log('injectCustomCSS: Final HTML has karte-custom-css:', html.includes('karte-custom-css'));

    return html;
}

function composePreviewHtml(innerHtml, themeOverride = null) {
    const custom = loadCustomCss();
    // Use themeOverride if provided, otherwise use selected theme value or persisted theme
    const theme = themeOverride || (themeSel && themeSel.value) || localStorage.getItem('karte-theme') || 'light';
    if (custom) {
        return `<!doctype html><html data-theme="${theme}"><head><meta charset="utf-8"><style>${custom}</style></head><body>${innerHtml}</body></html>`;
    }
    const themeVars = getThemeVariablesCSS();
    const baseCSS = getBasePreviewCSS();
    return `<!doctype html><html data-theme="${theme}"><head><meta charset="utf-8"><style>${themeVars}${baseCSS}</style></head><body>${innerHtml}</body></html>`;
}

// Initialize custom CSS status on load
updateCustomCssStatus();

// Export preview to PDF (Wails: save HTML and open in browser, Browser: print dialog)
async function exportPdf() {
    try {
        if (exportPdfBtn) {
            exportPdfBtn.disabled = true;
        }
        statusEl.textContent = 'Exporting PDF...';
        const content = ta.value || '';

        // Check if this is a Marp presentation (same logic as updatePreview)
        let isMarp = false;
        if (content.startsWith('---')) {
            const fmEnd = content.indexOf('\n---\n');
            if (fmEnd > 0) {
                const yamlContent = content.substring(4, fmEnd);
                const marpMatch = yamlContent.match(/^marp:\s*(true|false)\s*$/m);
                if (marpMatch && marpMatch[1] === 'true') {
                    isMarp = true;
                }
                if (!isMarp) {
                    const hasHeader = yamlContent.match(/^header:\s*["']?/m);
                    const hasFooter = yamlContent.match(/^footer:\s*["']?/m);
                    const hasPaginate = yamlContent.match(/^paginate:\s*(true|false)\s*$/m);
                    if (hasHeader || hasFooter || hasPaginate) {
                        isMarp = true;
                    }
                }
            }
        }

        let html = await api.PreviewMarkdown(content);
        console.log('exportPdf: isMarp =', isMarp);
        console.log('exportPdf: HTML length =', html.length);
        console.log('exportPdf: HTML preview (first 500 chars):', html.substring(0, 500));

        // For regular markdown, inject custom CSS (same as preview)
        if (!isMarp) {
            console.log('exportPdf: Injecting custom CSS for regular markdown');
            html = injectCustomCSS(html);
        } else {
            console.log('exportPdf: Skipping custom CSS injection for Marp');
        }

        if (!isBrowser) {
            // 非同期でPDFエクスポートを開始
            showPDFExportProgress(0, html.length);
            try {
                await api.ExportPDF(html);
                // イベントで完了を待つ（exportPdfCompletedハンドラーで処理）
            } catch (error) {
                hidePDFExportProgress();
                throw error;
            }
        } else {
            // In browser, open print dialog
            const win = window.open('about:blank', '_blank', 'noopener');
            if (!win) throw new Error('Popup blocked');
            win.document.open();
            win.document.write(html);
            win.document.close();
            setTimeout(() => {
                win.focus();
                win.print();
            }, 100);
        }
    } catch (error) {
        console.error('Export PDF failed:', error);
        hidePDFExportProgress();
        const msg = (error && (error.message || String(error))) || 'Export PDF failed';
        statusEl.textContent = msg;
        alert(msg);
    }
    finally {
        // ボタンの有効化はイベントハンドラーで行う（非同期完了時）
        // エラー時のみここで有効化
        if (exportPdfBtn && !exportPdfBtn.disabled) {
            // 既に有効化されている場合は何もしない
        }
    }
}

// Export preview to HTML file
async function exportHtml() {
    try {
        statusEl.textContent = 'Exporting HTML...';
        const content = ta.value || '';

        // Check if this is a Marp presentation (same logic as updatePreview)
        let isMarp = false;
        if (content.startsWith('---')) {
            const fmEnd = content.indexOf('\n---\n');
            if (fmEnd > 0) {
                const yamlContent = content.substring(4, fmEnd);
                const marpMatch = yamlContent.match(/^marp:\s*(true|false)\s*$/m);
                if (marpMatch && marpMatch[1] === 'true') {
                    isMarp = true;
                }
                if (!isMarp) {
                    const hasHeader = yamlContent.match(/^header:\s*["']?/m);
                    const hasFooter = yamlContent.match(/^footer:\s*["']?/m);
                    const hasPaginate = yamlContent.match(/^paginate:\s*(true|false)\s*$/m);
                    if (hasHeader || hasFooter || hasPaginate) {
                        isMarp = true;
                    }
                }
            }
        }

        let html = await api.PreviewMarkdown(content);
        console.log('exportHtml: isMarp =', isMarp);
        console.log('exportHtml: HTML length =', html.length);
        console.log('exportHtml: HTML preview (first 500 chars):', html.substring(0, 500));

        // For regular markdown, inject custom CSS (same as preview)
        if (!isMarp) {
            console.log('exportHtml: Injecting custom CSS for regular markdown');
            html = injectCustomCSS(html);
        } else {
            console.log('exportHtml: Skipping custom CSS injection for Marp');
        }

        if (!isBrowser) {
            const fileUrl = await api.ExportPreviewHTML(html);
            statusEl.textContent = 'HTML exported: ' + fileUrl;
            BrowserOpenURL(fileUrl);
        } else {
            // In browser, download the HTML file
            const blob = new Blob([html], { type: 'text/html' });
            const url = URL.createObjectURL(blob);
            const a = document.createElement('a');
            a.href = url;
            a.download = `preview-${new Date().toISOString().replace(/[:.]/g, '-').slice(0, -5)}.html`;
            document.body.appendChild(a);
            a.click();
            document.body.removeChild(a);
            URL.revokeObjectURL(url);
            statusEl.textContent = 'HTML downloaded';
        }
    } catch (error) {
        console.error('Export HTML failed:', error);
        const msg = (error && (error.message || String(error))) || 'Export HTML failed';
        statusEl.textContent = msg;
        alert(msg);
    }
}

// Switch between tabs
function switchToTab(tabName) {
    // Update tab buttons
    tabs.forEach(tab => {
        tab.classList.toggle('active', tab.dataset.tab === tabName);
    });

    // Update tab contents
    tabContents.forEach(content => {
        content.classList.toggle('active', content.id === `${tabName}-tab`);
    });

    // If switching to graph tab, update graph and resize SVG
    if (tabName === 'graph') {
        console.log('Switched to graph tab, updating graph...');

        // Update graph data
        if (graphModule) {
            // 少し遅延させてコンテナサイズを正しく取得（flexboxレイアウトの計算を待つ）
            setTimeout(() => {
                updateGraph().then(() => {
                    // シミュレーションサイズを更新（SVGはCSSで自動的に追従）
                    if (graphModule && typeof graphModule.updateSimulationSize === 'function') {
                        graphModule.updateSimulationSize();

                        // Restart simulation to apply changes
                        if (graphModule.simulation) {
                            graphModule.simulation.alpha(0.3).restart();
                        }
                    }
                });
            }, 100);
        }
    }
}

// Update graph data
async function updateGraph() {
    if (!graphModule) return;

    try {
        console.log('Updating graph data...');
        const graphData = await api.GetGraphData();
        console.log('Graph data received:', graphData);

        if (graphData) {
            console.log('Graph data details:');
            console.log('- Nodes count:', graphData.nodes?.length || 0);
            console.log('- Edges count:', graphData.edges?.length || 0);
            console.log('- Sample nodes:', graphData.nodes?.slice(0, 3));
            console.log('- Sample edges:', graphData.edges?.slice(0, 3));

            graphModule.setData(graphData);

            // Set focus on current file if available
            if (currentPath) {
                const focusId = currentPath.startsWith('content/') ?
                    `doc:/${currentPath.replace('content/', '')}` :
                    `doc:/${currentPath}`;
                console.log('Setting focus on:', focusId);
                graphModule.setFocus({ roots: [focusId], depth: 3 });
            }
        } else {
            console.warn('No graph data received');
        }
    } catch (error) {
        console.error('Failed to update graph:', error);
    }
}

// Show conflict resolution modal
function showConflictResolutionModal(conflictInfo) {
    currentConflictInfo = conflictInfo;

    // Extract path from conflict info
    const path = conflictInfo.path || conflictInfo.Path || '';
    const localPath = path.startsWith('content/') ? path.replace('content/', '') : path;
    conflictFilePath.textContent = localPath;

    // Display diff content
    diffLocal.textContent = conflictInfo.local_content || conflictInfo.LocalContent || '';
    diffRemote.textContent = conflictInfo.remote_content || conflictInfo.RemoteContent || '';

    // Show modal
    conflictModal.style.display = 'flex';
}

// Hide conflict resolution modal
function hideConflictResolutionModal() {
    conflictModal.style.display = 'none';
    currentConflictInfo = null;
}

// Handle conflict resolution
async function resolveConflict(strategy) {
    if (!currentConflictInfo) {
        console.error('No conflict info available');
        return;
    }

    const path = currentConflictInfo.path || currentConflictInfo.Path || '';
    const localPath = path.startsWith('content/') ? path.replace('content/', '') : path;

    try {
        statusEl.textContent = 'コンフリクトを解決中...';
        await api.ResolveConflict(localPath, strategy);
        statusEl.textContent = 'コンフリクトを解決しました';
        hideConflictResolutionModal();

        // Reload file to show resolved content
        if (currentPath === localPath) {
            await loadFile(currentPath);
        }

        setTimeout(() => {
            statusEl.textContent = '';
        }, 3000);
    } catch (error) {
        console.error('Failed to resolve conflict:', error);
        statusEl.textContent = `コンフリクト解決に失敗しました: ${error.message || error}`;
    }
}

// Setup conflict resolution button handlers
if (resolveConflictBtn && cancelConflictBtn) {
    resolveConflictBtn.addEventListener('click', () => {
        const selected = document.querySelector('input[name="conflictResolution"]:checked');
        if (selected) {
            resolveConflict(selected.value);
        }
    });

    cancelConflictBtn.addEventListener('click', () => {
        hideConflictResolutionModal();
    });
}

// Update link to latest version (called from HTML onclick)
async function updateLinkToLatest(button) {
    const sourceDocID = button.getAttribute('data-source-doc-id');
    const targetDocID = button.getAttribute('data-target-doc-id');

    if (!sourceDocID || !targetDocID) {
        console.error('Missing doc IDs for link update');
        return;
    }

    try {
        await api.UpdateLinkToLatest(sourceDocID, targetDocID);
        // Preview will be updated automatically via link-updated event
        setStatusMessage('リンクを最新版に更新しました', 2000);
    } catch (error) {
        console.error('Failed to update link to latest:', error);
        setStatusMessage('リンクの更新に失敗しました: ' + error.message, 5000);
    }
}

// Make updateLinkToLatest available globally for onclick handlers
window.updateLinkToLatest = updateLinkToLatest;

// Setup updateLinkToLatest handler in preview iframe
function setupUpdateLinkToLatestHandler() {
    if (!pv) {
        return;
    }

    try {
        const iframeWindow = pv.contentWindow;
        const iframeDoc = pv.contentDocument || iframeWindow?.document;
        if (!iframeWindow || !iframeDoc) {
            // Retry after a short delay
            setTimeout(() => setupUpdateLinkToLatestHandler(), 100);
            return;
        }

        // Make updateLinkToLatest available in iframe context
        iframeWindow.updateLinkToLatest = updateLinkToLatest;

        // Also setup event listeners for buttons (as fallback if onclick doesn't work)
        const updateButtons = iframeDoc.querySelectorAll('.update-to-latest-btn');
        updateButtons.forEach(button => {
            // Remove existing listeners to avoid duplicates
            const newButton = button.cloneNode(true);
            button.parentNode.replaceChild(newButton, button);

            newButton.addEventListener('click', (e) => {
                e.preventDefault();
                updateLinkToLatest(newButton);
            });
        });
    } catch (error) {
        console.error('Failed to setup updateLinkToLatest handler in iframe:', error);
    }
}

// Initialize when DOM is loaded
if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
} else {
    init();
}
