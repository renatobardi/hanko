package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/adapter/acp"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// O comando do harness acp roda na máquina do servidor e quem o escreve é o admin de uma empresa:
// o operador decide (HANKO_ACP_ALLOW / HANKO_ACP_COMMANDS), na contratação e na importação.
func TestACPOperatorGateOnHireAndImport(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	reason := func(err error, field string) string {
		t.Helper()
		var ve *domain.ValidationError
		if !errors.As(err, &ve) || !errors.Is(err, domain.ErrInvalid) || !strings.HasSuffix(ve.Field, field) {
			t.Fatalf("err = %v, quer erro de validação em %s", err, field)
		}
		return ve.Reason
	}

	e.svc.ACP = acp.Policy{} // padrão do operador: desligado
	if r := reason(errOf(hireACP(e, `{"command":"/bin/sh","args":["-c","id"]}`)), "adapter_config"); !strings.Contains(r, "HANKO_ACP_ALLOW") {
		t.Errorf("desligado: %q", r)
	}

	e.svc.ACP = acp.Policy{Allow: true, Commands: []string{"meu-agente"}}
	if r := reason(errOf(hireACP(e, `{"command":"/bin/sh","args":["-c","id"]}`)), "adapter_config"); !strings.Contains(r, "HANKO_ACP_COMMANDS") {
		t.Errorf("fora da lista: %q", r)
	}
	p, err := hireACP(e, `{"command":"meu-agente"}`)
	if err != nil {
		t.Fatalf("na lista: %v", err)
	}

	// importação: o mesmo gate, e nada é criado
	doc, err := e.svc.ExportCompany(ctx, e.co.ID, service.ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i := range doc.Personas {
		if doc.Personas[i].Agent == "acp" {
			doc.Personas[i].AdapterConfig = map[string]any{"command": "/bin/sh", "args": []any{"-c", "id"}}
		}
	}
	adm := mkUser(t, e, "adm@x.com", domain.RoleAdmin)
	_, err = e.svc.ImportCompany(as(adm), domain.HumanActor, doc, service.ImportOptions{Slug: "copia"})
	if r := reason(err, "adapter_config"); !strings.Contains(r, "HANKO_ACP_COMMANDS") {
		t.Errorf("import fora da lista: %q", r)
	}
	if _, err := e.svc.GetCompany(ctx, "copia"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("a recusa não pode deixar empresa: %v", err)
	}
	_ = p
}

func errOf(_ domain.Persona, err error) error { return err }
