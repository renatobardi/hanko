package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Uma conversa com projeto (ADR-0090) roda no workspace do projeto quando a persona não tem o seu; sem projeto, a run
// de chat segue como antes (workspace da persona ou diretório temporário).
func TestConversationProjectWorkspace(t *testing.T) {
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
	p := e.persona(t, "Ana", nil) // sem workspace próprio

	// sem projeto: diretório temporário, como antes
	cv, err := e.svc.CreateConversation(ctx, domain.HumanActor, e.co.ID, service.CreateConversationInput{PersonaID: p.ID, Message: "oi"})
	if err != nil {
		t.Fatal(err)
	}
	if run := chatRun(t, e, cv.ID); run.WorkspaceID != nil || run.WorkspaceSource != domain.WorkspaceNone {
		t.Fatalf("chat sem projeto nem workspace da persona roda em diretório temporário: %+v", run)
	}
	if cv.ProjectID != nil {
		t.Fatalf("sem projeto: %+v", cv.Conversation)
	}

	// com projeto: o workspace do projeto, com a origem gravada na run
	cv, err = e.svc.CreateConversation(ctx, domain.HumanActor, e.co.ID, service.CreateConversationInput{PersonaID: p.ID, ProjectID: &pr.ID, Message: "oi"})
	if err != nil {
		t.Fatal(err)
	}
	if cv.ProjectID == nil || *cv.ProjectID != pr.ID {
		t.Fatalf("a conversa guarda o projeto: %+v", cv.Conversation)
	}
	if run := chatRun(t, e, cv.ID); run.WorkspaceID == nil || *run.WorkspaceID != ws.ID || run.WorkspaceSource != domain.WorkspaceFromProject {
		t.Fatalf("chat com projeto roda no workspace do projeto: %+v", run)
	}
	// a mensagem seguinte abre outra run no mesmo workspace
	finishChatRun(t, e, chatRun(t, e, cv.ID).ID, "", "ok")
	if _, err := e.svc.SendMessage(ctx, domain.HumanActor, e.co.ID, cv.ID, "e agora?"); err != nil {
		t.Fatal(err)
	}
	if run := chatRun(t, e, cv.ID); run.WorkspaceID == nil || *run.WorkspaceID != ws.ID {
		t.Fatalf("a run seguinte mantém o workspace do projeto: %+v", run)
	}

	// a precedência é a da task (projeto > persona): um chat sobre o projeto cai na mesma pasta que as tasks dele,
	// mesmo quando a persona tem workspace próprio
	other, err := e.svc.CreateWorkspace(ctx, domain.HumanActor, e.co.ID, service.CreateWorkspaceInput{Name: "Pessoal", Path: e.svc.WorkspacesRoot + "/" + e.co.Slug + "/pessoal"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, p.ID, service.UpdatePersonaInput{WorkspaceID: &other.ID}); err != nil {
		t.Fatal(err)
	}
	cv, err = e.svc.CreateConversation(ctx, domain.HumanActor, e.co.ID, service.CreateConversationInput{PersonaID: p.ID, ProjectID: &pr.ID, Message: "oi"})
	if err != nil {
		t.Fatal(err)
	}
	if run := chatRun(t, e, cv.ID); run.WorkspaceID == nil || *run.WorkspaceID != ws.ID || run.WorkspaceSource != domain.WorkspaceFromProject {
		t.Fatalf("projeto vem antes do workspace da persona, como nas tasks: %+v", run)
	}

	// projeto de outra empresa ou inexistente é recusado
	bogus := domain.NewID()
	if _, err := e.svc.CreateConversation(ctx, domain.HumanActor, e.co.ID, service.CreateConversationInput{PersonaID: p.ID, ProjectID: &bogus, Message: "oi"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("projeto inexistente: %v", err)
	}

	// sala com projeto: as runs das participantes também usam o workspace do projeto
	beto := e.persona(t, "Beto", nil)
	room, err := e.svc.CreateRoom(ctx, domain.HumanActor, e.co.ID, service.CreateRoomInput{Title: "Sala", PersonaIDs: []domain.ID{beto.ID, p.ID}, ProjectID: &pr.ID, Message: "@" + beto.Slug + " oi"})
	if err != nil {
		t.Fatal(err)
	}
	if room.ProjectID == nil || *room.ProjectID != pr.ID {
		t.Fatalf("a sala guarda o projeto: %+v", room.Conversation)
	}
	if run := chatRun(t, e, room.ID); run.WorkspaceID == nil || *run.WorkspaceID != ws.ID || run.WorkspaceSource != domain.WorkspaceFromProject {
		t.Fatalf("run da sala no workspace do projeto: %+v", run)
	}
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
