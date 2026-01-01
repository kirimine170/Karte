// ログ検証ユーティリティ
// テストでログの順序や内容を検証するためのヘルパー関数

import { eventLogger, type EventLog } from '../utils/event-logger';

/**
 * ログの順序を検証
 */
export function expectLogSequence(expected: Array<{ component: string; action: string }>): void {
    const logs = eventLogger.getLogs();
    
    if (logs.length < expected.length) {
        throw new Error(
            `Expected at least ${expected.length} logs, but got ${logs.length}.\n` +
            `Logs: ${JSON.stringify(logs, null, 2)}`
        );
    }

    expected.forEach((expectedLog, index) => {
        const actualLog = logs[index];
        if (!actualLog) {
            throw new Error(
                `Expected log at index ${index} but got nothing.\n` +
                `Expected: ${JSON.stringify(expectedLog)}\n` +
                `All logs: ${JSON.stringify(logs, null, 2)}`
            );
        }

        if (actualLog.component !== expectedLog.component || actualLog.action !== expectedLog.action) {
            throw new Error(
                `Log mismatch at index ${index}.\n` +
                `Expected: ${JSON.stringify(expectedLog)}\n` +
                `Actual: ${JSON.stringify({ component: actualLog.component, action: actualLog.action })}\n` +
                `All logs: ${JSON.stringify(logs, null, 2)}`
            );
        }
    });
}

/**
 * 特定のコンポーネントのログを取得
 */
export function getLogsByComponent(component: string): EventLog[] {
    return eventLogger.getLogsByComponent(component);
}

/**
 * 特定のアクションのログを取得
 */
export function getLogsByAction(action: string): EventLog[] {
    return eventLogger.getLogsByAction(action);
}

/**
 * ログが特定の順序で記録されていることを検証（部分一致）
 */
export function expectLogContainsSequence(expected: Array<{ component: string; action: string }>): void {
    const logs = eventLogger.getLogs();
    let searchIndex = 0;

    for (const log of logs) {
        if (searchIndex >= expected.length) {
            return; // すべて見つかった
        }

        const expectedLog = expected[searchIndex];
        if (log.component === expectedLog.component && log.action === expectedLog.action) {
            searchIndex++;
        }
    }

    if (searchIndex < expected.length) {
        throw new Error(
            `Expected log sequence not found.\n` +
            `Expected: ${JSON.stringify(expected)}\n` +
            `Actual logs: ${JSON.stringify(logs.map(l => ({ component: l.component, action: l.action })), null, 2)}`
        );
    }
}

/**
 * ログをクリア
 */
export function clearLogs(): void {
    eventLogger.clearLogs();
}

/**
 * ログの数を検証
 */
export function expectLogCount(count: number): void {
    const actualCount = eventLogger.getLogCount();
    if (actualCount !== count) {
        throw new Error(
            `Expected ${count} logs, but got ${actualCount}.\n` +
            `Logs: ${JSON.stringify(eventLogger.getLogs(), null, 2)}`
        );
    }
}

/**
 * ログが特定の条件を満たすことを検証
 */
export function expectLogMatches(
    index: number,
    matcher: (log: EventLog) => boolean,
    description?: string
): void {
    const logs = eventLogger.getLogs();
    const log = logs[index];

    if (!log) {
        throw new Error(`No log at index ${index}. Total logs: ${logs.length}`);
    }

    if (!matcher(log)) {
        throw new Error(
            `Log at index ${index} does not match condition${description ? `: ${description}` : ''}.\n` +
            `Log: ${JSON.stringify(log, null, 2)}`
        );
    }
}

