import { RULES, DEVICES, clamp, textStats, postRatio, cropRect, validatePost, createManifest, parseManifest, ICONS, createPostRenderer } from '../postlab-renderer/renderer.mjs';
const renderer = createPostRenderer();
document.body.insertAdjacentHTML('afterbegin', ICONS);

const $ = id => document.getElementById(id);
const STORAGE = 'karte.post-lab.draft.v1';
const defaults = () => ({ mode: 'app', aspect: '0.8', username: 'karte_studio', location: 'Kyoto, Japan', caption: '小さな発見を，日々のなかに．🌿 窓辺に届くやわらかな光と，お気に入りのグリーン．いつもより少しだけ，ゆっくり過ごしてみる．\n\n何気ない日常にも，残しておきたい瞬間がある．\nあなたの今日の「小さな発見」は何ですか？\n\n#暮らしの記録 #日々のこと #KarteStudio', devices: ['compact', 'standard', 'android'], textScale: 100, collapsedLines: 2, gridRatio: 0.75, customWidth: 360, customHeight: 780, pendingImages: [] });
let state = defaults();
let storedDraft = false;
try { const saved = localStorage.getItem(STORAGE); if (saved) { state = { ...state, ...parseManifest(JSON.parse(saved)) }; storedDraft = true; } } catch { /* A damaged draft does not prevent startup． */ }
let images = [], active = 0, view = 'feed', previewZoom = 0.72, nextId = 1;
let toastTimer, renderFrame, saveTimer, loadGeneration = 0;
let fileQueue = Promise.resolve();
const expanded = new Set();


