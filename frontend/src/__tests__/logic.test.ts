import { getByRole, getByText, within } from '@testing-library/dom';
import { describe, expect, it, vi } from 'vitest';
import {
    applyCsvImports,
    buildCsvMarkdownTable,
    buildCsvMarkdownTableFromData,
    buildFileDisplayLabel,
    convertMarkdownToHtml,
    filterFilesByQuery,
    parseCsvContent,
    parseCsvLine
} from '../logic';

describe('Markdown conversion', () => {
    it('converts headings and emphasis into HTML', () => {
        const html = convertMarkdownToHtml('# Title\n\nThis is **bold** and *italic*.');
        const container = document.createElement('div');
        container.innerHTML = html;

        expect(container.querySelector('h1')?.textContent).toBe('Title');
        expect(getByText(container, 'bold')).toBeTruthy();
        expect(getByText(container, 'italic')).toBeTruthy();
    });

    it('applies CSV imports before converting markdown', () => {
        const markdown = 'Data table:\n\n@import data/sample.csv';
        const csvLoader = () => 'name,score\nAlice,10\nBob,8';
        const html = convertMarkdownToHtml(markdown, { csvLoader });

        const container = document.createElement('div');
        container.innerHTML = html;
        const table = getByRole(container, 'table');
        const rows = within(table).getAllByRole('row');

        expect(rows.length).toBe(3);
        expect(within(rows[1]).getByText('Alice')).toBeTruthy();
        expect(within(rows[2]).getByText('8')).toBeTruthy();
    });
});

describe('CSV parsing helpers', () => {
    it('parses a CSV line with quoted commas and escaped quotes', () => {
        const parsed = parseCsvLine('"a","b, c","he said ""hi"""');
        expect(parsed).toEqual(['a', 'b, c', 'he said "hi"']);
    });

    it('parses CSV content with headers and rows', () => {
        const csv = 'name,notes\n"Smith, Jr.","Loves commas"\nCarol,"Multi word"';
        const parsed = parseCsvContent(csv);
        expect(parsed.headers).toEqual(['name', 'notes']);
        expect(parsed.rows[0]).toEqual(['Smith, Jr.', 'Loves commas']);
    });

    it('returns empty data when given non-string input', () => {
        const parsed = parseCsvContent(42 as unknown as string);
        expect(parsed).toEqual({ headers: [], rows: [] });
    });
});

describe('CSV import helpers', () => {
    it('builds markdown table from CSV text', () => {
        const csv = 'name,notes\n"Smith, Jr.","Loves commas"';
        const tableMarkdown = buildCsvMarkdownTable(csv);
        expect(tableMarkdown).toContain('| name | notes |');
        expect(tableMarkdown).toContain('Smith, Jr.');
    });

    it('builds markdown table from data arrays', () => {
        const data = [
            [],
            ['Alice', '10'],
            ['Bob', '8']
        ];
        const tableMarkdown = buildCsvMarkdownTableFromData(data);
        expect(tableMarkdown).toContain('| Column 1 | Column 2 |');
        expect(tableMarkdown).toContain('| Alice | 10 |');
    });

    it('keeps CSV import tags when loader returns empty', () => {
        const markdown = 'No replacement\n@import data/sample.csv';
        const csvLoader = vi.fn().mockReturnValue('');
        const withImports = applyCsvImports(markdown, csvLoader);
        expect(withImports).toBe(markdown);
        expect(csvLoader).toHaveBeenCalledWith('data/sample.csv');
    });
});

describe('File list helpers', () => {
    const files = [
        { path: 'content/notes/alpha.md', title: 'Alpha' },
        { path: 'content/notes/beta.md', title: 'Beta' },
        { path: 'content/tasks/todo.md' }
    ];

    it('filters files by title or path', () => {
        const filteredByTitle = filterFilesByQuery(files, 'beta');
        expect(filteredByTitle.map((f) => f.path)).toEqual(['content/notes/beta.md']);

        const filteredByPath = filterFilesByQuery(files, 'todo');
        expect(filteredByPath.map((f) => f.path)).toEqual(['content/tasks/todo.md']);
    });

    it('returns empty list when files are missing', () => {
        expect(filterFilesByQuery(null as unknown as typeof files, 'alpha')).toEqual([]);
    });

    it('builds display label with fallback title', () => {
        expect(buildFileDisplayLabel(files[0])).toBe('Alpha  —  notes/alpha.md');
        expect(buildFileDisplayLabel(files[2])).toBe('Untitled  —  tasks/todo.md');
        expect(buildFileDisplayLabel(null as unknown as (typeof files)[number])).toBe('');
    });
});
