import { test, expect, type Page } from '@playwright/test';

test.beforeEach(async ({ page }) => {
    await page.goto('/');
    await page.locator('.tab[data-tab="accounting"]').click();
    await expect(page.locator('#accounting-tab .ac-save-indicator')).toContainText('保存済み');
});

async function fillEntry(page: Page, description: string, amount: number, debit = '会議費', credit = '現金', date = '2026-10-26') {
    const form = page.locator('#ac-entry-form');
    await form.locator('[name="date"]').fill(date);
    await form.locator('[name="description"]').fill(description);
    await form.locator('[data-side="debit"] [data-line-field="account"]').selectOption(debit);
    await form.locator('[data-side="credit"] [data-line-field="account"]').selectOption(credit);
    await form.locator('[data-side="debit"] [data-line-field="amount"]').fill(String(amount));
    await form.locator('[data-side="credit"] [data-line-field="amount"]').fill(String(amount));
}

test('post a receipt-independent journal，persist，report，and reverse', async ({ page }) => {
    await page.locator('.ac-header [data-action="new-entry"]').click();
    await fillEntry(page, '検証用イベント参加費', 8000, '現金', '売上高', '2026-09-26');
    await page.locator('[name="post"]').click();
    await expect(page.locator('.ac-notice')).toContainText('仕訳を記帳しました');
    await page.locator('.ac-header [data-action="new-entry"]').click();
    await fillEntry(page, '検証用会場費', 6400);
    await page.locator('[name="save-draft"]').click();
    await expect(page.locator('.ac-metrics')).toContainText('￥8,000');
    await page.locator('[data-view="journal"]').click();
    await page.locator('tr', { hasText: '検証用会場費' }).getByRole('button', { name: '確認・編集' }).click();
    await page.locator('[data-side="credit"] [data-line-field="amount"]').fill('6000');
    await page.locator('[name="post"]').click();
    await expect(page.locator('#ac-entry-error')).toContainText('一致');
    await page.locator('[data-side="credit"] [data-line-field="amount"]').fill('6400');
    await page.locator('[name="post"]').click();
    await page.reload();
    await page.locator('.tab[data-tab="accounting"]').click();
    await expect(page.locator('.ac-metrics')).toContainText('￥1,600');
    await page.screenshot({ path: '/tmp/karte-accounting-overview.png', fullPage: true });
    await page.locator('[data-view="ledger"]').click();
    await page.getByLabel('取引月', { exact: true }).fill('2026-10');
    await expect(page.locator('.ac-table')).toContainText('借 ￥8,000');
    await expect(page.locator('.ac-table')).toContainText('借 ￥1,600');
    await page.locator('[data-view="reports"]').click();
    await expect(page.locator('.ac-report').first()).toContainText('-￥6,400');
    await expect(page.locator('.ac-report').nth(1)).toContainText('￥1,600');
    await page.screenshot({ path: '/tmp/karte-accounting-reports.png', fullPage: true });
    await page.locator('[data-view="journal"]').click();
    await page.locator('tr', { hasText: '検証用会場費' }).getByRole('button', { name: '詳細' }).click();
    await page.getByRole('button', { name: '反対仕訳を作成' }).click();
    await page.locator('[name="post"]').click();
    await page.locator('[data-view="overview"]').click();
    await page.getByRole('button', { name: '全期間に戻す' }).click();
    await expect(page.locator('.ac-metrics').nth(0)).toContainText('￥8,000');
});

