import { BaseComponent } from './component-base';
import { useUIStore, useDocStore, useCustomCssStore, useBoardStore } from '../stores/index';
import GraphModule from '../graph-d3';
import type { BoardDocument, WailsAppAPI } from '../types/wails-api';
import type { GraphData } from '../types/wails-api';
import { eventLogger } from '../utils/event-logger';
import { applyCustomCssToHtml } from '../utils/custom-css';
import { renderMarkdownPreview } from '../utils/preview-renderer';
import { writePreviewFrame } from '../utils/preview-frame';
import { convertTimestampsToLinks } from '../utils/preview-audio';

export class GraphView extends BaseComponent {
    private unsubscribe: (() => void)[] = [];
    private api: WailsAppAPI;
    private graphModule: GraphModule | null = null;
    private toggleTagNodesBtn: HTMLButtonElement | null = null;
    private openBoardBtn: HTMLButtonElement | null = null;

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
                    void this.handleNodeClick(nodeId);
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

        this.openBoardBtn = document.getElementById('openBoardBtn') as HTMLButtonElement;
        if (this.openBoardBtn) {
            this.unsubscribe.push(
                this.addEventListener(this.openBoardBtn, 'click', async () => {
                    const currentPath = useDocStore.getState().currentPath;
                    if (!currentPath) {
                        useUIStore.getState().setStatusMessage('先に対象ファイルを選択してください', 2000);
                        return;
                    }
                    try {
                        const board = await this.api.CreateBoardForResource(currentPath);
                        this.applyBoardDocument(board);
                        eventLogger.log('GraphView', 'open-board', { currentPath, boardPath: board.path });
                    } catch (error) {
                        console.error('Failed to open board:', error);
                        useUIStore.getState().setStatusMessage('コルクボードを開けませんでした', 2500);
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

    private async handleNodeClick(nodeId: string): Promise<void> {
        // ドキュメントIDからファイルパスに変換
        const filePath = nodeId.replace('doc:/', 'content/');
        eventLogger.log('GraphView', 'node-click', { nodeId, filePath });
        
        // ファイルを読み込む
        await this.loadFile(filePath);
        if (!filePath.toLowerCase().endsWith('.board.md')) {
            useUIStore.getState().setActiveTab('editor');
        }
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
            if (path.toLowerCase().endsWith('.board.md')) {
                const board = await this.api.LoadBoard(path);
                this.applyBoardDocument(board);
                return;
            }
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
            const { prepared, html } = await renderMarkdownPreview(content, this.api, path);
            const finalHtml = this.buildPreviewHtml(prepared, html);
            docStore.setPreviewHtml(finalHtml);

            // iframeに反映
            const preview = document.getElementById('preview') as HTMLIFrameElement;
            if (preview) {
                writePreviewFrame(preview, finalHtml);
            }
        } catch (error) {
            console.error('Failed to load file:', error);
            useUIStore.getState().setStatusMessage('ファイルの読み込みに失敗しました', 3000);
        }
    }

    private buildPreviewHtml(content: string, html: string): string {
        const customCss = useCustomCssStore.getState().customCss;
        const theme = useUIStore.getState().theme;
        const withCss = applyCustomCssToHtml(content, html, customCss, theme);
        return convertTimestampsToLinks(withCss);
    }

    private applyBoardDocument(board: BoardDocument): void {
        useBoardStore.getState().setBoard(board);
        useDocStore.getState().setCurrentPath(board.path);
        useDocStore.getState().setMarkdownContent(board.rawContent);
        useDocStore.getState().setPreviewHtml('');
        useDocStore.getState().clearUnsavedChanges();
        useUIStore.getState().setActiveTab('board');
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
