// エントリーポイント
import { App } from './app';

// アプリケーションの初期化
const app = new App();
app.init().catch((error) => {
    console.error('Failed to initialize application:', error);
});

// クリーンアップ（必要に応じて）
window.addEventListener('beforeunload', () => {
    app.destroy();
});

