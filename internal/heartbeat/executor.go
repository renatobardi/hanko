package heartbeat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/riverqueue/river"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/service"
)

var (
	errRunCancelled = errors.New("run cancelada")
	errRunTimeout   = errors.New("tempo limite da run excedido")
)

const (
	memoryTimeout = 20 * time.Second
	finishTimeout = 30 * time.Second
	eventTimeout  = 10 * time.Second
	cancelMsgType = "run.cancel"
)

// finalStatus maps how an execution ended to the run status and error text.
// cause is context.Cause of the run context; parentErr is the server context's error.
func finalStatus(cause, parentErr, runErr error, res adapter.Result) (domain.RunStatus, string) {
	switch {
	case errors.Is(cause, errRunCancelled):
		return domain.RunCancelled, "cancelada pelo usuário"
	case errors.Is(cause, errRunTimeout):
		return domain.RunExpired, "tempo limite excedido"
	case parentErr != nil:
		return domain.RunInterrupted, "interrompida pelo encerramento do servidor"
	case runErr != nil:
		return domain.RunFailed, runErr.Error()
	case res.IsError:
		msg := res.Error
		if msg == "" {
			msg = "o agente reportou erro"
		}
		return domain.RunFailed, msg
	}
	return domain.RunDone, ""
}

// jobInfo describes the river job running an execution.
type jobInfo struct {
	final bool          // last attempt: a load failure may now fail the run
	age   time.Duration // time since the job was created
}

// executeRun is the body of the execute_run job (first and final attempt).
func (h *Heartbeat) executeRun(ctx context.Context, a ExecuteRunArgs) error {
	return h.executeRunJob(ctx, a, jobInfo{final: true})
}

