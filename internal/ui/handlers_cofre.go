package ui

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

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
	VaultHealth(ctx context.Context, companyID domain.ID) (service.HealthReport, error)
	PreviewEnvImport(ctx context.Context, companyID domain.ID, in service.EnvImportInput) (service.EnvImportPlan, error)
	ImportEnv(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.EnvImportInput) (service.VaultImportResult, error)
	ReauthWithPassword(ctx context.Context, id auth.Identity, password string) (service.ReauthProof, error)
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
	gen, gerr := genOptsFromForm(r)
	if gerr != nil {
		s.fieldsFail(w, r, co, be, gerr, nil)
		return
	}
	sensitive := !visible
	in := service.CreateSecretFieldInput{Name: fname, Kind: domain.SecretFieldKind(kind), Sensitive: &sensitive,
		SecretFieldValueInput: service.SecretFieldValueInput{Value: r.PostFormValue("value"), Generate: generate, Gen: gen}}
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
	gen, gerr := genOptsFromForm(r)
	if gerr != nil {
		s.fieldsFail(w, r, co, be, gerr, nil)
		return
	}
	in := service.SecretFieldValueInput{Value: r.PostFormValue("value"), Generate: r.PostFormValue("generate") != "", Gen: gen}
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

// genOptsFromForm lê o bloco do gerador (gen_length, gen_class e gen_words, ADR-0068 §7.1). Campo vazio vale o padrão;
// número que não lê vira erro de validação, não o padrão em silêncio.
func genOptsFromForm(r *http.Request) (domain.GenOptions, error) {
	var o domain.GenOptions
	if v := strings.TrimSpace(r.PostFormValue("gen_length")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return o, domain.Invalid("length", "tamanho: um número de 8 a 128")
		}
		o.Length = n
	}
	if v := strings.TrimSpace(r.PostFormValue("gen_words")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return o, domain.Invalid("passphrase_words", "palavras: um número de 3 a 12")
		}
		o.PassphraseWords = n
	}
	for _, c := range r.PostForm["gen_class"] {
		o.Classes = append(o.Classes, domain.GenClass(c))
	}
	return o, nil
}

// healthKindText dá o nome de cada categoria do relatório de saúde.
var healthKindText = map[domain.HealthKind]string{
	domain.HealthDuplicate: i18n.N("Repetido"),
	domain.HealthStale:     i18n.N("Velho (passou do prazo de rotação)"),
	domain.HealthUnused:    i18n.N("Sem uso"),
	domain.HealthOrphan:    i18n.N("Órfão"),
	domain.HealthWeak:      i18n.N("Fraco"),
}

// secretsHealthVM monta a página de saúde: contagem por categoria e os achados, na ordem do domínio.
func (s *server) secretsHealthVM(r *http.Request, co domain.Company, be vaultUIBackend) (views.SecretsHealthVM, error) {
	ctx := r.Context()
	base := "/c/" + co.Slug
	vm := views.SecretsHealthVM{Sections: settingsSections(ctx, base, "segredos"), BackHref: base + "/configuracoes/segredos"}
	rep, err := be.VaultHealth(ctx, co.ID)
	if err != nil {
		return vm, err
	}
	counts := map[domain.HealthKind]int{}
	for _, f := range rep.Findings {
		counts[f.Kind]++
		vm.Rows = append(vm.Rows, views.HealthRowVM{Kind: i18n.T(ctx, healthKindText[f.Kind]), Secret: f.Secret, Field: f.Field, Detail: f.Detail})
	}
	for _, k := range domain.HealthKinds {
		vm.Counts = append(vm.Counts, views.HealthCountVM{Label: i18n.T(ctx, healthKindText[k]), Count: counts[k]})
	}
	return vm, nil
}

func (s *server) handleSecretsHealth(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, ok := s.vaultBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	sh := s.settingsShell(r, co, i18n.T(r.Context(), "Saúde dos segredos"))
	vm, err := s.secretsHealthVM(r, co, be)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.render(w, r, http.StatusOK, views.SecretsHealthPage(sh, vm))
}

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

// ---------- import de .env (ADR-0068 §8.1, #62) ----------

// envImportVM monta o formulário do import; as coleções vêm da lista da empresa, a padrão fica na opção vazia.
func (s *server) envImportVM(r *http.Request, co domain.Company, be vaultUIBackend) (views.EnvImportVM, error) {
	ctx := r.Context()
	base := "/c/" + co.Slug
	vm := views.EnvImportVM{Sections: settingsSections(ctx, base, "segredos"), BackHref: base + "/configuracoes/segredos",
		PostHref: base + "/configuracoes/segredos/importar-env", Mode: service.EnvImportItems}
	cols, err := be.ListCollections(ctx, co.ID)
	if err != nil {
		return vm, err
	}
	for _, c := range cols {
		if !c.IsDefault {
			vm.Collections = append(vm.Collections, c.Name)
		}
	}
	return vm, nil
}

