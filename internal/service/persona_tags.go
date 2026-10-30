package service

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Tags de persona (ADR-0087). Papéis (item 4): viewer lê, member cria, edita, aplica e tira, admin arquiva, desarquiva e
// deleta. Nenhuma persona classifica (item 9): o agente só lê. Toda escrita trava as tags da empresa (LockPersonaTags),
// grava o activity_event e recalcula `personas.tag_names` na mesma transação (itens 10 e 12).

const (
	subjectPersonaTag = "persona_tag"
	whatPersonaTag    = "persona tag" // rótulo dos erros de "não encontrada"
)

// PersonaTagView é a tag com a contagem de personas que a têm.
type PersonaTagView struct {
	domain.PersonaTag
	PersonaCount int
}

// PersonaTagInput cria uma tag. Cor vazia vale gray.
type PersonaTagInput struct {
	Name        string
	Color       domain.PersonaTagColor
	Description string
}

// UpdatePersonaTagInput edita uma tag; campo nil não muda.
type UpdatePersonaTagInput struct {
	Name        *string
	Color       *domain.PersonaTagColor
	Description *string
}

func personaTagFromRow(r store.PersonaTag) domain.PersonaTag {
	return domain.PersonaTag{ID: r.ID, CompanyID: r.CompanyID, Name: r.Name, Color: domain.PersonaTagColor(r.Color),
		Description: r.Description, ArchivedAt: r.ArchivedAt, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

// noPersonaWrites recusa a escrita de tag por uma persona: classificar cabe ao humano (ADR-0087 item 9).
func noPersonaWrites(actor domain.Actor) error {
	if actor.Type == domain.ActorPersona {
		return fmt.Errorf("personas não criam nem aplicam tags: %w", domain.ErrForbidden)
	}
	return nil
}

func (s *Service) requireTagWrite(ctx context.Context, actor domain.Actor, companyID domain.ID, role domain.MemberRole) error {
	if err := noPersonaWrites(actor); err != nil {
		return err
	}
	return s.require(ctx, companyID, role)
}

func normalizeTagInput(in PersonaTagInput) (PersonaTagInput, error) {
	name, err := domain.NormalizePersonaTagName(in.Name)
	if err != nil {
		return in, err
	}
	in.Name = name
	if in.Color == "" {
		in.Color = domain.TagGray
	}
	if !in.Color.Valid() {
		return in, domain.Invalid("color", fmt.Sprintf("unknown color %q (want one of %v)", in.Color, domain.PersonaTagColors))
	}
	return in, domain.ValidPersonaTagDescription(in.Description)
}

// ListPersonaTags lista o cadastro, sem as arquivadas a menos que includeArchived.
func (s *Service) ListPersonaTags(ctx context.Context, companyID domain.ID, includeArchived bool) ([]PersonaTagView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.read().ListPersonaTagsWithCount(ctx, store.ListPersonaTagsWithCountParams{CompanyID: companyID, IncludeArchived: includeArchived})
	if err != nil {
		return nil, fmt.Errorf("list persona tags: %w", err)
	}
	out := make([]PersonaTagView, len(rows))
	for i, r := range rows {
		out[i] = PersonaTagView{PersonaTag: personaTagFromRow(r.PersonaTag), PersonaCount: int(r.PersonaCount)}
	}
	return out, nil
}

// GetPersonaTag devolve uma tag com a contagem de personas, que a confirmação de deletar mostra.
func (s *Service) GetPersonaTag(ctx context.Context, companyID, tagID domain.ID) (PersonaTagView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return PersonaTagView{}, err
	}
	return tagView(ctx, s.read(), companyID, tagID)
}

func tagView(ctx context.Context, q *store.Queries, companyID, tagID domain.ID) (PersonaTagView, error) {
	r, err := q.GetPersonaTag(ctx, store.GetPersonaTagParams{CompanyID: companyID, ID: tagID})
	if err != nil {
		return PersonaTagView{}, mapErr(err, whatPersonaTag)
	}
	n, err := q.CountPersonaTagLinks(ctx, store.CountPersonaTagLinksParams{CompanyID: companyID, TagID: tagID})
	if err != nil {
		return PersonaTagView{}, fmt.Errorf("count tag links: %w", err)
	}
	return PersonaTagView{PersonaTag: personaTagFromRow(r), PersonaCount: int(n)}, nil
}

// CreatePersonaTag cria uma tag no cadastro (member; até 200 por empresa, contando as arquivadas).
func (s *Service) CreatePersonaTag(ctx context.Context, actor domain.Actor, companyID domain.ID, in PersonaTagInput) (PersonaTagView, error) {
	if err := s.requireTagWrite(ctx, actor, companyID, domain.MemberMember); err != nil {
		return PersonaTagView{}, err
	}
	in, err := normalizeTagInput(in)
	if err != nil {
		return PersonaTagView{}, err
	}
	var out domain.PersonaTag
	err = s.withTx(ctx, func(t *txc) error {
		out, err = t.createPersonaTag(actor, companyID, in)
		return err
	})
	return PersonaTagView{PersonaTag: out}, err
}

// createPersonaTag insere a tag já normalizada e grava persona_tag.created. Roda na transação de quem chama.
func (t *txc) createPersonaTag(actor domain.Actor, companyID domain.ID, in PersonaTagInput) (domain.PersonaTag, error) {
	if err := t.q.LockPersonaTags(t.ctx, companyID.String()); err != nil {
		return domain.PersonaTag{}, fmt.Errorf("lock persona tags: %w", err)
	}
	n, err := t.q.CountPersonaTags(t.ctx, companyID)
	if err != nil {
		return domain.PersonaTag{}, fmt.Errorf("count persona tags: %w", err)
	}
	if n >= domain.MaxPersonaTagsPerCompany {
		return domain.PersonaTag{}, domain.Invalid("name", fmt.Sprintf("too many tags in the company (max %d, archived included)", domain.MaxPersonaTagsPerCompany))
	}
	r, err := t.q.CreatePersonaTag(t.ctx, store.CreatePersonaTagParams{ID: domain.NewID(), CompanyID: companyID, Name: in.Name,
		Color: string(in.Color), Description: in.Description, Now: t.now})
	if err != nil {
		return domain.PersonaTag{}, tagNameErr(err, in.Name)
	}
	tag := personaTagFromRow(r)
	return tag, t.activity(actor, companyID, domain.VerbPersonaTagCreated, subjectPersonaTag, tag.ID,
		map[string]any{"name": tag.Name, "color": tag.Color}, "")
}

func tagNameErr(err error, name string) error {
	if isUniqueOn(err, "persona_tags_name_uq") {
		return fmt.Errorf("já existe uma tag chamada %q: %w", name, domain.ErrConflict)
	}
	return mapErr(err, whatPersonaTag)
}

// UpdatePersonaTag edita nome, cor ou descrição (member). O id e as ligações não mudam; renomear recalcula a busca das
// personas que têm a tag.
func (s *Service) UpdatePersonaTag(ctx context.Context, actor domain.Actor, companyID, tagID domain.ID, in UpdatePersonaTagInput) (PersonaTagView, error) {
	if err := s.requireTagWrite(ctx, actor, companyID, domain.MemberMember); err != nil {
		return PersonaTagView{}, err
	}
	var out PersonaTagView
	err := s.withTx(ctx, func(t *txc) error {
		if err := t.updatePersonaTag(actor, companyID, tagID, in); err != nil {
			return err
		}
		var err error
		out, err = tagView(t.ctx, t.q, companyID, tagID)
		return err
	})
	return out, err
}

// updatePersonaTag grava a edição, recalcula a busca se o nome mudou e grava persona_tag.updated. Sem mudança, nada.
func (t *txc) updatePersonaTag(actor domain.Actor, companyID, tagID domain.ID, in UpdatePersonaTagInput) error {
	cur, err := t.lockedTag(companyID, tagID)
	if err != nil {
		return err
	}
	next, changed, err := applyTagUpdate(cur, in)
	if err != nil || len(changed) == 0 {
		return err
	}
	if _, err := t.q.UpdatePersonaTag(t.ctx, store.UpdatePersonaTagParams{CompanyID: companyID, ID: tagID, Name: next.Name,
		Color: string(next.Color), Description: next.Description, Now: t.now}); err != nil {
		return tagNameErr(err, next.Name)
	}
	if slices.Contains(changed, "name") {
		if err := t.refreshTagHolders(companyID, tagID); err != nil {
			return err
		}
	}
	return t.activity(actor, companyID, domain.VerbPersonaTagUpdated, subjectPersonaTag, tagID,
		map[string]any{"name": next.Name, "fields": changed}, "")
}

// applyTagUpdate valida a edição e devolve a tag nova com os campos que mudaram.
func applyTagUpdate(cur store.PersonaTag, in UpdatePersonaTagInput) (PersonaTagInput, []string, error) {
	next := PersonaTagInput{Name: cur.Name, Color: domain.PersonaTagColor(cur.Color), Description: cur.Description}
	if in.Name != nil {
		next.Name = *in.Name
	}
	if in.Color != nil {
		next.Color = *in.Color
	}
	if in.Description != nil {
		next.Description = *in.Description
	}
	next, err := normalizeTagInput(next)
	if err != nil {
		return next, nil, err
	}
	var changed []string
	if next.Name != cur.Name {
		changed = append(changed, "name")
	}
	if string(next.Color) != cur.Color {
		changed = append(changed, "color")
	}
	if next.Description != cur.Description {
		changed = append(changed, "description")
	}
	return next, changed, nil
}

func (t *txc) lockedTag(companyID, tagID domain.ID) (store.PersonaTag, error) {
	if err := t.q.LockPersonaTags(t.ctx, companyID.String()); err != nil {
		return store.PersonaTag{}, fmt.Errorf("lock persona tags: %w", err)
	}
	r, err := t.q.GetPersonaTagForUpdate(t.ctx, store.GetPersonaTagForUpdateParams{CompanyID: companyID, ID: tagID})
	if err != nil {
		return store.PersonaTag{}, mapErr(err, whatPersonaTag)
	}
	return r, nil
}

// refreshTagHolders recalcula `tag_names` das personas que têm a tag.
func (t *txc) refreshTagHolders(companyID, tagID domain.ID) error {
	ids, err := t.q.ListPersonaIDsWithTag(t.ctx, store.ListPersonaIDsWithTagParams{CompanyID: companyID, TagID: tagID})
	if err != nil {
		return fmt.Errorf("list tag holders: %w", err)
	}
	return t.refreshTagNames(companyID, ids)
}

func (t *txc) refreshTagNames(companyID domain.ID, personaIDs []domain.ID) error {
	if len(personaIDs) == 0 {
		return nil
	}
	if err := t.q.RefreshPersonaTagNames(t.ctx, store.RefreshPersonaTagNamesParams{CompanyID: companyID, PersonaIds: personaIDs}); err != nil {
		return fmt.Errorf("refresh tag names: %w", err)
	}
	return nil
}

// ArchivePersonaTag arquiva a tag (admin). Ela continua nas personas e não se aplica a mais nenhuma; arquivar de novo não
// muda nada.
func (s *Service) ArchivePersonaTag(ctx context.Context, actor domain.Actor, companyID, tagID domain.ID) (PersonaTagView, error) {
	return s.setTagArchived(ctx, actor, companyID, tagID, true)
}

// UnarchivePersonaTag devolve a tag ao estado ativo (admin).
func (s *Service) UnarchivePersonaTag(ctx context.Context, actor domain.Actor, companyID, tagID domain.ID) (PersonaTagView, error) {
	return s.setTagArchived(ctx, actor, companyID, tagID, false)
}

func (s *Service) setTagArchived(ctx context.Context, actor domain.Actor, companyID, tagID domain.ID, archive bool) (PersonaTagView, error) {
	if err := s.requireTagWrite(ctx, actor, companyID, domain.MemberAdmin); err != nil {
		return PersonaTagView{}, err
	}
	var out PersonaTagView
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.lockedTag(companyID, tagID)
		if err != nil {
			return err
		}
		if (cur.ArchivedAt != nil) != archive {
			var at *time.Time
			verb := domain.VerbPersonaTagUnarchived
			if archive {
				at, verb = &t.now, domain.VerbPersonaTagArchived
			}
			if _, err := t.q.SetPersonaTagArchived(t.ctx, store.SetPersonaTagArchivedParams{CompanyID: companyID, ID: tagID, ArchivedAt: at, Now: t.now}); err != nil {
				return mapErr(err, whatPersonaTag)
			}
			if err := t.activity(actor, companyID, verb, subjectPersonaTag, tagID, map[string]any{"name": cur.Name}, ""); err != nil {
				return err
			}
		}
		out, err = tagView(t.ctx, t.q, companyID, tagID)
		return err
	})
	return out, err
}

