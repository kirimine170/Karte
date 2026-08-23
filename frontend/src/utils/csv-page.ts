import type {
    CsvPageRequest,
    CsvPageResult,
    CsvSavePageRequest,
    CsvSaveResult,
    WailsAppAPI,
} from '../types/wails-api';

export const CSV_PAGE_LIMIT = 50;
export const CSV_MAX_PAGE_LIMIT = 200;
export const CSV_LEGACY_MAX_RECORDS = 200;
export const CSV_MAX_PAGE_TRANSFER_BYTES = 4 * 1024 * 1024;
export const CSV_MAX_FIELDS = 1024;
export const CSV_MAX_CELL_BYTES = 256 * 1024;
export const CSV_MAX_RECORD_BYTES = 1024 * 1024;

export interface CsvLegacySaveContext {
    totalRows: number;
}

export class CsvPageClient {
    private generation = 0;
    private destroyed = false;

    constructor(private readonly api: WailsAppAPI) {}

    async load(request: CsvPageRequest): Promise<CsvPageResult | null> {
        if (this.destroyed) {
            return null;
        }
        const generation = ++this.generation;
        try {
            const result = await loadCsvPage(this.api, request);
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

export async function loadCsvPage(
    api: WailsAppAPI,
    request: CsvPageRequest,
): Promise<CsvPageResult> {
    const normalized = normalizeCsvPageRequest(request);
    if (typeof api.GetCsvPage === 'function') {
        const result = await api.GetCsvPage(normalized);
        return validateCsvPageResult(result, normalized);
    }
    return loadLegacyCsvPage(api, normalized);
}

export async function saveCsvPage(
    api: WailsAppAPI,
    request: CsvSavePageRequest,
    legacyContext?: CsvLegacySaveContext,
): Promise<CsvSaveResult> {
    const normalized = normalizeCsvSaveRequest(request);
    validateCsvRecords(normalized.header, normalized.rows, normalized.limit);
    if (typeof api.SaveCsvPage === 'function') {
        const result = await api.SaveCsvPage(normalized);
        return validateCsvSaveResult(result, normalized.path);
    }

    // The old API can only replace a complete table．It already receives the
    // full payload before this frontend can impose a bound，so compatibility is
    // restricted to a single page containing at most 200 total records．
    if (normalized.page !== 1 ||
        (normalized.revision !== '' && (!legacyContext || legacyContext.totalRows > normalized.limit)) ||
        normalized.rows.length + 1 > CSV_LEGACY_MAX_RECORDS) {
        throw new Error('Legacy CSV save is limited to one bounded page');
    }
    await api.SaveCsvFile(normalized.path, [normalized.header, ...normalized.rows]);
    return {
        path: normalized.path,
        revision: '',
        totalRows: normalized.rows.length,
    };
}

function normalizeCsvPageRequest(request: CsvPageRequest): CsvPageRequest {
    if (!request || typeof request.path !== 'string' || request.path.length === 0) {
        throw new Error('CSV page requires a path');
    }
    const page = Number.isSafeInteger(request.page) && request.page > 0 ? request.page : 1;
    const requestedLimit = Number.isSafeInteger(request.limit) && request.limit > 0
        ? request.limit
        : CSV_PAGE_LIMIT;
    const limit = Math.min(requestedLimit, CSV_MAX_PAGE_LIMIT);
    if (page - 1 > Math.floor(Number.MAX_SAFE_INTEGER / limit)) {
        throw new Error('CSV page offset exceeds the safe integer range');
    }
    return {
        path: request.path,
        page,
        limit,
    };
}

function normalizeCsvSaveRequest(request: CsvSavePageRequest): CsvSavePageRequest {
    const normalized = normalizeCsvPageRequest(request);
    if (request.page !== normalized.page || request.limit !== normalized.limit) {
        throw new Error('CSV save page and limit must already be normalized');
    }
    if (typeof request.revision !== 'string') {
        throw new Error('CSV save requires a revision');
    }
    return {
        ...normalized,
        revision: request.revision,
        header: request.header,
        rows: request.rows,
    };
}

function validateCsvPageResult(
    result: CsvPageResult,
    request: CsvPageRequest,
): CsvPageResult {
    if (!result || result.path !== request.path || result.page !== request.page ||
        result.limit !== request.limit || !Number.isSafeInteger(result.totalRows) ||
        result.totalRows < 0 || typeof result.revision !== 'string' || result.revision.length === 0) {
        throw new Error('Backend returned invalid CSV page metadata');
    }
    validateCsvRecords(result.header, result.rows, request.limit);
    const start = (request.page - 1) * request.limit;
    const lastPage = Math.max(1, Math.ceil(result.totalRows / request.limit));
    const expectedRows = Math.min(request.limit, Math.max(result.totalRows - start, 0));
    const expectedHasMore = start + expectedRows < result.totalRows;
    if (request.page > lastPage || result.rows.length !== expectedRows || Boolean(result.hasMore) !== expectedHasMore) {
        throw new Error('Backend returned inconsistent CSV page boundaries');
    }
    return {
        path: result.path,
        header: result.header.map(String),
        rows: result.rows.map((row) => row.map(String)),
        page: result.page,
        limit: result.limit,
        totalRows: result.totalRows,
        hasMore: Boolean(result.hasMore),
        revision: result.revision,
        legacy: false,
    };
}

function validateCsvSaveResult(result: CsvSaveResult, path: string): CsvSaveResult {
    if (!result || result.path !== path || typeof result.revision !== 'string' ||
        result.revision.length === 0 || !Number.isSafeInteger(result.totalRows) || result.totalRows < 0) {
        throw new Error('Backend returned an invalid CSV save result');
    }
    return result;
}

function validateCsvRecords(header: unknown, rows: unknown, limit: number): asserts rows is string[][] {
    if (!Array.isArray(header) || header.length === 0 || header.length > CSV_MAX_FIELDS ||
        !header.every((cell) => typeof cell === 'string') ||
        !Array.isArray(rows) || rows.length > limit) {
        throw new Error('Backend returned invalid CSV records');
    }
    if (!header.some((cell) => cell.length > 0)) {
        throw new Error('Backend returned an empty CSV header');
    }
    const width = header.length;
    validateCsvRecordBytes(header);
    for (const row of rows) {
        if (!Array.isArray(row) || row.length !== width || !row.every((cell) => typeof cell === 'string')) {
            throw new Error('Backend returned inconsistent CSV records');
        }
        validateCsvRecordBytes(row);
    }
    if (encodedTransferSize(header, rows) > CSV_MAX_PAGE_TRANSFER_BYTES) {
        throw new Error(`CSV page exceeds the ${CSV_MAX_PAGE_TRANSFER_BYTES}-byte transfer limit`);
    }
}

async function loadLegacyCsvPage(
    api: WailsAppAPI,
    request: CsvPageRequest,
): Promise<CsvPageResult> {
    // GetCsvFile returns its entire payload before frontend code can impose a
    // bound．Only old backends use this path，and retained/rendered data is
    // rejected above 200 records．
    const records = await api.GetCsvFile(request.path);
    if (!Array.isArray(records) || records.length === 0 || records.length > CSV_LEGACY_MAX_RECORDS) {
        throw new Error(`Legacy CSV read is limited to ${CSV_LEGACY_MAX_RECORDS} records`);
    }
    const header = records[0];
    const allRows = records.slice(1);
    const start = Math.min((request.page - 1) * request.limit, allRows.length);
    if ((request.page > 1 && allRows.length === 0) || (allRows.length > 0 && start >= allRows.length)) {
        throw new Error('CSV page is outside the available legacy rows');
    }
    const rows = allRows.slice(start, start + request.limit);
    validateCsvRecords(header, rows, request.limit);
    return {
        path: request.path,
        header: header.map(String),
        rows: rows.map((row) => row.map(String)),
        page: request.page,
        limit: request.limit,
        totalRows: allRows.length,
        hasMore: start + rows.length < allRows.length,
        revision: '',
        legacy: true,
    };
}

function encodedTransferSize(header: unknown, rows: unknown): number {
    return new TextEncoder().encode(JSON.stringify({ header, rows })).byteLength;
}

function validateCsvRecordBytes(record: string[]): void {
    let bytes = Math.max(record.length - 1, 0);
    for (const cell of record) {
        const cellBytes = new TextEncoder().encode(cell).byteLength;
        if (cellBytes > CSV_MAX_CELL_BYTES) {
            throw new Error(`CSV cell exceeds the ${CSV_MAX_CELL_BYTES}-byte limit`);
        }
        bytes += cellBytes;
    }
    if (bytes > CSV_MAX_RECORD_BYTES) {
        throw new Error(`CSV record exceeds the ${CSV_MAX_RECORD_BYTES}-byte limit`);
    }
}
