import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { PostLab } from '../post-lab';
import { MainTabs } from '../main-tabs';
import { useUIStore } from '../../stores/ui-store';
import type { WailsAppAPI } from '../../types/wails-api';

describe('Post lab integration', () => {
    let component: PostLab;
    let tabs: MainTabs;
    const save = vi.fn(async () => '/selected/draft.json');
    beforeEach(() => {
        save.mockClear();
        useUIStore.setState({ activeTab: 'editor' });
        document.body.innerHTML = `<div id="mainContainer"><div id="contentArea"><div class="editor-pane-wrapper"><div class="tabs"><button class="tab" data-tab="editor">Editor</button><button class="tab" data-tab="postlab">Instagram</button></div><div class="tab-content active" id="editor-tab"></div><div class="tab-content" id="postlab-tab"></div></div></div></div>`;
        tabs = new MainTabs(); tabs.init();
        component = new PostLab({ SaveInstagramDraft: save } as unknown as WailsAppAPI); component.init();
    });
    afterEach(() => { component.destroy(); tabs.destroy(); });

    it('mounts only when selected，keeps the draft frame when switching，and restores editor layout', () => {
        expect(document.querySelector('iframe')).toBeNull();
        (document.querySelector('[data-tab="postlab"]') as HTMLButtonElement).click();
        const frame = document.querySelector('iframe');
        expect(frame?.getAttribute('src')).toBe('/postlab/index.html');
        expect(document.getElementById('contentArea')?.classList.contains('postlab-mode')).toBe(true);
        useUIStore.getState().setActiveTab('editor');
        expect(document.getElementById('contentArea')?.classList.contains('postlab-mode')).toBe(false);
        useUIStore.getState().setActiveTab('postlab');
        expect(document.querySelector('iframe')).toBe(frame);
    });

    it('accepts file requests only from the owned frame at the same origin', async () => {
        useUIStore.getState().setActiveTab('postlab');
        const frame = document.querySelector('iframe') as HTMLIFrameElement;
        const data = { channel: 'karte.postlab.file.v1', id: 'request-1', operation: 'draft', content: '{}' };
        window.dispatchEvent(new MessageEvent('message', { data, origin: location.origin, source: window }));
        window.dispatchEvent(new MessageEvent('message', { data, origin: 'https://other.invalid', source: frame.contentWindow }));
        expect(save).not.toHaveBeenCalled();
        window.dispatchEvent(new MessageEvent('message', { data, origin: location.origin, source: frame.contentWindow }));
        await Promise.resolve();
        expect(save).toHaveBeenCalledWith('{}');
        component.destroy();
        window.dispatchEvent(new MessageEvent('message', { data, origin: location.origin, source: frame.contentWindow }));
        expect(save).toHaveBeenCalledTimes(1);
    });
});
