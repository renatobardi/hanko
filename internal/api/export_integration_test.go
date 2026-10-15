//go:build integration

package api_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// seedRich monta uma empresa com todos os tipos de relação que o arquivo precisa preservar.
func (e *apiEnv) seedRich() domain.Company {
	e.t.Helper()
	ctx := context.Background()
	co := e.company("Rica", "rica")
	must := func(err error) {
		e.t.Helper()
		if err != nil {
			e.t.Fatal(err)
		}
	}
	ws, err := e.svc.CreateWorkspace(ctx, domain.HumanActor, co.ID, service.CreateWorkspaceInput{Name: "Repo", Slug: "repo", Path: "/srv/repo"})
	must(err)
	pr, err := e.svc.CreateProject(ctx, domain.HumanActor, co.ID, service.CreateProjectInput{Name: "Lançamento", Slug: "lancamento", WorkspaceID: &ws.ID})
	must(err)
	limit := 50.0
	sub, err := e.svc.CreateSubscription(ctx, domain.HumanActor, co.ID, service.CreateSubscriptionInput{
		Name: "Plano", Agent: "fake", MonthlyUSD: 100, WeeklyLimitUSD: &limit, ModelPrefixes: []string{"m"}})
	must(err)
	_, err = e.svc.SetCompanyBudget(ctx, domain.HumanActor, co.ID, ptr(int64(123456)))
	must(err)

	boss := e.persona(co, "Chefe", func(in *service.HirePersonaInput) {
		in.Instructions = "mande"
		in.WorkspaceID = &ws.ID
		in.SubscriptionID = &sub.ID
		in.AdapterConfig = json.RawMessage(`{"max_turns":5,"api_key":"sk-segredo-1234567890","env":{"GITHUB_TOKEN":"ghp_segredo1234567890abcd","LOG":"debug"}}`)
	})
	dev := e.persona(co, "Dev", func(in *service.HirePersonaInput) {
		in.ReportsTo = &boss.ID
		in.RequireReview = true
		in.BudgetMonthlyCents = ptr(int64(5000))
	})
	rev := e.persona(co, "Revisora", func(in *service.HirePersonaInput) { in.ReportsTo = &boss.ID })
	alt := e.persona(co, "Alt", func(in *service.HirePersonaInput) {
		in.ReviewerAgent, in.ReviewerModel = "claude", "m2"
	})
	_ = alt
	_, err = e.svc.UpdatePersona(ctx, domain.HumanActor, co.ID, dev.ID, service.UpdatePersonaInput{ReviewerPersona: &rev.ID})
	must(err)

	t1 := e.task(co, "Pai", &dev, func(in *service.CreateTaskInput) { in.ProjectID = &pr.ID; in.Priority = domain.PriorityHigh })
	t2 := e.task(co, "Filha", nil, func(in *service.CreateTaskInput) { in.ParentID = &t1.ID; in.Description = "detalhe" })
	e.task(co, "Solta", &boss, nil)
	_, err = e.svc.AddComment(ctx, domain.HumanActor, co.ID, t2.ID, service.AddCommentInput{Body: "olá"})
	must(err)
	_, err = e.svc.AddComment(ctx, domain.PersonaActor(dev.ID), co.ID, t1.ID, service.AddCommentInput{Body: "feito"})
	must(err)
	_, err = e.svc.CreateFlow(ctx, domain.HumanActor, co.ID, service.CreateFlowInput{Name: "Diário", Cron: "0 9 * * *", PersonaID: boss.ID, ProjectID: &pr.ID, TaskTitle: "rotina {{n}}"})
	must(err)

	// uma execução encerrada, com log e comentário amarrado a ela
	e.setNow(time.Date(2026, 3, 10, 13, 0, 0, 0, time.UTC))
	run, err := e.svc.CheckoutRun(ctx, wakeOf(dev, t1))
	must(err)
	_, err = e.svc.MarkRunStarted(ctx, co.ID, run.ID, "tr", "")
	must(err)
	_, err = e.svc.AppendRunEvent(ctx, co.ID, run.ID, adapter.Event{Kind: domain.EventAssistant, Text: "pensando", OccurredAt: e.now()})
	must(err)
	_, err = e.svc.AddComment(ctx, domain.PersonaActor(dev.ID), co.ID, t1.ID, service.AddCommentInput{Body: "da run", RunID: &run.ID})
	must(err)
	_, err = e.svc.FinishRun(ctx, co.ID, run.ID, service.FinishRunInput{Status: domain.RunDone, Result: usdRes(1.5)})
	must(err)
	return co
}

