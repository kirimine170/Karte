export type AccountGroup = 'asset' | 'liability' | 'equity' | 'revenue' | 'expense';
export interface Account { name: string; group: AccountGroup; normal: 'debit' | 'credit' }
export interface JournalLine { account: string; subaccount: string; amount: number }
export interface JournalEntry {
    id: string; date: string; description: string; eventId: string; person: string;
    receiptId: string; status: 'draft' | 'posted'; debit: JournalLine[]; credit: JournalLine[];
    updatedAt: string; sourceKey?: string; sourceId?: string;
}
export interface AccountingEvent { id: string; name: string; date: string }
interface Observed<T> { value: T | null; confidence: number }
export interface Receipt {
    id: string; name: string; sha256: string; mediaType: string; size: number; importedAt: string; error?: string;
    observation?: {
        merchant: Observed<string>; transaction_date: Observed<string>; total_amount: Observed<number>;
        payment_method: Observed<string>; warnings: { message: string }[];
        items: { name: string; amount?: number; usage_category: string }[];
    };
    ocr?: { engine: string; pages: { blocks: { text: string }[] }[] };
}
export interface Book { version: number; revision: number; accounts: Account[]; entries: JournalEntry[]; events: AccountingEvent[]; receipts: Receipt[] }
export interface Filter { month: string; eventId: string }
export const MAX_AMOUNT = 1_000_000_000_000;
export const yen = (value: number) => new Intl.NumberFormat('ja-JP', { style: 'currency', currency: 'JPY', maximumFractionDigits: 0 }).format(value);
export const sum = (lines: JournalLine[]) => lines.reduce((total, line) => total + line.amount, 0);
export function localDate(date = new Date()): string {
    return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`;
}
export function emptyEntry(): JournalEntry {
    return { id: '', date: localDate(), description: '', eventId: '', person: '', receiptId: '', status: 'draft',
        debit: [{ account: '消耗品費', subaccount: '', amount: 0 }], credit: [{ account: '現金', subaccount: '', amount: 0 }], updatedAt: '' };
}
export function validateEntry(book: Book, entry: JournalEntry): string {
    if (!/^\d{4}-\d{2}-\d{2}$/.test(entry.date) || !Number.isFinite(Date.parse(entry.date)) || new Date(entry.date).toISOString().slice(0, 10) !== entry.date) return '正しい取引発生日を入力してください．';
    if (!entry.description.trim()) return '摘要を入力してください．';
    if (entry.eventId && !book.events.some(event => event.id === entry.eventId)) return 'イベントを登録してから選択してください．';
    for (const lines of [entry.debit, entry.credit]) {
        if (!lines.length || lines.length > 100) return '借方・貸方に1〜100行を入力してください．';
        for (const line of lines) {
            if (!book.accounts.some(account => account.name === line.account)) return `未登録の勘定科目です: ${line.account}`;
            if (!Number.isSafeInteger(line.amount) || line.amount <= 0 || line.amount > MAX_AMOUNT) return '金額は正の整数円で入力してください．';
        }
        if (sum(lines) > MAX_AMOUNT) return '仕訳合計は1兆円以下にしてください．';
    }
    if (sum(entry.debit) !== sum(entry.credit)) return '借方と貸方の合計を一致させてください．';
    if (entry.receiptId && (!book.receipts.some(r => r.id === entry.receiptId) || book.entries.some(e => e.id !== entry.id && e.receiptId === entry.receiptId))) return '証憑が存在しないか，既に仕訳に使用されています．';
    return '';
}
export function filteredEntries(book: Book, filter: Filter, postedOnly = true): JournalEntry[] {
    return book.entries.filter(e => (!postedOnly || e.status === 'posted') && (!filter.month || e.date.startsWith(filter.month)) && (!filter.eventId || e.eventId === filter.eventId))
        .sort((a, b) => a.date.localeCompare(b.date) || a.updatedAt.localeCompare(b.updatedAt));
}
export interface AccountTotal { account: Account; debit: number; credit: number; net: number }
export function accountTotals(book: Book, entries: JournalEntry[]): AccountTotal[] {
    return book.accounts.map(account => {
        const debit = entries.reduce((total, e) => total + sum(e.debit.filter(l => l.account === account.name)), 0);
        const credit = entries.reduce((total, e) => total + sum(e.credit.filter(l => l.account === account.name)), 0);
        return { account, debit, credit, net: account.normal === 'debit' ? debit - credit : credit - debit };
    }).filter(row => row.debit !== 0 || row.credit !== 0);
}
export function profitLoss(book: Book, entries: JournalEntry[]) {
    const totals = accountTotals(book, entries);
    const revenue = totals.filter(r => r.account.group === 'revenue').reduce((t, r) => t + r.credit - r.debit, 0);
    const expense = totals.filter(r => r.account.group === 'expense').reduce((t, r) => t + r.debit - r.credit, 0);
    return { revenue, expense, profit: revenue - expense, totals: totals.filter(r => r.account.group === 'revenue' || r.account.group === 'expense') };
}
export function balanceSheet(book: Book, filter: Filter) {
    const entries = book.entries.filter(e => e.status === 'posted' && (!filter.eventId || e.eventId === filter.eventId) && (!filter.month || e.date.slice(0, 7) <= filter.month));
    const totals = accountTotals(book, entries);
    const group = (name: AccountGroup) => totals.filter(r => r.account.group === name).reduce((t, r) => t + (name === 'asset' ? r.debit - r.credit : r.credit - r.debit), 0);
    const profit = profitLoss(book, entries).profit;
    const assets = group('asset'), liabilities = group('liability'), equity = group('equity');
    return { totals, assets, liabilities, equity, profit, difference: assets - liabilities - equity - profit };
}
export function ledger(book: Book, filter: Filter, accountName: string) {
    const delta = (e: JournalEntry) => sum(e.debit.filter(l => l.account === accountName)) - sum(e.credit.filter(l => l.account === accountName));
    const opening = book.entries.filter(e => e.status === 'posted' && (!filter.eventId || e.eventId === filter.eventId) && filter.month && e.date.slice(0, 7) < filter.month).reduce((t, e) => t + delta(e), 0);
    let balance = opening;
    const rows = filteredEntries(book, filter).filter(e => [...e.debit, ...e.credit].some(l => l.account === accountName)).map(entry => {
        const debit = sum(entry.debit.filter(l => l.account === accountName)), credit = sum(entry.credit.filter(l => l.account === accountName));
        balance += debit - credit;
        return { entry, debit, credit, balance };
    });
    return { opening, rows, balance };
}
const csvValue = (value: string | number) => {
    const text = String(value);
    // Prevent spreadsheet formula execution when a description or ID is opened in another app．
    const safe = /^[=+\-@\t\r]/.test(text) ? `'${text}` : text;
    return `"${safe.replace(/"/g, '""')}"`;
};
export function journalCSV(entries: JournalEntry[]): string {
    const rows: (string | number)[][] = [['仕訳ID', '取引発生日', '勘定科目', '補助科目', '数量', '単価', '金額', '勘定科目', '補助科目', '数量', '単価', '金額', '担当者ID（HackSick_ID）', '摘要', 'イベントID', '状態']];
    for (const entry of entries) {
        // Split a compound entry into balanced pairs without changing its total or account effects．
        let di = 0, ci = 0, dr = entry.debit[0]?.amount ?? 0, cr = entry.credit[0]?.amount ?? 0;
        while (di < entry.debit.length && ci < entry.credit.length) {
            const d = entry.debit[di]!, c = entry.credit[ci]!, amount = Math.min(dr, cr);
            rows.push([entry.id, entry.date, d.account, d.subaccount, 1, amount, amount, c.account, c.subaccount, 1, amount, amount, entry.person, entry.description, entry.eventId, entry.status === 'posted' ? '記帳済み' : '下書き']);
            dr -= amount; cr -= amount;
            if (!dr) { di++; dr = entry.debit[di]?.amount ?? 0; }
            if (!cr) { ci++; cr = entry.credit[ci]?.amount ?? 0; }
        }
    }
    return '\ufeff' + rows.map(row => row.map(csvValue).join(',')).join('\r\n');
}
export function parseCSV(text: string): string[][] {
    const rows: string[][] = []; let row: string[] = [], value = '', quoted = false;
    const input = text.replace(/^\ufeff/, '');
    for (let i = 0; i < input.length; i++) {
        const c = input[i];
        if (c === '"') { if (quoted && input[i + 1] === '"') { value += '"'; i++; } else quoted = !quoted; }
        else if (c === ',' && !quoted) { row.push(value); value = ''; }
        else if ((c === '\n' || c === '\r') && !quoted) { if (c === '\r' && input[i + 1] === '\n') i++; row.push(value); if (row.some(v => v)) rows.push(row); row = []; value = ''; }
        else value += c;
    }
    if (quoted) throw new Error('CSVの引用符が閉じていません．');
    row.push(value); if (row.some(v => v)) rows.push(row);
    return rows;
}
