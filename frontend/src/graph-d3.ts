// @ts-nocheck
// D3.jsベースのグラフ描画モジュール

import * as d3 from 'd3';

class GraphD3Module {
    constructor(containerId) {
        // d3 is imported via ES module bundler (Vite)

        this.containerId = containerId;
        this.container = document.getElementById(containerId);
        this.data = { nodes: [], edges: [] };
        this.style = {
            nodeSize: { min: 16, max: 48 },
            edgeWidth: { min: 1, max: 6 },
            palette: {
                note: '#3498db',
                board: '#b7791f',
                'asset:image': '#e74c3c',
                missing: '#95a5a6',
                wikilink: '#2c3e50',
                markdown_link: '#34495e',
                quote: '#f39c12',
                img: '#9b59b6',
                tag: '#10b981' // Green color for tag nodes
            }
        };
        this.showTagNodes = true; // Default: show tag nodes
        this.focus = { roots: [], depth: 3 };
        this.eventHandlers = {};

        this.svg = null;
        this.scene = null;
        this.simulation = null;
        this.nodeElements = null;
        this.edgeElements = null;
        this.labelElements = null;
        this.zoomBehavior = null;
        this.zoomTransform = d3.zoomIdentity;

        this.tooltip = null;
        this.resizeObserver = null;
        this.handleResize = null;
        this.windowResizeHandler = null;
        this.visibilityChangeHandler = null;
        this.resizeTimer = null;
        this.renderCenterTimer = null;
        this.focusCenterTimer = null;
        this.viewActive = true;
        this.simulationRunning = false;
        this.simulationNeedsRestart = false;
        this.centerViewPending = false;
        this.resizePending = false;
        this.destroyed = false;

        this.init();
    }

    init() {
        if (!this.container) {
            throw new Error(`Container with id '${this.containerId}' not found`);
        }

        console.log('Initializing SVG for container:', this.container);
        console.log('Container dimensions:', {
            width: this.container.clientWidth,
            height: this.container.clientHeight,
            offsetWidth: this.container.offsetWidth,
            offsetHeight: this.container.offsetHeight
        });

        // SVG要素を作成（viewBoxなし、CSSのみでサイズ追従）
        // エディタ側（textarea/iframe）と同じアプローチ：CSSのみで親要素に追従
        this.svg = d3.select(this.container)
            .append('svg')
            .attr('width', '100%')
            .attr('height', '100%')
            .style('display', 'block');

        this.scene = this.svg.append('g').attr('class', 'graph-scene');

        console.log('SVG created successfully:', !!this.svg);

        // ツールチップ用のdivを作成
        this.tooltip = d3.select('body')
            .append('div')
            .attr('id', 'graph-tooltip')
            .style('opacity', 0)
            .style('position', 'absolute')
            .style('background', 'rgba(0, 0, 0, 0.9)')
            .style('color', 'white')
            .style('padding', '8px 12px')
            .style('border-radius', '4px')
            .style('font-size', '12px')
            .style('pointer-events', 'none')
            .style('z-index', '1000');

        // イベントリスナーを設定
        this.setupEventListeners();
    }

    updateSimulationSize() {
        // viewBoxなしの実装：コンテナサイズを直接取得してシミュレーションに反映
        if (this.destroyed || !this.container) return;

        const rect = this.container.getBoundingClientRect();
        if (!rect || rect.width <= 0 || rect.height <= 0) return;

        const width = rect.width;
        const height = rect.height;

        // シミュレーションのサイズを更新
        if (this.simulation) {
            const centerForce = this.simulation.force('center');
            if (centerForce) {
                centerForce.x(width / 2).y(height / 2);
            }
        }
    }

