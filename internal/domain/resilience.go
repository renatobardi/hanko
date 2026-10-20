package domain

import (
	"encoding/json"
	"strings"
	"time"
)

// Resiliência de run (ADR-0039): continuação limitada de run interrompido, diagnóstico com decisão humana,
// steering e fila de follow-ups.

// MaxContinuations é quantas continuações automáticas uma cadeia de runs ganha (a original é a tentativa 0).
const MaxContinuations = 2

// ContinuationReason diz por que uma run é continuação de outra.
type ContinuationReason string

const (
	ContinueNone     ContinuationReason = ""
	ContinueRecovery ContinuationReason = "recovery" // a run anterior foi interrompida (queda do processo ou do servidor)
	ContinueSteer    ContinuationReason = "steer"    // o operador interrompeu a run para mudar o rumo (harness sem injeção)
	ContinueRetry    ContinuationReason = "retry"    // "Tentar de novo": recomeça do zero, sem sessão
	ContinueManual   ContinuationReason = "continue" // "Continuar": decisão humana, retoma a sessão
)

func (r ContinuationReason) Valid() bool {
	switch r {
	case ContinueNone, ContinueRecovery, ContinueSteer, ContinueRetry, ContinueManual:
		return true
	}
	return false
}

// ResumesSession diz se a continuação retoma a sessão do harness (retry recomeça do zero).
func (r ContinuationReason) ResumesSession() bool { return r != ContinueNone && r != ContinueRetry }

// RunDecision é a decisão do operador sobre uma run que parou.
type RunDecision string

const (
	DecisionNone     RunDecision = ""
	DecisionRetry    RunDecision = "retry"    // Tentar de novo
	DecisionContinue RunDecision = "continue" // Continuar
	DecisionClose    RunDecision = "close"    // Encerrar
)

func (d RunDecision) Valid() bool {
	return d == DecisionRetry || d == DecisionContinue || d == DecisionClose
}

// DiagnosisCause é a causa da parada de uma run.
type DiagnosisCause string

const (
	CauseProcessRestart DiagnosisCause = "process_restart" // o processo do servidor caiu e a run ficou órfã
	CauseServerShutdown DiagnosisCause = "server_shutdown" // o servidor foi encerrado com a run em andamento
	CauseAgentError     DiagnosisCause = "agent_error"     // o harness reportou erro ou saiu com falha
	CauseTimeout        DiagnosisCause = "timeout"         // estourou o tempo limite
	CauseCancelled      DiagnosisCause = "cancelled"       // cancelada pelo usuário
)

// DiagnosisAction é a próxima ação prevista.
type DiagnosisAction string

const (
	ActionAutoContinue  DiagnosisAction = "auto_continue"  // o sistema abre a continuação sozinho
	ActionHumanDecision DiagnosisAction = "human_decision" // espera Tentar de novo / Continuar / Encerrar
	ActionNone          DiagnosisAction = "none"           // nada a fazer (já decidida, ou encerrou)
	ActionContinued     DiagnosisAction = "continued"      // a continuação já foi aberta
)

// DiagnosisOwner é quem tem a próxima ação.
type DiagnosisOwner string

const (
	OwnerSystem DiagnosisOwner = "system"
	OwnerHuman  DiagnosisOwner = "human"
)

// UncertainTool é a tool de efeito externo que estava em andamento quando a run morreu: o desfecho dela é
// desconhecido (pode ter acontecido, pela metade ou não).
type UncertainTool struct {
	Name  string `json:"name"`
	Input string `json:"input,omitempty"` // resumo curto da chamada
	Seq   int64  `json:"seq"`             // seq do evento tool_use no log da run
}

// RunDiagnosis é o card de diagnóstico de uma run que parou: causa, tentativa, próxima ação e dono.
type RunDiagnosis struct {
	Cause       DiagnosisCause  `json:"cause"`
	CauseDetail string          `json:"cause_detail,omitempty"`
	Attempt     int             `json:"attempt"`      // continuação em que a run parou (0 = original)
	MaxAttempts int             `json:"max_attempts"` // MaxContinuations
	NextAction  DiagnosisAction `json:"next_action"`
	Owner       DiagnosisOwner  `json:"owner"`
	Exhausted   bool            `json:"exhausted,omitempty"` // as continuações automáticas acabaram
	Uncertain   *UncertainTool  `json:"uncertain,omitempty"` // desfecho incerto: exige decisão humana
}

