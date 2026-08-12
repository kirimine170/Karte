import { createStore } from 'zustand/vanilla';
import type { UIState, Theme, ActiveTab } from '../types/ui-state';

const THEME_STORAGE_KEY = 'karte-theme';
const HARD_WRAP_STORAGE_KEY = 'karte-hard-wrap';

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

function getInitialHardWrap(): boolean {
    if (typeof window === 'undefined') {
        return false;
    }
    try {
        return localStorage.getItem(HARD_WRAP_STORAGE_KEY) === 'true';
    } catch {
        return false;
    }
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
const initialHardWrap = getInitialHardWrap();
applyThemeToDocument(initialTheme);

export const useUIStore = createStore<UIStore>((set, get) => ({
    // Initial state
    sidebarVisible: true,
    imageGalleryVisible: true,
    csvGalleryVisible: true,
    workspaceMode: false,
    activeTab: 'editor',
    theme: initialTheme,
    hardWrap: initialHardWrap,
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
    setHardWrap: (hardWrap) => {
        set({ hardWrap });
        try {
            localStorage.setItem(HARD_WRAP_STORAGE_KEY, String(hardWrap));
        } catch {
            // Ignore storage errors (private mode, permissions).
        }
    },
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
