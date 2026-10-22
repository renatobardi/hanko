//go:build integration

package ui_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Etapas do projeto e cards de status do M20 na UI, contra o service e o banco reais (ADR-0048).

func stagesForm() url.Values {
	return url.Values{
		"key_1": {"triagem"}, "name_1": {"Triagem"}, "status_1": {"todo"}, "persona_1": {"tri"}, "gate_1": {"none"},
		"key_2": {"dev"}, "name_2": {"Desenvolvimento"}, "status_2": {"in_progress"}, "persona_2": {"dev"}, "gate_2": {"approval"},
		"instructions_2": {"Faça mudanças pequenas."},
		"key_3":          {""}, "name_3": {"linha em branco"}, // ignorada: sem chave
	}
}

func TestRealProjectStagesTabAndTaskPanel(t *testing.T) {
	u := newRealUI(t)
	ctx := context.Background()
	tri := m15Persona(t, u, "Tri", func(in *service.HirePersonaInput) { in.Slug = "tri" })
	dev := m15Persona(t, u, "Dev", func(in *service.HirePersonaInput) { in.Slug = "dev" })
	pr, err := u.svc.CreateProject(ctx, domain.HumanActor, u.co.ID, service.CreateProjectInput{Name: "Site", Slug: "site"})
	if err != nil {
		t.Fatal(err)
	}

	// a aba existe e oferece o formulário (linhas em branco para acrescentar)
	mustContain(t, u.get("/c/acme/projetos/site?secao=etapas"), 200, "Etapas do projeto", "Salvar etapas", `name="key_1"`, `name="key_3"`,
		"Gate ao concluir", "Aprendizados das etapas", "Nenhum aprendizado ainda")

	// uma persona com status in_review é recusada com o motivo, sem gravar nada
	bad := stagesForm()
	bad.Set("status_1", "in_review")
	rec := u.form("/c/acme/projetos/site/etapas", bad)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "in_review") {
		t.Fatalf("etapa inválida: %d %.300s", rec.Code, rec.Body.String())
	}

	rec = u.form("/c/acme/projetos/site/etapas", stagesForm())
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "secao=etapas&ok=etapas-salvas") {
		t.Fatalf("salvar etapas: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	mustContain(t, u.get("/c/acme/projetos/site?secao=etapas&ok=etapas-salvas"), 200, "Etapas do projeto salvas.", `value="triagem"`, `value="Desenvolvimento"`,
		"Faça mudanças pequenas.")
	v, err := u.svc.ProjectStages(ctx, u.co.ID, pr.Slug)
	if err != nil || len(v.Stages) != 2 || v.Stages[1].ExitGate != domain.StageGateApproval || v.Stages[0].PersonaSlug != "tri" {
		t.Fatalf("etapas gravadas: %v %+v", err, v)
	}

	// a task nova do projeto cai na primeira etapa e a lateral mostra o painel
	tk, err := u.svc.CreateTask(ctx, domain.HumanActor, u.co.ID, service.CreateTaskInput{Title: "Home nova", ProjectID: &pr.ID})
	if err != nil {
		t.Fatal(err)
	}
	page := "/c/acme/tasks/" + tk.Key
	mustContain(t, u.get(page), 200, "Etapa do projeto", "Triagem", "1/2", "Concluir etapa", "Mover à mão", "Tirar do pipeline", page+"/etapa/concluir")
	if got, _ := u.svc.GetTask(ctx, u.co.ID, tk.Key); got.PersonaID == nil || *got.PersonaID != tri.ID {
		t.Fatalf("a task devia nascer com a persona da triagem: %+v", got.PersonaID)
	}

	// concluir a etapa: vai para o dev
	rec = u.form(page+"/etapa/concluir", url.Values{"note": {"triada"}})
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "ok=etapa-concluida") {
		t.Fatalf("concluir: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	mustContain(t, u.get(page+"?ok=etapa-concluida"), 200, "Etapa concluída.", "Desenvolvimento", "2/2", "aprovação humana")
	if got, _ := u.svc.GetTask(ctx, u.co.ID, tk.Key); got.PersonaID == nil || *got.PersonaID != dev.ID || got.Status != domain.TaskInProgress {
		t.Fatalf("depois da triagem: persona=%v status=%s", got.PersonaID, got.Status)
	}

	// concluir o dev abre o gate de aprovação: a lateral diz que espera o gate
	u.form(page+"/etapa/concluir", url.Values{"note": {"pronto"}})
	mustContain(t, u.get(page), 200, "esperando o gate")
	// mover à mão volta para a triagem
	rec = u.form(page+"/etapa/mover", url.Values{"to": {"triagem"}, "note": {"voltou"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("mover: %d", rec.Code)
	}
	mustContain(t, u.get(page), 200, "Triagem", "1/2")
	// tirar do pipeline
	rec = u.form(page+"/etapa/sair", url.Values{})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("sair: %d", rec.Code)
	}
	mustContain(t, u.get(page), 200, "Entrar no pipeline")

	// apagar todas as chaves desliga o pipeline
	rec = u.form("/c/acme/projetos/site/etapas", url.Values{"key_1": {""}})
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "ok=etapas-limpas") {
		t.Fatalf("limpar: %d %s", rec.Code, rec.Header().Get("Location"))
	}
}

func TestRealStatusCardFormDecisionCronAndDashboard(t *testing.T) {
	u := newRealUI(t)
	ctx := context.Background()
	mustContain(t, u.get("/c/acme/cards-de-status/novo"), 200, "Quem escreve o resumo", `value="decision"`, "O decision model (uma chamada, barato)",
		"Num horário (cron)", `name="cron"`, "Mostrar este card no Painel", `name="q_project"`)

	// cron inválido: 422 com o motivo
	rec := u.form("/c/acme/cards-de-status/novo", url.Values{"interest": {"o que está bloqueado?"}, "engine": {"decision"}, "mode": {"cron"}, "cron": {"99 7 * * *"},
		"tr_status": {"1"}})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "expressão cron") {
		t.Fatalf("cron inválido: %d %.300s", rec.Code, rec.Body.String())
	}
	rec = u.form("/c/acme/cards-de-status/novo", url.Values{"interest": {"o que está bloqueado?"}, "title": {"Bloqueios"}, "engine": {"decision"}, "mode": {"cron"},
		"cron": {"0 7 * * 1-5"}, "cron_tz": {"America/Sao_Paulo"}, "on_dashboard": {"1"}, "q_open": {"1"}, "tr_status": {"1"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("criar card decision: %d %.400s", rec.Code, rec.Body.String())
	}
	cards, err := u.svc.ListStatusCards(ctx, u.co.ID, nil)
	if err != nil || len(cards) != 1 {
		t.Fatalf("cards: %v %d", err, len(cards))
	}
	c := cards[0]
	if c.Engine != domain.CardEngineDecision || !c.OnDashboard || c.Policy.Mode != domain.CardModeCron || c.Policy.Cron != "0 7 * * 1-5" || c.SummarizerPersonaID != nil {
		t.Fatalf("card gravado: %+v", c.StatusCard)
	}
	detail := "/c/acme/cards-de-status/" + c.ID.String()
	mustContain(t, u.get(detail), 200, "Bloqueios")
	mustContain(t, u.get(detail+"?aba=configuracao"), 200, "Decision model: o resumo sai de uma chamada só", `value="0 7 * * 1-5"`, `name="q_status"`, "Só tasks abertas")
	// o motor não muda: o formulário de edição não oferece a escolha
	if strings.Contains(u.get(detail+"?aba=configuracao").Body.String(), `name="engine"`) {
		t.Fatal("o motor é imutável depois de criado")
	}
	rec = u.form(detail, url.Values{"interest": {"só o crítico"}, "title": {"Bloqueios"}, "mode": {"cron"}, "cron": {"0 8 * * *"}, "cron_tz": {"UTC"}, "tr_status": {"1"},
		"q_status": {"blocked"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("editar: %d %.300s", rec.Code, rec.Body.String())
	}
	got, _ := u.svc.GetStatusCard(ctx, u.co.ID, c.ID)
	if got.Interest != "só o crítico" || got.Policy.Cron != "0 8 * * *" || got.OnDashboard || len(got.Queries) != 1 || got.Queries[0].Statuses[0] != domain.TaskBlocked {
		t.Fatalf("depois de editar: %+v", got.StatusCard)
	}
}

// O Painel mostra o card marcado, com o resumo e o link do artefato versionado.
func TestRealDashboardShowsStatusCardWithArtifact(t *testing.T) {
	u := newRealUI(t)
	ctx := context.Background()
	sum := m15Persona(t, u, "Resumidora", nil)
	pr, err := u.svc.CreateProject(ctx, domain.HumanActor, u.co.ID, service.CreateProjectInput{Name: "Site", Slug: "site"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := u.svc.CreateTask(ctx, domain.HumanActor, u.co.ID, service.CreateTaskInput{Title: "Home quebrada", ProjectID: &pr.ID, PersonaID: &sum.ID, Status: domain.TaskBlocked}); err != nil {
		t.Fatal(err)
	}
	card, err := u.svc.CreateStatusCard(ctx, domain.HumanActor, u.co.ID, service.CreateStatusCardInput{Interest: "bloqueios do Site", SummarizerPersonaID: &sum.ID, OnDashboard: true})
	if err != nil {
		t.Fatal(err)
	}
	gen := func() (domain.Run, service.CardRunRef) {
		c, _ := u.svc.GetStatusCard(ctx, u.co.ID, card.ID)
		run, err := u.svc.CheckoutRun(ctx, service.Wake{CompanyID: u.co.ID, PersonaID: sum.ID, TaskID: *c.GeneratingTaskID, Trigger: domain.TriggerHeartbeat})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := u.svc.MarkRunStarted(ctx, u.co.ID, run.ID, "tr", ""); err != nil {
			t.Fatal(err)
		}
		return run, service.CardRunRef{PersonaID: sum.ID, TaskID: *c.GeneratingTaskID, RunID: &run.ID}
	}
	_, ref := gen()
	if _, err := u.svc.SetStatusCardQuery(ctx, domain.PersonaActor(sum.ID), u.co.ID, ref, card.ID, service.SetCardQueryInput{Title: "Bloqueios do Site",
		Queries: []service.CardQueryInput{{Statuses: []string{"blocked"}, Project: "site"}}}); err != nil {
		t.Fatal(err)
	}
	// a montagem abre a primeira atualização; a resumidora grava o resumo
	c, _ := u.svc.GetStatusCard(ctx, u.co.ID, card.ID)
	if c.GeneratingTaskID == nil {
		t.Fatalf("a montagem devia abrir a atualização: %+v", c.StatusCard)
	}
	// fecha a run da montagem antes de abrir a da atualização
	runs, _ := u.svc.ListRuns(ctx, u.co.ID, service.RunFilter{Limit: 5})
	for _, r := range runs {
		if r.Status.Active() {
			_, _ = u.svc.FinishRun(ctx, u.co.ID, r.ID, service.FinishRunInput{Status: domain.RunDone})
		}
	}
	_, ref = gen()
	if _, _, err := u.svc.SetStatusCardSummary(ctx, domain.PersonaActor(sum.ID), u.co.ID, ref, card.ID, service.SetCardSummaryInput{Markdown: "## Bloqueios\n\n- Home quebrada está **bloqueada**."}); err != nil {
		t.Fatal(err)
	}
	dash := u.get("/c/acme/")
	mustContain(t, dash, 200, "Cards de status", "Bloqueios do Site", "Home quebrada está", "card-bloqueios-do-site.md", "versão 1")
	// o link do artefato abre a página dele
	art, err := u.svc.ListArtifacts(ctx, u.co.ID, service.ArtifactFilter{Query: "card-"})
	if err != nil || len(art) != 1 {
		t.Fatalf("artefato: %v %d", err, len(art))
	}
	mustContain(t, u.get("/c/acme/artefatos/"+art[0].ID.String()), 200, "card-bloqueios-do-site.md")
	// desmarcar tira do Painel
	off := false
	if _, err := u.svc.UpdateStatusCard(ctx, domain.HumanActor, u.co.ID, card.ID, service.UpdateStatusCardInput{OnDashboard: &off}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(u.get("/c/acme/").Body.String(), "Home quebrada está") {
		t.Fatal("o card desmarcado não devia estar no Painel")
	}
}
