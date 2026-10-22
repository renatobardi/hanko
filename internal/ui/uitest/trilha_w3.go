package uitest

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/teams"
)

// W3Fake é o estado das telas da trilha onda 3 (ADR-0060): favoritos e recentes, perfil, novidades e onboarding.
type W3Fake struct {
	Favorites []domain.NavItem
	Recents   []domain.NavItem
	// Account liga o perfil (sem conta, como no modo trusted, GetProfile falha).
	Account bool
	Profile domain.UserProfile
	// WhatsSeen é a chave de novidades já dispensada.
	WhatsSeen string
	// Onboarding
	ProbeStatus domain.ReadinessStatus // o que o próximo teste devolve (padrão ready)
	Probe       *service.OnboardingProbeResult
	Skipped     []string
	Dismissed   bool
	FirstTask   bool
}

func (f *Fake) NavItems(context.Context, domain.ID) (favs, recs []domain.NavItem, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("NavItems"); err != nil {
		return nil, nil, err
	}
	return append([]domain.NavItem{}, f.W3.Favorites...), append([]domain.NavItem{}, f.W3.Recents...), nil
}

func (f *Fake) SetFavorite(_ context.Context, _ domain.Actor, _ domain.ID, path, title string, on bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("SetFavorite")
	if err := f.fail("SetFavorite"); err != nil {
		return err
	}
	p, err := domain.NormalizeNavPath(path)
	if err != nil {
		return err
	}
	f.W3.Favorites = slices.DeleteFunc(f.W3.Favorites, func(i domain.NavItem) bool { return i.Path == p })
	if on {
		f.W3.Favorites = append(f.W3.Favorites, domain.NavItem{Kind: domain.NavFavorite, Path: p, Title: domain.CleanNavTitle(title)})
	}
	return nil
}

func (f *Fake) TouchRecent(_ context.Context, _ domain.ID, path, title string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("TouchRecent"); err != nil {
		return err
	}
	p, err := domain.NormalizeNavPath(path)
	if err != nil || !domain.IsNavDetail(p) {
		return nil
	}
	f.W3.Recents = slices.DeleteFunc(f.W3.Recents, func(i domain.NavItem) bool { return i.Path == p })
	f.W3.Recents = append([]domain.NavItem{{Kind: domain.NavRecent, Path: p, Title: domain.CleanNavTitle(title)}}, f.W3.Recents...)
	if len(f.W3.Recents) > domain.MaxRecents {
		f.W3.Recents = f.W3.Recents[:domain.MaxRecents]
	}
	return nil
}

func (f *Fake) GetProfile(context.Context) (domain.UserProfile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("GetProfile"); err != nil {
		return domain.UserProfile{}, err
	}
	if !f.W3.Account {
		return domain.UserProfile{}, domain.Invalid("user", "sem conta")
	}
	return f.W3.Profile, nil
}

func (f *Fake) UpdateProfile(_ context.Context, in domain.ProfileInput) (domain.UserProfile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("UpdateProfile")
	if err := f.fail("UpdateProfile"); err != nil {
		return domain.UserProfile{}, err
	}
	if !f.W3.Account {
		return domain.UserProfile{}, domain.Invalid("user", "sem conta")
	}
	p, err := f.W3.Profile.Apply(in)
	if err != nil {
		return domain.UserProfile{}, err
	}
	f.W3.Profile = p
	return p, nil
}

func (f *Fake) ProfileOf(context.Context, domain.ID) domain.UserProfile {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.W3.Profile
}

func (f *Fake) WhatsNewSeen(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.W3.WhatsSeen, f.fail("WhatsNewSeen")
}

func (f *Fake) DismissWhatsNew(_ context.Context, version string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("DismissWhatsNew")
	if err := f.fail("DismissWhatsNew"); err != nil {
		return err
	}
	f.W3.WhatsSeen = version
	return nil
}

func (f *Fake) onboardingFacts() domain.OnboardingFacts {
	facts := domain.OnboardingFacts{Personas: len(f.Personas), HasRun: len(f.Runs) > 0, FirstTask: f.W3.FirstTask, Skipped: f.W3.Skipped, Dismissed: f.W3.Dismissed}
	if f.W3.Probe != nil {
		facts.HarnessAgent, facts.HarnessStatus = f.W3.Probe.Agent, string(f.W3.Probe.Status)
	}
	return facts
}

func (f *Fake) OnboardingStatus(context.Context, domain.ID) (domain.OnboardingStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("OnboardingStatus"); err != nil {
		return domain.OnboardingStatus{}, err
	}
	return domain.ComputeOnboarding(f.onboardingFacts()), nil
}

