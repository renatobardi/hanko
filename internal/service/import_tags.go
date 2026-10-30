package service

import (
	"fmt"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/export"
	"github.com/renatobardi/hanko/internal/store"
)

// Tags de persona no import (ADR-0087 item 11; ADR-0022). O arquivo traz o cadastro e, em cada persona, os nomes das
// tags. Uma tag com o mesmo nome, sem distinguir caixa, de uma tag que já existe na empresa passa a ser essa tag.

// planPersonaTags valida o cadastro e as ligações do arquivo sem tocar no banco.
func planPersonaTags(doc export.Document) error {
	if len(doc.PersonaTags) > domain.MaxPersonaTagsPerCompany {
		return domain.Invalid("persona_tags", fmt.Sprintf("too many tags (max %d)", domain.MaxPersonaTagsPerCompany))
	}
	known := map[string]bool{}
	for i, x := range doc.PersonaTags {
		if _, err := normalizeTagInput(importTagInput(x)); err != nil {
			return domain.Invalid(iv("persona_tags", i, "name"), invalidReason(err))
		}
		k := domain.PersonaTagKey(x.Name)
		if known[k] {
			return domain.Invalid(iv("persona_tags", i, "name"), fmt.Sprintf("duplicate %q", x.Name))
		}
		known[k] = true
	}
	for i, p := range doc.Personas {
		if len(p.Tags) > domain.MaxTagsPerPersona {
			return domain.Invalid(iv("personas", i, "tags"), fmt.Sprintf("too many tags (max %d)", domain.MaxTagsPerPersona))
		}
		for _, n := range p.Tags {
			if !known[domain.PersonaTagKey(n)] {
				return domain.Invalid(iv("personas", i, "tags"), fmt.Sprintf("unknown tag %q (not in persona_tags)", n))
			}
		}
	}
	return nil
}

func importTagInput(x export.PersonaTag) PersonaTagInput {
	return PersonaTagInput{Name: x.Name, Color: domain.PersonaTagColor(x.Color), Description: x.Description}
}

// importPersonaTags cria (ou reaproveita pelo nome) as tags do arquivo e liga as das personas em link. Recalcula a busca
// das personas ligadas. Roda na transação do import.
func (t *txc) importPersonaTags(cid domain.ID, doc export.Document, personas map[string]domain.ID, link func(slug string) bool) error {
	if len(doc.PersonaTags) == 0 {
		return nil
	}
	if err := t.q.LockPersonaTags(t.ctx, cid.String()); err != nil {
		return fmt.Errorf("lock persona tags: %w", err)
	}
	byKey := map[string]domain.ID{}
	for _, x := range doc.PersonaTags {
		id, err := t.importPersonaTag(cid, x)
		if err != nil {
			return err
		}
		byKey[domain.PersonaTagKey(x.Name)] = id
	}
	var touched []domain.ID
	for _, p := range doc.Personas {
		if len(p.Tags) == 0 || !link(p.Slug) {
			continue
		}
		if err := t.linkImportedTags(cid, personas[p.Slug], p, byKey); err != nil {
			return err
		}
		touched = append(touched, personas[p.Slug])
	}
	return t.refreshTagNames(cid, touched)
}

// linkImportedTags liga as tags do arquivo à persona e confere o teto de 20 com as que ela já tinha.
func (t *txc) linkImportedTags(cid, pid domain.ID, p export.Persona, byKey map[string]domain.ID) error {
	for _, n := range p.Tags {
		if _, err := t.q.InsertPersonaTagLink(t.ctx, store.InsertPersonaTagLinkParams{CompanyID: cid, PersonaID: pid,
			TagID: byKey[domain.PersonaTagKey(n)], Now: t.now}); err != nil {
			return fmt.Errorf("import persona tag link: %w", err)
		}
	}
	n, err := t.q.CountTagsOfPersona(t.ctx, store.CountTagsOfPersonaParams{CompanyID: cid, PersonaID: pid})
	if err != nil {
		return fmt.Errorf("count persona tags: %w", err)
	}
	if n > domain.MaxTagsPerPersona {
		return domain.Invalid("personas.tags", fmt.Sprintf("persona %q would have %d tags (max %d)", p.Slug, n, domain.MaxTagsPerPersona))
	}
	return nil
}

// importPersonaTag devolve a tag da empresa com o nome dado, criando-a se não existe.
func (t *txc) importPersonaTag(cid domain.ID, x export.PersonaTag) (domain.ID, error) {
	in, err := normalizeTagInput(importTagInput(x))
	if err != nil {
		return domain.ID{}, err
	}
	cur, err := t.q.GetPersonaTagByName(t.ctx, store.GetPersonaTagByNameParams{CompanyID: cid, Name: in.Name})
	if err == nil {
		return cur.ID, nil
	}
	if !isNoRows(err) {
		return domain.ID{}, fmt.Errorf("get persona tag by name: %w", err)
	}
	n, err := t.q.CountPersonaTags(t.ctx, cid)
	if err != nil {
		return domain.ID{}, fmt.Errorf("count persona tags: %w", err)
	}
	if n >= domain.MaxPersonaTagsPerCompany {
		return domain.ID{}, domain.Invalid("persona_tags", fmt.Sprintf("the company would have more than %d tags", domain.MaxPersonaTagsPerCompany))
	}
	var archived *time.Time
	if x.Archived {
		archived = &t.now
	}
	created := x.CreatedAt
	if created.IsZero() {
		created = t.now
	}
	r, err := t.q.CreatePersonaTag(t.ctx, store.CreatePersonaTagParams{ID: domain.NewID(), CompanyID: cid, Name: in.Name,
		Color: string(in.Color), Description: in.Description, ArchivedAt: archived, Now: created})
	if err != nil {
		return domain.ID{}, tagNameErr(err, in.Name)
	}
	return r.ID, nil
}
