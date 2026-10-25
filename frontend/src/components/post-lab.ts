import { useUIStore } from '../stores/ui-store';
import type { WailsAppAPI } from '../types/wails-api';

const CHANNEL = 'karte.postlab.file.v1';

/** Owns the editor lifecycle and native file dialogs．Rendering belongs to KarteRenderer． */
export class PostLab {
    private frame: HTMLIFrameElement | null = null;
    private unsubscribe: (() => void) | null = null;
    private destroyed = false;
    private saving = false;

    constructor(private readonly api: WailsAppAPI) {}

    init(): void {
        window.addEventListener('message', this.onMessage);
        this.unsubscribe = useUIStore.subscribe(state => {
            if (state.activeTab === 'postlab') this.mount();
        });
        if (useUIStore.getState().activeTab === 'postlab') this.mount();
    }

    private mount(): void {
        if (this.frame || this.destroyed) return;
        const target = document.getElementById('postlab-tab');
        if (!target) return;
        this.frame = document.createElement('iframe');
        this.frame.title = 'Instagram 投稿シミュレータ';
        this.frame.className = 'postlab-frame';
        this.frame.src = '/postlab/index.html';
        target.replaceChildren(this.frame);
    }

    private onMessage = async (event: MessageEvent): Promise<void> => {
        if (!this.frame || event.source !== this.frame.contentWindow || event.origin !== window.location.origin) return;
        const data = event.data;
        if (!data || data.channel !== CHANNEL || typeof data.id !== 'string' || data.id.length > 100) return;
        if (data.operation !== 'draft' && data.operation !== 'image') return;
        const source = this.frame.contentWindow;
        const respond = (result: { path?: string; error?: string }) => {
            if (!this.destroyed) source?.postMessage({ channel: CHANNEL, id: data.id, ...result }, event.origin === 'null' ? '*' : event.origin);
        };
        if (this.saving) { respond({ error: '保存ダイアログが開いています．' }); return; }
        this.saving = true;
        try {
            let path: string;
            if (data.operation === 'draft') {
                if (typeof data.content !== 'string' || data.content.length > 1024 * 1024 || !this.api.SaveInstagramDraft) throw new Error('下書きを保存できません．');
                path = await this.api.SaveInstagramDraft(data.content);
            } else {
                if (typeof data.content !== 'string' || data.content.length > 24 * 1024 * 1024 || typeof data.filename !== 'string' || !this.api.SaveInstagramImage) throw new Error('画像を保存できません．');
                path = await this.api.SaveInstagramImage(data.filename, data.content);
            }
            respond({ path });
        } catch (error) {
            respond({ error: error instanceof Error ? error.message : String(error) });
        } finally { this.saving = false; }
    };

    destroy(): void {
        this.destroyed = true;
        this.unsubscribe?.();
        window.removeEventListener('message', this.onMessage);
        this.frame?.remove();
        this.frame = null;
    }
}
