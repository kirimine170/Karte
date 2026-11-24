import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import page from '../../../index.html?raw';
import { GoogleDriveDialog } from '../google-drive-dialog';
import type { GoogleDriveDocument, WailsAppAPI, WailsRuntimeAPI } from '../../types/wails-api';

const account = { displayName: 'Test account', emailAddress: 'test@example.com' };
const disconnected = { displayName: '', emailAddress: '' };
const doc: GoogleDriveDocument = {
    file: { id: 'drive-id', name: 'note.md', capabilities: { canEdit: true } },
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
        PickGoogleDriveMarkdown: vi.fn(async () => ({ account: currentAccount = account, id: doc.file.id })),
        CancelGoogleDriveAuthorization: vi.fn(async () => {}),
        DisconnectGoogleDrive: vi.fn(async () => { currentAccount = disconnected; }),
        ListGoogleDriveMarkdown: vi.fn(async () => [doc.file]),
        LoadGoogleDriveMarkdown: vi.fn(async () => doc),
        CreateGoogleDriveMarkdown: vi.fn(async (name: string, content: string) => ({ ...doc, file: { ...doc.file, name }, content })),
        SaveGoogleDriveMarkdown: vi.fn(async (_id: string, _checksum: string, content: string) => ({ ...doc, content })),
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
        expect(api.CreateGoogleDriveMarkdown).toHaveBeenCalledWith('created.md', '# Test');
        expect(api.SaveGoogleDriveMarkdown).not.toHaveBeenCalled();

        click('googleDriveOpenInBrowser');
        expect(browserOpenURL).toHaveBeenCalledWith('https://drive.google.com/open?id=drive-id');
        click('googleDriveDisconnect');
        await vi.waitFor(() => expect(element('googleDriveAccount').textContent).toBe('未接続'));
    });

    it('opens a Picker selection and preserves the draft when the server rejects an update', async () => {
        click('googleDriveBtn');
        await vi.waitFor(() => expect(element<HTMLButtonElement>('googleDrivePick').disabled).toBe(false));
        click('googleDrivePick');
        await vi.waitFor(() => expect(element<HTMLTextAreaElement>('googleDriveEditor').value).toBe('first line\nsecond line\n'));
        await vi.waitFor(() => expect(element<HTMLTextAreaElement>('googleDriveEditor').readOnly).toBe(false));
        expect(api.LoadGoogleDriveMarkdown).toHaveBeenCalledWith(doc.file.id);

        api.SaveGoogleDriveMarkdown.mockRejectedValueOnce('Drive上の内容が変わりました');
        enter('googleDriveEditor', 'first line\nchanged\n');
        click('googleDriveSave');
        await vi.waitFor(() => expect(element('googleDriveStatus').textContent).toBe('Drive上の内容が変わりました'));
        expect(api.SaveGoogleDriveMarkdown).toHaveBeenCalledWith(doc.file.id, doc.checksum, 'first line\r\nchanged\r\n');
        expect(element<HTMLTextAreaElement>('googleDriveEditor').value).toBe('first line\nchanged\n');
    });
});
