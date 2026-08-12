export const ASR_INITIALIZATION_TIMEOUT_MS = 60_000;

export type ASRStatusPollingDecision = 'continue' | 'complete' | 'timeout';

export function getASRStatusPollingDecision(
    status: { initializing: boolean },
    elapsedMs: number,
): ASRStatusPollingDecision {
    if (!status.initializing) {
        return 'complete';
    }
    if (elapsedMs >= ASR_INITIALIZATION_TIMEOUT_MS) {
        return 'timeout';
    }
    return 'continue';
}

export function markASRInitializationStopped<T extends { initializing: boolean }>(status: T): T {
    return { ...status, initializing: false };
}