    setupEventListeners() {
        // リサイズイベント（viewBoxなしなので、シミュレーションサイズのみ更新）
        this.handleResize = () => {
            if (this.destroyed) return;
            if (!this.canRunSimulation()) {
                this.resizePending = true;
                return;
            }
            this.clearTimer('resizeTimer');
            this.resizeTimer = window.setTimeout(() => {
                this.resizeTimer = null;
                if (this.destroyed) return;
                // シミュレーションサイズを更新（SVGはCSSで自動的に追従）
                this.updateSimulationSize();

                // hidden／非active中はalphaだけを更新し，復帰までtimerを開始しない．
                if (this.simulation) {
                    this.simulation.alpha(0.3);
                    this.simulationNeedsRestart = true;
                    this.resumeSimulationIfNeeded();
                }
            }, 50);
        };

        this.windowResizeHandler = () => {
            if (this.destroyed || !this.handleResize) return;
            this.handleResize();
        };
        window.addEventListener('resize', this.windowResizeHandler);

        this.visibilityChangeHandler = () => {
            if (this.destroyed) return;
            if (document.hidden) {
                this.pauseSimulation();
            } else {
                this.resumeSimulationIfNeeded();
            }
        };
        document.addEventListener('visibilitychange', this.visibilityChangeHandler);

        // ResizeObserverでコンテナサイズの変化を監視
        if (typeof ResizeObserver !== 'undefined') {
            this.resizeObserver = new ResizeObserver(this.handleResize);
            this.resizeObserver.observe(this.container);
        }
    }

    setActive(active) {
        if (this.destroyed) return;
        this.viewActive = Boolean(active);
        if (this.viewActive) {
            this.resumeSimulationIfNeeded();
        } else {
            this.pauseSimulation();
        }
    }

    // API メソッド

    setData(graph) {
        console.log('GraphD3Module.setData called with:', graph);
        if (!graph || (!graph.nodes && !graph.Nodes)) {
            console.warn('setData called with invalid graph data');
            return;
        }
        // Normalize data structure (support both lowercase and uppercase field names)
        this.data = {
            nodes: graph.nodes || graph.Nodes || [],
            edges: graph.edges || graph.Edges || []
        };
        // Normalize node IDs (convert ID to id for consistency)
        this.data.nodes = this.data.nodes.map(node => {
            if (node.ID && !node.id) {
                node.id = node.ID;
            }
            if (node.Kind && !node.kind) {
                node.kind = node.Kind;
            }
            if (node.Label && !node.label) {
                node.label = node.Label;
            }
            if (node.Tags && !node.tags) {
                node.tags = node.Tags;
            }
            // Debug: log tag nodes
            if (node.kind === 'tag' || node.Kind === 'tag') {
                console.log('Found tag node:', node.id || node.ID, node.label || node.Label);
            }
            return node;
        });

        // Count tag nodes
        const tagNodeCount = this.data.nodes.filter(n => (n.kind || n.Kind) === 'tag').length;
        const tagEdgeCount = this.data.edges.filter(e => (e.kind || e.Kind) === 'tag').length;
        console.log(`Graph data: ${this.data.nodes.length} nodes (${tagNodeCount} tag nodes), ${this.data.edges.length} edges (${tagEdgeCount} tag edges)`);
        // Normalize edge IDs and references
        this.data.edges = this.data.edges.map(edge => {
            if (edge.ID && !edge.id) {
                edge.id = edge.ID;
            }
            if (edge.Kind && !edge.kind) {
                edge.kind = edge.Kind;
            }
            if (edge.Source && !edge.source) {
                edge.source = edge.Source;
            }
            if (edge.Target && !edge.target) {
                edge.target = edge.Target;
            }
            return edge;
        });
        console.log('Normalized data:', this.data);
        this.render();
    }

    // Toggle tag nodes visibility
    toggleTagNodes() {
        this.showTagNodes = !this.showTagNodes;
        this.render();
    }

    areTagNodesVisible() {
        return this.showTagNodes;
    }

    mergeData(patch) {
        // 差分更新（簡易実装）
        if (patch.nodes) {
            patch.nodes.forEach(node => {
                const index = this.data.nodes.findIndex(n => n.id === node.id);
                if (index >= 0) {
                    this.data.nodes[index] = node;
                } else {
                    this.data.nodes.push(node);
                }
            });
        }
        if (patch.edges) {
            patch.edges.forEach(edge => {
                const index = this.data.edges.findIndex(e => e.id === edge.id);
                if (index >= 0) {
                    this.data.edges[index] = edge;
                } else {
                    this.data.edges.push(edge);
                }
            });
        }
        this.render();
    }

    setFocus({ roots, depth }) {
        this.focus = { roots, depth };
        this.applyFocus();
    }

    setStyle(style) {
        this.style = { ...this.style, ...style };
        this.render();
    }

