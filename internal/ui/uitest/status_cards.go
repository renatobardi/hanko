package uitest

import (
	"context"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Cards de status do Fake (ADR-0032): a máquina de estados real fica no service; aqui só o necessário para as
// telas (lista, detalhe com abas, formulários e ações).

// StatusCardsData guarda os cards do Fake.
type StatusCardsData struct {
	Cards     []service.StatusCardView
	Updates   map[domain.ID][]service.StatusCardUpdateView
	Revisions map[domain.ID][]domain.StatusCardRevision
	Previews  map[domain.ID]service.StatusCardPreview
	// LastRefreshFull é o full do último RefreshStatusCard.
	LastRefreshFull bool
}

func (f *Fake) cards() *StatusCardsData {
	if f.Cards == nil {
		f.Cards = &StatusCardsData{}
	}
	if f.Cards.Updates == nil {
		f.Cards.Updates = map[domain.ID][]service.StatusCardUpdateView{}
		f.Cards.Revisions = map[domain.ID][]domain.StatusCardRevision{}
		f.Cards.Previews = map[domain.ID]service.StatusCardPreview{}
	}
	return f.Cards
}

func (f *Fake) cardIndex(id domain.ID) int {
	for i, c := range f.cards().Cards {
		if c.ID == id {
			return i
		}
	}
	return -1
}

func (f *Fake) refreshCardView(i int) {
	c := &f.Cards.Cards[i]
	c.ViewState = c.View()
	c.SummarizerName, c.SummarizerSlug = "", ""
	if c.SummarizerPersonaID != nil {
		for _, p := range f.Personas {
			if p.ID == *c.SummarizerPersonaID {
				c.SummarizerName, c.SummarizerSlug = p.Name, p.Slug
			}
		}
	}
}

func (f *Fake) ListStatusCards(_ context.Context, _ domain.ID, archived *bool) ([]service.StatusCardView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ListStatusCards"); err != nil {
		return nil, err
	}
	var out []service.StatusCardView
	for i := range f.cards().Cards {
		f.refreshCardView(i)
		c := f.Cards.Cards[i]
		if archived != nil && (c.ArchivedAt != nil) != *archived {
			continue
		}
		c.Mentioned = nil
		out = append(out, c)
	}
	return out, nil
}

func (f *Fake) GetStatusCard(_ context.Context, _, id domain.ID) (service.StatusCardView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("GetStatusCard"); err != nil {
		return service.StatusCardView{}, err
	}
	i := f.cardIndex(id)
	if i < 0 {
		return service.StatusCardView{}, domain.ErrNotFound
	}
	f.refreshCardView(i)
	return f.Cards.Cards[i], nil
}

func (f *Fake) CreateStatusCard(_ context.Context, a domain.Actor, companyID domain.ID, in service.CreateStatusCardInput) (service.StatusCardView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("CreateStatusCard"); err != nil {
		return service.StatusCardView{}, err
	}
	if strings.TrimSpace(in.Interest) == "" {
		return service.StatusCardView{}, domain.Invalid("interest", "descreva o interesse")
	}
	pol := domain.DefaultCardPolicy()
	if in.Policy != nil {
		pol = *in.Policy
	}
	if err := pol.Normalize(); err != nil {
		return service.StatusCardView{}, err
	}
	now := Now()
	c := service.StatusCardView{StatusCard: domain.StatusCard{ID: domain.NewID(), CompanyID: companyID, Title: strings.TrimSpace(in.Title), TitlePinned: in.TitlePinned,
		Interest: strings.TrimSpace(in.Interest), SummarizerPersonaID: in.SummarizerPersonaID, Policy: pol, State: domain.CardCompiling,
		Queries: []domain.StatusCardQuery{}, MentionedTaskIDs: []domain.ID{}, CreatedBy: a, CreatedAt: now, UpdatedAt: now}}
	if in.SummarizerPersonaID != nil {
		gen := domain.NewID()
		c.GeneratingTaskID, c.GeneratingSince = &gen, &now
	} else {
		c.State, c.FailureReason = domain.CardError, "Escolha a persona resumidora para montar o card."
	}
	f.cards().Cards = append(f.Cards.Cards, c)
	f.Calls = append(f.Calls, "CreateStatusCard")
	i := len(f.Cards.Cards) - 1
	f.refreshCardView(i)
	return f.Cards.Cards[i], nil
}

