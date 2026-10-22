package ui

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Cards de status (ADR-0032). Ler é de qualquer membro; criar, configurar e atualizar é de member; arquivar é de
// quem criou ou de admin; apagar é de admin. A barreira é o service; a UI só esconde os controles.

// maxStatusCardFormBytes é o teto do corpo do formulário (interesse de 20 mil caracteres e os outros campos).
const maxStatusCardFormBytes = 128 << 10

var statusCardNotices = map[string]string{
	"criado":      i18n.N("Card criado. A resumidora está montando os filtros."),
	"salvo":       i18n.N("Configuração salva."),
	"atualizando": i18n.N("Atualização pedida: a resumidora está escrevendo o resumo."),
	"montando":    i18n.N("Montagem pedida: a resumidora vai refazer os filtros."),
	"arquivado":   i18n.N("Card arquivado."),
	"restaurado":  i18n.N("Card restaurado: o resumo vai ser refeito do zero."),
	"apagado":     i18n.N("Card apagado."),
}

type statusCardTab struct{ key, label string }

var statusCardTabs = []statusCardTab{
	{"resumo", i18n.N("Resumo")},
	{"configuracao", i18n.N("Configuração")},
	{"tasks", i18n.N("Tasks observadas")},
	{"historico", i18n.N("Histórico")},
	{"filtros", i18n.N("Filtros")},
}

func (s *server) statusCardRoutes(mux langMux) {
	co := func(h companyHandler) http.HandlerFunc { return s.withCompany(h) }
	post := func(h http.HandlerFunc) http.HandlerFunc { return s.sameOrigin(h) }
	capped := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxStatusCardFormBytes)
			h(w, r)
		}
	}
	const base = "/c/{company}/cards-de-status"
	mux.HandleFunc("GET "+base, co(s.handleStatusCards))
	mux.HandleFunc("GET "+base+"/novo", co(s.handleNewStatusCard))
	mux.HandleFunc("POST "+base+"/novo", capped(post(co(s.handleCreateStatusCard))))
	mux.HandleFunc("GET "+base+"/{card}", co(s.handleStatusCard))
	mux.HandleFunc("GET "+base+"/{card}/estado", co(s.handleStatusCardLive))
	mux.HandleFunc("POST "+base+"/{card}", capped(post(co(s.handleUpdateStatusCard))))
	mux.HandleFunc("POST "+base+"/{card}/atualizar", post(co(s.handleStatusCardRefresh)))
	mux.HandleFunc("POST "+base+"/{card}/montar", post(co(s.handleStatusCardRecompile)))
	mux.HandleFunc("POST "+base+"/{card}/arquivar", post(co(s.handleStatusCardArchive(true))))
	mux.HandleFunc("POST "+base+"/{card}/restaurar", post(co(s.handleStatusCardArchive(false))))
	mux.HandleFunc("POST "+base+"/{card}/excluir", post(co(s.handleStatusCardDelete)))
}

func cardsBase(co domain.Company) string { return "/c/" + co.Slug + "/cards-de-status" }

// ---------- rótulos ----------

func statusCardBadge(ctx context.Context, v domain.StatusCardView) views.Badge {
	switch v {
	case domain.CardViewArchived:
		return views.Badge{Label: i18n.T(ctx, "Arquivado"), Class: "soft"}
	case domain.CardViewCompiling:
		return views.Badge{Label: i18n.T(ctx, "Montando"), Class: "run", Live: true}
	case domain.CardViewError:
		return views.Badge{Label: i18n.T(ctx, "Erro"), Class: "danger"}
	case domain.CardViewPausedBudget:
		return views.Badge{Label: i18n.T(ctx, "Pausado — orçamento"), Class: "warn"}
	case domain.CardViewPausedHours:
		return views.Badge{Label: i18n.T(ctx, "Pausado — horário"), Class: "warn"}
	case domain.CardViewUpdating:
		return views.Badge{Label: i18n.T(ctx, "Atualizando"), Class: "run", Live: true}
	case domain.CardViewStale:
		return views.Badge{Label: i18n.T(ctx, "Desatualizado"), Class: "outline"}
	}
	return views.Badge{Label: i18n.T(ctx, "Em dia"), Class: "done"}
}

func cardTitle(ctx context.Context, v service.StatusCardView) (string, bool) {
	if strings.TrimSpace(v.Title) == "" {
		return i18n.T(ctx, "Card sem título"), true
	}
	return v.Title, false
}

func cardBusy(v service.StatusCardView) bool {
	return v.ArchivedAt == nil && (v.GeneratingTaskID != nil || (v.Engine == domain.CardEngineDecision && v.GeneratingSince != nil))
}