    setLayout({ name, options = {} }) {
        console.log(`Setting layout to: ${name}`);
        this.render();
    }

    on(event, handler) {
        if (this.destroyed) return;
        if (!this.eventHandlers[event]) {
            this.eventHandlers[event] = [];
        }
        this.eventHandlers[event].push(handler);
    }

    emit(event, data) {
        if (this.destroyed) return;
        if (this.eventHandlers[event]) {
            this.eventHandlers[event].forEach(handler => handler(data));
        }
    }

    // 内部メソッド

    render() {
        if (this.destroyed) return;
        this.stopCurrentSimulation();
        this.clearTimer('resizeTimer');
        this.clearTimer('renderCenterTimer');
        this.clearTimer('focusCenterTimer');
        this.centerViewPending = false;
        this.resizePending = false;
        console.log('render() called');
        console.log('SVG exists:', !!this.svg);
        console.log('Data nodes:', this.data.nodes?.length || 0);
        console.log('Data edges:', this.data.edges?.length || 0);

        if (!this.svg) {
            console.error('SVG is not initialized');
            return;
        }

        // 既存の要素を削除
        this.svg.selectAll('*').remove();
        this.scene = this.svg.append('g').attr('class', 'graph-scene');

        if (!this.data.nodes || !this.data.nodes.length) {
            console.warn('No nodes to render');
            return;
        }

        // 非表示tag nodeとそれに接続するedgeはDOMだけでなくsimulationからも除外する．
        const visibleNodes = this.data.nodes.filter(d => {
            const kind = d.kind || d.Kind;
            return kind !== 'tag' || this.showTagNodes;
        });
        const visibleNodeIds = new Set(visibleNodes.map(node => node.id || node.ID));
        const visibleEdges = this.data.edges.filter(edge => {
            const kind = edge.kind || edge.Kind;
            if (kind === 'tag' && !this.showTagNodes) return false;
            const sourceId = getEdgeEndpointId(edge.source || edge.Source);
            const targetId = getEdgeEndpointId(edge.target || edge.Target);
            return visibleNodeIds.has(sourceId) && visibleNodeIds.has(targetId);
        });
        if (visibleNodes.length === 0) {
            console.warn('No visible nodes to render');
            return;
        }

        console.log('Starting to render nodes and edges');

        // コンテナサイズをforce simulationに渡すために取得
        // viewBoxなしなので、ピクセル単位で直接使用
        const rect = this.container.getBoundingClientRect();
        const width = rect.width > 0 ? rect.width : (this.container.clientWidth || 800);
        const height = rect.height > 0 ? rect.height : (this.container.clientHeight || 600);

        // カスタムforce: 枠への斥力
        const self = this;
        function forceBoundary(alpha) {
            const margin = 50; // マージン距離
            const strength = 0.3; // 斥力の強度

            // 現在のコンテナサイズを取得（固定値として使用）
            const rect = self.container.getBoundingClientRect();
            if (!rect || rect.width <= 0 || rect.height <= 0) return; // 無効なサイズの場合は処理をスキップ

            const currentWidth = Math.floor(rect.width);
            const currentHeight = Math.floor(rect.height);

            for (let i = 0; i < visibleNodes.length; i++) {
                const node = visibleNodes[i];
                if (node.x == null || node.y == null) continue;

                // ノードの半径を取得
                const nodeRadius = self.getNodeSize(node);

                // 左端への斥力
                if (node.x < margin + nodeRadius) {
                    const distance = margin + nodeRadius - node.x;
                    node.vx = (node.vx || 0) + (distance * strength * alpha);
                }
                // 右端への斥力
                if (node.x > currentWidth - margin - nodeRadius) {
                    const distance = node.x - (currentWidth - margin - nodeRadius);
                    node.vx = (node.vx || 0) - (distance * strength * alpha);
                }
                // 上端への斥力
                if (node.y < margin + nodeRadius) {
                    const distance = margin + nodeRadius - node.y;
                    node.vy = (node.vy || 0) + (distance * strength * alpha);
                }
                // 下端への斥力
                if (node.y > currentHeight - margin - nodeRadius) {
                    const distance = node.y - (currentHeight - margin - nodeRadius);
                    node.vy = (node.vy || 0) - (distance * strength * alpha);
                }
            }
        }

        // Force simulationを作成（表示対象だけを使用）
        this.simulation = d3.forceSimulation(visibleNodes)
            .force('link', d3.forceLink(visibleEdges)
                .id(d => d.id || d.ID)
                .distance(100)
            )
            .force('charge', d3.forceManyBody().strength(-500))
            .force('center', d3.forceCenter(width / 2, height / 2).strength(0.1))
            .force('collision', d3.forceCollide().radius(30))
            .force('boundary', forceBoundary);
        this.simulation.stop();
        this.simulationRunning = false;
        this.simulationNeedsRestart = true;

        this.edgeElements = this.scene.append('g')
            .selectAll('line')
            .data(visibleEdges)
            .enter()
            .append('line')
            .attr('stroke', d => this.getEdgeColor(d))
            .attr('stroke-width', d => this.getEdgeWidth(d.weight))
            .attr('stroke-dasharray', d => this.getEdgeDash(d.kind))
            .attr('marker-end', 'url(#arrowhead)')
            .attr('class', d => d.targetUpdated ? 'edge-updated' : 'edge-normal')
            .attr('cursor', 'pointer')
            .on('mouseover', (event, d) => {
                this.showTooltip(event, d);
            })
            .on('mouseout', () => {
                this.hideTooltip();
            });

        this.nodeElements = this.scene.append('g')
            .selectAll('circle')
            .data(visibleNodes)
            .enter()
            .append('circle')
            .attr('r', d => this.getNodeSize(d))
            .attr('fill', d => this.getNodeColor(d))
            .attr('stroke', '#fff')
            .attr('stroke-width', 2)
            .call(this.drag());

        // ラベルを描画
        this.labelElements = this.scene.append('g')
            .selectAll('text')
            .data(visibleNodes)
            .enter()
            .append('text')
            .text(d => d.label)
            .attr('dx', 0)
            .attr('dy', 5)
            .attr('text-anchor', 'middle')
            .attr('fill', '#fff')
            .attr('font-size', '12px')
            .attr('pointer-events', 'none');

        // 矢印マーカーを定義
        const defs = this.svg.append('defs');
        const marker = defs.append('marker')
            .attr('id', 'arrowhead')
            .attr('viewBox', '0 -5 10 10')
            .attr('refX', 25)
            .attr('refY', 0)
            .attr('markerWidth', 6)
            .attr('markerHeight', 6)
            .attr('orient', 'auto');

        marker.append('path')
            .attr('d', 'M0,-5L10,0L0,5')
            .attr('fill', '#999');

        this.setupZoomBehavior();

        // イベントリスナー
        this.nodeElements
            .on('click', (event, d) => {
                this.emit('node:click', d);
            })
            .on('mouseover', (event, d) => {
                this.showTooltip(event, d);
            })
            .on('mouseout', () => {
                this.hideTooltip();
            });

        // lifecycle外の旧tickがDOMを書き換えないようsimulation identityも検証する．
        const simulation = this.simulation;
        simulation.on('tick', () => {
            if (this.destroyed || this.simulation !== simulation || !this.canRunSimulation()) return;
            this.edgeElements
                .attr('x1', d => {
                    // D3 forceLink converts source/target to node objects, but we may have IDs
                    const source = typeof d.source === 'object' ? d.source : visibleNodes.find(n => (n.id || n.ID) === (d.source || d.Source));
                    return source ? (source.x || 0) : 0;
                })
                .attr('y1', d => {
                    const source = typeof d.source === 'object' ? d.source : visibleNodes.find(n => (n.id || n.ID) === (d.source || d.Source));
                    return source ? (source.y || 0) : 0;
                })
                .attr('x2', d => {
                    const target = typeof d.target === 'object' ? d.target : visibleNodes.find(n => (n.id || n.ID) === (d.target || d.Target));
                    return target ? (target.x || 0) : 0;
                })
                .attr('y2', d => {
                    const target = typeof d.target === 'object' ? d.target : visibleNodes.find(n => (n.id || n.ID) === (d.target || d.Target));
                    return target ? (target.y || 0) : 0;
                });

            this.nodeElements
                .attr('cx', d => d.x)
                .attr('cy', d => d.y);

            this.labelElements
                .attr('x', d => d.x)
                .attr('y', d => d.y);
        });
        simulation.on('end.lifecycle', () => {
            if (this.destroyed || this.simulation !== simulation) return;
            this.simulationRunning = false;
            this.simulationNeedsRestart = false;
        });
        this.resumeSimulationIfNeeded();

        // シミュレーション開始後にビューを調整
        if (this.canRunSimulation()) {
            this.renderCenterTimer = window.setTimeout(() => {
                this.renderCenterTimer = null;
                if (this.destroyed || this.simulation !== simulation) return;
                this.centerViewWhenActive();
                // シミュレーションサイズを更新（SVGはCSSで自動的に追従）
                this.updateSimulationSize();
            }, 500);
        } else {
            this.centerViewPending = true;
        }
    }

