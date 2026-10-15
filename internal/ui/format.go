package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
)

var monthsPT = [...]string{"jan", "fev", "mar", "abr", "mai", "jun", "jul", "ago", "set", "out", "nov", "dez"}
var monthsEN = [...]string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}

func isEN(ctx context.Context) bool { return i18n.FromContext(ctx) == i18n.EN }

// shortDate is a day and month in the language of ctx: "13 out" / "Oct 13".
func shortDate(ctx context.Context, t time.Time) string {
	if isEN(ctx) {
		return fmt.Sprintf("%s %d", monthsEN[t.Month()-1], t.Day())
	}
	return fmt.Sprintf("%d %s", t.Day(), monthsPT[t.Month()-1])
}

// relTime renders a compact relative time: "agora", "4 min", "1 h", "ontem", "3 dias", "13 out".
func relTime(t, now time.Time, loc *time.Location) string {
	return relTimeCtx(context.Background(), t, now, loc)
}

// relTimeCtx is relTime in the language of ctx ("now", "4 min", "1 h", "yesterday", "3 days", "Oct 13").
func relTimeCtx(ctx context.Context, t, now time.Time, loc *time.Location) string {
	r, _ := relParts(ctx, t, now, loc)
	return r
}

// relParts is the relative time and whether it is a span ("4 min", "3 days") that reads
// "há 4 min" / "4 min ago" in the past tense, as opposed to "agora", "ontem" or a date.
func relParts(ctx context.Context, t, now time.Time, loc *time.Location) (string, bool) {
	if t.IsZero() {
		return "—", false
	}
	d := now.Sub(t)
	switch {
	case d < 0:
		d = 0
		fallthrough
	case d < time.Minute:
		return i18n.T(ctx, "agora"), false
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes())), true
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h", int(d.Hours())), true
	}
	days := daysBetween(t.In(loc), now.In(loc))
	switch {
	case days <= 1:
		return i18n.T(ctx, "ontem"), false
	case days < 7:
		return i18n.Tf(ctx, "%d dias", days), true
	}
	return shortDate(ctx, t.In(loc)), false
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
	return agoCtx(context.Background(), t, now, loc)
}

// agoCtx is ago in the language of ctx ("4 min ago").
func agoCtx(ctx context.Context, t, now time.Time, loc *time.Location) string {
	r, span := relParts(ctx, t, now, loc)
	if span {
		return i18n.Tf(ctx, "há %s", r)
	}
	return r
}

func fmtClock(t time.Time, loc *time.Location) string { return t.In(loc).Format("15:04:05") }

func fmtDateTime(ctx context.Context, t time.Time, loc *time.Location) string {
	if t.IsZero() {
		return "—"
	}
	t = t.In(loc)
	return shortDate(ctx, t) + " · " + t.Format("15:04")
}

// dayLabel names the activity day group: "HOJE · 13 OUT", "ONTEM · 12 OUT".
func dayLabel(ctx context.Context, t, now time.Time, loc *time.Location) string {
	t, now = t.In(loc), now.In(loc)
	date := strings.ToUpper(shortDate(ctx, t))
	switch daysBetween(t, now) {
	case 0:
		return i18n.T(ctx, "Hoje") + " · " + date
	case 1:
		return i18n.T(ctx, "Ontem") + " · " + date
	}
	return date
}

func sameDay(a, b time.Time, loc *time.Location) bool {
	return daysBetween(a.In(loc), b.In(loc)) == 0
}

// fmtUSD formats a dollar amount the pt-BR way: "US$ 1.234,50".
func fmtUSD(v float64) string { return fmtUSDCtx(context.Background(), v) }

// fmtUSDCtx formats a dollar amount in the language of ctx: "US$ 1.234,50" / "$1,234.50".
func fmtUSDCtx(ctx context.Context, v float64) string {
	en := isEN(ctx)
	if v > 0 && v < 0.01 {
		s := fmt.Sprintf("%.4f", v)
		if en {
			return "$" + s
		}
		return "US$ " + strings.Replace(s, ".", ",", 1)
	}
	s := fmt.Sprintf("%.2f", v)
	whole, frac, _ := strings.Cut(s, ".")
	sep, dec := '.', ","
	if en {
		sep, dec = ',', "."
	}
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 && r != '-' {
			b.WriteRune(sep)
		}
		b.WriteRune(r)
	}
	if en {
		return "$" + b.String() + dec + frac
	}
	return "US$ " + b.String() + dec + frac
}

func fmtCostPtr(v *float64) string { return fmtCostPtrCtx(context.Background(), v) }

func fmtCostPtrCtx(ctx context.Context, v *float64) string {
	if v == nil {
		return "—"
	}
	return fmtUSDCtx(ctx, *v)
}

// fmtTokens abbreviates counts: 842, 11,9k, 1,2M.
func fmtTokens(n int64) string { return fmtTokensCtx(context.Background(), n) }

// fmtTokensCtx abbreviates counts in the language of ctx: 842, 11,9k / 11.9k.
func fmtTokensCtx(ctx context.Context, n int64) string {
	dec := func(s string) string {
		if isEN(ctx) {
			return s
		}
		return strings.Replace(s, ".", ",", 1)
	}
	switch {
	case n < 1000:
		return fmt.Sprint(n)
	case n < 1_000_000:
		return dec(fmt.Sprintf("%.1fk", float64(n)/1000))
	}
	return dec(fmt.Sprintf("%.1fM", float64(n)/1_000_000))
}

func fmtUsage(u domain.Usage) string { return fmtUsageCtx(context.Background(), u) }

func fmtUsageCtx(ctx context.Context, u domain.Usage) string {
	if u.InputTokens == 0 && u.OutputTokens == 0 {
		return "—"
	}
	return fmtTokensCtx(ctx, u.InputTokens) + " / " + fmtTokensCtx(ctx, u.OutputTokens)
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