test('receipt originals persist，reject duplicates，and link to a draft', async ({ page }) => {
    const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a4foAAAAASUVORK5CYII=', 'base64');
    await page.locator('#ac-receipt-upload').setInputFiles({ name: '検証レシート.png', mimeType: 'image/png', buffer: png });
    await expect(page.locator('.ac-receipt-preview img')).toBeVisible();
    await page.locator('#ac-receipt-upload').setInputFiles({ name: '同じレシート.png', mimeType: 'image/png', buffer: png });
    await expect(page.locator('.ac-notice')).toContainText('取り込み済み');
    await page.getByRole('button', { name: '仕訳を作成', exact: true }).click();
    await expect(page.locator('#ac-entry-receipt-preview img')).toBeVisible();
    await fillEntry(page, '証憑からの検証仕訳', 1200);
    await page.locator('[name="save-draft"]').click();
    await expect(page.locator('.ac-receipt-item')).toContainText('仕訳下書きあり');
    await page.reload();
    await page.locator('.tab[data-tab="accounting"]').click();
    await page.locator('[data-view="receipts"]').click();
    await page.locator('.ac-receipt-item').click();
    await expect(page.locator('.ac-receipt-preview img')).toBeVisible();
    await page.getByRole('button', { name: '仕訳を開く' }).click();
    await expect(page.locator('[name="description"]')).toHaveValue('証憑からの検証仕訳');
});

test('Sheet-shaped CSV import remains a draft and deduplicates', async ({ page }) => {
    const csv = '\ufeff\n,仕訳ID,取引発生日,勘定科目,補助科目,数量,単価,金額,勘定科目,補助科目,数量,単価,金額,担当者ID（HackSick_ID）,摘要,イベントID\n,0,2026-02-28,現金,,8,1000,8000,売上高,参加費収入,8,1000,8000,2,CSV検証用参加費,2\n';
    const payload = { name: '仕訳帳.csv', mimeType: 'text/csv', buffer: Buffer.from(csv) };
    await page.locator('#ac-csv-upload').setInputFiles(payload);
    await expect(page.locator('.ac-table')).toContainText('CSV検証用参加費');
    await expect(page.locator('.ac-badge.draft')).toContainText('下書き');
    await page.locator('#ac-csv-upload').setInputFiles(payload);
    await expect(page.locator('.ac-notice')).toContainText('新しい仕訳はありません');
    await page.locator('[data-view="overview"]').click();
    await expect(page.locator('.ac-metric').first()).toContainText('￥0');
});

test('accounting uses the whole workspace and restores the editor layout', async ({ page }) => {
    await expect(page.locator('#contentArea')).toHaveClass(/accounting-mode/);
    await expect(page.locator('.preview-pane')).toBeHidden();
    await page.locator('.tab[data-tab="editor"]').click();
    await expect(page.locator('#contentArea')).not.toHaveClass(/accounting-mode/);
    await expect(page.locator('.preview-pane')).toBeVisible();
    await page.setViewportSize({ width: 720, height: 900 });
    await page.locator('.tab[data-tab="accounting"]').click();
    await expect(page.locator('.ac-header')).toBeVisible();
    expect(await page.locator('#accounting-tab').evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true);
});

const sampleCSV = (count = 3) => '\ufeff仕訳ID,取引発生日,勘定科目,補助科目,数量,単価,金額,勘定科目,補助科目,数量,単価,金額,担当者ID（HackSick_ID）,摘要,イベントID\n' + Array.from({length:count},(_,i)=>`${i+100},2026-${i===2?'09':'10'}-20,会議費,,1,${100*(i+1)},${100*(i+1)},現金,,1,${100*(i+1)},${100*(i+1)},test,CSV検証${i+1},${i===2?'B':'A'}`).join('\n');
async function importFromButton(page:Page, action:string, text:string, name='検証.csv') {
    const chooser=page.waitForEvent('filechooser'); await page.locator(`[data-action="${action}"]`).first().click();
    await(await chooser).setFiles({name,mimeType:'text/csv',buffer:Buffer.from(text)});
    await expect(page.locator('.ac-save-indicator')).toContainText('保存済み');
}
async function newDraft(page:Page,name='手入力の下書き') {
    await page.locator('.ac-header [data-action="new-entry"]').click(); await fillEntry(page,name,500);
    await page.locator('[name="save-draft"]').click(); await expect(page.locator('.ac-entry-dialog')).toHaveCount(0);
}