func (h *Heartbeat) executeRunJob(ctx context.Context, a ExecuteRunArgs, ji jobInfo) (err error) {
	if h.svc == nil {
		return errNotAttached
	}
	log := h.log.With("run_id", a.RunID, "company_id", a.CompanyID)

	rc, err := h.svc.LoadRunContext(ctx, a.CompanyID, a.RunID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			log.Warn("execute_run: run inexistente")
			return nil
		}
		// A transient load failure is retried by river; only the last attempt gives up the run.
		// The run may already hold a result waiting to be finalized: never fail it from here
		// before the last attempt.
		if ji.final {
			h.failEarly(ctx, a, fmt.Errorf("carregar contexto: %w", err))
		}
		return fmt.Errorf("execute_run: load context: %w", err)
	}
	if rc.Run.Status == domain.RunRunning && rc.PendingResult != nil {
		// The harness already ran: only finalization is missing. Never execute it twice.
		return h.finishPending(ctx, a, rc, ji, log)
	}
	if rc.Run.Status == domain.RunRunning {
		// Only a previous attempt of this job starts a run, and it left no result: the
		// harness may be gone mid-way. Fail the run instead of leaving it running forever.
		h.failEarly(ctx, a, errors.New("execução anterior não foi finalizada"))
		return nil
	}
	if rc.Run.Status != domain.RunQueued {
		log.Debug("execute_run: run não está na fila", "status", rc.Run.Status)
		return nil
	}
	agent, model := effectiveHarness(rc)
	log = log.With("agent", agent)
	if rc.Chat != nil {
		log = log.With("conversation_id", rc.Chat.Conversation.ID)
	} else {
		log = log.With("task_key", rc.Task.Key)
	}

	ad, ok := h.reg.Get(agent)
	if !ok {
		h.failEarly(ctx, a, fmt.Errorf("adapter desconhecido: %q", agent))
		return nil
	}
	if rc.Chat != nil && !adapter.ResumesSessions(ad) {
		rc.Chat.ResumeSession = "" // the harness cannot resume: the prompt carries the history
	}

	ctx, span := otel.Tracer("hanko/heartbeat").Start(ctx, "hanko.run", trace.WithAttributes(
		attribute.String("hanko.company_id", rc.Company.ID.String()),
		attribute.String("hanko.persona_id", rc.Persona.ID.String()),
		attribute.String("hanko.run_id", rc.Run.ID.String()),
		attribute.String("hanko.agent", agent),
		attribute.String("hanko.model", model),
	))
	if rc.Chat != nil {
		span.SetAttributes(attribute.String("hanko.conversation_id", rc.Chat.Conversation.ID.String()))
	} else {
		span.SetAttributes(attribute.String("hanko.task_id", rc.Task.ID.String()))
	}
	defer span.End()
	traceID := ""
	if sc := span.SpanContext(); sc.HasTraceID() {
		traceID = sc.TraceID().String()
	}

	// Run context: cancelled by run.cancel on the broker, bounded by RunTimeout.
	cctx, cancelCause := context.WithCancelCause(ctx)
	defer cancelCause(nil)
	execCtx, cancelTimeout := context.WithTimeoutCause(cctx, h.opts.RunTimeout, errRunTimeout)
	defer cancelTimeout()
	if h.broker != nil {
		sub, unsub := h.broker.Subscribe(events.RunTopic(a.RunID.String()), 16)
		defer unsub()
		go func() {
			for {
				select {
				case <-execCtx.Done():
					return
				case m, ok := <-sub:
					if !ok {
						return
					}
					if m.Type == cancelMsgType {
						cancelCause(errRunCancelled)
						return
					}
				}
			}
		}()
	}

	scope := scopeFor(rc.Company.Slug, rc.Workspace)
	curating := rc.Run.Trigger == domain.TriggerMemory

	// Memory is best effort: failures become warnings in the run log. A curation run only reads the briefing:
	// it never takes the pending handoff (it belongs to the next working session) nor closes a session.
	var warnings []string
	var handoff string
	var herr error
	if !curating {
		handoff, herr = h.callMemory(execCtx, func(c context.Context) (string, error) { return h.mem.AcceptHandoff(c, scope) })
		if herr != nil {
			warnings = append(warnings, "memória indisponível: "+herr.Error())
		}
	}
	briefing, berr := h.callMemory(execCtx, func(c context.Context) (string, error) { return h.mem.Briefing(c, scope) })
	if berr != nil && herr == nil {
		warnings = append(warnings, "memória indisponível: "+berr.Error())
	}

	if _, err := h.svc.MarkRunStarted(execCtx, a.CompanyID, a.RunID, traceID, handoff); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "mark started")
		h.failEarly(ctx, a, fmt.Errorf("marcar início: %w", err))
		return fmt.Errorf("execute_run: mark started: %w", err)
	}

	emit := func(kind domain.RunEventKind, text string) {
		h.appendEvent(execCtx, a, adapter.Event{Kind: kind, Text: text, OccurredAt: time.Now().UTC()})
	}
	for _, w := range warnings {
		emit(domain.EventSystem, w)
	}

	// From here on the run is "running": always finish it.
	var (
		res    adapter.Result
		runErr error
	)
	cleanup := func() {}
	defer func() { cleanup() }()

	extras := h.runExtras(execCtx, rc, emit)
	spec, dirCleanup, specErr := h.buildSpec(rc, briefing, handoff, scope, extras, emit)
	if dirCleanup != nil {
		cleanup = dirCleanup
	}
	if specErr != nil {
		runErr = specErr
	} else {
		spec.Timeout = h.opts.RunTimeout
		sink := adapter.SinkFunc(func(c context.Context, e adapter.Event) error {
			h.appendEvent(c, a, e)
			return nil
		})
		res, runErr = safeRun(execCtx, ad, spec, sink)
	}

	cause := context.Cause(execCtx)
	status, msg := finalStatus(cause, ctx.Err(), runErr, res)
	if status != domain.RunDone {
		if res.Error == "" || status != domain.RunFailed {
			res.Error = msg
		}
		res.IsError = true
	}
	span.SetAttributes(attribute.String("hanko.run.status", string(status)))
	if status != domain.RunDone {
		span.SetStatus(codes.Error, msg)
	}

	// The run context may be dead (cancel, timeout, shutdown); finishing needs a fresh one.
	fctx, cancelF := context.WithTimeout(context.WithoutCancel(ctx), finishTimeout)
	defer cancelF()
	if status != domain.RunDone {
		h.appendEvent(fctx, a, adapter.Event{Kind: domain.EventStatus, Text: fmt.Sprintf("%s: %s", status, msg), OccurredAt: time.Now().UTC()})
	}
	in := service.FinishRunInput{Status: status, Result: res}
	// Keep the outcome before finishing: if finalization keeps failing, a later attempt
	// finishes the run from here instead of executing the harness again.
	if serr := h.svc.SavePendingResult(fctx, a.CompanyID, a.RunID, in); serr != nil {
		log.Warn("execute_run: guardar resultado pendente", "err", serr)
	}
	if ferr := h.finishWithRetry(ctx, a, in, log); ferr != nil {
		span.RecordError(ferr)
		return h.finishFailed(ctx, a, ferr, ji, log)
	}
	log.Info("run finalizada", "status", status, "exit_code", res.ExitCode)

	if !curating {
		h.finalizeMemorySession(fctx, scope, agent, res.SessionRef, log)
	}
	return nil
}

