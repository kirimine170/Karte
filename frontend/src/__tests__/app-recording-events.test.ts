import { beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from '../app';
import { useASRStore } from '../stores/index';

describe('App recording events', () => {
    beforeEach(() => {
        useASRStore.setState({
            isRecording: true,
            recordingTranscriptPath: null,
            micLevel: 0,
            realtimeTranscript: { partial: '', final: [] },
        });
    });

    it('consumes partial，final，input-level，and stopped payloads in order', () => {
        const handlers = new Map<string, (data: unknown) => void>();
        const runtime = {
            EventsOn: vi.fn((name: string, callback: (data: unknown) => void) => {
                handlers.set(name, callback);
                return () => undefined;
            }),
        };
        const app = new App();
        Reflect.set(app, 'api', { GetFileList: vi.fn().mockResolvedValue([]) });
        Reflect.set(app, 'runtime', runtime);
        const setupWailsEvents = Reflect.get(app, 'setupWailsEvents') as (this: App) => void;
        setupWailsEvents.call(app);

        handlers.get('recording-transcript-partial')?.({
            text: 'partial text',
            transcriptPath: 'content/transcripts/live.md',
        });
        expect(useASRStore.getState()).toMatchObject({
            recordingTranscriptPath: 'content/transcripts/live.md',
            realtimeTranscript: { partial: 'partial text', final: [] },
        });

        handlers.get('recording-input-level')?.({ level: 0.42 });
        expect(useASRStore.getState().micLevel).toBe(42);

        handlers.get('recording-transcript-final')?.({ text: 'confirmed text' });
        expect(useASRStore.getState().realtimeTranscript).toEqual({
            partial: '',
            final: ['confirmed text'],
        });

        handlers.get('recording-stopped')?.({
            audioPath: 'data/audio/live.wav',
            transcriptPath: 'content/transcripts/live.md',
            error: 'transcript sync failed',
        });
        expect(useASRStore.getState()).toMatchObject({
            isRecording: false,
            micLevel: 0,
            recordingTranscriptPath: 'content/transcripts/live.md',
            realtimeTranscript: { partial: '', final: ['confirmed text'] },
        });
    });
});