    drag() {
        const drag = d3.drag()
            .on('start', (event, d) => {
                if (this.destroyed || !this.simulation) return;
                if (!event.active) {
                    this.simulation.alphaTarget(0.3);
                    this.simulationNeedsRestart = true;
                    this.resumeSimulationIfNeeded();
                }
                d.fx = d.x;
                d.fy = d.y;
            })
            .on('drag', (event, d) => {
                if (this.destroyed) return;
                d.fx = event.x;
                d.fy = event.y;
                // リアルタイム斥力を適用
                this.applyRealTimeRepulsion(d);
            })
            .on('end', (event, d) => {
                if (this.destroyed || !this.simulation) return;
                if (!event.active) this.simulation.alphaTarget(0);
                d.fx = null;
                d.fy = null;
            });
        return drag;
    }

    applyRealTimeRepulsion(draggedNode) {
        if (this.destroyed) return;
        const repulsionStrength = 1000;
        const repulsionRadius = 150;

        this.getSimulationNodes().forEach(node => {
            if (node.id === draggedNode.id) return;

            const dx = draggedNode.x - node.x;
            const dy = draggedNode.y - node.y;
            const distance = Math.sqrt(dx * dx + dy * dy);

            if (distance < repulsionRadius && distance > 0) {
                const force = repulsionStrength / (distance * distance);
                const normalizedX = dx / distance;
                const normalizedY = dy / distance;

                node.x -= normalizedX * force * 0.01;
                node.y -= normalizedY * force * 0.01;
            }
        });
    }