// DeletePersonaTag apaga a tag e todas as ligações (admin; não se desfaz). O evento guarda o nome e quantas personas
// perderam a tag; não há um evento por persona. Devolve essa contagem.
func (s *Service) DeletePersonaTag(ctx context.Context, actor domain.Actor, companyID, tagID domain.ID) (int, error) {
	if err := s.requireTagWrite(ctx, actor, companyID, domain.MemberAdmin); err != nil {
		return 0, err
	}
	var n int
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.lockedTag(companyID, tagID)
		if err != nil {
			return err
		}
		ids, err := t.q.ListPersonaIDsWithTag(t.ctx, store.ListPersonaIDsWithTagParams{CompanyID: companyID, TagID: tagID})
		if err != nil {
			return fmt.Errorf("list tag holders: %w", err)
		}
		if _, err := t.q.DeletePersonaTag(t.ctx, store.DeletePersonaTagParams{CompanyID: companyID, ID: tagID}); err != nil {
			return fmt.Errorf("delete persona tag: %w", err)
		}
		if err := t.refreshTagNames(companyID, ids); err != nil {
			return err
		}
		n = len(ids)
		return t.activity(actor, companyID, domain.VerbPersonaTagDeleted, subjectPersonaTag, tagID,
			map[string]any{"name": cur.Name, "personas": n}, "")
	})
	return n, err
}

