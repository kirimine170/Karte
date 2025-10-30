// D3.jsベースのグラフ描画モジュール

import * as d3 from 'd3';

class GraphD3Module {
    constructor(containerId) {
        // d3 is imported via ES module bundler (Vite)

        this.container = document.getElementById(containerId);
        this.data = { nodes: [], edges: [] };
        this.style = {
            nodeSize: { min: 16, max: 48 },
            edgeWidth: { min: 1, max: 6 },
            palette: {
                note: '#3498db',
                'asset:image': '#e74c3c',
                missing: '#95a5a6',
                wikilink: '#2c3e50',
                markdown_link: '#34495e',
                quote: '#f39c12',
                img: '#9b59b6'
            }
        };
        this.focus = { roots: [], depth: 3 };
        this.eventHandlers = {};

        this.svg = null;
        this.simulation = null;
        this.nodeElements = null;
        this.edgeElements = null;
        this.labelElements = null;

        this.tooltip = null;

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

        // SVG要素を作成
        const width = this.container.clientWidth || 800;
        const height = this.container.clientHeight || 600;

        console.log('Creating SVG with dimensions:', { width, height });

        this.svg = d3.select(this.container)
            .append('svg')
            .attr('width', width)
            .attr('height', height);

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

    setupEventListeners() {
        // リサイズイベント
        window.addEventListener('resize', () => {
            const width = this.container.clientWidth;
            const height = this.container.clientHeight;
            this.svg.attr('width', width).attr('height', height);
        });
    }

    // API メソッド

    setData(graph) {
        console.log('GraphD3Module.setData called with:', graph);
        this.data = graph;

        if (graph && graph.nodes && graph.edges) {
            console.log('Adding elements to D3:', {
                nodes: graph.nodes.length,
                edges: graph.edges.length
            });

            this.render();
        } else {
            console.warn('Invalid graph data structure:', graph);
        }
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
        if (!this.eventHandlers[event]) {
            this.eventHandlers[event] = [];
        }
        this.eventHandlers[event].push(handler);
    }

    emit(event, data) {
        if (this.eventHandlers[event]) {
            this.eventHandlers[event].forEach(handler => handler(data));
        }
    }

    // 内部メソッド

    render() {
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

        if (!this.data.nodes || !this.data.nodes.length) {
            console.warn('No nodes to render');
            return;
        }

        console.log('Starting to render nodes and edges');

        const width = this.container.clientWidth;
        const height = this.container.clientHeight || 600;

        // Force simulationを作成
        this.simulation = d3.forceSimulation(this.data.nodes)
            .force('link', d3.forceLink(this.data.edges)
                .id(d => d.id)
                .distance(100)
            )
            .force('charge', d3.forceManyBody().strength(-500))
            .force('center', d3.forceCenter(width / 2, height / 2))
            .force('collision', d3.forceCollide().radius(30));

        // エッジを描画
        this.edgeElements = this.svg.append('g')
            .selectAll('line')
            .data(this.data.edges)
            .enter()
            .append('line')
            .attr('stroke', d => this.getEdgeColor(d.kind))
            .attr('stroke-width', d => this.getEdgeWidth(d.weight))
            .attr('stroke-dasharray', d => this.getEdgeDash(d.kind))
            .attr('marker-end', 'url(#arrowhead)');

        // ノードを描画
        this.nodeElements = this.svg.append('g')
            .selectAll('circle')
            .data(this.data.nodes)
            .enter()
            .append('circle')
            .attr('r', d => this.getNodeSize(d))
            .attr('fill', d => this.getNodeColor(d))
            .attr('stroke', '#fff')
            .attr('stroke-width', 2)
            .call(this.drag());

        // ラベルを描画
        this.labelElements = this.svg.append('g')
            .selectAll('text')
            .data(this.data.nodes)
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

        // シミュレーションを開始
        this.simulation.on('tick', () => {
            this.edgeElements
                .attr('x1', d => d.source.x)
                .attr('y1', d => d.source.y)
                .attr('x2', d => d.target.x)
                .attr('y2', d => d.target.y);

            this.nodeElements
                .attr('cx', d => d.x)
                .attr('cy', d => d.y);

            this.labelElements
                .attr('x', d => d.x)
                .attr('y', d => d.y);
        });

        // シミュレーション開始後にビューを調整
        setTimeout(() => {
            this.centerView();
        }, 500);
    }

    drag() {
        const drag = d3.drag()
            .on('start', (event, d) => {
                if (!event.active) this.simulation.alphaTarget(0.3).restart();
                d.fx = d.x;
                d.fy = d.y;
            })
            .on('drag', (event, d) => {
                d.fx = event.x;
                d.fy = event.y;
                // リアルタイム斥力を適用
                this.applyRealTimeRepulsion(d);
            })
            .on('end', (event, d) => {
                if (!event.active) this.simulation.alphaTarget(0);
                d.fx = null;
                d.fy = null;
            });
        return drag;
    }

    applyRealTimeRepulsion(draggedNode) {
        const repulsionStrength = 1000;
        const repulsionRadius = 150;

        this.data.nodes.forEach(node => {
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
        if (this.data.nodes.length === 0) return;

        // すべてのノードの重心を計算
        const sumX = this.data.nodes.reduce((sum, d) => sum + (d.x || 0), 0);
        const sumY = this.data.nodes.reduce((sum, d) => sum + (d.y || 0), 0);
        const centerX = sumX / this.data.nodes.length;
        const centerY = sumY / this.data.nodes.length;

        console.log(`Centering view on centroid: (${centerX.toFixed(2)}, ${centerY.toFixed(2)})`);

        // ビューを調整（ズーム/パンの実装は省略）
        const width = this.container.clientWidth;
        const height = this.container.clientHeight;

        const offsetX = width / 2 - centerX;
        const offsetY = height / 2 - centerY;

        this.data.nodes.forEach(d => {
            d.x += offsetX;
            d.y += offsetY;
        });

        this.simulation.restart();
    }

    applyFocus() {
        // フォーカス機能の実装（簡易版）
        if (!this.focus.roots || this.focus.roots.length === 0) {
            return;
        }

        // フォーカスされたノードを強調
        this.nodeElements
            .attr('opacity', d => this.focus.roots.includes(d.id) ? 1 : 0.3)
            .attr('stroke-width', d => this.focus.roots.includes(d.id) ? 3 : 2);

        setTimeout(() => {
            this.centerView();
        }, 100);
    }

    showTooltip(event, data) {
        const content = `
            <div style="font-weight: bold; margin-bottom: 4px;">${data.label || 'Unknown'}</div>
            <div><strong>ID:</strong> ${data.id || 'N/A'}</div>
            <div><strong>Type:</strong> ${data.kind || 'N/A'}</div>
            <div><strong>Exists:</strong> ${data.exists ? 'Yes' : 'No'}</div>
            <div><strong>Incoming:</strong> ${data.degIn || 0}</div>
            <div><strong>Outgoing:</strong> ${data.degOut || 0}</div>
        `;

        this.tooltip
            .style('left', (event.pageX + 10) + 'px')
            .style('top', (event.pageY - 10) + 'px')
            .html(content)
            .style('opacity', 1);
    }

    hideTooltip() {
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
        const kind = node.kind;
        const exists = node.exists;
        if (!exists) return this.style.palette.missing;
        return this.style.palette[kind] || this.style.palette.note;
    }

    getEdgeColor(kind) {
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

    destroy() {
        if (this.simulation) {
            this.simulation.stop();
        }
        if (this.tooltip) {
            this.tooltip.remove();
        }
    }
}

export default GraphD3Module;

