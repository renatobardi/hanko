package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
)

var monthsPT = [...]string{"jan", "fev", "mar", "abr", "mai", "jun", "jul", "ago", "set", "out", "nov", "dez"}

// relTime renders a compact relative time: "agora", "4 min", "1 h", "ontem", "3 dias", "13 out".
func relTime(t, now time.Time, loc *time.Location) string {
	if t.IsZero() {
		return "—"
	}
	d := now.Sub(t)
	switch {
	case d < 0:
		d = 0
		fallthrough
	case d < time.Minute:
		return "agora"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h", int(d.Hours()))
	}
	days := daysBetween(t.In(loc), now.In(loc))
	switch {
	case days <= 1:
		return "ontem"
	case days < 7:
		return fmt.Sprintf("%d dias", days)
	}
	return fmt.Sprintf("%d %s", t.In(loc).Day(), monthsPT[t.In(loc).Month()-1])
}

func daysBetween(a, b time.Time) int {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	ta := time.Date(ay, am, ad, 0, 0, 0, 0, time.UTC)
	tb := time.Date(by, bm, bd, 0, 0, 0, 0, time.UTC)
	return int(tb.Sub(ta).Hours() / 24)
}

// ago is relTime prefixed with "há" for min/h/dias ("há 4 min"); "agora", "ontem" and dates stay as is.
func ago(t, now time.Time, loc *time.Location) string {
	r := relTime(t, now, loc)
	if strings.HasSuffix(r, " min") || strings.HasSuffix(r, " h") || strings.HasSuffix(r, " dias") {
		return "há " + r
	}
	return r
}

func fmtClock(t time.Time, loc *time.Location) string { return t.In(loc).Format("15:04:05") }

func fmtDateTime(t time.Time, loc *time.Location) string {
	if t.IsZero() {
		return "—"
	}
	t = t.In(loc)
	return fmt.Sprintf("%d %s · %s", t.Day(), monthsPT[t.Month()-1], t.Format("15:04"))
}

// dayLabel names the activity day group: "HOJE · 13 OUT", "ONTEM · 12 OUT".
func dayLabel(t, now time.Time, loc *time.Location) string {
	t, now = t.In(loc), now.In(loc)
	date := fmt.Sprintf("%d %s", t.Day(), strings.ToUpper(monthsPT[t.Month()-1]))
	switch daysBetween(t, now) {
	case 0:
		return "Hoje · " + date
	case 1:
		return "Ontem · " + date
	}
	return date
}

func sameDay(a, b time.Time, loc *time.Location) bool {
	return daysBetween(a.In(loc), b.In(loc)) == 0
}

// fmtUSD formats a dollar amount the pt-BR way: "US$ 1.234,50".
func fmtUSD(v float64) string {
	if v > 0 && v < 0.01 {
		return "US$ " + strings.Replace(fmt.Sprintf("%.4f", v), ".", ",", 1)
	}
	s := fmt.Sprintf("%.2f", v)
	whole, frac, _ := strings.Cut(s, ".")
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 && r != '-' {
			b.WriteByte('.')
		}
		b.WriteRune(r)
	}
	return "US$ " + b.String() + "," + frac
}

func fmtCostPtr(v *float64) string {
	if v == nil {
		return "—"
	}
	return fmtUSD(*v)
}

// fmtTokens abbreviates counts: 842, 11,9k, 1,2M.
func fmtTokens(n int64) string {
	switch {
	case n < 1000:
		return fmt.Sprint(n)
	case n < 1_000_000:
		return strings.Replace(fmt.Sprintf("%.1fk", float64(n)/1000), ".", ",", 1)
	}
	return strings.Replace(fmt.Sprintf("%.1fM", float64(n)/1_000_000), ".", ",", 1)
}

func fmtUsage(u domain.Usage) string {
	if u.InputTokens == 0 && u.OutputTokens == 0 {
		return "—"
	}
	return fmtTokens(u.InputTokens) + " / " + fmtTokens(u.OutputTokens)
}

// fmtDuration renders "4m 12s", "2h 05m", "8s".
func fmtDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh %02dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm %02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

func runDuration(r domain.Run, now time.Time) string {
	if r.StartedAt == nil {
		return "—"
	}
	end := now
	if r.FinishedAt != nil {
		end = *r.FinishedAt
	}
	return fmtDuration(end.Sub(*r.StartedAt))
}

// shortID is the human handle of a run: the last 6 hex digits of its id.
func shortID(id domain.ID) string {
	s := strings.ReplaceAll(id.String(), "-", "")
	return "#" + s[len(s)-6:]
}

func loadLoc(name string) *time.Location {
	if name == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return loc
}

func fmtInt(n int) string { return fmt.Sprint(n) }
