package uitest

import (
	"context"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
)

// ReadinessFake guarda a prontidão das personas e o catálogo de modelos do Fake (ADR-0055).
type ReadinessFake struct {
	// ByPersona é o último teste de cada persona; ausente = nunca testada.
	ByPersona map[domain.ID]domain.Readiness
	// Next é o resultado que TestPersonaReadiness grava (padrão: pronta, com os quatro passos ok).
	Next *domain.Readiness
	// Catalog é o catálogo de modelos devolvido (padrão: um claude com preço e um sem preço).
	Catalog []domain.CatalogModel
	// LastProjectEnv guarda o último ambiente escolhido para um projeto.
	LastProjectEnv *domain.ID
}

func (f *Fake) GetPersonaReadiness(_ context.Context, _, personaID domain.ID) (*domain.Readiness, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("GetPersonaReadiness")
	if err := f.fail("GetPersonaReadiness"); err != nil {
		return nil, err
	}
	if r, ok := f.Ready.ByPersona[personaID]; ok {
		return &r, nil
	}
	return nil, nil
}

func (f *Fake) ListPersonaReadiness(_ context.Context, _ domain.ID) (map[domain.ID]domain.Readiness, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ListPersonaReadiness"); err != nil {
		return nil, err
	}
	out := map[domain.ID]domain.Readiness{}
	for k, v := range f.Ready.ByPersona {
		out[k] = v
	}
	return out, nil
}

func (f *Fake) TestPersonaReadiness(_ context.Context, _ domain.Actor, companyID, personaID domain.ID) (domain.Readiness, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("TestPersonaReadiness")
	if err := f.fail("TestPersonaReadiness"); err != nil {
		return domain.Readiness{}, err
	}
	var p *domain.Persona
	for i := range f.Personas {
		if f.Personas[i].ID == personaID {
			p = &f.Personas[i]
		}
	}
	if p == nil {
		return domain.Readiness{}, domain.ErrNotFound
	}
	r := domain.Readiness{PersonaID: personaID, CompanyID: companyID, Agent: p.Agent, Model: p.Model, EnvironmentSource: domain.EnvFromLocal,
		Trigger: domain.ReadinessManual, DurationMs: 12, CheckedAt: time.Now().UTC(),
		Steps: []domain.ReadinessStep{{Name: domain.StepEnvironment, Status: domain.StepOK, Detail: "local"},
			{Name: domain.StepBinary, Status: domain.StepOK, Detail: "/usr/bin/" + p.Agent}, {Name: domain.StepAuth, Status: domain.StepOK, Detail: "login do harness"},
			{Name: domain.StepModel, Status: domain.StepOK, Detail: p.Model}}}
	if f.Ready.Next != nil {
		r = *f.Ready.Next
		r.PersonaID, r.CompanyID, r.Agent, r.Model = personaID, companyID, p.Agent, p.Model
		if r.CheckedAt.IsZero() {
			r.CheckedAt = time.Now().UTC()
		}
	}
	r.Status, r.Reason = domain.SummarizeReadiness(r.Steps)
	if f.Ready.ByPersona == nil {
		f.Ready.ByPersona = map[domain.ID]domain.Readiness{}
	}
	f.Ready.ByPersona[personaID] = r
	return r, nil
}

func (f *Fake) ModelCatalog(_ context.Context, _ domain.ID, agent string) ([]domain.CatalogModel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ModelCatalog"); err != nil {
		return nil, err
	}
	if f.Ready.Catalog != nil {
		return append([]domain.CatalogModel(nil), f.Ready.Catalog...), nil
	}
	all := []domain.CatalogModel{
		{Agent: "claude", Model: "sonnet", Origins: []domain.ModelOrigin{domain.ModelBuiltin}, Priced: true, PriceModel: "claude-sonnet"},
		{Agent: "claude", Model: "modelo-sem-preco", Origins: []domain.ModelOrigin{domain.ModelProbed}},
		{Agent: "codex", Model: "gpt-5", Origins: []domain.ModelOrigin{domain.ModelPriced}, Priced: true, PriceModel: "gpt-5"},
	}
	var out []domain.CatalogModel
	for _, m := range all {
		if agent == "" || m.Agent == agent || m.Agent == "" {
			out = append(out, m)
		}
	}
	return out, nil
}

func (f *Fake) SetProjectEnvironment(_ context.Context, _ domain.Actor, _, projectID domain.ID, envID *domain.ID) (domain.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("SetProjectEnvironment")
	if err := f.fail("SetProjectEnvironment"); err != nil {
		return domain.Project{}, err
	}
	f.Ready.LastProjectEnv = envID
	for i := range f.Projects {
		if f.Projects[i].ID == projectID {
			f.Projects[i].EnvironmentID = envID
			return f.Projects[i], nil
		}
	}
	return domain.Project{}, domain.ErrNotFound
}
