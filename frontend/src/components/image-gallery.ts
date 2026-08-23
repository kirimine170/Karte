import { BaseComponent } from './component-base';
import type { WailsAppAPI, ImageInfo } from '../types/wails-api';
import { eventLogger } from '../utils/event-logger';
import { useUIStore } from '../stores/ui-store';
import { useModalStore } from '../stores/modal-store';

export class ImageGallery extends BaseComponent {
    private unsubscribe: (() => void)[] = [];
    private api: WailsAppAPI;
    private destroyed = true;
    private captureScreenBtn: HTMLButtonElement | null = null;
    private imageGalleryGrid: HTMLElement | null = null;
    private imageGalleryEmpty: HTMLElement | null = null;
    private imageGalleryRequestId = 0;
    private imageObserver: IntersectionObserver | null = null;
    private imageLoadQueue: Array<() => Promise<void>> = [];
    private activeImageLoads = 0;
    private maxImageLoads = 6;
    private renderChunkSize = 36;
    private renderAnimationFrame: number | null = null;

    constructor(api: WailsAppAPI) {
        super();
        this.api = api;
    }

    init(): void {
        if (!this.destroyed) {
            return;
        }
        this.destroyed = false;
        eventLogger.log('ImageGallery', 'init');

        this.captureScreenBtn = document.getElementById('captureScreenBtn') as HTMLButtonElement;
        this.imageGalleryGrid = document.getElementById('imageGalleryGrid');
        this.imageGalleryEmpty = document.getElementById('imageGalleryEmpty');

        // キャプチャボタンの設定
        if (this.captureScreenBtn) {
            // ブラウザモードでは無効化
            const isBrowser = typeof window !== 'undefined' && !(window as any).go;
            if (isBrowser || !this.api.CaptureScreenInteractive) {
                this.captureScreenBtn.disabled = true;
                this.captureScreenBtn.title = 'スクリーンショットはデスクトップアプリでのみ利用できます';
            } else {
                this.unsubscribe.push(
                    this.addEventListener(this.captureScreenBtn, 'click', async () => {
                        await this.handleCaptureScreen();
                    })
                );
            }
        }

        if (this.imageGalleryGrid) {
            this.setupGalleryEventDelegation(this.imageGalleryGrid);
        }

        // 初期ギャラリーの読み込み
        void this.loadImageGallery();
    }

    private async handleCaptureScreen(): Promise<void> {
        if (!this.captureScreenBtn || this.captureScreenBtn.disabled) {
            return;
        }

        try {
            this.captureScreenBtn.disabled = true;
            eventLogger.log('ImageGallery', 'capture-screen-start');
            useUIStore.getState().setStatusMessage('スクリーンショットを取得中...', 0);

            const path = await this.api.CaptureScreenInteractive();

            if (this.destroyed) {
                return;
            }

            if (path && typeof path === 'string') {
                eventLogger.log('ImageGallery', 'capture-screen-success', { path });
                useUIStore.getState().setStatusMessage('スクリーンショットを保存しました', 3000);
                await this.loadImageGallery();
            } else {
                eventLogger.log('ImageGallery', 'capture-screen-cancelled');
                useUIStore.getState().setStatusMessage('スクリーンショットがキャンセルされました', 2000);
            }
        } catch (error) {
            if (this.destroyed) {
                return;
            }
            console.error('Failed to capture screenshot:', error);
            eventLogger.log('ImageGallery', 'capture-screen-error', { error: String(error) });
            const msg = error instanceof Error
                ? error.message
                : String(error || 'スクリーンショットに失敗しました');
            useUIStore.getState().setStatusMessage(msg, 5000);
        } finally {
            if (!this.destroyed && this.captureScreenBtn) {
                this.captureScreenBtn.disabled = false;
            }
        }
    }

    private async loadImageGallery(): Promise<void> {
        if (this.destroyed || !this.imageGalleryGrid || !this.api.GetImageList) {
            return;
        }

        const requestId = ++this.imageGalleryRequestId;
        this.stopRenderWork();

        try {
            eventLogger.log('ImageGallery', 'load-gallery-start');
            const images = await this.api.GetImageList();

            if (!this.isRequestActive(requestId)) {
                // より新しいリクエストが先に完了した場合は無視
                return;
            }

            eventLogger.log('ImageGallery', 'load-gallery-success', { count: images.length });
            const dedupedImages = this.deduplicateImages(images);
            this.renderImageGallery(dedupedImages, requestId);
        } catch (error) {
            if (!this.isRequestActive(requestId)) {
                return;
            }
            console.error('Failed to load image gallery:', error);
            eventLogger.log('ImageGallery', 'load-gallery-error', { error: String(error) });
        }
    }

