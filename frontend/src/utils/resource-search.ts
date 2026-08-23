import type {
    CsvInfo,
    FileItem,
    ImageInfo,
    ResourceKind,
    ResourceSearchItem,
    ResourceSearchMetadata,
    ResourceSearchRequest,
    ResourceSearchResult,
    WailsAppAPI,
} from '../types/wails-api';

export const RESOURCE_SEARCH_PAGE_LIMIT = 50;
export const LEGACY_RESOURCE_SEARCH_MAX_ITEMS = 200;

export interface LegacyResourceSearchOptions {
    documentItems?: FileItem[];
    boardPath?: string;
}

export class ResourceSearchClient {
    private generation = 0;
    private destroyed = false;

    constructor(private readonly api: WailsAppAPI) {}

    async search(
        request: ResourceSearchRequest,
        legacy: LegacyResourceSearchOptions = {},
    ): Promise<ResourceSearchResult | null> {
        if (this.destroyed) {
            return null;
        }
        const generation = ++this.generation;
        try {
            const result = await searchResourcePage(this.api, request, legacy);
            if (this.destroyed || generation !== this.generation) {
                return null;
            }
            return result;
        } catch (error) {
            if (this.destroyed || generation !== this.generation) {
                return null;
            }
            throw error;
        }
    }

    cancel(): void {
        this.generation += 1;
    }

    destroy(): void {
        if (this.destroyed) {
            return;
        }
        this.destroyed = true;
        this.generation += 1;
    }
}

export async function searchResourcePage(
    api: WailsAppAPI,
    request: ResourceSearchRequest,
    legacy: LegacyResourceSearchOptions = {},
): Promise<ResourceSearchResult> {
    const normalized = normalizeResourceRequest(request);
    if (typeof api.SearchResources === 'function') {
        const result = await api.SearchResources(normalized);
        return validateResourceResult(result, normalized);
    }
    return searchLegacyResourcePage(api, normalized, legacy);
}

function normalizeResourceRequest(request: ResourceSearchRequest): ResourceSearchRequest {
    const page = Number.isSafeInteger(request.page) && request.page > 0 ? request.page : 1;
    const requestedLimit = Number.isSafeInteger(request.limit) && request.limit > 0
        ? request.limit
        : RESOURCE_SEARCH_PAGE_LIMIT;
    const limit = Math.min(requestedLimit, RESOURCE_SEARCH_PAGE_LIMIT);
    const kinds = Array.from(new Set(request.kinds)).filter(isResourceKind);
    if (kinds.length === 0) {
        throw new Error('Resource search requires at least one supported kind');
    }
    const excludePaths = Array.from(new Set(request.excludePaths || []));
    return {
        query: normalizeResourceQuery(request.query),
        kinds,
        excludePaths,
        page,
        limit,
    };
}

function validateResourceResult(result: ResourceSearchResult, request: ResourceSearchRequest): ResourceSearchResult {
    if (!result || !Array.isArray(result.items)) {
        throw new Error('Backend returned an invalid resource search result');
    }
    const requestedKinds = new Set(request.kinds);
    const items = result.items.slice(0, request.limit).map((item) => {
        if (!item || !requestedKinds.has(item.kind) || typeof item.path !== 'string' || item.path.length === 0 ||
            typeof item.title !== 'string' || !item.metadata) {
            throw new Error('Backend returned an invalid resource search item');
        }
        return item;
    });
    const total = Number.isSafeInteger(result.total) && result.total >= items.length ? result.total : items.length;
    return {
        items,
        query: normalizeResourceQuery(result.query || request.query),
        kinds: request.kinds,
        page: request.page,
        limit: request.limit,
        total,
        hasMore: Boolean(result.hasMore) || result.items.length > request.limit,
    };
}

async function searchLegacyResourcePage(
    api: WailsAppAPI,
    request: ResourceSearchRequest,
    legacy: LegacyResourceSearchOptions,
): Promise<ResourceSearchResult> {
    // Legacy list APIs return their entire payload before frontend code can
    // apply a bound．This compatibility path therefore caps retained metadata
    // and rendered pages to 200 items，but cannot cap the old backend payload．
    const candidates = new Map<string, ResourceSearchItem>();
    const requestedKinds = new Set(request.kinds);
    const backendMatchedDocumentPaths = new Set<string>();

    if (requestedKinds.has('markdown') || requestedKinds.has('pdf')) {
        const documents = await loadLegacyDocuments(api, request.query, legacy);
        for (const document of documents.items) {
            addLegacyCandidate(candidates, resourceFromFileItem(document), requestedKinds);
            if (documents.backendFiltered && document?.path) {
                backendMatchedDocumentPaths.add(document.path);
            }
        }
    }
    if (requestedKinds.has('image') && typeof api.GetImageList === 'function') {
        const images = await api.GetImageList();
        for (const image of Array.isArray(images) ? images : []) {
            addLegacyCandidate(candidates, resourceFromImage(image), requestedKinds);
        }
    }
    if (requestedKinds.has('csv') && typeof api.GetCsvList === 'function') {
        const csvs = await api.GetCsvList();
        for (const csv of Array.isArray(csvs) ? csvs : []) {
            addLegacyCandidate(candidates, resourceFromCSV(csv), requestedKinds);
        }
    }

    const excluded = new Set(request.excludePaths || []);
    const matches = Array.from(candidates.values())
        .filter((item) => !excluded.has(item.path) &&
            (backendMatchedDocumentPaths.has(item.path) || resourceMatchesQuery(item, request.query)))
        .sort(compareResourceItems)
        .slice(0, LEGACY_RESOURCE_SEARCH_MAX_ITEMS);
    const start = Math.min((request.page - 1) * request.limit, matches.length);
    const end = Math.min(start + request.limit, matches.length);
    return {
        items: matches.slice(start, end),
        query: request.query,
        kinds: request.kinds,
        page: request.page,
        limit: request.limit,
        total: matches.length,
        hasMore: end < matches.length,
    };
}

