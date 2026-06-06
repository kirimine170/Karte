import { createStore } from 'zustand/vanilla';
import type { ModalState } from '../types/ui-state';
import type { ConflictResolutionStrategy } from '../types/wails-api';

interface ModalStore extends ModalState {
    // Actions
    showFilenameModal: () => void;
    hideFilenameModal: () => void;
    setFilenameModalValue: (value: string) => void;
    showRenameFileModal: (currentPath: string) => void;
    hideRenameFileModal: () => void;
    setRenameFileModalValue: (value: string) => void;
    showUnsavedConfirmModal: (onSave: () => void, onDiscard: () => void) => void;
    hideUnsavedConfirmModal: () => void;
    showCustomCssModal: () => void;
    hideCustomCssModal: () => void;
    setCustomCssModalValue: (value: string) => void;
    showWebClipModal: () => void;
    hideWebClipModal: () => void;
    setWebClipModalUrl: (url: string) => void;
    setWebClipImporting: (importing: boolean) => void;
    setWebClipWarnings: (warnings: string[]) => void;
    showCsvEditModal: (filePath: string, data: string[][]) => void;
    hideCsvEditModal: () => void;
    setCsvEditModalData: (data: string[][]) => void;
    showConflictModal: (conflictInfo: { path: string; localContent: string; remoteContent: string }) => void;
    hideConflictModal: () => void;
    showImagePreviewModal: (imagePath: string, imageName: string, metadata: string, systemMetadata: string) => void;
    hideImagePreviewModal: () => void;
    setImagePreviewModalMetadata: (metadata: string) => void;
    setImagePreviewModalSystemMetadata: (systemMetadata: string) => void;
}

export const useModalStore = createStore<ModalStore>((set) => ({
    // Initial state
    filenameModal: {
        visible: false,
        value: '',
    },
    renameFileModal: {
        visible: false,
        value: '',
        currentPath: '',
    },
    unsavedConfirmModal: {
        visible: false,
        onSave: () => {},
        onDiscard: () => {},
    },
    customCssModal: {
        visible: false,
        value: '',
    },
    webClipModal: {
        visible: false,
        url: '',
        importing: false,
        warnings: [],
    },
    csvEditModal: {
        visible: false,
        filePath: '',
        data: [],
    },
    conflictModal: {
        visible: false,
        conflictInfo: null,
    },
    imagePreviewModal: {
        visible: false,
        imagePath: '',
        imageName: '',
        metadata: '',
        systemMetadata: '',
    },

    // Actions
    showFilenameModal: () => set({ filenameModal: { visible: true, value: '' } }),
    hideFilenameModal: () => set({ filenameModal: { visible: false, value: '' } }),
    setFilenameModalValue: (value) => set((state) => ({
        filenameModal: { ...state.filenameModal, value },
    })),
    showRenameFileModal: (currentPath) => set({
        renameFileModal: { visible: true, value: '', currentPath },
    }),
    hideRenameFileModal: () => set({
        renameFileModal: { visible: false, value: '', currentPath: '' },
    }),
    setRenameFileModalValue: (value) => set((state) => ({
        renameFileModal: { ...state.renameFileModal, value },
    })),
    showUnsavedConfirmModal: (onSave, onDiscard) => set({
        unsavedConfirmModal: { visible: true, onSave, onDiscard },
    }),
    hideUnsavedConfirmModal: () => set({
        unsavedConfirmModal: { visible: false, onSave: () => {}, onDiscard: () => {} },
    }),
    showCustomCssModal: () => set((state) => ({
        customCssModal: { visible: true, value: state.customCssModal.value },
    })),
    hideCustomCssModal: () => set({ customCssModal: { visible: false, value: '' } }),
    setCustomCssModalValue: (value) => set((state) => ({
        customCssModal: { ...state.customCssModal, value },
    })),
    showWebClipModal: () => set({
        webClipModal: { visible: true, url: '', importing: false, warnings: [] },
    }),
    hideWebClipModal: () => set({
        webClipModal: { visible: false, url: '', importing: false, warnings: [] },
    }),
    setWebClipModalUrl: (url) => set((state) => ({
        webClipModal: { ...state.webClipModal, url },
    })),
    setWebClipImporting: (importing) => set((state) => ({
        webClipModal: { ...state.webClipModal, importing },
    })),
    setWebClipWarnings: (warnings) => set((state) => ({
        webClipModal: { ...state.webClipModal, warnings },
    })),
    showCsvEditModal: (filePath, data) => set({
        csvEditModal: { visible: true, filePath, data },
    }),
    hideCsvEditModal: () => set({
        csvEditModal: { visible: false, filePath: '', data: [] },
    }),
    setCsvEditModalData: (data) => set((state) => ({
        csvEditModal: { ...state.csvEditModal, data },
    })),
    showConflictModal: (conflictInfo) => set({
        conflictModal: { visible: true, conflictInfo },
    }),
    hideConflictModal: () => set({
        conflictModal: { visible: false, conflictInfo: null },
    }),
    showImagePreviewModal: (imagePath, imageName, metadata, systemMetadata) => set({
        imagePreviewModal: { visible: true, imagePath, imageName, metadata, systemMetadata },
    }),
    hideImagePreviewModal: () => set({
        imagePreviewModal: { visible: false, imagePath: '', imageName: '', metadata: '', systemMetadata: '' },
    }),
    setImagePreviewModalMetadata: (metadata) => set((state) => ({
        imagePreviewModal: { ...state.imagePreviewModal, metadata },
    })),
    setImagePreviewModalSystemMetadata: (systemMetadata) => set((state) => ({
        imagePreviewModal: { ...state.imagePreviewModal, systemMetadata },
    })),
}));
