import { create } from 'zustand';
import type { ExportState } from '../types/ui-state';

interface ExportStore extends ExportState {
    // Actions
    setPdfExportProgress: (visible: boolean, progress?: number, message?: string) => void;
    setTranscriptionProgress: (visible: boolean, progress?: number, message?: string) => void;
    hideAllProgress: () => void;
}

export const useExportStore = create<ExportStore>((set) => ({
    // Initial state
    pdfExportProgress: {
        visible: false,
        progress: 0,
        message: '',
    },
    transcriptionProgress: {
        visible: false,
        progress: 0,
        message: '',
    },

    // Actions
    setPdfExportProgress: (visible, progress = 0, message = '') => {
        set({
            pdfExportProgress: {
                visible,
                progress,
                message,
            },
        });
    },
    setTranscriptionProgress: (visible, progress = 0, message = '') => {
        set({
            transcriptionProgress: {
                visible,
                progress,
                message,
            },
        });
    },
    hideAllProgress: () => {
        set({
            pdfExportProgress: {
                visible: false,
                progress: 0,
                message: '',
            },
            transcriptionProgress: {
                visible: false,
                progress: 0,
                message: '',
            },
        });
    },
}));

