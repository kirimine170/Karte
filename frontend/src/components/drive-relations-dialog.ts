import GraphModule from '../graph-d3';
import type {
    DriveFolderSyncStatus, DriveRelationFile, DriveRelationFolder, DriveRelationSnapshot, GoogleDriveAccount,
    GraphData, WailsAppAPI, WailsRuntimeAPI,
} from '../types/wails-api';

const refreshInterval = 60_000;
const typeLabel = { markdown: 'Markdown', photo: '写真', sheet: 'スプレッドシート' };
const graphKind = { markdown: 'note', photo: 'asset:image', sheet: 'asset:sheet' };
const tagKey = (tag: string): string => tag.replace(/\s+/gu, ' ').trim().toLowerCase();

interface RelatedFile {
    file: DriveRelationFile;
    tags: string[];
    outgoing: boolean;
    incoming: boolean;
}

export class DriveRelationsDialog {
    private readonly events = new AbortController();
    private readonly dialog = this.control<HTMLDialogElement>('Dialog');
    private readonly status = this.control('Status');
    private readonly folders = this.control<HTMLSelectElement>('Folder');
    private readonly files = this.control<HTMLSelectElement>('Files');
    private account: GoogleDriveAccount | null = null;
    private snapshot: DriveRelationSnapshot | null = null;
    private graph: GraphModule | null = null;
    private selected = '';
    private tags = new Set<string>();
    private timer: number | undefined;
    private busy = false;
    private authorizing = false;
    private sync: DriveFolderSyncStatus | null = null;

    constructor(private readonly api: WailsAppAPI, private readonly runtime: WailsRuntimeAPI) {}

    init(): void {
        if (typeof this.api.GoogleDriveSearchAccount !== 'function') return;
        this.control('Btn').hidden = false;
        this.on('Btn', 'click', () => {
            this.dialog.showModal();
            void this.run(async () => {
                this.setAccount(await this.api.GoogleDriveSearchAccount());
                await this.loadFolders();
            });
        });
        this.on('Close', 'click', () => this.dialog.close());
        this.on('Dialog', 'close', () => {
            window.clearTimeout(this.timer);
            this.clearResults();
        });
        this.on('Connect', 'click', () => {
            void this.run(async () => {
                this.setAccount(await this.api.ConnectGoogleDriveSearch());
                await this.loadFolders();
            }, true);
        });
        this.on('Cancel', 'click', () => { void this.api.CancelGoogleDriveSearchAuthorization(); });
        this.on('Disconnect', 'click', () => {
            this.clearResults();
            void this.run(async () => {
                await this.api.DisconnectGoogleDriveSearch();
                this.setAccount(null);
                await this.refreshFolderSyncStatus();
            });
        });
        this.on('Folder', 'change', () => {
            this.clearResults();
            void this.run(() => this.refresh());
        });
        this.on('Refresh', 'click', () => {
            void this.run(() => this.folders.value ? this.refresh() : this.loadFolders());
        });
        this.on('Files', 'change', () => {
            this.selected = this.files.value;
            this.renderRelated();
        });
        this.on('Tags', 'change', (event) => {
            const input = event.target;
            if (!(input instanceof HTMLInputElement)) return;
            if (input.checked) this.tags.add(input.value);
            else this.tags.delete(input.value);
            this.renderFiles();
        });
        this.on('Open', 'click', () => {
            const file = this.snapshot?.files.find(file => file.id === this.selected);
            if (file) this.runtime.BrowserOpenURL(file.url);
        });
        this.on('Copy', 'click', () => {
            const text = this.snapshot?.files.find(file => file.id === this.selected)?.legacy;
            if (!text) return;
            void this.copy(text);
        });
        this.on('Dialog', 'cancel', event => {
            if (this.busy) event.preventDefault();
        });
        this.initFolderSync();
        this.renderControls();
    }

    private initFolderSync(): void {
        if (typeof this.api.GetGoogleDriveFolderSyncStatus !== 'function') return;
        this.control('Sync').hidden = false;
        const unsubscribe = this.runtime.EventsOn('drive-folder-sync', status => {
            this.setSyncStatus(status as DriveFolderSyncStatus);
        });
        this.events.signal.addEventListener('abort', unsubscribe, { once: true });
        this.on('SyncStart', 'click', () => { void this.changeFolderSync(true); });
        this.on('SyncStop', 'click', () => { void this.changeFolderSync(false); });
    }

