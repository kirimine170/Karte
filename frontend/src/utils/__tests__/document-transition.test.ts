import { beforeEach, describe, expect, it } from 'vitest';
import { useBoardStore, useDocStore, useUIStore } from '../../stores/index';
import type { BoardDocument } from '../../types/wails-api';
import {
    activateDocumentTab,
    beginDocumentTransition,
    cancelDocumentTransition,
    commitBoardDocumentTransition,
    commitDocumentPreview,
    commitEditorDocumentTransition,
} from '../document-transition';

const board: BoardDocument = {
    path: 'content/example.board.md',
    title: 'Example board',
    docId: 'board:example',
    type: 'karte-board',
    version: 1,
    created: '2026-08-23',
    updated: '2026-08-23',
    tags: [],
    cards: [],
    edges: [],
    layout: { cards: {}, viewport: { x: 0, y: 0, zoom: 1 } },
    notes: '',
    rawContent: '---\ntype: karte-board\n---',
};

describe('document transitions', () => {
    beforeEach(() => {
        useDocStore.setState({
            currentPath: 'content/old.md',
            markdownContent: '# Old',
            previewHtml: '<p>old preview</p>',
            hasUnsavedChanges: false,
            files: [],
            searchQuery: '',
        });
        useBoardStore.getState().clear();
        useUIStore.setState({ activeTab: 'editor', workspaceMode: false });
    });

    it('commits board path，raw source，empty preview，and tab as one ordered transition', () => {
        const transition = beginDocumentTransition(board.path);
        const committedSnapshots: Array<{
            currentPath: string;
            markdownContent: string;
            previewHtml: string;
        }> = [];
        const unsubscribe = useDocStore.subscribe((state, previous) => {
            if (state.currentPath !== previous.currentPath) {
                committedSnapshots.push({
                    currentPath: state.currentPath,
                    markdownContent: state.markdownContent,
                    previewHtml: state.previewHtml,
                });
            }
        });

        expect(commitBoardDocumentTransition(transition, board)).toBe(true);

        expect(committedSnapshots).toEqual([{
            currentPath: board.path,
            markdownContent: board.rawContent,
            previewHtml: '',
        }]);
        expect(useBoardStore.getState().board).toBe(board);
        expect(useUIStore.getState().activeTab).toBe('board');
        unsubscribe();
    });

    it('keeps a loaded Markdown preview blank until its matching render commits', () => {
        const transition = beginDocumentTransition('content/next.md');

        expect(useDocStore.getState().previewHtml).toBe('');
        expect(commitEditorDocumentTransition(transition, '# Next')).toBe(true);
        expect(useDocStore.getState()).toMatchObject({
            currentPath: 'content/next.md',
            markdownContent: '# Next',
            previewHtml: '',
        });
        expect(useUIStore.getState().activeTab).toBe('editor');

        expect(commitDocumentPreview(transition, '<p>next preview</p>')).toBe(true);
        expect(useDocStore.getState().previewHtml).toBe('<p>next preview</p>');
    });

    it('rejects an older preview that resolves after a newer document', () => {
        const older = beginDocumentTransition('content/older.md');
        expect(commitEditorDocumentTransition(older, '# Older')).toBe(true);

        const newer = beginDocumentTransition('content/newer.md');
        expect(commitEditorDocumentTransition(newer, '# Newer')).toBe(true);
        expect(commitDocumentPreview(newer, '<p>newer preview</p>')).toBe(true);

        expect(commitDocumentPreview(older, '<p>older preview</p>')).toBe(false);
        expect(useDocStore.getState().previewHtml).toBe('<p>newer preview</p>');
    });

    it('rejects every late commit after its owner cancels on destroy', () => {
        const transition = beginDocumentTransition('content/late.md');

        cancelDocumentTransition(transition);

        expect(commitEditorDocumentTransition(transition, '# Late')).toBe(false);
        expect(commitDocumentPreview(transition, '<p>late preview</p>')).toBe(false);
        expect(useDocStore.getState()).toMatchObject({
            currentPath: 'content/old.md',
            markdownContent: '# Old',
            previewHtml: '',
        });
    });

    it('lets a later explicit tab choice invalidate a pending document load', () => {
        const transition = beginDocumentTransition('content/late.md');

        activateDocumentTab('graph');

        expect(commitEditorDocumentTransition(transition, '# Late')).toBe(false);
        expect(useUIStore.getState().activeTab).toBe('graph');
        expect(useDocStore.getState().currentPath).toBe('content/old.md');
    });
});
