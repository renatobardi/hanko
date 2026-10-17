package heartbeat

import (
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

func roomRunContext(resume string) service.RunContext {
	t0 := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	rc := chatContext(nil, nil, resume)
	beto := domain.NewID()
	gone := domain.NewID()
	rc.Chat.Conversation.Kind = domain.ConversationRoom
	rc.Chat.Conversation.PersonaID = domain.ID{}
	rc.Chat.Conversation.Title = "Reunião de segunda"
	left := t0
	rc.Chat.Room = &service.RoomContext{Mode: domain.RoomMentions, ModeratorID: &rc.Persona.ID, Hop: 1, MaxHops: 2,
		Participants: []service.RoomParticipantView{
			{RoomParticipant: domain.RoomParticipant{PersonaID: rc.Persona.ID}, Name: "Ana", Slug: "ana", Title: "Engenheira"},
			{RoomParticipant: domain.RoomParticipant{PersonaID: beto}, Name: "Beto", Slug: "beto", Title: "Produto"},
			{RoomParticipant: domain.RoomParticipant{PersonaID: gone, RemovedAt: &left}, Name: "Caio", Slug: "caio"},
		}}
	reply := msg(domain.MessagePersona, "Já fiz a minha parte.", t0.Add(time.Minute))
	reply.PersonaID = &rc.Persona.ID
	betoMsg := msg(domain.MessagePersona, "@ana revisa os números?", t0.Add(2*time.Minute))
	betoMsg.PersonaID, betoMsg.PersonaName = &beto, "Beto"
	caioMsg := msg(domain.MessagePersona, "Saindo.", t0.Add(90*time.Second))
	caioMsg.PersonaID = &gone // sem nome na mensagem: vem dos participantes, mesmo de quem saiu
	rc.Chat.History = []domain.ConversationMessage{msg(domain.MessageHuman, "Qual o status?", t0), reply, caioMsg}
	rc.Chat.New = []domain.ConversationMessage{betoMsg}
	return rc
}

func TestBuildRoomPrompt(t *testing.T) {
	rc := roomRunContext("")
	system, prompt := BuildChatPrompt(rc, "briefing", "")
	for _, want := range []string{"Escreva código limpo.", "Protocolo Hanko (sala)", "Ana (Engenheira)", "Beto (@beto, Produto)", "moderadora",
		"`@slug`", "até 2 saltos", "hanko_task_create", "hanko_gate_request", "Não peça confirmação"} {
		if !strings.Contains(system, want) {
			t.Errorf("system lacks %q:\n%s", want, system)
		}
	}
	if strings.Contains(system, "Caio") || strings.Contains(system, "Ana (@ana") {
		t.Errorf("system lists someone who left or the persona herself:\n%s", system)
	}
	for _, want := range []string{"# Sala: Reunião de segunda", "salto 1", "## Histórico recente", "**Humano**", "**Ana (você)**", "**Caio**",
		"## Mensagens novas para você", "**Beto**", "@ana revisa os números?", "### Briefing"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt)
		}
	}
	if strings.Index(prompt, "Qual o status?") > strings.Index(prompt, "@ana revisa") {
		t.Error("history must come before the new messages")
	}

	_, resumed := BuildChatPrompt(roomRunContext("sess"), "", "")
	if strings.Contains(resumed, "Qual o status?") || !strings.Contains(resumed, "Sessão anterior retomada") || !strings.Contains(resumed, "@ana revisa") {
		t.Errorf("resumed room prompt:\n%s", resumed)
	}

	// histórico grande: só o que cabe no orçamento de 24 KiB
	big := roomRunContext("")
	for i := 0; i < 40; i++ {
		big.Chat.History = append(big.Chat.History, msg(domain.MessageHuman, strings.Repeat("x", 3000), time.Now()))
	}
	_, p := BuildChatPrompt(big, "", "")
	if !strings.Contains(p, "mensagens anteriores omitidas") || len(p) > MaxChatHistoryBytes+8<<10 {
		t.Errorf("history budget: %d bytes", len(p))
	}
}
