package ui

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

const gateAged = 24 * time.Hour

func gateKindLabel(ctx context.Context, k domain.GateKind) string {
	switch k {
	case domain.GateApproval:
		return i18n.T(ctx, "Aprovação")
	case domain.GateTaskReview:
		return i18n.T(ctx, "Revisão de task")
	case domain.GateHire:
		return i18n.T(ctx, "Contratação")
	case domain.GateMemory:
		return i18n.T(ctx, "Memória")
	case domain.GateTool:
		return i18n.T(ctx, "Tool")
	}
	return string(k)
}

// gateBadge: amber is reserved for what waits for a human.
func gateBadge(ctx context.Context, s domain.GateStatus) views.Badge {
	switch s {
	case domain.GatePending:
		return views.Badge{Label: i18n.T(ctx, "Pendente"), Class: "warn"}
	case domain.GateApproved:
		return views.Badge{Label: i18n.T(ctx, "Aprovado"), Class: "done"}
	case domain.GateRejected:
		return views.Badge{Label: i18n.T(ctx, "Rejeitado"), Class: "danger"}
	case domain.GateChangesRequested:
		return views.Badge{Label: i18n.T(ctx, "Mudanças pedidas"), Class: "outline"}
	case domain.GateExpired:
		return views.Badge{Label: i18n.T(ctx, "Expirado"), Class: "soft"}
	}
	return views.Badge{Label: string(s), Class: "outline"}
}

var mdNoise = strings.NewReplacer("**", "", "__", "", "`", "", "#", "", ">", "", "*", "")

// excerpt is a one-line plain-text teaser of a markdown body.
func excerpt(body string, n int) string {
	t := strings.Join(strings.Fields(mdNoise.Replace(body)), " ")
	if utf8.RuneCountInString(t) <= n {
		return t
	}
	r := []rune(t)
	return strings.TrimSpace(string(r[:n])) + "…"
}

func gateRow(ctx context.Context, g service.GateView, base string, now time.Time, loc *time.Location, names lookup) views.GateRow {
	row := views.GateRow{
		ID:         g.ID.String(),
		Short:      shortID(g.ID),
		Href:       base + "/gates/" + g.ID.String(),
		Title:      g.Title,
		Kind:       string(g.Kind),
		KindLabel:  gateKindLabel(ctx, g.Kind),
		Badge:      gateBadge(ctx, g.Status),
		TaskKey:    g.TaskKey,
		TaskTitle:  g.TaskTitle,
		Persona:    g.PersonaName,
		Excerpt:    excerpt(g.Body, 170),
		Created:    agoCtx(ctx, g.CreatedAt, now, loc),
		Note:       g.DecisionNote,
		Pending:    g.Status == domain.GatePending,
		DecideHref: base + "/gates/" + g.ID.String() + "/decidir",
	}
	if rv := g.Review; rv != nil {
		info := &views.ReviewInfo{Reviewer: rv.Reviewer, Justification: rv.Justification, SHA: rv.ArtifactSHA256,
			Badge: views.Badge{Label: i18n.T(ctx, "Sem veredito do revisor"), Class: "outline"}}
		switch rv.Verdict {
		case domain.ReviewApproved:
			info.Badge = views.Badge{Label: i18n.T(ctx, "Aprovado pelo revisor"), Class: "done"}
		case domain.ReviewRejected:
			info.Badge = views.Badge{Label: i18n.T(ctx, "Reprovado pelo revisor"), Class: "danger"}
			info.Rejected = true
		}
		row.Review = info
	}
	if g.TaskKey != "" {
		row.TaskHref = base + "/tasks/" + g.TaskKey
	}
	if g.PersonaID != nil {
		if p, ok := names.personas[*g.PersonaID]; ok {
			row.Persona, row.PersonaHref = p.Name, base+"/personas/"+p.Slug
		}
	}
	row.Aged = row.Pending && now.Sub(g.CreatedAt) > gateAged
	if g.DecidedAt != nil {
		row.Decided = agoCtx(ctx, *g.DecidedAt, now, loc)
	}
	if g.DecidedBy != nil {
		row.DecidedBy = names.actorName(*g.DecidedBy)
	}
	return row
}

func gateRows(ctx context.Context, list []service.GateView, base string, now time.Time, loc *time.Location, names lookup) []views.GateRow {
	out := make([]views.GateRow, 0, len(list))
	for _, g := range list {
		out = append(out, gateRow(ctx, g, base, now, loc, names))
	}
	return out
}

var gateNotices = map[string]string{
	"aprovado":  i18n.N("Gate aprovado. A decisão foi registrada e a persona, se houver, é acordada para seguir."),
	"rejeitado": i18n.N("Gate rejeitado. A decisão foi registrada; a task volta para A fazer com a sua nota."),
	"mudancas":  i18n.N("Mudanças pedidas. A task volta para Em andamento com a sua nota."),
}

type gateTab struct {
	key, label string
	statuses   []domain.GateStatus
}

