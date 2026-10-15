package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

var weekdayShort = [...]string{"dom", "seg", "ter", "qua", "qui", "sex", "sáb"}
var weekdayShortEN = [...]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}

// weekdayEvery are the "toda …" / "todo …" phrases of a single weekday (index = time.Weekday).
var weekdayEvery = [...]string{
	i18n.N("Todo domingo às %s"), i18n.N("Toda segunda às %s"), i18n.N("Toda terça às %s"), i18n.N("Toda quarta às %s"),
	i18n.N("Toda quinta às %s"), i18n.N("Toda sexta às %s"), i18n.N("Todo sábado às %s"),
}

func weekdayName(ctx context.Context, d int) string {
	if isEN(ctx) {
		return weekdayShortEN[d]
	}
	return weekdayShort[d]
}

// fmtWhen renders an upcoming instant: "hoje 14:30", "amanhã 07:00", "sex 17 out 07:00".
func fmtWhen(ctx context.Context, t, now time.Time, loc *time.Location) string {
	if t.IsZero() {
		return "—"
	}
	t, now = t.In(loc), now.In(loc)
	switch daysBetween(now, t) {
	case 0:
		return i18n.T(ctx, "hoje") + " " + t.Format("15:04")
	case 1:
		return i18n.T(ctx, "amanhã") + " " + t.Format("15:04")
	}
	return weekdayName(ctx, int(t.Weekday())) + " " + shortDate(ctx, t) + " " + t.Format("15:04")
}

// describeCron turns a 5-field cron into a short pt-BR phrase. Unknown shapes
// fall back to "Agenda personalizada" (the expression is always shown too).
func describeCron(spec string) string { return describeCronCtx(context.Background(), spec) }

// describeCronCtx is describeCron in the language of ctx.
func describeCronCtx(ctx context.Context, spec string) string {
	f := strings.Fields(spec)
	if len(f) != 5 {
		return ""
	}
	min, hour, dom, mon, dow := f[0], f[1], f[2], f[3], f[4]
	num := func(s string) (int, bool) {
		n, err := strconv.Atoi(s)
		return n, err == nil
	}
	clock := func() (string, bool) {
		m, ok1 := num(min)
		h, ok2 := num(hour)
		if !ok1 || !ok2 {
			return "", false
		}
		return fmt.Sprintf("%02d:%02d", h, m), true
	}
	every := func(s string) (int, bool) {
		if !strings.HasPrefix(s, "*/") {
			return 0, false
		}
		return num(strings.TrimPrefix(s, "*/"))
	}
	switch {
	case spec == "* * * * *":
		return i18n.T(ctx, "A cada minuto")
	case hour == "*" && dom == "*" && mon == "*" && dow == "*":
		if n, ok := every(min); ok {
			return i18n.Tf(ctx, "A cada %d minutos", n)
		}
		if m, ok := num(min); ok {
			if m == 0 {
				return i18n.T(ctx, "A cada hora")
			}
			return i18n.Tf(ctx, "Toda hora, no minuto %d", m)
		}
	case dom == "*" && mon == "*" && dow == "*":
		if n, ok := every(hour); ok {
			if m, ok := num(min); ok && m == 0 {
				return i18n.Tf(ctx, "A cada %d horas", n)
			}
		}
		if c, ok := clock(); ok {
			return i18n.Tf(ctx, "Todo dia às %s", c)
		}
	case dom == "*" && mon == "*":
		c, ok := clock()
		if !ok {
			break
		}
		if dow == "1-5" {
			return i18n.Tf(ctx, "Dias úteis às %s", c)
		}
		if dow == "0,6" || dow == "6,0" {
			return i18n.Tf(ctx, "Fins de semana às %s", c)
		}
		var names []string
		for _, p := range strings.Split(dow, ",") {
			d, ok := num(p)
			if !ok || d < 0 || d > 7 {
				return i18n.T(ctx, "Agenda personalizada")
			}
			names = append(names, weekdayName(ctx, d%7))
		}
		if len(names) == 1 {
			d, _ := num(dow)
			return i18n.Tf(ctx, weekdayEvery[d%7], c)
		}
		return i18n.Tf(ctx, "%s e %s às %s", strings.Join(names[:len(names)-1], ", "), names[len(names)-1], c)
	case mon == "*" && dow == "*":
		if d, ok := num(dom); ok {
			if c, ok := clock(); ok {
				return i18n.Tf(ctx, "Todo dia %d do mês às %s", d, c)
			}
		}
	}
	return i18n.T(ctx, "Agenda personalizada")
}

// cronHint validates the expression and lists the next occurrences.
func cronHint(ctx context.Context, spec, tz string, now time.Time, loc *time.Location) views.CronHintVM {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return views.CronHintVM{}
	}
	sched, err := service.ParseCron(spec, tz)
	if err != nil {
		return views.CronHintVM{Error: i18n.T(ctx, "Agenda inválida: confira os cinco campos e o fuso.")}
	}
	h := views.CronHintVM{Human: describeCronCtx(ctx, spec)}
	t := now
	for i := 0; i < 3; i++ {
		t = sched.Next(t)
		if t.IsZero() {
			break
		}
		h.Next = append(h.Next, fmtWhen(ctx, t, now, loc))
	}
	return h
}

var cronPresets = []views.Option{
	{Value: "", Label: i18n.N("Modelos de agenda")},
	{Value: "0 * * * *", Label: i18n.N("A cada hora")},
	{Value: "0 */2 * * *", Label: i18n.N("A cada 2 horas")},
	{Value: "0 7 * * *", Label: i18n.N("Todo dia às 07:00")},
	{Value: "0 8 * * 1-5", Label: i18n.N("Dias úteis às 08:00")},
	{Value: "0 8 * * 1", Label: i18n.N("Toda segunda às 08:00")},
	{Value: "0 7 * * 5", Label: i18n.N("Toda sexta às 07:00")},
	{Value: "0 9 1 * *", Label: i18n.N("Todo dia 1 às 09:00")},
}
