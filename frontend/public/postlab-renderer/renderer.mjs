// Browser-independent rules and crop geometry．Shared with future Karte adapters．
export const RULES = Object.freeze({
  app: { label: 'Instagramアプリ', minRatio: 3 / 4, maxRatio: 1.91, maxImages: 20, maxCaption: 2200 },
  api: { label: '外部連携API', minRatio: 4 / 5, maxRatio: 1.91, maxImages: 10, maxCaption: 2200, maxBytes: 8 * 1024 * 1024 },
});

export const DEVICES = Object.freeze([
  { id: 'compact', name: 'iPhone · Compact', width: 375, height: 667, family: 'ios' },
  { id: 'standard', name: 'iPhone · Standard', width: 393, height: 852, family: 'ios' },
  { id: 'android', name: 'Android · Large', width: 412, height: 915, family: 'android' },
  { id: 'desktop', name: 'Web · Desktop', width: 600, height: 800, family: 'web' },
  { id: 'custom', name: 'カスタム', width: 360, height: 780, family: 'web' },
]);

export function clamp(value, min, max) { return Math.min(max, Math.max(min, value)); }

export function textStats(text) {
  const normalized = text.replace(/\r\n?/g, '\n');
  return {
    characters: Array.from(normalized).length,
    graphemes: typeof Intl.Segmenter === 'function'
      ? Array.from(new Intl.Segmenter('ja', { granularity: 'grapheme' }).segment(normalized)).length
      : Array.from(normalized).length,
    hashtags: Array.from(normalized.matchAll(/(?:^|[^\p{L}\p{N}_])#[\p{L}\p{N}_]+/gu)).length,
    mentions: Array.from(normalized.matchAll(/(?:^|[^\p{L}\p{N}_.])@[a-zA-Z0-9_]+(?:\.[a-zA-Z0-9_]+)*/gu)).length,
    lines: normalized.split('\n').length,
  };
}

export function postRatio(selection, mode, image) {
  const rule = RULES[mode] || RULES.app;
  const raw = selection === 'auto' ? (image ? image.width / image.height : 4 / 5) : Number(selection);
  return clamp(Number.isFinite(raw) && raw > 0 ? raw : 4 / 5, rule.minRatio, rule.maxRatio);
}

// x/y describe the position within the available travel，rather than the image center．
export function cropRect(width, height, ratio, options = {}) {
  if (![width, height, ratio].every(n => Number.isFinite(n) && n > 0)) throw new RangeError('Invalid crop dimensions');
  const zoom = clamp(Number(options.zoom) || 1, 1, 3);
  const x = clamp(Number.isFinite(options.x) ? options.x : 0.5, 0, 1);
  const y = clamp(Number.isFinite(options.y) ? options.y : 0.5, 0, 1);
  let sw = Math.min(width, height * ratio) / zoom;
  let sh = sw / ratio;
  return { sx: (width - sw) * x, sy: (height - sh) * y, sw, sh, retained: sw * sh / (width * height) };
}

export function validatePost({ mode, caption, images, ratio }) {
  const rule = RULES[mode] || RULES.app;
  const stats = textStats(caption);
  const issues = [];
  const add = (level, code, message) => issues.push({ level, code, message });
  if (!images.length) add('error', 'empty', '画像を1枚以上追加してください．');
  if (images.length > rule.maxImages) add('error', 'count', `画像はこのルールセットで${rule.maxImages}枚までです．現在${images.length}枚あります．`);
  if (stats.characters > rule.maxCaption) add('error', 'caption', `キャプションが目安の${rule.maxCaption.toLocaleString()}文字を${(stats.characters - rule.maxCaption).toLocaleString()}文字超えています．`);
  if (stats.hashtags > 5) add('warning', 'hashtags', 'ハッシュタグが5個を超えています．現在のアカウント側の上限も確認してください．');
  if (mode === 'api' && stats.mentions > 20) add('error', 'mentions', 'このAPIルールセットではメンションは20件までです．');
  if (ratio < rule.minRatio - 0.0001 || ratio > rule.maxRatio + 0.0001) add('error', 'ratio', '選択した投稿経路の比率範囲外です．');
  images.forEach((image, i) => {
    const prefix = `${i + 1}枚目`;
    if (image.width < 1080) add('warning', 'resolution', `${prefix}：元画像の幅が1080px未満です．拡大時にぼやける可能性があります．`);
    if (mode === 'api' && image.type !== 'image/jpeg') add('warning', 'format', `${prefix}：API向けの元画像はJPEG以外です．JPEG書き出しで変換できます．`);
    if (mode === 'api' && image.bytes > rule.maxBytes) add('warning', 'size', `${prefix}：元画像がAPI想定の8MiBを超えています．書き出し後の容量を再確認してください．`);
    const rect = cropRect(image.width, image.height, ratio, image.crop);
    if (rect.retained < 0.985) add('info', 'crop', `${prefix}：元画像の${Math.round((1 - rect.retained) * 100)}%がトリミングされます．`);
    if (rect.sw < 1080) add('warning', 'crop-resolution', `${prefix}：切り出し部分の幅は${Math.round(rect.sw)}pxです．1080pxへの書き出しで拡大されます．`);
  });
  return { stats, issues };
}

export function createManifest(state, images, ratio) {
  return {
    schema: 'karte.instagram-draft', version: 1,
    mode: state.mode, username: state.username, location: state.location, caption: state.caption,
    aspect: state.aspect, ratio,
    simulation: { devices: state.devices, textScale: state.textScale, collapsedLines: state.collapsedLines, gridRatio: state.gridRatio, customWidth: state.customWidth, customHeight: state.customHeight },
    images: images.map(image => ({ filename: image.name, width: image.width, height: image.height, mimeType: image.type, bytes: image.bytes, crop: { ...image.crop }, sourceRect: cropRect(image.width, image.height, ratio, image.crop) })),
  };
}

export function parseManifest(value) {
  if (!value || value.schema !== 'karte.instagram-draft' || value.version !== 1 || !Array.isArray(value.images) || value.images.length > 20 || typeof value.caption !== 'string' || value.caption.length > 50000) throw new Error('対応する下書きJSONではありません．');
  const sim = value.simulation || {};
  const aspects = ['auto', '0.75', '0.8', '1', '1.91'];
  return {
    mode: value.mode === 'api' ? 'api' : 'app',
    username: String(value.username || 'karte_studio').slice(0, 30),
    location: String(value.location || '').slice(0, 100), caption: value.caption,
    aspect: aspects.includes(value.aspect) ? value.aspect : '0.8',
    devices: Array.isArray(sim.devices) && sim.devices.some(id => DEVICES.some(d => d.id === id)) ? sim.devices.filter(id => DEVICES.some(d => d.id === id)) : ['compact', 'standard', 'android'],
    textScale: clamp(Number(sim.textScale) || 100, 85, 150), collapsedLines: clamp(Number(sim.collapsedLines) || 2, 1, 3),
    gridRatio: sim.gridRatio === 1 ? 1 : 0.75,
    customWidth: clamp(Number(sim.customWidth) || 360, 280, 800), customHeight: clamp(Number(sim.customHeight) || 780, 480, 1200),
    pendingImages: value.images.map(item => {
      if (!item || typeof item.filename !== 'string') throw new Error('画像のファイル名が不正です．');
      return { filename: item.filename, crop: { x: clamp(Number.isFinite(item.crop?.x) ? item.crop.x : 0.5, 0, 1), y: clamp(Number.isFinite(item.crop?.y) ? item.crop.y : 0.5, 0, 1), zoom: clamp(Number(item.crop?.zoom) || 1, 1, 3) } };
    }),
  };
}

export const ICONS = '  <svg class="svg-library" aria-hidden="true" xmlns="http://www.w3.org/2000/svg">\n    <symbol id="i-camera" viewBox="0 0 24 24"><rect x="3" y="3" width="18" height="18" rx="5"/><circle cx="12" cy="12" r="4"/><path d="M17.5 6.5h.01"/></symbol>\n    <symbol id="i-upload" viewBox="0 0 24 24"><path d="M12 16V4m-4 4 4-4 4 4M4 15v5h16v-5"/></symbol>\n    <symbol id="i-download" viewBox="0 0 24 24"><path d="M12 3v13m-4-4 4 4 4-4M4 16v5h16v-5"/></symbol>\n    <symbol id="i-heart" viewBox="0 0 24 24"><path d="M20.8 4.6a5.5 5.5 0 0 0-7.8 0L12 5.7l-1.1-1.1a5.5 5.5 0 0 0-7.8 7.8L12 21l8.8-8.6a5.5 5.5 0 0 0 0-7.8Z"/></symbol>\n    <symbol id="i-chat" viewBox="0 0 24 24"><path d="M21 11.5a9 9 0 1 0-5 8.1L22 21l-1.6-5.2a9 9 0 0 0 .6-4.3Z"/></symbol>\n    <symbol id="i-send" viewBox="0 0 24 24"><path d="m22 2-7 20-4-9L2 9 22 2Zm0 0L11 13"/></symbol>\n    <symbol id="i-bookmark" viewBox="0 0 24 24"><path d="M5 3h14v19l-7-5-7 5V3Z"/></symbol>\n    <symbol id="i-home" viewBox="0 0 24 24"><path d="m3 10 9-8 9 8v11h-7v-7h-4v7H3V10Z"/></symbol>\n    <symbol id="i-search" viewBox="0 0 24 24"><circle cx="10.5" cy="10.5" r="7.5"/><path d="m16 16 6 6"/></symbol>\n    <symbol id="i-plus" viewBox="0 0 24 24"><rect x="3" y="3" width="18" height="18" rx="5"/><path d="M12 7v10M7 12h10"/></symbol>\n    <symbol id="i-grid" viewBox="0 0 24 24"><path d="M3 3h18v18H3zM9 3v18m6-18v18M3 9h18M3 15h18"/></symbol>\n    <symbol id="i-crop" viewBox="0 0 24 24"><path d="M6 2v16h16M2 6h16v16M10 6h8v8"/></symbol>\n    <symbol id="i-phone" viewBox="0 0 24 24"><rect x="6" y="2" width="12" height="20" rx="3"/><path d="M10 5h4m-3 14h2"/></symbol>\n    <symbol id="i-info" viewBox="0 0 24 24"><circle cx="12" cy="12" r="9"/><path d="M12 11v6m0-10h.01"/></symbol>\n    <symbol id="i-check" viewBox="0 0 24 24"><path d="m5 12 4 4L19 6"/></symbol>\n    <symbol id="i-sliders" viewBox="0 0 24 24"><path d="M4 7h4m4 0h8M4 17h8m4 0h4"/><circle cx="10" cy="7" r="2"/><circle cx="14" cy="17" r="2"/></symbol>\n    <symbol id="i-arrow" viewBox="0 0 24 24"><path d="M5 12h14m-5-5 5 5-5 5"/></symbol>\n  </svg>';
function icon(name) { return `<svg aria-hidden="true"><use href="#i-${name}"/></svg>`; }
function escapeHtml(value) { return String(value).replace(/[&<>"']/g, char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[char]); }

// The renderer owns image geometry，feed/profile markup，and caption measurement．
// The caller owns editing，persistence，navigation，and event handling．
export function createPostRenderer() {
  let state, images = [], active = 0, view = 'feed', previewZoom = 0.72;
  let expanded = new Set();
  const postCanvases = new Map();
  function configure(model) {
    ({ state, images, active = 0, view = 'feed', previewZoom = 0.72, expanded = new Set() } = model);
  }
  function ratio() { return postRatio(state.aspect, state.mode, images[0]); }
  function current() { return images[active]; }
function buildPostCanvas(image) {
  const r = ratio();
  const key = JSON.stringify([image.id, image.crop, r]);
  if (postCanvases.has(key)) return postCanvases.get(key);
  const canvas = document.createElement('canvas');
  canvas.width = 1080; canvas.height = Math.round(1080 / r);
  const ctx = canvas.getContext('2d');
  ctx.fillStyle = '#fff'; ctx.fillRect(0, 0, canvas.width, canvas.height);
  ctx.imageSmoothingQuality = 'high';
  const c = cropRect(image.width, image.height, r, image.crop);
  ctx.drawImage(image.element, c.sx, c.sy, c.sw, c.sh, 0, 0, canvas.width, canvas.height);
  // Keep only the current geometry of each image．
  for (const oldKey of postCanvases.keys()) if (oldKey.startsWith(`[${image.id},`)) postCanvases.delete(oldKey);
  postCanvases.set(key, canvas);
  return canvas;
}

function drawPost(target, image = current(), grid = false, width = 600) {
  const r = grid ? state.gridRatio : ratio();
  target.width = width; target.height = Math.round(width / r);
  const ctx = target.getContext('2d');
  ctx.clearRect(0, 0, target.width, target.height);
  if (!image) { ctx.fillStyle = '#f3f0f7'; ctx.fillRect(0, 0, target.width, target.height); return; }
  const source = buildPostCanvas(image);
  const rect = grid ? cropRect(source.width, source.height, r) : { sx: 0, sy: 0, sw: source.width, sh: source.height };
  ctx.imageSmoothingQuality = 'high';
  ctx.drawImage(source, rect.sx, rect.sy, rect.sw, rect.sh, 0, 0, target.width, target.height);
}

function highlightText(text) {
  const fragment = document.createDocumentFragment();
  const tokens = text.split(/([#@][\p{L}\p{N}_.]+)/gu);
  tokens.forEach((token, i) => {
    if (i % 2) { const span = document.createElement('span'); span.className = 'caption-tag'; span.textContent = token; fragment.append(span); }
    else fragment.append(document.createTextNode(token));
  });
  return fragment;
}

// Measure the actual browser layout at the logical viewport width，before visual scaling．
function fillCaption(node, device) {
  const text = state.caption.replace(/\r\n?/g, '\n');
  const graphemes = typeof Intl.Segmenter === 'function' ? Array.from(new Intl.Segmenter('ja', { granularity: 'grapheme' }).segment(text), item => item.segment) : Array.from(text);
  const name = state.username || 'karte_studio';
  const lineHeight = 20 * state.textScale / 100;
  const maxHeight = lineHeight * state.collapsedLines + 1;
  const set = (body, tail = '') => { node.innerHTML = `<strong>${escapeHtml(name)}</strong> `; node.append(document.createTextNode(body)); if (tail) { const end = document.createElement('span'); end.textContent = tail; node.append(end); } };
  set(text);
  const fullLines = Math.max(1, Math.round(node.scrollHeight / lineHeight));
  const overflows = node.scrollHeight > maxHeight;
  let visible = graphemes.length;
  if (overflows && !expanded.has(device.id)) {
    let low = 0, high = graphemes.length;
    while (low < high) {
      const middle = Math.ceil((low + high) / 2);
      set(graphemes.slice(0, middle).join(''), '…続きを読む');
      if (node.scrollHeight <= maxHeight) low = middle; else high = middle - 1;
    }
    visible = low;
  }
  node.innerHTML = `<strong>${escapeHtml(name)}</strong> `;
  node.append(highlightText(graphemes.slice(0, visible).join('')));
  if (overflows) {
    if (!expanded.has(device.id)) node.append(document.createTextNode('…'));
    const more = document.createElement('button'); more.className = 'caption-more'; more.dataset.expand = device.id;
    more.textContent = expanded.has(device.id) ? ' 閉じる' : '続きを読む'; more.setAttribute('aria-expanded', String(expanded.has(device.id)));
    node.append(more);
  }
  if (!text) node.hidden = true;
  return { visible: Array.from(graphemes.slice(0, visible).join('')).length, fullLines, collapsed: overflows && !expanded.has(device.id) };
}

function phoneHtml(device) {
  const w = device.width, h = device.height;
  const name = escapeHtml(state.username || 'karte_studio');
  const image = current();
  const avatar = `<span class="avatar">${escapeHtml((state.username || 'k')[0])}</span>`;
  const media = `<div class="post-media" style="aspect-ratio:${ratio()}"><canvas class="feed-canvas" role="img" aria-label="${escapeHtml(image?.name || '画像なし')}"></canvas>${images.length > 1 ? `<span class="carousel-count">${active + 1}/${images.length}</span><button class="carousel-prev" data-slide="-1" aria-label="前の画像">‹</button><button class="carousel-next" data-slide="1" aria-label="次の画像">›</button>` : ''}${!image ? '<span class="empty-media">画像を追加してください</span>' : ''}</div>`;
  const feed = `<article class="ig-post"><div class="post-header">${avatar}<div><strong>${name}</strong>${state.location ? `<small>${escapeHtml(state.location)}</small>` : ''}</div><span class="post-more">•••</span></div>${media}<div class="post-actions">${icon('heart')}${icon('chat')}${icon('send')}${images.length > 1 ? `<div class="carousel-dots">${images.slice(0, 7).map((_, i) => `<i class="${i === Math.min(active, 6) ? 'active' : ''}"></i>`).join('')}</div>` : ''}<span class="bookmark">${icon('bookmark')}</span></div><div class="post-body"><strong class="like-count">128件のいいね</strong><div class="post-caption"></div><span class="comment-label">コメントをすべて見る</span><small class="post-date">たった今 · プレビュー</small></div></article><div class="feed-end"><span>✳</span>ここまでが投稿プレビューです</div>`;
  const grid = `<div class="profile-top"><div class="profile-account">${avatar}<div><strong>${images.length}</strong><span>投稿</span></div><div><strong>1,024</strong><span>フォロワー</span></div><div><strong>128</strong><span>フォロー中</span></div></div><strong>Karte Studio</strong><p>小さな発見を，日々のなかに．<br>Field notes for everyday life 🌿</p><div class="profile-buttons"><span>プロフィールを編集</span><span>シェア</span></div></div><div class="profile-tabs">${icon('grid')}${icon('camera')}</div><div class="profile-grid">${images.length ? Array.from({ length: Math.max(9, images.length) }, (_, i) => `<div class="grid-tile ${i === active ? 'selected' : ''}" style="aspect-ratio:${state.gridRatio}">${i < images.length ? `<button data-image="${i}" aria-label="一覧の${i + 1}枚目を選択"><canvas data-grid-image="${i}" role="img" aria-label="${escapeHtml(images[i].name)}"></canvas>${images.length > 1 ? '<span class="grid-stack">▣</span>' : ''}</button>` : '<span class="grid-placeholder">✳</span>'}</div>`).join('') : '<p class="empty-grid">画像を追加してください</p>'}</div><div class="profile-footnote">追加した画像を一覧のサムネイルとして配置した比較用レイアウトです．</div>`;
  return `<section class="device-card" data-device="${device.id}"><div class="device-card-heading"><span class="device-indicator ${device.family}"></span><strong>${escapeHtml(device.name)}</strong><span>${w} × ${h}</span></div><div class="phone-holder" style="width:${(w + 16) * previewZoom}px;height:${(h + 16) * previewZoom}px"><div class="phone-shell ${device.family}" style="width:${w + 16}px;height:${h + 16}px;transform:scale(${previewZoom})"><div class="phone-screen" style="--ig-text-scale:${state.textScale / 100}"><div class="status-bar"><strong>9:41</strong><span>▮▮▮　◒　▰</span></div><div class="ig-header"><span class="ig-wordmark">${view === 'grid' ? name : 'Instagram'}</span><span>${icon('heart')}${icon('send')}</span></div><div class="phone-scroll" data-scroll="${device.id}">${view === 'grid' ? grid : feed}</div><div class="ig-bottom">${icon('home')}${icon('search')}${icon('plus')}${icon('camera')}<span class="mini-avatar">${escapeHtml((state.username || 'k')[0])}</span></div><div class="home-indicator"></div></div></div></div><div class="device-metrics" id="metrics-${device.id}"></div></section>`;
}


  function renderComparison(stage) {
    const scrollPositions = new Map(Array.from(stage.querySelectorAll('[data-scroll]'), node => [node.dataset.scroll, node.scrollTop]));
  const devices = DEVICES.filter(device => state.devices.includes(device.id)).map(device => device.id === 'custom' ? { ...device, width: state.customWidth, height: state.customHeight } : device);
  stage.innerHTML = devices.map(phoneHtml).join('');
  for (const device of devices) {
    const card = stage.querySelector(`[data-device="${device.id}"]`);
    card.querySelectorAll('.feed-canvas').forEach(canvas => drawPost(canvas, current(), false, device.width * 2));
    card.querySelectorAll('[data-grid-image]').forEach(canvas => drawPost(canvas, images[Number(canvas.dataset.gridImage)], true, 400));
    const caption = card.querySelector('.post-caption');
    if (caption) {
      const stats = fillCaption(caption, device);
      // offset positions are independent of transform scale．The scroll viewport excludes navigation．
      const scroller = card.querySelector('.phone-scroll');
      const offset = caption.getBoundingClientRect().top - scroller.getBoundingClientRect().top;
      const below = offset / previewZoom >= scroller.clientHeight;
      card.querySelector('.device-metrics').innerHTML = `<div><span>キャプション</span><strong>${stats.visible}文字${stats.collapsed ? 'まで表示' : ' · 全文表示'}</strong></div><div><span>全文の折り返し</span><strong>${stats.fullLines}行</strong></div><p class="${below ? 'metric-warning' : ''}">${below ? '↓ 本文は初期画面の下にあります' : '本文の先頭が初期画面に入ります'}</p>`;
    } else {
      const source = current() && buildPostCanvas(current());
      const retained = source ? cropRect(source.width, source.height, state.gridRatio).retained : 1;
      card.querySelector('.device-metrics').innerHTML = `<div><span>サムネイルの幅</span><strong>${Math.round(device.width / 3)} CSS px</strong></div><div><span>投稿からの追加切り取り</span><strong>${Math.round((1 - retained) * 100)}%</strong></div><p>中央位置で一覧の枠へフィット</p>`;
    }
    card.querySelector('.phone-scroll').scrollTop = scrollPositions.get(device.id) || 0;
  }
}


  return { configure, renderComparison, drawPost, buildPostCanvas, clear: () => postCanvases.clear() };
}