    centerView() {
        if (this.destroyed) return;
        const nodes = this.getSimulationNodes();
        if (nodes.length === 0) return;

        // すべてのノードの重心を計算
        const sumX = nodes.reduce((sum, d) => sum + (d.x || 0), 0);
        const sumY = nodes.reduce((sum, d) => sum + (d.y || 0), 0);
        const centerX = sumX / nodes.length;
        const centerY = sumY / nodes.length;

        console.log(`Centering view on centroid: (${centerX.toFixed(2)}, ${centerY.toFixed(2)})`);

        // ビューを調整（ズーム/パンの実装は省略）
        const width = this.container.clientWidth;
        const height = this.container.clientHeight;

        const scale = this.zoomTransform?.k || 1;
        this.zoomTransform = d3.zoomIdentity.translate(width / 2 - centerX * scale, height / 2 - centerY * scale).scale(scale);
        this.applyZoomTransform();
    }

    applyFocus() {
        if (this.destroyed) return;
        this.clearTimer('focusCenterTimer');
        // フォーカス機能の実装（簡易版）
        if (!this.focus.roots || this.focus.roots.length === 0) {
            return;
        }

        // フォーカスされたノードを強調
        if (!this.nodeElements) return;
        this.nodeElements
            .attr('opacity', d => this.focus.roots.includes(d.id) ? 1 : 0.3)
            .attr('stroke-width', d => this.focus.roots.includes(d.id) ? 3 : 2);

        if (this.canRunSimulation()) {
            this.focusCenterTimer = window.setTimeout(() => {
                this.focusCenterTimer = null;
                if (this.destroyed) return;
                this.centerViewWhenActive();
            }, 100);
        } else {
            this.centerViewPending = true;
        }
    }

