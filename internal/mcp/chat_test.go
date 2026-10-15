package mcp

import (
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// chatSession connects with the token of a chat run: same Hanko MCP as any run, but with a conversation and no task.
func chatSession(t *testing.T) (*env, domain.ID, func()) {
	t.Helper()
	e := setup(t, false)
	cv := domain.NewID()
	runID := domain.NewID()
	tok, err := e.tokens.IssueForConversation(e.f.company.ID, e.f.personas[1].ID, runID, cv)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	e.f.runs[runID] = service.RunView{Run: domain.Run{ID: runID, Status: domain.RunRunning, Trigger: domain.TriggerChat, ConversationID: &cv, Agent: "fake", CreatedAt: started, StartedAt: &started},
		PersonaName: "Engenheira", ConversationTitle: "Plano"}
	e.runID = runID
	cs, err := e.connect(t, tok)
	if err != nil {
		t.Fatal(err)
	}
	e.cs = cs
	return e, cv, func() { _ = cs.Close() }
}

func TestChatWhoamiHasConversationAndNoTask(t *testing.T) {
	e, cv, done := chatSession(t)
	defer done()
	res := call(t, e.cs, "hanko_whoami", map[string]any{})
	var out whoamiOut
	structured(t, res, &out)
	if out.Actor != "persona" || out.PersonaSlug != "eng" || out.ConversationID != cv.String() || out.TaskKey != "" || out.RunID != e.runID.String() {
		t.Fatalf("whoami = %+v (%s)", out, text(res))
	}
	if !strings.Contains(text(res), "chat, sem task") {
		t.Errorf("text should say it is a chat: %s", text(res))
	}
}

// A persona can open a task from the chat: it has no parent and the description cites the conversation.
func TestChatTaskCreateCitesTheConversation(t *testing.T) {
	e, cv, done := chatSession(t)
	defer done()
	res := call(t, e.cs, "hanko_task_create", map[string]any{"title": "Revisar a migração", "description": "Ver o plano B."})
	if res.IsError {
		t.Fatal(text(res))
	}
	in := e.f.lastCreate
	if in.ParentID != nil {
		t.Errorf("a task opened from a chat has no parent: %v", in.ParentID)
	}
	if !strings.HasPrefix(in.Description, "Ver o plano B.") || !strings.Contains(in.Description, cv.String()) || !strings.Contains(in.Description, "/c/acme/chat/"+cv.String()) {
		t.Errorf("description = %q", in.Description)
	}
	if e.f.lastActor.Type != domain.ActorPersona {
		t.Errorf("actor = %+v", e.f.lastActor)
	}

	// without description the note alone becomes the description
	call(t, e.cs, "hanko_task_create", map[string]any{"title": "Outra"})
	if !strings.HasPrefix(e.f.lastCreate.Description, "Aberta a partir da conversa") {
		t.Errorf("description = %q", e.f.lastCreate.Description)
	}
}

// Tools that need the task of the run fail clearly in a chat instead of acting on nothing.
func TestChatGateRequestNeedsATask(t *testing.T) {
	e, _, done := chatSession(t)
	defer done()
	res := call(t, e.cs, "hanko_gate_request", map[string]any{"title": "Fazer deploy"})
	if !res.IsError || !strings.Contains(text(res), "conversa (chat)") || !strings.Contains(text(res), "task") {
		t.Fatalf("expected a clear refusal, got error=%v %q", res.IsError, text(res))
	}
	if len(e.f.gates) != 0 {
		t.Error("no gate may be created without a task")
	}
	// with an explicit task the persona can still ask for a decision
	if res := call(t, e.cs, "hanko_gate_request", map[string]any{"title": "Fazer deploy", "task": "HNK-1"}); res.IsError {
		t.Fatalf("gate with explicit task: %s", text(res))
	}
}
