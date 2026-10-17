package ui

import (
	"bytes"
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Fila de revisão, aprendizados e exemplos de decisão na UI (ADR-0034), mais os pontos de encaixe nas telas que já
// existiam: o contador na Caixa de entrada, o painel "Cases" da task e o "Salvar como exemplo" do gate.

var reviewNotices = map[string]string{
	"revisado": caseNotices["revisado"],
	"sugestao": caseNotices["sugestao"],
	"atencao":  caseNotices["atencao"],
	"lote":     i18n.N("Itens aprovados."),
}

func reviewKindBadge(ctx context.Context, kind string) views.Badge {
	switch kind {
	case "suggestion":
		return views.Badge{Label: i18n.T(ctx, "sugestão"), Class: "run"}
	case "attention":
		return views.Badge{Label: i18n.T(ctx, "atenção"), Class: "soft"}
	}
	return views.Badge{Label: i18n.T(ctx, "revisão"), Class: "warn"}
}

func (s *server) handleReviewQueue(w http.ResponseWriter, r *http.Request, co domain.Company) {
	s.renderReviewQueue(w, r, co, "", nil, http.StatusOK)
}

func (s *server) renderReviewQueue(w http.ResponseWriter, r *http.Request, co domain.Company, errMsg string, failures []string, status int) {
	ctx := r.Context()
	items, err := s.pb().ReviewQueue(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	base := "/c/" + co.Slug
	loc, now := loadLoc(co.Timezone), s.now()
	vm := views.ReviewQueueVM{Notice: i18n.T(ctx, reviewNotices[r.URL.Query().Get("ok")]), Error: errMsg, BulkAction: base + "/revisao/lote", Failures: failures}
	for _, it := range items {
		href := caseHref(base, it.Case.Identifier)
		row := views.ReviewRow{Kind: it.Kind, KindBadge: reviewKindBadge(ctx, it.Kind), CaseID: it.Case.ID.String(), Identifier: it.Case.Identifier, Title: it.Case.Title,
			Href: href, Version: it.Case.Version, Reason: it.Reason, Pipeline: it.Case.PipelineName, Since: agoCtx(ctx, it.Since, now, loc),
			Action: href + "/revisar", ApproveTo: it.ApproveToName, RejectTo: it.RejectToName, ChangesTo: it.RequestChangesToName,
			RequireRejectReason: it.RequireRejectReason, RequireChanges: it.RequireChangesReason, SuggestionAction: href + "/sugestao",
			SuggestedStage: it.SuggestedStageName, AttentionAction: href + "/atencao"}
		switch it.Kind {
		case "review":
			row.Selectable = s.canDecideStage(ctx, co.ID, domain.StageConfig{Approval: approvalOf(it.Approver)})
			if it.Case.StageName != "" {
				row.Pipeline += " › " + it.Case.StageName
			}
		case "attention":
			switch it.Reason {
			case "blockers_resolved":
				row.Reason = i18n.T(ctx, "Os bloqueios deste case foram resolvidos.")
			case "children_terminal":
				row.Reason = i18n.T(ctx, "Todos os cases filhos terminaram.")
			}
		case "suggestion":
			if it.Suggestion != nil {
				row.Reason = i18n.Tf(ctx, "Mover para %s (%d%%): %s", it.SuggestedStageName, int(it.Suggestion.Confidence*100+0.5), it.Reason)
			}
		}
		vm.Rows = append(vm.Rows, row)
	}
	sh := s.shell(r, co, "revisao", i18n.T(ctx, "Fila de revisão"))
	s.render(w, r, status, views.ReviewQueuePage(sh, vm))
}

func approvalOf(approver string) *domain.StageApproval {
	if approver == "" {
		return nil
	}
	return &domain.StageApproval{Required: true, Approver: approver}
}

func (s *server) handleBulkApprove(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, &co, domain.Invalid("form", "formulário inválido"))
		return
	}
	reason := trimForm(r, "reason")
	var items []service.BulkReviewItem
	for _, v := range r.PostForm["item"] {
		id, ver, ok := strings.Cut(v, ":")
		cid, err := domain.ParseID(id)
		n, err2 := strconv.Atoi(ver)
		if !ok || err != nil || err2 != nil {
			continue
		}
		items = append(items, service.BulkReviewItem{CaseID: cid, ExpectedVersion: n, Reason: reason})
	}
	if len(items) == 0 {
		s.renderReviewQueue(w, r, co, i18n.T(ctx, "Selecione ao menos um item."), nil, http.StatusUnprocessableEntity)
		return
	}
	res, err := s.pb().BulkApprove(ctx, actorFrom(ctx), co.ID, items)
	if err != nil {
		s.renderReviewQueue(w, r, co, pipelineErr(ctx, err), nil, statusFor(err))
		return
	}
	var failures []string
	for _, x := range res {
		if x.OK {
			continue
		}
		label := x.CaseID.String()
		if cv, err := s.pb().GetCaseView(ctx, co.ID, x.CaseID.String()); err == nil {
			label = cv.Identifier + " — " + cv.Title
		}
		failures = append(failures, label+": "+pipelineErr(ctx, x.Err))
	}
	if len(failures) > 0 {
		s.renderReviewQueue(w, r, co, "", failures, http.StatusConflict)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/revisao?ok=lote")
}

// ---------- aprendizados ----------

var learningNotices = map[string]string{
	"exemplo":  caseNotices["exemplo"],
	"notas":    i18n.N("Notas salvas."),
	"apagado":  i18n.N("Exemplo apagado."),
	"exportar": i18n.N("Só admin exporta os exemplos."),
}

func learningText(ctx context.Context, l service.Learning, who string) string {
	switch {
	case l.Kind == string(domain.CaseEvTransitionForced):
		return i18n.Tf(ctx, "%s forçou \"%s\" de %s para %s", who, l.CaseTitle, l.FromName, l.ToName)
	case l.Decision == string(domain.ReviewApprove):
		return i18n.Tf(ctx, "%s aprovou \"%s\" movendo para %s", who, l.CaseTitle, l.ToName)
	case l.Decision == string(domain.ReviewReject):
		return i18n.Tf(ctx, "%s recusou \"%s\" movendo para %s", who, l.CaseTitle, l.ToName)
	}
	return i18n.Tf(ctx, "%s pediu mudanças em \"%s\", que voltou para %s", who, l.CaseTitle, l.ToName)
}

func (s *server) handleLearnings(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	b := s.pb()
	base := "/c/" + co.Slug
	q := r.URL.Query()
	tab := "decisoes"
	if q.Get("aba") == "exemplos" {
		tab = "exemplos"
	}
	loc, now := loadLoc(co.Timezone), s.now()
	vm := views.LearningsVM{Tab: tab, Notice: i18n.T(ctx, learningNotices[q.Get("ok")]), ExportHref: base + "/exemplos/export.jsonl"}
	for _, t := range []struct{ k, l string }{{"decisoes", i18n.N("Aprendizados")}, {"exemplos", i18n.N("Exemplos de decisão")}} {
		vm.Tabs = append(vm.Tabs, views.TabLink{Key: t.k, Label: i18n.T(ctx, t.l), Href: base + "/aprendizados?aba=" + t.k, On: t.k == tab})
	}
	personas, err := s.be.ListPersonas(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	names := s.actorNames(ctx, co.ID, personas)
	if tab == "exemplos" {
		xs, err := b.ListDecisionExamples(ctx, co.ID, service.DecisionExampleFilter{Limit: 500})
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		me := actorFrom(ctx).ID
		for _, x := range xs {
			row := views.ExampleRow{ID: x.ID.String(), SourceKind: string(x.SourceKind), Outcome: x.DecisionOutcome, Cutoff: fmtDateTime(ctx, x.CutoffAt, loc),
				Notes: x.Notes, Mine: sameIDPtr(me, x.CreatedByUserID), EditAction: base + "/exemplos/" + x.ID.String(), DeleteAction: base + "/exemplos/" + x.ID.String() + "/apagar"}
			switch x.SourceKind {
			case domain.DecisionFromGate:
				row.SourceLabel, row.SourceHref = i18n.T(ctx, "Gate")+" "+shortID(x.SourceID), base+"/gates/"+x.SourceID.String()
			default:
				row.SourceLabel = i18n.T(ctx, "Revisão de case")
				if c, ok := x.Snapshot["case"].(map[string]any); ok {
					if id, _ := c["identifier"].(string); id != "" {
						row.SourceLabel += " " + id
						row.SourceHref = caseHref(base, id)
					}
				}
				if row.SourceHref == "" {
					row.SourceHref = base + "/aprendizados"
				}
			}
			vm.Examples = append(vm.Examples, row)
		}
	} else {
		pipes, err := b.ListPipelines(ctx, co.ID, true)
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		f := service.LearningFilter{Decision: q.Get("decisao"), Limit: 300}
		vm.PipelineOptions = []views.Option{{Value: "", Label: i18n.T(ctx, "Todos os pipelines")}}
		var cur *service.PipelineView
		for i, p := range pipes {
			sel := p.Key == q.Get("pipeline")
			if sel {
				cur = &pipes[i]
				f.PipelineID = &pipes[i].ID
			}
			vm.PipelineOptions = append(vm.PipelineOptions, views.Option{Value: p.Key, Label: p.Name, Selected: sel})
		}
		if cur != nil {
			vm.StageOptions = []views.Option{{Value: "", Label: i18n.T(ctx, "Todas as etapas")}}
			for _, st := range cur.Stages {
				sel := st.Key == q.Get("etapa")
				if sel {
					f.StageID = &st.ID
				}
				vm.StageOptions = append(vm.StageOptions, views.Option{Value: st.Key, Label: st.Name, Selected: sel})
			}
		}
		for _, d := range []struct{ v, l string }{{"", i18n.N("Todas as decisões")}, {"approve", i18n.N("Aprovações")}, {"reject", i18n.N("Recusas")},
			{"request_changes", i18n.N("Pedidos de mudança")}, {"forced", i18n.N("Transições forçadas")}} {
			vm.DecisionOptions = append(vm.DecisionOptions, views.Option{Value: d.v, Label: i18n.T(ctx, d.l), Selected: f.Decision == d.v})
		}
		if f.Decision != "" && f.Decision != "forced" && !domain.ReviewDecision(f.Decision).Valid() {
			f.Decision = ""
		}
		ls, err := b.ListLearnings(ctx, co.ID, f)
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		saved := s.savedExamples(ctx, co.ID, domain.DecisionFromCaseReview)
		var day *views.LearningDay
		for _, l := range ls {
			label := dayLabel(ctx, l.At, now, loc)
			if day == nil || day.Label != label {
				vm.Days = append(vm.Days, views.LearningDay{Label: label})
				day = &vm.Days[len(vm.Days)-1]
			}
			who := l.ActorName
			if who == "" {
				who = names.of(ctx, l.Actor)
			}
			it := views.LearningItem{Text: learningText(ctx, l, who), Reason: l.Reason, CaseHref: caseHref(base, l.CaseIdentifier), CaseIdentifier: l.CaseIdentifier,
				PipelineName: l.PipelineName, When: fmtClock(l.At, loc), Icon: "clipboard-check"}
			if l.PipelineKey != "" {
				it.PipelineHref = base + "/pipelines/" + l.PipelineKey
			}
			if l.Reason == "" {
				it.Reason = i18n.T(ctx, "(sem nota)")
			}
			if l.Kind == string(domain.CaseEvTransitionForced) {
				it.Icon = "alert"
			} else if l.Actor.Type == domain.ActorHuman {
				it.Example = &views.ExampleFormVM{Action: base + "/exemplos", SourceKind: string(domain.DecisionFromCaseReview), SourceID: l.EventID.String(),
					Next: base + "/aprendizados", Saved: saved[l.EventID], SavedHref: base + "/aprendizados?aba=exemplos"}
			}
			day.Items = append(day.Items, it)
		}
		vm.Empty = len(ls) == 0
	}
	sh := s.shell(r, co, "aprendizados", i18n.T(ctx, "Aprendizados"))
	s.render(w, r, http.StatusOK, views.LearningsPage(sh, vm))
}

// safeNext só aceita voltar para uma página da própria empresa.
func safeNext(co domain.Company, next, fallback string) string {
	base := "/c/" + co.Slug + "/"
	if strings.HasPrefix(next, base) && !strings.Contains(next, "//") && !strings.ContainsAny(next, "\\\r\n") {
		return next
	}
	return fallback
}

func (s *server) handleSaveExample(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	base := "/c/" + co.Slug
	src, err := domain.ParseID(trimForm(r, "source_id"))
	if err != nil {
		s.fail(w, r, &co, domain.Invalid("source_id", "não é um id"))
		return
	}
	if _, err := s.pb().SaveDecisionExample(ctx, actorFrom(ctx), co.ID, service.SaveDecisionExampleInput{SourceKind: domain.DecisionSource(trimForm(r, "source_kind")),
		SourceID: src, Notes: r.PostFormValue("notes")}); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	to := safeNext(co, trimForm(r, "next"), base+"/aprendizados?aba=exemplos")
	sep := "?"
	if strings.Contains(to, "?") {
		sep = "&"
	}
	redirect(w, r, to+sep+"ok=exemplo")
}

func (s *server) handleExampleNotes(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	id, err := domain.ParseID(r.PathValue("example"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	if _, err := s.pb().UpdateDecisionExampleNotes(ctx, actorFrom(ctx), co.ID, id, r.PostFormValue("notes")); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/aprendizados?aba=exemplos&ok=notas")
}

func (s *server) handleDeleteExample(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	id, err := domain.ParseID(r.PathValue("example"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	if err := s.pb().DeleteDecisionExample(ctx, actorFrom(ctx), co.ID, id); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/aprendizados?aba=exemplos&ok=apagado")
}

func (s *server) handleExportExamples(w http.ResponseWriter, r *http.Request, co domain.Company) {
	var buf bytes.Buffer
	if _, err := s.pb().ExportDecisionExamples(r.Context(), co.ID, &buf); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="decision-examples.jsonl"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(buf.Bytes())
}

// ---------- encaixes nas telas existentes ----------

// reviewInboxTab é a aba "Revisão" da Caixa de entrada: leva para /revisao com a contagem da fila.
func (s *server) reviewInboxTab(ctx context.Context, co domain.Company) (views.TabLink, bool) {
	b, ok := s.pipesBE()
	if !ok {
		return views.TabLink{}, false
	}
	n, err := b.ReviewQueueCount(ctx, co.ID)
	if err != nil {
		return views.TabLink{}, false
	}
	return views.TabLink{Key: "revisao", Label: i18n.T(ctx, "Revisão"), Href: "/c/" + co.Slug + "/revisao", Count: n}, true
}

// taskCases são os cases ligados à task (painel "Cases" da task).
func (s *server) taskCases(ctx context.Context, co domain.Company, taskID domain.ID) []views.TaskCaseRow {
	b, ok := s.pipesBE()
	if !ok {
		return nil
	}
	cs, err := b.CasesForTask(ctx, co.ID, taskID)
	if err != nil {
		return nil
	}
	base := "/c/" + co.Slug
	out := make([]views.TaskCaseRow, 0, len(cs))
	for _, c := range cs {
		row := views.TaskCaseRow{Identifier: c.Identifier, Title: c.Title, Href: caseHref(base, c.Identifier), Role: linkRoleLabel(ctx, c.Role)}
		if c.PipelineName != "" {
			row.Where = c.PipelineName + " › " + c.StageName
		}
		out = append(out, row)
	}
	return out
}

// gateExample é o "Salvar como exemplo" de um gate decidido.
func (s *server) gateExample(ctx context.Context, co domain.Company, g service.GateView) *views.ExampleFormVM {
	b, ok := s.pipesBE()
	if !ok || !g.Status.Decision() || g.DecidedAt == nil {
		return nil
	}
	base := "/c/" + co.Slug
	f := &views.ExampleFormVM{Action: base + "/exemplos", SourceKind: string(domain.DecisionFromGate), SourceID: g.ID.String(), Next: base + "/gates/" + g.ID.String(),
		SavedHref: base + "/aprendizados?aba=exemplos"}
	id := g.ID
	if xs, err := b.ListDecisionExamples(ctx, co.ID, service.DecisionExampleFilter{SourceKind: domain.DecisionFromGate, SourceID: &id, Limit: 100}); err == nil {
		me := actorFrom(ctx).ID
		for _, x := range xs {
			if sameIDPtr(me, x.CreatedByUserID) {
				f.Saved = true
			}
		}
	}
	return f
}
