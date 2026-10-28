import { BaseComponent } from './component-base';
import { useUIStore } from '../stores/ui-store';
import { getAccountingAPI, isNativeAccounting, type AccountingAPI } from '../accounting/api';
import { balanceSheet, emptyEntry, filteredEntries, journalCSV, ledger, localDate, profitLoss, sum, validateEntry, yen, type Book, type Filter, type JournalEntry, type Receipt } from '../accounting/core';

type View = 'overview' | 'receipts' | 'journal' | 'ledger' | 'reports' | 'events';
const escape = (value: unknown) => String(value ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]!);
const titles: Record<View, string> = { overview: '会計の概要', receipts: '証憑の確認', journal: '仕訳帳', ledger: '総勘定元帳', reports: '収支・残高', events: 'イベント管理' };
const groupNames: Record<string, string> = { asset: '資産', liability: '負債', equity: '純資産', revenue: '収益', expense: '費用' };

export class AccountingView extends BaseComponent {
    private api: AccountingAPI;
    private book: Book | null = null;
    private view: View = 'overview';
    private filter: Filter = { month: '', eventId: '' };
    private account = '現金';
    private selectedReceipt = '';
    private draft: JournalEntry | null = null;
    private draftBaseline = '';
    private selectedDrafts = new Set<string>();
    private confirmation: { title: string; text: string; accept: string; entries?: JournalEntry[]; resolve: (accepted: boolean) => void } | null = null;
    private busy = false;
    private message = '';
    private error = false;
    private search = '';
    private previewToken = 0;
    private cleanup: (() => void)[] = [];
    private destroyed = false;

    constructor(api: AccountingAPI = getAccountingAPI()) { super(); this.api = api; }

    init(): void {
        this.element = document.getElementById('accounting-tab');
        if (!this.element) return;
        this.cleanup.push(this.addEventListener(this.element, 'click', event => void this.click(event)));
        this.cleanup.push(this.addEventListener(this.element, 'change', event => void this.change(event)));
        this.cleanup.push(this.addEventListener(this.element, 'input', event => this.input(event)));
        this.cleanup.push(this.addEventListener(this.element, 'submit', event => { event.preventDefault(); void this.submit(event); }));
        this.cleanup.push(this.addEventListener(this.element, 'keydown', event => this.keydown(event)));
        const beforeUnload = (event: BeforeUnloadEvent) => { if (this.isDirty() || this.busy) { event.preventDefault(); event.returnValue = ''; } };
        window.addEventListener('beforeunload', beforeUnload);
        this.cleanup.push(() => window.removeEventListener('beforeunload', beforeUnload));
        const drop = (event: Event) => { const files = (event as CustomEvent<{ files: File[] }>).detail.files; void this.importReceipts(files); };
        window.addEventListener('karte-accounting-file-drop', drop);
        this.cleanup.push(() => window.removeEventListener('karte-accounting-file-drop', drop));
        this.render(); void this.reload();
    }

    private async reload() {
        await this.run(async () => { this.book = await this.api.GetAccountingData(); }, '会計データを読み込みました．');
    }
    private async run(work: () => Promise<void>, success = '') {
        if (this.busy) return;
        this.busy = true; this.error = false; this.message = '処理中です…'; this.render();
        try { await work(); if (success || this.message === '処理中です…') this.message = success; }
        catch (error) { this.error = true; this.message = String(error instanceof Error ? error.message : error); }
        finally {
            this.busy = false;
            if (!this.destroyed) {
                this.render();
                const alert = this.element?.querySelector('#ac-entry-error');
                if (alert && this.error) alert.textContent = this.message;
            }
        }
    }
    private options(selected: string, events = false) {
        if (!this.book) return '';
        if (events) return '<option value="">すべてのイベント</option>' + this.book.events.map(e => `<option value="${escape(e.id)}" ${e.id === selected ? 'selected' : ''}>${escape(e.name)} · ${escape(e.id)}</option>`).join('');
        return Object.entries(groupNames).map(([group, name]) => `<optgroup label="${name}">${this.book!.accounts.filter(a => a.group === group).map(a => `<option ${a.name === selected ? 'selected' : ''}>${escape(a.name)}</option>`).join('')}</optgroup>`).join('');
    }
    private button(action: string, text: string, extra = '') { return `<button type="button" class="ac-button" data-action="${action}" ${this.busy ? 'disabled' : ''} ${extra}>${text}</button>`; }

