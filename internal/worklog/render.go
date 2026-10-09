package worklog

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	_ "time/tzdata"
)

// Caller text remains visible and searchable without executable Markdown,
// HTML, links, fences or Karte import directives.
func literal(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '\n' {
			b.WriteString("\n> ")
			continue
		}
		if r == '\r' {
			continue
		}
		if r < 32 || (r >= 33 && r <= 126 && !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9')) {
			fmt.Fprintf(&b, "&#%d;", r)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func quote(s string) string { b, _ := json.Marshal(s); return string(b) }
func header(id, title, project, kind, sensitivity, timezone, date string) string {
	return fmt.Sprintf("---\ndoc_id: %s\ntitle: %s\nproject: %s\nkind: %s\nsensitivity: %s\ntags: [\"ephy:worklog\"]\nworklog_record: true\ntimezone: %s\ndate: %s\n---\n\n", id, quote(title), project, kind, sensitivity, quote(timezone), quote(date))
}

// Reports use the receipt clock. Caller occurred_at remains separate testimony.
func eventDate(e Event, zone string) string {
	loc, _ := time.LoadLocation(zone)
	t, _ := time.Parse(time.RFC3339Nano, e.RecordedAt)
	return t.In(loc).Format("2006-01-02")
}

func taskOnDay(t Task, date string) (Task, bool) {
	events := []Event{}
	activeToday := false
	status := "running"
	for _, e := range t.Events {
		d := eventDate(e, t.Timezone)
		if d > date {
			continue
		}
		events = append(events, e)
		if d == date {
			activeToday = true
		}
		if e.Kind != "start" {
			status, _ = statusAfter(status, e.EventInput)
		}
	}
	if len(events) == 0 || (!activeToday && status != "running" && status != "paused") {
		return Task{}, false
	}
	t.Events = events
	t.Status = status
	t.ViewBaseHash = nil
	return t, true
}

func eventBody(e Event) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### %d · %s · %s\n\n出所: %s / 受信: %s", e.Sequence, e.Kind, e.EventID, e.Origin, e.RecordedAt)
	if e.OccurredAt != "" {
		fmt.Fprintf(&b, " / 申告発生時刻: %s", literal(e.OccurredAt))
	}
	b.WriteString("\n\n> " + literal(e.Text) + "\n\n")
	if e.Outcome != "" {
		b.WriteString("申告した終了状態: " + e.Outcome + "\n\n")
	}
	if e.NextAction != "" {
		b.WriteString("次の作業案（未実行）:\n\n> " + literal(e.NextAction) + "\n\n")
	}
	for _, r := range e.Evidence {
		b.WriteString("参照: " + literal(r.Reference))
		if r.SHA256 != "" {
			b.WriteString(" / SHA256: " + r.SHA256)
		}
		b.WriteString("\n\n")
	}
	return b.String()
}

func renderTask(t Task) []byte {
	var b strings.Builder
	b.WriteString(header(t.DocID, "作業記録: "+t.Title, t.Project, "note", t.Sensitivity, t.Timezone, eventDate(t.Events[0], t.Timezone)))
	fmt.Fprintf(&b, "# 作業記録\n\n> %s\n\n作業ID: %s / 最後に記録された状態: %s\n\n", literal(t.Title), t.TaskID, t.Status)
	b.WriteString("この文書は原本JSONから生成した閲覧用記録です．model_report はモデルの申告，host_observation は実行側の観測です．completed は作業の終了申告で，検証済みを意味しません．\n\n")
	b.WriteString("目的:\n\n> " + literal(t.Objective) + "\n\n")
	if t.Repository != "" {
		b.WriteString("対象: " + literal(t.Repository) + " / 基準: " + literal(t.BaseRevision) + "\n\n")
	}
	for _, e := range t.Events {
		b.WriteString(eventBody(e))
	}
	return []byte(b.String())
}

func renderDailyBody(tasks []Task, date string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# 日報 · %s\n\n受信日で集計しています．後から届いた観測の発生時刻は各イベントに別記します．状態は最後の記録時点のものです．記録がない時間帯の活動や成功は推定しません．次の作業案は自動採用されません．\n\n", date)
	if len(tasks) == 0 {
		b.WriteString("この日に受信した記録・持ち越し作業はありません．\n")
		return b.String()
	}
	for _, t := range tasks {
		last := t.Events[len(t.Events)-1]
		fmt.Fprintf(&b, "## 作業 %s\n\n> %s\n\n最後の記録状態: %s / 最終観測: %s\n\n", t.TaskID, literal(t.Title), t.Status, last.RecordedAt)
		count := 0
		for _, e := range t.Events {
			if eventDate(e, t.Timezone) == date {
				b.WriteString(eventBody(e))
				count++
			}
		}
		if count == 0 {
			b.WriteString("前日以前からの持ち越しです．この日に新しい記録はありません．\n\n")
		}
		for i := len(t.Events) - 1; i >= 0; i-- {
			if t.Events[i].NextAction != "" {
				b.WriteString("最後に記録された次の作業案:\n\n> " + literal(t.Events[i].NextAction) + "\n\n")
				break
			}
		}
	}
	return b.String()
}

func renderDaily(c Config, d Daily) []byte {
	var b strings.Builder
	b.WriteString(header(d.DocID, "Ephy日報 "+d.Date, c.Project, "journal", c.Sensitivity, c.Timezone, d.Date))
	b.WriteString(d.Body)
	b.WriteString("\n## 原本スナップショット\n\n")
	for _, source := range d.Sources {
		fmt.Fprintf(&b, "- %s / SHA256: %s\n", source.Reference, source.SHA256)
	}
	return []byte(b.String())
}
