import { test } from 'node:test';
import assert from 'node:assert/strict';
import { validateBoard, makeEdge, connectionPoints, fitViewport, markdownHTML, classifyFile, textQuote, resolveTextQuote, pdfPreviewLayout, projectPdfRect } from '../model.mjs';
import { createSample } from '../sample.mjs';

test('portable board round trip preserves source anchors and judgments', () => {
  const board=createSample();board.cards.push({id:'quote2',kind:'excerpt',title:'page 2',body:'access',x:20,y:30,w:270,h:230,sourceId:'report',page:2,anchor:'p.2'});
  const restored=validateBoard(JSON.parse(JSON.stringify(board)));
  assert.equal(restored.cards.at(-1).page,2);assert.equal(restored.cards.at(-1).sourceId,'report');assert.equal(restored.edges[0].relation,'contradicts');assert.equal(restored.edges[0].status,'pending');
});
test('rejects broken references, duplicate IDs, invalid geometry, and remote assets', () => {
  for(const mutate of [b=>b.edges[0].from='missing',b=>b.cards[1].id=b.cards[0].id,b=>b.cards[0].x=NaN,b=>b.cards[0].asset='https://example.com/track.pdf',b=>b.edges[0].relation='__proto__',b=>b.cards[0].kind='__proto__',b=>b.cards[0].attachment='data:text/html;base64,PHNjcmlwdD4=']){
    const board=createSample();mutate(board);assert.throws(()=>validateBoard(board));
  }
});
test('connections are directed, avoid duplicates, and never connect to themselves', () => {
  const edge=makeEdge('a','b',[],'e1');assert.equal(edge.status,'pending');assert.equal(edge.reason,'');
  assert.equal(makeEdge('a','b',[edge],'e2'),null);assert.equal(makeEdge('a','a',[],'e3'),null);assert.ok(makeEdge('b','a',[edge],'e4'));
});
test('threads terminate at the card boundary, including vertical and diagonal layouts', () => {
  assert.deepEqual(connectionPoints({x:0,y:0,w:200,h:100},{x:400,y:0,w:200,h:100}),{x1:200,y1:50,x2:400,y2:50});
  const vertical=connectionPoints({x:0,y:0,w:100,h:100},{x:0,y:300,w:100,h:100});assert.equal(vertical.y1,100);assert.equal(vertical.y2,300);assert.equal(vertical.x1,50);
  const coincident=connectionPoints({x:0,y:0,w:100,h:100},{x:0,y:0,w:100,h:100});assert.ok(Object.values(coincident).every(Number.isFinite));
  const p=connectionPoints({x:0,y:0,w:100,h:100},{x:200,y:200,w:100,h:100});assert.equal(p.x1,100);assert.equal(p.y1,100);assert.equal(p.x2,200);assert.equal(p.y2,200);
});
test('fit leaves room for the palette and footer at desktop dimensions',()=>{
  const cards=createSample().cards,view=fitViewport(cards,1200,900);
  for(const c of cards){assert.ok(c.x*view.zoom+view.x>=0);assert.ok(c.y*view.zoom+view.y>=130);assert.ok((c.y+c.h)*view.zoom+view.y<=850);}
});
test('Markdown does not interpret embedded HTML or executable links',()=>{
  const html=markdownHTML('# Title\n<script>alert(1)</script>\n[x](javascript:alert(1))\n**bold**');
  assert.ok(html.includes('&lt;script&gt;'));assert.ok(!html.includes('<script>'));assert.ok(!html.includes('<a'));assert.ok(html.includes('<strong>bold</strong>'));
});
test('file types are case insensitive and bounded to supported media',()=>{
  assert.equal(classifyFile('scan.PDF'),'pdf');assert.equal(classifyFile('memo.md'),'markdown');assert.equal(classifyFile('voice.m4a'),'audio');assert.equal(classifyFile('photo.webp'),'image');assert.equal(classifyFile('index.html'),null);
});
test('quotes keep exact Japanese text and recover after an insertion before the reference',()=>{
  const text='補足：現地スケッチを参照のこと．',start=text.indexOf('現地スケッチ'),anchor=textQuote(text,start,start+6);
  assert.equal(anchor.exact,'現地スケッチ');assert.deepEqual(resolveTextQuote(text,anchor),{start,end:start+6});
  const changed='新しい注記．'+text;assert.equal(resolveTextQuote(changed,anchor).start,changed.indexOf('現地スケッチ'));
  assert.equal(resolveTextQuote('参照する言葉を削除しました．',anchor),null);
});
test('ambiguous quotes are left unresolved unless the saved context identifies one occurrence',()=>{
  const anchor={...textQuote('スケッチ',0,4),start:100,end:104,prefix:'',suffix:''};
  assert.equal(resolveTextQuote('Aスケッチ，Bスケッチ',anchor),null);
  assert.deepEqual(resolveTextQuote('Aスケッチ，Bスケッチ',{...anchor,prefix:'B'}),{start:7,end:11});
  assert.equal(resolveTextQuote('foo foo foo',textQuote('foo foo',0,3)),null);
});
test('different passages may connect the same pair of documents without collapsing into one edge',()=>{
  const a=textQuote('現地スケッチと配置図',0,6),b=textQuote('現地スケッチと配置図',7,10);
  const e=makeEdge('notes','north',[],'quoted',a);
  assert.ok(makeEdge('notes','north',[e],'other',b));assert.ok(makeEdge('notes','north',[e],'whole-card'));
  assert.equal(makeEdge('notes','north',[e],'duplicate',a),null);
});
test('PDF text and image regions survive serialization with page and normalized geometry',()=>{
  const board=createSample(),quote={...textQuote('North access is unavailable',0,12),type:'pdf-text',page:2,rects:[{x:.1,y:.25,w:.4,h:.02}]};
  board.edges.push(makeEdge('report','north',board.edges,'pdf-quote',quote));
  board.edges.push(makeEdge('report','plan',board.edges,'pdf-region',{type:'pdf-region',page:1,exact:'図の一部',rects:[{x:.25,y:.35,w:.5,h:.2}]}));
  const restored=validateBoard(JSON.parse(JSON.stringify(board)));
  assert.deepEqual(restored.edges.at(-2).fromAnchor,quote);assert.equal(restored.edges.at(-1).fromAnchor.rects[0].x,.25);
});
test('invalid source anchors cannot be loaded as valid research references',()=>{
  const valid={...textQuote('North access',0,12),type:'pdf-text',page:1,rects:[{x:.1,y:.2,w:.4,h:.02}]};
  for(const mutate of [a=>a.page=0,a=>a.page=3,a=>a.rects[0].w=2,a=>a.rects[0].x=-1,a=>a.end=1,a=>a.type='unknown']){
    const board=createSample(),anchor=structuredClone(valid);mutate(anchor);board.edges[0].fromAnchor=anchor;assert.throws(()=>validateBoard(board));
  }
  const board=createSample();board.edges[1].fromAnchor=valid;assert.throws(()=>validateBoard(board));
});
test('enlarged PDF preview fills the card width and keeps the cited passage visible',()=>{
  const layout=pdfPreviewLayout(234,190,595,842,'detail',{x:.4,y:.7});
  assert.equal(layout.w,234);assert.equal(layout.x,0);assert.ok(layout.h>190);
  const highlight=projectPdfRect({x:.1,y:.69,w:.6,h:.02},layout,234,190);
  assert.ok(highlight);assert.ok(Math.abs(highlight.y+highlight.h/2-.5)<.001);
  assert.equal(projectPdfRect({x:.1,y:0,w:.6,h:.01},layout,234,190),null);
  assert.equal(pdfPreviewLayout(234,190,595,842).y,0);
});
test('full-page preview preserves the complete page and maps highlights inside it',()=>{
  const layout=pdfPreviewLayout(234,190,595,842,'page');
  assert.ok(layout.w<234);assert.equal(layout.h,190);assert.ok(layout.x>0);
  const whole=projectPdfRect({x:0,y:0,w:1,h:1},layout,234,190);
  assert.ok(Math.abs(whole.x-layout.x/234)<.0001);assert.equal(whole.y,0);assert.equal(whole.h,1);
  const board=createSample();board.cards[0].pdfPreview='page';assert.equal(validateBoard(JSON.parse(JSON.stringify(board))).cards[0].pdfPreview,'page');
});

