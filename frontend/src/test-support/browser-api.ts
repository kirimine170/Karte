// @ts-nocheck
const isBrowser = typeof window !== 'undefined' && !window.go;

function sanitizeCsvValue(value) {
    const trimmed = value.trim();
    if (trimmed.startsWith('"') && trimmed.endsWith('"')) {
        return trimmed.slice(1, -1);
    }
    return trimmed;
}

function parseCsvContent(csvText) {
    const rows = csvText
        .split(/\r?\n/)
        .map((line) => line.trim())
        .filter((line) => line.length > 0)
        .map((line) => line.split(',').map(sanitizeCsvValue));

    if (rows.length === 0) {
        return null;
    }

    const [headers, ...dataRows] = rows;
    return { headers, rows: dataRows };
}

function buildCsvTableHtml(parsedCsv, sourcePath) {
    const headerCells = parsedCsv.headers
        .map((cell) => `<th scope="col">${cell}</th>`)
        .join('');
    const bodyRows = parsedCsv.rows
        .map((row) => `<tr>${row.map((cell) => `<td>${cell}</td>`).join('')}</tr>`)
        .join('');

    return `
<figure data-testid="csv-import" aria-label="CSV preview">
    <figcaption>@import ${sourcePath}</figcaption>
    <table data-testid="csv-table">
        <thead><tr>${headerCells}</tr></thead>
        <tbody>${bodyRows}</tbody>
    </table>
</figure>`;
}

async function renderCsvImport(importPath) {
    const url = importPath.startsWith('http')
        ? importPath
        : `/${importPath.replace(/^\//, '')}`;
    const response = await fetch(url);
    if (!response.ok) {
        throw new Error(`Failed to load CSV (${response.status})`);
    }
    const csvText = await response.text();
    const parsed = parseCsvContent(csvText);
    if (!parsed) {
        throw new Error('CSV is empty');
    }
    return buildCsvTableHtml(parsed, importPath);
}

function basicMarkdownToHtml(content) {
    return content
        .replace(/^# (.*$)/gim, '<h1>$1</h1>')
        .replace(/^## (.*$)/gim, '<h2>$1</h2>')
        .replace(/^### (.*$)/gim, '<h3>$1</h3>')
        .replace(/\*\*(.*)\*\*/gim, '<strong>$1</strong>')
        .replace(/\*(.*)\*/gim, '<em>$1</em>')
        .replace(/\n/gim, '<br>');
}

export function createBrowserApi() {
    if (!isBrowser) {
        return null;
    }

    return {
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

        async ClipURL(request) {
            console.log('Mock ClipURL called:', request);
            return {
                markdownPath: `content/clips/mock-web-clip-${Date.now()}.md`,
                assetDir: `content/clips/assets/mock-web-clip-${Date.now()}`,
                title: 'Mock Web Clip',
                sourceUrl: request.url,
                warnings: [],
            };
        },

        async PreviewMarkdown(content) {
            const importRegex = /^@import\s+([^\s]+)\s*$/gm;
            const placeholders = [];

            const contentWithPlaceholders = content.replace(importRegex, (_match, importPath) => {
                const token = `__CSV_IMPORT_${placeholders.length}__`;
                placeholders.push({ token, importPath: importPath.trim() });
                return token;
            });

            let html = basicMarkdownToHtml(contentWithPlaceholders);

            for (const placeholder of placeholders) {
                try {
                    const tableHtml = await renderCsvImport(placeholder.importPath);
                    html = html.replace(placeholder.token, tableHtml);
                } catch (error) {
                    const message = error?.message || 'CSV import failed';
                    html = html.replace(
                        placeholder.token,
                        `<p data-testid="csv-error">Failed to import ${placeholder.importPath}: ${message}</p>`
                    );
                }
            }

            return html;
        },

        async PreviewMarkdownForPath(path, content) {
            console.log('Mock PreviewMarkdownForPath called:', path);
            return this.PreviewMarkdown(content);
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

        async GetCsvFile(path) {
            const url = path.startsWith('http')
                ? path
                : `/${path.replace(/^\//, '')}`;
            const response = await fetch(url);
            if (!response.ok) {
                throw new Error(`Failed to load CSV (${response.status})`);
            }
            const csvText = await response.text();
            const parsed = parseCsvContent(csvText);
            if (!parsed) {
                return [];
            }
            return [parsed.headers, ...parsed.rows];
        },

        async CreateNewFile(filename) {
            console.log('Mock CreateNewFile called:', filename);
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

        async GetImageSystemMetadata(path) {
            console.log('Mock GetImageSystemMetadata called:', path);
            return 'schema: karte.image.metadata.v1\nsource:\n  kind: web_clip';
        },
        async SaveImageMetadata(path, yaml) {
            console.log('Mock SaveImageMetadata called:', path, yaml);
            return true;
        },

        async SaveImageSystemMetadata(path, yaml) {
            console.log('Mock SaveImageSystemMetadata called:', path, yaml);
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
}
