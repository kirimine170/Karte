import { BaseComponent } from './component-base';
import { useUIStore, useDocStore, useExportStore, useModalStore, useCustomCssStore } from '../stores/index';
import type { WailsAppAPI } from '../types/wails-api';
import { eventLogger } from '../utils/event-logger';

export class Topbar extends BaseComponent {
    private unsubscribe: (() => void)[] = [];
    private api: WailsAppAPI;

    // DOM要素
    private sidebarToggleBtn: HTMLButtonElement | null = null;
    private galleryToggleBtn: HTMLButtonElement | null = null;
    private csvToggleBtn: HTMLButtonElement | null = null;
    private themeSelect: HTMLSelectElement | null = null;
    private hardwrapCheckbox: HTMLInputElement | null = null;
    private saveBtn: HTMLButtonElement | null = null;
    private newBtn: HTMLButtonElement | null = null;
    private exportPdfBtn: HTMLButtonElement | null = null;
    private customCssBtn: HTMLButtonElement | null = null;
    private customCssStatus: HTMLElement | null = null;

    constructor(api: WailsAppAPI, parent?: HTMLElement) {
        super(parent);
        this.api = api;
    }

    init(): void {
        eventLogger.log('Topbar', 'init');

        const bar = document.querySelector('.bar');
        if (!bar) {
            console.error('Topbar: .bar element not found');
            return;
        }
        this.element = bar as HTMLElement;

        // DOM要素の取得
        this.sidebarToggleBtn = document.getElementById('sidebarToggle') as HTMLButtonElement;
        this.galleryToggleBtn = document.getElementById('galleryToggle') as HTMLButtonElement;
        this.csvToggleBtn = document.getElementById('csvToggle') as HTMLButtonElement;
        this.themeSelect = document.getElementById('theme') as HTMLSelectElement;
        this.hardwrapCheckbox = document.getElementById('hardwrap') as HTMLInputElement;
        this.saveBtn = document.getElementById('saveBtn') as HTMLButtonElement;
        this.newBtn = document.getElementById('newBtn') as HTMLButtonElement;
        this.exportPdfBtn = document.getElementById('exportPdfBtn') as HTMLButtonElement;
        this.customCssBtn = document.getElementById('customCssBtn') as HTMLButtonElement;
        this.customCssStatus = document.getElementById('customCssStatus');

        // デバッグ: ボタンの存在確認
        console.log('Topbar buttons:', {
            sidebarToggle: !!this.sidebarToggleBtn,
            galleryToggle: !!this.galleryToggleBtn,
            csvToggle: !!this.csvToggleBtn
        });

        // イベントリスナーの設定
        this.setupEventListeners();

        // 状態の購読
        this.subscribeToStores();

        // 初期状態の反映
        this.updateUI();
    }

