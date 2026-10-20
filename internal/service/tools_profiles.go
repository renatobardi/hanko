package service

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Perfis de ferramentas (ADR-0036): conjuntos nomeados de regras include/exclude com ação padrão (deny por
// padrão), ligados a empresa, projeto ou persona. Admin configura; qualquer membro lê. A resolução escolhe UM
// perfil: o do escopo mais estreito (persona > projeto > empresa) e, no mesmo escopo, a maior prioridade e
// depois o vínculo mais novo. Sem perfil algum, vale o comportamento anterior: todas as tools das integrações
// da persona.

const maxToolProfileDescRunes = 500

// CreateToolProfileInput define um perfil novo.
type CreateToolProfileInput struct {
	Name            string
	Slug            string // derivado do nome quando vazio
	Description     string
	DefaultAction   domain.ToolDefaultAction // padrão deny
	RateLimitPerMin int                      // padrão 120
}

// UpdateToolProfileInput muda um perfil; nil fica como está.
type UpdateToolProfileInput struct {
	Name            *string
	Description     *string
	DefaultAction   *domain.ToolDefaultAction
	RateLimitPerMin *int
}

// ToolRuleInput é uma regra a acrescentar (ou trocar: a mesma combinação de alvo troca efeito e modo).
type ToolRuleInput struct {
	Target        domain.ToolRuleTarget
	IntegrationID *domain.ID
	ToolName      string
	Risk          domain.ToolRisk
	Effect        domain.ToolRuleEffect
	Mode          domain.ToolMode // padrão allow
}

// ToolBindingInput liga um perfil a um escopo.
type ToolBindingInput struct {
	Scope     domain.ToolBindingScope
	ProjectID *domain.ID
	PersonaID *domain.ID
	Priority  int
}

// ToolProfileView é um perfil com regras, vínculos e quantos gateways o usam.
type ToolProfileView struct {
	domain.ToolProfile
	Gateways int `json:"gateways"`
}

func profileFromRow(r store.ToolProfile) domain.ToolProfile {
	return domain.ToolProfile{ID: r.ID, CompanyID: r.CompanyID, Slug: r.Slug, Name: r.Name, Description: r.Description,
		DefaultAction: domain.ToolDefaultAction(r.DefaultAction), RateLimitPerMin: int(r.RateLimitPerMin),
		Rules: []domain.ToolRule{}, Bindings: []domain.ToolBinding{},
		CreatedBy: actorOf(r.CreatedByType, r.CreatedByID), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

func ruleFromRow(r store.ToolProfileRule, slug string) domain.ToolRule {
	return domain.ToolRule{ID: r.ID, Target: domain.ToolRuleTarget(r.Target), IntegrationID: r.IntegrationID, IntegrationSlug: slug,
		ToolName: r.ToolName, Risk: domain.ToolRisk(r.Risk), Effect: domain.ToolRuleEffect(r.Effect), Mode: domain.ToolMode(r.Mode),
		CreatedAt: r.CreatedAt}
}

func bindingFromRow(r store.ToolProfileBinding, profileSlug, target string) domain.ToolBinding {
	return domain.ToolBinding{ID: r.ID, ProfileID: r.ProfileID, Scope: domain.ToolBindingScope(r.Scope), ProjectID: r.ProjectID,
		PersonaID: r.PersonaID, Priority: int(r.Priority), CreatedAt: r.CreatedAt, ProfileSlug: profileSlug, TargetName: target}
}

func validateProfileFields(name, desc string, def domain.ToolDefaultAction, rate int) (string, string, error) {
	n := strings.TrimSpace(name)
	switch {
	case n == "":
		return "", "", domain.Invalid("name", "required")
	case utf8.RuneCountInString(n) > 120:
		return "", "", domain.Invalid("name", "at most 120 characters")
	}
	d := strings.TrimSpace(desc)
	if utf8.RuneCountInString(d) > maxToolProfileDescRunes {
		return "", "", domain.Invalid("description", fmt.Sprintf("at most %d characters", maxToolProfileDescRunes))
	}
	if !def.Valid() {
		return "", "", domain.Invalid("default_action", "use deny ou allow")
	}
	if rate < 1 || rate > domain.MaxToolRateLimit {
		return "", "", domain.Invalid("rate_limit_per_min", fmt.Sprintf("use 1 a %d", domain.MaxToolRateLimit))
	}
	return n, d, nil
}

// CreateToolProfile cria um perfil (admin).
func (s *Service) CreateToolProfile(ctx context.Context, actor domain.Actor, companyID domain.ID, in CreateToolProfileInput) (ToolProfileView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return ToolProfileView{}, err
	}
	if in.DefaultAction == "" {
		in.DefaultAction = domain.ToolDefaultDeny
	}
	if in.RateLimitPerMin == 0 {
		in.RateLimitPerMin = domain.DefaultToolRateLimit
	}
	name, desc, err := validateProfileFields(in.Name, in.Description, in.DefaultAction, in.RateLimitPerMin)
	if err != nil {
		return ToolProfileView{}, err
	}
	slug, err := resolveSlug(in.Slug, name)
	if err != nil {
		return ToolProfileView{}, err
	}
	if slices.Contains(reservedUISlugs, slug) || slug == "novo" || slug == "efetivas" {
		return ToolProfileView{}, domain.Invalid("slug", fmt.Sprintf("%q is reserved", slug))
	}
	var out ToolProfileView
	err = s.withTx(ctx, func(t *txc) error {
		n, err := t.q.CountToolProfiles(ctx, companyID)
		if err != nil {
			return fmt.Errorf("count profiles: %w", err)
		}
		if n >= domain.MaxToolProfiles {
			return domain.Invalid("profiles", fmt.Sprintf("no máximo %d perfis por empresa", domain.MaxToolProfiles))
		}
		row, err := t.q.CreateToolProfile(ctx, store.CreateToolProfileParams{ID: domain.NewID(), CompanyID: companyID, Slug: slug, Name: name,
			Description: desc, DefaultAction: string(in.DefaultAction), RateLimitPerMin: i32(in.RateLimitPerMin),
			ActorType: string(actor.Type), ActorID: actor.ID, Now: t.now})
		if err != nil {
			return mapErr(err, "create tool profile")
		}
		out = ToolProfileView{ToolProfile: profileFromRow(row)}
		return t.activity(actor, companyID, "tool_profile.created", "tool_profile", row.ID,
			map[string]any{"slug": row.Slug, "name": row.Name, "default_action": row.DefaultAction}, "")
	})
	return out, err
}

// resolveProfileRef resolve um perfil por id ou slug.
func resolveProfileRef(ctx context.Context, q *store.Queries, companyID domain.ID, ref string) (store.ToolProfile, error) {
	var r store.ToolProfile
	var err error
	if id, perr := uuid.Parse(ref); perr == nil {
		r, err = q.GetToolProfileByID(ctx, store.GetToolProfileByIDParams{CompanyID: companyID, ID: id})
	} else {
		r, err = q.GetToolProfileBySlug(ctx, store.GetToolProfileBySlugParams{CompanyID: companyID, Slug: ref})
	}
	if err != nil {
		return store.ToolProfile{}, mapErr(err, "get tool profile")
	}
	return r, nil
}

// loadProfile monta o perfil com regras e vínculos.
func loadProfile(ctx context.Context, q *store.Queries, row store.ToolProfile) (ToolProfileView, error) {
	p := ToolProfileView{ToolProfile: profileFromRow(row)}
	rules, err := q.ListToolProfileRules(ctx, store.ListToolProfileRulesParams{CompanyID: row.CompanyID, ProfileID: row.ID})
	if err != nil {
		return p, fmt.Errorf("list rules: %w", err)
	}
	for _, r := range rules {
		p.Rules = append(p.Rules, ruleFromRow(store.ToolProfileRule{ID: r.ID, CompanyID: r.CompanyID, ProfileID: r.ProfileID, Target: r.Target,
			IntegrationID: r.IntegrationID, ToolName: r.ToolName, Risk: r.Risk, Effect: r.Effect, Mode: r.Mode, CreatedAt: r.CreatedAt}, r.IntegrationSlug))
	}
	binds, err := q.ListToolBindings(ctx, row.CompanyID)
	if err != nil {
		return p, fmt.Errorf("list bindings: %w", err)
	}
	for _, b := range binds {
		if b.ProfileID == row.ID {
			p.Bindings = append(p.Bindings, bindingFromRow(store.ToolProfileBinding{ID: b.ID, CompanyID: b.CompanyID, ProfileID: b.ProfileID,
				Scope: b.Scope, ProjectID: b.ProjectID, PersonaID: b.PersonaID, Priority: b.Priority, CreatedAt: b.CreatedAt}, b.ProfileSlug, b.TargetName))
		}
	}
	n, err := q.CountToolGatewaysForProfile(ctx, row.ID)
	if err != nil {
		return p, fmt.Errorf("count gateways: %w", err)
	}
	p.Gateways = int(n)
	return p, nil
}

// GetToolProfile devolve um perfil (por id ou slug) com regras e vínculos.
func (s *Service) GetToolProfile(ctx context.Context, companyID domain.ID, ref string) (ToolProfileView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return ToolProfileView{}, err
	}
	q := s.read()
	row, err := resolveProfileRef(ctx, q, companyID, ref)
	if err != nil {
		return ToolProfileView{}, err
	}
	return loadProfile(ctx, q, row)
}

// ListToolProfiles lista os perfis da empresa com regras e vínculos.
func (s *Service) ListToolProfiles(ctx context.Context, companyID domain.ID) ([]ToolProfileView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	q := s.read()
	rows, err := q.ListToolProfiles(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list tool profiles: %w", err)
	}
	out := make([]ToolProfileView, 0, len(rows))
	for _, r := range rows {
		v, err := loadProfile(ctx, q, r)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// UpdateToolProfile muda nome, descrição, ação padrão ou teto de taxa (admin).
func (s *Service) UpdateToolProfile(ctx context.Context, actor domain.Actor, companyID, profileID domain.ID, in UpdateToolProfileInput) (ToolProfileView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return ToolProfileView{}, err
	}
	var out ToolProfileView
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetToolProfileByIDForUpdate(ctx, store.GetToolProfileByIDForUpdateParams{CompanyID: companyID, ID: profileID})
		if err != nil {
			return mapErr(err, "get tool profile")
		}
		name, desc, def, rate := cur.Name, cur.Description, domain.ToolDefaultAction(cur.DefaultAction), int(cur.RateLimitPerMin)
		if in.Name != nil {
			name = *in.Name
		}
		if in.Description != nil {
			desc = *in.Description
		}
		if in.DefaultAction != nil {
			def = *in.DefaultAction
		}
		if in.RateLimitPerMin != nil {
			rate = *in.RateLimitPerMin
		}
		name, desc, err = validateProfileFields(name, desc, def, rate)
		if err != nil {
			return err
		}
		row, err := t.q.UpdateToolProfile(ctx, store.UpdateToolProfileParams{CompanyID: companyID, ID: profileID, Name: name, Description: desc,
			DefaultAction: string(def), RateLimitPerMin: i32(rate), Now: t.now})
		if err != nil {
			return mapErr(err, "update tool profile")
		}
		var changed []string
		if row.Name != cur.Name {
			changed = append(changed, "name")
		}
		if row.Description != cur.Description {
			changed = append(changed, "description")
		}
		if row.DefaultAction != cur.DefaultAction {
			changed = append(changed, "default_action")
		}
		if row.RateLimitPerMin != cur.RateLimitPerMin {
			changed = append(changed, "rate_limit_per_min")
		}
		if out, err = loadProfile(ctx, t.q, row); err != nil {
			return err
		}
		if len(changed) == 0 {
			return nil
		}
		return t.activity(actor, companyID, "tool_profile.updated", "tool_profile", row.ID, map[string]any{"slug": row.Slug, "fields": changed}, "")
	})
	return out, err
}

