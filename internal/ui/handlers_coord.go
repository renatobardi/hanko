package ui

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Coordenação de tasks na UI (ADR-0040): breadcrumb, dependências, subtasks, pausa herdada e política de revisão.

var coordNotices = map[string]string{
	"dependencia": i18n.N("Dependência adicionada."),
	"removida":    i18n.N("Dependência removida."),
	"pausada":     i18n.N("Task pausada (com as subtasks)."),
	"retomada":    i18n.N("Task retomada."),
	"pai":         i18n.N("Task pai atualizada."),
	"politica":    i18n.N("Política de revisão salva."),
}

func init() {
	for k, v := range coordNotices {
		notices[k] = v
	}
	notices["atualizada"] = i18n.N("Task atualizada.")
	inboxNotices["retomada"] = coordNotices["retomada"]
}

func layerLabel(ctx context.Context, l domain.ReviewLayer) string {
	switch l {
	case domain.ReviewLayerTask:
		return i18n.T(ctx, "task")
	case domain.ReviewLayerProject:
		return i18n.T(ctx, "projeto")
	}
	return i18n.T(ctx, "persona")
}

// policyOriginLabel descreve, na execução, a política de revisão que valeu e de que camada veio.
func policyOriginLabel(ctx context.Context, o *domain.RunPolicyOrigin) string {
	if o == nil {
		return ""
	}
	req := i18n.T(ctx, "dispensada")
	if o.ReviewRequired {
		req = i18n.T(ctx, "obrigatória")
	}
	return i18n.Tf(ctx, "%s (origem: %s) · revisor: origem %s", req, layerLabel(ctx, o.ReviewRequiredSource), layerLabel(ctx, o.ReviewerSource))
}

// reviewSummary diz, em uma frase, a política que vale para a task e de onde cada decisão veio.
func reviewSummary(ctx context.Context, p service.ReviewPolicyView, names interface{ personaName(domain.ID) string }) string {
	ef := p.Effective
	req := i18n.T(ctx, "dispensada")
	if ef.Required {
		req = i18n.T(ctx, "obrigatória")
	}
	out := i18n.Tf(ctx, "Revisão %s (origem: %s)", req, layerLabel(ctx, ef.RequiredSource))
	if ef.Required {
		who := i18n.T(ctx, "direto ao gate humano")
		switch {
		case ef.ReviewerPersonaID != nil:
			who = names.personaName(*ef.ReviewerPersonaID)
		case ef.ReviewerAgent != "":
			who = ef.ReviewerAgent
			if ef.ReviewerModel != "" {
				who += " · " + ef.ReviewerModel
			}
		}
		out += " · " + i18n.Tf(ctx, "revisor: %s (origem: %s)", who, layerLabel(ctx, ef.ReviewerSource))
	}
	return out
}

type personaNamer struct{ byID map[domain.ID]string }

func (p personaNamer) personaName(id domain.ID) string {
	if n, ok := p.byID[id]; ok {
		return n
	}
	return id.String()[:8]
}

func requireOptions(ctx context.Context, o domain.ReviewOverride) []views.Option {
	sel := "inherit"
	if o.RequireReview != nil {
		sel = "no"
		if *o.RequireReview {
			sel = "yes"
		}
	}
	return optionList(sel, [][2]string{{"inherit", i18n.T(ctx, "Herdar (projeto/persona)")}, {"yes", i18n.T(ctx, "Exigir")}, {"no", i18n.T(ctx, "Dispensar")}})
}

func reviewerOptions(ctx context.Context, o domain.ReviewOverride, personas []domain.Persona) []views.Option {
	sel := "inherit"
	switch {
	case o.ReviewerMode == domain.ReviewerNone:
		sel = "none"
	case o.ReviewerMode == domain.ReviewerCustom && o.ReviewerPersonaID != nil:
		sel = o.ReviewerPersonaID.String()
	}
	opts := []views.Option{{Value: "inherit", Label: i18n.T(ctx, "Herdar (projeto/persona)"), Selected: sel == "inherit"},
		{Value: "none", Label: i18n.T(ctx, "Nenhum: direto ao gate humano"), Selected: sel == "none"}}
	for _, p := range personas {
		opts = append(opts, views.Option{Value: p.ID.String(), Label: p.Name, Selected: sel == p.ID.String()})
	}
	return opts
}

func coordBadge(ctx context.Context, st domain.TaskStatus) views.Badge {
	return taskBadge(ctx, service.TaskView{Task: domain.Task{Status: st}})
}

