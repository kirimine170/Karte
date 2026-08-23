import { describe, it, expect, beforeEach, vi } from 'vitest';
import { MainTabs } from '../main-tabs';
import { useUIStore } from '../../stores/ui-store';
import { useDocStore } from '../../stores/doc-store';
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
        useDocStore.setState({
            currentPath: '',
            markdownContent: '',
            previewHtml: '',
            hasUnsavedChanges: false,
            files: [],
            searchQuery: '',
        });

        document.body.innerHTML = `
            <div class="main-container" id="mainContainer">
                <div class="content-area" id="contentArea">
                    <div class="editor-pane-wrapper">
                        <div class="tabs">
                            <button class="tab active" data-tab="editor">エディター</button>
                            <button class="tab" data-tab="graph">グラフ</button>
                            <button class="tab" data-tab="board">コルクボード</button>
                        </div>
                        <div class="tab-content active" id="editor-tab"></div>
                        <div class="tab-content" id="graph-tab"></div>
                        <div class="tab-content" id="board-tab"></div>
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

    it('switches from Board source to Editor without retaining a Markdown preview', () => {
        useUIStore.setState({ activeTab: 'board' });
        useDocStore.setState({
            currentPath: 'content/example.board.md',
            markdownContent: '---\ntype: karte-board\n---',
            previewHtml: '<p>stale Markdown preview</p>',
        });
        const mainTabs = new MainTabs();
        mainTabs.init();

        const editorTab = document.querySelector<HTMLButtonElement>('.tab[data-tab="editor"]');
        editorTab?.click();

        expect(useUIStore.getState().activeTab).toBe('editor');
        expect(useDocStore.getState()).toMatchObject({
            currentPath: 'content/example.board.md',
            markdownContent: '---\ntype: karte-board\n---',
            previewHtml: '',
        });
        expect(document.getElementById('editor-tab')?.classList.contains('active')).toBe(true);
        mainTabs.destroy();
    });
});
