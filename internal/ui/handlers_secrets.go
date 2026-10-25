package ui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/settings"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Configurações › Segredos e Instância (ADR-0050, ADR-0051). A tela nunca recebe o valor de um segredo do service,
// exceto na resposta única de criar/rotacionar gerado e de revelar, que é renderizada direto (sem redirecionar,
// para o valor não ir parar em URL, histórico nem cache).

type secretsBackend interface {
	CreateSecret(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateSecretInput) (service.SecretView, error)
	RotateSecret(ctx context.Context, actor domain.Actor, companyID domain.ID, scope domain.SecretScope, name string, in service.RotateSecretInput) (service.SecretView, error)
	RevealSecret(ctx context.Context, actor domain.Actor, companyID domain.ID, scope domain.SecretScope, name string) (service.SecretView, error)
	DeleteSecret(ctx context.Context, actor domain.Actor, companyID domain.ID, scope domain.SecretScope, name string, force bool) error
	ListSecrets(ctx context.Context, companyID domain.ID, scope domain.SecretScope, withUsage bool) ([]service.SecretView, error)
}

type instanceBackend interface {
	ListInstanceSettings(ctx context.Context) ([]settings.Entry, error)
	SetInstanceSetting(ctx context.Context, actor domain.Actor, key string, raw json.RawMessage) (settings.Entry, error)
	UnsetInstanceSetting(ctx context.Context, actor domain.Actor, key string) (settings.Entry, error)
	ListInstanceSettingAudit(ctx context.Context, limit int) ([]service.InstanceSettingAudit, error)
	BlobStorageStatus(ctx context.Context) (service.BlobStatus, error)
}

func (s *server) secretsBE() (secretsBackend, bool) {
	b, ok := s.be.(secretsBackend)
	return b, ok
}

func (s *server) instanceBE() (instanceBackend, bool) {
	b, ok := s.be.(instanceBackend)
	return b, ok
}

var secretNotices = map[string]string{
	"criado":      i18n.N("Segredo criado."),
	"rotacionado": i18n.N("Segredo rotacionado: a versão subiu e quem o referencia já usa o valor novo na próxima execução."),
	"apagado":     i18n.N("Segredo apagado."),
	"env":         i18n.N("Import do .env concluído."),
	"cofre":       i18n.N("Arquivo do cofre importado."),
}

var instanceNotices = map[string]string{
	"salvo":  i18n.N("Configuração salva."),
	"padrao": i18n.N("Configuração de volta ao padrão."),
}

// settingTexts traduz o rótulo e a ajuda de cada chave de instance_settings; o texto pt-BR é o de settings.Defs
// (um teste confere que os dois coincidem).
var settingTexts = map[string][2]string{
	settings.ACPAllow:               {i18n.N("Liberar o harness ACP"), i18n.N("Personas acp executam comandos nesta máquina.")},
	settings.ACPCommands:            {i18n.N("Executáveis ACP permitidos"), i18n.N("Vazio = qualquer comando (com o harness liberado).")},
	settings.IntegrationsAllowStdio: {i18n.N("Liberar MCP stdio"), i18n.N("Servidores MCP stdio executam comandos nesta máquina.")},
	settings.IntegrationsAllowPriv:  {i18n.N("Liberar destinos internos"), i18n.N("Webhooks e testes para loopback e redes privadas.")},
	settings.ArtifactsMaxBytes:      {i18n.N("Tamanho máximo de artefato (bytes)"), ""},
	settings.ArtifactsQuotaBytes:    {i18n.N("Cota de artefatos por empresa (bytes)"), ""},
	settings.RunReserveUSD:          {i18n.N("Piso da reserva de orçamento por run (US$)"), ""},
	settings.AlertsSilentFactor:     {i18n.N("Fator do alerta de persona silenciosa"), ""},
	settings.AlertsRunStuckAfter:    {i18n.N("Tempo para uma run presa virar alerta"), ""},
	settings.ReauthMinutes:          {i18n.N("Janela da reautenticação recente (min)"), i18n.N("Quanto tempo uma prova de identidade libera as ações sensíveis do cofre (ADR-0070).")},
	settings.RateLimitEnabled: {i18n.N("Ligar o rate limit geral"),
		i18n.N("Token bucket por API key, usuário e IP para escritas, MCP e webhooks de entrada (429 + Retry-After).")},
	settings.RateLimitWrites:        {i18n.N("Escritas por minuto (por API key ou usuário)"), ""},
	settings.RateLimitIP:            {i18n.N("Escritas por minuto (por IP, somando identidades)"), ""},
	settings.RateLimitMCP:           {i18n.N("Chamadas MCP por minuto (por identidade ou run)"), ""},
	settings.RateLimitWebhooks:      {i18n.N("Webhooks de entrada por minuto (por IP)"), ""},
	settings.RateLimitBrowserErrors: {i18n.N("Erros de navegador por minuto (por IP)"), ""},
	settings.SecretsHistoryVersions: {i18n.N("Versões de histórico por campo"), i18n.N("Retenção do histórico de cada campo: as últimas N versões ou as de até D dias, o que guardar menos (ADR-0068 §3.2).")},
	settings.SecretsHistoryDays:     {i18n.N("Dias de histórico por campo"), ""},
}