func (e *apiEnv) now() time.Time { return time.Unix(0, e.clock.Load()).UTC() }

func (e *apiEnv) exportRaw(slug, query string) []byte {
	return e.want(200, "GET", "/api/v1/companies/"+slug+"/export"+query, "", nil)
}

// normalized tira o que legitimamente muda entre duas exportações (hora da exportação e slug).
func normalized(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	delete(m, "exported_at")
	m["company"].(map[string]any)["slug"] = "-"
	return m
}

func TestAPIExportImportIdaEVolta(t *testing.T) {
	e := newAPIEnv(t)
	e.seedRich()

	for _, q := range []string{"", "?with_runs=true", "?with_logs=true"} {
		raw := e.exportRaw("rica", q)
		var created struct {
			Company struct{ Slug string } `json:"company"`
			Counts  map[string]int
		}
		e.want(201, "POST", "/api/v1/companies/import?slug=copia&paused=false", string(raw), &created)
		if created.Company.Slug != "copia" || created.Counts["personas"] != 4 || created.Counts["tasks"] != 3 {
			t.Fatalf("q=%q: %+v", q, created)
		}
		again := e.exportRaw("copia", q)
		if a, b := normalized(t, raw), normalized(t, again); !reflect.DeepEqual(a, b) {
			ja, _ := json.MarshalIndent(a, "", " ")
			jb, _ := json.MarshalIndent(b, "", " ")
			t.Fatalf("q=%q: ida e volta difere\n--- original\n%s\n--- copia\n%s", q, ja, jb)
		}
		// libera o slug para a próxima volta
		if _, err := e.svc.Pool.Exec(context.Background(), `DELETE FROM companies WHERE slug='copia'`); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAPIExportSemSegredosESemRuns(t *testing.T) {
	e := newAPIEnv(t)
	e.seedRich()
	raw := string(e.exportRaw("rica", ""))
	for _, secret := range []string{"sk-segredo", "ghp_segredo", "api_key", "GITHUB_TOKEN"} {
		if strings.Contains(raw, secret) {
			t.Fatalf("o arquivo vazou %q", secret)
		}
	}
	if !strings.Contains(raw, `"max_turns"`) || !strings.Contains(raw, `"LOG"`) {
		t.Fatalf("a configuração não sensível deveria permanecer: %s", raw)
	}
	var d struct {
		Runs []json.RawMessage `json:"runs"`
	}
	_ = json.Unmarshal([]byte(raw), &d)
	if len(d.Runs) != 0 {
		t.Fatal("sem --with-runs não há execuções")
	}
	withRuns := string(e.exportRaw("rica", "?with_runs=true"))
	if !strings.Contains(withRuns, `"runs"`) || strings.Contains(withRuns, "pensando") {
		t.Fatal("with_runs traz execuções, mas não o log")
	}
	if !strings.Contains(string(e.exportRaw("rica", "?with_logs=true")), "pensando") {
		t.Fatal("with_logs deveria trazer o log")
	}
}

func TestAPIImportPreservaRelacoes(t *testing.T) {
	e := newAPIEnv(t)
	src := e.seedRich()
	raw := e.exportRaw("rica", "?with_logs=true")
	e.want(201, "POST", "/api/v1/companies/import?slug=destino", string(raw), nil)
	ctx := context.Background()
	dst, err := e.svc.GetCompany(ctx, "destino")
	if err != nil {
		t.Fatal(err)
	}
	if dst.ID == src.ID {
		t.Fatal("deveria ser uma empresa nova")
	}
	pe := func(slug string) domain.Persona {
		p, err := e.svc.GetPersona(ctx, dst.ID, slug)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	boss, dev, rev, alt := pe("chefe"), pe("dev"), pe("revisora"), pe("alt")
	if dev.ReportsTo == nil || *dev.ReportsTo != boss.ID || rev.ReportsTo == nil || *rev.ReportsTo != boss.ID {
		t.Fatalf("organograma perdido: %+v %+v", dev.ReportsTo, rev.ReportsTo)
	}
	if dev.ReviewerPersonaID == nil || *dev.ReviewerPersonaID != rev.ID || !dev.RequireReview {
		t.Fatalf("revisor perdido: %+v", dev)
	}
	if alt.ReviewerAgent != "claude" || alt.ReviewerModel != "m2" {
		t.Fatalf("harness alternativo perdido: %+v", alt)
	}
	if boss.SubscriptionID == nil || boss.WorkspaceID == nil {
		t.Fatal("assinatura/workspace da persona perdidos")
	}
	sub, err := e.svc.GetSubscription(ctx, dst.ID, "Plano")
	if err != nil || sub.ID != *boss.SubscriptionID || sub.WeeklyLimitUSD == nil || *sub.WeeklyLimitUSD != 50 {
		t.Fatalf("assinatura: %+v %v", sub, err)
	}
	if boss.ID == pe("chefe").ID && boss.ID == mustPersonaOf(t, e, src).ID {
		t.Fatal("ids deveriam ser novos")
	}
	child, err := e.svc.GetTask(ctx, dst.ID, "HNK-2")
	if err != nil || child.ParentID == nil || child.Title != "Filha" {
		t.Fatalf("filha: %+v %v", child, err)
	}
	parent, _ := e.svc.GetTask(ctx, dst.ID, "HNK-1")
	if *child.ParentID != parent.ID || parent.PersonaID == nil || *parent.PersonaID != dev.ID || parent.ProjectID == nil {
		t.Fatalf("pai: %+v", parent)
	}
	// a numeração continua de onde parou
	next := e.task(dst, "nova", nil, nil)
	if next.Number != 4 {
		t.Fatalf("próxima task = %d, esperava 4", next.Number)
	}
	if dst.BudgetMonthlyCents == nil || *dst.BudgetMonthlyCents != 123456 {
		got, _ := e.svc.GetCompany(ctx, "destino")
		if got.BudgetMonthlyCents == nil || *got.BudgetMonthlyCents != 123456 {
			t.Fatalf("orçamento: %v", got.BudgetMonthlyCents)
		}
	}
	acts, err := e.svc.ListActivity(ctx, dst.ID, service.ActivityFilter{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, a := range acts {
		found = found || a.Verb == "company.imported"
	}
	if !found {
		t.Fatal("faltou activity company.imported")
	}
}

func mustPersonaOf(t *testing.T, e *apiEnv, co domain.Company) domain.Persona {
	p, err := e.svc.GetPersona(context.Background(), co.ID, "chefe")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// mutate aplica uma edição ao arquivo exportado e devolve o JSON.
func mutate(t *testing.T, raw []byte, f func(m map[string]any)) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	f(m)
	b, _ := json.Marshal(m)
	return string(b)
}

func TestAPIImportInvalidoNaoGravaNada(t *testing.T) {
	e := newAPIEnv(t)
	e.seedRich()
	raw := e.exportRaw("rica", "?with_logs=true")
	first := func(m map[string]any, k string) map[string]any { return m[k].([]any)[0].(map[string]any) }
	last := func(m map[string]any, k string) map[string]any {
		l := m[k].([]any)
		return l[len(l)-1].(map[string]any)
	}

	cases := []struct {
		name   string
		status int
		edit   func(m map[string]any)
	}{
		{"formato", 422, func(m map[string]any) { m["format"] = "outro.v9" }},
		{"persona com agente desconhecido", 422, func(m map[string]any) { first(m, "personas")["agent"] = "nao-existe" }},
		{"referência quebrada", 422, func(m map[string]any) { first(m, "tasks")["persona"] = "fantasma" }},
		{"pai inexistente", 422, func(m map[string]any) { last(m, "tasks")["parent"] = 999 }},
		{"ciclo no organograma", 422, func(m map[string]any) {
			ps := m["personas"].([]any)
			for _, p := range ps {
				pm := p.(map[string]any)
				if pm["slug"] == "chefe" {
					pm["reports_to"] = "dev"
				}
			}
		}},
		{"slug de persona repetido", 422, func(m map[string]any) {
			ps := m["personas"].([]any)
			ps[1].(map[string]any)["slug"] = ps[0].(map[string]any)["slug"]
		}},
		{"número de task repetido", 422, func(m map[string]any) {
			ts := m["tasks"].([]any)
			ts[1].(map[string]any)["number"] = ts[0].(map[string]any)["number"]
		}},
		{"status de task desconhecido", 422, func(m map[string]any) { last(m, "tasks")["status"] = "quase" }},
		{"cron inválido", 422, func(m map[string]any) { first(m, "flows")["cron"] = "de vez em quando" }},
		{"número de task acima do limite", 422, func(m map[string]any) { last(m, "tasks")["number"] = 2_000_000_000 }},
		{"número de task zero", 422, func(m map[string]any) { last(m, "tasks")["number"] = 0 }},
		{"próximo número absurdo", 422, func(m map[string]any) { m["company"].(map[string]any)["next_task_number"] = int64(9e18) }},
		{"custo negativo", 422, func(m map[string]any) { first(m, "runs")["cost_usd"] = -3.5 }},
		{"custo de tabela negativo", 422, func(m map[string]any) { first(m, "runs")["list_cost_usd"] = -0.01 }},
		{"tokens negativos", 422, func(m map[string]any) { first(m, "runs")["usage"] = map[string]any{"input_tokens": -5} }},
		{"período sem início", 422, func(m map[string]any) {
			first(m, "subscriptions")["periods"].([]any)[0].(map[string]any)["started_at"] = "0001-01-01T00:00:00Z"
		}},
		{"evento de run fora de ordem", 422, func(m map[string]any) {
			r := first(m, "runs")
			r["events"].([]any)[0].(map[string]any)["seq"] = 0
		}},
	}
	for _, c := range cases {
		e.want(c.status, "POST", "/api/v1/companies/import?slug=falha", mutate(t, raw, c.edit), nil)
		var n int
		if err := e.svc.Pool.QueryRow(context.Background(), `SELECT count(*) FROM companies WHERE slug='falha'`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s: empresa parcial gravada (n=%d err=%v)", c.name, n, err)
		}
	}
	// JSON que não é um arquivo: o validador do esquema recusa
	e.want(422, "POST", "/api/v1/companies/import", `{"hello":"world"}`, nil)

	var before, after int
	_ = e.svc.Pool.QueryRow(context.Background(), `SELECT count(*) FROM companies`).Scan(&before)
	// falha no meio da gravação (violação de unicidade só detectada pelo banco): nome de run repetido
	// já é barrado na validação; aqui o slug em uso é a falha do banco e nada mais entra
	e.want(409, "POST", "/api/v1/companies/import?slug=acme", string(raw), nil)
	_ = e.svc.Pool.QueryRow(context.Background(), `SELECT count(*) FROM companies`).Scan(&after)
	if before != after {
		t.Fatalf("slug em uso criou empresa: %d -> %d", before, after)
	}
}

func TestAPIImportSlugDerivadoEPausado(t *testing.T) {
	e := newAPIEnv(t)
	e.seedRich()
	raw := e.exportRaw("rica", "")
	// sem ?slug, o do arquivo já existe: 409
	e.want(409, "POST", "/api/v1/companies/import", string(raw), nil)

	statuses := func(slug string) (map[string]domain.PersonaStatus, bool) {
		co := mustCompany(t, e, slug)
		ps, err := e.svc.ListPersonas(context.Background(), co.ID)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]domain.PersonaStatus{}
		for _, p := range ps {
			out[p.Slug] = p.Status
		}
		fl, err := e.svc.ListFlows(context.Background(), co.ID)
		if err != nil || len(fl) == 0 {
			t.Fatalf("fluxos: %+v %v", fl, err)
		}
		return out, fl[0].Enabled
	}

	// padrão: entra PAUSADA, sem precisar pedir (nada trabalha até alguém liberar)
	e.want(201, "POST", "/api/v1/companies/import?slug=pausada", string(raw), nil)
	ps, flowOn := statuses("pausada")
	for slug, st := range ps {
		if st != domain.PersonaPaused {
			t.Fatalf("%s deveria entrar pausada por padrão: %s", slug, st)
		}
	}
	if flowOn {
		t.Fatal("fluxo deveria entrar desligado por padrão")
	}
	// paused=false liga o que o arquivo traz ligado
	e.want(201, "POST", "/api/v1/companies/import?slug=ativa&paused=false", string(raw), nil)
	ps, flowOn = statuses("ativa")
	if !flowOn {
		t.Fatal("paused=false deveria manter o fluxo ligado")
	}
	for slug, st := range ps {
		if st == domain.PersonaPaused {
			t.Fatalf("%s não deveria estar pausada com paused=false", slug)
		}
	}
	e.want(422, "POST", "/api/v1/companies/import?slug=Inválido!", string(raw), nil)
	e.want(422, "POST", "/api/v1/companies/import?slug=x&paused=talvez", string(raw), nil)
}

func mustCompany(t *testing.T, e *apiEnv, slug string) domain.Company {
	co, err := e.svc.GetCompany(context.Background(), slug)
	if err != nil {
		t.Fatal(err)
	}
	return co
}

func TestAPIExportImportPersonaNaoPode(t *testing.T) {
	e := newAPIEnv(t)
	co := e.seedRich()
	p := mustPersonaOf(t, e, co)
	hdr := e.personaToken(co, p)
	if st, _ := e.call("GET", "/api/v1/companies/rica/export", "", hdr); st != 403 {
		t.Fatalf("export com run token = %d", st)
	}
	raw := e.exportRaw("rica", "")
	if st, _ := e.call("POST", "/api/v1/companies/import?slug=x", string(raw), hdr); st != 403 {
		t.Fatalf("import com run token = %d", st)
	}
}
