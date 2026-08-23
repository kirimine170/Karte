import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { CsvGallery } from '../csv-gallery';
import { ImageGallery } from '../image-gallery';
import type { ImageInfo } from '../../types/wails-api';

interface PendingImageLoad {
    path: string;
    resolve: (url: string) => void;
}

class ControlledIntersectionObserver {
    readonly root: Element | Document | null;
    readonly rootMargin: string;
    readonly thresholds: readonly number[];
    readonly observed = new Set<Element>();
    readonly disconnect = vi.fn(() => {
        this.observed.clear();
    });
    readonly observe = vi.fn((target: Element) => {
        this.observed.add(target);
    });
    readonly unobserve = vi.fn((target: Element) => {
        this.observed.delete(target);
    });
    readonly takeRecords = vi.fn((): IntersectionObserverEntry[] => []);

    constructor(
        private readonly callback: IntersectionObserverCallback,
        options: IntersectionObserverInit = {}
    ) {
        this.root = options.root ?? null;
        this.rootMargin = options.rootMargin ?? '0px';
        this.thresholds = Array.isArray(options.threshold)
            ? options.threshold
            : [options.threshold ?? 0];
        controlledObservers.push(this);
    }

    emit(targets: Element[] = Array.from(this.observed)): void {
        const entries = targets.map((target) => ({
            time: 0,
            target,
            rootBounds: null,
            boundingClientRect: target.getBoundingClientRect(),
            intersectionRect: target.getBoundingClientRect(),
            isIntersecting: true,
            intersectionRatio: 1,
        })) as IntersectionObserverEntry[];
        this.callback(entries, this as unknown as IntersectionObserver);
    }
}

const controlledObservers: ControlledIntersectionObserver[] = [];
const pendingAnimationFrames = new Map<number, FrameRequestCallback>();
const cancelledAnimationFrames = new Map<number, FrameRequestCallback>();
let nextAnimationFrameId = 1;

function lifecycleCleanupCount(component: object): number {
    return (component as { unsubscribe: Array<() => void> }).unsubscribe.length;
}

function runNextAnimationFrame(): void {
    const next = pendingAnimationFrames.entries().next().value as
        | [number, FrameRequestCallback]
        | undefined;
    if (!next) {
        throw new Error('Expected a pending animation frame');
    }
    const [id, callback] = next;
    pendingAnimationFrames.delete(id);
    callback(0);
}

