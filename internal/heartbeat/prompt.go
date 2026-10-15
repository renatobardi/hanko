package heartbeat

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

const (
	// MaxThreadComments is how many of the latest comments go into the prompt.
	MaxThreadComments = 30
	maxCommentBytes   = 8 << 10
)

// BuildPrompt builds the appended system prompt (persona instructions + Hanko protocol)
// and the user prompt (task header, thread, project memory). It is pure.
func BuildPrompt(rc service.RunContext, briefing, handoff string) (system, prompt string) {
	p, c, t := rc.Persona, rc.Company, rc.Task

	var sb strings.Builder
	if s := strings.TrimSpace(p.Instructions); s != "" {
		sb.WriteString(s)
		sb.WriteString("\n\n")
	}
	title := p.Name
	if p.Title != "" {
		title += " (" + p.Title + ")"
	}
	sb.WriteString("## Protocolo Hanko\n\n")
	fmt.Fprintf(&sb, "Você é a persona %s da empresa %s.\n", title, c.Name)
	fmt.Fprintf(&sb, "- Trabalhe na task %s e somente nela.\n", t.Key)
	sb.WriteString("- Use as tools MCP `hanko_*` (por exemplo `hanko_task_get`, `hanko_task_comment`, `hanko_task_update`) para ler a task, comentar e atualizar status e prioridade.\n")
	sb.WriteString("- Registre decisões e bloqueios como comentário na task; mude o status quando o trabalho avançar (`in_review` quando pronto para revisão, `blocked` quando depender de alguém).\n")
	sb.WriteString("- Entregue arquivos (relatório, plano, dados, imagem) como artefato com `hanko_artifact_put` em vez de colar texto longo em comentário; o mesmo nome na mesma task cria a versão seguinte.\n")
	sb.WriteString("- Se o seu trabalho mede uma das metas listadas, registre o valor atual (medido de fato) com `hanko_goal_update`; `hanko_goal_list` mostra todas as metas.\n")
	sb.WriteString("- Antes de uma ação irreversível (merge, deploy, gasto, contratação, apagar dados), peça a decisão de um humano com `hanko_gate_request` e encerre a execução; você será acordada com a decisão e a nota. Consulte `hanko_gate_status` se precisar.\n")
	sb.WriteString("- Termine com um resumo objetivo do que fez e do próximo passo. Esse resumo é publicado automaticamente como comentário na task; não o repita via tool.\n")
	system = strings.TrimSpace(sb.String())

	var pb strings.Builder
	fmt.Fprintf(&pb, "# Task %s: %s\n\n", t.Key, t.Title)
	fmt.Fprintf(&pb, "- Chave: %s\n- Status: %s\n- Prioridade: %s\n", t.Key, t.Status, t.Priority)
	if t.ProjectName != "" {
		fmt.Fprintf(&pb, "- Projeto: %s\n", t.ProjectName)
	}
	fmt.Fprintf(&pb, "- Execução: %s (gatilho: %s)\n\n", rc.Run.ID, rc.Run.Trigger)
	pb.WriteString("## Descrição\n\n")
	if d := strings.TrimSpace(t.Description); d != "" {
		pb.WriteString(d)
	} else {
		pb.WriteString("(sem descrição)")
	}
	pb.WriteString("\n\n")

	pb.WriteString("## Thread\n\n")
	cs := rc.Comments
	if len(cs) == 0 {
		pb.WriteString("(sem comentários)\n\n")
	} else {
		if len(cs) > MaxThreadComments {
			fmt.Fprintf(&pb, "(%d comentários anteriores omitidos)\n\n", len(cs)-MaxThreadComments)
			cs = cs[len(cs)-MaxThreadComments:]
		}
		for _, cm := range cs {
			fmt.Fprintf(&pb, "**%s** · %s\n\n%s\n\n", authorName(rc, cm.Author), cm.CreatedAt.UTC().Format("2006-01-02 15:04 UTC"), clip(strings.TrimSpace(cm.Body), maxCommentBytes))
		}
	}

	if len(rc.Goals) > 0 {
		pb.WriteString("## Metas ativas\n\n")
		for _, g := range rc.Goals {
			pb.WriteString(goalLine(g))
		}
		pb.WriteString("\n")
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
	pb.WriteString("---\nExecute o trabalho desta task agora.\n")
	return system, pb.String()
}

// goalLine é uma meta em uma linha: progresso, valores e prazo; "(esta task serve a ela)" marca a meta da task.
func goalLine(g service.GoalBrief) string {
	unit := ""
	if g.Unit != "" {
		unit = " " + g.Unit
	}
	line := fmt.Sprintf("- `%s` %s: %.0f%% (%s de %s%s)", g.Slug, clip(g.Title, 120), g.Progress, fmtNum(g.Current), fmtNum(g.Target), unit)
	if g.Metric != "" {
		line += ", mede " + clip(g.Metric, 80)
	}
	if g.DueDate != nil {
		line += ", prazo " + g.DueDate.Format("2006-01-02")
	}
	if g.Linked {
		line += " (esta task serve a ela)"
	}
	return line + "\n"
}

func fmtNum(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func authorName(rc service.RunContext, a domain.Actor) string {
	switch a.Type {
	case domain.ActorHuman:
		return "Humano"
	case domain.ActorSystem:
		return "Sistema"
	case domain.ActorPersona:
		if a.ID != nil {
			if *a.ID == rc.Persona.ID {
				return rc.Persona.Name + " (você)"
			}
			return "Persona " + a.ID.String()[:8]
		}
	}
	return string(a.Type)
}

func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…[truncado]"
}

// buildEnv returns the environment overrides for the harness. base is the existing
// OTEL_RESOURCE_ATTRIBUTES value of the server process, to which Hanko's are appended.
func buildEnv(base, publicURL, token string, rc service.RunContext) map[string]string {
	attrs := fmt.Sprintf("hanko.company_id=%s,hanko.persona_id=%s,hanko.run_id=%s,hanko.task_id=%s",
		rc.Company.ID, rc.Persona.ID, rc.Run.ID, rc.Task.ID)
	if base = strings.TrimSpace(base); base != "" {
		attrs = strings.TrimRight(base, ",") + "," + attrs
	}
	env := map[string]string{
		"OTEL_RESOURCE_ATTRIBUTES":     attrs,
		"CLAUDE_CODE_ENABLE_TELEMETRY": "1",
		"HANKO_RUN_ID":                 rc.Run.ID.String(),
		"HANKO_TASK_KEY":               rc.Task.Key,
	}
	// No token means a credential-less (review) run: it does not even learn where Hanko is.
	if token != "" {
		env["HANKO_URL"] = publicURL
		env["HANKO_RUN_TOKEN"] = token
	}
	return env
}

// buildMCPConfig renders {"mcpServers":{...}} with the Hanko server first-class and the
// memory provider's entries merged in (Hanko wins on a name clash).
func buildMCPConfig(publicURL, token string, extra map[string]json.RawMessage) (json.RawMessage, error) {
	hanko, err := json.Marshal(map[string]any{
		"type":    "http",
		"url":     publicURL + "/mcp",
		"headers": map[string]string{"Authorization": "Bearer " + token},
	})
	if err != nil {
		return nil, fmt.Errorf("mcp config: %w", err)
	}
	servers := map[string]json.RawMessage{}
	for k, v := range extra {
		servers[k] = v
	}
	if token != "" {
		servers["hanko"] = hanko
	}
	out, err := json.Marshal(map[string]any{"mcpServers": servers})
	if err != nil {
		return nil, fmt.Errorf("mcp config: %w", err)
	}
	return out, nil
}

// BuildReviewPrompt builds the prompts of a review run: the reviewer's protocol (and, when the run
// belongs to another persona, that persona's instructions) and the artifact to judge. It is pure.
func BuildReviewPrompt(rc service.RunContext, briefing string) (system, prompt string) {
	rv, t := rc.Review, rc.Task
	var sb strings.Builder
	if rv.Independent {
		if s := strings.TrimSpace(rc.Persona.Instructions); s != "" {
			sb.WriteString(s)
			sb.WriteString("\n\n")
		}
	}
	sb.WriteString("## Protocolo de revisão Hanko\n\n")
	fmt.Fprintf(&sb, "Você é o revisor independente da entrega de %s na task %s da empresa %s. Não é a autora: avalie com ceticismo e sem presumir que o trabalho está certo.\n", rv.AuthorName, t.Key, rc.Company.Name)
	sb.WriteString("- A revisão é somente leitura: não edite arquivos, não mude status, não abra gates nem comente na task.\n")
	sb.WriteString("- Confira se a entrega cumpre a descrição da task. Se houver workspace, inspecione os arquivos para verificar as afirmações da autora.\n")
	sb.WriteString("- Sua resposta final DEVE começar com uma linha `VEREDITO: APROVADO` ou `VEREDITO: REPROVADO`, seguida da justificativa objetiva (o que está certo, o que falta ou está errado).\n")
	sb.WriteString("- A decisão final é de um humano; seu veredito é uma recomendação registrada no gate.\n")
	system = strings.TrimSpace(sb.String())

	var pb strings.Builder
	fmt.Fprintf(&pb, "# Revisão da task %s: %s\n\n", t.Key, t.Title)
	fmt.Fprintf(&pb, "- Execução de revisão: %s\n- sha256 do artefato: %s\n- Status pedido pela autora: %s\n\n", rc.Run.ID, rv.ArtifactSHA256, rv.RequestedStatus)
	pb.WriteString("## Artefato a revisar\n\n")
	pb.WriteString(rv.Artifact)
	if b := strings.TrimSpace(briefing); b != "" {
		pb.WriteString("\n## Memória do projeto\n\n" + b + "\n")
	}
	pb.WriteString("\n---\nRevise agora e responda começando por `VEREDITO: APROVADO` ou `VEREDITO: REPROVADO`.\n")
	return system, pb.String()
}
