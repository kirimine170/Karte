export type EventLogLevel = 'debug' | 'info' | 'warn' | 'error';
export type EventLoggerMode = 'development' | 'production' | 'test';

export interface EventLog {
    component: string;
    action: string;
    state?: unknown;
    timestamp: number;
    level: EventLogLevel;
}

export interface EventLoggerConfig {
    api?: {
        SaveEventLogs: (logsJson: string) => Promise<boolean>;
    };
    autoSaveInterval?: number;
    maxLogs?: number;
    minLevel?: EventLogLevel;
    consoleLevel?: EventLogLevel | 'off';
    mode?: EventLoggerMode;
    maxBatchBytes?: number;
    maxBatchEntries?: number;
    maxStateBytes?: number;
}

interface QueuedEventLog {
    id: number;
    entry: EventLog;
    encoded: string;
    encodedBytes: number;
}

interface EventLogSnapshot {
    items: QueuedEventLog[];
    payload: string;
}

interface EventLogStateMarker {
    __eventLogState: 'truncated' | 'unserializable';
    originalBytes?: number;
}

const levelPriority: Record<EventLogLevel, number> = {
    debug: 10,
    info: 20,
    warn: 30,
    error: 40,
};

const utf8Encoder = new TextEncoder();
const frontendEventLogMaxFieldBytes = 192;
const frontendEventLogMaxStateBytes = 48 << 10;
const backendEventLogMaxPayloadBytes = 8 << 20;
const backendEventLogMaxEntries = 20000;
const truncatedFieldSuffix = '…[truncated]';

function detectMode(): EventLoggerMode {
    if (
        (typeof process !== 'undefined' && process.env.NODE_ENV === 'test') ||
        import.meta.env?.MODE === 'test' ||
        (typeof window !== 'undefined' && (window as Window & { __VITEST__?: boolean }).__VITEST__)
    ) {
        return 'test';
    }
    if (import.meta.env?.PROD || import.meta.env?.MODE === 'production') {
        return 'production';
    }
    return 'development';
}

function classifyLegacyAction(action: string): EventLogLevel {
    const normalized = action.toLowerCase();
    if (normalized.endsWith('-error') || normalized.includes('failure')) {
        return 'error';
    }
    if (
        normalized.includes('cancel') ||
        normalized.includes('timeout') ||
        normalized.includes('warning')
    ) {
        return 'warn';
    }
    if (
        normalized === 'init' ||
        normalized.includes('startup') ||
        normalized.includes('file-load') ||
        normalized.includes('save') ||
        normalized.endsWith('-success')
    ) {
        return 'info';
    }
    return 'debug';
}

export class EventLogger {
    private logs: QueuedEventLog[] = [];
    private config: EventLoggerConfig;
    private nextID = 1;
    private autoSaveTimer: number | null = null;
    private pendingSave: Promise<boolean> | null = null;
    private inFlightIDs: Set<number> | null = null;
    private destroyed = false;
    private destroyPromise: Promise<boolean> | null = null;

    constructor(config: EventLoggerConfig = {}) {
        const mode = config.mode ?? detectMode();
        this.config = {
            autoSaveInterval: 60000,
            maxLogs: 10000,
            maxBatchBytes: 512 << 10,
            maxBatchEntries: 1000,
            maxStateBytes: 48 << 10,
            minLevel: mode === 'production' ? 'info' : 'debug',
            consoleLevel: mode === 'production' ? 'warn' : mode === 'test' ? 'off' : 'debug',
            ...config,
            mode,
        };
    }

    log(
        component: string,
        action: string,
        state?: unknown,
        level?: EventLogLevel
    ): void {
        const resolvedLevel = level ?? classifyLegacyAction(action);
        if (this.destroyed || !this.isEnabled(resolvedLevel)) {
            return;
        }

        const normalizedState = this.normalizeState(state);
        const entry: EventLog = {
            component: this.normalizeField(component, 'component'),
            action: this.normalizeField(action, 'action'),
            state: normalizedState,
            timestamp: Date.now(),
            level: resolvedLevel,
        };
        const encoded = JSON.stringify(entry);
        this.logs.push({
            id: this.nextID++,
            entry,
            encoded,
            encodedBytes: utf8Encoder.encode(encoded).byteLength,
        });
        this.trimPendingLogs();
        this.writeConsole(
            resolvedLevel,
            `[${entry.component}] ${entry.action}`,
            normalizedState
        );
    }

