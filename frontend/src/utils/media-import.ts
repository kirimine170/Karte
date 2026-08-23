import type { MediaImportKind, MediaImportSession, WailsAppAPI } from '../types/wails-api';

export const MEDIA_IMPORT_CHUNK_BYTES = 256 * 1024;
export const LEGACY_MEDIA_IMPORT_MAX_BYTES = 64 * 1024 * 1024;

interface ActiveMediaImport {
    id: string;
    settled: boolean;
    abortPromise?: Promise<void>;
}

type ChunkMediaAPI = Required<Pick<
    WailsAppAPI,
    'BeginMediaImport' | 'AppendMediaImportChunk' | 'FinishMediaImport' | 'AbortMediaImport'
>>;

function mediaImportCancelled(): Error {
    const error = new Error('Media import was cancelled');
    error.name = 'AbortError';
    return error;
}

function hasChunkMediaAPI(api: WailsAppAPI): api is WailsAppAPI & ChunkMediaAPI {
    return typeof api.BeginMediaImport === 'function'
        && typeof api.AppendMediaImportChunk === 'function'
        && typeof api.FinishMediaImport === 'function'
        && typeof api.AbortMediaImport === 'function';
}

export function mediaArrayBufferToBase64(buffer: ArrayBuffer): string {
    const bytes = new Uint8Array(buffer);
    const parts: string[] = [];
    const blockBytes = 8 * 1024;
    for (let start = 0; start < bytes.length; start += blockBytes) {
        const end = Math.min(start + blockBytes, bytes.length);
        let block = '';
        for (let index = start; index < end; index += 1) {
            block += String.fromCharCode(bytes[index]);
        }
        parts.push(block);
    }
    return btoa(parts.join(''));
}

export async function importDroppedMediaFile(
    api: WailsAppAPI,
    transfers: MediaImportTransferManager,
    kind: MediaImportKind,
    file: File,
    fallbackName: string,
): Promise<string> {
    const nativePath = (file as File & { path?: string }).path;
    if (nativePath) {
        switch (kind) {
        case 'audio':
            return api.ImportAudioFile(nativePath);
        case 'image':
            return api.ImportImageFile(nativePath);
        case 'pdf':
            return api.ImportPdfFile(nativePath);
        case 'csv':
            return api.ImportCsvFile(nativePath);
        }
    }
    return transfers.importFile(kind, file, fallbackName);
}

export class MediaImportTransferManager {
    private readonly active = new Set<ActiveMediaImport>();
    private destroyed = false;

    constructor(private readonly api: WailsAppAPI) {}

    async importFile(kind: MediaImportKind, file: File, fallbackName: string): Promise<string> {
        if (this.destroyed) {
            throw mediaImportCancelled();
        }
        if (!Number.isSafeInteger(file.size) || file.size <= 0) {
            throw new Error('Media file size must be a positive safe integer');
        }
        if (hasChunkMediaAPI(this.api)) {
            return this.importChunked(this.api, kind, file, fallbackName);
        }
        return this.importLegacy(kind, file, fallbackName);
    }

    destroy(): void {
        if (this.destroyed) {
            return;
        }
        this.destroyed = true;
        for (const transfer of Array.from(this.active)) {
            void this.abortOnce(transfer);
        }
    }

    private async importChunked(
        api: WailsAppAPI & ChunkMediaAPI,
        kind: MediaImportKind,
        file: File,
        fallbackName: string,
    ): Promise<string> {
        const session = await api.BeginMediaImport(kind, file.name || fallbackName, file.size);
        const transfer: ActiveMediaImport = { id: session.id, settled: false };
        this.active.add(transfer);
        try {
            this.validateSession(session, file.size);
            if (this.destroyed) {
                throw mediaImportCancelled();
            }
            const chunkSize = Math.min(session.chunkSize, MEDIA_IMPORT_CHUNK_BYTES);
            let offset = 0;
            while (offset < file.size) {
                if (this.destroyed || transfer.settled) {
                    throw mediaImportCancelled();
                }
                const end = Math.min(offset + chunkSize, file.size);
                const chunk = file.slice(offset, end);
                if (typeof chunk.arrayBuffer !== 'function') {
                    throw new Error('This browser cannot read dropped file chunks');
                }
                const buffer = await chunk.arrayBuffer();
                if (buffer.byteLength !== end - offset) {
                    throw new Error(`Media chunk read ${buffer.byteLength} bytes，expected ${end - offset}`);
                }
                if (this.destroyed || transfer.settled) {
                    throw mediaImportCancelled();
                }
                const nextOffset = await api.AppendMediaImportChunk(
                    session.id,
                    offset,
                    mediaArrayBufferToBase64(buffer),
                );
                if (nextOffset !== end) {
                    throw new Error(`Media import offset advanced to ${nextOffset}，expected ${end}`);
                }
                offset = nextOffset;
            }
            if (this.destroyed || transfer.settled) {
                throw mediaImportCancelled();
            }
            const importedPath = await api.FinishMediaImport(session.id);
            transfer.settled = true;
            this.active.delete(transfer);
            return importedPath;
        } catch (error) {
            await this.abortOnce(transfer);
            throw error;
        }
    }

    private validateSession(session: MediaImportSession, fileSize: number): void {
        if (!session || typeof session.id !== 'string' || session.id.length === 0) {
            throw new Error('Backend returned an invalid media import session');
        }
        if (!Number.isSafeInteger(session.chunkSize) || session.chunkSize <= 0) {
            throw new Error('Backend returned an invalid media chunk size');
        }
        if (!Number.isSafeInteger(session.maxBytes) || session.maxBytes <= 0 || fileSize > session.maxBytes) {
            throw new Error(`Media file exceeds the backend limit of ${session.maxBytes} bytes`);
        }
    }

    private async abortOnce(transfer: ActiveMediaImport): Promise<void> {
        if (transfer.abortPromise) {
            await transfer.abortPromise;
            return;
        }
        if (transfer.settled) {
            return;
        }
        transfer.settled = true;
        this.active.delete(transfer);
        if (typeof this.api.AbortMediaImport !== 'function') {
            return;
        }
        transfer.abortPromise = (async () => {
            try {
                await this.api.AbortMediaImport!(transfer.id);
            } catch (error) {
                console.error('AbortMediaImport failed', error);
            }
        })();
        await transfer.abortPromise;
    }

    private async importLegacy(kind: MediaImportKind, file: File, fallbackName: string): Promise<string> {
        if (file.size > LEGACY_MEDIA_IMPORT_MAX_BYTES) {
            throw new Error(`Legacy media import is limited to ${LEGACY_MEDIA_IMPORT_MAX_BYTES} bytes`);
        }
        if (typeof file.arrayBuffer !== 'function') {
            throw new Error('This browser cannot read the dropped file');
        }
        const buffer = await file.arrayBuffer();
        if (buffer.byteLength > LEGACY_MEDIA_IMPORT_MAX_BYTES) {
            throw new Error(`Legacy media import is limited to ${LEGACY_MEDIA_IMPORT_MAX_BYTES} bytes`);
        }
        if (this.destroyed) {
            throw mediaImportCancelled();
        }
        const encoded = mediaArrayBufferToBase64(buffer);
        const filename = file.name || fallbackName;
        switch (kind) {
        case 'audio':
            return this.api.ImportAudioBase64(filename, encoded);
        case 'image':
            return this.api.ImportImageBase64(filename, encoded);
        case 'pdf':
            return this.api.ImportPdfBase64(filename, encoded);
        case 'csv':
            return this.api.ImportCsvBase64(filename, encoded);
        }
    }
}
