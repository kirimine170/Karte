import { getByRole, getByText, within } from '@testing-library/dom';
import { describe, expect, it } from 'vitest';
import {
    applyCsvImports,
    buildCsvMarkdownTable,
    buildFileDisplayLabel,
    convertMarkdownToHtml,
    filterFilesByQuery,
    parseCsvContent
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

describe('CSV import helpers', () => {
    it('parses CSV with quoted values and builds markdown table', () => {
        const csv = 'name,notes\n"Smith, Jr.","Loves commas"\nCarol,"Multi word"';
        const parsed = parseCsvContent(csv);
        expect(parsed.headers).toEqual(['name', 'notes']);
        expect(parsed.rows[0]).toEqual(['Smith, Jr.', 'Loves commas']);

        const tableMarkdown = buildCsvMarkdownTable(csv);
        expect(tableMarkdown).toContain('| name | notes |');
        expect(tableMarkdown).toContain('Smith, Jr.');
    });

    it('ignores missing loader while applying CSV imports', () => {
        const markdown = 'No replacement\n@import data/sample.csv';
        const withImports = applyCsvImports(markdown);
        expect(withImports).toBe(markdown);
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

    it('builds display label with fallback title', () => {
        expect(buildFileDisplayLabel(files[0])).toBe('Alpha  —  notes/alpha.md');
        expect(buildFileDisplayLabel(files[2])).toBe('Untitled  —  tasks/todo.md');
    });
});
