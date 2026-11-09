//go:build integration

package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// POST /conversations aceita o projeto (id ou slug) e devolve project_id (ADR-0090); projeto desconhecido é 404.
func TestAPIConversationProject(t *testing.T) {
	e := newAPIEnv(t)
	ctx := context.Background()
	base := e.base()
	p := e.persona(e.co, "Ana", nil)
	pr, err := e.svc.CreateProject(ctx, domain.HumanActor, e.co.ID, service.CreateProjectInput{Name: "Kubo", Slug: "kubo"})
	if err != nil {
		t.Fatal(err)
	}

	var cv api.Conversation
	e.want(http.StatusCreated, "POST", base+"/conversations", `{"persona":"`+p.Slug+`","project":"kubo","message":"oi"}`, &cv)
	if cv.ProjectID != pr.ID.String() {
		t.Fatalf("project_id: %+v", cv)
	}
	var got api.Conversation
	e.want(http.StatusOK, "GET", base+"/conversations/"+cv.ID, "", &got)
	if got.ProjectID != pr.ID.String() {
		t.Fatalf("get: %+v", got)
	}

	var plain api.Conversation
	e.want(http.StatusCreated, "POST", base+"/conversations", `{"persona":"`+p.Slug+`","message":"oi"}`, &plain)
	if plain.ProjectID != "" {
		t.Fatalf("sem projeto: %+v", plain)
	}
	e.want(http.StatusNotFound, "POST", base+"/conversations", `{"persona":"`+p.Slug+`","project":"nao-existe","message":"oi"}`, nil)
}
