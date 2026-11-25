package googledrive

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

const maxCSVCells = 100_000

type sheetProperties struct {
	ID    int    `json:"sheetId"`
	Title string `json:"title"`
	Index int    `json:"index"`
	Type  string `json:"sheetType"`
	Grid  struct {
		Rows    int `json:"rowCount"`
		Columns int `json:"columnCount"`
	} `json:"gridProperties"`
}

type sheetValue struct {
	Text    *string     `json:"stringValue,omitempty"`
	Number  json.Number `json:"numberValue,omitempty"`
	Bool    *bool       `json:"boolValue,omitempty"`
	Formula *string     `json:"formulaValue,omitempty"`
}

func (v sheetValue) csvText() string {
	switch {
	case v.Text != nil:
		return sheetLineEndings.Replace(*v.Text)
	case v.Number != "":
		return v.Number.String()
	case v.Bool != nil && *v.Bool:
		return "TRUE"
	case v.Bool != nil:
		return "FALSE"
	case v.Formula != nil:
		return sheetLineEndings.Replace(*v.Formula)
	default:
		return ""
	}
}

var sheetLineEndings = strings.NewReplacer("\r\n", "\n", "\r", "\n")

type sheetCell struct {
	Value *sheetValue `json:"userEnteredValue,omitempty"`
}

type sheetRow struct {
	Cells []sheetCell `json:"values"`
}

type sheetSnapshot struct {
	Sheet sheetProperties
	Rows  []sheetRow
}

// CSV represents the first grid tab. Other tabs are outside the CSV editor.
func (s *Service) readSheet(ctx context.Context, access, id string) (sheetSnapshot, error) {
	endpoint := s.sheetsURL + "/" + id
	const properties = "sheetId,title,index,sheetType,gridProperties(rowCount,columnCount)"
	query := url.Values{"fields": {"sheets(properties(" + properties + "))"}}
	body, err := s.request(ctx, access, http.MethodGet, endpoint+"?"+query.Encode(), "", nil, 1<<20)
	if err != nil {
		return sheetSnapshot{}, fmt.Errorf("スプレッドシートを開けません。Google Sheets APIの設定とファイルの権限を確認してください: %w", err)
	}
	var metadata struct {
		Sheets []struct{ Properties sheetProperties }
	}
	if err := json.Unmarshal(body, &metadata); err != nil {
		return sheetSnapshot{}, errors.New("スプレッドシートのタブを読み取れません")
	}
	var first *sheetProperties
	for _, entry := range metadata.Sheets {
		p := entry.Properties
		if p.Type == "GRID" && (first == nil || p.Index < first.Index) {
			first = &p
		}
	}
	if first == nil {
		return sheetSnapshot{}, errors.New("CSVとして開けるシートタブがありません")
	}
	filter := map[string]any{"dataFilters": []any{
		map[string]any{"gridRange": map[string]int{"sheetId": first.ID}},
	}}
	query.Set("fields", "sheets(properties("+properties+"),data(rowData(values(userEnteredValue))))")
	body, err = s.sheetRequest(ctx, access, endpoint+":getByDataFilter?"+query.Encode(), filter, 16<<20)
	if err != nil {
		return sheetSnapshot{}, err
	}
	var values struct {
		Sheets []struct {
			Properties sheetProperties
			Data       []struct{ RowData []sheetRow }
		}
	}
	if err := json.Unmarshal(body, &values); err != nil || len(values.Sheets) != 1 ||
		values.Sheets[0].Properties.ID != first.ID || len(values.Sheets[0].Data) > 1 {
		return sheetSnapshot{}, errors.New("スプレッドシートのセルを読み取れません")
	}
	snapshot := sheetSnapshot{Sheet: values.Sheets[0].Properties}
	if data := values.Sheets[0].Data; len(data) == 1 {
		snapshot.Rows = data[0].RowData
	}
	return snapshot, nil
}

func (s *Service) sheetRequest(ctx context.Context, access, endpoint string, payload any, limit int64) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return s.request(ctx, access, http.MethodPost, endpoint, "application/json", bytes.NewReader(data), limit)
}

func parseCSV(content string) ([][]string, error) {
	if len(content) > maxText || !utf8.ValidString(content) {
		return nil, errors.New("2 MiB以下のUTF-8 CSVを指定してください")
	}
	reader := csv.NewReader(strings.NewReader(strings.TrimPrefix(content, "\uFEFF")))
	reader.FieldsPerRecord = -1
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("CSVの形式を確認してください: %w", err)
	}
	cells := 0
	for _, row := range rows {
		cells += len(row)
	}
	if cells > maxCSVCells {
		return nil, errors.New("CSVは10万セルまで編集できます")
	}
	return rows, nil
}

