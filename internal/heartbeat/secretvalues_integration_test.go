//go:build integration

package heartbeat

import (
	"context"
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

// Fatia 3 do cofre (ADR-0068 §4.4): o valor que a tool hanko_secret_get entrega à run sai do log dela. O adapter chama a
// tool no meio da execução e ecoa o valor num evento e no resumo; nada do que a run grava pode carregá-lo.

const toolRedactSentinel = "SENTINELA-valor-entregue-pela-tool-7a6b5c"

// toolUseAgent chama hanko_secret_get durante a run e ecoa o valor.
type toolUseAgent struct {
	*fake.Adapter
	svc *service.Service
	co  domain.ID
}

func (a *toolUseAgent) Run(ctx context.Context, spec adapter.Spec, sink adapter.Sink) (adapter.Result, error) {
	res, err := a.svc.SecretGet(ctx, a.co, service.SecretGetInput{RunID: spec.RunID, Item: "gh-login"})
	if err != nil || res.Decision != service.NativeAllow {
		return adapter.Result{}, fmt.Errorf("tool sem valor: %+v %v", res, err)
	}
	if err := sink.Emit(ctx, adapter.Event{Kind: domain.EventToolResult, Text: "li o login: " + res.Value, OccurredAt: time.Now().UTC()}); err != nil {
		return adapter.Result{}, err
	}
	return adapter.Result{Summary: "usei o login " + res.Value}, nil
}

func TestValorDaToolNaoSaiDoLogDaRun(t *testing.T) {
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
	ag := &toolUseAgent{Adapter: fake.New(fake.Options{Delay: -1}), svc: svc}
	hb.Attach(svc, adapter.Registry{"fake": ag}, memory.Noop{}, broker, authForTest())
	co, err := svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Acme", Slug: "acme", TaskPrefix: "ACM"})
	if err != nil {
		t.Fatal(err)
	}
	ag.co = co.ID
	if err := hb.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = hb.Stop(sctx)
	})

	if _, err := svc.CreateSecret(ctx, domain.HumanActor, co.ID, service.CreateSecretInput{Name: "gh-login", Value: toolRedactSentinel}); err != nil {
		t.Fatal(err)
	}
	p, err := svc.HirePersona(ctx, domain.HumanActor, co.ID, service.HirePersonaInput{Name: "Ana", Agent: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetToolPolicy(ctx, domain.HumanActor, co.ID, service.ToolPolicyInput{Scope: domain.PolicyScopePersona, PersonaID: &p.ID,
		Target: domain.PolicyTargetNative, ToolName: service.HankoSecretGetTool, Mode: domain.PolicyAllow}); err != nil {
		t.Fatal(err)
	}

	run := runTask(t, svc, co, p)
	if run.Status != domain.RunDone {
		t.Fatalf("status=%s error=%q", run.Status, run.Error)
	}
	if strings.Contains(run.Summary, toolRedactSentinel) || !strings.Contains(run.Summary, "usei o login") {
		t.Fatalf("o resumo deveria existir sem o valor: %q", run.Summary)
	}
	evs, err := svc.ListRunEvents(ctx, co.ID, run.ID, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, e := range evs {
		if strings.Contains(e.Text, "li o login") {
			seen = true
		}
		if b := fmt.Sprintf("%+v", e); strings.Contains(b, toolRedactSentinel) {
			t.Fatalf("evento do log com o valor da tool: %s", b)
		}
	}
	if !seen {
		t.Fatal("o evento da tool não chegou ao log")
	}
	// a run acabou: o registro de valores da tool foi esquecido
	if left := svc.RunSecretValues(run.ID); len(left) != 0 {
		t.Fatalf("valores da tool depois da run: %d", len(left))
	}
}
