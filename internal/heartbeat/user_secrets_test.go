package heartbeat

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// fakeUserSecretsBackend faz o papel do service na expansão do ${secret:user/...}: devolve o texto, os valores e o erro
// que o teste manda, e guarda o texto que recebeu.
type fakeUserSecretsBackend struct {
	backend
	out   string
	vals  []string
	err   error
	calls int
	in    string
}

func (f *fakeUserSecretsBackend) ResolveRunUserSecrets(_ context.Context, _, _ domain.ID, text string) (string, []string, error) {
	f.calls++
	f.in = text
	return f.out, f.vals, f.err
}

// O prompt da run recebe o segredo pessoal que o dono delegou (ADR-0068 §6). Sem referência pessoal, ou sem o cofre,
// o prompt fica como está.
func TestResolveRunUserSecretsTrocaOPrompt(t *testing.T) {
	rc := service.RunContext{Run: domain.Run{ID: domain.NewID()}, Company: domain.Company{ID: domain.NewID()}}
	const ref = "tok=${secret:user/meu-token}"
	const valor = "SENTINELA-token-heartbeat-4d2c1a"

	// sem referência pessoal o backend nem é chamado
	fb := &fakeUserSecretsBackend{}
	h := &Heartbeat{svc: fb}
	spec := adapter.Spec{Prompt: "faça X ${secret:base}"}
	vals, err := h.resolveRunUserSecrets(context.Background(), rc, &spec)
	if err != nil || vals != nil || spec.Prompt != "faça X ${secret:base}" || fb.calls != 0 {
		t.Fatalf("sem referência pessoal: valores %v, erro %v, chamadas %d", vals, err, fb.calls)
	}

	// com referência, o prompt sai com o valor e o valor entra na redação da run
	fb = &fakeUserSecretsBackend{out: "tok=" + valor, vals: []string{valor}}
	h = &Heartbeat{svc: fb}
	spec = adapter.Spec{Prompt: ref}
	vals, err = h.resolveRunUserSecrets(context.Background(), rc, &spec)
	if err != nil || len(vals) != 1 || vals[0] != valor || spec.Prompt != "tok="+valor || fb.calls != 1 || fb.in != ref {
		t.Fatalf("com referência: valores %d, erro %v, chamadas %d, texto recebido %q", len(vals), err, fb.calls, fb.in)
	}

	// sem delegação válida a run falha cedo: o erro nomeia a origem e o prompt não muda
	fb = &fakeUserSecretsBackend{err: errors.New("user/meu-token: sem delegação válida do dono para esta run")}
	h = &Heartbeat{svc: fb}
	spec = adapter.Spec{Prompt: ref}
	if _, err := h.resolveRunUserSecrets(context.Background(), rc, &spec); err == nil ||
		!strings.Contains(err.Error(), "segredo pessoal: ") || !strings.Contains(err.Error(), "sem delegação") || spec.Prompt != ref {
		t.Fatalf("sem delegação: erro %v, prompt %q", err, spec.Prompt)
	}

	// um cofre sem a expansão de usuário deixa o prompt como está
	spec = adapter.Spec{Prompt: ref}
	if vals, err := (&Heartbeat{svc: &struct{ backend }{}}).resolveRunUserSecrets(context.Background(), rc, &spec); err != nil || vals != nil || spec.Prompt != ref {
		t.Fatalf("backend sem o cofre: valores %v, erro %v, prompt %q", vals, err, spec.Prompt)
	}
}
