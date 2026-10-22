package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

type runTab struct {
	key, label string
	statuses   []domain.RunStatus
}

var runTabs = []runTab{
	{"todas", i18n.N("Todas"), nil},
	{"ao-vivo", i18n.N("Ao vivo"), []domain.RunStatus{domain.RunQueued, domain.RunRunning}},
	{"concluidas", i18n.N("Concluídas"), []domain.RunStatus{domain.RunDone}},
	{"falhas", i18n.N("Falhas"), []domain.RunStatus{domain.RunFailed, domain.RunExpired, domain.RunInterrupted}},
}

func (s *server) handleRuns(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	cur := runTabs[0]
	for _, t := range runTabs {
		if t.key == r.URL.Query().Get("aba") {
			cur = t
		}
	}
	base := "/c/" + co.Slug
	loc := loadLoc(co.Timezone)
	now := s.now()

	runs, err := s.be.ListRuns(ctx, co.ID, service.RunFilter{Statuses: cur.statuses, Limit: 100})
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	live, err := s.be.ListRuns(ctx, co.ID, service.RunFilter{Statuses: runTabs[1].statuses, Limit: 100})
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm := views.RunsVM{Live: len(live), Subtitle: i18n.T(ctx, "Cada vez que uma persona é acordada vira uma execução auditável: status, eventos, saída, custo e a task de origem.")}
	for _, t := range runTabs {
		vm.Tabs = append(vm.Tabs, views.TabLink{Key: t.key, Label: i18n.T(ctx, t.label), Href: base + "/execucoes?aba=" + t.key, On: t.key == cur.key})
	}
	for _, rv := range runs {
		vm.Rows = append(vm.Rows, runRow(ctx, rv, base, now, loc))
	}
	sh := s.shell(r, co, "execucoes", i18n.T(ctx, "Execuções"), views.Crumb{Label: i18n.T(ctx, "Execuções")})
	s.render(w, r, http.StatusOK, views.RunsPage(sh, vm))
}

const pageEvents = 1000

func (s *server) runVM(ctx context.Context, co domain.Company, rv service.RunView, personaSlug string, lines []views.LogLine, lastSeq int64) views.RunVM {
	base := "/c/" + co.Slug
	loc := loadLoc(co.Timezone)
	now := s.now()
	started := "—"
	if rv.StartedAt != nil {
		started = fmtClock(*rv.StartedAt, loc)
	}
	vm := views.RunVM{
		Name:         rv.Name,
		Short:        shortID(rv.ID),
		ID:           rv.ID.String(),
		Badge:        runBadge(ctx, rv.Status),
		Persona:      rv.PersonaName,
		TaskKey:      rv.TaskKey,
		TaskHref:     base + "/tasks/" + rv.TaskKey,
		TaskTitle:    rv.TaskTitle,
		Trigger:      triggerLabelCtx(ctx, rv.Trigger),
		Started:      started,
		Duration:     runDuration(rv.Run, now),
		Events:       int(lastSeq),
		Tokens:       fmtUsageCtx(ctx, rv.Usage),
		Cost:         fmtCostPtrCtx(ctx, rv.CostUSD),
		Agent:        rv.Agent,
		Model:        rv.Model,
		ModelSource:  modelSourceLabel(ctx, rv.ModelSource),
		PolicyOrigin: policyOriginLabel(ctx, rv.PolicyOrigin),
		ListCost:     fmtCostPtrCtx(ctx, rv.ListCostUSD),
		CostSource:   costSourceLabel(ctx, rv.CostSource),
		Subscription: subscriptionLabel(ctx, rv.SubscriptionID),
		Session:      rv.SessionRef,
		ExitCode:     "—",
		Error:        rv.Error,
		Summary:      renderMD(rv.Summary),
		Active:       rv.Status.Active(),
		Terminal:     rv.Status.Terminal(),
		Lines:        lines,
		LastSeq:      lastSeq,
	}
	if rv.InstructionRevision != nil {
		vm.Instructions = fmt.Sprintf("r%d", *rv.InstructionRevision)
		if len(rv.InstructionSHA256) >= 12 {
			vm.Instructions += " · " + rv.InstructionSHA256[:12]
		}
		{
			vm.InstructionsHref = base + "/personas/" + rv.PersonaID.String() + "/instrucoes?ver=" + strconv.Itoa(*rv.InstructionRevision) + "#revisao"
		}
	}
	if rv.ConversationID != nil {
		vm.Chat, vm.TaskKey, vm.TaskTitle = true, i18n.T(ctx, "Chat"), rv.ConversationTitle
		vm.TaskHref = base + "/chat/" + rv.ConversationID.String()
	}
	if rv.ExitCode != nil {
		vm.ExitCode = strconv.Itoa(*rv.ExitCode)
	}
	if personaSlug != "" {
		vm.PersonaHref = base + "/personas/" + personaSlug
	}
	if rv.Status.Active() {
		vm.CancelHref = base + "/execucoes/" + rv.ID.String() + "/cancelar"
	}
	vm.Diag = diagVM(ctx, base, rv, base+"/execucoes/"+rv.ID.String())
	vm.Steer = steerVM(ctx, base, rv)
	return vm
}

