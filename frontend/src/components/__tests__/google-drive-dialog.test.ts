import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import page from '../../../index.html?raw';
import { GoogleDriveDialog } from '../google-drive-dialog';
import type { GoogleDriveDocument, GoogleDriveKind, WailsAppAPI, WailsRuntimeAPI } from '../../types/wails-api';

const account = { displayName: 'Test account', emailAddress: 'test@example.com' };
const disconnected = { displayName: '', emailAddress: '' };
const doc: GoogleDriveDocument = {
    file: { id: 'drive-id', name: 'note.md', kind: 'markdown', mimeType: 'text/markdown', capabilities: { canEdit: true } },
    checksum: 'remote-checksum',
    content: 'first line\r\nsecond line\r\n',
};

const element = <T extends HTMLElement = HTMLElement>(id: string) => document.getElementById(id) as T;
const click = (id: string) => element<HTMLButtonElement>(id).click();
const enter = (id: string, text: string) => {
    const field = element<HTMLInputElement | HTMLTextAreaElement>(id);
    field.value = text;
    field.dispatchEvent(new Event('input'));
};

describe('Google Drive dialog', () => {
    let component: GoogleDriveDialog;
    let currentAccount = disconnected;
    const browserOpenURL = vi.fn();
    const api = {
        GoogleDriveAccount: vi.fn(async () => currentAccount),
        ConnectGoogleDrive: vi.fn(async () => currentAccount = account),
        PickGoogleDriveFile: vi.fn(async () => ({ account: currentAccount = account, id: doc.file.id })),
        CancelGoogleDriveAuthorization: vi.fn(async () => {}),
        DisconnectGoogleDrive: vi.fn(async () => { currentAccount = disconnected; }),
        ListGoogleDriveFiles: vi.fn(async () => [doc.file]),
        OpenGoogleDriveFolder: vi.fn(async () => {}),
        LoadGoogleDriveFile: vi.fn(async () => doc),
        CreateGoogleDriveFile: vi.fn(async (kind: GoogleDriveKind, name: string, content: string) => ({
            ...doc, file: { ...doc.file, kind, name }, content,
        })),
        SaveGoogleDriveFile: vi.fn(async (_id: string, _checksum: string, content: string) => ({ ...doc, content })),
    };

    beforeEach(() => {
        vi.clearAllMocks();
        currentAccount = disconnected;
        const parsed = new DOMParser().parseFromString(page, 'text/html');
        document.body.innerHTML = '';
        document.body.append(parsed.getElementById('googleDriveBtn')!, parsed.getElementById('googleDriveDialog')!);
        const dialog = element<HTMLDialogElement>('googleDriveDialog');
        dialog.showModal = vi.fn(() => { dialog.open = true; });
        dialog.close = vi.fn(() => { dialog.open = false; });
        component = new GoogleDriveDialog(
            api as unknown as WailsAppAPI,
            { BrowserOpenURL: browserOpenURL } as unknown as WailsRuntimeAPI,
        );
        component.init();
    });

    afterEach(() => {
        component.destroy();
        vi.restoreAllMocks();
    });

    it('connects, creates Markdown and opens the same file in Drive', async () => {
        click('googleDriveBtn');
        await vi.waitFor(() => expect(element<HTMLButtonElement>('googleDriveConnect').disabled).toBe(false));
        click('googleDriveConnect');
        await vi.waitFor(() => expect(element('googleDriveAccount').textContent).toBe(account.emailAddress));

        await vi.waitFor(() => expect(element<HTMLInputElement>('googleDriveName').readOnly).toBe(false));
        enter('googleDriveName', 'created.md');
        enter('googleDriveEditor', '# Test');
        element('googleDriveEditor').dispatchEvent(new KeyboardEvent('keydown', { key: 's', ctrlKey: true, bubbles: true }));
        await vi.waitFor(() => expect(element('googleDriveStatus').textContent).toBe('Driveに保存しました'));
        expect(api.CreateGoogleDriveFile).toHaveBeenCalledWith('markdown', 'created.md', '# Test');
        expect(api.SaveGoogleDriveFile).not.toHaveBeenCalled();

        click('googleDriveOpenInBrowser');
        expect(browserOpenURL).toHaveBeenCalledWith('https://drive.google.com/open?id=drive-id');
        click('googleDriveFolder');
        await vi.waitFor(() => expect(element('googleDriveStatus').textContent).toBe('ブラウザでKarteフォルダを開きました'));
        expect(api.OpenGoogleDriveFolder).toHaveBeenCalledOnce();
        click('googleDriveDisconnect');
        await vi.waitFor(() => expect(element('googleDriveAccount').textContent).toBe('未接続'));
    });

    it('opens a Picker selection and preserves the draft when the server rejects an update', async () => {
        click('googleDriveBtn');
        await vi.waitFor(() => expect(element<HTMLButtonElement>('googleDrivePick').disabled).toBe(false));
        click('googleDrivePick');
        await vi.waitFor(() => expect(element<HTMLTextAreaElement>('googleDriveEditor').value).toBe('first line\nsecond line\n'));
        await vi.waitFor(() => expect(element<HTMLTextAreaElement>('googleDriveEditor').readOnly).toBe(false));
        expect(api.LoadGoogleDriveFile).toHaveBeenCalledWith(doc.file.id);

        api.SaveGoogleDriveFile.mockRejectedValueOnce('Drive上の内容が変わりました');
        enter('googleDriveEditor', 'first line\nchanged\n');
        click('googleDriveSave');
        await vi.waitFor(() => expect(element('googleDriveStatus').textContent).toBe('Drive上の内容が変わりました'));
        expect(api.SaveGoogleDriveFile).toHaveBeenCalledWith(doc.file.id, doc.checksum, 'first line\r\nchanged\r\n');
        expect(element<HTMLTextAreaElement>('googleDriveEditor').value).toBe('first line\nchanged\n');
    });

    it('creates CSV as a native spreadsheet and uses the loaded tab version for updates', async () => {
        const sheet: GoogleDriveDocument = {
            file: { ...doc.file, id: 'sheet-id', kind: 'sheet', name: 'Inventory', mimeType: 'application/vnd.google-apps.spreadsheet' },
            checksum: 'sheet-version', content: '商品,数量\nノート,2\n', sheetTab: '在庫',
        };
        api.CreateGoogleDriveFile.mockResolvedValueOnce({ ...sheet, sheetTab: undefined });
        api.LoadGoogleDriveFile.mockResolvedValueOnce(sheet);
        api.SaveGoogleDriveFile.mockResolvedValueOnce({ ...sheet, content: '商品,数量\nノート,3\n' });
        click('googleDriveBtn');
        await vi.waitFor(() => expect(element<HTMLButtonElement>('googleDriveConnect').disabled).toBe(false));
        click('googleDriveConnect');
        await vi.waitFor(() => expect(element<HTMLButtonElement>('googleDriveNewSheet').disabled).toBe(false));
        click('googleDriveNewSheet');
        enter('googleDriveName', 'Inventory');
        enter('googleDriveEditor', sheet.content);
        click('googleDriveSave');
        await vi.waitFor(() => expect(element('googleDriveEditorLabel').textContent).toBe('CSV（在庫）'));
        expect(api.CreateGoogleDriveFile).toHaveBeenCalledWith('sheet', 'Inventory', sheet.content);
        expect(api.LoadGoogleDriveFile).toHaveBeenCalledWith('sheet-id');
        enter('googleDriveEditor', '商品,数量\nノート,3\n');
        click('googleDriveSave');
        await vi.waitFor(() => expect(api.SaveGoogleDriveFile).toHaveBeenCalledWith('sheet-id', 'sheet-version', '商品,数量\nノート,3\n'));
    });

    it('previews photos picked from Drive and replaces the same file with binary data', async () => {
        const photo: GoogleDriveDocument = {
            file: { ...doc.file, id: 'photo-id', kind: 'photo', name: 'photo.png', mimeType: 'image/png' },
            checksum: 'photo-version', content: 'YWJj',
        };
        api.PickGoogleDriveFile.mockImplementationOnce(async () => ({ account: currentAccount = account, id: photo.file.id }));
        api.LoadGoogleDriveFile.mockResolvedValueOnce(photo);
        api.SaveGoogleDriveFile.mockResolvedValueOnce({ ...photo, content: 'eHl6' });
        click('googleDriveBtn');
        await vi.waitFor(() => expect(element<HTMLButtonElement>('googleDrivePick').disabled).toBe(false));
        click('googleDrivePick');
        await vi.waitFor(() => expect(element<HTMLButtonElement>('googleDriveReplacePhoto').disabled).toBe(false));
        expect(element<HTMLImageElement>('googleDrivePhotoPreview').src).toBe('data:image/png;base64,YWJj');
        expect(element('googleDriveEditor').hidden).toBe(true);
        const input = element<HTMLInputElement>('googleDriveLocalFile');
        vi.spyOn(input, 'click').mockImplementation(() => {});
        click('googleDriveReplacePhoto');
        Object.defineProperty(input, 'files', { value: [new File(['xyz'], 'replacement.png', { type: 'image/png' })] });
        input.dispatchEvent(new Event('change'));
        await vi.waitFor(() => expect(element<HTMLImageElement>('googleDrivePhotoPreview').src).toBe('data:image/png;base64,eHl6'));
        click('googleDriveSave');
        await vi.waitFor(() => expect(api.SaveGoogleDriveFile).toHaveBeenCalledWith('photo-id', 'photo-version', 'eHl6'));
        expect(api.CreateGoogleDriveFile).not.toHaveBeenCalled();
    });
});
