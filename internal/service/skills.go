package service

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/skillmd"
	"github.com/renatobardi/hanko/internal/store"
)

// Skills (ADR-0029): habilidades reutilizáveis (um SKILL.md) que o admin escreve uma vez e liga às personas.
// Leitura é de qualquer membro da empresa; criar, editar, apagar e ligar/desligar é de admin.

// CreateSkillInput define uma skill nova. Body pode vir como SKILL.md inteiro (com frontmatter): o frontmatter
// completa name e description quando estes vêm vazios e nunca é gravado no corpo.
type CreateSkillInput struct {
	Name        string
	Slug        string // derivado do nome quando vazio
	Description string
	Body        string
}

// UpdateSkillInput muda uma skill; campos nil ficam como estão. A versão sobe quando algo muda.
type UpdateSkillInput struct {
	Name, Description, Body *string
}

// SkillView é uma skill com o número de personas que a usam.
type SkillView struct {
	domain.Skill
	Personas int `json:"personas"`
}

func skillFromRow(r store.Skill) domain.Skill {
	k := domain.Skill{ID: r.ID, CompanyID: r.CompanyID, Slug: r.Slug, Name: r.Name, Description: r.Description,
		Body: r.Body, Version: int(r.Version), CreatedBy: actorOf(r.CreatedByType, r.CreatedByID),
		UpdatedBy: actorOf(r.UpdatedByType, r.UpdatedByID), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		SourceID: r.SourceID, SourcePath: r.SourcePath, SourceModified: r.SourceModified}
	if r.SourceCommit != nil {
		k.SourceCommit = *r.SourceCommit
	}
	return k
}

// normalizeSkill valida e normaliza os campos de uma skill. Absorve o frontmatter do corpo, se houver.
func normalizeSkill(name, description, body string) (n, d, b string, err error) {
	doc, perr := skillmd.Parse(body)
	if perr != nil {
		return "", "", "", domain.Invalid("body", perr.Error())
	}
	if doc.HasFrontmatter {
		body = doc.Body
		if strings.TrimSpace(name) == "" {
			name = doc.Name
		}
		if strings.TrimSpace(description) == "" {
			description = doc.Description
		}
	}
	n = strings.TrimSpace(name)
	d = strings.Join(strings.Fields(description), " ")
	b = strings.TrimSpace(body)
	switch {
	case n == "":
		return "", "", "", domain.Invalid("name", "required")
	case utf8.RuneCountInString(n) > domain.MaxSkillNameRunes:
		return "", "", "", domain.Invalid("name", fmt.Sprintf("at most %d characters", domain.MaxSkillNameRunes))
	case utf8.RuneCountInString(d) > domain.MaxSkillDescRunes:
		return "", "", "", domain.Invalid("description", fmt.Sprintf("at most %d characters", domain.MaxSkillDescRunes))
	case b == "":
		return "", "", "", domain.Invalid("body", "required")
	case len(b) > domain.MaxSkillBodyBytes:
		return "", "", "", domain.Invalid("body", fmt.Sprintf("at most %d KiB", domain.MaxSkillBodyBytes>>10))
	}
	return n, d, b, nil
}

// CreateSkill cria uma skill. Slug repetido na empresa é ErrConflict.
func (s *Service) CreateSkill(ctx context.Context, actor domain.Actor, companyID domain.ID, in CreateSkillInput) (domain.Skill, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.Skill{}, err
	}
	name, desc, body, err := normalizeSkill(in.Name, in.Description, in.Body)
	if err != nil {
		return domain.Skill{}, err
	}
	slug, err := resolveSlug(in.Slug, name)
	if err != nil {
		return domain.Skill{}, err
	}
	if slices.Contains(reservedUISlugs, slug) { // rotas fixas da UI (/skills/nova, /skills/preview)
		return domain.Skill{}, domain.Invalid("slug", fmt.Sprintf("%q is reserved", slug))
	}
	var out domain.Skill
	err = s.withTx(ctx, func(t *txc) error {
		row, err := t.q.CreateSkill(ctx, store.CreateSkillParams{ID: domain.NewID(), CompanyID: companyID, Slug: slug,
			Name: name, Description: desc, Body: body, ActorType: string(actor.Type), ActorID: actor.ID, Now: t.now})
		if err != nil {
			return mapErr(err, "create skill")
		}
		out = skillFromRow(row)
		if err := t.recordSkillVersion(row); err != nil {
			return err
		}
		return t.activity(actor, companyID, "skill.created", "skill", row.ID,
			map[string]any{"slug": row.Slug, "name": row.Name, "version": row.Version}, "")
	})
	return out, err
}