// finishPending finalizes a run whose result was saved by an earlier attempt.
func (h *Heartbeat) finishPending(ctx context.Context, a ExecuteRunArgs, rc service.RunContext, ji jobInfo, log *slog.Logger) error {
	in := *rc.PendingResult
	if ferr := h.finishWithRetry(ctx, a, in, log); ferr != nil {
		return h.finishFailed(ctx, a, ferr, ji, log)
	}
	log.Info("run finalizada (resultado pendente)", "status", in.Status, "exit_code", in.Result.ExitCode)
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finishTimeout)
	defer cancel()
	if rc.Run.Trigger != domain.TriggerMemory {
		pagent, _ := effectiveHarness(rc)
		h.finalizeMemorySession(fctx, scopeFor(rc.Company.Slug, rc.Workspace), pagent, in.Result.SessionRef, log)
	}
	return nil
}

func (h *Heartbeat) finalizeMemorySession(ctx context.Context, scope memory.Scope, agent, sessionRef string, log *slog.Logger) {
	if sessionRef == "" {
		return
	}
	mctx, cancel := context.WithTimeout(ctx, memoryTimeout)
	defer cancel()
	if merr := h.mem.FinalizeSession(mctx, scope, agent, sessionRef); merr != nil {
		log.Warn("execute_run: finalizar sessão de memória", "err", merr)
	}
}

// finishWithRetry calls FinishRun, retrying locally with backoff (h.finishBackoff). Missing
// or invalid runs are not retried.
func (h *Heartbeat) finishWithRetry(ctx context.Context, a ExecuteRunArgs, in service.FinishRunInput, log *slog.Logger) error {
	var err error
	for i := 0; ; i++ {
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finishTimeout)
		_, err = h.svc.FinishRun(fctx, a.CompanyID, a.RunID, in)
		cancel()
		if err == nil {
			return nil
		}
		if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrInvalid) || i >= len(h.finishBackoff) {
			break
		}
		log.Warn("execute_run: finalizar run falhou, tentando de novo", "attempt", i+1, "err", err)
		t := time.NewTimer(h.finishBackoff[i])
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return fmt.Errorf("execute_run: finish: %w", err)
		}
	}
	return fmt.Errorf("execute_run: finish: %w", err)
}

// finishFailed decides what to do when finalization still fails after the local retries.
// The run is only snoozed while it is still running; a later attempt finalizes it from
// runs.pending_result without executing the harness.
func (h *Heartbeat) finishFailed(ctx context.Context, a ExecuteRunArgs, ferr error, ji jobInfo, log *slog.Logger) error {
	lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finishTimeout)
	defer cancel()
	cur, lerr := h.svc.LoadRunContext(lctx, a.CompanyID, a.RunID)
	switch {
	case errors.Is(lerr, domain.ErrNotFound):
		return nil
	case lerr == nil && cur.Run.Status != domain.RunRunning:
		log.Info("execute_run: run já finalizada por outro caminho", "status", cur.Run.Status)
		return nil
	}
	if ji.age > finishGiveUp {
		return ferr
	}
	log.Warn("execute_run: finalização pendente, nova tentativa agendada", "err", ferr)
	return river.JobSnooze(finishSnooze)
}

