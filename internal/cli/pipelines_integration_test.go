//go:build integration

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hanko pipeline/case/review/learnings/decision-example contra o servidor de verdade (ADR-0034).
func TestCLIPipelinesCases(t *testing.T) {
	_, _, env := realServer(t)

	out := mustOK(t, env, "pipeline", "create", "--name", "Vendas", "--stage", "novo=Novo:working", "--stage", "ganho=Ganho:done", "--stage", "perdido=Perdido:cancelled")
	if !strings.Contains(out, "Pipeline vendas criado com 3 etapa(s).") {
		t.Fatalf("create: %q", out)
	}
	cfg := filepath.Join(t.TempDir(), "rev.json")
	if err := os.WriteFile(cfg, []byte(`{"review":{"approve_to":"ganho","reject_to":"perdido","request_changes_to":"novo"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	mustOK(t, env, "pipeline", "stage", "add", "vendas", "--name", "Revisão", "--key", "revisao", "--kind", "review", "--position", "2", "--config", cfg)
	out = mustOK(t, env, "pipeline", "show", "vendas")
	for _, want := range []string{"Vendas (vendas)", "revisao", "→ ganho | perdido"} {
		if !strings.Contains(out, want) {
			t.Errorf("show sem %q:\n%s", want, out)
		}
	}
	mustFail(t, env, 1, "422", "pipeline", "stage", "add", "vendas", "--name", "X", "--kind", "review")
	if out = mustOK(t, env, "pipeline", "list"); !strings.Contains(out, "vendas") {
		t.Errorf("list:\n%s", out)
	}

	out = mustOK(t, env, "case", "create", "--title", "Lead Acme", "--type", "lead", "--key", "acme.com", "--pipeline", "vendas", "--field", "porte=grande", "--field", "vagas=10")
	if !strings.Contains(out, "Case HNK-C1 criado.") {
		t.Fatalf("case create: %q", out)
	}
	out = mustOK(t, env, "case", "create", "--title", "Lead Acme", "--type", "lead", "--key", "acme.com", "--summary", "200 pessoas")
	if !strings.Contains(out, "Case HNK-C1 atualizado (versão 2).") {
		t.Fatalf("upsert: %q", out)
	}
	mustOK(t, env, "case", "update", "HNK-C1", "--field", "vagas=12")
	mustFail(t, env, 2, "informe o que alterar", "case", "update", "HNK-C1")
	mustFail(t, env, 1, "409", "case", "update", "HNK-C1", "--title", "x", "--version", "1")
	out = mustOK(t, env, "case", "move", "HNK-C1", "revisao")
	if !strings.Contains(out, "em Revisão") {
		t.Fatalf("move: %q", out)
	}
	out = mustOK(t, env, "review", "list")
	if !strings.Contains(out, "HNK-C1") || !strings.Contains(out, "review") {
		t.Errorf("review list:\n%s", out)
	}
	mustFail(t, env, 1, "422", "review", "changes", "HNK-C1")
	mustOK(t, env, "review", "changes", "HNK-C1", "--reason", "falta o CNPJ")
	mustOK(t, env, "case", "move", "HNK-C1", "revisao")
	out = mustOK(t, env, "review", "approve", "HNK-C1")
	if !strings.Contains(out, "HNK-C1 aprovado.") {
		t.Fatalf("approve: %q", out)
	}
	if out = mustOK(t, env, "review", "list"); !strings.Contains(out, "Nada precisa de você agora.") {
		t.Errorf("fila vazia:\n%s", out)
	}
	out = mustOK(t, env, "case", "show", "HNK-C1")
	for _, want := range []string{"Lead Acme", "Vendas › Ganho", "vagas: 12", "review_decided — falta o CNPJ"} {
		if !strings.Contains(out, want) {
			t.Errorf("case show sem %q:\n%s", want, out)
		}
	}
	if out = mustOK(t, env, "case", "list", "--type", "lead"); !strings.Contains(out, "HNK-C1") {
		t.Errorf("case list:\n%s", out)
	}
	out = mustOK(t, env, "learnings", "--pipeline", "vendas")
	if !strings.Contains(out, "request_changes") || !strings.Contains(out, "falta o CNPJ") || !strings.Contains(out, "approve") {
		t.Errorf("learnings:\n%s", out)
	}
	out = mustOK(t, env, "--json", "learnings")
	var ls []map[string]any
	if err := json.Unmarshal([]byte(out), &ls); err != nil || len(ls) != 2 {
		t.Fatalf("learnings --json: %v %s", err, out)
	}
	mustOK(t, env, "decision-example", "list")
	if out = mustOK(t, env, "decision-example", "export"); strings.TrimSpace(out) != "" {
		t.Errorf("export vazio: %q", out)
	}
	mustOK(t, env, "pipeline", "transitions", "vendas", "--edge", "novo:revisao", "--strict")
	out = mustOK(t, env, "pipeline", "archive", "vendas")
	if !strings.Contains(out, "Pipeline arquivado: vendas") {
		t.Errorf("archive: %q", out)
	}
}
