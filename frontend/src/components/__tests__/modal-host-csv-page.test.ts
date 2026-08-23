import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ModalHost } from '../modal-host';
import { useModalStore, useUIStore } from '../../stores';
import type { CsvPageResult } from '../../types/wails-api';

function installCsvModalDOM(): void {
    document.body.innerHTML = `
        <div id="csvEditModal">
            <span id="csvEditFileName"></span>
            <table>
                <thead id="csvEditTableHead"></thead>
                <tbody id="csvEditTableBody"></tbody>
            </table>
            <button id="csvPrevPageBtn"></button>
            <span id="csvPageLabel"></span>
            <button id="csvNextPageBtn"></button>
            <button id="csvAddRowBtn"></button>
            <button id="csvAddColBtn"></button>
            <button id="csvDeleteRowBtn"></button>
            <button id="csvDeleteColBtn"></button>
            <button id="csvSaveBtn"></button>
            <button id="csvCancelBtn"></button>
        </div>
    `;
}

function pageResult(page: number, overrides: Partial<CsvPageResult> = {}): CsvPageResult {
    const result: CsvPageResult = {
        path: 'data/csv/test.csv',
        header: ['formula', 'markup'],
        rows: [],
        page,
        limit: 50,
        totalRows: 150,
        hasMore: page < 3,
        revision: 'revision-1',
        ...overrides,
    };
    if (!overrides.rows) {
        const start = (result.page - 1) * result.limit;
        const count = Math.min(result.limit, Math.max(result.totalRows - start, 0));
        result.rows = Array.from({ length: count }, (_, index) => [
            index === 0 ? '=SUM(A1:A2)' : `row-${page}-${index}`,
            index === 0 ? '<img src=x onerror=alert(1)>' : '',
        ]);
    }
    return result;
}

function deferred<T>(): {
    promise: Promise<T>;
    resolve: (value: T) => void;
    reject: (error: unknown) => void;
} {
    let resolve!: (value: T) => void;
    let reject!: (error: unknown) => void;
    const promise = new Promise<T>((done, fail) => {
        resolve = done;
        reject = fail;
    });
    return { promise, resolve, reject };
}