// buildSpec assembles the adapter.Spec and resolves the working directory. The returned
// cleanup removes a temp workspace; it may be nil.
func (h *Heartbeat) buildSpec(rc service.RunContext, briefing, handoff string, scope memory.Scope, extras service.RunExtras, emit func(domain.RunEventKind, string)) (adapter.Spec, func(), error) {
	publicURL := strings.TrimRight(h.opts.PublicURL, "/")
	review := rc.Review != nil && rc.Run.Trigger == domain.TriggerReview
	// A review run is headless and read-only: it gets no run token, no Hanko MCP and no
	// credentialed memory MCP (the artifact to judge is in the prompt).
	var token string
	var extraMCP map[string]json.RawMessage
	if !review {
		var err error
		if rc.Chat != nil {
			token, err = h.tokens.IssueForConversation(rc.Company.ID, rc.Persona.ID, rc.Run.ID, rc.Chat.Conversation.ID)
		} else {
			token, err = h.tokens.Issue(rc.Company.ID, rc.Persona.ID, rc.Run.ID, rc.Task.ID)
		}
		if err != nil {
			return adapter.Spec{}, nil, fmt.Errorf("emitir run token: %w", err)
		}
		extraMCP = mergeMCP(extras.MCP, toolGatewayMCP(publicURL, token, extras.ToolGateway), h.mem.AgentMCP(scope, rc.Run.ID.String(), token))
	}
	mcpCfg, err := buildMCPConfig(publicURL, token, extraMCP)
	if err != nil {
		return adapter.Spec{}, nil, err
	}

	dir, cleanup, err := workspaceDir(rc, h.opts.WorkspacesRoot, emit)
	if err != nil {
		return adapter.Spec{}, nil, err
	}
	system, prompt := "", ""
	if rc.Chat != nil {
		system, prompt = BuildChatPrompt(rc, briefing, handoff)
	} else {
		system, prompt = BuildPrompt(rc, briefing, handoff)
	}
	sessionRef := ""
	if rc.Chat != nil {
		sessionRef = rc.Chat.ResumeSession
	}
	agent, model := effectiveHarness(rc)
	cfg := rc.Persona.AdapterConfig
	if review {
		system, prompt = BuildReviewPrompt(rc, briefing)
		if agent != rc.Persona.Agent {
			cfg = nil // the persona's adapter_config targets its own harness, not the alternate one that reviews
		}
	}
	if rc.Run.Trigger == domain.TriggerMemory {
		system, prompt = BuildMemoryPrompt(rc, briefing)
	}
	// Skills (ADR-0029): o claude as carrega nativamente de um diretório temporário da run; os demais
	// harnesses recebem o índice no system prompt e buscam o corpo pela tool MCP hanko_skill_get.
	// Uma run de revisão não usa skills.
	var skills []domain.Skill
	if !review && len(extras.Skills) > 0 {
		if agent == "claude" {
			skills = extras.Skills
		} else {
			system = strings.TrimSpace(system + "\n\n" + SkillIndex(extras.Skills))
		}
	}
	return adapter.Spec{
		RunID: rc.Run.ID, CompanyID: rc.Company.ID, PersonaID: rc.Persona.ID, TaskID: rc.Task.ID, TaskKey: rc.Task.Key,
		ConversationID: conversationID(rc), Agent: agent, Model: model, Dir: dir,
		System: system, Prompt: prompt, SessionRef: sessionRef,
		Env:       buildEnv(os.Getenv("OTEL_RESOURCE_ATTRIBUTES"), publicURL, token, rc),
		MCPConfig: mcpCfg, Skills: skills, Config: cfg, ReadOnly: review,
	}, cleanup, nil
}

// runExtrasBackend é o que o service oferece para skills e MCPs da empresa; opcional para que os fakes
// de teste que não os usam continuem valendo.
type runExtrasBackend interface {
	RunExtras(ctx context.Context, companyID, personaID domain.ID) (service.RunExtras, error)
}

// runExtras busca as skills habilitadas e os servidores MCP da empresa para a run. É melhor esforço:
// uma falha vira aviso no log da run e a execução segue sem os extras. Revisões e a curadoria de memória
// (gatilho memory) não os recebem: são runs de sistema, restritas ao que o protocolo delas define (ADR-0029).
func (h *Heartbeat) runExtras(ctx context.Context, rc service.RunContext, emit func(domain.RunEventKind, string)) service.RunExtras {
	if rc.Review != nil && rc.Run.Trigger == domain.TriggerReview || rc.Run.Trigger == domain.TriggerMemory {
		return service.RunExtras{}
	}
	eb, ok := h.svc.(runExtrasBackend)
	if !ok {
		return service.RunExtras{}
	}
	ex, err := h.runExtrasFor(ctx, eb, rc)
	if err != nil {
		emit(domain.EventSystem, "skills e integrações indisponíveis nesta execução: "+err.Error())
		return service.RunExtras{}
	}
	for _, w := range ex.Warnings {
		emit(domain.EventSystem, w)
	}
	return ex
}

