import { createStore } from 'zustand/vanilla';
import { subscribeWithSelector } from 'zustand/middleware';
import type { DocumentState } from '../types/ui-state';
import type { FileItem } from '../types/wails-api';

interface DocStore extends DocumentState {
    // Actions
    setCurrentPath: (path: string) => void;
    setFiles: (files: FileItem[]) => void;
    setSearchQuery: (query: string) => void;
    setHasUnsavedChanges: (hasChanges: boolean) => void;
    setMarkdownContent: (content: string) => void;
    setMarkdownContentAndMarkUnsaved: (content: string) => void;
    setPreviewHtml: (html: string) => void;
    clearUnsavedChanges: () => void;
}

export const useDocStore = createStore<DocStore>()(subscribeWithSelector((set) => ({
    // Initial state
    currentPath: '',
    files: [],
    searchQuery: '',
    hasUnsavedChanges: false,
    markdownContent: '',
    previewHtml: '',

    // Actions
    setCurrentPath: (path) => set((state) => ({
        currentPath: path,
        previewHtml: path === state.currentPath ? state.previewHtml : '',
    })),
    setFiles: (files) => set({ files }),
    setSearchQuery: (query) => set({ searchQuery: query }),
    setHasUnsavedChanges: (hasChanges) => set({ hasUnsavedChanges: hasChanges }),
    setMarkdownContent: (content) => set({ markdownContent: content }),
    setMarkdownContentAndMarkUnsaved: (content) => set({ markdownContent: content, hasUnsavedChanges: true }),
    setPreviewHtml: (html) => set((state) => ({
        previewHtml: isPreviewablePath(state.currentPath) ? html : '',
    })),
    clearUnsavedChanges: () => set({ hasUnsavedChanges: false }),
})));

function isPreviewablePath(path: string): boolean {
    const normalizedPath = path.toLowerCase();
    return !normalizedPath.endsWith('.pdf') && !normalizedPath.endsWith('.board.md');
}
