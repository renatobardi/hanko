// Package uitest provides an in-memory service backend with realistic sample
// data. It is shared by the UI tests and by the visual-QA tool in
// internal/ui/preview; production code must not import it.
package uitest

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Fake is a mutable in-memory backend. Err injects a failure per method name
// (e.g. Err["GetTask"] = domain.ErrNotFound).
type Fake struct {
	mu         sync.Mutex
	Err        map[string]error
	Companies  []domain.Company
	Personas   []domain.Persona
	Projects   []domain.Project
	Workspaces []domain.Workspace
	Tasks      []service.TaskView
	Comments   []domain.Comment
	Runs       []service.RunView
	Events     map[domain.ID][]domain.RunEvent
	Activity   []domain.ActivityEvent
	Health     map[domain.ID]service.PersonaHealth
	Alerts     []domain.Alert
	Reviewing  []service.InboxReview // automatic reviews in progress (Caixa de entrada)
	Gates      []service.GateView
	Calls      []string // names of mutating calls, in order
	Ledger     []LedgerEntry
	Flows      []service.FlowView
	Fires      []service.FlowFireView
	Subs       []service.SubscriptionStatus
	Members    []domain.Member
	Invites    []domain.Invite
	// Artefatos e metas (ADR-0028). ArtifactData guarda o conteúdo de cada versão; os limites zerados valem o padrão.
	Artifacts          []domain.Artifact
	ArtifactData       map[domain.ID][]byte
	ArtifactMaxBytes   int64
	ArtifactQuotaBytes int64
	Goals              []domain.Goal
	GoalUpdates        []domain.GoalUpdate
	// SearchRes é o que Search devolve (e SearchQ/SearchF a última chamada); Memory liga MemorySearchAvailable.
	SearchRes service.SearchResult
	SearchQ   string
	SearchF   service.SearchFilter
	Memory    bool
	// Conversations and ConvMessages are the chat (ADR-0027).
	Conversations []service.ConversationView
	ConvMessages  []domain.ConversationMessage
	// MyRoleIs é o papel do usuário logado nesta empresa (vazio = owner).
	MyRoleIs domain.MemberRole
	// Cards são os cards de status (ADR-0032).
	Cards    *StatusCardsData
	nextTask int64

	// Env são os ambientes de execução (ADR-0037).
	Env EnvFake
}

var _ = (*Fake)(nil)

func (f *Fake) fail(name string) error {
	if f.Err == nil {
		return nil
	}
	return f.Err[name]
}

// SetErr injects (or clears, with nil) a failure for a method while handlers are running.
func (f *Fake) SetErr(name string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err == nil {
		f.Err = map[string]error{}
	}
	f.Err[name] = err
}

func (f *Fake) note(name string) { f.Calls = append(f.Calls, name) }

// Called reports whether a mutating method was invoked.
func (f *Fake) Called(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.Calls {
		if c == name {
			return true
		}
	}
	return false
}

// AppendEvent adds a run event (as the executor would) and returns it.
func (f *Fake) AppendEvent(runID domain.ID, kind domain.RunEventKind, text string) domain.RunEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Events == nil {
		f.Events = map[domain.ID][]domain.RunEvent{}
	}
	seq := int64(len(f.Events[runID]) + 1)
	e := domain.RunEvent{ID: domain.NewID(), RunID: runID, Seq: seq, Kind: kind, Text: text, OccurredAt: time.Now().UTC(), ReceivedAt: time.Now().UTC()}
	f.Events[runID] = append(f.Events[runID], e)
	return e
}

// FinishRun marks a run finished (as the executor would).
func (f *Fake) FinishRun(runID domain.ID, st domain.RunStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.Runs {
		if f.Runs[i].ID == runID {
			now := time.Now().UTC()
			f.Runs[i].Status = st
			f.Runs[i].FinishedAt = &now
		}
	}
}

