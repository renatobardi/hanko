package ui

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/search"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// searchTypeLabel e searchTypeIcon dão o rótulo e o ícone de cada grupo.
func searchTypeLabel(ctx context.Context, t service.SearchType) string {
	switch t {
	case service.SearchTask:
		return i18n.T(ctx, "Tasks")
	case service.SearchComment:
		return i18n.T(ctx, "Comentários")
	case service.SearchPersona:
		return i18n.T(ctx, "Personas")
	case service.SearchRun:
		return i18n.T(ctx, "Execuções")
	case service.SearchFlow:
		return i18n.T(ctx, "Fluxos")
	case service.SearchMemory:
		return i18n.T(ctx, "Memória")
	}
	return string(t)
}

func searchTypeIcon(t service.SearchType) string {
	switch t {
	case service.SearchTask:
		return "list"
	case service.SearchComment:
		return "message-square"
	case service.SearchPersona:
		return "bot"
	case service.SearchRun:
		return "activity"
	case service.SearchFlow:
		return "workflow"
	case service.SearchMemory:
		return "database"
	}
	return "search"
}

func segs(in []search.Segment) []views.Seg {
	out := make([]views.Seg, len(in))
	for i, s := range in {
		out[i] = views.Seg{Text: s.Text, Match: s.Match}
	}
	return out
}

func (s *server) handleSearch(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if r := []rune(q); len(r) > 512 {
		q = string(r[:512])
	}
	typ := r.URL.Query().Get("tipo")
	var types []service.SearchType
	if typ != "" {
		t := service.SearchType(typ)
		if !t.Valid() {
			typ = ""
		} else {
			types = []service.SearchType{t}
		}
	}
	base := "/c/" + co.Slug
	hasMem := s.be.MemorySearchAvailable(ctx)
	vm := views.SearchVM{Query: q, Type: typ, HasMemo: hasMem,
		Subtitle: i18n.T(ctx, "Uma busca só em tasks, comentários, personas, execuções, fluxos e na memória da empresa.")}

	tab := func(key, label string) views.TabLink {
		href := base + "/buscar"
		v := url.Values{}
		if q != "" {
			v.Set("q", q)
		}
		if key != "" {
			v.Set("tipo", key)
		}
		if len(v) > 0 {
			href += "?" + v.Encode()
		}
		return views.TabLink{Key: key, Label: label, Href: href, On: key == typ}
	}
	vm.Tabs = append(vm.Tabs, tab("", i18n.T(ctx, "Tudo")))
	for _, t := range service.SearchTypes {
		if t == service.SearchMemory && !hasMem {
			continue
		}
		vm.Tabs = append(vm.Tabs, tab(string(t), searchTypeLabel(ctx, t)))
	}

	if q != "" {
		res, err := s.be.Search(ctx, co.ID, q, service.SearchFilter{Types: types, Limit: 10})
		switch {
		case err == nil:
			vm.Total = res.Total
			loc := loadLoc(co.Timezone)
			now := s.now()
			for _, g := range res.Groups {
				vm.Groups = append(vm.Groups, s.searchGroupVM(ctx, base, g, now, loc))
			}
		case errors.Is(err, service.ErrSearchTooSlow):
			vm.Error = i18n.T(ctx, "A busca demorou demais; use palavras mais específicas.")
		case errors.Is(err, domain.ErrInvalid):
			vm.Error = i18n.T(ctx, "Use ao menos uma palavra (letras ou números) na busca.")
		default:
			s.fail(w, r, &co, err)
			return
		}
	}

	if isHX(r) {
		s.render(w, r, http.StatusOK, views.SearchResults(vm))
		return
	}
	sh := s.shell(r, co, "buscar", i18n.T(ctx, "Buscar"), views.Crumb{Label: i18n.T(ctx, "Buscar")})
	s.render(w, r, http.StatusOK, views.SearchPage(sh, vm))
}

func (s *server) searchGroupVM(ctx context.Context, base string, g service.SearchGroup, now time.Time, loc *time.Location) views.SearchGroupVM {
	gv := views.SearchGroupVM{Icon: searchTypeIcon(g.Type), Label: searchTypeLabel(ctx, g.Type), Count: len(g.Hits), More: g.More, Unavailable: g.Unavailable}
	for _, h := range g.Hits {
		hv := views.SearchHitVM{Ref: h.Ref, Title: segs(h.Title), Snippet: segs(h.Snippet), Exact: h.Exact}
		if h.At != nil {
			hv.When = relTimeCtx(ctx, *h.At, now, loc)
		}
		switch h.Type {
		case service.SearchTask:
			hv.Href = base + "/tasks/" + url.PathEscape(h.Ref)
			b := taskBadge(ctx, service.TaskView{Task: domain.Task{Status: domain.TaskStatus(h.Status)}})
			hv.Badge = &b
		case service.SearchComment:
			hv.Href = base + "/tasks/" + url.PathEscape(h.TaskKey)
			hv.Meta = i18n.Tf(ctx, "Comentário de %s", authorLabel(ctx, h.Author))
		case service.SearchPersona:
			hv.Href = base + "/personas/" + url.PathEscape(h.Ref)
			b := personaBadge(ctx, domain.PersonaStatus(h.Status))
			hv.Badge = &b
		case service.SearchRun:
			hv.Href = base + "/execucoes/" + url.PathEscape(h.Ref)
			hv.Ref = h.TaskKey
			hv.Meta = h.Persona
			b := runBadge(ctx, domain.RunStatus(h.Status))
			hv.Badge = &b
		case service.SearchFlow:
			hv.Href = base + "/fluxos/" + url.PathEscape(h.Ref)
			if h.Status == "enabled" {
				hv.Badge = &views.Badge{Label: i18n.T(ctx, "Ativo"), Class: "outline"}
			} else {
				hv.Badge = &views.Badge{Label: i18n.T(ctx, "Pausado"), Class: "soft"}
			}
		case service.SearchMemory:
			// páginas da memória não têm tela própria no Hanko: sem link.
		}
		gv.Hits = append(gv.Hits, hv)
	}
	return gv
}

// authorLabel traduz os autores sem nome (human/system); o nome de uma persona passa como está.
func authorLabel(ctx context.Context, a string) string {
	switch a {
	case "human":
		return i18n.T(ctx, "Você")
	case "system":
		return i18n.T(ctx, "Sistema")
	}
	return a
}