test('close unchanged，discard cancel/confirm，Escape and focus without window.confirm',async({page})=>{
    await page.evaluate(()=>{window.confirm=()=>{throw Error('browser confirm must not be used');};});
    await page.locator('.ac-header [data-action="new-entry"]').click();
    await page.getByRole('button',{name:'閉じる',exact:true}).click(); await expect(page.locator('.ac-entry-dialog')).toHaveCount(0);
    await page.locator('[data-action="new-entry"]').last().click(); await fillEntry(page,'閉じるテスト',500);
    await page.getByRole('button',{name:'閉じる',exact:true}).click(); await expect(page.getByRole('alertdialog')).toContainText('入力を閉じますか');
    await page.getByRole('button',{name:'キャンセル',exact:true}).click(); await expect(page.locator('[name="description"]')).toHaveValue('閉じるテスト');
    await page.getByRole('button',{name:'閉じる',exact:true}).click(); await page.keyboard.press('Escape'); await expect(page.getByRole('alertdialog')).toHaveCount(0);
    await page.keyboard.press('Escape'); await expect(page.getByRole('button',{name:'キャンセル',exact:true})).toBeFocused();
    await page.keyboard.press('Shift+Tab'); await expect(page.getByRole('button',{name:'破棄して閉じる'})).toBeFocused();
    await page.keyboard.press('Tab'); await expect(page.getByRole('button',{name:'キャンセル',exact:true})).toBeFocused();
    await page.getByRole('button',{name:'破棄して閉じる'}).click(); await expect(page.locator('.ac-entry-dialog')).toHaveCount(0);
    await expect(page.locator('.ac-footer')).toContainText('下書き 0件');
});

test('saved draft closes unchanged，delete cancel/confirm persists，posted readonly closes',async({page})=>{
    await newDraft(page,'削除対象'); await page.getByRole('button',{name:'仕訳帳を開く →'}).click();
    await page.getByRole('button',{name:'確認・編集'}).click(); await page.getByRole('button',{name:'閉じる',exact:true}).click(); await expect(page.locator('.ac-entry-dialog')).toHaveCount(0);
    await page.getByRole('button',{name:'確認・編集'}).click(); await page.getByRole('button',{name:'下書きを削除'}).click();
    await page.getByRole('button',{name:'キャンセル',exact:true}).click(); await expect(page.locator('[name="description"]')).toHaveValue('削除対象');
    await page.getByRole('button',{name:'下書きを削除'}).click(); await page.getByRole('button',{name:'削除する',exact:true}).click();
    await expect(page.locator('.ac-notice')).toContainText('下書きを削除しました'); await page.reload(); await page.locator('.tab[data-tab="accounting"]').click();
    await expect(page.locator('.ac-footer')).toContainText('下書き 0件'); await newDraft(page,'記帳済み表示'); await page.locator('[data-view="journal"]').click();
    await page.getByRole('button',{name:'確認・編集'}).click(); await page.locator('[name="post"]').click(); await page.getByRole('button',{name:'詳細',exact:true}).click();
    await expect(page.locator('[name="description"]')).toBeDisabled(); await expect(page.getByRole('button',{name:'下書きを削除'})).toHaveCount(0);
    await page.getByRole('button',{name:'閉じる',exact:true}).click(); await expect(page.locator('.ac-entry-dialog')).toHaveCount(0);
});