// cardCompileFailed: a montagem não terminou e o card não tem filtros.
func cardCompileFailed(v service.StatusCardView) bool {
	return v.ArchivedAt == nil && v.State == domain.CardError && v.QueryVersion == 0 && v.GeneratingTaskID == nil
}

// canArchiveCard: quem criou o card (humano) ou admin.
func canArchiveCard(ctx context.Context, v service.StatusCardView) bool {
	if views.RoleFrom(ctx).AtLeast(domain.MemberAdmin) {
		return true
	}
	if !views.RoleFrom(ctx).AtLeast(domain.MemberMember) {
		return false
	}
	a := actorFrom(ctx)
	return a.ID != nil && v.CreatedBy.Type == a.Type && v.CreatedBy.ID != nil && *v.CreatedBy.ID == *a.ID
}

func (s *server) cardTile(ctx context.Context, co domain.Company, v service.StatusCardView, now time.Time, loc *time.Location) views.StatusCardTile {
	href := cardsBase(co) + "/" + v.ID.String()
	title, untitled := cardTitle(ctx, v)
	t := views.StatusCardTile{Title: title, Untitled: untitled, Href: href, Badge: statusCardBadge(ctx, v.ViewState), Summarizer: v.SummarizerName,
		Pending: v.PendingChangeCount, Compiling: v.ViewState == domain.CardViewCompiling, CompileFailed: cardCompileFailed(v),
		Archived: v.ArchivedAt != nil, RefreshURL: href + "/atualizar", RecompileURL: href + "/montar", ArchiveURL: href + "/arquivar",
		RestoreURL: href + "/restaurar", CanRefresh: views.RoleFrom(ctx).AtLeast(domain.MemberMember), CanArchive: canArchiveCard(ctx, v)}
	if v.Summary != "" {
		t.Summary = renderMD(clipSummary(v.Summary, 1200))
	}
	if v.LastGeneratedAt != nil {
		t.Updated = agoCtx(ctx, *v.LastGeneratedAt, now, loc)
	}
	return t
}

// clipSummary corta a prévia do resumo num fim de linha (o markdown cortado no meio de um bloco fica feio).
func clipSummary(md string, n int) string {
	if len(md) <= n {
		return md
	}
	cut := strings.LastIndex(md[:n], "\n")
	if cut < n/2 {
		cut = n
	}
	return strings.ToValidUTF8(md[:cut], "") + "\n\n…"
}

// ---------- lista ----------

func (s *server) handleStatusCards(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	list, err := s.be.ListStatusCards(ctx, co.ID, nil)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	loc, now := loadLoc(co.Timezone), s.now()
	q := r.URL.Query()
	vm := views.StatusCardsVM{Subtitle: i18n.T(ctx, "Resumos vivos das tasks que importam, mantidos por uma persona."),
		Notice: i18n.T(ctx, statusCardNotices[q.Get("ok")]), NewHref: cardsBase(co) + "/novo", GridHref: cardsBase(co) + "?parcial=1",
		ShowArchived: q.Get("arquivados") == "1"}
	for _, v := range list {
		t := s.cardTile(ctx, co, v, now, loc)
		if v.ArchivedAt != nil {
			vm.Archived = append(vm.Archived, t)
			continue
		}
		vm.Cards = append(vm.Cards, t)
		vm.Busy = vm.Busy || cardBusy(v)
	}
	if q.Get("parcial") == "1" {
		s.render(w, r, http.StatusOK, views.StatusCardsGrid(vm))
		return
	}
	sh := s.shell(r, co, "cards-status", i18n.T(ctx, "Cards de status"), views.Crumb{Label: i18n.T(ctx, "Cards de status")})
	s.render(w, r, http.StatusOK, views.StatusCardsPage(sh, vm))
}

// ---------- formulário ----------

func (s *server) cardSummarizerOptions(ctx context.Context, co domain.Company, sel *domain.ID) []views.Option {
	personas, _ := s.be.ListPersonas(ctx, co.ID) // sem a lista o select fica só com a opção vazia
	return personaOptions(personas, sel, i18n.T(ctx, "Escolha a resumidora"))
}

func policyForm(vm *views.StatusCardFormVM, p domain.CardPolicy) {
	vm.Mode = p.Mode
	vm.CronExpr, vm.CronTZ, vm.CronSkip = p.Cron, p.CronTZ, p.CronSkipUnchanged
	vm.IntervalMinutes, vm.DebounceSeconds = strconv.Itoa(p.IntervalMinutes), strconv.Itoa(p.DebounceSeconds)
	vm.MaxPerHour, vm.DailyTokenCap = strconv.Itoa(p.MaxUpdatesPerHour), strconv.FormatInt(p.DailyTokenCap, 10)
	vm.TrStatus, vm.TrMembership, vm.TrHuman, vm.TrAssignee, vm.TrAny = p.Triggers.Status, p.Triggers.Membership, p.Triggers.HumanComments,
		p.Triggers.Assignee, p.Triggers.AnyUpdate
	if p.ActiveHours != nil {
		vm.HoursStart, vm.HoursEnd, vm.HoursTZ = p.ActiveHours.Start, p.ActiveHours.End, p.ActiveHours.TZ
	}
}

