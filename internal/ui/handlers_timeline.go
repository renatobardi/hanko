package ui

import (
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Geometria da timeline (unidades do viewBox; o SVG escala para a largura do card).
const (
	tlLabelW = 180
	tlChartW = 920
	tlLaneH  = 40
	tlBarH   = 18
	tlHeadH  = 28
)

var timelineRanges = []struct {
	key, label string
	span       time.Duration
}{
	{"hoje", i18n.N("Hoje"), 0},
	{"7d", i18n.N("7 dias"), 7 * 24 * time.Hour},
	{"30d", i18n.N("30 dias"), 30 * 24 * time.Hour},
}

// handleTimeline desenha a linha do tempo do trabalho: uma faixa por persona e uma barra por execução
// que cruza a janela escolhida (hoje, 7 ou 30 dias).
func (s *server) handleTimeline(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	loc, now := loadLoc(co.Timezone), s.now()
	cur := timelineRanges[1]
	for _, rg := range timelineRanges {
		if rg.key == r.URL.Query().Get("intervalo") {
			cur = rg
		}
	}
	local := now.In(loc)
	from := local.Add(-cur.span)
	if cur.span == 0 {
		from = time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	}
	to := now
	runs, err := s.be.ListRuns(ctx, co.ID, service.RunFilter{Limit: 500})
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	base := "/c/" + co.Slug
	vm := views.TimelineVM{Width: tlLabelW + tlChartW, LabelW: tlLabelW}
	for _, rg := range timelineRanges {
		vm.Ranges = append(vm.Ranges, views.TabLink{Key: rg.key, Label: i18n.T(ctx, rg.label), Href: base + "/timeline?intervalo=" + rg.key, On: rg.key == cur.key})
	}
	x := func(t time.Time) float64 {
		if t.Before(from) {
			t = from
		}
		if t.After(to) {
			t = to
		}
		return tlLabelW + float64(t.Sub(from))/float64(to.Sub(from))*tlChartW
	}
	type lane struct {
		name, slug string
		bars       []views.TimelineBar
	}
	lanes := map[string]*lane{}
	var busy time.Duration
	for _, rv := range runs {
		start := rv.CreatedAt
		if rv.StartedAt != nil {
			start = *rv.StartedAt
		}
		end := to
		if rv.FinishedAt != nil {
			end = *rv.FinishedAt
		}
		if end.Before(from) || start.After(to) {
			continue
		}
		name := rv.PersonaName
		if name == "" {
			name = i18n.T(ctx, "Sem persona")
		}
		l, ok := lanes[name]
		if !ok {
			l = &lane{name: name}
			lanes[name] = l
		}
		x0, x1 := x(start), x(end)
		if x1-x0 < 3 {
			x1 = x0 + 3
		}
		subject := rv.TaskKey
		if rv.ConversationID != nil {
			subject = i18n.T(ctx, "Chat")
		}
		vm.Runs++
		if rv.Status == domain.RunFailed {
			vm.Failed++
		}
		d := end.Sub(start)
		if start.Before(from) {
			d = end.Sub(from)
		}
		busy += d
		l.bars = append(l.bars, views.TimelineBar{X: x0, W: x1 - x0, Class: timelineClass(rv.Status),
			Href: base + "/execucoes/" + rv.ID.String(), Label: subject,
			Title: fmt.Sprintf("%s · %s · %s · %s", runTitle(rv), subject, fmtDuration(end.Sub(start)), runBadge(ctx, rv.Status).Label)})
	}
	names := make([]string, 0, len(lanes))
	for n := range lanes {
		names = append(names, n)
	}
	sort.Strings(names)
	for i, n := range names {
		l := lanes[n]
		y := tlHeadH + i*tlLaneH
		for j := range l.bars {
			l.bars[j].Y = y + (tlLaneH-tlBarH)/2
			l.bars[j].H = tlBarH
		}
		vm.Lanes = append(vm.Lanes, views.TimelineLane{Name: l.name, Y: y, H: tlLaneH, Bars: l.bars})
	}
	vm.Personas = len(names)
	vm.Height = tlHeadH + len(names)*tlLaneH + 8
	vm.Busy = fmtDuration(busy)
	step, layout := 24*time.Hour, "02/01"
	switch {
	case cur.span == 0:
		step, layout = 3*time.Hour, "15:04"
	case cur.span > 7*24*time.Hour:
		step = 5 * 24 * time.Hour
	}
	t0 := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, loc)
	for t := t0; !t.After(to); t = t.Add(step) {
		if t.Before(from) {
			continue
		}
		vm.Ticks = append(vm.Ticks, views.TimelineTick{X: x(t), Label: t.In(loc).Format(layout)})
	}
	vm.NowX = x(to)
	sh := s.shell(r, co, "timeline", i18n.T(ctx, "Timeline"), views.Crumb{Label: i18n.T(ctx, "Timeline")})
	s.render(w, r, http.StatusOK, views.TimelinePage(sh, vm))
}

func runTitle(rv service.RunView) string {
	if rv.Name != "" {
		return rv.Name
	}
	return shortID(rv.ID)
}

func timelineClass(st domain.RunStatus) string {
	switch st {
	case domain.RunRunning, domain.RunQueued:
		return "run"
	case domain.RunFailed:
		return "bad"
	case domain.RunDone:
		return "done"
	}
	return "other"
}
