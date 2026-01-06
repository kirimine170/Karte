import { beforeEach, describe, expect, it, vi } from 'vitest';
import { Topbar } from '../topbar';
import { useDocStore } from '../../stores/doc-store';
import { useExportStore } from '../../stores/export-store';
import { useUIStore } from '../../stores/ui-store';

const createApi = () => ({
    ExportPDF: vi.fn(),
});

describe('Topbar PDF export', () => {
    beforeEach(() => {
        useDocStore.setState({
            currentPath: '',
            files: [],
            searchQuery: '',
            hasUnsavedChanges: false,
            markdownContent: '',
            previewHtml: '',
        });
        useExportStore.setState({
            pdfExportProgress: { visible: false, progress: 0, message: '' },
            transcriptionProgress: { visible: false, progress: 0, message: '' },
        });
        useUIStore.setState({
            sidebarVisible: true,
            imageGalleryVisible: true,
            csvGalleryVisible: true,
            activeTab: 'editor',
            theme: 'light',
            hardWrap: false,
            statusMessage: '',
            statusClearTimer: null,
        });
    });

    it('exports PDF using preview HTML when iframe is missing', async () => {
        const api = createApi();
        api.ExportPDF.mockResolvedValue('/tmp/test.pdf');
        useDocStore.getState().setPreviewHtml('<html><body>Preview</body></html>');

        const topbar = new Topbar(api as any);
        await (topbar as any).handleExportPDF();

        expect(api.ExportPDF).toHaveBeenCalledWith('<html><body>Preview</body></html>');
        expect(useExportStore.getState().pdfExportProgress.visible).toBe(false);
        expect(useUIStore.getState().statusMessage).toContain('PDFをエクスポートしました');
    });

    it('shows status when there is no content to export', async () => {
        const api = createApi();
        const topbar = new Topbar(api as any);

        await (topbar as any).handleExportPDF();

        expect(api.ExportPDF).not.toHaveBeenCalled();
        expect(useUIStore.getState().statusMessage).toBe('エクスポートするコンテンツがありません');
    });

    it('handles export errors without throwing', async () => {
        const api = createApi();
        api.ExportPDF.mockRejectedValue(new Error('export failed'));
        useDocStore.getState().setPreviewHtml('<html><body>Preview</body></html>');

        const topbar = new Topbar(api as any);
        await (topbar as any).handleExportPDF();

        expect(api.ExportPDF).toHaveBeenCalled();
        expect(useExportStore.getState().pdfExportProgress.visible).toBe(false);
        expect(useUIStore.getState().statusMessage).toBe('PDFエクスポートに失敗しました');
    });
});
