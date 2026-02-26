// Wails API統合レイヤー
import type { WailsAppAPI, WailsRuntimeAPI } from '../types/wails-api';

// ブラウザ環境かどうかをチェック
const isBrowser = typeof window !== 'undefined' && !(window as Window & { go?: unknown }).go;

// Wails App APIの取得
export async function getWailsAppAPI(): Promise<WailsAppAPI> {
    if (isBrowser) {
        // ブラウザ環境ではモックAPIを使用
        const { createBrowserApi } = await import('../test-support/browser-api');
        return createBrowserApi() as unknown as WailsAppAPI;
    }

    // Wails環境では実際のAPIを使用
    const AppModule = await import('../../wailsjs/wailsjs/go/main/App');
    return AppModule as unknown as WailsAppAPI;
}

// Wails Runtime APIの取得
export async function getWailsRuntimeAPI(): Promise<WailsRuntimeAPI> {
    if (isBrowser) {
        // ブラウザ環境ではモックAPIを使用
        return {
            EventsOn: () => () => {},
            BrowserOpenURL: (url: string) => {
                console.log('Mock BrowserOpenURL called with', url);
            },
        };
    }

    // Wails環境では実際のAPIを使用
    const RuntimeModule = await import('../../wailsjs/wailsjs/runtime/runtime');
    return RuntimeModule as unknown as WailsRuntimeAPI;
}
