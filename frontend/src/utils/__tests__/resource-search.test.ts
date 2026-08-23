import { describe, expect, it, vi } from 'vitest';
import type { ResourceKind, ResourceSearchItem, ResourceSearchResult, WailsAppAPI } from '../../types/wails-api';
import {
    LEGACY_RESOURCE_SEARCH_MAX_ITEMS,
    RESOURCE_SEARCH_PAGE_LIMIT,
    ResourceSearchClient,
    searchResourcePage,
} from '../resource-search';

function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void; reject: (error: unknown) => void } {
    let resolve!: (value: T) => void;
    let reject!: (error: unknown) => void;
    const promise = new Promise<T>((promiseResolve, promiseReject) => {
        resolve = promiseResolve;
        reject = promiseReject;
    });
    return { promise, resolve, reject };
}

function item(path: string, kind: ResourceKind = 'markdown'): ResourceSearchItem {
    return {
        kind,
        path,
        title: path.split('/').pop() || path,
        metadata: {
            name: path.split('/').pop() || path,
            extension: kind === 'csv' ? '.csv' : kind === 'image' ? '.webp' : kind === 'pdf' ? '.pdf' : '.md',
            size: 1,
            modTime: '2026-01-01T00:00:00.000Z',
        },
    };
}

function result(query: string, items: ResourceSearchItem[]): ResourceSearchResult {
    return {
        items,
        query,
        kinds: ['markdown'],
        page: 1,
        limit: RESOURCE_SEARCH_PAGE_LIMIT,
        total: items.length,
        hasMore: false,
    };
}

describe('ResourceSearchClient', () => {
    it('drops reverse-order stale results and results that settle after destroy', async () => {
        const first = deferred<ResourceSearchResult>();
        const second = deferred<ResourceSearchResult>();
        const afterDestroy = deferred<ResourceSearchResult>();
        const searchResources = vi.fn((request) => {
            if (request.query === 'first') return first.promise;
            if (request.query === 'second') return second.promise;
            return afterDestroy.promise;
        });
        const client = new ResourceSearchClient({ SearchResources: searchResources } as unknown as WailsAppAPI);

        const firstRequest = client.search({ query: 'first', kinds: ['markdown'], page: 1, limit: 50 });
        const secondRequest = client.search({ query: 'second', kinds: ['markdown'], page: 1, limit: 50 });
        second.resolve(result('second', [item('content/second.md')]));
        expect((await secondRequest)?.items[0]?.path).toBe('content/second.md');
        first.resolve(result('first', [item('content/first.md')]));
        expect(await firstRequest).toBeNull();

        const destroyedRequest = client.search({ query: 'destroyed', kinds: ['markdown'], page: 1, limit: 50 });
        client.destroy();
        afterDestroy.resolve(result('destroyed', [item('content/destroyed.md')]));
        expect(await destroyedRequest).toBeNull();
    });

    it('drops stale and post-destroy rejected searches', async () => {
        const stale = deferred<ResourceSearchResult>();
        const current = deferred<ResourceSearchResult>();
        const afterDestroy = deferred<ResourceSearchResult>();
        const searchResources = vi.fn((request) => {
            if (request.query === 'stale') return stale.promise;
            if (request.query === 'current') return current.promise;
            return afterDestroy.promise;
        });
        const client = new ResourceSearchClient({ SearchResources: searchResources } as unknown as WailsAppAPI);

        const staleRequest = client.search({ query: 'stale', kinds: ['csv'], page: 1, limit: 50 });
        const currentRequest = client.search({ query: 'current', kinds: ['csv'], page: 1, limit: 50 });
        current.resolve({ ...result('current', []), kinds: ['csv'] });
        await expect(currentRequest).resolves.not.toBeNull();
        stale.reject(new Error('stale search failed'));
        await expect(staleRequest).resolves.toBeNull();

        const destroyedRequest = client.search({ query: 'destroyed', kinds: ['csv'], page: 1, limit: 50 });
        client.destroy();
        afterDestroy.reject(new Error('destroyed search failed'));
        await expect(destroyedRequest).resolves.toBeNull();
    });

    it('caps an unavoidable legacy full-list payload to 200 retained items and 50 per page', async () => {
        const images = Array.from({ length: 1_000 }, (_, index) => ({
            path: `data/image/item-${String(index).padStart(4, '0')}.webp`,
            name: `item-${String(index).padStart(4, '0')}.webp`,
            size: 1,
            modTime: '2026-01-01T00:00:00.000Z',
        }));
        const getImageList = vi.fn().mockResolvedValue(images);
        const api = { GetImageList: getImageList } as unknown as WailsAppAPI;
        const first = await searchResourcePage(api, { query: '', kinds: ['image'], page: 1, limit: 50 });
        expect(first.items).toHaveLength(50);
        expect(first.total).toBe(LEGACY_RESOURCE_SEARCH_MAX_ITEMS);
        expect(first.hasMore).toBe(true);
        const fourth = await searchResourcePage(api, { query: '', kinds: ['image'], page: 4, limit: 50 });
        expect(fourth.items).toHaveLength(50);
        expect(fourth.hasMore).toBe(false);
        expect(getImageList).toHaveBeenCalledTimes(2);
    });

    it('uses the typed backend exclusively and defensively bounds an oversized page', async () => {
        const oversized = Array.from({ length: 1_000 }, (_, index) => item(`content/item-${index}.md`));
        const searchResources = vi.fn().mockResolvedValue(result('', oversized));
        const getFileList = vi.fn();
        const getImageList = vi.fn();
        const getCsvList = vi.fn();
        const page = await searchResourcePage({
            SearchResources: searchResources,
            GetFileList: getFileList,
            GetImageList: getImageList,
            GetCsvList: getCsvList,
        } as unknown as WailsAppAPI, {
            query: '',
            kinds: ['markdown'],
            page: 1,
            limit: 50,
        });
        expect(page.items).toHaveLength(50);
        expect(page.hasMore).toBe(true);
        expect(searchResources).toHaveBeenCalledWith({
            query: '',
            kinds: ['markdown'],
            excludePaths: [],
            page: 1,
            limit: 50,
        });
        expect(getFileList).not.toHaveBeenCalled();
        expect(getImageList).not.toHaveBeenCalled();
        expect(getCsvList).not.toHaveBeenCalled();
    });
});
