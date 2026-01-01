// コンポーネント境界の型定義

export interface ComponentBoundaries {
    // Topbar: トップバーコンポーネント
    Topbar: {
        // DOM要素への参照
        element: HTMLElement | null;
        // 初期化関数
        init: () => void;
        // クリーンアップ関数
        destroy: () => void;
    };

    // Sidebar: サイドバーコンポーネント
    Sidebar: {
        element: HTMLElement | null;
        init: () => void;
        destroy: () => void;
    };

    // MainTabs: メインタブコンポーネント
    MainTabs: {
        element: HTMLElement | null;
        init: () => void;
        destroy: () => void;
    };

    // EditorLayout: エディターレイアウトコンポーネント
    EditorLayout: {
        element: HTMLElement | null;
        init: () => void;
        destroy: () => void;
    };

    // GraphView: グラフビューコンポーネント
    GraphView: {
        element: HTMLElement | null;
        init: () => void;
        destroy: () => void;
    };

    // ModalHost: モーダルホストコンポーネント
    ModalHost: {
        element: HTMLElement | null;
        init: () => void;
        destroy: () => void;
    };

    // OverlayHost: オーバーレイホストコンポーネント
    OverlayHost: {
        element: HTMLElement | null;
        init: () => void;
        destroy: () => void;
    };
}

// コンポーネント初期化オプション
export interface ComponentInitOptions {
    // 親要素（オプション）
    parent?: HTMLElement;
    // その他のオプション
    [key: string]: unknown;
}