func (s *server) coordRowsOf(ctx context.Context, base string, in []service.DependencyView, removeBase string) []views.CoordRow {
	out := make([]views.CoordRow, 0, len(in))
	for _, d := range in {
		row := views.CoordRow{Key: d.Key, Href: base + "/tasks/" + d.Key, Title: d.Title, Badge: coordBadge(ctx, d.Status), Who: d.PersonaName, Closed: d.Closed}
		if removeBase != "" {
			row.RemoveHref = removeBase + "/" + d.Key + "/remover"
		}
		out = append(out, row)
	}
	return out
}

// coordVM monta o que a página da task mostra da coordenação.
func (s *server) coordVM(ctx context.Context, co domain.Company, t service.TaskView, personas []domain.Persona, names lookup, loc *time.Location) (views.TaskCoordVM, error) {
	base := "/c/" + co.Slug
	taskBase := base + "/tasks/" + t.Key
	rel, err := s.be.TaskRelations(ctx, co.ID, t.ID)
	if err != nil {
		return views.TaskCoordVM{}, err
	}
	pol, err := s.be.TaskReviewPolicy(ctx, co.ID, t.ID)
	if err != nil {
		return views.TaskCoordVM{}, err
	}
	c := views.TaskCoordVM{
		Blocked: t.Blocked, OpenDeps: t.OpenDependencies, ChildrenOpn: t.ChildrenOpen,
		DependsOn:      s.coordRowsOf(ctx, base, rel.DependsOn, taskBase+"/dependencias"),
		Dependents:     s.coordRowsOf(ctx, base, rel.Dependents, ""),
		Children:       s.coordRowsOf(ctx, base, rel.Children, ""),
		AddDepHref:     taskBase + "/dependencias",
		ParentHref:     taskBase + "/pai",
		HoldHref:       taskBase + "/pausar",
		ForceCloseHref: taskBase + "/forcar-conclusao",
		NewSubtaskHref: base + "/tasks/nova?pai=" + t.Key,
		CanForceClose:  t.ChildrenOpen > 0 && t.Status.Open(),
	}
	for _, a := range rel.Ancestors {
		c.Crumbs = append(c.Crumbs, views.CoordCrumb{Key: a.Key, Href: base + "/tasks/" + a.Key, Title: a.Title})
	}
	if n := len(rel.Ancestors); n > 0 {
		c.ParentKey = rel.Ancestors[n-1].Key
	}
	if h := t.Hold; h != nil {
		ch := &views.CoordHold{Reason: h.Reason, By: names.actorName(h.By), When: fmtDateTime(ctx, h.At, loc), OriginKey: h.OriginKey,
			OriginHref: base + "/tasks/" + h.OriginKey, Inherited: h.Inherited}
		if !h.Inherited {
			ch.ResumeHref = taskBase + "/retomar"
		}
		c.Hold = ch
	}
	pn := personaNamer{byID: map[domain.ID]string{}}
	for _, p := range personas {
		pn.byID[p.ID] = p.Name
	}
	c.Review = views.CoordReview{Summary: reviewSummary(ctx, pol, pn), CanEdit: true, Action: taskBase + "/politica-revisao",
		RequireOptions: requireOptions(ctx, pol.Task), ReviewerOptions: reviewerOptions(ctx, pol.Task, personas)}
	s.gitCoord(ctx, co, t, &c)
	return c, nil
}

// respondTaskSide devolve o painel lateral atualizado (htmx) ou volta para a task (sem JS).
func (s *server) respondTaskSide(w http.ResponseWriter, r *http.Request, co domain.Company, taskID domain.ID, ok string) {
	base := "/c/" + co.Slug
	t, err := s.be.GetTask(r.Context(), co.ID, taskID.String())
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	if !isHX(r) {
		redirect(w, r, base+"/tasks/"+t.Key+"?ok="+ok)
		return
	}
	vm, err := s.buildTaskVM(r, co, t)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.render(w, r, http.StatusOK, views.TaskSideUpdate(vm, base))
}

func (s *server) taskOf(w http.ResponseWriter, r *http.Request, co domain.Company) (service.TaskView, bool) {
	t, err := s.be.GetTask(r.Context(), co.ID, r.PathValue("task"))
	if err != nil {
		s.fail(w, r, &co, err)
		return service.TaskView{}, false
	}
	return t, true
}

func (s *server) handleAddDependency(w http.ResponseWriter, r *http.Request, co domain.Company) {
	t, ok := s.taskOf(w, r, co)
	if !ok {
		return
	}
	dep, err := s.be.GetTask(r.Context(), co.ID, trimForm(r, "dep"))
	if err != nil {
		s.fail(w, r, &co, domain.Invalid("dep", i18n.T(r.Context(), "task não encontrada")))
		return
	}
	if _, err := s.be.AddTaskDependency(r.Context(), actorFrom(r.Context()), co.ID, t.ID, dep.ID); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.respondTaskSide(w, r, co, t.ID, "dependencia")
}

