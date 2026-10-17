package service

import (
	"context"
	"fmt"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
	"github.com/renatobardi/hanko/internal/textdiff"
)

// skillVersionText é o texto comparado entre versões: nome, descrição e corpo.
func skillVersionText(v domain.SkillVersion) string {
	return "# " + v.Name + "\n\n> " + v.Description + "\n\n" + v.Body + "\n"
}

// DiffSkillVersions compara duas versões da skill (de from para to).
func (s *Service) DiffSkillVersions(ctx context.Context, companyID, skillID domain.ID, from, to int) (textdiff.Result, error) {
	a, err := s.GetSkillVersion(ctx, companyID, skillID, from)
	if err != nil {
		return textdiff.Result{}, err
	}
	b, err := s.GetSkillVersion(ctx, companyID, skillID, to)
	if err != nil {
		return textdiff.Result{}, err
	}
	return textdiff.Lines(skillVersionText(a), skillVersionText(b)), nil
}

// Versões imutáveis e versão fixada por persona (ADR-0035). Toda mudança de nome, descrição ou corpo grava uma
// versão na mesma transação; restaurar cria uma versão nova com o conteúdo antigo (nunca reescreve a história).

func intPtr(v *int32) *int {
	if v == nil {
		return nil
	}
	n := int(*v)
	return &n
}

// skillBodySHA é o sha256 (hex) do corpo, o mesmo que a migration usa no backfill; o cálculo é o padrão das revisões
// append-only (domain.RevisionSHA, ADR-0052).
func skillBodySHA(body string) string { return domain.RevisionSHA(body) }

// recordSkillVersion grava a versão corrente da linha (revision = version). Idempotente por (skill, revision).
func (t *txc) recordSkillVersion(row store.Skill) error {
	if err := t.q.InsertSkillVersion(t.ctx, store.InsertSkillVersionParams{ID: domain.NewID(), CompanyID: row.CompanyID,
		SkillID: row.ID, Revision: row.Version, Name: row.Name, Description: row.Description, Body: row.Body,
		Sha256: skillBodySHA(row.Body), SourceCommit: row.SourceCommit, AuthorType: row.UpdatedByType, AuthorID: row.UpdatedByID,
		Now: t.now}); err != nil {
		return fmt.Errorf("insert skill version: %w", err)
	}
	return nil
}

func skillVersionFromRow(r store.SkillVersion) domain.SkillVersion {
	v := domain.SkillVersion{ID: r.ID, CompanyID: r.CompanyID, SkillID: r.SkillID, Revision: int(r.Revision), Name: r.Name,
		Description: r.Description, Body: r.Body, BodyBytes: len(r.Body), SHA256: r.Sha256, Author: actorOf(r.AuthorType, r.AuthorID),
		CreatedAt: r.CreatedAt}
	if r.SourceCommit != nil {
		v.SourceCommit = *r.SourceCommit
	}
	return v
}

// ListSkillVersions devolve as versões da skill, da mais nova para a mais antiga, sem o corpo.
func (s *Service) ListSkillVersions(ctx context.Context, companyID, skillID domain.ID) ([]domain.SkillVersion, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	q := s.read()
	if _, err := q.GetSkillByID(ctx, store.GetSkillByIDParams{CompanyID: companyID, ID: skillID}); err != nil {
		return nil, mapErr(err, "get skill")
	}
	rows, err := q.ListSkillVersions(ctx, store.ListSkillVersionsParams{CompanyID: companyID, SkillID: skillID, Lim: 500})
	if err != nil {
		return nil, fmt.Errorf("list skill versions: %w", err)
	}
	out := make([]domain.SkillVersion, len(rows))
	for i, r := range rows {
		out[i] = domain.SkillVersion{ID: r.ID, CompanyID: r.CompanyID, SkillID: r.SkillID, Revision: int(r.Revision), Name: r.Name,
			Description: r.Description, BodyBytes: int(r.BodyBytes), SHA256: r.Sha256, Author: actorOf(r.AuthorType, r.AuthorID),
			CreatedAt: r.CreatedAt}
		if r.SourceCommit != nil {
			out[i].SourceCommit = *r.SourceCommit
		}
	}
	return out, nil
}

