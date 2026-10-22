//go:build integration

package api_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Etapas do projeto e cards de status do M20 pela API (ADR-0048).
func TestAPIProjectStagesLifecycle(t *testing.T) {
	e := newAPIEnv(t)
	ctx := context.Background()
	tri := e.persona(e.co, "Tri", func(in *service.HirePersonaInput) { in.Slug = "tri" })
	dev := e.persona(e.co, "Dev", func(in *service.HirePersonaInput) { in.Slug = "dev" })
	pr, err := e.svc.CreateProject(ctx, domain.HumanActor, e.co.ID, service.CreateProjectInput{Name: "Site", Slug: "site"})
	if err != nil {
		t.Fatal(err)
	}
	stagesURL := e.base() + "/projects/site/stages"

	var empty api.ProjectStages
	e.want(200, "GET", stagesURL, "", &empty)
	if empty.ProjectSlug != "site" || len(empty.Stages) != 0 {
		t.Fatalf("projeto sem etapas: %+v", empty)
	}
	// validações: chave inválida, status que a persona não trabalha, persona inexistente
	e.want(422, "PUT", stagesURL, `{"stages":[{"key":"A b","name":"x","status":"todo"}]}`, nil)
	e.want(422, "PUT", stagesURL, `{"stages":[{"key":"a","name":"x","status":"in_review","persona":"tri"}]}`, nil)
	e.want(422, "PUT", stagesURL, `{"stages":[{"key":"a","name":"x","status":"todo","persona":"nao-existe"}]}`, nil)
	e.want(422, "PUT", stagesURL, `{"stages":[]}`, nil)

	var got api.ProjectStages
	e.want(200, "PUT", stagesURL, fmt.Sprintf(`{"stages":[
		{"key":"triagem","name":"Triagem","status":"todo","persona":%q},
		{"key":"dev","name":"Desenvolvimento","status":"in_progress","persona":%q,"exit_gate":"approval","instructions":"pequeno"},
		{"key":"qa","name":"QA","status":"in_review"}]}`, tri.Slug, dev.ID), &got)
	if len(got.Stages) != 3 || got.Stages[1].PersonaSlug != "dev" || got.Stages[1].ExitGate != "approval" || got.Stages[0].Position != 1 || got.Stages[2].PersonaID != "" {
		t.Fatalf("etapas: %+v", got.Stages)
	}

	// a task nasce na primeira etapa e a API devolve a posição
	tk := e.task(e.co, "Home nova", nil, func(in *service.CreateTaskInput) { in.ProjectID = &pr.ID })
	var task api.Task
	e.want(200, "GET", e.base()+"/tasks/"+tk.Key, "", &task)
	if task.Stage == nil || task.Stage.Key != "triagem" || task.PersonaID != tri.ID.String() {
		t.Fatalf("task: stage=%+v persona=%s", task.Stage, task.PersonaID)
	}
	var ts api.TaskStage
	e.want(200, "GET", e.base()+"/tasks/"+tk.Key+"/stage", "", &ts)
	if ts.State != "working" || ts.Stage.Key != "triagem" || ts.Position != 1 || ts.Total != 3 || ts.NextStage != "dev" || len(ts.Events) != 1 || ts.Events[0].Kind != "enrolled" {
		t.Fatalf("posição: %+v", ts)
	}

	// concluir, concluir de novo (gate), mover à mão e sair
	e.want(200, "POST", e.base()+"/tasks/"+tk.Key+"/stage/complete", `{"note":"triada"}`, &ts)
	if ts.Stage.Key != "dev" || ts.State != "working" {
		t.Fatalf("depois de concluir: %+v", ts)
	}
	e.want(200, "POST", e.base()+"/tasks/"+tk.Key+"/stage/complete", `{"note":"pronto"}`, &ts)
	if ts.State != "completing" {
		t.Fatalf("o dev tem gate de aprovação: %+v", ts)
	}
	var gates api.ListBody[api.Gate]
	e.want(200, "GET", e.base()+"/gates?status=pending", "", &gates)
	if len(gates.Items) != 1 || gates.Items[0].Kind != "approval" {
		t.Fatalf("gates: %+v", gates.Items)
	}
	e.want(200, "POST", e.base()+"/gates/"+gates.Items[0].ID+"/decide", `{"decision":"changes_requested","note":"cubra o caso vazio"}`, nil)
	var lr api.ListBody[api.StageLearning]
	e.want(200, "GET", e.base()+"/projects/site/stage-learnings?stage=dev", "", &lr)
	if len(lr.Items) != 1 || lr.Items[0].Decision != "changes_requested" || lr.Items[0].Note != "cubra o caso vazio" || lr.Items[0].TaskKey != tk.Key {
		t.Fatalf("aprendizados: %+v", lr.Items)
	}
	e.want(200, "GET", e.base()+"/tasks/"+tk.Key+"/stage", "", &ts)
	if ts.State != "working" || ts.Stage.Key != "dev" || len(ts.Learnings) != 1 {
		t.Fatalf("voltou para o dev: %+v", ts)
	}
	e.want(200, "POST", e.base()+"/tasks/"+tk.Key+"/stage/advance", `{"to":"triagem","note":"volta"}`, &ts)
	if ts.Stage.Key != "triagem" {
		t.Fatalf("mover à mão: %+v", ts)
	}
	e.want(422, "POST", e.base()+"/tasks/"+tk.Key+"/stage/advance", `{"to":"nao-existe"}`, nil)
	// a etapa com task não sai da lista
	e.want(409, "PUT", stagesURL, `{"stages":[{"key":"dev","name":"Dev","status":"in_progress"}]}`, nil)
	e.want(409, "DELETE", stagesURL, "", nil)
	e.want(204, "POST", e.base()+"/tasks/"+tk.Key+"/stage/leave", `{}`, nil)
	e.want(204, "DELETE", stagesURL, "", nil)
	e.want(404, "GET", e.base()+"/tasks/"+tk.Key+"/stage", "", nil) // a posição saiu com o pipeline

	// iniciar uma task solta numa etapa
	e.want(200, "PUT", stagesURL, `{"stages":[{"key":"a","name":"A","status":"todo"},{"key":"b","name":"B","status":"in_progress"}]}`, nil)
	solo := e.task(e.co, "avulsa", &dev, func(in *service.CreateTaskInput) { in.ProjectID = &pr.ID })
	e.want(200, "POST", e.base()+"/tasks/"+solo.Key+"/stage/start", `{"stage":"b"}`, &ts)
	if ts.Stage.Key != "b" {
		t.Fatalf("start: %+v", ts)
	}
	e.want(409, "POST", e.base()+"/tasks/"+solo.Key+"/stage/start", `{}`, nil)
}

