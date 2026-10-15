package heartbeat

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

func chatContext(history, news []domain.ConversationMessage, resume string) service.RunContext {
	rc := sampleContext()
	cv := domain.Conversation{ID: domain.NewID(), CompanyID: rc.Company.ID, PersonaID: rc.Persona.ID, Title: "Plano da semana"}
	rc.Task = service.TaskView{}
	rc.Run.TaskID = domain.ID{}
	rc.Run.ConversationID = &cv.ID
	rc.Run.Trigger = domain.TriggerChat
	rc.Chat = &service.ChatContext{Conversation: cv, History: history, New: news, ResumeSession: resume}
	return rc
}

func msg(kind domain.MessageAuthor, body string, at time.Time) domain.ConversationMessage {
	return domain.ConversationMessage{ID: domain.NewID(), Kind: kind, Body: body, CreatedAt: at}
}

func TestBuildChatPrompt(t *testing.T) {
	t0 := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	hist := []domain.ConversationMessage{
		msg(domain.MessageHuman, "Qual o risco?", t0),
		msg(domain.MessagePersona, "Baixo.", t0.Add(time.Minute)),
		msg(domain.MessageSystem, "A execução falhou.", t0.Add(2*time.Minute)),
	}
	news := []domain.ConversationMessage{msg(domain.MessageHuman, "E o plano B?", t0.Add(time.Hour)), msg(domain.MessageHuman, "Responda curto.", t0.Add(time.Hour+time.Minute))}

	t.Run("fresh session carries history, memory and every new message", func(t *testing.T) {
		rc := chatContext(hist, news, "")
		system, prompt := BuildChatPrompt(rc, "briefing do projeto", "handoff anterior")
		for _, want := range []string{"Escreva código limpo.", "Protocolo Hanko (conversa)", "Ana (Engenheira)", "Acme", "hanko_task_create", "hanko_gate_request"} {
			if !strings.Contains(system, want) {
				t.Errorf("system lacks %q:\n%s", want, system)
			}
		}
		for _, want := range []string{"# Conversa: Plano da semana", rc.Chat.Conversation.ID.String(), "gatilho: chat", "## Histórico recente", "Qual o risco?", "Baixo.", "A execução falhou.",
			"Ana (você)", "Sistema", "### Handoff", "handoff anterior", "### Briefing", "briefing do projeto", "## Mensagens novas", "E o plano B?", "Responda curto.", "Responda agora"} {
			if !strings.Contains(prompt, want) {
				t.Errorf("prompt lacks %q:\n%s", want, prompt)
			}
		}
		if strings.Index(prompt, "Qual o risco?") > strings.Index(prompt, "E o plano B?") {
			t.Error("history must come before the new messages")
		}
		if strings.Contains(prompt, "HNK-") || strings.Contains(prompt, "Task ") {
			t.Errorf("a chat prompt has no task:\n%s", prompt)
		}
	})

	t.Run("resumed session does not repeat the history", func(t *testing.T) {
		_, prompt := BuildChatPrompt(chatContext(hist, news, "sess-1"), "", "")
		if strings.Contains(prompt, "Baixo.") || strings.Contains(prompt, "## Histórico recente") || !strings.Contains(prompt, "Sessão anterior retomada") {
			t.Errorf("history repeated on resume:\n%s", prompt)
		}
		if !strings.Contains(prompt, "E o plano B?") || strings.Contains(prompt, "## Memória do projeto") {
			t.Errorf("new messages missing or empty memory shown:\n%s", prompt)
		}
	})

	t.Run("first message of an empty history", func(t *testing.T) {
		_, prompt := BuildChatPrompt(chatContext(nil, news[:1], ""), "", "")
		if !strings.Contains(prompt, "(sem mensagens anteriores)") {
			t.Errorf("prompt:\n%s", prompt)
		}
	})

	t.Run("long histories keep the newest messages within the budget", func(t *testing.T) {
		var long []domain.ConversationMessage
		for i := 0; i < 200; i++ {
			long = append(long, msg(domain.MessageHuman, fmt.Sprintf("mensagem-%03d %s", i, strings.Repeat("x", 900)), t0.Add(time.Duration(i)*time.Minute)))
		}
		_, prompt := BuildChatPrompt(chatContext(long, news[:1], ""), "", "")
		if len(prompt) > MaxChatHistoryBytes+8<<10 {
			t.Errorf("prompt has %d bytes, budget is %d plus the new messages", len(prompt), MaxChatHistoryBytes)
		}
		if !strings.Contains(prompt, "mensagem-199") || strings.Contains(prompt, "mensagem-000") || !strings.Contains(prompt, "mensagens anteriores omitidas") {
			t.Errorf("expected the newest kept and the oldest dropped with a notice")
		}
	})

	t.Run("one huge message is clipped", func(t *testing.T) {
		huge := msg(domain.MessageHuman, strings.Repeat("y", 100<<10), t0)
		_, prompt := BuildChatPrompt(chatContext(nil, []domain.ConversationMessage{huge}, ""), "", "")
		if len(prompt) > 16<<10 {
			t.Errorf("prompt has %d bytes", len(prompt))
		}
	})
}
