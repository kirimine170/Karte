import { beforeEach, describe, expect, it, vi } from 'vitest';

import { App } from '../app';
import { useDocStore } from '../stores/doc-store';

describe('App file events', () => {
    beforeEach(() => {
        useDocStore.setState({ files: [] });
    });

    it('refreshes the sidebar file list when SaveFile emits file-changed', async () => {
        const handlers = new Map<string, (...args: unknown[]) => void>();
        const files = [{ path: 'content/projects/ephy/note/2026-09/new.md', title: 'New' }];
        const app = new App() as any;
        app.api = {
            GetFileList: vi.fn().mockResolvedValue(files),
        };
        app.runtime = {
            EventsOn: vi.fn((name: string, handler: (...args: unknown[]) => void) => {
                handlers.set(name, handler);
                return () => {};
            }),
        };
        app.refreshPreview = vi.fn();
        app.refreshGraph = vi.fn();

        app.setupWailsEvents();
        handlers.get('file-changed')?.('content/projects/ephy/note/2026-09/new.md');

        await vi.waitFor(() => expect(app.api.GetFileList).toHaveBeenCalledOnce());
        expect(useDocStore.getState().files).toEqual(files);
        expect(app.refreshPreview).toHaveBeenCalledOnce();
        expect(app.refreshGraph).toHaveBeenCalledOnce();
    });
});
