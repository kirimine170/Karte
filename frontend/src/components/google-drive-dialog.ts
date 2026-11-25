import type { GoogleDriveAccount, GoogleDriveDocument, GoogleDriveFile, GoogleDriveKind, WailsAppAPI, WailsRuntimeAPI } from '../types/wails-api';

const fileKindLabel: Record<GoogleDriveKind, string> = {
    markdown: 'Markdown',
    sheet: 'スプレッドシート',
    photo: '写真',
};
const photoFormats: Record<string, string> = { jpg: 'image/jpeg', jpeg: 'image/jpeg', png: 'image/png', gif: 'image/gif', webp: 'image/webp' };
const photoAccept = '.jpg,.jpeg,.png,.gif,.webp';
const maxPhoto = 5 * 1024 * 1024;
const maxCSV = 2 * 1024 * 1024;

export class GoogleDriveDialog {
    private readonly events = new AbortController();
    private readonly dialog = this.control<HTMLDialogElement>('googleDriveDialog');
    private readonly status = this.control('googleDriveStatus');
    private readonly files = this.control<HTMLSelectElement>('googleDriveFiles');
    private readonly name = this.control<HTMLInputElement>('googleDriveName');
    private readonly editor = this.control<HTMLTextAreaElement>('googleDriveEditor');
    private readonly localFile = this.control<HTMLInputElement>('googleDriveLocalFile');
    private readonly photoPreview = this.control<HTMLImageElement>('googleDrivePhotoPreview');
    private readonly accountLabel = this.control('googleDriveAccount');
    private account = '';
    private document: GoogleDriveDocument | null = null;
    private kind: GoogleDriveKind = 'markdown';
    private photoContent = '';
    private replacingPhoto = false;
    private busy = false;
    private authorizing = false;

    constructor(private readonly api: WailsAppAPI, private readonly runtime: WailsRuntimeAPI) {}

