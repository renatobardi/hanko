//go:build integration

package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Workspaces e projetos pela API (ADR-0025).
func TestAPIWorkspaces(t *testing.T) {
	e := newAPIEnv(t)
	ws := e.base() + "/workspaces"

	var site api.Workspace
	e.want(201, "POST", ws, `{"name":"Site","path":"/srv/work/site","repo_url":"https://github.com/o/site","default_branch":"main"}`, &site)
	if site.Slug != "site" || site.DefaultBranch != "main" || site.RepoURL != "https://github.com/o/site" {
		t.Fatalf("criado: %+v", site)
	}
	for name, body := range map[string]string{
		"caminho relativo":      `{"name":"A","path":"site"}`,
		"travessia":             `{"name":"B","path":"/srv/work/../etc"}`,
		"repo com credencial":   `{"name":"C","repo_url":"https://u:s@github.com/o/r"}`,
		"repo com esquema file": `{"name":"D","repo_url":"file:///etc/passwd"}`,
		"branch inválida":       `{"name":"E","default_branch":"a b"}`,
	} {
		if status, b := e.call("POST", ws, body, nil); status != http.StatusUnprocessableEntity {
			t.Errorf("%s: status %d, quer 422: %s", name, status, b)
		}
	}

	var got api.Workspace
	e.want(200, "PATCH", ws+"/site", `{"name":"Site v2","default_branch":"develop","repo_url":""}`, &got)
	if got.Name != "Site v2" || got.DefaultBranch != "develop" || got.RepoURL != "" || got.Path != "/srv/work/site" {
		t.Fatalf("patch: %+v", got)
	}
	if status, _ := e.call("PATCH", ws+"/site", `{"path":"relativo"}`, nil); status != http.StatusUnprocessableEntity {
		t.Errorf("patch com caminho ruim: %d", status)
	}

	// quem usa o workspace
	e.want(201, "POST", e.base()+"/personas", `{"name":"Dev","agent":"fake","model":"m","workspace":"site"}`, nil)
	e.want(201, "POST", e.base()+"/projects", `{"name":"Lançamento","workspace":"site"}`, nil)
	e.want(200, "GET", ws+"/site", "", &got)
	if got.Usage == nil || got.Usage.Personas != 1 || got.Usage.Projects != 1 || len(got.Usage.PersonaItems) != 1 || got.Usage.PersonaItems[0].Slug != "dev" {
		t.Fatalf("uso: %+v", got.Usage)
	}
	var list api.ListBody[api.Workspace]
	e.want(200, "GET", ws, "", &list)
	if len(list.Items) != 1 || list.Items[0].Usage == nil || list.Items[0].Usage.Personas != 1 {
		t.Fatalf("lista com uso: %+v", list.Items)
	}

	// em uso: 409; com unlink, desvincula e exclui
	if status, b := e.call("DELETE", ws+"/site", "", nil); status != http.StatusConflict {
		t.Fatalf("delete em uso: %d %s", status, b)
	}
	e.want(204, "DELETE", ws+"/site?unlink=true", "", nil)
	if status, _ := e.call("GET", ws+"/site", "", nil); status != http.StatusNotFound {
		t.Errorf("depois de excluir: %d", status)
	}
	var pr api.Project
	e.want(200, "GET", e.base()+"/projects/lancamento", "", &pr)
	if pr.WorkspaceID != "" {
		t.Errorf("o projeto continua apontando para o workspace excluído: %+v", pr)
	}
}

