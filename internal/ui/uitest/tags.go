package uitest

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Tags de persona (ADR-0087) no backend falso: cadastro em PersonaTags e ligações em Personas[i].Tags. As regras (papéis,
// tetos, unicidade) são do service; aqui só o necessário para as telas.

func (f *Fake) tagView(t domain.PersonaTag) service.PersonaTagView {
	n := 0
	for _, p := range f.Personas {
		if slices.ContainsFunc(p.Tags, func(x domain.PersonaTag) bool { return x.ID == t.ID }) {
			n++
		}
	}
	return service.PersonaTagView{PersonaTag: t, PersonaCount: n}
}

func (f *Fake) tagIndex(id domain.ID) int {
	return slices.IndexFunc(f.PersonaTags, func(t domain.PersonaTag) bool { return t.ID == id })
}

// syncTag reescreve a tag em todas as personas que a têm (renomear, arquivar).
func (f *Fake) syncTag(t domain.PersonaTag) {
	for i := range f.Personas {
		for j := range f.Personas[i].Tags {
			if f.Personas[i].Tags[j].ID == t.ID {
				f.Personas[i].Tags[j] = t
			}
		}
	}
}

func (f *Fake) ListPersonaTags(_ context.Context, _ domain.ID, includeArchived bool) ([]service.PersonaTagView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ListPersonaTags"); err != nil {
		return nil, err
	}
	var out []service.PersonaTagView
	for _, t := range f.PersonaTags {
		if includeArchived || !t.Archived() {
			out = append(out, f.tagView(t))
		}
	}
	return out, nil
}

func (f *Fake) CreatePersonaTag(_ context.Context, _ domain.Actor, companyID domain.ID, in service.PersonaTagInput) (service.PersonaTagView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("CreatePersonaTag")
	if err := f.fail("CreatePersonaTag"); err != nil {
		return service.PersonaTagView{}, err
	}
	t, err := f.newTag(companyID, in)
	return f.tagView(t), err
}

func (f *Fake) newTag(companyID domain.ID, in service.PersonaTagInput) (domain.PersonaTag, error) {
	name, err := domain.NormalizePersonaTagName(in.Name)
	if err != nil {
		return domain.PersonaTag{}, err
	}
	if slices.ContainsFunc(f.PersonaTags, func(t domain.PersonaTag) bool { return domain.PersonaTagKey(t.Name) == domain.PersonaTagKey(name) }) {
		return domain.PersonaTag{}, domain.ErrConflict
	}
	color := in.Color
	if color == "" {
		color = domain.TagGray
	}
	now := time.Now()
	t := domain.PersonaTag{ID: domain.NewID(), CompanyID: companyID, Name: name, Color: color, Description: in.Description, CreatedAt: now, UpdatedAt: now}
	f.PersonaTags = append(f.PersonaTags, t)
	return t, nil
}

func (f *Fake) UpdatePersonaTag(_ context.Context, _ domain.Actor, _, tagID domain.ID, in service.UpdatePersonaTagInput) (service.PersonaTagView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("UpdatePersonaTag")
	if err := f.fail("UpdatePersonaTag"); err != nil {
		return service.PersonaTagView{}, err
	}
	i := f.tagIndex(tagID)
	if i < 0 {
		return service.PersonaTagView{}, domain.ErrNotFound
	}
	t := &f.PersonaTags[i]
	if in.Name != nil {
		t.Name = strings.TrimSpace(*in.Name)
	}
	if in.Color != nil {
		t.Color = *in.Color
	}
	if in.Description != nil {
		t.Description = *in.Description
	}
	f.syncTag(*t)
	return f.tagView(*t), nil
}

func (f *Fake) setArchived(tagID domain.ID, archive bool) (service.PersonaTagView, error) {
	i := f.tagIndex(tagID)
	if i < 0 {
		return service.PersonaTagView{}, domain.ErrNotFound
	}
	t := &f.PersonaTags[i]
	t.ArchivedAt = nil
	if archive {
		now := time.Now()
		t.ArchivedAt = &now
	}
	f.syncTag(*t)
	return f.tagView(*t), nil
}

func (f *Fake) ArchivePersonaTag(_ context.Context, _ domain.Actor, _, tagID domain.ID) (service.PersonaTagView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("ArchivePersonaTag")
	return f.setArchived(tagID, true)
}

func (f *Fake) UnarchivePersonaTag(_ context.Context, _ domain.Actor, _, tagID domain.ID) (service.PersonaTagView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("UnarchivePersonaTag")
	return f.setArchived(tagID, false)
}