    private setupEventListeners(): void {
        const docStore = useDocStore.getState();
        const modalStore = useModalStore.getState();

        // サイドバートグル
        if (this.sidebarToggleBtn) {
            this.unsubscribe.push(
                this.addEventListener(this.sidebarToggleBtn, 'click', () => {
                    const uiStore = useUIStore.getState();
                    const currentState = uiStore.sidebarVisible;
                    const newState = !currentState;
                    eventLogger.log('Topbar', 'sidebar-toggle', {
                        currentState,
                        newState,
                        visible: newState
                    });
                    uiStore.setSidebarVisible(newState);
                })
            );
        } else {
            console.error('Topbar: sidebarToggleBtn not found');
        }

        // ギャラリートグル
        if (this.galleryToggleBtn) {
            this.unsubscribe.push(
                this.addEventListener(this.galleryToggleBtn, 'click', () => {
                    const uiStore = useUIStore.getState();
                    const currentState = uiStore.imageGalleryVisible;
                    const newState = !currentState;
                    eventLogger.log('Topbar', 'gallery-toggle', {
                        currentState,
                        newState,
                        visible: newState
                    });
                    uiStore.setImageGalleryVisible(newState);
                })
            );
        } else {
            console.error('Topbar: galleryToggleBtn not found');
        }

        // CSVトグル
        if (this.csvToggleBtn) {
            this.unsubscribe.push(
                this.addEventListener(this.csvToggleBtn, 'click', () => {
                    const uiStore = useUIStore.getState();
                    const currentState = uiStore.csvGalleryVisible;
                    const newState = !currentState;
                    eventLogger.log('Topbar', 'csv-toggle', {
                        currentState,
                        newState,
                        visible: newState
                    });
                    uiStore.setCsvGalleryVisible(newState);
                })
            );
        } else {
            console.error('Topbar: csvToggleBtn not found');
        }

        // テーマ変更
        if (this.themeSelect) {
            this.unsubscribe.push(
                this.addEventListener(this.themeSelect, 'change', (e) => {
                    const target = e.target as HTMLSelectElement;
                    const theme = target.value as 'light' | 'dark' | 'hc';
                    eventLogger.log('Topbar', 'theme-change', { theme });
                    uiStore.setTheme(theme);
                })
            );
            this.unsubscribe.push(
                this.addEventListener(this.themeSelect, 'input', (e) => {
                    const target = e.target as HTMLSelectElement;
                    const theme = target.value as 'light' | 'dark' | 'hc';
                    eventLogger.log('Topbar', 'theme-change', { theme });
                    uiStore.setTheme(theme);
                })
            );
        }

        // ハードラップ
        if (this.hardwrapCheckbox) {
            this.unsubscribe.push(
                this.addEventListener(this.hardwrapCheckbox, 'change', (e) => {
                    const target = e.target as HTMLInputElement;
                    eventLogger.log('Topbar', 'hardwrap-change', { enabled: target.checked });
                    uiStore.setHardWrap(target.checked);
                })
            );
        }

        // 保存ボタン
        if (this.saveBtn) {
            this.unsubscribe.push(
                this.addEventListener(this.saveBtn, 'click', async () => {
                    eventLogger.log('Topbar', 'save-click');
                    await this.handleSave();
                })
            );
        }

        // 新規ボタン
        if (this.newBtn) {
            this.unsubscribe.push(
                this.addEventListener(this.newBtn, 'click', async () => {
                    eventLogger.log('Topbar', 'new-file-click');
                    const docStore = useDocStore.getState();
                    if (docStore.hasUnsavedChanges) {
                        modalStore.showUnsavedConfirmModal(
                            async () => {
                                const saved = await this.handleSave();
                                if (saved) {
                                    modalStore.showFilenameModal();
                                }
                            },
                            () => {
                                docStore.clearUnsavedChanges();
                                modalStore.showFilenameModal();
                            }
                        );
                        return;
                    }
                    modalStore.showFilenameModal();
                })
            );
        }

        // PDFエクスポートボタン
        if (this.exportPdfBtn) {
            this.unsubscribe.push(
                this.addEventListener(this.exportPdfBtn, 'click', async () => {
                    eventLogger.log('Topbar', 'export-pdf-click');
                    await this.handleExportPDF();
                })
            );
        }

        // カスタムCSSボタン
        if (this.customCssBtn) {
            this.unsubscribe.push(
                this.addEventListener(this.customCssBtn, 'click', async () => {
                    eventLogger.log('Topbar', 'custom-css-click');
                    await this.handleCustomCSS();
                })
            );
        }
    }

