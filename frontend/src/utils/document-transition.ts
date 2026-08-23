import { useBoardStore, useDocStore, useUIStore } from '../stores/index';
import type { ActiveTab } from '../types/ui-state';
import type { BoardDocument } from '../types/wails-api';

export type DocumentTransition = Readonly<{
    generation: number;
    requestedPath: string;
}>;

export type DocumentPreviewGuard = Readonly<{
    generation: number;
    path: string;
}>;

let documentGeneration = 0;

export function beginDocumentTransition(requestedPath: string): DocumentTransition {
    const transition = {
        generation: ++documentGeneration,
        requestedPath,
    };
    clearDocumentPreview();
    return transition;
}

export function isDocumentTransitionActive(transition: DocumentTransition): boolean {
    return transition.generation === documentGeneration;
}

export function cancelDocumentTransition(transition: DocumentTransition | null): void {
    if (transition && isDocumentTransitionActive(transition)) {
        documentGeneration += 1;
    }
}

export function invalidateDocumentTransitions(): void {
    documentGeneration += 1;
}

export function commitBoardDocumentTransition(
    transition: DocumentTransition,
    board: BoardDocument
): boolean {
    if (!isDocumentTransitionActive(transition)) {
        return false;
    }
    useBoardStore.getState().setBoard(board);
    useDocStore.setState({
        currentPath: board.path,
        markdownContent: board.rawContent,
        previewHtml: '',
        hasUnsavedChanges: false,
    });
    useUIStore.getState().setActiveTab('board');
    return true;
}

export function commitEditorDocumentTransition(
    transition: DocumentTransition,
    content: string
): boolean {
    if (!isDocumentTransitionActive(transition)) {
        return false;
    }
    const isPdf = isPdfDocumentPath(transition.requestedPath);
    useDocStore.setState({
        currentPath: transition.requestedPath,
        markdownContent: isPdf ? '' : content,
        previewHtml: '',
        hasUnsavedChanges: false,
    });
    useUIStore.getState().setActiveTab('editor');
    return true;
}

export function commitDocumentPreview(
    transition: DocumentTransition,
    html: string
): boolean {
    return commitGuardedDocumentPreview({
        generation: transition.generation,
        path: transition.requestedPath,
    }, html);
}

export function captureDocumentPreviewGuard(path: string): DocumentPreviewGuard {
    return { generation: documentGeneration, path };
}

export function commitGuardedDocumentPreview(
    guard: DocumentPreviewGuard,
    html: string
): boolean {
    const currentPath = useDocStore.getState().currentPath;
    if (
        guard.generation !== documentGeneration ||
        guard.path !== currentPath ||
        isBoardDocumentPath(currentPath) ||
        isPdfDocumentPath(currentPath)
    ) {
        return false;
    }
    useDocStore.getState().setPreviewHtml(html);
    return true;
}

export function activateDocumentTab(tab: ActiveTab): void {
    invalidateDocumentTransitions();
    if (tab === 'editor' && isBoardDocumentPath(useDocStore.getState().currentPath)) {
        clearDocumentPreview();
    }
    useUIStore.getState().setActiveTab(tab);
}

export function isBoardDocumentPath(path: string): boolean {
    return path.toLowerCase().endsWith('.board.md');
}

export function isPdfDocumentPath(path: string): boolean {
    return path.toLowerCase().endsWith('.pdf');
}

function clearDocumentPreview(): void {
    if (useDocStore.getState().previewHtml) {
        useDocStore.getState().setPreviewHtml('');
    }
}
