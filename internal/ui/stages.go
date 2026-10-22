package ui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Etapas do projeto (ADR-0048): a aba "Etapas" do projeto, o painel da task e os cards de status no Painel. O service
// é a barreira de papéis e de regras; a UI só esconde os controles. O backend é uma interface opcional (como os de
// Git e de pipelines de cases): os fakes de teste que não a usam continuam valendo.

type stagesBackend interface {
	ProjectStages(ctx context.Context, companyID domain.ID, projectRef string) (service.ProjectStagesView, error)
	SetProjectStages(ctx context.Context, actor domain.Actor, companyID domain.ID, projectRef string, specs []domain.StageSpec) (service.ProjectStagesView, error)
	ClearProjectStages(ctx context.Context, actor domain.Actor, companyID domain.ID, projectRef string) error
	StageLearnings(ctx context.Context, companyID domain.ID, projectRef, stageKey string, limit int) ([]domain.StageLearning, error)
	TaskStage(ctx context.Context, companyID domain.ID, taskRef string) (service.TaskStageView, error)
	StartTaskStage(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID, stageKey string) (service.TaskStageView, error)
	CompleteTaskStage(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID, note string) (service.TaskStageView, error)
	AdvanceTaskStage(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID, in service.AdvanceStageInput) (service.TaskStageView, error)
	LeaveTaskStage(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID, note string) error
	DashboardStatusCards(ctx context.Context, companyID domain.ID) ([]service.DashboardCard, error)
}

func (s *server) stagesBE() (stagesBackend, bool) {
	b, ok := s.be.(stagesBackend)
	return b, ok
}

// blankStageRows é quantas linhas em branco o formulário oferece para acrescentar etapas.
const blankStageRows = 3

func init() {
	for k, v := range map[string]string{
		"etapas-salvas":   i18n.N("Etapas do projeto salvas."),
		"etapas-limpas":   i18n.N("Pipeline do projeto desligado."),
		"etapa-concluida": i18n.N("Etapa concluída."),
		"etapa-movida":    i18n.N("Task movida de etapa."),
		"etapa-entrou":    i18n.N("Task no pipeline do projeto."),
		"etapa-saiu":      i18n.N("Task fora do pipeline do projeto."),
	} {
		notices[k] = v
		projectNotices[k] = v
	}
}

func (s *server) stageRoutes(mux langMux) {
	co := func(h companyHandler) http.HandlerFunc { return s.withCompany(h) }
	post := func(h http.HandlerFunc) http.HandlerFunc { return s.sameOrigin(h) }
	mux.HandleFunc("POST /c/{company}/projetos/{project}/etapas", post(co(s.handleSaveProjectStages)))
	mux.HandleFunc("POST /c/{company}/tasks/{task}/etapa/entrar", post(co(s.handleTaskStageStart)))
	mux.HandleFunc("POST /c/{company}/tasks/{task}/etapa/concluir", post(co(s.handleTaskStageComplete)))
	mux.HandleFunc("POST /c/{company}/tasks/{task}/etapa/mover", post(co(s.handleTaskStageMove)))
	mux.HandleFunc("POST /c/{company}/tasks/{task}/etapa/sair", post(co(s.handleTaskStageLeave)))
}

func stageStatusOptions(ctx context.Context, sel domain.TaskStatus) []views.Option {
	var out []views.Option
	for _, st := range []domain.TaskStatus{domain.TaskTodo, domain.TaskInProgress, domain.TaskInReview} {
		out = append(out, views.Option{Value: string(st), Label: taskStatusLabel(ctx, st), Selected: st == sel})
	}
	return out
}

func stageGateLabel(ctx context.Context, g domain.StageExitGate) string {
	switch g {
	case domain.StageGateApproval:
		return i18n.T(ctx, "aprovação humana")
	case domain.StageGateReview:
		return i18n.T(ctx, "revisão da task")
	}
	return i18n.T(ctx, "nenhum (avança sozinha)")
}

func stageGateOptions(ctx context.Context, sel domain.StageExitGate) []views.Option {
	var out []views.Option
	for _, g := range []domain.StageExitGate{domain.StageGateNone, domain.StageGateApproval, domain.StageGateReview} {
		out = append(out, views.Option{Value: string(g), Label: stageGateLabel(ctx, g), Selected: g == sel})
	}
	return out
}

func stageDecisionClass(d string) string {
	switch d {
	case string(domain.GateApproved):
		return "done"
	case string(domain.GateRejected):
		return "danger"
	}
	return "warn"
}

