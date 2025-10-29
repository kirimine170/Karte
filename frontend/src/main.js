import { GetFileList, LoadFile, SaveFile, PreviewMarkdown, GetGraphData, CreateNewFile } from '../wailsjs/wailsjs/go/main/App';
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
    }
};

// Use mock functions if in browser, otherwise use real Wails functions
const api = isBrowser ? mockFunctions : {
    GetFileList,
    LoadFile,
    SaveFile,
    PreviewMarkdown,
    GetGraphData,
    CreateNewFile
};

// Global variables
let currentPath = '';
let files = [];
let graphModule = null;

// DOM elements
const statusEl = document.getElementById('status');
const ta = document.getElementById('editor');
const pv = document.getElementById('preview');
const tree = document.getElementById('tree');
const inp = document.getElementById('q');
const saveBtn = document.getElementById('saveBtn');
const openBtn = document.getElementById('openBtn');
const newBtn = document.getElementById('newBtn');
const themeSel = document.getElementById('theme');
const hardwrapChk = document.getElementById('hardwrap');
const tabs = document.querySelectorAll('.tab');
const tabContents = document.querySelectorAll('.tab-content');

// Modal elements
const filenameModal = document.getElementById('filenameModal');
const filenameInput = document.getElementById('filenameInput');
const createFileBtn = document.getElementById('createFileBtn');
const cancelFileBtn = document.getElementById('cancelFileBtn');

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
        } else {
            console.log('Running in browser mode - Wails events disabled');
        }

        // Initialize graph module
        console.log('Initializing graph module...');
        try {
            graphModule = new GraphModule('graph-container');
            graphModule.on('node:click', (data) => {
                console.log('Node clicked:', data);
                if (data.id && data.id.startsWith('doc:/')) {
                    loadFile(data.id);
                    switchToTab('editor');
                }
            });
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
        const html = await api.PreviewMarkdown(content);
        pv.srcdoc = html;
    } catch (error) {
        console.error('Failed to update preview:', error);
        pv.srcdoc = '<p>Preview failed to load</p>';
    }
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
    });

    // Hard wrap checkbox
    hardwrapChk.addEventListener('change', () => {
        updatePreview();
    });

    // Search input
    inp.addEventListener('input', () => {
        renderFileList();
    });

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
            updateGraph().then(() => {
                // Resize SVG after data is updated
                if (graphModule.svg && graphModule.container) {
                    const width = graphModule.container.clientWidth;
                    const height = graphModule.container.clientHeight;

                    console.log('Resizing SVG to:', { width, height });

                    graphModule.svg
                        .attr('width', width || 800)
                        .attr('height', height || 600);

                    // Restart simulation to apply changes
                    if (graphModule.simulation) {
                        graphModule.simulation.restart();
                    }
                }
            });
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

// Initialize when DOM is loaded
if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
} else {
    init();
}
