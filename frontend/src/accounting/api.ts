import initialAccounts from '../../../internal/accounting/accounts.json';
import { parseCSV, validateEntry, type Account, type Book, type JournalEntry, type AccountingEvent } from './core';

export interface AccountingAPI {
    GetAccountingData(): Promise<Book>;
    SaveAccountingEntry(entry: JournalEntry, revision: number): Promise<Book>;
    DeleteAccountingDraft(id: string, revision: number): Promise<Book>;
    PostAccountingDrafts(ids: string[], revision: number): Promise<Book>;
    SaveAccountingExport?(content: string, name: string): Promise<string>;
    SaveAccountingEvent(event: AccountingEvent, revision: number): Promise<Book>;
    ImportAccountingReceipt(name: string, base64: string): Promise<Book>;
    GetAccountingReceiptData(id: string): Promise<string>;
    ExtractAccountingReceipt(id: string): Promise<Book>;
    ImportAccountingJournalCSV(text: string, revision: number): Promise<Book>;
    ImportAccountingEventsCSV(text: string, revision: number): Promise<Book>;
}
export const isNativeAccounting = () => !!(window as Window & { go?: unknown }).go;
export function getAccountingAPI(): AccountingAPI {
    const native = (window as Window & { go?: { main: { App: AccountingAPI } } }).go;
    return native ? native.main.App : new BrowserAccountingAPI();
}
export function emptyBook(): Book {
    return { version: 1, revision: 0, accounts: initialAccounts as Account[], entries: [], events: [], receipts: [] };
}
const id = () => crypto.randomUUID();
function openDatabase(): Promise<IDBDatabase> {
    return new Promise((resolve, reject) => {
        const request = indexedDB.open('karte-accounting-poc', 1);
        request.onupgradeneeded = () => request.result.createObjectStore('data');
        request.onsuccess = () => resolve(request.result);
        request.onerror = () => reject(new Error('会計用のブラウザ保存領域を開けません．'));
    });
}

