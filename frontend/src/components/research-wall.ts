import { useBoardStore, useDocStore, useUIStore, useModalStore, useCustomCssStore } from '../stores/index';
import type { BoardDocument, WailsAppAPI, WailsRuntimeAPI } from '../types/wails-api';
import { renderMarkdownPreview } from '../utils/preview-renderer';
import { applyCustomCssToHtml } from '../utils/custom-css';
import { convertTimestampsToLinks } from '../utils/preview-audio';
// @ts-ignore Renderer models are shared with the standalone PoC and have no native dependencies．
import { fromKarteBoard, toKarteBoard, validateBoard } from '../../../examples/crazy-wall/model.mjs';

const CHANNEL = 'karte.research-wall.v1';

/** Native lifecycle，managed resources and editors．The iframe owns only the wall UI． */
export class ResearchWall {
    private frame: HTMLIFrameElement | null = null;
    private unsubscribers: (() => void)[] = [];
    private documents = new Map<string, BoardDocument>();
    private sessionPath = '';
    private queue: Promise<unknown> = Promise.resolve();
    private ready = false;
    private applying = false;
    private destroyed = false;
    private refreshTimer?: ReturnType<typeof setTimeout>;
    private dirty = false;
    private returnButton: HTMLButtonElement | null = null;

    constructor(private readonly api: WailsAppAPI, private readonly runtime: WailsRuntimeAPI) {}

    init(): void {
        window.addEventListener('message', this.onMessage);
        this.returnButton = document.createElement('button');
        this.returnButton.id = 'returnToResearchWall'; this.returnButton.className = 'btn';
        this.returnButton.textContent = 'ボードへ戻る'; this.returnButton.hidden = true;
        this.returnButton.onclick = () => useUIStore.getState().setActiveTab('board');
        document.getElementById('saveBtn')?.after(this.returnButton);
        this.unsubscribers.push(useUIStore.subscribe((state, prev) => {
            if (this.returnButton) this.returnButton.hidden = state.activeTab === 'board' || !this.sessionPath;
            if (state.activeTab === 'board' && prev.activeTab !== 'board') { this.mount(); void this.refresh(); }
        }));
        this.unsubscribers.push(useBoardStore.subscribe((state, prev) => {
            if (this.applying || !state.board || state.board === prev.board) return;
            this.documents.set(state.board.path, state.board);
            if (state.board.path !== this.sessionPath) {
                this.sessionPath = state.board.path;
                if (this.ready) void this.sendBoard(state.board);
            }
        }));
        this.unsubscribers.push(useModalStore.subscribe((state, prev) => {
            if (prev.imagePreviewModal.visible && !state.imagePreviewModal.visible) void this.refresh();
        }));
        for (const name of ['file-changed','audio-transcribed','image-imported','pdf-imported','audio-imported']) {
            const unsubscribe = this.runtime.EventsOn(name, () => {
                clearTimeout(this.refreshTimer);
                this.refreshTimer = setTimeout(() => { void this.refresh(); }, 500);
            });
            if (unsubscribe) this.unsubscribers.push(unsubscribe);
        }
        if (useUIStore.getState().activeTab === 'board') this.mount();
    }