// UpdateSkill muda nome, descrição e/ou corpo; sem mudança real não sobe a versão.
func (s *Service) UpdateSkill(ctx context.Context, actor domain.Actor, companyID, skillID domain.ID, in UpdateSkillInput) (domain.Skill, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.Skill{}, err
	}
	var out domain.Skill
	err := s.withTx(ctx, func(t *txc) error {
		row, _, err := t.updateSkill(actor, companyID, skillID, in)
		if err != nil {
			return err
		}
		out = skillFromRow(row)
		return nil
	})
	return out, err
}

// updateSkill aplica a mudança na transação, grava a versão nova e o activity_event. changed vazio = nada mudou
// (a versão não sobe). Uma skill que veio de fonte passa a "modificada localmente" (ADR-0035).
func (t *txc) updateSkill(actor domain.Actor, companyID, skillID domain.ID, in UpdateSkillInput) (store.Skill, []string, error) {
	ctx := t.ctx
	cur, err := t.q.GetSkillByIDForUpdate(ctx, store.GetSkillByIDForUpdateParams{CompanyID: companyID, ID: skillID})
	if err != nil {
		return store.Skill{}, nil, mapErr(err, "get skill")
	}
	name, desc, body := cur.Name, cur.Description, cur.Body
	if in.Name != nil {
		name = *in.Name
	}
	if in.Description != nil {
		desc = *in.Description
	}
	if in.Body != nil {
		body = *in.Body
	}
	n, d, b, err := normalizeSkill(name, desc, body)
	if err != nil {
		return store.Skill{}, nil, err
	}
	var changed []string
	if n != cur.Name {
		changed = append(changed, "name")
	}
	if d != cur.Description {
		changed = append(changed, "description")
	}
	if b != cur.Body {
		changed = append(changed, "body")
	}
	if len(changed) == 0 {
		return cur, nil, nil
	}
	row, err := t.q.UpdateSkill(ctx, store.UpdateSkillParams{CompanyID: companyID, ID: skillID, Name: n, Description: d,
		Body: b, Version: cur.Version + 1, ActorType: string(actor.Type), ActorID: actor.ID, Now: t.now})
	if err != nil {
		return store.Skill{}, nil, mapErr(err, "update skill")
	}
	if row.SourceID != nil && !row.SourceModified {
		if err := t.q.SetSkillSourceModified(ctx, store.SetSkillSourceModifiedParams{CompanyID: companyID, ID: row.ID, Modified: true}); err != nil {
			return store.Skill{}, nil, fmt.Errorf("mark skill modified: %w", err)
		}
		row.SourceModified = true
	}
	if err := t.recordSkillVersion(row); err != nil {
		return store.Skill{}, nil, err
	}
	if err := t.activity(actor, companyID, "skill.updated", "skill", row.ID,
		map[string]any{"slug": row.Slug, "fields": changed, "version": row.Version}, ""); err != nil {
		return store.Skill{}, nil, err
	}
	return row, changed, nil
}

