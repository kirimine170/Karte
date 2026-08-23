import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { CsvGallery } from '../csv-gallery';
import { useModalStore } from '../../stores';

function csvResource(index: number, name = `file-${index}.csv`) {
    return {
        kind: 'csv' as const,
        path: `data/csv/${name}`,
        title: name,
        metadata: { name, extension: '.csv', size: index, modTime: '' },
    };
}

describe('CsvGallery paging', () => {
    beforeEach(() => {
        vi.spyOn(console, 'log').mockImplementation(() => {});
        document.body.innerHTML = `
            <div id="csvGalleryGrid"></div>
            <div id="csvGalleryEmpty"></div>
        `;
        useModalStore.getState().hideCsvEditModal();
    });

    afterEach(() => {
        vi.restoreAllMocks();
    });

    it('renders one SearchResources page from a 1000-item result and escapes names', async () => {
        const firstPage = Array.from({ length: 1000 }, (_, index) =>
            csvResource(index, index === 0 ? '<img src=x onerror=alert(1)>.csv' : `file-${index}.csv`));
        const api = {
            SearchResources: vi.fn().mockResolvedValue({
                items: firstPage,
                query: '',
                kinds: ['csv'],
                page: 1,
                limit: 50,
                total: 1000,
                hasMore: true,
            }),
            GetCsvList: vi.fn(),
        } as any;
        const gallery = new CsvGallery(api);

        gallery.init();
        await vi.waitFor(() => {
            expect(document.querySelectorAll('.csv-item:not(.csv-create-item)')).toHaveLength(50);
        });

        expect(api.SearchResources).toHaveBeenCalledWith({
            query: '',
            kinds: ['csv'],
            excludePaths: [],
            page: 1,
            limit: 50,
        });
        expect(api.GetCsvList).not.toHaveBeenCalled();
        expect(document.querySelector('#csvGalleryGrid img')).toBeNull();
        expect(document.querySelector('.csv-name')?.textContent).toBe('<img src=x onerror=alert(1)>.csv');
        expect(document.querySelector('[data-action="load-more-csv"]')).not.toBeNull();

        gallery.destroy();
    });

    it('loads one CSV page into the modal and ignores completion after destroy', async () => {
        let resolvePage!: (value: any) => void;
        const pagePromise = new Promise((resolve) => {
            resolvePage = resolve;
        });
        const api = {
            SearchResources: vi.fn().mockResolvedValue({
                items: [csvResource(1)],
                query: '',
                kinds: ['csv'],
                page: 1,
                limit: 50,
                total: 1,
                hasMore: false,
            }),
            GetCsvPage: vi.fn(() => pagePromise),
        } as any;
        const gallery = new CsvGallery(api);
        gallery.init();
        await vi.waitFor(() => expect(document.querySelector('[data-csv-path]')).not.toBeNull());

        document.querySelector<HTMLElement>('[data-csv-path]')?.dispatchEvent(new MouseEvent('dblclick', {
            bubbles: true,
        }));
        await vi.waitFor(() => expect(api.GetCsvPage).toHaveBeenCalledOnce());
        gallery.destroy();
        resolvePage({
            path: 'data/csv/file-1.csv',
            header: ['name'],
            rows: Array.from({ length: 50 }, (_, index) => [`row-${index}`]),
            page: 1,
            limit: 50,
            totalRows: 1000,
            hasMore: true,
            revision: 'revision-1',
        });
        await pagePromise;
        await Promise.resolve();

        expect(useModalStore.getState().csvEditModal.visible).toBe(false);
    });
});