    private render() {
        if (!this.element || this.destroyed) return;
        const b = this.book;
        const count = b?.entries.filter(e => e.status === 'draft').length ?? 0;
        this.pruneSelection();
        this.element.innerHTML = `<section class="ac-shell" aria-label="会計管理" ${this.draft || this.confirmation ? 'inert' : ''}>
            <aside class="ac-rail"><div class="ac-brand"><span class="ac-brand-mark">帳</span><div><strong>Karte 会計</strong><small>ACCOUNTING LAB</small></div></div>
                <nav aria-label="会計メニュー">${(Object.keys(titles) as View[]).map((view, i) => `<button type="button" class="ac-nav ${view === this.view ? 'is-active' : ''}" data-view="${view}" ${view === this.view ? 'aria-current="page"' : ''}><span class="ac-nav-icon">${['◫', '▤', '≡', '▥', '↗', '◇'][i]}</span>${titles[view]}${view === 'journal' && count ? `<span class="ac-count">${count}</span>` : ''}</button>`).join('')}</nav>
                <div class="ac-rail-note"><span class="ac-live-dot"></span> ローカルに保存<small>${isNativeAccounting() ? 'Karteデータフォルダー' : 'このブラウザのIndexedDB'}<br>検証実装 · JPY／税込入力</small></div>
            </aside>
            <div class="ac-main"><header class="ac-header"><div><span class="ac-eyebrow">WORKSPACE / ACCOUNTING</span><h1>${titles[this.view]}</h1></div><div class="ac-actions">${this.button('upload', '＋ 証憑を追加')}${this.button('new-entry', '仕訳を入力')}</div></header>
                <div class="ac-filters"><label>取引月 <input type="month" aria-label="取引月" data-filter="month" value="${escape(this.filter.month)}"></label><label>イベント <select aria-label="イベントで絞り込み" data-filter="eventId">${this.options(this.filter.eventId, true)}</select></label>${this.button('clear-filter', '全期間に戻す')}<span class="ac-save-indicator">${this.busy ? '処理中…' : `保存済み · rev ${b?.revision ?? 0}`}</span>${this.button('reload', '再読み込み')}</div>
                <p class="ac-notice ${this.error ? 'is-error' : ''}" role="status" aria-live="polite">${escape(this.message || (isNativeAccounting() ? '原本と下書きを確認してから記帳してください．集計には記帳済みの仕訳だけを使用します．' : 'ブラウザ検証モードです．このブラウザに保存され，デスクトップ版とは別の帳簿を使用します．OCRはデスクトップ版で利用できます．'))}</p>
                <div class="ac-content">${b ? this.content(b) : '<div class="ac-empty"><h2>会計データを読み込んでいます</h2><p>読み込みに失敗した場合は「再読み込み」を実行してください．</p></div>'}</div>
                <footer class="ac-footer"><span>参照シートの構成に対応 · 仕訳帳／総勘定元帳／PL／BS</span><span>下書き ${count}件 · 記帳済み ${b?.entries.filter(e => e.status === 'posted').length ?? 0}件</span></footer>
            </div></section>
            <input type="file" id="ac-receipt-upload" accept=".jpg,.jpeg,.png,.webp,.pdf" multiple hidden>
            <input type="file" id="ac-csv-upload" accept=".csv" hidden>
            <input type="file" id="ac-events-upload" accept=".csv" hidden>
            ${this.draft && b ? this.editor(this.draft) : ''}${this.confirmation ? this.confirmationDialog() : ''}`;
        const modal = this.element.querySelector<HTMLElement>(this.confirmation ? '.ac-confirm-dialog' : '.ac-entry-dialog');
        modal?.querySelector<HTMLElement>('button:not(:disabled), input:not(:disabled)')?.focus();
        if (this.busy) this.element.querySelectorAll<HTMLInputElement | HTMLSelectElement | HTMLButtonElement>('input, select, button').forEach(control => { control.disabled = true; });
        if (this.draft?.receiptId) void this.loadPreview(this.draft.receiptId, '#ac-entry-receipt-preview');
        else if (this.view === 'receipts' && this.selectedReceipt) void this.loadPreview(this.selectedReceipt, '#ac-receipt-preview');
    }