func scopeOf(user bool) domain.SecretScope {
	if user {
		return domain.SecretScopeUser
	}
	return domain.SecretScopeCompany
}

func userScopeForm(r *http.Request) bool {
	return r.PostFormValue("escopo") == "usuario" || r.URL.Query().Get("escopo") == "usuario"
}

func secretRow(base string, v service.SecretView, user bool) views.SecretRowVM {
	seg := "/configuracoes/segredos/" + url.PathEscape(v.Name)
	q := ""
	if user {
		q = "?escopo=usuario"
	}
	ref := "${secret:" + v.Name + "}"
	if user {
		ref = "${secret:user/" + v.Name + "}"
	}
	row := views.SecretRowVM{Name: v.Name, Reference: ref, Description: v.Description, Provider: string(v.Provider), ExternalRef: v.ExternalRef,
		Version: v.Version, Revealed: v.RevealedAt != nil, LastUsed: "—",
		RotateHref: base + seg + "/rotacionar" + q, RevealHref: base + seg + "/revelar" + q, DeleteHref: base + seg + "/apagar" + q, FieldsHref: base + seg + "/campos" + q}
	if v.LastUsedAt != nil {
		row.LastUsed = v.LastUsedAt.Local().Format("02/01/2006 15:04")
	}
	for _, u := range v.UsedBy {
		row.UsedBy = append(row.UsedBy, u.Kind+":"+u.Slug)
	}
	return row
}

// secretsVM monta a página; reveal, quando não nulo, é o valor único que acabou de ser gerado/revelado.
func (s *server) secretsVM(r *http.Request, co domain.Company, be secretsBackend, reveal *views.SecretRevealVM) (views.SecretsVM, error) {
	ctx := r.Context()
	base := "/c/" + co.Slug
	vm := views.SecretsVM{Sections: settingsSections(ctx, base, "segredos"), KeySet: s.platform.SecretKeySet, VaultOn: s.platform.VaultConfigured,
		Notice: i18n.T(ctx, secretNotices[r.URL.Query().Get("ok")]), Reveal: reveal, CollectionsHref: base + "/configuracoes/colecoes", HealthHref: base + "/configuracoes/segredos/saude", ImportEnvHref: base + "/configuracoes/segredos/importar-env", VaultFileHref: base + "/configuracoes/segredos/cofre",
		Form:  views.SecretFormVM{Action: base + "/configuracoes/segredos", Provider: "local", FieldErr: map[string]string{}},
		UForm: views.SecretFormVM{Action: base + "/configuracoes/segredos?escopo=usuario", Provider: "local", User: true, FieldErr: map[string]string{}}}
	_, vm.UserOK = auth.IdentityFrom(ctx)
	list, err := be.ListSecrets(ctx, co.ID, domain.SecretScopeCompany, true)
	if err != nil {
		return vm, err
	}
	for _, v := range list {
		vm.Company = append(vm.Company, secretRow(base, v, false))
	}
	if vm.UserOK {
		ul, err := be.ListSecrets(ctx, co.ID, domain.SecretScopeUser, false)
		if err != nil {
			return vm, err
		}
		for _, v := range ul {
			vm.User = append(vm.User, secretRow(base, v, true))
		}
	}
	return vm, nil
}