async function loadLegacyDocuments(
    api: WailsAppAPI,
    query: string,
    legacy: LegacyResourceSearchOptions,
): Promise<{ items: FileItem[]; backendFiltered: boolean }> {
    if (query && typeof api.SearchFiles === 'function') {
        const documents: FileItem[] = [];
        let page = 1;
        while (documents.length < LEGACY_RESOURCE_SEARCH_MAX_ITEMS) {
            const result = await api.SearchFiles(query, page, 100);
            const items = Array.isArray(result.items) ? result.items : [];
            documents.push(...items.slice(0, LEGACY_RESOURCE_SEARCH_MAX_ITEMS - documents.length));
            if (!result.hasMore || items.length === 0 || documents.length >= LEGACY_RESOURCE_SEARCH_MAX_ITEMS) {
                break;
            }
            page += 1;
        }
        return { items: documents, backendFiltered: true };
    }

    const merged: FileItem[] = [];
    if (legacy.boardPath && typeof api.GetBoardResourceCandidates === 'function') {
        const candidates = await api.GetBoardResourceCandidates(legacy.boardPath);
        merged.push(...(Array.isArray(candidates) ? candidates : []));
    } else if (typeof api.GetFileList === 'function') {
        const files = await api.GetFileList();
        merged.push(...(Array.isArray(files) ? files : []));
    }
    if (Array.isArray(legacy.documentItems)) {
        merged.push(...legacy.documentItems);
    }
    return { items: merged.slice(0, LEGACY_RESOURCE_SEARCH_MAX_ITEMS), backendFiltered: false };
}

function addLegacyCandidate(
    candidates: Map<string, ResourceSearchItem>,
    item: ResourceSearchItem | null,
    requestedKinds: Set<ResourceKind>,
): void {
    if (!item || !requestedKinds.has(item.kind) || candidates.has(item.path) ||
        candidates.size >= LEGACY_RESOURCE_SEARCH_MAX_ITEMS) {
        return;
    }
    candidates.set(item.path, item);
}

function resourceFromFileItem(file: FileItem): ResourceSearchItem | null {
    if (!file || typeof file.path !== 'string' || file.path.length === 0) {
        return null;
    }
    const extension = file.path.toLowerCase().endsWith('.pdf') ? '.pdf' : '.md';
    const kind: ResourceKind = extension === '.pdf' ? 'pdf' : 'markdown';
    const name = file.path.split('/').pop() || file.path;
    return {
        kind,
        path: file.path,
        title: file.title || name,
        metadata: metadataFromLegacy(name, extension, file.size, file.modTime),
    };
}

function resourceFromImage(image: ImageInfo): ResourceSearchItem | null {
    if (!image || typeof image.path !== 'string' || image.path.length === 0) {
        return null;
    }
    const name = image.name || image.path.split('/').pop() || image.path;
    return {
        kind: 'image',
        path: image.path,
        title: name,
        metadata: metadataFromLegacy(name, extensionOf(name), image.size, image.modTime),
    };
}

function resourceFromCSV(csv: CsvInfo): ResourceSearchItem | null {
    if (!csv || typeof csv.path !== 'string' || csv.path.length === 0) {
        return null;
    }
    const name = csv.name || csv.path.split('/').pop() || csv.path;
    return {
        kind: 'csv',
        path: csv.path,
        title: name,
        metadata: metadataFromLegacy(name, '.csv', csv.size, csv.modTime),
    };
}

function metadataFromLegacy(
    name: string,
    extension: string,
    size: unknown,
    modTime: unknown,
): ResourceSearchMetadata {
    return {
        name,
        extension,
        size: typeof size === 'number' && Number.isFinite(size) ? size : 0,
        modTime: typeof modTime === 'string' ? modTime : '',
    };
}

function resourceMatchesQuery(item: ResourceSearchItem, query: string): boolean {
    if (!query) {
        return true;
    }
    return normalizeResourceQuery(`${item.title}\n${item.path}\n${item.metadata.name}`).includes(query);
}

function compareResourceItems(left: ResourceSearchItem, right: ResourceSearchItem): number {
    const normalizedLeft = left.path.toLowerCase();
    const normalizedRight = right.path.toLowerCase();
    if (normalizedLeft !== normalizedRight) {
        return normalizedLeft.localeCompare(normalizedRight);
    }
    if (left.path !== right.path) {
        return left.path.localeCompare(right.path);
    }
    return left.kind.localeCompare(right.kind);
}

function normalizeResourceQuery(query: string): string {
    return (query || '').trim().toLowerCase();
}

function extensionOf(name: string): string {
    const dot = name.lastIndexOf('.');
    return dot >= 0 ? name.slice(dot).toLowerCase() : '';
}

function isResourceKind(value: unknown): value is ResourceKind {
    return value === 'markdown' || value === 'pdf' || value === 'image' || value === 'csv';
}