// DeleteSkill apaga a skill e, em cascata, as ligações com personas.
func (s *Service) DeleteSkill(ctx context.Context, actor domain.Actor, companyID, skillID domain.ID) error {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetSkillByID(ctx, store.GetSkillByIDParams{CompanyID: companyID, ID: skillID})
		if err != nil {
			return mapErr(err, "get skill")
		}
		if cur.SourceID != nil { // a próxima sincronização não recria a skill apagada (ADR-0035)
			if err := t.q.ExcludeSkillSourceEntriesOfSkill(ctx, store.ExcludeSkillSourceEntriesOfSkillParams{CompanyID: companyID,
				SkillID: &cur.ID, Now: t.now}); err != nil {
				return fmt.Errorf("exclude source entry: %w", err)
			}
		}
		if _, err := t.q.DeleteSkill(ctx, store.DeleteSkillParams{CompanyID: companyID, ID: skillID}); err != nil {
			return fmt.Errorf("delete skill: %w", err)
		}
		return t.activity(actor, companyID, "skill.deleted", "skill", skillID, map[string]any{"slug": cur.Slug, "name": cur.Name}, "")
	})
}

// ListSkills devolve as skills da empresa por nome, com a contagem de personas que as usam.
func (s *Service) ListSkills(ctx context.Context, companyID domain.ID) ([]SkillView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	q := s.read()
	rows, err := q.ListSkills(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}
	counts, err := q.CountSkillPersonas(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("count skill personas: %w", err)
	}
	byID := make(map[uuid.UUID]int, len(counts))
	for _, c := range counts {
		byID[c.SkillID] = int(c.Personas)
	}
	out := make([]SkillView, len(rows))
	for i, r := range rows {
		out[i] = SkillView{Skill: skillFromRow(r), Personas: byID[r.ID]}
	}
	return out, nil
}

// GetSkill resolve ref como UUID ou slug dentro da empresa.
func (s *Service) GetSkill(ctx context.Context, companyID domain.ID, ref string) (domain.Skill, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return domain.Skill{}, err
	}
	q := s.read()
	if id, err := uuid.Parse(ref); err == nil {
		r, err := q.GetSkillByID(ctx, store.GetSkillByIDParams{CompanyID: companyID, ID: id})
		if err != nil {
			return domain.Skill{}, mapErr(err, "get skill")
		}
		return skillFromRow(r), nil
	}
	r, err := q.GetSkillBySlug(ctx, store.GetSkillBySlugParams{CompanyID: companyID, Slug: ref})
	if err != nil {
		return domain.Skill{}, mapErr(err, "get skill")
	}
	return skillFromRow(r), nil
}

// SetPersonaSkill liga ou desliga uma skill para uma persona (admin).
func (s *Service) SetPersonaSkill(ctx context.Context, actor domain.Actor, companyID, personaID, skillID domain.ID, enabled bool) error {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		p, err := t.q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: personaID})
		if err != nil {
			return mapErr(err, "get persona")
		}
		sk, err := t.q.GetSkillByID(ctx, store.GetSkillByIDParams{CompanyID: companyID, ID: skillID})
		if err != nil {
			return mapErr(err, "get skill")
		}
		if err := t.q.SetPersonaSkill(ctx, store.SetPersonaSkillParams{CompanyID: companyID, PersonaID: personaID,
			SkillID: skillID, Enabled: enabled, Now: t.now}); err != nil {
			return fmt.Errorf("set persona skill: %w", err)
		}
		verb := "persona.skill_disabled"
		if enabled {
			verb = "persona.skill_enabled"
		}
		return t.activity(actor, companyID, verb, "persona", p.ID, map[string]any{"skill": sk.Slug, "persona": p.Slug}, "")
	})
}

// ListPersonaSkills devolve todas as skills da empresa com o estado para a persona.
func (s *Service) ListPersonaSkills(ctx context.Context, companyID, personaID domain.ID) ([]domain.PersonaSkill, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	q := s.read()
	if _, err := q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: personaID}); err != nil {
		return nil, mapErr(err, "get persona")
	}
	rows, err := q.ListPersonaSkillLinks(ctx, store.ListPersonaSkillLinksParams{CompanyID: companyID, PersonaID: personaID})
	if err != nil {
		return nil, fmt.Errorf("list persona skills: %w", err)
	}
	out := make([]domain.PersonaSkill, len(rows))
	for i, r := range rows {
		out[i] = domain.PersonaSkill{SkillID: r.SkillID, Slug: r.Slug, Name: r.Name, Description: r.Description, Enabled: r.Enabled,
			Version: int(r.Version), PinnedRevision: intPtr(r.PinnedRevision), FromSource: r.SourceID != nil}
	}
	return out, nil
}

