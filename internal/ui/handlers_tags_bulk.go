package ui

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Edição em lote das tags na lista de Personas (ADR-0087 item 5, fatia 2): marcar personas e aplicar ou tirar uma tag
// de todas. O service faz tudo numa transação, com um evento por persona alterada.

func (s *server) personasBulkVM(ctx context.Context, co domain.Company) (views.PersonasBulkVM, error) {
	vm := views.PersonasBulkVM{CanWrite: views.RoleFrom(ctx).AtLeast(domain.MemberMember), Action: "/c/" + co.Slug + "/tags/lote"}
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

// bulkNotice é o aviso do lote que acabou de rodar (?ok=lote-aplicada|lote-tirada&n=…&tag=…).
func bulkNotice(r *http.Request) string {
	q := r.URL.Query()
	n, err := strconv.Atoi(q.Get("n"))
	if err != nil || n < 0 {
		return ""
	}
	switch q.Get("ok") {
	case "lote-aplicada":
		return i18n.Tf(r.Context(), "Tag «%s» aplicada a %d persona(s).", q.Get("tag"), n)
	case "lote-tirada":
		return i18n.Tf(r.Context(), "Tag «%s» tirada de %d persona(s).", q.Get("tag"), n)
	}
	return ""
}

// handleBulkPersonaTags aplica (acao=aplicar) ou tira (acao=tirar) a tag de nome dado das personas marcadas. Aplicar cria
// a tag se ela não existe; tirar só vale para uma tag do cadastro.
func (s *server) handleBulkPersonaTags(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	var ids []domain.ID
	for _, v := range r.PostForm["persona"] {
		id, err := domain.ParseID(v)
		if err != nil {
			s.bulkFail(w, r, co, domain.Invalid("persona", i18n.T(ctx, "Persona inválida no lote.")))
			return
		}
		ids = append(ids, id)
	}
	name := r.PostFormValue("name")
	var res service.BulkTagResult
	var err error
	ok := "lote-aplicada"
	switch r.PostFormValue("acao") {
	case "aplicar":
		res, err = s.be.BulkTagPersonas(ctx, actorFrom(ctx), co.ID, ids, service.PersonaTagInput{Name: name})
	case "tirar":
		ok = "lote-tirada"
		res, err = s.bulkUntagByName(ctx, co, ids, name)
	default:
		err = domain.Invalid("acao", i18n.T(ctx, "Escolha aplicar ou tirar."))
	}
	if err != nil {
		s.bulkFail(w, r, co, err)
		return
	}
	q := url.Values{"ok": {ok}, "n": {strconv.Itoa(res.Changed)}, "tag": {res.Tag.Name}}
	redirect(w, r, "/c/"+co.Slug+"/personas?"+q.Encode())
}

// bulkUntagByName acha a tag pelo nome (sem distinguir caixa, arquivadas incluídas) e tira das personas.
func (s *server) bulkUntagByName(ctx context.Context, co domain.Company, ids []domain.ID, name string) (service.BulkTagResult, error) {
	all, err := s.be.ListPersonaTags(ctx, co.ID, true)
	if err != nil {
		return service.BulkTagResult{}, err
	}
	key := domain.PersonaTagKey(name)
	for _, t := range all {
		if domain.PersonaTagKey(t.Name) == key {
			return s.be.BulkUntagPersonas(ctx, actorFrom(ctx), co.ID, ids, t.ID)
		}
	}
	return service.BulkTagResult{}, domain.Invalid("name", i18n.Tf(ctx, "A tag «%s» não existe.", name))
}

// bulkFail redesenha a lista com o erro do lote (nenhuma persona marcada, teto de 20, tag arquivada).
func (s *server) bulkFail(w http.ResponseWriter, r *http.Request, co domain.Company, err error) {
	st, _, msg := friendly(r.Context(), err)
	if st >= http.StatusInternalServerError || st == http.StatusForbidden {
		s.fail(w, r, &co, err)
		return
	}
	s.renderPersonas(w, r, co, st, msg)
}