func (s *server) blankStatusCardForm(ctx context.Context, co domain.Company) views.StatusCardFormVM {
	vm := views.StatusCardFormVM{Action: cardsBase(co) + "/novo", CancelHref: cardsBase(co), FieldErr: map[string]string{},
		SummarizerOptions: s.cardSummarizerOptions(ctx, co, nil), HoursTZ: co.Timezone, Engine: string(domain.CardEnginePersona),
		CronTZ: co.Timezone, DecisionFields: true, QueryProject: s.cardProjectOptions(ctx, co, nil)}
	policyForm(&vm, domain.DefaultCardPolicy())
	vm.CronTZ = co.Timezone
	return vm
}

// statusCardFormValues lê o formulário para o modelo (para reexibir em caso de erro).
func statusCardFormValues(r *http.Request, vm *views.StatusCardFormVM) {
	vm.Interest, vm.Title = strings.TrimSpace(r.PostFormValue("interest")), trimForm(r, "title")
	vm.TitlePinned = r.PostFormValue("title_pinned") == "1"
	vm.Mode = trimForm(r, "mode")
	vm.IntervalMinutes, vm.DebounceSeconds = trimForm(r, "interval_minutes"), trimForm(r, "debounce_seconds")
	vm.MaxPerHour, vm.DailyTokenCap = trimForm(r, "max_updates_per_hour"), trimForm(r, "daily_token_cap")
	vm.TrStatus, vm.TrMembership = r.PostFormValue("tr_status") == "1", r.PostFormValue("tr_membership") == "1"
	vm.TrHuman, vm.TrAssignee, vm.TrAny = r.PostFormValue("tr_human") == "1", r.PostFormValue("tr_assignee") == "1", r.PostFormValue("tr_any") == "1"
	vm.HoursStart, vm.HoursEnd, vm.HoursTZ = trimForm(r, "hours_start"), trimForm(r, "hours_end"), trimForm(r, "hours_tz")
	selectOption(vm.SummarizerOptions, trimForm(r, "summarizer"))
	if !vm.Edit {
		vm.Engine = trimForm(r, "engine")
	}
	vm.CronExpr, vm.CronTZ, vm.CronSkip = trimForm(r, "cron"), trimForm(r, "cron_tz"), r.PostFormValue("cron_skip") == "1"
	vm.OnDashboard = r.PostFormValue("on_dashboard") == "1"
	vm.QueryStatuses, vm.QueryOpen = trimForm(r, "q_status"), r.PostFormValue("q_open") == "1"
	if len(vm.QueryProject) > 0 {
		selectOption(vm.QueryProject, trimForm(r, "q_project"))
	}
}

// cardProjectOptions lista os projetos para o filtro do card do decision model (a primeira opção é "todos").
func (s *server) cardProjectOptions(ctx context.Context, co domain.Company, sel *domain.ID) []views.Option {
	out := []views.Option{{Value: "", Label: i18n.T(ctx, "Todos os projetos"), Selected: sel == nil}}
	ps, err := s.be.ListProjects(ctx, co.ID)
	if err != nil {
		return out
	}
	for _, p := range ps {
		out = append(out, views.Option{Value: p.Slug, Label: p.Name, Selected: sel != nil && *sel == p.ID})
	}
	return out
}

// cardQueriesFromForm monta o filtro único do card do decision model (nil = as tasks abertas, o padrão do service).
func cardQueriesFromForm(vm views.StatusCardFormVM) []service.CardQueryInput {
	var statuses []string
	for _, st := range strings.Split(vm.QueryStatuses, ",") {
		if st = strings.TrimSpace(st); st != "" {
			statuses = append(statuses, st)
		}
	}
	project := ""
	for _, o := range vm.QueryProject {
		if o.Selected {
			project = o.Value
		}
	}
	if len(statuses) == 0 && project == "" && !vm.QueryOpen {
		return nil
	}
	return []service.CardQueryInput{{Statuses: statuses, Project: project, OpenOnly: vm.QueryOpen}}
}

func formInt(field, v string) (int, error) {
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(strings.ReplaceAll(strings.ReplaceAll(v, ".", ""), " ", ""))
	if err != nil {
		return 0, domain.Invalid("refresh_policy."+field, "invalid")
	}
	return n, nil
}

