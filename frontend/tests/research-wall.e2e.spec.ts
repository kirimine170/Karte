import { test, expect, type Page } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const boardPath = 'content/research-ci.board.md';
const boardTitle = '研究CIボード';
const analysis = '独自の分析：北側入口の利用状況を確認する．';
const source = '# 現地観察メモ\n\n現地スケッチを参照のこと．';

test.beforeEach(async ({ page }) => {
  // Seed only the browser preview's virtual workspace．Reloads retain UI saves．
  await page.addInitScript(({ boardPath, boardTitle, analysis, source }) => {
    const key = 'karte.research-wall.preview.v1';
    if (localStorage.getItem(key)) return;
    localStorage.setItem(key, JSON.stringify({
      documents: { 'content/field-notes.md': source }, media: {}, metadata: {},
      boards: {
        [boardPath]: {
          path: boardPath, title: boardTitle, docId: 'board:ci', type: 'karte-board', version: 1,
          created: '2026-10-27', updated: '2026-10-27', tags: ['research'], notes: '', rawContent: '# 研究',
          cards: [{ id: 'notes', type: 'resource', source: 'content/field-notes.md', title: '現地観察メモ', body: analysis }],
          edges: [], layout: { cards: { notes: { x: 100, y: 120, width: 300, height: 330 } }, viewport: { x: 100, y: 100, zoom: 1 } },
        },
      },
    }));
  }, { boardPath, boardTitle, analysis, source });
  await page.goto('/');
  await expect(page.locator('#editor')).toHaveValue(/mock file content/);
});

async function openBoard(page: Page, title = boardTitle) {
  await page.getByRole('button', { name: 'コルクボード', exact: true }).click();
  const wall = page.frameLocator('.research-wall-frame');
  await wall.getByRole('button', { name: 'ボードを開く', exact: true }).click();
  await wall.locator('.native-resource').filter({ hasText: title }).click();
  await expect(wall.getByRole('textbox', { name: 'ボード名', exact: true })).toHaveValue(title);
  return wall;
}

test('preserves legacy card analysis after layout autosave and reload', async ({ page }) => {
  const wall = await openBoard(page);
  await wall.locator('#material-list button').filter({ hasText: '現地観察メモ' }).click();
  await expect(wall.getByRole('textbox', { name: 'カードのメモ', exact: true })).toHaveValue(analysis);
  await expect(wall.locator('.card-md-content')).toContainText('現地スケッチを参照のこと．');
  await wall.locator('#viewport').press('ArrowRight');
  await wall.getByRole('textbox', { name: 'ボード名', exact: true }).fill('保存済みの研究CIボード');
  await expect(wall.locator('#save-state')).toContainText('Karteに保存済み');

  await page.reload();
  const restored = await openBoard(page, '保存済みの研究CIボード');
  await restored.locator('#material-list button').filter({ hasText: '現地観察メモ' }).click();
  await expect(restored.getByRole('textbox', { name: 'カードのメモ', exact: true })).toHaveValue(analysis);
  await expect(restored.locator('.card-md-content')).toContainText('現地スケッチを参照のこと．');
});

test('opens the existing editor and refreshes the source without replacing card notes', async ({ page }) => {
  const wall = await openBoard(page);
  await wall.locator('#material-list button').filter({ hasText: '現地観察メモ' }).click();
  await wall.getByRole('button', { name: 'Karteで本文を編集', exact: false }).click();
  await expect(page.locator('#editor')).toHaveValue(source);
  await page.locator('#editor').fill('# 更新した観察記録\n\n管理者に北側入口の利用を確認した．');
  await page.locator('#saveBtn').click();
  await expect(page.locator('#saveBtn')).not.toHaveClass(/unsaved/);
  await page.getByRole('button', { name: 'ボードへ戻る', exact: true }).click();
  await expect(wall.locator('.card-md-content')).toContainText('管理者に北側入口の利用を確認した．');
  await wall.locator('#material-list button').filter({ hasText: '現地観察メモ' }).click();
  await expect(wall.getByRole('textbox', { name: 'カードのメモ', exact: true })).toHaveValue(analysis);
});

test('imports a portable PDF reference and restores its page and highlight after reload', async ({ page }) => {
  const wall = await openBoard(page);
  const pdf = readFileSync(resolve(__dirname, '../../examples/crazy-wall/assets/warehouse-survey.pdf'));
  const portable = {
    schema: 'karte-research-wall/1', title: 'PDF引用の検証',
    cards: [
      { id: 'report', kind: 'pdf', title: '調査PDF', filename: 'survey.pdf', body: '', cardBody: '入口の図を確認する．', attachment: `data:application/pdf;base64,${pdf.toString('base64')}`, pages: 2, page: 1, x: 100, y: 120, w: 280, h: 330 },
      { id: 'claim', kind: 'note', title: '検討事項', body: '北側入口の図を参照する．', x: 500, y: 120, w: 270, h: 230 },
    ],
    edges: [{ id: 'reference', from: 'report', to: 'claim', relation: 'related_to', reason: '図で確認', status: 'reviewed', fromAnchor: { type: 'pdf-region', exact: '入口の図', page: 2, rects: [{ x: .1, y: .2, w: .3, h: .1 }] } }],
  };
  await wall.locator('#board-input').setInputFiles({ name: 'research.wall.json', mimeType: 'application/json', buffer: Buffer.from(JSON.stringify(portable)) });
  await expect(wall.getByRole('textbox', { name: 'ボード名', exact: true })).toHaveValue(portable.title);
  await expect(wall.locator('#save-state')).toContainText('Karteに保存済み');
  await expect(wall.locator('.pdf-thumb')).toBeVisible();

  await page.reload();
  const restored = await openBoard(page, portable.title);
  await restored.locator('.reference-chip').click();
  await expect(restored.locator('#page-label')).toHaveText('2 / 2');
  await expect(restored.locator('#viewer-body canvas')).toBeVisible();
  await expect(restored.locator('#viewer-body .reference-highlight')).toBeVisible();
  await expect(restored.locator('#viewer-body .textLayer')).not.toBeEmpty();
  await restored.getByRole('button', { name: '資料を閉じる', exact: true }).click();
  // Portable imports create another board，leaving the original analysis intact．
  await restored.getByRole('button', { name: 'ボードを開く', exact: true }).click();
  await restored.locator('.native-resource').filter({ hasText: boardTitle }).click();
  await restored.locator('#material-list button').filter({ hasText: '現地観察メモ' }).click();
  await expect(restored.getByRole('textbox', { name: 'カードのメモ', exact: true })).toHaveValue(analysis);
});