// mergeMCP junta servidores MCP; em colisão de nome vale o último mapa.
func mergeMCP(maps ...map[string]json.RawMessage) map[string]json.RawMessage {
	var out map[string]json.RawMessage
	for _, m := range maps {
		for k, v := range m {
			if out == nil {
				out = map[string]json.RawMessage{}
			}
			out[k] = v
		}
	}
	return out
}

// workspaceDir returns the workspace path when it exists on disk, otherwise a per-run
// temp dir (removed by cleanup). With a workspaces root configured the path is revalidated right before use
// (symlinks resolved, strictly inside the root): a symlink swapped in after the workspace was saved makes the run
// fall back to the temp dir with a warning event instead of working outside the root (ADR-0025).
func workspaceDir(rc service.RunContext, root string, emit func(domain.RunEventKind, string)) (string, func(), error) {
	if rc.Workspace != nil && rc.Workspace.Path != "" {
		dir, ok, why := usableWorkspaceDir(rc.Workspace.Path, root)
		if ok {
			return dir, nil, nil
		}
		emit(domain.EventSystem, why)
	}
	d, err := os.MkdirTemp("", "hanko-run-*")
	if err != nil {
		return "", nil, fmt.Errorf("criar diretório temporário: %w", err)
	}
	return d, func() { _ = os.RemoveAll(d) }, nil
}

// safeRun turns an adapter panic into an error so the run is still finished.
func safeRun(ctx context.Context, ad adapter.Adapter, spec adapter.Spec, sink adapter.Sink) (res adapter.Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("adapter %s: panic: %v", ad.Name(), r)
		}
	}()
	return ad.Run(ctx, spec, sink)
}

// appendEvent stores one event. A failure is logged and swallowed: losing a log line
// must not kill the run. A dead ctx still gets a short grace to flush the tail.
func (h *Heartbeat) appendEvent(ctx context.Context, a ExecuteRunArgs, e adapter.Event) {
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), eventTimeout)
		defer cancel()
	}
	if _, err := h.svc.AppendRunEvent(ctx, a.CompanyID, a.RunID, e); err != nil {
		h.log.Warn("execute_run: gravar evento", "run_id", a.RunID, "kind", e.Kind, "err", err)
	}
}

func (h *Heartbeat) callMemory(ctx context.Context, f func(context.Context) (string, error)) (string, error) {
	c, cancel := context.WithTimeout(ctx, memoryTimeout)
	defer cancel()
	s, err := f(c)
	if err != nil {
		return "", err
	}
	return s, nil
}

// failEarly finishes a run that could not even start.
func (h *Heartbeat) failEarly(ctx context.Context, a ExecuteRunArgs, cause error) {
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finishTimeout)
	defer cancel()
	_, err := h.svc.FinishRun(fctx, a.CompanyID, a.RunID, service.FinishRunInput{
		Status: domain.RunFailed,
		Result: adapter.Result{ExitCode: -1, IsError: true, Error: cause.Error()},
	})
	if err != nil {
		h.log.Error("execute_run: finalizar run com falha", "run_id", a.RunID, "err", err)
	}
}

// effectiveHarness is the harness and model a run executes with: the persona's, except for a
// review run, which carries the reviewer's (possibly another harness than the author's).
func effectiveHarness(rc service.RunContext) (agent, model string) {
	if rc.Run.Trigger == domain.TriggerReview && rc.Run.Agent != "" {
		return rc.Run.Agent, rc.Run.Model
	}
	return rc.Persona.Agent, rc.Persona.Model
}

// conversationID is the conversation of a chat run (zero for a task run).
func conversationID(rc service.RunContext) domain.ID {
	if rc.Chat == nil {
		return domain.ID{}
	}
	return rc.Chat.Conversation.ID
}
