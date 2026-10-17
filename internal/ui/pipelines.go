package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Pipelines, cases, fila de revisão, aprendizados e exemplos de decisão (ADR-0034). O service é a barreira de papéis
// e de regras; a UI só esconde os controles e mostra os erros. O backend é uma interface opcional: o Backend dos
// testes e do preview não precisa implementá-la, e as telas respondem "indisponível" em vez de quebrar.

type pipesBackend interface {
	CreatePipeline(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreatePipelineInput) (service.PipelineView, error)
	UpdatePipeline(ctx context.Context, actor domain.Actor, companyID, pipelineID domain.ID, in service.UpdatePipelineInput) (service.PipelineView, error)
	DeletePipeline(ctx context.Context, actor domain.Actor, companyID, pipelineID domain.ID) error
	GetPipeline(ctx context.Context, companyID domain.ID, ref string) (service.PipelineView, error)
	ListPipelines(ctx context.Context, companyID domain.ID, includeArchived bool) ([]service.PipelineView, error)
	AddStage(ctx context.Context, actor domain.Actor, companyID, pipelineID domain.ID, in service.StageInput, position *int) (service.PipelineView, error)
	UpdateStage(ctx context.Context, actor domain.Actor, companyID, pipelineID domain.ID, stageRef string, in service.UpdateStageInput) (service.PipelineView, error)
	DeleteStage(ctx context.Context, actor domain.Actor, companyID, pipelineID domain.ID, stageRef string) (service.PipelineView, error)
	ReorderStages(ctx context.Context, actor domain.Actor, companyID, pipelineID domain.ID, keys []string) (service.PipelineView, error)
	SetTransitions(ctx context.Context, actor domain.Actor, companyID, pipelineID domain.ID, specs []service.TransitionSpec, enforce bool) (service.PipelineView, error)
	ListPipelineEvents(ctx context.Context, companyID, pipelineID domain.ID, limit int) ([]service.PipelineEvent, error)
	CreateCase(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateCaseInput) (service.CaseView, bool, error)
	UpdateCase(ctx context.Context, actor domain.Actor, companyID, caseID domain.ID, in service.UpdateCaseInput) (service.CaseView, error)
	GetCaseView(ctx context.Context, companyID domain.ID, ref string) (service.CaseView, error)
	ListCases(ctx context.Context, companyID domain.ID, f service.CaseFilter) ([]service.CaseView, error)
	TransitionCase(ctx context.Context, actor domain.Actor, companyID, caseID domain.ID, in service.TransitionCaseInput) (service.CaseView, error)
	ResolveSuggestion(ctx context.Context, actor domain.Actor, companyID, caseID domain.ID, in service.ResolveSuggestionInput) (service.CaseView, error)
	ReviewCase(ctx context.Context, actor domain.Actor, companyID, caseID domain.ID, in service.ReviewCaseInput) (service.CaseView, error)
	BulkApprove(ctx context.Context, actor domain.Actor, companyID domain.ID, items []service.BulkReviewItem) ([]service.BulkReviewResult, error)
	LinkCaseTask(ctx context.Context, actor domain.Actor, companyID, caseID, taskID domain.ID, role domain.CaseLinkRole, runID *domain.ID) (service.CaseView, error)
	UnlinkCaseTask(ctx context.Context, actor domain.Actor, companyID, caseID, taskID domain.ID) (service.CaseView, error)
	SetCaseBlockers(ctx context.Context, actor domain.Actor, companyID, caseID domain.ID, expectedVersion int, blockers []domain.ID, runID *domain.ID) (service.CaseView, error)
	OpenCaseConversation(ctx context.Context, actor domain.Actor, companyID, caseID domain.ID) (service.TaskView, error)
	CasesForTask(ctx context.Context, companyID, taskID domain.ID) ([]service.CaseForTask, error)
	ReviewQueue(ctx context.Context, companyID domain.ID) ([]service.ReviewItem, error)
	ReviewQueueCount(ctx context.Context, companyID domain.ID) (int, error)
	ListLearnings(ctx context.Context, companyID domain.ID, f service.LearningFilter) ([]service.Learning, error)
	SaveDecisionExample(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.SaveDecisionExampleInput) (domain.DecisionExample, error)
	UpdateDecisionExampleNotes(ctx context.Context, actor domain.Actor, companyID, id domain.ID, notes string) (domain.DecisionExample, error)
	DeleteDecisionExample(ctx context.Context, actor domain.Actor, companyID, id domain.ID) error
	ListDecisionExamples(ctx context.Context, companyID domain.ID, f service.DecisionExampleFilter) ([]domain.DecisionExample, error)
	ExportDecisionExamples(ctx context.Context, companyID domain.ID, w io.Writer) (int, error)
}

var _ pipesBackend = (*service.Service)(nil)

func (s *server) pipesBE() (pipesBackend, bool) {
	b, ok := s.be.(pipesBackend)
	return b, ok
}

// maxPipeFormBytes é o teto dos formulários desta funcionalidade (resumo de 64 KiB, campos, instruções).
const maxPipeFormBytes = 256 << 10

