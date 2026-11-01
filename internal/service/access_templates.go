package service

import (
	"context"
	"fmt"
	"slices"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Modelos de acesso do assistente Conectar (ADR-0089): dado por empresa, cópia no uso. O admin cria e edita;
// leitura é de qualquer membro. "Personalizado" não é linha: o assistente abre o editor de perfil existente.

const maxTemplateDescRunes = 300

// AccessTemplateInput é a escrita de um modelo (create e update).
type AccessTemplateInput struct {
	Slug            string
	Name            string
	Description     string
	ReadMode        domain.ToolPolicyMode
	WriteMode       domain.ToolPolicyMode
	DestructiveMode domain.ToolPolicyMode
	Suggested       bool
}

// templateFromRow converte a linha do banco para o domínio.
func templateFromRow(r store.AccessTemplate) domain.AccessTemplate {
	return domain.AccessTemplate{ID: r.ID, CompanyID: r.CompanyID, Slug: r.Slug, Name: r.Name, Description: r.Description,
		ReadMode: domain.ToolPolicyMode(r.ReadMode), WriteMode: domain.ToolPolicyMode(r.WriteMode),
		DestructiveMode: domain.ToolPolicyMode(r.DestructiveMode), Suggested: r.Suggested,
		CreatedBy: actorOf(r.CreatedByType, r.CreatedByID), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

// validateTemplate valida nome, descrição e os três modos; devolve os campos normalizados.
func validateTemplate(in AccessTemplateInput) (name, desc string, err error) {
	name, derr := validateName(in.Name)
	if derr != nil {
		return "", "", derr
	}
	desc = trimSpace(in.Description)
	if utf8.RuneCountInString(desc) > maxTemplateDescRunes {
		return "", "", domain.Invalid("description", fmt.Sprintf("at most %d characters", maxTemplateDescRunes))
	}
	for field, m := range map[string]domain.ToolPolicyMode{
		"read_mode": in.ReadMode, "write_mode": in.WriteMode, "destructive_mode": in.DestructiveMode,
	} {
		if !m.Valid() {
			return "", "", domain.Invalid(field, "must be allow, ask or off")
		}
	}
	return name, desc, nil
}

// resolveTemplateRef resolve um modelo por id ou slug.
func resolveTemplateRef(ctx context.Context, q *store.Queries, companyID domain.ID, ref string) (store.AccessTemplate, error) {
	var r store.AccessTemplate
	var err error
	if id, perr := uuid.Parse(ref); perr == nil {
		r, err = q.GetAccessTemplateByID(ctx, store.GetAccessTemplateByIDParams{CompanyID: companyID, ID: id})
	} else {
		r, err = q.GetAccessTemplateBySlug(ctx, store.GetAccessTemplateBySlugParams{CompanyID: companyID, Slug: ref})
	}
	if err != nil {
		return store.AccessTemplate{}, mapErr(err, "get access template")
	}
	return r, nil
}

// CreateAccessTemplate cria um modelo (admin).
func (s *Service) CreateAccessTemplate(ctx context.Context, actor domain.Actor, companyID domain.ID, in AccessTemplateInput) (domain.AccessTemplate, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.AccessTemplate{}, err
	}
	name, desc, err := validateTemplate(in)
	if err != nil {
		return domain.AccessTemplate{}, err
	}
	slug, err := resolveSlug(in.Slug, name)
	if err != nil {
		return domain.AccessTemplate{}, err
	}
	if slices.Contains([]string{"personalizado"}, slug) {
		return domain.AccessTemplate{}, domain.Invalid("slug", fmt.Sprintf("%q is reserved", slug))
	}
	var out domain.AccessTemplate
	err = s.withTx(ctx, func(t *txc) error {
		n, err := t.q.CountAccessTemplates(ctx, companyID)
		if err != nil {
			return fmt.Errorf("count access templates: %w", err)
		}
		if n >= domain.MaxAccessTemplates {
			return domain.Invalid("templates", fmt.Sprintf("no máximo %d modelos por empresa", domain.MaxAccessTemplates))
		}
		row, err := t.insertAccessTemplate(ctx, companyID, slug, name, desc, in, t.systemActor())
		if err != nil {
			return err
		}
		if in.Suggested {
			if err := t.q.ClearSuggestedAccessTemplate(ctx, store.ClearSuggestedAccessTemplateParams{CompanyID: companyID, ID: row.ID, Now: t.now}); err != nil {
				return fmt.Errorf("clear suggested: %w", err)
			}
		}
		out = templateFromRow(row)
		return t.activity(actor, companyID, "access_template.created", "access_template", row.ID,
			templateActivity(row), "")
	})
	return out, err
}

// insertAccessTemplate insere a linha; erro único de slug vira conflito legível.
func (t *txc) insertAccessTemplate(ctx context.Context, companyID domain.ID, slug, name, desc string, in AccessTemplateInput, creator domain.Actor) (store.AccessTemplate, error) {
	row, err := t.q.CreateAccessTemplate(ctx, store.CreateAccessTemplateParams{
		ID: domain.NewID(), CompanyID: companyID, Slug: slug, Name: name, Description: desc,
		ReadMode: string(in.ReadMode), WriteMode: string(in.WriteMode), DestructiveMode: string(in.DestructiveMode),
		Suggested: in.Suggested, ActorType: string(creator.Type), ActorID: creator.ID, Now: t.now})
	if err != nil {
		return store.AccessTemplate{}, mapErr(err, "create access template")
	}
	return row, nil
}

// systemActor é o ator das linhas semeadas (mesma regra das coleções padrão do cofre).
func (t *txc) systemActor() domain.Actor { return domain.SystemActor }

func templateActivity(r store.AccessTemplate) map[string]any {
	return map[string]any{"slug": r.Slug, "name": r.Name, "read_mode": r.ReadMode, "write_mode": r.WriteMode,
		"destructive_mode": r.DestructiveMode, "suggested": r.Suggested}
}

// seedAccessTemplates semeia os dois modelos padrão na criação da empresa, na mesma transação (ADR-0089: nunca
// nasce empresa sem os modelos básicos).
func (t *txc) seedAccessTemplates(ctx context.Context, companyID domain.ID) error {
	for _, in := range []AccessTemplateInput{
		{Slug: "somente-leitura", Name: "Somente leitura", Description: "Ler sem restrição; escrever e apagar ficam fora do perfil.",
			ReadMode: domain.PolicyAllow, WriteMode: domain.PolicyOff, DestructiveMode: domain.PolicyOff},
		{Slug: "leitura-livre-escrita-com-aprovacao", Name: "Leitura livre, escrita com aprovação",
			Description:     "Ler sem restrição; escrever e apagar passam por aprovação.",
			ReadMode:        domain.PolicyAllow,
			WriteMode:       domain.PolicyAsk,
			DestructiveMode: domain.PolicyAsk,
			Suggested:       true},
	} {
		row, err := t.insertAccessTemplate(ctx, companyID, in.Slug, in.Name, in.Description, in, t.systemActor())
		if err != nil {
			return err
		}
		if err := t.activity(t.systemActor(), companyID, "access_template.seeded", "access_template", row.ID, templateActivity(row), ""); err != nil {
			return err
		}
	}
	return nil
}

// GetAccessTemplate devolve um modelo (por id ou slug).
func (s *Service) GetAccessTemplate(ctx context.Context, companyID domain.ID, ref string) (domain.AccessTemplate, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return domain.AccessTemplate{}, err
	}
	row, err := resolveTemplateRef(ctx, s.read(), companyID, ref)
	if err != nil {
		return domain.AccessTemplate{}, err
	}
	return templateFromRow(row), nil
}

// ListAccessTemplates lista os modelos da empresa (sugerido primeiro).
func (s *Service) ListAccessTemplates(ctx context.Context, companyID domain.ID) ([]domain.AccessTemplate, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.read().ListAccessTemplates(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list access templates: %w", err)
	}
	out := make([]domain.AccessTemplate, len(rows))
	for i, r := range rows {
		out[i] = templateFromRow(r)
	}
	return out, nil
}

// UpdateAccessTemplate edita um modelo (admin). Cópia no uso: conexões concluídas antes continuam iguais.
func (s *Service) UpdateAccessTemplate(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string, in AccessTemplateInput) (domain.AccessTemplate, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.AccessTemplate{}, err
	}
	name, desc, err := validateTemplate(in)
	if err != nil {
		return domain.AccessTemplate{}, err
	}
	var out domain.AccessTemplate
	err = s.withTx(ctx, func(t *txc) error {
		row, err := resolveTemplateRef(ctx, t.q, companyID, ref)
		if err != nil {
			return err
		}
		updated, err := t.q.UpdateAccessTemplate(ctx, store.UpdateAccessTemplateParams{
			CompanyID: companyID, ID: row.ID, Name: name, Description: desc,
			ReadMode: string(in.ReadMode), WriteMode: string(in.WriteMode), DestructiveMode: string(in.DestructiveMode),
			Suggested: in.Suggested, Now: t.now})
		if err != nil {
			return mapErr(err, "update access template")
		}
		if in.Suggested {
			if err := t.q.ClearSuggestedAccessTemplate(ctx, store.ClearSuggestedAccessTemplateParams{CompanyID: companyID, ID: row.ID, Now: t.now}); err != nil {
				return fmt.Errorf("clear suggested: %w", err)
			}
		}
		out = templateFromRow(updated)
		return t.activity(actor, companyID, "access_template.updated", "access_template", row.ID, templateActivity(updated), "")
	})
	return out, err
}

// DeleteAccessTemplate apaga um modelo (admin). Cópia no uso: conexões concluídas antes continuam iguais.
func (s *Service) DeleteAccessTemplate(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string) error {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		row, err := resolveTemplateRef(ctx, t.q, companyID, ref)
		if err != nil {
			return err
		}
		n, err := t.q.DeleteAccessTemplate(ctx, store.DeleteAccessTemplateParams{CompanyID: companyID, ID: row.ID})
		if err != nil {
			return mapErr(err, "delete access template")
		}
		if n == 0 {
			return notFound("access template")
		}
		return t.activity(actor, companyID, "access_template.deleted", "access_template", row.ID,
			map[string]any{"slug": row.Slug, "name": row.Name}, "")
	})
}
