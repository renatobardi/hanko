//go:build integration

package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
)

// Coordenação de tasks pela API (ADR-0040): dependências, árvore, pausa herdada e política de revisão.
func TestAPICoordination(t *testing.T) {
	e := newAPIEnv(t)
	base := e.base()
	var a, b, c api.Task
	e.want(201, "POST", base+"/tasks", `{"title":"A"}`, &a)
	e.want(201, "POST", base+"/tasks", `{"title":"B","depends_on":["`+a.Key+`"]}`, &b)
	if !b.Blocked || b.OpenDependencies != 1 || b.DependenciesTotal != 1 {
		t.Fatalf("B nasce bloqueada: %+v", b)
	}
	e.want(201, "POST", base+"/tasks", `{"title":"C","parent":"`+a.Key+`"}`, &c)
	if c.ParentID != a.ID {
		t.Fatalf("pai: %+v", c)
	}

	// ciclo: 409 com a cadeia
	status, body := e.call("POST", base+"/tasks/"+a.Key+"/dependencies", `{"depends_on":"`+b.Key+`"}`, nil)
	if status != http.StatusConflict || !strings.Contains(string(body), a.Key) || !strings.Contains(string(body), b.Key) {
		t.Fatalf("ciclo: %d %s", status, body)
	}
	if status, _ := e.call("POST", base+"/tasks/"+a.Key+"/dependencies", `{"depends_on":"HNK-999"}`, nil); status != http.StatusUnprocessableEntity {
		t.Errorf("dependência inexistente: %d", status)
	}
	var rel api.TaskRelations
	e.want(200, "GET", base+"/tasks/"+b.Key+"/relations", "", &rel)
	if len(rel.DependsOn) != 1 || rel.DependsOn[0].Key != a.Key || rel.DependsOn[0].Closed {
		t.Fatalf("relations: %+v", rel)
	}
	e.want(200, "GET", base+"/tasks/"+a.Key+"/relations", "", &rel)
	if len(rel.Children) != 1 || len(rel.Dependents) != 1 {
		t.Fatalf("relations da A: %+v", rel)
	}

	// fechar A com a filha C aberta: 422; override humano com motivo funciona
	if status, body := e.call("PATCH", base+"/tasks/"+a.Key, `{"status":"done"}`, nil); status != http.StatusUnprocessableEntity || !strings.Contains(string(body), c.Key) {
		t.Fatalf("pai com filho aberto: %d %s", status, body)
	}
	e.want(200, "PATCH", base+"/tasks/"+a.Key, `{"status":"done","force_close":true,"override_reason":"C virou outra frente"}`, &a)
	if a.Status != "done" {
		t.Fatalf("override: %+v", a)
	}
	e.want(200, "GET", base+"/tasks/"+b.Key, "", &b)
	if b.Blocked {
		t.Fatalf("fechar A libera B: %+v", b)
	}
	e.want(200, "DELETE", base+"/tasks/"+b.Key+"/dependencies/"+a.Key, "", &b)

	// pausa herdada
	var held api.Task
	e.want(200, "POST", base+"/tasks/"+c.Key+"/hold", `{"reason":"congelar"}`, &held)
	if held.Hold == nil || held.Hold.Inherited || held.Hold.Reason != "congelar" {
		t.Fatalf("hold: %+v", held.Hold)
	}
	var kid api.Task
	e.want(201, "POST", base+"/tasks", `{"title":"filha da pausada","parent":"`+c.Key+`"}`, &kid)
	if kid.Hold == nil || !kid.Hold.Inherited || kid.Hold.OriginKey != c.Key {
		t.Fatalf("herança: %+v", kid.Hold)
	}
	if status, _ := e.call("POST", base+"/tasks/"+c.Key+"/hold", `{"reason":"de novo"}`, nil); status != http.StatusConflict {
		t.Errorf("pausar duas vezes: %d", status)
	}
	if status, _ := e.call("POST", base+"/tasks/"+kid.Key+"/resume", "", nil); status != http.StatusUnprocessableEntity {
		t.Errorf("retomar herdada: %d", status)
	}
	var heldList api.ListBody[api.Task]
	e.want(200, "GET", base+"/held-tasks", "", &heldList)
	if len(heldList.Items) != 1 || heldList.Items[0].Key != c.Key {
		t.Fatalf("held-tasks: %+v", heldList.Items)
	}
	e.want(200, "POST", base+"/tasks/"+c.Key+"/resume", "", &held)
	if held.Hold != nil {
		t.Fatalf("retomada: %+v", held.Hold)
	}
	if status, _ := e.call("POST", base+"/tasks/"+c.Key+"/hold", `{"reason":""}`, nil); status != http.StatusUnprocessableEntity {
		t.Errorf("motivo vazio: %d", status)
	}

	// política de revisão: projeto e task, precedência task > projeto > persona
	e.want(201, "POST", base+"/projects", `{"name":"Proj"}`, nil)
	var pol api.ReviewOverride
	e.want(200, "PUT", base+"/projects/proj/review-policy", `{"require_review":true}`, &pol)
	if pol.RequireReview == nil || !*pol.RequireReview || pol.ReviewerMode != "inherit" {
		t.Fatalf("política do projeto: %+v", pol)
	}
	var inProj api.Task
	e.want(201, "POST", base+"/tasks", `{"title":"no projeto","project":"proj"}`, &inProj)
	var tp api.TaskReviewPolicy
	e.want(200, "GET", base+"/tasks/"+inProj.Key+"/review-policy", "", &tp)
	if !tp.Effective.Required || tp.Effective.RequiredSource != "project" {
		t.Fatalf("efetiva: %+v", tp.Effective)
	}
	e.want(200, "PUT", base+"/tasks/"+inProj.Key+"/review-policy", `{"require_review":false}`, &tp)
	if tp.Effective.Required || tp.Effective.RequiredSource != "task" || tp.Task.RequireReview == nil {
		t.Fatalf("a task vence o projeto: %+v", tp)
	}
	if status, _ := e.call("PUT", base+"/tasks/"+inProj.Key+"/review-policy", `{"reviewer_mode":"custom"}`, nil); status != http.StatusUnprocessableEntity {
		t.Errorf("custom sem revisor: %d", status)
	}
	e.want(200, "PUT", base+"/tasks/"+inProj.Key+"/review-policy", `{}`, &tp)
	if tp.Task.RequireReview != nil || tp.Effective.RequiredSource != "project" {
		t.Fatalf("limpar a sobreposição: %+v", tp)
	}
}
