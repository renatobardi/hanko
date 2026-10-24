package heartbeat

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// sfBackend é o que o executor pede para materializar ${secretfile:}; o resto da interface nunca é chamado aqui.
type sfBackend struct {
	backend // nil: só MaterializeSecretFiles é chamado
	called  bool
}

func (b *sfBackend) MaterializeSecretFiles(_ context.Context, _, _, _ domain.ID, cfg json.RawMessage) (json.RawMessage, []string, func(), error) {
	b.called = true
	return cfg, []string{"conteúdo"}, func() {}, nil
}

// ADR-0068 §5.3: ${secretfile:} falha cedo num ambiente remoto, com o nome dele, e nem chega a criar o arquivo.
func TestSecretfileRecusadoEmAmbienteRemoto(t *testing.T) {
	cfg := json.RawMessage(`{"env":{"KUBECONFIG":"${secretfile:prd#kubeconfig}"}}`)
	for _, tc := range []struct {
		driver domain.EnvironmentDriver
		want   string
	}{
		{domain.EnvSSH, "arquivo de segredo ainda não é suportado em ambiente SSH"},
		{domain.EnvSandbox, "arquivo de segredo ainda não é suportado em ambiente sandbox"},
	} {
		b := &sfBackend{}
		h := &Heartbeat{svc: b}
		spec := adapter.Spec{Config: cfg}
		_, _, err := h.materializeSecretFiles(context.Background(), service.RunContext{}, &runEnv{driver: tc.driver}, &spec)
		if err == nil || err.Error() != tc.want {
			t.Fatalf("%s: erro = %v, quero %q", tc.driver, err, tc.want)
		}
		if b.called {
			t.Fatalf("%s: criou o arquivo num ambiente remoto", tc.driver)
		}
	}
}

// Sem ambiente remoto (ou no local), o arquivo é criado e a configuração do spec recebe o caminho.
func TestSecretfileMaterializaNoLocal(t *testing.T) {
	cfg := json.RawMessage(`{"env":{"KUBECONFIG":"${secretfile:prd#kubeconfig}"}}`)
	for _, renv := range []*runEnv{nil, {driver: domain.EnvLocal}} {
		b := &sfBackend{}
		h := &Heartbeat{svc: b}
		spec := adapter.Spec{Config: cfg}
		values, cleanup, err := h.materializeSecretFiles(context.Background(), service.RunContext{}, renv, &spec)
		if err != nil || !b.called || len(values) != 1 || cleanup == nil {
			t.Fatalf("local: err=%v called=%v values=%v", err, b.called, values)
		}
		cleanup()
	}
}
