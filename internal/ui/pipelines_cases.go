package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Cases na UI (ADR-0034): lista plana ou em árvore, e a página do case com revisão, sugestão, tasks e histórico.

var caseNotices = map[string]string{
	"criado":     i18n.N("Case criado."),
	"salvo":      i18n.N("Case atualizado."),
	"movido":     i18n.N("Case movido."),
	"revisado":   i18n.N("Revisão registrada: o case foi para a etapa configurada."),
	"sugestao":   i18n.N("Sugestão resolvida."),
	"atencao":    i18n.N("Item dispensado da fila."),
	"conversa":   i18n.N("Conversa aberta numa task ligada ao case."),
	"ligada":     i18n.N("Task ligada."),
	"desligada":  i18n.N("Task desligada."),
	"bloqueios":  i18n.N("Bloqueios salvos."),
	"exemplo":    i18n.N("Exemplo de decisão salvo."),
	"atualizado": i18n.N("Case atualizado (mesmo tipo e key)."),
}

// parseKV lê "chave = valor" por linha (os campos do case).
func parseKV(raw string) (map[string]any, error) {
	out := map[string]any{}
	for i, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, domain.Invalid("fields", fmt.Sprintf("linha %d: use chave = valor", i+1))
		}
		out[k] = strings.TrimSpace(v)
	}
	return out, nil
}

func fieldsKV(f map[string]any) string {
	keys := make([]string, 0, len(f))
	for k := range f {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s = %s\n", k, strings.ReplaceAll(numOrText(f[k]), "\n", " "))
	}
	return strings.TrimRight(b.String(), "\n")
}

// ---------- lista ----------

func (s *server) handleCases(w http.ResponseWriter, r *http.Request, co domain.Company) {
	s.renderCases(w, r, co, views.CaseNewFormVM{}, http.StatusOK)
}

