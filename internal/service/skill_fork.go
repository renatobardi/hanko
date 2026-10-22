package service

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Fork de skill e as revisões que cada run recebeu (ADR-0044). Uma skill de fonte continua editável com a marca
// "modificada" (ADR-0035), mas o caminho recomendado para mexer nela é bifurcar: a cópia local nasce solta da fonte,
// com a história própria, e a sincronização da fonte original deixa de competir com as edições.

// ForkSkillInput controla a bifurcação.
type ForkSkillInput struct {
	Slug string // vazio = <slug>-fork (ou -fork-2...)
	Name string // vazio = o nome da skill original
	// Revision a copiar; 0 = a atual.
	Revision int
	// MovePersonas passa as personas ligadas à skill original para a cópia (a original fica desligada nelas).
	MovePersonas bool
}

// ForkSkill copia uma skill (na revisão pedida) para uma skill local nova, sem vínculo com a fonte. Admin.
func (s *Service) ForkSkill(ctx context.Context, actor domain.Actor, companyID, skillID domain.ID, in ForkSkillInput) (domain.Skill, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.Skill{}, err
	}
	if in.Revision < 0 {
		return domain.Skill{}, domain.Invalid("revision", "deve ser positiva")
	}
	var out domain.Skill
	err := s.withTx(ctx, func(t *txc) error {
		src, err := t.q.GetSkillByIDForUpdate(ctx, store.GetSkillByIDForUpdateParams{CompanyID: companyID, ID: skillID})
		if err != nil {
			return mapErr(err, "get skill")
		}
		name, desc, body, rev := src.Name, src.Description, src.Body, src.Version
		if in.Revision > 0 && in.Revision != int(src.Version) {
			v, err := t.q.GetSkillVersion(ctx, store.GetSkillVersionParams{CompanyID: companyID, SkillID: skillID, Revision: int32(in.Revision)}) //nolint:gosec // revisão positiva do usuário; o banco recusa o que não existe
			if err != nil {
				return mapErr(err, "get skill version")
			}
			name, desc, body, rev = v.Name, v.Description, v.Body, v.Revision
		}
		if n := strings.TrimSpace(in.Name); n != "" {
			if name, err = validateName(n); err != nil {
				return err
			}
		}
		slug, err := s.forkSlug(t, companyID, src.Slug, in.Slug)
		if err != nil {
			return err
		}
		row, err := t.q.CreateSkill(ctx, store.CreateSkillParams{ID: domain.NewID(), CompanyID: companyID, Slug: slug, Name: name,
			Description: desc, Body: body, ActorType: string(actor.Type), ActorID: actor.ID, Now: t.now})
		if err != nil {
			return mapErr(err, "create skill")
		}
		if err := t.q.SetSkillFork(ctx, store.SetSkillForkParams{CompanyID: companyID, ID: row.ID, ForkedFromID: &src.ID, ForkedFromRevision: &rev}); err != nil {
			return fmt.Errorf("set skill fork: %w", err)
		}
		row.ForkedFromID, row.ForkedFromRevision = &src.ID, &rev
		if err := t.recordSkillVersion(row); err != nil {
			return err
		}
		moved := int64(0)
		if in.MovePersonas {
			if moved, err = t.q.MovePersonaSkillLinks(ctx, store.MovePersonaSkillLinksParams{CompanyID: companyID, FromSkillID: src.ID, ToSkillID: row.ID, Now: t.now}); err != nil {
				return fmt.Errorf("move persona skill links: %w", err)
			}
			if err := t.q.DisablePersonaSkillLinks(ctx, store.DisablePersonaSkillLinksParams{CompanyID: companyID, SkillID: src.ID, Now: t.now}); err != nil {
				return fmt.Errorf("disable persona skill links: %w", err)
			}
		}
		out = skillFromRow(row)
		return t.activity(actor, companyID, "skill.forked", "skill", row.ID, map[string]any{"slug": row.Slug, "from": src.Slug,
			"from_revision": rev, "personas_moved": moved}, "")
	})
	return out, err
}