func (s *server) handleRun(w http.ResponseWriter, r *http.Request, co domain.Company) {
	runID, err := domain.ParseID(r.PathValue("run"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	rv, err := s.be.GetRun(r.Context(), co.ID, runID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	evs, err := s.be.ListRunEvents(r.Context(), co.ID, runID, 0, pageEvents)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	loc := loadLoc(co.Timezone)
	var lines []views.LogLine
	var last int64
	for _, e := range evs {
		lines = append(lines, logLine(e, loc))
		last = e.Seq
	}
	ctx := r.Context()
	vm := s.runVM(ctx, co, rv, "", lines, last)
	vm.IslandAPI, vm.LogMsgs, vm.TZ, vm.Lang = islandAPI(co), runLogMsgs(ctx), co.Timezone, string(i18n.FromContext(ctx))
	vm.AnchorMsgs, vm.SummaryDoc = anchorMsgs(ctx), docOf(rv.Summary)
	if rb, ok := s.be.(runSkillsBackend); ok { // skills e revisões da run (ADR-0044)
		if list, err := rb.ListRunSkills(ctx, co.ID, rv.ID); err == nil {
			for _, k := range list {
				label := k.Slug + " v" + strconv.Itoa(k.Revision)
				if k.Pinned {
					label += " (" + i18n.T(ctx, "fixada") + ")"
				}
				vm.Skills = append(vm.Skills, label)
			}
		}
	}
	const runArtifactsMax = 20
	arts, err := s.be.ListArtifacts(ctx, co.ID, service.ArtifactFilter{RunID: &rv.ID, AllVersions: true, Limit: runArtifactsMax})
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm.Artifacts = artifactMinis(ctx, arts, "/c/"+co.Slug)
	if len(arts) == runArtifactsMax {
		vm.ArtifactsHref = "/c/" + co.Slug + "/artefatos?execucao=" + rv.ID.String()
	}
	vm.SummaryAnchors = "[]"
	if strings.TrimSpace(rv.Summary) != "" {
		if cs, err := s.be.ListComments(ctx, co.ID, rv.TaskID); err == nil {
			names := s.lookup(ctx, nil, nil)
			vm.SummaryAnchors = anchorViewsJSON(ctx, cs, names, loc, domain.AnchorRunSummary, &rv.ID)
		}
	}
	base := "/c/" + co.Slug
	if !rv.Status.Terminal() {
		vm.SSEHref = base + "/execucoes/" + rv.ID.String() + "/sse?after=" + strconv.FormatInt(last, 10)
	} else if len(evs) == pageEvents {
		vm.Error = strings.TrimSpace(vm.Error + " " + i18n.Tf(ctx, "Log truncado nos primeiros %d eventos.", pageEvents))
	}
	sh := s.shell(r, co, "execucoes", i18n.T(ctx, "Execução")+" "+nameOrShort(vm.Name, vm.Short), views.Crumb{Label: i18n.T(ctx, "Execuções"), Href: base + "/execucoes"}, views.Crumb{Label: nameOrShort(vm.Name, vm.Short)})
	s.render(w, r, http.StatusOK, views.RunPage(sh, vm))
}

func (s *server) handleCancelRun(w http.ResponseWriter, r *http.Request, co domain.Company) {
	runID, err := domain.ParseID(r.PathValue("run"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	if _, err := s.be.CancelRun(r.Context(), actorFrom(r.Context()), co.ID, runID); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	if isHX(r) {
		// the SSE feed delivers the new status; nothing to swap.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/execucoes/"+runID.String())
}

// ---------- SSE ----------

func renderString(ctx context.Context, c templ.Component) (string, error) {
	var buf bytes.Buffer
	if err := c.Render(ctx, &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func lastEventID(r *http.Request) int64 {
	v := r.Header.Get("Last-Event-ID")
	if v == "" {
		v = r.URL.Query().Get("after")
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func asRunEvent(v any) (domain.RunEvent, bool) {
	switch e := v.(type) {
	case domain.RunEvent:
		return e, true
	case *domain.RunEvent:
		if e != nil {
			return *e, true
		}
	}
	return domain.RunEvent{}, false
}

// accessLost: the caller no longer reads this company (role removed or lowered below viewer, membership
// gone, session ended). SSE re-reads through the service on every tick, which re-checks the role, so
// this is what ends a stream that was opened while access still held.
func accessLost(err error) bool {
	return errors.Is(err, domain.ErrForbidden) || errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrUnauthenticated)
}

// handleRunSSE streams the run to the page: "run-event" carries one rendered
// log line, "run-status" the refreshed header, "run-end" closes the stream.
// It replays from ?after= (or Last-Event-ID) before following the broker, and
// re-reads the database on a ticker so dropped broker messages self-heal.
func (s *server) handleRunSSE(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	runID, err := domain.ParseID(r.PathValue("run"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	if _, err := s.be.GetRun(ctx, co.ID, runID); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	h.Set("Connection", "keep-alive")

	// subscribe before replaying so nothing falls between the two
	sub, cancel := s.broker.Subscribe(events.RunTopic(runID.String()), 256)
	defer cancel()

	loc := loadLoc(co.Timezone)
	after := lastEventID(r)
	lastKey, lastActions := "", ""

	write := func(id int64, event, html string) bool {
		var b strings.Builder
		if id > 0 {
			fmt.Fprintf(&b, "id: %d\n", id)
		}
		fmt.Fprintf(&b, "event: %s\n", event)
		for _, ln := range strings.Split(html, "\n") {
			b.WriteString("data: " + ln + "\n")
		}
		b.WriteString("\n")
		if _, err := w.Write([]byte(b.String())); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	sendLine := func(e domain.RunEvent) bool {
		html, err := renderString(ctx, views.RunLine(logLine(e, loc)))
		if err != nil {
			s.log.Error("ui sse render", "err", err)
			return true
		}
		after = e.Seq
		return write(e.Seq, "run-event", html)
	}
	flushEvents := func() bool {
		for {
			evs, err := s.be.ListRunEvents(ctx, co.ID, runID, after, 200)
			if err != nil {
				if accessLost(err) { // perdeu o papel na empresa: encerra o stream
					return false
				}
				s.log.Warn("ui sse events", "run", runID, "err", err)
				return true
			}
			for _, e := range evs {
				if !sendLine(e) {
					return false
				}
			}
			if len(evs) < 200 {
				return true
			}
		}
	}
	// status pushes the header when it changed and reports whether the run is over.
	status := func(force bool) (terminal, ok bool) {
		cur, err := s.be.GetRun(ctx, co.ID, runID)
		if err != nil {
			if accessLost(err) {
				return false, false
			}
			s.log.Warn("ui sse run", "run", runID, "err", err)
			return false, true
		}
		key := fmt.Sprintf("%s|%d|%v", cur.Status, after, cur.CostUSD)
		if force || key != lastKey {
			lastKey = key
			html, err := renderString(ctx, views.RunHeader(s.runVM(ctx, co, cur, "", nil, after)))
			if err == nil && !write(0, "run-status", html) {
				return false, false
			}
		}
		// The diagnosis card and the steer box change only with the run's state, never per log line (the text being
		// typed in the steer box must survive the stream).
		akey := fmt.Sprintf("%s|%s|%v|%v|%v", cur.Status, cur.Decision, cur.DecisionPending, cur.ContinuedByRunID, cur.Diagnosis != nil)
		if lastActions == "" {
			lastActions = akey // the page was rendered with this state already
		} else if akey != lastActions {
			lastActions = akey
			html, err := renderString(ctx, views.RunActionsBody(s.runVM(ctx, co, cur, "", nil, after)))
			if err == nil && !write(0, "run-actions", html) {
				return false, false
			}
		}
		return cur.Status.Terminal(), true
	}
	finish := func() {
		flushEvents()
		status(true)
		write(0, "run-end", "fim")
	}

	// headers go out with the first write; flush a comment so the client opens now
	_, _ = w.Write([]byte("retry: 3000\n: conectado\n\n"))
	if rc.Flush() != nil {
		return
	}
	if !flushEvents() {
		return
	}
	if term, ok := status(true); !ok {
		return
	} else if term {
		finish()
		return
	}

	tick := time.NewTicker(s.sseTick)
	defer tick.Stop()
	keep := time.NewTicker(15 * time.Second)
	defer keep.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case m, open := <-sub:
			if !open {
				return
			}
			if e, ok := asRunEvent(m.Data); ok && m.Type == "run.event" && e.Seq == after+1 {
				if !sendLine(e) {
					return
				}
			} else if !flushEvents() {
				return
			}
			if m.Type == "run.status" || m.Type == "run.cancel" {
				term, ok := status(false)
				if !ok {
					return
				}
				if term {
					finish()
					return
				}
			}
		case <-tick.C:
			if !flushEvents() {
				return
			}
			term, ok := status(false)
			if !ok {
				return
			}
			if term {
				finish()
				return
			}
		case <-keep.C:
			if _, err := w.Write([]byte(": keepalive\n\n")); err != nil || rc.Flush() != nil {
				return
			}
		}
	}
}

// nameOrShort is the friendly run name, or the short id for runs that have none.
func nameOrShort(name, short string) string {
	if name != "" {
		return name
	}
	return short
}

func modelSourceLabel(ctx context.Context, s domain.ModelSource) string {
	switch s {
	case domain.ModelFromTask:
		return i18n.T(ctx, "escolha manual na task")
	case domain.ModelFromPersona:
		return i18n.T(ctx, "modelo da persona")
	case domain.ModelFromHarnessDefault:
		return i18n.T(ctx, "padrão do harness")
	}
	return "—"
}

func costSourceLabel(ctx context.Context, s domain.CostSource) string {
	switch s {
	case domain.CostReported:
		return i18n.T(ctx, "real, informado pelo harness")
	case domain.CostEstimated:
		return i18n.T(ctx, "estimado pela tabela de preços")
	}
	return i18n.T(ctx, "desconhecido")
}

func subscriptionLabel(ctx context.Context, id *domain.ID) string {
	if id == nil {
		return "—"
	}
	return i18n.T(ctx, "coberta por assinatura")
}

// runSkillsBackend lista as skills (e revisões) que uma run recebeu (ADR-0044); opcional como os demais.
type runSkillsBackend interface {
	ListRunSkills(ctx context.Context, companyID, runID domain.ID) ([]service.RunSkill, error)
}
