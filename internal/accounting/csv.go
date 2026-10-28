package accounting

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"karte/internal/accounting/receipt/domain"
)

func csvRows(content string) ([][]string, error) {
	if len(content) > 10*1024*1024 {
		return nil, fmt.Errorf("CSVの上限は10MiBです")
	}
	r := csv.NewReader(strings.NewReader(strings.TrimPrefix(content, "\ufeff")))
	r.FieldsPerRecord = -1
	var rows [][]string
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("CSVを解析できません: %w", err)
		}
		rows = append(rows, row)
		if len(rows) > 50000 {
			return nil, fmt.Errorf("CSVの上限は50000行です")
		}
	}
	return rows, nil
}

func column(row []string, label string) int {
	for i, v := range row {
		if strings.TrimSpace(v) == label {
			return i
		}
	}
	return -1
}
func cell(row []string, i int) string {
	if i < 0 || i >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[i])
}
func amount(value string) (int64, error) {
	return strconv.ParseInt(strings.NewReplacer(",", "", "¥", "", "￥", "", "円", "").Replace(strings.TrimSpace(value)), 10, 64)
}

// ImportJournalCSV follows the source Sheet's paired account/subaccount/quantity/unit/amount columns．
// All records are validated before a single write，and imported entries require explicit posting．
func (s *Service) ImportJournalCSV(content string, revision int64) (Book, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.load()
	if err != nil {
		return b, err
	}
	if err = checkRevision(b, revision); err != nil {
		return b, err
	}
	rows, err := csvRows(content)
	if err != nil {
		return b, err
	}
	headerAt := -1
	var header []string
	for i, row := range rows {
		if column(row, "取引発生日") >= 0 && column(row, "摘要") >= 0 {
			headerAt = i
			header = row
			break
		}
	}
	if headerAt < 0 {
		return b, fmt.Errorf("仕訳帳の見出し（取引発生日・摘要）が見つかりません")
	}
	debit := column(header, "勘定科目")
	credit := -1
	for i := debit + 1; i < len(header); i++ {
		if header[i] == "勘定科目" {
			credit = i
			break
		}
	}
	if debit < 0 || credit < 0 || cell(header, debit+4) != "金額" || cell(header, credit+4) != "金額" {
		return b, fmt.Errorf("参照シートと同じ借方・貸方の5列構成で出力してください")
	}
	keys := map[string]bool{}
	importedIDs := map[string]bool{}
	for _, e := range b.Entries {
		if e.SourceID != "" {
			importedIDs[e.SourceID] = true
		}
		if e.SourceKey != "" {
			keys[e.SourceKey] = true
		}
		keys[e.ID] = true
	}
	added := 0
	for i, row := range rows[headerAt+1:] {
		date := cell(row, column(header, "取引発生日"))
		if date == "" {
			continue
		}
		key := hash([]byte(strings.Join(row, "\x00")))
		sourceID := cell(row, column(header, "仕訳ID"))
		if keys[key] || (sourceID != "" && keys[sourceID]) {
			continue
		}
		if sourceID != "" && importedIDs[sourceID] {
			return b, fmt.Errorf("CSVの仕訳ID %s は取り込み済みで内容が変更されています．既存仕訳の訂正として確認してください", sourceID)
		}
		da, e1 := amount(cell(row, debit+4))
		ca, e2 := amount(cell(row, credit+4))
		if e1 != nil || e2 != nil {
			return b, fmt.Errorf("CSV %d行目の金額が不正です", i+headerAt+2)
		}
		e := Entry{ID: domain.NewID("journal"), SourceID: sourceID, Date: strings.ReplaceAll(date, "/", "-"), Description: cell(row, column(header, "摘要")), EventID: cell(row, column(header, "イベントID")), Person: cell(row, column(header, "担当者ID（HackSick_ID）")), Status: "draft", SourceKey: key, UpdatedAt: time.Now().UTC().Format(time.RFC3339), Debit: []Line{{Account: cell(row, debit), Subaccount: cell(row, debit+1), Amount: da}}, Credit: []Line{{Account: cell(row, credit), Subaccount: cell(row, credit+1), Amount: ca}}}
		if e.EventID != "" {
			found := false
			for _, event := range b.Events {
				if event.ID == e.EventID {
					found = true
				}
			}
			if !found {
				b.Events = append(b.Events, Event{ID: e.EventID, Name: "イベント " + e.EventID})
			}
		}
		if err = ValidateEntry(b, e); err != nil {
			return b, fmt.Errorf("CSV %d行目: %w", i+headerAt+2, err)
		}
		b.Entries = append(b.Entries, e)
		keys[key] = true
		added++
	}
	if added == 0 {
		return b, fmt.Errorf("新しい仕訳はありません．空欄と取り込み済みの行を除外しました")
	}
	return s.save(b)
}

func (s *Service) ImportEventsCSV(content string, revision int64) (Book, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.load()
	if err != nil {
		return b, err
	}
	if err = checkRevision(b, revision); err != nil {
		return b, err
	}
	rows, err := csvRows(content)
	if err != nil {
		return b, err
	}
	var header []string
	added := 0
	for _, row := range rows {
		if header == nil {
			if column(row, "イベントID") >= 0 && column(row, "イベント名") >= 0 {
				header = row
			}
			continue
		}
		e := Event{ID: cell(row, column(header, "イベントID")), Name: cell(row, column(header, "イベント名")), Date: cell(row, column(header, "開催日時"))}
		if e.ID == "" && e.Name == "" {
			continue
		}
		if e.ID == "" || e.Name == "" || (e.Date != "" && !ValidDate(e.Date)) {
			return b, fmt.Errorf("イベントのID・名称・開催日時を確認してください")
		}
		found := false
		for i, old := range b.Events {
			if old.ID == e.ID {
				b.Events[i] = e
				found = true
				break
			}
		}
		if !found {
			b.Events = append(b.Events, e)
		}
		added++
	}
	if header == nil || added == 0 {
		return b, fmt.Errorf("イベント一覧の見出しとデータが見つかりません")
	}
	return s.save(b)
}
