package service_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Cards de status do M20 (ADR-0048): motor decision model, cron e resultado em artefato versionado.

type decCardEnv struct {
	*secEnv
	router *fakeRouter
	now    time.Time
	eng    domain.Persona
	site   domain.Project
}

func newDecCardEnv(t *testing.T) *decCardEnv {
	t.Helper()
	e, f := decisionEnv(t)
	e.configureDecision(t, f, "vendor/small")
	d := &decCardEnv{secEnv: e, router: f, now: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}
	d.svc.Now = func() time.Time { return d.now }
	d.eng = d.persona(t, "Eng", nil)
	pr, err := d.svc.CreateProject(context.Background(), domain.HumanActor, d.co.ID, service.CreateProjectInput{Name: "Site", Slug: "site"})
	if err != nil {
		t.Fatal(err)
	}
	d.site = pr
	f.reply = func(string) string {
		return `{"title":"Bloqueios do Site","summary":"## Bloqueios\n\n- Nada bloqueado hoje.","mentioned":[]}`
	}
	return d
}

func (d *decCardEnv) card(t *testing.T, id domain.ID) service.StatusCardView {
	t.Helper()
	v, err := d.svc.GetStatusCard(context.Background(), d.co.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (d *decCardEnv) create(t *testing.T, mod func(*service.CreateStatusCardInput)) service.StatusCardView {
	t.Helper()
	in := service.CreateStatusCardInput{Interest: "o que está bloqueado no Site?", Engine: domain.CardEngineDecision}
	if mod != nil {
		mod(&in)
	}
	v, err := d.svc.CreateStatusCard(context.Background(), domain.HumanActor, d.co.ID, in)
	if err != nil {
		t.Fatalf("CreateStatusCard: %v", err)
	}
	return v
}

func (d *decCardEnv) artifact(t *testing.T, name string) (service.ArtifactView, string) {
	t.Helper()
	ctx := context.Background()
	as, err := d.svc.ListArtifacts(ctx, d.co.ID, service.ArtifactFilter{Query: name})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range as {
		if a.Name == name {
			_, body, err := d.svc.ArtifactContent(ctx, d.co.ID, a.ID)
			if err != nil {
				t.Fatal(err)
			}
			return a, string(body)
		}
	}
	t.Fatalf("artefato %q não encontrado em %+v", name, as)
	return service.ArtifactView{}, ""
}

func TestCardPolicyCronValidation(t *testing.T) {
	d := newDecCardEnv(t)
	ctx := context.Background()
	for _, p := range []domain.CardPolicy{
		{Mode: domain.CardModeCron},                     // sem expressão
		{Mode: domain.CardModeCron, Cron: "99 7 * * *"}, // inválida
		{Mode: domain.CardModeCron, Cron: "0 7 * *"},    // 4 campos
		{Mode: domain.CardModeCron, Cron: "0 7 * * *", CronTZ: "Marte/Olimpo"},
	} {
		p := p
		_, err := d.svc.CreateStatusCard(ctx, domain.HumanActor, d.co.ID, service.CreateStatusCardInput{
			Interest: "x", Engine: domain.CardEngineDecision, Policy: &p})
		wantErr(t, err, domain.ErrInvalid)
	}
	v := d.create(t, func(in *service.CreateStatusCardInput) {
		in.Policy = &domain.CardPolicy{Mode: domain.CardModeCron, Cron: "0 7 * * 1-5", CronTZ: "America/Sao_Paulo"}
	})
	if v.Policy.Mode != domain.CardModeCron || v.Policy.Cron != "0 7 * * 1-5" {
		t.Fatalf("política: %+v", v.Policy)
	}
	// persona não escolhe o motor decision
	_, err := d.svc.CreateStatusCard(ctx, domain.PersonaActor(d.eng.ID), d.co.ID, service.CreateStatusCardInput{Interest: "x", Engine: domain.CardEngineDecision})
	wantErr(t, err, domain.ErrInvalid)
}

func TestDecisionCardRefreshPublishesVersionedArtifact(t *testing.T) {
	d := newDecCardEnv(t)
	ctx := context.Background()
	bloq := d.task(t, service.CreateTaskInput{Title: "Home quebrada", ProjectID: &d.site.ID, PersonaID: &d.eng.ID, Status: domain.TaskBlocked})

	v := d.create(t, func(in *service.CreateStatusCardInput) { in.OnDashboard = true })
	if v.Engine != domain.CardEngineDecision || v.SummarizerPersonaID != nil || v.QueryVersion != 1 || v.State != domain.CardActive {
		t.Fatalf("card novo: %+v", v.StatusCard)
	}
	if len(v.Queries) != 1 || !v.Queries[0].OpenOnly {
		t.Fatalf("filtro padrão = tasks abertas: %+v", v.Queries)
	}

	// 1ª atualização: o resumo vem do decision model, com os tokens no histórico, e vira a versão 1 do artefato
	d.router.reply = func(string) string {
		return `{"title":"Bloqueios do Site","summary":"## Bloqueios\n\n- ` + bloq.Key + ` está bloqueada.","mentioned":["` + bloq.Key + `"]}`
	}
	got, err := d.svc.RefreshStatusCard(ctx, domain.HumanActor, d.co.ID, v.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.SummaryRevision != 1 || !strings.Contains(got.Summary, bloq.Key) || got.Title != "Bloqueios do Site" || got.ViewState != domain.CardViewFresh {
		t.Fatalf("depois de atualizar: rev=%d título=%q estado=%s resumo=%q", got.SummaryRevision, got.Title, got.ViewState, got.Summary)
	}
	if len(got.Mentioned) != 1 || got.Mentioned[0].Key != bloq.Key {
		t.Fatalf("citadas: %+v", got.Mentioned)
	}
	ups, err := d.svc.ListStatusCardUpdates(ctx, d.co.ID, v.ID, 10)
	if err != nil || len(ups) != 1 || ups[0].Status != domain.CardUpdateOK || ups[0].InputTokens != 120 || ups[0].Model != "vendor/small" {
		t.Fatalf("rodadas: %v %+v", err, ups)
	}
	art, body := d.artifact(t, "card-bloqueios-do-site.md")
	if art.Version != 1 || !strings.HasPrefix(body, "# Bloqueios do Site\n") || !strings.Contains(body, bloq.Key) {
		t.Fatalf("artefato v%d: %q", art.Version, body)
	}
	if got.ArtifactID == nil || *got.ArtifactID != art.ID || got.ArtifactName != "card-bloqueios-do-site.md" {
		t.Fatalf("o card devia apontar para a versão do artefato: %+v", got.StatusCard)
	}
	// o custo do decision model entra no livro do decision model, com ator system
	inv, err := d.svc.ListDecisionInvocations(ctx, d.co.ID, 10)
	if err != nil || len(inv) != 1 || inv[0].Feature != domain.FeatureCardSummary || inv[0].Actor != domain.ActorSystem {
		t.Fatalf("invocações: %v %+v", err, inv)
	}

	// 2ª atualização com outro resumo: versão 2 do mesmo artefato; o mesmo conteúdo de novo não cria versão
	d.router.reply = func(string) string {
		return `{"title":"Bloqueios do Site","summary":"## Bloqueios\n\n- Tudo destravado.","mentioned":[]}`
	}
	d.now = d.now.Add(time.Hour)
	if _, err := d.svc.RefreshStatusCard(ctx, domain.HumanActor, d.co.ID, v.ID, false); err != nil {
		t.Fatal(err)
	}
	if art, _ = d.artifact(t, "card-bloqueios-do-site.md"); art.Version != 2 {
		t.Fatalf("esperava a versão 2, veio %d", art.Version)
	}
	d.now = d.now.Add(time.Hour)
	if _, err := d.svc.RefreshStatusCard(ctx, domain.HumanActor, d.co.ID, v.ID, false); err != nil {
		t.Fatal(err)
	}
	if art, _ = d.artifact(t, "card-bloqueios-do-site.md"); art.Version != 2 {
		t.Fatalf("o mesmo resumo não cria versão: v%d", art.Version)
	}
	revs, _ := d.svc.ListStatusCardRevisions(ctx, d.co.ID, v.ID, 10)
	if len(revs) != 3 {
		t.Fatalf("revisões = %d", len(revs))
	}

	// no Painel
	dash, err := d.svc.DashboardStatusCards(ctx, d.co.ID)
	if err != nil || len(dash) != 1 || dash[0].ArtifactID == nil || !strings.Contains(dash[0].Summary, "destravado") {
		t.Fatalf("painel: %v %+v", err, dash)
	}
	off := false
	if _, err := d.svc.UpdateStatusCard(ctx, domain.HumanActor, d.co.ID, v.ID, service.UpdateStatusCardInput{OnDashboard: &off}); err != nil {
		t.Fatal(err)
	}
	if dash, _ = d.svc.DashboardStatusCards(ctx, d.co.ID); len(dash) != 0 {
		t.Fatalf("card fora do painel apareceu: %+v", dash)
	}
}

func TestDecisionCardFailureKeepsLastGoodSummary(t *testing.T) {
	d := newDecCardEnv(t)
	ctx := context.Background()
	v := d.create(t, nil)
	if _, err := d.svc.RefreshStatusCard(ctx, domain.HumanActor, d.co.ID, v.ID, false); err != nil {
		t.Fatal(err)
	}
	d.now = d.now.Add(time.Hour)
	d.router.mu.Lock()
	d.router.failChat = 1
	d.router.mu.Unlock()
	if _, err := d.svc.RefreshStatusCard(ctx, domain.HumanActor, d.co.ID, v.ID, false); err == nil {
		t.Fatal("a falha do modelo devia voltar ao pedido manual")
	}
	got := d.card(t, v.ID)
	if got.State != domain.CardError || got.ViewState != domain.CardViewError || got.FailureReason == "" || got.SummaryRevision != 1 || !strings.Contains(got.Summary, "Nada bloqueado") {
		t.Fatalf("depois da falha: %+v", got.StatusCard)
	}
	ups, _ := d.svc.ListStatusCardUpdates(ctx, d.co.ID, v.ID, 10)
	if len(ups) != 2 || ups[0].Status != domain.CardUpdateFailed || ups[0].Error == "" {
		t.Fatalf("rodadas: %+v", ups)
	}
	// o card em erro não se atualiza sozinho (o tick o ignora) e sai do erro num pedido explícito
	if _, err := d.svc.EvaluateDueStatusCards(ctx); err != nil {
		t.Fatal(err)
	}
	if got = d.card(t, v.ID); got.State != domain.CardError {
		t.Fatalf("o tick tirou o card do erro: %s", got.State)
	}
	d.now = d.now.Add(time.Hour)
	if got, err := d.svc.RefreshStatusCard(ctx, domain.HumanActor, d.co.ID, v.ID, false); err != nil || got.State != domain.CardActive || got.SummaryRevision != 2 {
		t.Fatalf("recuperação: %v %+v", err, got.StatusCard)
	}
}

// O card de status atualiza de manhã: o tick só dispara no horário do cron e uma vez por horário.
func TestDecisionCardCronFiresInTheMorning(t *testing.T) {
	d := newDecCardEnv(t)
	ctx := context.Background()
	// 2026-10-20 12:00 UTC = 09:00 em São Paulo (UTC-3): o cron das 07:00 já passou hoje
	v := d.create(t, func(in *service.CreateStatusCardInput) {
		in.Policy = &domain.CardPolicy{Mode: domain.CardModeCron, Cron: "0 7 * * *", CronTZ: "America/Sao_Paulo"}
	})
	n, err := d.svc.EvaluateDueStatusCards(ctx)
	if err != nil || n != 1 {
		t.Fatalf("primeiro tick: n=%d err=%v", n, err)
	}
	got := d.card(t, v.ID)
	if got.SummaryRevision != 1 {
		t.Fatalf("o primeiro resumo sai no primeiro tick: %+v", got.StatusCard)
	}
	ups, _ := d.svc.ListStatusCardUpdates(ctx, d.co.ID, v.ID, 10)
	if ups[0].Trigger != domain.CardTriggerCron && ups[0].Trigger != domain.CardTriggerManual {
		t.Fatalf("gatilho = %s", ups[0].Trigger)
	}
	// a próxima verificação é às 07:00 de amanhã (10:00 UTC)
	if got.NextEvalAt == nil || !got.NextEvalAt.Equal(time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("próxima avaliação = %v", got.NextEvalAt)
	}
	// antes do horário, nada
	d.now = time.Date(2026, 10, 21, 9, 59, 0, 0, time.UTC)
	if n, _ := d.svc.EvaluateDueStatusCards(ctx); n != 0 {
		t.Fatalf("antes das 07:00 não avalia: n=%d", n)
	}
	// no horário: gera de novo, mesmo sem nenhuma task mudar
	d.router.reply = func(string) string {
		return `{"title":"Bloqueios do Site","summary":"## Bom dia\n\n- Nada bloqueado.","mentioned":[]}`
	}
	d.now = time.Date(2026, 10, 21, 10, 0, 30, 0, time.UTC)
	if n, err := d.svc.EvaluateDueStatusCards(ctx); err != nil || n != 1 {
		t.Fatalf("tick das 07:00: n=%d err=%v", n, err)
	}
	got = d.card(t, v.ID)
	if got.SummaryRevision != 2 || !strings.Contains(got.Summary, "Bom dia") {
		t.Fatalf("o card devia atualizar de manhã: rev=%d %q", got.SummaryRevision, got.Summary)
	}
	if got.NextEvalAt == nil || !got.NextEvalAt.Equal(time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("próxima avaliação = %v", got.NextEvalAt)
	}
	ups, _ = d.svc.ListStatusCardUpdates(ctx, d.co.ID, v.ID, 10)
	if ups[0].Trigger != domain.CardTriggerCron {
		t.Fatalf("gatilho da atualização das 07:00 = %s", ups[0].Trigger)
	}
	// com "só se mudou", o horário sem mudança não gera
	skip := true
	pol := got.Policy
	pol.CronSkipUnchanged = skip
	if _, err := d.svc.UpdateStatusCard(ctx, domain.HumanActor, d.co.ID, v.ID, service.UpdateStatusCardInput{Policy: &pol}); err != nil {
		t.Fatal(err)
	}
	d.now = time.Date(2026, 10, 22, 10, 0, 30, 0, time.UTC)
	if _, err := d.svc.EvaluateDueStatusCards(ctx); err != nil {
		t.Fatal(err)
	}
	if got = d.card(t, v.ID); got.SummaryRevision != 2 {
		t.Fatalf("sem mudança o cron com skip não atualiza: rev=%d", got.SummaryRevision)
	}
}

func TestDecisionCardEditsMarkTheSummaryStale(t *testing.T) {
	d := newDecCardEnv(t)
	ctx := context.Background()
	v := d.create(t, nil)
	if _, err := d.svc.RefreshStatusCard(ctx, domain.HumanActor, d.co.ID, v.ID, false); err != nil {
		t.Fatal(err)
	}
	// recompor não existe para este motor; trocar de resumidora também não
	_, err := d.svc.RecompileStatusCard(ctx, domain.HumanActor, d.co.ID, v.ID)
	wantErr(t, err, domain.ErrInvalid)
	_, err = d.svc.UpdateStatusCard(ctx, domain.HumanActor, d.co.ID, v.ID, service.UpdateStatusCardInput{SummarizerPersonaID: &d.eng.ID})
	wantErr(t, err, domain.ErrInvalid)

	// novo interesse e novos filtros: o tick refaz o resumo na hora
	interest := "só o que é crítico"
	qs := []service.CardQueryInput{{Label: "críticas", Priorities: []string{"critical"}, OpenOnly: true}}
	if _, err := d.svc.UpdateStatusCard(ctx, domain.HumanActor, d.co.ID, v.ID, service.UpdateStatusCardInput{Interest: &interest, Queries: &qs}); err != nil {
		t.Fatal(err)
	}
	d.now = d.now.Add(2 * time.Minute)
	d.router.reply = func(string) string {
		return `{"title":"","summary":"Só o crítico.","mentioned":[]}`
	}
	if n, err := d.svc.EvaluateDueStatusCards(ctx); err != nil || n != 1 {
		t.Fatalf("tick: n=%d err=%v", n, err)
	}
	got := d.card(t, v.ID)
	if got.SummaryRevision != 2 || got.Summary != "Só o crítico." {
		t.Fatalf("depois da edição: %+v", got.StatusCard)
	}
	// persona não escolhe filtros de um card persona
	pc, err := d.svc.CreateStatusCard(ctx, domain.HumanActor, d.co.ID, service.CreateStatusCardInput{Interest: "x", SummarizerPersonaID: &d.eng.ID})
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.svc.UpdateStatusCard(ctx, domain.HumanActor, d.co.ID, pc.ID, service.UpdateStatusCardInput{Queries: &qs})
	wantErr(t, err, domain.ErrInvalid)
}

func TestPersonaCardSummaryAlsoPublishesArtifact(t *testing.T) {
	ctx := context.Background()
	e := newCardEnv(t)
	v := e.create(t, nil)
	e.compile(t, v.ID)
	run, ref := e.startGen(t, v.ID)
	_ = run
	if _, _, err := e.svc.SetStatusCardSummary(ctx, domain.PersonaActor(e.sum.ID), e.co.ID, ref, v.ID, service.SetCardSummaryInput{
		Markdown: "## Resumo\n\n- tudo certo", Title: "Site em dia"}); err != nil {
		t.Fatal(err)
	}
	as, err := e.svc.ListArtifacts(ctx, e.co.ID, service.ArtifactFilter{Query: "card-"})
	if err != nil || len(as) != 1 || as[0].Name != "card-site-em-dia.md" || as[0].Version != 1 {
		t.Fatalf("artefatos: %v %+v", err, as)
	}
	if c := e.card(t, v.ID); c.ArtifactID == nil || *c.ArtifactID != as[0].ID {
		t.Fatalf("card sem o artefato: %+v", c.StatusCard)
	}
}
