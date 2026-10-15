package ui

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// MemoryBackend é a fatia do service que a seção "Memória" das Configurações usa (consolidação de memória
// governada, ADR-0030). É opcional: um backend que não a implementa só esconde a seção.
type MemoryBackend interface {
	GetMemoryStatus(ctx context.Context, companyID domain.ID) (service.MemoryStatus, error)
	UpdateMemorySettings(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.UpdateMemorySettingsInput) (service.MemoryStatus, error)
	ConsolidateMemoryNow(ctx context.Context, actor domain.Actor, companyID domain.ID, workspaceRef string) ([]domain.MemoryConsolidation, error)
	ListMemoryConsolidations(ctx context.Context, companyID domain.ID, limit int) ([]service.MemoryConsolidationView, error)
	ListMemoryProposals(ctx context.Context, companyID domain.ID, statuses []domain.MemoryProposalStatus, limit int) ([]service.MemoryProposalView, error)
	RetryMemoryProposal(ctx context.Context, actor domain.Actor, companyID, id domain.ID) (service.MemoryProposalView, error)
	ListWorkspaces(ctx context.Context, companyID domain.ID) ([]domain.Workspace, error)
}

var _ MemoryBackend = (*service.Service)(nil)

func (s *server) memBackend() (MemoryBackend, bool) {
	m, ok := s.be.(MemoryBackend)
	return m, ok
}

var memoryNotices = map[string]string{
	"salvo":        i18n.N("Configuração da memória salva."),
	"consolidando": i18n.N("Consolidação aberta: a persona curadora trabalha como em qualquer execução. O que ela quiser remover, fundir ou reescrever aparece na Caixa de entrada."),
	"repetindo":    i18n.N("A aplicação da proposta voltou para a fila."),
}

func consolidationBadge(ctx context.Context, st domain.MemoryConsolidationStatus) views.Badge {
	switch st {
	case domain.ConsolidationOpen:
		return views.Badge{Label: i18n.T(ctx, "Em andamento"), Class: "warn", Live: true}
	case domain.ConsolidationDone:
		return views.Badge{Label: i18n.T(ctx, "Concluída"), Class: "done"}
	case domain.ConsolidationFailed:
		return views.Badge{Label: i18n.T(ctx, "Falhou"), Class: "danger"}
	case domain.ConsolidationCancelled:
		return views.Badge{Label: i18n.T(ctx, "Cancelada"), Class: "outline"}
	case domain.ConsolidationExpired:
		return views.Badge{Label: i18n.T(ctx, "Não rodou"), Class: "soft"}
	}
	return views.Badge{Label: string(st), Class: "outline"}
}

func proposalBadge(ctx context.Context, st domain.MemoryProposalStatus) views.Badge {
	switch st {
	case domain.ProposalPending:
		return views.Badge{Label: i18n.T(ctx, "Pendente"), Class: "warn"}
	case domain.ProposalApproved:
		return views.Badge{Label: i18n.T(ctx, "Aprovada, aplicando"), Class: "outline"}
	case domain.ProposalRejected:
		return views.Badge{Label: i18n.T(ctx, "Rejeitada"), Class: "soft"}
	case domain.ProposalApplied:
		return views.Badge{Label: i18n.T(ctx, "Aplicada"), Class: "done"}
	case domain.ProposalFailed:
		return views.Badge{Label: i18n.T(ctx, "Falhou ao aplicar"), Class: "danger"}
	}
	return views.Badge{Label: string(st), Class: "outline"}
}

func proposalKindLabel(ctx context.Context, k domain.MemoryProposalKind) string {
	switch k {
	case domain.ProposalMerge:
		return i18n.T(ctx, "Fusão")
	case domain.ProposalDelete:
		return i18n.T(ctx, "Remoção")
	case domain.ProposalRewrite:
		return i18n.T(ctx, "Reescrita")
	}
	return string(k)
}

func memoryOriginLabel(ctx context.Context, o domain.MemoryOrigin) string {
	if o == domain.MemoryManual {
		return i18n.T(ctx, "manual")
	}
	return i18n.T(ctx, "agendada")
}

