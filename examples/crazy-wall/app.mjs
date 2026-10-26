import { RELATIONS, KINDS, clamp, classifyFile, connectionPoints, fitViewport, validateBoard, makeEdge, markdownHTML, textQuote, resolveTextQuote } from './model.mjs';
import { createSample } from './sample.mjs';

const $ = (q, root = document) => root.querySelector(q);
const $$ = (q, root = document) => [...root.querySelectorAll(q)];
const esc = v => String(v ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const uid = prefix => `${prefix}-${crypto.randomUUID()}`;
const viewport = $('#viewport'), world = $('#world'), cardLayer = $('#cards'), edgeLayer = $('#edges');
let board = createSample(), view = { x: 0, y: 0, zoom: .7 }, selected = null, tool = 'select', linkFrom = null;
let filter = 'all', query = '', drag = null, spaceHeld = false, dirty = false, saveTimer, toastTimer, saveVersion = 0;
let lastSave = Promise.resolve(), viewerSession = 0, viewerCardId = null, viewerPage = 1, renderTask = null;
let history = [], keyboardMove = false, importing = false;
let pendingQuote=null, linkAnchor=null, textLayerTask=null, pdfRegionMode=false, regionDrag=null, viewerFocus=null, pdfRenderSequence=0;
const thumbnails = new Map(), pdfDocs = new Map(), audioPlayers = new Map();
const thumbnailSizes = new Map();
const icons = { pdf: '▤', markdown: '≡', image: '▧', audio: '♫', note: '▣', excerpt: '❞' };

function toast(message, error = false) {
  clearTimeout(toastTimer); const t = $('#toast'); t.textContent = message; t.hidden = false; t.classList.toggle('error', error);
  toastTimer = setTimeout(() => t.hidden = true, error ? 6500 : 3300);
}
function snapshot() { return { ...board, cards: board.cards.map(c => ({...c})), edges: board.edges.map(e => ({...e})) }; }
function checkpoint() { history.push(snapshot()); if (history.length > 25) history.shift(); $('#undo').disabled = false; }
function undo() { if (!history.length) return toast('戻せる操作はありません．'); board = history.pop(); selected = null; linkFrom = null;cancelReference(); render(); scheduleSave(); }
const db = await new Promise((resolve, reject) => {
  const request = indexedDB.open('karte-research-wall-poc', 1);
  request.onupgradeneeded = () => request.result.createObjectStore('board');
  request.onsuccess = () => resolve(request.result); request.onerror = () => reject(request.error);
}).catch(() => null);
async function readSaved() {
  if (!db) return null;
  return new Promise(resolve => { const r = db.transaction('board').objectStore('board').get('current'); r.onsuccess = () => resolve(r.result); r.onerror = () => resolve(null); });
}
function scheduleSave() {
  dirty = true; saveVersion++; $('#save-state').textContent = '○ 保存中…';
  clearTimeout(saveTimer); saveTimer = setTimeout(persist, 250);
}
function persist() {
  if (!db) { $('#save-state').textContent = '○ 保存不可'; toast('ブラウザの保存領域を利用できません．JSONを書き出して保存してください．', true); return; }
  const value = snapshot(), version = saveVersion;
  lastSave = lastSave.catch(() => {}).then(() => new Promise((resolve, reject) => {
    const tx = db.transaction('board', 'readwrite'); tx.objectStore('board').put(value, 'current');
    tx.oncomplete = () => { if (version === saveVersion) { dirty = false; $('#save-state').textContent = '● ローカルに保存済み'; } resolve(); };
    tx.onerror = () => { $('#save-state').textContent = '○ 保存エラー'; toast('保存できませんでした．空き容量を確認し，JSONを書き出してください．', true); reject(tx.error); };
    tx.onabort = tx.onerror;
  }));
  lastSave.catch(() => {});
}
const saved = await readSaved();
if (saved) { try { board = validateBoard(saved); } catch { toast('保存データを読めないため，サンプルを表示しました．', true); } }
window.addEventListener('beforeunload', event => { if (dirty) { persist(); event.preventDefault(); event.returnValue = ''; } });

function sourceURL(c) { return c.attachment || c.asset || ''; }
function textNodes(root) {
  const walker=document.createTreeWalker(root,NodeFilter.SHOW_TEXT,{acceptNode:n=>n.parentElement.closest('.highlight-layer')?NodeFilter.FILTER_REJECT:NodeFilter.FILTER_ACCEPT});
  const nodes=[];while(walker.nextNode())nodes.push(walker.currentNode);return nodes;
}
function textRange(root,start,end) {
  const nodes=textNodes(root),range=document.createRange();let offset=0,started=false;
  for(const n of nodes){const next=offset+n.length;if(!started&&start>=offset&&start<next){range.setStart(n,start-offset);started=true;}if(started&&end<=next){range.setEnd(n,end-offset);return range;}offset=next;}
  return null;
}
function rootText(root){return textNodes(root).map(n=>n.textContent).join('');}
function clippedRects(range,box) {
  return [...range.getClientRects()].flatMap(r=>{const x=Math.max(r.left,box.left),y=Math.max(r.top,box.top),right=Math.min(r.right,box.right),bottom=Math.min(r.bottom,box.bottom);return right-x>1&&bottom-y>1?[{x:(x-box.left)/box.width,y:(y-box.top)/box.height,w:(right-x)/box.width,h:(bottom-y)/box.height}]:[];}).slice(0,150);
}
function addRectHighlight(layer,rect,key,extra='') {
  const el=document.createElement('span');el.className=`reference-highlight ${extra}`;el.dataset.referenceKey=key;
  el.style.cssText=`left:${rect.x*100}%;top:${rect.y*100}%;width:${rect.w*100}%;height:${rect.h*100}%;`;layer.append(el);
}
function paintTextRoot(root,refs,activeKey) {
  $('.highlight-layer',root)?.remove();const text=rootText(root),box=root.getBoundingClientRect(),layer=document.createElement('div');layer.className='highlight-layer';layer.setAttribute('aria-hidden','true');
  for(const r of refs){
    const resolved=resolveTextQuote(text,r.anchor);if(!resolved)continue;
    const range=textRange(root,resolved.start,resolved.end);if(!range)continue;
    for(const rect of clippedRects(range,box))addRectHighlight(layer,rect,r.key,r.key===activeKey?'focused-reference':'');
  }root.append(layer);
}
function paintPdfThumbnail(c) {
  const paper=cardLayer.querySelector(`[data-id="${CSS.escape(c.id)}"] .pdf-paper`);if(!paper)return;
  $('.highlight-layer',paper)?.remove();const page=Number(paper.dataset.page),size=thumbnailSizes.get(`${c.id}:${page}`);if(!size)return;
  const w=paper.clientWidth,h=paper.clientHeight,scale=Math.min(w/size.w,h/size.h),pw=size.w*scale,ph=size.h*scale;
  const layer=document.createElement('div');layer.className='highlight-layer';layer.setAttribute('aria-hidden','true');
  for(const r of cardReferences(c).filter(r=>r.anchor.page===page))for(const rect of r.anchor.rects)addRectHighlight(layer,{x:((w-pw)/2+rect.x*pw)/w,y:((h-ph)/2+rect.y*ph)/h,w:rect.w*pw/w,h:rect.h*ph/h},r.key);
  if(linkFrom===c.id&&linkAnchor?.page===page)for(const rect of linkAnchor.rects)addRectHighlight(layer,{x:((w-pw)/2+rect.x*pw)/w,y:((h-ph)/2+rect.y*ph)/h,w:rect.w*pw/w,h:rect.h*ph/h},'pending','focused-reference');
  paper.append(layer);
}
function paintBoardHighlights() {
  for(const c of board.cards){
    if(c.kind==='pdf'){paintPdfThumbnail(c);continue;}
    const root=cardLayer.querySelector(`[data-id="${CSS.escape(c.id)}"] [data-annotatable]`);if(!root)continue;
    const refs=cardReferences(c);if(linkFrom===c.id&&linkAnchor)refs.push({anchor:linkAnchor,key:'pending'});
    paintTextRoot(root,refs,selected?.type==='edge'?`${selected.id}:from`:null);
    for(const ref of refs){const chip=cardLayer.querySelector(`.reference-chip[data-reference-key="${CSS.escape(ref.key)}"]`);if(chip){const unresolved=!resolveTextQuote(rootText(root),ref.anchor);chip.classList.toggle('unresolved',unresolved);if(unresolved)chip.title='本文が変更され，引用箇所を特定できません．引用を再確認してください．';}}
  }
}
function referenceWorldPoint(key) {
  const el=cardLayer.querySelector(`.reference-highlight[data-reference-key="${CSS.escape(key)}"]`)||cardLayer.querySelector(`.reference-chip[data-reference-key="${CSS.escape(key)}"]`);
  if(!el)return null;const r=el.getBoundingClientRect(),v=viewport.getBoundingClientRect();
  return {x:(r.left+r.width/2-v.left-view.x)/view.zoom,y:(r.top+r.height/2-v.top-view.y)/view.zoom};
}
function showQuoteSelection(quote,box) {
  pendingQuote=quote;const popup=$('#reference-selection');($('#viewer').open?$('#viewer'):document.body).append(popup);
  $('#selection-quote').textContent=quote.anchor.page?`p.${quote.anchor.page} · ${quote.anchor.exact}`:`「${quote.anchor.exact}」`;
  popup.hidden=false;const width=popup.offsetWidth;
  const bounds=$('#viewer').open?$('#viewer').getBoundingClientRect():{left:0,right:window.innerWidth,top:0,bottom:window.innerHeight};
  popup.style.left=`${clamp(box.left+box.width/2-width/2,bounds.left+10,bounds.right-width-10)}px`;
  popup.style.top=`${clamp(box.bottom+9,bounds.top+10,bounds.bottom-popup.offsetHeight-12)}px`;
}
function captureTextSelection() {
  if(tool==='connect'||regionDrag)return;
  const sel=window.getSelection();if(!sel||sel.isCollapsed||!sel.rangeCount){if(!$('#reference-selection').matches(':hover'))$('#reference-selection').hidden=true;return;}
  const range=sel.getRangeAt(0),start=range.startContainer.nodeType===1?range.startContainer:range.startContainer.parentElement;
  const root=start.closest('[data-annotatable]');if(!root||!root.contains(range.endContainer))return;
  const c=board.cards.find(c=>c.id===root.dataset.sourceCard);if(!c)return;
  const prefix=document.createRange();prefix.selectNodeContents(root);prefix.setEnd(range.startContainer,range.startOffset);const begin=prefix.toString().length;
  const text=rootText(root),end=begin+range.toString().length;if(!text.slice(begin,end).trim()||end-begin>5000)return;
  let anchor=textQuote(text,begin,end);
  if(root.dataset.annotatable==='pdf'){
    const box=root.closest('.pdf-page').getBoundingClientRect(),rects=clippedRects(range,box);if(!rects.length)return;
    anchor={...anchor,type:'pdf-text',page:viewerPage,rects};
  }
  showQuoteSelection({cardId:c.id,anchor},range.getBoundingClientRect());
}
function cancelReference() {pendingQuote=null;linkAnchor=null;$('#reference-selection').hidden=true;$('#pending-reference').hidden=true;}
function beginQuotedLink() {
  if(!pendingQuote)return;const quote=pendingQuote;$('#viewer').close();setTool('connect');linkFrom=quote.cardId;linkAnchor=quote.anchor;selected=null;
  window.getSelection()?.removeAllRanges();$('#reference-selection').hidden=true;
  $('#pending-reference span').textContent=`「${quote.anchor.exact.slice(0,70)}」 → 参照先のカードを選択`;
  $('#pending-reference').hidden=false;$('#mode-hint').textContent='参照先をクリックすると，ハイライトから矢印でつながります．';renderCards();renderEdges();
}
$('#connect-selection').addEventListener('pointerdown',e=>e.preventDefault());$('#connect-selection').onclick=beginQuotedLink;
$('#cancel-selection').onclick=()=>{pendingQuote=null;$('#reference-selection').hidden=true;window.getSelection()?.removeAllRanges();};
$('#cancel-reference').onclick=()=>setTool('select');
document.addEventListener('pointerup',event=>{if(!event.target.closest('#pdf-region-layer,#reference-selection'))setTimeout(captureTextSelection,0);});
document.addEventListener('keyup',event=>{if(event.shiftKey)captureTextSelection();});
document.addEventListener('scroll',()=>$('#reference-selection').hidden=true,true);
function openReference(key) {
  const split=key.lastIndexOf(':'),edge=board.edges.find(e=>e.id===key.slice(0,split)),side=key.slice(split+1);if(!edge)return;
  const anchor=edge[`${side}Anchor`];if(anchor)void openViewer(edge[side],{...anchor,referenceKey:key});
}
function paintViewerHighlights(c,focus) {
  const refs=cardReferences(c),page=$('.pdf-page');
  if(page){
    $('.highlight-layer',page)?.remove();const layer=document.createElement('div');layer.className='highlight-layer';layer.setAttribute('aria-hidden','true');
    for(const r of refs.filter(r=>r.anchor.page===viewerPage))for(const rect of r.anchor.rects)addRectHighlight(layer,rect,r.key,r.key===focus?.referenceKey?'focused-reference':'');page.append(layer);
  }else{const root=$('#viewer-body [data-annotatable]');if(root)paintTextRoot(root,refs,focus?.referenceKey);}
  const target=focus?.referenceKey&&$('#viewer-body').querySelector(`.reference-highlight[data-reference-key="${CSS.escape(focus.referenceKey)}"]`);target?.scrollIntoView({block:'center'});
}
function cardReferences(c) {
  return board.edges.flatMap(e=>[['from',e.from,e.fromAnchor],['to',e.to,e.toAnchor]].filter(([,id,a])=>id===c.id&&a).map(([side,,anchor])=>({edge:e,anchor,key:`${e.id}:${side}`})));
}
function displayPdfPage(c) {
  const refs=cardReferences(c);
  return refs.find(r=>selected?.type==='edge'&&selected.id===r.edge.id)?.anchor.page||refs[0]?.anchor.page||c.page||1;
}
function referenceChips(c) {
  const refs=cardReferences(c);
  return refs.length?`<div class="reference-chips">${refs.map(r=>`<button class="reference-chip" data-reference-key="${esc(r.key)}" data-open-reference="${esc(r.key)}" title="引用箇所を開く：${esc(r.anchor.exact)}">${r.anchor.page?`p.${r.anchor.page} · `:''}「${esc(r.anchor.exact.slice(0,48))}」↗</button>`).join('')}</div>`:'';
}
function waveform(count = 43) { return Array.from({length:count}, (_,i) => `<i style="height:${8 + Math.abs(Math.sin(i * 2.7) * Math.cos(i * .37)) * 29}px"></i>`).join(''); }
function cardContent(c) {
  if (c.kind === 'image') return `<div class="card-image ${c.id === 'plan' ? 'plan' : ''}"><img src="${esc(sourceURL(c))}" alt="${esc(c.title)}" draggable="false"></div><h3>${esc(c.title)}</h3><p class="card-description">${esc(c.body)}</p>`;
  if (c.kind === 'pdf') {const page=displayPdfPage(c),thumb=thumbnails.get(`${c.id}:${page}`);return `<div class="pdf-paper" data-page="${page}">${thumb ? `<img class="pdf-thumb" src="${thumb}" alt="PDF ${page}ページのプレビュー">` : '<div class="paper-fallback"><small>FIELD RESEARCH / REPORT 03</small>Building<br>survey<i></i></div>'}<span class="page-stamp">${page} / ${c.pages || '…'}</span></div><h3>${esc(c.title)}</h3>${referenceChips(c)||`<p class="card-description">${esc(c.body)}</p>`}`;}
  if (c.kind === 'markdown') return `<div class="card-md-content" data-annotatable="body" data-source-card="${esc(c.id)}">${markdownHTML(c.body)}</div>${referenceChips(c)}`;
  if (c.kind === 'audio') return `<h3>${esc(c.title)}</h3><div class="audio-line"><button class="play-sample" data-action="play" data-id="${esc(c.id)}" aria-label="${esc(c.title)}を再生">▶</button><div class="waveform" aria-label="装飾用の波形">${waveform(33)}</div><small>${formatTime(c.duration || 0)}</small></div><p class="audio-caption">${esc(c.body.split('\n')[0])}</p>`;
  return `<h3>${esc(c.title)}</h3><p class="card-description" data-annotatable="body" data-source-card="${esc(c.id)}">${esc(c.body)}</p>${referenceChips(c)}`;
}
function renderCards() {
  const matches = board.cards.filter(matchesFilter), ids = new Set(matches.map(c => c.id));
  cardLayer.innerHTML = board.cards.map((c,i) => `<article tabindex="0" role="button" aria-label="${esc(KINDS[c.kind] + '：' + c.title)}" aria-pressed="${selected?.type === 'card' && selected.id === c.id}" class="card card-${c.kind}${selected?.type === 'card' && selected.id === c.id ? ' selected' : ''}${linkFrom === c.id ? ' link-source' : ''}${!ids.has(c.id) ? ' dimmed' : ''}" data-id="${esc(c.id)}" style="left:${c.x}px;top:${c.y}px;width:${c.w}px;height:${c.h}px"><div class="card-header"><span class="card-kind"><b>${icons[c.kind]}</b> ${esc(c.kind === 'note' ? c.badge || 'THOUGHT' : KINDS[c.kind])}</span><span class="card-serial">${String(i+1).padStart(2,'0')}</span></div>${cardContent(c)}<div class="card-footer"><span>${esc(c.anchor || c.date || (c.kind === 'note' ? '自分の解釈 / 仮説' : c.filename || 'メモ'))}</span><span class="badge">${esc(c.badge || '資料')}</span></div></article>`).join('');
  board.cards.filter(c => c.kind === 'pdf').forEach(c => void ensureThumbnail(c));
  paintBoardHighlights();
}
function matchesFilter(c) { return (filter === 'all' || c.kind === filter) && (!query || `${c.title} ${c.body} ${c.filename || ''}`.toLowerCase().includes(query.toLowerCase())); }
function renderLibrary() {
  const materials = board.cards.filter(c => !['note','excerpt'].includes(c.kind));
  $('#material-count').textContent = String(materials.length);
  $('#material-list').innerHTML = materials.filter(matchesFilter).map(c => `<button class="material-item${selected?.type === 'card' && selected.id === c.id ? ' active' : ''}" data-material="${esc(c.id)}" title="${esc(c.title)}：クリックで選択，ダブルクリックで開く"><span class="material-thumb ${c.kind}">${c.kind === 'image' ? `<img alt="" src="${esc(sourceURL(c))}">` : c.kind === 'pdf' ? 'PDF' : icons[c.kind]}</span><span class="material-info"><strong>${esc(c.title)}</strong><small>${KINDS[c.kind]} · ${esc(c.kind === 'audio' ? formatTime(c.duration || 0) : c.kind === 'pdf' ? `${c.pages || '…'} pages` : c.date || 'この端末')}</small></span><span class="material-arrow">↗</span></button>`).join('') || '<p class="empty-material">該当する資料がありません．<br>ファイルをドロップして追加できます．</p>';
}
function renderEdges() {
  const lookup = new Map(board.cards.map(c => [c.id,c]));
  const defs=`<defs>${Object.entries(RELATIONS).map(([key,r]) => `<marker id="arrow-${key}" viewBox="0 0 9 9" refX="7" refY="4.5" markerWidth="5" markerHeight="5" orient="auto"><path d="M0 0L8 4.5L0 9" fill="none" stroke="${r.color}" stroke-width="1.5"/></marker>`).join('')}</defs>`;
  const regular=[],anchored=[];
  for (const e of board.edges) {
    const a = lookup.get(e.from), b = lookup.get(e.to); if (!a || !b) continue;
    const p = connectionPoints(a,b), r = RELATIONS[e.relation], active = selected?.type === 'edge' && selected.id === e.id;
    const from=referenceWorldPoint(`${e.id}:from`),to=referenceWorldPoint(`${e.id}:to`);
    if(from){p.x1=from.x;p.y1=from.y;}if(to){p.x2=to.x;p.y2=to.y;}
    const mx = (p.x1+p.x2)/2, my = (p.y1+p.y2)/2;
    const d = `M${p.x1} ${p.y1} Q${mx+8} ${my+13} ${p.x2} ${p.y2}`;
    const dimmed = selected?.type === 'card' && e.from !== selected.id && e.to !== selected.id;
    const html=`<g class="edge${active ? ' selected' : ''}${dimmed ? ' dimmed' : ''}" data-edge="${esc(e.id)}"><path class="thread" d="${d}" stroke="${r.color}" ${e.status === 'pending' ? 'stroke-dasharray="5 4"' : ''} marker-end="url(#arrow-${e.relation})"/><path class="edge-hit" tabindex="0" role="button" aria-label="${esc(a.title + 'から' + b.title + 'への関係：' + r.short)}" data-edge="${esc(e.id)}" d="${d}"/><circle cx="${p.x1}" cy="${p.y1}" r="3.5" stroke="${r.color}"/><text x="${mx+5}" y="${my-4}" text-anchor="middle">${r.short}${e.status === 'pending' ? ' ?' : ''}</text></g>`;
    (e.fromAnchor||e.toAnchor?anchored:regular).push(html);
  }
  edgeLayer.innerHTML=defs+`<g transform="translate(40000 40000)">${regular.join('')}</g>`;
  $('#anchor-edges').innerHTML=`<g transform="translate(40000 40000)">${anchored.join('')}</g>`;
  $('#relation-count').textContent = board.edges.length;
}
function applyViewport() {
  world.style.transform = `translate(${view.x}px,${view.y}px) scale(${view.zoom})`;
  $('#zoom-level').textContent = `${Math.round(view.zoom*100)}%`;
  renderEdges();
}
function fit() { view = fitViewport(board.cards, viewport.clientWidth, viewport.clientHeight); applyViewport(); }
function zoom(factor, x = viewport.clientWidth / 2, y = viewport.clientHeight / 2) {
  const next = clamp(view.zoom*factor,.2,1.8), ratio = next/view.zoom;
  view.x = x - (x-view.x)*ratio; view.y = y-(y-view.y)*ratio; view.zoom = next; applyViewport();
}
function render() {
  $('#board-title').value = board.title; $('#undo').disabled = history.length === 0;
  renderCards(); renderLibrary(); renderEdges(); renderInspector(); applyViewport();
  $('#groups').innerHTML = board.cards.some(c => c.id === 'report') ? '<span class="group-label" style="left:100px;top:62px">01 / 観察と記録</span><span class="group-label" style="left:520px;top:805px">02 / 空間の手がかり</span>' : '';
}
function select(type, id) {
  selected = id ? {type,id} : null;
  // Keep card and library DOM stable so a selection does not swallow double-clicks or focus．
  $$('.card',cardLayer).forEach(el => { const active=type==='card'&&el.dataset.id===id;el.classList.toggle('selected',active);el.setAttribute('aria-pressed',String(active)); });
  $$('.material-item').forEach(el=>el.classList.toggle('active',type==='card'&&el.dataset.material===id));
  if(type==='edge'&&board.cards.some(c=>c.kind==='pdf'&&Number(cardLayer.querySelector(`[data-id="${CSS.escape(c.id)}"] .pdf-paper`)?.dataset.page)!==displayPdfPage(c)))renderCards();
  renderEdges(); renderInspector();
}
function setTool(next) {
  tool = next; linkFrom = null;cancelReference(); viewport.dataset.tool = next;
  $$('[data-tool]').forEach(b => b.setAttribute('aria-pressed', String(b.dataset.tool === next)));
  $('#mode-hint').textContent = next === 'text' ? '本文をドラッグして選択 →「この箇所からつなぐ」→ 参照先を選択 · PDFはダブルクリックで開く' : next === 'connect' ? '接続元 → 接続先の順にカードを選択 · Escで解除' : next === 'pan' ? 'ドラッグでボードを移動 · ホイールで拡大・縮小' : 'ドラッグで配置 · 空白をドラッグで移動 · ダブルクリックで資料を開く';
  renderCards();
  renderEdges();
}
function handleConnect(id) {
  if (!linkFrom) { linkFrom = id; renderCards(); $('#mode-hint').textContent = '接続先のカードを選択してください · Escで解除'; return; }
  const edge = makeEdge(linkFrom,id,board.edges,uid('edge'),linkAnchor);
  if (!edge) { toast(linkFrom === id ? '別のカードを選んでください．' : 'この向きの関係はすでにあります．'); return; }
  if(linkAnchor)edge.reason=`${linkAnchor.page?`p.${linkAnchor.page}の`:''}「${linkAnchor.exact}」からの参照．`;
  checkpoint(); board.edges.push(edge); setTool('select'); select('edge',edge.id); scheduleSave(); toast(edge.fromAnchor?'引用箇所を保存し，ハイライトから矢印でつなぎました．':'関係を作成しました．種類と判断理由を記録できます．');
}
function addNote() {
  checkpoint(); const c = { id:uid('note'),kind:'note',title:'新しい仮説',body:'ここに気づきや問いを記録…',x:(viewport.clientWidth/2-view.x)/view.zoom-150,y:(viewport.clientHeight/2-view.y)/view.zoom-110,w:310,h:240,badge:'仮説',color:'yellow' };
  board.cards.push(c); setTool('select'); select('card',c.id); scheduleSave(); $('#card-title')?.focus();
}
function deleteSelection() {
  if (!selected) return; checkpoint();
  if (selected.type === 'card') {
    const c = board.cards.find(c => c.id === selected.id); audioPlayers.get(c?.id)?.pause();
    board.cards = board.cards.filter(c => c.id !== selected.id); board.edges = board.edges.filter(e => e.from !== selected.id && e.to !== selected.id);
  } else board.edges = board.edges.filter(e => e.id !== selected.id);
  selected = null; render(); scheduleSave(); toast('ボードから外しました．⌘Zで戻せます．');
}
function renderInspector(mode) {
  const el = $('#inspector');
  const selectedCard=selected?.type==='card'?board.cards.find(c=>c.id===selected.id):null;
  el.classList.toggle('inspector-left',Boolean(selectedCard&&(selectedCard.x+selectedCard.w/2)*view.zoom+view.x>viewport.clientWidth*.6));
  if (mode === 'relations') {
    el.classList.remove('inspector-left');
    const pending = board.edges.filter(e=>e.status==='pending').length;
    el.innerHTML = `<div class="inspector-heading"><span class="eyebrow">CONNECTIONS</span><button class="icon-button" data-inspector="close" aria-label="詳細を閉じる">×</button></div><h2>関係を，ひとつずつ確かめる．</h2><div class="inspector-summary"><div><strong>${board.edges.length}</strong><small>すべての関係</small></div><div><strong>${pending}</strong><small>未確認</small></div></div><p class="fineprint">破線は未確認の関係です．支持・矛盾は利用者の判断を記録したもので，資料の正しさを保証しません．</p>${board.edges.map(e => `<button class="edge-row" data-inspector="edge" data-id="${esc(e.id)}"><span><i style="background:${RELATIONS[e.relation].color}"></i>${RELATIONS[e.relation].short} · ${e.status === 'reviewed' ? '確認済み' : '未確認'}</span><strong>${esc(cardTitle(e.from))} → ${esc(cardTitle(e.to))}</strong><small>${esc(e.reason || '判断理由を追加')}</small></button>`).join('')}`;
    el.hidden = false; return;
  }
  if (!selected) { el.hidden = true; return; }
  if (selected.type === 'edge') {
    const e = board.edges.find(e=>e.id===selected.id); if (!e) { el.hidden = true; return; }
    el.innerHTML = `<div class="inspector-heading"><span class="eyebrow">RELATIONSHIP</span><button class="icon-button" data-inspector="close" aria-label="詳細を閉じる">×</button></div><h2>この2つは，どうつながる？</h2><div class="relation-path">${esc(cardTitle(e.from))}<span>↓</span>${esc(cardTitle(e.to))}</div><label for="edge-relation">関係の種類</label><select id="edge-relation">${Object.entries(RELATIONS).map(([k,r])=>`<option value="${k}" ${e.relation===k?'selected':''}>${r.label}</option>`).join('')}</select><label for="edge-reason">判断理由・根拠</label><textarea id="edge-reason" rows="6" placeholder="どの記述・場面から，そう考えましたか？">${esc(e.reason)}</textarea><label class="status-check"><input id="edge-reviewed" type="checkbox" ${e.status==='reviewed'?'checked':''}>この関係を確認済みにする</label><p class="fineprint">線の向きは上の資料から下の資料へ．<br>未確認の関係は破線で表示されます．</p><button class="button" data-inspector="reverse">↑↓ 向きを入れ替える</button><button class="danger-button" data-inspector="delete">この関係を外す</button>`;
    $('.relation-path',el).insertAdjacentHTML('afterend',['from','to'].filter(side=>e[`${side}Anchor`]).map(side=>{const a=e[`${side}Anchor`];return `<div class="anchor-inspector"><span>${side==='from'?'始点':'終点'}の引用${a.page?` · p.${a.page}`:''}</span><blockquote>${esc(a.exact)}</blockquote><button class="text-button" data-inspector="open-reference" data-key="${esc(`${e.id}:${side}`)}">引用箇所を開く ↗</button></div>`;}).join(''));
  } else {
    const c = board.cards.find(c=>c.id===selected.id); if (!c) { el.hidden=true; return; }
    const edges = board.edges.filter(e=>e.from===c.id||e.to===c.id);
    el.innerHTML = `<div class="inspector-heading"><span class="eyebrow">${KINDS[c.kind]}</span><button class="icon-button" data-inspector="close" aria-label="詳細を閉じる">×</button></div><label for="card-title">タイトル</label><input id="card-title" value="${esc(c.title)}">${['note','excerpt'].includes(c.kind)?`<label for="card-body">${c.kind==='note'?'解釈・仮説':'抜粋・コメント'}</label><textarea id="card-body" rows="7">${esc(c.body)}</textarea>`:`<p class="fineprint">${esc(c.filename||'')}<br>${esc(c.kind==='pdf'?`${c.pages||'?'}ページ`:c.date||'取り込み済み')}<br>原資料の内容はボード上では変更しません．</p>`}${c.anchor?`<p class="fineprint">参照箇所：${esc(c.anchor)}</p>`:''}<button class="button dark" data-inspector="open">${['note','excerpt'].includes(c.kind)?'内容を開く':'原資料を開く'} ↗</button><button class="button" data-inspector="connect">⌁ このカードからつなぐ</button><label>つながっている資料 <span class="tiny-count">${edges.length}</span></label>${edges.map(e=>`<button class="related-item" data-inspector="edge" data-id="${esc(e.id)}">${esc(cardTitle(e.from===c.id?e.to:e.from))}<span>${e.from===c.id?'→':'←'} ${RELATIONS[e.relation].short} · ${e.status==='reviewed'?'確認済み':'未確認'}</span></button>`).join('')||'<p class="fineprint">まだ関係がありません．</p>'}<button class="danger-button" data-inspector="delete">このカードをボードから外す</button>`;
  }
  el.hidden=false;
}
function cardTitle(id) { return board.cards.find(c=>c.id===id)?.title || '参照元がボードにありません'; }

// Pointer coordinates always use the viewport transform, including while zoomed．
viewport.addEventListener('pointerdown', event => {
  if (event.button!==0 && event.button!==1) return;
  if (event.target.closest('button') || event.target.closest('[data-edge]')) return;
  const card = event.target.closest('.card');
  if(tool==='text'&&event.target.closest('[data-annotatable]')&&!spaceHeld)return;
  if (card && tool==='connect' && !spaceHeld) { handleConnect(card.dataset.id); return; }
  if (card && tool!=='pan' && !spaceHeld && event.button===0) {
    select('card',card.dataset.id); const c=board.cards.find(c=>c.id===card.dataset.id);
    drag={type:'card',id:c.id,startX:event.clientX,startY:event.clientY,x:c.x,y:c.y,moved:false};
  } else {
    drag={type:'pan',startX:event.clientX,startY:event.clientY,x:view.x,y:view.y,moved:false}; viewport.classList.add('panning');
  }
  if (drag.type==='pan') event.preventDefault();
});
viewport.addEventListener('pointermove', event => {
  if (!drag) return;
  const dx=event.clientX-drag.startX,dy=event.clientY-drag.startY;
  if (Math.hypot(dx,dy)<3 && !drag.moved) return;
  if (!drag.moved) { if(drag.type==='card') checkpoint(); viewport.setPointerCapture(event.pointerId); } drag.moved=true;
  if (drag.type==='pan') {view.x=drag.x+dx;view.y=drag.y+dy;applyViewport();}
  else {
    const c=board.cards.find(c=>c.id===drag.id); c.x=clamp(drag.x+dx/view.zoom,-30000,30000);c.y=clamp(drag.y+dy/view.zoom,-30000,30000);
    const el=cardLayer.querySelector(`[data-id="${CSS.escape(c.id)}"]`);el.style.left=`${c.x}px`;el.style.top=`${c.y}px`;renderEdges();
  }
});
function endDrag() { if (!drag) return; if(drag.type==='card'&&drag.moved) scheduleSave(); if(drag.type==='pan'&&!drag.moved&&tool!=='connect') select(null,null);drag=null;viewport.classList.remove('panning'); }
viewport.addEventListener('pointerup',endDrag);viewport.addEventListener('pointercancel',endDrag);
window.addEventListener('pointerup',endDrag);
viewport.addEventListener('wheel', event=>{event.preventDefault();const r=viewport.getBoundingClientRect();zoom(Math.exp(-event.deltaY*.0015),event.clientX-r.left,event.clientY-r.top);},{passive:false});
viewport.addEventListener('click',event=>{
  const ref=event.target.closest('[data-open-reference]');if(ref){openReference(ref.dataset.openReference);return;}
  const edge=event.target.closest('[data-edge]');if(edge) select('edge',edge.dataset.edge);
  const play=event.target.closest('[data-action=play]');if(play) playCard(play.dataset.id);
});
viewport.addEventListener('dblclick',event=>{const c=event.target.closest('.card');if(c&&tool!=='connect'&&!event.target.closest('button'))void openViewer(c.dataset.id);});
viewport.addEventListener('keydown',event=>{if(event.key==='Enter'){const el=event.target.closest('[data-edge],.card');if(el?.dataset.edge)select('edge',el.dataset.edge);else if(el?.dataset.id)void openViewer(el.dataset.id);}});
$('#material-list').addEventListener('click',event=>{const el=event.target.closest('[data-material]');if(!el)return;const c=board.cards.find(c=>c.id===el.dataset.material);select('card',c.id);view.x=viewport.clientWidth/2-(c.x+c.w/2)*view.zoom-100;view.y=viewport.clientHeight/2-(c.y+c.h/2)*view.zoom;applyViewport();});
$('#material-list').addEventListener('dblclick',event=>{const el=event.target.closest('[data-material]');if(el)void openViewer(el.dataset.material);});
$$('[data-tool]').forEach(b=>b.addEventListener('click',()=>setTool(b.dataset.tool)));
$$('[data-filter]').forEach(b=>b.addEventListener('click',()=>{filter=b.dataset.filter;$$('[data-filter]').forEach(x=>x.setAttribute('aria-pressed',String(x===b)));renderLibrary();renderCards();}));
$('#search').addEventListener('input',e=>{query=e.target.value;renderLibrary();renderCards();});
$('#add-note').onclick=addNote;$('#undo').onclick=undo;$('#fit').onclick=fit;$('#zoom-in').onclick=()=>zoom(1.15);$('#zoom-out').onclick=()=>zoom(1/1.15);
$('#relations-button').onclick=()=>renderInspector('relations');
$('#board-title').addEventListener('change',event=>{checkpoint();board.title=event.target.value.trim()||'無題のボード';scheduleSave();});
$('#collapse-library').onclick=()=>{$('#library').hidden=true;$('#expand-library').hidden=false;fit();};
$('#expand-library').onclick=()=>{$('#library').hidden=false;$('#expand-library').hidden=true;fit();};
$('#fullscreen').onclick=async()=>{try{if(document.fullscreenElement)await document.exitFullscreen();else await document.documentElement.requestFullscreen();}catch{toast('この環境では全画面表示を利用できません．',true);}};
$('#help-button').onclick=()=>$('#help-dialog').showModal();$('#close-help').onclick=()=>$('#help-dialog').close();
$('#inspector').addEventListener('click',event=>{
  const b=event.target.closest('[data-inspector]');if(!b)return;
  const action=b.dataset.inspector;
  if(action==='close'){select(null,null);return;}
  if(action==='edge'){select('edge',b.dataset.id);return;}
  if(action==='open-reference'){openReference(b.dataset.key);return;}
  if(action==='delete'){deleteSelection();return;}
  if(action==='open'){void openViewer(selected.id);return;}
  if(action==='connect'){const id=selected.id;setTool('connect');linkFrom=id;renderCards();$('#mode-hint').textContent='接続先のカードを選択してください · Escで解除';return;}
  if(action==='reverse'){
    const e=board.edges.find(e=>e.id===selected.id);
    if(!makeEdge(e.to,e.from,board.edges.filter(x=>x.id!==e.id),'check',e.toAnchor,e.fromAnchor)){toast('逆向きの関係がすでにあります．');return;}
    checkpoint();[e.from,e.to]=[e.to,e.from];[e.fromAnchor,e.toAnchor]=[e.toAnchor,e.fromAnchor];renderCards();renderEdges();renderInspector();scheduleSave();
  }
});
$('#inspector').addEventListener('focusin',event=>{if(event.target.matches('input,select,textarea'))checkpoint();});
$('#inspector').addEventListener('input',event=>{
  const el=event.target;if(!selected)return;
  if(selected.type==='edge'){
    const e=board.edges.find(e=>e.id===selected.id);
    if(el.id==='edge-relation')e.relation=el.value;else if(el.id==='edge-reason')e.reason=el.value;else if(el.id==='edge-reviewed')e.status=el.checked?'reviewed':'pending';else return;
    renderEdges();
  }else{
    const c=board.cards.find(c=>c.id===selected.id);if(el.id==='card-title')c.title=el.value;else if(el.id==='card-body')c.body=el.value;else return;
    renderCards();renderLibrary();renderEdges();
  }scheduleSave();
});
document.addEventListener('keydown',event=>{
  const editing=event.target.closest('input,textarea,select,[contenteditable]');
  if(editing||$('dialog[open]'))return;
  if((event.metaKey||event.ctrlKey)&&event.key.toLowerCase()==='z'){event.preventDefault();undo();return;}
  if(event.metaKey||event.ctrlKey||event.altKey)return;
  if(event.code==='Space'){spaceHeld=true;event.preventDefault();return;}
  const k=event.key.toLowerCase();
  if(k==='v')setTool('select');if(k==='h')setTool('pan');if(k==='t')setTool('text');if(k==='c')setTool('connect');if(k==='n')addNote();if(k==='f')fit();if(k==='/'){event.preventDefault();$('#search').focus();}
  if(k==='escape'){setTool('select');select(null,null);}
  if(['delete','backspace'].includes(k)&&selected){event.preventDefault();deleteSelection();}
  if(['ArrowLeft','ArrowRight','ArrowUp','ArrowDown'].includes(event.key)&&selected?.type==='card'){
    event.preventDefault();if(!keyboardMove){checkpoint();keyboardMove=true;}
    const c=board.cards.find(c=>c.id===selected.id),step=event.shiftKey?25:5;
    c.x=clamp(c.x+(event.key==='ArrowRight'?step:event.key==='ArrowLeft'?-step:0),-30000,30000);c.y=clamp(c.y+(event.key==='ArrowDown'?step:event.key==='ArrowUp'?-step:0),-30000,30000);renderCards();renderEdges();scheduleSave();
  }
});
document.addEventListener('keyup',event=>{if(event.code==='Space')spaceHeld=false;keyboardMove=false;});window.addEventListener('blur',()=>{spaceHeld=false;endDrag();});

async function pdfLibrary(){const pdf=await import('/vendor/pdfjs/build/pdf.mjs');pdf.GlobalWorkerOptions.workerSrc='/vendor/pdfjs/build/pdf.worker.mjs';return pdf;}
async function pdfDocument(c){
  if(!pdfDocs.has(c.id))pdfDocs.set(c.id,(async()=>{const pdf=await pdfLibrary();return pdf.getDocument({url:sourceURL(c),isEvalSupported:false,wasmUrl:'/vendor/pdfjs/wasm/',standardFontDataUrl:'/vendor/pdfjs/standard_fonts/',cMapUrl:'/vendor/pdfjs/cmaps/',cMapPacked:true}).promise;})());
  try{return await pdfDocs.get(c.id);}catch(error){pdfDocs.delete(c.id);throw error;}
}
async function ensureThumbnail(c){
  const pageNum=displayPdfPage(c),key=`${c.id}:${pageNum}`;
  if(thumbnails.has(key)||!sourceURL(c))return;
  thumbnails.set(key,'');
  try{
    const doc=await pdfDocument(c),page=await doc.getPage(pageNum),vp=page.getViewport({scale:.5});const canvas=document.createElement('canvas');canvas.width=vp.width;canvas.height=vp.height;
    await page.render({canvasContext:canvas.getContext('2d'),viewport:vp}).promise;thumbnails.set(key,canvas.toDataURL('image/jpeg',.8));thumbnailSizes.set(key,{w:vp.width,h:vp.height});
    const paper=cardLayer.querySelector(`[data-id="${CSS.escape(c.id)}"] .pdf-paper`);
    if(paper&&Number(paper.dataset.page)===pageNum){paper.innerHTML=`<img class="pdf-thumb" src="${thumbnails.get(key)}" alt="PDF ${pageNum}ページのプレビュー"><span class="page-stamp">${pageNum} / ${doc.numPages}</span>`;paintPdfThumbnail(c);renderEdges();}
  }catch{thumbnails.delete(key);}
}
function formatTime(seconds){const n=Math.max(0,Number(seconds)||0);return `${String(Math.floor(n/60)).padStart(2,'0')}:${String(Math.floor(n%60)).padStart(2,'0')}`;}
function playCard(id){
  const c=board.cards.find(c=>c.id===id);let a=audioPlayers.get(id);
  if(!a){a=new Audio(sourceURL(c));audioPlayers.set(id,a);a.addEventListener('ended',()=>{const b=cardLayer.querySelector(`[data-id="${CSS.escape(id)}"] .play-sample`);if(b)b.textContent='▶';});}
  if(!a.paused){a.pause();toast('再生を一時停止しました．');}else{for(const other of audioPlayers.values())if(other!==a)other.pause();a.play().catch(()=>toast('この形式の音声を再生できません．原資料をご確認ください．',true));}
  const b=cardLayer.querySelector(`[data-id="${CSS.escape(id)}"] .play-sample`);if(b)b.textContent=a.paused?'▶':'Ⅱ';
}
function stopMedia(){for(const a of audioPlayers.values())a.pause();$('#viewer-body audio')?.pause();}
async function openViewer(id, anchor){
  const c=board.cards.find(c=>c.id===id);if(!c)return;
  stopMedia();pendingQuote=null;$('#reference-selection').hidden=true;const session=++viewerSession;viewerCardId=id;viewerPage=anchor?.page||c.page||1;viewerFocus=anchor;pdfRegionMode=false;
  if(renderTask){renderTask.cancel();renderTask=null;}
  $('#viewer-kind').textContent=KINDS[c.kind];$('#viewer-title').textContent=c.title;$('#viewer-body').innerHTML='<p class="loading">資料を開いています…</p>';$('#viewer-footer').innerHTML='';if(!$('#viewer').open)$('#viewer').showModal();
  try{
    if(c.kind==='pdf'){await renderPdfViewer(c,session);return;}
    if(c.kind==='image')$('#viewer-body').innerHTML=`<img class="full-image" src="${esc(sourceURL(c))}" alt="${esc(c.title)}">`;
    else if(c.kind==='audio'){
      $('#viewer-body').innerHTML=`<div class="audio-document"><span class="eyebrow">LISTEN & FIND CONNECTIONS</span><h3>${esc(c.title)}</h3><div class="waveform" aria-label="装飾用の波形">${waveform(65)}</div><audio controls src="${esc(sourceURL(c))}"></audio><p>${esc(c.body)}</p></div>`;
      const audio=$('#viewer-body audio');if(anchor?.time)audio.addEventListener('loadedmetadata',()=>audio.currentTime=anchor.time,{once:true});
    }else $('#viewer-body').innerHTML=`<article class="markdown-document">${c.kind==='markdown'?`<div data-annotatable="body" data-source-card="${esc(c.id)}">${markdownHTML(c.body)}</div>`:`<h1>${esc(c.title)}</h1><p style="white-space:pre-line" data-annotatable="body" data-source-card="${esc(c.id)}">${esc(c.body)}</p>`}</article>`;
    if(session!==viewerSession)return;
    $('#viewer-footer').innerHTML=c.sourceId?`<small>参照箇所：${esc(c.anchor||'')}${board.cards.some(x=>x.id===c.sourceId)?'':' · 元資料はボードから外されています'}</small><button class="button dark" id="open-source" ${board.cards.some(x=>x.id===c.sourceId)?'':'disabled'}>元資料を開く ↗</button>`:c.kind==='note'?'<small>ボード右側の詳細欄で編集できます．</small>':`<label>抜粋・気づき<input id="excerpt-text" placeholder="残しておきたい内容を入力…"></label><button class="button dark" id="create-excerpt">${c.kind==='audio'?'この再生位置をカードに':'抜粋カードを作る'} ↗</button>`;
    $('#open-source')?.addEventListener('click',()=>void openViewer(c.sourceId,{page:c.page,time:c.time}));
    $('#create-excerpt')?.addEventListener('click',()=>createExcerpt(c));
    paintViewerHighlights(c,anchor);
  }catch(error){if(session===viewerSession)$('#viewer-body').innerHTML=`<p class="file-error">資料を開けませんでした．<br>${esc(error.message)}<br>ファイル形式や破損をご確認ください．</p>`;}
}
async function renderPdfViewer(c,session){
  const revision=++pdfRenderSequence;if(renderTask){renderTask.cancel();renderTask=null;}if(textLayerTask){textLayerTask.cancel();textLayerTask=null;}
  $('#reference-selection').hidden=true;
  $('#viewer-body').innerHTML='<p class="loading">PDFのページを読み込んでいます…</p>';
  const doc=await pdfDocument(c);if(session!==viewerSession||revision!==pdfRenderSequence)return;
  viewerPage=clamp(viewerPage,1,doc.numPages);const page=await doc.getPage(viewerPage);if(session!==viewerSession||revision!==pdfRenderSequence)return;
  const base=page.getViewport({scale:1}),scale=Math.min(1.15,($('#viewer-body').clientWidth-50)/base.width),vp=page.getViewport({scale}),dpr=Math.min(window.devicePixelRatio||1,2);
  const stage=document.createElement('div');stage.className='pdf-page';stage.style.cssText=`width:${vp.width}px;height:${vp.height}px;--total-scale-factor:${vp.scale*(vp.userUnit||1)};--scale-round-x:1px;--scale-round-y:1px;`;stage.dataset.page=viewerPage;
  const canvas=document.createElement('canvas');canvas.width=Math.ceil(vp.width*dpr);canvas.height=Math.ceil(vp.height*dpr);stage.append(canvas);
  const layer=document.createElement('div');layer.className='textLayer';layer.dataset.annotatable='pdf';layer.dataset.sourceCard=c.id;stage.append(layer);
  const region=document.createElement('div');region.id='pdf-region-layer';region.setAttribute('aria-label','PDF上の範囲選択');stage.append(region);$('#viewer-body').replaceChildren(stage);
  renderTask=page.render({canvasContext:canvas.getContext('2d'),viewport:vp,transform:dpr===1?undefined:[dpr,0,0,dpr,0,0]});await renderTask.promise;if(session!==viewerSession||revision!==pdfRenderSequence)return;renderTask=null;
  const text=await page.getTextContent();if(session!==viewerSession||revision!==pdfRenderSequence)return;
  const pdf=await pdfLibrary();textLayerTask=new pdf.TextLayer({textContentSource:text,container:layer,viewport:vp});await textLayerTask.render();if(session!==viewerSession||revision!==pdfRenderSequence)return;textLayerTask=null;
  const extracted=text.items.map(t=>t.str).join(' ');
  if(!extracted.trim())pdfRegionMode=true;
  $('#viewer-footer').innerHTML=`<button class="page-button" id="prev-page" aria-label="前のページ" ${viewerPage===1?'disabled':''}>‹</button><span id="page-label">${viewerPage} / ${doc.numPages}</span><button class="page-button" id="next-page" aria-label="次のページ" ${viewerPage===doc.numPages?'disabled':''}>›</button><div class="pdf-selection-tools"><button id="pdf-text-mode" ${!extracted.trim()?'disabled':''}>Ⅰ 文字を選択</button><button id="pdf-region-mode">▧ 範囲を囲む</button></div><small id="pdf-selection-hint"></small><label>抜粋<input id="excerpt-text" placeholder="選択した文字から参照を作成できます"></label><button class="button dark" id="create-excerpt">このページからカードを作る ↗</button>`;
  $('#prev-page').onclick=()=>{viewerPage--;void renderPdfViewer(c,session).catch(viewerError);};$('#next-page').onclick=()=>{viewerPage++;void renderPdfViewer(c,session).catch(viewerError);};$('#create-excerpt').onclick=()=>createExcerpt(c,extracted);
  const updateMode=()=>{stage.dataset.selectionMode=pdfRegionMode?'region':'text';$('#pdf-text-mode').setAttribute('aria-pressed',String(!pdfRegionMode));$('#pdf-region-mode').setAttribute('aria-pressed',String(pdfRegionMode));$('#pdf-selection-hint').textContent=pdfRegionMode?'図・画像も囲んで参照できます':'PDF上の文字をドラッグして選択';};
  $('#pdf-text-mode').onclick=()=>{pdfRegionMode=false;updateMode();};$('#pdf-region-mode').onclick=()=>{pdfRegionMode=true;window.getSelection()?.removeAllRanges();updateMode();};updateMode();
  region.addEventListener('pointerdown',event=>{if(event.button!==0)return;event.preventDefault();window.getSelection()?.removeAllRanges();$('#reference-selection').hidden=true;const b=stage.getBoundingClientRect();regionDrag={x:clamp((event.clientX-b.left)/b.width,0,1),y:clamp((event.clientY-b.top)/b.height,0,1)};region.setPointerCapture(event.pointerId);region.innerHTML='<div class="region-draft"></div>';});
  region.addEventListener('pointermove',event=>{if(!regionDrag)return;const b=stage.getBoundingClientRect(),x=clamp((event.clientX-b.left)/b.width,0,1),y=clamp((event.clientY-b.top)/b.height,0,1);const r={x:Math.min(x,regionDrag.x),y:Math.min(y,regionDrag.y),w:Math.abs(x-regionDrag.x),h:Math.abs(y-regionDrag.y)};regionDrag.rect=r;$('.region-draft',region).style.cssText=`left:${r.x*100}%;top:${r.y*100}%;width:${r.w*100}%;height:${r.h*100}%;`;});
  region.addEventListener('pointerup',()=>{const r=regionDrag?.rect;regionDrag=null;if(!r||r.w<.005||r.h<.005){region.innerHTML='';return;}const b=stage.getBoundingClientRect();showQuoteSelection({cardId:c.id,anchor:{type:'pdf-region',page:viewerPage,exact:'図・画像の選択範囲',rects:[r]}},{left:b.left+r.x*b.width,top:b.top+r.y*b.height,width:r.w*b.width,bottom:b.top+(r.y+r.h)*b.height});});
  region.addEventListener('pointercancel',()=>{regionDrag=null;region.innerHTML='';});paintViewerHighlights(c,viewerFocus);
}
function viewerError(e){if(e.name!=='RenderingCancelledException'){toast('PDFを表示できませんでした．',true);}}
function createExcerpt(c,extracted=''){
  const text=$('#excerpt-text')?.value.trim()||window.getSelection()?.toString().trim()||extracted.slice(0,220)||c.body;
  const time=c.kind==='audio'?$('#viewer-body audio')?.currentTime||0:undefined;
  const anchor=c.kind==='pdf'?`p.${viewerPage}`:c.kind==='audio'?formatTime(time):'本文からの抜粋';
  checkpoint();const excerpt={id:uid('excerpt'),kind:'excerpt',title:c.kind==='audio'?`${c.title} / ${anchor}`:`${c.title}からの抜粋`,body:text||'ここに気づきを記録…',sourceId:c.id,anchor,page:c.kind==='pdf'?viewerPage:undefined,time,x:c.x+c.w+70,y:c.y+70,w:270,h:230,badge:'抜粋'};
  board.cards.push(excerpt);board.edges.push({id:uid('edge'),from:c.id,to:excerpt.id,relation:'related_to',reason:`${anchor}の参照箇所から作成．内容の解釈は別途確認する．`,status:'pending'});
  $('#viewer').close();select('card',excerpt.id);render();scheduleSave();fit();toast(`${anchor}への参照を持つ抜粋カードを作成しました．`);
}
$('#close-viewer').onclick=()=>$('#viewer').close();
$('#viewer').addEventListener('close',()=>{viewerSession++;pdfRenderSequence++;pendingQuote=null;regionDrag=null;$('#reference-selection').hidden=true;stopMedia();if(renderTask){renderTask.cancel();renderTask=null;}if(textLayerTask){textLayerTask.cancel();textLayerTask=null;}});

function readDataURL(file){return new Promise((resolve,reject)=>{const reader=new FileReader();reader.onload=()=>resolve(reader.result);reader.onerror=()=>reject(reader.error);reader.readAsDataURL(file);});}
function normalizeDataURL(data,kind,name){
  if(kind==='pdf')return data.replace(/^data:[^;]*;/,'data:application/pdf;');
  if(kind==='audio'&&!/^data:audio\//.test(data)){const m={mp3:'mpeg',m4a:'mp4',wav:'wav',ogg:'ogg',aac:'aac',flac:'flac'};return data.replace(/^data:[^;]*;/,`data:audio/${m[name.split('.').pop().toLowerCase()]||'mpeg'};`);}
  return data;
}
async function importFiles(files,point){
  if(importing){toast('資料の取り込みが終わるまでお待ちください．');return;}
  const list=[...files];if(!list.length)return;
  if(board.cards.length+list.length>250){toast('PoCではカード250枚まで追加できます．',true);return;}
  let bytes=board.cards.reduce((sum,c)=>sum+(c.attachment?.length||0)*.75+(c.kind==='markdown'?new TextEncoder().encode(c.body).length:0),0);
  const candidates=[];
  for(const file of list){const kind=classifyFile(file.name);if(!kind){toast(`${file.name}：対応していない形式です．`,true);continue;}if(file.size>20*1024*1024||bytes+file.size>80*1024*1024){toast('1ファイル20MB，合計80MBまで取り込めます．',true);continue;}bytes+=file.size;candidates.push({file,kind});}
  if(!candidates.length)return;
  importing=true;$('#import-files').disabled=true;$('#open-board').disabled=true;$('#reset-sample').disabled=true;
  checkpoint();let added=0;const p=point||{x:(viewport.clientWidth/2-view.x)/view.zoom,y:(viewport.clientHeight/2-view.y)/view.zoom};
  for(const {file,kind}of candidates){
    try{
      const c={id:uid('material'),kind,title:file.name.replace(/\.[^.]+$/,''),filename:file.name,body:'',x:clamp(p.x+added*45,-30000,30000),y:clamp(p.y+added*45,-30000,30000),w:kind==='audio'?310:280,h:kind==='audio'?220:330,date:new Date().toLocaleDateString('ja-JP'),badge:'未確認'};
      if(kind==='markdown'){if(file.size>1024*1024)throw new Error('Markdownは1MBまでです．');c.body=await file.text();}
      else{c.attachment=normalizeDataURL(await readDataURL(file),kind,file.name);}
      if(kind==='pdf'){
        const doc=await pdfDocument(c);c.pages=doc.numPages;c.page=1;const page=await doc.getPage(1),text=await page.getTextContent();c.body=text.items.map(t=>t.str).join(' ').slice(0,200);
      }
      if(kind==='audio'){
        c.body='再生位置を指定して，根拠となる箇所をカードに残せます．';
        c.duration=await new Promise(resolve=>{const a=new Audio(c.attachment);const timer=setTimeout(()=>{a.removeAttribute('src');resolve(0);},3000);a.onloadedmetadata=()=>{clearTimeout(timer);resolve(Number.isFinite(a.duration)?a.duration:0);};a.onerror=()=>{clearTimeout(timer);resolve(0);};});
      }
      board.cards.push(c);added++;
    }catch(error){toast(`${file.name}を取り込めませんでした．${error.message}`,true);}
  }
  importing=false;$('#import-files').disabled=false;$('#open-board').disabled=false;$('#reset-sample').disabled=false;
  render();scheduleSave();if(added)toast(`${added}件の資料をボードに追加しました．`);
}
$('#import-files').onclick=()=>$('#file-input').click();$('#file-input').onchange=async event=>{await importFiles(event.target.files);event.target.value='';};
let dragDepth=0;
viewport.addEventListener('dragenter',event=>{if(!event.dataTransfer.types.includes('Files'))return;event.preventDefault();dragDepth++;viewport.classList.add('dropping');});
viewport.addEventListener('dragover',event=>{event.preventDefault();event.dataTransfer.dropEffect='copy';});
viewport.addEventListener('dragleave',()=>{dragDepth=Math.max(0,dragDepth-1);if(!dragDepth)viewport.classList.remove('dropping');});
viewport.addEventListener('drop',event=>{event.preventDefault();dragDepth=0;viewport.classList.remove('dropping');const r=viewport.getBoundingClientRect();void importFiles(event.dataTransfer.files,{x:(event.clientX-r.left-view.x)/view.zoom,y:(event.clientY-r.top-view.y)/view.zoom});});
window.addEventListener('dragover',event=>event.preventDefault());window.addEventListener('drop',event=>event.preventDefault());
$('#export-board').onclick=async()=>{
  const button=$('#export-board');button.disabled=true;button.textContent='書き出し中…';
  try{
    const exported=snapshot();for(const c of exported.cards){if(c.asset&&!c.attachment&&c.kind!=='markdown'){const response=await fetch(c.asset);if(!response.ok)throw new Error('資料を読み込めません．');c.attachment=await readDataURL(await response.blob());delete c.asset;}}
    const blob=new Blob([JSON.stringify(exported,null,2)],{type:'application/json'}),url=URL.createObjectURL(blob),a=document.createElement('a');a.href=url;a.download=`${board.title.replace(/[\\/:*?"<>|]/g,'_')}.wall.json`;a.click();setTimeout(()=>URL.revokeObjectURL(url),1000);toast('資料本体を含めて書き出しました．JSONを開くと復元できます．');
  }catch(error){toast(error.message,true);}finally{button.disabled=false;button.textContent='↓ ボードを書き出す';}
};
$('#open-board').onclick=()=>$('#board-input').click();
$('#board-input').onchange=async event=>{
  const file=event.target.files[0];if(!file)return;
  try{if(file.size>115*1024*1024)throw new Error('JSONは115MBまで読み込めます．');const data=validateBoard(JSON.parse(await file.text()));checkpoint();stopMedia();board=data;selected=null;linkFrom=null;thumbnails.clear();pdfDocs.clear();render();scheduleSave();fit();toast('ボードを復元しました．前のボードは⌘Zで戻せます．');}
  catch(error){toast(`ボードを開けません．${error.message}`,true);}finally{event.target.value='';}
};
$('#reset-sample').onclick=()=>{checkpoint();stopMedia();board=createSample();selected=null;setTool('select');thumbnails.clear();pdfDocs.clear();render();scheduleSave();fit();toast('サンプルを復元しました．⌘Zで前の状態に戻せます．');};
let resizeTimer;new ResizeObserver(()=>{clearTimeout(resizeTimer);resizeTimer=setTimeout(()=>applyViewport(),70);}).observe(viewport);
if(window.innerWidth<800){$('#library').hidden=true;$('#expand-library').hidden=false;}
render();fit();setTool('select');if(!saved)scheduleSave();
