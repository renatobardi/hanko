package ui

import (
	"net/http"
	"sort"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

func (s *server) handleActivity(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	acts, err := s.be.ListActivity(ctx, co.ID, service.ActivityFilter{Limit: 100})
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	personas, err := s.be.ListPersonas(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	tasks, err := s.be.ListTasks(ctx, co.ID, service.TaskFilter{Limit: 500})
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	names := s.lookup(r.Context(), personas, tasks)
	loc := loadLoc(co.Timezone)
	now := s.now()
	base := "/c/" + co.Slug

	sort.SliceStable(acts, func(i, j int) bool { return acts[i].OccurredAt.After(acts[j].OccurredAt) })
	vm := views.ActivityVM{Subtitle: i18n.T(ctx, "Quem fez o quê, em qual registro, quando. Cada linha leva de volta à task ou à persona responsável.")}
	var lastDay time.Time
	for _, a := range acts {
		if len(vm.Days) == 0 || !sameDay(a.OccurredAt, lastDay, loc) {
			vm.Days = append(vm.Days, views.ActivityDay{Label: dayLabel(ctx, a.OccurredAt, now, loc)})
			lastDay = a.OccurredAt
		}
		d := &vm.Days[len(vm.Days)-1]
		d.Items = append(d.Items, s.activityItem(ctx, a, base, names, now, loc, false))
	}
	sh := s.shell(co, "atividade", i18n.T(ctx, "Atividade"), views.Crumb{Label: i18n.T(ctx, "Atividade")})
	s.render(w, r, http.StatusOK, views.ActivityPage(sh, vm))
}

func (s *server) handleSoon(w http.ResponseWriter, r *http.Request, co domain.Company) {
	it, ok := views.FindNav(r.PathValue("item"))
	if !ok || it.Milestone == "" {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	ctx := r.Context()
	label := i18n.T(ctx, it.Label)
	sh := s.shell(co, it.Key, label, views.Crumb{Label: label})
	s.render(w, r, http.StatusOK, views.SoonPage(sh, views.SoonVM{Label: label, Milestone: it.Milestone, Blurb: i18n.T(ctx, it.Blurb)}))
}
