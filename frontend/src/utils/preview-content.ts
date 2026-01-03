import type { WailsAppAPI } from '../types/wails-api';
import { parseCsvContent } from '../logic';

const importArgsRegex = /([a-zA-Z_][a-zA-Z0-9_]*)\s*=\s*"([^"]*)"/g;
const importCsvLineRegex = /^\s*@import\((.*?)\)\s*$/;
const importCsvSimpleRegex = /^\s*@import\s+([^\s]+\.csv)\s*$/;

function parseImportArgs(line: string): Record<string, string> {
    const args: Record<string, string> = {};
    const matches = line.matchAll(importArgsRegex);
    for (const match of matches) {
        args[match[1]] = match[2];
    }
    return args;
}

function escapeHtml(value: string): string {
    return value
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;')
        .replace(/'/g, '&#39;');
}

function buildCsvHtmlTableFromText(csvText: string): string {
    const { headers, rows } = parseCsvContent(csvText);
    if (headers.length === 0 && rows.length === 0) {
        return '';
    }
    const columnHeaders = headers.length > 0
        ? headers
        : (rows[0] || []).map((_, index) => `Column ${index + 1}`);
    const headerCells = columnHeaders.map((cell) => `<th>${escapeHtml(String(cell))}</th>`).join('');
    const bodyRows = rows.map((row) => {
        const cells = columnHeaders.map((_, index) => {
            const cell = row[index] ?? '';
            return `<td>${escapeHtml(String(cell))}</td>`;
        }).join('');
        return `<tr>${cells}</tr>`;
    }).join('');
    return `<table><thead><tr>${headerCells}</tr></thead><tbody>${bodyRows}</tbody></table>`;
}

async function resolveCsvImportToHtml(path: string, api: WailsAppAPI): Promise<string> {
    try {
        if (path.startsWith('content/')) {
            const csvText = await api.LoadFile(path);
            const table = buildCsvHtmlTableFromText(csvText);
            return table || `@import(type="csv", path="${path}")`;
        }
        const data = await api.GetCsvFile(path);
        const headers = data[0] || [];
        const rows = data.slice(1);
        const headerCells = headers.map((cell) => `<th>${escapeHtml(String(cell ?? ''))}</th>`).join('');
        const bodyRows = rows.map((row) => {
            const cells = headers.map((_, index) => {
                const cell = row?.[index] ?? '';
                return `<td>${escapeHtml(String(cell))}</td>`;
            }).join('');
            return `<tr>${cells}</tr>`;
        }).join('');
        const table = `<table><thead><tr>${headerCells}</tr></thead><tbody>${bodyRows}</tbody></table>`;
        return table || `@import(type="csv", path="${path}")`;
    } catch (error) {
        console.error('Failed to resolve CSV import:', error);
        return `@import(type="csv", path="${path}")`;
    }
}

function pushHtmlBlock(output: string[], html: string): void {
    output.push('');
    output.push(html);
    output.push('');
}

export async function prepareMarkdownForPreview(content: string, api: WailsAppAPI): Promise<string> {
    if (!content) {
        return '';
    }

    const lines = content.split(/\r?\n/);
    const output: string[] = [];

    for (const line of lines) {
        const importMatch = line.match(importCsvLineRegex);
        if (importMatch) {
            const args = parseImportArgs(importMatch[1]);
            if (args.type === 'csv' && args.path) {
                const table = await resolveCsvImportToHtml(args.path, api);
                pushHtmlBlock(output, table);
                continue;
            }
            output.push(line);
            continue;
        }

        const simpleMatch = line.match(importCsvSimpleRegex);
        if (simpleMatch && simpleMatch[1]) {
            const table = await resolveCsvImportToHtml(simpleMatch[1], api);
            pushHtmlBlock(output, table);
            continue;
        }

        output.push(line);
    }

    return output.join('\n');
}