    private content(b: Book): string {
        const entries = filteredEntries(b, this.filter);
        if (this.view === 'overview') {
            const pl = profitLoss(b, entries);
            const pending = filteredEntries(b, this.filter, false).filter(e => e.status === 'draft').length;
            return `<div class="ac-metrics">${[['収入', pl.revenue, '記帳済みの収益'], ['支出', pl.expense, '記帳済みの費用'], ['収支差額', pl.profit, '収入 − 支出']].map(([label, value, note]) => `<article class="ac-metric"><span>${label}</span><strong class="${Number(value) < 0 ? 'ac-negative' : ''}">${yen(Number(value))}</strong><small>${note}</small></article>`).join('')}<article class="ac-metric ac-pending"><span>確認待ちの仕訳</span><strong>${pending}<em>件</em></strong><small>集計への反映前</small></article></div>
                <div class="ac-section-heading"><div><h2>最近の仕訳</h2><p>原本から帳簿まで，確認しながら進めます．</p></div>${this.button('go-journal', '仕訳帳を開く →')}</div>${this.journalTable(filteredEntries(b, this.filter, false).slice(-8).reverse())}
                <div class="ac-start-grid"><article><span>01 / CAPTURE</span><h3>証憑を取り込む</h3><p>レシート画像やPDFの原本を保存．同じファイルの取り込みを検知します．</p>${this.button('upload', '証憑を選択')}</article><article><span>02 / REVIEW</span><h3>仕訳を確認する</h3><p>日付・用途・勘定科目・イベントを入力．貸借を合わせて，下書きまたは記帳済みで保存します．</p>${this.button('new-entry', '仕訳を入力')}</article><article><span>03 / CONNECT</span><h3>既存の帳簿を取り込む</h3><p>参照スプレッドシートの「仕訳帳」をUTF-8のCSVで取り込み，確認後に記帳できます．</p>${this.button('import-csv', '仕訳CSVを取り込む')}</article></div>`;
        }
        if (this.view === 'journal') {
            const all = this.visibleEntries();
            return `<div class="ac-section-heading"><div><h2>取引の一覧 <span id="ac-journal-count" class="ac-muted">${all.length}件</span></h2><p>記帳済みの訂正は，詳細から反対仕訳を作成します．</p></div><div class="ac-actions">${this.button('import-csv', 'CSV取込')}${this.button('export-csv', '表示中のCSVを保存')}${this.button('backup', '帳簿JSONを保存')}</div></div><label class="ac-search">検索 <input id="ac-search" type="search" value="${escape(this.search)}" placeholder="摘要・科目・担当者"></label><div id="ac-bulk-controls">${this.bulkControls()}</div><div id="ac-journal-table">${this.journalTable(all.slice().reverse(), true)}</div>`;
        }
        if (this.view === 'receipts') return this.receipts(b);
        if (this.view === 'ledger') {
            const result = ledger(b, this.filter, this.account);
            return `<div class="ac-section-heading"><div><h2>勘定科目ごとの動き</h2><p>月の開始前の記帳済み残高を繰り越して表示します．残高には借方／貸方を併記します．</p></div><label>勘定科目 <select id="ac-ledger-account">${this.options(this.account)}</select></label></div><div class="ac-table-wrap"><table class="ac-table"><thead><tr><th>取引日</th><th>摘要</th><th>相手科目</th><th class="ac-number">借方</th><th class="ac-number">貸方</th><th class="ac-number">残高</th></tr></thead><tbody><tr class="ac-total"><td colspan="5">前月繰越${this.filter.month ? '' : '（全期間）'}</td><td class="ac-number">${this.balanceLabel(result.opening)}</td></tr>${result.rows.map(row => `<tr><td>${row.entry.date}</td><td>${escape(row.entry.description)}</td><td>${escape([...row.entry.debit, ...row.entry.credit].filter(l => l.account !== this.account).map(l => l.account).join('・'))}</td><td class="ac-number">${row.debit ? yen(row.debit) : '—'}</td><td class="ac-number">${row.credit ? yen(row.credit) : '—'}</td><td class="ac-number">${this.balanceLabel(row.balance)}</td></tr>`).join('')}<tr class="ac-total"><td colspan="5">期末残高</td><td class="ac-number">${this.balanceLabel(result.balance)}</td></tr></tbody></table></div>`;
        }
        if (this.view === 'reports') return this.reports(b, entries);
        return `<div class="ac-section-heading"><div><h2>イベントの登録</h2><p>参照シートのイベントIDをそのまま利用できます．登録済みIDは名称・日付を更新します．</p></div>${this.button('import-events', 'イベント一覧CSVを取込')}</div><form id="ac-event-form" class="ac-event-form"><label>イベントID<input name="id" required placeholder="例: 2"></label><label>イベント名<input name="name" required placeholder="例: 第2回LT会"></label><label>開催日<input name="date" type="date"></label><button class="ac-button" ${this.busy ? 'disabled' : ''}>登録・更新</button></form><div class="ac-table-wrap"><table class="ac-table"><thead><tr><th>ID</th><th>イベント名</th><th>開催日</th><th class="ac-number">記帳済み仕訳</th></tr></thead><tbody>${b.events.map(e => `<tr><td>${escape(e.id)}</td><td>${escape(e.name)}</td><td>${escape(e.date || '—')}</td><td class="ac-number">${b.entries.filter(j => j.eventId === e.id && j.status === 'posted').length}件</td></tr>`).join('') || '<tr><td colspan="4">イベントはまだ登録されていません．</td></tr>'}</tbody></table></div>`;
    }
    private balanceLabel(value: number) { return value === 0 ? yen(0) : `${value > 0 ? '借' : '貸'} ${yen(Math.abs(value))}`; }

    private journalTable(entries: JournalEntry[], selectable = false): string {
        if (!entries.length) return `<div class="ac-empty"><span class="ac-empty-icon">≡</span><h3>表示する仕訳はありません</h3><p>証憑から仕訳を作成するか，手入力・CSVで登録してください．</p>${this.button('new-entry', '最初の仕訳を入力')}</div>`;
        return `<div class="ac-table-wrap"><table class="ac-table"><thead><tr>${selectable ? `<th class="ac-select-cell"><input type="checkbox" id="ac-select-all" aria-label="表示中の下書きをすべて選択" ${entries.some(e => e.status === 'draft') ? '' : 'disabled'} ${entries.some(e => e.status === 'draft') && entries.filter(e => e.status === 'draft').every(e => this.selectedDrafts.has(e.id)) ? 'checked' : ''}></th>` : ''}<th>取引日・状態</th><th>摘要／イベント</th><th>借方</th><th>貸方</th><th class="ac-number">金額</th><th>証憑</th><th></th></tr></thead><tbody>${entries.map(e => `<tr>${selectable ? `<td class="ac-select-cell">${e.status === 'draft' ? `<input type="checkbox" data-select-draft="${escape(e.id)}" aria-label="${escape(e.description)}を選択" ${this.selectedDrafts.has(e.id) ? 'checked' : ''}>` : ''}</td>` : ''}<td><span class="ac-date">${e.date}</span><span class="ac-badge ${e.status}">${e.status === 'posted' ? '記帳済み' : '下書き'}</span></td><td><strong>${escape(e.description)}</strong><small>${escape(this.book?.events.find(v => v.id === e.eventId)?.name || (e.eventId ? `イベント ${e.eventId}` : '共通'))}${e.person ? ` · 担当 ${escape(e.person)}` : ''}</small></td><td>${e.debit.map(l => `<span>${escape(l.account)}${l.subaccount ? `<small>${escape(l.subaccount)}</small>` : ''}</span>`).join('<br>')}</td><td>${e.credit.map(l => `<span>${escape(l.account)}${l.subaccount ? `<small>${escape(l.subaccount)}</small>` : ''}</span>`).join('<br>')}</td><td class="ac-number">${yen(sum(e.debit))}</td><td>${e.receiptId ? '<span class="ac-linked">あり</span>' : '—'}</td><td>${this.button('edit', e.status === 'draft' ? '確認・編集' : '詳細', `data-id="${escape(e.id)}"`)}</td></tr>`).join('')}</tbody></table></div>`;
    }

