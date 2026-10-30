package ui

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Islands (ADR-0005, ADR-0026): the host renders the page and hands each React island its props as data-*
// attributes — JSON for structured data and a JSON map of texts that are already translated here (i18n.T), so
// the JS carries no message catalog. The markup inside the island element is the version without JS.

// islandAPI is the REST base the islands call (same origin, session cookie).
func islandAPI(co domain.Company) string { return "/api/v1/companies/" + co.Slug }

// msgsJSON serializes translated texts for data-msgs.
func msgsJSON(m map[string]string) string {
	b, err := json.Marshal(m)
	if err != nil { // a map[string]string cannot fail to marshal
		return "{}"
	}
	return string(b)
}

func runLogMsgs(ctx context.Context) string {
	return msgsJSON(map[string]string{
		"log.search":         i18n.T(ctx, "Buscar no log"),
		"log.filter":         i18n.T(ctx, "Filtrar por tipo de evento"),
		"log.follow":         i18n.T(ctx, "ir para o fim"),
		"log.paused":         i18n.T(ctx, "Pausado"),
		"log.following":      i18n.T(ctx, "Seguindo o fim"),
		"log.older":          i18n.T(ctx, "Carregar eventos anteriores"),
		"log.loading":        i18n.T(ctx, "Carregando…"),
		"log.count":          i18n.T(ctx, "{total} eventos"),
		"log.count.filtered": i18n.T(ctx, "{shown} de {total} eventos"),
		"log.nomatch":        i18n.T(ctx, "Nenhum evento corresponde ao filtro."),
		"log.empty":          i18n.T(ctx, "Sem eventos registrados."),
		"log.waiting":        i18n.T(ctx, "Aguardando eventos…"),
		"log.live":           i18n.T(ctx, "ao vivo"),
		"log.reconnecting":   i18n.T(ctx, "reconectando…"),
		"log.error":          i18n.T(ctx, "Não foi possível carregar o log."),
		"log.retry":          i18n.T(ctx, "Tentar de novo"),
		"log.expand":         i18n.T(ctx, "Expandir"),
		"log.collapse":       i18n.T(ctx, "Recolher"),
		"log.payload":        i18n.T(ctx, "Payload"),
	})
}

func orgMsgs(ctx context.Context) string {
	return msgsJSON(map[string]string{
		"org.toolbar":    i18n.T(ctx, "Controles do organograma"),
		"org.label":      i18n.T(ctx, "Organograma das personas"),
		"org.zoom.in":    i18n.T(ctx, "Aproximar"),
		"org.zoom.out":   i18n.T(ctx, "Afastar"),
		"org.fit":        i18n.T(ctx, "Ajustar à tela"),
		"org.expand.all": i18n.T(ctx, "Expandir tudo"),
		"org.hint.edit":  i18n.T(ctx, "Arraste uma persona sobre outra para mudar quem ela reporta · Ctrl + roda aproxima · arraste o fundo para mover"),
		"org.hint.view":  i18n.T(ctx, "Ctrl + roda aproxima · arraste o fundo para mover"),
		"org.drop.top":   i18n.T(ctx, "Solte aqui para mover ao topo (reporta a você)"),
		"org.collapse":   i18n.T(ctx, "Recolher o ramo de {name}"),
		"org.expand":     i18n.T(ctx, "Expandir o ramo de {name} ({n} escondidas)"),
		"org.moved":      i18n.T(ctx, "{name} agora reporta a {to}."),
		"org.moved.top":  i18n.T(ctx, "{name} foi para o topo e reporta a você."),
		"org.err.cycle":  i18n.T(ctx, "Não dá: {to} já está abaixo de {name}; isso criaria um ciclo."),
		"org.err.self":   i18n.T(ctx, "Uma persona não reporta a si mesma."),
		"org.err.api":    i18n.T(ctx, "Não foi possível mover: {err}"),
		// filtros e tags (ADR-0087)
		"org.filter.label":          i18n.T(ctx, "Filtros do organograma"),
		"org.filter.tags":           i18n.T(ctx, "Tags"),
		"org.filter.tags.n":         i18n.T(ctx, "Tags ({n})"),
		"org.filter.notags":         i18n.T(ctx, "Nenhuma tag no cadastro."),
		"org.filter.untagged":       i18n.T(ctx, "Sem tag"),
		"org.filter.all":            i18n.T(ctx, "Exigir todas as tags (E)"),
		"org.filter.archived":       i18n.T(ctx, "Mostrar arquivadas"),
		"org.filter.manage":         i18n.T(ctx, "Gerenciar tags"),
		"org.filter.levels":         i18n.T(ctx, "Níveis"),
		"org.filter.levels.n":       i18n.T(ctx, "Níveis {n}"),
		"org.filter.level":          i18n.T(ctx, "Nível {n}"),
		"org.filter.depth":          i18n.T(ctx, "Até o nível"),
		"org.filter.depth.all":      i18n.T(ctx, "todos"),
		"org.filter.mode":           i18n.T(ctx, "Modo"),
		"org.filter.mode.highlight": i18n.T(ctx, "Destacar"),
		"org.filter.mode.prune":     i18n.T(ctx, "Podar mantendo o caminho"),
		"org.filter.clear":          i18n.T(ctx, "Limpar filtros"),
		"org.filter.empty":          i18n.T(ctx, "Nenhuma persona nesse recorte."),
		"org.tags.edit":             i18n.T(ctx, "Tags de {name}"),
		"org.tags.menu":             i18n.T(ctx, "Aplicar ou tirar tags de {name}"),
		"org.tags.search":           i18n.T(ctx, "Buscar ou criar tag"),
		"org.tags.create":           i18n.T(ctx, "Criar tag «{name}»"),
		"org.tags.close":            i18n.T(ctx, "Fechar"),
		"org.tags.applied":          i18n.T(ctx, "Tag {tag} aplicada a {name}."),
		"org.tags.removed":          i18n.T(ctx, "Tag {tag} tirada de {name}."),
		"org.tags.err":              i18n.T(ctx, "Não foi possível mudar a tag: {err}"),
	})
}

