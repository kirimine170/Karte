import { GetFileList, LoadFile, SaveFile, PreviewMarkdown, GetGraphData, CreateNewFile, ExportPDF, ExportPreviewHTML, GetCustomCSS, SetCustomCSS, ClearCustomCSS, ResolveConflict, ImportAudioFile, ImportAudioBase64, GetASRStatus, GetAudioFileURL, StartRecording, StopRecording, IsRecording, LogJS } from '../wailsjs/wailsjs/go/main/App';
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

    async GetASRStatus() {
        console.log('Mock GetASRStatus called');
        return { initialized: false, initializing: false };
    },

    async GetAudioFileURL(audioPath) {
        console.log('Mock GetAudioFileURL called:', audioPath);
        return `/audio/${audioPath}`;
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
    GetASRStatus,
    GetAudioFileURL,
    StartRecording,
    StopRecording,
    IsRecording,
    LogJS
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

// Modal elements
const filenameModal = document.getElementById('filenameModal');
const filenameInput = document.getElementById('filenameInput');
const createFileBtn = document.getElementById('createFileBtn');
const cancelFileBtn = document.getElementById('cancelFileBtn');

// Custom CSS elements
const customCssBtn = document.getElementById('customCssBtn');
const customCssModal = document.getElementById('customCssModal');
const customCssTextarea = document.getElementById('customCssTextarea');
const saveCustomCssBtn = document.getElementById('saveCustomCssBtn');
const clearCustomCssBtn = document.getElementById('clearCustomCssBtn');
const cancelCustomCssBtn = document.getElementById('cancelCustomCssBtn');
const customCssStatus = document.getElementById('customCssStatus');

const supportedAudioExt = ['.wav', '.mp3', '.m4a'];

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

// Audio player elements
const audioPlayerContainer = document.getElementById('audioPlayerContainer');
const audioPlayer = document.getElementById('audioPlayer');

let customCssCache = '';
let currentConflictInfo = null;
let asrStatusCheckInterval = null;

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
        a.textContent = (file.title || 'Untitled') + '  —  ' + file.path.replace(/^content\//, '');
        a.href = '#';
        a.onclick = (e) => {
            e.preventDefault();
            console.log('File clicked:', file);
            console.log('File path:', file.path);
            loadFile(file.path);
        };
        frag.appendChild(a);
    }
    tree.appendChild(frag);
}

// Load a file
async function loadFile(path) {
    console.log('loadFile called with path:', path);
    try {
        statusEl.textContent = 'Loading...';
        console.log('Calling LoadFile with path:', path);
        const content = await api.LoadFile(path);
        console.log('LoadFile returned content, length:', content.length);
        ta.value = content;
        currentPath = path;

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

        // Update preview
        await updatePreview();

        // Also update audio player directly (in case updatePreview didn't call it)
        await updateAudioPlayer(content);

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

    try {
        statusEl.textContent = 'Saving...';
        console.log('Calling SaveFile with path:', currentPath, 'content length:', ta.value.length);
        await api.SaveFile(currentPath, ta.value);
        statusEl.textContent = 'Saved';
        console.log('File saved successfully');
    } catch (error) {
        console.error('Failed to save file:', error);
        statusEl.textContent = 'Save failed: ' + error.message;
    }
}

// Update preview
async function updatePreview() {
    try {
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

            pv.onload = () => {
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
                } catch (err) {
                    console.warn('Failed to restore Marp slide position:', err);
                } finally {
                    pv.onload = null;
                }
            };

            pv.srcdoc = mdHtml;

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
        pv.srcdoc = finalHtml;

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

    // Store original onload handler if exists
    const originalOnload = pv.onload;

    // Wait for iframe to load
    pv.onload = () => {
        // Call original onload if it exists
        if (originalOnload) {
            try {
                originalOnload();
            } catch (err) {
                console.warn('Original onload handler error:', err);
            }
        }

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
        } catch (err) {
            // Cross-origin or other error
            console.warn('Could not access iframe document:', err);
        }
    };
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

    // Editor input with debouncing
    let inputTimeout = null;
    ta.addEventListener('input', () => {
        clearTimeout(inputTimeout);
        inputTimeout = setTimeout(() => {
            updatePreview();
        }, 300);
    });

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

    setupDragAndDrop();
    setupRecording();
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
        handleAudioDrop(event.dataTransfer?.files || []).catch((error) => {
            console.error('handleAudioDrop failed', error);
        });
    });
}

function isSupportedAudioFile(name = '') {
    const lower = (name || '').toLowerCase();
    return supportedAudioExt.some((ext) => lower.endsWith(ext));
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
            const pdfPath = await api.ExportPDF(html);
            statusEl.textContent = 'PDF exported: ' + pdfPath;
            BrowserOpenURL('file://' + pdfPath);
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
        const msg = (error && (error.message || String(error))) || 'Export PDF failed';
        statusEl.textContent = msg;
        alert(msg);
    }
    finally {
        if (exportPdfBtn) {
            exportPdfBtn.disabled = false;
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

// Initialize when DOM is loaded
if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
} else {
    init();
}