func (s *server) pipelineRoutes(mux langMux) {
	co := func(h companyHandler) http.HandlerFunc { return s.withCompany(s.needPipes(h)) }
	post := func(h http.HandlerFunc) http.HandlerFunc {
		return s.sameOrigin(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxPipeFormBytes)
			h(w, r)
		})
	}
	mux.HandleFunc("GET /c/{company}/pipelines", co(s.handlePipelines))
	mux.HandleFunc("POST /c/{company}/pipelines", post(co(s.handleCreatePipeline)))
	mux.HandleFunc("GET /c/{company}/pipelines/{pipeline}", co(s.handlePipeline))
	mux.HandleFunc("POST /c/{company}/pipelines/{pipeline}", post(co(s.handleUpdatePipeline)))
	mux.HandleFunc("POST /c/{company}/pipelines/{pipeline}/arquivar", post(co(s.handleArchivePipeline)))
	mux.HandleFunc("POST /c/{company}/pipelines/{pipeline}/excluir", post(co(s.handleDeletePipeline)))
	mux.HandleFunc("POST /c/{company}/pipelines/{pipeline}/etapas", post(co(s.handleAddStage)))
	mux.HandleFunc("POST /c/{company}/pipelines/{pipeline}/etapas/{stage}", post(co(s.handleUpdateStage)))
	mux.HandleFunc("POST /c/{company}/pipelines/{pipeline}/etapas/{stage}/remover", post(co(s.handleDeleteStage)))
	mux.HandleFunc("POST /c/{company}/pipelines/{pipeline}/etapas/{stage}/subir", post(co(s.handleMoveStage(-1))))
	mux.HandleFunc("POST /c/{company}/pipelines/{pipeline}/etapas/{stage}/descer", post(co(s.handleMoveStage(1))))
	mux.HandleFunc("POST /c/{company}/pipelines/{pipeline}/transicoes", post(co(s.handleSetTransitions)))
	mux.HandleFunc("POST /c/{company}/pipelines/{pipeline}/cases", post(co(s.handleCreatePipelineCase)))

	mux.HandleFunc("GET /c/{company}/cases", co(s.handleCases))
	mux.HandleFunc("POST /c/{company}/cases", post(co(s.handleCreateCase)))
	mux.HandleFunc("GET /c/{company}/cases/{case}", co(s.handleCase))
	mux.HandleFunc("POST /c/{company}/cases/{case}", post(co(s.handleUpdateCase)))
	mux.HandleFunc("POST /c/{company}/cases/{case}/mover", post(co(s.handleMoveCase)))
	mux.HandleFunc("POST /c/{company}/cases/{case}/revisar", post(co(s.handleReviewCase)))
	mux.HandleFunc("POST /c/{company}/cases/{case}/sugestao", post(co(s.handleResolveSuggestion)))
	mux.HandleFunc("POST /c/{company}/cases/{case}/atencao", post(co(s.handleDismissAttention)))
	mux.HandleFunc("POST /c/{company}/cases/{case}/conversa", post(co(s.handleCaseConversation)))
	mux.HandleFunc("POST /c/{company}/cases/{case}/tasks", post(co(s.handleLinkCaseTask)))
	mux.HandleFunc("POST /c/{company}/cases/{case}/tasks/{task}/desligar", post(co(s.handleUnlinkCaseTask)))
	mux.HandleFunc("POST /c/{company}/cases/{case}/bloqueios", post(co(s.handleCaseBlockers)))

	mux.HandleFunc("GET /c/{company}/revisao", co(s.handleReviewQueue))
	mux.HandleFunc("POST /c/{company}/revisao/lote", post(co(s.handleBulkApprove)))
	mux.HandleFunc("GET /c/{company}/aprendizados", co(s.handleLearnings))
	mux.HandleFunc("POST /c/{company}/exemplos", post(co(s.handleSaveExample)))
	mux.HandleFunc("GET /c/{company}/exemplos/export.jsonl", co(s.handleExportExamples))
	mux.HandleFunc("POST /c/{company}/exemplos/{example}", post(co(s.handleExampleNotes)))
	mux.HandleFunc("POST /c/{company}/exemplos/{example}/apagar", post(co(s.handleDeleteExample)))
}

// needPipes responde 501 quando o backend não oferece pipelines.
func (s *server) needPipes(h companyHandler) companyHandler {
	return func(w http.ResponseWriter, r *http.Request, co domain.Company) {
		if _, ok := s.pipesBE(); !ok {
			s.unavailable(w, r)
			return
		}
		h(w, r, co)
	}
}

func (s *server) pb() pipesBackend {
	b, _ := s.pipesBE()
	return b
}

var pipelineNotices = map[string]string{
	"criado":     i18n.N("Pipeline criado."),
	"salvo":      i18n.N("Pipeline atualizado."),
	"arquivado":  i18n.N("Pipeline arquivado: não recebe cases novos nem movimentações."),
	"reativado":  i18n.N("Pipeline reativado."),
	"excluido":   i18n.N("Pipeline excluído."),
	"etapa":      i18n.N("Etapas atualizadas."),
	"transicoes": i18n.N("Transições salvas."),
	"case":       i18n.N("Case criado."),
}

func stageKindLabel(ctx context.Context, k domain.StageKind) string {
	switch k {
	case domain.StageReview:
		return i18n.T(ctx, "Revisão")
	case domain.StageDone:
		return i18n.T(ctx, "Concluído")
	case domain.StageCancelled:
		return i18n.T(ctx, "Cancelado")
	}
	return i18n.T(ctx, "Trabalho")
}

func caseBadge(ctx context.Context, st domain.CaseStatus) views.Badge {
	switch st {
	case domain.CaseDraft:
		return views.Badge{Label: i18n.T(ctx, "Rascunho"), Class: "soft"}
	case domain.CaseInProgress:
		return views.Badge{Label: i18n.T(ctx, "Em andamento"), Class: "run"}
	case domain.CaseInReview:
		return views.Badge{Label: i18n.T(ctx, "Em revisão"), Class: "warn"}
	case domain.CaseApproved:
		return views.Badge{Label: i18n.T(ctx, "Aprovado"), Class: "done"}
	case domain.CaseDone:
		return views.Badge{Label: i18n.T(ctx, "Concluído"), Class: "done"}
	}
	return views.Badge{Label: i18n.T(ctx, "Cancelado"), Class: "soft"}
}

func caseStatusLabel(ctx context.Context, st domain.CaseStatus) string { return caseBadge(ctx, st).Label }

func linkRoleLabel(ctx context.Context, r domain.CaseLinkRole) string {
	switch r {
	case domain.LinkOrigin:
		return i18n.T(ctx, "origem")
	case domain.LinkAutomation:
		return i18n.T(ctx, "automação")
	case domain.LinkConversation:
		return i18n.T(ctx, "conversa")
	}
	return i18n.T(ctx, "trabalho")
}

func caseHref(base, identifier string) string { return base + "/cases/" + url.PathEscape(identifier) }