func (s *server) renderSecrets(w http.ResponseWriter, r *http.Request, co domain.Company, status int, vm views.SecretsVM) {
	ctx := r.Context()
	sh := s.settingsShell(r, co, i18n.T(ctx, "Segredos"))
	w.Header().Set("Cache-Control", "no-store") // a página pode carregar um valor que só existe nesta resposta
	s.render(w, r, status, views.SecretsPage(sh, vm))
}

func (s *server) handleSecrets(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, ok := s.secretsBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	vm, err := s.secretsVM(r, co, be, nil)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.renderSecrets(w, r, co, http.StatusOK, vm)
}

// secretErrorMsg traduz o erro do service em texto para a página (nunca com o valor).
func secretErrorMsg(ctx context.Context, err error) string {
	var ve *domain.ValidationError
	if errors.As(err, &ve) {
		switch ve.Field {
		case "name":
			return i18n.T(ctx, "Nome inválido: comece com uma letra e use letras, números, ponto, hífen ou sublinhado (até 64 caracteres).")
		case "value":
			return i18n.T(ctx, "Informe o valor ou marque para gerar um (não os dois).")
		case "secrets":
			return i18n.T(ctx, "O servidor não tem HANKO_SECRET_KEY: não há como guardar segredos cifrados.")
		case "external_ref":
			return i18n.T(ctx, "Referência de cofre inválida: use caminho#campo.")
		case "provider":
			return i18n.T(ctx, "Nenhum cofre externo configurado neste servidor.")
		case "scope":
			return i18n.T(ctx, "Segredos pessoais exigem uma pessoa autenticada.")
		}
		return ve.Field + ": " + ve.Reason + "."
	}
	if errors.Is(err, domain.ErrConflict) {
		return i18n.T(ctx, "Conflito: já existe um segredo com esse nome, o valor desta versão já foi revelado, ou o segredo está em uso (marque apagar mesmo em uso).")
	}
	_, title, msg := friendly(ctx, err)
	return title + ". " + msg
}

func (s *server) secretsFail(w http.ResponseWriter, r *http.Request, co domain.Company, be secretsBackend, err error, form *views.SecretFormVM) {
	vm, lerr := s.secretsVM(r, co, be, nil)
	if lerr != nil {
		s.fail(w, r, &co, lerr)
		return
	}
	vm.Error = secretErrorMsg(r.Context(), err)
	if form != nil {
		if form.User {
			vm.UForm = *form
		} else {
			vm.Form = *form
		}
	}
	s.renderSecrets(w, r, co, statusFor(err), vm)
}

func (s *server) handleCreateSecret(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, ok := s.secretsBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	user := userScopeForm(r)
	base := "/c/" + co.Slug
	form := views.SecretFormVM{Action: base + "/configuracoes/segredos", Name: trimForm(r, "name"), Description: trimForm(r, "description"),
		Provider: trimForm(r, "provider"), ExternalRef: trimForm(r, "external_ref"), Generate: r.PostFormValue("generate") != "", User: user,
		FieldErr: map[string]string{}} // o valor digitado nunca volta para o formulário
	if user {
		form.Action += "?escopo=usuario"
	}
	gen, gerr := genOptsFromForm(r)
	if gerr != nil {
		s.secretsFail(w, r, co, be, gerr, &form)
		return
	}
	in := service.CreateSecretInput{Scope: scopeOf(user), Name: form.Name, Description: form.Description, Provider: domain.SecretProvider(form.Provider),
		Value: r.PostFormValue("value"), Generate: form.Generate, Gen: gen, ExternalRef: form.ExternalRef}
	if in.Provider == domain.SecretProviderVault {
		in.Value, in.Generate = "", false
	}
	v, err := be.CreateSecret(ctx, actorFrom(ctx), co.ID, in)
	if err != nil {
		s.secretsFail(w, r, co, be, err, &form)
		return
	}
	s.afterSecretWrite(w, r, co, be, v, user, "criado")
}

