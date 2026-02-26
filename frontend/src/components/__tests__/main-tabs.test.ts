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
            activeTab: 'editor',
            theme: 'light',
            hardWrap: false,
            statusMessage: '',
            statusClearTimer: null,
        });

        document.body.innerHTML = `
            <div class="editor-pane-wrapper">
                <div class="tabs">
                    <button class="tab active" data-tab="editor">エディター</button>
                    <button class="tab" data-tab="graph">グラフ</button>
                </div>
                <div class="tab-content active" id="editor-tab"></div>
                <div class="tab-content" id="graph-tab"></div>
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
});

