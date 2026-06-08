import { describe, it, expect, beforeEach, vi } from 'vitest';
import { useUIStore } from '../ui-store';

describe('UIStore', () => {
    beforeEach(() => {
        // 各テスト前にストアをリセット
        useUIStore.setState({
            sidebarVisible: true,
            imageGalleryVisible: true,
            csvGalleryVisible: true,
            workspaceMode: false,
            activeTab: 'editor',
            theme: 'light',
            hardWrap: false,
            statusMessage: '',
            statusClearTimer: null,
        });
    });

    describe('setSidebarVisible', () => {
        it('should toggle sidebar visibility', () => {
            const store = useUIStore.getState();
            expect(store.sidebarVisible).toBe(true);

            store.setSidebarVisible(false);
            expect(useUIStore.getState().sidebarVisible).toBe(false);

            store.setSidebarVisible(true);
            expect(useUIStore.getState().sidebarVisible).toBe(true);
        });
    });

    describe('setImageGalleryVisible', () => {
        it('should toggle image gallery visibility', () => {
            const store = useUIStore.getState();
            expect(store.imageGalleryVisible).toBe(true);

            store.setImageGalleryVisible(false);
            expect(useUIStore.getState().imageGalleryVisible).toBe(false);
        });
    });

    describe('setCsvGalleryVisible', () => {
        it('should toggle CSV gallery visibility', () => {
            const store = useUIStore.getState();
            expect(store.csvGalleryVisible).toBe(true);

            store.setCsvGalleryVisible(false);
            expect(useUIStore.getState().csvGalleryVisible).toBe(false);
        });
    });

    describe('setTheme', () => {
        it('should set theme and apply to document', () => {
            const store = useUIStore.getState();
            store.setTheme('dark');

            expect(useUIStore.getState().theme).toBe('dark');
            expect(document.documentElement.getAttribute('data-theme')).toBe('dark');
        });
    });

    describe('setStatusMessage', () => {
        it('should set status message', () => {
            const store = useUIStore.getState();
            store.setStatusMessage('Test message');

            expect(useUIStore.getState().statusMessage).toBe('Test message');
        });

        it('should clear message after duration', async () => {
            vi.useFakeTimers();
            const store = useUIStore.getState();
            store.setStatusMessage('Test message', 1000);

            expect(useUIStore.getState().statusMessage).toBe('Test message');

            vi.advanceTimersByTime(1000);
            expect(useUIStore.getState().statusMessage).toBe('');
            vi.useRealTimers();
        });
    });
});