// pipelineErr devolve o texto de um erro para as telas desta funcionalidade (o conflito de versão diz a atual).
func pipelineErr(ctx context.Context, err error) string {
	if cur, ok := domain.CurrentVersion(err); ok {
		return i18n.Tf(ctx, "O case mudou enquanto você olhava (agora está na versão %d). Confira e tente de novo.", cur)
	}
	switch {
	case errors.Is(err, domain.ErrReviewOutdated):
		return i18n.T(ctx, "O case mudou depois de aprovado: mande-o para revisão antes de seguir.")
	case errors.Is(err, domain.ErrCaseBlocked):
		return i18n.T(ctx, "O case está bloqueado (bloqueios abertos ou filhos sem terminar).") + " " + err.Error()
	case errors.Is(err, domain.ErrForbidden):
		return i18n.T(ctx, "Seu papel nesta empresa não permite esta ação.")
	case errors.Is(err, domain.ErrNotAllowed), errors.Is(err, domain.ErrInvalid), errors.Is(err, domain.ErrConflict):
		_, _, msg := friendly(ctx, err)
		var ve *domain.ValidationError
		if errors.As(err, &ve) || errors.Is(err, domain.ErrNotAllowed) || errors.Is(err, domain.ErrConflict) {
			return msg + " (" + err.Error() + ")"
		}
		return msg
	}
	_, _, msg := friendly(ctx, err)
	return msg
}

// ---------- lista de pipelines ----------

const defaultStagesText = "Novo | working\nEm revisão | review\nConcluído | done\nCancelado | cancelled"

func (s *server) handlePipelines(w http.ResponseWriter, r *http.Request, co domain.Company) {
	s.renderPipelines(w, r, co, views.PipelineFormVM{Stages: defaultStagesText}, http.StatusOK)
}

func (s *server) renderPipelines(w http.ResponseWriter, r *http.Request, co domain.Company, form views.PipelineFormVM, status int) {
	ctx := r.Context()
	base := "/c/" + co.Slug
	showArch := r.URL.Query().Get("arquivados") == "1"
	list, err := s.pb().ListPipelines(ctx, co.ID, showArch)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	form.Action = base + "/pipelines"
	vm := views.PipelinesVM{Notice: i18n.T(ctx, pipelineNotices[r.URL.Query().Get("ok")]), Form: form, ShowArchived: showArch,
		ArchivedHref: base + "/pipelines?arquivados=1"}
	if showArch {
		vm.ArchivedHref = base + "/pipelines"
	}
	for _, p := range list {
		names := make([]string, 0, len(p.Stages))
		for _, st := range p.Stages {
			names = append(names, st.Name)
		}
		vm.Rows = append(vm.Rows, views.PipelineRow{Name: p.Name, Key: p.Key, Href: base + "/pipelines/" + p.Key, Description: excerpt(p.Description, 140),
			Stages: len(p.Stages), StageNames: strings.Join(names, " → "), Open: p.OpenCases, InReview: p.InReview, Archived: p.ArchivedAt != nil})
	}
	sh := s.shell(r, co, "pipelines", i18n.T(ctx, "Pipelines"))
	s.render(w, r, status, views.PipelinesPage(sh, vm))
}

// parseStagesText lê "Nome | tipo" por linha.
func parseStagesText(raw string) ([]service.StageInput, error) {
	var out []service.StageInput
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, kind, _ := strings.Cut(line, "|")
		kind = strings.ToLower(strings.TrimSpace(kind))
		if kind == "" {
			kind = string(domain.StageWorking)
		}
		st := service.StageInput{Name: strings.TrimSpace(name), Kind: domain.StageKind(kind)}
		if !st.Kind.Valid() {
			return nil, domain.Invalid("stages", fmt.Sprintf("tipo %q desconhecido", kind))
		}
		out = append(out, st)
	}
	return out, nil
}

// wireReviewDefaults liga os destinos de uma etapa de revisão criada pelo formulário rápido: aprovar vai para a
// primeira done depois dela, recusar para a primeira cancelled (ou done), pedir mudanças volta à working anterior.
func wireReviewDefaults(stages []service.StageInput) {
	keys := make([]string, len(stages))
	for i := range stages {
		keys[i] = domain.Slugify(stages[i].Name)
		stages[i].Key = keys[i]
	}
	for i := range stages {
		if stages[i].Kind != domain.StageReview {
			continue
		}
		rt := &domain.ReviewRouting{}
		for j := range stages {
			switch {
			case stages[j].Kind == domain.StageDone && rt.ApproveTo == "":
				rt.ApproveTo = keys[j]
			case stages[j].Kind == domain.StageCancelled && rt.RejectTo == "":
				rt.RejectTo = keys[j]
			}
		}
		if rt.RejectTo == "" {
			rt.RejectTo = rt.ApproveTo
		}
		for j := i - 1; j >= 0; j-- {
			if stages[j].Kind == domain.StageWorking {
				rt.RequestChangesTo = keys[j]
				break
			}
		}
		stages[i].Config.Review = rt
	}
}

func (s *server) handleCreatePipeline(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	form := views.PipelineFormVM{Name: trimForm(r, "name"), Key: trimForm(r, "key"), Description: trimForm(r, "description"), Stages: r.PostFormValue("stages")}
	stages, err := parseStagesText(form.Stages)
	if err == nil {
		wireReviewDefaults(stages)
		var pv service.PipelineView
		pv, err = s.pb().CreatePipeline(ctx, actorFrom(ctx), co.ID, service.CreatePipelineInput{Name: form.Name, Key: form.Key, Description: form.Description, Stages: stages})
		if err == nil {
			redirect(w, r, "/c/"+co.Slug+"/pipelines/"+pv.Key+"?ok=criado")
			return
		}
	}
	form.Error = pipelineErr(ctx, err)
	s.renderPipelines(w, r, co, form, statusFor(err))
}

// ---------- página do pipeline ----------

func (s *server) handlePipeline(w http.ResponseWriter, r *http.Request, co domain.Company) {
	s.renderPipeline(w, r, co, r.PathValue("pipeline"), "", "", nil, http.StatusOK)
}

type addDraft struct {
	title  string
	fields map[string]string
}

