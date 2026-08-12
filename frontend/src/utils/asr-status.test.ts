import { describe, expect, it } from 'vitest';
import {
    ASR_INITIALIZATION_TIMEOUT_MS,
    getASRStatusPollingDecision,
    markASRInitializationStopped,
} from './asr-status';

describe('getASRStatusPollingDecision', () => {
    it('completes after successful, disabled, or failed initialization', () => {
        expect(getASRStatusPollingDecision({ initializing: false }, 2_000)).toBe('complete');
    });

    it('continues while initialization is active', () => {
        expect(getASRStatusPollingDecision({ initializing: true }, 2_000)).toBe('continue');
    });

    it('returns timeout when initialization exceeds the deadline', () => {
        expect(getASRStatusPollingDecision({ initializing: true }, ASR_INITIALIZATION_TIMEOUT_MS)).toBe('timeout');
    });

    it('clears the visible initialization state after timeout or polling failure', () => {
        expect(markASRInitializationStopped({ initialized: false, initializing: true })).toEqual({
            initialized: false,
            initializing: false,
        });
    });
});
