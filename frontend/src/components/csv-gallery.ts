import { BaseComponent } from './component-base';
import type { WailsAppAPI, CsvInfo } from '../types/wails-api';
import { useModalStore, useUIStore } from '../stores/index';
import { eventLogger } from '../utils/event-logger';

export class CsvGallery extends BaseComponent {
    private unsubscribe: (() => void)[] = [];
    private api: WailsAppAPI;
    private csvGalleryGrid: HTMLElement | null = null;
    private csvGalleryEmpty: HTMLElement | null = null;
    private csvGalleryRequestId = 0;

    constructor(api: WailsAppAPI) {
        super();
        this.api = api;
    }

    init(): void {
        eventLogger.log('CsvGallery', 'init');

        this.csvGalleryGrid = document.getElementById('csvGalleryGrid');
        this.csvGalleryEmpty = document.getElementById('csvGalleryEmpty');

        this.loadCsvGallery();
    }

    private async loadCsvGallery(): Promise<void> {
        if (!this.csvGalleryGrid || !this.api.GetCsvList) {
            return;
        }

        const requestId = ++this.csvGalleryRequestId;

        try {
            eventLogger.log('CsvGallery', 'load-gallery-start');
            const csvs = await this.api.GetCsvList();
            if (requestId !== this.csvGalleryRequestId) {
                return;
            }
            eventLogger.log('CsvGallery', 'load-gallery-success', { count: csvs.length });
            this.renderCsvGallery(csvs);
        } catch (error) {
            console.error('Failed to load CSV gallery:', error);
            eventLogger.log('CsvGallery', 'load-gallery-error', { error: String(error) });
            this.renderCsvGallery([]);
        }
    }

    private renderCsvGallery(csvs: CsvInfo[]): void {
        if (!this.csvGalleryGrid || !this.csvGalleryEmpty) {
            return;
        }

        this.csvGalleryGrid.innerHTML = '';

        const createItem = this.createElement('div', 'csv-item csv-create-item');
        const icon = this.createElement('div', 'csv-create-icon', '+');
        createItem.appendChild(icon);
        createItem.title = '新規CSV作成';
        this.unsubscribe.push(
            this.addEventListener(createItem, 'click', async () => {
                await this.createNewCsv();
            })
        );
        this.csvGalleryGrid.appendChild(createItem);

        if (!csvs || csvs.length === 0) {
            this.csvGalleryEmpty.style.display = 'block';
            this.csvGalleryGrid.style.display = 'grid';
            return;
        }

        this.csvGalleryEmpty.style.display = 'none';
        this.csvGalleryGrid.style.display = 'grid';

        csvs.forEach((csv) => {
            const item = this.createElement('div', 'csv-item');
            item.innerHTML = `
                <div class="csv-icon">📊</div>
                <div class="csv-name">${csv.name}</div>
            `;
            item.setAttribute('data-csv-path', csv.path);
            item.setAttribute('data-csv-name', csv.name);

            this.unsubscribe.push(
                this.addEventListener(item, 'dblclick', async () => {
                    await this.openCsvEditModal(csv.path);
                })
            );

            item.draggable = true;
            this.unsubscribe.push(
                this.addEventListener(item, 'dragstart', (event) => {
                    event.dataTransfer.effectAllowed = 'copy';
                    event.dataTransfer.setData('text/plain', csv.path);
                    event.dataTransfer.setData('application/json', JSON.stringify({
                        type: 'csv',
                        path: csv.path,
                        name: csv.name,
                    }));
                    (window as any).currentDragCsvData = { path: csv.path, name: csv.name, type: 'csv' };
                    item.style.opacity = '0.5';
                })
            );
            this.unsubscribe.push(
                this.addEventListener(item, 'dragend', () => {
                    item.style.opacity = '1';
                    (window as any).currentDragCsvData = null;
                })
            );

            this.csvGalleryGrid.appendChild(item);
        });
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

            await this.api.SaveCsvFile(path, defaultData);
            await this.loadCsvGallery();
            await this.openCsvEditModal(path);
            useUIStore.getState().setStatusMessage('新しいCSVファイルを作成しました', 3000);
        } catch (error) {
            console.error('Failed to create CSV:', error);
            useUIStore.getState().setStatusMessage('CSVファイルの作成に失敗しました', 3000);
        }
    }

    private async openCsvEditModal(path: string): Promise<void> {
        try {
            const data = await this.api.GetCsvFile(path);
            useModalStore.getState().showCsvEditModal(path, data);
        } catch (error) {
            console.error('Failed to load CSV file:', error);
            useUIStore.getState().setStatusMessage('CSVファイルの読み込みに失敗しました', 3000);
        }
    }

    async refresh(): Promise<void> {
        await this.loadCsvGallery();
    }

    destroy(): void {
        this.unsubscribe.forEach((unsub) => unsub());
        this.unsubscribe = [];
    }
}