func (s *server) renderPipeline(w http.ResponseWriter, r *http.Request, co domain.Company, ref, tab, errMsg string, draft *addDraft, status int) {
	ctx := r.Context()
	b := s.pb()
	pv, err := b.GetPipeline(ctx, co.ID, ref)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	base := "/c/" + co.Slug
	href := base + "/pipelines/" + pv.Key
	if tab == "" {
		tab = r.URL.Query().Get("aba")
	}
	switch tab {
	case "config", "atividade":
	default:
		tab = "quadro"
	}
	loc, now := loadLoc(co.Timezone), s.now()
	vm := views.PipelineVM{Name: pv.Name, Key: pv.Key, Description: pv.Description, Href: href, Archived: pv.ArchivedAt != nil, Strict: pv.EnforceTransitions,
		Tab: tab, Notice: i18n.T(ctx, pipelineNotices[r.URL.Query().Get("ok")]), Error: errMsg, MetaAction: href, ArchiveAction: href + "/arquivar",
		DeleteAction: href + "/excluir", ReviewHref: base + "/revisao", TotalCases: pv.TotalCases}
	for _, t := range []struct{ key, label string }{{"quadro", i18n.N("Quadro")}, {"config", i18n.N("Configuração")}, {"atividade", i18n.N("Atividade")}} {
		vm.Tabs = append(vm.Tabs, views.TabLink{Key: t.key, Label: i18n.T(ctx, t.label), Href: href + "?aba=" + t.key, On: t.key == tab})
	}
	personas, err := s.be.ListPersonas(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	personaName := map[domain.ID]string{}
	for _, p := range personas {
		personaName[p.ID] = p.Name
	}
	switch tab {
	case "quadro":
		cases, err := b.ListCases(ctx, co.ID, service.CaseFilter{PipelineID: &pv.ID, Limit: 500})
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		for _, st := range pv.Stages {
			col := views.StageColumn{Key: st.Key, Name: st.Name, Kind: string(st.Kind), KindLabel: stageKindLabel(ctx, st.Kind), Disabled: st.Config.Disabled,
				DisabledReason: st.Config.DisabledReason, Approval: st.Config.ApprovalRequired()}
			if a := st.Config.Automation; a.Enabled() {
				col.Automation = personaName[*a.PersonaID]
			}
			for _, c := range cases {
				if c.StageID == nil || *c.StageID != st.ID {
					continue
				}
				card := views.CaseCard{Identifier: c.Identifier, Title: c.Title, Href: caseHref(base, c.Identifier), MoveAction: caseHref(base, c.Identifier) + "/mover",
					Version: c.Version, Type: c.Type, Updated: agoCtx(ctx, c.UpdatedAt, now, loc)}
				card.Badges = caseFlags(ctx, c)
				col.Cards = append(col.Cards, card)
			}
			col.Count = len(col.Cards)
			vm.Columns = append(vm.Columns, col)
		}
		vm.Add = s.addFormVM(ctx, pv, href, draft)
		if draft != nil {
			vm.Add.Error = errMsg
			vm.Error = ""
		}
	case "config":
		members, _ := s.be.ListMembers(ctx, co.ID)
		for i, st := range pv.Stages {
			vm.Stages = append(vm.Stages, stageEditVM(ctx, pv, st, i, href, personas, members))
		}
		vm.NewStage = views.StageEditVM{Action: href + "/etapas", KindOptions: kindOptions(ctx, domain.StageWorking)}
		vm.Transitions = transitionsVM(pv, href)
	case "atividade":
		evs, err := b.ListPipelineEvents(ctx, co.ID, pv.ID, 200)
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		names := s.actorNames(ctx, co.ID, personas)
		for _, e := range evs {
			row := caseEventRow(ctx, e.CaseEvent, names, now, loc)
			row.CaseIdentifier, row.CaseHref = e.CaseIdentifier, caseHref(base, e.CaseIdentifier)
			vm.Events = append(vm.Events, row)
		}
	}
	sh := s.shell(r, co, "pipelines", pv.Name, views.Crumb{Label: i18n.T(ctx, "Pipelines"), Href: base + "/pipelines"}, views.Crumb{Label: pv.Name})
	s.render(w, r, status, views.PipelinePage(sh, vm))
}

// caseFlags são os selos de um case nas listas: sugestão, atenção, reaprovação.
func caseFlags(ctx context.Context, c service.CaseView) []views.Badge {
	var out []views.Badge
	if c.PendingSuggestion != nil {
		out = append(out, views.Badge{Label: i18n.T(ctx, "sugestão"), Class: "warn"})
	}
	if c.Attention != "" {
		out = append(out, views.Badge{Label: i18n.T(ctx, "atenção"), Class: "warn"})
	}
	if c.ReapprovalRequired {
		out = append(out, views.Badge{Label: i18n.T(ctx, "reaprovar"), Class: "danger"})
	}
	return out
}

func (s *server) addFormVM(ctx context.Context, pv service.PipelineView, href string, draft *addDraft) views.CaseAddFormVM {
	f := views.CaseAddFormVM{Action: href + "/cases"}
	var first *domain.Stage
	for i := range pv.Stages {
		if !pv.Stages[i].Config.Disabled && !pv.Stages[i].Kind.Terminal() {
			first = &pv.Stages[i]
			break
		}
	}
	if first == nil {
		return f
	}
	f.StageName = first.Name
	if draft != nil {
		f.Title = draft.title
	}
	for _, fd := range first.Config.Fields {
		if !fd.ShowInAddForm {
			continue
		}
		fi := views.FieldInput{Key: fd.Key, Label: fd.Label, Type: string(fd.Type), Required: fd.Required}
		if draft != nil {
			fi.Value = draft.fields[fd.Key]
		}
		if fd.Type == domain.FieldSelect {
			fi.Options = append(fi.Options, views.Option{Value: "", Label: "—"})
			for _, o := range fd.Options {
				fi.Options = append(fi.Options, views.Option{Value: o, Label: o, Selected: o == fi.Value})
			}
		}
		f.Fields = append(f.Fields, fi)
	}
	return f
}

func kindOptions(ctx context.Context, cur domain.StageKind) []views.Option {
	out := make([]views.Option, 0, len(domain.StageKinds))
	for _, k := range domain.StageKinds {
		out = append(out, views.Option{Value: string(k), Label: stageKindLabel(ctx, k) + " (" + string(k) + ")", Selected: k == cur})
	}
	return out
}

func stageKeyOptions(pv service.PipelineView, cur, self string, blank bool) []views.Option {
	var out []views.Option
	if blank {
		out = append(out, views.Option{Value: "", Label: "—", Selected: cur == ""})
	}
	for _, st := range pv.Stages {
		if st.Key == self {
			continue
		}
		out = append(out, views.Option{Value: st.Key, Label: st.Name, Selected: st.Key == cur})
	}
	return out
}

// fieldsText escreve os campos de uma etapa no formato do editor ("key | Rótulo | tipo | opções | flags").
func fieldsText(fs []domain.StageField) string {
	var b strings.Builder
	for _, f := range fs {
		var flags []string
		if f.Required {
			flags = append(flags, "obrigatorio")
		}
		if f.ShowInAddForm {
			flags = append(flags, "formulario")
		}
		fmt.Fprintf(&b, "%s | %s | %s | %s | %s\n", f.Key, f.Label, f.Type, strings.Join(f.Options, ", "), strings.Join(flags, ", "))
	}
	return strings.TrimRight(b.String(), "\n")
}

// parseFieldsText lê o editor de campos.
func parseFieldsText(raw string) ([]domain.StageField, error) {
	var out []domain.StageField
	for i, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "|")
		for len(parts) < 5 {
			parts = append(parts, "")
		}
		f := domain.StageField{Key: strings.TrimSpace(parts[0]), Label: strings.TrimSpace(parts[1]), Type: domain.FieldType(strings.ToLower(strings.TrimSpace(parts[2])))}
		if f.Label == "" {
			f.Label = f.Key
		}
		if f.Type == "" {
			f.Type = domain.FieldText
		}
		for _, o := range strings.Split(parts[3], ",") {
			if o = strings.TrimSpace(o); o != "" {
				f.Options = append(f.Options, o)
			}
		}
		for _, fl := range strings.Split(strings.ToLower(parts[4]), ",") {
			switch strings.TrimSpace(fl) {
			case "obrigatorio", "obrigatório", "required":
				f.Required = true
			case "formulario", "formulário", "form":
				f.ShowInAddForm = true
			case "":
			default:
				return nil, domain.Invalid("fields", fmt.Sprintf("linha %d: marca %q desconhecida", i+1, strings.TrimSpace(fl)))
			}
		}
		out = append(out, f)
	}
	return out, nil
}

