import { markdownHTML, textQuote } from './model.mjs';
export function createSample() {
  const board = {
    schema: 'karte-research-wall/1', title: '旧港倉庫の再生計画',
    cards: [
      { id: 'report', kind: 'pdf', title: '建物調査レポート', filename: 'warehouse-survey.pdf', asset: '/assets/warehouse-survey.pdf', body: '北側アクセス不可．搬入・来訪者ともに東側入口を利用する．', page: 1, pages: 2, x: 100, y: 110, w: 250, h: 330, date: '2026.09.28', badge: '未確認' },
      { id: 'north', kind: 'image', title: '北側入口の現地スケッチ', filename: 'warehouse.svg', asset: '/assets/warehouse.svg', body: '北側にも階段と扉がある．現在も使われている？', x: 470, y: 85, w: 340, h: 295, date: '2026.10.12', badge: '観察' },
      { id: 'notes', kind: 'markdown', title: '現地観察メモ', filename: 'field-notes.md', asset: '/assets/field-notes.md', body: '# 現地観察メモ\n\n2026.10.12 / 旧港倉庫 北側\n\n現地スケッチを参照のこと．\n\n- 北側に小さな階段と扉が残っている．\n- 扉の近くに手入れされた植木鉢がある．\n- 東側には搬入車両の動線がある．\n\n> 北側は「使われていない」のか，「入口ではない」のか？\n\n## 次のアクション\n管理者に，現在の利用状況を確認する．', x: 980, y: 110, w: 270, h: 315, date: '2026.10.12', badge: '現地メモ' },
      { id: 'interview', kind: 'audio', title: '地域ヒアリング #02', filename: 'interview-demo.wav', asset: '/assets/interview-demo.wav', body: '「以前は北側から入る人もいた．今はどうだろうね．」\n※ 引用は架空，音源は再生確認用の環境音です．', x: 100, y: 570, w: 310, h: 220, date: '2026.10.14', badge: '要照合', duration: 8 },
      { id: 'hypothesis', kind: 'note', title: '北側を，地域に開く入口に？', body: '搬入と来訪者の動線を分けられれば，\n小さな広場とつながる入口をつくれる．\n\nまず「いま誰が使っているか」を確かめたい．', x: 525, y: 490, w: 310, h: 240, color: 'yellow', badge: '仮説 01' },
      { id: 'quote', kind: 'excerpt', title: '「入口」の意味が違う？', body: '報告書のアクセス＝車両？\nヒアリングの入口＝歩行者？\n\n同じ言葉でも，対象を確認する．', sourceId: 'interview', anchor: '00:02', time: 2, x: 1000, y: 540, w: 250, h: 215, badge: '問い' },
      { id: 'plan', kind: 'image', title: '敷地と周辺の動線', filename: 'site-plan.svg', asset: '/assets/site-plan.svg', body: '北側は生活道路，東側は搬入路に面している．', x: 505, y: 855, w: 330, h: 260, date: '2026.09.28', badge: '配置図' },
    ],
    edges: [
      { id: 'e1', from: 'report', to: 'north', relation: 'contradicts', reason: 'レポートの「北側アクセス不可」と，現地に残る階段・扉が食い違う．アクセスの対象が車両なのか歩行者なのかは未確認．', status: 'pending' },
      { id: 'e2', from: 'north', to: 'hypothesis', relation: 'supports', reason: '既存の階段と扉は入口再生の手がかりになる．構造・管理上の利用可否は別途確認する．', status: 'reviewed' },
      { id: 'e3', from: 'notes', to: 'hypothesis', relation: 'supports', reason: '搬入車両との動線分離が現地観察からの課題として挙がっている．', status: 'reviewed' },
      { id: 'e4', from: 'interview', to: 'hypothesis', relation: 'questions', reason: '過去の利用についての証言であり，現在も開放できるかは判断できない．管理者への確認が必要．', status: 'pending' },
      { id: 'e5', from: 'interview', to: 'quote', relation: 'related_to', reason: 'ヒアリングをもとに生まれた問い．', status: 'reviewed' },
      { id: 'e6', from: 'plan', to: 'hypothesis', relation: 'supports', reason: '北側の生活道路に向けた小さな入口という配置上の可能性がある．', status: 'pending' },
      { id: 'e7', from: 'quote', to: 'report', relation: 'questions', reason: '報告書で使われる「アクセス」の定義を原著者へ確認したい．', status: 'pending' },
    ],
  };
  const plain=markdownHTML(board.cards.find(c=>c.id==='notes').body).replace(/<[^>]*>/g,'').replaceAll('&gt;','>').replaceAll('&lt;','<').replaceAll('&amp;','&');
  const start=plain.indexOf('現地スケッチ');
  board.edges.push({id:'e8',from:'notes',to:'north',relation:'related_to',reason:'本文の「現地スケッチ」が指す原資料．',status:'reviewed',fromAnchor:textQuote(plain,start,start+6)});
  return board;
}