func (f *Fake) OnboardingLastProbe(context.Context, domain.ID) (*service.OnboardingProbeResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.W3.Probe, nil
}

func (f *Fake) OnboardingProbe(_ context.Context, _ domain.Actor, _ domain.ID, agent, model string) (service.OnboardingProbeResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("OnboardingProbe")
	if err := f.fail("OnboardingProbe"); err != nil {
		return service.OnboardingProbeResult{}, err
	}
	if !slices.Contains([]string{"claude", "codex", "fake"}, agent) {
		return service.OnboardingProbeResult{}, domain.Invalid("agent", "harness desconhecido")
	}
	st := f.W3.ProbeStatus
	if st == "" {
		st = domain.ReadinessReady
	}
	r := service.OnboardingProbeResult{Agent: agent, Model: model, Status: st, DurationMs: 12, Steps: []domain.ReadinessStep{
		{Name: domain.StepEnvironment, Status: domain.StepOK, Detail: "local"}, {Name: domain.StepBinary, Status: domain.StepOK, Detail: "/usr/bin/" + agent},
		{Name: domain.StepAuth, Status: domain.StepOK, Detail: "login do harness"}, {Name: domain.StepModel, Status: domain.StepOK, Detail: "padrão do harness"}}}
	if st != domain.ReadinessReady {
		r.Reason = "auth: sem login"
		r.Steps[2] = domain.ReadinessStep{Name: domain.StepAuth, Status: domain.StepFail, Detail: "sem login"}
	}
	f.W3.Probe = &r
	return r, nil
}

func (f *Fake) OnboardingSkip(_ context.Context, _ domain.Actor, _ domain.ID, step domain.OnboardingStepKey) (domain.OnboardingStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("OnboardingSkip")
	if !step.Valid() || !step.Skippable() {
		return domain.OnboardingStatus{}, domain.Invalid("step", "só o passo do harness pode ser pulado")
	}
	f.W3.Skipped = append(f.W3.Skipped, string(step))
	return domain.ComputeOnboarding(f.onboardingFacts()), nil
}

func (f *Fake) OnboardingDismiss(_ context.Context, _ domain.Actor, _ domain.ID, dismissed bool) (domain.OnboardingStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note(map[bool]string{true: "OnboardingDismiss", false: "OnboardingResume"}[dismissed])
	f.W3.Dismissed = dismissed
	return domain.ComputeOnboarding(f.onboardingFacts()), nil
}

func (f *Fake) OnboardingInstallTeam(ctx context.Context, actor domain.Actor, companyID domain.ID, slug string) (service.OnboardingTeamResult, error) {
	f.mu.Lock()
	f.note("OnboardingInstallTeam")
	err := f.fail("OnboardingInstallTeam")
	f.mu.Unlock()
	if err != nil {
		return service.OnboardingTeamResult{}, err
	}
	pkg, ok, terr := teams.Get(strings.TrimSpace(slug))
	if terr != nil || !ok {
		return service.OnboardingTeamResult{}, fmt.Errorf("pacote %q: %w", slug, domain.ErrNotFound)
	}
	var lead *domain.Persona
	for i, pp := range pkg.Personas {
		st := domain.PersonaPaused
		if (pp.ReportsTo == "" && lead == nil) || (i == 0 && lead == nil) {
			st = domain.PersonaActive
		}
		p, err := f.HirePersona(ctx, actor, companyID, service.HirePersonaInput{Name: pp.Name, Slug: pp.Slug, Agent: "fake"})
		if err != nil {
			return service.OnboardingTeamResult{}, err
		}
		f.mu.Lock()
		for j := range f.Personas {
			if f.Personas[j].ID == p.ID {
				f.Personas[j].Status = st
				p = f.Personas[j]
			}
		}
		f.mu.Unlock()
		if st == domain.PersonaActive {
			cp := p
			lead = &cp
		}
	}
	return service.OnboardingTeamResult{Install: service.TeamInstall{Package: pkg.Package}, Lead: lead}, nil
}

func (f *Fake) OnboardingStartTask(ctx context.Context, actor domain.Actor, companyID, personaID domain.ID, title, description string) (service.TaskView, error) {
	tv, err := f.CreateTask(ctx, actor, companyID, service.CreateTaskInput{Title: title, Description: description, PersonaID: &personaID})
	if err != nil {
		return service.TaskView{}, err
	}
	f.mu.Lock()
	f.W3.FirstTask = true
	f.mu.Unlock()
	return tv, nil
}