// The browser preview uses IndexedDB．The Wails app uses the Go file service above．
class BrowserAccountingAPI implements AccountingAPI {
    private async transact<T>(work: (store: IDBObjectStore, book: Book) => T, revision?: number): Promise<T> {
        const db = await openDatabase();
        return new Promise((resolve, reject) => {
            const tx = db.transaction('data', 'readwrite'), store = tx.objectStore('data');
            const read = store.get('book'); let result: T; let failure: unknown;
            read.onsuccess = () => {
                try {
                    const book: Book = read.result ?? emptyBook();
                    if (revision !== undefined && book.revision !== revision) throw new Error('他の操作で更新されました．再読み込みしてからやり直してください．');
                    result = work(store, book);
                } catch (error) { failure = error; tx.abort(); }
            };
            tx.oncomplete = () => { db.close(); resolve(result); };
            tx.onabort = tx.onerror = () => { db.close(); reject(failure ?? new Error('会計データの保存に失敗しました．')); };
        });
    }
    private save(store: IDBObjectStore, book: Book): Book { book.revision++; store.put(book, 'book'); return book; }
    GetAccountingData() { return this.transact((_store, book) => book); }
    SaveAccountingEntry(entry: JournalEntry, revision: number) {
        return this.transact((store, book) => {
            const old = book.entries.find(e => e.id === entry.id);
            if (old?.status === 'posted') throw new Error('記帳済みの仕訳は変更できません．');
            if (entry.id && !old) throw new Error('編集対象の仕訳が見つかりません．');
            if (entry.status !== 'draft' && entry.status !== 'posted') throw new Error('仕訳の状態が不正です．');
            const error = validateEntry(book, entry); if (error) throw new Error(error);
            const next = { ...structuredClone(entry), id: old?.id ?? id(), sourceKey: old?.sourceKey, sourceId: old?.sourceId, updatedAt: new Date().toISOString() };
            book.entries = old ? book.entries.map(e => e.id === old.id ? next : e) : [...book.entries, next];
            return this.save(store, book);
        }, revision);
    }
    DeleteAccountingDraft(entryId: string, revision: number) {
        return this.transact((store, book) => {
            const entry = book.entries.find(e => e.id === entryId);
            if (!entry || entry.status !== 'draft') throw new Error('削除できる下書きがありません．');
            book.entries = book.entries.filter(e => e.id !== entryId); return this.save(store, book);
        }, revision);
    }
    PostAccountingDrafts(ids: string[], revision: number) {
        return this.transact((store, book) => {
            if (!ids.length || new Set(ids).size !== ids.length) throw new Error('承認する下書きを重複なく選択してください．');
            const selected = ids.map(id => {
                const entry = book.entries.find(e => e.id === id);
                if (!entry || entry.status !== 'draft') throw new Error('承認対象の下書きが更新・削除されています．再読み込みしてください．');
                const error = validateEntry(book, entry); if (error) throw new Error(`${entry.description}: ${error}`);
                return entry;
            });
            const updatedAt = new Date().toISOString();
            for (const entry of selected) { entry.status = 'posted'; entry.updatedAt = updatedAt; }
            return this.save(store, book);
        }, revision);
    }
    SaveAccountingEvent(event: AccountingEvent, revision: number) {
        return this.transact((store, book) => {
            if (!event.id.trim() || !event.name.trim() || (event.date && !/^\d{4}-\d{2}-\d{2}$/.test(event.date))) throw new Error('イベントID・名称・日付を確認してください．');
            const old = book.events.find(e => e.id === event.id);
            book.events = old ? book.events.map(e => e.id === event.id ? event : e) : [...book.events, event];
            return this.save(store, book);
        }, revision);
    }
    async ImportAccountingReceipt(name: string, encoded: string) {
        const bytes = Uint8Array.from(atob(encoded), c => c.charCodeAt(0));
        if (!bytes.length || bytes.length > 30 * 1024 * 1024) throw new Error('証憑の上限は30MiBです．');
        const ext = name.split('.').pop()?.toLowerCase() ?? '';
        const mime = ({ jpg: 'image/jpeg', jpeg: 'image/jpeg', png: 'image/png', webp: 'image/webp', pdf: 'application/pdf' } as Record<string, string>)[ext];
        const hex = Array.from(bytes.slice(0, 12), b => b.toString(16).padStart(2, '0')).join('');
        const matches = mime === 'image/jpeg' ? hex.startsWith('ffd8ff') : mime === 'image/png' ? hex.startsWith('89504e470d0a1a0a') : mime === 'application/pdf' ? hex.startsWith('255044462d') : mime === 'image/webp' && hex.startsWith('52494646') && hex.slice(16) === '57454250';
        if (!mime || !matches) throw new Error('JPEG・PNG・WebP・PDFの原本を選択してください．');
        const digest = Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', bytes)), b => b.toString(16).padStart(2, '0')).join('');
        return this.transact((store, book) => {
            if (book.receipts.some(r => r.sha256 === digest)) throw new Error('同じ証憑は取り込み済みです．');
            const receiptId = id();
            book.receipts.push({ id: receiptId, name, sha256: digest, mediaType: mime, size: bytes.length, importedAt: new Date().toISOString() });
            store.put(`data:${mime};base64,${encoded}`, `receipt:${receiptId}`); return this.save(store, book);
        });
    }
    async GetAccountingReceiptData(receiptId: string): Promise<string> {
        const db = await openDatabase();
        return new Promise((resolve, reject) => {
            const tx = db.transaction('data'), request = tx.objectStore('data').get(`receipt:${receiptId}`);
            request.onsuccess = () => { if (!request.result) reject(new Error('証憑が見つかりません．')); else resolve(request.result); };
            request.onerror = () => reject(request.error); tx.oncomplete = () => db.close();
        });
    }
    async ExtractAccountingReceipt(_id: string): Promise<Book> { throw new Error('OCRはデスクトップ版のYomiToku環境で利用できます．この画面では原本を見ながら入力できます．'); }
    ImportAccountingJournalCSV(text: string, revision: number) {
        return this.transact((store, book) => {
            if (text.length > 10 * 1024 * 1024) throw new Error('CSVの上限は10MiBです．');
            const rows = parseCSV(text); const hi = rows.findIndex(row => row.includes('取引発生日') && row.includes('摘要'));
            const header = rows[hi]; if (!header) throw new Error('仕訳帳の見出しがありません．');
            const di = header.indexOf('勘定科目'), ci = header.indexOf('勘定科目', di + 1);
            if (di < 0 || ci < 0 || header[di + 4] !== '金額' || header[ci + 4] !== '金額') throw new Error('参照シートと同じ借方・貸方の5列構成にしてください．');
            const keys = new Set(book.entries.flatMap(e => [e.sourceKey, e.id]));
            const sourceIds = new Set(book.entries.map(e => e.sourceId).filter(Boolean)); let count = 0;
            for (const row of rows.slice(hi + 1)) {
                const read = (name: string) => row[header.indexOf(name)]?.trim() ?? '';
                if (!read('取引発生日')) continue;
                const key = JSON.stringify(row);
                const sourceId = read('仕訳ID');
                if (keys.has(key) || (sourceId && keys.has(sourceId))) continue;
                if (sourceId && sourceIds.has(sourceId)) throw new Error(`CSVの仕訳ID ${sourceId} は取り込み済みで内容が変更されています．既存仕訳の訂正として確認してください．`);
                const money = (index: number) => { const raw = (row[index] ?? '').trim().replace(/[,¥￥円]/g, ''); return /^\d+$/.test(raw) ? Number(raw) : NaN; };
                const entry: JournalEntry = { id: id(), date: read('取引発生日').replace(/\//g, '-'), description: read('摘要'), eventId: read('イベントID'), person: read('担当者ID（HackSick_ID）'), receiptId: '', status: 'draft', updatedAt: new Date().toISOString(), sourceKey: key, sourceId,
                    debit: [{ account: row[di]?.trim() ?? '', subaccount: row[di + 1]?.trim() ?? '', amount: money(di + 4) }], credit: [{ account: row[ci]?.trim() ?? '', subaccount: row[ci + 1]?.trim() ?? '', amount: money(ci + 4) }] };
                if (entry.eventId && !book.events.some(e => e.id === entry.eventId)) book.events.push({ id: entry.eventId, name: `イベント ${entry.eventId}`, date: '' });
                const error = validateEntry(book, entry); if (error) throw new Error(error);
                book.entries.push(entry); keys.add(key); count++;
            }
            if (!count) throw new Error('新しい仕訳はありません．空欄と取り込み済みの行を除外しました．');
            return this.save(store, book);
        }, revision);
    }
    ImportAccountingEventsCSV(text: string, revision: number) {
        return this.transact((store, book) => {
            const rows = parseCSV(text), hi = rows.findIndex(row => row.includes('イベントID') && row.includes('イベント名'));
            const header = rows[hi]; if (!header) throw new Error('イベント一覧の見出しがありません．');
            for (const row of rows.slice(hi + 1)) {
                const read = (name: string) => row[header.indexOf(name)]?.trim() ?? '';
                const event = { id: read('イベントID'), name: read('イベント名'), date: read('開催日時') };
                if (!event.id && !event.name) continue;
                if (!event.id || !event.name || (event.date && !/^\d{4}-\d{2}-\d{2}$/.test(event.date))) throw new Error('イベントのID・名称・開催日時を確認してください．');
                book.events = [...book.events.filter(e => e.id !== event.id), event];
            }
            return this.save(store, book);
        }, revision);
    }
}