// memoryVM monta a seção "Memória". A seção some quando o backend não suporta a consolidação; erros ao
// montá-la viram aviso na própria seção, sem derrubar as Configurações.
func (s *server) memoryVM(r *http.Request, co domain.Company, pre views.MemoryVM) views.MemoryVM {
	m, ok := s.memBackend()
	if !ok {
		return views.MemoryVM{}
	}
	ctx := r.Context()
	vm := views.MemoryVM{Show: true, CanAdmin: views.RoleFrom(ctx).AtLeast(domain.MemberAdmin), Notice: pre.Notice, Error: pre.Error}
	if vm.Notice == "" {
		if k := r.URL.Query().Get("mem"); memoryNotices[k] != "" {
			vm.Notice = i18n.T(ctx, memoryNotices[k])
		}
	}
	fail := func(err error) views.MemoryVM {
		s.log.Error("ui: seção memória", "err", err, "company", co.Slug)
		vm.Error = i18n.T(ctx, "Não foi possível ler o estado da memória. Tente de novo.")
		return vm
	}
	st, err := m.GetMemoryStatus(ctx, co.ID)
	if err != nil {
		return fail(err)
	}
	base, loc, now := "/c/"+co.Slug, loadLoc(co.Timezone), s.now()
	vm.Available, vm.Provider = st.Available, st.Provider
	vm.Enabled, vm.IntervalHours = st.Settings.Enabled, st.Settings.IntervalHours
	vm.CuratorID = idOrEmpty(st.Settings.CuratorPersonaID)
	vm.Pending = st.PendingProposals
	vm.InboxHref = base + "/gates?status=pendentes&tipo=memoria"
	vm.LastFire, vm.NextFire = i18n.T(ctx, "nunca"), "—"
	if st.Settings.LastFiredAt != nil {
		vm.LastFire = fmtWhen(ctx, *st.Settings.LastFiredAt, now, loc)
	}
	if st.Settings.Enabled && st.Settings.NextFireAt != nil {
		vm.NextFire = fmtWhen(ctx, *st.Settings.NextFireAt, now, loc)
	}
	if !st.Available {
		return vm
	}

	personas, err := s.be.ListPersonas(ctx, co.ID)
	if err != nil {
		return fail(err)
	}
	vm.Curators = []views.Option{{Value: "", Label: i18n.T(ctx, "— nenhuma —"), Selected: vm.CuratorID == ""}}
	for _, p := range personas {
		vm.Curators = append(vm.Curators, views.Option{Value: p.ID.String(), Label: p.Name + " (" + p.Slug + ")", Selected: p.ID.String() == vm.CuratorID})
	}
	wss, err := m.ListWorkspaces(ctx, co.ID)
	if err != nil {
		return fail(err)
	}
	vm.Workspaces = []views.Option{{Value: "", Label: i18n.T(ctx, "Todos os workspaces"), Selected: true}}
	for _, w := range wss {
		vm.Workspaces = append(vm.Workspaces, views.Option{Value: w.Slug, Label: w.Name + " (" + w.Slug + ")"})
	}

	cs, err := m.ListMemoryConsolidations(ctx, co.ID, 15)
	if err != nil {
		return fail(err)
	}
	for _, c := range cs {
		row := views.MemoryConsolidationRow{When: fmtWhen(ctx, c.CreatedAt, now, loc), Workspace: c.WorkspaceSlug,
			Origin: memoryOriginLabel(ctx, c.Origin), Status: consolidationBadge(ctx, c.Status), TaskKey: c.TaskKey,
			Proposals: c.Proposals, Cost: fmtCostPtrCtx(ctx, c.CostUSD), Summary: c.Summary}
		if c.TaskKey != "" {
			row.TaskHref = base + "/tasks/" + c.TaskKey
		}
		if c.RunID != nil {
			row.RunHref = base + "/execucoes/" + c.RunID.String()
		}
		vm.Consolidations = append(vm.Consolidations, row)
	}
	ps, err := m.ListMemoryProposals(ctx, co.ID, nil, 15)
	if err != nil {
		return fail(err)
	}
	for _, p := range ps {
		row := views.MemoryProposalRow{ID: p.ID.String(), When: fmtWhen(ctx, p.CreatedAt, now, loc), Kind: proposalKindLabel(ctx, p.Kind),
			Workspace: p.WorkspaceSlug, Target: "—", Status: proposalBadge(ctx, p.Status), Error: p.LastError, Diff: p.Diff}
		if len(p.Targets) > 0 {
			row.Target = p.Targets[0]
			if len(p.Targets) > 1 {
				row.Target += " +" + strconv.Itoa(len(p.Targets)-1)
			}
		}
		if p.GateID != nil {
			row.GateHref = base + "/gates/" + p.GateID.String()
		}
		if p.Status == domain.ProposalFailed {
			row.CanRetry = vm.CanAdmin
			row.RetryHref = base + "/configuracoes/memoria/propostas/" + p.ID.String() + "/repetir"
			if p.NextAttemptAt != nil {
				row.NextTry = fmtWhen(ctx, *p.NextAttemptAt, now, loc)
			}
		}
		vm.Proposals = append(vm.Proposals, row)
	}
	return vm
}