    private receipts(b: Book) {
        const receipt = b.receipts.find(r => r.id === this.selectedReceipt);
        const linked = receipt && b.entries.find(e => e.receiptId === receipt.id);
        return `<div class="ac-section-heading"><div><h2>原本と仕訳をつなぐ <span class="ac-muted">${b.receipts.length}件</span></h2><p>JPEG・PNG・WebP・PDF · 1件30MiBまで · OCRは単ページPDFに対応</p></div>${this.button('upload', '証憑を追加')}</div><div class="ac-receipts"><div class="ac-receipt-list">${b.receipts.map(r => { const e = b.entries.find(e => e.receiptId === r.id); return `<button type="button" class="ac-receipt-item ${r.id === this.selectedReceipt ? 'is-active' : ''}" data-receipt="${escape(r.id)}"><span class="ac-file-icon">${r.mediaType === 'application/pdf' ? 'PDF' : 'IMG'}</span><span><strong>${escape(r.name)}</strong><small>${(r.size / 1024).toFixed(0)} KB · ${e?.status === 'posted' ? '記帳済み' : e ? '仕訳下書きあり' : '未仕訳'}</small></span></button>`; }).join('') || '<div class="ac-empty"><h3>証憑を追加してください</h3><p>原本を保存してから，OCRまたは手入力で仕訳を作成します．</p></div>'}</div><div class="ac-receipt-detail">${receipt ? `<div class="ac-section-heading"><div><h3>${escape(receipt.name)}</h3><small class="ac-hash" title="${receipt.sha256}">SHA-256 ${receipt.sha256.slice(0, 20)}…</small></div><div class="ac-actions">${this.button('ocr', receipt.observation ? 'OCRを再実行' : 'OCRで読み取る', `data-id="${receipt.id}"`)}${this.button(linked ? 'edit' : 'receipt-entry', linked ? '仕訳を開く' : '仕訳を作成', `data-id="${linked?.id ?? receipt.id}"`)}</div></div><div id="ac-receipt-preview" class="ac-receipt-preview" aria-label="証憑原本プレビュー">原本を読み込んでいます…</div>${receipt.error ? `<p class="ac-notice is-error">OCR: ${escape(receipt.error)}<br>原本は保存されています．手入力で仕訳を作成できます．</p>` : ''}${this.observation(receipt)}` : '<div class="ac-empty"><h3>左の証憑を選択</h3><p>原本を表示し，読み取った金額や日付を確認できます．</p></div>'}</div></div>`;
    }
    private observation(r: Receipt) {
        const o = r.observation;
        if (!o) return '<p class="ac-muted">OCRの未実行・失敗時も，原本を見ながら仕訳を入力できます．</p>';
        return `<div class="ac-observed"><h3>OCRの読み取り結果 <span class="ac-badge draft">要確認${r.ocr?.engine === 'mock' ? ' · モック' : ''}</span></h3><dl><div><dt>店舗</dt><dd>${escape(o.merchant.value || '未検出')}</dd></div><div><dt>日付</dt><dd>${escape(o.transaction_date.value || '未検出')}</dd></div><div><dt>合計</dt><dd>${o.total_amount.value == null ? '未検出' : yen(o.total_amount.value)}</dd></div><div><dt>支払方法</dt><dd>${escape(o.payment_method.value || '未検出')}</dd></div></dl>${(o.warnings ?? []).map(w => `<p class="ac-warning">${escape(w.message)}</p>`).join('')}<details><summary>OCRテキストを確認</summary><pre>${escape(r.ocr?.pages.flatMap(p => p.blocks.map(b => b.text)).join('\n') || '')}</pre></details><p class="ac-muted">自動抽出値は原本とは別に保持します．用途・勘定科目・金額は仕訳画面で確認してください．</p></div>`;
    }

    private reports(b: Book, entries: JournalEntry[]) {
        const pl = profitLoss(b, entries), bs = balanceSheet(b, this.filter);
        const eventRows = [...b.events.map(e => ({ id: e.id, name: e.name })), { id: '', name: '共通（イベント未指定）' }].filter(e => !this.filter.eventId || e.id === this.filter.eventId);
        const months = [...new Set(b.entries.filter(e => e.status === 'posted').map(e => e.date.slice(0, 7)))].sort().filter(m => !this.filter.month || m === this.filter.month);
        const summaryTable = (rows: { label: string; revenue: number; expense: number; profit: number }[]) => `<div class="ac-table-wrap"><table class="ac-table"><thead><tr><th>対象</th><th class="ac-number">収入</th><th class="ac-number">支出</th><th class="ac-number">収支差額</th></tr></thead><tbody>${rows.map(r => `<tr><td>${escape(r.label)}</td><td class="ac-number">${yen(r.revenue)}</td><td class="ac-number">${yen(r.expense)}</td><td class="ac-number ${r.profit < 0 ? 'ac-negative' : ''}">${yen(r.profit)}</td></tr>`).join('') || '<tr><td colspan="4">記帳済みデータがありません．</td></tr>'}</tbody></table></div>`;
        return `<div class="ac-report-grid"><article class="ac-report"><h2>損益計算書 <span>選択期間</span></h2><div class="ac-report-rows">${pl.totals.map(r => `<div><span>${escape(r.account.name)}<small>${groupNames[r.account.group]}</small></span><strong>${yen(r.account.group === 'expense' ? r.debit - r.credit : r.credit - r.debit)}</strong></div>`).join('')}<div class="ac-total"><span>収入合計</span><strong>${yen(pl.revenue)}</strong></div><div><span>支出合計</span><strong>${yen(pl.expense)}</strong></div><div class="ac-report-bottom"><span>収支差額</span><strong class="${pl.profit < 0 ? 'ac-negative' : ''}">${yen(pl.profit)}</strong></div></div></article><article class="ac-report"><h2>貸借対照表 <span>${this.filter.month ? `${this.filter.month} 月末累計` : '全期間累計'}</span></h2><p class="ac-muted">選択月以前の仕訳を累計します．${this.filter.eventId ? '選択イベント内の取引のみです．' : '全イベントの残高です．'}</p><div class="ac-report-rows">${bs.totals.filter(r => ['asset', 'liability', 'equity'].includes(r.account.group)).map(r => `<div><span>${escape(r.account.name)}<small>${groupNames[r.account.group]}</small></span><strong>${yen(r.account.group === 'asset' ? r.debit - r.credit : r.credit - r.debit)}</strong></div>`).join('')}<div class="ac-total"><span>資産合計</span><strong>${yen(bs.assets)}</strong></div><div><span>負債・純資産</span><strong>${yen(bs.liabilities + bs.equity)}</strong></div><div><span>累計損益</span><strong>${yen(bs.profit)}</strong></div><div class="ac-report-bottom"><span>貸借差額</span><strong class="${bs.difference ? 'ac-negative' : ''}">${yen(bs.difference)}</strong></div></div></article></div><div class="ac-section-heading"><h2>イベント別PL</h2></div>${summaryTable(eventRows.map(e => ({ label: e.name, ...profitLoss(b, entries.filter(j => j.eventId === e.id)) })))}<div class="ac-section-heading"><h2>月次PL</h2></div>${summaryTable(months.map(month => ({ label: month, ...profitLoss(b, filteredEntries(b, { month, eventId: this.filter.eventId })) })))}`;
    }