// DeleteToolProfile apaga um perfil (admin) com as regras e vínculos. Um perfil usado por gateway não sai.
func (s *Service) DeleteToolProfile(ctx context.Context, actor domain.Actor, companyID, profileID domain.ID) error {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetToolProfileByIDForUpdate(ctx, store.GetToolProfileByIDForUpdateParams{CompanyID: companyID, ID: profileID})
		if err != nil {
			return mapErr(err, "get tool profile")
		}
		n, err := t.q.CountToolGatewaysForProfile(ctx, profileID)
		if err != nil {
			return fmt.Errorf("count gateways: %w", err)
		}
		if n > 0 {
			return domain.Invalid("profile", fmt.Sprintf("o perfil é usado por %d gateway(s); troque o perfil deles antes de apagar", n))
		}
		if _, err := t.q.DeleteToolProfile(ctx, store.DeleteToolProfileParams{CompanyID: companyID, ID: profileID}); err != nil {
			return fmt.Errorf("delete tool profile: %w", err)
		}
		return t.activity(actor, companyID, "tool_profile.deleted", "tool_profile", profileID, map[string]any{"slug": cur.Slug, "name": cur.Name}, "")
	})
}

// validateRule confere a forma da regra e que a integração é da empresa.
func (t *txc) validateRule(companyID domain.ID, in *ToolRuleInput) (store.Integration, error) {
	var integ store.Integration
	if !in.Target.Valid() {
		return integ, domain.Invalid("target", "use integration, tool ou risk")
	}
	if !in.Effect.Valid() {
		return integ, domain.Invalid("effect", "use include ou exclude")
	}
	if in.Mode == "" {
		in.Mode = domain.ToolModeAllow
	}
	if !in.Mode.Valid() {
		return integ, domain.Invalid("mode", "use allow ou ask")
	}
	if in.Effect == domain.ToolExclude {
		in.Mode = domain.ToolModeAllow // o modo só vale para inclusão
	}
	in.ToolName = strings.TrimSpace(in.ToolName)
	switch in.Target {
	case domain.ToolTargetIntegration, domain.ToolTargetTool:
		if in.IntegrationID == nil {
			return integ, domain.Invalid("integration", "required")
		}
		if in.Risk != "" {
			return integ, domain.Invalid("risk", "only applies to risk rules")
		}
		if in.Target == domain.ToolTargetIntegration && in.ToolName != "" {
			return integ, domain.Invalid("tool", "only applies to tool rules")
		}
		if in.Target == domain.ToolTargetTool && !toolNameRe.MatchString(in.ToolName) {
			return integ, domain.Invalid("tool", "nome de tool inválido")
		}
	case domain.ToolTargetRisk:
		if !in.Risk.Valid() {
			return integ, domain.Invalid("risk", "use read, write, destructive ou unknown")
		}
		if in.ToolName != "" {
			return integ, domain.Invalid("tool", "only applies to tool rules")
		}
	}
	if in.IntegrationID != nil {
		r, err := t.q.GetIntegrationByID(t.ctx, store.GetIntegrationByIDParams{CompanyID: companyID, ID: *in.IntegrationID})
		if err != nil {
			return integ, domain.Invalid("integration", "integration not found in company")
		}
		if domain.IntegrationKind(r.Kind) != domain.IntegrationMCP {
			return integ, domain.Invalid("integration", "só servidores MCP entram em perfis")
		}
		integ = r
		stdio := parseIntegrationConfig(r.Config).Transport == domain.TransportStdio
		if stdio && in.Target != domain.ToolTargetIntegration {
			return integ, domain.Invalid("target", "MCP stdio não passa pelo gateway: o perfil só inclui ou exclui a integração inteira")
		}
		if stdio && in.Effect == domain.ToolInclude && in.Mode == domain.ToolModeAsk {
			return integ, domain.Invalid("mode", "MCP stdio não passa pelo gateway: não há como perguntar antes de cada chamada")
		}
	}
	return integ, nil
}

