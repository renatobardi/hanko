package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Gateways nomeados (ADR-0036): um endpoint MCP para clientes de fora da instalação (uma IDE, um script), com um
// perfil e tokens hkgw_ próprios. O token só é mostrado na criação; o banco guarda o sha256. Admin configura.

const maxGatewayTokenLabelRunes = 120

// CreateToolGatewayInput define um gateway.
type CreateToolGatewayInput struct {
	Name      string
	Slug      string
	ProfileID domain.ID
	Enabled   *bool // padrão ligado
}

// UpdateToolGatewayInput muda um gateway; nil fica como está.
type UpdateToolGatewayInput struct {
	Name      *string
	ProfileID *domain.ID
	Enabled   *bool
}

// CreateGatewayTokenInput define um token.
type CreateGatewayTokenInput struct {
	Label     string
	Actions   []string      // padrão: tools/list e tools/call
	ExpiresIn time.Duration // padrão 90 dias; máximo 366
}

// ToolGatewayView é um gateway com os tokens (sem valores).
type ToolGatewayView struct {
	domain.ToolGateway
	Tokens []domain.ToolGatewayToken `json:"tokens"`
}

func gatewayFromRow(r store.ToolGateway) domain.ToolGateway {
	return domain.ToolGateway{ID: r.ID, CompanyID: r.CompanyID, Slug: r.Slug, Name: r.Name, ProfileID: r.ProfileID, Enabled: r.Enabled,
		CreatedBy: actorOf(r.CreatedByType, r.CreatedByID), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

func tokenFromRow(r store.ToolGatewayToken) domain.ToolGatewayToken {
	return domain.ToolGatewayToken{ID: r.ID, GatewayID: r.GatewayID, Label: r.Label, Prefix: r.Prefix, Actions: nonNil(r.Actions),
		ExpiresAt: r.ExpiresAt, LastUsedAt: r.LastUsedAt, RevokedAt: r.RevokedAt, CreatedBy: actorOf(r.CreatedByType, r.CreatedByID), CreatedAt: r.CreatedAt}
}

func validGatewayName(name string) (string, error) {
	n := strings.TrimSpace(name)
	switch {
	case n == "":
		return "", domain.Invalid("name", "required")
	case utf8.RuneCountInString(n) > 120:
		return "", domain.Invalid("name", "at most 120 characters")
	}
	return n, nil
}

// CreateToolGateway cria um gateway nomeado (admin).
func (s *Service) CreateToolGateway(ctx context.Context, actor domain.Actor, companyID domain.ID, in CreateToolGatewayInput) (ToolGatewayView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return ToolGatewayView{}, err
	}
	name, err := validGatewayName(in.Name)
	if err != nil {
		return ToolGatewayView{}, err
	}
	slug, err := resolveSlug(in.Slug, name)
	if err != nil {
		return ToolGatewayView{}, err
	}
	if slices.Contains(reservedUISlugs, slug) {
		return ToolGatewayView{}, domain.Invalid("slug", fmt.Sprintf("%q is reserved", slug))
	}
	enabled := in.Enabled == nil || *in.Enabled
	var out ToolGatewayView
	err = s.withTx(ctx, func(t *txc) error {
		prof, err := t.q.GetToolProfileByID(ctx, store.GetToolProfileByIDParams{CompanyID: companyID, ID: in.ProfileID})
		if err != nil {
			return domain.Invalid("profile", "tool profile not found in company")
		}
		n, err := t.q.CountToolGateways(ctx, companyID)
		if err != nil {
			return fmt.Errorf("count gateways: %w", err)
		}
		if n >= domain.MaxToolGateways {
			return domain.Invalid("gateways", fmt.Sprintf("no máximo %d gateways por empresa", domain.MaxToolGateways))
		}
		row, err := t.q.CreateToolGateway(ctx, store.CreateToolGatewayParams{ID: domain.NewID(), CompanyID: companyID, Slug: slug, Name: name,
			ProfileID: prof.ID, Enabled: enabled, ActorType: string(actor.Type), ActorID: actor.ID, Now: t.now})
		if err != nil {
			return mapErr(err, "create gateway")
		}
		out = ToolGatewayView{ToolGateway: gatewayFromRow(row), Tokens: []domain.ToolGatewayToken{}}
		out.ProfileSlug, out.ProfileName = prof.Slug, prof.Name
		return t.activity(actor, companyID, "tool_gateway.created", "tool_gateway", row.ID,
			map[string]any{"slug": row.Slug, "name": row.Name, "profile": prof.Slug}, "")
	})
	return out, err
}

// ListToolGateways lista os gateways (qualquer membro lê; tokens nunca têm o valor).
func (s *Service) ListToolGateways(ctx context.Context, companyID domain.ID) ([]domain.ToolGateway, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.read().ListToolGateways(ctx, store.ListToolGatewaysParams{CompanyID: companyID, Now: s.Now().UTC()})
	if err != nil {
		return nil, fmt.Errorf("list gateways: %w", err)
	}
	out := make([]domain.ToolGateway, len(rows))
	for i, r := range rows {
		g := gatewayFromRow(store.ToolGateway{ID: r.ID, CompanyID: r.CompanyID, Slug: r.Slug, Name: r.Name, ProfileID: r.ProfileID,
			Enabled: r.Enabled, CreatedByType: r.CreatedByType, CreatedByID: r.CreatedByID, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt})
		g.ProfileSlug, g.ProfileName, g.ActiveTokens = r.ProfileSlug, r.ProfileName, int(r.ActiveTokens)
		if ts, ok := r.LastUsedAt.(time.Time); ok {
			g.LastUsedAt = &ts
		}
		out[i] = g
	}
	return out, nil
}

func resolveGatewayRef(ctx context.Context, q *store.Queries, companyID domain.ID, ref string) (store.ToolGateway, error) {
	var r store.ToolGateway
	var err error
	if id, perr := uuid.Parse(ref); perr == nil {
		r, err = q.GetToolGatewayByID(ctx, store.GetToolGatewayByIDParams{CompanyID: companyID, ID: id})
	} else {
		r, err = q.GetToolGatewayBySlug(ctx, store.GetToolGatewayBySlugParams{CompanyID: companyID, Slug: ref})
	}
	if err != nil {
		return store.ToolGateway{}, mapErr(err, "get gateway")
	}
	return r, nil
}

// GetToolGateway devolve um gateway (id ou slug) com os tokens.
func (s *Service) GetToolGateway(ctx context.Context, companyID domain.ID, ref string) (ToolGatewayView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return ToolGatewayView{}, err
	}
	q := s.read()
	row, err := resolveGatewayRef(ctx, q, companyID, ref)
	if err != nil {
		return ToolGatewayView{}, err
	}
	return s.gatewayView(ctx, q, row)
}