    private subscribeToStores(): void {
        // UI Store
        this.unsubscribe.push(
            useUIStore.subscribe((state) => {
                if (this.themeSelect) {
                    this.themeSelect.value = state.theme;
                }
                if (this.hardwrapCheckbox) {
                    this.hardwrapCheckbox.checked = state.hardWrap;
                }

                // ボタンのアクティブ状態を更新
                if (this.sidebarToggleBtn) {
                    if (state.sidebarVisible) {
                        this.sidebarToggleBtn.style.backgroundColor = 'var(--color-lavender-100)';
                    } else {
                        this.sidebarToggleBtn.style.backgroundColor = '';
                    }
                }

                if (this.galleryToggleBtn) {
                    if (state.imageGalleryVisible) {
                        this.galleryToggleBtn.style.backgroundColor = 'var(--color-lavender-100)';
                    } else {
                        this.galleryToggleBtn.style.backgroundColor = '';
                    }
                }

                if (this.csvToggleBtn) {
                    if (state.csvGalleryVisible) {
                        this.csvToggleBtn.style.backgroundColor = 'var(--color-lavender-100)';
                    } else {
                        this.csvToggleBtn.style.backgroundColor = '';
                    }
                }
            })
        );

        // Doc Store
        this.unsubscribe.push(
            useDocStore.subscribe((state) => {
                if (this.saveBtn) {
                    this.toggleClass(this.saveBtn, 'unsaved', state.hasUnsavedChanges);
                }
            })
        );

        // Custom CSS Store
        this.unsubscribe.push(
            useCustomCssStore.subscribe((state) => {
                this.updateCustomCssStatus(state.customCss);
            })
        );
    }

    private updateUI(): void {
        const uiStore = useUIStore.getState();
        const docStore = useDocStore.getState();

        if (this.themeSelect) {
            this.themeSelect.value = uiStore.theme;
        }
        if (this.hardwrapCheckbox) {
            this.hardwrapCheckbox.checked = uiStore.hardWrap;
        }
        if (this.saveBtn) {
            this.toggleClass(this.saveBtn, 'unsaved', docStore.hasUnsavedChanges);
        }
        this.updateCustomCssStatus(useCustomCssStore.getState().customCss);

        // ボタンの初期状態を反映
        if (this.sidebarToggleBtn) {
            if (uiStore.sidebarVisible) {
                this.sidebarToggleBtn.style.backgroundColor = 'var(--color-lavender-100)';
            }
        }
        if (this.galleryToggleBtn) {
            if (uiStore.imageGalleryVisible) {
                this.galleryToggleBtn.style.backgroundColor = 'var(--color-lavender-100)';
            }
        }
        if (this.csvToggleBtn) {
            if (uiStore.csvGalleryVisible) {
                this.csvToggleBtn.style.backgroundColor = 'var(--color-lavender-100)';
            }
        }
    }

    private updateCustomCssStatus(css: string): void {
        if (!this.customCssStatus) {
            return;
        }
        this.customCssStatus.textContent = css ? 'Custom CSS active' : '';
    }


    private async handleSave(): Promise<boolean> {
        const docStore = useDocStore.getState();
        if (!docStore.currentPath) {
            eventLogger.log('Topbar', 'save-error', { error: 'no-file-selected' });
            useUIStore.getState().setStatusMessage('ファイルが選択されていません', 2000);
            return false;
        }

        if (docStore.currentPath.toLowerCase().endsWith('.pdf')) {
            eventLogger.log('Topbar', 'save-error', { error: 'pdf-readonly' });
            useUIStore.getState().setStatusMessage('PDF閲覧中は保存できません', 2000);
            return false;
        }

        try {
            eventLogger.log('Topbar', 'save-start', { path: docStore.currentPath });
            await this.api.SaveFile(docStore.currentPath, docStore.markdownContent);
            docStore.clearUnsavedChanges();
            eventLogger.log('Topbar', 'save-success', { path: docStore.currentPath });
            useUIStore.getState().setStatusMessage('保存しました', 2000);
            return true;
        } catch (error) {
            console.error('Save failed:', error);
            eventLogger.log('Topbar', 'save-error', { error: String(error) });
            useUIStore.getState().setStatusMessage('保存に失敗しました', 3000);
            return false;
        }
    }