func (t *txc) insertRule(companyID, profileID domain.ID, in ToolRuleInput) (store.ToolProfileRule, error) {
	row, err := t.q.UpsertToolProfileRule(t.ctx, store.UpsertToolProfileRuleParams{ID: domain.NewID(), CompanyID: companyID, ProfileID: profileID,
		Target: string(in.Target), IntegrationID: in.IntegrationID, ToolName: in.ToolName, Risk: string(in.Risk), Effect: string(in.Effect),
		Mode: string(in.Mode), Now: t.now})
	if err != nil {
		return row, mapErr(err, "upsert rule")
	}
	return row, nil
}

func (t *txc) checkRuleCount(profileID domain.ID, adding int) error {
	n, err := t.q.CountToolProfileRules(t.ctx, profileID)
	if err != nil {
		return fmt.Errorf("count rules: %w", err)
	}
	if int(n)+adding > domain.MaxToolProfileRules {
		return domain.Invalid("rules", fmt.Sprintf("no máximo %d regras por perfil", domain.MaxToolProfileRules))
	}
	return nil
}

// AddToolProfileRule acrescenta (ou troca) uma regra (admin).
func (s *Service) AddToolProfileRule(ctx context.Context, actor domain.Actor, companyID, profileID domain.ID, in ToolRuleInput) (domain.ToolRule, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.ToolRule{}, err
	}
	var out domain.ToolRule
	err := s.withTx(ctx, func(t *txc) error {
		prof, err := t.q.GetToolProfileByIDForUpdate(ctx, store.GetToolProfileByIDForUpdateParams{CompanyID: companyID, ID: profileID})
		if err != nil {
			return mapErr(err, "get tool profile")
		}
		integ, err := t.validateRule(companyID, &in)
		if err != nil {
			return err
		}
		if err := t.checkRuleCount(profileID, 1); err != nil {
			return err
		}
		row, err := t.insertRule(companyID, profileID, in)
		if err != nil {
			return err
		}
		if err := t.q.TouchToolProfile(ctx, store.TouchToolProfileParams{CompanyID: companyID, ID: profileID, Now: t.now}); err != nil {
			return fmt.Errorf("touch profile: %w", err)
		}
		out = ruleFromRow(row, integ.Slug)
		return t.activity(actor, companyID, "tool_profile.rule_set", "tool_profile", profileID, map[string]any{"slug": prof.Slug,
			"target": row.Target, "integration": integ.Slug, "tool": row.ToolName, "risk": row.Risk, "effect": row.Effect, "mode": row.Mode}, "")
	})
	return out, err
}