func (f *Fake) UpdateStatusCard(_ context.Context, _ domain.Actor, _, id domain.ID, in service.UpdateStatusCardInput) (service.StatusCardView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("UpdateStatusCard"); err != nil {
		return service.StatusCardView{}, err
	}
	i := f.cardIndex(id)
	if i < 0 {
		return service.StatusCardView{}, domain.ErrNotFound
	}
	c := &f.Cards.Cards[i]
	if in.Policy != nil {
		p := *in.Policy
		if err := p.Normalize(); err != nil {
			return service.StatusCardView{}, err
		}
		c.Policy = p
	}
	if in.Interest != nil {
		if strings.TrimSpace(*in.Interest) == "" {
			return service.StatusCardView{}, domain.Invalid("interest", "descreva o interesse")
		}
		c.Interest = *in.Interest
	}
	if in.Title != nil {
		c.Title = *in.Title
	}
	if in.TitlePinned != nil {
		c.TitlePinned = *in.TitlePinned
	}
	if in.SummarizerPersonaID != nil {
		c.SummarizerPersonaID = in.SummarizerPersonaID
	}
	now := Now()
	if in.Archived != nil {
		if *in.Archived {
			c.ArchivedAt, c.GeneratingTaskID = &now, nil
			f.Calls = append(f.Calls, "ArchiveStatusCard")
		} else {
			c.ArchivedAt = nil
			f.Calls = append(f.Calls, "RestoreStatusCard")
		}
	} else {
		f.Calls = append(f.Calls, "UpdateStatusCard")
	}
	c.UpdatedAt = now
	f.refreshCardView(i)
	return *c, nil
}

func (f *Fake) DeleteStatusCard(_ context.Context, _ domain.Actor, _, id domain.ID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("DeleteStatusCard"); err != nil {
		return err
	}
	i := f.cardIndex(id)
	if i < 0 {
		return domain.ErrNotFound
	}
	f.Cards.Cards = append(f.Cards.Cards[:i], f.Cards.Cards[i+1:]...)
	f.Calls = append(f.Calls, "DeleteStatusCard")
	return nil
}

func (f *Fake) RecompileStatusCard(_ context.Context, _ domain.Actor, _, id domain.ID) (service.StatusCardView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("RecompileStatusCard"); err != nil {
		return service.StatusCardView{}, err
	}
	i := f.cardIndex(id)
	if i < 0 {
		return service.StatusCardView{}, domain.ErrNotFound
	}
	c := &f.Cards.Cards[i]
	gen, now := domain.NewID(), Now()
	c.State, c.FailureReason, c.GeneratingTaskID, c.GeneratingSince = domain.CardCompiling, "", &gen, &now
	f.Calls = append(f.Calls, "RecompileStatusCard")
	f.refreshCardView(i)
	return *c, nil
}

func (f *Fake) RefreshStatusCard(_ context.Context, _ domain.Actor, _, id domain.ID, full bool) (service.StatusCardView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("RefreshStatusCard"); err != nil {
		return service.StatusCardView{}, err
	}
	i := f.cardIndex(id)
	if i < 0 {
		return service.StatusCardView{}, domain.ErrNotFound
	}
	c := &f.Cards.Cards[i]
	if c.GeneratingTaskID != nil {
		return service.StatusCardView{}, domain.ErrConflict
	}
	gen, now := domain.NewID(), Now()
	c.State, c.GeneratingTaskID, c.GeneratingSince = domain.CardActive, &gen, &now
	f.Cards.LastRefreshFull = full
	f.Calls = append(f.Calls, "RefreshStatusCard")
	f.refreshCardView(i)
	return *c, nil
}

func (f *Fake) ListStatusCardUpdates(_ context.Context, _, id domain.ID, _ int) ([]service.StatusCardUpdateView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cardIndex(id) < 0 {
		return nil, domain.ErrNotFound
	}
	return f.Cards.Updates[id], nil
}

func (f *Fake) ListStatusCardRevisions(_ context.Context, _, id domain.ID, _ int) ([]domain.StatusCardRevision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cardIndex(id) < 0 {
		return nil, domain.ErrNotFound
	}
	return f.Cards.Revisions[id], nil
}

func (f *Fake) PreviewStatusCard(_ context.Context, _, id domain.ID) (service.StatusCardPreview, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cardIndex(id) < 0 {
		return service.StatusCardPreview{}, domain.ErrNotFound
	}
	return f.Cards.Previews[id], nil
}