func (s *server) renderCases(w http.ResponseWriter, r *http.Request, co domain.Company, form views.CaseNewFormVM, status int) {
	ctx := r.Context()
	b := s.pb()
	base := "/c/" + co.Slug
	q := r.URL.Query()
	loc, now := loadLoc(co.Timezone), s.now()
	tree := q.Get("visao") == "arvore"
	f := service.CaseFilter{Q: strings.TrimSpace(q.Get("q")), Type: strings.TrimSpace(q.Get("tipo")), Limit: 500}
	if st := q.Get("status"); st != "" {
		f.Statuses = []domain.CaseStatus{domain.CaseStatus(st)}
	}
	pipes, err := b.ListPipelines(ctx, co.ID, true)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	pipeRef := q.Get("pipeline")
	switch pipeRef {
	case "":
	case "-":
		f.LooseOnly = true
	default:
		for _, p := range pipes {
			if p.Key == pipeRef {
				f.PipelineID = &p.ID
			}
		}
		if f.PipelineID == nil {
			s.fail(w, r, &co, domain.ErrNotFound)
			return
		}
	}
	projects, err := s.be.ListProjects(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	projRef := q.Get("projeto")
	for _, p := range projects {
		if projRef != "" && p.Slug == projRef {
			f.ProjectID = &p.ID
		}
	}
	all, err := b.ListCases(ctx, co.ID, service.CaseFilter{Limit: 1})
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	list, err := b.ListCases(ctx, co.ID, f)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	sortBy := q.Get("ordenar")
	switch sortBy {
	case "titulo":
		sort.SliceStable(list, func(i, j int) bool { return strings.ToLower(list[i].Title) < strings.ToLower(list[j].Title) })
	case "criado":
		sort.SliceStable(list, func(i, j int) bool { return list[i].CreatedAt.After(list[j].CreatedAt) })
	}
	row := func(c service.CaseView, depth int) views.CaseRow {
		cr := views.CaseRow{Identifier: c.Identifier, Title: c.Title, Href: caseHref(base, c.Identifier), Type: c.Type, Badge: caseBadge(ctx, c.Status),
			Project: c.ProjectName, Updated: agoCtx(ctx, c.UpdatedAt, now, loc), Depth: depth, Flags: caseFlags(ctx, c)}
		if c.PipelineKey != "" {
			cr.Where, cr.WhereHref = c.PipelineName+" › "+c.StageName, base+"/pipelines/"+c.PipelineKey
		} else {
			cr.Where = i18n.T(ctx, "solto")
		}
		if c.ChildCount > 0 {
			cr.Children = i18n.Tf(ctx, "%d de %d filhos terminaram", c.TerminalChildCount, c.ChildCount)
		}
		return cr
	}
	vm := views.CasesVM{Notice: i18n.T(ctx, caseNotices[q.Get("ok")]), Empty: len(all) == 0, Tree: tree, Q: f.Q, Total: len(list)}
	if tree {
		byParent := map[domain.ID][]service.CaseView{}
		inList := map[domain.ID]bool{}
		for _, c := range list {
			inList[c.ID] = true
		}
		var roots []service.CaseView
		for _, c := range list {
			if c.ParentCaseID != nil && inList[*c.ParentCaseID] {
				byParent[*c.ParentCaseID] = append(byParent[*c.ParentCaseID], c)
			} else {
				roots = append(roots, c)
			}
		}
		var rows []views.CaseRow
		var walk func(c service.CaseView, depth int)
		walk = func(c service.CaseView, depth int) {
			rows = append(rows, row(c, depth))
			if depth >= 6 {
				return
			}
			for _, k := range byParent[c.ID] {
				walk(k, depth+1)
			}
		}
		for _, c := range roots {
			walk(c, 0)
		}
		vm.Groups = []views.CaseGroup{{Rows: rows}}
	} else {
		group := q.Get("agrupar")
		order := []string{}
		groups := map[string][]views.CaseRow{}
		for _, c := range list {
			label := ""
			switch group {
			case "status":
				label = caseStatusLabel(ctx, c.Status)
			case "pipeline":
				label = c.PipelineName
				if label == "" {
					label = i18n.T(ctx, "Soltos")
				}
			case "tipo":
				label = c.Type
				if label == "" {
					label = i18n.T(ctx, "Sem tipo")
				}
			}
			if _, ok := groups[label]; !ok {
				order = append(order, label)
			}
			groups[label] = append(groups[label], row(c, 0))
		}
		for _, l := range order {
			vm.Groups = append(vm.Groups, views.CaseGroup{Label: l, Rows: groups[l]})
		}
	}
	// filtros
	vm.StatusOptions = []views.Option{{Value: "", Label: i18n.T(ctx, "Todos os status")}}
	for _, st := range domain.CaseStatuses {
		vm.StatusOptions = append(vm.StatusOptions, views.Option{Value: string(st), Label: caseStatusLabel(ctx, st), Selected: q.Get("status") == string(st)})
	}
	types := map[string]bool{}
	tl, _ := b.ListCases(ctx, co.ID, service.CaseFilter{Limit: 500})
	for _, c := range tl {
		if c.Type != "" {
			types[c.Type] = true
		}
	}
	vm.TypeOptions = []views.Option{{Value: "", Label: i18n.T(ctx, "Todos os tipos")}}
	for _, t := range sortedStrings(types) {
		vm.TypeOptions = append(vm.TypeOptions, views.Option{Value: t, Label: t, Selected: f.Type == t})
	}
	vm.PipelineOptions = []views.Option{{Value: "", Label: i18n.T(ctx, "Todos os pipelines")}, {Value: "-", Label: i18n.T(ctx, "Soltos"), Selected: pipeRef == "-"}}
	for _, p := range pipes {
		vm.PipelineOptions = append(vm.PipelineOptions, views.Option{Value: p.Key, Label: p.Name, Selected: p.Key == pipeRef})
	}
	vm.ProjectOptions = []views.Option{{Value: "", Label: i18n.T(ctx, "Todos os projetos")}}
	for _, p := range projects {
		vm.ProjectOptions = append(vm.ProjectOptions, views.Option{Value: p.Slug, Label: p.Name, Selected: p.Slug == projRef})
	}
	for _, g := range []struct{ v, l string }{{"", i18n.N("Sem agrupar")}, {"status", i18n.N("Agrupar por status")}, {"pipeline", i18n.N("Agrupar por pipeline")}, {"tipo", i18n.N("Agrupar por tipo")}} {
		vm.GroupOptions = append(vm.GroupOptions, views.Option{Value: g.v, Label: i18n.T(ctx, g.l), Selected: q.Get("agrupar") == g.v})
	}
	for _, o := range []struct{ v, l string }{{"", i18n.N("Mais recentes")}, {"criado", i18n.N("Criados por último")}, {"titulo", i18n.N("Título")}} {
		vm.SortOptions = append(vm.SortOptions, views.Option{Value: o.v, Label: i18n.T(ctx, o.l), Selected: sortBy == o.v})
	}
	qs := url.Values{}
	for k, v := range q {
		if k != "visao" && k != "ok" && len(v) > 0 && v[0] != "" {
			qs.Set(k, v[0])
		}
	}
	vm.ListHref = base + "/cases?" + qs.Encode()
	qs.Set("visao", "arvore")
	vm.TreeHref = base + "/cases?" + qs.Encode()
	form.Action = base + "/cases"
	form.StatusOptions = nil
	for _, st := range domain.CaseStatuses {
		form.StatusOptions = append(form.StatusOptions, views.Option{Value: string(st), Label: caseStatusLabel(ctx, st), Selected: st == domain.CaseDraft})
	}
	form.PipelineOptions = []views.Option{{Value: "", Label: i18n.T(ctx, "Nenhum (case solto)")}}
	for _, p := range pipes {
		if p.ArchivedAt == nil {
			form.PipelineOptions = append(form.PipelineOptions, views.Option{Value: p.Key, Label: p.Name})
		}
	}
	vm.Form = form
	sh := s.shell(r, co, "cases", i18n.T(ctx, "Cases"))
	s.render(w, r, status, views.CasesPage(sh, vm))
}

func sortedStrings(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (s *server) handleCreateCase(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	form := views.CaseNewFormVM{Title: trimForm(r, "title"), Type: trimForm(r, "case_type"), Key: trimForm(r, "key"), Summary: r.PostFormValue("summary"),
		Fields: r.PostFormValue("fields")}
	fields, err := parseKV(form.Fields)
	if err == nil {
		in := service.CreateCaseInput{Title: form.Title, Type: form.Type, Key: form.Key, Summary: form.Summary, Fields: fields}
		if p := trimForm(r, "pipeline"); p != "" {
			var pv service.PipelineView
			if pv, err = s.pb().GetPipeline(ctx, co.ID, p); err == nil {
				in.PipelineID = &pv.ID
				in.Fields = typedFields(pv, fields)
			}
		} else {
			in.Status = domain.CaseStatus(trimForm(r, "status"))
		}
		if err == nil {
			var cv service.CaseView
			var created bool
			if cv, created, err = s.pb().CreateCase(ctx, actorFrom(ctx), co.ID, in); err == nil {
				ok := "criado"
				if !created {
					ok = "atualizado"
				}
				redirect(w, r, caseHref("/c/"+co.Slug, cv.Identifier)+"?ok="+ok)
				return
			}
		}
	}
	form.Error = pipelineErr(ctx, err)
	s.renderCases(w, r, co, form, statusFor(err))
}

// ---------- página do case ----------

func (s *server) handleCase(w http.ResponseWriter, r *http.Request, co domain.Company) {
	s.renderCase(w, r, co, r.PathValue("case"), "", http.StatusOK)
}

func (s *server) renderCase(w http.ResponseWriter, r *http.Request, co domain.Company, ref, errMsg string, status int) {
	ctx := r.Context()
	b := s.pb()
	cv, err := b.GetCaseView(ctx, co.ID, ref)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	base := "/c/" + co.Slug
	href := caseHref(base, cv.Identifier)
	loc, now := loadLoc(co.Timezone), s.now()
	personas, err := s.be.ListPersonas(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	names := s.actorNames(ctx, co.ID, personas)
	vm := views.CaseVM{Identifier: cv.Identifier, Title: cv.Title, Type: cv.Type, Key: cv.Key, SummaryRaw: cv.Summary, HasSummary: strings.TrimSpace(cv.Summary) != "",
		Summary: renderMD(cv.Summary), Badge: caseBadge(ctx, cv.Status), Version: cv.Version, Notice: i18n.T(ctx, caseNotices[r.URL.Query().Get("ok")]), Error: errMsg,
		PipelineName: cv.PipelineName, StageName: cv.StageName, StageKind: string(cv.StageKind), ProjectName: cv.ProjectName,
		Created: fmtDateTime(ctx, cv.CreatedAt, loc), Updated: fmtDateTime(ctx, cv.UpdatedAt, loc), FieldsText: fieldsKV(cv.Fields), Base: base,
		EditAction: href, MoveAction: href + "/mover", AttentionAction: href + "/atencao", ConversationAction: href + "/conversa", LinkAction: href + "/tasks",
		BlockersAction: href + "/bloqueios", ReapprovalRequired: cv.ReapprovalRequired, Closed: cv.TerminalKind != "", Loose: cv.PipelineID == nil}
	if cv.PipelineKey != "" {
		vm.PipelineHref = base + "/pipelines/" + cv.PipelineKey
	}
	if cv.ProjectSlug != "" {
		vm.ProjectHref = base + "/projetos/" + cv.ProjectSlug
	}
	if cv.ParentIdentifier != "" {
		vm.ParentID, vm.ParentTitle, vm.ParentHref = cv.ParentIdentifier, cv.ParentTitle, caseHref(base, cv.ParentIdentifier)
	}
	switch cv.Attention {
	case "blockers_resolved":
		vm.Attention = i18n.T(ctx, "Os bloqueios deste case foram resolvidos.")
	case "children_terminal":
		vm.Attention = i18n.T(ctx, "Todos os cases filhos terminaram.")
	}
	var pv *service.PipelineView
	var stage *domain.Stage
	if cv.PipelineID != nil {
		p, err := b.GetPipeline(ctx, co.ID, cv.PipelineID.String())
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		pv = &p
		vm.Strict = p.EnforceTransitions
		if st, ok := p.StageByID(*cv.StageID); ok {
			stage = &st
		}
		for _, st := range p.Stages {
			if st.ID == *cv.StageID {
				continue
			}
			label := st.Name
			if st.Config.Disabled {
				label += " (" + i18n.T(ctx, "desligada") + ")"
			}
			vm.MoveOptions = append(vm.MoveOptions, views.Option{Value: st.Key, Label: label})
		}
	}
	// campos: os da etapa primeiro (com o rótulo), depois os demais
	seen := map[string]bool{}
	if stage != nil {
		for _, f := range stage.Config.Fields {
			seen[f.Key] = true
			vm.Fields = append(vm.Fields, views.CaseFieldRow{Key: f.Key, Label: f.Label, Value: numOrText(cv.Fields[f.Key]), Required: f.Required})
		}
	}
	keys := make([]string, 0, len(cv.Fields))
	for k := range cv.Fields {
		if !seen[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		vm.Fields = append(vm.Fields, views.CaseFieldRow{Key: k, Label: k, Value: numOrText(cv.Fields[k])})
	}
	for _, l := range cv.Links {
		row := views.CaseLinkRow{Key: l.TaskKey, Title: l.TaskTitle, Href: base + "/tasks/" + l.TaskKey, Role: linkRoleLabel(ctx, l.Role), Status: string(l.TaskStatus),
			Persona: l.PersonaName}
		row.UnlinkAction = href + "/tasks/" + url.PathEscape(l.TaskID.String()) + "/desligar"
		vm.Links = append(vm.Links, row)
	}
	kids, err := b.ListCases(ctx, co.ID, service.CaseFilter{ParentID: &cv.ID, Limit: 200})
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	for _, k := range kids {
		vm.Children = append(vm.Children, views.CaseRow{Identifier: k.Identifier, Title: k.Title, Href: caseHref(base, k.Identifier), Badge: caseBadge(ctx, k.Status)})
	}
	if cv.ChildCount > 0 {
		vm.ChildrenText = i18n.Tf(ctx, "%d de %d filhos terminaram", cv.TerminalChildCount, cv.ChildCount)
	}
	var bl []string
	for _, x := range cv.Blockers {
		vm.Blockers = append(vm.Blockers, views.CaseRefRow{Identifier: x.Identifier, Title: x.Title, Href: caseHref(base, x.Identifier), Resolved: x.Resolved})
		bl = append(bl, x.Identifier)
	}
	vm.BlockersText = strings.Join(bl, ", ")
	for _, x := range cv.Blocking {
		vm.Blocking = append(vm.Blocking, views.CaseRefRow{Identifier: x.Identifier, Title: x.Title, Href: caseHref(base, x.Identifier)})
	}
	if vm.Loose {
		for _, st := range domain.CaseStatuses {
			vm.StatusOptions = append(vm.StatusOptions, views.Option{Value: string(st), Label: caseStatusLabel(ctx, st), Selected: st == cv.Status})
		}
	}
	// revisão
	if stage != nil && stage.Kind == domain.StageReview && cv.TerminalKind == "" && pv != nil {
		rt := stage.Config.Review
		rv := views.CaseReviewVM{Action: href + "/revisar", Version: cv.Version, RequireRejectReason: rt.RejectReasonRequired(), RequireChanges: rt.ChangesReasonRequired(),
			WhatHappensHere: stage.Config.WhatHappensHere, CanDecide: s.canDecideStage(ctx, co.ID, stage.Config)}
		if rt != nil {
			if st, ok := pv.Stage(rt.ApproveTo); ok {
				rv.ApproveTo = st.Name
			}
			if st, ok := pv.Stage(rt.RejectTo); ok {
				rv.RejectTo = st.Name
			}
			if st, ok := pv.Stage(rt.RequestChangesTo); ok {
				rv.ChangesTo = st.Name
			}
		}
		vm.Review = &rv
	}
	if sg := cv.PendingSuggestion; sg != nil {
		sv := views.CaseSuggestionVM{Action: href + "/sugestao", ToStage: sg.ToStage, Rationale: sg.Rationale, Confidence: fmt.Sprintf("%d%%", int(sg.Confidence*100+0.5)),
			When: agoCtx(ctx, sg.At, now, loc), Version: cv.Version, InReview: stage != nil && stage.Kind == domain.StageReview}
		if pv != nil {
			if st, ok := pv.Stage(sg.ToStage); ok {
				sv.ToStage = st.Name
			}
		}
		sv.Who = "Hanko"
		if sg.PersonaID != nil {
			sv.Who = names.of(ctx, domain.PersonaActor(*sg.PersonaID))
		}
		vm.Suggestion = &sv
	}
	// histórico do mais novo ao mais antigo; decisões de revisão podem virar exemplo
	saved := s.savedExamples(ctx, co.ID, domain.DecisionFromCaseReview)
	for i := len(cv.Events) - 1; i >= 0; i-- {
		e := cv.Events[i]
		row := caseEventRow(ctx, e, names, now, loc)
		if e.Kind == domain.CaseEvReviewDecided && e.Actor.Type == domain.ActorHuman {
			row.Example = &views.ExampleFormVM{Action: base + "/exemplos", SourceKind: string(domain.DecisionFromCaseReview), SourceID: e.ID.String(), Next: href,
				Saved: saved[e.ID], SavedHref: base + "/aprendizados?aba=exemplos"}
		}
		vm.Events = append(vm.Events, row)
	}
	crumbs := []views.Crumb{{Label: i18n.T(ctx, "Cases"), Href: base + "/cases"}}
	if cv.PipelineKey != "" {
		crumbs = []views.Crumb{{Label: i18n.T(ctx, "Pipelines"), Href: base + "/pipelines"}, {Label: cv.PipelineName, Href: vm.PipelineHref}}
	}
	crumbs = append(crumbs, views.Crumb{Label: cv.Identifier})
	active := "cases"
	if cv.PipelineKey != "" {
		active = "pipelines"
	}
	sh := s.shell(r, co, active, cv.Title, crumbs...)
	s.render(w, r, status, views.CasePage(sh, vm))
}

// canDecideStage diz se o usuário pode decidir a revisão (só para esconder os botões; o service confere).
func (s *server) canDecideStage(ctx context.Context, companyID domain.ID, c domain.StageConfig) bool {
	role := views.RoleFrom(ctx)
	if !role.AtLeast(domain.MemberMember) {
		return false
	}
	if !c.ApprovalRequired() {
		return true
	}
	ref, err := domain.ParseApprover(c.Approval.Approver)
	if err != nil {
		return false
	}
	switch {
	case ref.AnyMember:
		return true
	case ref.UserID != nil:
		a := actorFrom(ctx)
		return (a.ID != nil && *a.ID == *ref.UserID) || role.AtLeast(domain.MemberAdmin)
	}
	return role.AtLeast(domain.MemberAdmin)
}

// savedExamples são as decisões que o usuário já salvou como exemplo (por origem).
func (s *server) savedExamples(ctx context.Context, companyID domain.ID, kind domain.DecisionSource) map[domain.ID]bool {
	out := map[domain.ID]bool{}
	xs, err := s.pb().ListDecisionExamples(ctx, companyID, service.DecisionExampleFilter{SourceKind: kind, Limit: 500})
	if err != nil {
		return out
	}
	me := actorFrom(ctx).ID
	for _, x := range xs {
		if sameIDPtr(me, x.CreatedByUserID) {
			out[x.SourceID] = true
		}
	}
	return out
}

func sameIDPtr(a, b *domain.ID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// ---------- ações do case ----------

func formVersion(r *http.Request) int {
	v, _ := strconv.Atoi(trimForm(r, "expected_version"))
	return v
}

func (s *server) caseID(w http.ResponseWriter, r *http.Request, co domain.Company) (service.CaseView, bool) {
	cv, err := s.pb().GetCaseView(r.Context(), co.ID, r.PathValue("case"))
	if err != nil {
		s.fail(w, r, &co, err)
		return cv, false
	}
	return cv, true
}

// caseDone volta para o case (ou para next, quando é uma tela desta funcionalidade).
func (s *server) caseDone(w http.ResponseWriter, r *http.Request, co domain.Company, identifier, ok string) {
	base := "/c/" + co.Slug
	to := caseHref(base, identifier) + "?ok=" + ok
	if trimForm(r, "next") == "revisao" {
		to = base + "/revisao?ok=" + ok
	}
	redirect(w, r, to)
}

// caseFail mostra o erro na página do case (ou na fila, quando veio dela).
func (s *server) caseFail(w http.ResponseWriter, r *http.Request, co domain.Company, cv service.CaseView, err error) {
	if isHX(r) {
		s.fail(w, r, &co, fmt.Errorf("%s: %w", pipelineErr(r.Context(), err), err))
		return
	}
	if trimForm(r, "next") == "revisao" {
		s.renderReviewQueue(w, r, co, pipelineErr(r.Context(), err), nil, statusFor(err))
		return
	}
	s.renderCase(w, r, co, cv.ID.String(), pipelineErr(r.Context(), err), statusFor(err))
}

func (s *server) handleUpdateCase(w http.ResponseWriter, r *http.Request, co domain.Company) {
	cv, ok := s.caseID(w, r, co)
	if !ok {
		return
	}
	ctx := r.Context()
	title, summary := trimForm(r, "title"), r.PostFormValue("summary")
	fields, err := parseKV(r.PostFormValue("fields"))
	if err != nil {
		s.caseFail(w, r, co, cv, err)
		return
	}
	// apagar a linha remove o campo: o editor manda o mapa inteiro
	if cv.PipelineID != nil {
		if pv, err := s.pb().GetPipeline(ctx, co.ID, cv.PipelineID.String()); err == nil {
			fields = typedFields(pv, fields)
		}
	}
	up := service.UpdateCaseInput{ExpectedVersion: formVersion(r), Title: &title, Summary: &summary, Fields: fields, ReplaceFields: true}
	if st := trimForm(r, "status"); st != "" && cv.PipelineID == nil {
		cs := domain.CaseStatus(st)
		up.Status = &cs
	}
	if _, err := s.pb().UpdateCase(ctx, actorFrom(ctx), co.ID, cv.ID, up); err != nil {
		s.caseFail(w, r, co, cv, err)
		return
	}
	s.caseDone(w, r, co, cv.Identifier, "salvo")
}

func (s *server) handleMoveCase(w http.ResponseWriter, r *http.Request, co domain.Company) {
	cv, ok := s.caseID(w, r, co)
	if !ok {
		return
	}
	ctx := r.Context()
	out, err := s.pb().TransitionCase(ctx, actorFrom(ctx), co.ID, cv.ID, service.TransitionCaseInput{ExpectedVersion: formVersion(r), ToStage: trimForm(r, "to_stage"),
		Force: checked(r, "force"), Reason: trimForm(r, "reason")})
	if err != nil {
		s.caseFail(w, r, co, cv, err)
		return
	}
	if isHX(r) {
		w.Header().Set("X-Case-Version", strconv.Itoa(out.Version))
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.caseDone(w, r, co, cv.Identifier, "movido")
}

func (s *server) handleReviewCase(w http.ResponseWriter, r *http.Request, co domain.Company) {
	cv, ok := s.caseID(w, r, co)
	if !ok {
		return
	}
	ctx := r.Context()
	if _, err := s.pb().ReviewCase(ctx, actorFrom(ctx), co.ID, cv.ID, service.ReviewCaseInput{ExpectedVersion: formVersion(r),
		Decision: domain.ReviewDecision(trimForm(r, "decision")), Reason: trimForm(r, "reason")}); err != nil {
		s.caseFail(w, r, co, cv, err)
		return
	}
	s.caseDone(w, r, co, cv.Identifier, "revisado")
}

func (s *server) handleResolveSuggestion(w http.ResponseWriter, r *http.Request, co domain.Company) {
	cv, ok := s.caseID(w, r, co)
	if !ok {
		return
	}
	ctx := r.Context()
	if _, err := s.pb().ResolveSuggestion(ctx, actorFrom(ctx), co.ID, cv.ID, service.ResolveSuggestionInput{ExpectedVersion: formVersion(r),
		Accept: trimForm(r, "accept") == "1", Reason: trimForm(r, "reason")}); err != nil {
		s.caseFail(w, r, co, cv, err)
		return
	}
	s.caseDone(w, r, co, cv.Identifier, "sugestao")
}

func (s *server) handleDismissAttention(w http.ResponseWriter, r *http.Request, co domain.Company) {
	cv, ok := s.caseID(w, r, co)
	if !ok {
		return
	}
	ctx := r.Context()
	if _, err := s.pb().UpdateCase(ctx, actorFrom(ctx), co.ID, cv.ID, service.UpdateCaseInput{ExpectedVersion: formVersion(r), ClearAttention: true}); err != nil {
		s.caseFail(w, r, co, cv, err)
		return
	}
	s.caseDone(w, r, co, cv.Identifier, "atencao")
}

func (s *server) handleCaseConversation(w http.ResponseWriter, r *http.Request, co domain.Company) {
	cv, ok := s.caseID(w, r, co)
	if !ok {
		return
	}
	ctx := r.Context()
	tv, err := s.pb().OpenCaseConversation(ctx, actorFrom(ctx), co.ID, cv.ID)
	if err != nil {
		s.caseFail(w, r, co, cv, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/tasks/"+tv.Key)
}

func (s *server) handleLinkCaseTask(w http.ResponseWriter, r *http.Request, co domain.Company) {
	cv, ok := s.caseID(w, r, co)
	if !ok {
		return
	}
	ctx := r.Context()
	tv, err := s.be.GetTask(ctx, co.ID, trimForm(r, "task"))
	if err == nil {
		_, err = s.pb().LinkCaseTask(ctx, actorFrom(ctx), co.ID, cv.ID, tv.ID, domain.LinkWork, nil)
	}
	if err != nil {
		s.caseFail(w, r, co, cv, err)
		return
	}
	s.caseDone(w, r, co, cv.Identifier, "ligada")
}

func (s *server) handleUnlinkCaseTask(w http.ResponseWriter, r *http.Request, co domain.Company) {
	cv, ok := s.caseID(w, r, co)
	if !ok {
		return
	}
	ctx := r.Context()
	tid, err := domain.ParseID(r.PathValue("task"))
	if err == nil {
		_, err = s.pb().UnlinkCaseTask(ctx, actorFrom(ctx), co.ID, cv.ID, tid)
	}
	if err != nil {
		s.caseFail(w, r, co, cv, err)
		return
	}
	s.caseDone(w, r, co, cv.Identifier, "desligada")
}

func (s *server) handleCaseBlockers(w http.ResponseWriter, r *http.Request, co domain.Company) {
	cv, ok := s.caseID(w, r, co)
	if !ok {
		return
	}
	ctx := r.Context()
	var ids []domain.ID
	for _, ref := range strings.Split(r.PostFormValue("blockers"), ",") {
		if ref = strings.TrimSpace(ref); ref == "" {
			continue
		}
		b, err := s.pb().GetCaseView(ctx, co.ID, ref)
		if err != nil {
			s.caseFail(w, r, co, cv, domain.Invalid("blockers", i18n.Tf(ctx, "case %s não encontrado", ref)))
			return
		}
		ids = append(ids, b.ID)
	}
	if _, err := s.pb().SetCaseBlockers(ctx, actorFrom(ctx), co.ID, cv.ID, formVersion(r), ids, nil); err != nil {
		s.caseFail(w, r, co, cv, err)
		return
	}
	s.caseDone(w, r, co, cv.Identifier, "bloqueios")
}