var gateStatusTabs = []gateTab{
	{"pendentes", i18n.N("Pendentes"), []domain.GateStatus{domain.GatePending}},
	{"aprovados", i18n.N("Aprovados"), []domain.GateStatus{domain.GateApproved}},
	{"mudancas", i18n.N("Mudanças pedidas"), []domain.GateStatus{domain.GateChangesRequested}},
	{"rejeitados", i18n.N("Rejeitados"), []domain.GateStatus{domain.GateRejected, domain.GateExpired}},
	{"todos", i18n.N("Todos"), nil},
}

type gateKindTab struct {
	key, label string
	kind       domain.GateKind
}

var gateKindTabs = []gateKindTab{
	{"todos", i18n.N("Todos os tipos"), ""},
	{"aprovacao", i18n.N("Aprovação"), domain.GateApproval},
	{"revisao", i18n.N("Revisão"), domain.GateTaskReview},
	{"contratacao", i18n.N("Contratação"), domain.GateHire},
	{"memoria", i18n.N("Memória"), domain.GateMemory},
	{"tools", i18n.N("Tools"), domain.GateTool},
}

func gateStatusIn(st domain.GateStatus, list []domain.GateStatus) bool {
	return list == nil || has(list, st)
}

func has[T comparable](list []T, v T) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (s *server) handleGates(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	mine := personal(ctx) && r.URL.Query().Get("minhas") == "1"
	all, err := s.be.ListGates(ctx, co.ID, service.GateFilter{Limit: 500, Mine: mine})
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	personas, err := s.be.ListPersonas(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	names := s.lookup(r.Context(), personas, nil)
	base := "/c/" + co.Slug
	loc, now := loadLoc(co.Timezone), s.now()

	curS, curK := gateStatusTabs[0], gateKindTabs[0]
	for _, t := range gateStatusTabs {
		if t.key == r.URL.Query().Get("status") {
			curS = t
		}
	}
	for _, t := range gateKindTabs {
		if t.key == r.URL.Query().Get("tipo") {
			curK = t
		}
	}
	href := func(status, kind string) string {
		h := base + "/gates?status=" + status
		if kind != "todos" {
			h += "&tipo=" + kind
		}
		if mine {
			h += "&minhas=1"
		}
		return h
	}
	vm := views.GatesVM{Mine: mine}
	if personal(ctx) {
		vm.MineHref = href(curS.key, curK.key)
		if mine {
			vm.MineHref = strings.Replace(vm.MineHref, "&minhas=1", "", 1)
		} else {
			vm.MineHref += "&minhas=1"
		}
	}
	for _, t := range gateStatusTabs {
		n := 0
		for _, g := range all {
			if gateStatusIn(g.Status, t.statuses) && (curK.kind == "" || g.Kind == curK.kind) {
				n++
			}
		}
		vm.Status = append(vm.Status, views.TabLink{Key: t.key, Label: i18n.T(ctx, t.label), Href: href(t.key, curK.key), Count: n, On: t.key == curS.key})
	}
	for _, t := range gateKindTabs {
		n := 0
		for _, g := range all {
			if gateStatusIn(g.Status, curS.statuses) && (t.kind == "" || g.Kind == t.kind) {
				n++
			}
		}
		vm.Kinds = append(vm.Kinds, views.TabLink{Key: t.key, Label: i18n.T(ctx, t.label), Href: href(curS.key, t.key), Count: n, On: t.key == curK.key})
	}
	var shown []service.GateView
	pending := 0
	for _, g := range all {
		if g.Status == domain.GatePending {
			pending++
		}
		if gateStatusIn(g.Status, curS.statuses) && (curK.kind == "" || g.Kind == curK.kind) {
			shown = append(shown, g)
		}
	}
	vm.Rows = gateRows(ctx, shown, base, now, loc, names)
	vm.Notice = i18n.T(ctx, gateNotices[r.URL.Query().Get("ok")])
	switch pending {
	case 0:
		vm.Subtitle = i18n.T(ctx, "Pontos em que um humano decide. Nada pendente agora.")
	case 1:
		vm.Subtitle = i18n.T(ctx, "Pontos em que um humano decide. 1 pendente: a persona fica parada até a sua resposta.")
	default:
		vm.Subtitle = i18n.Tf(ctx, "Pontos em que um humano decide. %d pendentes: as personas ficam paradas até a sua resposta.", pending)
	}
	vm.Empty = i18n.T(ctx, "Nenhum gate corresponde a este filtro. Uma persona pede um gate antes de uma ação irreversível, merge, deploy, gasto ou contratação.")
	sh := s.shell(r, co, "gates", i18n.T(ctx, "Gates"), views.Crumb{Label: i18n.T(ctx, "Gates")})
	s.render(w, r, http.StatusOK, views.GatesPage(sh, vm))
}

func (s *server) handleGate(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	id, err := domain.ParseID(r.PathValue("gate"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	g, err := s.be.GetGate(ctx, co.ID, id)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	personas, err := s.be.ListPersonas(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	base := "/c/" + co.Slug
	loc, now := loadLoc(co.Timezone), s.now()
	vm := views.GateVM{
		Row:     gateRow(ctx, g, base, now, loc, s.lookup(ctx, personas, nil)),
		Body:    renderMD(g.Body),
		HasBody: strings.TrimSpace(g.Body) != "",
		Created: fmtDateTime(ctx, g.CreatedAt, loc),
		Notice:  i18n.T(ctx, gateNotices[r.URL.Query().Get("ok")]),
	}
	if g.DecidedAt != nil {
		vm.Decided = fmtDateTime(ctx, *g.DecidedAt, loc)
	}
	if g.RunID != nil {
		vm.RunHref, vm.RunShort = base+"/execucoes/"+g.RunID.String(), shortID(*g.RunID)
	}
	vm.Example = s.gateExample(ctx, co, g) // ADR-0034
	sh := s.shell(r, co, "gates", g.Title, views.Crumb{Label: i18n.T(ctx, "Gates"), Href: base + "/gates"}, views.Crumb{Label: shortID(g.ID)})
	s.render(w, r, http.StatusOK, views.GatePage(sh, vm))
}

// handleDecideGate answers a gate (approve, reject, ask for changes) and goes back to where the
// human was: the inbox, the task, the dashboard or the gate itself.
func (s *server) handleDecideGate(w http.ResponseWriter, r *http.Request, co domain.Company) {
	id, err := domain.ParseID(r.PathValue("gate"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	decision := domain.GateStatus(trimForm(r, "decision"))
	always := decision == "approved_always" // "aprovar e sempre permitir" (ADR-0053)
	if always {
		decision = domain.GateApproved
	}
	if !decision.Decision() {
		s.fail(w, r, &co, domain.Invalid(i18n.T(r.Context(), "decisão"), i18n.T(r.Context(), "escolha aprovar, rejeitar ou pedir mudanças")))
		return
	}
	note := strings.TrimSpace(r.PostFormValue("note"))
	if decision == domain.GateChangesRequested && note == "" {
		s.fail(w, r, &co, domain.Invalid(i18n.T(r.Context(), "nota"), i18n.T(r.Context(), "explique o que precisa mudar")))
		return
	}
	g, err := s.be.DecideGate(r.Context(), actorFrom(r.Context()), co.ID, id, service.DecideGateInput{Decision: decision, Note: note, Always: always})
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	ok := map[domain.GateStatus]string{domain.GateApproved: "aprovado", domain.GateRejected: "rejeitado", domain.GateChangesRequested: "mudancas"}[decision]
	base := "/c/" + co.Slug
	to := base + "/gates/" + g.ID.String() + "?ok=" + ok
	switch trimForm(r, "next") {
	case "caixa":
		to = base + "/caixa?ok=" + ok
	case "painel":
		to = base + "/?ok=" + ok
	case "task":
		if g.TaskKey != "" {
			to = base + "/tasks/" + g.TaskKey + "?ok=" + ok
		}
	}
	redirect(w, r, to)
}

type decisionTab struct {
	key, label string
	status     domain.GateStatus
}

var decisionTabs = []decisionTab{
	{"todas", i18n.N("Todas"), ""},
	{"aprovadas", i18n.N("Aprovadas"), domain.GateApproved},
	{"mudancas", i18n.N("Mudanças pedidas"), domain.GateChangesRequested},
	{"rejeitadas", i18n.N("Rejeitadas"), domain.GateRejected},
}

func (s *server) handleDecisions(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	all, err := s.be.ListGates(ctx, co.ID, service.GateFilter{
		Statuses: []domain.GateStatus{domain.GateApproved, domain.GateRejected, domain.GateChangesRequested}, Limit: 500})
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	personas, err := s.be.ListPersonas(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	names := s.lookup(r.Context(), personas, nil)
	base := "/c/" + co.Slug
	loc, now := loadLoc(co.Timezone), s.now()
	cur := decisionTabs[0]
	for _, t := range decisionTabs {
		if t.key == r.URL.Query().Get("d") {
			cur = t
		}
	}
	vm := views.DecisionsVM{Subtitle: i18n.T(ctx, "O que você decidiu, com a sua nota. Cada decisão também vira memória da empresa.")}
	var shown []service.GateView
	for _, t := range decisionTabs {
		n := 0
		for _, g := range all {
			if t.status == "" || g.Status == t.status {
				n++
			}
		}
		vm.Tabs = append(vm.Tabs, views.TabLink{Key: t.key, Label: i18n.T(ctx, t.label), Href: base + "/decisoes?d=" + t.key, Count: n, On: t.key == cur.key})
	}
	for _, g := range all {
		if cur.status == "" || g.Status == cur.status {
			shown = append(shown, g)
		}
	}
	sort.SliceStable(shown, func(i, j int) bool {
		a, b := shown[i].CreatedAt, shown[j].CreatedAt
		if shown[i].DecidedAt != nil {
			a = *shown[i].DecidedAt
		}
		if shown[j].DecidedAt != nil {
			b = *shown[j].DecidedAt
		}
		return a.After(b)
	})
	vm.Rows = gateRows(ctx, shown, base, now, loc, names)
	sh := s.shell(r, co, "decisoes", i18n.T(ctx, "Decisões"), views.Crumb{Label: i18n.T(ctx, "Decisões")})
	s.render(w, r, http.StatusOK, views.DecisionsPage(sh, vm))
}
