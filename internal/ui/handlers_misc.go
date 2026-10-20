package ui

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

func (s *server) handleActivity(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	// filtro por ator (ADR-0042): ?ator=system | persona:<slug> | human:<e-mail>
	ator := strings.TrimSpace(r.URL.Query().Get("ator"))
	filter := service.ActivityFilter{Limit: 100}
	if rs, ok := s.be.(actorResolver); ok && ator != "" {
		af, err := rs.ResolveActor(ctx, co.ID, ator)
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		filter.ActorType, filter.ActorID = af.Type, af.ID
	} else {
		ator = ""
	}
	acts, err := s.be.ListActivity(ctx, co.ID, filter)
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
	vm := views.ActivityVM{Subtitle: i18n.T(ctx, "Quem fez o quê, em qual registro, quando. Cada linha leva de volta à task ou à persona responsável."),
		Action: base + "/atividade", Filtered: ator != ""}
	if _, ok := s.be.(actorResolver); ok {
		vm.ActorOptions = s.actorOptions(ctx, co, personas, ator)
	}
	var lastDay time.Time
	for _, a := range acts {
		if len(vm.Days) == 0 || !sameDay(a.OccurredAt, lastDay, loc) {
			vm.Days = append(vm.Days, views.ActivityDay{Label: dayLabel(ctx, a.OccurredAt, now, loc)})
			lastDay = a.OccurredAt
		}
		d := &vm.Days[len(vm.Days)-1]
		d.Items = append(d.Items, s.activityItem(ctx, a, base, names, now, loc, false))
	}
	sh := s.shell(r, co, "atividade", i18n.T(ctx, "Atividade"), views.Crumb{Label: i18n.T(ctx, "Atividade")})
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
	sh := s.shell(r, co, it.Key, label, views.Crumb{Label: label})
	s.render(w, r, http.StatusOK, views.SoonPage(sh, views.SoonVM{Label: label, Milestone: it.Milestone, Blurb: i18n.T(ctx, it.Blurb)}))
}

// actorResolver converte "persona:ana" / "human:ana@x.com" / "system" em filtro de atividade (ADR-0042).
type actorResolver interface {
	ResolveActor(ctx context.Context, companyID domain.ID, ref string) (service.ActorFilter, error)
}

// actorOptions são as opções do seletor de ator: todos, o sistema, cada pessoa da empresa e cada persona.
func (s *server) actorOptions(ctx context.Context, co domain.Company, personas []domain.Persona, sel string) []views.Option {
	out := []views.Option{{Value: "", Label: i18n.T(ctx, "Todos"), Selected: sel == ""},
		{Value: "system", Label: i18n.T(ctx, "Sistema"), Selected: sel == "system"}}
	if members, err := s.be.ListMembers(ctx, co.ID); err == nil {
		for _, m := range members {
			label := m.Name
			if label == "" {
				label = m.Email
			}
			v := "human:" + m.Email
			out = append(out, views.Option{Value: v, Label: label, Selected: strings.EqualFold(sel, v)})
		}
	}
	for _, p := range personas {
		v := "persona:" + p.Slug
		out = append(out, views.Option{Value: v, Label: p.Name, Selected: sel == v})
	}
	return out
}
