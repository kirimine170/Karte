import { BaseComponent } from './component-base';
import type { ResourceSearchItem, WailsAppAPI } from '../types/wails-api';
import { useModalStore, useUIStore } from '../stores/index';
import { eventLogger } from '../utils/event-logger';
import { CsvPageClient, CSV_PAGE_LIMIT, saveCsvPage } from '../utils/csv-page';
import {
    LEGACY_RESOURCE_SEARCH_MAX_ITEMS,
    ResourceSearchClient,
    RESOURCE_SEARCH_PAGE_LIMIT,
} from '../utils/resource-search';

export class CsvGallery extends BaseComponent {
    private unsubscribe: (() => void)[] = [];
    private api: WailsAppAPI;
    private readonly csvPageClient: CsvPageClient;
    private readonly resourceSearch: ResourceSearchClient;
    private destroyed = true;
    private csvGalleryGrid: HTMLElement | null = null;
    private csvGalleryEmpty: HTMLElement | null = null;
    private csvs: ResourceSearchItem[] = [];
    private page = 1;
    private hasMore = false;

    constructor(api: WailsAppAPI) {
        super();
        this.api = api;
        this.csvPageClient = new CsvPageClient(api);
        this.resourceSearch = new ResourceSearchClient(api);
    }

    init(): void {
        if (!this.destroyed) {
            return;
        }
        this.destroyed = false;
        eventLogger.log('CsvGallery', 'init');

        this.csvGalleryGrid = document.getElementById('csvGalleryGrid');
        this.csvGalleryEmpty = document.getElementById('csvGalleryEmpty');

        if (this.csvGalleryGrid) {
            this.setupGalleryEventDelegation(this.csvGalleryGrid);
        }

        void this.loadCsvGallery(true);
    }

    private async loadCsvGallery(reset = false): Promise<void> {
        if (this.destroyed || !this.csvGalleryGrid) {
            return;
        }
        const requestedPage = reset ? 1 : this.page + 1;

        try {
            eventLogger.log('CsvGallery', 'load-gallery-start');
            const result = await this.resourceSearch.search({
                query: '',
                kinds: ['csv'],
                page: requestedPage,
                limit: RESOURCE_SEARCH_PAGE_LIMIT,
            });
            if (!result || this.destroyed) {
                return;
            }
            const next = reset ? result.items : [...this.csvs, ...result.items];
            this.csvs = next.slice(0, LEGACY_RESOURCE_SEARCH_MAX_ITEMS);
            this.page = requestedPage;
            this.hasMore = result.hasMore && this.csvs.length < LEGACY_RESOURCE_SEARCH_MAX_ITEMS;
            eventLogger.log('CsvGallery', 'load-gallery-success', { count: this.csvs.length });
            this.renderCsvGallery();
        } catch (error) {
            if (this.destroyed) {
                return;
            }
            console.error('Failed to load CSV gallery:', error);
            eventLogger.log('CsvGallery', 'load-gallery-error', { error: String(error) });
            if (reset) {
                this.csvs = [];
                this.hasMore = false;
                this.renderCsvGallery();
            }
        }
    }

    private renderCsvGallery(): void {
        const grid = this.csvGalleryGrid;
        if (!grid || !this.csvGalleryEmpty) {
            return;
        }

        grid.innerHTML = '';

        const createItem = this.createElement('div', 'csv-item csv-create-item');
        const icon = this.createElement('div', 'csv-create-icon', '+');
        createItem.appendChild(icon);
        createItem.title = '新規CSV作成';
        grid.appendChild(createItem);

        if (this.csvs.length === 0) {
            this.csvGalleryEmpty.style.display = 'block';
            grid.style.display = 'grid';
            return;
        }

        this.csvGalleryEmpty.style.display = 'none';
        grid.style.display = 'grid';

        this.csvs.forEach((csv) => {
            const item = this.createElement('div', 'csv-item');
            item.appendChild(this.createElement('div', 'csv-icon', '📊'));
            item.appendChild(this.createElement('div', 'csv-name', csv.metadata.name || csv.title));
            item.setAttribute('data-csv-path', csv.path);
            item.setAttribute('data-csv-name', csv.metadata.name || csv.title);

            item.draggable = true;
            grid.appendChild(item);
        });

        if (this.hasMore) {
            const loadMore = this.createElement('button', 'btn csv-load-more', 'さらに読み込む');
            loadMore.setAttribute('type', 'button');
            loadMore.dataset.action = 'load-more-csv';
            grid.appendChild(loadMore);
        }
    }