func stageEditVM(ctx context.Context, pv service.PipelineView, st domain.Stage, i int, href string, personas []domain.Persona, members []domain.Member) views.StageEditVM {
	sAction := href + "/etapas/" + url.PathEscape(st.Key)
	v := views.StageEditVM{Key: st.Key, Name: st.Name, Kind: string(st.Kind), KindOptions: kindOptions(ctx, st.Kind), First: i == 0, Last: i == len(pv.Stages)-1,
		WhatHappensHere: st.Config.WhatHappensHere, Disabled: st.Config.Disabled, DisabledReason: st.Config.DisabledReason,
		ApprovalRequired: st.Config.ApprovalRequired(), ChildrenTerminal: st.Config.RequireChildrenTerminal(), FieldsText: fieldsText(st.Config.Fields),
		Action: sAction, DeleteAction: sAction + "/remover", UpAction: sAction + "/subir", DownAction: sAction + "/descer",
		RequireRejectReason: st.Config.Review.RejectReasonRequired(), RequireChangesReason: st.Config.Review.ChangesReasonRequired()}
	approver := domain.ApproverAnyMember
	if st.Config.Approval != nil && st.Config.Approval.Approver != "" {
		approver = st.Config.Approval.Approver
	}
	v.ApproverOptions = append(v.ApproverOptions, views.Option{Value: domain.ApproverAnyMember, Label: i18n.T(ctx, "qualquer membro"), Selected: approver == domain.ApproverAnyMember})
	for _, m := range members {
		val := domain.MemberApprover(m.UserID)
		label := m.Name
		if label == "" {
			label = m.Email
		}
		v.ApproverOptions = append(v.ApproverOptions, views.Option{Value: val, Label: label, Selected: approver == val})
	}
	if st.Kind != domain.StageReview {
		for _, p := range personas {
			val := domain.PersonaApprover(p.ID)
			v.ApproverOptions = append(v.ApproverOptions, views.Option{Value: val, Label: "Persona: " + p.Name, Selected: approver == val})
		}
	}
	var auto *domain.ID
	var prio domain.Priority
	if a := st.Config.Automation; a != nil {
		auto, prio, v.Instructions = a.PersonaID, a.Priority, a.Instructions
	}
	v.AutomationOptions = append(v.AutomationOptions, views.Option{Value: "", Label: i18n.T(ctx, "sem automação"), Selected: auto == nil})
	for _, p := range personas {
		v.AutomationOptions = append(v.AutomationOptions, views.Option{Value: p.ID.String(), Label: p.Name, Selected: auto != nil && *auto == p.ID})
	}
	v.PriorityOptions = append(v.PriorityOptions, views.Option{Value: "", Label: i18n.T(ctx, "padrão (média)"), Selected: prio == ""})
	for _, p := range []domain.Priority{domain.PriorityCritical, domain.PriorityHigh, domain.PriorityMedium, domain.PriorityLow} {
		v.PriorityOptions = append(v.PriorityOptions, views.Option{Value: string(p), Label: priorityLabel(ctx, p), Selected: prio == p})
	}
	rv := st.Config.Review
	at, rt, ct := "", "", ""
	if rv != nil {
		at, rt, ct = rv.ApproveTo, rv.RejectTo, rv.RequestChangesTo
	}
	v.ApproveToOptions = stageKeyOptions(pv, at, st.Key, false)
	v.RejectToOptions = stageKeyOptions(pv, rt, st.Key, false)
	v.ChangesToOptions = stageKeyOptions(pv, ct, st.Key, true)
	var sum []string
	if v.ApprovalRequired {
		sum = append(sum, i18n.T(ctx, "aprovação"))
	}
	if auto != nil {
		for _, p := range personas {
			if p.ID == *auto {
				sum = append(sum, i18n.Tf(ctx, "automação: %s", p.Name))
			}
		}
	}
	if len(st.Config.Fields) > 0 {
		sum = append(sum, i18n.Tf(ctx, "%d campo(s)", len(st.Config.Fields)))
	}
	if st.Config.Disabled {
		sum = append(sum, i18n.T(ctx, "desligada"))
	}
	v.Summary = strings.Join(sum, " · ")
	return v
}

