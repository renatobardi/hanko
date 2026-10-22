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
	if pi := strings.TrimSpace(rc.ProjectInstructions); pi != "" {
		name := t.ProjectName
		if name == "" {
			name = "do projeto"
		}
		fmt.Fprintf(&sb, "## Instruções do projeto %s\n\n%s\n\n", name, pi)
	}
	title := p.Name
	if p.Title != "" {
		title += " (" + p.Title + ")"
	}
	sb.WriteString("## Protocolo Hanko\n\n")
	fmt.Fprintf(&sb, "Você é a persona %s da empresa %s.\n", title, c.Name)
	fmt.Fprintf(&sb, "- Trabalhe na task %s e somente nela.\n", t.Key)
	if t.PersonaID != nil && *t.PersonaID != p.ID && rc.Run.Trigger == domain.TriggerComment {
		sb.WriteString("- Você foi citada com @ num comentário desta task, que é de outra persona: responda só ao que foi pedido a você, no resumo final, e não mude o status, a prioridade nem a atribuição da task.\n")
	}
	sb.WriteString("- Use as tools MCP `hanko_*` (por exemplo `hanko_task_get`, `hanko_task_comment`, `hanko_task_update`) para ler a task, comentar e atualizar status e prioridade.\n")
	sb.WriteString("- Registre decisões e bloqueios como comentário na task; mude o status quando o trabalho avançar (`in_review` quando pronto para revisão, `blocked` quando depender de alguém).\n")
	sb.WriteString("- Para dividir o trabalho, crie subtasks com `hanko_task_create` (o pai padrão é esta task) e ordene-as com `depends_on`; uma task com dependência aberta só é despachada quando todas fecharem. Deixe esta task em `in_progress` e encerre: você será acordada quando a última subtask fechar. `hanko_task_dependency` ajusta dependências depois.\n")
	sb.WriteString("- Entregue arquivos (relatório, plano, dados, imagem) como artefato com `hanko_artifact_put` em vez de colar texto longo em comentário; o mesmo nome na mesma task cria a versão seguinte.\n")
	sb.WriteString("- Se o seu trabalho mede uma das metas listadas, registre o valor atual (medido de fato) com `hanko_goal_update`; `hanko_goal_list` mostra todas as metas.\n")
	sb.WriteString("- Se perceber que as suas instruções permanentes deveriam mudar, proponha com `hanko_persona_instruction_propose` (texto novo completo e motivo): um humano decide e você não fica bloqueada.\n")
	sb.WriteString("- Se precisar de um servidor MCP remoto que exige autorização (OAuth) e ele ainda não está conectado, peça com `hanko_connection_request` e encerre a execução: um humano autoriza e você é acordada quando a task retomar.\n")
	sb.WriteString("- Antes de uma ação irreversível (merge, deploy, gasto, contratação, apagar dados), peça a decisão de um humano com `hanko_gate_request` e encerre a execução; você será acordada com a decisão e a nota. Consulte `hanko_gate_status` se precisar.\n")
	sb.WriteString("- Para uma dúvida objetiva que só um humano resolve, use `hanko_gate_request` com `kind=question` e `questions` (campos single, multi ou text): você recebe as respostas estruturadas ao ser acordada. Se um gate voltar com mudanças pedidas, ajuste e reenvie no mesmo gate com `hanko_gate_resubmit`. Para propor contratar uma persona use `hanko_hire_request`.\n")
	if rc.Worktree != nil {
		fmt.Fprintf(&sb, "- Seu diretório de trabalho é um worktree Git exclusivo desta task, na branch `%s`. Faça commits nessa branch (mensagens claras, uma mudança lógica por commit); não troque de branch, não faça push e não mexa fora do diretório. O diff da branch contra a base aparece para os humanos na aba Git da task.\n", rc.Worktree.Branch)
	}
	sb.WriteString("- Termine com um resumo objetivo do que fez e do próximo passo. Esse resumo é publicado automaticamente como comentário na task; não o repita via tool.\n")
	system = strings.TrimSpace(sb.String())

	var pb strings.Builder
	fmt.Fprintf(&pb, "# Task %s: %s\n\n", t.Key, t.Title)
	fmt.Fprintf(&pb, "- Chave: %s\n- Status: %s\n- Prioridade: %s\n", t.Key, t.Status, t.Priority)
	if t.ProjectName != "" {
		fmt.Fprintf(&pb, "- Projeto: %s\n", t.ProjectName)
	}
	fmt.Fprintf(&pb, "- Execução: %s (gatilho: %s)\n", rc.Run.ID, rc.Run.Trigger)
	if rc.SessionReset != nil {
		fmt.Fprintf(&pb, "- Sessão nova: %s pediu para recomeçar; não há conversa anterior com o harness, só este briefing e a memória do projeto.", resetAuthor(rc))
		if r := strings.TrimSpace(rc.SessionReset.Reason); r != "" {
			fmt.Fprintf(&pb, " Motivo: %s.", clip(r, 300))
		}
		pb.WriteString("\n")
	}
	pb.WriteString("\n## Descrição\n\n")
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

	pb.WriteString(attachmentsSection(rc))
	pb.WriteString(coordinationSection(rc))
	pb.WriteString(planSection(rc))
	pb.WriteString(stageSection(rc))

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
	pb.WriteString(continuationSection(rc.Continuation, rc.ResumeSession != ""))
	pb.WriteString("---\nExecute o trabalho desta task agora.\n")
	return system, pb.String()
}

