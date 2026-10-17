package heartbeat

import (
	"fmt"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// continuationSection is the briefing of a run that continues another one (ADR-0039): why it exists, whether the
// harness session was resumed, the last steps of the previous run and, for an uncertain outcome, the instruction to
// verify before repeating any effect. It is pure; "" when the run is not a continuation.
func continuationSection(c *service.ContinuationContext, resumed bool) string {
	if c == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## Continuação de uma execução anterior\n\n")
	prev := c.OfRunName
	if prev == "" {
		prev = c.OfRunID.String()
	}
	switch c.Reason {
	case domain.ContinueSteer:
		fmt.Fprintf(&sb, "Esta execução continua a `%s`, que o operador interrompeu para mudar o rumo. As novas instruções dele estão no fim da thread (ou nas mensagens novas): siga-as no lugar do plano anterior onde divergirem.\n", prev)
	case domain.ContinueRetry:
		fmt.Fprintf(&sb, "Esta execução refaz o trabalho da `%s`, que terminou como %s", prev, c.PrevStatus)
		if e := strings.TrimSpace(c.PrevError); e != "" {
			fmt.Fprintf(&sb, " (%s)", clip(e, 300))
		}
		sb.WriteString(". O operador pediu para tentar de novo, do zero: não presuma que algo do trabalho anterior está certo.\n")
	case domain.ContinueManual:
		fmt.Fprintf(&sb, "Esta execução continua a `%s`, que terminou como %s. O operador decidiu continuar.\n", prev, c.PrevStatus)
	default:
		fmt.Fprintf(&sb, "Esta execução continua a `%s`, que foi interrompida antes de terminar (continuação %d de %d).\n", prev, c.Attempt, c.MaxAttempts)
	}
	if n := strings.TrimSpace(c.DecisionNote); n != "" {
		fmt.Fprintf(&sb, "\nNota do operador: %s\n", clip(n, 1000))
	}
	if resumed {
		sb.WriteString("\n- A sessão do harness foi retomada: você tem o contexto da execução anterior.\n")
	} else if c.Reason != domain.ContinueRetry {
		sb.WriteString("\n- A sessão do harness não pôde ser retomada: recupere o contexto pela thread, pelos arquivos do workspace e pelos passos abaixo.\n")
	}
	sb.WriteString("- Antes de repetir qualquer passo, confira no workspace e na task o que já foi feito; não refaça trabalho concluído.\n")
	if u := c.Diagnosis; u != nil && u.Uncertain != nil {
		fmt.Fprintf(&sb, "\n**Desfecho incerto.** Quando a execução anterior parou, a tool `%s` estava em andamento (%s) e pode ter produzido efeito externo (arquivo escrito, comando executado, mudança no Hanko ou em outro sistema). ",
			u.Uncertain.Name, clip(u.Uncertain.Input, 200))
		sb.WriteString("Verifique o estado real antes de repetir essa ação: só a repita se o efeito comprovadamente não aconteceu ou se for idempotente.\n")
	}
	if len(c.Tail) > 0 {
		sb.WriteString("\nÚltimos passos da execução anterior:\n")
		for _, l := range c.Tail {
			fmt.Fprintf(&sb, "- %s\n", l)
		}
	}
	sb.WriteString("\n")
	return sb.String()
}
