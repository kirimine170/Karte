import { create } from 'zustand';
import type { DocumentState } from '../types/ui-state';
import type { FileItem } from '../types/wails-api';

interface DocStore extends DocumentState {
    // Actions
    setCurrentPath: (path: string) => void;
    setFiles: (files: FileItem[]) => void;
    setSearchQuery: (query: string) => void;
    setHasUnsavedChanges: (hasChanges: boolean) => void;
    setMarkdownContent: (content: string) => void;
    setPreviewHtml: (html: string) => void;
    clearUnsavedChanges: () => void;
}

export const useDocStore = create<DocStore>((set) => ({
    // Initial state
    currentPath: '',
    files: [],
    searchQuery: '',
    hasUnsavedChanges: false,
    markdownContent: '',
    previewHtml: '',

    // Actions
    setCurrentPath: (path) => set({ currentPath: path }),
    setFiles: (files) => set({ files }),
    setSearchQuery: (query) => set({ searchQuery: query }),
    setHasUnsavedChanges: (hasChanges) => set({ hasUnsavedChanges: hasChanges }),
    setMarkdownContent: (content) => set({ markdownContent: content }),
    setPreviewHtml: (html) => set({ previewHtml: html }),
    clearUnsavedChanges: () => set({ hasUnsavedChanges: false }),
}));

