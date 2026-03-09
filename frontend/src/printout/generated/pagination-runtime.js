"use strict";
function createPrintoutPagination(doc = document) {
    const runtimeWindow = window;
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
        setMeta('karte-printout-ready', String(state));
        setMeta('karte-printout-error', err ? String(err) : '');
        setMeta('karte-printout-pages', String(pages));
    }
    function shouldPaginate() {
        const root = doc.documentElement;
        if (!root)
            return false;
        const mode = root.getAttribute('data-printout');
        return Boolean(mode && mode.toLowerCase() !== 'infinite');
    }
    function resetArticle(article) {
        if (!article.dataset.kartePrintOriginalHtml)
            return;
        article.innerHTML = article.dataset.kartePrintOriginalHtml;
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
            if (!hasTextSiblings && only && only.children.length > 0) {
                return Array.from(only.children).filter((child) => child instanceof HTMLElement);
            }
        }
        return direct;
    }
    function buildPages() {
        let pageCount = 0;
        reportReady(false, '', pageCount);
        try {
            if (!shouldPaginate()) {
                reportReady(true, '', pageCount);
                return;
            }
            const article = doc.querySelector('article');
            if (!article) {
                reportReady(true, '', pageCount);
                return;
            }
            if (!article.dataset.kartePrintOriginalHtml) {
                article.dataset.kartePrintOriginalHtml = article.innerHTML;
            }
            else {
                resetArticle(article);
            }
            const blocks = flowBlocksFromArticle(article);
            if (blocks.length === 0) {
                reportReady(true, '', pageCount);
                return;
            }
            const pages = doc.createElement('div');
            pages.className = 'karte-print-pages';
            article.innerHTML = '';
            article.appendChild(pages);
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
                    const children = Array.from(block.children).filter((child) => child instanceof HTMLElement);
                    if (children.length === 0) {
                        fitBlockIfNeeded(block, maxHeight);
                        return;
                    }
                    current.removeChild(block);
                    children.forEach((child) => {
                        current.appendChild(child);
                        if (current.scrollHeight <= maxHeight + 1)
                            return;
                        current.removeChild(child);
                        current = createPage();
                        maxHeight = current.clientHeight;
                        current.appendChild(child);
                    });
                    return;
                }
                current = createPage();
                maxHeight = current.clientHeight;
                current.appendChild(block);
                if (current.scrollHeight > maxHeight + 1 && isAtomic(block)) {
                    fitBlockIfNeeded(block, maxHeight);
                }
            });
            reportReady(true, '', pageCount);
        }
        catch (err) {
            const message = err instanceof Error ? err.message : String(err);
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
