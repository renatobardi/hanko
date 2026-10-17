package uitest

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// EnvFake guarda os ambientes de execução do Fake (ADR-0037). As validações de verdade ficam no service; aqui
// só o essencial para as telas (nome obrigatório, slug único, um padrão).
type EnvFake struct {
	List    []service.EnvironmentView
	Leases  map[domain.ID][]service.EnvironmentLeaseView
	RunEnv  map[domain.ID]service.RunEnvironmentInfo
	Drivers map[domain.EnvironmentDriver]string
	// Probe é o relatório devolvido pelos testes de conexão.
	Probe domain.ProbeReport
	// LastCreate e LastUpdate guardam a última entrada recebida (os testes conferem o que o formulário mandou).
	LastCreate service.CreateEnvironmentInput
	LastUpdate service.UpdateEnvironmentInput
}

func (f *Fake) envIndex(ref string) int {
	for i, v := range f.Env.List {
		if v.Slug == ref || v.ID.String() == ref {
			return i
		}
	}
	return -1
}

// envPersonas recalcula as personas de cada ambiente.
func (f *Fake) envView(v service.EnvironmentView) service.EnvironmentView {
	v.Personas = nil
	for _, p := range f.Personas {
		if p.EnvironmentID != nil && *p.EnvironmentID == v.ID {
			v.Personas = append(v.Personas, service.PersonaRef{ID: p.ID, Slug: p.Slug, Name: p.Name})
		}
	}
	if v.Config.EnvNames == nil {
		v.Config.EnvNames = []string{}
	}
	return v
}

func (f *Fake) ListEnvironments(_ context.Context, _ domain.ID) ([]service.EnvironmentView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ListEnvironments"); err != nil {
		return nil, err
	}
	out := make([]service.EnvironmentView, len(f.Env.List))
	for i, v := range f.Env.List {
		out[i] = f.envView(v)
	}
	return out, nil
}

func (f *Fake) GetEnvironment(_ context.Context, _ domain.ID, ref string) (service.EnvironmentView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("GetEnvironment"); err != nil {
		return service.EnvironmentView{}, err
	}
	if i := f.envIndex(ref); i >= 0 {
		return f.envView(f.Env.List[i]), nil
	}
	return service.EnvironmentView{}, domain.ErrNotFound
}

func (f *Fake) CreateEnvironment(_ context.Context, _ domain.Actor, companyID domain.ID, in service.CreateEnvironmentInput) (service.EnvironmentView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("CreateEnvironment")
	f.Env.LastCreate = in
	if err := f.fail("CreateEnvironment"); err != nil {
		return service.EnvironmentView{}, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return service.EnvironmentView{}, domain.Invalid("name", "required")
	}
	slug := in.Slug
	if slug == "" {
		slug = domain.Slugify(name)
	}
	if f.envIndex(slug) >= 0 {
		return service.EnvironmentView{}, domain.ErrConflict
	}
	now := time.Now().UTC()
	v := service.EnvironmentView{Environment: domain.Environment{ID: domain.NewID(), CompanyID: companyID, Slug: slug, Name: name,
		Description: in.Description, Driver: in.Driver, Status: domain.EnvActive, KeySet: in.PrivateKey != "", CreatedAt: now, UpdatedAt: now,
		Config: domain.EnvironmentConfig{SSH: in.SSH, Sandbox: in.Sandbox}}}
	for k := range in.Env {
		v.Config.EnvNames = append(v.Config.EnvNames, k)
	}
	sort.Strings(v.Config.EnvNames)
	f.Env.List = append(f.Env.List, v)
	return f.envView(v), nil
}

func (f *Fake) UpdateEnvironment(_ context.Context, _ domain.Actor, _, id domain.ID, in service.UpdateEnvironmentInput) (service.EnvironmentView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("UpdateEnvironment")
	f.Env.LastUpdate = in
	if err := f.fail("UpdateEnvironment"); err != nil {
		return service.EnvironmentView{}, err
	}
	i := f.envIndex(id.String())
	if i < 0 {
		return service.EnvironmentView{}, domain.ErrNotFound
	}
	v := &f.Env.List[i]
	if in.Name != nil {
		v.Name = *in.Name
	}
	if in.Description != nil {
		v.Description = *in.Description
	}
	if in.SSH != nil {
		v.Config.SSH = in.SSH
	}
	if in.Sandbox != nil {
		v.Config.Sandbox = in.Sandbox
	}
	if in.PrivateKey != nil {
		v.KeySet = true
	}
	if in.Reactivate {
		v.Status = domain.EnvActive
	}
	return f.envView(*v), nil
}