    private deduplicateImages(images: ImageInfo[]): ImageInfo[] {
        if (!Array.isArray(images)) {
            return [];
        }
        const seen = new Map<string, ImageInfo>();
        for (const image of images) {
            if (!image || !image.path) {
                continue;
            }
            if (!seen.has(image.path)) {
                seen.set(image.path, image);
            }
        }
        return Array.from(seen.values());
    }

    private renderImageGallery(images: ImageInfo[], requestId: number): void {
        if (!this.isRequestActive(requestId) || !this.imageGalleryGrid || !this.imageGalleryEmpty) {
            return;
        }

        this.imageGalleryGrid.innerHTML = '';

        if (!images || images.length === 0) {
            this.imageGalleryEmpty.style.display = 'block';
            this.imageGalleryGrid.style.display = 'none';
            return;
        }

        this.imageGalleryEmpty.style.display = 'none';
        this.imageGalleryGrid.style.display = 'grid';

        const observer = new IntersectionObserver(
            (entries) => {
                if (!this.isRequestActive(requestId) || this.imageObserver !== observer) {
                    return;
                }
                for (const entry of entries) {
                    if (!entry.isIntersecting) {
                        continue;
                    }
                    const target = entry.target as HTMLImageElement;
                    observer.unobserve(target);
                    this.enqueueImageLoad(target, requestId);
                }
            },
            {
                root: this.imageGalleryGrid,
                rootMargin: '200px 0px',
                threshold: 0.01,
            }
        );
        this.imageObserver = observer;

        this.renderImageThumbnails(images, requestId);
    }

    private renderImageThumbnails(images: ImageInfo[], requestId: number): void {
        const grid = this.imageGalleryGrid;
        const observer = this.imageObserver;
        if (!grid || !observer) {
            return;
        }
        const total = images.length;
        let index = 0;

        const renderChunk = () => {
            this.renderAnimationFrame = null;
            if (!this.isRequestActive(requestId) || !this.imageGalleryGrid || !this.imageObserver) {
                return;
            }
            const fragment = document.createDocumentFragment();
            const end = Math.min(index + this.renderChunkSize, total);
            for (; index < end; index += 1) {
                const image = images[index];
                if (!image) {
                    continue;
                }
                const thumbnail = this.createImageThumbnail(image);
                fragment.appendChild(thumbnail);
                observer.observe(thumbnail);
            }
            grid.appendChild(fragment);
            if (index < total) {
                this.renderAnimationFrame = requestAnimationFrame(renderChunk);
            }
        };

        this.renderAnimationFrame = requestAnimationFrame(renderChunk);
    }

    private enqueueImageLoad(target: HTMLImageElement, requestId: number): void {
        const path = target.getAttribute('data-image-path');
        if (!path || target.dataset.loaded === 'true') {
            return;
        }
        target.dataset.loaded = 'pending';
        this.imageLoadQueue.push(async () => {
            if (!this.isRequestActive(requestId)) {
                return;
            }
            try {
                const imageURL = await this.api.GetImageFileURL(path);
                if (!this.isRequestActive(requestId) || !target.isConnected) {
                    return;
                }
                target.src = imageURL;
                target.dataset.loaded = 'true';
            } catch (error) {
                if (this.isRequestActive(requestId) && target.isConnected) {
                    target.dataset.loaded = 'false';
                    console.error('Failed to load image thumbnail:', path, error);
                }
            }
        });
        this.processImageQueue(requestId);
    }

    private processImageQueue(requestId: number): void {
        if (!this.isRequestActive(requestId)) {
            return;
        }
        while (this.activeImageLoads < this.maxImageLoads && this.imageLoadQueue.length > 0) {
            const task = this.imageLoadQueue.shift();
            if (!task) {
                return;
            }
            this.activeImageLoads += 1;
            void task().finally(() => {
                this.activeImageLoads -= 1;
                this.processImageQueue(this.imageGalleryRequestId);
            });
        }
    }

    private createImageThumbnail(image: ImageInfo): HTMLImageElement {
        const thumbnail = document.createElement('img');
        thumbnail.className = 'image-thumbnail';
        thumbnail.loading = 'lazy';
        thumbnail.decoding = 'async';
        thumbnail.dataset.loaded = 'false';
        thumbnail.alt = image.name;
        thumbnail.title = image.name;

        // データ属性に画像情報を保存
        thumbnail.setAttribute('data-image-path', image.path);
        thumbnail.setAttribute('data-image-name', image.name);

        thumbnail.draggable = true;
        return thumbnail;
    }