func (s *Service) gatewayView(ctx context.Context, q *store.Queries, row store.ToolGateway) (ToolGatewayView, error) {
	out := ToolGatewayView{ToolGateway: gatewayFromRow(row), Tokens: []domain.ToolGatewayToken{}}
	if prof, err := q.GetToolProfileByID(ctx, store.GetToolProfileByIDParams{CompanyID: row.CompanyID, ID: row.ProfileID}); err == nil {
		out.ProfileSlug, out.ProfileName = prof.Slug, prof.Name
	}
	toks, err := q.ListGatewayTokens(ctx, store.ListGatewayTokensParams{CompanyID: row.CompanyID, GatewayID: row.ID})
	if err != nil {
		return out, fmt.Errorf("list gateway tokens: %w", err)
	}
	now := s.Now().UTC()
	for _, tk := range toks {
		t := tokenFromRow(tk)
		out.Tokens = append(out.Tokens, t)
		if t.Active(now) {
			out.ActiveTokens++
		}
		if t.LastUsedAt != nil && (out.LastUsedAt == nil || t.LastUsedAt.After(*out.LastUsedAt)) {
			lu := *t.LastUsedAt
			out.LastUsedAt = &lu
		}
	}
	return out, nil
}

// UpdateToolGateway muda nome, perfil ou estado (admin).
func (s *Service) UpdateToolGateway(ctx context.Context, actor domain.Actor, companyID, gatewayID domain.ID, in UpdateToolGatewayInput) (ToolGatewayView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return ToolGatewayView{}, err
	}
	var out ToolGatewayView
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetToolGatewayByID(ctx, store.GetToolGatewayByIDParams{CompanyID: companyID, ID: gatewayID})
		if err != nil {
			return mapErr(err, "get gateway")
		}
		name, prof, enabled := cur.Name, cur.ProfileID, cur.Enabled
		var changed []string
		if in.Name != nil {
			n, err := validGatewayName(*in.Name)
			if err != nil {
				return err
			}
			if n != name {
				name = n
				changed = append(changed, "name")
			}
		}
		if in.ProfileID != nil && *in.ProfileID != prof {
			if _, err := t.q.GetToolProfileByID(ctx, store.GetToolProfileByIDParams{CompanyID: companyID, ID: *in.ProfileID}); err != nil {
				return domain.Invalid("profile", "tool profile not found in company")
			}
			prof = *in.ProfileID
			changed = append(changed, "profile")
		}
		if in.Enabled != nil && *in.Enabled != enabled {
			enabled = *in.Enabled
			changed = append(changed, "enabled")
		}
		row := cur
		if len(changed) > 0 {
			if row, err = t.q.UpdateToolGateway(ctx, store.UpdateToolGatewayParams{CompanyID: companyID, ID: gatewayID, Name: name,
				ProfileID: prof, Enabled: enabled, Now: t.now}); err != nil {
				return mapErr(err, "update gateway")
			}
		}
		if out, err = s.gatewayView(ctx, t.q, row); err != nil {
			return err
		}
		if len(changed) == 0 {
			return nil
		}
		return t.activity(actor, companyID, "tool_gateway.updated", "tool_gateway", row.ID, map[string]any{"slug": row.Slug, "fields": changed}, "")
	})
	return out, err
}