    private async changeFolderSync(enabled: boolean): Promise<void> {
        this.control<HTMLButtonElement>(enabled ? 'SyncStart' : 'SyncStop').disabled = true;
        try {
            if (enabled) await this.api.StartGoogleDriveFolderSync(this.folders.value);
            else await this.api.StopGoogleDriveFolderSync();
            await this.refreshFolderSyncStatus();
        } catch (error) {
            this.control('SyncStatus').textContent = error instanceof Error ? error.message : String(error);
            this.renderControls();
        }
    }

    private async refreshFolderSyncStatus(): Promise<void> {
        if (typeof this.api.GetGoogleDriveFolderSyncStatus === 'function') {
            this.setSyncStatus(await this.api.GetGoogleDriveFolderSyncStatus());
        }
    }

    private setSyncStatus(status: DriveFolderSyncStatus): void {
        this.sync = status;
        let text = status.enabled ? '同期中' : '停止中';
        if (status.enabled && status.updatedAt) {
            text += ` · 前回 ${new Date(status.updatedAt).toLocaleTimeString()} · ダウンロード ${status.downloaded}件 · アップロード ${status.uploaded}件`;
            if (!status.writerReady) text += ' · アップロードは保存用接続を待機中';
            if (status.issues) text += ` · ${status.issues}件の問題（${status.message}）`;
        }
        this.control('SyncStatus').textContent = text;
        this.renderControls();
    }

    canCloseApp(): boolean {
        return !this.busy;
    }

    destroy(): void {
        this.events.abort();
        window.clearTimeout(this.timer);
        this.graph?.destroy();
    }

    private setAccount(account: GoogleDriveAccount | null): void {
        const previous = this.account?.permissionId || this.account?.emailAddress;
        const next = account?.permissionId || account?.emailAddress;
        this.account = account?.emailAddress ? account : null;
        if (!this.account || previous !== next) {
            this.folders.replaceChildren();
            this.clearResults();
        }
        this.control('Account').textContent = this.account?.emailAddress || '未接続';
    }

    private get storageKey(): string {
        return 'karte.driveRelations.folder.' + (this.account?.permissionId || this.account?.emailAddress.toLowerCase() || '');
    }

    private async loadFolders(): Promise<void> {
        if (!this.account) {
            await this.refreshFolderSyncStatus();
            return;
        }
        const candidates = await this.api.ListGoogleDriveSearchFolders();
        const preferred = this.folders.value || this.readFolderPreference();
        this.folders.replaceChildren(
            new Option('検索フォルダを選択', ''),
            ...candidates.map(folder => this.folderOption(folder, candidates.length > 1)),
        );
        if (preferred && !candidates.some(folder => folder.id === preferred)) {
            this.folders.add(new Option('以前に選択したフォルダ', preferred));
        }
        this.folders.value = preferred || (candidates.length === 1 ? candidates[0]?.id ?? '' : '');
        if (this.folders.value) await this.refresh();
        else if (!candidates.length) this.status.textContent = 'マイドライブ直下にKarteフォルダを作成して、更新してください';
        else this.status.textContent = '検索するKarteフォルダを選択してください';
    }

    private folderOption(folder: DriveRelationFolder, showDetails: boolean): HTMLOptionElement {
        const date = new Date(folder.createdTime);
        const time = Number.isNaN(date.getTime()) ? '' : date.toLocaleString();
        return new Option(showDetails ? `${folder.name}（${time} ${folder.id.slice(-8)}）` : folder.name, folder.id);
    }

    private readFolderPreference(): string {
        try {
            return localStorage.getItem(this.storageKey) ?? '';
        } catch {
            return '';
        }
    }

    private async refresh(): Promise<void> {
        const folder = this.folders.value;
        if (!folder) return;
        this.snapshot = await this.api.RefreshGoogleDriveRelations(folder);
        const selected = this.folders.selectedOptions[0];
        if (selected && selected.text === '以前に選択したフォルダ') selected.text = this.snapshot.folder.name;
        try {
            localStorage.setItem(this.storageKey, folder);
        } catch {
            // The index is usable for this session if storage is unavailable.
        }
        this.renderTags();
        this.renderFiles();
        const time = new Date(this.snapshot.updatedAt).toLocaleTimeString();
        const incomplete = this.snapshot.incomplete ? ` · 一部を取得できませんでした（${this.snapshot.incomplete}件）` : '';
        this.status.textContent = `${this.snapshot.files.length}件 · 更新 ${time}${incomplete}`;
        await this.refreshFolderSyncStatus();
    }

