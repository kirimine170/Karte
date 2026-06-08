import { BaseComponent } from './component-base';
import { useUIStore } from '../stores/index';
import { eventLogger } from '../utils/event-logger';

export class MainTabs extends BaseComponent {
    private unsubscribe: (() => void)[] = [];
    private tabButtons: HTMLButtonElement[] = [];
    private tabContents: HTMLElement[] = [];

    init(): void {
        eventLogger.log('MainTabs', 'init');
        
        const tabsContainer = document.querySelector('.editor-pane-wrapper .tabs');
        if (!tabsContainer) {
            console.error('MainTabs: .tabs element not found');
            return;
        }
        this.element = tabsContainer as HTMLElement;

        // タブボタンとコンテンツの取得
        this.tabButtons = Array.from(document.querySelectorAll('.tab')) as HTMLButtonElement[];
        this.tabContents = Array.from(document.querySelectorAll('.tab-content')) as HTMLElement[];

        // イベントリスナーの設定
        this.setupEventListeners();

        // 状態の購読
        this.subscribeToStores();

        // 初期状態の反映
        this.updateUI();
    }

    private setupEventListeners(): void {
        const uiStore = useUIStore.getState();

        // タブボタンのクリックイベント
        this.tabButtons.forEach((button) => {
            const tabName = button.dataset.tab;
            if (tabName) {
                this.unsubscribe.push(
                    this.addEventListener(button, 'click', () => {
                        eventLogger.log('MainTabs', 'tab-switch', { tab: tabName });
                        uiStore.setActiveTab(tabName as 'editor' | 'graph' | 'board');
                    })
                );
            }
        });
    }

    private subscribeToStores(): void {
        // UI Store - アクティブタブ
        this.unsubscribe.push(
            useUIStore.subscribe((state) => {
                this.switchTab(state.activeTab);
            })
        );
    }

    private updateUI(): void {
        const uiStore = useUIStore.getState();
        this.switchTab(uiStore.activeTab);
    }

    private switchTab(activeTab: 'editor' | 'graph' | 'board'): void {
        // タブボタンの更新
        this.tabButtons.forEach((button) => {
            const tabName = button.dataset.tab;
            if (tabName === activeTab) {
                button.classList.add('active');
            } else {
                button.classList.remove('active');
            }
        });

        // タブコンテンツの更新
        this.tabContents.forEach((content) => {
            const tabId = content.id;
            if (tabId === `${activeTab}-tab`) {
                content.classList.add('active');
            } else {
                content.classList.remove('active');
            }
        });
    }

    destroy(): void {
        this.unsubscribe.forEach((unsub) => unsub());
        this.unsubscribe = [];
    }
}