    private editor(e: JournalEntry) {
        const posted = e.status === 'posted';
        const side = (name: 'debit' | 'credit', title: string) => `<section class="ac-entry-side"><h3>${title}</h3>${e[name].map((line, i) => `<div class="ac-entry-line" data-side="${name}" data-index="${i}"><label>勘定科目<select data-line-field="account" ${posted ? 'disabled' : ''}>${this.options(line.account)}</select></label><label>補助科目<input data-line-field="subaccount" value="${escape(line.subaccount)}" ${posted ? 'disabled' : ''} placeholder="用途・担当者など"></label><label>金額（円）<input type="number" min="1" max="1000000000000" step="1" data-line-field="amount" value="${line.amount || ''}" ${posted ? 'disabled' : ''} required></label>${!posted && e[name].length > 1 ? this.button('remove-line', '×', `data-side="${name}" data-index="${i}" aria-label="行を削除"`) : ''}</div>`).join('')}${posted ? '' : this.button('add-line', '＋ 行を追加', `data-side="${name}"`)}</section>`;
        const r = this.book!.receipts.find(r => r.id === e.receiptId);
        return `<div class="ac-modal-backdrop"><section class="ac-entry-dialog" ${this.confirmation ? 'inert' : ''} role="dialog" aria-modal="true" aria-labelledby="ac-entry-title"><header><div><span class="ac-eyebrow">JOURNAL ENTRY</span><h2 id="ac-entry-title">${posted ? '記帳済み仕訳' : e.id ? '下書きの確認' : '新しい仕訳'}</h2></div>${this.button('close-entry', '閉じる')}</header><form id="ac-entry-form">${r ? '<details class="ac-editor-original" open><summary>証憑原本を確認</summary><div id="ac-entry-receipt-preview" class="ac-receipt-preview">原本を読み込んでいます…</div></details>' : ''}<div class="ac-entry-fields"><label>取引発生日<input name="date" type="date" value="${e.date}" required ${posted ? 'disabled' : ''}></label><label>イベント<select name="eventId" ${posted ? 'disabled' : ''}>${this.options(e.eventId, true).replace('すべてのイベント', '共通（イベント未指定）')}</select></label><label>担当者ID<input name="person" value="${escape(e.person)}" ${posted ? 'disabled' : ''}></label><label class="ac-field-wide">摘要・実際の用途<input name="description" value="${escape(e.description)}" placeholder="何に使った支出・収入かを記録" required ${posted ? 'disabled' : ''}></label></div>${r ? `<div class="ac-linked-receipt">証憑: ${escape(r.name)}${r.observation?.total_amount.value != null ? ` · OCR合計 ${yen(r.observation.total_amount.value)}` : ''}<small>原本と読み取り結果を確認し，必要に応じて金額を修正してください．</small></div>` : ''}<div class="ac-entry-sides">${side('debit', '借方')}${side('credit', '貸方')}</div><div class="ac-entry-balance" id="ac-entry-balance">${this.entryBalance(e)}</div><p class="ac-muted">税込金額の整数円で入力します．記帳後は変更できず，訂正には反対仕訳を使用します．</p><p id="ac-entry-error" class="ac-form-error" role="alert"></p><footer>${posted ? this.button('reverse', '反対仕訳を作成') : `${e.id ? this.button('delete-draft', '下書きを削除') : '<span></span>'}<div class="ac-actions"><button type="submit" name="save-draft" class="ac-button" ${this.busy ? 'disabled' : ''}>下書きを保存</button><button type="submit" name="post" class="ac-button ac-primary" ${this.busy ? 'disabled' : ''}>確認して記帳</button></div>`}</footer></form></section></div>`;
    }
    private entryBalance(e: JournalEntry) { const debit = sum(e.debit), credit = sum(e.credit); return `<span>借方合計 <strong>${yen(debit)}</strong></span><span>貸方合計 <strong>${yen(credit)}</strong></span><span class="${debit !== credit ? 'ac-negative' : ''}">差額 <strong>${yen(debit - credit)}</strong></span>`; }