// TagPersona aplica uma tag ativa à persona (member; até 20 por persona). Aplicar de novo não muda nada.
func (s *Service) TagPersona(ctx context.Context, actor domain.Actor, companyID, personaID, tagID domain.ID) error {
	if err := s.requireTagWrite(ctx, actor, companyID, domain.MemberMember); err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		tag, err := t.lockedTag(companyID, tagID)
		if err != nil {
			return err
		}
		return t.tagPersona(actor, companyID, personaID, personaTagFromRow(tag))
	})
}

// tagPersona liga a tag à persona, com o teto e o evento. A trava das tags já foi tomada por quem chama.
func (t *txc) tagPersona(actor domain.Actor, companyID, personaID domain.ID, tag domain.PersonaTag) error {
	if tag.Archived() {
		return fmt.Errorf("a tag %q está arquivada e não se aplica: %w", tag.Name, domain.ErrConflict)
	}
	if _, err := t.q.GetPersonaByID(t.ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: personaID}); err != nil {
		return mapErr(err, "persona")
	}
	n, err := t.q.CountTagsOfPersona(t.ctx, store.CountTagsOfPersonaParams{CompanyID: companyID, PersonaID: personaID})
	if err != nil {
		return fmt.Errorf("count persona tags: %w", err)
	}
	added, err := t.q.InsertPersonaTagLink(t.ctx, store.InsertPersonaTagLinkParams{CompanyID: companyID, PersonaID: personaID, TagID: tag.ID, Now: t.now})
	if err != nil {
		return fmt.Errorf("tag persona: %w", err)
	}
	if added == 0 {
		return nil
	}
	if n >= domain.MaxTagsPerPersona {
		return domain.Invalid("tag", fmt.Sprintf("too many tags on the persona (max %d)", domain.MaxTagsPerPersona))
	}
	if err := t.refreshTagNames(companyID, []domain.ID{personaID}); err != nil {
		return err
	}
	return t.activity(actor, companyID, domain.VerbPersonaTagged, "persona", personaID,
		map[string]any{"tag_id": tag.ID, "tag": tag.Name}, "")
}