// DeleteToolGateway apaga um gateway e seus tokens (admin).
func (s *Service) DeleteToolGateway(ctx context.Context, actor domain.Actor, companyID, gatewayID domain.ID) error {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetToolGatewayByID(ctx, store.GetToolGatewayByIDParams{CompanyID: companyID, ID: gatewayID})
		if err != nil {
			return mapErr(err, "get gateway")
		}
		if _, err := t.q.DeleteToolGateway(ctx, store.DeleteToolGatewayParams{CompanyID: companyID, ID: gatewayID}); err != nil {
			return fmt.Errorf("delete gateway: %w", err)
		}
		return t.activity(actor, companyID, "tool_gateway.deleted", "tool_gateway", gatewayID, map[string]any{"slug": cur.Slug, "name": cur.Name}, "")
	})
}

// newGatewayToken gera "hkgw_<32 bytes base64url>", o prefixo exibível e o hash.
func newGatewayToken() (tok, prefix, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", "", fmt.Errorf("gerar token: %w", err)
	}
	tok = domain.GatewayTokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	return tok, tok[:len(domain.GatewayTokenPrefix)+6], auth.HashToken(tok), nil
}

// CreateGatewayToken gera um token para o gateway (admin). O valor volta só aqui.
func (s *Service) CreateGatewayToken(ctx context.Context, actor domain.Actor, companyID, gatewayID domain.ID, in CreateGatewayTokenInput) (domain.ToolGatewayToken, string, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.ToolGatewayToken{}, "", err
	}
	label := strings.TrimSpace(in.Label)
	if utf8.RuneCountInString(label) > maxGatewayTokenLabelRunes || strings.ContainsAny(label, "\r\n") {
		return domain.ToolGatewayToken{}, "", domain.Invalid("label", fmt.Sprintf("at most %d characters, one line", maxGatewayTokenLabelRunes))
	}
	actions := in.Actions
	if len(actions) == 0 {
		actions = slices.Clone(domain.GatewayActions)
	}
	seen := map[string]bool{}
	var acts []string
	for _, a := range actions {
		a = strings.TrimSpace(a)
		if !slices.Contains(domain.GatewayActions, a) {
			return domain.ToolGatewayToken{}, "", domain.Invalid("actions", "use tools/list e/ou tools/call")
		}
		if !seen[a] {
			seen[a] = true
			acts = append(acts, a)
		}
	}
	slices.Sort(acts)
	ttl := in.ExpiresIn
	if ttl == 0 {
		ttl = domain.DefaultGatewayTokenTTL
	}
	if ttl < time.Hour || ttl > domain.MaxGatewayTokenTTL {
		return domain.ToolGatewayToken{}, "", domain.Invalid("expires_in", "use de 1 hora a 366 dias")
	}
	tok, prefix, hash, err := newGatewayToken()
	if err != nil {
		return domain.ToolGatewayToken{}, "", err
	}
	var out domain.ToolGatewayToken
	err = s.withTx(ctx, func(t *txc) error {
		g, err := t.q.GetToolGatewayByID(ctx, store.GetToolGatewayByIDParams{CompanyID: companyID, ID: gatewayID})
		if err != nil {
			return mapErr(err, "get gateway")
		}
		n, err := t.q.CountActiveGatewayTokens(ctx, store.CountActiveGatewayTokensParams{GatewayID: gatewayID, Now: t.now})
		if err != nil {
			return fmt.Errorf("count tokens: %w", err)
		}
		if n >= domain.MaxGatewayTokens {
			return domain.Invalid("tokens", fmt.Sprintf("no máximo %d tokens ativos por gateway; revogue um antes", domain.MaxGatewayTokens))
		}
		row, err := t.q.CreateGatewayToken(ctx, store.CreateGatewayTokenParams{ID: domain.NewID(), CompanyID: companyID, GatewayID: gatewayID,
			Label: label, Prefix: prefix, TokenHash: hash, Actions: acts, ExpiresAt: t.now.Add(ttl), ActorType: string(actor.Type),
			ActorID: actor.ID, Now: t.now})
		if err != nil {
			return mapErr(err, "create gateway token")
		}
		out = tokenFromRow(row)
		return t.activity(actor, companyID, "tool_gateway.token_created", "tool_gateway", g.ID, map[string]any{"slug": g.Slug,
			"token_id": row.ID, "prefix": row.Prefix, "label": row.Label, "actions": row.Actions, "expires_at": row.ExpiresAt}, "")
	})
	if err != nil {
		return domain.ToolGatewayToken{}, "", err
	}
	return out, tok, nil
}