func transitionsVM(pv service.PipelineView, href string) views.TransitionsVM {
	t := views.TransitionsVM{Action: href + "/transicoes", Strict: pv.EnforceTransitions}
	on := map[string]bool{}
	for _, tr := range pv.Transitions {
		f, _ := pv.StageByID(tr.From)
		to, _ := pv.StageByID(tr.To)
		on[f.Key+">"+to.Key] = true
	}
	for _, st := range pv.Stages {
		t.Names = append(t.Names, st.Name)
	}
	for _, from := range pv.Stages {
		row := views.TransitionRow{Name: from.Name}
		for _, to := range pv.Stages {
			v := from.Key + ">" + to.Key
			row.Cells = append(row.Cells, views.TransitionCell{Value: v, On: on[v], Self: from.Key == to.Key})
		}
		t.Rows = append(t.Rows, row)
	}
	return t
}

// ---------- ações do pipeline ----------

func (s *server) pipelineRef(w http.ResponseWriter, r *http.Request, co domain.Company) (service.PipelineView, bool) {
	pv, err := s.pb().GetPipeline(r.Context(), co.ID, r.PathValue("pipeline"))
	if err != nil {
		s.fail(w, r, &co, err)
		return pv, false
	}
	return pv, true
}

func (s *server) pipelineDone(w http.ResponseWriter, r *http.Request, co domain.Company, key, tab, ok string) {
	to := "/c/" + co.Slug + "/pipelines/" + key + "?ok=" + ok
	if tab != "" {
		to += "&aba=" + tab
	}
	redirect(w, r, to)
}

func (s *server) handleUpdatePipeline(w http.ResponseWriter, r *http.Request, co domain.Company) {
	pv, ok := s.pipelineRef(w, r, co)
	if !ok {
		return
	}
	ctx := r.Context()
	name, key, desc := trimForm(r, "name"), trimForm(r, "key"), trimForm(r, "description")
	out, err := s.pb().UpdatePipeline(ctx, actorFrom(ctx), co.ID, pv.ID, service.UpdatePipelineInput{Name: &name, Key: &key, Description: &desc})
	if err != nil {
		s.renderPipeline(w, r, co, pv.ID.String(), "config", pipelineErr(ctx, err), nil, statusFor(err))
		return
	}
	s.pipelineDone(w, r, co, out.Key, "config", "salvo")
}

func (s *server) handleArchivePipeline(w http.ResponseWriter, r *http.Request, co domain.Company) {
	pv, ok := s.pipelineRef(w, r, co)
	if !ok {
		return
	}
	ctx := r.Context()
	arch := pv.ArchivedAt == nil
	if _, err := s.pb().UpdatePipeline(ctx, actorFrom(ctx), co.ID, pv.ID, service.UpdatePipelineInput{Archived: &arch}); err != nil {
		s.renderPipeline(w, r, co, pv.Key, "config", pipelineErr(ctx, err), nil, statusFor(err))
		return
	}
	msg := "reativado"
	if arch {
		msg = "arquivado"
	}
	s.pipelineDone(w, r, co, pv.Key, "config", msg)
}