// NeedsDecision diz se o card espera o operador.
func (d RunDiagnosis) NeedsDecision() bool { return d.NextAction == ActionHumanDecision }

// SteerMode é como um harness aceita uma mensagem do operador no meio da run.
type SteerMode string

const (
	SteerNone    SteerMode = "none"    // não aceita
	SteerInject  SteerMode = "inject"  // a mensagem entra na run viva
	SteerRestart SteerMode = "restart" // interrompe a run e continua com a mensagem
)

func (m SteerMode) Valid() bool { return m == SteerNone || m == SteerInject || m == SteerRestart }

// SteerModeFor é a capability de steering do harness pelo nome (claude, codex, acp, fake…). Os adapters declaram a
// mesma coisa em adapter.Steerer; um teste garante que coincidem. Harness desconhecido cai em restart, que
// funciona com qualquer um.
func SteerModeFor(agent string) SteerMode {
	switch strings.ToLower(strings.TrimSpace(agent)) {
	case "claude", "acp", "fake":
		return SteerInject
	}
	return SteerRestart
}

// ---- tools abertas e desfecho incerto ----

// readOnlyTools são as tools que não têm efeito fora do próprio agente; qualquer outra é tratada como efeito
// externo (lista conservadora).
var readOnlyTools = map[string]bool{
	"read": true, "glob": true, "grep": true, "ls": true, "notebookread": true, "webfetch": true, "websearch": true,
	"todowrite": true, "todoread": true, "toolsearch": true, "exitplanmode": true, "bashoutput": true,
	"read_file": true, "list_files": true, "search": true, "think": true, "fetch": true,
}

// ToolHasExternalEffect diz se uma tool pode ter mudado algo fora do agente (arquivo, processo, API, Hanko).
// kind é a categoria que o harness anuncia, quando anuncia (ACP: read|search|think|fetch são leitura).
func ToolHasExternalEffect(name, kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "read", "search", "think", "fetch":
		return false
	}
	n := strings.ToLower(strings.TrimSpace(name))
	if i := strings.IndexByte(n, ':'); i >= 0 { // "Read: /x" (título composto)
		n = strings.TrimSpace(n[:i])
	}
	if readOnlyTools[n] {
		return false
	}
	// tools do próprio Hanko (mcp__hanko__hanko_task_get, hanko_task_list…): só as de leitura
	if i := strings.LastIndex(n, "hanko_"); i >= 0 {
		rest := n[i:]
		for _, suffix := range []string{"_get", "_list", "_search", "_whoami", "_read"} {
			if strings.HasSuffix(rest, suffix) {
				return false
			}
		}
		if rest == "hanko_whoami" {
			return false
		}
	}
	return true
}

// OpenToolCall é uma chamada de tool sem resultado no log.
type OpenToolCall struct {
	Seq   int64
	ID    string
	Name  string
	Input string
	Kind  string
}

// OpenToolCalls reconstrói, do log em ordem de seq, as chamadas de tool que não receberam resultado. O
// resultado casa pela id da chamada (payload "id" no tool_use e "tool_use_id" no tool_result) e, quando um dos
// lados não traz id, pela ordem (o resultado mais antigo fecha a chamada aberta mais antiga).
func OpenToolCalls(events []RunEvent) []OpenToolCall {
	var open []OpenToolCall
	for _, e := range events {
		switch e.Kind {
		case EventToolUse:
			c := OpenToolCall{Seq: e.Seq, Name: toolName(e), Input: e.Text}
			var pl struct {
				ID    string `json:"id"`
				Name  string `json:"name"`
				Kind  string `json:"kind"`
				Title string `json:"title"`
			}
			if len(e.Payload) > 0 && json.Unmarshal(e.Payload, &pl) == nil {
				c.ID, c.Kind = pl.ID, pl.Kind
				if c.ID == "" {
					var alt struct {
						ToolCallID string `json:"tool_call_id"`
						ToolUseID  string `json:"tool_use_id"`
					}
					if json.Unmarshal(e.Payload, &alt) == nil {
						c.ID = firstNonEmptyStr(alt.ToolCallID, alt.ToolUseID)
					}
				}
			}
			open = append(open, c)
		case EventToolResult:
			var pl struct {
				ToolUseID string `json:"tool_use_id"`
			}
			if len(e.Payload) > 0 {
				_ = json.Unmarshal(e.Payload, &pl)
			}
			if len(open) == 0 {
				continue
			}
			idx := 0 // sem id: o resultado mais antigo fecha a chamada mais antiga
			if pl.ToolUseID != "" {
				found := -1
				for i, c := range open {
					if c.ID == pl.ToolUseID {
						found = i
						break
					}
				}
				if found >= 0 {
					idx = found
				}
			}
			open = append(open[:idx], open[idx+1:]...)
		}
	}
	return open
}

