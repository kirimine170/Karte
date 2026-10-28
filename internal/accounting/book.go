// Package accounting implements the local accounting proof of concept．
package accounting

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"karte/internal/accounting/receipt/domain"
)

//go:embed accounts.json receipt/worker/*.py
var resources embed.FS

const MaxAmount int64 = 1_000_000_000_000

type Account struct {
	Name   string `json:"name"`
	Group  string `json:"group"`
	Normal string `json:"normal"`
}

type Line struct {
	Account    string `json:"account"`
	Subaccount string `json:"subaccount"`
	Amount     int64  `json:"amount"`
}

type Entry struct {
	ID          string `json:"id"`
	Date        string `json:"date"`
	Description string `json:"description"`
	EventID     string `json:"eventId"`
	Person      string `json:"person"`
	ReceiptID   string `json:"receiptId"`
	Status      string `json:"status"`
	Debit       []Line `json:"debit"`
	Credit      []Line `json:"credit"`
	SourceKey   string `json:"sourceKey,omitempty"`
	SourceID    string `json:"sourceId,omitempty"`
	UpdatedAt   string `json:"updatedAt"`
}

type Event struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Date string `json:"date"`
}

type Receipt struct {
	ID          string                     `json:"id"`
	Name        string                     `json:"name"`
	SHA256      string                     `json:"sha256"`
	MediaType   string                     `json:"mediaType"`
	Size        int                        `json:"size"`
	ImportedAt  time.Time                  `json:"importedAt"`
	Observation *domain.ReceiptObservation `json:"observation,omitempty"`
	OCR         *domain.OCRDocument        `json:"ocr,omitempty"`
	Error       string                     `json:"error,omitempty"`
}

type Book struct {
	Version  int       `json:"version"`
	Revision int64     `json:"revision"`
	Accounts []Account `json:"accounts"`
	Entries  []Entry   `json:"entries"`
	Events   []Event   `json:"events"`
	Receipts []Receipt `json:"receipts"`
}

// Each mutation loads fresh state under the lock．Revision checks protect against stale windows．
type Service struct {
	dir string
	mu  sync.Mutex
}

func NewService(dataDir string) *Service {
	return &Service{dir: filepath.Join(dataDir, "data", "accounting")}
}

func EmptyBook() Book {
	var accounts []Account
	data, _ := resources.ReadFile("accounts.json")
	_ = json.Unmarshal(data, &accounts)
	return Book{Version: 1, Accounts: accounts, Entries: []Entry{}, Events: []Event{}, Receipts: []Receipt{}}
}

func (s *Service) load() (Book, error) {
	b := EmptyBook()
	data, err := os.ReadFile(filepath.Join(s.dir, "book.json"))
	if errors.Is(err, os.ErrNotExist) {
		return b, nil
	}
	if err != nil {
		return b, err
	}
	if err = json.Unmarshal(data, &b); err != nil {
		return b, fmt.Errorf("会計データを読み取れません．原本を確認してください: %w", err)
	}
	if b.Version != 1 {
		return b, fmt.Errorf("未対応の会計データ形式です")
	}
	return b, nil
}

func (s *Service) Load() (Book, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.load() }

func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".accounting-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func (s *Service) save(b Book) (Book, error) {
	b.Revision++
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return b, err
	}
	path := filepath.Join(s.dir, "book.json")
	if prev, err := os.ReadFile(path); err == nil {
		if err = atomicWrite(path+".bak", prev); err != nil {
			return b, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return b, err
	}
	return b, atomicWrite(path, data)
}

func checkRevision(b Book, revision int64) error {
	if b.Revision != revision {
		return fmt.Errorf("他の操作で会計データが更新されました．再読み込みしてからやり直してください")
	}
	return nil
}

func ValidDate(date string) bool {
	d, e := time.Parse("2006-01-02", date)
	return e == nil && d.Format("2006-01-02") == date
}