func (s *server) handleDeletePipeline(w http.ResponseWriter, r *http.Request, co domain.Company) {
	pv, ok := s.pipelineRef(w, r, co)
	if !ok {
		return
	}
	ctx := r.Context()
	if err := s.pb().DeletePipeline(ctx, actorFrom(ctx), co.ID, pv.ID); err != nil {
		s.renderPipeline(w, r, co, pv.Key, "config", pipelineErr(ctx, err), nil, statusFor(err))
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/pipelines?ok=excluido")
}

func (s *server) handleAddStage(w http.ResponseWriter, r *http.Request, co domain.Company) {
	pv, ok := s.pipelineRef(w, r, co)
	if !ok {
		return
	}
	ctx := r.Context()
	in := service.StageInput{Name: trimForm(r, "name"), Key: trimForm(r, "key"), Kind: domain.StageKind(trimForm(r, "kind")),
		Config: domain.StageConfig{WhatHappensHere: trimForm(r, "what_happens_here")}}
	if in.Kind == domain.StageReview {
		// destinos provisórios: a primeira done e a primeira cancelled (ou done); o admin ajusta depois
		tmp := append(append([]service.StageInput{}, stagesAsInputs(pv)...), in)
		wireReviewDefaults(tmp)
		in.Config.Review = tmp[len(tmp)-1].Config.Review
		in.Key = strings.TrimSpace(trimForm(r, "key"))
	}
	if _, err := s.pb().AddStage(ctx, actorFrom(ctx), co.ID, pv.ID, in, nil); err != nil {
		s.renderPipeline(w, r, co, pv.Key, "config", pipelineErr(ctx, err), nil, statusFor(err))
		return
	}
	s.pipelineDone(w, r, co, pv.Key, "config", "etapa")
}

// stagesAsInputs devolve as etapas existentes como entradas (com a key atual), para derivar os destinos padrão.
func stagesAsInputs(pv service.PipelineView) []service.StageInput {
	out := make([]service.StageInput, len(pv.Stages))
	for i, st := range pv.Stages {
		out[i] = service.StageInput{Key: st.Key, Name: st.Key, Kind: st.Kind}
	}
	return out
}

func checked(r *http.Request, name string) bool { return r.PostFormValue(name) != "" }

func (s *server) handleUpdateStage(w http.ResponseWriter, r *http.Request, co domain.Company) {
	pv, ok := s.pipelineRef(w, r, co)
	if !ok {
		return
	}
	ctx := r.Context()
	cur, found := pv.Stage(r.PathValue("stage"))
	if !found {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	fields, err := parseFieldsText(r.PostFormValue("fields"))
	if err != nil {
		s.renderPipeline(w, r, co, pv.Key, "config", pipelineErr(ctx, err), nil, statusFor(err))
		return
	}
	name, key, kind := trimForm(r, "name"), trimForm(r, "key"), domain.StageKind(trimForm(r, "kind"))
	cfg := domain.StageConfig{Fields: fields, WhatHappensHere: trimForm(r, "what_happens_here"), Disabled: checked(r, "disabled"),
		DisabledReason: trimForm(r, "disabled_reason")}
	if checked(r, "approval") {
		cfg.Approval = &domain.StageApproval{Required: true, Approver: trimForm(r, "approver")}
	}
	if p := trimForm(r, "automation_persona"); p != "" {
		pid, err := domain.ParseID(p)
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		cfg.Automation = &domain.StageAutomation{PersonaID: &pid, Instructions: r.PostFormValue("automation_instructions"), Priority: domain.Priority(trimForm(r, "automation_priority"))}
	}
	if kind == domain.StageReview {
		rr, rc := checked(r, "require_reject_reason"), checked(r, "require_changes_reason")
		cfg.Review = &domain.ReviewRouting{ApproveTo: trimForm(r, "approve_to"), RejectTo: trimForm(r, "reject_to"), RequestChangesTo: trimForm(r, "request_changes_to"),
			RequireRejectReason: &rr, RequireChangesReason: &rc}
		if cur.Kind != domain.StageReview && cfg.Review.ApproveTo == "" {
			tmp := stagesAsInputs(pv)
			for i := range tmp {
				if tmp[i].Key == cur.Key {
					tmp[i].Kind = domain.StageReview
				}
			}
			wireReviewDefaults(tmp)
			for i := range tmp {
				if pv.Stages[i].Key == cur.Key {
					cfg.Review = tmp[i].Config.Review
				}
			}
		}
	}
	if checked(r, "children_terminal") {
		cfg.Gates = &domain.StageGates{RequireChildrenTerminal: true}
	}
	if _, err := s.pb().UpdateStage(ctx, actorFrom(ctx), co.ID, pv.ID, cur.Key, service.UpdateStageInput{Name: &name, Key: &key, Kind: &kind, Config: &cfg}); err != nil {
		s.renderPipeline(w, r, co, pv.Key, "config", pipelineErr(ctx, err), nil, statusFor(err))
		return
	}
	s.pipelineDone(w, r, co, pv.Key, "config", "etapa")
}

func (s *server) handleDeleteStage(w http.ResponseWriter, r *http.Request, co domain.Company) {
	pv, ok := s.pipelineRef(w, r, co)
	if !ok {
		return
	}
	ctx := r.Context()
	if _, err := s.pb().DeleteStage(ctx, actorFrom(ctx), co.ID, pv.ID, r.PathValue("stage")); err != nil {
		s.renderPipeline(w, r, co, pv.Key, "config", pipelineErr(ctx, err), nil, statusFor(err))
		return
	}
	s.pipelineDone(w, r, co, pv.Key, "config", "etapa")
}

func (s *server) handleMoveStage(delta int) companyHandler {
	return func(w http.ResponseWriter, r *http.Request, co domain.Company) {
		pv, ok := s.pipelineRef(w, r, co)
		if !ok {
			return
		}
		ctx := r.Context()
		keys := make([]string, len(pv.Stages))
		idx := -1
		for i, st := range pv.Stages {
			keys[i] = st.Key
			if st.Key == r.PathValue("stage") {
				idx = i
			}
		}
		if j := idx + delta; idx >= 0 && j >= 0 && j < len(keys) {
			keys[idx], keys[j] = keys[j], keys[idx]
			if _, err := s.pb().ReorderStages(ctx, actorFrom(ctx), co.ID, pv.ID, keys); err != nil {
				s.renderPipeline(w, r, co, pv.Key, "config", pipelineErr(ctx, err), nil, statusFor(err))
				return
			}
		}
		s.pipelineDone(w, r, co, pv.Key, "config", "etapa")
	}
}

func (s *server) handleSetTransitions(w http.ResponseWriter, r *http.Request, co domain.Company) {
	pv, ok := s.pipelineRef(w, r, co)
	if !ok {
		return
	}
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, &co, domain.Invalid("form", "formulário inválido"))
		return
	}
	var specs []service.TransitionSpec
	for _, e := range r.PostForm["edge"] {
		f, t, ok := strings.Cut(e, ">")
		if ok {
			specs = append(specs, service.TransitionSpec{From: f, To: t})
		}
	}
	if _, err := s.pb().SetTransitions(ctx, actorFrom(ctx), co.ID, pv.ID, specs, checked(r, "strict")); err != nil {
		s.renderPipeline(w, r, co, pv.Key, "config", pipelineErr(ctx, err), nil, statusFor(err))
		return
	}
	s.pipelineDone(w, r, co, pv.Key, "config", "transicoes")
}

// formFields lê os campos "f.<key>" de um formulário de case.
func formFields(r *http.Request) (map[string]any, map[string]string) {
	vals, raw := map[string]any{}, map[string]string{}
	_ = r.ParseForm()
	for k, vs := range r.PostForm {
		if !strings.HasPrefix(k, "f.") || len(vs) == 0 {
			continue
		}
		key, v := strings.TrimPrefix(k, "f."), strings.TrimSpace(vs[0])
		raw[key] = v
		if v != "" {
			vals[key] = v
		}
	}
	return vals, raw
}

// typedFields converte os campos number das etapas em número.
func typedFields(pv service.PipelineView, vals map[string]any) map[string]any {
	for _, st := range pv.Stages {
		for _, f := range st.Config.Fields {
			if f.Type != domain.FieldNumber {
				continue
			}
			if s, ok := vals[f.Key].(string); ok {
				if n, ok := parseNum(s); ok {
					vals[f.Key] = n
				}
			}
		}
	}
	return vals
}

