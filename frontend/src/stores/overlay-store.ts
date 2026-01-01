import { create } from 'zustand';
import type { OverlayState } from '../types/ui-state';

interface OverlayStore extends OverlayState {
    // Actions
    showDropOverlay: () => void;
    hideDropOverlay: () => void;
}

export const useOverlayStore = create<OverlayStore>((set) => ({
    // Initial state
    dropOverlay: {
        visible: false,
    },

    // Actions
    showDropOverlay: () => set({ dropOverlay: { visible: true } }),
    hideDropOverlay: () => set({ dropOverlay: { visible: false } }),
}));

