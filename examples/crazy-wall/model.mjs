export const RELATIONS = {
  supports: { label: '支持する', short: '支持', color: '#507e6a' },
  contradicts: { label: '矛盾する', short: '矛盾', color: '#b35447' },
  related_to: { label: '関連する', short: '関連', color: '#7862a6' },
  questions: { label: '要確認', short: '要確認', color: '#b78b3b' },
};
export const KINDS = { pdf: 'PDF', markdown: 'MARKDOWN', image: 'IMAGE', audio: 'AUDIO', note: 'THOUGHT', excerpt: 'EXCERPT' };
export const clamp = (v, min, max) => Math.max(min, Math.min(max, v));
export function pdfPreviewLayout(width,height,pageWidth,pageHeight,mode='detail',focus) {
  const scale=(mode==='page'?Math.min:Math.max)(width/pageWidth,height/pageHeight),w=pageWidth*scale,h=pageHeight*scale;
  if(mode==='page')return {x:(width-w)/2,y:(height-h)/2,w,h};
  return {x:clamp(width/2-(focus?.x??.5)*w,Math.min(0,width-w),0),y:focus?clamp(height/2-focus.y*h,Math.min(0,height-h),0):0,w,h};
}
export function projectPdfRect(rect,layout,width,height) {
  const x=Math.max(0,layout.x+rect.x*layout.w),y=Math.max(0,layout.y+rect.y*layout.h);
  const right=Math.min(width,layout.x+(rect.x+rect.w)*layout.w),bottom=Math.min(height,layout.y+(rect.y+rect.h)*layout.h);
  return right>x&&bottom>y?{x:x/width,y:y/height,w:(right-x)/width,h:(bottom-y)/height}:null;
}
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
    if (!c || typeof c.id !== 'string' || !c.id || c.id.length>200 || /[\r\n]/.test(c.id) || ids.has(c.id) || !Object.hasOwn(KINDS, c.kind) || typeof c.title !== 'string' || typeof c.body !== 'string' || !['x','y','w','h'].every(k => Number.isFinite(c[k])) || c.w < 100 || c.w > 1200 || c.h < 80 || c.h > 1800 || Math.abs(c.x) > 30000 || Math.abs(c.y) > 30000) throw new Error('カードの形式が正しくありません．');
    if (c.source && (typeof c.source!=='string'||! /^(content|data\/(image|audio|csv))\//.test(c.source)||c.source.split('/').some(p=>p==='..'||p==='.')||c.source.includes('\\'))) throw new Error('不正な管理ファイルの参照です．');
    if (c.resourceURL && !/^\/(pdf|image|audio)\//.test(c.resourceURL) && !/^data:(image\/(png|jpeg|webp|gif)|application\/pdf|audio\/[a-z\d.+-]+);base64,/i.test(c.resourceURL)) throw new Error('不正な管理資料URLです．');
    if (c.asset && !/^\/assets\/[a-z\d._-]+$/i.test(c.asset)) throw new Error('不正な資料パスです．');
    if(c.pdfPreview!==undefined&&!['page','detail'].includes(c.pdfPreview))throw new Error('PDFの表示形式が正しくありません．');
    if (c.attachment && (!/^data:(image\/(png|jpeg|webp|gif|svg\+xml)|application\/pdf|audio\/[a-z\d.+-]+);base64,/i.test(c.attachment) || c.attachment.length > 30 * 1024 * 1024)) throw new Error('資料データの形式が正しくありません．');
    ids.add(c.id);
  }
  const edgeIds = new Set();
  for (const e of data.edges) {
    if (!e || typeof e.id !== 'string' || !e.id || e.id.length>200 || /[\r\n]/.test(e.id) || edgeIds.has(e.id) || !ids.has(e.from) || !ids.has(e.to) || e.from === e.to || !Object.hasOwn(RELATIONS, e.relation) || typeof e.reason !== 'string' || !['pending','reviewed'].includes(e.status)) throw new Error('関係の形式が正しくありません．');
    for (const [side,id] of [['fromAnchor',e.from],['toAnchor',e.to]]) {
      if (e[side] !== undefined && !validAnchor(e[side], data.cards.find(c=>c.id===id))) throw new Error('引用箇所の形式が正しくありません．');
    }
    edgeIds.add(e.id);
  }
  return data;
}
export function makeEdge(from, to, edges, id, fromAnchor, toAnchor) {
  if (!from || !to || from === to) return null;
  if (edges.some(e => e.from === from && e.to === to && anchorIdentity(e.fromAnchor)===anchorIdentity(fromAnchor) && anchorIdentity(e.toAnchor)===anchorIdentity(toAnchor))) return null;
  return { id, from, to, relation: 'related_to', reason: '', status: 'pending', ...(fromAnchor?{fromAnchor}:{}), ...(toAnchor?{toAnchor}:{}) };
}
export function anchorIdentity(a) {
  if (!a) return '';
  return JSON.stringify([a.type,a.page,a.start,a.end,a.exact,a.type==='pdf-region'?a.rects:undefined,a.revision]);
}
export function textQuote(text, start, end) {
  return {type:'text',start,end,exact:text.slice(start,end),prefix:text.slice(Math.max(0,start-40),start),suffix:text.slice(end,end+40)};
}
export function resolveTextQuote(text,a) {
  if (!a?.exact) return null;
  const matches=[];
  for (let start=text.indexOf(a.exact);start!==-1;start=text.indexOf(a.exact,start+1)) {
    const end=start+a.exact.length;
    const score=Number(Boolean(a.prefix)&&text.slice(Math.max(0,start-a.prefix.length),start)===a.prefix)+Number(Boolean(a.suffix)&&text.slice(end,end+a.suffix.length)===a.suffix);
    matches.push({start,end,score});
    if(matches.length>5000)return null;
  }
  if(matches.length===1)return {start:matches[0].start,end:matches[0].end};
  const maxScore=matches.reduce((max,m)=>Math.max(max,m.score),0),best=matches.filter(m=>m.score===maxScore);
  return best.length===1&&best[0].score>0?{start:best[0].start,end:best[0].end}:null;
}
export function validAnchor(a,card) {
  if (!a||!card||!['text','pdf-text','pdf-region'].includes(a.type)||typeof a.exact!=='string'||!a.exact.trim()||a.exact.length>5000) return false;
  if(a.type==='text'&&!['markdown','note','excerpt'].includes(card.kind))return false;
  if(a.type!=='pdf-region'&&(!Number.isInteger(a.start)||!Number.isInteger(a.end)||a.start<0||a.end<=a.start||a.end-a.start!==a.exact.length||typeof a.prefix!=='string'||typeof a.suffix!=='string'||a.prefix.length>40||a.suffix.length>40))return false;
  if(a.type.startsWith('pdf-')) {
    if(card.kind!=='pdf'||!Number.isInteger(a.page)||a.page<1||a.page>(card.pages||10000)||!Array.isArray(a.rects)||!a.rects.length||a.rects.length>150)return false;
    return a.rects.every(r=>['x','y','w','h'].every(k=>Number.isFinite(r[k]))&&r.x>=0&&r.y>=0&&r.w>0&&r.h>0&&r.x+r.w<=1.001&&r.y+r.h<=1.001);
  }
  return true;
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

// Portable board projection．No filesystem，Wails or browser APIs are used here．
const WALL_CARD_FIELDS=['kind','page','pages','duration','sourceId','anchor','time','badge','date','pdfPreview'];
export function fromKarteBoard(doc, resources=[]) {
  const byPath=new Map(resources.map(r=>[r.path,r]));
  return {schema:'karte-research-wall/1',title:doc.title,managedPath:doc.path,viewport:doc.layout?.viewport,
    cards:(doc.cards||[]).map((c,i)=>{
      const saved=c.meta?.researchWall||{},r=byPath.get(c.source),kind=r?.kind||saved.kind||classifyFile(c.source||'')||(c.type==='excerpt'?'excerpt':'note'),l=doc.layout?.cards?.[c.id];
      return {id:c.id,kind,title:c.title,body:c.body||'',...Object.fromEntries(WALL_CARD_FIELDS.filter(k=>saved[k]!==undefined).map(k=>[k,saved[k]])),
        ...(c.source?{source:c.source,filename:c.source.split('/').pop()}:{}),x:l?.x??i*320,y:l?.y??100,w:l?.width||280,h:l?.height||330,
        ...(r?resourcePatch(r):c.source?{sourceError:'原資料を読み込めません．再読込または管理先を確認してください．'}:{})};
    }),
    edges:(doc.edges||[]).map(e=>({id:e.id,from:e.from,to:e.to,relation:Object.hasOwn(RELATIONS,e.relation)?e.relation:'related_to',reason:e.description||e.label||'',status:e.meta?.researchWall?.status||'pending',...e.meta?.researchWall,
      ...(Object.hasOwn(RELATIONS,e.relation)?{}:{nativeRelation:e.relation})}))};
}
export function anchorIsCurrent(anchor,card){return !card.sourceError&&(!anchor.revision||!card.revision||anchor.revision===card.revision);}
export function resourcePatch(r) {
  return {kind:r.kind,body:['markdown','audio'].includes(r.kind)?(r.body||'').replace(/^---\r?\n[\s\S]*?\r?\n---(?:\r?\n|$)/,''):r.body||'',source:r.path,resourceURL:r.url||'',revision:r.revision,editPath:r.editPath,sourceError:undefined};
}
export function toKarteBoard(wall,original) {
  validateBoard(wall);
  const cards=new Map((original.cards||[]).map(c=>[c.id,c])),edges=new Map((original.edges||[]).map(e=>[e.id,e]));
  return {...original,title:wall.title,
    cards:wall.cards.map(c=>{const prev=cards.get(c.id);return {...prev,id:c.id,type:prev?.type||(c.source?'resource':c.kind==='excerpt'?'excerpt':'claim'),title:c.title,source:c.source||'',body:c.body,
      meta:{...prev?.meta,researchWall:Object.fromEntries(WALL_CARD_FIELDS.filter(k=>c[k]!==undefined).map(k=>[k,c[k]]))}};}),
    edges:wall.edges.map(e=>{const prev=edges.get(e.id);return {...prev,id:e.id,from:e.from,to:e.to,relation:e.nativeRelation&&e.relation==='related_to'?e.nativeRelation:e.relation,description:e.reason,
      meta:{...prev?.meta,researchWall:{status:e.status,...(e.fromAnchor?{fromAnchor:e.fromAnchor}:{}),...(e.toAnchor?{toAnchor:e.toAnchor}:{})}}};}),
    layout:{cards:Object.fromEntries(wall.cards.map(c=>[c.id,{x:c.x,y:c.y,width:c.w,height:c.h}])),viewport:wall.viewport||original.layout?.viewport||{x:0,y:0,zoom:1}}};
}