function escapeHtml(value) { return String(value).replace(/[&<>"']/g, char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[char]); }
function toast(message) { clearTimeout(toastTimer); $('toast').textContent = message; $('toast').hidden = false; toastTimer = setTimeout(() => { $('toast').hidden = true; }, 5200); }
function ratio() { return postRatio(state.aspect, state.mode, images[0]); }
function current() { return images[active]; }
function draftManifest() {
  const manifest = createManifest(state, images, ratio());
  // Retain references to images that have not been reselected yet．
  manifest.images.push(...state.pendingImages);
  return manifest;
}
function save() {
  clearTimeout(saveTimer);
  saveTimer = setTimeout(() => {
    try {
      localStorage.setItem(STORAGE, JSON.stringify(draftManifest()));
      $('save-status').textContent = '設定を保存済み · 画像は次回再選択';
    } catch { $('save-status').textContent = '自動保存できません · JSONで保存できます'; }
  }, 250);
}
function scheduleRender() { cancelAnimationFrame(renderFrame); renderFrame = requestAnimationFrame(render); save(); }

function syncControls() {
  for (const key of ['mode', 'aspect', 'username', 'location', 'caption']) $(key).value = state[key];
  $('text-scale').value = state.textScale;
  $('collapsed-lines').value = state.collapsedLines;
  $('grid-ratio').value = state.gridRatio;
  $('custom-width').value = state.customWidth;
  $('custom-height').value = state.customHeight;
}

function renderEditor() {
  const rule = RULES[state.mode];
  $('account-summary').textContent = state.username || 'karte_studio';
  $('image-count').textContent = `${images.length} / ${rule.maxImages}`;
  $('aspect').querySelector('[value="0.75"]').disabled = state.mode === 'api';
  $('aspect-note').textContent = state.mode === 'api' ? 'API想定：4:5〜1.91:1 ／ 全画像に共通の比率を適用．' : 'アプリ想定：3:4〜1.91:1 ／ 全画像に共通の比率を適用．';
  $('image-list').innerHTML = images.map((image, index) => `<button class="thumbnail ${index === active ? 'selected' : ''}" data-image="${index}" aria-label="${index + 1}枚目：${escapeHtml(image.name)}" aria-pressed="${index === active}"><img src="${escapeHtml(image.url)}" alt=""><span>${index + 1}</span></button>`).join('');
  const image = current();
  $('image-details').textContent = image ? `${image.name} · ${image.width} × ${image.height} px${image.sample ? ' · SAMPLE' : ` · ${(image.bytes / 1024 / 1024).toFixed(2)} MiB`}${state.pendingImages.length ? ` · 元画像${state.pendingImages.length}枚が未選択` : ''}` : state.pendingImages.length ? `下書きの元画像を${state.pendingImages.length}枚追加し直してください．` : '画像を追加すると，ここに情報を表示します．';
  for (const id of ['remove-image', 'open-crop', 'export-image']) $(id).disabled = !image;
  $('move-left').disabled = !image || active === 0;
  $('move-right').disabled = !image || active === images.length - 1;
  const result = validatePost({ ...state, images, ratio: ratio() });
  if (state.pendingImages.length) result.issues.unshift({ level: 'warning', code: 'pending', message: `下書きの元画像${state.pendingImages.length}枚が未選択です．同じファイル名の画像を追加してください．` });
  const stats = result.stats;
  $('char-count').textContent = `${stats.characters.toLocaleString()} / 2,200`;
  $('char-count').classList.toggle('over-limit', stats.characters > 2200);
  $('hashtag-count').textContent = `# ${stats.hashtags}`;
  $('mention-count').textContent = `@ ${stats.mentions}`;
  $('grapheme-count').textContent = `見た目 ${stats.graphemes.toLocaleString()}文字`;
  $('caption-meter').style.width = `${Math.min(stats.characters / 2200 * 100, 100)}%`;
  $('caption-meter').classList.toggle('over-limit', stats.characters > 2200);
  const errors = result.issues.filter(item => item.level === 'error').length;
  const warnings = result.issues.filter(item => item.level === 'warning').length;
  $('validation-status').textContent = errors ? `要修正 ${errors}` : warnings ? `注意 ${warnings}` : '暫定ルール内';
  $('validation-status').className = `check-label ${errors ? 'has-error' : warnings ? 'has-warning' : ''}`;
  const messages = result.issues.length ? result.issues : [{ level: 'success', message: 'このルールセットでは問題は見つかりませんでした．実際の投稿可否は保証しません．' }];
  $('validation-list').innerHTML = messages.map(item => `<li class="issue ${item.level}"><span class="issue-mark">${item.level === 'error' ? '!' : item.level === 'warning' ? '!' : item.level === 'info' ? 'i' : '✓'}</span><span>${escapeHtml(item.message)}</span></li>`).join('');
  $('text-scale-value').textContent = `${state.textScale}%`;
}

function renderDevices() {
  $('device-choices').innerHTML = DEVICES.map(device => `<label class="device-chip ${state.devices.includes(device.id) ? 'selected' : ''}"><input type="checkbox" value="${device.id}" ${state.devices.includes(device.id) ? 'checked' : ''}><span>${device.id === 'compact' ? 'Compact' : device.id === 'standard' ? 'Standard' : device.id === 'android' ? 'Android' : device.id === 'desktop' ? 'Desktop' : 'カスタム'}</span></label>`).join('');
  $('custom-settings').hidden = !state.devices.includes('custom');
  $('comparison-label').textContent = view === 'grid' ? 'PROFILE PREVIEW' : 'FEED PREVIEW';
  $('ratio-summary').textContent = `${Math.round(1080)} × ${Math.round(1080 / ratio())} px · 文字 ${state.textScale}%${view === 'grid' ? ` · 一覧 ${state.gridRatio === 1 ? '1:1' : '3:4'}` : ''}`;
  renderer.renderComparison($('device-stage'));
}

function renderCrop() {
  const image = current();
  const crop = image?.crop || { x: 0.5, y: 0.5, zoom: 1 };
  $('crop-frame').style.aspectRatio = ratio();
  $('crop-frame').style.width = `${Math.min(420, 490 * ratio())}px`;
  renderer.drawPost($('crop-canvas'), image, false, 1080);
  renderer.drawPost($('grid-crop-canvas'), image, true, 360);
  for (const [key, value] of [['x', crop.x * 100], ['y', crop.y * 100], ['zoom', crop.zoom * 100]]) {
    $(`crop-${key}`).value = value;
    $(`crop-${key}-value`).textContent = key === 'zoom' ? `${crop.zoom.toFixed(2)}×` : `${Math.round(value)}%`;
    $(`crop-${key}`).disabled = !image;
  }
  if (image) {
    const c = cropRect(image.width, image.height, ratio(), crop);
    $('crop-metrics').innerHTML = `<span>元画像 ${image.width} × ${image.height}</span><span>保持 ${Math.round(c.retained * 100)}%</span><span>出力 1080 × ${Math.round(1080 / ratio())}</span>`;
  } else $('crop-metrics').textContent = '画像を追加してください．';
}

function render() {
  active = clamp(active, 0, Math.max(0, images.length - 1));
  renderer.configure({ state, images, active, view, previewZoom, expanded });
  renderEditor();
  document.querySelectorAll('.view-tab').forEach(button => { button.classList.toggle('active', button.dataset.view === view); button.setAttribute('aria-pressed', String(button.dataset.view === view)); });
  $('comparison-view').hidden = !['feed', 'grid'].includes(view);
  $('crop-view').hidden = view !== 'crop';
  $('guide-view').hidden = view !== 'guide';
  if (view === 'crop') renderCrop();
  else if (view !== 'guide') renderDevices();
}

function releaseImages() { for (const image of images) if (!image.sample) URL.revokeObjectURL(image.url); images = []; renderer.clear(); }
function loadElement(url) { return new Promise((resolve, reject) => { const element = new Image(); element.onload = () => resolve(element); element.onerror = () => reject(new Error('画像を読み込めませんでした．')); element.src = url; }); }
async function addSample(generation = loadGeneration) {
  const element = await loadElement('./assets/sample.svg');
  if (generation !== loadGeneration) return;
  const pending = state.pendingImages.find(item => item.filename === 'sample.svg');
  images.push({ id: nextId++, name: 'sample.svg', url: './assets/sample.svg', element, width: element.naturalWidth, height: element.naturalHeight, type: 'image/svg+xml', bytes: 0, sample: true, crop: pending?.crop || { x: 0.5, y: 0.5, zoom: 1 } });
  state.pendingImages = state.pendingImages.filter(item => item !== pending);
}

async function addFiles(files, generation) {
  if (generation !== loadGeneration) return;
  const errors = [];
  for (const file of Array.from(files)) {
    if (generation !== loadGeneration) return;
    if (!['image/jpeg', 'image/png', 'image/webp'].includes(file.type)) { errors.push(`${file.name}：JPEG・PNG・WebPを選んでください．`); continue; }
    if (images.length >= 20) { errors.push('この試作品では20枚まで読み込めます．'); break; }
    if (images.length + state.pendingImages.length >= 20 && !state.pendingImages.some(item => item.filename === file.name)) { errors.push('下書きの未選択画像を含めると20枚になります．元画像と同じファイル名で追加してください．'); continue; }
    if (file.size > 30 * 1024 * 1024) { errors.push(`${file.name}：試作品の読み込み上限30MiBを超えています．`); continue; }
    const url = URL.createObjectURL(file);
    try {
      const element = await loadElement(url);
      if (generation !== loadGeneration) { URL.revokeObjectURL(url); return; }
      // Guard excessive canvas decode dimensions as a local implementation limit．
      if (element.naturalWidth * element.naturalHeight > 80000000) throw new Error('試作品の上限8000万画素を超えています．');
      if (images.length === 1 && images[0].sample) releaseImages();
      const pendingIndex = state.pendingImages.findIndex(item => item.filename === file.name);
      const pending = pendingIndex >= 0 ? state.pendingImages.splice(pendingIndex, 1)[0] : null;
      images.push({ id: nextId++, name: file.name, element, url, width: element.naturalWidth, height: element.naturalHeight, type: file.type, bytes: file.size, crop: pending?.crop || { x: 0.5, y: 0.5, zoom: 1 } });
      active = images.length - 1;
    } catch (error) { URL.revokeObjectURL(url); errors.push(`${file.name}：${error.message}`); }
  }
  $('file-input').value = '';
  expanded.clear(); scheduleRender();
  if (errors.length) toast(errors.join(' '));
}
function queueFiles(files) {
  const snapshot = Array.from(files), generation = loadGeneration;
  fileQueue = fileQueue.then(() => addFiles(snapshot, generation)).catch(() => toast('画像を読み込めませんでした．もう一度選択してください．'));
}

async function download(blob, filename) {
  // Browser previews use downloads．The desktop app delegates only file persistence．
  if (window.parent !== window && window.parent.go) {
    const operation = blob.type === 'image/jpeg' ? 'image' : 'draft';
    const content = operation === 'draft' ? await blob.text() : await new Promise((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve(String(reader.result).split(',')[1]);
      reader.onerror = () => reject(new Error('JPEGを読み出せませんでした．'));
      reader.readAsDataURL(blob);
    });
    const id = crypto.randomUUID();
    try {
      const path = await new Promise((resolve, reject) => {
        const timeout = setTimeout(() => { window.removeEventListener('message', receive); reject(new Error('保存の応答がありません．')); }, 180000);
        function receive(event) {
          if (event.source !== window.parent || event.origin !== window.location.origin || event.data?.channel !== 'karte.postlab.file.v1' || event.data.id !== id) return;
          clearTimeout(timeout); window.removeEventListener('message', receive);
          if (event.data.error) reject(new Error(event.data.error)); else resolve(event.data.path);
        }
        window.addEventListener('message', receive);
        window.parent.postMessage({ channel: 'karte.postlab.file.v1', id, operation, filename, content }, location.origin === 'null' ? '*' : location.origin);
      });
      return Boolean(path);
    } catch (error) { toast(error.message); return false; }
  }
  const url = URL.createObjectURL(blob), anchor = document.createElement('a');
  anchor.href = url; anchor.download = filename; document.body.append(anchor); anchor.click(); anchor.remove();
  setTimeout(() => URL.revokeObjectURL(url), 5000);
  return true;
}

for (const key of ['username', 'location', 'caption']) $(key).addEventListener('input', event => { state[key] = event.target.value; if (key === 'caption') expanded.clear(); scheduleRender(); });
$('mode').addEventListener('change', event => {
  state.mode = event.target.value;
  if (state.mode === 'api' && state.aspect === '0.75') { state.aspect = '0.8'; $('aspect').value = state.aspect; toast('API想定の縦長比率4:5に変更しました．トリミングを確認してください．'); }
  renderer.clear(); expanded.clear(); scheduleRender();
});
$('aspect').addEventListener('change', event => { state.aspect = event.target.value; renderer.clear(); scheduleRender(); });
for (const [id, key] of [['text-scale', 'textScale'], ['collapsed-lines', 'collapsedLines'], ['grid-ratio', 'gridRatio']]) $(id).addEventListener('input', event => { state[key] = Number(event.target.value); expanded.clear(); scheduleRender(); });
for (const [id, key, min, max] of [['custom-width', 'customWidth', 280, 800], ['custom-height', 'customHeight', 480, 1200]]) $(id).addEventListener('change', event => { state[key] = clamp(Number(event.target.value) || min, min, max); event.target.value = state[key]; scheduleRender(); });
$('preview-zoom').addEventListener('change', event => { previewZoom = Number(event.target.value); scheduleRender(); });
$('device-choices').addEventListener('change', event => {
  const checked = Array.from($('device-choices').querySelectorAll('input:checked'), input => input.value);
  if (!checked.length) { event.target.checked = true; toast('比較する画面を1つ以上選択してください．'); return; }
  state.devices = checked; scheduleRender();
});
document.querySelector('.view-tabs').addEventListener('click', event => { const tab = event.target.closest('[data-view]'); if (tab) { view = tab.dataset.view; render(); } });
for (const [id, nextView] of [['open-crop', 'crop'], ['open-guide', 'guide']]) $(id).addEventListener('click', () => { view = nextView; render(); });
document.addEventListener('click', event => {
  const imageButton = event.target.closest('[data-image]');
  if (imageButton) { active = Number(imageButton.dataset.image); scheduleRender(); }
  const slide = event.target.closest('[data-slide]');
  if (slide && images.length) { active = (active + Number(slide.dataset.slide) + images.length) % images.length; scheduleRender(); }
  const toggle = event.target.closest('[data-expand]');
  if (toggle) { const id = toggle.dataset.expand; if (expanded.has(id)) expanded.delete(id); else expanded.add(id); render(); }
});
for (const [id, direction] of [['move-left', -1], ['move-right', 1]]) $(id).addEventListener('click', () => {
  const next = active + direction;
  if (next < 0 || next >= images.length) return;
  [images[active], images[next]] = [images[next], images[active]]; active = next; renderer.clear(); scheduleRender();
});
$('remove-image').addEventListener('click', () => { const [removed] = images.splice(active, 1); if (removed && !removed.sample) URL.revokeObjectURL(removed.url); renderer.clear(); scheduleRender(); });
$('drop-zone').addEventListener('click', () => $('file-input').click());
$('file-input').addEventListener('change', event => queueFiles(event.target.files));
$('drop-zone').addEventListener('dragover', event => { event.preventDefault(); $('drop-zone').classList.add('dragging'); });
$('drop-zone').addEventListener('dragleave', () => $('drop-zone').classList.remove('dragging'));
$('drop-zone').addEventListener('drop', event => { event.preventDefault(); $('drop-zone').classList.remove('dragging'); queueFiles(event.dataTransfer.files); });
// Prevent accidental navigation when a file misses the drop area．
document.addEventListener('dragover', event => { if (event.dataTransfer.types.includes('Files')) event.preventDefault(); });
document.addEventListener('drop', event => { if (event.dataTransfer.types.includes('Files')) event.preventDefault(); });

for (const key of ['x', 'y', 'zoom']) $(`crop-${key}`).addEventListener('input', event => { if (!current()) return; current().crop[key] = Number(event.target.value) / 100; scheduleRender(); });
$('reset-crop').addEventListener('click', () => { if (current()) { current().crop = { x: 0.5, y: 0.5, zoom: 1 }; scheduleRender(); } });
let dragging = null;
$('crop-frame').addEventListener('pointerdown', event => {
  if (!current()) return;
  event.preventDefault();
  const c = cropRect(current().width, current().height, ratio(), current().crop);
  dragging = { px: event.clientX, py: event.clientY, crop: { ...current().crop }, rect: c, image: current() };
  $('crop-frame').setPointerCapture(event.pointerId); $('crop-frame').classList.add('is-dragging');
});
$('crop-frame').addEventListener('pointermove', event => {
  if (!dragging || current() !== dragging.image) return;
  const { crop, rect, image } = dragging;
  const display = $('crop-frame').getBoundingClientRect();
  const travelX = image.width - rect.sw, travelY = image.height - rect.sh;
  image.crop.x = travelX > 0.01 ? clamp(crop.x - (event.clientX - dragging.px) / display.width * rect.sw / travelX, 0, 1) : crop.x;
  image.crop.y = travelY > 0.01 ? clamp(crop.y - (event.clientY - dragging.py) / display.height * rect.sh / travelY, 0, 1) : crop.y;
  scheduleRender();
});
for (const type of ['pointerup', 'pointercancel', 'lostpointercapture']) $('crop-frame').addEventListener(type, () => { dragging = null; $('crop-frame').classList.remove('is-dragging'); });
$('crop-frame').addEventListener('keydown', event => {
  const changes = { ArrowLeft: ['x', 0.02], ArrowRight: ['x', -0.02], ArrowUp: ['y', 0.02], ArrowDown: ['y', -0.02] };
  if (!current() || !changes[event.key]) return;
  event.preventDefault(); const [key, delta] = changes[event.key]; current().crop[key] = clamp(current().crop[key] + delta, 0, 1); scheduleRender();
});
$('export-image').addEventListener('click', () => {
  if (!current()) return;
  renderer.configure({ state, images, active, view, previewZoom, expanded });
  const image = current(), canvas = renderer.buildPostCanvas(image);
  canvas.toBlob(async blob => { if (!blob) { toast('画像を書き出せませんでした．'); return; } if (await download(blob, `${image.name.replace(/\.[^.]+$/, '')}-instagram-${active + 1}.jpg`)) toast(`JPEG保存：${canvas.width} × ${canvas.height}px · ${(blob.size / 1024 / 1024).toFixed(2)}MiB`); }, 'image/jpeg', 0.92);
});
$('save-draft').addEventListener('click', async () => {
  const manifest = draftManifest();
  if (await download(new Blob([JSON.stringify(manifest, null, 2)], { type: 'application/json' }), 'karte-instagram-draft.json')) toast('下書きJSONを保存しました．元画像は別途保管してください．');
});
document.addEventListener('keydown', event => {
  if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 's') { event.preventDefault(); $('save-draft').click(); }
});
$('import-button').addEventListener('click', () => $('draft-input').click());
$('draft-input').addEventListener('change', async event => {
  const file = event.target.files[0]; if (!file) return;
  try {
    if (file.size > 1024 * 1024) throw new Error('下書きJSONは1MiB以内にしてください．');
    const next = parseManifest(JSON.parse(await file.text()));
    ++loadGeneration;
    state = { ...defaults(), ...next }; active = 0; expanded.clear(); renderer.clear();
    const retained = [], remaining = [...images], pending = [];
    for (const item of state.pendingImages) {
      const index = remaining.findIndex(image => item.filename === image.name);
      if (index >= 0) { const [image] = remaining.splice(index, 1); image.crop = item.crop; retained.push(image); }
      else pending.push(item);
    }
    for (const image of remaining) if (!image.sample) URL.revokeObjectURL(image.url);
    state.pendingImages = pending;
    images = retained;
    if (!images.length && state.pendingImages.some(item => item.filename === 'sample.svg')) await addSample();
    syncControls(); scheduleRender();
    toast(state.pendingImages.length ? '下書きを開きました．元画像を追加すると位置を復元します．' : '下書きを開きました．');
  } catch (error) { toast(error.message || '下書きを読み込めませんでした．'); }
  event.target.value = '';
});
$('reset-button').addEventListener('click', async () => { ++loadGeneration; releaseImages(); state = defaults(); active = 0; expanded.clear(); await addSample(); syncControls(); scheduleRender(); toast('サンプルを読み込みました．'); });

syncControls();
if (!storedDraft || state.pendingImages.some(item => item.filename === 'sample.svg')) {
  try { await addSample(); } catch { toast('サンプルを読み込めませんでした．画像を追加してください．'); }
}
render();
if (storedDraft && state.pendingImages.length) toast('保存した設定を復元しました．元画像を追加し直してください．');
