import { create } from 'zustand';
import type { ASRState } from '../types/ui-state';
import type { ASRStatus } from '../types/wails-api';

interface ASRStore extends ASRState {
    // Actions
    setStatus: (status: ASRStatus) => void;
    setIsRecording: (isRecording: boolean) => void;
    setRecordingTranscriptPath: (path: string | null) => void;
    setMicLevel: (level: number) => void;
    setRealtimeTranscript: (partial: string, final?: string[]) => void;
    appendFinalTranscript: (text: string) => void;
    clearRealtimeTranscript: () => void;
}

export const useASRStore = create<ASRStore>((set, get) => ({
    // Initial state
    status: {
        initialized: false,
        initializing: false,
    },
    isRecording: false,
    recordingTranscriptPath: null,
    micLevel: 0,
    realtimeTranscript: {
        partial: '',
        final: [],
    },

    // Actions
    setStatus: (status) => set({ status }),
    setIsRecording: (isRecording) => set({ isRecording }),
    setRecordingTranscriptPath: (path) => set({ recordingTranscriptPath: path }),
    setMicLevel: (level) => set({ micLevel: level }),
    setRealtimeTranscript: (partial, final) => {
        if (final) {
            set({
                realtimeTranscript: {
                    partial,
                    final: [...get().realtimeTranscript.final, ...final],
                },
            });
        } else {
            set({
                realtimeTranscript: {
                    ...get().realtimeTranscript,
                    partial,
                },
            });
        }
    },
    appendFinalTranscript: (text) => {
        const { realtimeTranscript } = get();
        set({
            realtimeTranscript: {
                ...realtimeTranscript,
                final: [...realtimeTranscript.final, text],
            },
        });
    },
    clearRealtimeTranscript: () => {
        set({
            realtimeTranscript: {
                partial: '',
                final: [],
            },
        });
    },
}));

