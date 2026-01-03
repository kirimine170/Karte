import { BaseComponent } from './component-base';
import type { WailsAppAPI, ImageInfo } from '../types/wails-api';
import { eventLogger } from '../utils/event-logger';
import { useUIStore } from '../stores/ui-store';
import { useModalStore } from '../stores/modal-store';

export class ImageGallery extends BaseComponent {
    private unsubscribe: (() => void)[] = [];
    private api: WailsAppAPI;
    private captureScreenBtn: HTMLButtonElement | null = null;
    private imageGalleryGrid: HTMLElement | null = null;
    private imageGalleryEmpty: HTMLElement | null = null;
    private imageGalleryRequestId = 0;

    constructor(api: WailsAppAPI) {
        super();
        this.api = api;
    }

    init(): void {
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

        // 初期ギャラリーの読み込み
        this.loadImageGallery();
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

            if (path && typeof path === 'string') {
                eventLogger.log('ImageGallery', 'capture-screen-success', { path });
                useUIStore.getState().setStatusMessage('スクリーンショットを保存しました', 3000);
                await this.loadImageGallery();
            } else {
                eventLogger.log('ImageGallery', 'capture-screen-cancelled');
                useUIStore.getState().setStatusMessage('スクリーンショットがキャンセルされました', 2000);
            }
        } catch (error) {
            console.error('Failed to capture screenshot:', error);
            eventLogger.log('ImageGallery', 'capture-screen-error', { error: String(error) });
            const msg = (error && (error as Error).message) || String(error) || 'スクリーンショットに失敗しました';
            useUIStore.getState().setStatusMessage(msg, 5000);
        } finally {
            if (this.captureScreenBtn) {
                this.captureScreenBtn.disabled = false;
            }
        }
    }

    private async loadImageGallery(): Promise<void> {
        if (!this.imageGalleryGrid || !this.api.GetImageList) {
            return;
        }

        const requestId = ++this.imageGalleryRequestId;

        try {
            eventLogger.log('ImageGallery', 'load-gallery-start');
            const images = await this.api.GetImageList();

            if (requestId !== this.imageGalleryRequestId) {
                // より新しいリクエストが先に完了した場合は無視
                return;
            }

            eventLogger.log('ImageGallery', 'load-gallery-success', { count: images.length });
            const dedupedImages = this.deduplicateImages(images);
            await this.renderImageGallery(dedupedImages);
        } catch (error) {
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

    private async renderImageGallery(images: ImageInfo[]): Promise<void> {
        if (!this.imageGalleryGrid || !this.imageGalleryEmpty) {
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

        // 画像を順次読み込んで表示
        for (const image of images) {
            try {
                const imageURL = await this.api.GetImageFileURL(image.path);
                const thumbnail = this.createImageThumbnail(image, imageURL);
                this.imageGalleryGrid.appendChild(thumbnail);
            } catch (error) {
                console.error('Failed to load image thumbnail:', image.path, error);
            }
        }
    }

    private createImageThumbnail(image: ImageInfo, imageURL: string): HTMLElement {
        const thumbnail = document.createElement('img');
        thumbnail.className = 'image-thumbnail';
        thumbnail.src = imageURL;
        thumbnail.alt = image.name;
        thumbnail.title = image.name;

        // データ属性に画像情報を保存
        thumbnail.setAttribute('data-image-path', image.path);
        thumbnail.setAttribute('data-image-name', image.name);

        // クリックでプレビュー表示
        this.unsubscribe.push(
            this.addEventListener(thumbnail, 'click', async () => {
                await this.showImagePreview(image.path, image.name, imageURL);
            })
        );

        // ドラッグ&ドロップ対応
        thumbnail.draggable = true;

        this.unsubscribe.push(
            this.addEventListener(thumbnail, 'dragstart', (e) => {
                const path = thumbnail.getAttribute('data-image-path');
                const name = thumbnail.getAttribute('data-image-name');

                if (!path || !name) {
                    console.error('Missing image data attributes');
                    return;
                }

                // グローバル変数に保存（iframeのドロップハンドラーで使用）
                (window as any).currentDragImageData = { path, name };

                e.dataTransfer.effectAllowed = 'copy';
                e.dataTransfer.setData('text/plain', path);
                e.dataTransfer.setData('application/json', JSON.stringify({ path, name }));

                // 視覚的フィードバック
                thumbnail.style.opacity = '0.5';
            })
        );

        this.unsubscribe.push(
            this.addEventListener(thumbnail, 'dragend', () => {
                thumbnail.style.opacity = '1';
                (window as any).currentDragImageData = null;
            })
        );

        return thumbnail;
    }

    private async showImagePreview(imagePath: string, imageName: string, imageURL?: string): Promise<void> {
        try {
            let finalImageURL = imageURL;
            if (!finalImageURL) {
                finalImageURL = await this.api.GetImageFileURL(imagePath);
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

            // モーダルストアを使用してプレビューを表示
            const modalStore = useModalStore.getState();
            modalStore.showImagePreviewModal(imagePath, imageName, metadata);
        } catch (error) {
            console.error('Error showing image preview:', error);
            useUIStore.getState().setStatusMessage('画像プレビューの表示に失敗しました', 3000);
        }
    }

    // 外部からギャラリーを再読み込みするためのメソッド
    async refresh(): Promise<void> {
        await this.loadImageGallery();
    }

    destroy(): void {
        this.unsubscribe.forEach((unsub) => unsub());
        this.unsubscribe = [];
    }
}

