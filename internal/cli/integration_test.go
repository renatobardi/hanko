//go:build integration

package cli

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/testdb"
)

type noJobs struct{}

func (noJobs) WakeTx(context.Context, pgx.Tx, service.Wake) error                { return nil }
func (noJobs) ExecuteRunTx(context.Context, pgx.Tx, domain.ID, domain.ID) error  { return nil }
func (noJobs) StartReviewTx(context.Context, pgx.Tx, domain.ID, domain.ID) error { return nil }
func (noJobs) DeliverMemoryTx(context.Context, pgx.Tx, domain.ID) error          { return nil }

// realServer sobe a API de verdade (service + Postgres) e devolve o ambiente da CLI apontando para ela.
func realServer(t *testing.T) (*service.Service, domain.Company, map[string]string) {
	t.Helper()
	pool := testdb.New(t)
	broker := events.NewBroker()
	svc := service.New(pool, noJobs{}, broker)
	svc.Now = func() time.Time { return time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC) }
	mux := http.NewServeMux()
	api.Mount(mux, api.Deps{Svc: svc, Broker: broker, Logger: slog.New(slog.DiscardHandler), Version: "it", DB: pool})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	co, err := svc.CreateCompany(context.Background(), domain.HumanActor, service.CreateCompanyInput{Name: "Acme", Slug: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	return svc, co, map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}
}