// cardPolicyFromForm monta a política; os números vazios ficam com o padrão.
func cardPolicyFromForm(vm views.StatusCardFormVM) (domain.CardPolicy, error) {
	p := domain.CardPolicy{Mode: vm.Mode, Triggers: domain.CardTriggers{Status: vm.TrStatus, Membership: vm.TrMembership,
		HumanComments: vm.TrHuman, Assignee: vm.TrAssignee, AnyUpdate: vm.TrAny}, Cron: vm.CronExpr, CronTZ: vm.CronTZ, CronSkipUnchanged: vm.CronSkip}
	var err error
	if p.IntervalMinutes, err = formInt("interval_minutes", vm.IntervalMinutes); err != nil {
		return p, err
	}
	if p.DebounceSeconds, err = formInt("debounce_seconds", vm.DebounceSeconds); err != nil {
		return p, err
	}
	if p.MaxUpdatesPerHour, err = formInt("max_updates_per_hour", vm.MaxPerHour); err != nil {
		return p, err
	}
	tokenCap, err := formInt("daily_token_cap", vm.DailyTokenCap)
	if err != nil {
		return p, err
	}
	p.DailyTokenCap = int64(tokenCap)
	if vm.HoursStart != "" || vm.HoursEnd != "" {
		p.ActiveHours = &domain.ActiveHours{Start: vm.HoursStart, End: vm.HoursEnd, TZ: vm.HoursTZ}
	}
	return p, normalizeCardPolicyUI(&p)
}

// normalizeCardPolicyUI normaliza a política e confere a expressão cron como o service faz.
func normalizeCardPolicyUI(p *domain.CardPolicy) error {
	if err := p.Normalize(); err != nil {
		return err
	}
	if p.Mode == domain.CardModeCron {
		if _, err := service.ParseCron(p.Cron, p.CronTZ); err != nil {
			return domain.Invalid("refresh_policy.cron", "invalid")
		}
	}
	return nil
}

func statusCardFormError(ctx context.Context, vm *views.StatusCardFormVM, err error) {
	var ve *domain.ValidationError
	if errors.As(err, &ve) {
		field := strings.TrimPrefix(ve.Field, "refresh_policy.")
		if strings.HasPrefix(field, "active_hours") {
			field = "active_hours"
		}
		msg := ""
		switch field {
		case "interest":
			msg = i18n.T(ctx, "Descreva o que acompanhar (até 20 mil caracteres).")
		case "title":
			msg = i18n.T(ctx, "Use até 200 caracteres no nome.")
		case "summarizer":
			msg = i18n.T(ctx, "Escolha uma persona desta empresa.")
		case "mode":
			msg = i18n.T(ctx, "Escolha quando atualizar.")
		case "cron":
			msg = i18n.T(ctx, "Informe uma expressão cron de 5 campos válida, por exemplo 0 7 * * 1-5.")
		case "cron_tz":
			msg = i18n.T(ctx, "Fuso desconhecido.")
		case "engine":
			msg = i18n.T(ctx, "Escolha a persona ou o decision model.")
		case "interval_minutes":
			msg = i18n.T(ctx, "Use de 15 a 1440 minutos.")
		case "debounce_seconds":
			msg = i18n.T(ctx, "Use de 30 a 3600 segundos.")
		case "max_updates_per_hour":
			msg = i18n.T(ctx, "Use de 1 a 60 atualizações por hora.")
		case "daily_token_cap":
			msg = i18n.T(ctx, "Use de 1.000 a 100.000.000 tokens.")
		case "triggers":
			msg = i18n.T(ctx, "Marque ao menos um tipo de mudança.")
		case "active_hours":
			msg = i18n.T(ctx, "Informe início e fim diferentes (HH:MM) e um fuso válido.")
		case "archived":
			msg = i18n.T(ctx, "Restaure o card para mudar a configuração.")
		default:
			msg = ve.Field + ": " + ve.Reason + "."
		}
		vm.FieldErr[field] = msg
		vm.Error = msg
		return
	}
	_, title, msg := friendly(ctx, err)
	vm.Error = title + ". " + msg
}

func (s *server) renderNewStatusCard(w http.ResponseWriter, r *http.Request, co domain.Company, status int, vm views.StatusCardFormVM) {
	ctx := r.Context()
	sh := s.shell(r, co, "cards-status", i18n.T(ctx, "Novo card de status"),
		views.Crumb{Label: i18n.T(ctx, "Cards de status"), Href: cardsBase(co)}, views.Crumb{Label: i18n.T(ctx, "Novo")})
	s.render(w, r, status, views.NewStatusCardPage(sh, vm))
}

func (s *server) handleNewStatusCard(w http.ResponseWriter, r *http.Request, co domain.Company) {
	s.renderNewStatusCard(w, r, co, http.StatusOK, s.blankStatusCardForm(r.Context(), co))
}