// attachmentsSection lista os anexos da task (ou da conversa) no briefing (ADR-0052). O conteúdo não vai no prompt: a
// persona lê o que precisar com `hanko_attachment_get`. Vazio quando não há anexo.
func attachmentsSection(rc service.RunContext) string {
	if len(rc.Attachments) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Anexos\n\n")
	b.WriteString("Arquivos anexados pela equipe. O conteúdo não está neste prompt: leia o que precisar com `hanko_attachment_get` (pelo id ou pelo nome).\n\n")
	for _, a := range rc.Attachments {
		fmt.Fprintf(&b, "- `%s` (%s, %s) · %s · %s · id %s", clip(a.Name, 120), a.Mime, fmtBytes(a.SizeBytes), authorName(rc, a.Author),
			a.CreatedAt.UTC().Format("2006-01-02 15:04 UTC"), a.ID)
		if a.CommentID != nil {
			for _, c := range rc.Comments {
				if c.ID == *a.CommentID {
					fmt.Fprintf(&b, " · no comentário de %s das %s", authorName(rc, c.Author), c.CreatedAt.UTC().Format("2006-01-02 15:04 UTC"))
					break
				}
			}
		}
		if a.New {
			b.WriteString(" · NOVO nesta mensagem")
		}
		b.WriteString("\n")
	}
	if len(rc.Attachments) >= service.MaxBriefingAttachments {
		fmt.Fprintf(&b, "\n(mostrando os %d anexos mais recentes)\n", service.MaxBriefingAttachments)
	}
	b.WriteString("\n")
	return b.String()
}

func fmtBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
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
	if len(g.Chain) > 1 {
		parts := make([]string, len(g.Chain))
		for i, c := range g.Chain {
			parts[i] = clip(c, 80)
		}
		line += ", cadeia: " + strings.Join(parts, " › ")
	}
	if g.Linked {
		line += " (esta task serve a ela)"
	}
	return line + "\n"
}

