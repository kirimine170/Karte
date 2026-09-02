// イベントログシステム
// 構造化ログ形式でイベントを記録し、テストで検証可能にする

export interface EventLog {
    component: string;
    action: string;
    state?: any;
    timestamp: number;
}

export interface EventLoggerConfig {
    api?: {
        SaveEventLogs: (logsJson: string) => Promise<boolean>;
    };
    autoSaveInterval?: number; // 自動保存間隔（ミリ秒）
    maxLogs?: number; // 最大ログ数（メモリ保護）
}

const SAFE_STRING_KEYS = new Set(['theme', 'tab', 'reason', 'stage', 'mode', 'strategy', 'kind', 'status']);
const SAFE_CODE_PATTERN = /^[A-Za-z0-9._-]{1,64}$/;
const SAFE_ERROR_CODES = new Set(['no-content', 'no-file-selected', 'pdf-readonly', 'board-autosave']);

function sanitizeEventState(state: any): Record<string, boolean | number | string> | undefined {
    if (!state || typeof state !== 'object' || Array.isArray(state)) {
        return undefined;
    }
    const sanitized: Record<string, boolean | number | string> = {};
    for (const [key, value] of Object.entries(state)) {
        if (typeof value === 'boolean' || (typeof value === 'number' && Number.isFinite(value))) {
            sanitized[key] = value;
            continue;
        }
        if (key === 'error' && typeof value === 'string') {
            sanitized.error = SAFE_ERROR_CODES.has(value) ? value : 'redacted';
            continue;
        }
        if (SAFE_STRING_KEYS.has(key) && typeof value === 'string' && SAFE_CODE_PATTERN.test(value)) {
            sanitized[key] = value;
        }
    }
    return Object.keys(sanitized).length > 0 ? sanitized : undefined;
}

class EventLogger {
    private logs: EventLog[] = [];
    private isTestMode: boolean;
    private config: EventLoggerConfig;
    private autoSaveInterval: number | null = null;
    private pendingSave: boolean = false;

    constructor(config: EventLoggerConfig = {}) {
        // テスト環境かどうかを判定（vitest環境変数またはwindowオブジェクトの存在で判定）
        this.isTestMode = typeof process !== 'undefined' && process.env.NODE_ENV === 'test' ||
            typeof import.meta !== 'undefined' && import.meta.env?.MODE === 'test' ||
            typeof window !== 'undefined' && (window as any).__VITEST__;

        this.config = {
            autoSaveInterval: config.autoSaveInterval || 60000, // デフォルト60秒
            maxLogs: config.maxLogs || 10000, // デフォルト10000件
            ...config
        };
    }

    /**
     * イベントログを記録
     */
    log(component: string, action: string, state?: any): void {
        const safeState = sanitizeEventState(state);
        const safeComponent = SAFE_CODE_PATTERN.test(component) ? component : 'unknown';
        const safeAction = SAFE_CODE_PATTERN.test(action) ? action : 'unknown';
        const logEntry: EventLog = {
            component: safeComponent,
            action: safeAction,
            ...(safeState ? { state: safeState } : {}),
            timestamp: Date.now(),
        };

        // 常に配列に保存（テスト環境以外でも）
        this.logs.push(logEntry);

        // メモリ保護：最大ログ数を超えたら古いログを削除
        if (this.config.maxLogs && this.logs.length > this.config.maxLogs) {
            this.logs = this.logs.slice(-this.config.maxLogs);
        }

        // 常にconsole.logで出力（開発・デバッグ用）
        console.log(`[${safeComponent}] ${safeAction}`, safeState || '');
    }

    /**
     * ログを取得（テスト用）
     */
    getLogs(): EventLog[] {
        return [...this.logs];
    }

    /**
     * ログをクリア（テスト用）
     */
    clearLogs(): void {
        this.logs = [];
    }

    /**
     * 特定のコンポーネントのログを取得
     */
    getLogsByComponent(component: string): EventLog[] {
        return this.logs.filter(log => log.component === component);
    }

    /**
     * 特定のアクションのログを取得
     */
    getLogsByAction(action: string): EventLog[] {
        return this.logs.filter(log => log.action === action);
    }

    /**
     * ログの数を取得
     */
    getLogCount(): number {
        return this.logs.length;
    }

    /**
     * ログをJSON形式で取得
     */
    getLogsAsJson(): string {
        return JSON.stringify(this.logs, null, 2);
    }

    /**
     * ログをバックエンドに保存
     */
    async saveToBackend(): Promise<boolean> {
        if (!this.config.api?.SaveEventLogs) {
            console.warn('EventLogger: SaveEventLogs API not available');
            return false;
        }

        if (this.logs.length === 0) {
            return true; // ログがなければ保存不要
        }

        if (this.pendingSave) {
            return false; // 既に保存中
        }

        this.pendingSave = true;
        try {
            const logsJson = this.getLogsAsJson();
            const result = await this.config.api.SaveEventLogs(logsJson);
            if (result) {
                // 保存成功後、ログをクリア（または保持する）
                // this.logs = []; // 必要に応じてコメントアウト
            }
            return result;
        } catch (_error) {
            console.error('EventLogger: Failed to save logs to backend');
            return false;
        } finally {
            this.pendingSave = false;
        }
    }

    /**
     * 自動保存を開始
     */
    startAutoSave(intervalMs?: number): void {
        this.stopAutoSave();
        const interval = intervalMs || this.config.autoSaveInterval || 60000;
        this.autoSaveInterval = window.setInterval(() => {
            if (this.logs.length > 0) {
                this.saveToBackend().catch(_error => {
                    console.error('EventLogger: Auto-save failed');
                });
            }
        }, interval);
    }

    /**
     * 自動保存を停止
     */
    stopAutoSave(): void {
        if (this.autoSaveInterval !== null) {
            clearInterval(this.autoSaveInterval);
            this.autoSaveInterval = null;
        }
    }

    /**
     * APIを設定（アプリ初期化時に呼び出す）
     */
    setApi(api: EventLoggerConfig['api']): void {
        this.config.api = api;
    }
}

// シングルトンインスタンス（後でAPIを設定する）
export const eventLogger = new EventLogger();