// RevokeGatewayToken revoga um token (admin). Revogar de novo não muda nada.
func (s *Service) RevokeGatewayToken(ctx context.Context, actor domain.Actor, companyID, gatewayID, tokenID domain.ID) (domain.ToolGatewayToken, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.ToolGatewayToken{}, err
	}
	var out domain.ToolGatewayToken
	err := s.withTx(ctx, func(t *txc) error {
		g, err := t.q.GetToolGatewayByID(ctx, store.GetToolGatewayByIDParams{CompanyID: companyID, ID: gatewayID})
		if err != nil {
			return mapErr(err, "get gateway")
		}
		row, err := t.q.RevokeGatewayToken(ctx, store.RevokeGatewayTokenParams{CompanyID: companyID, GatewayID: gatewayID, ID: tokenID, Now: &t.now})
		if err != nil {
			return mapErr(err, "revoke gateway token")
		}
		out = tokenFromRow(row)
		return t.activity(actor, companyID, "tool_gateway.token_revoked", "tool_gateway", g.ID, map[string]any{"slug": g.Slug,
			"token_id": row.ID, "prefix": row.Prefix}, "")
	})
	return out, err
}

// GatewayCaller autentica um token hkgw_ para o gateway /mcp/gw/{empresa}/{gateway}. Qualquer problema (empresa,
// gateway desligado, token de outro gateway, expirado ou revogado) é ErrUnauthenticated, sem dizer qual.
func (s *Service) GatewayCaller(ctx context.Context, companySlug, gatewaySlug, token string) (ToolCaller, error) {
	if !strings.HasPrefix(token, domain.GatewayTokenPrefix) {
		return ToolCaller{}, domain.ErrUnauthenticated
	}
	q := s.read()
	tk, err := q.GetGatewayTokenByHash(ctx, auth.HashToken(token))
	if err != nil {
		if isNoRows(err) {
			return ToolCaller{}, domain.ErrUnauthenticated
		}
		return ToolCaller{}, fmt.Errorf("get gateway token: %w", err)
	}
	now := s.Now().UTC()
	if !tokenFromRow(tk).Active(now) {
		return ToolCaller{}, domain.ErrUnauthenticated
	}
	co, err := q.GetCompanyBySlugForGateway(ctx, companySlug)
	if err != nil || co.ID != tk.CompanyID {
		return ToolCaller{}, domain.ErrUnauthenticated
	}
	g, err := q.GetToolGatewayBySlug(ctx, store.GetToolGatewayBySlugParams{CompanyID: co.ID, Slug: gatewaySlug})
	if err != nil || g.ID != tk.GatewayID || !g.Enabled {
		return ToolCaller{}, domain.ErrUnauthenticated
	}
	stale := now.Add(-time.Minute)
	if err := q.TouchGatewayToken(ctx, store.TouchGatewayTokenParams{ID: tk.ID, Now: &now, StaleBefore: &stale}); err != nil {
		return ToolCaller{}, fmt.Errorf("touch gateway token: %w", err)
	}
	gid, pid, tid := g.ID, g.ProfileID, tk.ID
	return ToolCaller{CompanyID: co.ID, GatewayID: &gid, GatewaySlug: g.Slug, GatewayName: g.Name, ProfileID: &pid, TokenID: &tid,
		Actions: nonNil(tk.Actions)}, nil
}
