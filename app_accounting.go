package main

import (
	"context"
	"fmt"
	"karte/internal/accounting"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *App) accountingStore() (*accounting.Service, error) {
	a.accountingMu.Lock()
	defer a.accountingMu.Unlock()
	if a.dataDir == "" {
		return nil, fmt.Errorf("会計データの保存先が未初期化です")
	}
	if a.accountingService == nil {
		a.accountingService = accounting.NewService(a.dataDir)
	}
	return a.accountingService, nil
}

func (a *App) GetAccountingData() (accounting.Book, error) {
	s, e := a.accountingStore()
	if e != nil {
		return accounting.Book{}, e
	}
	return s.Load()
}
func (a *App) SaveAccountingEntry(entry accounting.Entry, revision int64) (accounting.Book, error) {
	s, e := a.accountingStore()
	if e != nil {
		return accounting.Book{}, e
	}
	return s.SaveEntry(entry, revision)
}
func (a *App) DeleteAccountingDraft(id string, revision int64) (accounting.Book, error) {
	s, e := a.accountingStore()
	if e != nil {
		return accounting.Book{}, e
	}
	return s.DeleteDraft(id, revision)
}
func (a *App) PostAccountingDrafts(ids []string, revision int64) (accounting.Book, error) {
	s, e := a.accountingStore()
	if e != nil {
		return accounting.Book{}, e
	}
	return s.PostDrafts(ids, revision)
}

// WKWebView does not handle anchor downloads consistently．Use the native save panel．
func (a *App) SaveAccountingExport(content, name string) (string, error) {
	ext := filepath.Ext(name)
	if ext != ".csv" && ext != ".json" {
		return "", fmt.Errorf("CSVまたはJSONの保存名を指定してください")
	}
	if len(content) > 100*1024*1024 {
		return "", fmt.Errorf("書き出しデータの上限は100MiBです")
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title: "会計データを保存", DefaultFilename: filepath.Base(name),
		Filters: []runtime.FileFilter{{DisplayName: "会計データ", Pattern: "*" + ext}},
	})
	if err != nil || path == "" {
		return "", err
	}
	if err = os.WriteFile(path, []byte(content), 0600); err != nil {
		return "", err
	}
	return path, nil
}
func (a *App) SaveAccountingEvent(event accounting.Event, revision int64) (accounting.Book, error) {
	s, e := a.accountingStore()
	if e != nil {
		return accounting.Book{}, e
	}
	return s.SaveEvent(event, revision)
}
func (a *App) ImportAccountingReceipt(name, data string) (accounting.Book, error) {
	s, e := a.accountingStore()
	if e != nil {
		return accounting.Book{}, e
	}
	return s.ImportReceipt(name, data)
}
func (a *App) GetAccountingReceiptData(id string) (string, error) {
	s, e := a.accountingStore()
	if e != nil {
		return "", e
	}
	return s.ReceiptData(id)
}
func (a *App) ExtractAccountingReceipt(id string) (accounting.Book, error) {
	s, e := a.accountingStore()
	if e != nil {
		return accounting.Book{}, e
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return s.Extract(ctx, id)
}
func (a *App) ImportAccountingJournalCSV(data string, revision int64) (accounting.Book, error) {
	s, e := a.accountingStore()
	if e != nil {
		return accounting.Book{}, e
	}
	return s.ImportJournalCSV(data, revision)
}
func (a *App) ImportAccountingEventsCSV(data string, revision int64) (accounting.Book, error) {
	s, e := a.accountingStore()
	if e != nil {
		return accounting.Book{}, e
	}
	return s.ImportEventsCSV(data, revision)
}
