//go:build integration

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hanko artifact/goal contra o servidor de verdade (ADR-0028).
func TestCLIArtefatos(t *testing.T) {
	_, _, env := realServer(t)
	dir := t.TempDir()
	plano := filepath.Join(dir, "plano.md")
	if err := os.WriteFile(plano, []byte("# um\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustOK(t, env, "task", "create", "Relatório")

	out := mustOK(t, env, "artifact", "put", plano, "--task", "HNK-1")
	if !strings.Contains(out, "Artefato plano.md v1 publicado") {
		t.Fatalf("put: %q", out)
	}
	if err := os.WriteFile(plano, []byte("# dois\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out = mustOK(t, env, "artifact", "put", plano, "--task", "HNK-1")
	if !strings.Contains(out, "plano.md v2 publicado") {
		t.Fatalf("put v2: %q", out)
	}
	// o mesmo conteúdo não cria versão
	if out = mustOK(t, env, "artifact", "put", plano, "--task", "HNK-1"); !strings.Contains(out, "plano.md v2 publicado") {
		t.Errorf("put repetido: %q", out)
	}
	// entrada padrão exige --name
	mustFail(t, env, 2, "informe --name", "artifact", "put", "-")

	var as []struct {
		ID      string `json:"id"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal([]byte(mustOK(t, env, "--json", "artifact", "list", "--task", "HNK-1")), &as); err != nil || len(as) != 1 || as[0].Version != 2 {
		t.Fatalf("list --json: %+v %v", as, err)
	}
	out = mustOK(t, env, "artifact", "list")
	if !strings.Contains(out, "plano.md") || !strings.Contains(out, "v2") || !strings.Contains(out, "HNK-1") {
		t.Errorf("list:\n%s", out)
	}
	if out = mustOK(t, env, "artifact", "list", "--kind", "image"); strings.Contains(out, "plano.md") {
		t.Errorf("list --kind image:\n%s", out)
	}

	// baixar: saída padrão e arquivo
	if out = mustOK(t, env, "artifact", "get", as[0].ID); out != "# dois\n" {
		t.Errorf("get: %q", out)
	}
	dest := filepath.Join(dir, "baixado.md")
	mustOK(t, env, "artifact", "get", as[0].ID, "-o", dest)
	if b, err := os.ReadFile(dest); err != nil || string(b) != "# dois\n" {
		t.Errorf("get -o: %q %v", b, err)
	}
	out = mustOK(t, env, "artifact", "get", as[0].ID, "--info")
	if !strings.Contains(out, "2/2") || !strings.Contains(out, "text/markdown") || !strings.Contains(out, "v1") {
		t.Errorf("get --info:\n%s", out)
	}
	out = mustOK(t, env, "artifact", "diff", as[0].ID)
	if !strings.Contains(out, "v1 → v2: +1 −1") || !strings.Contains(out, "+ # dois") || !strings.Contains(out, "- # um") {
		t.Errorf("diff:\n%s", out)
	}
	if out = mustOK(t, env, "task", "show", "HNK-1"); !strings.Contains(out, "Artefatos (1)") || !strings.Contains(out, "plano.md v2") {
		t.Errorf("task show sem artefatos:\n%s", out)
	}
	if out = mustOK(t, env, "artifact", "usage"); !strings.Contains(out, "Usado") || !strings.Contains(out, "Cota") {
		t.Errorf("usage:\n%s", out)
	}
	mustFail(t, env, 1, "404", "artifact", "get", "00000000-0000-0000-0000-000000000000")

	// exportar com artefatos
	exp := filepath.Join(dir, "empresa.json")
	mustOK(t, env, "export", "--with-artifacts", "-o", exp)
	b, err := os.ReadFile(exp)
	if err != nil || !strings.Contains(string(b), `"artifacts"`) {
		t.Fatalf("export --with-artifacts: %v", err)
	}
	out = mustOK(t, env, "import", exp, "--slug", "copia")
	if !strings.Contains(out, "artifacts") {
		t.Errorf("import não contou os artefatos:\n%s", out)
	}
	copyEnv := map[string]string{"HANKO_URL": env["HANKO_URL"], "HANKO_COMPANY": "copia"}
	if out = mustOK(t, copyEnv, "artifact", "list", "--all-versions"); strings.Count(out, "plano.md") != 2 {
		t.Errorf("artefatos importados:\n%s", out)
	}

	mustOK(t, env, "artifact", "rm", as[0].ID)
	if out = mustOK(t, env, "artifact", "list"); strings.Contains(out, "plano.md") {
		t.Errorf("depois de apagar:\n%s", out)
	}
}

func TestCLIMetas(t *testing.T) {
	_, _, env := realServer(t)
	mustOK(t, env, "project", "create", "Crescimento")

	out := mustOK(t, env, "goal", "create", "Chegar a 100 usuários", "--start", "10", "--target", "100", "--unit", "usuários",
		"--metric", "usuários ativos", "--due", "2026-12-31", "--project", "crescimento")
	if !strings.Contains(out, "Meta criada: Chegar a 100 usuários (chegar-a-100-usuarios)") {
		t.Fatalf("create: %q", out)
	}
	mustFail(t, env, 2, "informe o alvo", "goal", "create", "Sem alvo")
	mustFail(t, env, 1, "422", "goal", "create", "Igual", "--start", "5", "--target", "5")
	mustFail(t, env, 1, "422", "goal", "create", "Data", "--target", "5", "--due", "31/12/2026")

	out = mustOK(t, env, "goal", "progress", "chegar-a-100-usuarios", "55", "--note", "metade")
	if !strings.Contains(out, "50% (55 de 100 usuários)") {
		t.Errorf("progress: %q", out)
	}
	mustFail(t, env, 2, "valor inválido", "goal", "progress", "chegar-a-100-usuarios", "abc")

	// vírgula decimal
	if out = mustOK(t, env, "goal", "progress", "chegar-a-100-usuarios", "60,5"); !strings.Contains(out, "(60.5 de 100") {
		t.Errorf("vírgula: %q", out)
	}

	mustOK(t, env, "task", "create", "Campanha", "--goal", "chegar-a-100-usuarios")
	mustOK(t, env, "task", "create", "Outra")
	out = mustOK(t, env, "task", "goal", "HNK-2", "chegar-a-100-usuarios")
	if !strings.Contains(out, "HNK-2 ligada à meta") {
		t.Errorf("task goal: %q", out)
	}
	if out = mustOK(t, env, "task", "list", "--goal", "chegar-a-100-usuarios"); !strings.Contains(out, "Campanha") || !strings.Contains(out, "Outra") {
		t.Errorf("task list --goal:\n%s", out)
	}
	out = mustOK(t, env, "task", "goal", "HNK-2", "--none")
	if !strings.Contains(out, "HNK-2 sem meta.") {
		t.Errorf("task goal --none: %q", out)
	}
	mustFail(t, env, 2, "informe a meta ou use --none", "task", "goal", "HNK-2")
	if out = mustOK(t, env, "task", "show", "HNK-1"); !strings.Contains(out, "Chegar a 100 usuários (chegar-a-100-usuarios)") {
		t.Errorf("task show sem meta:\n%s", out)
	}

	out = mustOK(t, env, "goal", "show", "chegar-a-100-usuarios")
	for _, want := range []string{"Chegar a 100 usuários", "ativa", "60.5", "2026-12-31", "Crescimento", "Histórico (2)", "metade"} {
		if !strings.Contains(out, want) {
			t.Errorf("show sem %q:\n%s", want, out)
		}
	}
	if out = mustOK(t, env, "goal", "list", "--project", "crescimento"); !strings.Contains(out, "chegar-a-100-usuarios") || !strings.Contains(out, "2026-12-31") {
		t.Errorf("list:\n%s", out)
	}

	out = mustOK(t, env, "goal", "progress", "chegar-a-100-usuarios", "100")
	if !strings.Contains(out, "Meta atingida.") {
		t.Errorf("atingir: %q", out)
	}
	if out = mustOK(t, env, "goal", "list", "--status", "achieved"); !strings.Contains(out, "atingida") {
		t.Errorf("list achieved:\n%s", out)
	}
	mustFail(t, env, 1, "422", "goal", "progress", "chegar-a-100-usuarios", "120")
	out = mustOK(t, env, "goal", "update", "chegar-a-100-usuarios", "--target", "200", "--status", "active", "--clear-due")
	if !strings.Contains(out, "Meta atualizada") || !strings.Contains(out, "ativa") {
		t.Errorf("update: %q", out)
	}
	out = mustOK(t, env, "goal", "update", "chegar-a-100-usuarios", "--status", "abandoned")
	if !strings.Contains(out, "abandonada") {
		t.Errorf("abandon: %q", out)
	}
}