    private async loadPreview(receiptId: string, selector: string) {
        const token = ++this.previewToken, receipt = this.book?.receipts.find(r => r.id === receiptId);
        if (!receipt) return;
        try {
            const url = await this.api.GetAccountingReceiptData(receipt.id);
            if (token !== this.previewToken || this.destroyed) return;
            const target = this.element?.querySelector(selector); if (!target) return;
            const media = document.createElement(receipt.mediaType === 'application/pdf' ? 'iframe' : 'img');
            if (media instanceof HTMLImageElement) { media.alt = receipt.name; media.src = url; }
            else { media.title = receipt.name; media.src = url; }
            target.replaceChildren(media);
        } catch (error) { const target = this.element?.querySelector(selector); if (target && token === this.previewToken) target.textContent = String(error); }
    }

    private input(event: Event) {
        const target = event.target as HTMLInputElement;
        if (target.id === 'ac-search') {
            this.search = target.value;
            this.pruneSelection(); this.updateJournal(); return;
        }
        this.captureDraft();
    }
    private captureDraft() {
        if (!this.draft || this.draft.status === 'posted') return;
        const form = this.element?.querySelector<HTMLFormElement>('#ac-entry-form'); if (!form) return;
        for (const name of ['date', 'description', 'eventId', 'person'] as const) this.draft[name] = (form.elements.namedItem(name) as HTMLInputElement).value;
        for (const side of ['debit', 'credit'] as const) {
            this.draft[side] = Array.from(form.querySelectorAll<HTMLElement>(`.ac-entry-line[data-side="${side}"]`)).map(row => ({
                account: row.querySelector<HTMLSelectElement>('[data-line-field="account"]')!.value,
                subaccount: row.querySelector<HTMLInputElement>('[data-line-field="subaccount"]')!.value,
                amount: Number(row.querySelector<HTMLInputElement>('[data-line-field="amount"]')!.value),
            }));
        }
        const balance = form.querySelector('#ac-entry-balance'); if (balance) balance.innerHTML = this.entryBalance(this.draft);
    }

    private async change(event: Event) {
        const target = event.target as HTMLInputElement;
        if (target.id === 'ac-select-all') {
            this.selectedDrafts = target.checked ? new Set(this.visibleEntries().filter(e => e.status === 'draft').map(e => e.id)) : new Set();
            this.updateSelection();
        } else if (target.dataset.selectDraft) {
            if (target.checked) this.selectedDrafts.add(target.dataset.selectDraft); else this.selectedDrafts.delete(target.dataset.selectDraft);
            this.updateSelection();
        } else if (target.dataset.filter) { this.filter[target.dataset.filter as keyof Filter] = target.value; this.render(); }
        else if (target.id === 'ac-ledger-account') { this.account = target.value; this.render(); }
        else if (target.id === 'ac-receipt-upload') {
            const files = Array.from(target.files ?? []);
            await this.importReceipts(files);
        } else if (target.id === 'ac-csv-upload' || target.id === 'ac-events-upload') {
            const file = target.files?.[0]; if (!file || !this.book) return;
            const kind = target.id;
            await this.run(async () => {
                if (file.size > 10 * 1024 * 1024) throw new Error('CSVの上限は10MiBです．');
                const text = await file.text();
                this.book = kind === 'ac-csv-upload' ? await this.api.ImportAccountingJournalCSV(text, this.book!.revision) : await this.api.ImportAccountingEventsCSV(text, this.book!.revision);
                this.view = kind === 'ac-csv-upload' ? 'journal' : 'events';
            }, kind === 'ac-csv-upload' ? 'CSVを下書きとして取り込みました．一覧で選択し，一括承認できます．' : 'イベント一覧を取り込みました．');
        } else this.captureDraft();
    }

    private async importReceipts(files: File[]) {
        if (!files.length) return;
        await this.run(async () => {
            this.view = 'receipts';
            for (const file of files) {
                if (file.size > 30 * 1024 * 1024) throw new Error('証憑の上限は30MiBです．');
                const encoded = await new Promise<string>((resolve, reject) => { const reader = new FileReader(); reader.onload = () => resolve(String(reader.result).split(',')[1]!); reader.onerror = () => reject(new Error('ファイルを読み取れません．')); reader.readAsDataURL(file); });
                this.book = await this.api.ImportAccountingReceipt(file.name, encoded); this.selectedReceipt = this.book.receipts.slice(-1)[0]!.id;
            }
        }, '証憑原本を保存しました．内容を確認して仕訳を作成してください．');
    }

