import { createStore } from 'zustand/vanilla';

interface CustomCssStore {
    customCss: string;
    setCustomCss: (css: string) => void;
}

export const useCustomCssStore = createStore<CustomCssStore>((set) => ({
    customCss: '',
    setCustomCss: (css) => set({ customCss: css }),
}));