    showTooltip(event, data) {
        if (this.destroyed || !this.tooltip) return;
        let content = '';

        const kind = data.kind || data.Kind;
        const label = data.label || data.Label;
        const id = data.id || data.ID;

        // ノードの場合
        if (kind && (kind === 'note' || kind === 'board' || kind === 'asset:image' || kind === 'tag')) {
            const tags = data.tags || data.Tags || [];
            content = `
                <div style="font-weight: bold; margin-bottom: 4px;">${label || 'Unknown'}</div>
                <div><strong>ID:</strong> ${id || 'N/A'}</div>
                <div><strong>Type:</strong> ${kind || 'N/A'}</div>
                ${kind !== 'tag' ? `<div><strong>Exists:</strong> ${(data.exists !== undefined ? data.exists : data.Exists) ? 'Yes' : 'No'}</div>` : ''}
                <div><strong>Incoming:</strong> ${data.degIn || data.DegIn || 0}</div>
                <div><strong>Outgoing:</strong> ${data.degOut || data.DegOut || 0}</div>
                ${tags.length > 0 ? `<div><strong>Tags:</strong> ${tags.join(', ')}</div>` : ''}
                ${data.hash || data.Hash ? `<div><strong>Hash:</strong> ${(data.hash || data.Hash).substring(0, 8)}...</div>` : ''}
            `;
        } else {
            // エッジの場合
            const source = data.source || data.Source || '';
            const target = data.target || data.Target || '';
            content = `
                <div style="font-weight: bold; margin-bottom: 4px;">Link: ${source.split('/').pop() || 'Unknown'} → ${target.split('/').pop() || 'Unknown'}</div>
                <div><strong>Type:</strong> ${kind || 'N/A'}</div>
                <div><strong>Weight:</strong> ${data.weight || data.Weight || 1}</div>
                ${(data.targetUpdated || data.TargetUpdated) ? '<div style="color: #f39c12;"><strong>⚠️ Target Updated</strong></div>' : ''}
                ${data.sourceHash || data.SourceHash ? `<div><strong>Source Hash:</strong> ${(data.sourceHash || data.SourceHash).substring(0, 8)}...</div>` : ''}
                ${data.targetHash || data.TargetHash ? `<div><strong>Target Hash:</strong> ${(data.targetHash || data.TargetHash).substring(0, 8)}...</div>` : ''}
            `;
        }

        this.tooltip
            .style('left', (event.pageX + 10) + 'px')
            .style('top', (event.pageY - 10) + 'px')
            .html(content)
            .style('opacity', 1);
    }

    hideTooltip() {
        if (this.destroyed || !this.tooltip) return;
        this.tooltip.style('opacity', 0);
    }

    // ヘルパーメソッド

    getNodeSize(node) {
        const deg = (node.degIn || 0) + (node.degOut || 0);
        return Math.max(
            this.style.nodeSize.min,
            Math.min(this.style.nodeSize.max, 16 + 4 * Math.sqrt(deg))
        );
    }

    getNodeColor(node) {
        const kind = node.kind || node.Kind;
        const exists = node.exists !== undefined ? node.exists : node.Exists;
        if (!exists) return this.style.palette.missing;
        return this.style.palette[kind] || this.style.palette.note;
    }

    getEdgeColor(edge) {
        const kind = edge.kind || edge.Kind;

        // ターゲットが更新された場合は警告色（オレンジ/黄色）
        if (edge.targetUpdated || edge.TargetUpdated) {
            return '#f39c12'; // オレンジ色
        }

        // タグエッジには特別な色
        if (kind === 'tag') {
            return '#10b981'; // タグノードと同じ緑色
        }

        return this.style.palette[kind] || this.style.palette.wikilink;
    }

    getEdgeWidth(weight) {
        return Math.max(
            this.style.edgeWidth.min,
            Math.min(this.style.edgeWidth.max, 1 + (weight || 1))
        );
    }

    getEdgeDash(kind) {
        switch (kind) {
            case 'quote': return '5,5';
            case 'img': return '3,3';
            default: return 'none';
        }
    }

    // ユーティリティメソッド

    fit() {
        // 実装省略
        console.log('fit() called');
    }

    center() {
        // 実装省略
        console.log('center() called');
    }

    canRunSimulation() {
        return !this.destroyed && this.viewActive && !document.hidden;
    }