describe('ModalHost paged CSV editor', () => {
    beforeEach(() => {
        vi.spyOn(console, 'log').mockImplementation(() => {});
        installCsvModalDOM();
        useModalStore.getState().hideCsvEditModal();
        useUIStore.setState({ statusMessage: '', statusClearTimer: null });
    });

    afterEach(() => {
        useModalStore.getState().hideCsvEditModal();
        vi.restoreAllMocks();
    });

    it('renders only one page with text nodes and disables structural edits off the final page', () => {
        const host = new ModalHost({} as any);
        host.init();
        const rows = Array.from({ length: 50 }, (_, index) => [
            index === 0 ? '=HYPERLINK("https://example.invalid")' : `row-${index}`,
            index === 0 ? '<img src=x onerror=alert(1)>' : '',
        ]);

        useModalStore.getState().showCsvEditPage(pageResult(1, {
            rows,
            totalRows: 1000,
            hasMore: true,
        }));

        expect(document.querySelectorAll('#csvEditTableBody tr')).toHaveLength(50);
        expect(document.querySelector('#csvEditTableBody')?.textContent).toContain('=HYPERLINK');
        expect(document.querySelector('#csvEditTableBody')?.textContent).toContain('<img src=x');
        expect(document.querySelector('#csvEditTableBody img')).toBeNull();
        expect(document.getElementById('csvPageLabel')?.textContent).toBe('1 / 20（全1000行）');
        expect((document.getElementById('csvAddRowBtn') as HTMLButtonElement).disabled).toBe(true);
        expect((document.getElementById('csvDeleteRowBtn') as HTMLButtonElement).disabled).toBe(true);
        expect((document.getElementById('csvAddColBtn') as HTMLButtonElement).disabled).toBe(true);

        useModalStore.getState().showCsvEditPage(pageResult(1, {
            rows,
            totalRows: 50,
            hasMore: false,
        }));
        const addRow = document.getElementById('csvAddRowBtn') as HTMLButtonElement;
        expect(addRow.disabled).toBe(true);
        addRow.dispatchEvent(new MouseEvent('click', { bubbles: true }));
        expect(document.querySelectorAll('#csvEditTableBody tr')).toHaveLength(50);

        host.destroy();
    });

    it('refuses page navigation while contenteditable data is dirty', async () => {
        const api = {
            GetCsvPage: vi.fn(),
        } as any;
        const host = new ModalHost(api);
        host.init();
        useModalStore.getState().showCsvEditPage(pageResult(1));
        const cell = document.querySelector<HTMLTableCellElement>('#csvEditTableBody td');
        if (!cell) {
            throw new Error('expected a CSV cell');
        }
        cell.textContent = '=UNSAVED()';

        (document.getElementById('csvNextPageBtn') as HTMLButtonElement).click();

        expect(api.GetCsvPage).not.toHaveBeenCalled();
        expect(cell.textContent).toBe('=UNSAVED()');
        expect(useUIStore.getState().statusMessage).toContain('保存または取消');
        host.destroy();
    });

    it('keeps the newest reverse-order page load and ignores completion after destroy', async () => {
        const errorLog = vi.spyOn(console, 'error').mockImplementation(() => {});
        const pending = new Map<number, ReturnType<typeof deferred<CsvPageResult>>>();
        const api = {
            GetCsvPage: vi.fn((request) => {
                const operation = deferred<CsvPageResult>();
                pending.set(request.page, operation);
                return operation.promise;
            }),
        } as any;
        const host = new ModalHost(api);
        host.init();
        useModalStore.getState().showCsvEditPage(pageResult(2));

        (document.getElementById('csvNextPageBtn') as HTMLButtonElement).click();
        (document.getElementById('csvPrevPageBtn') as HTMLButtonElement).click();
        expect(api.GetCsvPage.mock.calls.map(([request]: any[]) => request.page)).toEqual([3, 1]);

        pending.get(1)?.resolve(pageResult(1, {
            rows: Array.from({ length: 50 }, (_, index) => index === 0
                ? ['newest', 'page']
                : [`newest-${index}`, 'page']),
        }));
        await vi.waitFor(() => expect(useModalStore.getState().csvEditModal.page).toBe(1));
        pending.get(3)?.reject(new Error('stale page failed'));
        await Promise.resolve();
        expect(useModalStore.getState().csvEditModal.data[1]).toEqual(['newest', 'page']);
        expect(errorLog).not.toHaveBeenCalled();

        useModalStore.getState().showCsvEditPage(pageResult(2));
        (document.getElementById('csvNextPageBtn') as HTMLButtonElement).click();
        host.destroy();
        pending.get(3)?.reject(new Error('destroyed page failed'));
        await Promise.resolve();
        await Promise.resolve();
        expect(useModalStore.getState().csvEditModal.page).toBe(2);
        expect(errorLog).not.toHaveBeenCalled();
    });

    it('keeps edits made during save，rebases the revision，and permits only one in-flight save', async () => {
        const save = deferred<any>();
        const api = {
            SaveCsvPage: vi.fn(() => save.promise),
        } as any;
        const host = new ModalHost(api);
        host.init();
        useModalStore.getState().showCsvEditPage(pageResult(1, {
            totalRows: 1,
            hasMore: false,
        }));

        const saveButton = document.getElementById('csvSaveBtn') as HTMLButtonElement;
        saveButton.click();
        saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }));
        await vi.waitFor(() => expect(api.SaveCsvPage).toHaveBeenCalledOnce());
        expect(saveButton.disabled).toBe(true);

        const firstCell = document.querySelector<HTMLTableCellElement>('#csvEditTableBody td');
        if (!firstCell) {
            throw new Error('expected a CSV cell');
        }
        firstCell.textContent = '=NEWER_EDIT()';
        (document.getElementById('csvAddRowBtn') as HTMLButtonElement).click();
        save.resolve({ path: 'data/csv/test.csv', revision: 'revision-2', totalRows: 1 });

        await vi.waitFor(() => expect(useModalStore.getState().csvEditModal.revision).toBe('revision-2'));
        expect(useModalStore.getState().csvEditModal.visible).toBe(true);
        expect(useModalStore.getState().csvEditModal.data[1]?.[0]).toBe('=NEWER_EDIT()');
        expect(useModalStore.getState().csvEditModal.totalRows).toBe(2);
        expect(document.getElementById('csvPageLabel')?.textContent).toBe('1 / 1（全2行）');
        expect(useUIStore.getState().statusMessage).toBe('');
        expect(saveButton.disabled).toBe(false);

        host.destroy();
    });

    it('reports a current save rejection while preserving edits made in flight', async () => {
        const errorLog = vi.spyOn(console, 'error').mockImplementation(() => {});
        const save = deferred<any>();
        const api = {
            SaveCsvPage: vi.fn(() => save.promise),
        } as any;
        const host = new ModalHost(api);
        host.init();
        useModalStore.getState().showCsvEditPage(pageResult(1, { totalRows: 1, hasMore: false }));

        (document.getElementById('csvSaveBtn') as HTMLButtonElement).click();
        await vi.waitFor(() => expect(api.SaveCsvPage).toHaveBeenCalledOnce());
        const cell = document.querySelector<HTMLTableCellElement>('#csvEditTableBody td');
        if (!cell) {
            throw new Error('expected a CSV cell');
        }
        cell.textContent = '=EDIT_AFTER_SAVE_STARTED()';
        save.reject(new Error('injected save failure'));

        await vi.waitFor(() => expect(useUIStore.getState().statusMessage).toContain('保存に失敗'));
        expect(errorLog).toHaveBeenCalledOnce();
        expect(cell.textContent).toBe('=EDIT_AFTER_SAVE_STARTED()');
        expect(useModalStore.getState().csvEditModal.visible).toBe(true);
        host.destroy();
    });

    it('keeps the editor open and requests reload after a committed durability error', async () => {
        vi.spyOn(console, 'error').mockImplementation(() => {});
        const api = {
            SaveCsvPage: vi.fn().mockRejectedValue(
                new Error('csv commit completed but durability is unconfirmed: directory Sync')
            ),
        } as any;
        const host = new ModalHost(api);
        host.init();
        useModalStore.getState().showCsvEditPage(pageResult(1, { totalRows: 1, hasMore: false }));

        (document.getElementById('csvSaveBtn') as HTMLButtonElement).click();

        await vi.waitFor(() => expect(useUIStore.getState().statusMessage).toContain('再読み込み'));
        expect(useModalStore.getState().csvEditModal.visible).toBe(true);
        expect(api.SaveCsvPage).toHaveBeenCalledOnce();
        host.destroy();
    });

    it('saves the visible page with its revision and does not close a newer modal', async () => {
        const save = deferred<any>();
        const api = {
            SaveCsvPage: vi.fn(() => save.promise),
        } as any;
        const host = new ModalHost(api);
        host.init();
        useModalStore.getState().showCsvEditPage(pageResult(1, {
            totalRows: 1,
            hasMore: false,
        }));

        (document.getElementById('csvSaveBtn') as HTMLButtonElement).click();
        await vi.waitFor(() => expect(api.SaveCsvPage).toHaveBeenCalledOnce());
        expect(api.SaveCsvPage).toHaveBeenCalledWith({
            path: 'data/csv/test.csv',
            revision: 'revision-1',
            page: 1,
            limit: 50,
            header: ['formula', 'markup'],
            rows: [['=SUM(A1:A2)', '<img src=x onerror=alert(1)>']],
        });

        useModalStore.getState().showCsvEditPage(pageResult(1, {
            path: 'data/csv/newer.csv',
            revision: 'newer-revision',
        }));
        save.resolve({ path: 'data/csv/test.csv', revision: 'revision-2', totalRows: 1 });
        await vi.waitFor(() => expect(useModalStore.getState().csvEditModal.filePath).toBe('data/csv/newer.csv'));
        expect(useModalStore.getState().csvEditModal.visible).toBe(true);

        host.destroy();
    });

    it('does not report a rejected save after destroy', async () => {
        const errorLog = vi.spyOn(console, 'error').mockImplementation(() => {});
        const save = deferred<any>();
        const api = {
            SaveCsvPage: vi.fn(() => save.promise),
        } as any;
        const host = new ModalHost(api);
        host.init();
        useModalStore.getState().showCsvEditPage(pageResult(1, { totalRows: 1, hasMore: false }));

        (document.getElementById('csvSaveBtn') as HTMLButtonElement).click();
        await vi.waitFor(() => expect(api.SaveCsvPage).toHaveBeenCalledOnce());
        host.destroy();
        save.reject(new Error('save failed after destroy'));
        await Promise.resolve();
        await Promise.resolve();

        expect(errorLog).not.toHaveBeenCalled();
        expect(useUIStore.getState().statusMessage).toBe('');
    });

    it('does not let a pre-cancel save resolve close the same identity after reopen', async () => {
        const oldSave = deferred<any>();
        const api = {
            SaveCsvPage: vi.fn()
                .mockImplementationOnce(() => oldSave.promise)
                .mockResolvedValueOnce({
                    path: 'data/csv/test.csv',
                    revision: 'revision-3',
                    totalRows: 1,
                }),
        } as any;
        const host = new ModalHost(api);
        host.init();
        const page = pageResult(1, { totalRows: 1, hasMore: false });
        useModalStore.getState().showCsvEditPage(page);

        (document.getElementById('csvSaveBtn') as HTMLButtonElement).click();
        await vi.waitFor(() => expect(api.SaveCsvPage).toHaveBeenCalledOnce());
        (document.getElementById('csvCancelBtn') as HTMLButtonElement).click();
        useModalStore.getState().showCsvEditPage(page);
        oldSave.resolve({ path: 'data/csv/test.csv', revision: 'revision-2', totalRows: 1 });

        await vi.waitFor(() => expect((document.getElementById('csvSaveBtn') as HTMLButtonElement).disabled).toBe(false));
        expect(useModalStore.getState().csvEditModal.visible).toBe(true);
        expect(useModalStore.getState().csvEditModal.revision).toBe('revision-1');
        expect(useUIStore.getState().statusMessage).toBe('');

        (document.getElementById('csvSaveBtn') as HTMLButtonElement).click();
        await vi.waitFor(() => expect(api.SaveCsvPage).toHaveBeenCalledTimes(2));
        await vi.waitFor(() => expect(useModalStore.getState().csvEditModal.visible).toBe(false));
        host.destroy();
    });

    it('does not report a pre-cancel save rejection after the same identity reopens', async () => {
        const errorLog = vi.spyOn(console, 'error').mockImplementation(() => {});
        const oldSave = deferred<any>();
        const api = {
            SaveCsvPage: vi.fn(() => oldSave.promise),
        } as any;
        const host = new ModalHost(api);
        host.init();
        const page = pageResult(1, { totalRows: 1, hasMore: false });
        useModalStore.getState().showCsvEditPage(page);

        (document.getElementById('csvSaveBtn') as HTMLButtonElement).click();
        await vi.waitFor(() => expect(api.SaveCsvPage).toHaveBeenCalledOnce());
        (document.getElementById('csvCancelBtn') as HTMLButtonElement).click();
        useModalStore.getState().showCsvEditPage(page);
        oldSave.reject(new Error('old save rejected'));

        await vi.waitFor(() => expect((document.getElementById('csvSaveBtn') as HTMLButtonElement).disabled).toBe(false));
        expect(useModalStore.getState().csvEditModal.visible).toBe(true);
        expect(useUIStore.getState().statusMessage).toBe('');
        expect(errorLog).not.toHaveBeenCalled();
        host.destroy();
    });
});