    init(): void {
        const launcher = this.control<HTMLButtonElement>('googleDriveBtn');
        if (typeof this.api.GoogleDriveAccount !== 'function') return;

        launcher.hidden = false;
        this.on('googleDriveBtn', async () => {
            this.dialog.showModal();
            await this.run(async () => {
                this.setAccount(await this.api.GoogleDriveAccount());
                if (this.account) await this.list();
                else this.status.textContent = '';
            });
        });
        this.on('googleDriveConnect', () => this.authorize(false));
        this.on('googleDrivePick', () => this.authorize(true));
        this.on('googleDriveCancelAuth', () => this.api.CancelGoogleDriveAuthorization());
        this.on('googleDriveDisconnect', async () => {
            if (!this.confirmDiscard()) return;
            await this.run(async () => {
                await this.api.DisconnectGoogleDrive();
                this.setAccount({ emailAddress: '', displayName: '' });
                this.showDocument(null);
                this.status.textContent = '接続を解除しました';
            });
        });
        this.on('googleDriveRefresh', () => this.run(() => this.list()));
        this.on('googleDriveFolder', () => this.run(async () => {
            await this.api.OpenGoogleDriveFolder();
            this.status.textContent = 'ブラウザでKarteフォルダを開きました';
        }));
        this.on('googleDriveLoad', async () => {
            const id = this.files.value;
            if (!id || !this.confirmDiscard()) return;
            await this.run(() => this.load(id));
        });
        this.on('googleDriveNew', () => this.newFile('markdown'));
        this.on('googleDriveNewSheet', () => this.newFile('sheet'));
        this.on('googleDriveImport', () => this.chooseLocalFile(false));
        this.on('googleDriveLibrary', () => this.run(async () => {
            const panel = this.control('googleDriveLibraryPanel');
            panel.hidden = !panel.hidden;
            if (!panel.hidden) {
                const images = await this.api.GetImageList();
                this.control<HTMLSelectElement>('googleDriveLibraryImage').replaceChildren(
                    ...images.map(image => new Option(image.name, image.path)),
                );
                this.status.textContent = images.length ? '' : 'Karteにコピーできる写真がありません';
            } else {
                this.status.textContent = '';
            }
        }));
        this.on('googleDriveCopyImage', async () => {
            const path = this.control<HTMLSelectElement>('googleDriveLibraryImage').value;
            if (!path || !this.confirmDiscard()) return;
            await this.run(async () => {
                const saved = await this.api.CopyKarteImageToDrive(path);
                this.showDocument(saved);
                this.files.add(this.option(saved.file));
                this.files.value = saved.file.id;
                this.control('googleDriveLibraryPanel').hidden = true;
                this.status.textContent = '写真とタグをDriveにコピーしました';
            });
        });
        this.on('googleDriveReplacePhoto', () => this.chooseLocalFile(true));
        this.localFile.addEventListener('change', () => {
            const file = this.localFile.files?.[0];
            if (file && this.confirmDiscard()) void this.run(() => this.importFile(file, this.replacingPhoto));
        }, { signal: this.events.signal });
        this.on('googleDriveSave', () => this.run(() => this.save()));
        this.on('googleDriveOpenInBrowser', () => {
            if (!this.document) return;
            const url = new URL('https://drive.google.com/open');
            url.searchParams.set('id', this.document.file.id);
            this.runtime.BrowserOpenURL(url.toString());
        });
        this.on('googleDriveClose', () => this.dialog.close());
        this.files.addEventListener('change', () => this.render(), { signal: this.events.signal });
        this.name.addEventListener('input', () => this.render(), { signal: this.events.signal });
        this.editor.addEventListener('input', () => this.render(), { signal: this.events.signal });
        this.dialog.addEventListener('keydown', event => {
            if (!(event.ctrlKey || event.metaKey) || event.key.toLowerCase() !== 's') return;
            event.preventDefault();
            event.stopPropagation();
            this.control<HTMLButtonElement>('googleDriveSave').click();
        }, { signal: this.events.signal });
        this.dialog.addEventListener('cancel', event => {
            if (this.busy) event.preventDefault();
        }, { signal: this.events.signal });
    }

    canCloseApp(): boolean {
        return !this.busy && this.confirmDiscard();
    }

    destroy(): void {
        this.events.abort();
    }

    private async authorize(pick: boolean): Promise<void> {
        if (!this.confirmDiscard()) return;
        await this.run(async () => {
            if (pick) {
                const selection = await this.api.PickGoogleDriveFile();
                if (this.account !== selection.account.emailAddress) this.showDocument(null);
                this.setAccount(selection.account);
                await this.load(selection.id);
            } else {
                const account = await this.api.ConnectGoogleDrive();
                if (this.account !== account.emailAddress) this.showDocument(null);
                this.setAccount(account);
            }
            await this.list();
        }, true);
    }

    private setAccount(account: GoogleDriveAccount): void {
        if (account.emailAddress !== this.account) this.files.replaceChildren();
        this.account = account.emailAddress;
        this.accountLabel.textContent = this.account || '未接続';
    }

    private async list(): Promise<void> {
        const previous = this.document?.file.id ?? this.files.value;
        const files = await this.api.ListGoogleDriveFiles();
        this.files.replaceChildren(...files.map(file => this.option(file)));
        if (files.some(file => file.id === previous)) this.files.value = previous;
        this.status.textContent = files.length === 0 ? 'Karteフォルダにアクセスできるファイルがありません' : '';
    }

    private async load(id: string): Promise<void> {
        this.showDocument(await this.api.LoadGoogleDriveFile(id));
        this.status.textContent = 'Driveから読み込みました';
    }