test('add/remove both sides，save changes and post compound entry',async({page})=>{
    await newDraft(page,'複合仕訳'); await page.locator('[data-view="journal"]').click(); await page.getByRole('button',{name:'確認・編集'}).click();
    for(const side of ['debit','credit']) {
        await page.locator(`[data-action="add-line"][data-side="${side}"]`).click(); await expect(page.locator(`.ac-entry-line[data-side="${side}"]`)).toHaveCount(2);
        await page.locator(`[data-action="remove-line"][data-side="${side}"]`).last().click(); await expect(page.locator(`.ac-entry-line[data-side="${side}"]`)).toHaveCount(1);
    }
    await page.locator('[name="description"]').fill('複合仕訳の変更'); await page.locator('[name="save-draft"]').click(); await page.getByRole('button',{name:'確認・編集'}).click();
    await expect(page.locator('[name="description"]')).toHaveValue('複合仕訳の変更'); await page.locator('[data-action="add-line"][data-side="debit"]').click();
    await page.locator('[data-side="debit"] [data-line-field="amount"]').first().fill('200'); await page.locator('[data-side="debit"] [data-line-field="amount"]').last().fill('300');
    await page.locator('[name="post"]').click(); await expect(page.locator('.ac-notice')).toContainText('仕訳を記帳しました');
    await page.getByRole('button',{name:'詳細',exact:true}).click(); await expect(page.locator('.ac-entry-line[data-side="debit"]')).toHaveCount(2);
});

test('CSV bulk selection，individual/all/clear，cancel and atomic approval persist',async({page})=>{
    await newDraft(page); await importFromButton(page,'import-csv',sampleCSV()); await expect(page.locator('[data-action="bulk-post"]')).toBeDisabled();
    await page.locator('[data-action="select-csv"]').click(); await expect(page.locator('#ac-bulk-controls')).toContainText('下書き 3件を選択');
    await page.locator('[data-action="clear-selection"]').click(); await page.locator('[data-select-draft]').first().check(); await expect(page.locator('#ac-bulk-controls')).toContainText('下書き 1件を選択');
    await page.locator('[data-select-draft]').first().uncheck(); await page.locator('#ac-select-all').check(); await expect(page.locator('[data-select-draft]:checked')).toHaveCount(4);
    await page.locator('#ac-select-all').uncheck(); await expect(page.locator('[data-action="bulk-post"]')).toBeDisabled();
    await page.locator('[data-action="select-csv"]').click(); await page.locator('[data-action="bulk-post"]').click(); await expect(page.getByRole('alertdialog')).toContainText('各 ￥600');
    await expect(page.locator('.ac-confirm-list > div')).toHaveCount(3); await page.getByRole('button',{name:'キャンセル',exact:true}).click(); await expect(page.locator('.ac-badge.draft')).toHaveCount(4);
    await page.locator('[data-action="bulk-post"]').click(); await page.getByRole('button',{name:'3件を承認して記帳'}).click();
    await expect(page.locator('.ac-notice')).toContainText('3件の下書きを承認して記帳'); await expect(page.locator('.ac-badge.posted')).toHaveCount(3); await expect(page.locator('.ac-badge.draft')).toHaveCount(1);
    await page.reload(); await page.locator('.tab[data-tab="accounting"]').click(); await expect(page.locator('.ac-metric').nth(1)).toContainText('￥600'); await expect(page.locator('.ac-footer')).toContainText('下書き 1件 · 記帳済み 3件');
});

test('month/event/search prune hidden selections and only visible draft posts',async({page})=>{
    await importFromButton(page,'import-csv',sampleCSV()); await page.locator('#ac-select-all').check(); await page.getByLabel('取引月',{exact:true}).fill('2026-10');
    await expect(page.locator('#ac-bulk-controls')).toContainText('下書き 2件を選択'); await page.getByLabel('イベントで絞り込み').selectOption('B'); await expect(page.locator('#ac-bulk-controls')).toContainText('下書き 0件を選択');
    await page.getByRole('button',{name:'全期間に戻す'}).click(); await page.locator('#ac-select-all').check(); await page.locator('#ac-search').fill('CSV検証2');
    await expect(page.locator('#ac-bulk-controls')).toContainText('下書き 1件を選択'); await expect(page.locator('#ac-journal-count')).toHaveText('1件');
    await page.locator('[data-action="bulk-post"]').click(); await expect(page.getByRole('alertdialog')).not.toContainText('CSV検証1'); await page.getByRole('button',{name:'1件を承認して記帳'}).click();
    await page.locator('#ac-search').fill(''); await expect(page.locator('.ac-badge.draft')).toHaveCount(2); await expect(page.locator('.ac-badge.posted')).toHaveCount(1);
});

