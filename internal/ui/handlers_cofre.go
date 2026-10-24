package ui

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Configurações › Segredos › campos e coleções (ADR-0068, fatia 1). Revelar campo, histórico, restaurar versão e
// concessões exigem reautenticação recente (ADR-0070, #57) e não existem aqui: a tela só diz isso em texto.
// Campo secreto nunca leva valor para o HTML; só a resposta de um valor gerado o mostra, uma vez.

type vaultUIBackend interface {
	CreateSecretField(ctx context.Context, actor domain.Actor, companyID domain.ID, scope domain.SecretScope, secretName string, in service.CreateSecretFieldInput) (service.SecretFieldView, error)
	ListSecretFields(ctx context.Context, companyID domain.ID, scope domain.SecretScope, secretName string) ([]service.SecretFieldView, error)
	UpdateSecretFieldMeta(ctx context.Context, actor domain.Actor, companyID domain.ID, scope domain.SecretScope, secretName, fieldName string, sensitive *bool, position *int) (service.SecretFieldView, error)
	RotateSecretField(ctx context.Context, actor domain.Actor, companyID domain.ID, scope domain.SecretScope, secretName, fieldName string, in service.SecretFieldValueInput) (service.SecretFieldView, error)
	CreateCollection(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CollectionInput) (service.CollectionView, error)
	ListCollections(ctx context.Context, companyID domain.ID) ([]service.CollectionView, error)
	GetCollection(ctx context.Context, companyID domain.ID, idOrName string) (service.CollectionView, error)
	DeleteCollection(ctx context.Context, actor domain.Actor, companyID domain.ID, idOrName string) error
	ListCollectionSecrets(ctx context.Context, companyID domain.ID, idOrName string) ([]domain.Secret, error)
	AddSecretToCollection(ctx context.Context, actor domain.Actor, companyID domain.ID, idOrName, secretName string) error
	RemoveSecretFromCollection(ctx context.Context, actor domain.Actor, companyID domain.ID, idOrName, secretName string) error
	ListCollectionGrants(ctx context.Context, companyID domain.ID, idOrName string) ([]service.CollectionGrantView, error)
}

// vaultReq devolve o backend do cofre e o contexto da requisição; sem o cofre ligado, responde indisponível.
func (s *server) vaultReq(w http.ResponseWriter, r *http.Request) (vaultUIBackend, context.Context, bool) {
	be, ok := s.vaultBE()
	if !ok {
		s.unavailable(w, r)
		return nil, nil, false
	}
	return be, r.Context(), true
}

func (s *server) vaultBE() (vaultUIBackend, bool) {
	b, ok := s.be.(vaultUIBackend)
	return b, ok
}

var vaultNotices = map[string]string{
	"campo":           i18n.N("Campo adicionado."),
	"valor":           i18n.N("Valor do campo trocado: a versão do campo subiu."),
	"marca":           i18n.N("Marca do campo atualizada."),
	"colecao":         i18n.N("Coleção criada."),
	"colecao-apagada": i18n.N("Coleção apagada."),
	"no":              i18n.N("Segredo posto na coleção."),
	"fora":            i18n.N("Segredo tirado da coleção."),
}

// vaultErrorMsg traduz o erro do service em texto para a página, sem ecoar valor.
func vaultErrorMsg(ctx context.Context, err error) string {
	var ve *domain.ValidationError
	if errors.As(err, &ve) {
		switch ve.Field {
		case "name":
			return i18n.T(ctx, "Nome inválido.")
		case "description":
			return i18n.T(ctx, "Descrição inválida: até 300 caracteres.")
		case "rotate_after_days":
			return i18n.T(ctx, "Prazo de rotação inválido: de 1 a 3650 dias.")
		case "collection":
			return i18n.T(ctx, "A coleção padrão não se apaga.")
		}
	}
	if errors.Is(err, domain.ErrConflict) {
		var ve2 *domain.ValidationError
		if !errors.As(err, &ve2) {
			return i18n.T(ctx, "Conflito: já existe um item com esse nome, ou o estado atual não permite a ação.")
		}
	}
	return secretErrorMsg(ctx, err)
}

func vaultDone(r *http.Request, key string) string {
	return i18n.T(r.Context(), vaultNotices[key])
}

func scopeQuery(user bool) string {
	if user {
		return "?escopo=usuario"
	}
	return ""
}

