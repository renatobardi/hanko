//go:build integration

package heartbeat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/adapter/fake"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/testdb"
)

const (
	hbSentinel  = "SENTINELA-do-despacho-1a2b3c4d5e"
	hbSentinel2 = "SENTINELA-do-env-9z8y7x6w5v"
)

func newSecretStack(t *testing.T) (*service.Service, *spyAdapter, domain.Company) {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	broker := events.NewBroker()
	hb, err := New(pool, Options{Workers: 2, PeriodicInterval: time.Hour, RunTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	svc := service.New(pool, hb, broker)
	box, err := secretbox.New("0123456789abcdef0123456789abcdef-hb", "")
	if err != nil {
		t.Fatal(err)
	}
	svc.Integrations = service.IntegrationsConfig{Secrets: box}
	spy := &spyAdapter{Adapter: fake.New(fake.Options{Delay: -1})}
	hb.Attach(svc, adapter.Registry{"fake": spy}, memory.Noop{}, broker, authForTest())
	co, err := svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Acme", Slug: "acme", TaskPrefix: "ACM"})
	if err != nil {
		t.Fatal(err)
	}
	if err := hb.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = hb.Stop(sctx)
	})
	return svc, spy, co
}

func runTask(t *testing.T, svc *service.Service, co domain.Company, p domain.Persona) service.RunView {
	t.Helper()
	ctx := context.Background()
	task, err := svc.CreateTask(ctx, domain.HumanActor, co.ID, service.CreateTaskInput{Title: "usar o segredo", PersonaID: &p.ID, Status: domain.TaskTodo})
	if err != nil {
		t.Fatal(err)
	}
	var run service.RunView
	waitFor(t, 30*time.Second, "run terminar", func() bool {
		runs, err := svc.ListRuns(ctx, co.ID, service.RunFilter{TaskID: &task.ID})
		if err != nil || len(runs) == 0 || !runs[0].Status.Terminal() {
			return false
		}
		run = runs[0]
		return true
	})
	return run
}

// Demo do M7: a persona usa um segredo por referência. O adapter recebe o valor, e nada que sai da run o carrega.
func TestPersonaUsesSecretByReference(t *testing.T) {
	ctx := context.Background()
	svc, spy, co := newSecretStack(t)
	if _, err := svc.CreateSecret(ctx, domain.HumanActor, co.ID, service.CreateSecretInput{Name: "gateway-token", Value: hbSentinel}); err != nil {
		t.Fatal(err)
	}
	cfg := json.RawMessage(fmt.Sprintf(`{"summary":"conectei com o token ${secret:gateway-token}","env":{"ANTHROPIC_AUTH_TOKEN":%q}}`, hbSentinel2))
	p, err := svc.HirePersona(ctx, domain.HumanActor, co.ID, service.HirePersonaInput{Name: "Ana", Agent: "fake", AdapterConfig: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(p.AdapterConfig), hbSentinel2) {
		t.Fatalf("o env em claro ficou na persona: %s", p.AdapterConfig)
	}

	run := runTask(t, svc, co, p)
	if run.Status != domain.RunDone {
		t.Fatalf("status=%s error=%q", run.Status, run.Error)
	}

	// o adapter recebeu o valor resolvido (é para isso que serve) e a lista de segredos a redigir
	specs := spy.all()
	if len(specs) != 1 {
		t.Fatalf("specs = %d", len(specs))
	}
	sp := specs[0]
	if !strings.Contains(string(sp.Config), hbSentinel) || !strings.Contains(string(sp.Config), hbSentinel2) {
		t.Fatalf("o adapter deveria receber os valores resolvidos: %s", sp.Config)
	}
	if len(sp.Secrets) != 2 {
		t.Fatalf("Spec.Secrets = %v", sp.Secrets)
	}
	// o briefing e o prompt não carregam o valor
	for _, text := range []string{sp.System, sp.Prompt, string(sp.MCPConfig)} {
		if strings.Contains(text, hbSentinel) || strings.Contains(text, hbSentinel2) {
			t.Fatalf("prompt/briefing/MCP com valor de segredo")
		}
	}

	// nada do que a run produz guarda o valor: resumo, log, comentários, atividade
	if strings.Contains(run.Summary, hbSentinel) || strings.Contains(run.Error, hbSentinel) {
		t.Fatalf("run: summary=%q error=%q", run.Summary, run.Error)
	}
	if !strings.Contains(run.Summary, "conectei com o token") {
		t.Fatalf("o resumo deveria existir (redigido): %q", run.Summary)
	}
	evs, err := svc.ListRunEvents(ctx, co.ID, run.ID, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		b, _ := json.Marshal(e)
		if strings.Contains(string(b), hbSentinel) || strings.Contains(string(b), hbSentinel2) {
			t.Fatalf("evento do log com valor de segredo: %s", b)
		}
	}
	comments, err := svc.ListComments(ctx, co.ID, run.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range comments {
		if strings.Contains(c.Body, hbSentinel) {
			t.Fatalf("comentário com valor de segredo: %q", c.Body)
		}
	}
	rows, err := svc.Pool.Query(ctx, `SELECT data::text FROM activity_events`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var d string
		_ = rows.Scan(&d)
		if strings.Contains(d, hbSentinel) || strings.Contains(d, hbSentinel2) {
			t.Fatalf("atividade com valor de segredo: %s", d)
		}
	}
}

// Segredo apagado: a run falha cedo, com o nome do segredo e nunca o valor, sem chamar o adapter.
func TestRunFailsEarlyWhenReferencedSecretIsGone(t *testing.T) {
	ctx := context.Background()
	svc, spy, co := newSecretStack(t)
	if _, err := svc.CreateSecret(ctx, domain.HumanActor, co.ID, service.CreateSecretInput{Name: "temporario", Value: hbSentinel}); err != nil {
		t.Fatal(err)
	}
	p, err := svc.HirePersona(ctx, domain.HumanActor, co.ID, service.HirePersonaInput{Name: "Ana", Agent: "fake",
		AdapterConfig: json.RawMessage(`{"env":{"TOKEN_X":"${secret:temporario}"}}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteSecret(ctx, domain.HumanActor, co.ID, domain.SecretScopeCompany, "temporario", true); err != nil {
		t.Fatal(err)
	}
	run := runTask(t, svc, co, p)
	if run.Status != domain.RunFailed || !strings.Contains(run.Error, "temporario") || strings.Contains(run.Error, hbSentinel) {
		t.Fatalf("status=%s error=%q", run.Status, run.Error)
	}
	if len(spy.all()) != 0 {
		t.Fatal("o adapter não deveria rodar sem a credencial")
	}
}
