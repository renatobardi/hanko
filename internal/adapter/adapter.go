// Package adapter runs a persona's agent harness (claude, codex, acp, fake) for one run
// and streams normalized events back to Hanko.
package adapter

import (
	"context"
	"encoding/json"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/adapter/procrun"
	"github.com/renatobardi/hanko/internal/domain"
)

// Spec is everything an adapter needs to execute one run.
type Spec struct {
	RunID     domain.ID
	CompanyID domain.ID
	PersonaID domain.ID
	TaskID    domain.ID
	TaskKey   string
	// ConversationID is set (and TaskID zero) on a chat run, which belongs to a conversation, not a task.
	ConversationID domain.ID
	Agent          string // claude | codex | acp | fake
	Model          string
	Dir            string // working directory (workspace path); empty = temp dir
	System         string // persona instructions + Hanko protocol (appended system prompt)
	Prompt         string // task + thread + memory briefing
	SessionRef     string // previous agent session id to resume, if any
	Env            map[string]string
	MCPConfig      json.RawMessage // {"mcpServers":{...}} written to a temp file for the harness
	// Skills são as skills habilitadas da persona que o harness carrega nativamente (hoje só o claude, via
	// um diretório temporário da run; ADR-0029). Nos demais harnesses o índice vai em System e o corpo
	// sai pela tool MCP hanko_skill_get, então o campo fica vazio.
	Skills  []domain.Skill
	Config  json.RawMessage // persona.adapter_config
	Timeout time.Duration
	// ReadOnly forces the harness into its read-only mode (review runs), ignoring any
	// permissive adapter_config (permission mode, sandbox, extra args).
	ReadOnly bool
	// Steer entrega ao adapter, durante a run, as mensagens que o operador injeta (ADR-0039). Só é preenchido para
	// adapter com SteerInject; o adapter confirma cada mensagem (Steer.Ack) depois de entregá-la ao harness.
	Steer <-chan Steer
	// Secrets são os valores de segredos resolvidos no despacho (${secret:...}, ADR-0050) que entraram em Config,
	// Env ou MCPConfig; entram na redação do log e do resultado da run, qualquer que seja o nome da variável.
	Secrets []string
	// Exec é o ambiente remoto da run (host SSH ou contêiner, ADR-0037); nil = local, como sempre foi.
	Exec ExecTarget
	// ToolGuard, quando definido, decide as tools nativas do harness pela política de tools (ADR-0053): o ACP o consulta
	// a cada permission request. Nil = só a política de permissão do adapter_config.
	ToolGuard ToolGuard
	// ToolHookBin, quando definido, é o executável `hanko` que o claude chama como hook PreToolUse para a mesma política
	// (ADR-0053). Vazio = sem hook. Não vale em ambiente remoto (o executável não está lá).
	ToolHookBin string
}

// ToolGuard decide as tools nativas do harness (Bash, Edit...) pela política de tools do Hanko (ADR-0053). Check pode
// bloquear até a decisão de um humano (gate) e volta cedo quando ctx é cancelado.
type ToolGuard interface {
	Check(ctx context.Context, req ToolRequest) ToolVerdict
}

// ToolRequest é a chamada de tool que o harness pede para fazer.
type ToolRequest struct {
	Name  string // nome da tool (no ACP, o tipo da chamada: execute, edit...)
	Title string
	Input json.RawMessage
}

// ToolDecision é a resposta do guarda.
type ToolDecision string

const (
	ToolNoOpinion ToolDecision = ""      // nenhuma política alcança a chamada: vale o que o adapter já fazia
	ToolAllow     ToolDecision = "allow" // liberada
	ToolDeny      ToolDecision = "deny"  // recusada
)

// ToolVerdict é a decisão e o motivo (mostrado ao agente e gravado no log).
type ToolVerdict struct {
	Decision ToolDecision
	Reason   string
}

