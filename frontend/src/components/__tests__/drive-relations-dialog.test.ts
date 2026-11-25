import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import page from '../../../index.html?raw';
import { DriveRelationsDialog } from '../drive-relations-dialog';
import type { DriveRelationFile, DriveRelationSnapshot, WailsAppAPI, WailsRuntimeAPI } from '../../types/wails-api';

const graph = vi.hoisted(() => ({ on: vi.fn(), setData: vi.fn(), destroy: vi.fn() }));
vi.mock('../../graph-d3', () => ({ default: class {
    on = graph.on;
    setData = graph.setData;
    destroy = graph.destroy;
} }));

const account = { displayName: 'Test account', emailAddress: 'test@example.com', permissionId: 'member' };
const disconnected = { displayName: '', emailAddress: '' };
const folder = { id: 'karte', name: 'Karte', createdTime: '2026-11-25T00:00:00Z' };
const file = (id: string, kind: DriveRelationFile['kind'], tags: string[], linkIds: string[] = []): DriveRelationFile => ({
    id, title: id, name: id, kind, tags, linkIds, url: 'https://drive.google.com/open?id=' + id, legacy: '', unresolved: 0,
});
const snapshot: DriveRelationSnapshot = {
    folder,
    files: [
        { ...file('note', 'markdown', ['展示', '在庫'], ['sheet']), legacy: 'Karte tags: 展示, 在庫' },
        file('photo', 'photo', ['展示'], ['note']),
        file('sheet', 'sheet', ['在庫']),
    ],
    incomplete: 0,
    updatedAt: '2026-11-25T07:17:00Z',
};

const el = <T extends HTMLElement = HTMLElement>(name: string) => document.getElementById('driveRelations' + name) as T;
const click = (name: string) => el<HTMLButtonElement>(name).click();

describe('Drive relationship search', () => {
    let component: DriveRelationsDialog;
    let current = disconnected;
    const browser = vi.fn();
    const clipboard = vi.fn(async () => true);
    const api = {
        GoogleDriveSearchAccount: vi.fn(async () => current),
        ConnectGoogleDriveSearch: vi.fn(async () => current = account),
        CancelGoogleDriveSearchAuthorization: vi.fn(async () => {}),
        DisconnectGoogleDriveSearch: vi.fn(async () => { current = disconnected; }),
        ListGoogleDriveSearchFolders: vi.fn(async () => [folder]),
        RefreshGoogleDriveRelations: vi.fn(async (_id: string) => snapshot),
    };

    beforeEach(() => {
        vi.clearAllMocks();
        current = disconnected;
        localStorage.clear();
        const parsed = new DOMParser().parseFromString(page, 'text/html');
        document.body.replaceChildren(parsed.getElementById('driveRelationsBtn')!, parsed.getElementById('driveRelationsDialog')!);
        const dialog = el<HTMLDialogElement>('Dialog');
        dialog.showModal = () => { dialog.open = true; };
        dialog.close = () => {
            dialog.open = false;
            dialog.dispatchEvent(new Event('close'));
        };
        component = new DriveRelationsDialog(api as unknown as WailsAppAPI, {
            BrowserOpenURL: browser,
            ClipboardSetText: clipboard,
        } as unknown as WailsRuntimeAPI);
        component.init();
    });

    afterEach(() => {
        component.destroy();
        vi.useRealTimers();
    });

    it('finds notes, photos and sheets with exact tags and follows explicit links in both directions', async () => {
        click('Btn');
        await vi.waitFor(() => expect(el<HTMLButtonElement>('Connect').disabled).toBe(false));
        click('Connect');
        await vi.waitFor(() => expect(el<HTMLButtonElement>('Open').disabled).toBe(false));
        expect(api.RefreshGoogleDriveRelations).toHaveBeenCalledWith('karte');
        expect(el('Related').textContent).toContain('被リンク');
        expect(el('Related').textContent).toContain('リンク先');
        expect(graph.setData).toHaveBeenCalled();
        const tags = el('Tags').querySelectorAll('input');
        tags.forEach(input => {
            input.checked = true;
            input.dispatchEvent(new Event('change', { bubbles: true }));
        });
        expect([...el<HTMLSelectElement>('Files').options].map(option => option.value)).toEqual(['note']);
        click('Open');
        click('Copy');
        expect(browser).toHaveBeenCalledWith(snapshot.files[0]?.url);
        await vi.waitFor(() => expect(clipboard).toHaveBeenCalledWith('Karte tags: 展示, 在庫'));
    });

    it('requires explicit selection when the folder is ambiguous and clears results on lost access', async () => {
        current = account;
        api.ListGoogleDriveSearchFolders.mockResolvedValueOnce([folder, { ...folder, id: 'second' }]);
        click('Btn');
        await vi.waitFor(() => expect(el('Status').textContent).toContain('選択してください'));
        expect(api.RefreshGoogleDriveRelations).not.toHaveBeenCalled();
        const folders = el<HTMLSelectElement>('Folder');
        folders.value = 'karte';
        folders.dispatchEvent(new Event('change'));
        await vi.waitFor(() => expect(el<HTMLButtonElement>('Open').disabled).toBe(false));
        expect(localStorage.getItem('karte.driveRelations.folder.member')).toBe('karte');
        api.RefreshGoogleDriveRelations.mockRejectedValueOnce('Google Driveへの操作に失敗しました (HTTP 403)');
        click('Refresh');
        await vi.waitFor(() => expect(el('Status').textContent).toContain('403'));
        expect(el('Results').hidden).toBe(true);
        expect(el<HTMLSelectElement>('Files').length).toBe(0);
    });

    it('refreshes automatically while the dialog is open and stops when it closes', async () => {
        current = account;
        vi.useFakeTimers();
        click('Btn');
        await vi.advanceTimersByTimeAsync(0);
        expect(api.RefreshGoogleDriveRelations).toHaveBeenCalledTimes(1);
        await vi.advanceTimersByTimeAsync(60_000);
        expect(api.RefreshGoogleDriveRelations).toHaveBeenCalledTimes(2);
        click('Close');
        await vi.advanceTimersByTimeAsync(60_000);
        expect(api.RefreshGoogleDriveRelations).toHaveBeenCalledTimes(2);
    });
});
