package service

import (
	"context"
	"fmt"

	"github.com/renatobardi/hanko/internal/domain"
)

// Edição em lote das tags (ADR-0087 item 5, fatia 2): aplicar ou tirar uma tag de várias personas da lista. Usa as
// mesmas regras e os mesmos eventos de uma persona por vez, com um evento por persona alterada. Tudo numa transação: se
// uma persona não pode receber a tag (teto de 20, persona que não é da empresa), nenhuma recebe.

// MaxBulkPersonas é quantas personas um lote aceita.
const MaxBulkPersonas = 500

// BulkTagResult é a tag do lote e quantas personas mudaram (as que já tinham, ou não tinham, a tag não contam).
type BulkTagResult struct {
	Tag     domain.PersonaTag
	Changed int
}

// bulkIDs tira repetidas e confere o tamanho do lote.
func bulkIDs(ids []domain.ID) ([]domain.ID, error) {
	seen := make(map[domain.ID]bool, len(ids))
	out := make([]domain.ID, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	if len(out) == 0 {
		return nil, domain.Invalid("personas", "select at least one persona")
	}
	if len(out) > MaxBulkPersonas {
		return nil, domain.Invalid("personas", fmt.Sprintf("too many personas in one batch (max %d)", MaxBulkPersonas))
	}
	return out, nil
}

// BulkTagPersonas aplica a tag de nome dado a todas as personas, criando a tag se ela não existe (member). Tag arquivada
// não se aplica.
func (s *Service) BulkTagPersonas(ctx context.Context, actor domain.Actor, companyID domain.ID, personaIDs []domain.ID, in PersonaTagInput) (BulkTagResult, error) {
	if err := s.requireTagWrite(ctx, actor, companyID, domain.MemberMember); err != nil {
		return BulkTagResult{}, err
	}
	ids, err := bulkIDs(personaIDs)
	if err != nil {
		return BulkTagResult{}, err
	}
	if in, err = normalizeTagInput(in); err != nil {
		return BulkTagResult{}, err
	}
	var res BulkTagResult
	err = s.withTx(ctx, func(t *txc) error {
		if res.Tag, err = t.tagByNameOrCreate(actor, companyID, in); err != nil {
			return err
		}
		for _, id := range ids {
			added, err := t.tagPersona(actor, companyID, id, res.Tag)
			if err != nil {
				return err
			}
			if added {
				res.Changed++
			}
		}
		return nil
	})
	return res, err
}

// BulkUntagPersonas tira a tag de todas as personas (member; vale para tag arquivada).
func (s *Service) BulkUntagPersonas(ctx context.Context, actor domain.Actor, companyID domain.ID, personaIDs []domain.ID, tagID domain.ID) (BulkTagResult, error) {
	if err := s.requireTagWrite(ctx, actor, companyID, domain.MemberMember); err != nil {
		return BulkTagResult{}, err
	}
	ids, err := bulkIDs(personaIDs)
	if err != nil {
		return BulkTagResult{}, err
	}
	var res BulkTagResult
	err = s.withTx(ctx, func(t *txc) error {
		row, err := t.lockedTag(companyID, tagID)
		if err != nil {
			return err
		}
		res.Tag = personaTagFromRow(row)
		for _, id := range ids {
			removed, err := t.untagPersona(actor, companyID, id, res.Tag)
			if err != nil {
				return err
			}
			if removed {
				res.Changed++
			}
		}
		return nil
	})
	return res, err
}
