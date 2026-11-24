import type { GoogleDriveAccount, GoogleDriveDocument, WailsAppAPI, WailsRuntimeAPI } from '../types/wails-api';

export class GoogleDriveDialog {
    private readonly events = new AbortController();
    private readonly dialog = this.control<HTMLDialogElement>('googleDriveDialog');
    private readonly status = this.control('googleDriveStatus');
    private readonly files = this.control<HTMLSelectElement>('googleDriveFiles');
    private readonly name = this.control<HTMLInputElement>('googleDriveName');
    private readonly editor = this.control<HTMLTextAreaElement>('googleDriveEditor');
    private readonly accountLabel = this.control('googleDriveAccount');
    private account = '';
    private document: GoogleDriveDocument | null = null;
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
        this.on('googleDriveNew', () => {
            if (this.confirmDiscard()) {
                this.showDocument(null);
                this.name.focus();
            }
        });
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
                const selection = await this.api.PickGoogleDriveMarkdown();
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
        const files = await this.api.ListGoogleDriveMarkdown();
        this.files.replaceChildren(...files.map(file => new Option(file.name, file.id)));
        if (files.some(file => file.id === previous)) this.files.value = previous;
        this.status.textContent = files.length === 0 ? 'KarteフォルダにアクセスできるMarkdownファイルがありません' : '';
    }

    private async load(id: string): Promise<void> {
        this.showDocument(await this.api.LoadGoogleDriveMarkdown(id));
        this.status.textContent = 'Driveから読み込みました';
    }

    private async save(): Promise<void> {
        const existing = this.document;
        // Textarea normalizes line endings. Preserve the source convention on edited documents.
        const content = existing?.content.includes('\r\n')
            ? this.editor.value.replace(/\n/g, '\r\n')
            : this.editor.value;
        const saved = existing
            ? await this.api.SaveGoogleDriveMarkdown(existing.file.id, existing.checksum, content)
            : await this.api.CreateGoogleDriveMarkdown(this.name.value, content);
        this.showDocument(saved);
        if (!Array.from(this.files.options).some(option => option.value === saved.file.id)) {
            this.files.add(new Option(saved.file.name, saved.file.id));
        }
        this.files.value = saved.file.id;
        this.status.textContent = 'Driveに保存しました';
    }

    private showDocument(doc: GoogleDriveDocument | null): void {
        this.document = doc;
        this.name.value = doc?.file.name ?? '';
        this.editor.value = doc?.content ?? '';
        this.render();
    }

    private get dirty(): boolean {
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
        this.control<HTMLButtonElement>('googleDriveConnect').disabled = this.busy || connected;
        this.control<HTMLButtonElement>('googleDrivePick').disabled = this.busy;
        this.control<HTMLButtonElement>('googleDriveDisconnect').disabled = !ready;
        this.control<HTMLButtonElement>('googleDriveCancelAuth').hidden = !this.authorizing;
        this.control<HTMLButtonElement>('googleDriveRefresh').disabled = !ready;
        this.control<HTMLButtonElement>('googleDriveFolder').disabled = !ready;
        this.control<HTMLButtonElement>('googleDriveLoad').disabled = !ready || !this.files.value;
        this.control<HTMLButtonElement>('googleDriveNew').disabled = !ready;
        this.control<HTMLButtonElement>('googleDriveOpenInBrowser').disabled = this.busy || !this.document;
        this.control<HTMLButtonElement>('googleDriveSave').disabled = !ready || !editable || !this.dirty || !this.name.value.trim();
        this.control<HTMLButtonElement>('googleDriveClose').disabled = this.busy;
        this.files.disabled = !ready;
        this.name.readOnly = !ready || !!this.document;
        this.editor.readOnly = !ready || !editable;
    }

    private control<T extends HTMLElement = HTMLElement>(id: string): T {
        return document.getElementById(id) as T;
    }

    private on(id: string, action: () => void | Promise<void>): void {
        this.control(id).addEventListener('click', () => { void action(); }, { signal: this.events.signal });
    }
}
