package ui

import (
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

func taskStatusLabel(s domain.TaskStatus) string {
	switch s {
	case domain.TaskBacklog:
		return "Backlog"
	case domain.TaskTodo:
		return "A fazer"
	case domain.TaskInProgress:
		return "Em andamento"
	case domain.TaskInReview:
		return "Em revisão"
	case domain.TaskBlocked:
		return "Bloqueada"
	case domain.TaskDone:
		return "Concluída"
	case domain.TaskCancelled:
		return "Cancelada"
	}
	return string(s)
}

func taskBadge(t service.TaskView) views.Badge {
	switch t.Status {
	case domain.TaskInProgress:
		if t.ActiveRunID != nil {
			return views.Badge{Label: "Executando", Class: "run"}
		}
		return views.Badge{Label: "Em andamento", Class: "outline"}
	case domain.TaskInReview:
		return views.Badge{Label: "Em revisão", Class: "warn"}
	case domain.TaskBlocked:
		return views.Badge{Label: "Bloqueada", Class: "danger"}
	case domain.TaskDone:
		return views.Badge{Label: "Concluída", Class: "done"}
	case domain.TaskCancelled:
		return views.Badge{Label: "Cancelada", Class: "soft"}
	}
	return views.Badge{Label: taskStatusLabel(t.Status), Class: "outline"}
}

func priorityLabel(p domain.Priority) string {
	switch p {
	case domain.PriorityCritical:
		return "Crítica"
	case domain.PriorityHigh:
		return "Alta"
	case domain.PriorityMedium:
		return "Média"
	case domain.PriorityLow:
		return "Baixa"
	}
	return string(p)
}

func runBadge(s domain.RunStatus) views.Badge {
	switch s {
	case domain.RunQueued:
		return views.Badge{Label: "Na fila", Class: "outline"}
	case domain.RunRunning:
		return views.Badge{Label: "Executando", Class: "run", Live: true}
	case domain.RunDone:
		return views.Badge{Label: "Concluída", Class: "done"}
	case domain.RunInterrupted:
		return views.Badge{Label: "Interrompida", Class: "warn"}
	case domain.RunFailed:
		return views.Badge{Label: "Falhou", Class: "danger"}
	case domain.RunCancelled:
		return views.Badge{Label: "Cancelada", Class: "soft"}
	case domain.RunExpired:
		return views.Badge{Label: "Expirou", Class: "danger"}
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

func runLabel(s domain.RunStatus) string {
	return map[domain.RunStatus]string{
		domain.RunQueued: "na fila", domain.RunRunning: "executando", domain.RunDone: "concluída",
		domain.RunInterrupted: "interrompida", domain.RunFailed: "falhou", domain.RunCancelled: "cancelada", domain.RunExpired: "expirou",
	}[s]
}

func triggerLabel(t domain.RunTrigger) string {
	switch t {
	case domain.TriggerAssign:
		return "por atribuição"
	case domain.TriggerComment:
		return "por comentário"
	case domain.TriggerManual:
		return "manual"
	case domain.TriggerHeartbeat:
		return "heartbeat"
	}
	return string(t)
}

func personaBadge(s domain.PersonaStatus) views.Badge {
	switch s {
	case domain.PersonaRunning:
		return views.Badge{Label: "Executando", Class: "run", Live: true}
	case domain.PersonaActive:
		return views.Badge{Label: "Ativa", Class: "outline"}
	case domain.PersonaIdle:
		return views.Badge{Label: "Ociosa", Class: "outline"}
	case domain.PersonaPaused:
		return views.Badge{Label: "Pausada", Class: "danger"}
	case domain.PersonaError:
		return views.Badge{Label: "Erro", Class: "danger"}
	case domain.PersonaAwaitingGate:
		return views.Badge{Label: "Aguarda gate", Class: "warn"}
	case domain.PersonaOff:
		return views.Badge{Label: "Desligada", Class: "soft"}
	}
	return views.Badge{Label: string(s), Class: "outline"}
}

func heartbeatLabel(sec int) string {
	switch {
	case sec <= 0:
		return "manual"
	case sec%3600 == 0:
		return "a cada " + itoa(sec/3600) + " h"
	case sec%60 == 0:
		return "a cada " + itoa(sec/60) + " min"
	}
	return "a cada " + itoa(sec) + " s"
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
