import { describe, expect, it, vi } from 'vitest';
import { prepareMarkdownForPreview } from '../preview-content';

describe('paged CSV preview', () => {
    it('requests only page one，escapes cells，and reports truncation', async () => {
        const api = {
            GetCsvPage: vi.fn().mockResolvedValue({
                path: 'data/csv/formulas.csv',
                header: ['=SUM(A1:A2)', '<img src=x onerror=alert(1)>'],
                rows: Array.from({ length: 50 }, (_, index) => index === 0
                    ? ['=HYPERLINK("https://example.invalid")', '<script>alert(1)</script>']
                    : [`row-${index}`, 'safe']),
                page: 1,
                limit: 50,
                totalRows: 1000,
                hasMore: true,
                revision: 'revision-1',
            }),
            GetCsvFile: vi.fn(),
            LoadFile: vi.fn(),
        } as any;

        const html = await prepareMarkdownForPreview(
            '@import(type="csv", path="data/csv/formulas.csv")',
            api,
        );

        expect(api.GetCsvPage).toHaveBeenCalledWith({
            path: 'data/csv/formulas.csv',
            page: 1,
            limit: 50,
        });
        expect(api.GetCsvFile).not.toHaveBeenCalled();
        expect(api.LoadFile).not.toHaveBeenCalled();
        expect(html).toContain('=HYPERLINK(&quot;https://example.invalid&quot;)');
        expect(html).toContain('&lt;script&gt;alert(1)&lt;/script&gt;');
        expect(html).not.toContain('<script>alert(1)</script>');
        expect(html).toContain('先頭50行を表示しています（全1000行）');
    });
});
