import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useDocStore } from '../doc-store';

describe('DocStore', () => {
    beforeEach(() => {
        useDocStore.setState({
            files: [],
            currentPath: 'content/test.md',
            markdownContent: '# Test',
            previewHtml: '',
            hasUnsavedChanges: false,
            searchQuery: '',
        });
    });

    it('updates Markdown content and unsaved state in one notification', () => {
        const listener = vi.fn();
        const unsubscribe = useDocStore.subscribe(listener);

        useDocStore.getState().setMarkdownContentAndMarkUnsaved('# Updated');

        expect(listener).toHaveBeenCalledTimes(1);
        expect(useDocStore.getState()).toMatchObject({
            markdownContent: '# Updated',
            hasUnsavedChanges: true,
        });
        unsubscribe();
    });

    it('clears preview on path changes and rejects preview HTML for board documents', () => {
        useDocStore.getState().setPreviewHtml('<p>old preview</p>');

        useDocStore.getState().setCurrentPath('content/example.board.md');
        useDocStore.getState().setPreviewHtml('<p>late stale preview</p>');

        expect(useDocStore.getState()).toMatchObject({
            currentPath: 'content/example.board.md',
            previewHtml: '',
        });
    });
});