func (f *Fake) CreateCompany(_ context.Context, actor domain.Actor, in service.CreateCompanyInput) (domain.Company, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("CreateCompany")
	if err := f.fail("CreateCompany"); err != nil {
		return domain.Company{}, err
	}
	if strings.TrimSpace(in.Name) == "" {
		return domain.Company{}, domain.Invalid("name", "obrigatório")
	}
	slug := in.Slug
	if slug == "" {
		slug = domain.Slugify(in.Name)
	}
	for _, c := range f.Companies {
		if c.Slug == slug {
			return domain.Company{}, domain.ErrConflict
		}
	}
	prefix := in.TaskPrefix
	if prefix == "" {
		prefix = "HNK"
	}
	c := domain.Company{ID: domain.NewID(), Slug: slug, Name: in.Name, TaskPrefix: prefix, Timezone: "America/Sao_Paulo", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	f.Companies = append(f.Companies, c)
	return c, nil
}

func (f *Fake) GetCompany(_ context.Context, ref string) (domain.Company, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("GetCompany"); err != nil {
		return domain.Company{}, err
	}
	for _, c := range f.Companies {
		if c.Slug == ref || c.ID.String() == ref {
			return c, nil
		}
	}
	return domain.Company{}, domain.ErrNotFound
}

func (f *Fake) ListCompanies(context.Context) ([]domain.Company, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ListCompanies"); err != nil {
		return nil, err
	}
	return append([]domain.Company(nil), f.Companies...), nil
}

func (f *Fake) ListProjects(_ context.Context, _ domain.ID) ([]domain.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.Project(nil), f.Projects...), f.fail("ListProjects")
}

func (f *Fake) HirePersona(_ context.Context, _ domain.Actor, companyID domain.ID, in service.HirePersonaInput) (domain.Persona, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("HirePersona")
	if err := f.fail("HirePersona"); err != nil {
		return domain.Persona{}, err
	}
	for _, p := range f.Personas {
		if p.Slug == in.Slug {
			return domain.Persona{}, domain.ErrConflict
		}
	}
	icon := in.Icon
	if icon == "" {
		icon = domain.DefaultPersonaIcon
	}
	if !domain.ValidPersonaIcon(icon) {
		return domain.Persona{}, domain.Invalid("icon", "unknown icon")
	}
	p := domain.Persona{ID: domain.NewID(), CompanyID: companyID, Slug: in.Slug, Name: in.Name, Title: in.Title, Icon: icon, Instructions: in.Instructions,
		Agent: in.Agent, Model: in.Model, AdapterConfig: in.AdapterConfig, Status: domain.PersonaIdle, HeartbeatIntervalSec: in.HeartbeatIntervalSec, ReportsTo: in.ReportsTo, RequireReview: in.RequireReview,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	f.Personas = append(f.Personas, p)
	return p, nil
}

func (f *Fake) ListPersonas(context.Context, domain.ID) ([]domain.Persona, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ListPersonas"); err != nil {
		return nil, err
	}
	return append([]domain.Persona(nil), f.Personas...), nil
}

func (f *Fake) GetPersona(_ context.Context, _ domain.ID, ref string) (domain.Persona, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("GetPersona"); err != nil {
		return domain.Persona{}, err
	}
	for _, p := range f.Personas {
		if p.Slug == ref || p.ID.String() == ref {
			return p, nil
		}
	}
	return domain.Persona{}, domain.ErrNotFound
}

func (f *Fake) GetPersonaHealth(_ context.Context, _, personaID domain.ID) (service.PersonaHealth, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("GetPersonaHealth"); err != nil {
		return service.PersonaHealth{}, err
	}
	return f.Health[personaID], nil
}

func (f *Fake) UpdatePersona(_ context.Context, _ domain.Actor, _, personaID domain.ID, in service.UpdatePersonaInput) (domain.Persona, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("UpdatePersona")
	if err := f.fail("UpdatePersona"); err != nil {
		return domain.Persona{}, err
	}
	for i, p := range f.Personas {
		if p.ID != personaID {
			continue
		}
		if in.Status != nil {
			if *in.Status != p.Status {
				delete(f.Health, p.ID) // a status change by the user resets the failure counter
			}
			p.Status = *in.Status
		}
		switch {
		case in.ClearReportsTo:
			p.ReportsTo = nil
		case in.ReportsTo != nil:
			if *in.ReportsTo == p.ID || f.descendant(p.ID, *in.ReportsTo) {
				return domain.Persona{}, domain.Invalid("reports_to", "would create a cycle in the chart")
			}
			id := *in.ReportsTo
			p.ReportsTo = &id
		}
		if in.RequireReview != nil {
			p.RequireReview = *in.RequireReview
		}
		if in.Icon != nil {
			if !domain.ValidPersonaIcon(*in.Icon) {
				return domain.Persona{}, domain.Invalid("icon", "unknown icon")
			}
			p.Icon = *in.Icon
		}
		if err := f.applyReviewer(&p, in); err != nil {
			return domain.Persona{}, err
		}
		if in.ClearBudget {
			p.BudgetMonthlyCents = nil
		} else if in.BudgetMonthlyCents != nil {
			p.BudgetMonthlyCents = in.BudgetMonthlyCents
		}
		if in.ClearSubscription {
			p.SubscriptionID = nil
		} else if in.SubscriptionID != nil {
			id := *in.SubscriptionID
			p.SubscriptionID = &id
		}
		f.Personas[i] = p
		return p, nil
	}
	return domain.Persona{}, domain.ErrNotFound
}

