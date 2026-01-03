import { BaseComponent } from './component-base';
import { useUIStore, useDocStore, useCustomCssStore } from '../stores/index';
import GraphModule from '../graph-d3.js';
import type { WailsAppAPI } from '../types/wails-api';
import type { GraphData } from '../types/wails-api';
import { eventLogger } from '../utils/event-logger';
import { applyCustomCssToHtml } from '../utils/custom-css';

export class GraphView extends BaseComponent {
    private unsubscribe: (() => void)[] = [];
    private api: WailsAppAPI;
    private graphModule: GraphModule | null = null;
    private toggleTagNodesBtn: HTMLButtonElement | null = null;

    constructor(api: WailsAppAPI, parent?: HTMLElement) {
        super(parent);
        this.api = api;
    }

    init(): void {
        eventLogger.log('GraphView', 'init');
        
        const graphContainer = document.getElementById('graph-container');
        if (!graphContainer) {
            console.error('GraphView: #graph-container element not found');
            return;
        }
        this.element = graphContainer as HTMLElement;

        // グラフモジュールの初期化
        this.initGraphModule();

        // イベントリスナーの設定
        this.setupEventListeners();

        // 状態の購読
        this.subscribeToStores();

        // グラフデータの読み込み
        this.loadGraphData();
    }

    private initGraphModule(): void {
        try {
            this.graphModule = new GraphModule('graph-container');
            
            // ノードクリックイベント
            this.graphModule.on('node:click', (data: { id?: string; ID?: string }) => {
                const nodeId = data.id || data.ID;
                if (nodeId && nodeId.startsWith('doc:/')) {
                    this.handleNodeClick(nodeId);
                }
            });
        } catch (error) {
            console.error('Failed to initialize graph module:', error);
        }
    }

    private setupEventListeners(): void {
        // タグノード表示トグルボタン
        this.toggleTagNodesBtn = document.getElementById('toggleTagNodesBtn') as HTMLButtonElement;
        if (this.toggleTagNodesBtn) {
            this.unsubscribe.push(
                this.addEventListener(this.toggleTagNodesBtn, 'click', () => {
                    if (this.graphModule) {
                        this.graphModule.toggleTagNodes();
                        const showTagNodes = this.graphModule.showTagNodes;
                        eventLogger.log('GraphView', 'tag-nodes-toggle', { showTagNodes });
                        if (this.toggleTagNodesBtn) {
                            this.toggleTagNodesBtn.textContent = `タグノード表示: ${showTagNodes ? 'ON' : 'OFF'}`;
                        }
                    }
                })
            );
        }
    }

    private subscribeToStores(): void {
        // Doc Store - 現在のファイルパスが変更されたらグラフを更新
        this.unsubscribe.push(
            useDocStore.subscribe((state) => {
                if (state.currentPath && this.graphModule) {
                    const focusId = state.currentPath.startsWith('content/')
                        ? `doc:/${state.currentPath.replace('content/', '')}`
                        : `doc:/${state.currentPath}`;
                    this.graphModule.setFocus({ roots: [focusId], depth: 3 });
                }
            })
        );
    }

    private async loadGraphData(): Promise<void> {
        try {
            eventLogger.log('GraphView', 'load-graph-data-start');
            const graphData = await this.api.GetGraphData();
            this.updateGraph(graphData);
            eventLogger.log('GraphView', 'load-graph-data-success', { 
                nodeCount: graphData.nodes?.length || 0,
                edgeCount: graphData.edges?.length || 0
            });
        } catch (error) {
            console.error('Failed to load graph data:', error);
            eventLogger.log('GraphView', 'load-graph-data-error', { error: String(error) });
            useUIStore.getState().setStatusMessage('グラフデータの読み込みに失敗しました', 3000);
        }
    }

    private updateGraph(graphData: GraphData): void {
        if (!this.graphModule) {
            return;
        }

        this.graphModule.setData(graphData);

        // 現在のファイルにフォーカス
        const docStore = useDocStore.getState();
        if (docStore.currentPath) {
            const focusId = docStore.currentPath.startsWith('content/')
                ? `doc:/${docStore.currentPath.replace('content/', '')}`
                : `doc:/${docStore.currentPath}`;
            this.graphModule.setFocus({ roots: [focusId], depth: 3 });
        }
    }

    private handleNodeClick(nodeId: string): void {
        // ドキュメントIDからファイルパスに変換
        const filePath = nodeId.replace('doc:/', 'content/');
        eventLogger.log('GraphView', 'node-click', { nodeId, filePath });
        
        // ファイルを読み込む
        this.loadFile(filePath);
        
        // エディタタブに切り替え
        useUIStore.getState().setActiveTab('editor');
    }

    private async loadFile(path: string): Promise<void> {
        const docStore = useDocStore.getState();

        // 未保存の変更がある場合は確認
        if (docStore.hasUnsavedChanges) {
            const confirmed = window.confirm('未保存の変更があります。保存せずに続行しますか？');
            if (!confirmed) {
                return;
            }
        }

        try {
            const content = await this.api.LoadFile(path);
            docStore.setCurrentPath(path);
            if (path.toLowerCase().endsWith('.pdf')) {
                docStore.setMarkdownContent('');
                docStore.setPreviewHtml('');
                docStore.clearUnsavedChanges();
                return;
            }

            docStore.setMarkdownContent(content);
            docStore.clearUnsavedChanges();

            // プレビューを更新
            const html = await this.api.PreviewMarkdown(content);
            const finalHtml = this.buildPreviewHtml(content, html);
            docStore.setPreviewHtml(finalHtml);

            // iframeに反映
            const preview = document.getElementById('preview') as HTMLIFrameElement;
            if (preview && preview.contentDocument) {
                preview.contentDocument.open();
                preview.contentDocument.write(finalHtml);
                preview.contentDocument.close();
            }
        } catch (error) {
            console.error('Failed to load file:', error);
            useUIStore.getState().setStatusMessage('ファイルの読み込みに失敗しました', 3000);
        }
    }

    private buildPreviewHtml(content: string, html: string): string {
        const customCss = useCustomCssStore.getState().customCss;
        const theme = useUIStore.getState().theme;
        return applyCustomCssToHtml(content, html, customCss, theme);
    }

    async refresh(): Promise<void> {
        await this.loadGraphData();
    }

    destroy(): void {
        this.unsubscribe.forEach((unsub) => unsub());
        this.unsubscribe = [];
        
        // グラフモジュールのクリーンアップ
        if (this.graphModule) {
            // GraphModuleにdestroyメソッドがあれば呼び出す
            if (typeof (this.graphModule as unknown as { destroy?: () => void }).destroy === 'function') {
                (this.graphModule as unknown as { destroy: () => void }).destroy();
            }
            this.graphModule = null;
        }
    }
}