// ---------- campos ----------

func (s *server) fieldsVM(r *http.Request, co domain.Company, be vaultUIBackend, sh views.Shell, name string, user bool) (views.SecretFieldsVM, error) {
	ctx := r.Context()
	base := "/c/" + co.Slug
	seg := base + "/configuracoes/segredos/" + url.PathEscape(name) + "/campos"
	_, hasID := auth.IdentityFrom(ctx)
	vm := views.SecretFieldsVM{Sections: settingsSections(ctx, base, "segredos"), Notice: vaultDone(r, r.URL.Query().Get("ok")),
		Name: name, User: user, CanWrite: sh.CanAdmin() || (user && hasID),
		BackHref: base + "/configuracoes/segredos", CreateHref: seg + scopeQuery(user)}
	list, err := be.ListSecretFields(ctx, co.ID, scopeOf(user), name)
	if err != nil {
		return vm, err
	}
	for _, f := range list {
		ref := "${secret:" + name + "#" + f.Name + "}"
		if user {
			ref = "${secret:user/" + name + "#" + f.Name + "}"
		}
		row := views.SecretFieldRowVM{Name: f.Name, Reference: ref, Kind: string(f.Kind), Sensitive: f.Sensitive, Version: f.Version,
			Revealed:  f.RevealedAt != nil,
			ValueHref: seg + "/" + url.PathEscape(f.Name) + "/valor" + scopeQuery(user), MetaHref: seg + "/" + url.PathEscape(f.Name) + "/marca" + scopeQuery(user)}
		if !f.Sensitive { // campo secreto nunca leva texto para a página
			row.VisibleText = f.VisibleText
		}
		vm.Fields = append(vm.Fields, row)
	}
	return vm, nil
}

func (s *server) renderFields(w http.ResponseWriter, r *http.Request, co domain.Company, sh views.Shell, status int, vm views.SecretFieldsVM) {
	w.Header().Set("Cache-Control", "no-store")
	s.render(w, r, status, views.SecretFieldsPage(sh, vm))
}

func (s *server) handleSecretFields(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, ok := s.vaultBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	s.showFields(w, r, co, be, http.StatusOK, "", func(*views.SecretFieldsVM) {})
}

// showFields monta e renderiza a página; mut ajusta o VM (erro, formulário, valor único).
func (s *server) showFields(w http.ResponseWriter, r *http.Request, co domain.Company, be vaultUIBackend, status int, _ string, mut func(*views.SecretFieldsVM)) {
	user, name := userScopeForm(r), r.PathValue("name")
	sh := s.settingsShell(r, co, name)
	vm, err := s.fieldsVM(r, co, be, sh, name, user)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	mut(&vm)
	s.renderFields(w, r, co, sh, status, vm)
}

func (s *server) fieldsFail(w http.ResponseWriter, r *http.Request, co domain.Company, be vaultUIBackend, err error, form func(*views.SecretFieldsVM)) {
	s.showFields(w, r, co, be, statusFor(err), "", func(vm *views.SecretFieldsVM) {
		vm.Error = vaultErrorMsg(r.Context(), err)
		if form != nil {
			form(vm)
		}
	})
}

func (s *server) afterFieldWrite(w http.ResponseWriter, r *http.Request, co domain.Company, be vaultUIBackend, v service.SecretFieldView, user bool, ok string) {
	name := r.PathValue("name")
	if v.RevealedValue == "" {
		redirect(w, r, "/c/"+co.Slug+"/configuracoes/segredos/"+url.PathEscape(name)+"/campos"+scopeQuery(user)+okQuery(user, ok))
		return
	}
	ref := "${secret:" + name + "#" + v.Name + "}"
	if user {
		ref = "${secret:user/" + name + "#" + v.Name + "}"
	}
	s.showFields(w, r, co, be, http.StatusOK, "", func(vm *views.SecretFieldsVM) {
		vm.Reveal = &views.SecretRevealVM{Name: name + "#" + v.Name, Reference: ref, Value: v.RevealedValue}
	})
}

func okQuery(user bool, ok string) string {
	if user {
		return "&ok=" + ok
	}
	return "?ok=" + ok
}

