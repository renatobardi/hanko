package service_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Cards de status (ADR-0032).

type cardEnv struct {
	*env
	now  time.Time
	sum  domain.Persona // a resumidora
	eng  domain.Persona // dona das tasks observadas
	site domain.Project
}

func newCardEnv(t *testing.T) *cardEnv {
	t.Helper()
	e := &cardEnv{env: newEnv(t), now: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)}
	e.svc.Now = func() time.Time { return e.now }
	e.sum = e.persona(t, "Resumidora", nil)
	e.eng = e.persona(t, "Eng", nil)
	pr, err := e.svc.CreateProject(context.Background(), domain.HumanActor, e.co.ID, service.CreateProjectInput{Name: "Site", Slug: "site"})
	if err != nil {
		t.Fatal(err)
	}
	e.site = pr
	return e
}

func (e *cardEnv) advance(d time.Duration) { e.now = e.now.Add(d) }

func (e *cardEnv) create(t *testing.T, mod func(*service.CreateStatusCardInput)) service.StatusCardView {
	t.Helper()
	in := service.CreateStatusCardInput{Interest: "tudo que está bloqueado no projeto Site", SummarizerPersonaID: &e.sum.ID}
	if mod != nil {
		mod(&in)
	}
	v, err := e.svc.CreateStatusCard(context.Background(), domain.HumanActor, e.co.ID, in)
	if err != nil {
		t.Fatalf("CreateStatusCard: %v", err)
	}
	return v
}