func (s *server) handleEnvImportForm(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, ok := s.vaultBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	sh := s.settingsShell(r, co, i18n.T(r.Context(), "Importar .env"))
	if !sh.CanAdmin() {
		s.fail(w, r, &co, domain.ErrForbidden)
		return
	}
	vm, err := s.envImportVM(r, co, be)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.render(w, r, http.StatusOK, views.EnvImportPage(sh, vm))
}

// envImportInput lê o formulário. O conteúdo não é aparado: o .env vai como foi colado.
func envImportInput(r *http.Request) service.EnvImportInput {
	mode := service.EnvImportItems
	if r.PostFormValue("mode") == service.EnvImportSingleItem {
		mode = service.EnvImportSingleItem
	}
	return service.EnvImportInput{Content: r.PostFormValue("content"), Mode: mode, ItemName: trimForm(r, "item_name"),
		Collection: trimForm(r, "collection"), Overwrite: r.PostFormValue("overwrite") != ""}
}

// envPlanVM devolve a prévia sem valores: nomes de segredo e de campo e as linhas ignoradas, com o motivo.
func envPlanVM(ctx context.Context, p service.EnvImportPlan) *views.EnvImportPlanVM {
	out := &views.EnvImportPlanVM{Collection: p.Collection}
	if out.Collection == "" {
		out.Collection = i18n.T(ctx, "padrão")
	}
	for _, it := range p.Items {
		out.Rows = append(out.Rows, views.EnvImportRowVM{Secret: it.Secret, Fields: strings.Join(it.Fields, ", "), Exists: it.Exists})
	}
	for _, sk := range p.Skipped {
		out.Skipped = append(out.Skipped, i18n.T(ctx, "linha")+" "+strconv.Itoa(sk.Line)+": "+sk.Reason)
	}
	return out
}

func (s *server) renderEnvImport(w http.ResponseWriter, r *http.Request, co domain.Company, be vaultUIBackend, status int, in service.EnvImportInput,
	plan *service.EnvImportPlan, err error, needPassword bool) {
	ctx := r.Context()
	sh := s.settingsShell(r, co, i18n.T(ctx, "Importar .env"))
	vm, lerr := s.envImportVM(r, co, be)
	if lerr != nil {
		s.fail(w, r, &co, lerr)
		return
	}
	vm.Content, vm.Mode, vm.ItemName, vm.Collection, vm.Overwrite = in.Content, in.Mode, in.ItemName, in.Collection, in.Overwrite
	if plan != nil {
		vm.Plan = envPlanVM(ctx, *plan)
	}
	vm.NeedPassword = needPassword
	var re *domain.ReauthError
	switch {
	case needPassword && errors.As(err, &re):
		vm.Error = i18n.T(ctx, "O servidor pede uma prova recente de identidade: informe a senha da conta e grave de novo.")
	case needPassword:
		vm.Error = i18n.T(ctx, "Senha da conta incorreta.")
	case err != nil:
		vm.Error = vaultErrorMsg(ctx, err)
	}
	w.Header().Set("Cache-Control", "no-store")
	s.render(w, r, status, views.EnvImportPage(sh, vm))
}

// handleEnvImport faz as duas etapas do formulário: "previa" mostra o mapeamento sem gravar; "gravar" grava, com a senha
// da conta como prova de identidade quando o servidor a pede (ADR-0070).
func (s *server) handleEnvImport(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, ctx, ok := s.vaultReq(w, r)
	if !ok {
		return
	}
	in := envImportInput(r)
	plan, err := be.PreviewEnvImport(ctx, co.ID, in)
	if err != nil {
		s.renderEnvImport(w, r, co, be, statusFor(err), in, nil, err, false)
		return
	}
	if r.PostFormValue("etapa") != "gravar" {
		s.renderEnvImport(w, r, co, be, http.StatusOK, in, &plan, nil, false)
		return
	}
	if pw := r.PostFormValue("senha"); pw != "" {
		id, has := auth.IdentityFrom(ctx)
		if has && id.Via == auth.ViaSession {
			proof, perr := be.ReauthWithPassword(ctx, id, pw)
			if perr != nil {
				s.renderEnvImport(w, r, co, be, http.StatusUnauthorized, in, &plan, perr, true)
				return
			}
			id.Session.ReauthAt, id.Session.ReauthMethod = &proof.At, proof.Method
			ctx = auth.WithIdentity(ctx, id)
		}
	}
	if _, err := be.ImportEnv(ctx, actorFrom(ctx), co.ID, in); err != nil {
		var re *domain.ReauthError
		if errors.As(err, &re) {
			s.renderEnvImport(w, r, co, be, http.StatusUnauthorized, in, &plan, err, true)
			return
		}
		s.renderEnvImport(w, r, co, be, statusFor(err), in, &plan, err, false)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/configuracoes/segredos?ok=env")
}