func TestAPIProjects(t *testing.T) {
	e := newAPIEnv(t)
	base := e.base()
	var pr api.Project
	e.want(201, "POST", base+"/projects", `{"name":"Lançamento","budget_monthly_cents":5000}`, &pr)
	if pr.Status != "active" || pr.BudgetMonthlyCents == nil || *pr.BudgetMonthlyCents != 5000 {
		t.Fatalf("criado: %+v", pr)
	}
	if status, _ := e.call("POST", base+"/projects", `{"name":"Neg","budget_monthly_cents":-1}`, nil); status != http.StatusUnprocessableEntity {
		t.Errorf("orçamento negativo: %d", status)
	}

	var task api.Task
	e.want(201, "POST", base+"/tasks", `{"title":"a","project":"lancamento"}`, &task)
	e.want(201, "POST", base+"/tasks", `{"title":"b","project":"lancamento","status":"done"}`, nil)
	e.want(200, "GET", base+"/projects/lancamento", "", &pr)
	if pr.Stats == nil || pr.Stats.TasksTotal != 2 || pr.Stats.Tasks["done"] != 1 || pr.Stats.ProgressPercent != 50 || pr.Stats.Budget.State != "ok" {
		t.Fatalf("stats: %+v", pr.Stats)
	}
	var tasks api.ListBody[api.Task]
	e.want(200, "GET", base+"/tasks?project=lancamento", "", &tasks)
	if len(tasks.Items) != 2 {
		t.Fatalf("filtro de tasks por projeto: %d", len(tasks.Items))
	}
	if status, _ := e.call("GET", base+"/tasks?project=nao-existe", "", nil); status != http.StatusNotFound {
		t.Errorf("projeto desconhecido no filtro de tasks: %d", status)
	}

	// arquivar: não recebe task nova; a task antiga sai com clear_project
	e.want(200, "PATCH", base+"/projects/lancamento", `{"status":"archived"}`, &pr)
	if pr.Status != "archived" {
		t.Fatalf("status: %+v", pr)
	}
	if status, b := e.call("POST", base+"/tasks", `{"title":"c","project":"lancamento"}`, nil); status != http.StatusUnprocessableEntity {
		t.Errorf("task em projeto arquivado: %d %s", status, b)
	}
	if status, _ := e.call("PATCH", base+"/projects/lancamento", `{"status":"pausado"}`, nil); status != http.StatusUnprocessableEntity {
		t.Errorf("status inválido: %d", status)
	}
	e.want(200, "PATCH", base+"/tasks/"+task.Key, `{"clear_project":true}`, &task)
	if task.ProjectID != "" {
		t.Errorf("clear_project: %+v", task)
	}
	e.want(200, "PATCH", base+"/projects/lancamento", `{"status":"active","clear_budget":true,"description":"d"}`, &pr)
	if pr.Status != "active" || pr.BudgetMonthlyCents != nil || pr.Description != "d" {
		t.Fatalf("reativar: %+v", pr)
	}
}

func TestAPIProjectBudgetAndCosts(t *testing.T) {
	e := newAPIEnv(t)
	base := e.base()
	p := e.persona(e.co, "Eng", nil)
	var pr, other api.Project
	e.want(201, "POST", base+"/projects", `{"name":"Capado","budget_monthly_cents":1000}`, &pr)
	e.want(201, "POST", base+"/projects", `{"name":"Livre"}`, &other)
	prID, _ := domain.ParseID(pr.ID)
	otherID, _ := domain.ParseID(other.ID)
	at := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

	spend := func(project domain.ID, usd float64) {
		e.t.Helper()
		e.setNow(at)
		tv := e.task(e.co, "gasto", &p, func(in *service.CreateTaskInput) { in.ProjectID = &project })
		run, err := e.svc.CheckoutRun(t.Context(), wakeOf(p, tv))
		if err != nil {
			e.t.Fatalf("checkout: %v", err)
		}
		if _, err := e.svc.MarkRunStarted(t.Context(), e.co.ID, run.ID, "tr", ""); err != nil {
			e.t.Fatal(err)
		}
		if _, err := e.svc.FinishRun(t.Context(), e.co.ID, run.ID, service.FinishRunInput{Status: domain.RunDone, Result: adapter.Result{CostUSD: &usd}}); err != nil {
			e.t.Fatal(err)
		}
	}
	for i := 0; i < 7; i++ {
		spend(prID, 1)
	}
	spend(prID, 1.2) // 82% de US$ 10
	spend(otherID, 4)

	e.want(200, "GET", base+"/projects/capado", "", &pr)
	if pr.Stats == nil || pr.Stats.Budget.State != "warning" || pr.Stats.Budget.SpentUSD != 8.2 {
		t.Fatalf("orçamento do projeto: %+v", pr.Stats)
	}

	var all, only api.CostSummary
	e.want(200, "GET", base+"/costs?group_by=project&from=2026-03-01&to=2026-03-31", "", &all)
	e.want(200, "GET", base+"/costs?group_by=persona&project=capado&from=2026-03-01&to=2026-03-31", "", &only)
	if all.TotalUSD != 12.2 || only.TotalUSD != 8.2 || only.Runs != 8 || only.PaidUSD != 0 {
		t.Fatalf("custos: total=%v filtrado=%+v", all.TotalUSD, only)
	}
	if status, _ := e.call("GET", base+"/costs?project=nao-existe", "", nil); status != http.StatusNotFound {
		t.Errorf("projeto desconhecido no filtro de custos: %d", status)
	}

	// estourar o teto: o gasto sobe, a próxima execução do projeto é recusada e a API diz por quê
	spend(prID, 2)
	tv := e.task(e.co, "mais uma", &p, func(in *service.CreateTaskInput) { in.ProjectID = &prID })
	if _, err := e.svc.CheckoutRun(t.Context(), wakeOf(p, tv)); err == nil {
		t.Fatal("o checkout deveria ser recusado")
	}
	e.want(200, "GET", base+"/projects/capado", "", &pr)
	if pr.Stats.Budget.State != "exceeded" {
		t.Fatalf("depois de estourar: %+v", pr.Stats.Budget)
	}
	var pe api.Persona
	e.want(200, "GET", base+"/personas/eng", "", &pe)
	if pe.Status == "paused" {
		t.Error("o orçamento do projeto não pode pausar a persona")
	}
}