func (s *server) handleCreateSecretField(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, ctx, ok := s.vaultReq(w, r)
	if !ok {
		return
	}
	user, name := userScopeForm(r), r.PathValue("name")
	fname, kind := trimForm(r, "name"), trimForm(r, "kind")
	visible := r.PostFormValue("visibilidade") == "visivel"
	generate := r.PostFormValue("generate") != ""
	sensitive := !visible
	in := service.CreateSecretFieldInput{Name: fname, Kind: domain.SecretFieldKind(kind), Sensitive: &sensitive,
		SecretFieldValueInput: service.SecretFieldValueInput{Value: r.PostFormValue("value"), Generate: generate}}
	v, err := be.CreateSecretField(ctx, actorFrom(ctx), co.ID, scopeOf(user), name, in)
	if err != nil {
		s.fieldsFail(w, r, co, be, err, func(vm *views.SecretFieldsVM) { // o valor digitado nunca volta
			vm.NewName, vm.NewKind, vm.NewVisible, vm.NewGenerate = fname, kind, visible, generate
		})
		return
	}
	s.afterFieldWrite(w, r, co, be, v, user, "campo")
}

func (s *server) handleRotateSecretField(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, ctx, ok := s.vaultReq(w, r)
	if !ok {
		return
	}
	user, name := userScopeForm(r), r.PathValue("name")
	in := service.SecretFieldValueInput{Value: r.PostFormValue("value"), Generate: r.PostFormValue("generate") != ""}
	v, err := be.RotateSecretField(ctx, actorFrom(ctx), co.ID, scopeOf(user), name, r.PathValue("field"), in)
	if err != nil {
		s.fieldsFail(w, r, co, be, err, nil)
		return
	}
	s.afterFieldWrite(w, r, co, be, v, user, "valor")
}

func (s *server) handleSecretFieldMeta(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, ctx, ok := s.vaultReq(w, r)
	if !ok {
		return
	}
	user, name := userScopeForm(r), r.PathValue("name")
	sensitive := r.PostFormValue("visibilidade") != "visivel"
	v, err := be.UpdateSecretFieldMeta(ctx, actorFrom(ctx), co.ID, scopeOf(user), name, r.PathValue("field"), &sensitive, nil)
	if err != nil {
		s.fieldsFail(w, r, co, be, err, nil)
		return
	}
	s.afterFieldWrite(w, r, co, be, v, user, "marca")
}

// ---------- coleções ----------

func daysText(d *int) string {
	if d == nil {
		return ""
	}
	return strconv.Itoa(*d)
}

func (s *server) collectionsVM(r *http.Request, co domain.Company, be vaultUIBackend, sh views.Shell) (views.CollectionsVM, error) {
	ctx := r.Context()
	base := "/c/" + co.Slug
	vm := views.CollectionsVM{Sections: settingsSections(ctx, base, "segredos"), Notice: vaultDone(r, r.URL.Query().Get("ok")), CanAdmin: sh.CanAdmin(),
		BackHref: base + "/configuracoes/segredos", CreateHref: base + "/configuracoes/colecoes"}
	list, err := be.ListCollections(ctx, co.ID)
	if err != nil {
		return vm, err
	}
	for _, c := range list {
		vm.Rows = append(vm.Rows, views.CollectionRowVM{Name: c.Name, Description: c.Description, IsDefault: c.IsDefault, Secrets: c.Secrets,
			RotateDays: daysText(c.RotateAfterDays), Href: base + "/configuracoes/colecoes/" + url.PathEscape(c.Name)})
	}
	return vm, nil
}

func (s *server) renderCollections(w http.ResponseWriter, r *http.Request, co domain.Company, be vaultUIBackend, status int, err error, form func(*views.CollectionsVM)) {
	sh := s.settingsShell(r, co, i18n.T(r.Context(), "Coleções de segredos"))
	vm, lerr := s.collectionsVM(r, co, be, sh)
	if lerr != nil {
		s.fail(w, r, &co, lerr)
		return
	}
	if err != nil {
		vm.Error = vaultErrorMsg(r.Context(), err)
	}
	if form != nil {
		form(&vm)
	}
	s.render(w, r, status, views.CollectionsPage(sh, vm))
}

func (s *server) handleCollections(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, ok := s.vaultBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	s.renderCollections(w, r, co, be, http.StatusOK, nil, nil)
}