// CreateAndTagPersona aplica a tag de nome dado, criando-a antes se não existe, numa transação só (o seletor, ADR-0087
// item 5). Tag com o mesmo nome, sem distinguir caixa, é reaproveitada; arquivada, recusa.
func (s *Service) CreateAndTagPersona(ctx context.Context, actor domain.Actor, companyID, personaID domain.ID, in PersonaTagInput) (domain.PersonaTag, error) {
	if err := s.requireTagWrite(ctx, actor, companyID, domain.MemberMember); err != nil {
		return domain.PersonaTag{}, err
	}
	in, err := normalizeTagInput(in)
	if err != nil {
		return domain.PersonaTag{}, err
	}
	var tag domain.PersonaTag
	err = s.withTx(ctx, func(t *txc) error {
		if err := t.q.LockPersonaTags(t.ctx, companyID.String()); err != nil {
			return fmt.Errorf("lock persona tags: %w", err)
		}
		r, err := t.q.GetPersonaTagByName(t.ctx, store.GetPersonaTagByNameParams{CompanyID: companyID, Name: in.Name})
		switch {
		case err == nil:
			tag = personaTagFromRow(r)
		case isNoRows(err):
			if tag, err = t.createPersonaTag(actor, companyID, in); err != nil {
				return err
			}
		default:
			return fmt.Errorf("get persona tag by name: %w", err)
		}
		return t.tagPersona(actor, companyID, personaID, tag)
	})
	return tag, err
}

