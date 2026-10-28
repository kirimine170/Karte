import { describe, expect, it } from 'vitest';
import { emptyBook } from './api';
import { accountTotals, balanceSheet, emptyEntry, filteredEntries, journalCSV, ledger, parseCSV, profitLoss, validateEntry, type JournalEntry } from './core';

function entry(date: string, debit: string, credit: string, amount: number, status: 'draft' | 'posted' = 'posted'): JournalEntry {
    return { ...emptyEntry(), id: crypto.randomUUID(), date, description: '検証用取引', status, debit: [{ account: debit, subaccount: '', amount }], credit: [{ account: credit, subaccount: '', amount }] };
}
describe('accounting totals', () => {
    it('excludes drafts and handles refunds and reversals', () => {
        const book = emptyBook();
        book.entries = [entry('2026-09-01', '現金', '売上高', 8000), entry('2026-10-01', '会議費', '現金', 6400), entry('2026-10-02', '現金', '会議費', 400), entry('2026-10-03', '会議費', '現金', 50000, 'draft')];
        const all = filteredEntries(book, { month: '', eventId: '' });
        expect(profitLoss(book, all)).toMatchObject({ revenue: 8000, expense: 6000, profit: 2000 });
        const filter = { month: '2026-10', eventId: '' };
        expect(profitLoss(book, filteredEntries(book, filter)).profit).toBe(-6000);
        expect(ledger(book, filter, '現金')).toMatchObject({ opening: 8000, balance: 2000 });
        expect(balanceSheet(book, filter)).toMatchObject({ assets: 2000, profit: 2000, difference: 0 });
        expect(balanceSheet(book, { month: '2026-09', eventId: '' })).toMatchObject({ assets: 8000, difference: 0 });
    });
    it('separates event results and includes assets/liabilities without treating them as expenses', () => {
        const book = emptyBook(); book.events.push({ id: '0', name: '検証イベント', date: '' });
        const purchase = entry('2026-10-01', '工具器具備品', '短期借入金', 2820); purchase.eventId = '0';
        book.entries = [purchase, entry('2026-10-02', '消耗品費', '現金', 110)];
        const filter = { month: '', eventId: '0' };
        expect(profitLoss(book, filteredEntries(book, filter)).profit).toBe(0);
        expect(balanceSheet(book, filter)).toMatchObject({ assets: 2820, liabilities: 2820, difference: 0 });
    });
    it('rejects mismatched amounts and invalid dates', () => {
        const book = emptyBook(); const e = entry('2026-02-30', '会議費', '現金', 100);
        expect(validateEntry(book, e)).toContain('取引発生日');
        e.date = '2026-02-28'; e.credit[0]!.amount = 200;
        expect(validateEntry(book, e)).toContain('一致');
        e.credit[0]!.amount = e.debit[0]!.amount = .5;
        expect(validateEntry(book, e)).toContain('整数円');
    });
});
describe('spreadsheet CSV', () => {
    it('preserves account effects when splitting compound entries into balanced rows', () => {
        const book = emptyBook(), e = entry('2026-10-26', '会議費', '現金', 1200);
        e.debit = [{ account: '会議費', subaccount: '', amount: 1000 }, { account: '支払手数料', subaccount: '', amount: 200 }];
        const rows = parseCSV(journalCSV([e])).slice(1);
        expect(rows).toHaveLength(2);
        expect(rows.map(row => Number(row[6]))).toEqual([1000, 200]);
        expect(rows.every(row => row[6] === row[11])).toBe(true);
        const reconstructed = rows.map(row => entry(row[1]!, row[2]!, row[7]!, Number(row[6])));
        expect(accountTotals(book, reconstructed)).toEqual(accountTotals(book, [e]));
    });
    it('handles quoted commas/newlines and protects formulas in exported fields', () => {
        const e = entry('2026-10-26', '会議費', '現金', 100); e.description = '=IMPORTXML("test", "test")\n用途';
        const rows = parseCSV(journalCSV([e]));
        expect(rows[1]![13]).toBe(`'${e.description}`);
        expect(() => parseCSV('"unterminated')).toThrow();
    });
});
