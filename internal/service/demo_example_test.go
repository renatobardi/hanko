package service_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/export"
	"github.com/renatobardi/hanko/internal/service"
)

// TestExemploDemoImporta garante que examples/demo-company.json continua importável e íntegro
// quando o formato evoluir (ADR-0022). Também é o cenário das demos (docs/DEMOS.md).
func TestExemploDemoImporta(t *testing.T) {
	raw, err := os.ReadFile("../../examples/demo-company.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc export.Document
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("o exemplo não é um export.Document: %v", err)
	}
	if doc.Format != export.Format {
		t.Fatalf("formato = %q", doc.Format)
	}

	e := newEnv(t)
	e.svc.WorkspacesRoot = "/tmp/hanko-demo" // como no roteiro das demos
	bg := context.Background()
	res, err := e.svc.ImportCompany(bg, domain.HumanActor, doc, service.ImportOptions{Slug: "demo"})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	want := map[string]int{"workspaces": 1, "projects": 1, "subscriptions": 1, "personas": 3, "tasks": 4, "comments": 1, "flows": 1, "runs": 0}
	for k, v := range want {
		if res.Counts[k] != v {
			t.Errorf("counts[%s] = %d, quer %d", k, res.Counts[k], v)
		}
	}
	co := res.Company
	if co.Slug != "demo" || co.TaskPrefix != "DEMO" || co.BudgetMonthlyCents == nil || *co.BudgetMonthlyCents != 5000 {
		t.Fatalf("empresa = %+v", co)
	}

	ps, err := e.svc.ListPersonas(bg, co.ID)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]domain.Persona{}
	byID := map[domain.ID]domain.Persona{}
	for _, p := range ps {
		by[p.Slug] = p
		byID[p.ID] = p
	}
	cto, ana, rui := by["cto"], by["ana"], by["rui"]
	if cto.Agent != "claude" || cto.Model != "sonnet" || ana.Agent != "claude" || ana.Model != "haiku" || rui.Agent != "codex" {
		t.Errorf("agentes/modelos: %s/%s %s/%s %s", cto.Agent, cto.Model, ana.Agent, ana.Model, rui.Agent)
	}
	if ana.ReportsTo == nil || *ana.ReportsTo != cto.ID || rui.ReportsTo == nil || *rui.ReportsTo != cto.ID || cto.ReportsTo != nil {
		t.Errorf("organograma incorreto")
	}
	if !ana.RequireReview || ana.ReviewerPersonaID == nil || *ana.ReviewerPersonaID != rui.ID {
		t.Errorf("revisão da Ana: require=%v reviewer=%v", ana.RequireReview, ana.ReviewerPersonaID)
	}
	if ana.BudgetMonthlyCents == nil || *ana.BudgetMonthlyCents != 1000 {
		t.Errorf("orçamento da Ana = %v", ana.BudgetMonthlyCents)
	}
	if ana.SubscriptionID == nil || cto.SubscriptionID == nil || rui.SubscriptionID != nil {
		t.Errorf("assinaturas: ana=%v cto=%v rui=%v", ana.SubscriptionID, cto.SubscriptionID, rui.SubscriptionID)
	}
	if oc, err := e.svc.OrgChart(bg, co.ID); err != nil || oc.Total != 3 || oc.Depth != 2 || len(oc.Roots) != 1 {
		t.Errorf("organograma: %+v %v", oc, err)
	}

	tasks, err := e.svc.ListTasks(bg, co.ID, service.TaskFilter{Limit: 100})
	if err != nil || len(tasks) != 4 {
		t.Fatalf("tasks = %d, %v", len(tasks), err)
	}
	var child *service.TaskView
	for i := range tasks {
		if tasks[i].Number == 3 {
			child = &tasks[i]
		}
	}
	if child == nil || child.Key != "DEMO-3" || child.ParentID == nil || child.PersonaID == nil || *child.PersonaID != ana.ID {
		t.Fatalf("task 3 = %+v", child)
	}
	if cs, err := e.svc.ListComments(bg, co.ID, child.ID); err != nil || len(cs) != 1 {
		t.Errorf("comentários da DEMO-3: %d %v", len(cs), err)
	}

	fl, err := e.svc.ListFlows(bg, co.ID)
	if err != nil || len(fl) != 1 || fl[0].PersonaID != cto.ID || fl[0].Cron != "0 9 * * *" {
		t.Fatalf("fluxos: %+v %v", fl, err)
	}
	subs, err := e.svc.ListSubscriptions(bg, co.ID)
	if err != nil || len(subs) != 1 || subs[0].Name != "Plano Claude" {
		t.Fatalf("assinaturas: %+v %v", subs, err)
	}

	// importar duas vezes na mesma instalação é 409, sem sobras
	if _, err := e.svc.ImportCompany(bg, domain.HumanActor, doc, service.ImportOptions{Slug: "demo"}); err == nil {
		t.Errorf("segundo import com o mesmo slug deveria falhar")
	}
}