func (s *server) handleRemoveDependency(w http.ResponseWriter, r *http.Request, co domain.Company) {
	t, ok := s.taskOf(w, r, co)
	if !ok {
		return
	}
	dep, err := s.be.GetTask(r.Context(), co.ID, r.PathValue("dep"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	if _, err := s.be.RemoveTaskDependency(r.Context(), actorFrom(r.Context()), co.ID, t.ID, dep.ID); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.respondTaskSide(w, r, co, t.ID, "removida")
}

func (s *server) handleHold(w http.ResponseWriter, r *http.Request, co domain.Company) {
	t, ok := s.taskOf(w, r, co)
	if !ok {
		return
	}
	if _, err := s.be.HoldTask(r.Context(), actorFrom(r.Context()), co.ID, t.ID, trimForm(r, "reason")); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.respondTaskSide(w, r, co, t.ID, "pausada")
}

func (s *server) handleResume(w http.ResponseWriter, r *http.Request, co domain.Company) {
	t, ok := s.taskOf(w, r, co)
	if !ok {
		return
	}
	if _, err := s.be.ResumeTask(r.Context(), actorFrom(r.Context()), co.ID, t.ID); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.respondTaskSide(w, r, co, t.ID, "retomada")
}

// handleInboxResume retoma, a partir da Caixa, uma raiz de pausa.
func (s *server) handleInboxResume(w http.ResponseWriter, r *http.Request, co domain.Company) {
	id, err := domain.ParseID(r.PathValue("task"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	if _, err := s.be.ResumeTask(r.Context(), actorFrom(r.Context()), co.ID, id); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.inboxDone(w, r, co, "retomada")
}

func (s *server) handleForceClose(w http.ResponseWriter, r *http.Request, co domain.Company) {
	t, ok := s.taskOf(w, r, co)
	if !ok {
		return
	}
	done := domain.TaskDone
	if _, err := s.be.UpdateTask(r.Context(), actorFrom(r.Context()), co.ID, t.ID, service.UpdateTaskInput{
		Status: &done, ForceClose: true, OverrideReason: trimForm(r, "reason")}); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.respondTaskSide(w, r, co, t.ID, "atualizada")
}

func (s *server) handleSetParent(w http.ResponseWriter, r *http.Request, co domain.Company) {
	t, ok := s.taskOf(w, r, co)
	if !ok {
		return
	}
	in := service.UpdateTaskInput{}
	if ref := trimForm(r, "parent"); ref == "" {
		in.ClearParent = true
	} else {
		p, err := s.be.GetTask(r.Context(), co.ID, ref)
		if err != nil {
			s.fail(w, r, &co, domain.Invalid("parent", i18n.T(r.Context(), "task não encontrada")))
			return
		}
		in.ParentID = &p.ID
	}
	if _, err := s.be.UpdateTask(r.Context(), actorFrom(r.Context()), co.ID, t.ID, in); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.respondTaskSide(w, r, co, t.ID, "pai")
}

func (s *server) handleReviewPolicy(w http.ResponseWriter, r *http.Request, co domain.Company) {
	t, ok := s.taskOf(w, r, co)
	if !ok {
		return
	}
	o := domain.ReviewOverride{ReviewerMode: domain.ReviewerInherit}
	switch trimForm(r, "require") {
	case "yes":
		v := true
		o.RequireReview = &v
	case "no":
		v := false
		o.RequireReview = &v
	}
	switch rv := trimForm(r, "reviewer"); rv {
	case "", "inherit":
	case "none":
		o.ReviewerMode = domain.ReviewerNone
	default:
		id, err := domain.ParseID(rv)
		if err != nil {
			s.fail(w, r, &co, domain.Invalid("reviewer", i18n.T(r.Context(), "id inválido")))
			return
		}
		o.ReviewerMode, o.ReviewerPersonaID = domain.ReviewerCustom, &id
	}
	if _, err := s.be.SetTaskReviewPolicy(r.Context(), actorFrom(r.Context()), co.ID, t.ID, o); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.respondTaskSide(w, r, co, t.ID, "politica")
}

// parseTaskKeys separa chaves por vírgula/espaço e resolve cada uma; uma chave desconhecida é erro de campo.
func (s *server) parseTaskKeys(ctx context.Context, co domain.Company, raw string) ([]domain.ID, error) {
	var out []domain.ID
	for _, f := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' || r == ' ' }) {
		t, err := s.be.GetTask(ctx, co.ID, f)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return nil, domain.Invalid("depends_on", i18n.Tf(ctx, "task %s não encontrada", f))
			}
			return nil, err
		}
		out = append(out, t.ID)
	}
	return out, nil
}