func (f *Fake) ArchiveEnvironment(_ context.Context, _ domain.Actor, _, id domain.ID, moveTo *domain.ID) (service.EnvironmentView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("ArchiveEnvironment")
	if err := f.fail("ArchiveEnvironment"); err != nil {
		return service.EnvironmentView{}, err
	}
	i := f.envIndex(id.String())
	if i < 0 {
		return service.EnvironmentView{}, domain.ErrNotFound
	}
	for j := range f.Personas {
		if p := f.Personas[j].EnvironmentID; p != nil && *p == id {
			f.Personas[j].EnvironmentID = moveTo
		}
	}
	f.Env.List[i].Status, f.Env.List[i].IsDefault = domain.EnvArchived, false
	return f.envView(f.Env.List[i]), nil
}

func (f *Fake) SetDefaultEnvironment(_ context.Context, _ domain.Actor, _ domain.ID, id *domain.ID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("SetDefaultEnvironment")
	if err := f.fail("SetDefaultEnvironment"); err != nil {
		return err
	}
	for i := range f.Env.List {
		f.Env.List[i].IsDefault = id != nil && f.Env.List[i].ID == *id
	}
	return nil
}

func (f *Fake) SetPersonaEnvironment(_ context.Context, _ domain.Actor, _, personaID domain.ID, envID *domain.ID) (domain.Persona, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("SetPersonaEnvironment")
	if err := f.fail("SetPersonaEnvironment"); err != nil {
		return domain.Persona{}, err
	}
	for i := range f.Personas {
		if f.Personas[i].ID == personaID {
			f.Personas[i].EnvironmentID = envID
			return f.Personas[i], nil
		}
	}
	return domain.Persona{}, domain.ErrNotFound
}

func (f *Fake) ProbeEnvironment(_ context.Context, _ domain.Actor, _, id domain.ID) (service.EnvironmentView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("ProbeEnvironment")
	if err := f.fail("ProbeEnvironment"); err != nil {
		return service.EnvironmentView{}, err
	}
	i := f.envIndex(id.String())
	if i < 0 {
		return service.EnvironmentView{}, domain.ErrNotFound
	}
	now := time.Now().UTC()
	rep := f.Env.Probe
	ok := rep.OK
	f.Env.List[i].LastProbeAt, f.Env.List[i].LastProbeOK, f.Env.List[i].LastProbeReport = &now, &ok, &rep
	return f.envView(f.Env.List[i]), nil
}

func (f *Fake) ProbeEnvironmentDraft(_ context.Context, _ domain.ID, in service.CreateEnvironmentInput) (domain.ProbeReport, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("ProbeEnvironmentDraft")
	f.Env.LastCreate = in
	if err := f.fail("ProbeEnvironmentDraft"); err != nil {
		return domain.ProbeReport{}, err
	}
	return f.Env.Probe, nil
}

func (f *Fake) ListEnvironmentLeases(_ context.Context, _, envID domain.ID, _ int) ([]service.EnvironmentLeaseView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ListEnvironmentLeases"); err != nil {
		return nil, err
	}
	return append([]service.EnvironmentLeaseView(nil), f.Env.Leases[envID]...), nil
}

func (f *Fake) RunEnvironmentOf(_ context.Context, _, runID domain.ID) (service.RunEnvironmentInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if info, ok := f.Env.RunEnv[runID]; ok {
		return info, nil
	}
	return service.RunEnvironmentInfo{}, domain.ErrNotFound
}

func (f *Fake) EnvironmentDrivers(context.Context) map[domain.EnvironmentDriver]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[domain.EnvironmentDriver]string{domain.EnvSSH: "", domain.EnvSandbox: ""}
	for k, v := range f.Env.Drivers {
		out[k] = v
	}
	return out
}