// UntagPersona tira a tag da persona (member; vale para tag arquivada). Tirar o que não está não muda nada.
func (s *Service) UntagPersona(ctx context.Context, actor domain.Actor, companyID, personaID, tagID domain.ID) error {
	if err := s.requireTagWrite(ctx, actor, companyID, domain.MemberMember); err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		tag, err := t.lockedTag(companyID, tagID)
		if err != nil {
			return err
		}
		if _, err := t.q.GetPersonaByID(t.ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: personaID}); err != nil {
			return mapErr(err, "persona")
		}
		removed, err := t.q.DeletePersonaTagLink(t.ctx, store.DeletePersonaTagLinkParams{CompanyID: companyID, PersonaID: personaID, TagID: tagID})
		if err != nil || removed == 0 {
			return err
		}
		if err := t.refreshTagNames(companyID, []domain.ID{personaID}); err != nil {
			return err
		}
		return t.activity(actor, companyID, domain.VerbPersonaUntagged, "persona", personaID,
			map[string]any{"tag_id": tagID, "tag": tag.Name}, "")
	})
}

// personaTagsByPersona devolve as tags de cada persona da empresa, em ordem de nome.
func personaTagsByPersona(ctx context.Context, q *store.Queries, companyID domain.ID) (map[domain.ID][]domain.PersonaTag, error) {
	rows, err := q.ListPersonaTagRefs(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list persona tag refs: %w", err)
	}
	out := map[domain.ID][]domain.PersonaTag{}
	for _, r := range rows {
		out[r.PersonaID] = append(out[r.PersonaID], personaTagFromRow(r.PersonaTag))
	}
	return out, nil
}

// attachTags preenche Persona.Tags de cada persona.
func attachTags(ctx context.Context, q *store.Queries, companyID domain.ID, ps []domain.Persona) error {
	by, err := personaTagsByPersona(ctx, q, companyID)
	if err != nil {
		return err
	}
	for i := range ps {
		ps[i].Tags = by[ps[i].ID]
	}
	return nil
}