func idOrEmpty(id *domain.ID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

// memoryFail re-renderiza as Configurações com o erro na seção "Memória".
func (s *server) memoryFail(w http.ResponseWriter, r *http.Request, co domain.Company, err error) {
	st, title, msg := friendly(r.Context(), err)
	if st >= 500 || st == http.StatusNotFound {
		s.fail(w, r, &co, err)
		return
	}
	vm, verr := s.settingsVM(r)
	if verr != nil {
		s.fail(w, r, &co, verr)
		return
	}
	vm.Memory.Error = title + ". " + msg
	s.renderSettings(w, r, co, st, vm)
}

func (s *server) handleMemorySave(w http.ResponseWriter, r *http.Request, co domain.Company) {
	m, ok := s.memBackend()
	if !ok {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	ctx := r.Context()
	enabled := r.PostFormValue("enabled") == "1"
	in := service.UpdateMemorySettingsInput{Enabled: &enabled}
	if raw := trimForm(r, "interval_hours"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			s.memoryFail(w, r, co, domain.Invalid(i18n.T(ctx, "intervalo"), i18n.T(ctx, "informe um número de horas")))
			return
		}
		in.IntervalHours = &n
	}
	if raw := trimForm(r, "curator"); raw == "" {
		in.ClearCurator = true
	} else {
		id, err := domain.ParseID(raw)
		if err != nil {
			s.memoryFail(w, r, co, domain.Invalid(i18n.T(ctx, "curador"), i18n.T(ctx, "escolha uma persona da lista")))
			return
		}
		in.CuratorPersonaID = &id
	}
	if _, err := m.UpdateMemorySettings(ctx, actorFrom(ctx), co.ID, in); err != nil {
		s.memoryFail(w, r, co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/configuracoes?mem=salvo#memoria")
}

func (s *server) handleMemoryConsolidate(w http.ResponseWriter, r *http.Request, co domain.Company) {
	m, ok := s.memBackend()
	if !ok {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	if _, err := m.ConsolidateMemoryNow(r.Context(), actorFrom(r.Context()), co.ID, strings.TrimSpace(r.PostFormValue("workspace"))); err != nil {
		s.memoryFail(w, r, co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/configuracoes?mem=consolidando#memoria")
}

func (s *server) handleMemoryRetry(w http.ResponseWriter, r *http.Request, co domain.Company) {
	m, ok := s.memBackend()
	if !ok {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	id, err := domain.ParseID(r.PathValue("proposal"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	if _, err := m.RetryMemoryProposal(r.Context(), actorFrom(r.Context()), co.ID, id); err != nil {
		s.memoryFail(w, r, co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/configuracoes?mem=repetindo#memoria")
}