func (e *cardEnv) card(t *testing.T, id domain.ID) service.StatusCardView {
	t.Helper()
	v, err := e.svc.GetStatusCard(context.Background(), e.co.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// startGen abre a run da resumidora na task de geração atual do card.
func (e *cardEnv) startGen(t *testing.T, cardID domain.ID) (domain.Run, service.CardRunRef) {
	t.Helper()
	c := e.card(t, cardID)
	if c.GeneratingTaskID == nil {
		t.Fatalf("card sem geração: %+v", c.StatusCard)
	}
	ctx := context.Background()
	run, err := e.svc.CheckoutRun(ctx, service.Wake{CompanyID: e.co.ID, PersonaID: e.sum.ID, TaskID: *c.GeneratingTaskID, Trigger: domain.TriggerHeartbeat})
	if err != nil {
		t.Fatalf("CheckoutRun: %v", err)
	}
	if run.Trigger != domain.TriggerStatusCard {
		t.Fatalf("trigger = %s, quer status_card", run.Trigger)
	}
	if _, err := e.svc.MarkRunStarted(ctx, e.co.ID, run.ID, "tr", ""); err != nil {
		t.Fatal(err)
	}
	return run, service.CardRunRef{PersonaID: e.sum.ID, TaskID: *c.GeneratingTaskID, RunID: &run.ID}
}

func (e *cardEnv) finish(t *testing.T, run domain.Run, status domain.RunStatus, tokens int64) {
	t.Helper()
	if _, err := e.svc.FinishRun(context.Background(), e.co.ID, run.ID, service.FinishRunInput{Status: status,
		Result: adapter.Result{Summary: "feito", Usage: domain.Usage{InputTokens: tokens, OutputTokens: tokens / 2}}}); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}
}

// compile faz a montagem completa: a resumidora grava um filtro (projeto Site, status blocked).
func (e *cardEnv) compile(t *testing.T, cardID domain.ID) service.StatusCardView {
	t.Helper()
	run, ref := e.startGen(t, cardID)
	v, err := e.svc.SetStatusCardQuery(context.Background(), domain.PersonaActor(e.sum.ID), e.co.ID, ref, cardID, service.SetCardQueryInput{
		Title: "Bloqueios do Site", ChangeSummary: "um filtro",
		Queries: []service.CardQueryInput{{Label: "bloqueadas", Statuses: []string{"blocked"}, Project: "site"}}})
	if err != nil {
		t.Fatalf("SetStatusCardQuery: %v", err)
	}
	e.finish(t, run, domain.RunDone, 1000)
	return v
}

// summarize grava um resumo na geração atual.
func (e *cardEnv) summarize(t *testing.T, cardID domain.ID, md string, mentioned ...string) service.StatusCardView {
	t.Helper()
	run, ref := e.startGen(t, cardID)
	v, _, err := e.svc.SetStatusCardSummary(context.Background(), domain.PersonaActor(e.sum.ID), e.co.ID, ref, cardID,
		service.SetCardSummaryInput{Markdown: md, ChangeSummary: "resumo", Mentioned: mentioned})
	if err != nil {
		t.Fatalf("SetStatusCardSummary: %v", err)
	}
	e.finish(t, run, domain.RunDone, 2000)
	return v
}

func (e *cardEnv) blocked(t *testing.T, title string) service.TaskView {
	t.Helper()
	return e.task(t, service.CreateTaskInput{Title: title, Status: domain.TaskBlocked, ProjectID: &e.site.ID, PersonaID: &e.eng.ID})
}

func (e *cardEnv) setStatus(t *testing.T, id domain.ID, st domain.TaskStatus) {
	t.Helper()
	if _, err := e.svc.UpdateTask(context.Background(), domain.HumanActor, e.co.ID, id, service.UpdateTaskInput{Status: &st}); err != nil {
		t.Fatal(err)
	}
}

func (e *cardEnv) tick(t *testing.T) {
	t.Helper()
	if _, err := e.svc.EvaluateDueStatusCards(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
}

func (e *cardEnv) updates(t *testing.T, id domain.ID) []service.StatusCardUpdateView {
	t.Helper()
	us, err := e.svc.ListStatusCardUpdates(context.Background(), e.co.ID, id, 0)
	if err != nil {
		t.Fatal(err)
	}
	return us
}

func TestStatusCardCompileQueryAndFullSummary(t *testing.T) {
	e := newCardEnv(t)
	ctx := context.Background()
	t1 := e.blocked(t, "Deploy travado")
	e.task(t, service.CreateTaskInput{Title: "outra coisa", ProjectID: &e.site.ID})
	e.jobs.reset()

	c := e.create(t, nil)
	if c.State != domain.CardCompiling || c.ViewState != domain.CardViewCompiling || c.GeneratingTaskID == nil {
		t.Fatalf("criado: %+v", c.StatusCard)
	}
	// a task de montagem é da resumidora, prioridade baixa, e acordou com o gatilho do card
	tk, err := e.svc.GetTask(ctx, e.co.ID, c.GeneratingTaskID.String())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tk.Title, "Montar card de status: ") || tk.Priority != domain.PriorityLow || tk.PersonaID == nil || *tk.PersonaID != e.sum.ID ||
		!strings.Contains(tk.Description, "tudo que está bloqueado") || !strings.Contains(tk.Description, "hanko_status_card_set_query") {
		t.Fatalf("task de montagem: %+v", tk.Task)
	}
	if len(e.jobs.wakes) != 1 || e.jobs.wakes[0].Trigger != domain.TriggerStatusCard {
		t.Fatalf("wakes: %+v", e.jobs.wakes)
	}
	// acordar à mão é recusado
	if err := e.svc.WakeTask(ctx, domain.HumanActor, e.co.ID, tk.ID); err == nil || !strings.Contains(err.Error(), "card de status") {
		t.Fatalf("WakeTask: %v", err)
	}

	v := e.compile(t, c.ID)
	if v.QueryVersion != 1 || v.Title != "Bloqueios do Site" || len(v.Queries) != 1 || v.Queries[0].ProjectID == nil || *v.Queries[0].ProjectID != e.site.ID {
		t.Fatalf("depois da montagem: %+v", v.StatusCard)
	}
	// a montagem dispara a primeira atualização, do zero
	if v.GeneratingTaskID == nil || v.ViewState != domain.CardViewUpdating {
		t.Fatalf("sem geração depois da montagem: %+v", v.StatusCard)
	}
	gen, err := e.svc.GetTask(ctx, e.co.ID, v.GeneratingTaskID.String())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(gen.Title, "Atualizar card de status: Bloqueios do Site") || !strings.Contains(gen.Description, t1.Key) ||
		!strings.Contains(gen.Description, "Tipo: do zero") || strings.Contains(gen.Description, "outra coisa") {
		t.Fatalf("task de geração: %s\n%s", gen.Title, gen.Description)
	}
	us := e.updates(t, c.ID)
	if len(us) != 2 || us[0].Kind != domain.CardUpdateFull || us[0].Trigger != domain.CardTriggerQuery || us[0].Status != domain.CardUpdateRunning ||
		us[1].Kind != domain.CardUpdateCompile || us[1].Status != domain.CardUpdateOK || us[1].InputTokens != 1000 || us[1].RunID == nil {
		t.Fatalf("rodadas: %+v", us)
	}
	// a task de montagem fechou quando a run terminou
	if mt, _ := e.svc.GetTask(ctx, e.co.ID, tk.ID.String()); mt.Status != domain.TaskDone {
		t.Fatalf("task de montagem: %s", mt.Status)
	}
	// as tasks de sistema nunca entram no card
	pv, err := e.svc.PreviewStatusCard(ctx, e.co.ID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pv.Tasks) != 1 || pv.Tasks[0].Key != t1.Key || pv.Queries[0].Matches != 1 || !strings.Contains(pv.Queries[0].Description, "Site (site)") {
		t.Fatalf("preview: %+v", pv)
	}

	s := e.summarize(t, c.ID, "# Bloqueios\n\n- "+t1.Key+" segue travado", t1.Key, "XYZ-999")
	if s.SummaryRevision != 1 || s.State != domain.CardActive || s.GeneratingTaskID != nil || s.ViewState != domain.CardViewFresh ||
		s.LastUpdateKind != domain.CardUpdateFull || len(s.MentionedTaskIDs) != 1 || s.MentionedTaskIDs[0] != t1.ID || s.NextEvalAt == nil {
		t.Fatalf("depois do resumo: %+v", s.StatusCard)
	}
	revs, err := e.svc.ListStatusCardRevisions(ctx, e.co.ID, c.ID, 0)
	if err != nil || len(revs) != 1 || revs[0].Revision != 1 || revs[0].Kind != domain.CardUpdateFull {
		t.Fatalf("revisões: %+v %v", revs, err)
	}
	full := e.card(t, c.ID)
	if len(full.Mentioned) != 1 || full.Mentioned[0].Key != t1.Key {
		t.Fatalf("citadas: %+v", full.Mentioned)
	}
}

func TestStatusCardIncrementalVersusFull(t *testing.T) {
	e := newCardEnv(t)
	t1 := e.blocked(t, "um")
	c := e.create(t, nil)
	e.compile(t, c.ID)
	e.summarize(t, c.ID, "v1")

	// uma mudança: depois do debounce, incremental
	e.setStatus(t, t1.ID, domain.TaskInProgress) // saiu do conjunto (só blocked)
	e.advance(2 * time.Minute)
	e.tick(t)
	got := e.card(t, c.ID)
	if got.PendingChangeCount != 1 || got.ViewState != domain.CardViewStale || got.GeneratingTaskID != nil || got.LastChangeAt == nil {
		t.Fatalf("primeira avaliação (debounce): %+v", got.StatusCard)
	}
	e.advance(6 * time.Minute) // passa do debounce de 5 min
	e.tick(t)
	got = e.card(t, c.ID)
	if got.GeneratingTaskID == nil {
		t.Fatalf("sem geração depois do debounce: %+v", got.StatusCard)
	}
	us := e.updates(t, c.ID)
	if us[0].Kind != domain.CardUpdateIncremental || us[0].Trigger != domain.CardTriggerReactive || len(us[0].Changes) != 1 ||
		us[0].Changes[0].ChangeKind != domain.ChangeLeft || us[0].Changes[0].Key != t1.Key {
		t.Fatalf("rodada incremental: %+v", us[0])
	}
	gen, _ := e.svc.GetTask(context.Background(), e.co.ID, got.GeneratingTaskID.String())
	if !strings.Contains(gen.Description, "Resumo anterior (revisão 1)") || !strings.Contains(gen.Description, "saiu do conjunto") {
		t.Fatalf("pacote incremental:\n%s", gen.Description)
	}
	e.summarize(t, c.ID, "v2")
	if got = e.card(t, c.ID); got.LastUpdateKind != domain.CardUpdateIncremental || got.PendingChangeCount != 0 || got.SummaryRevision != 2 {
		t.Fatalf("depois do incremental: %+v", got.StatusCard)
	}

	// mais de 10 mudanças: full
	for i := 0; i < 11; i++ {
		e.blocked(t, fmt.Sprintf("novo %d", i))
	}
	e.advance(10 * time.Minute)
	e.tick(t) // registra as mudanças
	e.advance(10 * time.Minute)
	e.tick(t)
	us = e.updates(t, c.ID)
	if us[0].Kind != domain.CardUpdateFull || len(us[0].Changes) != 11 {
		t.Fatalf("muitas mudanças deveriam pedir full: %+v", us[0])
	}
	e.summarize(t, c.ID, "v3")

	// pedido explícito com full=false e uma mudança: incremental; com full=true: full
	e.blocked(t, "mais um")
	v, err := e.svc.RefreshStatusCard(context.Background(), domain.HumanActor, e.co.ID, c.ID, false)
	if err != nil || v.GeneratingTaskID == nil {
		t.Fatalf("refresh: %+v %v", v.StatusCard, err)
	}
	if us = e.updates(t, c.ID); us[0].Kind != domain.CardUpdateIncremental || us[0].Trigger != domain.CardTriggerManual {
		t.Fatalf("refresh incremental: %+v", us[0])
	}
	if _, err := e.svc.RefreshStatusCard(context.Background(), domain.HumanActor, e.co.ID, c.ID, true); err == nil {
		t.Fatal("refresh durante a geração deveria conflitar")
	}
	e.summarize(t, c.ID, "v4")
	if _, err := e.svc.RefreshStatusCard(context.Background(), domain.HumanActor, e.co.ID, c.ID, true); err != nil {
		t.Fatal(err)
	}
	if us = e.updates(t, c.ID); us[0].Kind != domain.CardUpdateFull {
		t.Fatalf("refresh full: %+v", us[0])
	}
}

func TestStatusCardPolicyLimits(t *testing.T) {
	e := newCardEnv(t)
	ctx := context.Background()
	e.blocked(t, "um")
	c := e.create(t, func(in *service.CreateStatusCardInput) {
		in.Policy = &domain.CardPolicy{Mode: domain.CardModeReactive, DebounceSeconds: 30, MaxUpdatesPerHour: 2,
			Triggers: domain.CardTriggers{Membership: true, Status: true}, DailyTokenCap: 50000}
	})
	e.compile(t, c.ID)
	e.summarize(t, c.ID, "v1") // 1ª geração da hora

	// 2ª geração da hora
	e.blocked(t, "dois")
	e.advance(time.Minute)
	e.tick(t)
	e.advance(time.Minute)
	e.tick(t)
	e.summarize(t, c.ID, "v2")

	// 3ª: o teto por hora segura
	e.blocked(t, "três")
	e.advance(time.Minute)
	e.tick(t)
	e.advance(time.Minute)
	e.tick(t)
	got := e.card(t, c.ID)
	if got.GeneratingTaskID != nil || got.PendingChangeCount != 1 || got.NextEvalAt == nil || !got.NextEvalAt.After(e.now.Add(30*time.Minute)) {
		t.Fatalf("teto por hora: %+v", got.StatusCard)
	}
	// passada a hora, gera
	e.advance(time.Hour)
	e.tick(t)
	if got = e.card(t, c.ID); got.GeneratingTaskID == nil {
		t.Fatalf("depois da hora: %+v", got.StatusCard)
	}

	// teto de tokens: a soma do dia passa de 50000 → paused_budget, e o "Atualizar agora" é recusado
	run, ref := e.startGen(t, c.ID)
	if _, _, err := e.svc.SetStatusCardSummary(ctx, domain.PersonaActor(e.sum.ID), e.co.ID, ref, c.ID, service.SetCardSummaryInput{Markdown: "v3"}); err != nil {
		t.Fatal(err)
	}
	e.finish(t, run, domain.RunDone, 60000)
	e.blocked(t, "quatro")
	e.advance(time.Minute)
	e.tick(t)
	got = e.card(t, c.ID)
	if got.State != domain.CardPausedBudget || got.ViewState != domain.CardViewPausedBudget || got.GeneratingTaskID != nil {
		t.Fatalf("teto de tokens: %+v", got.StatusCard)
	}
	if _, err := e.svc.RefreshStatusCard(ctx, domain.HumanActor, e.co.ID, c.ID, false); err == nil || !strings.Contains(err.Error(), "teto diário") {
		t.Fatalf("refresh com teto estourado: %v", err)
	}
	// no dia seguinte volta
	e.now = time.Date(2026, 10, 17, 0, 1, 0, 0, time.UTC)
	e.tick(t)
	if got = e.card(t, c.ID); got.State != domain.CardActive || got.GeneratingTaskID == nil {
		t.Fatalf("dia seguinte: %+v", got.StatusCard)
	}
}

func TestStatusCardActiveHoursAndManualMode(t *testing.T) {
	e := newCardEnv(t) // 12:00 UTC
	ctx := context.Background()
	e.blocked(t, "um")
	c := e.create(t, func(in *service.CreateStatusCardInput) {
		in.Policy = &domain.CardPolicy{Mode: domain.CardModeReactive, DebounceSeconds: 30,
			Triggers: domain.CardTriggers{Membership: true}, ActiveHours: &domain.ActiveHours{Start: "13:00", End: "18:00", TZ: "UTC"}}
	})
	e.compile(t, c.ID) // pedido explícito: ignora o horário
	e.summarize(t, c.ID, "v1")
	e.blocked(t, "dois")
	e.advance(time.Minute)
	e.tick(t)
	got := e.card(t, c.ID)
	if got.State != domain.CardPausedHours || got.NextEvalAt == nil || got.NextEvalAt.UTC().Hour() != 13 {
		t.Fatalf("fora do horário: %+v", got.StatusCard)
	}
	e.now = time.Date(2026, 10, 16, 13, 0, 0, 0, time.UTC)
	e.tick(t)
	if got = e.card(t, c.ID); got.State != domain.CardActive || got.GeneratingTaskID == nil {
		t.Fatalf("dentro do horário: %+v", got.StatusCard)
	}
	e.summarize(t, c.ID, "v2")

	// modo manual: só conta pendências
	manual := domain.CardPolicy{Mode: domain.CardModeManual, Triggers: domain.CardTriggers{Membership: true}}
	if _, err := e.svc.UpdateStatusCard(ctx, domain.HumanActor, e.co.ID, c.ID, service.UpdateStatusCardInput{Policy: &manual}); err != nil {
		t.Fatal(err)
	}
	e.blocked(t, "três")
	e.advance(time.Minute)
	e.tick(t)
	e.advance(20 * time.Minute)
	e.tick(t)
	if got = e.card(t, c.ID); got.GeneratingTaskID != nil || got.PendingChangeCount != 1 || got.ViewState != domain.CardViewStale {
		t.Fatalf("modo manual: %+v", got.StatusCard)
	}
}

func TestStatusCardRunEndsWithoutSummary(t *testing.T) {
	e := newCardEnv(t)
	ctx := context.Background()
	c := e.create(t, nil)
	run, _ := e.startGen(t, c.ID)
	compileTask := *e.card(t, c.ID).GeneratingTaskID
	e.finish(t, run, domain.RunDone, 500) // terminou sem chamar set_query
	got := e.card(t, c.ID)
	if got.State != domain.CardError || got.ViewState != domain.CardViewError || got.GeneratingTaskID != nil ||
		!strings.Contains(got.FailureReason, "A montagem não terminou") {
		t.Fatalf("montagem sem filtros: %+v", got.StatusCard)
	}
	us := e.updates(t, c.ID)
	if us[0].Status != domain.CardUpdateFailed || us[0].InputTokens != 500 {
		t.Fatalf("rodada: %+v", us[0])
	}
	if tk, _ := e.svc.GetTask(ctx, e.co.ID, compileTask.String()); tk.Status != domain.TaskCancelled {
		t.Fatalf("task: %s", tk.Status)
	}
	// o tick não mexe num card em erro; atualizar sem filtros é recusado; montar de novo funciona
	e.advance(time.Hour)
	e.tick(t)
	if _, err := e.svc.RefreshStatusCard(ctx, domain.HumanActor, e.co.ID, c.ID, false); err == nil {
		t.Fatal("refresh sem filtros")
	}
	v, err := e.svc.RecompileStatusCard(ctx, domain.HumanActor, e.co.ID, c.ID)
	if err != nil || v.State != domain.CardCompiling || v.GeneratingTaskID == nil {
		t.Fatalf("recompile: %+v %v", v.StatusCard, err)
	}
	e.compile(t, c.ID)

	// atualização que falha: erro, e o último resumo bom continua
	e.summarize(t, c.ID, "resumo bom")
	if _, err := e.svc.RefreshStatusCard(ctx, domain.HumanActor, e.co.ID, c.ID, false); err != nil {
		t.Fatal(err)
	}
	run, _ = e.startGen(t, c.ID)
	e.finish(t, run, domain.RunFailed, 10)
	got = e.card(t, c.ID)
	if got.State != domain.CardError || got.Summary != "resumo bom" || !strings.Contains(got.FailureReason, "A atualização não terminou") {
		t.Fatalf("atualização falha: %+v", got.StatusCard)
	}
	// o pedido explícito tira do erro
	if v, err := e.svc.RefreshStatusCard(ctx, domain.HumanActor, e.co.ID, c.ID, false); err != nil || v.State != domain.CardActive || v.GeneratingTaskID == nil {
		t.Fatalf("refresh depois do erro: %+v %v", v.StatusCard, err)
	}
}

func TestStatusCardGenerationTimeout(t *testing.T) {
	e := newCardEnv(t)
	c := e.create(t, nil) // a run nunca começa (persona sem orçamento, por exemplo)
	e.advance(2*time.Hour + time.Minute)
	e.tick(t)
	got := e.card(t, c.ID)
	if got.State != domain.CardError || got.GeneratingTaskID != nil || !strings.Contains(got.FailureReason, "2 h") {
		t.Fatalf("timeout: %+v", got.StatusCard)
	}
	us := e.updates(t, c.ID)
	if us[0].Status != domain.CardUpdateFailed {
		t.Fatalf("rodada: %+v", us[0])
	}
}

func TestStatusCardWritesOnlyBySummarizerOnGenerationTask(t *testing.T) {
	e := newCardEnv(t)
	ctx := context.Background()
	c := e.create(t, nil)
	_, ref := e.startGen(t, c.ID)
	q := service.SetCardQueryInput{Queries: []service.CardQueryInput{{Statuses: []string{"blocked"}}}}

	// outra persona, mesmo na task certa
	other := ref
	other.PersonaID = e.eng.ID
	_, err := e.svc.SetStatusCardQuery(ctx, domain.PersonaActor(e.eng.ID), e.co.ID, other, c.ID, q)
	wantErr(t, err, domain.ErrNotAllowed)
	// a resumidora, noutra task
	wrong := ref
	wrong.TaskID = domain.NewID()
	_, err = e.svc.SetStatusCardQuery(ctx, domain.PersonaActor(e.sum.ID), e.co.ID, wrong, c.ID, q)
	wantErr(t, err, domain.ErrNotAllowed)
	// o resumo não vale na montagem
	_, _, err = e.svc.SetStatusCardSummary(ctx, domain.PersonaActor(e.sum.ID), e.co.ID, ref, c.ID, service.SetCardSummaryInput{Markdown: "x"})
	wantErr(t, err, domain.ErrInvalid)
	// filtro inválido
	_, err = e.svc.SetStatusCardQuery(ctx, domain.PersonaActor(e.sum.ID), e.co.ID, ref, c.ID,
		service.SetCardQueryInput{Queries: []service.CardQueryInput{{Statuses: []string{"zzz"}}}})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.SetStatusCardQuery(ctx, domain.PersonaActor(e.sum.ID), e.co.ID, ref, c.ID,
		service.SetCardQueryInput{Queries: []service.CardQueryInput{{Project: "nao-existe"}}})
	wantErr(t, err, domain.ErrInvalid)
	// a certa
	if _, err := e.svc.SetStatusCardQuery(ctx, domain.PersonaActor(e.sum.ID), e.co.ID, ref, c.ID, q); err != nil {
		t.Fatal(err)
	}
	// a montagem já terminou: repetir é recusado
	_, err = e.svc.SetStatusCardQuery(ctx, domain.PersonaActor(e.sum.ID), e.co.ID, ref, c.ID, q)
	wantErr(t, err, domain.ErrNotAllowed)
}

func TestStatusCardPersonaOwnershipAndRoles(t *testing.T) {
	e := newCardEnv(t)
	ctx := context.Background()
	// a persona cria (a resumidora padrão é ela mesma) e só mexe nos próprios
	pv, err := e.svc.CreateStatusCard(ctx, domain.PersonaActor(e.eng.ID), e.co.ID, service.CreateStatusCardInput{Interest: "minhas tasks"})
	if err != nil {
		t.Fatal(err)
	}
	if pv.SummarizerPersonaID == nil || *pv.SummarizerPersonaID != e.eng.ID || pv.CreatedBy.Type != domain.ActorPersona {
		t.Fatalf("card da persona: %+v", pv.StatusCard)
	}
	hv := e.create(t, nil)
	title := "outro"
	_, err = e.svc.UpdateStatusCard(ctx, domain.PersonaActor(e.eng.ID), e.co.ID, hv.ID, service.UpdateStatusCardInput{Title: &title})
	wantErr(t, err, domain.ErrNotAllowed)
	if _, err := e.svc.UpdateStatusCard(ctx, domain.PersonaActor(e.eng.ID), e.co.ID, pv.ID, service.UpdateStatusCardInput{Title: &title}); err != nil {
		t.Fatal(err)
	}
	// interesse de persona: até 4000 caracteres
	_, err = e.svc.CreateStatusCard(ctx, domain.PersonaActor(e.eng.ID), e.co.ID, service.CreateStatusCardInput{Interest: strings.Repeat("a", 4001)})
	wantErr(t, err, domain.ErrInvalid)
	// lista do MCP: os cards da persona
	mine, err := e.svc.ListPersonaStatusCards(ctx, e.co.ID, e.eng.ID, nil)
	if err != nil || len(mine) != 1 || mine[0].ID != pv.ID {
		t.Fatalf("cards da persona: %+v %v", mine, err)
	}
	// a resumidora vê o card da task de geração dela
	sv, err := e.svc.ListPersonaStatusCards(ctx, e.co.ID, e.sum.ID, hv.GeneratingTaskID)
	if err != nil || len(sv) != 1 || sv[0].ID != hv.ID {
		t.Fatalf("card da geração: %+v %v", sv, err)
	}

	// papéis: viewer lê, não cria; member cria e arquiva o próprio, não o alheio; admin arquiva qualquer um
	users := map[domain.MemberRole]domain.User{}
	for _, r := range []domain.MemberRole{domain.MemberViewer, domain.MemberMember, domain.MemberAdmin} {
		u := mkUser(t, e.env, string(r)+"@x.com", domain.RoleMember)
		join(t, e.env, u, r)
		users[r] = u
	}
	if _, err := e.svc.ListStatusCards(as(users[domain.MemberViewer]), e.co.ID, nil); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.CreateStatusCard(as(users[domain.MemberViewer]), users[domain.MemberViewer].Actor(), e.co.ID, service.CreateStatusCardInput{Interest: "x", SummarizerPersonaID: &e.sum.ID})
	wantErr(t, err, domain.ErrForbidden)
	mc, err := e.svc.CreateStatusCard(as(users[domain.MemberMember]), users[domain.MemberMember].Actor(), e.co.ID, service.CreateStatusCardInput{Interest: "x", SummarizerPersonaID: &e.sum.ID})
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	_, err = e.svc.UpdateStatusCard(as(users[domain.MemberMember]), users[domain.MemberMember].Actor(), e.co.ID, hv.ID, service.UpdateStatusCardInput{Archived: &yes})
	wantErr(t, err, domain.ErrForbidden)
	if _, err := e.svc.UpdateStatusCard(as(users[domain.MemberMember]), users[domain.MemberMember].Actor(), e.co.ID, mc.ID, service.UpdateStatusCardInput{Archived: &yes}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.UpdateStatusCard(as(users[domain.MemberAdmin]), users[domain.MemberAdmin].Actor(), e.co.ID, hv.ID, service.UpdateStatusCardInput{Archived: &yes}); err != nil {
		t.Fatal(err)
	}
	err = e.svc.DeleteStatusCard(as(users[domain.MemberMember]), users[domain.MemberMember].Actor(), e.co.ID, mc.ID)
	wantErr(t, err, domain.ErrForbidden)
	if err := e.svc.DeleteStatusCard(as(users[domain.MemberAdmin]), users[domain.MemberAdmin].Actor(), e.co.ID, mc.ID); err != nil {
		t.Fatal(err)
	}
	// não membro: 404
	stranger := mkUser(t, e.env, "fora@x.com", domain.RoleMember)
	_, err = e.svc.GetStatusCard(as(stranger), e.co.ID, hv.ID)
	wantErr(t, err, domain.ErrNotFound)
}

func TestStatusCardPersonaLimit(t *testing.T) {
	e := newCardEnv(t)
	ctx := context.Background()
	for i := 0; i < domain.MaxCardsPerPersona; i++ {
		if _, err := e.svc.CreateStatusCard(ctx, domain.PersonaActor(e.eng.ID), e.co.ID, service.CreateStatusCardInput{Interest: fmt.Sprint("c", i)}); err != nil {
			t.Fatal(err)
		}
	}
	_, err := e.svc.CreateStatusCard(ctx, domain.PersonaActor(e.eng.ID), e.co.ID, service.CreateStatusCardInput{Interest: "mais um"})
	wantErr(t, err, domain.ErrConflict)
	// humanos seguem podendo
	e.create(t, nil)
}

func TestStatusCardArchiveRestore(t *testing.T) {
	e := newCardEnv(t)
	ctx := context.Background()
	e.blocked(t, "um")
	c := e.create(t, nil)
	e.compile(t, c.ID)
	gen := *e.card(t, c.ID).GeneratingTaskID
	yes, no := true, false
	// arquivar durante a geração encerra a geração
	v, err := e.svc.UpdateStatusCard(ctx, domain.HumanActor, e.co.ID, c.ID, service.UpdateStatusCardInput{Archived: &yes})
	if err != nil || v.ViewState != domain.CardViewArchived || v.GeneratingTaskID != nil || v.NextEvalAt != nil {
		t.Fatalf("arquivado: %+v %v", v.StatusCard, err)
	}
	if tk, _ := e.svc.GetTask(ctx, e.co.ID, gen.String()); tk.Status != domain.TaskCancelled {
		t.Fatalf("task de geração: %s", tk.Status)
	}
	if us := e.updates(t, c.ID); us[0].Status != domain.CardUpdateFailed {
		t.Fatalf("rodada: %+v", us[0])
	}
	arch := true
	if l, _ := e.svc.ListStatusCards(ctx, e.co.ID, &arch); len(l) != 1 {
		t.Fatalf("arquivados: %d", len(l))
	}
	title := "x"
	_, err = e.svc.UpdateStatusCard(ctx, domain.HumanActor, e.co.ID, c.ID, service.UpdateStatusCardInput{Title: &title})
	wantErr(t, err, domain.ErrInvalid)
	// restaurar pede um resumo do zero (gatilho restore)
	v, err = e.svc.UpdateStatusCard(ctx, domain.HumanActor, e.co.ID, c.ID, service.UpdateStatusCardInput{Archived: &no})
	if err != nil || v.ArchivedAt != nil || v.GeneratingTaskID == nil {
		t.Fatalf("restaurado: %+v %v", v.StatusCard, err)
	}
	if us := e.updates(t, c.ID); us[0].Trigger != domain.CardTriggerRestore || us[0].Kind != domain.CardUpdateFull {
		t.Fatalf("rodada de restauração: %+v", us[0])
	}
	// mudar o interesse remonta; o mesmo interesse não
	same := "tudo que está bloqueado no projeto Site"
	before := len(e.updates(t, c.ID))
	if _, err := e.svc.UpdateStatusCard(ctx, domain.HumanActor, e.co.ID, c.ID, service.UpdateStatusCardInput{Interest: &same}); err != nil {
		t.Fatal(err)
	}
	if n := len(e.updates(t, c.ID)); n != before {
		t.Fatalf("mesmo interesse remontou: %d → %d", before, n)
	}
	other := "os bugs críticos"
	v, err = e.svc.UpdateStatusCard(ctx, domain.HumanActor, e.co.ID, c.ID, service.UpdateStatusCardInput{Interest: &other})
	if err != nil || v.State != domain.CardCompiling {
		t.Fatalf("interesse novo: %+v %v", v.StatusCard, err)
	}
}

func TestStatusCardWithoutSummarizer(t *testing.T) {
	e := newCardEnv(t)
	ctx := context.Background()
	v, err := e.svc.CreateStatusCard(ctx, domain.HumanActor, e.co.ID, service.CreateStatusCardInput{Interest: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if v.State != domain.CardError || v.GeneratingTaskID != nil || !strings.Contains(v.FailureReason, "resumidora") {
		t.Fatalf("sem resumidora: %+v", v.StatusCard)
	}
	v, err = e.svc.UpdateStatusCard(ctx, domain.HumanActor, e.co.ID, v.ID, service.UpdateStatusCardInput{SummarizerPersonaID: &e.sum.ID})
	if err != nil || v.State != domain.CardCompiling || v.GeneratingTaskID == nil {
		t.Fatalf("com resumidora: %+v %v", v.StatusCard, err)
	}
	bad := domain.CardPolicy{Mode: "sempre"}
	_, err = e.svc.UpdateStatusCard(ctx, domain.HumanActor, e.co.ID, v.ID, service.UpdateStatusCardInput{Policy: &bad})
	wantErr(t, err, domain.ErrInvalid)
}
