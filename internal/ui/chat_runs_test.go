package ui

import (
	"net/http"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

// A chat run shows up in Execuções and in the run page identified as chat, pointing at its conversation.
func TestChatRunsAreIdentified(t *testing.T) {
	e, cv := chatEnv(t)
	now := uitest.Now()
	rv := service.RunView{
		Run: domain.Run{ID: uitest.ID("run:chat"), CompanyID: cv.CompanyID, PersonaID: cv.PersonaID, ConversationID: &cv.ID,
			Trigger: domain.TriggerChat, Status: domain.RunDone, Agent: "claude", CreatedAt: now, StartedAt: &now, FinishedAt: &now, Summary: "Hoje: **revisar**."},
		PersonaName: cv.PersonaName, ConversationTitle: cv.Title,
	}
	e.f.Runs = append(e.f.Runs, rv)

	rec := e.get(t, "/c/hanko/execucoes")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Chat", cv.Title)

	rec = e.get(t, "/c/hanko/execucoes/"+rv.ID.String())
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `href="/c/hanko/chat/`+cv.ID.String()+`"`, "conversa (chat)", cv.Title)
	wantNotContains(t, rec, `href="/c/hanko/tasks/"`)

	wantContains(t, e.get(t, "/c/hanko/personas/ada"), cv.Title, "/execucoes/"+rv.ID.String())
}