func (s *server) handleCreateCollection(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, ctx, ok := s.vaultReq(w, r)
	if !ok {
		return
	}
	name, desc, days := trimForm(r, "name"), trimForm(r, "description"), trimForm(r, "rotate_after_days")
	in := service.CollectionInput{Name: name, Description: desc}
	if days != "" {
		n, perr := strconv.Atoi(days)
		if perr != nil {
			s.renderCollections(w, r, co, be, http.StatusUnprocessableEntity, domain.Invalid("rotate_after_days", "not a number"),
				func(vm *views.CollectionsVM) { vm.NewName, vm.NewDesc, vm.NewDays = name, desc, days })
			return
		}
		in.RotateAfterDays = &n
	}
	if _, err := be.CreateCollection(ctx, actorFrom(ctx), co.ID, in); err != nil {
		s.renderCollections(w, r, co, be, statusFor(err), err, func(vm *views.CollectionsVM) { vm.NewName, vm.NewDesc, vm.NewDays = name, desc, days })
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/configuracoes/colecoes?ok=colecao")
}

func (s *server) collectionVM(r *http.Request, co domain.Company, be vaultUIBackend, sh views.Shell, ref string) (views.CollectionVM, error) {
	ctx := r.Context()
	base := "/c/" + co.Slug
	c, err := be.GetCollection(ctx, co.ID, ref)
	if err != nil {
		return views.CollectionVM{}, err
	}
	seg := base + "/configuracoes/colecoes/" + url.PathEscape(c.Name)
	vm := views.CollectionVM{Sections: settingsSections(ctx, base, "segredos"), Notice: vaultDone(r, r.URL.Query().Get("ok")), CanAdmin: sh.CanAdmin(),
		BackHref: base + "/configuracoes/colecoes", Name: c.Name, Desc: c.Description, IsDefault: c.IsDefault, RotateDays: daysText(c.RotateAfterDays),
		AddHref: seg + "/segredos", DeleteHref: seg + "/apagar"}
	secs, err := be.ListCollectionSecrets(ctx, co.ID, ref)
	if err != nil {
		return vm, err
	}
	for _, sec := range secs {
		vm.Secrets = append(vm.Secrets, views.CollectionSecretVM{Name: sec.Name, RemoveHref: seg + "/segredos/" + url.PathEscape(sec.Name) + "/remover"})
	}
	if gr, err := be.ListCollectionGrants(ctx, co.ID, ref); err == nil {
		vm.GrantCount = len(gr)
	}
	return vm, nil
}

func (s *server) showCollection(w http.ResponseWriter, r *http.Request, co domain.Company, be vaultUIBackend, status int, err error) {
	ref := r.PathValue("coll")
	sh := s.settingsShell(r, co, ref)
	vm, lerr := s.collectionVM(r, co, be, sh, ref)
	if lerr != nil {
		s.fail(w, r, &co, lerr)
		return
	}
	if err != nil {
		vm.Error = vaultErrorMsg(r.Context(), err)
	}
	s.render(w, r, status, views.CollectionPage(sh, vm))
}

func (s *server) handleCollection(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, ok := s.vaultBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	s.showCollection(w, r, co, be, http.StatusOK, nil)
}

func (s *server) handleDeleteCollection(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, ctx, ok := s.vaultReq(w, r)
	if !ok {
		return
	}
	if err := be.DeleteCollection(ctx, actorFrom(ctx), co.ID, r.PathValue("coll")); err != nil {
		s.showCollection(w, r, co, be, statusFor(err), err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/configuracoes/colecoes?ok=colecao-apagada")
}

func (s *server) handleCollectionAdd(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, ctx, ok := s.vaultReq(w, r)
	if !ok {
		return
	}
	if err := be.AddSecretToCollection(ctx, actorFrom(ctx), co.ID, r.PathValue("coll"), trimForm(r, "secret")); err != nil {
		s.showCollection(w, r, co, be, statusFor(err), err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/configuracoes/colecoes/"+url.PathEscape(r.PathValue("coll"))+"?ok=no")
}

func (s *server) handleCollectionRemove(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, ctx, ok := s.vaultReq(w, r)
	if !ok {
		return
	}
	if err := be.RemoveSecretFromCollection(ctx, actorFrom(ctx), co.ID, r.PathValue("coll"), r.PathValue("name")); err != nil {
		s.showCollection(w, r, co, be, statusFor(err), err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/configuracoes/colecoes/"+url.PathEscape(r.PathValue("coll"))+"?ok=fora")
}