// DeleteToolProfileRule tira uma regra (admin).
func (s *Service) DeleteToolProfileRule(ctx context.Context, actor domain.Actor, companyID, profileID, ruleID domain.ID) error {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		prof, err := t.q.GetToolProfileByIDForUpdate(ctx, store.GetToolProfileByIDForUpdateParams{CompanyID: companyID, ID: profileID})
		if err != nil {
			return mapErr(err, "get tool profile")
		}
		n, err := t.q.DeleteToolProfileRule(ctx, store.DeleteToolProfileRuleParams{CompanyID: companyID, ProfileID: profileID, ID: ruleID})
		if err != nil {
			return fmt.Errorf("delete rule: %w", err)
		}
		if n == 0 {
			return notFound("rule")
		}
		if err := t.q.TouchToolProfile(ctx, store.TouchToolProfileParams{CompanyID: companyID, ID: profileID, Now: t.now}); err != nil {
			return fmt.Errorf("touch profile: %w", err)
		}
		return t.activity(actor, companyID, "tool_profile.rule_deleted", "tool_profile", profileID, map[string]any{"slug": prof.Slug, "rule_id": ruleID}, "")
	})
}

// Escolhas do assistente de perfis, por integração e por tool.
const (
	ToolChoiceDefault = ""      // segue o padrão do perfil (ou a escolha da integração)
	ToolChoiceOff     = "off"   // desligado: exclui
	ToolChoiceAsk     = "ask"   // perguntar antes
	ToolChoiceAllow   = "allow" // permitido
)

