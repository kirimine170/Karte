import { describe, expect, it, vi } from 'vitest';
import type { CsvPageResult, WailsAppAPI } from '../../types/wails-api';
import {
    CSV_LEGACY_MAX_RECORDS,
    CSV_MAX_PAGE_TRANSFER_BYTES,
    CsvPageClient,
    loadCsvPage,
    saveCsvPage,
} from '../csv-page';

function csvPage(page: number, value = `page-${page}`): CsvPageResult {
    return {
        path: 'data/csv/test.csv',
        header: ['name'],
        rows: Array.from({ length: 50 }, (_, index) => [index === 0 ? value : `row-${page}-${index}`]),
        page,
        limit: 50,
        totalRows: 150,
        hasMore: page < 3,
        revision: 'revision-1',
    };
}

function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void } {
    let resolve!: (value: T) => void;
    const promise = new Promise<T>((done) => {
        resolve = done;
    });
    return { promise, resolve };
}

describe('CSV page client', () => {
    it('keeps only the newest page and ignores completion after destroy', async () => {
        const pending = new Map<number, ReturnType<typeof deferred<CsvPageResult>>>();
        const api = {
            GetCsvPage: vi.fn((request) => {
                const operation = deferred<CsvPageResult>();
                pending.set(request.page, operation);
                return operation.promise;
            }),
        } as unknown as WailsAppAPI;
        const client = new CsvPageClient(api);

        const first = client.load({ path: 'data/csv/test.csv', page: 1, limit: 50 });
        const second = client.load({ path: 'data/csv/test.csv', page: 2, limit: 50 });
        pending.get(2)?.resolve(csvPage(2));
        await expect(second).resolves.toMatchObject({ page: 2 });
        pending.get(1)?.resolve(csvPage(1));
        await expect(first).resolves.toBeNull();

        const third = client.load({ path: 'data/csv/test.csv', page: 3, limit: 50 });
        client.destroy();
        pending.get(3)?.resolve(csvPage(3));
        await expect(third).resolves.toBeNull();
    });

    it('bounds the unavoidable legacy full-table fallback before retaining a page', async () => {
        const records = [
            ['name'],
            ...Array.from({ length: 120 }, (_, index) => [`row-${index}`]),
        ];
        const api = {
            GetCsvFile: vi.fn().mockResolvedValue(records),
        } as unknown as WailsAppAPI;

        const result = await loadCsvPage(api, { path: 'data/csv/test.csv', page: 2, limit: 50 });
        expect(result.legacy).toBe(true);
        expect(result.rows).toHaveLength(50);
        expect(result.rows[0]).toEqual(['row-50']);
        expect(result.totalRows).toBe(120);

        api.GetCsvFile = vi.fn().mockResolvedValue(
            Array.from({ length: CSV_LEGACY_MAX_RECORDS + 1 }, () => ['value'])
        );
        await expect(loadCsvPage(api, {
            path: 'data/csv/test.csv',
            page: 1,
            limit: 50,
        })).rejects.toThrow('Legacy CSV read is limited');
    });

    it('rejects an oversized JSON transfer and inconsistent rows', async () => {
        const oversized = 'x'.repeat(256 * 1024);
        const api = {
            GetCsvPage: vi.fn().mockResolvedValue({
                ...csvPage(1),
                rows: Array.from({ length: 50 }, (_, index) => [index < 17 ? oversized : '']),
            }),
        } as unknown as WailsAppAPI;
        await expect(loadCsvPage(api, {
            path: 'data/csv/test.csv',
            page: 1,
            limit: 50,
        })).rejects.toThrow('transfer limit');

        api.GetCsvPage = vi.fn().mockResolvedValue({
            ...csvPage(1),
            header: ['left', 'right'],
            rows: [['only-one']],
        });
        await expect(loadCsvPage(api, {
            path: 'data/csv/test.csv',
            page: 1,
            limit: 50,
        })).rejects.toThrow('inconsistent');
    });

    it('uses optimistic SaveCsvPage and restricts old SaveCsvFile to one bounded page', async () => {
        const api = {
            SaveCsvPage: vi.fn().mockResolvedValue({
                path: 'data/csv/test.csv',
                revision: 'revision-2',
                totalRows: 1,
            }),
            SaveCsvFile: vi.fn().mockResolvedValue(true),
        } as unknown as WailsAppAPI;
        const request = {
            path: 'data/csv/test.csv',
            revision: 'revision-1',
            page: 1,
            limit: 50,
            header: ['name'],
            rows: [['saved']],
        };

        await expect(saveCsvPage(api, request, { totalRows: 1 })).resolves.toMatchObject({
            revision: 'revision-2',
        });
        expect(api.SaveCsvPage).toHaveBeenCalledWith(request);
        expect(api.SaveCsvFile).not.toHaveBeenCalled();

        api.SaveCsvPage = undefined;
        await expect(saveCsvPage(api, { ...request, page: 2 }, { totalRows: 51 }))
            .rejects.toThrow('one bounded page');
        expect(api.SaveCsvFile).not.toHaveBeenCalled();
    });
});
