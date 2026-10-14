package heartbeat

import (
	"context"
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
	log = log.With("task_key", rc.Task.Key, "agent", rc.Persona.Agent)

	ad, ok := h.reg.Get(rc.Persona.Agent)
	if !ok {
		h.failEarly(ctx, a, fmt.Errorf("adapter desconhecido: %q", rc.Persona.Agent))
		return nil
	}

	ctx, span := otel.Tracer("hanko/heartbeat").Start(ctx, "hanko.run", trace.WithAttributes(
		attribute.String("hanko.company_id", rc.Company.ID.String()),
		attribute.String("hanko.persona_id", rc.Persona.ID.String()),
		attribute.String("hanko.run_id", rc.Run.ID.String()),
		attribute.String("hanko.task_id", rc.Task.ID.String()),
		attribute.String("hanko.agent", rc.Persona.Agent),
		attribute.String("hanko.model", rc.Persona.Model),
	))
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

	// Memory is best effort: failures become warnings in the run log.
	var warnings []string
	handoff, herr := h.callMemory(execCtx, func(c context.Context) (string, error) { return h.mem.AcceptHandoff(c, scope) })
	if herr != nil {
		warnings = append(warnings, "memória indisponível: "+herr.Error())
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

	spec, dirCleanup, specErr := h.buildSpec(rc, briefing, handoff, scope, emit)
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

	h.finalizeMemorySession(fctx, scope, rc.Persona.Agent, res.SessionRef, log)
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
	h.finalizeMemorySession(fctx, scopeFor(rc.Company.Slug, rc.Workspace), rc.Persona.Agent, in.Result.SessionRef, log)
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
func (h *Heartbeat) buildSpec(rc service.RunContext, briefing, handoff string, scope memory.Scope, emit func(domain.RunEventKind, string)) (adapter.Spec, func(), error) {
	token, err := h.tokens.Issue(rc.Company.ID, rc.Persona.ID, rc.Run.ID, rc.Task.ID)
	if err != nil {
		return adapter.Spec{}, nil, fmt.Errorf("emitir run token: %w", err)
	}
	publicURL := strings.TrimRight(h.opts.PublicURL, "/")
	mcpCfg, err := buildMCPConfig(publicURL, token, h.mem.AgentMCP(scope, rc.Run.ID.String()))
	if err != nil {
		return adapter.Spec{}, nil, err
	}

	dir, cleanup, err := workspaceDir(rc, emit)
	if err != nil {
		return adapter.Spec{}, nil, err
	}
	system, prompt := BuildPrompt(rc, briefing, handoff)
	return adapter.Spec{
		RunID: rc.Run.ID, CompanyID: rc.Company.ID, PersonaID: rc.Persona.ID, TaskID: rc.Task.ID, TaskKey: rc.Task.Key,
		Agent: rc.Persona.Agent, Model: rc.Persona.Model, Dir: dir,
		System: system, Prompt: prompt, SessionRef: "",
		Env:       buildEnv(os.Getenv("OTEL_RESOURCE_ATTRIBUTES"), publicURL, token, rc),
		MCPConfig: mcpCfg, Config: rc.Persona.AdapterConfig,
	}, cleanup, nil
}

// workspaceDir returns the workspace path when it exists on disk, otherwise a per-run
// temp dir (removed by cleanup).
func workspaceDir(rc service.RunContext, emit func(domain.RunEventKind, string)) (string, func(), error) {
	if rc.Workspace != nil && rc.Workspace.Path != "" {
		if st, err := os.Stat(rc.Workspace.Path); err == nil && st.IsDir() {
			return rc.Workspace.Path, nil, nil
		}
		emit(domain.EventSystem, fmt.Sprintf("workspace %q não encontrado no disco; usando diretório temporário", rc.Workspace.Path))
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
