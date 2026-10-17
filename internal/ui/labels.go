package ui

import (
	"context"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

func taskStatusLabel(ctx context.Context, s domain.TaskStatus) string {
	switch s {
	case domain.TaskBacklog:
		return i18n.T(ctx, "Backlog")
	case domain.TaskTodo:
		return i18n.T(ctx, "A fazer")
	case domain.TaskInProgress:
		return i18n.T(ctx, "Em andamento")
	case domain.TaskInReview:
		return i18n.T(ctx, "Em revisão")
	case domain.TaskBlocked:
		return i18n.T(ctx, "Bloqueada")
	case domain.TaskDone:
		return i18n.T(ctx, "Concluída")
	case domain.TaskCancelled:
		return i18n.T(ctx, "Cancelada")
	}
	return string(s)
}

func taskBadge(ctx context.Context, t service.TaskView) views.Badge {
	// Coordenação (ADR-0040): a pausa e a espera por dependência vencem o status das tasks abertas.
	if t.Status.Open() {
		if t.Hold != nil {
			return views.Badge{Label: i18n.T(ctx, "Pausada"), Class: "warn"}
		}
		if t.Blocked && t.Status != domain.TaskBlocked {
			return views.Badge{Label: i18n.T(ctx, "Aguarda dependências"), Class: "warn"}
		}
	}
	switch t.Status {
	case domain.TaskInProgress:
		if t.ActiveRunID != nil {
			return views.Badge{Label: i18n.T(ctx, "Executando"), Class: "run"}
		}
		return views.Badge{Label: i18n.T(ctx, "Em andamento"), Class: "outline"}
	case domain.TaskInReview:
		return views.Badge{Label: i18n.T(ctx, "Em revisão"), Class: "warn"}
	case domain.TaskBlocked:
		return views.Badge{Label: i18n.T(ctx, "Bloqueada"), Class: "danger"}
	case domain.TaskDone:
		return views.Badge{Label: i18n.T(ctx, "Concluída"), Class: "done"}
	case domain.TaskCancelled:
		return views.Badge{Label: i18n.T(ctx, "Cancelada"), Class: "soft"}
	}
	return views.Badge{Label: taskStatusLabel(ctx, t.Status), Class: "outline"}
}

func priorityLabel(ctx context.Context, p domain.Priority) string {
	switch p {
	case domain.PriorityCritical:
		return i18n.T(ctx, "Crítica")
	case domain.PriorityHigh:
		return i18n.T(ctx, "Alta")
	case domain.PriorityMedium:
		return i18n.T(ctx, "Média")
	case domain.PriorityLow:
		return i18n.T(ctx, "Baixa")
	}
	return string(p)
}

func runBadge(ctx context.Context, s domain.RunStatus) views.Badge {
	switch s {
	case domain.RunQueued:
		return views.Badge{Label: i18n.T(ctx, "Na fila"), Class: "outline"}
	case domain.RunRunning:
		return views.Badge{Label: i18n.T(ctx, "Executando"), Class: "run", Live: true}
	case domain.RunDone:
		return views.Badge{Label: i18n.T(ctx, "Concluída"), Class: "done"}
	case domain.RunInterrupted:
		return views.Badge{Label: i18n.T(ctx, "Interrompida"), Class: "warn"}
	case domain.RunFailed:
		return views.Badge{Label: i18n.T(ctx, "Falhou"), Class: "danger"}
	case domain.RunCancelled:
		return views.Badge{Label: i18n.T(ctx, "Cancelada"), Class: "soft"}
	case domain.RunExpired:
		return views.Badge{Label: i18n.T(ctx, "Expirou"), Class: "danger"}
	}
	return views.Badge{Label: string(s), Class: "outline"}
}

func runDot(s domain.RunStatus) string {
	switch s {
	case domain.RunQueued, domain.RunRunning:
		return "live"
	case domain.RunFailed, domain.RunExpired:
		return "bad"
	case domain.RunInterrupted:
		return "warn"
	}
	return ""
}

func runLabel(ctx context.Context, s domain.RunStatus) string {
	switch s {
	case domain.RunQueued:
		return i18n.T(ctx, "na fila")
	case domain.RunRunning:
		return i18n.T(ctx, "executando")
	case domain.RunDone:
		return i18n.T(ctx, "concluída")
	case domain.RunInterrupted:
		return i18n.T(ctx, "interrompida")
	case domain.RunFailed:
		return i18n.T(ctx, "falhou")
	case domain.RunCancelled:
		return i18n.T(ctx, "cancelada")
	case domain.RunExpired:
		return i18n.T(ctx, "expirou")
	}
	return ""
}

func triggerLabel(t domain.RunTrigger) string { return triggerLabelCtx(context.Background(), t) }

func triggerLabelCtx(ctx context.Context, t domain.RunTrigger) string {
	switch t {
	case domain.TriggerAssign:
		return i18n.T(ctx, "por atribuição")
	case domain.TriggerComment:
		return i18n.T(ctx, "por comentário")
	case domain.TriggerManual:
		return i18n.T(ctx, "manual")
	case domain.TriggerHeartbeat:
		return i18n.T(ctx, "verificação periódica")
	case domain.TriggerGate:
		return i18n.T(ctx, "decisão de gate")
	case domain.TriggerChat:
		return i18n.T(ctx, "conversa (chat)")
	}
	return string(t)
}

func personaBadge(ctx context.Context, s domain.PersonaStatus) views.Badge {
	switch s {
	case domain.PersonaRunning:
		return views.Badge{Label: i18n.T(ctx, "Executando"), Class: "run", Live: true}
	case domain.PersonaActive:
		return views.Badge{Label: i18n.T(ctx, "Ativa"), Class: "outline"}
	case domain.PersonaIdle:
		return views.Badge{Label: i18n.T(ctx, "Ociosa"), Class: "outline"}
	case domain.PersonaPaused:
		return views.Badge{Label: i18n.T(ctx, "Pausada"), Class: "danger"}
	case domain.PersonaError:
		return views.Badge{Label: i18n.T(ctx, "Erro"), Class: "danger"}
	case domain.PersonaAwaitingGate:
		return views.Badge{Label: i18n.T(ctx, "Aguarda gate"), Class: "warn"}
	case domain.PersonaOff:
		return views.Badge{Label: i18n.T(ctx, "Desligada"), Class: "soft"}
	}
	return views.Badge{Label: string(s), Class: "outline"}
}

func heartbeatLabel(sec int) string { return heartbeatLabelCtx(context.Background(), sec) }

func heartbeatLabelCtx(ctx context.Context, sec int) string {
	switch {
	case sec <= 0:
		return i18n.T(ctx, "manual")
	case sec%3600 == 0:
		return i18n.Tf(ctx, "a cada %d h", sec/3600)
	case sec%60 == 0:
		return i18n.Tf(ctx, "a cada %d min", sec/60)
	}
	return i18n.Tf(ctx, "a cada %d s", sec)
}

func itoa(n int) string { return fmtInt(n) }

func logLabel(k domain.RunEventKind) string {
	switch k {
	case domain.EventToolUse:
		return "tool"
	case domain.EventToolResult:
		return "tool.result"
	case domain.EventResult:
		return "result"
	}
	return string(k)
}