func (f *Fake) personaName(id *domain.ID) string {
	if id == nil {
		return ""
	}
	for _, p := range f.Personas {
		if p.ID == *id {
			return p.Name
		}
	}
	return ""
}

func (f *Fake) CreateTask(_ context.Context, _ domain.Actor, companyID domain.ID, in service.CreateTaskInput) (service.TaskView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("CreateTask")
	if err := f.fail("CreateTask"); err != nil {
		return service.TaskView{}, err
	}
	if strings.TrimSpace(in.Title) == "" {
		return service.TaskView{}, domain.Invalid("title", "obrigatório")
	}
	f.nextTask++
	n := f.nextTask
	now := time.Now().UTC()
	tv := service.TaskView{Task: domain.Task{ID: domain.NewID(), CompanyID: companyID, Number: n, Key: domain.TaskKey("HNK", n), Title: in.Title, Description: in.Description,
		Status: in.Status, Priority: in.Priority, PersonaID: in.PersonaID, ProjectID: in.ProjectID, GoalID: in.GoalID, Model: in.Model, CreatedBy: domain.HumanActor, CreatedAt: now, UpdatedAt: now}}
	tv.PersonaName = f.personaName(in.PersonaID)
	f.Tasks = append(f.Tasks, tv)
	return tv, nil
}

func (f *Fake) ListTasks(_ context.Context, _ domain.ID, fl service.TaskFilter) ([]service.TaskView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ListTasks"); err != nil {
		return nil, err
	}
	var out []service.TaskView
	for _, t := range f.Tasks {
		if len(fl.Statuses) > 0 {
			ok := false
			for _, s := range fl.Statuses {
				ok = ok || s == t.Status
			}
			if !ok {
				continue
			}
		}
		if fl.PersonaID != nil && (t.PersonaID == nil || *t.PersonaID != *fl.PersonaID) {
			continue
		}
		if fl.ProjectID != nil && (t.ProjectID == nil || *t.ProjectID != *fl.ProjectID) {
			continue
		}
		if fl.GoalID != nil && (t.GoalID == nil || *t.GoalID != *fl.GoalID) {
			continue
		}
		if q := strings.ToLower(fl.Query); q != "" && !strings.Contains(strings.ToLower(t.Title+" "+t.Key), q) {
			continue
		}
		out = append(out, t)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	if fl.Limit > 0 && len(out) > fl.Limit {
		out = out[:fl.Limit]
	}
	return out, nil
}

func (f *Fake) findTask(ref string) (int, bool) {
	for i, t := range f.Tasks {
		if t.Key == ref || t.ID.String() == ref {
			return i, true
		}
	}
	return 0, false
}

func (f *Fake) GetTask(_ context.Context, _ domain.ID, ref string) (service.TaskView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("GetTask"); err != nil {
		return service.TaskView{}, err
	}
	if i, ok := f.findTask(ref); ok {
		return f.Tasks[i], nil
	}
	return service.TaskView{}, domain.ErrNotFound
}

func (f *Fake) UpdateTask(_ context.Context, _ domain.Actor, _, taskID domain.ID, in service.UpdateTaskInput) (service.TaskView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("UpdateTask")
	if err := f.fail("UpdateTask"); err != nil {
		return service.TaskView{}, err
	}
	i, ok := f.findTask(taskID.String())
	if !ok {
		return service.TaskView{}, domain.ErrNotFound
	}
	t := &f.Tasks[i]
	if in.Status != nil {
		t.Status = *in.Status
	}
	if in.Priority != nil {
		t.Priority = *in.Priority
	}
	if in.Unassign {
		t.PersonaID, t.PersonaName = nil, ""
	}
	if in.PersonaID != nil {
		t.PersonaID = in.PersonaID
		t.PersonaName = f.personaName(in.PersonaID)
	}
	if in.ClearGoal {
		t.GoalID = nil
	}
	if in.GoalID != nil {
		t.GoalID = in.GoalID
	}
	if in.ClearProject {
		t.ProjectID, t.ProjectName = nil, ""
	}
	if in.ProjectID != nil {
		t.ProjectID, t.ProjectName = in.ProjectID, ""
		for _, p := range f.Projects {
			if p.ID == *in.ProjectID {
				if p.Status == domain.ProjectArchived {
					return service.TaskView{}, domain.Invalid("project", "arquivado")
				}
				t.ProjectName = p.Name
			}
		}
	}
	t.UpdatedAt = time.Now().UTC()
	return *t, nil
}

