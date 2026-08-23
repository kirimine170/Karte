import { describe, expect, it, vi } from 'vitest';
import type { WailsAppAPI } from '../../types/wails-api';
import {
    importDroppedMediaFile,
    LEGACY_MEDIA_IMPORT_MAX_BYTES,
    MEDIA_IMPORT_CHUNK_BYTES,
    mediaArrayBufferToBase64,
    MediaImportTransferManager,
} from '../media-import';

function copyArrayBuffer(data: Uint8Array): ArrayBuffer {
    const copy = new Uint8Array(data.length);
    copy.set(data);
    return copy.buffer;
}

function fakeFile(data: Uint8Array, name = 'report.pdf'): {
    file: File;
    fullArrayBuffer: ReturnType<typeof vi.fn>;
    slices: Array<[number, number]>;
} {
    const fullArrayBuffer = vi.fn(async () => {
        throw new Error('full-file arrayBuffer must not be called');
    });
    const slices: Array<[number, number]> = [];
    const file = {
        name,
        size: data.length,
        arrayBuffer: fullArrayBuffer,
        slice(start = 0, end = data.length) {
            slices.push([start, end]);
            return {
                arrayBuffer: async () => copyArrayBuffer(data.slice(start, end)),
            };
        },
    } as unknown as File;
    return { file, fullArrayBuffer, slices };
}

function chunkAPI(overrides: Partial<WailsAppAPI> = {}): WailsAppAPI {
    return {
        BeginMediaImport: vi.fn(async () => ({ id: 'session-1', chunkSize: 7, maxBytes: 1024 })),
        AppendMediaImportChunk: vi.fn(async (_id: string, offset: number, encoded: string) => offset + atob(encoded).length),
        FinishMediaImport: vi.fn(async () => 'content/imported.pdf'),
        AbortMediaImport: vi.fn(async () => undefined),
        ImportAudioFile: vi.fn(async () => ''),
        ImportAudioBase64: vi.fn(async () => ''),
        ImportImageFile: vi.fn(async () => ''),
        ImportImageBase64: vi.fn(async () => ''),
        ImportPdfFile: vi.fn(async () => ''),
        ImportPdfBase64: vi.fn(async () => ''),
        ImportCsvFile: vi.fn(async () => ''),
        ImportCsvBase64: vi.fn(async () => ''),
        ...overrides,
    } as unknown as WailsAppAPI;
}

