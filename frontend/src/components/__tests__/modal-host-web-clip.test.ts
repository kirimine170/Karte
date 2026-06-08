import { describe, it, expect, beforeEach, vi } from 'vitest';
import { waitFor } from '@testing-library/dom';
import { ModalHost } from '../modal-host';
import { useDocStore, useModalStore, useUIStore } from '../../stores/index';

const mockApi = {
    ClipURL: vi.fn(),
    GetFileList: vi.fn(),
    LoadFile: vi.fn(),
    PreviewMarkdown: vi.fn(),
} as any;

describe('ModalHost Web Clip', () => {
    beforeEach(() => {
        vi.clearAllMocks();
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
        useDocStore.setState({
            files: [],
            currentPath: '',
            markdownContent: '',
            previewHtml: '',
            hasUnsavedChanges: false,
            searchQuery: '',
        });
        useModalStore.setState({
            filenameModal: { visible: false, value: '' },
            renameFileModal: { visible: false, value: '', currentPath: '' },
            unsavedConfirmModal: { visible: false, onSave: () => {}, onDiscard: () => {} },
            customCssModal: { visible: false, value: '' },
            webClipModal: { visible: false, url: '', importing: false, warnings: [] },
            csvEditModal: { visible: false, filePath: '', data: [] },
            conflictModal: { visible: false, conflictInfo: null },
            imagePreviewModal: { visible: false, imagePath: '', imageName: '', metadata: '', systemMetadata: '' },
        });

        document.body.innerHTML = `
            <div id="filenameModal"></div>
            <input id="filenameInput" />
            <button id="createFileBtn"></button>
            <button id="cancelFileBtn"></button>
            <div id="renameFileModal"></div>
            <input id="renameFileInput" />
            <button id="confirmRenameBtn"></button>
            <button id="cancelRenameBtn"></button>
            <div id="unsavedConfirmModal"></div>
            <button id="unsavedSaveBtn"></button>
            <button id="unsavedDiscardBtn"></button>
            <button id="unsavedCancelBtn"></button>
            <div id="customCssModal"></div>
            <textarea id="customCssTextarea"></textarea>
            <button id="saveCustomCssBtn"></button>
            <button id="clearCustomCssBtn"></button>
            <button id="cancelCustomCssBtn"></button>
            <div id="webClipModal"></div>
            <input id="webClipUrlInput" />
            <button id="importWebClipBtn"></button>
            <button id="cancelWebClipBtn"></button>
            <div id="webClipWarnings"></div>
            <div id="csvEditModal"></div>
            <span id="csvEditFileName"></span>
            <thead id="csvEditTableHead"></thead>
            <tbody id="csvEditTableBody"></tbody>
            <button id="csvAddRowBtn"></button>
            <button id="csvAddColBtn"></button>
            <button id="csvDeleteRowBtn"></button>
            <button id="csvDeleteColBtn"></button>
            <button id="csvSaveBtn"></button>
            <button id="csvCancelBtn"></button>
            <div id="conflictModal"></div>
            <div id="conflictFilePath"></div>
            <div id="diffLocal"></div>
            <div id="diffRemote"></div>
            <button id="resolveConflictBtn"></button>
            <button id="cancelConflictBtn"></button>
            <div id="imagePreviewModal"><div class="image-preview-overlay"></div></div>
            <img id="imagePreviewImg" />
            <div id="imagePreviewName"></div>
            <div id="imagePreviewPath"></div>
            <button id="imagePreviewClose"></button>
            <textarea id="imageMetadataEditor"></textarea>
            <button id="imageMetadataSaveBtn"></button>
            <div id="imageMetadataStatus"></div>
            <textarea id="imageSystemMetadataEditor"></textarea>
            <button id="imageSystemMetadataSaveBtn"></button>
            <div id="imageSystemMetadataStatus"></div>
        `;
    });

    it('imports a web clip and opens the generated markdown', async () => {
        mockApi.ClipURL.mockResolvedValue({
            markdownPath: 'content/clips/example.md',
            assetDir: 'content/clips/assets/example',
            title: 'Example',
            sourceUrl: 'https://example.com/article',
            warnings: [],
        });
        mockApi.GetFileList.mockResolvedValue([{ path: 'content/clips/example.md', title: 'Example' }]);
        mockApi.LoadFile.mockResolvedValue('# Example');
        mockApi.PreviewMarkdown.mockResolvedValue('<h1>Example</h1>');

        const host = new ModalHost(mockApi);
        host.init();
        useModalStore.getState().showWebClipModal();
        useModalStore.getState().setWebClipModalUrl('https://example.com/article');

        document.getElementById('importWebClipBtn')?.dispatchEvent(new MouseEvent('click', { bubbles: true }));

        await waitFor(() => expect(mockApi.ClipURL).toHaveBeenCalledWith({
            url: 'https://example.com/article',
            mode: 'article',
            imageMode: 'download',
            outputDir: '',
        }));
        await waitFor(() => expect(useDocStore.getState().currentPath).toBe('content/clips/example.md'));
        expect(useDocStore.getState().markdownContent).toBe('# Example');
        expect(useModalStore.getState().webClipModal.visible).toBe(false);
    });

    it('keeps the modal open and shows warnings when import fails', async () => {
        mockApi.ClipURL.mockRejectedValue(new Error('fetch failed'));

        const host = new ModalHost(mockApi);
        host.init();
        useModalStore.getState().showWebClipModal();
        useModalStore.getState().setWebClipModalUrl('https://example.com/article');

        document.getElementById('importWebClipBtn')?.dispatchEvent(new MouseEvent('click', { bubbles: true }));

        await waitFor(() => expect(useModalStore.getState().webClipModal.warnings).toEqual(['fetch failed']));
        expect(useModalStore.getState().webClipModal.visible).toBe(true);
    });
});
