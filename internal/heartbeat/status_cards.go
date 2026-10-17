package heartbeat

import (
	"context"
	"fmt"
	"strings"

	"github.com/riverqueue/river"

	"github.com/renatobardi/hanko/internal/service"
)

// Cards de status (ADR-0032): o tick periódico que avalia os cards devidos e o prompt da run de geração.

// StatusCardTickArgs é a varredura periódica dos cards de status (job "status_card_tick").
type StatusCardTickArgs struct{}

func (StatusCardTickArgs) Kind() string { return "status_card_tick" }

func (StatusCardTickArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 1} }

// statusCardBackend é o que o tick pede ao service; opcional para que os fakes de teste que não o usam
// continuem valendo.
type statusCardBackend interface {
	EvaluateDueStatusCards(ctx context.Context) (int, error)
}

type statusCardTickWorker struct {
	river.WorkerDefaults[StatusCardTickArgs]
	h *Heartbeat
}

func (w *statusCardTickWorker) Work(ctx context.Context, _ *river.Job[StatusCardTickArgs]) error {
	return w.h.statusCardTick(ctx)
}

// statusCardTick avalia os cards cuja próxima verificação chegou. A avaliação é idempotente; um card que
// falhar fica para o próximo tick.
func (h *Heartbeat) statusCardTick(ctx context.Context) error {
	if h.svc == nil {
		return errNotAttached
	}
	sb, ok := h.svc.(statusCardBackend)
	if !ok {
		return nil
	}
	n, err := sb.EvaluateDueStatusCards(ctx)
	if err != nil {
		return fmt.Errorf("status_card_tick: %w", err)
	}
	if n > 0 {
		h.log.Debug("status_card_tick", "evaluated", n)
	}
	return nil
}

// BuildStatusCardPrompt monta os prompts de uma run de card de status (gatilho status_card, ADR-0032): as
// instruções da persona, o protocolo de montagem e atualização e o pacote de contexto que o service gravou na
// descrição da task. É pura.
func BuildStatusCardPrompt(rc service.RunContext, briefing string) (system, prompt string) {
	p, c, t := rc.Persona, rc.Company, rc.Task
	var sb strings.Builder
	if s := strings.TrimSpace(p.Instructions); s != "" {
		sb.WriteString(s)
		sb.WriteString("\n\n")
	}
	sb.WriteString("## Protocolo de card de status Hanko\n\n")
	fmt.Fprintf(&sb, "Você é a persona resumidora %s da empresa %s. Um card de status é um resumo vivo de um conjunto de tasks que interessa a alguém do time.\n", p.Name, c.Name)
	sb.WriteString("- A descrição da task traz o card (id), a etapa e o contexto. Trabalhe só neste card e não crie tasks.\n")
	sb.WriteString("- **Etapa montagem**: transforme o interesse em 1 a 10 filtros de task e chame `hanko_status_card_set_query` com `card`, `queries` " +
		"(cada filtro: `status`, `priority`, `persona`, `project`, `goal`, `text`, `open`, `label`), `title` (curto, se o título não estiver fixado) e `change_summary`. " +
		"Dentro de um filtro todos os campos valem juntos (E); uma task entra se casar com qualquer filtro (OU). Use `hanko_persona_list`, `hanko_project_list` e `hanko_goal_list` para achar os slugs. " +
		"Prefira filtros precisos a filtros largos: o card observa no máximo 300 tasks.\n")
	sb.WriteString("- **Etapa atualização**: escreva o resumo em markdown (até 64 KiB) para quem acompanha: o que importa primeiro (bloqueios, riscos, decisões pendentes), " +
		"depois o andamento, citando as tasks pela chave (HNK-12). No tipo incremental, parta do resumo anterior e incorpore as mudanças; no tipo do zero (full), reescreva a partir das tasks. " +
		"Grave com `hanko_status_card_set_summary` (`card`, `markdown`, `mentioned` com as chaves citadas, `change_summary` com o que mudou, `title` opcional).\n")
	sb.WriteString("- Leia o detalhe de uma task com `hanko_task_get` só quando o contexto não bastar. Não altere tasks, não comente nelas e não peça gates.\n")
	sb.WriteString("- Sem a chamada da tool a etapa conta como falha. Depois de gravar, termine com uma frase curta do que fez.\n")
	system = strings.TrimSpace(sb.String())

	var pb strings.Builder
	fmt.Fprintf(&pb, "# %s\n\n", t.Title)
	fmt.Fprintf(&pb, "- Task: %s\n- Execução: %s (gatilho: %s)\n- Aberta em: %s\n\n", t.Key, rc.Run.ID, rc.Run.Trigger, rc.Run.CreatedAt.UTC().Format("2006-01-02 15:04 UTC"))
	if d := strings.TrimSpace(t.Description); d != "" {
		pb.WriteString(d)
		pb.WriteString("\n\n")
	}
	if b := strings.TrimSpace(briefing); b != "" {
		pb.WriteString("## Memória do projeto (para contexto)\n\n" + clip(b, 8<<10) + "\n\n")
	}
	pb.WriteString("---\nExecute a etapa agora.\n")
	return system, pb.String()
}
