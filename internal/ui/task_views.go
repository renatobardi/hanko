package ui

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// tasksViewCookie guarda a última visão da lista de tasks (visão, agrupamento, ordem): uma URL sem esses
// parâmetros reabre como a pessoa deixou.
const tasksViewCookie = "hanko_tasks"

type taskViewOpts struct {
	Board bool
	Group string // status | prioridade | persona | projeto | nenhum
	Sort  string // atualizada | criada | prioridade | titulo
}

var (
	taskGroups = []string{"status", "prioridade", "persona", "projeto", "nenhum"}
	taskSorts  = []string{"atualizada", "criada", "prioridade", "titulo"}
)

func oneOf(v string, list []string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// taskViewOpts lê visão/agrupar/ordem da URL; sem nenhum deles, do cookie. Quando a URL traz algum,
// a escolha vira o novo padrão (cookie).
func (s *server) taskViewOpts(w http.ResponseWriter, r *http.Request) taskViewOpts {
	src := r.URL.Query()
	explicit := src.Has("visao") || src.Has("agrupar") || src.Has("ordem")
	if !explicit {
		if c, err := r.Cookie(tasksViewCookie); err == nil {
			if v, err := url.ParseQuery(c.Value); err == nil {
				src = v
			}
		}
	}
	o := taskViewOpts{Board: src.Get("visao") == "quadro", Group: src.Get("agrupar"), Sort: src.Get("ordem")}
	if !oneOf(o.Group, taskGroups) {
		o.Group = "status"
	}
	if !oneOf(o.Sort, taskSorts) {
		o.Sort = "atualizada"
	}
	if explicit && !isHX(r) {
		v := url.Values{"agrupar": {o.Group}, "ordem": {o.Sort}}
		if o.Board {
			v.Set("visao", "quadro")
		}
		http.SetCookie(w, &http.Cookie{Name: tasksViewCookie, Value: v.Encode(), Path: "/", MaxAge: int(langCookieTTL / time.Second), //nolint:gosec // Secure vem da config (https)
			HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode})
	}
	return o
}

// sameActor diz se a task foi criada por quem está vendo (humano com o mesmo id; no modo trusted, o dono local).
func sameActor(a, me domain.Actor) bool {
	if a.Type != me.Type {
		return false
	}
	if a.ID == nil || me.ID == nil {
		return a.ID == nil && me.ID == nil
	}
	return *a.ID == *me.ID
}

var priorityRank = map[domain.Priority]int{domain.PriorityCritical: 0, domain.PriorityHigh: 1, domain.PriorityMedium: 2, domain.PriorityLow: 3}

func sortTasks(list []service.TaskView, by string) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		switch by {
		case "criada":
			return a.CreatedAt.After(b.CreatedAt)
		case "prioridade":
			if priorityRank[a.Priority] != priorityRank[b.Priority] {
				return priorityRank[a.Priority] < priorityRank[b.Priority]
			}
		case "titulo":
			if x, y := strings.ToLower(a.Title), strings.ToLower(b.Title); x != y {
				return x < y
			}
		}
		return a.UpdatedAt.After(b.UpdatedAt)
	})
}

// groupTasks agrupa a lista (já ordenada) pelo critério escolhido; grupos vazios não aparecem.
func groupTasks(ctx context.Context, list []service.TaskView, by string, row func(service.TaskView) views.TaskRow) []views.TaskGroup {
	type bucket struct {
		label string
		rows  []views.TaskRow
	}
	var order []string
	buckets := map[string]*bucket{}
	add := func(key, label string, tv service.TaskView) {
		b, ok := buckets[key]
		if !ok {
			b = &bucket{label: label}
			buckets[key] = b
			order = append(order, key)
		}
		b.rows = append(b.rows, row(tv))
	}
	for _, tv := range list {
		switch by {
		case "prioridade":
			add(string(tv.Priority), priorityLabel(ctx, tv.Priority), tv)
		case "persona":
			if tv.PersonaName == "" {
				add("￿", i18n.T(ctx, "Sem dono"), tv)
			} else {
				add(tv.PersonaName, tv.PersonaName, tv)
			}
		case "projeto":
			if tv.ProjectName == "" {
				add("￿", i18n.T(ctx, "Sem projeto"), tv)
			} else {
				add(tv.ProjectName, tv.ProjectName, tv)
			}
		case "nenhum":
			add("", i18n.T(ctx, "Tasks"), tv)
		default:
			add(string(tv.Status), i18n.T(ctx, groupLabel[tv.Status]), tv)
		}
	}
	switch by {
	case "prioridade":
		sort.SliceStable(order, func(i, j int) bool {
			return priorityRank[domain.Priority(order[i])] < priorityRank[domain.Priority(order[j])]
		})
	case "persona", "projeto":
		sort.SliceStable(order, func(i, j int) bool { return strings.ToLower(order[i]) < strings.ToLower(order[j]) })
	case "status":
		rank := map[string]int{}
		for i, st := range groupOrder {
			rank[string(st)] = i
		}
		sort.SliceStable(order, func(i, j int) bool { return rank[order[i]] < rank[order[j]] })
	}
	out := make([]views.TaskGroup, 0, len(order))
	for _, k := range order {
		b := buckets[k]
		out = append(out, views.TaskGroup{Label: b.label, Count: len(b.rows), Rows: b.rows})
	}
	return out
}

func groupChoices(ctx context.Context) [][2]string {
	return [][2]string{
		{"status", i18n.T(ctx, "Agrupar por status")}, {"prioridade", i18n.T(ctx, "Agrupar por prioridade")},
		{"persona", i18n.T(ctx, "Agrupar por persona")}, {"projeto", i18n.T(ctx, "Agrupar por projeto")},
		{"nenhum", i18n.T(ctx, "Sem agrupar")},
	}
}

func sortChoices(ctx context.Context) [][2]string {
	return [][2]string{
		{"atualizada", i18n.T(ctx, "Atualizadas primeiro")}, {"criada", i18n.T(ctx, "Criadas primeiro")},
		{"prioridade", i18n.T(ctx, "Maior prioridade primeiro")}, {"titulo", i18n.T(ctx, "Título (A–Z)")},
	}
}

func optionList(sel string, choices [][2]string) []views.Option {
	out := make([]views.Option, 0, len(choices))
	for _, c := range choices {
		out = append(out, views.Option{Value: c[0], Label: c[1], Selected: c[0] == sel})
	}
	return out
}
