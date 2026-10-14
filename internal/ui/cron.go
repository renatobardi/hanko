package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

var weekdayShort = [...]string{"dom", "seg", "ter", "qua", "qui", "sex", "sáb"}

// weekday names for "toda …" / "todo …" phrases.
var weekdayLong = [...]string{"todo domingo", "toda segunda", "toda terça", "toda quarta", "toda quinta", "toda sexta", "todo sábado"}

// fmtWhen renders an upcoming instant: "hoje 14:30", "amanhã 07:00", "sex 17 out 07:00".
func fmtWhen(t, now time.Time, loc *time.Location) string {
	if t.IsZero() {
		return "—"
	}
	t, now = t.In(loc), now.In(loc)
	switch daysBetween(now, t) {
	case 0:
		return "hoje " + t.Format("15:04")
	case 1:
		return "amanhã " + t.Format("15:04")
	}
	return fmt.Sprintf("%s %d %s %s", weekdayShort[t.Weekday()], t.Day(), monthsPT[t.Month()-1], t.Format("15:04"))
}

// describeCron turns a 5-field cron into a short pt-BR phrase. Unknown shapes
// fall back to "Agenda personalizada" (the expression is always shown too).
func describeCron(spec string) string {
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
		return "A cada minuto"
	case hour == "*" && dom == "*" && mon == "*" && dow == "*":
		if n, ok := every(min); ok {
			return fmt.Sprintf("A cada %d minutos", n)
		}
		if m, ok := num(min); ok {
			if m == 0 {
				return "A cada hora"
			}
			return fmt.Sprintf("Toda hora, no minuto %d", m)
		}
	case dom == "*" && mon == "*" && dow == "*":
		if n, ok := every(hour); ok {
			if m, ok := num(min); ok && m == 0 {
				return fmt.Sprintf("A cada %d horas", n)
			}
		}
		if c, ok := clock(); ok {
			return "Todo dia às " + c
		}
	case dom == "*" && mon == "*":
		c, ok := clock()
		if !ok {
			break
		}
		if dow == "1-5" {
			return "Dias úteis às " + c
		}
		if dow == "0,6" || dow == "6,0" {
			return "Fins de semana às " + c
		}
		var names []string
		for _, p := range strings.Split(dow, ",") {
			d, ok := num(p)
			if !ok || d < 0 || d > 7 {
				return "Agenda personalizada"
			}
			names = append(names, weekdayShort[d%7])
		}
		if len(names) == 1 {
			d, _ := num(dow)
			return strings.ToUpper(weekdayLong[d%7][:1]) + weekdayLong[d%7][1:] + " às " + c
		}
		return strings.Join(names[:len(names)-1], ", ") + " e " + names[len(names)-1] + " às " + c
	case mon == "*" && dow == "*":
		if d, ok := num(dom); ok {
			if c, ok := clock(); ok {
				return fmt.Sprintf("Todo dia %d do mês às %s", d, c)
			}
		}
	}
	return "Agenda personalizada"
}

// cronHint validates the expression and lists the next occurrences.
func cronHint(spec, tz string, now time.Time, loc *time.Location) views.CronHintVM {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return views.CronHintVM{}
	}
	sched, err := service.ParseCron(spec, tz)
	if err != nil {
		return views.CronHintVM{Error: "Agenda inválida: confira os cinco campos e o fuso."}
	}
	h := views.CronHintVM{Human: describeCron(spec)}
	t := now
	for i := 0; i < 3; i++ {
		t = sched.Next(t)
		if t.IsZero() {
			break
		}
		h.Next = append(h.Next, fmtWhen(t, now, loc))
	}
	return h
}

var cronPresets = []views.Option{
	{Value: "", Label: "Modelos de agenda"},
	{Value: "0 * * * *", Label: "A cada hora"},
	{Value: "0 */2 * * *", Label: "A cada 2 horas"},
	{Value: "0 7 * * *", Label: "Todo dia às 07:00"},
	{Value: "0 8 * * 1-5", Label: "Dias úteis às 08:00"},
	{Value: "0 8 * * 1", Label: "Toda segunda às 08:00"},
	{Value: "0 7 * * 5", Label: "Toda sexta às 07:00"},
	{Value: "0 9 1 * *", Label: "Todo dia 1 às 09:00"},
}
