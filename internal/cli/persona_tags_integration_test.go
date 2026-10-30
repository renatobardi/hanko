//go:build integration

package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// `hanko persona tag` contra o servidor de verdade (ADR-0087 item 9): cadastro, aplicar pelo nome, recorte e deletar.
func TestCLIPersonaTags(t *testing.T) {
	svc, co, env := realServer(t)
	ctx := context.Background()
	ceo, err := svc.HirePersona(ctx, domain.HumanActor, co.ID, service.HirePersonaInput{Name: "Ceo", Agent: "fake", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.HirePersona(ctx, domain.HumanActor, co.ID, service.HirePersonaInput{Name: "Eng", Agent: "fake", Model: "m", ReportsTo: &ceo.ID}); err != nil {
		t.Fatal(err)
	}

	if out := mustOK(t, env, "persona", "tag", "create", "Backend", "--color", "blue"); !strings.Contains(out, "Backend (blue)") {
		t.Fatalf("create: %q", out)
	}
	mustOK(t, env, "persona", "tag", "add", "eng", "backend")
	mustOK(t, env, "persona", "tag", "add", "ceo", "Gestão", "--create", "--color", "amber")
	if out := mustOK(t, env, "persona", "tag", "ls"); !strings.Contains(out, "Backend") || !strings.Contains(out, "Gestão") {
		t.Fatalf("ls: %q", out)
	}
	out := mustOK(t, env, "persona", "tag", "personas", "--tag", "backend", "--level", "2")
	if !strings.Contains(out, "eng") || strings.Contains(out, "ceo") {
		t.Fatalf("personas: %q", out)
	}
	mustOK(t, env, "persona", "tag", "edit", "backend", "--name", "Servidor")
	mustFail(t, env, 2, "--yes", "persona", "tag", "delete", "servidor")
	if out := mustOK(t, env, "persona", "tag", "delete", "servidor", "--yes"); !strings.Contains(out, "1 persona") {
		t.Fatalf("delete: %q", out)
	}
	mustFail(t, env, 2, "não encontrada", "persona", "tag", "remove", "eng", "servidor")
}
