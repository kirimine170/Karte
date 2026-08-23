import { afterEach, describe, expect, it, vi } from 'vitest';

import { EventLogger } from '../event-logger';

function deferred<T>() {
    let resolve!: (value: T) => void;
    let reject!: (reason?: unknown) => void;
    const promise = new Promise<T>((promiseResolve, promiseReject) => {
        resolve = promiseResolve;
        reject = promiseReject;
    });
    return { promise, resolve, reject };
}

describe('EventLogger', () => {
    afterEach(() => {
        vi.useRealTimers();
        vi.restoreAllMocks();
    });

    it('disables debug hot-path logs in production and applies explicit levels to storage and console', () => {
        const warn = vi.spyOn(console, 'warn').mockImplementation(() => undefined);
        const info = vi.spyOn(console, 'info').mockImplementation(() => undefined);
        const error = vi.spyOn(console, 'error').mockImplementation(() => undefined);
        const logger = new EventLogger({ mode: 'production' });

        logger.log('Editor', 'editor-input');
        logger.log('Editor', 'layout-update');
        logger.log('OverlayHost', 'drag-over');
        logger.log('App', 'init');
        logger.log('Sidebar', 'file-load-success');
        logger.log('App', 'save-success');
        logger.log('Sidebar', 'file-select-cancelled');
        logger.log('App', 'printout-ready-timeout');
        logger.log('App', 'save-error');

        expect(logger.getLogs().map(({ action, level }) => ({ action, level }))).toEqual([
            { action: 'init', level: 'info' },
            { action: 'file-load-success', level: 'info' },
            { action: 'save-success', level: 'info' },
            { action: 'file-select-cancelled', level: 'warn' },
            { action: 'printout-ready-timeout', level: 'warn' },
            { action: 'save-error', level: 'error' },
        ]);
        expect(info).not.toHaveBeenCalled();
        expect(warn).toHaveBeenCalledTimes(2);
        expect(error).toHaveBeenCalledOnce();

        logger.setLevel('debug');
        logger.setConsoleLevel('off');
        logger.debug('Editor', 'input-enabled');
        expect(logger.getLogsByAction('input-enabled')).toHaveLength(1);
        expect(warn).toHaveBeenCalledTimes(2);
    });

    it('acknowledges only the successful snapshot and retains logs added while saving', async () => {
        const firstSave = deferred<boolean>();
        const save = vi
            .fn<(payload: string) => Promise<boolean>>()
            .mockImplementationOnce(() => firstSave.promise)
            .mockResolvedValueOnce(true);
        const logger = new EventLogger({ mode: 'test', api: { SaveEventLogs: save } });

        logger.log('Editor', 'first');
        const saving = logger.saveToBackend();
        expect(save).toHaveBeenCalledOnce();
        logger.log('Editor', 'during-save');

        firstSave.resolve(true);
        await expect(saving).resolves.toBe(true);
        expect(logger.getLogs().map(({ action }) => action)).toEqual(['during-save']);
        expect(JSON.parse(save.mock.calls[0]![0])).toMatchObject([{ action: 'first' }]);

        await expect(logger.saveToBackend()).resolves.toBe(true);
        expect(save).toHaveBeenCalledTimes(2);
        expect(JSON.parse(save.mock.calls[1]![0])).toMatchObject([{ action: 'during-save' }]);
        expect(logger.getLogCount()).toBe(0);
    });

    it('retains the complete snapshot and concurrent additions when persistence fails', async () => {
        const failedSave = deferred<boolean>();
        const save = vi
            .fn<(payload: string) => Promise<boolean>>()
            .mockImplementationOnce(() => failedSave.promise)
            .mockResolvedValueOnce(true);
        const logger = new EventLogger({ mode: 'test', api: { SaveEventLogs: save } });

        logger.log('Editor', 'before-failure');
        const saving = logger.saveToBackend();
        logger.log('Editor', 'during-failure');
        failedSave.resolve(false);

        await expect(saving).resolves.toBe(false);
        expect(logger.getLogs().map(({ action }) => action)).toEqual([
            'before-failure',
            'during-failure',
        ]);

        await expect(logger.saveToBackend()).resolves.toBe(true);
        expect(JSON.parse(save.mock.calls[1]![0])).toMatchObject([
            { action: 'before-failure' },
            { action: 'during-failure' },
        ]);
    });

    it('coalesces duplicate saves while one snapshot is in flight', async () => {
        const result = deferred<boolean>();
        const save = vi.fn<(payload: string) => Promise<boolean>>(() => result.promise);
        const logger = new EventLogger({ mode: 'test', api: { SaveEventLogs: save } });
        logger.log('App', 'queued');

        const first = logger.saveToBackend();
        const duplicate = logger.saveToBackend();
        expect(first).toBe(duplicate);
        expect(save).toHaveBeenCalledOnce();

        result.resolve(true);
        await expect(duplicate).resolves.toBe(true);
        expect(logger.getLogCount()).toBe(0);
    });

    it('normalizes oversized and circular state without blocking later logs', async () => {
        const save = vi.fn<(payload: string) => Promise<boolean>>().mockResolvedValue(true);
        const logger = new EventLogger({ mode: 'test', api: { SaveEventLogs: save } });
        const circular: { self?: unknown } = {};
        circular.self = circular;

        logger.log('Editor', 'oversized-state', { text: 'x'.repeat(70 << 10) });
        logger.log('Editor', 'circular-state', circular);
        logger.log('Editor', 'normal-state', { value: 42 });

        await expect(logger.saveToBackend()).resolves.toBe(true);
        const payload = JSON.parse(save.mock.calls[0]![0]);
        expect(payload).toMatchObject([
            { action: 'oversized-state', state: { __eventLogState: 'truncated' } },
            { action: 'circular-state', state: { __eventLogState: 'unserializable' } },
            { action: 'normal-state', state: { value: 42 } },
        ]);
        expect(payload[0].state.originalBytes).toBeGreaterThan(64 << 10);
        expect(logger.getLogCount()).toBe(0);
    });

    it('sends bounded prefix batches and drains every batch during destroy', async () => {
        const save = vi.fn<(payload: string) => Promise<boolean>>().mockResolvedValue(true);
        const logger = new EventLogger({
            mode: 'test',
            api: { SaveEventLogs: save },
            maxBatchBytes: 250,
            maxBatchEntries: 2,
        });
        for (let index = 1; index <= 5; index++) {
            logger.log('Editor', `batch-${index}`, { text: 'x'.repeat(80) });
        }

        await expect(logger.destroy()).resolves.toBe(true);
        expect(save.mock.calls.length).toBeGreaterThan(1);
        const actions: string[] = [];
        for (const [payload] of save.mock.calls) {
            expect(new TextEncoder().encode(payload).byteLength).toBeLessThanOrEqual(250);
            const entries = JSON.parse(payload);
            expect(entries.length).toBeLessThanOrEqual(2);
            actions.push(...entries.map((entry: { action: string }) => entry.action));
        }
        expect(actions).toEqual(['batch-1', 'batch-2', 'batch-3', 'batch-4', 'batch-5']);
        expect(logger.getLogCount()).toBe(0);
    });

    it('normalizes oversized fields on UTF-8 boundaries before persistence', async () => {
        const save = vi.fn<(payload: string) => Promise<boolean>>().mockResolvedValue(true);
        const logger = new EventLogger({ mode: 'test', api: { SaveEventLogs: save } });

        logger.log('界'.repeat(200), `${'🙂'.repeat(200)}-save-error`);
        await expect(logger.saveToBackend()).resolves.toBe(true);

        const [entry] = JSON.parse(save.mock.calls[0]![0]);
        expect(new TextEncoder().encode(entry.component).byteLength).toBeLessThanOrEqual(192);
        expect(new TextEncoder().encode(entry.action).byteLength).toBeLessThanOrEqual(192);
        expect(entry.component).toContain('[truncated]');
        expect(entry.action).toContain('[truncated]');
        expect(entry.component).not.toContain('�');
        expect(entry.action).not.toContain('�');
        expect(entry.level).toBe('error');
    });

    it('keeps default snapshots below the backend payload limit', async () => {
        const save = vi.fn<(payload: string) => Promise<boolean>>().mockResolvedValue(true);
        const logger = new EventLogger({ mode: 'test', api: { SaveEventLogs: save } });
        for (let index = 0; index < 10000; index++) {
            logger.log('Editor', `entry-${index}`, { value: 'x'.repeat(100) });
        }

        await expect(logger.saveToBackend()).resolves.toBe(true);
        const payload = save.mock.calls[0]![0];
        expect(new TextEncoder().encode(payload).byteLength).toBeLessThanOrEqual(512 << 10);
        expect(new TextEncoder().encode(payload).byteLength).toBeLessThan(8 << 20);
        expect(JSON.parse(payload)).toHaveLength(1000);
        expect(logger.getLogCount()).toBe(9000);
    });

    it('keeps one autosave timer and performs no timer or logging work after destroy', async () => {
        vi.useFakeTimers();
        const autoSave = deferred<boolean>();
        const save = vi.fn<(payload: string) => Promise<boolean>>(() => autoSave.promise);
        const logger = new EventLogger({ mode: 'test', api: { SaveEventLogs: save } });

        logger.startAutoSave(1000);
        logger.startAutoSave(1000);
        expect(vi.getTimerCount()).toBe(1);
        logger.log('App', 'autosave');
        await vi.advanceTimersByTimeAsync(1000);
        expect(save).toHaveBeenCalledOnce();
        await vi.advanceTimersByTimeAsync(3000);
        expect(save).toHaveBeenCalledOnce();

        autoSave.resolve(true);
        await expect(logger.destroy()).resolves.toBe(true);
        expect(vi.getTimerCount()).toBe(0);
        logger.log('App', 'after-destroy');
        await vi.advanceTimersByTimeAsync(5000);
        expect(save).toHaveBeenCalledOnce();
        expect(logger.getLogsByAction('after-destroy')).toHaveLength(0);
    });
});
