//go:build integration

package heartbeat

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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

// Fatia 3 do cofre (ADR-0068 §5.2, #58): o ${secretfile:} do adapter_config vira arquivo 0600 nesta run e some ao fim dela,
// inclusive quando a run falha. O adapter vê o caminho, nunca o conteúdo em config, e o conteúdo fica fora da redação.

const sfSentinel = "SENTINELA-secretfile-kubeconfig-5c4b3a"

// failingSpy grava o spec e falha a run: o arquivo do segredo tem de sair mesmo assim.
type failingSpy struct{ *spyAdapter }

func (f *failingSpy) Run(ctx context.Context, spec adapter.Spec, sink adapter.Sink) (adapter.Result, error) {
	f.mu.Lock()
	f.specs = append(f.specs, spec)
	f.mu.Unlock()
	return adapter.Result{}, errors.New("harness caiu no meio da execução")
}

// newCofreStack monta o serviço, o heartbeat e a empresa "acme" para os testes de cofre numa run. A fábrica recebe o
// serviço e devolve o adapter que a run vai usar; ele só roda depois que o teste cria a persona.
func newCofreStack(t *testing.T, mkAdapter func(*service.Service) adapter.Adapter) (*service.Service, domain.Company) {
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
	hb.Attach(svc, adapter.Registry{"fake": mkAdapter(svc)}, memory.Noop{}, broker, authForTest())
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
	return svc, co
}

// newSecretfileStack monta o cofre com um adapter que vai passar pelo despacho; o adapter é o spy ou o que falha,
// conforme o teste.
func newSecretfileStack(t *testing.T, failing bool) (*service.Service, *spyAdapter, domain.Company) {
	t.Helper()
	spy := &spyAdapter{Adapter: fake.New(fake.Options{Delay: -1})}
	svc, co := newCofreStack(t, func(*service.Service) adapter.Adapter {
		if failing {
			return &failingSpy{spy}
		}
		return spy
	})
	return svc, spy, co
}

// kubeconfigPath tira o caminho do KUBECONFIG do adapter_config que o adapter recebeu.
func kubeconfigPath(t *testing.T, sp adapter.Spec) string {
	t.Helper()
	var doc struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(sp.Config, &doc); err != nil {
		t.Fatalf("adapter_config do spec: %v", err)
	}
	p := doc.Env["KUBECONFIG"]
	if p == "" || strings.Contains(string(sp.Config), "${secretfile:") || strings.Contains(string(sp.Config), sfSentinel) {
		t.Fatalf("o adapter deveria receber o caminho, não a referência nem o conteúdo: %s", sp.Config)
	}
	return p
}

func seedKubeconfig(t *testing.T, svc *service.Service, co domain.Company) {
	t.Helper()
	ctx := context.Background()
	if _, err := svc.CreateSecret(ctx, domain.HumanActor, co.ID, service.CreateSecretInput{Name: "prd-cluster", Value: "principal"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateSecretField(ctx, domain.HumanActor, co.ID, domain.SecretScopeCompany, "prd-cluster",
		service.CreateSecretFieldInput{Name: "kubeconfig", Kind: domain.SecretFieldFile, File: []byte(sfSentinel), FileName: "kubeconfig"}); err != nil {
		t.Fatal(err)
	}
}

// Run que termina: o arquivo existe com 0600 durante a execução e não existe quando a run acaba.
func TestRunSecretfileSomeAoFimDaRun(t *testing.T) {
	ctx := context.Background()
	svc, spy, co := newSecretfileStack(t, false)
	seedKubeconfig(t, svc, co)
	cfg := json.RawMessage(`{"env":{"KUBECONFIG":"${secretfile:prd-cluster#kubeconfig}"}}`)
	p, err := svc.HirePersona(ctx, domain.HumanActor, co.ID, service.HirePersonaInput{Name: "Ana", Agent: "fake", AdapterConfig: cfg})
	if err != nil {
		t.Fatal(err)
	}

	run := runTask(t, svc, co, p)
	if run.Status != domain.RunDone {
		t.Fatalf("status=%s error=%q", run.Status, run.Error)
	}
	specs := spy.all()
	if len(specs) != 1 {
		t.Fatalf("specs = %d", len(specs))
	}
	path := kubeconfigPath(t, specs[0])
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("o arquivo do segredo sobreviveu ao fim da run: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("o diretório do segredo sobreviveu ao fim da run: %v", err)
	}
	if strings.Contains(run.Summary, sfSentinel) || strings.Contains(run.Error, sfSentinel) {
		t.Fatalf("o resumo da run carrega o conteúdo do arquivo: %q", run.Summary)
	}
}

// Critério 3, inclusive quando falha: o harness cai e o arquivo do segredo mesmo assim sai do disco.
func TestRunSecretfileApagadoQuandoARunFalha(t *testing.T) {
	ctx := context.Background()
	svc, spy, co := newSecretfileStack(t, true)
	seedKubeconfig(t, svc, co)
	cfg := json.RawMessage(`{"env":{"KUBECONFIG":"${secretfile:prd-cluster#kubeconfig}"}}`)
	p, err := svc.HirePersona(ctx, domain.HumanActor, co.ID, service.HirePersonaInput{Name: "Ana", Agent: "fake", AdapterConfig: cfg})
	if err != nil {
		t.Fatal(err)
	}

	run := runTask(t, svc, co, p)
	if run.Status == domain.RunDone {
		t.Fatalf("a run deveria falhar, status=%s", run.Status)
	}
	specs := spy.all()
	if len(specs) != 1 {
		t.Fatalf("o adapter deveria ter sido chamado uma vez, specs = %d", len(specs))
	}
	path := kubeconfigPath(t, specs[0])
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("o arquivo do segredo ficou no disco depois da run que falhou: %v", err)
	}
	if strings.Contains(run.Error, sfSentinel) {
		t.Fatalf("o erro da run carrega o conteúdo: %q", run.Error)
	}
}