    private clearResults(): void {
        this.snapshot = null;
        this.selected = '';
        this.tags.clear();
        this.control('Results').hidden = true;
        this.control('Tags').replaceChildren();
        this.files.replaceChildren();
        this.control('Related').replaceChildren();
        this.graph?.destroy();
        this.graph = null;
    }

    private renderTags(): void {
        const labels = new Map<string, string>();
        for (const file of this.snapshot?.files ?? []) {
            for (const tag of file.tags) labels.set(tagKey(tag), labels.get(tagKey(tag)) ?? tag);
        }
        this.tags = new Set([...this.tags].filter(tag => labels.has(tag)));
        this.control('Tags').replaceChildren(...[...labels].sort((a, b) => a[1].localeCompare(b[1])).map(([key, tag]) => {
            const label = document.createElement('label');
            const input = document.createElement('input');
            input.type = 'checkbox';
            input.value = key;
            input.checked = this.tags.has(key);
            label.append(input, document.createTextNode(' ' + tag));
            return label;
        }));
    }

    private renderFiles(): void {
        const all = this.snapshot?.files ?? [];
        const matches = all.filter(file => [...this.tags].every(tag => file.tags.some(value => tagKey(value) === tag)));
        this.files.replaceChildren(...matches.map(file => new Option(this.fileLabel(file), file.id)));
        this.selected = matches.some(file => file.id === this.selected) ? this.selected : (matches[0]?.id ?? '');
        this.files.value = this.selected;
        this.control('Results').hidden = !this.snapshot;
        this.renderRelated();
    }

    private fileLabel(file: DriveRelationFile): string {
        return `[${typeLabel[file.kind]}] ${file.title}${file.title === file.name ? '' : `（${file.name}）`}`;
    }

    private relatedFiles(current: DriveRelationFile): RelatedFile[] {
        const tags = new Set(current.tags.map(tagKey));
        const links = new Set(current.linkIds);
        return (this.snapshot?.files ?? []).flatMap(file => {
            if (file.id === current.id) return [];
            const common = file.tags.filter(tag => tags.has(tagKey(tag)));
            const outgoing = links.has(file.id);
            const incoming = file.linkIds.includes(current.id);
            return common.length || outgoing || incoming ? [{ file, tags: common, outgoing, incoming }] : [];
        });
    }

    private selectRelated(id: string): void {
        if (![...this.files.options].some(option => option.value === id)) {
            this.tags.clear();
            this.renderTags();
        }
        this.selected = id;
        this.renderFiles();
    }

    private renderRelated(): void {
        const current = this.snapshot?.files.find(file => file.id === this.selected);
        this.control<HTMLButtonElement>('Open').disabled = this.busy || !current;
        this.control('Copy').hidden = !current?.legacy;
        this.control<HTMLButtonElement>('Copy').disabled = this.busy;
        this.control<HTMLButtonElement>('SyncStart').disabled = this.busy || !this.account || !this.folders.value || !!this.sync?.enabled;
        this.control<HTMLButtonElement>('SyncStop').disabled = !this.sync?.enabled;
        const panel = this.control('Related');
        panel.replaceChildren();
        const graphContainer = this.control('Graph');
        if (!current) {
            panel.textContent = '一致するファイルがありません';
            graphContainer.hidden = true;
            return;
        }
        const related = this.relatedFiles(current);
        const heading = document.createElement('h3');
        heading.textContent = '関連ファイル';
        panel.append(heading);
        const list = document.createElement('ul');
        for (const relation of related) {
            const row = document.createElement('li');
            const link = document.createElement('button');
            link.type = 'button';
            link.textContent = this.fileLabel(relation.file);
            link.disabled = this.busy;
            link.addEventListener('click', () => this.selectRelated(relation.file.id));
            const reasons = [
                relation.tags.length ? `共通タグ（${relation.tags.join(', ')}）` : '',
                relation.outgoing ? 'リンク先' : '',
                relation.incoming ? '被リンク' : '',
            ].filter(Boolean);
            row.append(link, document.createTextNode(' ' + reasons.join(' · ')));
            list.append(row);
        }
        panel.append(related.length ? list : document.createTextNode('関連するファイルがありません'));
        if (current.unresolved) {
            const unresolved = document.createElement('p');
            unresolved.textContent = `未解決のリンク ${current.unresolved}件`;
            panel.append(unresolved);
        }
        graphContainer.hidden = related.length === 0;
        if (related.length) this.showGraph(current, related);
    }