    pauseSimulation() {
        if (this.destroyed) return;
        if (this.renderCenterTimer !== null || this.focusCenterTimer !== null) {
            this.centerViewPending = true;
        }
        if (this.resizeTimer !== null) {
            this.resizePending = true;
        }
        this.clearTimer('resizeTimer');
        this.clearTimer('renderCenterTimer');
        this.clearTimer('focusCenterTimer');
        if (!this.simulation) return;
        if (this.simulationRunning) {
            this.simulationNeedsRestart = true;
        }
        this.simulation.stop();
        this.simulationRunning = false;
    }

    resumeSimulationIfNeeded() {
        if (!this.canRunSimulation()) return;
        if (this.resizePending) {
            this.resizePending = false;
            this.updateSimulationSize();
            if (this.simulation) {
                this.simulation.alpha(0.3);
                this.simulationNeedsRestart = true;
            }
        }
        if (this.centerViewPending) {
            this.centerViewPending = false;
            this.centerView();
        }
        if (!this.simulation || !this.simulationNeedsRestart) return;
        this.simulation.restart();
        this.simulationRunning = true;
        this.simulationNeedsRestart = false;
    }

    centerViewWhenActive() {
        if (!this.canRunSimulation()) {
            this.centerViewPending = true;
            return;
        }
        this.centerViewPending = false;
        this.centerView();
    }

    getSimulationNodes() {
        if (!this.simulation || typeof this.simulation.nodes !== 'function') return [];
        return this.simulation.nodes();
    }

    stopCurrentSimulation() {
        const simulation = this.simulation;
        if (simulation) {
            simulation.on('tick', null);
            simulation.on('end.lifecycle', null);
            simulation.stop();
        }
        this.simulation = null;
        this.simulationRunning = false;
        this.simulationNeedsRestart = false;
    }

    clearTimer(field) {
        const timer = this[field];
        if (timer !== null) {
            window.clearTimeout(timer);
            this[field] = null;
        }
    }

    destroy() {
        if (this.destroyed) return;
        this.destroyed = true;
        this.clearTimer('resizeTimer');
        this.clearTimer('renderCenterTimer');
        this.clearTimer('focusCenterTimer');
        this.stopCurrentSimulation();
        if (this.tooltip) {
            this.tooltip.remove();
            this.tooltip = null;
        }
        if (this.windowResizeHandler) {
            window.removeEventListener('resize', this.windowResizeHandler);
            this.windowResizeHandler = null;
        }
        if (this.visibilityChangeHandler) {
            document.removeEventListener('visibilitychange', this.visibilityChangeHandler);
            this.visibilityChangeHandler = null;
        }
        if (this.resizeObserver) {
            this.resizeObserver.disconnect();
            this.resizeObserver = null;
        }
        this.svg?.remove();
        this.svg = null;
        this.scene = null;
        this.nodeElements = null;
        this.edgeElements = null;
        this.labelElements = null;
        this.zoomBehavior = null;
        this.handleResize = null;
        this.eventHandlers = {};
    }

    setupZoomBehavior() {
        if (!this.svg || !this.scene) {
            return;
        }

        this.zoomBehavior = d3.zoom()
            .scaleExtent([0.25, 4])
            .filter((event) => {
                const target = event.target;
                if (event.type === 'dblclick') {
                    return false;
                }
                if (target && target.closest && target.closest('circle')) {
                    return false;
                }
                return true;
            })
            .on('zoom', (event) => {
                this.zoomTransform = event.transform;
                this.applyZoomTransform();
            });

        this.svg.call(this.zoomBehavior);
        this.svg.on('wheel.zoom', null);
        this.svg.call(this.zoomBehavior).on('wheel.zoom', (event) => {
            event.preventDefault();
            this.zoomBehavior.wheelDelta(() => -event.deltaY * (event.ctrlKey ? 0.004 : 0.0025));
            this.zoomBehavior.scaleBy(this.svg, Math.pow(2, -event.deltaY * (event.ctrlKey ? 0.004 : 0.0025)), d3.pointer(event));
        });
        this.applyZoomTransform();
    }

    applyZoomTransform() {
        if (this.destroyed || !this.scene) {
            return;
        }
        this.scene.attr('transform', this.zoomTransform.toString());
    }
}

function getEdgeEndpointId(endpoint) {
    if (endpoint && typeof endpoint === 'object') {
        return endpoint.id || endpoint.ID;
    }
    return endpoint;
}

export default GraphD3Module;
