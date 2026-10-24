package view

import (
	"fmt"
	"html/template"
	"time"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
)

var wib = mustLocation("Asia/Jakarta")

func mustLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

func funcMap() template.FuncMap {
	return template.FuncMap{
		"fmtTime":     fmtTime,
		"fmtDuration": fmtDuration,
		"statusBadge": statusBadge,
		"levelLabel":  levelLabel,
	}
}

// fmtTime formats a timestamp as "02 Jan 2006 15:04" in WIB.
func fmtTime(v any) string {
	switch t := v.(type) {
	case time.Time:
		return formatTime(t)
	case *time.Time:
		if t == nil {
			return "—"
		}
		return formatTime(*t)
	default:
		return "—"
	}
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.In(wib).Format("02 Jan 2006 15:04")
}

// fmtDuration formats milliseconds or a time.Duration for the UI.
func fmtDuration(v any) string {
	switch n := v.(type) {
	case nil:
		return "—"
	case time.Duration:
		return fmt.Sprintf("%d ms", n.Milliseconds())
	case int:
		return formatMS(int64(n))
	case int32:
		return formatMS(int64(n))
	case int64:
		return formatMS(n)
	case *int:
		if n == nil {
			return "—"
		}
		return formatMS(int64(*n))
	case *int32:
		if n == nil {
			return "—"
		}
		return formatMS(int64(*n))
	case *int64:
		if n == nil {
			return "—"
		}
		return formatMS(*n)
	default:
		return "—"
	}
}

func formatMS(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%d ms", ms)
	}
	return fmt.Sprintf("%.2f dtk", float64(ms)/1000)
}

// statusBadge maps a migration status to a CSS class.
func statusBadge(v any) string {
	s := fmt.Sprint(v)
	switch s {
	case "success":
		return "badge-success"
	case "failed":
		return "badge-failed"
	case "running":
		return "badge-running"
	default:
		return "badge-draft"
	}
}

// levelLabel maps view/edit (or authz.Level) to the labels used in the UI.
func levelLabel(v any) string {
	switch x := v.(type) {
	case authz.Level:
		return levelText(x.String())
	case string:
		return levelText(x)
	default:
		return levelText(fmt.Sprint(v))
	}
}

func levelText(s string) string {
	switch s {
	case "view":
		return "View Only"
	case "edit":
		return "View + Edit"
	default:
		return s
	}
}