func (f *Fake) AddComment(_ context.Context, actor domain.Actor, companyID, taskID domain.ID, in service.AddCommentInput) (domain.Comment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("AddComment")
	if err := f.fail("AddComment"); err != nil {
		return domain.Comment{}, err
	}
	c := domain.Comment{ID: domain.NewID(), CompanyID: companyID, TaskID: taskID, Author: actor, Body: in.Body, CreatedAt: time.Now().UTC()}
	f.Comments = append(f.Comments, c)
	return c, nil
}

func (f *Fake) ListComments(_ context.Context, _, taskID domain.ID) ([]domain.Comment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ListComments"); err != nil {
		return nil, err
	}
	var out []domain.Comment
	for _, c := range f.Comments {
		if c.TaskID == taskID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *Fake) WakeTask(_ context.Context, _ domain.Actor, _, taskID domain.ID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("WakeTask")
	if err := f.fail("WakeTask"); err != nil {
		return err
	}
	if i, ok := f.findTask(taskID.String()); ok && f.Tasks[i].PersonaID == nil {
		return domain.Invalid("persona", "a task não tem dono")
	}
	return nil
}

func (f *Fake) ListRuns(_ context.Context, _ domain.ID, fl service.RunFilter) ([]service.RunView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ListRuns"); err != nil {
		return nil, err
	}
	var out []service.RunView
	for _, r := range f.Runs {
		if fl.TaskID != nil && r.TaskID != *fl.TaskID {
			continue
		}
		if fl.PersonaID != nil && r.PersonaID != *fl.PersonaID {
			continue
		}
		if len(fl.Statuses) > 0 {
			ok := false
			for _, s := range fl.Statuses {
				ok = ok || s == r.Status
			}
			if !ok {
				continue
			}
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if fl.Limit > 0 && len(out) > fl.Limit {
		out = out[:fl.Limit]
	}
	return out, nil
}

func (f *Fake) GetRun(_ context.Context, _, runID domain.ID) (service.RunView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("GetRun"); err != nil {
		return service.RunView{}, err
	}
	for _, r := range f.Runs {
		if r.ID == runID {
			return r, nil
		}
	}
	return service.RunView{}, domain.ErrNotFound
}

func (f *Fake) ListRunEvents(_ context.Context, _, runID domain.ID, after int64, limit int) ([]domain.RunEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ListRunEvents"); err != nil {
		return nil, err
	}
	var out []domain.RunEvent
	for _, e := range f.Events[runID] {
		if e.Seq > after {
			out = append(out, e)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ListRunEventsBefore devolve os últimos eventos antes de beforeSeq (0 = do fim), em ordem crescente.
func (f *Fake) ListRunEventsBefore(_ context.Context, _, runID domain.ID, before int64, limit int) ([]domain.RunEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ListRunEventsBefore"); err != nil {
		return nil, err
	}
	var out []domain.RunEvent
	for _, e := range f.Events[runID] {
		if before <= 0 || e.Seq < before {
			out = append(out, e)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func (f *Fake) CancelRun(_ context.Context, _ domain.Actor, _, runID domain.ID) (service.RunView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("CancelRun")
	if err := f.fail("CancelRun"); err != nil {
		return service.RunView{}, err
	}
	for i := range f.Runs {
		if f.Runs[i].ID == runID {
			now := time.Now().UTC()
			f.Runs[i].Status = domain.RunCancelled
			f.Runs[i].FinishedAt = &now
			return f.Runs[i], nil
		}
	}
	return service.RunView{}, domain.ErrNotFound
}

func (f *Fake) Search(_ context.Context, _ domain.ID, q string, fl service.SearchFilter) (service.SearchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("Search"); err != nil {
		return service.SearchResult{}, err
	}
	f.SearchQ, f.SearchF = q, fl
	return f.SearchRes, nil
}

func (f *Fake) MemorySearchAvailable(context.Context) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Memory
}

func (f *Fake) ListActivity(_ context.Context, _ domain.ID, fl service.ActivityFilter) ([]domain.ActivityEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ListActivity"); err != nil {
		return nil, err
	}
	out := append([]domain.ActivityEvent(nil), f.Activity...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].OccurredAt.After(out[j].OccurredAt) })
	if fl.Limit > 0 && len(out) > fl.Limit {
		out = out[:fl.Limit]
	}
	return out, nil
}

func (f *Fake) Dashboard(context.Context, domain.ID) (service.DashboardStats, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("Dashboard"); err != nil {
		return service.DashboardStats{}, err
	}
	var st service.DashboardStats
	for _, p := range f.Personas {
		if p.Status.CanWork() {
			st.ActivePersonas++
		}
	}
	for _, t := range f.Tasks {
		if t.Status == domain.TaskInProgress {
			st.TasksInProgress++
		}
		if t.Status.Open() {
			st.OpenTasks++
		}
	}
	for _, g := range f.Gates {
		if g.Status == domain.GatePending {
			st.PendingGates++
		}
	}
	for _, r := range f.Runs {
		if r.Status == domain.RunRunning {
			st.RunningRuns++
		}
		if r.CostUSD != nil {
			st.MonthCostUSD += *r.CostUSD
		}
	}
	if len(f.Ledger) > 0 {
		st.MonthCostUSD = 0
		for _, e := range f.Ledger {
			st.MonthCostUSD += e.CostUSD
		}
	}
	if len(f.Companies) > 0 {
		b := service.BudgetFor(f.Companies[0].BudgetMonthlyCents, st.MonthCostUSD)
		st.MonthBudgetCents, st.BudgetPercent, st.BudgetState = b.BudgetCents, b.Percent, b.State
	}
	return st, nil
}

// ID builds a deterministic UUID from a label (stable across runs).
func ID(label string) domain.ID { return uuid.NewSHA1(uuid.NameSpaceOID, []byte("hanko:"+label)) }

// Pretty is a debugging helper.
func (f *Fake) Pretty() string {
	b, _ := json.MarshalIndent(f.Tasks, "", " ")
	return fmt.Sprint(len(b))
}

func mustUUID(s string) [16]byte {
	u, err := uuidParse(s)
	if err != nil {
		panic(err)
	}
	return u
}

func uuidParse(s string) ([16]byte, error) {
	u, err := uuid.Parse(s)
	return [16]byte(u), err
}

// applyReviewer mirrors the service rules for the reviewer of a persona: another persona of the
// company or an alternate harness, never both, never itself, never the author's own harness+model.
func (f *Fake) applyReviewer(p *domain.Persona, in service.UpdatePersonaInput) error {
	switch {
	case in.ClearReviewer:
		if in.ReviewerPersona != nil || in.ReviewerAgent != nil || in.ReviewerModel != nil {
			return domain.Invalid("reviewer", "cannot set and clear at once")
		}
		p.ReviewerPersonaID, p.ReviewerAgent, p.ReviewerModel = nil, "", ""
	case in.ReviewerPersona != nil:
		if *in.ReviewerPersona == p.ID {
			return domain.Invalid("reviewer", "a persona cannot review itself; use an alternate harness instead")
		}
		if f.personaName(in.ReviewerPersona) == "" {
			return domain.Invalid("reviewer", "persona not found in company")
		}
		id := *in.ReviewerPersona
		p.ReviewerPersonaID, p.ReviewerAgent, p.ReviewerModel = &id, "", ""
	case in.ReviewerAgent != nil || in.ReviewerModel != nil:
		agent, model := "", ""
		if in.ReviewerAgent != nil {
			agent = *in.ReviewerAgent
		}
		if in.ReviewerModel != nil {
			model = *in.ReviewerModel
		}
		switch {
		case agent == "" && model != "":
			return domain.Invalid("reviewer_agent", "required when reviewer_model is set")
		case agent != "" && !domain.ValidAgent(agent):
			return domain.Invalid("reviewer_agent", "unknown agent "+agent)
		case agent != "" && agent == p.Agent && model == p.Model:
			return domain.Invalid("reviewer_agent", "the alternate harness is the same as the author's (same agent and model); change the agent or the model")
		}
		p.ReviewerPersonaID, p.ReviewerAgent, p.ReviewerModel = nil, agent, model
	}
	return nil
}