test('stale bulk approval fails without partial posting and retries after reload',async({page,context})=>{
    await importFromButton(page,'import-csv',sampleCSV()); await page.locator('[data-action="select-csv"]').click(); await page.locator('[data-action="bulk-post"]').click();
    const other=await context.newPage(); await other.goto('/'); await other.locator('.tab[data-tab="accounting"]').click(); await expect(other.locator('.ac-save-indicator')).toContainText('保存済み'); await newDraft(other,'別画面の更新');
    await page.getByRole('button',{name:'3件を承認して記帳'}).click(); await expect(page.locator('.ac-notice')).toContainText('再読み込み'); await expect(page.locator('.ac-badge.posted')).toHaveCount(0);
    await page.getByRole('button',{name:'再読み込み',exact:true}).click(); await expect(page.locator('.ac-badge.draft')).toHaveCount(4); await page.locator('[data-action="bulk-post"]').click(); await page.getByRole('button',{name:'3件を承認して記帳'}).click();
    await expect(page.locator('.ac-badge.posted')).toHaveCount(3); await expect(page.locator('.ac-badge.draft')).toHaveCount(1); await other.close();
});

test('event create/update/import，navigation，event defaults and ledger selection',async({page})=>{
    await page.locator('[data-view="events"]').click(); const form=page.locator('#ac-event-form');
    await form.locator('[name="id"]').fill('A'); await form.locator('[name="name"]').fill('検証イベント'); await form.locator('[name="date"]').fill('2026-10-20'); await form.getByRole('button',{name:'登録・更新'}).click(); await expect(page.locator('.ac-table')).toContainText('検証イベント');
    await form.locator('[name="id"]').fill('A'); await form.locator('[name="name"]').fill('更新後イベント'); await form.getByRole('button',{name:'登録・更新'}).click(); await expect(page.locator('.ac-table tbody tr')).toHaveCount(1);
    await importFromButton(page,'import-events','イベントID,イベント名,開催日時\nB,CSVイベント,2026-09-20'); await expect(page.locator('.ac-table tbody tr')).toHaveCount(2);
    await page.getByLabel('イベントで絞り込み').selectOption('B'); await page.locator('.ac-header [data-action="new-entry"]').click(); await expect(page.locator('[name="eventId"]')).toHaveValue('B'); await page.getByRole('button',{name:'閉じる',exact:true}).click(); await page.getByRole('button',{name:'全期間に戻す'}).click();
    for(const view of ['receipts','journal','reports','overview','ledger']) { await page.locator(`[data-view="${view}"]`).click(); await expect(page.locator('.ac-header h1')).toBeVisible(); }
    await page.locator('#ac-ledger-account').selectOption('会議費'); await expect(page.locator('.ac-table')).toContainText('期末残高');
});

test('CSV/JSON download content，export reimport deduplicates，invalid CSV atomic failure',async({page})=>{
    const fs=await import('node:fs/promises'); await importFromButton(page,'import-csv',sampleCSV().replace(',200,200,現金',',200,201,現金')); await expect(page.locator('.ac-notice')).toContainText('一致'); await expect(page.locator('.ac-footer')).toContainText('下書き 0件');
    await importFromButton(page,'import-csv',sampleCSV()); await page.locator('#ac-search').fill('CSV検証2'); let downloaded=page.waitForEvent('download'); await page.getByRole('button',{name:'表示中のCSVを保存'}).click();
    const csv=await fs.readFile((await(await downloaded).path())!,'utf8'); expect(csv).toContain('CSV検証2'); expect(csv).not.toContain('CSV検証1');
    downloaded=page.waitForEvent('download'); await page.getByRole('button',{name:'帳簿JSONを保存'}).click(); const json=JSON.parse(await fs.readFile((await(await downloaded).path())!,'utf8')); expect(json.entries).toHaveLength(3);
    await importFromButton(page,'import-csv',csv,'export.csv'); await expect(page.locator('.ac-notice')).toContainText('新しい仕訳はありません');
});