    private setupGalleryEventDelegation(grid: HTMLElement): void {
        this.unsubscribe.push(
            this.addEventListener(grid, 'click', (event) => {
                const thumbnail = this.thumbnailFromEvent(event);
                const path = thumbnail?.dataset.imagePath;
                const name = thumbnail?.dataset.imageName;
                if (path && name) {
                    void this.showImagePreview(path, name);
                }
            }),
            this.addEventListener(grid, 'dragstart', (event) => {
                const thumbnail = this.thumbnailFromEvent(event);
                const path = thumbnail?.dataset.imagePath;
                const name = thumbnail?.dataset.imageName;
                if (!thumbnail || !path || !name || !event.dataTransfer) {
                    return;
                }
                (window as any).currentDragImageData = { path, name };
                event.dataTransfer.effectAllowed = 'copy';
                event.dataTransfer.setData('text/plain', path);
                event.dataTransfer.setData('application/json', JSON.stringify({ path, name }));
                thumbnail.style.opacity = '0.5';
            }),
            this.addEventListener(grid, 'dragend', (event) => {
                const thumbnail = this.thumbnailFromEvent(event);
                if (thumbnail) {
                    thumbnail.style.opacity = '1';
                }
                (window as any).currentDragImageData = null;
            })
        );
    }

    private thumbnailFromEvent(event: Event): HTMLImageElement | null {
        if (!(event.target instanceof Element) || !this.imageGalleryGrid) {
            return null;
        }
        const thumbnail = event.target.closest<HTMLImageElement>('img.image-thumbnail');
        return thumbnail && this.imageGalleryGrid.contains(thumbnail) ? thumbnail : null;
    }

    private async showImagePreview(imagePath: string, imageName: string, imageURL?: string): Promise<void> {
        const requestId = this.imageGalleryRequestId;
        try {
            let finalImageURL = imageURL;
            if (!finalImageURL) {
                finalImageURL = await this.api.GetImageFileURL(imagePath);
            }

            if (!this.isRequestActive(requestId)) {
                return;
            }

            if (!finalImageURL) {
                console.error('Failed to get image URL for:', imagePath);
                useUIStore.getState().setStatusMessage('画像のURLを取得できませんでした', 3000);
                return;
            }

            eventLogger.log('ImageGallery', 'show-image-preview', { path: imagePath, name: imageName });

            // メタデータを先に読み込み
            let metadata = '';
            try {
                metadata = await this.api.GetImageMetadata(imagePath);
            } catch (error) {
                console.error('Failed to load image metadata:', error);
                // メタデータの読み込みに失敗してもプレビューは表示する
            }
            if (!this.isRequestActive(requestId)) {
                return;
            }
            let systemMetadata = '';
            try {
                systemMetadata = await this.api.GetImageSystemMetadata(imagePath);
            } catch (error) {
                console.error('Failed to load image system metadata:', error);
            }

            if (!this.isRequestActive(requestId)) {
                return;
            }

            // モーダルストアを使用してプレビューを表示
            const modalStore = useModalStore.getState();
            modalStore.showImagePreviewModal(imagePath, imageName, metadata, systemMetadata);
        } catch (error) {
            if (!this.isRequestActive(requestId)) {
                return;
            }
            console.error('Error showing image preview:', error);
            useUIStore.getState().setStatusMessage('画像プレビューの表示に失敗しました', 3000);
        }
    }

    // 外部からギャラリーを再読み込みするためのメソッド
    async refresh(): Promise<void> {
        await this.loadImageGallery();
    }

    destroy(): void {
        if (this.destroyed) {
            return;
        }
        this.destroyed = true;
        this.imageGalleryRequestId += 1;
        this.stopRenderWork();
        this.unsubscribe.forEach((unsub) => unsub());
        this.unsubscribe = [];
        this.captureScreenBtn = null;
        this.imageGalleryGrid = null;
        this.imageGalleryEmpty = null;
        (window as any).currentDragImageData = null;
    }

    private isRequestActive(requestId: number): boolean {
        return !this.destroyed && requestId === this.imageGalleryRequestId;
    }

    private stopRenderWork(): void {
        this.imageObserver?.disconnect();
        this.imageObserver = null;
        if (this.renderAnimationFrame !== null) {
            cancelAnimationFrame(this.renderAnimationFrame);
            this.renderAnimationFrame = null;
        }
        this.imageLoadQueue = [];
    }
}