func anchorMsgs(ctx context.Context) string {
	return msgsJSON(map[string]string{
		"anchor.doc":         i18n.T(ctx, "Texto do documento. Selecione um trecho para comentar."),
		"anchor.open":        i18n.T(ctx, "Ver comentários deste trecho"),
		"anchor.comment":     i18n.T(ctx, "Comentar neste trecho"),
		"anchor.placeholder": i18n.T(ctx, "Escreva o comentário sobre o trecho"),
		"anchor.send":        i18n.T(ctx, "Enviar"),
		"anchor.sending":     i18n.T(ctx, "Enviando…"),
		"anchor.cancel":      i18n.T(ctx, "Cancelar"),
		"anchor.sent":        i18n.T(ctx, "Comentário enviado."),
		"anchor.you":         i18n.T(ctx, "Você"),
		"anchor.thread":      i18n.T(ctx, "Comentários do trecho"),
		"anchor.lost":        i18n.T(ctx, "âncora perdida"),
		"anchor.moved":       i18n.T(ctx, "trecho movido"),
		"anchor.lost.title":  i18n.T(ctx, "Comentários com âncora perdida"),
		"anchor.err.empty":   i18n.T(ctx, "Escreva algo antes de enviar."),
		"anchor.err.send":    i18n.T(ctx, "Não foi possível enviar o comentário."),
	})
}

// orgNodesJSON is the tree handed to the org-chart island (post-order, siblings in chart order).
func orgNodesJSON(vm views.OrgVM) string {
	type node struct {
		ID       string   `json:"id"`
		Slug     string   `json:"slug"`
		Name     string   `json:"name"`
		Title    string   `json:"title"`
		Parent   string   `json:"parent"`
		Status   string   `json:"status"`
		Cls      string   `json:"cls"`
		Meta     string   `json:"meta"`
		Href     string   `json:"href"`
		Gate     bool     `json:"gate"`
		Initials string   `json:"initials"`
		Icon     string   `json:"icon"`
		Tags     []string `json:"tags"`
	}
	out := make([]node, 0, len(vm.Nodes))
	for _, n := range vm.Nodes {
		tags := n.TagIDs
		if tags == nil {
			tags = []string{}
		}
		out = append(out, node{ID: n.ID, Slug: n.Slug, Name: n.Name, Title: n.Title, Parent: n.ParentID, Status: n.Status,
			Cls: n.StatusClass, Meta: n.Meta, Href: n.Href, Gate: n.Gate, Initials: n.Initials, Icon: n.Icon, Tags: tags})
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// anchorViewsJSON lists, for one document, the anchored comments that point into it, already resolved against
// the current text (status exact/moved/lost and offsets in code points) by the service.
func anchorViewsJSON(ctx context.Context, comments []domain.Comment, names lookup, loc *time.Location, target domain.AnchorTarget, runID *domain.ID) string {
	type view struct {
		ID     string `json:"id"`
		Quote  string `json:"quote"`
		Start  int    `json:"start"`
		End    int    `json:"end"`
		Status string `json:"status"`
		Author string `json:"author"`
		When   string `json:"when"`
		Body   string `json:"body"`
	}
	out := []view{}
	for _, c := range comments {
		a := c.Anchor
		if a == nil || a.Target != target {
			continue
		}
		if target == domain.AnchorRunSummary && (runID == nil || a.RunID == nil || *a.RunID != *runID) {
			continue
		}
		v := view{ID: c.ID.String(), Quote: a.Quote, Status: domain.AnchorLost, Author: names.actorName(c.Author),
			When: fmtDateTime(ctx, c.CreatedAt, loc), Body: clipRunes(strings.TrimSpace(c.Body), 600)}
		if st := c.AnchorState; st != nil {
			v.Status, v.Start, v.End = st.Status, st.Start, st.End
		}
		out = append(out, v)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

// quoteOf is the citation shown above an anchored comment in the thread, and whether its anchor is lost.
func quoteOf(c domain.Comment) (quote string, lost bool) {
	if c.Anchor == nil {
		return "", false
	}
	return clipRunes(c.Anchor.Quote, 400), c.AnchorState != nil && c.AnchorState.Status == domain.AnchorLost
}

// docOf is a document as anchors see it (line endings normalized, as the browser shows it).
func docOf(s string) string { return domain.NormalizeDoc(s) }