func reservedSkillSlug(slug string) bool { return slices.Contains(reservedUISlugs, slug) }

// forkSlug escolhe o slug da cópia: o pedido, ou <origem>-fork, <origem>-fork-2...
func (s *Service) forkSlug(t *txc, companyID domain.ID, srcSlug, want string) (string, error) {
	if want = strings.TrimSpace(want); want != "" {
		slug, err := resolveSlug(want, want)
		if err != nil {
			return "", err
		}
		if reservedSkillSlug(slug) {
			return "", domain.Invalid("slug", fmt.Sprintf("%q is reserved", slug))
		}
		return slug, nil
	}
	for i := 1; i < 100; i++ {
		cand := srcSlug + "-fork"
		if i > 1 {
			cand += "-" + strconv.Itoa(i)
		}
		if len(cand) > 63 {
			cand = cand[len(cand)-63:]
			cand = strings.TrimLeft(cand, "-")
		}
		if _, err := t.q.GetSkillBySlug(t.ctx, store.GetSkillBySlugParams{CompanyID: companyID, Slug: cand}); err != nil {
			if isNoRows(err) {
				return cand, nil
			}
			return "", fmt.Errorf("check slug: %w", err)
		}
	}
	return "", domain.Invalid("slug", "não achei um slug livre; informe um")
}

// ListSkillForks lista as cópias locais de uma skill.
func (s *Service) ListSkillForks(ctx context.Context, companyID, skillID domain.ID) ([]domain.Skill, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.read().ListSkillForks(ctx, store.ListSkillForksParams{CompanyID: companyID, SkillID: &skillID})
	if err != nil {
		return nil, fmt.Errorf("list skill forks: %w", err)
	}
	out := make([]domain.Skill, len(rows))
	for i, r := range rows {
		out[i] = domain.Skill{ID: r.ID, CompanyID: companyID, Slug: r.Slug, Name: r.Name, Version: int(r.Version)}
	}
	return out, nil
}

// RecordRunSkills grava o retrato das skills que a run recebeu: skill, revisão efetiva (a fixada, senão a atual) e sha256
// do corpo. Chamado pelo executor no despacho; idempotente por (run, slug). Melhor esforço: o executor só avisa se falhar.
func (s *Service) RecordRunSkills(ctx context.Context, companyID, runID domain.ID, skills []domain.Skill) error {
	if len(skills) == 0 {
		return nil
	}
	return s.withTx(ctx, func(t *txc) error {
		for _, k := range skills {
			sid := k.ID
			if err := t.q.InsertRunSkill(ctx, store.InsertRunSkillParams{RunID: runID, CompanyID: companyID, SkillID: &sid, Slug: k.Slug,
				Revision: int32(k.Version), Sha256: skillBodySHA(k.Body), Pinned: k.Pinned}); err != nil { //nolint:gosec // a versão da skill é pequena e positiva
				return fmt.Errorf("insert run skill: %w", err)
			}
		}
		return nil
	})
}

// RunSkill é uma skill que uma run recebeu.
type RunSkill struct {
	Slug     string `json:"slug"`
	Revision int    `json:"revision"`
	SHA256   string `json:"sha256"`
	Pinned   bool   `json:"pinned"`
}

// ListRunSkills devolve as skills (e revisões) que a run recebeu.
func (s *Service) ListRunSkills(ctx context.Context, companyID, runID domain.ID) ([]RunSkill, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.read().ListRunSkills(ctx, store.ListRunSkillsParams{CompanyID: companyID, RunID: runID})
	if err != nil {
		return nil, fmt.Errorf("list run skills: %w", err)
	}
	out := make([]RunSkill, len(rows))
	for i, r := range rows {
		out[i] = RunSkill{Slug: r.Slug, Revision: int(r.Revision), SHA256: r.Sha256, Pinned: r.Pinned}
	}
	return out, nil
}
