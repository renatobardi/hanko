package ui

import (
	"context"
	"net/http"
	"net/url"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Tags de persona (ADR-0087): a aba Tags em Personas (cadastro) e aplicar e tirar tag na ficha. Os papéis valem no
// service; a tela só esconde o que o papel não alcança. A aba fica em /personas/tags; as ações de uma tag ficam em
// /tags/{tag}/…, porque /personas/tags/{tag}/… colide com /personas/{persona}/skills/{skill} no roteador.

// tagColorLabels são os rótulos da paleta, na ordem de domain.PersonaTagColors.
var tagColorLabels = map[domain.PersonaTagColor]string{
	domain.TagGray: i18n.N("Cinza"), domain.TagRed: i18n.N("Vermelho"), domain.TagAmber: i18n.N("Âmbar"), domain.TagGreen: i18n.N("Verde"),
	domain.TagTeal: i18n.N("Turquesa"), domain.TagBlue: i18n.N("Azul"), domain.TagViolet: i18n.N("Violeta"), domain.TagPink: i18n.N("Rosa"),
}

var tagNotices = map[string]string{
	"criada":       i18n.N("Tag criada."),
	"editada":      i18n.N("Tag atualizada."),
	"arquivada":    i18n.N("Tag arquivada: ela sai do seletor e continua nas personas."),
	"desarquivada": i18n.N("Tag desarquivada."),
	"deletada":     i18n.N("Tag deletada."),
}

func tagColorOptions(ctx context.Context, sel domain.PersonaTagColor) []views.Option {
	out := make([]views.Option, 0, len(domain.PersonaTagColors))
	for _, c := range domain.PersonaTagColors {
		out = append(out, views.Option{Value: string(c), Label: i18n.T(ctx, tagColorLabels[c]), Selected: c == sel})
	}
	return out
}

func tagChipOf(t domain.PersonaTag) views.TagChip {
	return views.TagChip{Name: t.Name, Color: string(t.Color), Description: t.Description, Archived: t.Archived()}
}

// tagChipsOf são os chips de uma persona (até 3 e "+N").
func tagChipsOf(tags []domain.PersonaTag) views.TagChipsVM {
	chips := make([]views.TagChip, len(tags))
	for i, t := range tags {
		chips[i] = tagChipOf(t)
	}
	return views.ChipsOf(chips)
}

// personaTagsVM monta o cartão de tags da ficha: chips com o POST que tira e os nomes ativos para o seletor.
func (s *server) personaTagsVM(ctx context.Context, co domain.Company, p domain.Persona) (views.PersonaTagsVM, error) {
	base := "/c/" + co.Slug
	pb := base + "/personas/" + p.Slug
	vm := views.PersonaTagsVM{ApplyHref: pb + "/tags", TagsHref: base + "/personas/tags", CanWrite: views.RoleFrom(ctx).AtLeast(domain.MemberMember)}
	for _, t := range p.Tags {
		vm.Chips = append(vm.Chips, views.PersonaTagChip{TagChip: tagChipOf(t), RemoveHref: pb + "/tags/" + t.ID.String() + "/remover"})
	}
	if !vm.CanWrite {
		return vm, nil
	}
	active, err := s.be.ListPersonaTags(ctx, co.ID, false)
	if err != nil {
		return vm, err
	}
	for _, t := range active {
		vm.Options = append(vm.Options, t.Name)
	}
	return vm, nil
}

func (s *server) handlePersonaTags(w http.ResponseWriter, r *http.Request, co domain.Company) {
	s.renderPersonaTags(w, r, co, http.StatusOK, "")
}

// renderPersonaTags desenha a aba Tags; errMsg não vazio é o erro do último formulário.
func (s *server) renderPersonaTags(w http.ResponseWriter, r *http.Request, co domain.Company, status int, errMsg string) {
	ctx := r.Context()
	archived := r.URL.Query().Get("arquivadas") == "1"
	tags, err := s.be.ListPersonaTags(ctx, co.ID, archived)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	base := "/c/" + co.Slug + "/personas/tags"
	vm := views.TagsPageVM{ShowArchived: archived, ToggleHref: base + "?arquivadas=1", CreateHref: base, Colors: tagColorOptions(ctx, domain.TagGray), Error: errMsg}
	if archived {
		vm.ToggleHref = base
	}
	for _, t := range tags {
		tb := "/c/" + co.Slug + "/tags/" + t.ID.String()
		row := views.TagRow{TagChip: tagChipOf(t.PersonaTag), ID: t.ID.String(), Personas: t.PersonaCount, EditHref: tb + "/editar",
			DeleteHref: tb + "/deletar", Colors: tagColorOptions(ctx, t.Color)}
		if t.Archived() {
			row.UnarchiveHref = tb + "/desarquivar"
		} else {
			row.ArchiveHref = tb + "/arquivar"
		}
		vm.Rows = append(vm.Rows, row)
	}
	vm.Subtitle = i18n.T(ctx, "Tags classificam as personas para filtrar o organograma e achar a quem delegar.")
	if msg, ok := tagNotices[r.URL.Query().Get("ok")]; ok && errMsg == "" {
		vm.Subtitle = i18n.T(ctx, msg)
	}
	sh := s.shell(r, co, "personas", i18n.T(ctx, "Tags"), views.Crumb{Label: i18n.T(ctx, "Personas"), Href: "/c/" + co.Slug + "/personas"},
		views.Crumb{Label: i18n.T(ctx, "Tags")})
	s.render(w, r, status, views.TagsPage(sh, vm))
}

// tagFormFail redesenha a aba com a mensagem do erro de um formulário (nome repetido, cor fora da paleta, teto).
func (s *server) tagFormFail(w http.ResponseWriter, r *http.Request, co domain.Company, err error) {
	st, _, msg := friendly(r.Context(), err)
	if st >= http.StatusInternalServerError || st == http.StatusForbidden || st == http.StatusNotFound {
		s.fail(w, r, &co, err)
		return
	}
	s.renderPersonaTags(w, r, co, st, msg)
}

func tagsRedirect(w http.ResponseWriter, r *http.Request, co domain.Company, ok string) {
	redirect(w, r, "/c/"+co.Slug+"/personas/tags?ok="+url.QueryEscape(ok))
}

func (s *server) handleCreatePersonaTag(w http.ResponseWriter, r *http.Request, co domain.Company) {
	in := service.PersonaTagInput{Name: r.PostFormValue("name"), Color: domain.PersonaTagColor(r.PostFormValue("color")),
		Description: r.PostFormValue("description")}
	if _, err := s.be.CreatePersonaTag(r.Context(), actorFrom(r.Context()), co.ID, in); err != nil {
		s.tagFormFail(w, r, co, err)
		return
	}
	tagsRedirect(w, r, co, "criada")
}

func (s *server) handleEditPersonaTag(w http.ResponseWriter, r *http.Request, co domain.Company) {
	id, err := domain.ParseID(r.PathValue("tag"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	name, desc := r.PostFormValue("name"), r.PostFormValue("description")
	color := domain.PersonaTagColor(r.PostFormValue("color"))
	in := service.UpdatePersonaTagInput{Name: &name, Color: &color, Description: &desc}
	if _, err := s.be.UpdatePersonaTag(r.Context(), actorFrom(r.Context()), co.ID, id, in); err != nil {
		s.tagFormFail(w, r, co, err)
		return
	}
	tagsRedirect(w, r, co, "editada")
}

// handleArchivePersonaTag arquiva (archive=true) ou desarquiva a tag.
func (s *server) handleArchivePersonaTag(archive bool) func(http.ResponseWriter, *http.Request, domain.Company) {
	return func(w http.ResponseWriter, r *http.Request, co domain.Company) {
		id, err := domain.ParseID(r.PathValue("tag"))
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		ok := "desarquivada"
		if archive {
			ok = "arquivada"
			_, err = s.be.ArchivePersonaTag(r.Context(), actorFrom(r.Context()), co.ID, id)
		} else {
			_, err = s.be.UnarchivePersonaTag(r.Context(), actorFrom(r.Context()), co.ID, id)
		}
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		tagsRedirect(w, r, co, ok)
	}
}

// handleDeletePersonaTag deleta a tag depois da confirmação explícita (não se desfaz).
func (s *server) handleDeletePersonaTag(w http.ResponseWriter, r *http.Request, co domain.Company) {
	id, err := domain.ParseID(r.PathValue("tag"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	if r.PostFormValue("confirm") != "1" {
		s.tagFormFail(w, r, co, domain.Invalid("confirm", i18n.T(r.Context(), "Confirme que a exclusão não se desfaz.")))
		return
	}
	if _, err := s.be.DeletePersonaTag(r.Context(), actorFrom(r.Context()), co.ID, id); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	tagsRedirect(w, r, co, "deletada")
}

// handleApplyPersonaTag aplica a tag de nome dado à persona e cria a tag antes, se ela não existe (o seletor da ficha).
func (s *server) handleApplyPersonaTag(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	p, err := s.be.GetPersona(ctx, co.ID, r.PathValue("persona"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	if _, err := s.be.CreateAndTagPersona(ctx, actorFrom(ctx), co.ID, p.ID, service.PersonaTagInput{Name: r.PostFormValue("name")}); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/personas/"+p.Slug+"?ok=tag#tags")
}

func (s *server) handleRemovePersonaTag(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	p, err := s.be.GetPersona(ctx, co.ID, r.PathValue("persona"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	id, err := domain.ParseID(r.PathValue("tag"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	if err := s.be.UntagPersona(ctx, actorFrom(ctx), co.ID, p.ID, id); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/personas/"+p.Slug+"?ok=tag-removida#tags")
}