    debug(component: string, action: string, state?: unknown): void {
        this.log(component, action, state, 'debug');
    }

    info(component: string, action: string, state?: unknown): void {
        this.log(component, action, state, 'info');
    }

    warn(component: string, action: string, state?: unknown): void {
        this.log(component, action, state, 'warn');
    }

    error(component: string, action: string, state?: unknown): void {
        this.log(component, action, state, 'error');
    }

    setLevel(level: EventLogLevel): void {
        this.config.minLevel = level;
    }

    setConsoleLevel(level: EventLogLevel | 'off'): void {
        this.config.consoleLevel = level;
    }

    getLogs(): EventLog[] {
        return this.logs.map(({ entry }) => entry);
    }

    clearLogs(): void {
        this.logs = [];
    }

    getLogsByComponent(component: string): EventLog[] {
        return this.logs
            .map(({ entry }) => entry)
            .filter((entry) => entry.component === component);
    }

    getLogsByAction(action: string): EventLog[] {
        return this.logs
            .map(({ entry }) => entry)
            .filter((entry) => entry.action === action);
    }

    getLogCount(): number {
        return this.logs.length;
    }

    getLogsAsJson(): string {
        return `[${this.logs.map(({ encoded }) => encoded).join(',')}]`;
    }

    saveToBackend(): Promise<boolean> {
        return this.startSave(false);
    }

    startAutoSave(intervalMs?: number): void {
        this.stopAutoSave();
        if (this.destroyed) {
            return;
        }

        const interval = intervalMs ?? this.config.autoSaveInterval ?? 60000;
        this.autoSaveTimer = window.setInterval(() => {
            if (!this.destroyed && this.logs.length > 0 && !this.pendingSave) {
                void this.saveToBackend();
            }
        }, interval);
    }

    stopAutoSave(): void {
        if (this.autoSaveTimer !== null) {
            window.clearInterval(this.autoSaveTimer);
            this.autoSaveTimer = null;
        }
    }

    destroy(): Promise<boolean> {
        if (this.destroyPromise) {
            return this.destroyPromise;
        }

        this.stopAutoSave();
        this.destroyed = true;
        this.destroyPromise = this.flushForDestroy();
        return this.destroyPromise;
    }

    setApi(api: EventLoggerConfig['api']): void {
        this.config.api = api;
    }

    private startSave(allowDestroyed: boolean): Promise<boolean> {
        if (this.pendingSave) {
            return this.pendingSave;
        }
        if (this.destroyed && !allowDestroyed) {
            return Promise.resolve(false);
        }
        if (this.logs.length === 0) {
            return Promise.resolve(true);
        }
        if (!this.config.api?.SaveEventLogs) {
            this.writeConsole('warn', 'EventLogger: SaveEventLogs API not available');
            return Promise.resolve(false);
        }

        const snapshot = this.createSnapshot();
        if (!snapshot) {
            return Promise.resolve(true);
        }
        this.inFlightIDs = new Set(snapshot.items.map(({ id }) => id));
        const operation = this.persistSnapshot(snapshot).finally(() => {
            if (this.pendingSave === operation) {
                this.pendingSave = null;
            }
            this.inFlightIDs = null;
        });
        this.pendingSave = operation;
        return operation;
    }

    private async persistSnapshot(snapshot: EventLogSnapshot): Promise<boolean> {
        try {
            const saved = await this.config.api!.SaveEventLogs(snapshot.payload);
            if (saved) {
                const acknowledged = new Set(snapshot.items.map(({ id }) => id));
                this.logs = this.logs.filter(({ id }) => !acknowledged.has(id));
            }
            return saved;
        } catch (error) {
            this.writeConsole('error', 'EventLogger: Failed to save logs to backend', error);
            return false;
        }
    }

    private async flushForDestroy(): Promise<boolean> {
        while (this.logs.length > 0) {
            const saved = await this.startSave(true);
            if (!saved) {
                return false;
            }
        }
        return true;
    }