test('receipt upload button，OCR browser error，draft deletion retains original',async({page})=>{
    const chooser=page.waitForEvent('filechooser'); await page.locator('.ac-header [data-action="upload"]').click(); await(await chooser).setFiles({name:'test.png',mimeType:'image/png',buffer:Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a4foAAAAASUVORK5CYII=','base64')}); await expect(page.locator('.ac-receipt-preview img')).toBeVisible();
    await page.getByRole('button',{name:'OCRで読み取る'}).click(); await expect(page.locator('.ac-notice')).toContainText('デスクトップ版'); await expect(page.locator('.ac-receipt-preview img')).toBeVisible();
    await page.getByRole('button',{name:'仕訳を作成',exact:true}).click(); await fillEntry(page,'証憑付き削除',1100); await page.locator('[name="save-draft"]').click(); await page.getByRole('button',{name:'仕訳を開く'}).click();
    await page.getByRole('button',{name:'下書きを削除'}).click(); await page.getByRole('button',{name:'削除する',exact:true}).click(); await expect(page.locator('.ac-receipt-item')).toContainText('未仕訳'); await expect(page.locator('.ac-receipt-preview img')).toBeVisible();
});


test('137 CSV drafts approve in one revision and retain their totals', async ({page}) => {
    await importFromButton(page,'import-csv',sampleCSV(137));
    await expect(page.locator('.ac-save-indicator')).toContainText('rev 1');
    await page.locator('[data-action="select-csv"]').click();
    await page.locator('[data-action="bulk-post"]').click();
    await expect(page.locator('.ac-confirm-list > div')).toHaveCount(137);
    await expect(page.getByRole('alertdialog')).toContainText('各 ￥945,300');
    await page.getByRole('button',{name:'137件を承認して記帳'}).click();
    await expect(page.locator('.ac-save-indicator')).toContainText('rev 2');
    await expect(page.locator('.ac-badge.posted')).toHaveCount(137);
    await page.locator('[data-view="overview"]').click();
    await expect(page.locator('.ac-metric').nth(1)).toContainText('￥945,300');
});


test('switches between accounting，Instagram，and the research wall', async ({ page }) => {
    await page.getByRole('button', { name: 'Instagram', exact: true }).click();
    await expect(page.locator('#postlab-tab')).toBeVisible();
    await expect(page.locator('iframe.postlab-frame')).toHaveAttribute('title', 'Instagram 投稿シミュレータ');
    await expect(page.locator('#accounting-tab')).toBeHidden();
    await page.getByRole('button', { name: 'コルクボード', exact: true }).click();
    await expect(page.locator('#board-tab')).toBeVisible();
    await expect(page.frameLocator('.research-wall-frame').locator('#viewport')).toBeVisible();
    await page.getByRole('button', { name: '会計', exact: true }).click();
    await expect(page.locator('#accounting-tab .ac-shell')).toBeVisible();
    await expect(page.locator('#contentArea')).toHaveClass(/accounting-mode/);
    await expect(page.locator('#contentArea')).not.toHaveClass(/postlab-mode/);
    await page.getByRole('button', { name: 'エディター', exact: true }).click();
    await expect(page.locator('#editor')).toBeVisible();
    await expect(page.locator('#contentArea')).not.toHaveClass(/accounting-mode|postlab-mode/);
});