func (s *server) handleCreateStatusCard(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	vm := s.blankStatusCardForm(ctx, co)
	statusCardFormValues(r, &vm)
	bad := func(err error) {
		statusCardFormError(ctx, &vm, err)
		s.renderNewStatusCard(w, r, co, statusFor(err), vm)
	}
	pol, err := cardPolicyFromForm(vm)
	if err != nil {
		bad(err)
		return
	}
	in := service.CreateStatusCardInput{Interest: vm.Interest, Title: vm.Title, TitlePinned: vm.TitlePinned, Policy: &pol,
		Engine: domain.CardEngine(vm.Engine), OnDashboard: vm.OnDashboard}
	if vm.Engine == string(domain.CardEngineDecision) {
		in.Queries = cardQueriesFromForm(vm)
	} else {
		sum, ok := parseID(trimForm(r, "summarizer"))
		if !ok {
			bad(domain.Invalid("summarizer", "invalid"))
			return
		}
		in.SummarizerPersonaID = sum
	}
	v, err := s.be.CreateStatusCard(ctx, actorFrom(ctx), co.ID, in)
	if err != nil {
		bad(err)
		return
	}
	redirect(w, r, cardsBase(co)+"/"+v.ID.String()+"?ok=criado")
}

// ---------- detalhe ----------

