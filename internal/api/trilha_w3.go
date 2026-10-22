package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko"
	"github.com/renatobardi/hanko/internal/changelog"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Trilha contínua, onda 3 (ADR-0061): onboarding, perfil próprio, favoritos e recentes, "O que há de novo".

// w3Backend é o subconjunto do service desta fatia; opcional pelo mesmo motivo de readinessBackend.
type w3Backend interface {
	NavItems(ctx context.Context, companyID domain.ID) (favorites, recents []domain.NavItem, err error)
	SetFavorite(ctx context.Context, actor domain.Actor, companyID domain.ID, path, title string, on bool) error
	GetProfile(ctx context.Context) (domain.UserProfile, error)
	UpdateProfile(ctx context.Context, in domain.ProfileInput) (domain.UserProfile, error)
	WhatsNewSeen(ctx context.Context) (string, error)
	DismissWhatsNew(ctx context.Context, version string) error
	OnboardingStatus(ctx context.Context, companyID domain.ID) (domain.OnboardingStatus, error)
	OnboardingLastProbe(ctx context.Context, companyID domain.ID) (*service.OnboardingProbeResult, error)
	OnboardingProbe(ctx context.Context, actor domain.Actor, companyID domain.ID, agent, model string) (service.OnboardingProbeResult, error)
	OnboardingSkip(ctx context.Context, actor domain.Actor, companyID domain.ID, step domain.OnboardingStepKey) (domain.OnboardingStatus, error)
	OnboardingDismiss(ctx context.Context, actor domain.Actor, companyID domain.ID, dismissed bool) (domain.OnboardingStatus, error)
	OnboardingInstallTeam(ctx context.Context, actor domain.Actor, companyID domain.ID, slug string) (service.OnboardingTeamResult, error)
	OnboardingStartTask(ctx context.Context, actor domain.Actor, companyID, personaID domain.ID, title, description string) (service.TaskView, error)
}

var _ w3Backend = (*service.Service)(nil)

func (s *server) w3() (w3Backend, error) {
	b, ok := s.svc.(w3Backend)
	if !ok {
		return nil, problem(http.StatusNotImplemented, "recurso indisponível neste servidor")
	}
	return b, nil
}

// ---------- onboarding ----------

// OnboardingStep é um passo do assistente de primeiros passos.
type OnboardingStep struct {
	Key     string `json:"key" enum:"company,harness,persona,task"`
	Done    bool   `json:"done"`
	Skipped bool   `json:"skipped,omitempty"`
}

// OnboardingState é o estado do assistente de uma empresa. Os passos são derivados do que a empresa já tem.
type OnboardingState struct {
	Steps         []OnboardingStep `json:"steps"`
	Current       string           `json:"current,omitempty" enum:"company,harness,persona,task" doc:"Primeiro passo aberto; vazio quando o assistente terminou."`
	Complete      bool             `json:"complete"`
	Dismissed     bool             `json:"dismissed"`
	HarnessAgent  string           `json:"harness_agent,omitempty"`
	HarnessStatus string           `json:"harness_status,omitempty" enum:"ready,not_ready,"`
	Probe         *OnboardingProbe `json:"probe,omitempty" doc:"Último teste do harness."`
}

// OnboardingProbe é o resultado do teste de um harness feito pelo assistente.
type OnboardingProbe struct {
	Agent      string          `json:"agent"`
	Model      string          `json:"model,omitempty"`
	Status     string          `json:"status" enum:"ready,not_ready"`
	Reason     string          `json:"reason,omitempty"`
	Steps      []ReadinessStep `json:"steps"`
	DurationMs int             `json:"duration_ms,omitempty"`
}

func onboardingProbeOut(p service.OnboardingProbeResult) OnboardingProbe {
	o := OnboardingProbe{Agent: p.Agent, Model: p.Model, Status: string(p.Status), Reason: p.Reason, DurationMs: p.DurationMs, Steps: make([]ReadinessStep, len(p.Steps))}
	for i, st := range p.Steps {
		o.Steps[i] = ReadinessStep{Name: st.Name, Status: string(st.Status), Detail: st.Detail}
	}
	return o
}