    private async save(): Promise<void> {
        const existing = this.document;
        // Textarea normalizes line endings. Preserve the source convention on edited documents.
        let content = this.kind === 'photo' ? this.photoContent : this.editor.value;
        if (this.kind === 'markdown' && existing?.content.includes('\r\n')) {
            content = content.replace(/\n/g, '\r\n');
        }
        const saved = existing
            ? await this.api.SaveGoogleDriveFile(existing.file.id, existing.checksum, content)
            : await this.api.CreateGoogleDriveFile(this.kind, this.name.value, content);
        this.showDocument(saved);
        if (!Array.from(this.files.options).some(option => option.value === saved.file.id)) {
            this.files.add(this.option(saved.file));
        }
        this.files.value = saved.file.id;
        if (!existing && saved.file.kind === 'sheet') {
            // If loading fails, retain the new file ID and allow reopening it.
            this.showDocument(await this.api.LoadGoogleDriveFile(saved.file.id));
        }
        this.status.textContent = 'Driveに保存しました';
    }

    private option(file: GoogleDriveFile): HTMLOptionElement {
        return new Option(`[${fileKindLabel[file.kind]}] ${file.name}`, file.id);
    }

    private newFile(kind: GoogleDriveKind): void {
        if (!this.confirmDiscard()) return;
        this.showDocument(null, kind);
        this.name.focus();
    }

    private chooseLocalFile(replace: boolean): void {
        this.replacingPhoto = replace;
        this.localFile.value = '';
        this.localFile.accept = replace ? photoAccept : photoAccept + ',.csv,.md,.markdown';
        this.localFile.click();
    }

    private async importFile(file: File, replace: boolean): Promise<void> {
        const extension = file.name.split('.').pop()?.toLowerCase() ?? '';
        const isCSV = extension === 'csv';
        if (!replace && (isCSV || extension === 'md' || extension === 'markdown')) {
            if (file.size > maxCSV) throw new Error('2 MiB以下のファイルを指定してください');
            const data = await file.arrayBuffer();
            let content: string;
            try {
                content = new TextDecoder('utf-8', { fatal: true }).decode(data);
            } catch {
                throw new Error('UTF-8のファイルを指定してください');
            }
            this.showDocument(null, isCSV ? 'sheet' : 'markdown');
            this.name.value = isCSV ? file.name.slice(0, -4) : file.name;
            this.editor.value = content;
        } else {
            const mime = Object.prototype.hasOwnProperty.call(photoFormats, extension) ? photoFormats[extension] : '';
            if (!mime || file.size === 0 || file.size > maxPhoto) {
                throw new Error('5 MiB以下のJPEG、PNG、GIF、WebPを指定してください');
            }
            if (replace && this.document?.file.mimeType !== mime) {
                throw new Error('元の写真と同じ画像形式を指定してください');
            }
            const reader = new FileReader();
            const base64 = await new Promise<string>((resolve, reject) => {
                reader.onload = () => resolve(String(reader.result).split(',')[1] ?? '');
                reader.onerror = () => reject(new Error('写真を読み取れません'));
                reader.readAsDataURL(file);
            });
            if (!replace) {
                this.showDocument(null, 'photo');
                this.name.value = file.name;
            }
            this.showPhoto(base64, mime);
        }
        this.status.textContent = 'Driveに保存するとファイルが反映されます';
    }

    private showPhoto(content: string, mime: string): void {
        this.photoContent = content;
        if (content && Object.values(photoFormats).includes(mime)) {
            this.photoPreview.src = `data:${mime};base64,${content}`;
        } else {
            this.photoPreview.removeAttribute('src');
        }
    }

    private showDocument(doc: GoogleDriveDocument | null, kind: GoogleDriveKind = 'markdown'): void {
        this.document = doc;
        this.kind = doc?.file.kind ?? kind;
        this.name.value = doc?.file.name ?? '';
        const photo = this.kind === 'photo';
        this.editor.value = photo ? '' : doc?.content ?? '';
        this.showPhoto(photo ? doc?.content ?? '' : '', doc?.file.mimeType ?? '');
        this.render();
    }

    private get dirty(): boolean {
        if (this.kind === 'photo') return this.photoContent !== (this.document?.content ?? '');
        const original = this.document?.content.replace(/\r\n?/g, '\n') ?? '';
        return this.editor.value !== original || (!this.document && !!this.name.value.trim());
    }

