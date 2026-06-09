import { createStore } from 'zustand/vanilla';
import type { BoardDocumentState } from '../types/ui-state';

interface BoardStore {
    board: BoardDocumentState | null;
    selectedCardId: string | null;
    selectedEdgeId: string | null;
    candidateResources: Array<{ path: string; title?: string }>;
    setBoard: (board: BoardDocumentState | null) => void;
    setSelectedCardId: (cardId: string | null) => void;
    setSelectedEdgeId: (edgeId: string | null) => void;
    setCandidateResources: (items: Array<{ path: string; title?: string }>) => void;
    clear: () => void;
}

export const useBoardStore = createStore<BoardStore>((set) => ({
    board: null,
    selectedCardId: null,
    selectedEdgeId: null,
    candidateResources: [],
    setBoard: (board) => set({ board }),
    setSelectedCardId: (selectedCardId) => set({ selectedCardId, selectedEdgeId: null }),
    setSelectedEdgeId: (selectedEdgeId) => set({ selectedEdgeId, selectedCardId: null }),
    setCandidateResources: (candidateResources) => set({ candidateResources }),
    clear: () => set({
        board: null,
        selectedCardId: null,
        selectedEdgeId: null,
        candidateResources: [],
    }),
}));
