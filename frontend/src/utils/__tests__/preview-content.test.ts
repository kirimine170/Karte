import { describe, expect, it, vi } from 'vitest';
import { prepareMarkdownForPreview } from '../preview-content';

const createApi = () => ({
    LoadFile: vi.fn(),
    GetCsvFile: vi.fn(),
});

describe('prepareMarkdownForPreview', () => {
    it('returns empty string for empty content', async () => {
        const api = createApi();
        const result = await prepareMarkdownForPreview('', api as any);
        expect(result).toBe('');
    });

    it('replaces CSV import line with HTML table (content path)', async () => {
        const api = createApi();
        api.LoadFile.mockResolvedValue('name,score\nAlice,10');

        const input = '@import(type="csv", path="content/sample.csv")';
        const result = await prepareMarkdownForPreview(input, api as any);

        expect(result).toContain('<table>');
        expect(result).toContain('<th>name</th>');
        expect(result).toContain('<td>Alice</td>');
        expect(api.LoadFile).toHaveBeenCalledWith('content/sample.csv');
    });

    it('replaces CSV import line with HTML table (external path)', async () => {
        const api = createApi();
        api.GetCsvFile.mockResolvedValue([['name', 'score'], ['Bob', '8']]);

        const input = '@import(type="csv", path="data/sample.csv")';
        const result = await prepareMarkdownForPreview(input, api as any);

        expect(result).toContain('<table>');
        expect(result).toContain('<td>Bob</td>');
        expect(api.GetCsvFile).toHaveBeenCalledWith('data/sample.csv');
    });

    it('keeps import tag when CSV resolution fails', async () => {
        const api = createApi();
        api.LoadFile.mockRejectedValue(new Error('load failed'));

        const input = '@import content/missing.csv';
        const result = await prepareMarkdownForPreview(input, api as any);

        expect(result).toContain('@import(type="csv", path="content/missing.csv")');
    });
});
