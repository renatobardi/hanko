//go:build integration

package cli

import (
	"strings"
	"testing"
)

// hanko workspace/project/task --project/cost --project contra o servidor de verdade (ADR-0025).
func TestCLIProjetosEWorkspaces(t *testing.T) {
	_, _, env := realServer(t)

	out := mustOK(t, env, "workspace", "create", "Site", "--path", "/srv/work/site", "--repo", "https://github.com/o/site", "--branch", "main")
	if !strings.Contains(out, "Site") {
		t.Fatalf("workspace create: %q", out)
	}
	mustFail(t, env, 1, "422", "workspace", "create", "Ruim", "--path", "relativo")
	mustFail(t, env, 1, "422", "workspace", "create", "Ruim2", "--path", "/srv/x", "--repo", "https://u:s@h/r")

	out = mustOK(t, env, "workspace", "update", "site", "--branch", "develop", "--name", "Site v2")
	if !strings.Contains(out, "Workspace atualizado: Site v2 (site)") {
		t.Errorf("workspace update: %q", out)
	}
	mustFail(t, env, 2, "informe o que alterar", "workspace", "update", "site")

	mustOK(t, env, "project", "create", "Lançamento", "--workspace", "site", "--budget-cents", "5000")
	out = mustOK(t, env, "workspace", "show", "site")
	for _, want := range []string{"/srv/work/site", "develop", "https://github.com/o/site", "1 projeto(s)", "projeto lancamento"} {
		if !strings.Contains(out, want) {
			t.Errorf("workspace show sem %q:\n%s", want, out)
		}
	}
	out = mustOK(t, env, "workspace", "list")
	if !strings.Contains(out, "Site v2") {
		t.Errorf("workspace list:\n%s", out)
	}

	// tasks com projeto
	out = mustOK(t, env, "task", "create", "Primeira", "--project", "lancamento")
	if !strings.Contains(out, "HNK-1") {
		t.Fatalf("task create: %q", out)
	}
	mustOK(t, env, "task", "create", "Solta")
	out = mustOK(t, env, "task", "list", "--project", "lancamento")
	if !strings.Contains(out, "Primeira") || strings.Contains(out, "Solta") {
		t.Errorf("task list --project:\n%s", out)
	}
	mustOK(t, env, "task", "create", "Feita", "--project", "lancamento", "--status", "done")

	out = mustOK(t, env, "project", "show", "lancamento")
	for _, want := range []string{"Lançamento (lancamento)", "ativo", "Workspace: site", "Tasks: 2", "progresso 50%", "Orçamento do mês"} {
		if !strings.Contains(out, want) {
			t.Errorf("project show sem %q:\n%s", want, out)
		}
	}
	if out = mustOK(t, env, "project", "list"); !strings.Contains(out, "lancamento") || !strings.Contains(out, "50%") {
		t.Errorf("project list:\n%s", out)
	}

	out = mustOK(t, env, "task", "project", "HNK-2", "lancamento")
	if !strings.Contains(out, "HNK-2 no projeto") {
		t.Errorf("task project: %q", out)
	}
	out = mustOK(t, env, "task", "project", "HNK-2", "--none")
	if !strings.Contains(out, "HNK-2 sem projeto.") {
		t.Errorf("task project --none: %q", out)
	}
	mustFail(t, env, 2, "informe o projeto ou use --none", "task", "project", "HNK-2")

	// editar, orçamento e arquivar
	out = mustOK(t, env, "project", "update", "lancamento", "--name", "Lançamento v2", "--budget-cents", "9000")
	if !strings.Contains(out, "Projeto atualizado: Lançamento v2 (lancamento) — ativo") {
		t.Errorf("project update: %q", out)
	}
	out = mustOK(t, env, "budget", "set", "--project", "lancamento", "120")
	if !strings.Contains(out, "Orçamento do projeto Lançamento v2: US$ 120.00") {
		t.Errorf("budget set --project: %q", out)
	}
	if out = mustOK(t, env, "budget", "show"); !strings.Contains(out, "lancamento") {
		t.Errorf("budget show sem o projeto:\n%s", out)
	}
	mustFail(t, env, 2, "não os dois", "budget", "set", "--project", "lancamento", "--persona", "x", "1")
	out = mustOK(t, env, "project", "update", "lancamento", "--archive")
	if !strings.Contains(out, "arquivado") {
		t.Errorf("archive: %q", out)
	}
	mustFail(t, env, 1, "422", "task", "create", "Tarde demais", "--project", "lancamento")
	mustFail(t, env, 2, "não os dois", "project", "update", "lancamento", "--archive", "--unarchive")
	mustOK(t, env, "project", "update", "lancamento", "--unarchive", "--clear-budget", "--clear-workspace")

	// custos filtrados por projeto
	out = mustOK(t, env, "cost", "--by", "persona", "--project", "lancamento")
	if !strings.Contains(out, "Total:") {
		t.Errorf("cost --project:\n%s", out)
	}
	mustFail(t, env, 1, "404", "cost", "--project", "fantasma")

	// excluir: com o projeto apontando para o workspace só com --unlink
	mustOK(t, env, "project", "update", "lancamento", "--workspace", "site")
	mustFail(t, env, 1, "409", "workspace", "delete", "site")
	out = mustOK(t, env, "workspace", "delete", "site", "--unlink")
	if !strings.Contains(out, "Workspace site excluído.") {
		t.Errorf("workspace delete --unlink: %q", out)
	}
	mustFail(t, env, 1, "404", "workspace", "show", "site")
}