func mustOK(t *testing.T, env map[string]string, args ...string) string {
	t.Helper()
	code, out, errOut := exec(t, Runtime{}, env, "", args...)
	if code != 0 {
		t.Fatalf("hanko %s: código %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), code, out, errOut)
	}
	return out
}

func mustFail(t *testing.T, env map[string]string, wantCode int, wantMsg string, args ...string) {
	t.Helper()
	code, out, errOut := exec(t, Runtime{}, env, "", args...)
	if code != wantCode || !strings.Contains(errOut, wantMsg) {
		t.Fatalf("hanko %s: código %d (quer %d), stderr %q (quer %q), stdout %q", strings.Join(args, " "), code, wantCode, errOut, wantMsg, out)
	}
}

// hanko subscription … contra o servidor de verdade: ciclo completo e erros da API.
func TestCLIAssinaturasContraServidorReal(t *testing.T) {
	_, _, env := realServer(t)

	out := mustOK(t, env, "subscription", "create", "Claude Max", "--agent", "fake", "--monthly", "200", "--models", "claude-opus",
		"--weekly-limit", "50", "--guard-pct", "80")
	if !strings.Contains(out, "Assinatura criada: Claude Max — fake, US$ 200.00 por mês, cobre claude-opus") {
		t.Fatalf("create: %q", out)
	}
	mustOK(t, env, "sub", "create", "Reserva", "--agent", "fake", "--monthly", "10", "--inactive")

	out = mustOK(t, env, "sub", "list")
	for _, want := range []string{"Claude Max", "US$ 200.00", "ligada", "US$ 0.00 / US$ 50.00", "Reserva", "desligada"} {
		if !strings.Contains(out, want) {
			t.Errorf("list sem %q:\n%s", want, out)
		}
	}
	out = mustOK(t, env, "sub", "show", "Claude Max")
	if !strings.Contains(out, "claude-opus") || !strings.Contains(out, "trava em 80%") || !strings.Contains(out, "livre") {
		t.Errorf("show:\n%s", out)
	}
	out = mustOK(t, env, "sub", "update", "Claude Max", "--monthly", "250", "--weekly-limit", "60", "--guard-pct", "70")
	if !strings.Contains(out, "US$ 250.00 por mês") || !strings.Contains(out, "trava em 70%") {
		t.Errorf("update: %q", out)
	}
	out = mustOK(t, env, "sub", "update", "Claude Max", "--no-weekly-limit")
	if !strings.Contains(out, "sem trava semanal") {
		t.Errorf("update --no-weekly-limit: %q", out)
	}
	out = mustOK(t, env, "sub", "update", "Claude Max", "--off")
	if !strings.Contains(out, "Assinatura atualizada") {
		t.Errorf("update --off: %q", out)
	}
	if out = mustOK(t, env, "sub", "show", "Claude Max"); !strings.Contains(out, "desligada") {
		t.Errorf("show após --off:\n%s", out)
	}
	if out = mustOK(t, env, "sub", "list", "--json"); !strings.Contains(out, `"name": "Claude Max"`) || !strings.Contains(out, `"active": false`) {
		t.Errorf("list --json:\n%s", out)
	}

	// erros da API chegam como mensagem, com código 1
	mustFail(t, env, 1, "409", "sub", "create", "Claude Max", "--agent", "fake")
	mustFail(t, env, 1, "422", "sub", "create", "Ruim", "--agent", "gemini")
	mustFail(t, env, 1, "422", "sub", "create", "Ruim", "--agent", "fake", "--monthly", "-5")
	mustFail(t, env, 1, "422", "sub", "update", "Claude Max", "--weekly-limit", "0")
	mustFail(t, env, 1, "404", "sub", "show", "Fantasma")
	mustFail(t, env, 1, "404", "sub", "rm", "Fantasma")

	out = mustOK(t, env, "sub", "rm", "Reserva")
	if !strings.Contains(out, "Assinatura Reserva removida.") {
		t.Errorf("rm: %q", out)
	}
	if out = mustOK(t, env, "sub", "list"); strings.Contains(out, "Reserva") {
		t.Errorf("a assinatura removida segue na lista:\n%s", out)
	}
}

// Persona com assinatura e revisor, execução com nome amigável e custos, tudo pela CLI.
func TestCLIPersonaRevisorNomeEExecucao(t *testing.T) {
	ctx := context.Background()
	svc, co, env := realServer(t)

	mustOK(t, env, "sub", "create", "Plano", "--agent", "claude", "--monthly", "31")
	mustOK(t, env, "persona", "hire", "Kenji", "--agent", "codex", "--model", "gpt-5")
	out := mustOK(t, env, "persona", "hire", "Ada", "--agent", "claude", "--model", "claude-sonnet-4-5", "--reviewer", "kenji", "--subscription", "Plano")
	if !strings.Contains(out, "Persona contratada: Ada (ada)") {
		t.Fatalf("hire: %q", out)
	}
	var ada domain.Persona
	check := func(what string, ok func(domain.Persona) bool) {
		t.Helper()
		p, err := svc.GetPersona(ctx, co.ID, "ada")
		if err != nil || !ok(p) {
			t.Fatalf("%s: %+v %v", what, p, err)
		}
		ada = p
	}
	kenji, _ := svc.GetPersona(ctx, co.ID, "kenji")
	check("contratada com revisor e assinatura", func(p domain.Persona) bool {
		return p.ReviewerPersonaID != nil && *p.ReviewerPersonaID == kenji.ID && p.SubscriptionID != nil
	})

	mustOK(t, env, "persona", "update", "ada", "--reviewer-agent", "codex", "--reviewer-model", "gpt-5")
	check("harness alternativo", func(p domain.Persona) bool {
		return p.ReviewerPersonaID == nil && p.ReviewerAgent == "codex" && p.ReviewerModel == "gpt-5"
	})
	mustOK(t, env, "persona", "update", "ada", "--reviewer-model", "gpt-5-mini")
	check("só o modelo", func(p domain.Persona) bool { return p.ReviewerAgent == "codex" && p.ReviewerModel == "gpt-5-mini" })
	mustOK(t, env, "persona", "update", "ada", "--reviewer", "kenji")
	check("persona revisora", func(p domain.Persona) bool {
		return p.ReviewerPersonaID != nil && p.ReviewerAgent == "" && p.ReviewerModel == ""
	})
	if out = mustOK(t, env, "persona", "show", "ada"); !strings.Contains(out, "Kenji") && !strings.Contains(out, kenji.ID.String()) {
		t.Errorf("show não mostra o revisor:\n%s", out)
	}
	mustOK(t, env, "persona", "update", "ada", "--no-reviewer")
	check("sem revisor", func(p domain.Persona) bool { return !p.HasReviewer() })
	mustOK(t, env, "persona", "update", "ada", "--no-subscription")
	check("sem assinatura", func(p domain.Persona) bool { return p.SubscriptionID == nil })
	mustOK(t, env, "persona", "update", "ada", "--subscription", "Plano")
	check("com assinatura de novo", func(p domain.Persona) bool { return p.SubscriptionID != nil })

	mustFail(t, env, 1, "422", "persona", "update", "ada", "--reviewer", "ada")
	mustFail(t, env, 1, "422", "persona", "update", "ada", "--reviewer-agent", "claude", "--reviewer-model", "claude-sonnet-4-5")
	mustFail(t, env, 1, "422", "persona", "update", "ada", "--reviewer-agent", "gemini")
	mustFail(t, env, 1, "404", "persona", "update", "ada", "--reviewer", "fantasma")
	mustFail(t, env, 1, "404", "persona", "update", "ada", "--subscription", "Fantasma")
	check("os pedidos recusados não mudaram nada", func(p domain.Persona) bool { return !p.HasReviewer() && p.SubscriptionID != nil })

	// uma execução da Ada: nome amigável, log pelo nome, custos pela CLI
	tv, err := svc.CreateTask(ctx, domain.HumanActor, co.ID, service.CreateTaskInput{Title: "tarefa", PersonaID: &ada.ID})
	if err != nil {
		t.Fatal(err)
	}
	run, err := svc.CheckoutRun(ctx, service.Wake{CompanyID: co.ID, PersonaID: ada.ID, TaskID: tv.ID, Trigger: domain.TriggerAssign})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.MarkRunStarted(ctx, co.ID, run.ID, "tr", ""); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"lendo o repositório", "rodando os testes"} {
		if _, err := svc.AppendRunEvent(ctx, co.ID, run.ID, adapter.Event{Kind: domain.EventAssistant, Text: text, OccurredAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	cost := 0.5
	if _, err := svc.FinishRun(ctx, co.ID, run.ID, service.FinishRunInput{Status: domain.RunDone,
		Result: adapter.Result{Model: "claude-sonnet-4-5", CostUSD: &cost, Usage: domain.Usage{InputTokens: 1_000_000}}}); err != nil {
		t.Fatal(err)
	}
	name := domain.RunName(run.ID, 0)

	for _, ref := range []string{name, strings.ToLower(name), run.ID.String()} {
		out = mustOK(t, env, "run", "logs", ref)
		if !strings.Contains(out, "lendo o repositório") || !strings.Contains(out, "rodando os testes") {
			t.Errorf("run logs %s:\n%s", ref, out)
		}
	}
	out = mustOK(t, env, "run", "show", strings.ToLower(name))
	for _, want := range []string{name + " (" + run.ID.String() + ")", "HNK-1", "Ada", "US$ 0.5000 (real, informado pelo harness)", "Custo de lista", "US$ 3.0000", "modelo da persona"} {
		if !strings.Contains(out, want) {
			t.Errorf("run show sem %q:\n%s", want, out)
		}
	}
	if out = mustOK(t, env, "run", "list"); !strings.Contains(out, name) {
		t.Errorf("run list não mostra o nome:\n%s", out)
	}
	absent := "Nobre_Farol" // tem a forma de um nome e não existe nesta empresa
	if name == absent {
		absent = "Gentil_Cometa"
	}
	mustFail(t, env, 1, "não encontrada", "run", "logs", absent)
	mustFail(t, env, 1, "422", "run", "logs", "isto-nao-e-id-nem-nome") // sem a forma de nome vai como id e a API recusa

	// custos: as quatro colunas e o rodapé
	svc.Now = func() time.Time { return time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC) }
	out = mustOK(t, env, "cost", "--from", "2026-03-01", "--to", "2026-03-31")
	for _, want := range []string{"REAL", "ESTIMADO", "LISTA", "PAGO", "2026-03-10", "sem uso", "Total: US$ 0.50 (real US$ 0.50, estimado US$ 0.00)",
		"Custo de lista: US$ 3.00", "pago (mensalidades rateadas): US$ 1.00", "sem uso: US$ 30.00"} {
		if !strings.Contains(out, want) {
			t.Errorf("cost sem %q:\n%s", want, out)
		}
	}
}

// hanko export | hanko import contra o servidor de verdade.
func TestCLIExportImport(t *testing.T) {
	svc, co, env := realServer(t)
	if _, err := svc.HirePersona(context.Background(), domain.HumanActor, co.ID, service.HirePersonaInput{
		Name: "Ada", Agent: "fake", Model: "m", AdapterConfig: json.RawMessage(`{"max_turns":3,"api_key":"sk-segredo-1234567890"}`)}); err != nil {
		t.Fatal(err)
	}
	doc := mustOK(t, env, "export")
	if !strings.Contains(doc, `"format": "hanko.company.v1"`) || !strings.Contains(doc, "Ada") || strings.Contains(doc, "sk-segredo") {
		t.Fatalf("export: %s", doc)
	}

	file := filepath.Join(t.TempDir(), "arq.json")
	if out := mustOK(t, env, "export", "-o", file, "--with-runs"); !strings.Contains(out, file) {
		t.Fatalf("export -o: %q", out)
	}
	if st, err := os.Stat(file); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("arquivo: %v %v", st, err)
	}

	out := mustOK(t, env, "import", file, "--slug", "copia")
	if !strings.Contains(out, "copia") || !strings.Contains(out, "personas") {
		t.Fatalf("import: %q", out)
	}
	env2 := map[string]string{"HANKO_URL": env["HANKO_URL"], "HANKO_COMPANY": "copia"}
	if out := mustOK(t, env2, "persona", "list"); !strings.Contains(out, "Ada") {
		t.Fatalf("persona da cópia: %q", out)
	}

	mustFail(t, env, 1, "slug", "import", file, "--slug", "copia") // já existe
	bad := filepath.Join(t.TempDir(), "ruim.json")
	if err := os.WriteFile(bad, []byte(`{"format":"outro","company":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	mustFail(t, env, 1, "422", "import", bad, "--slug", "ruim")
	notJSON := filepath.Join(t.TempDir(), "x.json")
	if err := os.WriteFile(notJSON, []byte("nada"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustFail(t, env, 2, "JSON", "import", notJSON)
}