// IntegrationToolChoices é o passo 2 do assistente para uma integração: a escolha do grupo (a integração
// inteira) e as escolhas por tool. Grupo "off" exclui a integração inteira (excluir vence, então as escolhas por
// tool não valem); por tool, off exclui, ask/allow incluem com o modo e vazio herda.
type IntegrationToolChoices struct {
	IntegrationID domain.ID
	Group         string
	Tools         map[string]string
}

func validChoice(c string) bool {
	return c == ToolChoiceDefault || c == ToolChoiceOff || c == ToolChoiceAsk || c == ToolChoiceAllow
}

func choiceRule(c string) (domain.ToolRuleEffect, domain.ToolMode) {
	switch c {
	case ToolChoiceOff:
		return domain.ToolExclude, domain.ToolModeAllow
	case ToolChoiceAsk:
		return domain.ToolInclude, domain.ToolModeAsk
	}
	return domain.ToolInclude, domain.ToolModeAllow
}

// SetIntegrationToolChoices reescreve as regras de integração e de tool de um perfil para uma integração (o passo
// 2 do assistente). As regras de risco ficam como estão.
func (s *Service) SetIntegrationToolChoices(ctx context.Context, actor domain.Actor, companyID, profileID domain.ID, in IntegrationToolChoices) (ToolProfileView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return ToolProfileView{}, err
	}
	if !validChoice(in.Group) {
		return ToolProfileView{}, domain.Invalid("group", "use off, ask, allow ou vazio")
	}
	if len(in.Tools) > domain.MaxToolsPerCatalog {
		return ToolProfileView{}, domain.Invalid("tools", "tools demais")
	}
	var out ToolProfileView
	err := s.withTx(ctx, func(t *txc) error {
		prof, err := t.q.GetToolProfileByIDForUpdate(ctx, store.GetToolProfileByIDForUpdateParams{CompanyID: companyID, ID: profileID})
		if err != nil {
			return mapErr(err, "get tool profile")
		}
		if _, err := t.q.DeleteToolProfileRulesForIntegration(ctx, store.DeleteToolProfileRulesForIntegrationParams{CompanyID: companyID,
			ProfileID: profileID, IntegrationID: &in.IntegrationID}); err != nil {
			return fmt.Errorf("clear integration rules: %w", err)
		}
		var rules []ToolRuleInput
		iid := in.IntegrationID
		if in.Group != ToolChoiceDefault {
			eff, mode := choiceRule(in.Group)
			rules = append(rules, ToolRuleInput{Target: domain.ToolTargetIntegration, IntegrationID: &iid, Effect: eff, Mode: mode})
		}
		names := make([]string, 0, len(in.Tools))
		for n := range in.Tools {
			names = append(names, n)
		}
		slices.Sort(names)
		for _, n := range names {
			c := in.Tools[n]
			if !validChoice(c) {
				return domain.Invalid("tools", fmt.Sprintf("escolha inválida para %q", n))
			}
			if c == ToolChoiceDefault {
				continue
			}
			eff, mode := choiceRule(c)
			rules = append(rules, ToolRuleInput{Target: domain.ToolTargetTool, IntegrationID: &iid, ToolName: n, Effect: eff, Mode: mode})
		}
		if err := t.checkRuleCount(profileID, len(rules)); err != nil {
			return err
		}
		slug := ""
		for _, r := range rules {
			integ, err := t.validateRule(companyID, &r)
			if err != nil {
				return err
			}
			slug = integ.Slug
			if _, err := t.insertRule(companyID, profileID, r); err != nil {
				return err
			}
		}
		if slug == "" {
			if integ, err := t.q.GetIntegrationByID(ctx, store.GetIntegrationByIDParams{CompanyID: companyID, ID: iid}); err == nil {
				slug = integ.Slug
			} else {
				return domain.Invalid("integration", "integration not found in company")
			}
		}
		if err := t.q.TouchToolProfile(ctx, store.TouchToolProfileParams{CompanyID: companyID, ID: profileID, Now: t.now}); err != nil {
			return fmt.Errorf("touch profile: %w", err)
		}
		if out, err = loadProfile(ctx, t.q, prof); err != nil {
			return err
		}
		return t.activity(actor, companyID, "tool_profile.rules_replaced", "tool_profile", profileID,
			map[string]any{"slug": prof.Slug, "integration": slug, "group": in.Group, "rules": len(rules)}, "")
	})
	return out, err
}

