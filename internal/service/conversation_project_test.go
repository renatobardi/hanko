package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// projectEnv prepara um workspace e um projeto que o usa, e uma persona sem workspace próprio.
type projectEnv struct {
	*env
	ws domain.Workspace
	pr domain.Project
	p  domain.Persona
}

func newProjectEnv(t *testing.T) *projectEnv {
	t.Helper()
	e := newEnv(t)
	ctx := context.Background()
	e.svc.WorkspacesRoot = t.TempDir()
	ws, err := e.svc.CreateWorkspace(ctx, domain.HumanActor, e.co.ID, service.CreateWorkspaceInput{Name: "Kubo", Path: e.svc.WorkspacesRoot + "/" + e.co.Slug + "/kubo"})
	if err != nil {
		t.Fatal(err)
	}
	pr, err := e.svc.CreateProject(ctx, domain.HumanActor, e.co.ID, service.CreateProjectInput{Name: "Kubo", WorkspaceID: &ws.ID})
	if err != nil {
		t.Fatal(err)
	}
	return &projectEnv{env: e, ws: ws, pr: pr, p: e.persona(t, "Ana", nil)}
}

// wantRunWorkspace confere a pasta de trabalho escolhida para a run mais recente da conversa.
func wantRunWorkspace(t *testing.T, e *env, convID domain.ID, ws *domain.ID, source domain.WorkspaceSource, why string) {
	t.Helper()
	run := chatRun(t, e, convID)
	switch {
	case ws == nil && run.WorkspaceID != nil, ws != nil && (run.WorkspaceID == nil || *run.WorkspaceID != *ws), run.WorkspaceSource != source:
		t.Fatalf("%s: workspace=%v origem=%s (%+v)", why, run.WorkspaceID, run.WorkspaceSource, run)
	}
}

// Uma conversa com projeto (ADR-0090) roda no workspace do projeto, com a precedência das tasks (projeto > persona >
// diretório temporário); sem projeto, a run de chat segue como antes.
func TestConversationProjectWorkspace(t *testing.T) {
	e := newProjectEnv(t)
	ctx := context.Background()

	cv, err := e.svc.CreateConversation(ctx, domain.HumanActor, e.co.ID, service.CreateConversationInput{PersonaID: e.p.ID, Message: "oi"})
	if err != nil {
		t.Fatal(err)
	}
	wantRunWorkspace(t, e.env, cv.ID, nil, domain.WorkspaceNone, "chat sem projeto nem workspace da persona roda em diretório temporário")
	if cv.ProjectID != nil {
		t.Fatalf("sem projeto: %+v", cv.Conversation)
	}

	cv, err = e.svc.CreateConversation(ctx, domain.HumanActor, e.co.ID, service.CreateConversationInput{PersonaID: e.p.ID, ProjectID: &e.pr.ID, Message: "oi"})
	if err != nil {
		t.Fatal(err)
	}
	if cv.ProjectID == nil || *cv.ProjectID != e.pr.ID {
		t.Fatalf("a conversa guarda o projeto: %+v", cv.Conversation)
	}
	wantRunWorkspace(t, e.env, cv.ID, &e.ws.ID, domain.WorkspaceFromProject, "chat com projeto roda no workspace do projeto")

	// a mensagem seguinte abre outra run no mesmo workspace
	finishChatRun(t, e.env, chatRun(t, e.env, cv.ID).ID, "", "ok")
	if _, err := e.svc.SendMessage(ctx, domain.HumanActor, e.co.ID, cv.ID, "e agora?"); err != nil {
		t.Fatal(err)
	}
	wantRunWorkspace(t, e.env, cv.ID, &e.ws.ID, domain.WorkspaceFromProject, "a run seguinte mantém o workspace do projeto")

	// projeto inexistente é recusado
	bogus := domain.NewID()
	if _, err := e.svc.CreateConversation(ctx, domain.HumanActor, e.co.ID, service.CreateConversationInput{PersonaID: e.p.ID, ProjectID: &bogus, Message: "oi"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("projeto inexistente: %v", err)
	}
}

// A precedência é a da task (projeto > persona): um chat sobre o projeto cai na mesma pasta que as tasks dele, mesmo
// quando a persona tem workspace próprio; sem projeto, vale o da persona.
func TestConversationProjectBeforePersonaWorkspace(t *testing.T) {
	e := newProjectEnv(t)
	ctx := context.Background()
	other, err := e.svc.CreateWorkspace(ctx, domain.HumanActor, e.co.ID, service.CreateWorkspaceInput{Name: "Pessoal", Path: e.svc.WorkspacesRoot + "/" + e.co.Slug + "/pessoal"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, e.p.ID, service.UpdatePersonaInput{WorkspaceID: &other.ID}); err != nil {
		t.Fatal(err)
	}
	cv, err := e.svc.CreateConversation(ctx, domain.HumanActor, e.co.ID, service.CreateConversationInput{PersonaID: e.p.ID, ProjectID: &e.pr.ID, Message: "oi"})
	if err != nil {
		t.Fatal(err)
	}
	wantRunWorkspace(t, e.env, cv.ID, &e.ws.ID, domain.WorkspaceFromProject, "projeto vem antes do workspace da persona, como nas tasks")

	cv, err = e.svc.CreateConversation(ctx, domain.HumanActor, e.co.ID, service.CreateConversationInput{PersonaID: e.p.ID, Message: "oi"})
	if err != nil {
		t.Fatal(err)
	}
	wantRunWorkspace(t, e.env, cv.ID, &other.ID, domain.WorkspaceFromPersona, "sem projeto vale o workspace da persona")
}

// Sala com projeto: as runs das participantes também usam o workspace do projeto.
func TestRoomProjectWorkspace(t *testing.T) {
	e := newProjectEnv(t)
	ctx := context.Background()
	beto := e.persona(t, "Beto", nil)
	room, err := e.svc.CreateRoom(ctx, domain.HumanActor, e.co.ID, service.CreateRoomInput{Title: "Sala", PersonaIDs: []domain.ID{beto.ID, e.p.ID}, ProjectID: &e.pr.ID, Message: "@" + beto.Slug + " oi"})
	if err != nil {
		t.Fatal(err)
	}
	if room.ProjectID == nil || *room.ProjectID != e.pr.ID {
		t.Fatalf("a sala guarda o projeto: %+v", room.Conversation)
	}
	wantRunWorkspace(t, e.env, room.ID, &e.ws.ID, domain.WorkspaceFromProject, "run da sala no workspace do projeto")
}

// chatRun devolve a run mais recente de uma conversa.
func chatRun(t *testing.T, e *env, convID domain.ID) domain.Run {
	t.Helper()
	runs, err := e.svc.ListRuns(context.Background(), e.co.ID, service.RunFilter{ConversationID: &convID})
	if err != nil || len(runs) == 0 {
		t.Fatalf("runs da conversa: %v %v", runs, err)
	}
	return runs[0].Run
}