describe('gallery lifecycle', () => {
    beforeEach(() => {
        vi.spyOn(console, 'log').mockImplementation(() => {});
        controlledObservers.length = 0;
        pendingAnimationFrames.clear();
        cancelledAnimationFrames.clear();
        nextAnimationFrameId = 1;

        vi.stubGlobal('IntersectionObserver', ControlledIntersectionObserver);
        vi.stubGlobal(
            'requestAnimationFrame',
            vi.fn((callback: FrameRequestCallback) => {
                const id = nextAnimationFrameId;
                nextAnimationFrameId += 1;
                pendingAnimationFrames.set(id, callback);
                return id;
            })
        );
        vi.stubGlobal(
            'cancelAnimationFrame',
            vi.fn((id: number) => {
                const callback = pendingAnimationFrames.get(id);
                if (callback) {
                    cancelledAnimationFrames.set(id, callback);
                }
                pendingAnimationFrames.delete(id);
            })
        );
    });

    afterEach(() => {
        vi.unstubAllGlobals();
        vi.restoreAllMocks();
    });

    it('keeps delegated listeners and actual image loads bounded across 100 refreshes', async () => {
        document.body.innerHTML = `
            <button id="captureScreenBtn"></button>
            <div id="imageGalleryGrid"></div>
            <div id="imageGalleryEmpty"></div>
        `;
        const images: ImageInfo[] = Array.from({ length: 50 }, (_, index) => ({
            path: `images/image-${index}.png`,
            name: `image-${index}.png`,
            size: index,
            modTime: '',
        }));
        const pendingImageLoads: PendingImageLoad[] = [];
        let activeImageLoads = 0;
        let peakImageLoads = 0;
        const getImageFileURL = vi.fn((path: string) => {
            activeImageLoads += 1;
            peakImageLoads = Math.max(peakImageLoads, activeImageLoads);
            return new Promise<string>((resolve) => {
                pendingImageLoads.push({
                    path,
                    resolve: (url: string) => {
                        activeImageLoads -= 1;
                        resolve(url);
                    },
                });
            });
        });
        const api = {
            GetImageList: vi.fn().mockResolvedValue(images),
            GetImageFileURL: getImageFileURL,
            GetImageMetadata: vi.fn().mockResolvedValue(''),
            GetImageSystemMetadata: vi.fn().mockResolvedValue(''),
        } as any;
        const grid = document.getElementById('imageGalleryGrid') as HTMLElement;
        const addEventListener = vi.spyOn(grid, 'addEventListener');
        const removeEventListener = vi.spyOn(grid, 'removeEventListener');
        const gallery = new ImageGallery(api);

        gallery.init();
        await vi.waitFor(() => {
            expect(controlledObservers).toHaveLength(1);
            expect(pendingAnimationFrames.size).toBe(1);
        });
        expect(addEventListener).toHaveBeenCalledTimes(3);
        const initialCleanupCount = lifecycleCleanupCount(gallery);

        runNextAnimationFrame();
        controlledObservers[0]!.emit();
        expect(getImageFileURL).toHaveBeenCalledTimes(6);

        for (let index = 0; index < 100; index += 1) {
            await gallery.refresh();
            runNextAnimationFrame();
            controlledObservers[controlledObservers.length - 1]?.emit();
        }

        expect(addEventListener).toHaveBeenCalledTimes(3);
        expect(lifecycleCleanupCount(gallery)).toBe(initialCleanupCount);
        expect(getImageFileURL).toHaveBeenCalledTimes(6);
        expect(peakImageLoads).toBe(6);
        expect(pendingAnimationFrames.size).toBe(1);
        expect(controlledObservers).toHaveLength(101);
        for (const observer of controlledObservers.slice(0, -1)) {
            expect(observer.disconnect).toHaveBeenCalledTimes(1);
        }

        for (const load of pendingImageLoads.slice(0, 6)) {
            load.resolve(`image://${load.path}`);
        }
        await vi.waitFor(() => {
            expect(getImageFileURL).toHaveBeenCalledTimes(12);
        });
        expect(peakImageLoads).toBeLessThanOrEqual(6);

        const liveLoad = pendingImageLoads[6]!;
        const liveTarget = Array.from(grid.querySelectorAll<HTMLImageElement>('.image-thumbnail')).find(
            (thumbnail) => thumbnail.dataset.imagePath === liveLoad.path
        );
        expect(liveTarget).toBeDefined();
        expect(liveTarget?.dataset.loaded).toBe('pending');
        const htmlBeforeDestroy = grid.innerHTML;
        const lastObserver = controlledObservers[controlledObservers.length - 1];
        const cancelledCallback = Array.from(pendingAnimationFrames.values())[0]!;
        const cancellationCountBeforeDestroy = cancelledAnimationFrames.size;

        gallery.destroy();

        expect(lastObserver?.disconnect).toHaveBeenCalledTimes(1);
        expect(pendingAnimationFrames.size).toBe(0);
        expect(cancelledAnimationFrames.size).toBe(cancellationCountBeforeDestroy + 1);
        expect(removeEventListener).toHaveBeenCalledTimes(3);

        if (liveTarget) {
            lastObserver?.emit([liveTarget]);
        }
        cancelledCallback(0);
        for (const load of pendingImageLoads.slice(6)) {
            load.resolve(`image://${load.path}`);
        }
        await vi.waitFor(() => {
            expect(activeImageLoads).toBe(0);
        });

        expect(getImageFileURL).toHaveBeenCalledTimes(12);
        expect(grid.innerHTML).toBe(htmlBeforeDestroy);
        expect(liveTarget?.dataset.loaded).toBe('pending');
        expect(liveTarget?.getAttribute('src')).toBeNull();
    });

    it('keeps CSV listeners constant across 100 refreshes', async () => {
        document.body.innerHTML = `
            <div id="csvGalleryGrid"></div>
            <div id="csvGalleryEmpty"></div>
        `;
        const api = {
            GetCsvList: vi.fn().mockResolvedValue([
                { path: 'data/csv/alpha.csv', name: 'alpha.csv', size: 1, modTime: '' },
                { path: 'data/csv/beta.csv', name: 'beta.csv', size: 2, modTime: '' },
            ]),
        } as any;
        const grid = document.getElementById('csvGalleryGrid') as HTMLElement;
        const addEventListener = vi.spyOn(grid, 'addEventListener');
        const removeEventListener = vi.spyOn(grid, 'removeEventListener');
        const gallery = new CsvGallery(api);

        gallery.init();
        await vi.waitFor(() => {
            expect(grid.querySelectorAll('.csv-item')).toHaveLength(3);
        });
        const initialCleanupCount = lifecycleCleanupCount(gallery);

        for (let index = 0; index < 100; index += 1) {
            await gallery.refresh();
        }

        expect(addEventListener).toHaveBeenCalledTimes(4);
        expect(lifecycleCleanupCount(gallery)).toBe(initialCleanupCount);
        expect(grid.querySelectorAll('.csv-item')).toHaveLength(3);

        gallery.destroy();
        expect(removeEventListener).toHaveBeenCalledTimes(4);
    });
});
