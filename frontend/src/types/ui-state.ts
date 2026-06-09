// UI状態の型定義

export type Theme = 'light' | 'dark' | 'hc';

export type ActiveTab = 'editor' | 'graph' | 'board';

export interface UIState {
    // レイアウト
    sidebarVisible: boolean;
    imageGalleryVisible: boolean;
    csvGalleryVisible: boolean;
    workspaceMode: boolean;
    
    // タブ
    activeTab: ActiveTab;
    
    // テーマ
    theme: Theme;
    
    // ハードラップ
    hardWrap: boolean;
    
    // ステータス表示
    statusMessage: string;
    statusClearTimer: number | null;
}

export interface DocumentState {
    // 現在のファイル
    currentPath: string;
    
    // ファイルリスト
    files: Array<{
        path: string;
        title?: string;
        [key: string]: unknown;
    }>;
    
    // 検索クエリ
    searchQuery: string;
    
    // 未保存状態
    hasUnsavedChanges: boolean;
    
    // マークダウンコンテンツ
    markdownContent: string;
    
    // プレビューHTML
    previewHtml: string;
}

export interface BoardCardLayout {
    x: number;
    y: number;
    width: number;
    height: number;
}

export interface BoardViewport {
    x: number;
    y: number;
    zoom: number;
}

export interface BoardCard {
    id: string;
    type: string;
    title: string;
    source?: string;
    tags?: string[];
    createdBy?: string;
    updatedBy?: string;
    reviewed?: boolean;
    reviewedBy?: string;
    model?: string;
    body: string;
    meta?: Record<string, unknown>;
}

export interface BoardEdge {
    id: string;
    from: string;
    to: string;
    relation: string;
    label?: string;
}

export interface BoardDocumentState {
    path: string;
    title: string;
    docId: string;
    type: string;
    version: number;
    created: string;
    updated: string;
    tags: string[];
    cards: BoardCard[];
    edges: BoardEdge[];
    layout: {
        cards: Record<string, BoardCardLayout>;
        viewport: BoardViewport;
    };
    notes: string;
    rawContent: string;
}

export interface ASRState {
    // ASRステータス
    status: {
        initialized: boolean;
        initializing: boolean;
    };
    
    // 録音中かどうか
    isRecording: boolean;
    
    // 録音トランスクリプトパス
    recordingTranscriptPath: string | null;
    
    // マイクレベル
    micLevel: number;
    
    // リアルタイムトランスクリプト
    realtimeTranscript: {
        partial: string;
        final: string[];
    };
}

export interface ExportState {
    // PDFエクスポート進捗
    pdfExportProgress: {
        visible: boolean;
        progress: number;
        message: string;
    };
    
    // トランスクリプション進捗
    transcriptionProgress: {
        visible: boolean;
        progress: number;
        message: string;
    };
}

export interface ModalState {
    // ファイル作成モーダル
    filenameModal: {
        visible: boolean;
        value: string;
    };
    
    // リネームモーダル
    renameFileModal: {
        visible: boolean;
        value: string;
        currentPath: string;
    };
    
    // 未保存確認モーダル
    unsavedConfirmModal: {
        visible: boolean;
        onSave: () => void;
        onDiscard: () => void;
    };
    
    // カスタムCSSモーダル
    customCssModal: {
        visible: boolean;
        value: string;
    };

    // Web Clipモーダル
    webClipModal: {
        visible: boolean;
        url: string;
        importing: boolean;
        warnings: string[];
    };
    
    // CSV編集モーダル
    csvEditModal: {
        visible: boolean;
        filePath: string;
        data: string[][];
    };
    
    // コンフリクト解決モーダル
    conflictModal: {
        visible: boolean;
        conflictInfo: {
            path: string;
            localContent: string;
            remoteContent: string;
        } | null;
    };
    
    // 画像プレビューモーダル
    imagePreviewModal: {
        visible: boolean;
        imagePath: string;
        imageName: string;
        metadata: string;
        systemMetadata: string;
    };
}

export interface OverlayState {
    // ドラッグ&ドロップオーバーレイ
    dropOverlay: {
        visible: boolean;
    };
}