func fmtNum(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// resetAuthor diz quem pediu a nova sessão.
func resetAuthor(rc service.RunContext) string {
	if rc.SessionReset == nil || rc.SessionReset.By.Type == domain.ActorSystem {
		return "o sistema"
	}
	return authorName(rc, rc.SessionReset.By)
}

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
	if rc.Chat != nil {
		attrs = fmt.Sprintf("hanko.company_id=%s,hanko.persona_id=%s,hanko.run_id=%s,hanko.conversation_id=%s",
			rc.Company.ID, rc.Persona.ID, rc.Run.ID, rc.Chat.Conversation.ID)
	}
	if base = strings.TrimSpace(base); base != "" {
		attrs = strings.TrimRight(base, ",") + "," + attrs
	}
	env := map[string]string{
		"OTEL_RESOURCE_ATTRIBUTES":     attrs,
		"CLAUDE_CODE_ENABLE_TELEMETRY": "1",
		"HANKO_RUN_ID":                 rc.Run.ID.String(),
		"HANKO_TASK_KEY":               rc.Task.Key,
	}
	if rc.Chat != nil {
		env["HANKO_CONVERSATION_ID"] = rc.Chat.Conversation.ID.String()
	}
	if rc.Worktree != nil { // os commits da persona saem com o nome dela (ADR-0054)
		email := rc.Persona.Slug + "@hanko.local"
		env["GIT_AUTHOR_NAME"], env["GIT_COMMITTER_NAME"] = rc.Persona.Name, rc.Persona.Name
		env["GIT_AUTHOR_EMAIL"], env["GIT_COMMITTER_EMAIL"] = email, email
		env["HANKO_WORKTREE_BRANCH"] = rc.Worktree.Branch
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

// SkillIndex é o índice de skills que entra no system prompt dos harnesses sem carga nativa de skills
// (codex, acp): nome e descrição de cada uma, com o corpo sob demanda pela tool MCP hanko_skill_get.
func SkillIndex(skills []domain.Skill) string {
	var sb strings.Builder
	sb.WriteString("## Skills disponíveis\n\n")
	sb.WriteString("Habilidades reutilizáveis habilitadas para você. Quando a descrição de uma bater com o trabalho, ")
	sb.WriteString("carregue as instruções completas com a tool MCP `hanko_skill_get` (argumento `slug`) antes de agir e siga-as.\n\n")
	for _, s := range skills {
		desc := strings.Join(strings.Fields(s.Description), " ")
		fmt.Fprintf(&sb, "- `%s`: %s\n", s.Slug, desc)
	}
	return strings.TrimRight(sb.String(), "\n")
}

// BuildMemoryPrompt builds the prompts of a memory curation run (trigger memory, ADR-0030): the persona's
// own instructions, the curation protocol and the workspace to curate. It is pure.
func BuildMemoryPrompt(rc service.RunContext, briefing string) (system, prompt string) {
	p, c, t := rc.Persona, rc.Company, rc.Task
	project := "default"
	if rc.Workspace != nil && rc.Workspace.Slug != "" {
		project = rc.Workspace.Slug
	}
	var sb strings.Builder
	if s := strings.TrimSpace(p.Instructions); s != "" {
		sb.WriteString(s)
		sb.WriteString("\n\n")
	}
	sb.WriteString("## Protocolo de curadoria de memória Hanko\n\n")
	fmt.Fprintf(&sb, "Você é a persona curadora %s da empresa %s e hoje cuida da memória do workspace **%s** (projeto `%s` no ai-memory).\n", p.Name, c.Name, project, project)
	sb.WriteString("- Objetivo: deduplicar, fundir páginas redundantes, marcar o que ficou obsoleto e resumir o que ficou longo, mantendo a memória enxuta e fiel.\n")
	sb.WriteString("- Use as tools do servidor MCP `ai-memory` (liste as tools disponíveis e leia as páginas antes de mudar qualquer coisa). Você só alcança a memória deste workspace.\n")
	sb.WriteString("- NUNCA apague nem reescreva fatos com decisão humana: as páginas em `hanko/decisions/` e qualquer página que registre uma decisão de gate são preservadas literalmente, inclusive dentro de fusões e resumos.\n")
	sb.WriteString("- Escritas que removem, fundem ou reescrevem conteúdo NÃO são aplicadas na hora: o Hanko as registra como proposta e um humano decide. Quando a tool responder que a proposta foi registrada, siga em frente; não repita a chamada nem tente contornar por outra tool.\n")
	sb.WriteString("- Escritas novas e aditivas valem direto. Registre o relatório desta rodada numa página nova em `hanko/curadoria/` (por exemplo `hanko/curadoria/AAAA-MM-DD-relatorio.md`).\n")
	sb.WriteString("- Não consuma handoff nem encerre sessões de memória.\n")
	sb.WriteString("- Prefira poucas propostas bem fundamentadas, cada uma sobre uma página ou um grupo coeso de páginas, em vez de muitas mudanças pequenas.\n")
	sb.WriteString("- Termine com um resumo objetivo: o que examinou, o que propôs (quantas fusões, remoções e reescritas), o que deixou como está e por quê.\n")
	system = strings.TrimSpace(sb.String())

	var pb strings.Builder
	fmt.Fprintf(&pb, "# Curadoria de memória: %s\n\n", t.Title)
	fmt.Fprintf(&pb, "- Workspace: %s\n- Execução: %s (gatilho: %s)\n\n", project, rc.Run.ID, rc.Run.Trigger)
	if b := strings.TrimSpace(briefing); b != "" {
		pb.WriteString("## Briefing da memória\n\n" + b + "\n\n")
	}
	pb.WriteString("---\nExecute a curadoria agora.\n")
	return system, pb.String()
}

// planSection é o bloco de plan-review do briefing (ADR-0041): na fase de planejamento diz que este run só planeja; na de
// execução traz o plano aprovado. Vazio quando a política de plano não vale para a task.
func planSection(rc service.RunContext) string {
	pc := rc.Plan
	if pc == nil {
		return ""
	}
	var b strings.Builder
	switch pc.Phase {
	case domain.PlanPhasePlan:
		b.WriteString("## Fase de planejamento (plan-review)\n\n")
		b.WriteString("Esta task só executa depois que um humano aprovar o seu plano. **Neste run você apenas planeja**: investigue (leia, busque), " +
			"mas não edite arquivos, não rode comandos que alterem o ambiente e não use tools de escrita do Hanko (elas são recusadas).\n\n")
		b.WriteString("1. Escreva o plano (passos, riscos, como validar, o que fica de fora) e publique-o com `hanko_artifact_put` (por exemplo `plano.md`).\n")
		b.WriteString("2. Peça a aprovação com `hanko_gate_request` `kind=plan` e `artifact=plano.md` (ou o texto em `body`), e encerre.\n")
		b.WriteString("3. Se voltar com mudanças pedidas, leia a nota na thread, publique a nova versão do artefato e reenvie com `hanko_gate_resubmit`.\n\n")
	case domain.PlanPhaseExecute:
		fmt.Fprintf(&b, "## Plano aprovado (revisão %d)\n\n", pc.Revision)
		b.WriteString("Um humano aprovou o plano abaixo. Execute-o; se perceber que ele não se sustenta, comente na task e peça um gate em vez de mudar o rumo sozinha.\n\n")
		b.WriteString(clip(strings.TrimSpace(pc.ApprovedPlan), 20<<10))
		b.WriteString("\n\n")
	}
	return b.String()
}

// stageSection é o bloco do pipeline do projeto (ADR-0048): a etapa em que a task está, o que se espera dela, como
// concluí-la e os aprendizados (decisões de gate anteriores nesta etapa). Vazio fora de pipeline.
func stageSection(rc service.RunContext) string {
	sc := rc.Stage
	if sc == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## Etapa do pipeline: %s (%d de %d)\n\n", sc.Name, sc.Position, sc.Total)
	fmt.Fprintf(&b, "Esta task atravessa o pipeline do projeto %s e está na etapa **%s**.", sc.ProjectName, sc.Name)
	if sc.NextName != "" {
		fmt.Fprintf(&b, " Depois dela vem **%s**.", sc.NextName)
	} else {
		b.WriteString(" É a última etapa: concluí-la encerra a task.")
	}
	b.WriteString("\n\n")
	if in := strings.TrimSpace(sc.Instructions); in != "" {
		b.WriteString("### O que se espera desta etapa\n\n" + clip(in, 8<<10) + "\n\n")
	}
	b.WriteString("Quando o trabalho **desta etapa** terminar, chame `hanko_stage_complete` (com uma nota curta do que entregou) e encerre a execução: ")
	switch sc.ExitGate {
	case domain.StageGateApproval:
		b.WriteString("um humano aprova antes de a task avançar. ")
	case domain.StageGateReview:
		b.WriteString("a entrega passa por revisão (e por um gate humano) antes de a task avançar. ")
	default:
		b.WriteString("a task avança sozinha para a próxima etapa e a persona dela é acordada. ")
	}
	b.WriteString("Não dê a task como concluída (`done`) por conta própria: isso é a conclusão da etapa, não da task.\n\n")
	if l := strings.TrimSpace(sc.Learnings); l != "" {
		b.WriteString("### Aprendizados desta etapa\n\nDecisões anteriores de humanos nesta etapa; use-as para acertar de primeira.\n\n" + l + "\n")
	}
	return b.String()
}