// SampleStatusCards põe no Fake três cards: um em dia (com resumo, revisões, rodadas e filtros), um montando e um
// arquivado. Devolve o id do card em dia.
func SampleStatusCards(f *Fake) domain.ID {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := f.cards()
	now := Now()
	co := f.Companies[0]
	var sum *domain.ID
	if len(f.Personas) > 0 {
		id := f.Personas[len(f.Personas)-2].ID
		sum = &id
	}
	var t1 service.TaskView
	if len(f.Tasks) > 0 {
		t1 = f.Tasks[0]
	}
	gen := now.Add(-10 * time.Minute)
	runID := domain.NewID()
	fresh := service.StatusCardView{StatusCard: domain.StatusCard{ID: ID("card:bloqueios"), CompanyID: co.ID, Title: "Bloqueios do Kubo",
		Interest: "tudo que está bloqueado no projeto Kubo e os bugs críticos", SummarizerPersonaID: sum, Policy: domain.DefaultCardPolicy(),
		State: domain.CardActive, QueryVersion: 2, QueryCompiledAt: &gen, SummaryRevision: 2, LastUpdateKind: domain.CardUpdateIncremental,
		LastGeneratedAt: &gen, Summary: "## Bloqueios\n\n- **" + t1.Key + "** espera o deploy.\n- Nenhum bug crítico aberto.",
		MentionedTaskIDs: []domain.ID{t1.ID}, PendingChangeCount: 1,
		Queries:   []domain.StatusCardQuery{{Label: "bloqueadas", Statuses: []domain.TaskStatus{domain.TaskBlocked}}, {Priorities: []domain.Priority{domain.PriorityCritical}, OpenOnly: true}},
		CreatedBy: domain.HumanActor, CreatedAt: now.Add(-48 * time.Hour), UpdatedAt: gen},
		Mentioned: []service.CardTaskRef{{ID: t1.ID, Key: t1.Key, Title: t1.Title, Status: t1.Status, Priority: t1.Priority, PersonaName: t1.PersonaName}}}
	since := now.Add(-time.Minute)
	genTask := domain.NewID()
	compiling := service.StatusCardView{StatusCard: domain.StatusCard{ID: ID("card:montando"), CompanyID: co.ID, Interest: "o que a Hana pesquisa esta semana",
		SummarizerPersonaID: sum, Policy: domain.DefaultCardPolicy(), State: domain.CardCompiling, GeneratingTaskID: &genTask, GeneratingSince: &since,
		Queries: []domain.StatusCardQuery{}, MentionedTaskIDs: []domain.ID{}, CreatedBy: domain.HumanActor, CreatedAt: since, UpdatedAt: since}}
	arch := now.Add(-24 * time.Hour)
	archived := service.StatusCardView{StatusCard: domain.StatusCard{ID: ID("card:antigo"), CompanyID: co.ID, Title: "Lançamento de agosto",
		Interest: "o lançamento de agosto", Policy: domain.DefaultCardPolicy(), State: domain.CardActive, QueryVersion: 1, SummaryRevision: 5,
		Summary: "Tudo entregue.", ArchivedAt: &arch, Queries: []domain.StatusCardQuery{}, MentionedTaskIDs: []domain.ID{},
		CreatedBy: domain.HumanActor, CreatedAt: arch, UpdatedAt: arch}}
	d.Cards = append(d.Cards, fresh, compiling, archived)
	cost := 0.0123
	d.Updates[fresh.ID] = []service.StatusCardUpdateView{
		{StatusCardUpdate: domain.StatusCardUpdate{ID: domain.NewID(), CardID: fresh.ID, Kind: domain.CardUpdateIncremental, Trigger: domain.CardTriggerReactive,
			RunID: &runID, Changes: []domain.CardChange{{TaskID: t1.ID.String(), Key: t1.Key, From: "in_progress", To: "blocked", ChangeKind: domain.ChangeStatus}},
			InputTokens: 4200, OutputTokens: 800, CostUSD: &cost, QueryVersion: 2, ChangeSummary: "um bloqueio novo", StartedAt: gen, Status: domain.CardUpdateOK},
			TaskKey: "HNK-90", RunName: "Ágil_Falcão"},
		{StatusCardUpdate: domain.StatusCardUpdate{ID: domain.NewID(), CardID: fresh.ID, Kind: domain.CardUpdateCompile, Trigger: domain.CardTriggerManual,
			Changes: []domain.CardChange{}, InputTokens: 900, OutputTokens: 100, QueryVersion: 1, StartedAt: now.Add(-48 * time.Hour), Status: domain.CardUpdateFailed,
			Error: "A montagem não terminou: a execução acabou (done) sem chamar hanko_status_card_set_query."}, TaskKey: "HNK-80"},
	}
	d.Revisions[fresh.ID] = []domain.StatusCardRevision{
		{ID: domain.NewID(), CardID: fresh.ID, Revision: 2, Markdown: fresh.Summary, Kind: domain.CardUpdateIncremental, RunID: &runID, CreatedAt: gen},
		{ID: domain.NewID(), CardID: fresh.ID, Revision: 1, Markdown: "## Primeira versão\n\nNada bloqueado.", Kind: domain.CardUpdateFull, CreatedAt: now.Add(-24 * time.Hour)},
	}
	hc := now.Add(-time.Hour)
	d.Previews[fresh.ID] = service.StatusCardPreview{QueryVersion: 2,
		Queries: []service.CardQueryPreview{{Query: fresh.Queries[0], Description: "bloqueadas — status: blocked", Matches: 1},
			{Query: fresh.Queries[1], Description: "prioridade: critical · só abertas", Matches: 0}},
		Tasks: []service.CardMatchedTask{{CardTaskRef: fresh.Mentioned[0], UpdatedAt: gen, HumanCommentAt: &hc, HumanComment: "consegue destravar hoje?", Queries: []int{0}}}}
	return fresh.ID
}
