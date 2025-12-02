import { GetFileList, LoadFile, SaveFile, PreviewMarkdown, GetGraphData, CreateNewFile, ExportPDF, ExportPreviewHTML, GetCustomCSS, SetCustomCSS, ClearCustomCSS, ResolveConflict } from '../wailsjs/wailsjs/go/main/App';
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
    ResolveConflict
};

// Global variables
let currentPath = '';
let files = [];
let graphModule = null;
let lastMarpSlideIndex = 0;

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

// Conflict modal elements
const conflictModal = document.getElementById('conflictModal');
const conflictFilePath = document.getElementById('conflictFilePath');
const diffLocal = document.getElementById('diffLocal');
const diffRemote = document.getElementById('diffRemote');
const resolveConflictBtn = document.getElementById('resolveConflictBtn');
const cancelConflictBtn = document.getElementById('cancelConflictBtn');

let customCssCache = '';
let currentConflictInfo = null;

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
            return;
        }

        // Regular markdown preview (with or without YAML frontmatter)
        // site.RenderMarkdown already returns a complete HTML document,
        // but we need to inject custom CSS and theme variables
        // Custom CSS is always applied to regular markdown, regardless of frontmatter
        const finalHtml = injectCustomCSS(mdHtml);
        pv.srcdoc = finalHtml;
    } catch (error) {
        console.error('Failed to update preview:', error);
        const errorMsg = error?.message || error?.toString() || 'Unknown error';
        pv.srcdoc = `<p style="color: red; padding: 20px;">Preview failed to load<br><small>${escapeHtml(errorMsg)}</small></p>`;
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
            const pdfUrl = await api.ExportPDF(html);
            statusEl.textContent = 'PDF exported: ' + pdfUrl;
            BrowserOpenURL(pdfUrl);//HACK Previewが開けずInvalid URLを吐く
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