func (s *server) handleCreatePipelineCase(w http.ResponseWriter, r *http.Request, co domain.Company) {
	pv, ok := s.pipelineRef(w, r, co)
	if !ok {
		return
	}
	ctx := r.Context()
	vals, raw := formFields(r)
	title := trimForm(r, "title")
	cv, _, err := s.pb().CreateCase(ctx, actorFrom(ctx), co.ID, service.CreateCaseInput{Title: title, PipelineID: &pv.ID, Fields: typedFields(pv, vals)})
	if err != nil {
		s.renderPipeline(w, r, co, pv.Key, "quadro", pipelineErr(ctx, err), &addDraft{title: title, fields: raw}, statusFor(err))
		return
	}
	redirect(w, r, caseHref("/c/"+co.Slug, cv.Identifier)+"?ok=criado")
}

// ---------- nomes dos autores ----------

type actorNames struct {
	users    map[domain.ID]string
	personas map[domain.ID]string
	you      string
}

func (s *server) actorNames(ctx context.Context, companyID domain.ID, personas []domain.Persona) actorNames {
	n := actorNames{users: map[domain.ID]string{}, personas: map[domain.ID]string{}, you: i18n.T(ctx, ownerName)}
	if ms, err := s.be.ListMembers(ctx, companyID); err == nil {
		for _, m := range ms {
			name := m.Name
			if name == "" {
				name = m.Email
			}
			n.users[m.UserID] = name
		}
	}
	for _, p := range personas {
		n.personas[p.ID] = p.Name
	}
	return n
}

func (n actorNames) of(ctx context.Context, a domain.Actor) string {
	switch a.Type {
	case domain.ActorHuman:
		if a.ID == nil {
			return n.you
		}
		if v := n.users[*a.ID]; v != "" {
			return v
		}
		return i18n.T(ctx, "um membro")
	case domain.ActorPersona:
		if a.ID != nil && n.personas[*a.ID] != "" {
			return n.personas[*a.ID]
		}
		return "Persona"
	}
	return "Hanko"
}

func payloadStr(p map[string]any, k string) string {
	v, _ := p[k].(string)
	return v
}

// caseEventRow descreve um evento do histórico em uma frase.
func caseEventRow(ctx context.Context, e domain.CaseEvent, names actorNames, now time.Time, loc *time.Location) views.CaseEventRow {
	p := e.Payload
	row := views.CaseEventRow{When: agoCtx(ctx, e.CreatedAt, now, loc), Actor: names.of(ctx, e.Actor), Reason: payloadStr(p, "reason"), Icon: "clock"}
	to, from := payloadStr(p, "to_name"), payloadStr(p, "from_name")
	switch e.Kind {
	case domain.CaseEvCreated:
		row.Text, row.Icon = i18n.T(ctx, "criou o case"), "plus"
	case domain.CaseEvUpdated:
		row.Text, row.Icon = i18n.T(ctx, "editou o case"), "pen-line"
	case domain.CaseEvFieldsChanged:
		row.Text, row.Icon = i18n.T(ctx, "mudou campos"), "pen-line"
	case domain.CaseEvStatusChanged:
		row.Text = i18n.Tf(ctx, "mudou o status para %s", caseStatusLabel(ctx, domain.CaseStatus(payloadStr(p, "to"))))
	case domain.CaseEvTransitioned:
		row.Text, row.Icon = i18n.Tf(ctx, "moveu de %s para %s", from, to), "chevron-right"
	case domain.CaseEvTransitionForced:
		row.Text, row.Icon = i18n.Tf(ctx, "forçou a passagem de %s para %s", from, to), "alert"
	case domain.CaseEvTransitionSuggested:
		row.Text, row.Icon, row.Reason = i18n.Tf(ctx, "sugeriu mover para %s", to), "sparkles", payloadStr(p, "rationale")
	case domain.CaseEvSuggestionResolved:
		switch payloadStr(p, "resolution") {
		case "accepted":
			row.Text = i18n.T(ctx, "aceitou a sugestão")
		case "dismissed":
			row.Text = i18n.T(ctx, "dispensou a sugestão")
		default:
			row.Text = i18n.T(ctx, "a sugestão ficou para trás (o case foi movido)")
		}
	case domain.CaseEvReviewDecided:
		row.Icon = "clipboard-check"
		switch domain.ReviewDecision(payloadStr(p, "decision")) {
		case domain.ReviewApprove:
			row.Text = i18n.Tf(ctx, "aprovou e moveu para %s", to)
		case domain.ReviewReject:
			row.Text = i18n.Tf(ctx, "recusou e moveu para %s", to)
		default:
			row.Text = i18n.Tf(ctx, "pediu mudanças e devolveu para %s", to)
		}
	case domain.CaseEvTaskLinked:
		row.Text, row.Icon = i18n.Tf(ctx, "ligou a task %s (%s)", payloadStr(p, "task_key"), linkRoleLabel(ctx, domain.CaseLinkRole(payloadStr(p, "role")))), "link"
	case domain.CaseEvTaskUnlinked:
		row.Text, row.Icon = i18n.T(ctx, "desligou uma task"), "link"
	case domain.CaseEvAutomationStarted:
		row.Text, row.Icon = i18n.Tf(ctx, "abriu a task %s para a persona %s", payloadStr(p, "task_key"), payloadStr(p, "persona")), "bot"
	case domain.CaseEvAutomationFailed:
		row.Text, row.Icon = i18n.T(ctx, "a automação da etapa não rodou"), "alert"
	case domain.CaseEvBlockersSet:
		row.Text, row.Icon = i18n.T(ctx, "trocou os bloqueios"), "lock"
	case domain.CaseEvBlockersResolved:
		row.Text, row.Icon = i18n.T(ctx, "os bloqueios foram resolvidos"), "check"
	case domain.CaseEvChildrenTerminal:
		row.Text, row.Icon = i18n.T(ctx, "todos os filhos terminaram"), "check"
	default:
		row.Text = string(e.Kind)
	}
	if e.Kind == domain.CaseEvAutomationFailed {
		row.Reason = payloadStr(p, "reason")
	}
	return row
}

// numOrText exibe um valor de campo.
func numOrText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		if x {
			return "true"
		}
		return "false"
	}
	return fmt.Sprint(v)
}