describe('MediaImportTransferManager', () => {
    it('uses ordered bounded slices and does not abort after Finish succeeds', async () => {
        const data = Uint8Array.from({ length: 20 }, (_, index) => index);
        const { file, fullArrayBuffer, slices } = fakeFile(data);
        const received: number[] = [];
        const offsets: number[] = [];
        const api = chunkAPI({
            AppendMediaImportChunk: vi.fn(async (_id, offset, encoded) => {
                const decoded = Uint8Array.from(atob(encoded), (character) => character.charCodeAt(0));
                offsets.push(offset);
                received.push(...decoded);
                expect(decoded.length).toBeLessThanOrEqual(7);
                return offset + decoded.length;
            }),
        });
        const transfers = new MediaImportTransferManager(api);

        await expect(transfers.importFile('pdf', file, 'fallback.pdf')).resolves.toBe('content/imported.pdf');

        expect(fullArrayBuffer).not.toHaveBeenCalled();
        expect(slices).toEqual([[0, 7], [7, 14], [14, 20]]);
        expect(offsets).toEqual([0, 7, 14]);
        expect(received).toEqual(Array.from(data));
        expect(api.FinishMediaImport).toHaveBeenCalledOnce();
        expect(api.AbortMediaImport).not.toHaveBeenCalled();
    });

    it('caps a backend-advertised chunk and avoids call-stack spreading', async () => {
        const data = new Uint8Array(MEDIA_IMPORT_CHUNK_BYTES + 5);
        data.fill(0xa5);
        const { file, slices } = fakeFile(data);
        const api = chunkAPI({
            BeginMediaImport: vi.fn(async () => ({
                id: 'session-large-advertisement',
                chunkSize: MEDIA_IMPORT_CHUNK_BYTES * 4,
                maxBytes: data.length,
            })),
        });
        const transfers = new MediaImportTransferManager(api);

        await transfers.importFile('pdf', file, 'fallback.pdf');

        expect(slices).toEqual([[0, MEDIA_IMPORT_CHUNK_BYTES], [MEDIA_IMPORT_CHUNK_BYTES, data.length]]);
        const encoded = mediaArrayBufferToBase64(copyArrayBuffer(data.slice(0, MEDIA_IMPORT_CHUNK_BYTES)));
        expect(atob(encoded)).toHaveLength(MEDIA_IMPORT_CHUNK_BYTES);
    });

    it('aborts exactly once when a middle Append fails', async () => {
        const { file } = fakeFile(Uint8Array.from({ length: 20 }, (_, index) => index));
        let calls = 0;
        const api = chunkAPI({
            AppendMediaImportChunk: vi.fn(async (_id, offset, encoded) => {
                calls += 1;
                if (calls === 2) {
                    throw new Error('injected append failure');
                }
                return offset + atob(encoded).length;
            }),
        });
        const transfers = new MediaImportTransferManager(api);

        await expect(transfers.importFile('pdf', file, 'fallback.pdf')).rejects.toThrow('injected append failure');
        transfers.destroy();

        expect(api.AbortMediaImport).toHaveBeenCalledTimes(1);
        expect(api.AbortMediaImport).toHaveBeenCalledWith('session-1');
        expect(api.FinishMediaImport).not.toHaveBeenCalled();
    });

    it('aborts exactly once when destroyed during a chunk read', async () => {
        let signalRead!: () => void;
        const readStarted = new Promise<void>((resolve) => {
            signalRead = resolve;
        });
        let deliverBuffer!: (buffer: ArrayBuffer) => void;
        const deferredRead = new Promise<ArrayBuffer>((resolve) => {
            deliverBuffer = resolve;
        });
        const file = {
            name: 'slow.pdf',
            size: 4,
            arrayBuffer: vi.fn(() => Promise.reject(new Error('full read'))),
            slice: vi.fn(() => ({
                arrayBuffer: () => {
                    signalRead();
                    return deferredRead;
                },
            })),
        } as unknown as File;
        const api = chunkAPI();
        const transfers = new MediaImportTransferManager(api);

        const importing = transfers.importFile('pdf', file, 'fallback.pdf');
        await readStarted;
        transfers.destroy();
        transfers.destroy();
        deliverBuffer(copyArrayBuffer(Uint8Array.from([1, 2, 3, 4])));

        await expect(importing).rejects.toMatchObject({ name: 'AbortError' });
        expect(api.AbortMediaImport).toHaveBeenCalledTimes(1);
        expect(api.AppendMediaImportChunk).not.toHaveBeenCalled();
        expect(api.FinishMediaImport).not.toHaveBeenCalled();
    });

    it('preserves the committed path when Finish wins a destroy race', async () => {
        const { file } = fakeFile(Uint8Array.from([1, 2, 3, 4]));
        let transfers!: MediaImportTransferManager;
        const api = chunkAPI({
            BeginMediaImport: vi.fn(async () => ({ id: 'finish-wins', chunkSize: 4, maxBytes: 4 })),
            FinishMediaImport: vi.fn(async () => {
                transfers.destroy();
                return 'content/committed.pdf';
            }),
        });
        transfers = new MediaImportTransferManager(api);

        await expect(transfers.importFile('pdf', file, 'fallback.pdf')).resolves.toBe('content/committed.pdf');
        expect(api.FinishMediaImport).toHaveBeenCalledOnce();
        expect(api.AbortMediaImport).toHaveBeenCalledTimes(1);
    });

    it('keeps the Finish error when Abort wins a destroy race', async () => {
        const { file } = fakeFile(Uint8Array.from([1, 2, 3, 4]));
        let transfers!: MediaImportTransferManager;
        const api = chunkAPI({
            BeginMediaImport: vi.fn(async () => ({ id: 'abort-wins', chunkSize: 4, maxBytes: 4 })),
            FinishMediaImport: vi.fn(async () => {
                transfers.destroy();
                throw new Error('media import session is closed');
            }),
        });
        transfers = new MediaImportTransferManager(api);

        await expect(transfers.importFile('pdf', file, 'fallback.pdf')).rejects.toThrow('media import session is closed');
        expect(api.FinishMediaImport).toHaveBeenCalledOnce();
        expect(api.AbortMediaImport).toHaveBeenCalledTimes(1);
    });

    it('uses a native path before Begin or browser reads', async () => {
        const { file, fullArrayBuffer, slices } = fakeFile(Uint8Array.from([1, 2, 3]), 'voice.mp3');
        Object.defineProperty(file, 'path', { value: '/native/voice.mp3' });
        const api = chunkAPI({
            ImportAudioFile: vi.fn(async () => 'data/audio/voice.mp3'),
        });
        const transfers = new MediaImportTransferManager(api);

        await expect(importDroppedMediaFile(api, transfers, 'audio', file, 'fallback.mp3')).resolves.toBe('data/audio/voice.mp3');

        expect(api.ImportAudioFile).toHaveBeenCalledWith('/native/voice.mp3');
        expect(api.BeginMediaImport).not.toHaveBeenCalled();
        expect(fullArrayBuffer).not.toHaveBeenCalled();
        expect(slices).toEqual([]);
    });

    it('uses the same native and bounded chunk paths for CSV imports', async () => {
        const native = fakeFile(Uint8Array.from([0x61, 0x2c, 0x62, 0x0a]), 'table.csv');
        Object.defineProperty(native.file, 'path', { value: '/native/table.csv' });
        const api = chunkAPI({
            ImportCsvFile: vi.fn(async () => 'data/csv/table.csv'),
            FinishMediaImport: vi.fn(async () => 'data/csv/table.csv'),
        });
        const transfers = new MediaImportTransferManager(api);

        await expect(importDroppedMediaFile(api, transfers, 'csv', native.file, 'fallback.csv'))
            .resolves.toBe('data/csv/table.csv');
        expect(api.ImportCsvFile).toHaveBeenCalledWith('/native/table.csv');
        expect(api.BeginMediaImport).not.toHaveBeenCalled();
        expect(native.fullArrayBuffer).not.toHaveBeenCalled();

        const webview = fakeFile(Uint8Array.from([0x61, 0x2c, 0x62, 0x0a]), 'webview.csv');
        await expect(importDroppedMediaFile(api, transfers, 'csv', webview.file, 'fallback.csv'))
            .resolves.toBe('data/csv/table.csv');
        expect(webview.fullArrayBuffer).not.toHaveBeenCalled();
        expect(webview.slices).toEqual([[0, 4]]);
        expect(api.FinishMediaImport).toHaveBeenCalledOnce();
    });

    it('falls back only for an old backend and guards size before a full read', async () => {
        const data = Uint8Array.from([0x25, 0x50, 0x44, 0x46]);
        const fullArrayBuffer = vi.fn(async () => copyArrayBuffer(data));
        const legacy = vi.fn(async () => 'content/legacy.pdf');
        const api = chunkAPI({
            BeginMediaImport: undefined,
            AppendMediaImportChunk: undefined,
            FinishMediaImport: undefined,
            AbortMediaImport: undefined,
            ImportPdfBase64: legacy,
        });
        const file = { name: 'legacy.pdf', size: data.length, arrayBuffer: fullArrayBuffer } as unknown as File;
        const transfers = new MediaImportTransferManager(api);

        await expect(transfers.importFile('pdf', file, 'fallback.pdf')).resolves.toBe('content/legacy.pdf');
        expect(fullArrayBuffer).toHaveBeenCalledOnce();
        expect(legacy).toHaveBeenCalledWith('legacy.pdf', 'JVBERg==');

        const oversizedRead = vi.fn(async () => new ArrayBuffer(0));
        const oversized = {
            name: 'oversized.pdf',
            size: LEGACY_MEDIA_IMPORT_MAX_BYTES + 1,
            arrayBuffer: oversizedRead,
        } as unknown as File;
        await expect(transfers.importFile('pdf', oversized, 'fallback.pdf')).rejects.toThrow('Legacy media import is limited');
        expect(oversizedRead).not.toHaveBeenCalled();
        expect(legacy).toHaveBeenCalledTimes(1);
    });

    it('rejects unsafe or non-positive sizes before Begin', async () => {
        const begin = vi.fn(async () => ({ id: 'unreachable', chunkSize: 1, maxBytes: 1 }));
        const api = chunkAPI({ BeginMediaImport: begin });
        const transfers = new MediaImportTransferManager(api);
        for (const size of [0, -1, Number.MAX_SAFE_INTEGER + 1, Number.NaN]) {
            const file = { name: 'invalid.pdf', size } as File;
            await expect(transfers.importFile('pdf', file, 'fallback.pdf')).rejects.toThrow('positive safe integer');
        }
        expect(begin).not.toHaveBeenCalled();
    });
});
