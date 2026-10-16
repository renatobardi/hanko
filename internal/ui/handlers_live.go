package ui

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// liveTail é quantas linhas do fim do log cada card do painel ao vivo mostra.
const liveTail = 6

// handleLive é o painel ao vivo: as execuções rodando agora, com o fim do log de cada uma e a fila.
// A página se atualiza sozinha (htmx a cada 5 s); o fragmento é o mesmo grid.
func (s *server) handleLive(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	runs, err := s.be.ListRuns(ctx, co.ID, service.RunFilter{Statuses: []domain.RunStatus{domain.RunRunning, domain.RunQueued}, Limit: 100})
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	base := "/c/" + co.Slug
	now := s.now()
	vm := views.LiveVM{Self: base + "/ao-vivo"}
	for _, rv := range runs {
		if rv.Status == domain.RunQueued {
			vm.Queued++
			continue
		}
		card := views.LiveCard{Persona: rv.PersonaName, Name: runTitle(rv), Href: base + "/execucoes/" + rv.ID.String(),
			Agent: agentModel(rv.Agent, rv.Model), Elapsed: runDuration(rv.Run, now), Cost: fmtCostPtrCtx(ctx, rv.CostUSD),
			CancelHref: base + "/execucoes/" + rv.ID.String() + "/cancelar"}
		if rv.ConversationID != nil {
			card.Subject, card.SubjectHref = i18n.T(ctx, "Chat"), base+"/chat/"+rv.ConversationID.String()
		} else {
			card.Subject, card.SubjectHref, card.Title = rv.TaskKey, base+"/tasks/"+rv.TaskKey, rv.TaskTitle
		}
		evs, err := s.be.ListRunEventsBefore(ctx, co.ID, rv.ID, 0, liveTail)
		if err == nil {
			if len(evs) > liveTail {
				evs = evs[len(evs)-liveTail:]
			}
			for _, e := range evs {
				card.Lines = append(card.Lines, views.LiveLine{Kind: string(e.Kind), Text: clipLine(e.Text, 180)})
			}
		}
		vm.Cards = append(vm.Cards, card)
	}
	if isHX(r) {
		s.render(w, r, http.StatusOK, views.LiveGrid(vm))
		return
	}
	sh := s.shell(r, co, "execucoes", i18n.T(ctx, "Ao vivo"), views.Crumb{Label: i18n.T(ctx, "Execuções"), Href: base + "/execucoes"}, views.Crumb{Label: i18n.T(ctx, "Ao vivo")})
	s.render(w, r, http.StatusOK, views.LivePage(sh, vm))
}

// clipLine é a primeira linha do texto, cortada em n runas.
func clipLine(s string, n int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i] + " …"
	}
	if utf8.RuneCountInString(s) > n {
		r := []rune(s)
		s = string(r[:n]) + "…"
	}
	return s
}