func onboardingOut(st domain.OnboardingStatus, probe *service.OnboardingProbeResult) OnboardingState {
	o := OnboardingState{Steps: make([]OnboardingStep, len(st.Steps)), Current: string(st.Current), Complete: st.Complete, Dismissed: st.Dismissed,
		HarnessAgent: st.HarnessAgent, HarnessStatus: st.HarnessStatus}
	for i, x := range st.Steps {
		o.Steps[i] = OnboardingStep{Key: string(x.Key), Done: x.Done, Skipped: x.Skipped}
	}
	if probe != nil {
		p := onboardingProbeOut(*probe)
		o.Probe = &p
	}
	return o
}

type onboardingProbeIn struct {
	CompanyPath
	Body struct {
		Agent string `json:"agent" enum:"claude,codex,fake" doc:"Harness a testar."`
		Model string `json:"model,omitempty" maxLength:"128" doc:"Modelo a validar; vazio = padrão do harness."`
	}
}

type onboardingSkipIn struct {
	CompanyPath
	Body struct {
		Step string `json:"step" enum:"harness" doc:"Passo a pular (só o do harness admite)."`
	}
}

type onboardingDismissIn struct {
	CompanyPath
	Body struct {
		Dismissed bool `json:"dismissed" doc:"true dispensa o assistente; false volta a oferecê-lo."`
	}
}

type onboardingTeamIn struct {
	CompanyPath
	Body struct {
		Slug string `json:"slug" example:"squad-de-produto" doc:"Pacote do catálogo embutido."`
	}
}

// OnboardingTeamInstalled é o resultado da instalação de um pacote pelo assistente.
type OnboardingTeamInstalled struct {
	Package  string `json:"package"`
	Personas int    `json:"personas" doc:"Personas criadas pelo pacote."`
	Lead     string `json:"lead,omitempty" doc:"Slug da persona de topo, já ativa."`
	LeadID   string `json:"lead_id,omitempty" format:"uuid"`
}

type onboardingTaskIn struct {
	CompanyPath
	Body struct {
		Persona     string `json:"persona" doc:"Id da persona que recebe a primeira task."`
		Title       string `json:"title" minLength:"1" maxLength:"500"`
		Description string `json:"description,omitempty" maxLength:"20000"`
	}
}

