import type { WailsAppAPI } from '../types/wails-api';
import { isMarpMarkdown } from './custom-css';
import { renderMarpPreview } from './marp-preview';
import { prepareMarkdownForPreview } from './preview-content';

export async function renderPreparedPreview(prepared: string, api: WailsAppAPI): Promise<string> {
    if (isMarpMarkdown(prepared)) {
        return renderMarpPreview(prepared);
    }
    return api.PreviewMarkdown(prepared);
}

export async function renderMarkdownPreview(content: string, api: WailsAppAPI): Promise<{ prepared: string; html: string }> {
    const prepared = await prepareMarkdownForPreview(content, api);
    const html = await renderPreparedPreview(prepared, api);
    return { prepared, html };
}
