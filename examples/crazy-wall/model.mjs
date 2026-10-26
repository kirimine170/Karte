export const RELATIONS = {
  supports: { label: '支持する', short: '支持', color: '#507e6a' },
  contradicts: { label: '矛盾する', short: '矛盾', color: '#b35447' },
  related_to: { label: '関連する', short: '関連', color: '#7d8496' },
  questions: { label: '要確認', short: '要確認', color: '#b78b3b' },
};
export const KINDS = { pdf: 'PDF', markdown: 'MARKDOWN', image: 'IMAGE', audio: 'AUDIO', note: 'THOUGHT', excerpt: 'EXCERPT' };
export const clamp = (v, min, max) => Math.max(min, Math.min(max, v));
export function classifyFile(name) {
  const ext = name.split('.').pop().toLowerCase();
  if (ext === 'pdf') return 'pdf';
  if (['md', 'markdown', 'txt'].includes(ext)) return 'markdown';
  if (['png','jpg','jpeg','webp','gif','svg'].includes(ext)) return 'image';
  if (['wav','mp3','m4a','ogg','flac','aac'].includes(ext)) return 'audio';
  return null;
}
export function connectionPoints(a, b) {
  const ca = { x: a.x + a.w / 2, y: a.y + a.h / 2 }, cb = { x: b.x + b.w / 2, y: b.y + b.h / 2 };
  const dx = cb.x - ca.x, dy = cb.y - ca.y;
  if (dx === 0 && dy === 0) return {x1:ca.x+a.w/2,y1:ca.y,x2:cb.x-b.w/2,y2:cb.y};
  const ta = Math.min(a.w / 2 / Math.abs(dx), a.h / 2 / Math.abs(dy));
  const tb = Math.min(b.w / 2 / Math.abs(dx), b.h / 2 / Math.abs(dy));
  return { x1: ca.x + dx * ta, y1: ca.y + dy * ta, x2: cb.x - dx * tb, y2: cb.y - dy * tb };
}
export function fitViewport(cards, width, height) {
  if (!cards.length) return { x: width / 2 - 400, y: 60, zoom: 0.8 };
  const minX = Math.min(...cards.map(c => c.x)), minY = Math.min(...cards.map(c => c.y));
  const maxX = Math.max(...cards.map(c => c.x + c.w)), maxY = Math.max(...cards.map(c => c.y + c.h));
  const zoom = clamp(Math.min((width - 100) / (maxX - minX), (height - 220) / (maxY - minY)), 0.2, 1.1);
  return { x: (width - (maxX - minX) * zoom) / 2 - minX * zoom, y: 140 + (height - 220 - (maxY - minY) * zoom) / 2 - minY * zoom, zoom };
}
export function validateBoard(data) {
  if (!data || data.schema !== 'karte-research-wall/1' || typeof data.title !== 'string' || !Array.isArray(data.cards) || !Array.isArray(data.edges) || data.cards.length > 250 || data.edges.length > 1000) throw new Error('対応するボードJSONではありません．');
  const ids = new Set();
  for (const c of data.cards) {
    if (!c || typeof c.id !== 'string' || ids.has(c.id) || !Object.hasOwn(KINDS, c.kind) || typeof c.title !== 'string' || typeof c.body !== 'string' || !['x','y','w','h'].every(k => Number.isFinite(c[k])) || c.w < 100 || c.w > 1200 || c.h < 80 || c.h > 1800 || Math.abs(c.x) > 30000 || Math.abs(c.y) > 30000) throw new Error('カードの形式が正しくありません．');
    if (c.asset && !/^\/assets\/[a-z\d._-]+$/i.test(c.asset)) throw new Error('不正な資料パスです．');
    if (c.attachment && (!/^data:(image\/(png|jpeg|webp|gif|svg\+xml)|application\/pdf|audio\/[a-z\d.+-]+);base64,/i.test(c.attachment) || c.attachment.length > 30 * 1024 * 1024)) throw new Error('資料データの形式が正しくありません．');
    ids.add(c.id);
  }
  const edgeIds = new Set();
  for (const e of data.edges) {
    if (!e || typeof e.id !== 'string' || edgeIds.has(e.id) || !ids.has(e.from) || !ids.has(e.to) || e.from === e.to || !Object.hasOwn(RELATIONS, e.relation) || typeof e.reason !== 'string' || !['pending','reviewed'].includes(e.status)) throw new Error('関係の形式が正しくありません．');
    edgeIds.add(e.id);
  }
  return data;
}
export function makeEdge(from, to, edges, id) {
  if (!from || !to || from === to) return null;
  if (edges.some(e => e.from === from && e.to === to)) return null;
  return { id, from, to, relation: 'related_to', reason: '', status: 'pending' };
}
export function markdownHTML(text) {
  // Escape before rendering．Raw HTML and executable URLs are never interpreted．
  const escaped = text.replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  return escaped.split('\n').map(line => {
    const inline = line.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>').replace(/`([^`]+)`/g, '<code>$1</code>');
    if (/^### /.test(line)) return `<h3>${inline.slice(4)}</h3>`;
    if (/^## /.test(line)) return `<h2>${inline.slice(3)}</h2>`;
    if (/^# /.test(line)) return `<h1>${inline.slice(2)}</h1>`;
    if (/^&gt; /.test(line)) return `<blockquote>${inline.slice(5)}</blockquote>`;
    if (/^- /.test(line)) return `<p class="md-list">• ${inline.slice(2)}</p>`;
    return line ? `<p>${inline}</p>` : '<br>';
  }).join('');
}