func toolName(e RunEvent) string {
	var pl struct {
		Name string `json:"name"`
	}
	if len(e.Payload) > 0 && json.Unmarshal(e.Payload, &pl) == nil && pl.Name != "" {
		return pl.Name
	}
	n := e.Text
	if i := strings.IndexByte(n, ':'); i >= 0 {
		n = n[:i]
	}
	return strings.TrimSpace(n)
}

func firstNonEmptyStr(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// UncertainOf devolve a primeira tool aberta com efeito externo (a mais antiga), ou nil.
func UncertainOf(open []OpenToolCall) *UncertainTool {
	for _, c := range open {
		if ToolHasExternalEffect(c.Name, c.Kind) {
			return &UncertainTool{Name: c.Name, Input: c.Input, Seq: c.Seq}
		}
	}
	return nil
}

// ---- fila de follow-ups ----

// FollowupKind diz de que fila vem o follow-up.
type FollowupKind string

const (
	FollowupChat FollowupKind = "chat" // mensagem humana numa conversa, esperando a run da conversa
	FollowupTask FollowupKind = "task" // instrução numa task, esperando a run da task
)

// FollowupStatus é o ciclo de vida de um follow-up da task.
type FollowupStatus string

const (
	FollowupPending   FollowupStatus = "pending"
	FollowupConsumed  FollowupStatus = "consumed"
	FollowupDiscarded FollowupStatus = "discarded"
)

// FollowupDelivery é como o follow-up chega ao agente.
type FollowupDelivery string

const (
	DeliveryQueue   FollowupDelivery = "queue"   // espera a run ativa terminar
	DeliverySteer   FollowupDelivery = "steer"   // injetado na run ativa
	DeliveryRestart FollowupDelivery = "restart" // a run ativa é interrompida e continua com ele
)

func (d FollowupDelivery) Valid() bool {
	return d == DeliveryQueue || d == DeliverySteer || d == DeliveryRestart
}

// FollowupWait é o motivo pelo qual um follow-up ainda não foi entregue.
type FollowupWait string

const (
	WaitRunActive          FollowupWait = "run_active"          // a run ativa precisa terminar
	WaitSteerPending       FollowupWait = "steer_pending"       // será injetado na run ativa
	WaitRestartPending     FollowupWait = "restart_pending"     // a run será interrompida e continuada
	WaitDecisionPending    FollowupWait = "decision_pending"    // há uma run parada esperando decisão humana
	WaitPersonaUnavailable FollowupWait = "persona_unavailable" // persona pausada, em erro ou desligada
	WaitUnassigned         FollowupWait = "unassigned"          // a task não tem persona
	WaitStarting           FollowupWait = "starting"            // nada impede: a próxima run está abrindo
)

// Followup é um item da fila unificada (chat + task): o que o operador mandou enquanto a run trabalha.
type Followup struct {
	ID             ID               `json:"id"`
	CompanyID      ID               `json:"company_id"`
	Kind           FollowupKind     `json:"kind"`
	TaskID         *ID              `json:"task_id,omitempty"`
	ConversationID *ID              `json:"conversation_id,omitempty"`
	Body           string           `json:"body"`
	Author         Actor            `json:"author"`
	Delivery       FollowupDelivery `json:"delivery"`
	// Position é a ordem de consumo (1 = o primeiro a ser entregue).
	Position int `json:"position"`
	// WaitReason e WaitDetail explicam a espera (WaitDetail: o nome da run, o motivo da persona…).
	WaitReason FollowupWait `json:"wait_reason"`
	WaitDetail string       `json:"wait_detail,omitempty"`
	WaitRunID  *ID          `json:"wait_run_id,omitempty"`
	CreatedAt  time.Time    `json:"created_at"`
	EditedAt   *time.Time   `json:"edited_at,omitempty"`
}

// MaxFollowupBytes é o maior corpo de um follow-up de task.
const MaxFollowupBytes = 20000

// MaxPendingFollowups é quantos follow-ups podem esperar por task.
const MaxPendingFollowups = 20