// OrgPersonaFilter é o recorte do organograma (ADR-0087 item 7). Sem nenhum critério, entram todas as personas.
type OrgPersonaFilter struct {
	TagIDs          []domain.ID
	Match           domain.OrgTagMatch // vazio = any
	Untagged        bool               // personas sem tag; combina por OU com as tags
	Levels          []int              // níveis marcados (1 = raiz); vazio = todos
	MaxDepth        int                // corta abaixo deste nível antes dos outros critérios; 0 = sem corte
	IncludeArchived bool               // aceita tag arquivada no filtro
}

// OrgPersona é uma persona do recorte, com o nível calculado (ADR-0087 item 6).
type OrgPersona struct {
	Persona domain.Persona
	Level   int
}

// ListOrgPersonas lista as personas do recorte por tag e por nível, na ordem do organograma (cada raiz e os seus
// subordinados). Tag e níveis se combinam por E.
func (s *Service) ListOrgPersonas(ctx context.Context, companyID domain.ID, f OrgPersonaFilter) ([]OrgPersona, error) {
	f, err := s.checkOrgFilter(ctx, companyID, f)
	if err != nil {
		return nil, err
	}
	chart, err := s.OrgChart(ctx, companyID)
	if err != nil {
		return nil, err
	}
	out := []OrgPersona{}
	var walk func(n *OrgNode)
	walk = func(n *OrgNode) {
		level := domain.OrgLevel(n.Depth)
		if f.MaxDepth > 0 && level > f.MaxDepth {
			return
		}
		if (len(f.Levels) == 0 || slices.Contains(f.Levels, level)) && f.matchTags(n.Persona.Tags) {
			out = append(out, OrgPersona{Persona: n.Persona, Level: level})
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	for _, r := range chart.Roots {
		walk(r)
	}
	return out, nil
}

// checkOrgFilter valida o filtro: combinação conhecida, níveis a partir de 1 e tags da empresa, arquivadas só com
// IncludeArchived.
func (s *Service) checkOrgFilter(ctx context.Context, companyID domain.ID, f OrgPersonaFilter) (OrgPersonaFilter, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return f, err
	}
	if f.Match == "" {
		f.Match = domain.OrgTagAny
	}
	if !f.Match.Valid() {
		return f, domain.Invalid("match", fmt.Sprintf("unknown match %q (want any or all)", f.Match))
	}
	if f.MaxDepth < 0 {
		return f, domain.Invalid("max_depth", "must be >= 0")
	}
	for _, l := range f.Levels {
		if l < 1 {
			return f, domain.Invalid("level", "levels start at 1")
		}
	}
	for _, id := range f.TagIDs {
		r, err := s.read().GetPersonaTag(ctx, store.GetPersonaTagParams{CompanyID: companyID, ID: id})
		if err != nil {
			return f, mapErr(err, whatPersonaTag)
		}
		if r.ArchivedAt != nil && !f.IncludeArchived {
			return f, domain.Invalid("tag", fmt.Sprintf("tag %q is archived: show archived tags to filter by it", r.Name))
		}
	}
	return f, nil
}

// matchTags aplica o critério de tag: any (alguma das tags), all (todas) e "sem tag", por OU.
func (f OrgPersonaFilter) matchTags(tags []domain.PersonaTag) bool {
	if len(f.TagIDs) == 0 && !f.Untagged {
		return true
	}
	if f.Untagged && len(tags) == 0 {
		return true
	}
	if len(f.TagIDs) == 0 {
		return false
	}
	has := func(id domain.ID) bool {
		return slices.ContainsFunc(tags, func(t domain.PersonaTag) bool { return t.ID == id })
	}
	if f.Match == domain.OrgTagAll {
		return !slices.ContainsFunc(f.TagIDs, func(id domain.ID) bool { return !has(id) })
	}
	return slices.ContainsFunc(f.TagIDs, has)
}