// GetSkillVersion devolve uma versão com o corpo.
func (s *Service) GetSkillVersion(ctx context.Context, companyID, skillID domain.ID, revision int) (domain.SkillVersion, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return domain.SkillVersion{}, err
	}
	r, err := s.read().GetSkillVersion(ctx, store.GetSkillVersionParams{CompanyID: companyID, SkillID: skillID, Revision: i32(revision)})
	if err != nil {
		return domain.SkillVersion{}, mapErr(err, "get skill version")
	}
	return skillVersionFromRow(r), nil
}

// RestoreSkillVersion volta a skill ao conteúdo de uma versão antiga criando uma versão NOVA (admin). Restaurar a
// versão que já é a atual não muda nada.
func (s *Service) RestoreSkillVersion(ctx context.Context, actor domain.Actor, companyID, skillID domain.ID, revision int) (domain.Skill, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.Skill{}, err
	}
	var out domain.Skill
	err := s.withTx(ctx, func(t *txc) error {
		v, err := t.q.GetSkillVersion(t.ctx, store.GetSkillVersionParams{CompanyID: companyID, SkillID: skillID, Revision: i32(revision)})
		if err != nil {
			return mapErr(err, "get skill version")
		}
		row, changed, err := t.updateSkill(actor, companyID, skillID, UpdateSkillInput{Name: &v.Name, Description: &v.Description, Body: &v.Body})
		if err != nil {
			return err
		}
		out = skillFromRow(row)
		if len(changed) == 0 {
			return nil
		}
		return t.activity(actor, companyID, "skill.restored", "skill", skillID,
			map[string]any{"slug": row.Slug, "from_revision": revision, "version": row.Version}, "")
	})
	return out, err
}

// SetPersonaSkillPin fixa (revision ≥ 1) ou solta (nil) a versão de uma skill para uma persona (admin). Fixar
// antes de ligar a skill cria o vínculo desligado.
func (s *Service) SetPersonaSkillPin(ctx context.Context, actor domain.Actor, companyID, personaID, skillID domain.ID, revision *int) error {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		p, err := t.q.GetPersonaByID(t.ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: personaID})
		if err != nil {
			return mapErr(err, "get persona")
		}
		sk, err := t.q.GetSkillByID(t.ctx, store.GetSkillByIDParams{CompanyID: companyID, ID: skillID})
		if err != nil {
			return mapErr(err, "get skill")
		}
		var pin *int32
		if revision != nil {
			if *revision < 1 {
				return domain.Invalid("pinned_revision", "use uma versão existente (1 ou mais) ou remova a fixação")
			}
			if _, err := t.q.GetSkillVersion(t.ctx, store.GetSkillVersionParams{CompanyID: companyID, SkillID: skillID, Revision: i32(*revision)}); err != nil {
				if isNoRows(err) {
					return domain.Invalid("pinned_revision", fmt.Sprintf("a skill não tem a versão %d", *revision))
				}
				return mapErr(err, "get skill version")
			}
			r := i32(*revision)
			pin = &r
		}
		if err := t.q.EnsurePersonaSkill(t.ctx, store.EnsurePersonaSkillParams{CompanyID: companyID, PersonaID: personaID, SkillID: skillID, Now: t.now}); err != nil {
			return fmt.Errorf("ensure persona skill: %w", err)
		}
		if _, err := t.q.SetPersonaSkillPin(t.ctx, store.SetPersonaSkillPinParams{CompanyID: companyID, PersonaID: personaID,
			SkillID: skillID, PinnedRevision: pin, Now: t.now}); err != nil {
			return fmt.Errorf("set persona skill pin: %w", err)
		}
		verb, data := "persona.skill_unpinned", map[string]any{"skill": sk.Slug, "persona": p.Slug}
		if revision != nil {
			verb, data["revision"] = "persona.skill_pinned", *revision
		}
		return t.activity(actor, companyID, verb, "persona", p.ID, data, "")
	})
}
