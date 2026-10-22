package heartbeat

import (
	"fmt"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

const (
	// MaxChatHistoryBytes is the budget of the conversation history that goes into a chat prompt; older
	// messages beyond it are left out (the persona's session and memory carry what matters).
	MaxChatHistoryBytes = 24 << 10
	maxChatMessageBytes = 8 << 10
	maxChatHistoryMsg   = 4 << 10
)

// BuildChatPrompt builds the appended system prompt (persona instructions + conversation protocol) and the user
// prompt (history, project memory, the new messages) of a chat run. It is pure. When the run resumes the
// harness session the history is not repeated: the session already holds it.
func BuildChatPrompt(rc service.RunContext, briefing, handoff string) (system, prompt string) {
	p, c, cc := rc.Persona, rc.Company, rc.Chat
	if cc.Room != nil {
		return BuildRoomPrompt(rc, briefing, handoff) // sala com várias personas (ADR-0033)
	}

	var sb strings.Builder
	if s := strings.TrimSpace(p.Instructions); s != "" {
		sb.WriteString(s)
		sb.WriteString("\n\n")
	}
	title := p.Name
	if p.Title != "" {
		title += " (" + p.Title + ")"
	}
	sb.WriteString("## Protocolo Hanko (conversa)\n\n")
	fmt.Fprintf(&sb, "Você é a persona %s da empresa %s e está conversando diretamente com uma pessoa da equipe, fora de qualquer task.\n", title, c.Name)
	sb.WriteString("- Responda às mensagens novas de forma direta e útil. Sua resposta final é publicada automaticamente na conversa, em markdown; não a repita via tool.\n")
	sb.WriteString("- Esta conversa não tem task. As tools MCP `hanko_*` que dependem da task da execução (como `hanko_gate_request` sem informar a task) não funcionam aqui.\n")
	sb.WriteString("- Para trabalho que mereça acompanhamento, abra uma task com `hanko_task_create` (a conversa é citada na descrição) e diga a chave dela na resposta. Pode consultar tasks (`hanko_task_list`, `hanko_task_get`) e personas (`hanko_persona_list`).\n")
	sb.WriteString("- Não execute ações irreversíveis (merge, deploy, gasto, contratação, apagar dados) a partir do chat: abra uma task e peça a decisão de um humano nela com `hanko_gate_request`.\n")
	sb.WriteString(artifactProtocol)
	system = strings.TrimSpace(sb.String())

	var pb strings.Builder
	fmt.Fprintf(&pb, "# Conversa: %s\n\n", cc.Conversation.Title)
	fmt.Fprintf(&pb, "- Conversa: %s\n- Execução: %s (gatilho: %s)\n\n", cc.Conversation.ID, rc.Run.ID, rc.Run.Trigger)

	if cc.ResumeSession != "" {
		pb.WriteString("## Histórico\n\n(Sessão anterior retomada: você já conhece o que foi dito antes.)\n\n")
	} else {
		pb.WriteString("## Histórico recente\n\n")
		writeChatHistory(&pb, rc, cc.History)
	}

	handoff, briefing = strings.TrimSpace(handoff), strings.TrimSpace(briefing)
	if handoff != "" || briefing != "" {
		pb.WriteString("## Memória do projeto\n\n")
		if handoff != "" {
			pb.WriteString("### Handoff\n\n" + handoff + "\n\n")
		}
		if briefing != "" {
			pb.WriteString("### Briefing\n\n" + briefing + "\n\n")
		}
	}

	pb.WriteString(continuationSection(rc.Continuation, cc.ResumeSession != ""))
	pb.WriteString(attachmentsSection(rc))
	pb.WriteString(artifactsSection(rc))
	pb.WriteString("## Mensagens novas\n\n")
	if len(cc.New) == 0 {
		pb.WriteString("(nenhuma mensagem nova)\n\n")
	}
	for _, m := range cc.New {
		fmt.Fprintf(&pb, "**%s** · %s\n\n%s\n\n", chatAuthor(rc, m), m.CreatedAt.UTC().Format("2006-01-02 15:04 UTC"), clip(strings.TrimSpace(m.Body), maxChatMessageBytes))
	}
	pb.WriteString("---\nResponda agora às mensagens novas.\n")
	return system, pb.String()
}

// writeChatHistory writes the newest messages that fit MaxChatHistoryBytes, oldest first.
func writeChatHistory(pb *strings.Builder, rc service.RunContext, hist []domain.ConversationMessage) {
	if len(hist) == 0 {
		pb.WriteString("(sem mensagens anteriores)\n\n")
		return
	}
	used, from := 0, len(hist)
	for i := len(hist) - 1; i >= 0; i-- {
		n := min(len(hist[i].Body), maxChatHistoryMsg) + 64
		if used+n > MaxChatHistoryBytes && from < len(hist) {
			break
		}
		used += n
		from = i
	}
	if from > 0 {
		fmt.Fprintf(pb, "(%d mensagens anteriores omitidas)\n\n", from)
	}
	for _, m := range hist[from:] {
		fmt.Fprintf(pb, "**%s** · %s\n\n%s\n\n", chatAuthor(rc, m), m.CreatedAt.UTC().Format("2006-01-02 15:04 UTC"), clip(strings.TrimSpace(m.Body), maxChatHistoryMsg))
	}
}

func chatAuthor(rc service.RunContext, m domain.ConversationMessage) string {
	switch m.Kind {
	case domain.MessageHuman:
		return "Humano"
	case domain.MessageSystem:
		return "Sistema"
	}
	return rc.Persona.Name + " (você)"
}
