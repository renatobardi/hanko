package client

import (
	"context"
	"net/http"

	"github.com/renatobardi/hanko/internal/api"
)

// Trilha contínua, onda 3 (ADR-0061): onboarding, perfil, favoritos e "O que há de novo".

// Onboarding devolve o estado do assistente de primeiros passos da empresa.
func (c *Client) Onboarding(ctx context.Context, company string) (api.OnboardingState, error) {
	return get[api.OnboardingState](ctx, c, http.MethodGet, cp(company)+"/onboarding", nil)
}

// OnboardingProbe testa um harness (claude, codex, fake) antes de existir persona.
func (c *Client) OnboardingProbe(ctx context.Context, company, agent, model string) (api.OnboardingProbe, error) {
	return get[api.OnboardingProbe](ctx, c, http.MethodPost, cp(company)+"/onboarding/probe", map[string]string{"agent": agent, "model": model})
}

// OnboardingSkip pula o passo do harness.
func (c *Client) OnboardingSkip(ctx context.Context, company, step string) (api.OnboardingState, error) {
	return get[api.OnboardingState](ctx, c, http.MethodPost, cp(company)+"/onboarding/skip", map[string]string{"step": step})
}

// OnboardingDismiss dispensa (true) ou volta a oferecer (false) o assistente.
func (c *Client) OnboardingDismiss(ctx context.Context, company string, dismissed bool) (api.OnboardingState, error) {
	return get[api.OnboardingState](ctx, c, http.MethodPost, cp(company)+"/onboarding/dismiss", map[string]bool{"dismissed": dismissed})
}

// OnboardingInstallTeam instala um pacote do catálogo e ativa a persona de topo.
func (c *Client) OnboardingInstallTeam(ctx context.Context, company, slug string) (api.OnboardingTeamInstalled, error) {
	return get[api.OnboardingTeamInstalled](ctx, c, http.MethodPost, cp(company)+"/onboarding/team", map[string]string{"slug": slug})
}

// OnboardingFirstTask cria a primeira task para a persona (id) e fecha o passo.
func (c *Client) OnboardingFirstTask(ctx context.Context, company, personaID, title, description string) (api.Task, error) {
	return get[api.Task](ctx, c, http.MethodPost, cp(company)+"/onboarding/task",
		map[string]string{"persona": personaID, "title": title, "description": description})
}

// Profile devolve o perfil da própria pessoa.
func (c *Client) Profile(ctx context.Context) (api.UserProfile, error) {
	return get[api.UserProfile](ctx, c, http.MethodGet, "/me/profile", nil)
}

// UpdateProfile altera só os campos presentes em patch (name, avatar_glyph, avatar_tone, timezone, locale).
func (c *Client) UpdateProfile(ctx context.Context, patch map[string]string) (api.UserProfile, error) {
	return get[api.UserProfile](ctx, c, http.MethodPatch, "/me/profile", patch)
}

// WhatsNew devolve as novidades da versão instalada no servidor.
func (c *Client) WhatsNew(ctx context.Context) (api.WhatsNew, error) {
	return get[api.WhatsNew](ctx, c, http.MethodGet, "/whats-new", nil)
}

// DismissWhatsNew dispensa as novidades da chave (campo key de WhatsNew).
func (c *Client) DismissWhatsNew(ctx context.Context, key string) error {
	return c.do(ctx, http.MethodPost, "/whats-new/dismiss", nil, map[string]string{"key": key}, nil)
}

// Nav devolve os favoritos e recentes da pessoa na empresa.
func (c *Client) Nav(ctx context.Context, company string) (api.NavState, error) {
	return get[api.NavState](ctx, c, http.MethodGet, cp(company)+"/nav", nil)
}

// SetFavorite marca ou desmarca uma página (relativa à empresa) como favorita.
func (c *Client) SetFavorite(ctx context.Context, company, path, title string, favorite bool) (api.NavState, error) {
	return get[api.NavState](ctx, c, http.MethodPut, cp(company)+"/nav/favorites", map[string]any{"path": path, "title": title, "favorite": favorite})
}
