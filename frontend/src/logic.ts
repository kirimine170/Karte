import { marked } from 'marked';

marked.setOptions({
    gfm: true,
    breaks: true
});

export function parseCsvLine(line: string): string[] {
    const cells: string[] = [];
    let current = '';
    let inQuotes = false;
    for (let i = 0; i < line.length; i += 1) {
        const char = line[i];
        if (inQuotes) {
            if (char === '"' && line[i + 1] === '"') {
                current += '"';
                i += 1;
            } else if (char === '"') {
                inQuotes = false;
            } else {
                current += char;
            }
        } else if (char === '"') {
            inQuotes = true;
        } else if (char === ',') {
            cells.push(current.trim());
            current = '';
        } else {
            current += char;
        }
    }
    cells.push(current.trim());
    return cells;
}

export interface ParsedCsv {
    headers: string[];
    rows: string[][];
}

export function parseCsvContent(csvText: string): ParsedCsv {
    if (typeof csvText !== 'string') {
        return { headers: [], rows: [] };
    }

    const lines = csvText
        .split(/\r?\n/)
        .map((line) => line.trim())
        .filter((line) => line.length > 0);

    if (lines.length === 0) {
        return { headers: [], rows: [] };
    }

    const parsedLines = lines.map(parseCsvLine);
    const [headerLine, ...dataLines] = parsedLines;
    return { headers: headerLine, rows: dataLines };
}

export function buildCsvMarkdownTable(csvText: string): string {
    const { headers, rows } = parseCsvContent(csvText);

    if (headers.length === 0 && rows.length === 0) {
        return '';
    }

    const columnHeaders = headers.length > 0
        ? headers
        : (rows[0] || []).map((_, index) => `Column ${index + 1}`);

    const dataRows = headers.length > 0 ? rows : rows;

    const headerRow = `| ${columnHeaders.join(' | ')} |`;
    const separatorRow = `| ${columnHeaders.map(() => '---').join(' | ')} |`;
    const bodyRows = dataRows.map((row) => `| ${columnHeaders.map((_, index) => (row[index] ?? '').trim()).join(' | ')} |`).join('\n');

    return [headerRow, separatorRow, bodyRows].filter(Boolean).join('\n');
}

export function buildCsvMarkdownTableFromData(data: string[][]): string {
    if (!Array.isArray(data) || data.length === 0) {
        return '';
    }
    const [headerRow, ...rows] = data;
    const headers = (headerRow || []).map((cell) => (cell ?? '').toString().trim());
    const columnHeaders = headers.length > 0
        ? headers
        : (rows[0] || []).map((_, index) => `Column ${index + 1}`);
    const bodyRows = rows.map((row) => {
        const cells = columnHeaders.map((_, index) => (row?.[index] ?? '').toString().trim());
        return `| ${cells.join(' | ')} |`;
    }).join('\n');
    const headerLine = `| ${columnHeaders.join(' | ')} |`;
    const separatorLine = `| ${columnHeaders.map(() => '---').join(' | ')} |`;
    return [headerLine, separatorLine, bodyRows].filter(Boolean).join('\n');
}

export type CsvLoader = (csvPath: string) => string | null | undefined;

export function applyCsvImports(markdown: string, csvLoader?: CsvLoader): string {
    if (!markdown || typeof markdown !== 'string') {
        return '';
    }

    if (!csvLoader) {
        return markdown;
    }

    return markdown.replace(/@import\s+([^\s]+\.csv)/g, (match, csvPath) => {
        try {
            const csvText = csvLoader(csvPath);
            if (!csvText) {
                return match;
            }
            const tableMarkdown = buildCsvMarkdownTable(csvText);
            return tableMarkdown || match;
        } catch (error) {
            console.error('Failed to load CSV for import:', error);
            return match;
        }
    });
}

export interface ConvertMarkdownOptions {
    csvLoader?: CsvLoader;
}

export function convertMarkdownToHtml(markdown: string, options: ConvertMarkdownOptions = {}): string {
    const prepared = applyCsvImports(markdown ?? '', options.csvLoader);
    return marked.parse(prepared) as string;
}

export interface FileItem {
    path: string;
    title?: string;
    [key: string]: unknown;
}

export function filterFilesByQuery(files: FileItem[], query: string): FileItem[] {
    if (!Array.isArray(files)) {
        return [];
    }
    const normalizedQuery = (query || '').toLowerCase();

    return files.filter((file) => {
        if (!file || typeof file !== 'object' || !file.path) {
            return false;
        }
        if (!normalizedQuery) {
            return true;
        }
        const pathMatch = file.path.toLowerCase().includes(normalizedQuery);
        const titleMatch = (file.title || '').toLowerCase().includes(normalizedQuery);
        return pathMatch || titleMatch;
    });
}

export function buildFileDisplayLabel(file: FileItem): string {
    if (!file || !file.path) {
        return '';
    }
    const title = file.title || 'Untitled';
    const relativePath = file.path.replace(/^content\//, '');
    return `${title}  —  ${relativePath}`;
}