// ListSkillPersonas devolve todas as personas da empresa com o estado para a skill.
func (s *Service) ListSkillPersonas(ctx context.Context, companyID, skillID domain.ID) ([]domain.SkillPersona, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	q := s.read()
	if _, err := q.GetSkillByID(ctx, store.GetSkillByIDParams{CompanyID: companyID, ID: skillID}); err != nil {
		return nil, mapErr(err, "get skill")
	}
	rows, err := q.ListSkillPersonaLinks(ctx, store.ListSkillPersonaLinksParams{CompanyID: companyID, SkillID: skillID})
	if err != nil {
		return nil, fmt.Errorf("list skill personas: %w", err)
	}
	out := make([]domain.SkillPersona, len(rows))
	for i, r := range rows {
		out[i] = domain.SkillPersona{PersonaID: r.PersonaID, Slug: r.Slug, Name: r.Name, Enabled: r.Enabled, PinnedRevision: intPtr(r.PinnedRevision)}
	}
	return out, nil
}

// PersonaSkill devolve uma skill HABILITADA para a persona (a tool hanko_skill_get do agente), com o conteúdo da
// versão fixada quando há uma (ADR-0035). Skill que existe mas não está ligada para ela é ErrNotFound: a persona
// só enxerga o que lhe foi dado.
func (s *Service) PersonaSkill(ctx context.Context, companyID, personaID domain.ID, slug string) (domain.Skill, error) {
	list, err := s.PersonaSkills(ctx, companyID, personaID)
	if err != nil {
		return domain.Skill{}, err
	}
	for _, k := range list {
		if k.Slug == slug {
			return k, nil
		}
	}
	return domain.Skill{}, notFound("get skill")
}

// PersonaSkills lista as skills habilitadas para a persona (índice da tool hanko_skill_list e o que a run
// recebe), com o conteúdo efetivo: o da versão fixada, senão o atual.
func (s *Service) PersonaSkills(ctx context.Context, companyID, personaID domain.ID) ([]domain.Skill, error) {
	rows, err := s.read().ListEnabledPinnedSkillsForPersona(ctx, store.ListEnabledPinnedSkillsForPersonaParams{CompanyID: companyID, PersonaID: personaID})
	if err != nil {
		return nil, fmt.Errorf("list persona skills: %w", err)
	}
	out := make([]domain.Skill, len(rows))
	for i, r := range rows {
		out[i] = domain.Skill{ID: r.ID, CompanyID: r.CompanyID, Slug: r.Slug, Name: r.Name, Description: r.Description,
			Body: r.Body, Version: int(r.Version), CreatedBy: actorOf(r.CreatedByType, r.CreatedByID),
			UpdatedBy: actorOf(r.UpdatedByType, r.UpdatedByID), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, Pinned: r.Pinned}
	}
	return out, nil
}

// FitSkills aplica os tetos por run (quantidade e bytes totais, contando o SKILL.md completo): devolve as
// skills que cabem, em ordem de slug, e os slugs omitidos. Pura; o executor avisa no log da run.
func FitSkills(skills []domain.Skill) (fit []domain.Skill, skipped []string) {
	total := 0
	for _, sk := range skills {
		size := len(sk.MD())
		if len(fit) >= domain.MaxSkillsPerRun || total+size > domain.MaxSkillsBytesPerRun {
			skipped = append(skipped, sk.Slug)
			continue
		}
		total += size
		fit = append(fit, sk)
	}
	return fit, skipped
}