    private async handleExportPDF(): Promise<void> {
        const docStore = useDocStore.getState();
        const exportStore = useExportStore.getState();

        const iframe = document.getElementById('preview') as HTMLIFrameElement | null;
        const previewDoc = iframe?.contentDocument;
        const printoutMode = (previewDoc?.documentElement?.getAttribute('data-printout') || '').toLowerCase();
        const finitePrintout = Boolean(printoutMode && printoutMode !== 'infinite');
        const renderedHtml = await this.getRenderedPreviewHtml();
        const fallbackHtml = docStore.previewHtml;
        const exportHtml = renderedHtml || (finitePrintout ? null : fallbackHtml);

        if (!exportHtml) {
            if (finitePrintout) {
                eventLogger.log('Topbar', 'export-pdf-error', { error: 'printout-not-ready' });
                useUIStore.getState().setStatusMessage('改ページ処理が未完了です。数秒待ってから再実行してください', 3000);
                return;
            }
            eventLogger.log('Topbar', 'export-pdf-error', { error: 'no-content' });
            useUIStore.getState().setStatusMessage('エクスポートするコンテンツがありません', 2000);
            return;
        }

        try {
            eventLogger.log('Topbar', 'export-pdf-start');
            exportStore.setPdfExportProgress(true, 0, 'PDFを生成中...');
            const path = await this.api.ExportPDF(exportHtml);
            exportStore.setPdfExportProgress(false);
            eventLogger.log('Topbar', 'export-pdf-success', { path });
            useUIStore.getState().setStatusMessage(`PDFをエクスポートしました: ${path}`, 3000);
        } catch (error) {
            console.error('PDF export failed:', error);
            eventLogger.log('Topbar', 'export-pdf-error', { error: String(error) });
            exportStore.setPdfExportProgress(false);
            useUIStore.getState().setStatusMessage('PDFエクスポートに失敗しました', 3000);
        }
    }

    private async getRenderedPreviewHtml(): Promise<string | null> {
        const iframe = document.getElementById('preview') as HTMLIFrameElement | null;
        const doc = iframe?.contentDocument;
        const root = doc?.documentElement;
        if (!root) {
            return null;
        }
        const printoutMode = root.getAttribute('data-printout')?.toLowerCase();
        if (printoutMode && printoutMode !== 'infinite') {
            await this.waitForPrintoutReady(iframe);
            const readyMeta = doc?.querySelector('meta[name="karte-printout-ready"]')?.getAttribute('content') || '';
            const pagesMeta = doc?.querySelector('meta[name="karte-printout-pages"]')?.getAttribute('content') || '';
            const pages = Number.parseInt(pagesMeta, 10);
            if (readyMeta.toLowerCase() !== 'true' || !Number.isFinite(pages) || pages <= 0) {
                eventLogger.log('Topbar', 'export-html-not-ready', {
                    printoutMode,
                    readyMeta,
                    pagesMeta,
                    debugMeta: doc?.querySelector('meta[name="karte-printout-debug"]')?.getAttribute('content') || '',
                });
                return null;
            }
        }
        eventLogger.log('Topbar', 'export-html-source-state', {
            printoutMode: printoutMode || '',
            readyMeta: doc?.querySelector('meta[name="karte-printout-ready"]')?.getAttribute('content') || '',
            pagesMeta: doc?.querySelector('meta[name="karte-printout-pages"]')?.getAttribute('content') || '',
            debugMeta: doc?.querySelector('meta[name="karte-printout-debug"]')?.getAttribute('content') || '',
        });
        const html = this.serializeExportHtml(root);
        if (!html.trim()) {
            return null;
        }
        if (html.toLowerCase().startsWith('<!doctype html')) {
            return html;
        }
        return `<!doctype html>\n${html}`;
    }

    private serializeExportHtml(root: HTMLElement): string {
        const clone = root.cloneNode(true) as HTMLElement;
        this.preparePrintoutDomForExport(clone);
        return clone.outerHTML || '';
    }