func (s *server) registerOnboarding(api huma.API) {
	huma.Register(api, op("get-onboarding", http.MethodGet, "/companies/{company}/onboarding", "onboarding",
		"Estado do assistente de primeiros passos (empresa, harness, persona, primeira task)"),
		func(ctx context.Context, in *CompanyPath) (*ItemOut[OnboardingState], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, err := s.w3()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			st, err := b.OnboardingStatus(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			probe, err := b.OnboardingLastProbe(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[OnboardingState]{Body: onboardingOut(st, probe)}, nil
		})

	huma.Register(api, op("onboarding-probe", http.MethodPost, "/companies/{company}/onboarding/probe", "onboarding",
		"Testa um harness (binário, login, modelo) antes de existir persona (admin)"),
		func(ctx context.Context, in *onboardingProbeIn) (*ItemOut[OnboardingProbe], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, err := s.w3()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			r, err := b.OnboardingProbe(ctx, sc.Actor, sc.Company.ID, in.Body.Agent, in.Body.Model)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[OnboardingProbe]{Body: onboardingProbeOut(r)}, nil
		})

	huma.Register(api, op("onboarding-skip", http.MethodPost, "/companies/{company}/onboarding/skip", "onboarding",
		"Pula o passo do harness (admin)"),
		func(ctx context.Context, in *onboardingSkipIn) (*ItemOut[OnboardingState], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, err := s.w3()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			st, err := b.OnboardingSkip(ctx, sc.Actor, sc.Company.ID, domain.OnboardingStepKey(in.Body.Step))
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[OnboardingState]{Body: onboardingOut(st, nil)}, nil
		})

	huma.Register(api, op("onboarding-dismiss", http.MethodPost, "/companies/{company}/onboarding/dismiss", "onboarding",
		"Dispensa (ou volta a oferecer) o assistente (admin)"),
		func(ctx context.Context, in *onboardingDismissIn) (*ItemOut[OnboardingState], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, err := s.w3()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			st, err := b.OnboardingDismiss(ctx, sc.Actor, sc.Company.ID, in.Body.Dismissed)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[OnboardingState]{Body: onboardingOut(st, nil)}, nil
		})

	huma.Register(api, op("onboarding-install-team", http.MethodPost, "/companies/{company}/onboarding/team", "onboarding",
		"Instala um pacote de time do catálogo e ativa a persona de topo (admin)"),
		func(ctx context.Context, in *onboardingTeamIn) (*ItemOut[OnboardingTeamInstalled], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, err := s.w3()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			r, err := b.OnboardingInstallTeam(ctx, sc.Actor, sc.Company.ID, in.Body.Slug)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			out := OnboardingTeamInstalled{Package: r.Install.Package.Slug, Personas: r.Install.Result.Counts["personas"]}
			if r.Lead != nil {
				out.Lead, out.LeadID = r.Lead.Slug, idStr(r.Lead.ID)
			}
			return &ItemOut[OnboardingTeamInstalled]{Body: out}, nil
		})

	huma.Register(api, created(op("onboarding-first-task", http.MethodPost, "/companies/{company}/onboarding/task", "onboarding",
		"Cria a primeira task para uma persona e fecha o passo (membro)")),
		func(ctx context.Context, in *onboardingTaskIn) (*ItemOut[Task], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, err := s.w3()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			pid, err := domain.ParseID(strings.TrimSpace(in.Body.Persona))
			if err != nil {
				return nil, problem(http.StatusUnprocessableEntity, "persona: id inválido")
			}
			tv, err := b.OnboardingStartTask(ctx, sc.Actor, sc.Company.ID, pid, in.Body.Title, in.Body.Description)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Task]{Body: taskOut(tv)}, nil
		})
}

// ---------- perfil ----------

// UserProfile é o perfil da própria pessoa.
type UserProfile struct {
	ID          string `json:"id" format:"uuid"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	AvatarGlyph string `json:"avatar_glyph,omitempty" doc:"Até 3 caracteres no avatar; vazio = a inicial do nome."`
	AvatarTone  string `json:"avatar_tone,omitempty" enum:"accent,green,amber,red,blue,violet,gray," doc:"Cor do avatar; vazio = a padrão."`
	Timezone    string `json:"timezone,omitempty" example:"America/Sao_Paulo" doc:"Fuso IANA para datas e horas; vazio = o da empresa."`
	Locale      string `json:"locale,omitempty" enum:"pt-BR,en," doc:"Idioma da UI; vazio = o do navegador."`
}

func userProfileOut(p domain.UserProfile) UserProfile {
	return UserProfile{ID: idStr(p.UserID), Email: p.Email, Name: p.Name, AvatarGlyph: p.AvatarGlyph, AvatarTone: p.AvatarTone, Timezone: p.Timezone, Locale: p.Locale}
}

type updateProfileIn struct {
	Body struct {
		Name        *string `json:"name,omitempty" maxLength:"80"`
		AvatarGlyph *string `json:"avatar_glyph,omitempty" maxLength:"12"`
		AvatarTone  *string `json:"avatar_tone,omitempty" enum:"accent,green,amber,red,blue,violet,gray,"`
		Timezone    *string `json:"timezone,omitempty" maxLength:"64"`
		Locale      *string `json:"locale,omitempty" enum:"pt-BR,en,"`
	}
}

// ---------- favoritos e recentes ----------

// NavItem é um favorito ou recente da sidebar.
type NavItem struct {
	Path      string    `json:"path" example:"/tasks/HNK-12" doc:"Relativo à empresa."`
	Title     string    `json:"title"`
	TouchedAt time.Time `json:"touched_at"`
}

// NavState são os favoritos e recentes da pessoa numa empresa.
type NavState struct {
	Favorites []NavItem `json:"favorites"`
	Recents   []NavItem `json:"recents"`
}

func navItemsOut(in []domain.NavItem) []NavItem {
	out := make([]NavItem, len(in))
	for i, it := range in {
		out[i] = NavItem{Path: it.Path, Title: it.Title, TouchedAt: it.TouchedAt}
	}
	return out
}

type setFavoriteIn struct {
	CompanyPath
	Body struct {
		Path     string `json:"path" example:"/tasks/HNK-12" maxLength:"300" doc:"Página, relativa à empresa."`
		Title    string `json:"title,omitempty" maxLength:"200"`
		Favorite bool   `json:"favorite" doc:"true marca; false desmarca."`
	}
}

// ---------- o que há de novo ----------

// WhatsNew são as novidades da versão instalada, lidas do CHANGELOG embutido (sem rede).
type WhatsNew struct {
	Installed string `json:"installed" doc:"Versão do binário."`
	Version   string `json:"version" doc:"Bloco do CHANGELOG usado (a versão instalada ou Unreleased)."`
	Date      string `json:"date,omitempty"`
	Body      string `json:"body" doc:"Markdown do bloco."`
	Key       string `json:"key" doc:"O que a dispensa grava: muda quando a versão instalada muda."`
	Dismissed bool   `json:"dismissed" doc:"A pessoa já dispensou as novidades desta versão."`
}

type dismissWhatsNewIn struct {
	Body struct {
		Key string `json:"key" maxLength:"100" doc:"O campo key de GET /whats-new."`
	}
}

func (s *server) registerW3Me(api huma.API) {
	huma.Register(api, op("get-profile", http.MethodGet, "/me/profile", "me", "Perfil da própria pessoa: nome, avatar, fuso e idioma"),
		func(ctx context.Context, _ *struct{}) (*ItemOut[UserProfile], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, err := s.w3()
			if err != nil {
				return nil, err
			}
			p, err := b.GetProfile(ctx)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[UserProfile]{Body: userProfileOut(p)}, nil
		})

	huma.Register(api, op("update-profile", http.MethodPatch, "/me/profile", "me", "Altera nome, avatar, fuso e idioma da própria pessoa (só o que for enviado)"),
		func(ctx context.Context, in *updateProfileIn) (*ItemOut[UserProfile], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, err := s.w3()
			if err != nil {
				return nil, err
			}
			p, err := b.UpdateProfile(ctx, domain.ProfileInput{Name: in.Body.Name, AvatarGlyph: in.Body.AvatarGlyph, AvatarTone: in.Body.AvatarTone,
				Timezone: in.Body.Timezone, Locale: in.Body.Locale})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[UserProfile]{Body: userProfileOut(p)}, nil
		})

	huma.Register(api, op("get-whats-new", http.MethodGet, "/whats-new", "me", "Novidades da versão instalada, lidas do CHANGELOG embutido (sem rede)"),
		func(ctx context.Context, _ *struct{}) (*ItemOut[WhatsNew], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, err := s.w3()
			if err != nil {
				return nil, err
			}
			rel, ok := changelog.For(changelog.Parse(hanko.Changelog), s.d.Version)
			key := changelog.Key(s.d.Version)
			out := WhatsNew{Installed: s.d.Version, Version: rel.Version, Date: rel.Date, Body: rel.Body, Key: key}
			if !ok {
				out.Dismissed = true // nada a mostrar
				return &ItemOut[WhatsNew]{Body: out}, nil
			}
			seen, err := b.WhatsNewSeen(ctx)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			out.Dismissed = seen == key
			return &ItemOut[WhatsNew]{Body: out}, nil
		})

	huma.Register(api, op("dismiss-whats-new", http.MethodPost, "/whats-new/dismiss", "me", "Dispensa as novidades desta versão para a própria pessoa"),
		func(ctx context.Context, in *dismissWhatsNewIn) (*struct{}, error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, err := s.w3()
			if err != nil {
				return nil, err
			}
			if err := b.DismissWhatsNew(ctx, in.Body.Key); err != nil {
				return nil, s.fail(ctx, err)
			}
			return nil, nil
		})

	huma.Register(api, op("get-nav", http.MethodGet, "/companies/{company}/nav", "me", "Favoritos e recentes da sidebar da própria pessoa"),
		func(ctx context.Context, in *CompanyPath) (*ItemOut[NavState], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, err := s.w3()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			favs, recs, err := b.NavItems(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[NavState]{Body: NavState{Favorites: navItemsOut(favs), Recents: navItemsOut(recs)}}, nil
		})

	huma.Register(api, op("set-nav-favorite", http.MethodPut, "/companies/{company}/nav/favorites", "me", "Marca ou desmarca uma página como favorita"),
		func(ctx context.Context, in *setFavoriteIn) (*ItemOut[NavState], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, err := s.w3()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			if err := b.SetFavorite(ctx, sc.Actor, sc.Company.ID, in.Body.Path, in.Body.Title, in.Body.Favorite); err != nil {
				return nil, s.fail(ctx, err)
			}
			favs, recs, err := b.NavItems(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[NavState]{Body: NavState{Favorites: navItemsOut(favs), Recents: navItemsOut(recs)}}, nil
		})
}
