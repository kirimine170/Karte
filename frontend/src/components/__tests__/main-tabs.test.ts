import { describe, it, expect, beforeEach, vi } from 'vitest';
import { MainTabs } from '../main-tabs';
import { useUIStore } from '../../stores/ui-store';
import { clearLogs, expectLogSequence } from '../../test-support/log-verifier';

describe('MainTabs', () => {
    beforeEach(() => {
        clearLogs();
        
        useUIStore.setState({
            sidebarVisible: true,
            imageGalleryVisible: true,
            csvGalleryVisible: true,
            workspaceMode: false,
            activeTab: 'editor',
            theme: 'light',
            hardWrap: false,
            statusMessage: '',
            statusClearTimer: null,
        });

        document.body.innerHTML = `
            <div class="main-container" id="mainContainer">
                <div class="content-area" id="contentArea">
                    <div class="editor-pane-wrapper">
                        <div class="tabs">
                            <button class="tab active" data-tab="editor">エディター</button>
                            <button class="tab" data-tab="graph">グラフ</button>
                            <button class="tab" data-tab="board">コルクボード</button>
                            <button class="tab" data-tab="postlab">Instagram</button>
                            <button class="tab" data-tab="accounting">会計</button>
                        </div>
                        <div class="tab-content active" id="editor-tab"></div>
                        <div class="tab-content" id="graph-tab"></div>
                        <div class="tab-content" id="board-tab"></div>
                        <div class="tab-content" id="postlab-tab"></div>
                        <div class="tab-content" id="accounting-tab"></div>
                    </div>
                </div>
            </div>
        `;
    });

    it('should initialize and log init event', () => {
        const mainTabs = new MainTabs();
        clearLogs();
        mainTabs.init();

        expectLogSequence([
            { component: 'MainTabs', action: 'init' }
        ]);
    });

    it('should log tab switch events', () => {
        const mainTabs = new MainTabs();
        mainTabs.init();
        clearLogs();

        const graphTab = document.querySelector('.tab[data-tab="graph"]') as HTMLButtonElement;
        graphTab.click();

        expectLogSequence([
            { component: 'MainTabs', action: 'tab-switch' }
        ]);
    });

    it('keeps Instagram and corkboard available alongside accounting', () => {
        const mainTabs = new MainTabs(); mainTabs.init();
        for (const name of ['postlab', 'accounting', 'board', 'editor']) {
            (document.querySelector(`.tabs [data-tab="${name}"]`) as HTMLButtonElement).click();
            expect(useUIStore.getState().activeTab).toBe(name);
            expect(document.getElementById(`${name}-tab`)!.classList.contains('active')).toBe(true);
            expect(document.querySelectorAll('.tab-content.active')).toHaveLength(1);
            expect(document.getElementById('contentArea')!.classList.contains('postlab-mode')).toBe(name === 'postlab' || name === 'board');
        }
        mainTabs.destroy();
    });

    it('opens accounting without selecting nested navigation as main tabs', () => {
        const nested = document.createElement('button'); nested.className = 'tab';
        nested.dataset.tab = 'nested'; document.getElementById('accounting-tab')!.append(nested);
        const mainTabs = new MainTabs(); mainTabs.init();
        (document.querySelector('.tabs [data-tab="accounting"]') as HTMLButtonElement).click();
        expect(useUIStore.getState().activeTab).toBe('accounting');
        expect(document.getElementById('accounting-tab')!.classList.contains('active')).toBe(true);
        expect(document.getElementById('editor-tab')!.classList.contains('active')).toBe(false);
        nested.click(); expect(useUIStore.getState().activeTab).toBe('accounting');
        mainTabs.destroy();
    });
});
