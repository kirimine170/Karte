import { beforeEach, afterEach, vi } from 'vitest';

// DOM環境のセットアップ
beforeEach(() => {
    // 各テスト前にDOMをクリーンアップ
    document.body.innerHTML = '';
    document.head.innerHTML = '';
    
    // グローバルオブジェクトのリセット
    if (typeof window.localStorage !== 'undefined' && window.localStorage.clear) {
        window.localStorage.clear();
    }
    if (typeof window.sessionStorage !== 'undefined' && window.sessionStorage.clear) {
        window.sessionStorage.clear();
    }
});

// タイマーのモック（必要に応じて使用）
// vi.useFakeTimers();

afterEach(() => {
    // vi.clearAllTimers();
    // vi.useRealTimers();
    // vi.useFakeTimers();
});

