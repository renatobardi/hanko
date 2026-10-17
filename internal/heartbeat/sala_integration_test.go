//go:build integration

package heartbeat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// A room with the real worker and the fake adapter: Lia (moderator) answers a message without mentions and
// cites Beto, who wakes up with Lia's reply as input, answers in the thread under his own name, and the chain
// stops there (Beto's reply mentions nobody).
func TestRoomEndToEnd(t *testing.T) {
	ctx := context.Background()
	svc, spy, co, _ := newChatStack(t, -1)
	ana, err := svc.HirePersona(ctx, domain.HumanActor, co.ID, service.HirePersonaInput{Name: "Lia", Title: "Moderadora", Agent: "fake",
		AdapterConfig: json.RawMessage(`{"summary":"Entendi. @beto, você levanta os números?"}`)})
	if err != nil {
		t.Fatal(err)
	}
	beto, err := svc.HirePersona(ctx, domain.HumanActor, co.ID, service.HirePersonaInput{Name: "Beto", Title: "Dados", Agent: "fake",
		AdapterConfig: json.RawMessage(`{"summary":"Números levantados: 42."}`)})
	if err != nil {
		t.Fatal(err)
	}
	cv, err := svc.CreateRoom(ctx, domain.HumanActor, co.ID, service.CreateRoomInput{PersonaIDs: []domain.ID{ana.ID, beto.ID},
		Message: "Como fechamos a semana?"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "duas respostas", func() bool { return len(replies(t, svc, co.ID, cv.ID)) == 2 })
	rs := replies(t, svc, co.ID, cv.ID)
	if rs[0].PersonaName != "Lia" || rs[1].PersonaName != "Beto" || rs[1].Body != "Números levantados: 42." {
		t.Fatalf("replies: %+v", rs)
	}
	runs, _ := svc.ListRuns(ctx, co.ID, service.RunFilter{ConversationID: &cv.ID})
	if len(runs) != 2 {
		t.Fatalf("runs: %d", len(runs))
	}
	var betoSpec bool
	for _, sp := range spy.all() {
		if strings.Contains(sp.System, "Protocolo Hanko (sala)") && strings.Contains(sp.Prompt, "**Lia**") &&
			strings.Contains(sp.Prompt, "@beto, você levanta") && sp.ConversationID == cv.ID {
			betoSpec = true
		}
	}
	if !betoSpec {
		t.Fatalf("Beto's run did not get Lia's reply as input: %+v", spy.all())
	}
	got, _ := svc.GetConversation(ctx, co.ID, cv.ID)
	if got.ActiveRunID != nil || got.PendingMessages != 0 {
		t.Fatalf("room should be idle: %+v", got)
	}
}
