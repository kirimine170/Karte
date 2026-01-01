import { create } from 'zustand';
import type { UIState, Theme, ActiveTab } from '../types/ui-state';

interface UIStore extends UIState {
    // Actions
    setSidebarVisible: (visible: boolean) => void;
    setImageGalleryVisible: (visible: boolean) => void;
    setCsvGalleryVisible: (visible: boolean) => void;
    setActiveTab: (tab: ActiveTab) => void;
    setTheme: (theme: Theme) => void;
    setHardWrap: (hardWrap: boolean) => void;
    setStatusMessage: (message: string, duration?: number) => void;
    clearStatusMessage: () => void;
}

export const useUIStore = create<UIStore>((set, get) => ({
    // Initial state
    sidebarVisible: true,
    imageGalleryVisible: true,
    csvGalleryVisible: true,
    activeTab: 'editor',
    theme: 'light',
    hardWrap: false,
    statusMessage: '',
    statusClearTimer: null,

    // Actions
    setSidebarVisible: (visible) => set({ sidebarVisible: visible }),
    setImageGalleryVisible: (visible) => set({ imageGalleryVisible: visible }),
    setCsvGalleryVisible: (visible) => set({ csvGalleryVisible: visible }),
    setActiveTab: (tab) => set({ activeTab: tab }),
    setTheme: (theme) => {
        set({ theme });
        // Apply theme to document
        const root = document.documentElement;
        root.setAttribute('data-theme', theme);
    },
    setHardWrap: (hardWrap) => set({ hardWrap }),
    setStatusMessage: (message, duration = 3000) => {
        const { statusClearTimer } = get();
        if (statusClearTimer) {
            clearTimeout(statusClearTimer);
        }
        const timer = window.setTimeout(() => {
            set({ statusMessage: '', statusClearTimer: null });
        }, duration);
        set({ statusMessage: message, statusClearTimer: timer });
    },
    clearStatusMessage: () => {
        const { statusClearTimer } = get();
        if (statusClearTimer) {
            clearTimeout(statusClearTimer);
            set({ statusMessage: '', statusClearTimer: null });
        }
    },
}));