func (sheet sheetSnapshot) document(file File) (Document, error) {
	var content strings.Builder
	writer := csv.NewWriter(&content)
	cells := 0
	for _, row := range sheet.Rows {
		cells += len(row.Cells)
		if cells > maxCSVCells {
			return Document{}, errors.New("CSVは10万セルまで編集できます")
		}
		record := make([]string, len(row.Cells))
		for i, cell := range row.Cells {
			if cell.Value != nil {
				record[i] = cell.Value.csvText()
			}
		}
		if len(record) == 0 || len(record) == 1 && record[0] == "" {
			// A physical blank line would be skipped by csv.Reader.
			writer.Flush()
			content.WriteString("\"\"\n")
		} else if err := writer.Write(record); err != nil {
			return Document{}, err
		}
	}
	writer.Flush()
	if content.Len() > maxText {
		return Document{}, errors.New("このシートのCSVは2 MiBを超えています")
	}
	// Include the tab identity and typed values so a reordered tab or a type
	// change in Sheets cannot be mistaken for the version shown in Karte.
	version, err := json.Marshal(struct {
		ID   int
		Rows []sheetRow
	}{sheet.Sheet.ID, sheet.Rows})
	if err != nil {
		return Document{}, err
	}
	return Document{File: file, Content: content.String(), Checksum: checksum(version), SheetTab: sheet.Sheet.Title}, writer.Error()
}

type sheetUpdate struct {
	Cells  *updateCells     `json:"updateCells,omitempty"`
	Append *appendDimension `json:"appendDimension,omitempty"`
}

type updateCells struct {
	Start map[string]int `json:"start"`
	Rows  []sheetRow     `json:"rows"`
	Mask  string         `json:"fields"`
}

type appendDimension struct {
	SheetID   int    `json:"sheetId"`
	Dimension string `json:"dimension"`
	Length    int    `json:"length"`
}

func (sheet sheetSnapshot) changes(next [][]string) []sheetUpdate {
	var updates []sheetUpdate
	if extra := len(next) - sheet.Sheet.Grid.Rows; extra > 0 {
		updates = append(updates, sheetUpdate{Append: &appendDimension{sheet.Sheet.ID, "ROWS", extra}})
	}
	columns := 0
	for _, row := range next {
		columns = max(columns, len(row))
	}
	if extra := columns - sheet.Sheet.Grid.Columns; extra > 0 {
		updates = append(updates, sheetUpdate{Append: &appendDimension{sheet.Sheet.ID, "COLUMNS", extra}})
	}
	for r := 0; r < max(len(sheet.Rows), len(next)); r++ {
		var previous []sheetCell
		var desired []string
		if r < len(sheet.Rows) {
			previous = sheet.Rows[r].Cells
		}
		if r < len(next) {
			desired = next[r]
		}
		var changed []sheetCell
		first := 0
		for c := 0; c <= max(len(previous), len(desired)); c++ {
			var oldText, newText string
			if c < len(previous) && previous[c].Value != nil {
				oldText = previous[c].Value.csvText()
			}
			if c < len(desired) {
				newText = desired[c]
			}
			if oldText != newText {
				if len(changed) == 0 {
					first = c
				}
				cell := sheetCell{}
				if newText != "" {
					cell.Value = &sheetValue{Text: &newText}
				}
				changed = append(changed, cell)
			} else if len(changed) > 0 {
				updates = append(updates, sheetUpdate{Cells: &updateCells{
					Start: map[string]int{"sheetId": sheet.Sheet.ID, "rowIndex": r, "columnIndex": first},
					Rows:  []sheetRow{{Cells: changed}},
					Mask:  "userEnteredValue",
				}})
				changed = nil
			}
		}
	}
	return updates
}

func (s *Service) saveSheet(ctx context.Context, access string, file File, expectedChecksum, content string) (Document, error) {
	rows, err := parseCSV(content)
	if err != nil {
		return Document{}, err
	}
	sheet, err := s.readSheet(ctx, access, file.ID)
	if err != nil {
		return Document{}, err
	}
	current, err := sheet.document(file)
	if err != nil {
		return Document{}, err
	}
	if current.Checksum != expectedChecksum {
		return Document{}, errContentChanged
	}
	updates := sheet.changes(rows)
	if len(updates) == 0 {
		return current, nil
	}
	endpoint := s.sheetsURL + "/" + file.ID + ":batchUpdate"
	if _, err := s.sheetRequest(ctx, access, endpoint, map[string]any{"requests": updates}, 8<<20); err != nil {
		return Document{}, err
	}
	sheet, err = s.readSheet(ctx, access, file.ID)
	if err != nil {
		return Document{}, errors.New("スプレッドシートを更新しました。内容を確認するには再読み込みしてください")
	}
	return sheet.document(file)
}