// BindToolProfile liga o perfil a um escopo (admin). O mesmo escopo de novo só troca a prioridade.
func (s *Service) BindToolProfile(ctx context.Context, actor domain.Actor, companyID, profileID domain.ID, in ToolBindingInput) (domain.ToolBinding, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.ToolBinding{}, err
	}
	if !in.Scope.Valid() {
		return domain.ToolBinding{}, domain.Invalid("scope", "use company, project ou persona")
	}
	if in.Priority < -1000 || in.Priority > 1000 {
		return domain.ToolBinding{}, domain.Invalid("priority", "use -1000 a 1000")
	}
	switch in.Scope {
	case domain.ToolScopeCompany:
		if in.ProjectID != nil || in.PersonaID != nil {
			return domain.ToolBinding{}, domain.Invalid("scope", "o vínculo de empresa não leva projeto nem persona")
		}
	case domain.ToolScopeProject:
		if in.ProjectID == nil || in.PersonaID != nil {
			return domain.ToolBinding{}, domain.Invalid("project", "required (and no persona)")
		}
	case domain.ToolScopePersona:
		if in.PersonaID == nil || in.ProjectID != nil {
			return domain.ToolBinding{}, domain.Invalid("persona", "required (and no project)")
		}
	}
	var out domain.ToolBinding
	err := s.withTx(ctx, func(t *txc) error {
		prof, err := t.q.GetToolProfileByIDForUpdate(ctx, store.GetToolProfileByIDForUpdateParams{CompanyID: companyID, ID: profileID})
		if err != nil {
			return mapErr(err, "get tool profile")
		}
		target := ""
		if in.ProjectID != nil {
			p, err := t.q.GetProjectByID(ctx, store.GetProjectByIDParams{CompanyID: companyID, ID: *in.ProjectID})
			if err != nil {
				return domain.Invalid("project", "project not found in company")
			}
			target = p.Name
		}
		if in.PersonaID != nil {
			p, err := t.q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: *in.PersonaID})
			if err != nil {
				return domain.Invalid("persona", "persona not found in company")
			}
			target = p.Name
		}
		row, err := t.q.UpsertToolBinding(ctx, store.UpsertToolBindingParams{ID: domain.NewID(), CompanyID: companyID, ProfileID: profileID,
			Scope: string(in.Scope), ProjectID: in.ProjectID, PersonaID: in.PersonaID, Priority: i32(in.Priority), Now: t.now})
		if err != nil {
			return mapErr(err, "bind tool profile")
		}
		out = bindingFromRow(row, prof.Slug, target)
		return t.activity(actor, companyID, "tool_profile.bound", "tool_profile", profileID, map[string]any{"slug": prof.Slug,
			"scope": row.Scope, "project_id": row.ProjectID, "persona_id": row.PersonaID, "priority": row.Priority}, "")
	})
	return out, err
}

