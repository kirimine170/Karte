import type { WailsAppAPI } from '../types/wails-api';

const bracketTimestampRegex = /\[(\d{1,2}):(\d{2})(?::(\d{2}))?(?:\.(\d{1,3}))?\]/g;

function toSeconds(part1: string, part2: string, part3?: string, part4?: string): number {
    const ms = part4 ? parseInt(String(part4).padEnd(3, '0'), 10) : 0;
    if (part3 !== undefined) {
        return parseInt(part1, 10) * 3600 + parseInt(part2, 10) * 60 + parseInt(part3, 10) + ms / 1000;
    }
    return parseInt(part1, 10) * 60 + parseInt(part2, 10) + ms / 1000;
}

export function convertTimestampsToLinks(html: string): string {
    return html.replace(bracketTimestampRegex, (match, p1, p2, p3, p4) => {
        const totalSeconds = toSeconds(p1, p2, p3, p4);
        return `<a href="#" class="timestamp-link" data-timestamp="${totalSeconds}">${match}</a>`;
    });
}

export function extractAudioPath(content: string): string | null {
    if (!content || !content.startsWith('---')) {
        return null;
    }
    const frontMatterRegex = /^---\s*\n([\s\S]*?)\n---\s*\n?/;
    const match = content.match(frontMatterRegex);
    if (!match) {
        return null;
    }
    const yamlContent = match[1];
    const simpleRegex = /^audio_path:\s*(.+?)\s*$/m;
    const simpleMatch = yamlContent.match(simpleRegex);
    if (simpleMatch && simpleMatch[1]) {
        let path = simpleMatch[1].trim();
        if ((path.startsWith('"') && path.endsWith('"')) || (path.startsWith("'") && path.endsWith("'"))) {
            path = path.slice(1, -1);
        }
        return path || null;
    }
    const audioPathRegex = /^audio_path:\s*(?:"([^"]*)"|'([^']*)'|([^\n\r]+?))\s*$/m;
    const audioPathMatch = yamlContent.match(audioPathRegex);
    if (audioPathMatch) {
        const audioPath = (audioPathMatch[1] || audioPathMatch[2] || audioPathMatch[3] || '').trim();
        return audioPath || null;
    }
    return null;
}

export async function updateAudioPlayerFromContent(
    api: WailsAppAPI,
    content: string,
    container: HTMLElement | null,
    audio: HTMLAudioElement | null
): Promise<void> {
    if (!container || !audio) {
        return;
    }
    const audioPath = extractAudioPath(content);
    if (!audioPath) {
        container.style.display = 'none';
        audio.src = '';
        return;
    }
    try {
        const audioURL = await api.GetAudioFileURL(audioPath);
        if (audio.src !== audioURL) {
            audio.src = audioURL;
            audio.load();
        }
        container.style.display = 'flex';
    } catch (error) {
        console.error('Failed to update audio player:', error);
        container.style.display = 'none';
        audio.src = '';
    }
}

export function setupTimestampLinkHandlers(iframe: HTMLIFrameElement): void {
    const doc = iframe.contentDocument;
    if (!doc) {
        return;
    }
    doc.addEventListener('click', (event) => {
        const target = event.target as HTMLElement | null;
        const link = target?.closest?.('a.timestamp-link') as HTMLAnchorElement | null;
        if (!link) {
            return;
        }
        event.preventDefault();
        const timestamp = parseFloat(link.dataset.timestamp || '');
        if (Number.isNaN(timestamp)) {
            return;
        }
        const parent = iframe.contentWindow?.parent;
        parent?.dispatchEvent(new CustomEvent('karte-timestamp-click', { detail: { timestamp } }));
    });
}