func TestAPIStatusCardDecisionCronAndDashboard(t *testing.T) {
	e := newAPIEnv(t)
	base := e.base() + "/status-cards"
	var c api.StatusCard
	e.want(201, "POST", base, `{"interest":"o que está bloqueado?","engine":"decision","queries":[{"status":["blocked"]}],"on_dashboard":true,
		"refresh_policy":{"mode":"cron","cron":"0 7 * * 1-5","cron_tz":"America/Sao_Paulo"}}`, &c)
	if c.Engine != "decision" || c.State != "active" || !c.OnDashboard || c.QueryVersion != 1 || len(c.Queries) != 1 || c.Queries[0].Statuses[0] != "blocked" ||
		c.Policy.Mode != "cron" || c.Policy.Cron != "0 7 * * 1-5" || c.Policy.CronTZ != "America/Sao_Paulo" || c.SummarizerPersonaID != "" {
		t.Fatalf("criado: %+v", c)
	}
	e.want(422, "POST", base, `{"interest":"x","refresh_policy":{"mode":"cron","cron":"99 7 * * *"}}`, nil)
	e.want(422, "POST", base, `{"interest":"x","engine":"outro"}`, nil)
	// sem o decision model ligado a atualização manual é recusada com o motivo, e o card fica em erro
	e.want(422, "POST", base+"/"+c.ID+"/refresh", `{}`, nil)
	e.want(200, "GET", base+"/"+c.ID, "", &c)
	if c.State != "error" || c.FailureReason == "" {
		t.Fatalf("depois da recusa: %+v", c)
	}
	// o painel só lista cards que já têm resumo
	var dash api.ListBody[api.DashboardStatusCard]
	e.want(200, "GET", e.base()+"/dashboard/status-cards", "", &dash)
	if len(dash.Items) != 0 {
		t.Fatalf("painel: %+v", dash.Items)
	}
	e.want(200, "PATCH", base+"/"+c.ID, `{"on_dashboard":false,"queries":[{"status":["blocked","in_review"],"open":true}]}`, &c)
	if c.OnDashboard || len(c.Queries[0].Statuses) != 2 {
		t.Fatalf("editado: %+v", c)
	}
}