func (s *server) stagePersonaOptions(ctx context.Context, co domain.Company, sel *domain.ID) []views.Option {
	personas, _ := s.be.ListPersonas(ctx, co.ID)
	return personaOptions(personas, sel, i18n.T(ctx, "Sem persona (etapa humana)"))
}

func (s *server) learningRows(ctx context.Context, co domain.Company, ls []domain.StageLearning) []views.StageLearningRow {
	loc := loadLoc(co.Timezone)
	out := make([]views.StageLearningRow, 0, len(ls))
	for _, l := range ls {
		row := views.StageLearningRow{When: fmtDateTime(ctx, l.CreatedAt, loc), Stage: l.StageKey, TaskKey: l.TaskKey,
			Decision: domain.LearningDecisionLabel(l.Decision), Class: stageDecisionClass(l.Decision), Note: l.Note}
		if l.TaskKey != "" {
			row.TaskHref = "/c/" + co.Slug + "/tasks/" + l.TaskKey
		}
		row.Decision = i18n.T(ctx, row.Decision)
		out = append(out, row)
	}
	return out
}

// projectStagesVM monta a aba Etapas do projeto: as etapas existentes e linhas em branco para acrescentar.
func (s *server) projectStagesVM(r *http.Request, co domain.Company, p service.ProjectView) views.ProjectStagesVM {
	ctx := r.Context()
	vm := views.ProjectStagesVM{Action: "/c/" + co.Slug + "/projetos/" + p.Slug + "/etapas", CanEdit: views.RoleFrom(ctx).AtLeast(domain.MemberAdmin) && p.Status != domain.ProjectArchived, Empty: true}
	b, ok := s.stagesBE()
	if !ok {
		return vm
	}
	v, err := b.ProjectStages(ctx, co.ID, p.ID.String())
	if err != nil {
		vm.Error = err.Error()
		return vm
	}
	vm.Empty = len(v.Stages) == 0
	n := 0
	for _, st := range v.Stages {
		n++
		vm.Rows = append(vm.Rows, views.StageFormRow{N: n, Key: st.Key, Name: st.Name, Instructions: st.Instructions, Tasks: st.Tasks,
			StatusOptions: stageStatusOptions(ctx, st.Status), PersonaOption: s.stagePersonaOptions(ctx, co, st.PersonaID),
			GateOptions: stageGateOptions(ctx, st.ExitGate)})
	}
	if vm.CanEdit {
		for i := 0; i < blankStageRows && n < domain.MaxProjectStages; i++ {
			n++
			vm.Rows = append(vm.Rows, views.StageFormRow{N: n, StatusOptions: stageStatusOptions(ctx, domain.TaskInProgress),
				PersonaOption: s.stagePersonaOptions(ctx, co, nil), GateOptions: stageGateOptions(ctx, domain.StageGateNone)})
		}
	}
	if ls, err := b.StageLearnings(ctx, co.ID, p.ID.String(), "", 30); err == nil {
		vm.Learnings = s.learningRows(ctx, co, ls)
	}
	return vm
}