    private async click(event: MouseEvent) {
        const button = (event.target as Element).closest<HTMLButtonElement>('button'); if (!button || this.busy) return;
        if (button.dataset.view) { this.view = button.dataset.view as View; this.render(); return; }
        if (button.dataset.receipt) { this.selectedReceipt = button.dataset.receipt; this.render(); return; }
        const action = button.dataset.action;
        if (this.confirmation) { if (action === 'confirm-accept' || action === 'confirm-cancel') this.resolveConfirmation(action === 'confirm-accept'); return; }
        if (action === 'upload') this.element?.querySelector<HTMLInputElement>('#ac-receipt-upload')?.click();
        else if (action === 'import-csv') this.element?.querySelector<HTMLInputElement>('#ac-csv-upload')?.click();
        else if (action === 'import-events') this.element?.querySelector<HTMLInputElement>('#ac-events-upload')?.click();
        else if (action === 'reload') await this.reload();
        else if (action === 'clear-filter') { this.filter = { month: '', eventId: '' }; this.render(); }
        else if (action === 'go-journal') { this.view = 'journal'; this.render(); }
        else if (action === 'new-entry' && this.book) { const entry = emptyEntry(); entry.eventId = this.filter.eventId; this.openDraft(entry); }
        else if (action === 'edit') { this.openDraft(this.book!.entries.find(e => e.id === button.dataset.id)!); }
        else if (action === 'close-entry') {
            this.captureDraft();
            if (this.isDirty() && !await this.ask('入力を閉じますか', '保存していない変更を破棄して閉じます．', '破棄して閉じる')) return;
            this.draft = null; this.render();
        } else if (action === 'add-line' && this.draft) { this.draft[button.dataset.side as 'debit' | 'credit'].push({ account: '消耗品費', subaccount: '', amount: 0 }); this.render(); }
        else if (action === 'remove-line' && this.draft) { this.draft[button.dataset.side as 'debit' | 'credit'].splice(Number(button.dataset.index), 1); this.render(); }
        else if (action === 'reverse' && this.draft) { this.draft = { ...this.draft, id: '', receiptId: '', status: 'draft', date: localDate(), description: `訂正（反対仕訳）: ${this.draft.description}`, debit: structuredClone(this.draft.credit), credit: structuredClone(this.draft.debit), sourceKey: undefined, sourceId: undefined }; this.openDraft(this.draft); }
        else if (action === 'delete-draft' && this.draft?.id) {
            const id = this.draft.id;
            if (await this.ask('下書きを削除しますか', `「${this.draft.description}」を削除します．証憑原本は保存されます．`, '削除する'))
                await this.run(async () => { this.book = await this.api.DeleteAccountingDraft(id, this.book!.revision); this.draft = null; }, '下書きを削除しました．');
        } else if (action === 'select-csv') {
            this.selectedDrafts = new Set(this.visibleEntries().filter(e => e.status === 'draft' && e.sourceKey).map(e => e.id)); this.updateJournal();
        } else if (action === 'clear-selection') { this.selectedDrafts.clear(); this.updateJournal(); }
        else if (action === 'bulk-post') {
            const entries = this.visibleEntries().filter(e => e.status === 'draft' && this.selectedDrafts.has(e.id));
            if (!entries.length) return;
            const revision = this.book!.revision;
            if (await this.ask('下書きを一括承認', `選択した${entries.length}件を記帳し，集計へ反映します．記帳後の訂正には反対仕訳を使用します．`, `${entries.length}件を承認して記帳`, entries))
                await this.run(async () => { this.book = await this.api.PostAccountingDrafts(entries.map(e => e.id), revision); this.selectedDrafts.clear(); }, `${entries.length}件の下書きを承認して記帳しました．`);
        }
        else if (action === 'ocr') await this.run(async () => { this.book = await this.api.ExtractAccountingReceipt(button.dataset.id!); const r = this.book.receipts.find(r => r.id === button.dataset.id); if (r?.error) throw new Error(r.error); }, 'OCR結果を保存しました．原本と照合してください．');
        else if (action === 'receipt-entry') {
            const r = this.book!.receipts.find(r => r.id === button.dataset.id)!; const obs = r.observation;
            this.draft = emptyEntry(); this.draft.receiptId = r.id; this.draft.date = obs?.transaction_date.value || '';
            this.draft.description = obs?.merchant.value ? `${obs.merchant.value} / ` : '';
            this.draft.debit[0]!.amount = this.draft.credit[0]!.amount = obs?.total_amount.value ?? 0;
            if (obs?.payment_method.value === 'credit_card') this.draft.credit[0]!.account = '未払金';
            this.draft.eventId = this.filter.eventId; this.openDraft(this.draft);
        } else if (action === 'export-csv') await this.download(journalCSV(this.visibleEntries()), `karte-journal-${localDate()}.csv`, 'text/csv;charset=utf-8');
        else if (action === 'backup') await this.download(JSON.stringify(this.book, null, 2), `karte-accounting-${localDate()}.json`, 'application/json');
    }

