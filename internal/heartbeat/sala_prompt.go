package heartbeat

import (
	"fmt"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// BuildRoomPrompt builds the prompts of a run in a room (ADR-0033): the persona's instructions plus the room
// protocol (who else is there, when to mention), the recent history with who said what (same 24 KiB budget as
// the chat) and the new messages this run picked up. It is pure. A resumed session skips the history.
func BuildRoomPrompt(rc service.RunContext, briefing, handoff string) (system, prompt string) {
	p, c, cc := rc.Persona, rc.Company, rc.Chat
	room := cc.Room

	var sb strings.Builder
	if s := strings.TrimSpace(p.Instructions); s != "" {
		sb.WriteString(s)
		sb.WriteString("\n\n")
	}
	title := p.Name
	if p.Title != "" {
		title += " (" + p.Title + ")"
	}
	var others []string
	for _, x := range room.Participants {
		if !x.Active() || x.PersonaID == p.ID {
			continue
		}
		o := fmt.Sprintf("%s (@%s", x.Name, x.Slug)
		if x.Title != "" {
			o += ", " + x.Title
		}
		others = append(others, o+")")
	}
	sb.WriteString("## Protocolo Hanko (sala)\n\n")
	fmt.Fprintf(&sb, "Você é a persona %s da empresa %s e está numa sala com uma ou mais pessoas da equipe e com: %s.\n",
		title, c.Name, strings.Join(others, "; "))
	if room.ModeratorID != nil && *room.ModeratorID == p.ID {
		sb.WriteString("- Você é a moderadora da sala: quando ninguém é citado, a pergunta é sua; responda e, se outra persona for mais indicada, cite-a.\n")
	}
	sb.WriteString("- Responda só sobre o que lhe cabe, de forma direta. Sua resposta final é publicada automaticamente na sala, com o seu nome, em markdown; não a repita via tool.\n")
	sb.WriteString("- Cite outra persona com `@slug` só quando precisar que ela aja ou responda: a citação a acorda e gasta uma execução dela. Não cite ninguém para agradecer ou concordar, e nunca cite a si mesma.\n")
	fmt.Fprintf(&sb, "- As citações entre personas têm limite (até %d saltos a partir da mensagem humana); depois disso só um humano chama de novo.\n", room.MaxHops)
	sb.WriteString("- Não peça confirmação ao humano para o que é reversível; faça e diga o que fez. Ações irreversíveis (merge, deploy, gasto, contratação, apagar dados) viram uma task com `hanko_task_create` e um pedido de decisão com `hanko_gate_request` nela.\n")
	sb.WriteString("- Esta sala não tem task: as tools MCP `hanko_*` que dependem da task da execução não funcionam sem informar a task.\n")
	sb.WriteString(artifactProtocol)
	system = strings.TrimSpace(sb.String())

	var pb strings.Builder
	fmt.Fprintf(&pb, "# Sala: %s\n\n", cc.Conversation.Title)
	fmt.Fprintf(&pb, "- Sala: %s\n- Execução: %s (gatilho: %s; salto %d)\n- Modo: %s\n\n", cc.Conversation.ID, rc.Run.ID, rc.Run.Trigger, room.Hop, roomModeLabel(room.Mode))

	if cc.ResumeSession != "" {
		pb.WriteString("## Histórico\n\n(Sessão anterior retomada: você já conhece o que foi dito antes.)\n\n")
	} else {
		pb.WriteString("## Histórico recente\n\n")
		writeRoomHistory(&pb, rc, cc.History)
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

	pb.WriteString(attachmentsSection(rc))
	pb.WriteString(artifactsSection(rc))
	pb.WriteString("## Mensagens novas para você\n\n")
	if len(cc.New) == 0 {
		pb.WriteString("(nenhuma mensagem nova)\n\n")
	}
	for _, m := range cc.New {
		fmt.Fprintf(&pb, "**%s** · %s\n\n%s\n\n", roomAuthor(rc, m), m.CreatedAt.UTC().Format("2006-01-02 15:04 UTC"), clip(strings.TrimSpace(m.Body), maxChatMessageBytes))
	}
	pb.WriteString("---\nResponda agora ao que lhe cabe nas mensagens novas.\n")
	return system, pb.String()
}

func roomModeLabel(m domain.RoomMode) string {
	if m == domain.RoomAll {
		return "todas as personas respondem a cada mensagem humana"
	}
	return "responde quem é citado com @slug; sem citação, a moderadora"
}

// writeRoomHistory é o writeChatHistory da sala: as mensagens mais novas que cabem em MaxChatHistoryBytes,
// com o nome de quem falou.
func writeRoomHistory(pb *strings.Builder, rc service.RunContext, hist []domain.ConversationMessage) {
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
		fmt.Fprintf(pb, "**%s** · %s\n\n%s\n\n", roomAuthor(rc, m), m.CreatedAt.UTC().Format("2006-01-02 15:04 UTC"), clip(strings.TrimSpace(m.Body), maxChatHistoryMsg))
	}
}

// roomAuthor é o rótulo de quem escreveu uma mensagem da sala.
func roomAuthor(rc service.RunContext, m domain.ConversationMessage) string {
	switch m.Kind {
	case domain.MessageHuman:
		return "Humano"
	case domain.MessageSystem:
		return "Sistema"
	}
	if m.PersonaID != nil && *m.PersonaID == rc.Persona.ID {
		return rc.Persona.Name + " (você)"
	}
	name := m.PersonaName
	if name == "" && m.PersonaID != nil && rc.Chat.Room != nil {
		if x, ok := (service.RoomView{Participants: rc.Chat.Room.Participants}).Participant(*m.PersonaID); ok {
			name = x.Name
		}
	}
	if name == "" {
		name = "Persona"
	}
	return name
}