// afterSecretWrite mostra o valor gerado na própria resposta (uma vez) ou redireciona com o aviso.
func (s *server) afterSecretWrite(w http.ResponseWriter, r *http.Request, co domain.Company, be secretsBackend, v service.SecretView, user bool, ok string) {
	if v.RevealedValue == "" {
		redirect(w, r, "/c/"+co.Slug+"/configuracoes/segredos?ok="+ok)
		return
	}
	vm, err := s.secretsVM(r, co, be, &views.SecretRevealVM{Name: v.Name, Reference: secretRef(v.Name, user), Value: v.RevealedValue})
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.renderSecrets(w, r, co, http.StatusOK, vm)
}

func secretRef(name string, user bool) string {
	if user {
		return "${secret:user/" + name + "}"
	}
	return "${secret:" + name + "}"
}

func (s *server) handleRotateSecret(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, ok := s.secretsBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	user, name := userScopeForm(r), r.PathValue("name")
	gen, gerr := genOptsFromForm(r)
	if gerr != nil {
		s.secretsFail(w, r, co, be, gerr, nil)
		return
	}
	in := service.RotateSecretInput{Provider: domain.SecretProvider(trimForm(r, "provider")), Value: r.PostFormValue("value"),
		Generate: r.PostFormValue("generate") != "", Gen: gen, ExternalRef: trimForm(r, "external_ref")}
	if in.Provider == domain.SecretProviderVault {
		in.Value, in.Generate = "", false
	}
	v, err := be.RotateSecret(ctx, actorFrom(ctx), co.ID, scopeOf(user), name, in)
	if err != nil {
		s.secretsFail(w, r, co, be, err, nil)
		return
	}
	s.afterSecretWrite(w, r, co, be, v, user, "rotacionado")
}

func (s *server) handleRevealSecret(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, ok := s.secretsBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	user := userScopeForm(r)
	v, err := be.RevealSecret(ctx, actorFrom(ctx), co.ID, scopeOf(user), r.PathValue("name"))
	if err != nil {
		s.secretsFail(w, r, co, be, err, nil)
		return
	}
	s.afterSecretWrite(w, r, co, be, v, user, "")
}

