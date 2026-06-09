import { createStore } from 'zustand/vanilla';
import type { UIState, Theme, ActiveTab } from '../types/ui-state';

const THEME_STORAGE_KEY = 'karte-theme';

function getInitialTheme(): Theme {
    if (typeof window === 'undefined') {
        return 'light';
    }
    try {
        const stored = localStorage.getItem(THEME_STORAGE_KEY);
        if (stored === 'light' || stored === 'dark' || stored === 'hc') {
            return stored;
        }
    } catch {
        // Ignore storage errors (private mode, permissions).
    }
    return 'light';
}

function applyThemeToDocument(theme: Theme): void {
    if (typeof document === 'undefined') {
        return;
    }
    document.documentElement.setAttribute('data-theme', theme);
    document.body?.setAttribute('data-theme', theme);
    document.querySelector('.app-container')?.setAttribute('data-theme', theme);
}

interface UIStore extends UIState {
    // Actions
    setSidebarVisible: (visible: boolean) => void;
    setImageGalleryVisible: (visible: boolean) => void;
    setCsvGalleryVisible: (visible: boolean) => void;
    setWorkspaceMode: (enabled: boolean) => void;
    setActiveTab: (tab: ActiveTab) => void;
    setTheme: (theme: Theme) => void;
    setHardWrap: (hardWrap: boolean) => void;
    setStatusMessage: (message: string, duration?: number) => void;
    clearStatusMessage: () => void;
}

const initialTheme = getInitialTheme();
applyThemeToDocument(initialTheme);

export const useUIStore = createStore<UIStore>((set, get) => ({
    // Initial state
    sidebarVisible: true,
    imageGalleryVisible: true,
    csvGalleryVisible: true,
    workspaceMode: false,
    activeTab: 'editor',
    theme: initialTheme,
    hardWrap: false,
    statusMessage: '',
    statusClearTimer: null,

    // Actions
    setSidebarVisible: (visible) => set({ sidebarVisible: visible }),
    setImageGalleryVisible: (visible) => set({ imageGalleryVisible: visible }),
    setCsvGalleryVisible: (visible) => set({ csvGalleryVisible: visible }),
    setWorkspaceMode: (workspaceMode) => set({ workspaceMode }),
    setActiveTab: (tab) => set((state) => ({
        activeTab: tab,
        workspaceMode: tab === 'editor' ? false : state.workspaceMode,
    })),
    setTheme: (theme) => {
        set({ theme });
        applyThemeToDocument(theme);
        try {
            localStorage.setItem(THEME_STORAGE_KEY, theme);
        } catch {
            // Ignore storage errors.
        }
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