    private preparePrintoutDomForExport(root: HTMLElement): void {
        const printoutMode = root.getAttribute('data-printout')?.toLowerCase();
        if (!printoutMode || printoutMode === 'infinite') {
            return;
        }
        root.setAttribute('data-export-target', 'pdf');

        const flowRoot = root.querySelector<HTMLElement>('.karte-print-flow-root');
        const pageContents = Array.from(root.querySelectorAll<HTMLElement>('section.karte-print-page > .karte-print-page-content'));
        if (flowRoot && pageContents.length > 0) {
            // Preserve preview pagination boundaries for PDF by converting each page edge
            // into explicit break markers in a single flow document.
            flowRoot.innerHTML = '';
            pageContents.forEach((content, index) => {
                const nodes = Array.from(content.childNodes);
                for (const node of nodes) {
                    flowRoot.appendChild(node.cloneNode(true));
                }
                const lastElement = flowRoot.lastElementChild as HTMLElement | null;
                const endsWithBreakMarker = Boolean(lastElement?.classList.contains('karte-force-page-break'));
                if (index < pageContents.length - 1 && !endsWithBreakMarker) {
                    const marker = root.ownerDocument.createElement('div');
                    marker.className = 'karte-force-page-break karte-auto-page-break';
                    marker.setAttribute('aria-hidden', 'true');
                    flowRoot.appendChild(marker);
                }
            });
            delete flowRoot.dataset.kartePrintOriginalHtml;
        } else {
            const originalHtml = flowRoot?.dataset?.kartePrintOriginalHtml;
            if (flowRoot && typeof originalHtml === 'string' && originalHtml.trim() !== '') {
                flowRoot.innerHTML = originalHtml;
                delete flowRoot.dataset.kartePrintOriginalHtml;
            }
        }

        // Remove preview pagination artifacts and runtime script before PDF.
        root.querySelectorAll('section.karte-print-page').forEach((el) => el.remove());
        root.querySelectorAll('.karte-print-pages').forEach((el) => el.remove());
        root.querySelectorAll('script#karte-printout-pagination').forEach((el) => el.remove());
    }

    private async waitForPrintoutReady(iframe: HTMLIFrameElement, timeoutMs = 6000): Promise<void> {
        const started = Date.now();
        while (Date.now() - started < timeoutMs) {
            const win = iframe.contentWindow as (Window & { __kartePrintoutReady?: boolean | string }) | null;
            const doc = iframe.contentDocument;
            const readyMeta = doc?.querySelector('meta[name="karte-printout-ready"]')?.getAttribute('content') || '';
            const pagesMeta = doc?.querySelector('meta[name="karte-printout-pages"]')?.getAttribute('content') || '';
            const pages = Number.parseInt(pagesMeta, 10);
            if (!win) {
                return;
            }
            if ((win.__kartePrintoutReady === true || readyMeta.toLowerCase() === 'true') && Number.isFinite(pages) && pages > 0) {
                return;
            }
            await new Promise((resolve) => setTimeout(resolve, 40));
        }
        const win = iframe.contentWindow as (Window & { __kartePrintoutReady?: boolean | string; __kartePrintoutError?: string }) | null;
        const state = win?.__kartePrintoutReady;
        const error = win?.__kartePrintoutError;
        console.warn('[Topbar] waitForPrintoutReady timeout', { timeoutMs, state, error });
        eventLogger.log('Topbar', 'printout-ready-timeout', {
            timeoutMs,
            state: state === undefined ? 'undefined' : String(state),
            error: error || '',
        });
    }

    private async handleCustomCSS(): Promise<void> {
        const modalStore = useModalStore.getState();
        try {
            eventLogger.log('Topbar', 'custom-css-open');
            const css = await this.api.GetCustomCSS();
            useCustomCssStore.getState().setCustomCss(css);
            modalStore.setCustomCssModalValue(css);
            modalStore.showCustomCssModal();
            eventLogger.log('Topbar', 'custom-css-opened');
        } catch (error) {
            console.error('Get custom CSS failed:', error);
            eventLogger.log('Topbar', 'custom-css-error', { error: String(error) });
            useUIStore.getState().setStatusMessage('カスタムCSSの取得に失敗しました', 3000);
        }
    }

    destroy(): void {
        this.unsubscribe.forEach((unsub) => unsub());
        this.unsubscribe = [];
    }
}