    private createSnapshot(): EventLogSnapshot | null {
        const maxEntries = Math.min(
            backendEventLogMaxEntries,
            Math.max(1, this.config.maxBatchEntries ?? 1000)
        );
        const maxBytes = Math.min(
            backendEventLogMaxPayloadBytes,
            Math.max(2, this.config.maxBatchBytes ?? (512 << 10))
        );
        const items: QueuedEventLog[] = [];
        let payloadBytes = 2;

        for (const queued of this.logs) {
            if (items.length >= maxEntries) {
                break;
            }
            const separatorBytes = items.length === 0 ? 0 : 1;
            if (payloadBytes + separatorBytes + queued.encodedBytes > maxBytes) {
                break;
            }
            items.push(queued);
            payloadBytes += separatorBytes + queued.encodedBytes;
        }
        if (items.length === 0) {
            // State normalization keeps each entry below the default batch limit．
            // A caller-supplied smaller limit must still make forward progress．
            items.push(this.logs[0]!);
        }
        return {
            items,
            payload: `[${items.map(({ encoded }) => encoded).join(',')}]`,
        };
    }

    private normalizeState(state: unknown): unknown {
        if (state === undefined) {
            return undefined;
        }
        try {
            const encoded = JSON.stringify(state);
            if (encoded === undefined) {
                return this.stateMarker('unserializable');
            }
            const byteLength = utf8Encoder.encode(encoded).byteLength;
            const maxStateBytes = Math.min(
                frontendEventLogMaxStateBytes,
                Math.max(1, this.config.maxStateBytes ?? frontendEventLogMaxStateBytes)
            );
            if (byteLength > maxStateBytes) {
                return this.stateMarker('truncated', byteLength);
            }
            return JSON.parse(encoded) as unknown;
        } catch {
            return this.stateMarker('unserializable');
        }
    }

    private stateMarker(
        reason: EventLogStateMarker['__eventLogState'],
        originalBytes?: number
    ): EventLogStateMarker {
        return originalBytes === undefined
            ? { __eventLogState: reason }
            : { __eventLogState: reason, originalBytes };
    }

    private normalizeField(value: string, name: 'component' | 'action'): string {
        const withoutControls = value.replace(/[\u0000-\u001f\u007f-\u009f]/g, '�');
        const normalized = withoutControls.trim() === '' ? `[empty-${name}]` : withoutControls;
        if (utf8Encoder.encode(normalized).byteLength <= frontendEventLogMaxFieldBytes) {
            return normalized;
        }

        const suffixBytes = utf8Encoder.encode(truncatedFieldSuffix).byteLength;
        const prefixLimit = frontendEventLogMaxFieldBytes - suffixBytes;
        let prefix = '';
        let prefixBytes = 0;
        for (const character of normalized) {
            const characterBytes = utf8Encoder.encode(character).byteLength;
            if (prefixBytes + characterBytes > prefixLimit) {
                break;
            }
            prefix += character;
            prefixBytes += characterBytes;
        }
        return `${prefix}${truncatedFieldSuffix}`;
    }

    private trimPendingLogs(): void {
        const maxLogs = this.config.maxLogs;
        if (!maxLogs || maxLogs < 1) {
            return;
        }

        if (!this.inFlightIDs) {
            if (this.logs.length > maxLogs) {
                this.logs = this.logs.slice(-maxLogs);
            }
            return;
        }

        let pendingCount = this.logs.reduce(
            (count, { id }) => count + (this.inFlightIDs!.has(id) ? 0 : 1),
            0
        );
        if (pendingCount <= maxLogs) {
            return;
        }
        this.logs = this.logs.filter(({ id }) => {
            if (this.inFlightIDs!.has(id) || pendingCount <= maxLogs) {
                return true;
            }
            pendingCount--;
            return false;
        });
    }

    private isEnabled(level: EventLogLevel): boolean {
        return levelPriority[level] >= levelPriority[this.config.minLevel ?? 'debug'];
    }

    private writeConsole(level: EventLogLevel, message: string, state?: unknown): void {
        const consoleLevel = this.config.consoleLevel ?? 'off';
        if (consoleLevel === 'off' || levelPriority[level] < levelPriority[consoleLevel]) {
            return;
        }

        const args = state === undefined ? [message] : [message, state];
        switch (level) {
        case 'debug':
            console.debug(...args);
            break;
        case 'info':
            console.info(...args);
            break;
        case 'warn':
            console.warn(...args);
            break;
        case 'error':
            console.error(...args);
            break;
        }
    }
}

export const eventLogger = new EventLogger();