func (s *server) handleSaveProjectStages(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	b, ok := s.stagesBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	p, err := s.be.GetProjectView(ctx, co.ID, r.PathValue("project"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	var specs []domain.StageSpec
	for i := 1; i <= domain.MaxProjectStages; i++ {
		key := trimForm(r, fmt.Sprintf("key_%d", i))
		if key == "" {
			continue
		}
		specs = append(specs, domain.StageSpec{Key: key, Name: trimForm(r, fmt.Sprintf("name_%d", i)),
			Status:       domain.TaskStatus(trimForm(r, fmt.Sprintf("status_%d", i))),
			Persona:      trimForm(r, fmt.Sprintf("persona_%d", i)),
			ExitGate:     domain.StageExitGate(trimForm(r, fmt.Sprintf("gate_%d", i))),
			Instructions: strings.TrimSpace(r.PostFormValue(fmt.Sprintf("instructions_%d", i)))})
	}
	back := "/c/" + co.Slug + "/projetos/" + p.Slug + "?secao=etapas"
	if len(specs) == 0 {
		if err := b.ClearProjectStages(ctx, actorFrom(ctx), co.ID, p.ID.String()); err != nil {
			s.renderStagesError(w, r, co, p, err)
			return
		}
		redirect(w, r, back+"&ok=etapas-limpas")
		return
	}
	if _, err := b.SetProjectStages(ctx, actorFrom(ctx), co.ID, p.ID.String(), specs); err != nil {
		s.renderStagesError(w, r, co, p, err)
		return
	}
	redirect(w, r, back+"&ok=etapas-salvas")
}

// renderStagesError reexibe a aba com o motivo da recusa.
func (s *server) renderStagesError(w http.ResponseWriter, r *http.Request, co domain.Company, p service.ProjectView, err error) {
	ctx := r.Context()
	vm := s.buildProjectVM(r, co, p)
	vm.Section = "etapas"
	vm.Stages = s.projectStagesVM(r, co, p)
	var ve *domain.ValidationError
	if errors.As(err, &ve) {
		vm.Stages.Error = ve.Field + ": " + ve.Reason
	} else {
		_, title, msg := friendly(ctx, err)
		vm.Stages.Error = title + ". " + msg
	}
	s.renderProject(w, r, co, statusFor(err), p, vm)
}

// ---------- painel da task ----------

func (s *server) taskStageVM(ctx context.Context, co domain.Company, t service.TaskView) views.TaskStageVM {
	vm := views.TaskStageVM{}
	b, ok := s.stagesBE()
	if !ok || t.ProjectID == nil || t.System {
		return vm
	}
	base := "/c/" + co.Slug + "/tasks/" + t.Key + "/etapa"
	vm.CanWrite = views.RoleFrom(ctx).AtLeast(domain.MemberMember) && t.Status.Open()
	vm.CompleteURL, vm.AdvanceURL, vm.LeaveURL, vm.StartAction = base+"/concluir", base+"/mover", base+"/sair", base+"/entrar"
	pv, err := b.ProjectStages(ctx, co.ID, t.ProjectID.String())
	if err != nil || len(pv.Stages) == 0 {
		return vm
	}
	vm.Show = true
	for _, st := range pv.Stages {
		vm.StartStages = append(vm.StartStages, views.Option{Value: st.Key, Label: st.Name, Selected: st.Position == 1})
	}
	tv, err := b.TaskStage(ctx, co.ID, t.ID.String())
	if err != nil || tv.State == domain.TaskStageFinished {
		return vm
	}
	vm.InPipeline = true
	vm.StageName, vm.Position, vm.Total = tv.Stage.Name, tv.Position, tv.Total
	vm.Persona, vm.Status = dashIfEmpty(tv.Stage.PersonaName), string(tv.Stage.Status)
	vm.ExitGate = stageGateLabel(ctx, tv.Stage.ExitGate)
	vm.Next = "—"
	for _, st := range pv.Stages {
		if st.Key == tv.NextKey {
			vm.Next = st.Name
		}
	}
	switch tv.State {
	case domain.TaskStageCompleting:
		vm.State, vm.StateClass = i18n.T(ctx, "esperando o gate"), "warn"
	default:
		vm.State, vm.StateClass = i18n.T(ctx, "em andamento"), "run"
	}
	vm.CanComplete = tv.State == domain.TaskStageWorking
	vm.BackOptions = []views.Option{{Value: "", Label: i18n.T(ctx, "Próxima etapa"), Selected: true}}
	for _, st := range pv.Stages {
		if st.Key != tv.Stage.Key {
			vm.BackOptions = append(vm.BackOptions, views.Option{Value: st.Key, Label: st.Name})
		}
	}
	vm.Learnings = s.learningRows(ctx, co, tv.Learnings)
	loc := loadLoc(co.Timezone)
	for _, e := range tv.Events {
		vm.History = append(vm.History, views.StageHistoryRow{When: fmtDateTime(ctx, e.CreatedAt, loc), Text: stageEventText(ctx, e), Note: truncRunes(e.Note, 200)})
	}
	return vm
}

func stageEventText(ctx context.Context, e domain.StageEvent) string {
	switch e.Kind {
	case domain.StageEvEnrolled:
		return i18n.Tf(ctx, "entrou na etapa %s", e.ToStageKey)
	case domain.StageEvCompleted:
		return i18n.Tf(ctx, "etapa %s concluída", e.StageKey)
	case domain.StageEvGateRequested:
		return i18n.Tf(ctx, "gate pedido na etapa %s", e.StageKey)
	case domain.StageEvGateDecided:
		return i18n.Tf(ctx, "gate decidido na etapa %s: %s", e.StageKey, i18n.T(ctx, domain.LearningDecisionLabel(e.Decision)))
	case domain.StageEvAdvanced:
		return i18n.Tf(ctx, "avançou de %s para %s", e.StageKey, e.ToStageKey)
	case domain.StageEvReturned:
		return i18n.Tf(ctx, "voltou para %s", e.ToStageKey)
	case domain.StageEvFinished:
		return i18n.T(ctx, "atravessou a última etapa")
	}
	return i18n.T(ctx, "saiu do pipeline")
}

func (s *server) stageTaskFrom(w http.ResponseWriter, r *http.Request, co domain.Company) (stagesBackend, service.TaskView, bool) {
	b, ok := s.stagesBE()
	if !ok {
		s.unavailable(w, r)
		return nil, service.TaskView{}, false
	}
	t, err := s.be.GetTask(r.Context(), co.ID, r.PathValue("task"))
	if err != nil {
		s.fail(w, r, &co, err)
		return nil, service.TaskView{}, false
	}
	return b, t, true
}

func (s *server) stageDone(w http.ResponseWriter, r *http.Request, co domain.Company, t service.TaskView, notice string, err error) {
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/tasks/"+t.Key+"?ok="+notice)
}

func (s *server) handleTaskStageStart(w http.ResponseWriter, r *http.Request, co domain.Company) {
	b, t, ok := s.stageTaskFrom(w, r, co)
	if !ok {
		return
	}
	_, err := b.StartTaskStage(r.Context(), actorFrom(r.Context()), co.ID, t.ID, trimForm(r, "stage"))
	s.stageDone(w, r, co, t, "etapa-entrou", err)
}

func (s *server) handleTaskStageComplete(w http.ResponseWriter, r *http.Request, co domain.Company) {
	b, t, ok := s.stageTaskFrom(w, r, co)
	if !ok {
		return
	}
	_, err := b.CompleteTaskStage(r.Context(), actorFrom(r.Context()), co.ID, t.ID, trimForm(r, "note"))
	s.stageDone(w, r, co, t, "etapa-concluida", err)
}

func (s *server) handleTaskStageMove(w http.ResponseWriter, r *http.Request, co domain.Company) {
	b, t, ok := s.stageTaskFrom(w, r, co)
	if !ok {
		return
	}
	_, err := b.AdvanceTaskStage(r.Context(), actorFrom(r.Context()), co.ID, t.ID, service.AdvanceStageInput{To: trimForm(r, "to"), Note: trimForm(r, "note")})
	s.stageDone(w, r, co, t, "etapa-movida", err)
}

func (s *server) handleTaskStageLeave(w http.ResponseWriter, r *http.Request, co domain.Company) {
	b, t, ok := s.stageTaskFrom(w, r, co)
	if !ok {
		return
	}
	err := b.LeaveTaskStage(r.Context(), actorFrom(r.Context()), co.ID, t.ID, trimForm(r, "note"))
	s.stageDone(w, r, co, t, "etapa-saiu", err)
}

// ---------- Painel ----------

// dashboardCards monta os cards de status marcados para o Painel (vazio sem o backend ou sem cards marcados).
func (s *server) dashboardCards(ctx context.Context, co domain.Company) []views.DashboardCardVM {
	b, ok := s.stagesBE()
	if !ok {
		return nil
	}
	cs, err := b.DashboardStatusCards(ctx, co.ID)
	if err != nil {
		return nil
	}
	loc, now := loadLoc(co.Timezone), s.now()
	out := make([]views.DashboardCardVM, 0, len(cs))
	for _, c := range cs {
		vm := views.DashboardCardVM{Title: c.Title, Href: cardsBase(co) + "/" + c.ID.String(), Summary: renderMD(truncRunes(c.Summary, 1200)),
			Revision: i18n.Tf(ctx, "versão %d", c.Revision), Engine: c.Engine, State: c.State, ArtifactName: c.ArtifactName}
		if c.UpdatedAt != nil {
			vm.Updated = i18n.Tf(ctx, "atualizado %s", agoCtx(ctx, *c.UpdatedAt, now, loc))
		}
		if c.NextAt != nil && c.NextAt.After(now) {
			vm.Next = i18n.Tf(ctx, "próxima %s", fmtWhen(ctx, *c.NextAt, now, loc))
		}
		if c.ArtifactID != nil {
			vm.ArtifactHref = "/c/" + co.Slug + "/artefatos/" + c.ArtifactID.String()
		}
		out = append(out, vm)
	}
	return out
}