func (s *server) statusCardFrom(w http.ResponseWriter, r *http.Request, co domain.Company) (service.StatusCardView, bool) {
	id, err := domain.ParseID(r.PathValue("card"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return service.StatusCardView{}, false
	}
	v, err := s.be.GetStatusCard(r.Context(), co.ID, id)
	if err != nil {
		s.fail(w, r, &co, err)
		return service.StatusCardView{}, false
	}
	return v, true
}

func cardEngineLabel(ctx context.Context, e domain.CardEngine) string {
	if e == domain.CardEngineDecision {
		return i18n.T(ctx, "Decision model: o resumo sai de uma chamada só ao modelo da empresa.")
	}
	return i18n.T(ctx, "Persona resumidora: uma run completa a cada atualização.")
}

// cardEngineForm completa o formulário de configuração com o motor, os filtros do decision model e a presença no Painel.
func (s *server) cardEngineForm(ctx context.Context, co domain.Company, v service.StatusCardView, f *views.StatusCardFormVM) {
	f.Engine, f.EngineLabel, f.OnDashboard = string(v.Engine), cardEngineLabel(ctx, v.Engine), v.OnDashboard
	if v.Engine != domain.CardEngineDecision {
		return
	}
	f.DecisionFields = true
	var proj *domain.ID
	if len(v.Queries) > 0 {
		q := v.Queries[0]
		proj, f.QueryOpen = q.ProjectID, q.OpenOnly
		sts := make([]string, len(q.Statuses))
		for i, st := range q.Statuses {
			sts[i] = string(st)
		}
		f.QueryStatuses = strings.Join(sts, ",")
	}
	f.QueryProject = s.cardProjectOptions(ctx, co, proj)
}

func (s *server) cardLiveVM(ctx context.Context, co domain.Company, v service.StatusCardView, rev *domain.StatusCardRevision, now time.Time, loc *time.Location) views.StatusCardLiveVM {
	href := cardsBase(co) + "/" + v.ID.String()
	lv := views.StatusCardLiveVM{Href: href + "/estado", Engine: string(v.Engine), Busy: cardBusy(v), Badge: statusCardBadge(ctx, v.ViewState),
		Compiling: v.ViewState == domain.CardViewCompiling, CompileFailed: cardCompileFailed(v), Pending: v.PendingChangeCount,
		RefreshURL: href + "/atualizar", RecompileURL: href + "/montar", CanRefresh: views.RoleFrom(ctx).AtLeast(domain.MemberMember) && v.ArchivedAt == nil}
	if v.State == domain.CardError && v.FailureReason != "" && !lv.CompileFailed {
		lv.Failure = v.FailureReason
	} else if lv.CompileFailed && v.FailureReason != "" {
		lv.Failure = v.FailureReason
	}
	switch v.ViewState {
	case domain.CardViewPausedBudget:
		lv.PausedMsg = i18n.T(ctx, "O teto diário de tokens deste card foi atingido; ele volta a se atualizar no dia seguinte.")
	case domain.CardViewPausedHours:
		lv.PausedMsg = i18n.T(ctx, "Fora do horário ativo: o card volta a se atualizar no início do horário.")
	}
	md, revision := v.Summary, v.SummaryRevision
	at := v.LastGeneratedAt
	if rev != nil {
		md, revision, at = rev.Markdown, rev.Revision, &rev.CreatedAt
	}
	if md != "" {
		lv.HasSummary, lv.Summary = true, renderMD(md)
		lv.RevisionLabel = i18n.Tf(ctx, "versão %d", revision)
		lv.Fallback = v.State == domain.CardError
	}
	if at != nil {
		lv.Updated = agoCtx(ctx, *at, now, loc)
	}
	if v.ArtifactID != nil {
		lv.ArtifactHref, lv.ArtifactName = "/c/"+co.Slug+"/artefatos/"+v.ArtifactID.String(), v.ArtifactName
	}
	return lv
}

func cardChangeText(ctx context.Context, ch domain.CardChange) string {
	switch ch.ChangeKind {
	case domain.ChangeEntered:
		return i18n.T(ctx, "entrou no conjunto")
	case domain.ChangeLeft:
		return i18n.T(ctx, "saiu do conjunto")
	case domain.ChangeStatus:
		return i18n.Tf(ctx, "mudou de status: %s → %s", taskStatusLabel(ctx, domain.TaskStatus(ch.From)), taskStatusLabel(ctx, domain.TaskStatus(ch.To)))
	case domain.ChangeAssignee:
		return i18n.T(ctx, "mudou de responsável")
	case domain.ChangeHumanComment:
		return i18n.T(ctx, "recebeu um comentário humano")
	}
	return i18n.T(ctx, "foi alterada")
}

func cardUpdateKindLabel(ctx context.Context, k domain.CardUpdateKind) string {
	switch k {
	case domain.CardUpdateCompile:
		return i18n.T(ctx, "Montagem")
	case domain.CardUpdateFull:
		return i18n.T(ctx, "Do zero")
	}
	return i18n.T(ctx, "Incremental")
}

func cardTriggerLabel(ctx context.Context, t domain.CardUpdateTrigger) string {
	switch t {
	case domain.CardTriggerManual:
		return i18n.T(ctx, "pedido")
	case domain.CardTriggerInterval:
		return i18n.T(ctx, "horário fixo")
	case domain.CardTriggerReactive:
		return i18n.T(ctx, "mudança")
	case domain.CardTriggerRestore:
		return i18n.T(ctx, "restauração")
	case domain.CardTriggerCron:
		return i18n.T(ctx, "horário (cron)")
	}
	return i18n.T(ctx, "filtros novos")
}

func cardUpdateBadge(ctx context.Context, st domain.CardUpdateStatus) views.Badge {
	switch st {
	case domain.CardUpdateRunning:
		return views.Badge{Label: i18n.T(ctx, "Em andamento"), Class: "run", Live: true}
	case domain.CardUpdateFailed:
		return views.Badge{Label: i18n.T(ctx, "Falhou"), Class: "danger"}
	}
	return views.Badge{Label: i18n.T(ctx, "Ok"), Class: "done"}
}

func (s *server) buildStatusCardVM(r *http.Request, co domain.Company, v service.StatusCardView) (views.StatusCardVM, error) {
	ctx := r.Context()
	base := "/c/" + co.Slug
	href := cardsBase(co) + "/" + v.ID.String()
	loc, now := loadLoc(co.Timezone), s.now()
	q := r.URL.Query()
	tab := statusCardTabs[0].key
	for _, t := range statusCardTabs {
		if t.key == q.Get("aba") {
			tab = t.key
		}
	}
	title, untitled := cardTitle(ctx, v)
	vm := views.StatusCardVM{Decision: v.Engine == domain.CardEngineDecision, Engine: cardEngineLabel(ctx, v.Engine), Title: title, Untitled: untitled, Badge: statusCardBadge(ctx, v.ViewState), Interest: v.Interest, Tab: tab,
		Notice: i18n.T(ctx, statusCardNotices[q.Get("ok")]), RevisionAction: href, RecompileURL: href + "/montar", ArchiveURL: href + "/arquivar",
		RestoreURL: href + "/restaurar", DeleteURL: href + "/excluir", Archived: v.ArchivedAt != nil, CanArchive: canArchiveCard(ctx, v),
		QueryVersion: v.QueryVersion}
	for _, t := range statusCardTabs {
		vm.Tabs = append(vm.Tabs, views.TabLink{Key: t.key, Label: i18n.T(ctx, t.label), Href: href + "?aba=" + t.key, On: t.key == tab})
	}
	if v.QueryCompiledAt != nil {
		vm.Compiled = agoCtx(ctx, *v.QueryCompiledAt, now, loc)
	}
	taskHref := func(key string) string { return base + "/tasks/" + key }
	switch tab {
	case "resumo":
		revs, err := s.be.ListStatusCardRevisions(ctx, co.ID, v.ID, domain.MaxCardRevisions)
		if err != nil {
			return vm, err
		}
		var shown *domain.StatusCardRevision
		want, _ := strconv.Atoi(q.Get("rev"))
		for i := range revs {
			rv := revs[i]
			sel := (want == 0 && i == 0) || rv.Revision == want
			if sel && want != 0 {
				shown = &revs[i]
			}
			label := i18n.Tf(ctx, "versão %d · %s", rv.Revision, fmtDateTime(ctx, rv.CreatedAt, loc))
			vm.RevisionOptions = append(vm.RevisionOptions, views.Option{Value: strconv.Itoa(rv.Revision), Label: label, Selected: sel})
		}
		vm.Live = s.cardLiveVM(ctx, co, v, shown, now, loc)
		// "Entrou nesta atualização": as mudanças da rodada que gerou a versão mostrada
		var runID *domain.ID
		switch {
		case shown != nil:
			runID = shown.RunID
		case len(revs) > 0:
			runID = revs[0].RunID
		}
		if runID != nil {
			ups, err := s.be.ListStatusCardUpdates(ctx, co.ID, v.ID, domain.MaxCardUpdates)
			if err != nil {
				return vm, err
			}
			for _, u := range ups {
				if u.RunID != nil && *u.RunID == *runID {
					for _, ch := range u.Changes {
						vm.Entered = append(vm.Entered, views.StatusCardChangeRow{Key: ch.Key, Href: taskHref(ch.Key), Text: cardChangeText(ctx, ch)})
					}
					break
				}
			}
		}
		for _, m := range v.Mentioned {
			vm.Mentioned = append(vm.Mentioned, views.StatusCardTaskRow{Key: m.Key, Href: taskHref(m.Key), Title: m.Title,
				Badge: taskBadge(ctx, service.TaskView{Task: domain.Task{Status: m.Status}}), Persona: m.PersonaName})
		}
	case "configuracao":
		vm.Form = views.StatusCardFormVM{Action: href, Edit: true, Title: v.Title, TitlePinned: v.TitlePinned, Interest: v.Interest,
			SummarizerOptions: s.cardSummarizerOptions(ctx, co, v.SummarizerPersonaID), FieldErr: map[string]string{}}
		policyForm(&vm.Form, v.Policy)
		if vm.Form.HoursTZ == "" {
			vm.Form.HoursTZ = co.Timezone
		}
		s.cardEngineForm(ctx, co, v, &vm.Form)
	case "tasks", "filtros":
		pv, err := s.be.PreviewStatusCard(ctx, co.ID, v.ID)
		if err != nil {
			return vm, err
		}
		vm.Truncated = pv.Truncated
		for i, cq := range pv.Queries {
			vm.Queries = append(vm.Queries, views.StatusCardQueryRow{N: i + 1, Description: cq.Description, Matches: cq.Matches})
		}
		for _, t := range pv.Tasks {
			row := views.StatusCardTaskRow{Key: t.Key, Href: taskHref(t.Key), Title: t.Title, Persona: t.PersonaName,
				Badge: taskBadge(ctx, service.TaskView{Task: domain.Task{Status: t.Status}}), Updated: agoCtx(ctx, t.UpdatedAt, now, loc)}
			if t.HumanCommentAt != nil {
				row.HumanComment = strings.Join(strings.Fields(truncRunes(t.HumanComment, 160)), " ")
				row.CommentWhen = agoCtx(ctx, *t.HumanCommentAt, now, loc)
			}
			vm.Tasks = append(vm.Tasks, row)
		}
	case "historico":
		ups, err := s.be.ListStatusCardUpdates(ctx, co.ID, v.ID, domain.MaxCardUpdates)
		if err != nil {
			return vm, err
		}
		for _, u := range ups {
			row := views.StatusCardUpdateRow{When: fmtDateTime(ctx, u.StartedAt, loc), Kind: cardUpdateKindLabel(ctx, u.Kind),
				Trigger: cardTriggerLabel(ctx, u.Trigger), Badge: cardUpdateBadge(ctx, u.Status), Changes: len(u.Changes),
				Tokens: strconv.FormatInt(u.InputTokens+u.OutputTokens, 10), Cost: "—", TaskKey: u.TaskKey, RunName: u.RunName,
				Summary: u.ChangeSummary, Error: u.Error}
			if u.CostUSD != nil {
				row.Cost = fmtUSDCtx(ctx, *u.CostUSD)
			}
			if u.TaskKey != "" {
				row.TaskHref = taskHref(u.TaskKey)
			}
			if u.RunID != nil {
				row.RunHref = base + "/execucoes/" + u.RunID.String()
			}
			vm.Updates = append(vm.Updates, row)
		}
	}
	return vm, nil
}

func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func (s *server) renderStatusCard(w http.ResponseWriter, r *http.Request, co domain.Company, status int, vm views.StatusCardVM) {
	ctx := r.Context()
	sh := s.shell(r, co, "cards-status", vm.Title, views.Crumb{Label: i18n.T(ctx, "Cards de status"), Href: cardsBase(co)}, views.Crumb{Label: vm.Title})
	s.render(w, r, status, views.StatusCardPage(sh, vm))
}

func (s *server) handleStatusCard(w http.ResponseWriter, r *http.Request, co domain.Company) {
	v, ok := s.statusCardFrom(w, r, co)
	if !ok {
		return
	}
	vm, err := s.buildStatusCardVM(r, co, v)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.renderStatusCard(w, r, co, http.StatusOK, vm)
}

// handleStatusCardLive é o fragmento do polling htmx (estado e resumo atual).
func (s *server) handleStatusCardLive(w http.ResponseWriter, r *http.Request, co domain.Company) {
	v, ok := s.statusCardFrom(w, r, co)
	if !ok {
		return
	}
	s.render(w, r, http.StatusOK, views.StatusCardLive(s.cardLiveVM(r.Context(), co, v, nil, s.now(), loadLoc(co.Timezone))))
}

func (s *server) handleUpdateStatusCard(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	v, ok := s.statusCardFrom(w, r, co)
	if !ok {
		return
	}
	form := views.StatusCardFormVM{Action: cardsBase(co) + "/" + v.ID.String(), Edit: true, FieldErr: map[string]string{},
		SummarizerOptions: s.cardSummarizerOptions(ctx, co, v.SummarizerPersonaID)}
	s.cardEngineForm(ctx, co, v, &form)
	statusCardFormValues(r, &form)
	bad := func(err error) {
		q := r.URL.Query()
		q.Set("aba", "configuracao")
		r.URL.RawQuery = q.Encode()
		vm, berr := s.buildStatusCardVM(r, co, v)
		if berr != nil {
			s.fail(w, r, &co, berr)
			return
		}
		statusCardFormError(ctx, &form, err)
		vm.Form = form
		s.renderStatusCard(w, r, co, statusFor(err), vm)
	}
	pol, err := cardPolicyFromForm(form)
	if err != nil {
		bad(err)
		return
	}
	in := service.UpdateStatusCardInput{Interest: &form.Interest, Title: &form.Title, TitlePinned: &form.TitlePinned, Policy: &pol,
		OnDashboard: &form.OnDashboard}
	if v.Engine == domain.CardEngineDecision {
		qs := cardQueriesFromForm(form)
		in.Queries = &qs
	} else {
		sum, ok := parseID(trimForm(r, "summarizer"))
		if !ok {
			bad(domain.Invalid("summarizer", "invalid"))
			return
		}
		in.SummarizerPersonaID = sum
	}
	if _, err := s.be.UpdateStatusCard(ctx, actorFrom(ctx), co.ID, v.ID, in); err != nil {
		bad(err)
		return
	}
	redirect(w, r, cardsBase(co)+"/"+v.ID.String()+"?aba=configuracao&ok=salvo")
}

// cardBack é para onde voltar depois de uma ação: a lista (botões da grade) ou o detalhe.
func cardBack(r *http.Request, co domain.Company, id domain.ID, notice string) string {
	if r.PostFormValue("de") == "lista" {
		return cardsBase(co) + "?ok=" + notice
	}
	return cardsBase(co) + "/" + id.String() + "?ok=" + notice
}

func (s *server) handleStatusCardRefresh(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	v, ok := s.statusCardFrom(w, r, co)
	if !ok {
		return
	}
	if _, err := s.be.RefreshStatusCard(ctx, actorFrom(ctx), co.ID, v.ID, r.PostFormValue("full") == "1"); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, cardBack(r, co, v.ID, "atualizando"))
}