func (s *server) handleDeleteSecret(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, ok := s.secretsBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	user := userScopeForm(r)
	if err := be.DeleteSecret(ctx, actorFrom(ctx), co.ID, scopeOf(user), r.PathValue("name"), r.PostFormValue("force") != ""); err != nil {
		s.secretsFail(w, r, co, be, err, nil)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/configuracoes/segredos?ok=apagado")
}

// ---------- Instância ----------

func groupTitle(ctx context.Context, g string) string {
	switch g {
	case "acp":
		return i18n.T(ctx, "Harness ACP")
	case "integrations":
		return i18n.T(ctx, "Integrações")
	case "artifacts":
		return i18n.T(ctx, "Artefatos")
	case "run":
		return i18n.T(ctx, "Execuções")
	case "alerts":
		return i18n.T(ctx, "Alertas")
	case "ratelimit":
		return i18n.T(ctx, "Limite de taxa")
	case "secrets":
		return i18n.T(ctx, "Segredos")
	}
	return i18n.T(ctx, "Experimentais")
}

func settingSource(ctx context.Context, src settings.Source) string {
	switch src {
	case settings.FromEnv:
		return i18n.T(ctx, "ambiente")
	case settings.FromDB:
		return i18n.T(ctx, "banco")
	}
	return i18n.T(ctx, "padrão")
}

func (s *server) instanceVM(r *http.Request, co domain.Company, be instanceBackend) views.InstanceVM {
	ctx := r.Context()
	base := "/c/" + co.Slug
	vm := views.InstanceVM{Sections: settingsSections(ctx, base, "instancia"), Notice: i18n.T(ctx, instanceNotices[r.URL.Query().Get("ok")])}
	entries, err := be.ListInstanceSettings(ctx)
	if err != nil {
		if errors.Is(err, domain.ErrNotAllowed) {
			vm.Forbidden = true
			return vm
		}
		vm.Error = secretErrorMsg(ctx, err)
		return vm
	}
	idx := map[string]int{}
	for _, e := range entries {
		gi, seen := idx[e.Group]
		if !seen {
			gi = len(vm.Groups)
			idx[e.Group] = gi
			vm.Groups = append(vm.Groups, views.SettingGroupVM{Title: groupTitle(ctx, e.Group)})
		}
		label, help := e.Label, e.Help
		if tx, ok := settingTexts[e.Key]; ok {
			label, help = tx[0], tx[1]
		}
		row := views.SettingRowVM{Key: e.Key, Label: i18n.T(ctx, label), Help: i18n.T(ctx, help), Kind: string(e.Kind), Value: settings.Display(e.Value),
			Input: settings.Display(e.Value), Source: string(e.Source), SourceTxt: settingSource(ctx, e.Source), Env: e.Env, Locked: e.Locked, Stored: e.Stored != nil,
			Action: base + "/configuracoes/instancia/" + url.PathEscape(e.Key), ClearHref: base + "/configuracoes/instancia/" + url.PathEscape(e.Key) + "/padrao"}
		if b, ok := e.Value.(bool); ok {
			row.BoolOn = b
		}
		if l, ok := e.Value.([]string); ok {
			row.Input = strings.Join(l, ",")
		}
		vm.Groups[gi].Rows = append(vm.Groups[gi].Rows, row)
	}
	if st, err := be.BlobStorageStatus(ctx); err == nil {
		for _, u := range st.Usage {
			vm.Blobs = append(vm.Blobs, views.BlobUsageVM{Driver: u.Driver, Versions: u.Versions, Size: fmtBytes(ctx, u.Bytes), Active: u.Driver == st.Active})
		}
		have := map[string]bool{}
		for _, b := range vm.Blobs {
			have[b.Driver] = true
		}
		for _, n := range st.Available {
			if !have[n] {
				vm.Blobs = append(vm.Blobs, views.BlobUsageVM{Driver: n, Size: fmtBytes(ctx, 0), Active: n == st.Active})
			}
		}
		vm.BlobNote = i18n.Tf(ctx, "Driver ativo para novos arquivos: %s. A escolha é do operador (HANKO_BLOB_DRIVER); para mover o que já existe use `hanko blobs migrate --from … --to …`.", st.Active)
	}
	if audit, err := be.ListInstanceSettingAudit(ctx, 30); err == nil {
		for _, a := range audit {
			who := a.ActorType
			vm.Audit = append(vm.Audit, views.SettingAuditRowVM{When: a.OccurredAt.Local().Format("02/01/2006 15:04"), Key: a.Key, Old: rawOrDash(a.Old), New: rawOrDash(a.New), Actor: who})
		}
	}
	return vm
}

func rawOrDash(b json.RawMessage) string {
	if len(b) == 0 || string(b) == "null" {
		return "—"
	}
	return string(b)
}

func (s *server) renderInstance(w http.ResponseWriter, r *http.Request, co domain.Company, status int, vm views.InstanceVM) {
	sh := s.settingsShell(r, co, i18n.T(r.Context(), "Instância"))
	s.render(w, r, status, views.InstancePage(sh, vm))
}

func (s *server) handleInstance(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, ok := s.instanceBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	s.renderInstance(w, r, co, http.StatusOK, s.instanceVM(r, co, be))
}

func (s *server) handleSetInstanceSetting(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, ok := s.instanceBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	key := r.PathValue("key")
	def, known := settings.Lookup(key)
	if !known {
		s.fail(w, r, &co, domain.Invalid("key", "unknown setting"))
		return
	}
	raw, err := settings.FromText(def, r.PostFormValue("value"))
	if err == nil {
		_, err = be.SetInstanceSetting(ctx, actorFrom(ctx), key, raw)
	} else {
		err = domain.Invalid(key, err.Error())
	}
	if err != nil {
		vm := s.instanceVM(r, co, be)
		vm.Error = secretErrorMsg(ctx, err)
		s.renderInstance(w, r, co, statusFor(err), vm)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/configuracoes/instancia?ok=salvo")
}

func (s *server) handleUnsetInstanceSetting(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, ok := s.instanceBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	if _, err := be.UnsetInstanceSetting(ctx, actorFrom(ctx), r.PathValue("key")); err != nil {
		vm := s.instanceVM(r, co, be)
		vm.Error = secretErrorMsg(ctx, err)
		s.renderInstance(w, r, co, statusFor(err), vm)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/configuracoes/instancia?ok=padrao")
}