    private showGraph(current: DriveRelationFile, related: RelatedFile[]): void {
        if (!this.graph) {
            this.graph = new GraphModule('driveRelationsGraph');
            this.graph.on('node:click', (node: { id?: string }) => {
                if (!this.busy && node.id?.startsWith('drive:')) this.selectRelated(node.id.slice(6));
            });
        }
        const graph: GraphData = { nodes: [], edges: [], meta: { directed: true } };
        for (const file of [current, ...related.map(relation => relation.file)]) {
            graph.nodes.push({ id: 'drive:' + file.id, kind: graphKind[file.kind], label: file.title, tags: file.tags, exists: true, degIn: 0, degOut: 0 });
        }
        const tagIDs = new Set<string>();
        const edgeIDs = new Set<string>();
        const addEdge = (source: string, target: string, kind: string) => {
            const id = `${source}>${target}`;
            if (edgeIDs.has(id)) return;
            edgeIDs.add(id);
            graph.edges.push({ id, source, target, kind, weight: 1 });
        };
        for (const relation of related) {
            const currentID = 'drive:' + current.id;
            const relatedID = 'drive:' + relation.file.id;
            if (relation.outgoing) addEdge(currentID, relatedID, 'markdown_link');
            if (relation.incoming) addEdge(relatedID, currentID, 'markdown_link');
            for (const tag of relation.tags) {
                const id = 'tag:' + tagKey(tag);
                if (!tagIDs.has(id)) {
                    tagIDs.add(id);
                    graph.nodes.push({ id, kind: 'tag', label: '#' + tag, tags: [], exists: true, degIn: 0, degOut: 0 });
                }
                addEdge(currentID, id, 'tag');
                addEdge(relatedID, id, 'tag');
            }
        }
        this.graph.setData(graph);
    }

    private async copy(text: string): Promise<void> {
        try {
            if (this.runtime.ClipboardSetText) {
                if (!await this.runtime.ClipboardSetText(text)) throw new Error('Clipboard unavailable');
            } else {
                await navigator.clipboard.writeText(text);
            }
            this.status.textContent = 'コピーしました。Driveの既存の説明を残して、末尾に貼り付けてください';
        } catch {
            this.status.textContent = 'クリップボードにコピーできませんでした';
        }
    }

    private async run(action: () => Promise<void>, authorizing = false): Promise<void> {
        if (this.busy) return;
        window.clearTimeout(this.timer);
        this.busy = true;
        this.authorizing = authorizing;
        this.status.textContent = authorizing ? 'ブラウザでGoogleの認可を完了してください' : 'Google Driveを確認しています…';
        this.renderControls();
        try {
            await action();
            if (!this.account) this.status.textContent = '';
        } catch (error) {
            this.clearResults();
            this.setAccount(await this.api.GoogleDriveSearchAccount().catch(() => null));
            this.status.textContent = error instanceof Error ? error.message : String(error);
        } finally {
            this.busy = false;
            this.authorizing = false;
            this.renderControls();
            if (this.dialog.open && this.account && this.folders.value) {
                this.timer = window.setTimeout(() => { void this.run(() => this.refresh()); }, refreshInterval);
            }
        }
    }

    private renderControls(): void {
        this.control<HTMLButtonElement>('Connect').disabled = this.busy || !!this.account;
        this.control('Consent').hidden = !!this.account;
        this.control('Cancel').hidden = !this.authorizing;
        for (const id of ['Refresh', 'Disconnect']) {
            this.control<HTMLButtonElement>(id).disabled = this.busy || !this.account;
        }
        this.control<HTMLButtonElement>('Close').disabled = this.busy;
        this.folders.disabled = this.busy || !this.account;
        this.files.disabled = this.busy;
        this.control<HTMLButtonElement>('Open').disabled = this.busy || !this.selected;
        this.control<HTMLButtonElement>('Copy').disabled = this.busy;
        for (const button of this.control('Related').querySelectorAll('button')) button.disabled = this.busy;
    }

    private control<T extends HTMLElement = HTMLElement>(suffix: string): T {
        return document.getElementById('driveRelations' + suffix) as T;
    }

    private on(suffix: string, name: string, listener: (event: Event) => void): void {
        this.control(suffix).addEventListener(name, listener, { signal: this.events.signal });
    }
}