func (f *Fake) DeletePersonaTag(_ context.Context, _ domain.Actor, _, tagID domain.ID) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("DeletePersonaTag")
	i := f.tagIndex(tagID)
	if i < 0 {
		return 0, domain.ErrNotFound
	}
	n := f.tagView(f.PersonaTags[i]).PersonaCount
	f.PersonaTags = slices.Delete(f.PersonaTags, i, i+1)
	for j := range f.Personas {
		f.Personas[j].Tags = slices.DeleteFunc(f.Personas[j].Tags, func(t domain.PersonaTag) bool { return t.ID == tagID })
	}
	return n, nil
}

func (f *Fake) UntagPersona(_ context.Context, _ domain.Actor, _, personaID, tagID domain.ID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("UntagPersona")
	for i := range f.Personas {
		if f.Personas[i].ID == personaID {
			f.Personas[i].Tags = slices.DeleteFunc(f.Personas[i].Tags, func(t domain.PersonaTag) bool { return t.ID == tagID })
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *Fake) CreateAndTagPersona(_ context.Context, _ domain.Actor, companyID, personaID domain.ID, in service.PersonaTagInput) (domain.PersonaTag, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("CreateAndTagPersona")
	if err := f.fail("CreateAndTagPersona"); err != nil {
		return domain.PersonaTag{}, err
	}
	pi := slices.IndexFunc(f.Personas, func(p domain.Persona) bool { return p.ID == personaID })
	if pi < 0 {
		return domain.PersonaTag{}, domain.ErrNotFound
	}
	ti := slices.IndexFunc(f.PersonaTags, func(t domain.PersonaTag) bool { return domain.PersonaTagKey(t.Name) == domain.PersonaTagKey(in.Name) })
	var t domain.PersonaTag
	if ti >= 0 {
		t = f.PersonaTags[ti]
		if t.Archived() {
			return t, domain.ErrConflict
		}
	} else {
		var err error
		if t, err = f.newTag(companyID, in); err != nil {
			return t, err
		}
	}
	if !slices.ContainsFunc(f.Personas[pi].Tags, func(x domain.PersonaTag) bool { return x.ID == t.ID }) {
		f.Personas[pi].Tags = append(f.Personas[pi].Tags, t)
	}
	return t, nil
}

func (f *Fake) BulkTagPersonas(ctx context.Context, actor domain.Actor, companyID domain.ID, personaIDs []domain.ID, in service.PersonaTagInput) (service.BulkTagResult, error) {
	f.mu.Lock()
	f.note("BulkTagPersonas")
	err := f.fail("BulkTagPersonas")
	f.mu.Unlock()
	if err != nil {
		return service.BulkTagResult{}, err
	}
	if len(personaIDs) == 0 {
		return service.BulkTagResult{}, domain.Invalid("personas", "select at least one persona")
	}
	var res service.BulkTagResult
	for _, id := range personaIDs {
		f.mu.Lock()
		pi := slices.IndexFunc(f.Personas, func(p domain.Persona) bool { return p.ID == id })
		had := pi >= 0 && slices.ContainsFunc(f.Personas[pi].Tags, func(t domain.PersonaTag) bool { return domain.PersonaTagKey(t.Name) == domain.PersonaTagKey(in.Name) })
		f.mu.Unlock()
		t, err := f.CreateAndTagPersona(ctx, actor, companyID, id, in)
		if err != nil {
			return res, err
		}
		res.Tag = t
		if !had {
			res.Changed++
		}
	}
	return res, nil
}

func (f *Fake) BulkUntagPersonas(_ context.Context, _ domain.Actor, _ domain.ID, personaIDs []domain.ID, tagID domain.ID) (service.BulkTagResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("BulkUntagPersonas")
	i := f.tagIndex(tagID)
	if i < 0 {
		return service.BulkTagResult{}, domain.ErrNotFound
	}
	res := service.BulkTagResult{Tag: f.PersonaTags[i]}
	for j := range f.Personas {
		if !slices.Contains(personaIDs, f.Personas[j].ID) {
			continue
		}
		before := len(f.Personas[j].Tags)
		f.Personas[j].Tags = slices.DeleteFunc(f.Personas[j].Tags, func(t domain.PersonaTag) bool { return t.ID == tagID })
		if len(f.Personas[j].Tags) < before {
			res.Changed++
		}
	}
	return res, nil
}