    private setupGalleryEventDelegation(grid: HTMLElement): void {
        this.unsubscribe.push(
            this.addEventListener(grid, 'click', (event) => {
                const action = event.target instanceof Element
                    ? event.target.closest<HTMLElement>('[data-action="load-more-csv"]')
                    : null;
                if (action && grid.contains(action)) {
                    void this.loadCsvGallery(false);
                    return;
                }
                const item = this.itemFromEvent(event);
                if (item?.classList.contains('csv-create-item')) {
                    void this.createNewCsv();
                }
            }),
            this.addEventListener(grid, 'dblclick', (event) => {
                const item = this.itemFromEvent(event);
                const path = item?.dataset.csvPath;
                if (path && !item?.classList.contains('csv-create-item')) {
                    void this.openCsvEditModal(path);
                }
            }),
            this.addEventListener(grid, 'dragstart', (event) => {
                const item = this.itemFromEvent(event);
                const path = item?.dataset.csvPath;
                const name = item?.dataset.csvName;
                if (!item || !path || !name || !event.dataTransfer) {
                    return;
                }
                event.dataTransfer.effectAllowed = 'copy';
                event.dataTransfer.setData('text/plain', path);
                event.dataTransfer.setData('application/json', JSON.stringify({
                    type: 'csv',
                    path,
                    name,
                }));
                (window as any).currentDragCsvData = { path, name, type: 'csv' };
                item.style.opacity = '0.5';
            }),
            this.addEventListener(grid, 'dragend', (event) => {
                const item = this.itemFromEvent(event);
                if (item) {
                    item.style.opacity = '1';
                }
                (window as any).currentDragCsvData = null;
            })
        );
    }

    private itemFromEvent(event: Event): HTMLElement | null {
        if (!(event.target instanceof Element) || !this.csvGalleryGrid) {
            return null;
        }
        const item = event.target.closest<HTMLElement>('.csv-item');
        return item && this.csvGalleryGrid.contains(item) ? item : null;
    }

    private async createNewCsv(): Promise<void> {
        const defaultData = [
            ['列1', '列2', '列3'],
            ['', '', ''],
            ['', '', ''],
        ];

        try {
            const timestamp = new Date().toISOString().replace(/[:.]/g, '-').slice(0, -5);
            const filename = `data-${timestamp}.csv`;
            const path = `data/csv/${filename}`;

            await saveCsvPage(this.api, {
                path,
                revision: '',
                page: 1,
                limit: CSV_PAGE_LIMIT,
                header: defaultData[0],
                rows: defaultData.slice(1),
            });
            if (this.destroyed) {
                return;
            }
            await this.loadCsvGallery(true);
            if (this.destroyed) {
                return;
            }
            await this.openCsvEditModal(path);
            if (this.destroyed) {
                return;
            }
            useUIStore.getState().setStatusMessage('新しいCSVファイルを作成しました', 3000);
        } catch (error) {
            if (this.destroyed) {
                return;
            }
            console.error('Failed to create CSV:', error);
            useUIStore.getState().setStatusMessage('CSVファイルの作成に失敗しました', 3000);
        }
    }

    private async openCsvEditModal(path: string): Promise<void> {
        try {
            const page = await this.csvPageClient.load({ path, page: 1, limit: CSV_PAGE_LIMIT });
            if (!page || this.destroyed) {
                return;
            }
            useModalStore.getState().showCsvEditPage(page);
        } catch (error) {
            if (this.destroyed) {
                return;
            }
            console.error('Failed to load CSV file:', error);
            useUIStore.getState().setStatusMessage('CSVファイルの読み込みに失敗しました', 3000);
        }
    }

    async refresh(): Promise<void> {
        await this.loadCsvGallery(true);
    }

    destroy(): void {
        if (this.destroyed) {
            return;
        }
        this.destroyed = true;
        this.csvPageClient.destroy();
        this.resourceSearch.destroy();
        this.unsubscribe.forEach((unsub) => unsub());
        this.unsubscribe = [];
        this.csvGalleryGrid = null;
        this.csvGalleryEmpty = null;
        this.csvs = [];
        this.hasMore = false;
        (window as any).currentDragCsvData = null;
    }
}