func ValidateEntry(b Book, e Entry) error {
	if !ValidDate(e.Date) {
		return fmt.Errorf("取引発生日を入力してください")
	}
	if strings.TrimSpace(e.Description) == "" || len(e.Description) > 10000 {
		return fmt.Errorf("摘要を入力してください（最大10000バイト）")
	}
	if e.Status != "draft" && e.Status != "posted" {
		return fmt.Errorf("仕訳の状態が不正です")
	}
	if e.EventID != "" {
		found := false
		for _, event := range b.Events {
			if event.ID == e.EventID {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("イベントを登録してから選択してください")
		}
	}
	accounts := map[string]bool{}
	for _, a := range b.Accounts {
		accounts[a.Name] = true
	}
	totals := [2]int64{}
	for side, lines := range [][]Line{e.Debit, e.Credit} {
		if len(lines) == 0 || len(lines) > 100 {
			return fmt.Errorf("借方・貸方それぞれに1〜100行を入力してください")
		}
		for _, l := range lines {
			if !accounts[l.Account] {
				return fmt.Errorf("未登録の勘定科目です: %s", l.Account)
			}
			if l.Amount <= 0 || l.Amount > MaxAmount || totals[side] > MaxAmount-l.Amount {
				return fmt.Errorf("金額は正の整数円，仕訳合計は1兆円以下にしてください")
			}
			totals[side] += l.Amount
		}
	}
	if totals[0] != totals[1] {
		return fmt.Errorf("貸借が一致しません（借方 %d円／貸方 %d円）", totals[0], totals[1])
	}
	if e.ReceiptID != "" {
		var receipt *Receipt
		for i := range b.Receipts {
			if b.Receipts[i].ID == e.ReceiptID {
				receipt = &b.Receipts[i]
				break
			}
		}
		if receipt == nil {
			return fmt.Errorf("証憑が見つかりません")
		}
		for _, other := range b.Entries {
			if other.ID != e.ID && other.ReceiptID == e.ReceiptID {
				return fmt.Errorf("この証憑には既に仕訳があります．既存の下書きを編集してください")
			}
		}
	}
	return nil
}

func (s *Service) SaveEntry(e Entry, revision int64) (Book, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.load()
	if err != nil {
		return b, err
	}
	if err = checkRevision(b, revision); err != nil {
		return b, err
	}
	index := -1
	for i, old := range b.Entries {
		if old.ID == e.ID {
			index = i
			if old.Status == "posted" {
				return b, fmt.Errorf("記帳済み仕訳は変更できません．訂正は反対仕訳で登録してください")
			}
			e.SourceKey = old.SourceKey
			e.SourceID = old.SourceID
			break
		}
	}
	if e.ID != "" && index < 0 {
		return b, fmt.Errorf("編集対象の仕訳が見つかりません")
	}
	if err = ValidateEntry(b, e); err != nil {
		return b, err
	}
	if e.ID == "" {
		e.ID = domain.NewID("journal")
		e.SourceKey = ""
		e.SourceID = ""
	}
	e.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if index < 0 {
		b.Entries = append(b.Entries, e)
	} else {
		b.Entries[index] = e
	}
	return s.save(b)
}

func (s *Service) DeleteDraft(id string, revision int64) (Book, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.load()
	if err != nil {
		return b, err
	}
	if err = checkRevision(b, revision); err != nil {
		return b, err
	}
	for i, e := range b.Entries {
		if e.ID == id {
			if e.Status != "draft" {
				return b, fmt.Errorf("削除できるのは下書きだけです")
			}
			b.Entries = append(b.Entries[:i], b.Entries[i+1:]...)
			return s.save(b)
		}
	}
	return b, fmt.Errorf("仕訳が見つかりません")
}

// PostDrafts validates the entire selection before persisting one revision．
func (s *Service) PostDrafts(ids []string, revision int64) (Book, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.load()
	if err != nil {
		return b, err
	}
	if err = checkRevision(b, revision); err != nil {
		return b, err
	}
	if len(ids) == 0 || len(ids) > len(b.Entries) {
		return b, fmt.Errorf("承認する下書きを選択してください")
	}
	selected := make(map[string]bool, len(ids))
	indexes := make(map[string]int, len(b.Entries))
	for i, e := range b.Entries {
		indexes[e.ID] = i
	}
	for _, id := range ids {
		if selected[id] {
			return b, fmt.Errorf("同じ仕訳が重複して選択されています")
		}
		i, found := indexes[id]
		if !found || b.Entries[i].Status != "draft" {
			return b, fmt.Errorf("承認対象の下書きが更新・削除されています．再読み込みしてください")
		}
		if err = ValidateEntry(b, b.Entries[i]); err != nil {
			return b, fmt.Errorf("%s: %w", b.Entries[i].Description, err)
		}
		selected[id] = true
	}
	updatedAt := time.Now().UTC().Format(time.RFC3339)
	for id := range selected {
		i := indexes[id]
		b.Entries[i].Status = "posted"
		b.Entries[i].UpdatedAt = updatedAt
	}
	return s.save(b)
}

func (s *Service) SaveEvent(e Event, revision int64) (Book, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.load()
	if err != nil {
		return b, err
	}
	if err = checkRevision(b, revision); err != nil {
		return b, err
	}
	if strings.TrimSpace(e.ID) == "" || strings.TrimSpace(e.Name) == "" || (e.Date != "" && !ValidDate(e.Date)) {
		return b, fmt.Errorf("イベントID・名称と正しい日付を入力してください")
	}
	for i, old := range b.Events {
		if old.ID == e.ID {
			b.Events[i] = e
			return s.save(b)
		}
	}
	b.Events = append(b.Events, e)
	return s.save(b)
}

func hash(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }
