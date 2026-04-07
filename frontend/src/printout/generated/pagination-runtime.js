"use strict";
function shouldPreserveSingleWrapper(block) {
    const tag = block.tagName.toUpperCase();
    return tag === 'UL' || tag === 'OL';
}
function hasDirectTextNodes(block) {
    return Array.from(block.childNodes).some((node) => node.nodeType === Node.TEXT_NODE && Boolean(node.textContent && node.textContent.trim() !== ''));
}
function createPrintoutPagination(doc = document) {
    const runtimeWindow = window;
    const PRINT_META_NAME = 'karte-printout';
    function resolvePrintoutMode() {
        const root = doc.documentElement;
        const attr = root?.getAttribute('data-printout');
        if (attr && attr.trim() !== '') {
            return attr.trim();
        }
        const fromMeta = doc.querySelector(`meta[name="${PRINT_META_NAME}"]`)?.content?.trim() || '';
        if (fromMeta && root) {
            root.setAttribute('data-printout', fromMeta);
        }
        return fromMeta;
    }
    function setMeta(name, content) {
        const head = doc.head || doc.documentElement;
        if (!head)
            return;
        const selector = `meta[name="${name}"]`;
        let el = head.querySelector(selector);
        if (!el) {
            el = doc.createElement('meta');
            el.setAttribute('name', name);
            head.appendChild(el);
        }
        el.setAttribute('content', content || '');
    }
    function reportReady(state, err, pages) {
        runtimeWindow.__kartePrintoutReady = state;
        runtimeWindow.__kartePrintoutError = err ? String(err) : '';
        if (runtimeWindow.__kartePrintoutDebug === undefined) {
            runtimeWindow.__kartePrintoutDebug = '';
        }
        setMeta('karte-printout-ready', String(state));
        setMeta('karte-printout-error', err ? String(err) : '');
        setMeta('karte-printout-pages', String(pages));
        setMeta('karte-printout-debug', runtimeWindow.__kartePrintoutDebug || '');
    }
    function setDebug(message) {
        runtimeWindow.__kartePrintoutDebug = message;
        setMeta('karte-printout-debug', message);
    }
    function shouldPaginate() {
        const mode = resolvePrintoutMode();
        return Boolean(mode && mode.toLowerCase() !== 'infinite');
    }
    function findFlowRoot() {
        const article = doc.querySelector('article');
        if (article) {
            article.classList.add('karte-print-flow-root');
            return article;
        }
        const main = doc.querySelector('main.container, main, .container');
        if (main) {
            main.classList.add('karte-print-flow-root');
            return main;
        }
        if (doc.body) {
            doc.body.classList.add('karte-print-flow-root');
            return doc.body;
        }
        return null;
    }
    function resetFlowRoot(root) {
        if (!root.dataset.kartePrintOriginalHtml)
            return;
        root.innerHTML = root.dataset.kartePrintOriginalHtml;
    }
    function setGuideOnlyMode(root, enabled) {
        if (!root)
            return;
        root.classList.toggle('karte-print-guide-only', enabled);
    }
    function isAtomic(el) {
        if (!el || !(el instanceof HTMLElement))
            return false;
        const tag = el.tagName.toUpperCase();
        return tag === 'IMG' || tag === 'PRE' || tag === 'TABLE' || tag === 'SVG' || tag === 'CANVAS' || tag === 'IFRAME' || tag === 'VIDEO';
    }
    function fitBlockIfNeeded(block, maxHeight) {
        if (!block)
            return;
        block.style.zoom = '';
        const height = block.getBoundingClientRect().height;
        if (height <= maxHeight + 1 || height <= 0)
            return;
        const scale = Math.max(0.15, maxHeight / height);
        block.style.zoom = String(scale);
    }
    function createSplitWrapper(current, block) {
        const wrapper = block.cloneNode(false);
        wrapper.style.zoom = '';
        current.appendChild(wrapper);
        return wrapper;
    }
    function distributeBlockAcrossPages(block, current, maxHeight, createPage) {
        const nodes = Array.from(block.childNodes);
        if (nodes.length === 0) {
            current.appendChild(block);
            fitBlockIfNeeded(block, maxHeight);
            return { current, maxHeight };
        }
        let activeWrapper = createSplitWrapper(current, block);
        for (const node of nodes) {
            activeWrapper.appendChild(node);
            if (current.scrollHeight <= maxHeight + 1) {
                continue;
            }
            activeWrapper.removeChild(node);
            if (!activeWrapper.hasChildNodes()) {
                activeWrapper.appendChild(node);
                fitBlockIfNeeded(activeWrapper, maxHeight);
                return { current, maxHeight };
            }
            current = createPage();
            maxHeight = current.clientHeight;
            activeWrapper = createSplitWrapper(current, block);
            activeWrapper.appendChild(node);
        }
        return { current, maxHeight };
    }
    function flowBlocksFromArticle(article) {
        const direct = Array.from(article.children).filter((child) => child instanceof HTMLElement);
        if (direct.length === 1) {
            const only = direct[0];
            let hasTextSiblings = false;
            for (let i = 0; i < article.childNodes.length; i += 1) {
                const node = article.childNodes[i];
                if (node?.nodeType === Node.TEXT_NODE && node.textContent && node.textContent.trim() !== '') {
                    hasTextSiblings = true;
                    break;
                }
            }
            if (!hasTextSiblings && only && only.children.length > 0 && !shouldPreserveSingleWrapper(only) && !hasDirectTextNodes(only)) {
                return Array.from(only.children).filter((child) => child instanceof HTMLElement);
            }
        }
        return direct;
    }
    function buildPages() {
        let pageCount = 0;
        setDebug('start');
        reportReady(false, '', pageCount);
        try {
            const flowRoot = findFlowRoot();
            if (!shouldPaginate()) {
                setGuideOnlyMode(flowRoot, false);
                setDebug('skip:printout=infinite-or-empty');
                reportReady(true, '', pageCount);
                return;
            }
            if (!flowRoot) {
                setDebug('skip:no-flow-root');
                reportReady(true, '', pageCount);
                return;
            }
            setGuideOnlyMode(flowRoot, false);
            const currentInnerHtml = flowRoot.innerHTML;
            const currentTrimmed = currentInnerHtml.trim();
            const storedOriginal = flowRoot.dataset.kartePrintOriginalHtml?.trim() || '';
            const looksPagedNow = currentTrimmed.includes('karte-print-pages');
            if (!storedOriginal && currentTrimmed !== '' && !looksPagedNow) {
                flowRoot.dataset.kartePrintOriginalHtml = currentInnerHtml;
            }
            else if (storedOriginal) {
                resetFlowRoot(flowRoot);
            }
            const blocks = flowBlocksFromArticle(flowRoot);
            if (blocks.length === 0) {
                setGuideOnlyMode(flowRoot, true);
                const elementChildren = flowRoot.children.length;
                const textNodeCount = Array.from(flowRoot.childNodes).filter((node) => node.nodeType === Node.TEXT_NODE && (node.textContent || '').trim() !== '').length;
                setDebug(`skip:no-flow-blocks root=${flowRoot.tagName.toLowerCase()} elements=${elementChildren} textNodes=${textNodeCount}`);
                reportReady(true, '', pageCount);
                return;
            }
            const pages = doc.createElement('div');
            pages.className = 'karte-print-pages';
            flowRoot.innerHTML = '';
            flowRoot.appendChild(pages);
            function createPage() {
                const page = doc.createElement('section');
                page.className = 'karte-print-page';
                const content = doc.createElement('div');
                content.className = 'karte-print-page-content';
                page.appendChild(content);
                pages.appendChild(page);
                pageCount = pages.querySelectorAll('section.karte-print-page').length;
                return content;
            }
            let current = createPage();
            let maxHeight = current.clientHeight;
            blocks.forEach((block) => {
                block.style.zoom = '';
                current.appendChild(block);
                if (current.scrollHeight <= maxHeight + 1)
                    return;
                current.removeChild(block);
                if (current.children.length === 0) {
                    current.appendChild(block);
                    if (isAtomic(block)) {
                        fitBlockIfNeeded(block, maxHeight);
                        return;
                    }
                    current.removeChild(block);
                    ({ current, maxHeight } = distributeBlockAcrossPages(block, current, maxHeight, createPage));
                    return;
                }
                current = createPage();
                maxHeight = current.clientHeight;
                current.appendChild(block);
                if (current.scrollHeight > maxHeight + 1 && isAtomic(block)) {
                    fitBlockIfNeeded(block, maxHeight);
                }
            });
            setGuideOnlyMode(flowRoot, false);
            setDebug(`ok:pages=${pageCount} blocks=${blocks.length} root=${flowRoot.tagName.toLowerCase()}`);
            reportReady(true, '', pageCount);
        }
        catch (err) {
            const flowRoot = findFlowRoot();
            setGuideOnlyMode(flowRoot, true);
            const message = err instanceof Error ? err.message : String(err);
            setDebug(`error:${message}`);
            reportReady('error', message, pageCount);
        }
        finally {
            if (runtimeWindow.__kartePrintoutReady !== true && runtimeWindow.__kartePrintoutReady !== 'error') {
                reportReady(true, '', pageCount);
            }
        }
    }
    let timer;
    function schedule() {
        window.clearTimeout(timer);
        reportReady(false, '', 0);
        timer = window.setTimeout(buildPages, 60);
    }
    function attach() {
        if (doc.readyState === 'loading') {
            doc.addEventListener('DOMContentLoaded', schedule);
        }
        else {
            schedule();
        }
        window.addEventListener('load', schedule);
        window.addEventListener('resize', schedule);
        if (doc.fonts?.ready) {
            doc.fonts.ready.then(schedule).catch(() => { });
        }
    }
    return {
        attach,
        buildPages,
        flowBlocksFromArticle,
    };
}
const runtimeWindow = window;
runtimeWindow.__karteCreatePrintoutPagination = (doc) => createPrintoutPagination(doc ?? document);
runtimeWindow.__karteRunPrintoutPagination = (doc) => {
    const pagination = createPrintoutPagination(doc ?? document);
    pagination.attach();
    return pagination;
};