// UnbindToolProfile tira um vínculo (admin).
func (s *Service) UnbindToolProfile(ctx context.Context, actor domain.Actor, companyID, profileID, bindingID domain.ID) error {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		prof, err := t.q.GetToolProfileByID(ctx, store.GetToolProfileByIDParams{CompanyID: companyID, ID: profileID})
		if err != nil {
			return mapErr(err, "get tool profile")
		}
		b, err := t.q.GetToolBinding(ctx, store.GetToolBindingParams{CompanyID: companyID, ProfileID: profileID, ID: bindingID})
		if err != nil {
			return mapErr(err, "get binding")
		}
		if _, err := t.q.DeleteToolBinding(ctx, store.DeleteToolBindingParams{CompanyID: companyID, ProfileID: profileID, ID: bindingID}); err != nil {
			return fmt.Errorf("delete binding: %w", err)
		}
		return t.activity(actor, companyID, "tool_profile.unbound", "tool_profile", profileID, map[string]any{"slug": prof.Slug,
			"scope": b.Scope, "project_id": b.ProjectID, "persona_id": b.PersonaID}, "")
	})
}

// resolveToolProfile escolhe o perfil que vale para a persona (no projeto, quando há). Sem vínculo: nil, nil.
func resolveToolProfile(ctx context.Context, q *store.Queries, companyID, personaID domain.ID, projectID *domain.ID) (*domain.ToolProfile, *domain.ToolBinding, error) {
	rows, err := q.ListToolBindingsFor(ctx, store.ListToolBindingsForParams{CompanyID: companyID, ProjectID: projectID, PersonaID: &personaID})
	if err != nil {
		return nil, nil, fmt.Errorf("list bindings: %w", err)
	}
	var best *domain.ToolBinding
	for _, r := range rows {
		b := bindingFromRow(store.ToolProfileBinding{ID: r.ID, CompanyID: r.CompanyID, ProfileID: r.ProfileID, Scope: r.Scope,
			ProjectID: r.ProjectID, PersonaID: r.PersonaID, Priority: r.Priority, CreatedAt: r.CreatedAt}, r.ProfileSlug, r.TargetName)
		if best == nil || domain.BindingWins(b, *best) {
			bb := b
			best = &bb
		}
	}
	if best == nil {
		return nil, nil, nil
	}
	p, err := loadProfileByID(ctx, q, companyID, best.ProfileID)
	if err != nil {
		return nil, nil, err
	}
	return p, best, nil
}

// loadProfileByID carrega só o perfil e as regras (sem vínculos), para as decisões do gateway.
func loadProfileByID(ctx context.Context, q *store.Queries, companyID, profileID domain.ID) (*domain.ToolProfile, error) {
	row, err := q.GetToolProfileByID(ctx, store.GetToolProfileByIDParams{CompanyID: companyID, ID: profileID})
	if err != nil {
		return nil, mapErr(err, "get tool profile")
	}
	p := profileFromRow(row)
	rules, err := q.ListToolProfileRules(ctx, store.ListToolProfileRulesParams{CompanyID: companyID, ProfileID: profileID})
	if err != nil {
		return nil, fmt.Errorf("list rules: %w", err)
	}
	for _, r := range rules {
		p.Rules = append(p.Rules, ruleFromRow(store.ToolProfileRule{ID: r.ID, CompanyID: r.CompanyID, ProfileID: r.ProfileID, Target: r.Target,
			IntegrationID: r.IntegrationID, ToolName: r.ToolName, Risk: r.Risk, Effect: r.Effect, Mode: r.Mode, CreatedAt: r.CreatedAt}, r.IntegrationSlug))
	}
	return &p, nil
}

// EffectiveTool é uma tool com a decisão efetiva para a persona.
type EffectiveTool struct {
	Tool    domain.IntegrationTool `json:"tool"`
	Verdict domain.ToolVerdict     `json:"verdict"`
	// Visible: aparece no tools/list da persona (ativa no catálogo e permitida pelo perfil).
	Visible bool `json:"visible"`
}

