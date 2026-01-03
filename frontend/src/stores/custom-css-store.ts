import { create } from 'zustand';

interface CustomCssStore {
    customCss: string;
    setCustomCss: (css: string) => void;
}

export const useCustomCssStore = create<CustomCssStore>((set) => ({
    customCss: '',
    setCustomCss: (css) => set({ customCss: css }),
}));
