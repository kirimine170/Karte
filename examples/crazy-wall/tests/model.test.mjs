import { test } from 'node:test';
import assert from 'node:assert/strict';
import { validateBoard, makeEdge, connectionPoints, fitViewport, markdownHTML, classifyFile } from '../model.mjs';
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
