package ui

import (
	"net/http"
	"sort"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
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
	vm := views.ActivityVM{Subtitle: "Quem fez o quê, em qual registro, quando. Cada linha leva de volta à task ou à persona responsável."}
	var lastDay time.Time
	for _, a := range acts {
		if len(vm.Days) == 0 || !sameDay(a.OccurredAt, lastDay, loc) {
			vm.Days = append(vm.Days, views.ActivityDay{Label: dayLabel(a.OccurredAt, now, loc)})
			lastDay = a.OccurredAt
		}
		d := &vm.Days[len(vm.Days)-1]
		d.Items = append(d.Items, s.activityItem(a, base, names, now, loc, false))
	}
	sh := s.shell(co, "atividade", "Atividade", views.Crumb{Label: "Atividade"})
	s.render(w, r, http.StatusOK, views.ActivityPage(sh, vm))
}

func (s *server) handleSoon(w http.ResponseWriter, r *http.Request, co domain.Company) {
	it, ok := views.FindNav(r.PathValue("item"))
	if !ok || it.Milestone == "" {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	sh := s.shell(co, it.Key, it.Label, views.Crumb{Label: it.Label})
	s.render(w, r, http.StatusOK, views.SoonPage(sh, views.SoonVM{Label: it.Label, Milestone: it.Milestone, Blurb: it.Blurb}))
}