func (s *server) handleStatusCardRecompile(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	v, ok := s.statusCardFrom(w, r, co)
	if !ok {
		return
	}
	if _, err := s.be.RecompileStatusCard(ctx, actorFrom(ctx), co.ID, v.ID); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, cardBack(r, co, v.ID, "montando"))
}

func (s *server) handleStatusCardArchive(archive bool) companyHandler {
	return func(w http.ResponseWriter, r *http.Request, co domain.Company) {
		ctx := r.Context()
		v, ok := s.statusCardFrom(w, r, co)
		if !ok {
			return
		}
		if _, err := s.be.UpdateStatusCard(ctx, actorFrom(ctx), co.ID, v.ID, service.UpdateStatusCardInput{Archived: &archive}); err != nil {
			s.fail(w, r, &co, err)
			return
		}
		if archive {
			redirect(w, r, cardsBase(co)+"?ok=arquivado")
			return
		}
		redirect(w, r, cardBack(r, co, v.ID, "restaurado"))
	}
}

func (s *server) handleStatusCardDelete(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	v, ok := s.statusCardFrom(w, r, co)
	if !ok {
		return
	}
	if err := s.be.DeleteStatusCard(ctx, actorFrom(ctx), co.ID, v.ID); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, cardsBase(co)+"?ok=apagado")
}
