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

function escapeCsvPreviewHtml(value) {
    return String(value)
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;')
        .replace(/'/g, '&#39;');
}

function buildCsvTableHtml(parsedCsv, sourcePath) {
    const headerCells = parsedCsv.headers
        .map((cell) => `<th scope="col">${escapeCsvPreviewHtml(cell)}</th>`)
        .join('');
    const bodyRows = parsedCsv.rows
        .map((row) => `<tr>${row.map((cell) => `<td>${escapeCsvPreviewHtml(cell)}</td>`).join('')}</tr>`)
        .join('');

    return `
<figure data-testid="csv-import" aria-label="CSV preview">
    <figcaption>@import ${escapeCsvPreviewHtml(sourcePath)}</figcaption>
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

    const mediaImportSessions = new Map();
    let nextMediaImportSession = 1;

    return {
        async GetFileList() {
            return [
                { path: 'content/README.md', title: 'README', modTime: '2026-06-06T08:00:00.000Z', size: 128 },
                { path: 'content/Test.md', title: 'Test Document', modTime: '2026-06-07T08:00:00.000Z', size: 128 },
                { path: 'content/Test.board.md', title: 'Test Board', modTime: '2026-06-05T08:00:00.000Z', size: 128 }
            ];
        },

        async SearchFiles(query, page, limit) {
            const files = await this.GetFileList();
            const normalizedQuery = String(query || '').trim().toLowerCase();
            const matches = [];
            for (const file of files) {
                const content = await this.LoadFile(file.path);
                const searchText = `${file.path}\n${file.title || ''}\n${content}`.toLowerCase();
                if (!normalizedQuery || searchText.includes(normalizedQuery)) {
                    matches.push(file);
                }
            }
            const normalizedPage = Number.isInteger(page) && page > 0 ? page : 1;
            const normalizedLimit = Math.min(Number.isInteger(limit) && limit > 0 ? limit : 50, 100);
            const start = (normalizedPage - 1) * normalizedLimit;
            const items = matches.slice(start, start + normalizedLimit);
            return {
                items,
                page: normalizedPage,
                limit: normalizedLimit,
                total: matches.length,
                hasMore: start + items.length < matches.length,
            };
        },

        async SearchResources(request) {
            const query = String(request?.query || '').trim().toLowerCase();
            const kinds = Array.isArray(request?.kinds) ? request.kinds : [];
            const excluded = new Set(Array.isArray(request?.excludePaths) ? request.excludePaths : []);
            const resources = [];
            if (kinds.includes('markdown') || kinds.includes('pdf')) {
                for (const file of await this.GetFileList()) {
                    const kind = file.path.toLowerCase().endsWith('.pdf') ? 'pdf' : 'markdown';
                    if (kinds.includes(kind)) {
                        resources.push({
                            kind,
                            path: file.path,
                            title: file.title || file.path,
                            metadata: {
                                name: file.path.split('/').pop() || file.path,
                                extension: kind === 'pdf' ? '.pdf' : '.md',
                                size: file.size || 0,
                                modTime: file.modTime || '',
                            },
                        });
                    }
                }
            }
            if (kinds.includes('image')) {
                for (const image of await this.GetImageList()) {
                    resources.push({
                        kind: 'image',
                        path: image.path,
                        title: image.name || image.path,
                        metadata: { name: image.name || image.path, extension: '.webp', size: image.size || 0, modTime: image.modTime || '' },
                    });
                }
            }
            if (kinds.includes('csv')) {
                for (const csv of await this.GetCsvList()) {
                    resources.push({
                        kind: 'csv',
                        path: csv.path,
                        title: csv.name || csv.path,
                        metadata: { name: csv.name || csv.path, extension: '.csv', size: csv.size || 0, modTime: csv.modTime || '' },
                    });
                }
            }
            const matches = resources
                .filter((item) => !excluded.has(item.path))
                .filter((item) => !query || `${item.title}\n${item.path}\n${item.metadata.name}`.toLowerCase().includes(query))
                .sort((left, right) => left.path.toLowerCase().localeCompare(right.path.toLowerCase()));
            const page = Number.isInteger(request?.page) && request.page > 0 ? request.page : 1;
            const limit = Math.min(Number.isInteger(request?.limit) && request.limit > 0 ? request.limit : 50, 100);
            const start = (page - 1) * limit;
            const items = matches.slice(start, start + limit);
            return { items, query, kinds, page, limit, total: matches.length, hasMore: start + items.length < matches.length };
        },

        async LoadFile(path) {
            return `# ${path.split('/').pop()}\n\nThis is a mock file content for testing in browser.\n\n## Features\n- Mock content\n- Browser testing\n- No backend required`;
        },

        async LoadBoard(path) {
            return {
                path,
                title: 'Mock Board',
                docId: 'board:mock',
                type: 'karte-board',
                version: 1,
                created: '2026-06-06',
                updated: '2026-06-06',
                tags: ['mock'],
                cards: [
                    {
                        id: 'card:resource-001',
                        type: 'resource',
                        title: 'README',
                        source: 'content/README.md',
                        tags: [],
                        createdBy: 'user',
                        body: 'Mock board card',
                        meta: {},
                    },
                ],
                edges: [],
                layout: {
                    cards: {
                        'card:resource-001': { x: 120, y: 80, width: 300, height: 180 },
                    },
                    viewport: { x: 0, y: 0, zoom: 1 },
                },
                notes: '',
                rawContent: '# Mock board source',
            };
        },

        async SaveBoard(_path, board) {
            return {
                ...board,
                updated: '2026-06-06',
                rawContent: board.rawContent || '# Mock board source',
            };
        },

        async CreateBoardForResource(path) {
            return this.LoadBoard(path.replace(/\.(md|pdf)$/i, '.board.md'));
        },

        async GetBoardResourceCandidates() {
            return this.GetFileList();
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

        async GetCsvPage(request) {
            const records = await this.GetCsvFile(request.path);
            const header = records[0] || [];
            const allRows = records.slice(1);
            const start = (request.page - 1) * request.limit;
            const rows = allRows.slice(start, start + request.limit);
            return {
                path: request.path,
                header,
                rows,
                page: request.page,
                limit: request.limit,
                totalRows: allRows.length,
                hasMore: start + rows.length < allRows.length,
                revision: `browser-csv-${request.path}-${allRows.length}`,
            };
        },

        async GetCsvList() {
            return [
                { path: 'data/csv/mock-1.csv', name: 'mock-1.csv', size: 512, modTime: '2026-06-07T10:00:00.000Z' },
                { path: 'data/csv/mock-2.csv', name: 'mock-2.csv', size: 768, modTime: '2026-06-06T10:00:00.000Z' }
            ];
        },

        async SaveCsvFile() {
            return true;
        },

        async SaveCsvPage(request) {
            return {
                path: request.path,
                revision: `browser-csv-saved-${Date.now()}`,
                totalRows: request.rows.length,
            };
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

        async BeginMediaImport(kind, filename, declaredSize) {
            const id = `browser-media-${nextMediaImportSession++}`;
            mediaImportSessions.set(id, { kind, filename, declaredSize, offset: 0 });
            return { id, chunkSize: 256 * 1024, maxBytes: kind === 'image' ? 64 * 1024 * 1024 : 512 * 1024 * 1024 };
        },

        async AppendMediaImportChunk(sessionId, expectedOffset, encodedChunk) {
            const session = mediaImportSessions.get(sessionId);
            if (!session || session.offset !== expectedOffset) {
                throw new Error('Invalid browser media import offset');
            }
            session.offset += atob(encodedChunk).length;
            return session.offset;
        },

        async FinishMediaImport(sessionId) {
            const session = mediaImportSessions.get(sessionId);
            if (!session || session.offset !== session.declaredSize) {
                throw new Error('Incomplete browser media import');
            }
            mediaImportSessions.delete(sessionId);
            const directory = session.kind === 'audio'
                ? 'data/audio'
                : session.kind === 'image'
                    ? 'data/image'
                    : session.kind === 'csv'
                        ? 'data/csv'
                        : 'content';
            return `${directory}/mock-${Date.now()}-${session.filename}`;
        },

        async AbortMediaImport(sessionId) {
            mediaImportSessions.delete(sessionId);
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

        async ImportCsvFile(path) {
            console.log('Mock ImportCsvFile called:', path);
            return `data/csv/mock-${Date.now()}.csv`;
        },

        async ImportCsvBase64(name, data) {
            console.log('Mock ImportCsvBase64 called:', name, data.length);
            return `data/csv/mock-${Date.now()}.csv`;
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
                { path: 'data/image/mock-1.png', name: 'mock-1.png', size: 1024, modTime: '2026-06-07T09:00:00.000Z' },
                { path: 'data/image/mock-2.jpg', name: 'mock-2.jpg', size: 2048, modTime: '2026-06-05T09:00:00.000Z' }
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
            return 'data/audio/mock-recording.wav';
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
        },

        async SaveEventLogs(_logsJson) {
            return true;
        }
    };
}