    private confirmDiscard(): boolean {
        return !this.dirty || window.confirm('Google Driveの未保存の編集を破棄しますか？');
    }

    private async run(action: () => Promise<void>, authorizing = false): Promise<void> {
        if (this.busy) return;
        this.busy = true;
        this.authorizing = authorizing;
        this.status.textContent = authorizing ? 'ブラウザでGoogleの認可を完了してください' : 'Google Driveと通信中…';
        this.render();
        try {
            await action();
        } catch (error) {
            this.status.textContent = error instanceof Error ? error.message : String(error);
            const account = await this.api.GoogleDriveAccount().catch(() => null);
            if (account) this.setAccount(account);
        } finally {
            this.authorizing = false;
            this.busy = false;
            this.render();
        }
    }

    private render(): void {
        const connected = !!this.account;
        const ready = connected && !this.busy;
        const editable = this.document?.file.capabilities.canEdit ?? true;
        const photo = this.kind === 'photo';
        const sheet = this.kind === 'sheet';
        const awaitingSheet = sheet && !!this.document && !this.document.sheetTab;
        this.control<HTMLButtonElement>('googleDriveConnect').disabled = this.busy || connected;
        this.control<HTMLButtonElement>('googleDrivePick').disabled = this.busy;
        this.control<HTMLButtonElement>('googleDriveDisconnect').disabled = !ready;
        this.control<HTMLButtonElement>('googleDriveCancelAuth').hidden = !this.authorizing;
        this.control<HTMLButtonElement>('googleDriveRefresh').disabled = !ready;
        this.control<HTMLButtonElement>('googleDriveFolder').disabled = !ready;
        this.control<HTMLButtonElement>('googleDriveLoad').disabled = !ready || !this.files.value;
        this.control<HTMLButtonElement>('googleDriveNew').disabled = !ready;
        this.control<HTMLButtonElement>('googleDriveNewSheet').disabled = !ready;
        this.control<HTMLButtonElement>('googleDriveImport').disabled = !ready;
        this.control<HTMLButtonElement>('googleDriveLibrary').disabled = !ready || typeof this.api.CopyKarteImageToDrive !== 'function';
        this.control<HTMLButtonElement>('googleDriveCopyImage').disabled = !ready || !this.control<HTMLSelectElement>('googleDriveLibraryImage').value;
        this.control<HTMLButtonElement>('googleDriveOpenInBrowser').disabled = this.busy || !this.document;
        this.control<HTMLButtonElement>('googleDriveSave').disabled = !ready || !editable || awaitingSheet || !this.dirty || !this.name.value.trim();
        this.control<HTMLButtonElement>('googleDriveClose').disabled = this.busy;
        this.files.disabled = !ready;
        this.name.readOnly = !ready || !!this.document;
        this.name.placeholder = sheet ? 'スプレッドシート名' : photo ? 'photo.jpg' : 'note.md';
        this.editor.readOnly = !ready || !editable || awaitingSheet;
        const editorLabel = this.control('googleDriveEditorLabel');
        editorLabel.hidden = photo;
        editorLabel.textContent = sheet ? `CSV${this.document?.sheetTab ? `（${this.document.sheetTab}）` : ''}` : 'Markdown';
        this.editor.hidden = photo;
        this.control('googleDrivePhoto').hidden = !photo;
        this.photoPreview.hidden = !this.photoContent;
        const replace = this.control<HTMLButtonElement>('googleDriveReplacePhoto');
        replace.hidden = !this.document || !editable;
        replace.disabled = !ready;
    }

    private control<T extends HTMLElement = HTMLElement>(id: string): T {
        return document.getElementById(id) as T;
    }

    private on(id: string, action: () => void | Promise<void>): void {
        this.control(id).addEventListener('click', () => { void action(); }, { signal: this.events.signal });
    }
}