// EffectiveIntegration é uma integração MCP da persona com o que ela enxerga dela.
type EffectiveIntegration struct {
	IntegrationID domain.ID          `json:"integration_id"`
	Slug          string             `json:"slug"`
	Name          string             `json:"name"`
	Transport     string             `json:"transport"`
	Enabled       bool               `json:"enabled"`
	Verdict       domain.ToolVerdict `json:"verdict"` // da integração inteira (stdio) ou resumo (http)
	Catalog       domain.ToolCatalogState
	Tools         []EffectiveTool `json:"tools"`
}

// EffectiveTools é o que uma persona enxerga e por quê.
type EffectiveTools struct {
	PersonaID domain.ID           `json:"persona_id"`
	ProjectID *domain.ID          `json:"project_id,omitempty"`
	Profile   *domain.ToolProfile `json:"profile,omitempty"`
	Binding   *domain.ToolBinding `json:"binding,omitempty"`
	// Policies são as políticas (ADR-0053) que valem para a persona: as dela e as padrão da empresa.
	Policies     []domain.ToolPolicy    `json:"policies"`
	Integrations []EffectiveIntegration `json:"integrations"`
}

// EffectiveToolsFor mostra o que a persona enxerga (num projeto, se informado) e de onde vem cada permissão.
// Qualquer membro lê: não há segredo aqui.
func (s *Service) EffectiveToolsFor(ctx context.Context, companyID, personaID domain.ID, projectID *domain.ID) (EffectiveTools, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return EffectiveTools{}, err
	}
	q := s.read()
	if _, err := q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: personaID}); err != nil {
		return EffectiveTools{}, mapErr(err, "get persona")
	}
	if projectID != nil {
		if _, err := q.GetProjectByID(ctx, store.GetProjectByIDParams{CompanyID: companyID, ID: *projectID}); err != nil {
			return EffectiveTools{}, mapErr(err, "get project")
		}
	}
	prof, bind, err := resolveToolProfile(ctx, q, companyID, personaID, projectID)
	if err != nil {
		return EffectiveTools{}, err
	}
	pols, err := loadToolPolicies(ctx, q, companyID, &personaID)
	if err != nil {
		return EffectiveTools{}, err
	}
	out := EffectiveTools{PersonaID: personaID, ProjectID: projectID, Profile: prof, Binding: bind, Policies: pols, Integrations: []EffectiveIntegration{}}
	integs, err := q.ListMCPIntegrationsForPersona(ctx, store.ListMCPIntegrationsForPersonaParams{CompanyID: companyID, PersonaID: personaID})
	if err != nil {
		return EffectiveTools{}, fmt.Errorf("list persona integrations: %w", err)
	}
	for _, integ := range integs {
		cfg := parseIntegrationConfig(integ.Config)
		ei := EffectiveIntegration{IntegrationID: integ.ID, Slug: integ.Slug, Name: integ.Name, Transport: cfg.Transport, Enabled: integ.Enabled,
			Catalog: domain.ToolCatalogState{IntegrationID: integ.ID}, Tools: []EffectiveTool{}}
		if cfg.Transport == domain.TransportStdio {
			ei.Verdict = domain.EvaluateIntegration(prof, integ.ID)
			out.Integrations = append(out.Integrations, ei)
			continue
		}
		ei.Verdict = domain.ToolVerdict{Allowed: false, Source: "default"}
		if cat, err := q.GetToolCatalog(ctx, store.GetToolCatalogParams{CompanyID: companyID, IntegrationID: integ.ID}); err == nil {
			ei.Catalog = catalogFromRow(cat)
		}
		rows, err := q.ListIntegrationTools(ctx, store.ListIntegrationToolsParams{CompanyID: companyID, IntegrationID: integ.ID})
		if err != nil {
			return EffectiveTools{}, fmt.Errorf("list catalog: %w", err)
		}
		for _, r := range rows {
			tl := toolFromRow(r, integ.Slug)
			v := toolVerdict(gatewayIntegration{row: integ, profile: prof}, pols, ToolCaller{PersonaID: &personaID}, tl.Name, tl.Risk)
			et := EffectiveTool{Tool: tl, Verdict: v, Visible: v.Allowed && tl.Status == domain.ToolActive}
			if et.Visible {
				ei.Verdict = domain.ToolVerdict{Allowed: true, Mode: domain.ToolModeAllow, Source: "tools"}
			}
			ei.Tools = append(ei.Tools, et)
		}
		out.Integrations = append(out.Integrations, ei)
	}
	return out, nil
}