// Steer é uma mensagem do operador para a run em andamento.
type Steer struct {
	ID   string // identifica o follow-up (devolvido ao service no ack)
	Text string
	// Ack informa o resultado da entrega ao harness (nil = entregue). O adapter chama uma vez; pode ser nil.
	Ack func(error)
}

// Done confirma a entrega (ou a falha dela) se houver quem espere.
func (s Steer) Done(err error) {
	if s.Ack != nil {
		s.Ack(err)
	}
}

// ExecTarget é o alvo de execução remoto de uma run (ADR-0037). O adapter passa o processo do harness por ele
// (procrun.Options.Target), usa WorkDir como diretório de trabalho e grava os arquivos auxiliares (config MCP,
// skills) em AuxDir, referindo-os pelo caminho remoto.
type ExecTarget interface {
	procrun.Target
	// WorkDir é o diretório de trabalho da run no alvo (a cópia do workspace).
	WorkDir() string
	// AuxDir devolve um diretório local para arquivos auxiliares da run e o caminho dele no alvo. O que for
	// gravado ali antes de o processo começar chega ao alvo.
	AuxDir() (local, remote string)
	// Describe é uma descrição curta do alvo para o log da run (sem segredos).
	Describe() string
}

// RemotePath traduz um caminho local dentro de AuxDir para o caminho correspondente no alvo.
func RemotePath(t ExecTarget, local string) string {
	l, r := t.AuxDir()
	rel, err := filepath.Rel(l, local)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return local
	}
	return path.Join(r, filepath.ToSlash(rel))
}

// Event is one normalized log entry emitted while the agent runs.
type Event struct {
	Kind       domain.RunEventKind
	Text       string
	Payload    json.RawMessage
	OccurredAt time.Time
	// SessionRef, quando preenchido, é a sessão do harness recém-anunciada: o service a grava na run na hora, para
	// que uma run que morra sem devolver o Result ainda possa ser retomada (ADR-0039). Não vai para o log.
	SessionRef string
}

// Sink receives events in order. Implementations must be safe to call from one goroutine.
type Sink interface {
	Emit(ctx context.Context, e Event) error
}

// SinkFunc adapts a function to Sink.
type SinkFunc func(ctx context.Context, e Event) error

func (f SinkFunc) Emit(ctx context.Context, e Event) error { return f(ctx, e) }

// Result is what the adapter reports when the agent exits.
type Result struct {
	ExitCode   int
	SessionRef string
	Summary    string   // final assistant message
	CostUSD    *float64 // reported by the harness when available
	Usage      domain.Usage
	Model      string // model actually used, when reported
	IsError    bool
	Error      string
}

// Adapter executes a run. Run blocks until the agent exits or ctx is cancelled.
type Adapter interface {
	Name() string
	Run(ctx context.Context, spec Spec, sink Sink) (Result, error)
}

// SessionResumer is implemented by adapters that can resume the harness session named by Spec.SessionRef
// (a chat run resumes the session of the previous run of its conversation).
type SessionResumer interface{ ResumesSessions() bool }

// ResumesSessions reports whether a can resume Spec.SessionRef.
func ResumesSessions(a Adapter) bool {
	r, ok := a.(SessionResumer)
	return ok && r.ResumesSessions()
}

// Steerer é implementado por adapters que declaram como aceitam uma mensagem no meio da run. Quem não implementa
// cai em domain.SteerRestart (interromper e continuar), que funciona com qualquer harness.
type Steerer interface{ SteerMode() domain.SteerMode }

// SteerModeOf é a capability de steering de a.
func SteerModeOf(a Adapter) domain.SteerMode {
	if s, ok := a.(Steerer); ok {
		return s.SteerMode()
	}
	return domain.SteerRestart
}

// Registry resolves adapters by agent name.
type Registry map[string]Adapter

func (r Registry) Get(name string) (Adapter, bool) { a, ok := r[name]; return a, ok }