    private async submit(event: SubmitEvent) {
        const form = event.target as HTMLFormElement;
        if (form.getAttribute('id') === 'ac-event-form') {
            const data = new FormData(form);
            await this.run(async () => { this.book = await this.api.SaveAccountingEvent({ id: String(data.get('id')).trim(), name: String(data.get('name')).trim(), date: String(data.get('date')) }, this.book!.revision); }, 'イベントを保存しました．');
        } else if (form.getAttribute('id') === 'ac-entry-form' && this.draft) {
            this.captureDraft(); const draft = structuredClone(this.draft);
            draft.status = (event.submitter as HTMLButtonElement)?.name === 'post' ? 'posted' : 'draft';
            const error = validateEntry(this.book!, draft);
            if (error) { form.querySelector('#ac-entry-error')!.textContent = error; return; }
            await this.run(async () => { this.book = await this.api.SaveAccountingEntry(draft, this.book!.revision); this.draft = null; }, draft.status === 'posted' ? '仕訳を記帳しました．集計に反映しました．' : '下書きを保存しました．集計にはまだ含まれません．');
        }
    }
    private visibleEntries() {
        return filteredEntries(this.book!, this.filter, false).filter(e => `${e.description} ${e.person} ${e.debit.map(l => l.account).join(' ')} ${e.credit.map(l => l.account).join(' ')}`.toLowerCase().includes(this.search.toLowerCase()));
    }
    private pruneSelection() {
        if (!this.book) return;
        const visible = new Set(this.visibleEntries().filter(e => e.status === 'draft').map(e => e.id));
        this.selectedDrafts = new Set([...this.selectedDrafts].filter(id => visible.has(id)));
    }
    private bulkControls() {
        const csvCount = this.visibleEntries().filter(e => e.status === 'draft' && e.sourceKey).length;
        return `<div class="ac-bulk-bar"><span role="status">下書き ${this.selectedDrafts.size}件を選択</span><div class="ac-actions">${this.button('select-csv', 'CSV取込の下書きを選択', csvCount ? '' : 'disabled')}${this.button('clear-selection', '選択を解除', this.selectedDrafts.size ? '' : 'disabled')}${this.button('bulk-post', `選択した下書きを一括承認（${this.selectedDrafts.size}件）`, `${this.selectedDrafts.size ? '' : 'disabled'}`)}</div><small>表示中の下書きだけを選択・承認します．月・イベント・検索を変更すると，対象外の選択を解除します．</small></div>`;
    }
    private updateJournal() {
        const entries = this.visibleEntries();
        const table = this.element?.querySelector('#ac-journal-table'); if (table) table.innerHTML = this.journalTable(entries.slice().reverse(), true);
        this.updateSelection();
        const count = this.element?.querySelector('#ac-journal-count'); if (count) count.textContent = `${entries.length}件`;
    }
    private updateSelection() {
        const controls = this.element?.querySelector('#ac-bulk-controls'); if (controls) controls.innerHTML = this.bulkControls();
        this.element?.querySelectorAll<HTMLInputElement>('[data-select-draft]').forEach(input => { input.checked = this.selectedDrafts.has(input.dataset.selectDraft!); });
        const all = this.element?.querySelector<HTMLInputElement>('#ac-select-all');
        if (all) {
            const count = this.visibleEntries().filter(e => e.status === 'draft').length;
            all.checked = count > 0 && this.selectedDrafts.size === count;
            all.indeterminate = this.selectedDrafts.size > 0 && this.selectedDrafts.size < count;
        }
    }
    private openDraft(entry: JournalEntry) { this.draft = structuredClone(entry); this.draftBaseline = JSON.stringify(this.draft); this.render(); }
    private isDirty() { return !!this.draft && this.draft.status !== 'posted' && JSON.stringify(this.draft) !== this.draftBaseline; }
    private ask(title: string, text: string, accept: string, entries?: JournalEntry[]): Promise<boolean> {
        if (this.confirmation) return Promise.resolve(false);
        this.captureDraft();
        return new Promise(resolve => { this.confirmation = { title, text, accept, entries, resolve }; this.render(); });
    }
    private confirmationDialog() {
        const c = this.confirmation!;
        return `<div class="ac-modal-backdrop ac-confirm-backdrop"><section class="ac-confirm-dialog" role="alertdialog" aria-modal="true" aria-labelledby="ac-confirm-title" aria-describedby="ac-confirm-description"><h2 id="ac-confirm-title">${escape(c.title)}</h2><p id="ac-confirm-description">${escape(c.text)}</p>${c.entries ? `<p class="ac-confirm-total">借方・貸方合計 各 ${yen(c.entries.reduce((total, e) => total + sum(e.debit), 0))}</p><div class="ac-confirm-list">${c.entries.map(e => `<div><span>${escape(e.date)} · ${escape(e.description)}</span><strong>${yen(sum(e.debit))}</strong></div>`).join('')}</div>` : ''}<div class="ac-actions">${this.button('confirm-cancel', 'キャンセル')}${this.button('confirm-accept', escape(c.accept))}</div></section></div>`;
    }
    private resolveConfirmation(accepted: boolean) { const confirmation = this.confirmation; this.confirmation = null; this.render(); confirmation?.resolve(accepted); }
    private keydown(event: KeyboardEvent) {
        if (!this.draft && !this.confirmation) return;
        if (event.key === 'Escape') {
            event.preventDefault(); event.stopPropagation();
            if (!this.busy) { if (this.confirmation) this.resolveConfirmation(false); else this.element?.querySelector<HTMLButtonElement>('[data-action="close-entry"]')?.click(); }
        } else if (event.key === 'Tab') {
            const modal = this.element?.querySelector(this.confirmation ? '.ac-confirm-dialog' : '.ac-entry-dialog');
            const controls = Array.from(modal?.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), select:not(:disabled), summary') ?? []).filter(el => el.getClientRects().length);
            const first = controls[0], last = controls[controls.length - 1];
            if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
            else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
        }
    }
    private async download(text: string, name: string, type: string) {
        if (this.api.SaveAccountingExport) {
            await this.run(async () => { const path = await this.api.SaveAccountingExport!(text, name); this.message = path ? `保存しました: ${path}` : '保存をキャンセルしました．'; });
            return;
        }
        const url = URL.createObjectURL(new Blob([text], { type })); const a = document.createElement('a'); a.href = url; a.download = name; a.click(); setTimeout(() => URL.revokeObjectURL(url), 1000);
    }
    async confirmClose(): Promise<boolean> {
        if (this.busy) { this.message = '会計データの処理中です．完了してから閉じてください．'; useUIStore.getState().setActiveTab('accounting'); this.render(); return false; }
        if (this.confirmation) return false;
        this.captureDraft();
        if (this.isDirty()) {
            useUIStore.getState().setActiveTab('accounting');
            if (!await this.ask('Karteを閉じますか', '会計の保存していない変更を破棄して終了します．', '破棄して終了')) return false;
        }
        this.draft = null; return true;
    }
    destroy(): void { this.confirmation?.resolve(false); this.confirmation = null; this.destroyed = true; this.previewToken++; this.cleanup.forEach(fn => fn()); this.cleanup = []; }
}