test('native projection preserves legacy metadata，unknown relationships and reference anchors',async()=>{
  const {fromKarteBoard,toKarteBoard}=await import('../model.mjs');
  const native={path:'content/test.board.md',title:'調査',docId:'board:1',tags:['research'],notes:'残すノート',cards:[{id:'a',type:'resource',source:'content/a.md',title:'資料',body:'古い本文',meta:{custom:'keep'}},{id:'b',type:'claim',title:'判断',body:'仮説',reviewed:true}],edges:[{id:'e',from:'a',to:'b',relation:'references',label:'existing',description:'根拠',meta:{custom:42}}],layout:{cards:{a:{x:10,y:20,width:280,height:330},b:{x:350,y:20,width:270,height:230}},viewport:{x:0,y:0,zoom:.8}}};
  const wall=fromKarteBoard(native,[{path:'content/a.md',kind:'markdown',body:'---\ntitle: A\n---\n現地スケッチを参照．',url:'',revision:'v2'}]);
  assert.equal(wall.cards[0].body,'現地スケッチを参照．');assert.equal(wall.edges[0].relation,'related_to');
  wall.edges[0].fromAnchor=textQuote(wall.cards[0].body,0,6);wall.edges[0].status='reviewed';
  const saved=toKarteBoard(wall,native);
  assert.equal(saved.docId,'board:1');assert.equal(saved.notes,'残すノート');assert.equal(saved.cards[0].meta.custom,'keep');assert.equal(saved.cards[1].type,'claim');assert.equal(saved.edges[0].relation,'references');assert.equal(saved.edges[0].meta.custom,42);
  const reloaded=fromKarteBoard(JSON.parse(JSON.stringify(saved)));
  assert.deepEqual(reloaded.edges[0].fromAnchor,wall.edges[0].fromAnchor);assert.equal(reloaded.edges[0].status,'reviewed');
  assert.ok(!JSON.stringify(saved).includes('resourceURL'));assert.ok(!JSON.stringify(saved).includes('revision'));
});