    private mount(): void {
        if (this.frame || this.destroyed) return;
        const target = document.getElementById('board-tab'); if (!target) return;
        this.frame = document.createElement('iframe');
        this.frame.title = 'Karte リサーチボード'; this.frame.className = 'research-wall-frame';
        this.frame.src = '/research-wall.html'; target.replaceChildren(this.frame);
    }
    private send(operation: string, value?: unknown): void {
        this.frame?.contentWindow?.postMessage({channel: CHANNEL, operation, value}, location.origin);
    }
    private async hydrate(doc: BoardDocument): Promise<any> {
        const paths = [...new Set(doc.cards.map(c => c.source).filter((p): p is string => Boolean(p)))];
        const results = await Promise.allSettled(paths.map(path => this.api.LoadResearchResource(path)));
        return fromKarteBoard(doc, results.flatMap(result => result.status === 'fulfilled' ? [result.value] : []));
    }
    private async sendBoard(doc: BoardDocument): Promise<void> {
        const wall = await this.hydrate(doc);
        if (this.sessionPath === doc.path) this.send('board', wall);
    }
    private async refresh(): Promise<void> {
        if (!this.ready || this.destroyed) return;
        const doc = this.documents.get(this.sessionPath); if (!doc) return;
        const path = this.sessionPath;
        const wall = await this.hydrate(doc);
        if (this.sessionPath === path) this.send('resources', wall.cards.filter((c: any) => c.source));
    }
    private applyDocument(doc: BoardDocument): void {
        this.documents.set(doc.path, doc); this.sessionPath = doc.path;
        this.applying = true;
        useBoardStore.getState().setBoard(doc);
        const store = useDocStore.getState();
        // Preserve a source document that is being edited while the wall autosaves．
        if (!store.currentPath || store.currentPath.endsWith('.board.md')) {
            store.setCurrentPath(doc.path); store.setMarkdownContent(doc.rawContent); store.setPreviewHtml(''); store.clearUnsavedChanges();
        }
        this.applying = false;
    }
    private async operate(operation: string, value: any): Promise<any> {
        switch (operation) {
            case 'load': {
                this.ready = true;
                const doc = useBoardStore.getState().board;
                if (!doc) return {schema:'karte-research-wall/1',title:'リサーチボード',cards:[],edges:[]};
                this.documents.set(doc.path, doc); this.sessionPath = doc.path;
                return this.hydrate(doc);
            }
            case 'save': {
                const wall = validateBoard(value);
                let doc = this.documents.get(wall.managedPath || this.sessionPath);
                if (!doc) { doc = await this.api.CreateResearchBoard(wall.title); this.documents.set(doc.path, doc); this.sessionPath = doc.path; }
                // Reject stale saves after opening another board instead of writing into that board．
                if (wall.managedPath && wall.managedPath !== doc.path) throw new Error('保存先のボードが見つかりません．');
                const updated = await this.api.SaveBoard(doc.path, toKarteBoard(wall, doc));
                this.documents.set(updated.path, updated);
                if (this.sessionPath === updated.path) this.applyDocument(updated);
                useDocStore.getState().setFiles(await this.api.GetFileList());
                return {path: updated.path};
            }
            case 'resources': return this.api.ListResearchResources();
            case 'resource': return this.api.LoadResearchResource(String(value));
            case 'boards': return (await this.api.GetFileList()).filter(file => file.path.endsWith('.board.md'));
            case 'open-board': {
                const doc = await this.api.LoadBoard(String(value)); this.applyDocument(doc); return this.hydrate(doc);
            }
            case 'new-board': {
                const doc = await this.api.CreateResearchBoard(String(value || 'リサーチボード')); this.applyDocument(doc);
                useDocStore.getState().setFiles(await this.api.GetFileList()); return this.hydrate(doc);
            }
            case 'import': {
                if (!value || typeof value.filename !== 'string' || typeof value.data !== 'string' || value.data.length > 28*1024*1024) throw new Error('資料の形式が正しくありません．');
                let resource = await this.api.ImportResearchResource(value.filename, value.data);
                if (resource.kind === 'image' && typeof value.metadata === 'string') {
                    if (value.metadata.length > 1024*1024) throw new Error('画像メタデータが大きすぎます．');
                    await this.api.SaveImageMetadata(resource.path, value.metadata);
                    resource = await this.api.LoadResearchResource(resource.path);
                }
                if (resource.kind === 'audio' && typeof value.transcript === 'string' && value.transcript.trim()) {
                    if (value.transcript.length > 1024*1024) throw new Error('音声メモが大きすぎます．');
                    const transcriptPath = await this.api.EditResearchResource(resource.path);
                    const linked = await this.api.LoadFile(transcriptPath);
                    const front = linked.match(/^---\r?\n[\s\S]*?\r?\n---(?:\r?\n|$)/)?.[0];
                    if (!front) throw new Error('音声メモの関連付けを復元できません．');
                    await this.api.SaveFile(transcriptPath, `${front}\n${value.transcript}`);
                    resource = await this.api.LoadResearchResource(resource.path);
                }
                useDocStore.getState().setFiles(await this.api.GetFileList()); return resource;
            }
            case 'export': {
                if (!value || typeof value.filename !== 'string' || typeof value.content !== 'string' || value.content.length > 115*1024*1024) throw new Error('書き出すボードの形式が正しくありません．');
                return this.api.SaveResearchBoardExport(value.filename, value.content);
            }
            case 'edit': return this.openEditor(String(value));
            default: throw new Error('対応していないボード操作です．');
        }
    }
    private async openEditor(path: string): Promise<void> {
        const resource = await this.api.LoadResearchResource(path);
        if (resource.kind === 'image') {
            const [metadata, system] = await Promise.all([this.api.GetImageMetadata(path), this.api.GetImageSystemMetadata(path)]);
            useModalStore.getState().showImagePreviewModal(path, resource.title, metadata, system); return;
        }
        const doc = useDocStore.getState();
        if (doc.hasUnsavedChanges && !doc.currentPath.endsWith('.board.md')) {
            // Save through the same canonical API before navigating from the existing editor．
            await this.api.SaveFile(doc.currentPath, doc.markdownContent); doc.clearUnsavedChanges();
        }
        const editPath = await this.api.EditResearchResource(path);
        if (!editPath) throw new Error('この資料の編集先が見つかりません．');
        const content = await this.api.LoadFile(editPath);
        let html = '';
        if (resource.kind !== 'pdf') {
            const preview = await renderMarkdownPreview(content, this.api, editPath);
            html = convertTimestampsToLinks(applyCustomCssToHtml(preview.prepared, preview.html, useCustomCssStore.getState().customCss, useUIStore.getState().theme));
        }
        doc.setCurrentPath(editPath); doc.setMarkdownContent(content); doc.setPreviewHtml(html); doc.clearUnsavedChanges();
        useUIStore.getState().setActiveTab('editor');
        useUIStore.getState().setStatusMessage('編集後は「ボード」タブで資料の更新を確認できます．', 4500);
    }
    private onMessage = (event: MessageEvent): void => {
        if (!this.frame || event.source !== this.frame.contentWindow || event.origin !== location.origin || event.data?.channel !== CHANNEL) return;
        const {id, operation, value} = event.data;
        if (operation === 'dirty') { this.dirty = Boolean(value); return; }
        if (typeof id !== 'string' || id.length > 100 || typeof operation !== 'string') return;
        this.queue = this.queue.catch(() => {}).then(async () => {
            try {
                const result = await this.operate(operation, value);
                if (!this.destroyed) this.frame?.contentWindow?.postMessage({channel:CHANNEL,id,value:result},location.origin);
            } catch (error) {
                if (!this.destroyed) this.frame?.contentWindow?.postMessage({channel:CHANNEL,id,error:error instanceof Error ? error.message : String(error)},location.origin);
            }
        });
    };
    async flush(): Promise<boolean> {
        if (!this.ready) return true;
        this.send('flush');
        // A saved acknowledgement is sent after the renderer's debounce and queued native writes．
        return new Promise(resolve => {
            const timeout = setTimeout(() => { window.removeEventListener('message', listener); resolve(false); }, 120000);
            const listener = (event: MessageEvent) => {
                if (event.source === this.frame?.contentWindow && event.origin === location.origin && event.data?.channel === CHANNEL && event.data.operation === 'flushed') {
                    clearTimeout(timeout); window.removeEventListener('message', listener); resolve(!event.data.error);
                }
            };
            window.addEventListener('message', listener);
        });
    }
    hasUnsavedChanges(): boolean { return this.dirty; }
    destroy(): void {
        this.destroyed = true; this.unsubscribers.forEach(unsub => unsub()); clearTimeout(this.refreshTimer);
        window.removeEventListener('message', this.onMessage); this.frame?.remove(); this.frame = null; this.returnButton?.remove();
    }
}