test('managed source refresh and layout saves preserve independent card analysis',async()=>{
  const {fromKarteBoard,toKarteBoard,resourcePatch}=await import('../model.mjs');
  for(const kind of ['markdown','pdf','image','audio']){
    const source=kind==='image'?'data/image/a.webp':kind==='audio'?'data/audio/a.wav':`content/a.${kind==='pdf'?'pdf':'md'}`;
    const original={path:'content/study.board.md',title:'研究',cards:[{id:'a',type:'resource',source,title:'資料',body:'自分の分析を残す．'}],edges:[],layout:{cards:{}}};
    const resource={path:source,kind,body:kind==='pdf'?'':'原資料のプレビュー',revision:'1'};
    const wall=fromKarteBoard(original,[resource]);
    assert.equal(wall.cards[0].body,resource.body);
    assert.equal(wall.cards[0].cardBody,original.cards[0].body);
    wall.cards[0].x+=50;
    assert.equal(toKarteBoard(wall,original).cards[0].body,'自分の分析を残す．');
    Object.assign(wall.cards[0],resourcePatch({...resource,body:'更新した原資料',revision:'2'}));
    assert.equal(toKarteBoard(wall,original).cards[0].body,'自分の分析を残す．');
    const portable=JSON.parse(JSON.stringify(wall));
    portable.cards[0].cardBody='カードのメモを編集．';
    const saved=toKarteBoard(portable,original);
    assert.equal(saved.cards[0].body,'カードのメモを編集．');
    assert.equal(fromKarteBoard(saved,[resource]).cards[0].cardBody,'カードのメモを編集．');
  }
});
