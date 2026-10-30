package ui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/export"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Telas do ADR-0057: ciclo de vida da empresa, pedidos de entrada, login da CLI, contas da instalação e import.

// lifeBackend é o que a UI usa do service para essas telas. Fica fora do Backend para que os fakes antigos
// continuem valendo: o servidor só o recebe quando o backend o implementa (o *service.Service sempre implementa).
type lifeBackend interface {
	UpdateCompany(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.UpdateCompanyInput) (domain.Company, error)
	ArchiveCompany(ctx context.Context, actor domain.Actor, companyID domain.ID) (domain.Company, error)
	RestoreCompany(ctx context.Context, actor domain.Actor, companyID domain.ID, resume bool) (domain.Company, error)
	DeleteCompany(ctx context.Context, actor domain.Actor, companyID domain.ID, confirm string) (service.CompanyDeletion, error)
	ImportCompany(ctx context.Context, actor domain.Actor, doc export.Document, opts service.ImportOptions) (service.ImportResult, error)
	JoinLinkState(ctx context.Context, companyID domain.ID) (domain.JoinLink, error)
	CreateJoinLink(ctx context.Context, actor domain.Actor, companyID domain.ID) (string, domain.JoinLink, error)
	RevokeJoinLink(ctx context.Context, actor domain.Actor, companyID domain.ID) error
	ListJoinRequests(ctx context.Context, companyID domain.ID, onlyPending bool) ([]domain.JoinRequest, error)
	DecideJoinRequest(ctx context.Context, actor domain.Actor, companyID, requestID domain.ID, approve bool, role domain.MemberRole) (domain.JoinRequest, error)
	JoinLinkInfo(ctx context.Context, token string) (domain.Company, error)
	RequestJoin(ctx context.Context, userID domain.ID, token, message string) (domain.JoinRequest, error)
	GetCLIAuth(ctx context.Context, code string) (domain.CLIAuthRequest, error)
	DecideCLIAuth(ctx context.Context, userID domain.ID, code string, approve bool) (domain.CLIAuthRequest, error)
	ListInstanceUsers(ctx context.Context) ([]domain.InstanceUser, error)
}

var _ lifeBackend = (*service.Service)(nil)

// conflictText devolve a frase pt-BR do service de um ErrConflict (sem o sufixo ": conflict") ou "".
func conflictText(err error) string {
	if !errors.Is(err, domain.ErrConflict) || errors.Is(err, domain.ErrCompanyArchived) {
		return ""
	}
	msg := strings.TrimSuffix(err.Error(), ": "+domain.ErrConflict.Error())
	if i := strings.LastIndex(msg, ": "); i >= 0 && strings.Contains(msg[i:], "conflict") {
		msg = msg[:i]
	}
	if msg == "" || msg == domain.ErrConflict.Error() {
		return ""
	}
	r := []rune(msg)
	r[0] = []rune(strings.ToUpper(string(r[0])))[0]
	return string(r) + "."
}

// lifeErrText é o texto de erro de um formulário: a frase do service para conflitos conhecidos, senão a mensagem amigável.
func lifeErrText(ctx context.Context, err error) string {
	if t := conflictText(err); t != "" {
		return t
	}
	_, title, msg := friendly(ctx, err)
	return title + ". " + msg
}

func (s *server) needLife(w http.ResponseWriter, r *http.Request, co *domain.Company) bool {
	if s.life == nil {
		s.fail(w, r, co, service.ErrNotImplemented)
		return false
	}
	return true
}

// ---------- cartão "Empresa" das Configurações ----------

func (s *server) companyAdminVM(r *http.Request, co domain.Company, pre views.CompanyAdminVM) views.CompanyAdminVM {
	if s.life == nil {
		return views.CompanyAdminVM{}
	}
	ctx := r.Context()
	role := views.RoleFrom(ctx)
	vm := views.CompanyAdminVM{Show: true, Name: co.Name, Slug: co.Slug, Description: co.Description, Timezone: co.Timezone, Prefix: co.TaskPrefix,
		Archived: co.Archived(), CanAdmin: role.AtLeast(domain.MemberAdmin), CanOwner: role.AtLeast(domain.MemberOwner),
		Error: pre.Error, Notice: pre.Notice}
	if co.ArchivedAt != nil {
		vm.ArchivedAt = relTimeCtx(ctx, *co.ArchivedAt, s.now(), time.UTC)
	}
	if ts, err := s.be.ListTasks(ctx, co.ID, service.TaskFilter{Limit: 1}); err == nil && len(ts) > 0 {
		vm.HasTasks = true
	}
	return vm
}

func (s *server) companyFail(w http.ResponseWriter, r *http.Request, co domain.Company, err error) {
	st, _, _ := friendly(r.Context(), err)
	if st >= 500 || st == http.StatusNotFound {
		s.fail(w, r, &co, err)
		return
	}
	vm, verr := s.settingsVM(r)
	if verr != nil {
		s.fail(w, r, &co, verr)
		return
	}
	vm.Company.Error = lifeErrText(r.Context(), err)
	s.renderSettings(w, r, co, st, vm)
}

func (s *server) handleCompanyUpdate(w http.ResponseWriter, r *http.Request, co domain.Company) {
	if !s.needLife(w, r, &co) {
		return
	}
	in := service.UpdateCompanyInput{}
	if v := trimForm(r, "name"); v != "" {
		in.Name = &v
	}
	if v := trimForm(r, "timezone"); v != "" {
		in.Timezone = &v
	}
	desc := trimForm(r, "description")
	in.Description = &desc
	if v := strings.ToUpper(trimForm(r, "prefix")); v != "" && v != co.TaskPrefix {
		in.TaskPrefix = &v
	}
	if _, err := s.life.UpdateCompany(r.Context(), actorFrom(r.Context()), co.ID, in); err != nil {
		s.companyFail(w, r, co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/configuracoes#empresa")
}

func (s *server) handleCompanyArchive(w http.ResponseWriter, r *http.Request, co domain.Company) {
	if !s.needLife(w, r, &co) {
		return
	}
	if _, err := s.life.ArchiveCompany(r.Context(), actorFrom(r.Context()), co.ID); err != nil {
		s.companyFail(w, r, co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/configuracoes#empresa")
}

func (s *server) handleCompanyRestore(w http.ResponseWriter, r *http.Request, co domain.Company) {
	if !s.needLife(w, r, &co) {
		return
	}
	if _, err := s.life.RestoreCompany(r.Context(), actorFrom(r.Context()), co.ID, r.PostFormValue("resume") != ""); err != nil {
		s.companyFail(w, r, co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/configuracoes#empresa")
}

func (s *server) handleCompanyDelete(w http.ResponseWriter, r *http.Request, co domain.Company) {
	if !s.needLife(w, r, &co) {
		return
	}
	if _, err := s.life.DeleteCompany(r.Context(), actorFrom(r.Context()), co.ID, trimForm(r, "confirm")); err != nil {
		s.companyFail(w, r, co, err)
		return
	}
	redirect(w, r, "/")
}

// ---------- pedidos de entrada: lado do admin ----------

// joinVM monta o link e a fila de pedidos da seção Membros (admin).
func (s *server) joinVM(ctx context.Context, co domain.Company, roles []views.RoleOption) views.JoinVM {
	if s.life == nil {
		return views.JoinVM{}
	}
	vm := views.JoinVM{Show: true}
	if st, err := s.life.JoinLinkState(ctx, co.ID); err == nil {
		vm.Enabled = st.Enabled
	}
	rs, err := s.life.ListJoinRequests(ctx, co.ID, false)
	if err != nil {
		return vm
	}
	now := s.now()
	for _, rq := range rs {
		row := views.JoinRequestRow{ID: rq.ID.String(), Name: firstNonEmpty(rq.Name, rq.Email), Email: rq.Email, Message: rq.Message,
			Status: string(rq.Status), When: relTimeCtx(ctx, rq.CreatedAt, now, time.UTC)}
		if rq.Role != nil {
			row.Role = memberRoleLabel(ctx, *rq.Role)
		}
		vm.Requests = append(vm.Requests, row)
	}
	return vm
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func (s *server) joinFail(w http.ResponseWriter, r *http.Request, co domain.Company, err error) {
	st, _, _ := friendly(r.Context(), err)
	if st >= 500 || st == http.StatusNotFound {
		s.fail(w, r, &co, err)
		return
	}
	vm, verr := s.settingsVM(r)
	if verr != nil {
		s.fail(w, r, &co, verr)
		return
	}
	vm.Members.Join.Error = lifeErrText(r.Context(), err)
	s.renderSettings(w, r, co, st, vm)
}

func (s *server) joinBase(r *http.Request) string {
	scheme := "http"
	if s.secure || r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (s *server) handleJoinLinkCreate(w http.ResponseWriter, r *http.Request, co domain.Company) {
	if !s.requireAuth(w, r, co) || !s.needLife(w, r, &co) {
		return
	}
	tok, _, err := s.life.CreateJoinLink(r.Context(), actorFrom(r.Context()), co.ID)
	if err != nil {
		s.joinFail(w, r, co, err)
		return
	}
	vm, err := s.settingsVM(r)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm.Members.Join.NewLink = s.joinBase(r) + auth.JoinPath + url.PathEscape(tok)
	s.renderSettings(w, r, co, http.StatusCreated, vm)
}

func (s *server) handleJoinLinkOff(w http.ResponseWriter, r *http.Request, co domain.Company) {
	if !s.requireAuth(w, r, co) || !s.needLife(w, r, &co) {
		return
	}
	if err := s.life.RevokeJoinLink(r.Context(), actorFrom(r.Context()), co.ID); err != nil {
		s.joinFail(w, r, co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/configuracoes#pedidos")
}

func (s *server) handleJoinDecide(w http.ResponseWriter, r *http.Request, co domain.Company) {
	if !s.requireAuth(w, r, co) || !s.needLife(w, r, &co) {
		return
	}
	id, err := domain.ParseID(r.PathValue("request"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	approve := r.PostFormValue("decision") == "approve"
	if _, err := s.life.DecideJoinRequest(r.Context(), actorFrom(r.Context()), co.ID, id, approve, domain.MemberRole(trimForm(r, "role"))); err != nil {
		s.joinFail(w, r, co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/configuracoes#pedidos")
}

// ---------- rotas públicas e de sessão ----------

func (s *server) accessRoutes(mux handlerRegistry) {
	if s.auth == nil || s.life == nil {
		return
	}
	mux.HandleFunc("GET "+auth.JoinPath+"{token}", s.handleJoinPage)
	mux.HandleFunc("POST "+auth.JoinPath+"{token}", s.sameOrigin(s.handleJoinSubmit))
	mux.HandleFunc("GET "+auth.CLIAuthPath+"{code}", s.handleCLIAuthPage)
	mux.HandleFunc("POST "+auth.CLIAuthPath+"{code}", s.sameOrigin(s.handleCLIAuthDecide))
	mux.HandleFunc("GET /admin/usuarios", s.handleUsers)
}

// loginRedirect manda quem não tem sessão para o login, voltando para esta página.
func loginRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, auth.LoginPath+"?next="+url.QueryEscape(r.URL.Path), http.StatusSeeOther)
}

func (s *server) joinInvalid(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusNotFound, views.JoinPage(views.JoinPageVM{Version: s.version, State: "invalid"}))
}

// resolveJoin vê o link e quem está logado. Falha de token conta no limitador por IP.
func (s *server) resolveJoin(w http.ResponseWriter, r *http.Request) (domain.Company, domain.User, bool) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	me, ok := auth.IdentityFrom(r.Context())
	if !ok {
		loginRedirect(w, r)
		return domain.Company{}, domain.User{}, false
	}
	ip := s.auth.Proxies.ClientIP(r)
	if ok, wait := s.auth.Limiter.Allow(ip, ""); !ok {
		w.Header().Set("Retry-After", itoa(int(wait.Seconds())+1))
		s.render(w, r, http.StatusTooManyRequests, views.JoinPage(views.JoinPageVM{Version: s.version, State: "invalid"}))
		return domain.Company{}, domain.User{}, false
	}
	co, err := s.life.JoinLinkInfo(r.Context(), r.PathValue("token"))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			s.auth.Limiter.Fail(ip, "")
			s.joinInvalid(w, r)
			return domain.Company{}, domain.User{}, false
		}
		s.fail(w, r, nil, err)
		return domain.Company{}, domain.User{}, false
	}
	return co, me.User, true
}

func (s *server) handleJoinPage(w http.ResponseWriter, r *http.Request) {
	co, u, ok := s.resolveJoin(w, r)
	if !ok {
		return
	}
	vm := views.JoinPageVM{Version: s.version, Token: r.PathValue("token"), Company: co.Name, Account: u.Email, State: "request"}
	if _, err := s.be.MyRole(r.Context(), co.ID); err == nil || u.Role == domain.RoleOwner {
		vm.State = "member"
	}
	s.render(w, r, http.StatusOK, views.JoinPage(vm))
}

func (s *server) handleJoinSubmit(w http.ResponseWriter, r *http.Request) {
	co, u, ok := s.resolveJoin(w, r)
	if !ok {
		return
	}
	vm := views.JoinPageVM{Version: s.version, Token: r.PathValue("token"), Company: co.Name, Account: u.Email, State: "request", Message: trimForm(r, "message")}
	if _, err := s.life.RequestJoin(r.Context(), u.ID, vm.Token, vm.Message); err != nil {
		switch {
		case errors.Is(err, domain.ErrNotFound):
			s.joinInvalid(w, r)
		case errors.Is(err, domain.ErrConflict):
			vm.State = "member"
			if t := conflictText(err); t != "" && strings.Contains(err.Error(), "pedido aberto") {
				vm.State, vm.Error = "request", t
			}
			s.render(w, r, http.StatusConflict, views.JoinPage(vm))
		case errors.Is(err, domain.ErrInvalid):
			vm.Error = lifeErrText(r.Context(), err)
			s.render(w, r, http.StatusUnprocessableEntity, views.JoinPage(vm))
		default:
			s.fail(w, r, nil, err)
		}
		return
	}
	vm.State = "sent"
	s.render(w, r, http.StatusOK, views.JoinPage(vm))
}

// ---------- /cli-auth/<código> ----------

func keyScopeLabel(ctx context.Context, sc domain.KeyScope) string {
	if sc == domain.KeyScopeRead {
		return i18n.T(ctx, "Só leitura (consultar, sem alterar nada)")
	}
	return i18n.T(ctx, "Completo (faz o mesmo que você)")
}

func (s *server) cliAuthVM(r *http.Request, code string) (views.CLIAuthVM, bool) {
	vm := views.CLIAuthVM{Version: s.version, Code: code, State: "invalid"}
	req, err := s.life.GetCLIAuth(r.Context(), code)
	if err != nil {
		return vm, false
	}
	vm.Device, vm.Scope, vm.State = req.Device, keyScopeLabel(r.Context(), req.Scope), "confirm"
	if me, ok := auth.IdentityFrom(r.Context()); ok {
		vm.Account = me.User.Email
	}
	return vm, true
}

func (s *server) handleCLIAuthPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	if _, ok := auth.IdentityFrom(r.Context()); !ok {
		loginRedirect(w, r)
		return
	}
	ip := s.auth.Proxies.ClientIP(r)
	if ok, wait := s.auth.Limiter.Allow(ip, ""); !ok {
		w.Header().Set("Retry-After", itoa(int(wait.Seconds())+1))
		s.render(w, r, http.StatusTooManyRequests, views.CLIAuthPage(views.CLIAuthVM{Version: s.version, State: "invalid"}))
		return
	}
	vm, ok := s.cliAuthVM(r, r.PathValue("code"))
	if !ok {
		s.auth.Limiter.Fail(ip, "")
		s.render(w, r, http.StatusNotFound, views.CLIAuthPage(vm))
		return
	}
	s.render(w, r, http.StatusOK, views.CLIAuthPage(vm))
}

func (s *server) handleCLIAuthDecide(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	me, ok := auth.IdentityFrom(r.Context())
	if !ok || me.Via != auth.ViaSession { // só uma pessoa logada no navegador autoriza; chave de API não
		loginRedirect(w, r)
		return
	}
	ip := s.auth.Proxies.ClientIP(r)
	if ok, wait := s.auth.Limiter.Allow(ip, ""); !ok {
		w.Header().Set("Retry-After", itoa(int(wait.Seconds())+1))
		s.render(w, r, http.StatusTooManyRequests, views.CLIAuthPage(views.CLIAuthVM{Version: s.version, State: "invalid"}))
		return
	}
	code := r.PathValue("code")
	vm, ok := s.cliAuthVM(r, code)
	if !ok {
		s.auth.Limiter.Fail(ip, "")
		s.render(w, r, http.StatusNotFound, views.CLIAuthPage(vm))
		return
	}
	approve := r.PostFormValue("decision") == "approve"
	if _, err := s.life.DecideCLIAuth(r.Context(), me.User.ID, code, approve); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			vm.State = "invalid"
			s.render(w, r, http.StatusNotFound, views.CLIAuthPage(vm))
			return
		}
		s.fail(w, r, nil, err)
		return
	}
	vm.State = "denied"
	if approve {
		vm.State = "approved"
	}
	s.render(w, r, http.StatusOK, views.CLIAuthPage(vm))
}

// ---------- /admin/usuarios ----------

func (s *server) handleUsers(w http.ResponseWriter, r *http.Request) {
	us, err := s.life.ListInstanceUsers(r.Context())
	if err != nil {
		s.fail(w, r, nil, err)
		return
	}
	vm := views.UsersVM{Version: s.version, Home: "/"}
	me, _ := auth.IdentityFrom(r.Context())
	now := s.now()
	for _, u := range us {
		row := views.UserRow{Name: u.User.DisplayName(), Email: u.User.Email, Role: roleLabel(r.Context(), u.User.Role), Provider: string(u.User.Provider),
			Companies: u.Companies, Keys: u.ActiveKeys, LastLogin: i18n.T(r.Context(), "nunca"), Self: u.User.ID == me.User.ID}
		if u.User.LastLoginAt != nil {
			row.LastLogin = relTimeCtx(r.Context(), *u.User.LastLoginAt, now, time.UTC)
		}
		vm.Users = append(vm.Users, row)
	}
	s.render(w, r, http.StatusOK, views.UsersPage(vm))
}

// ---------- /importar ----------

const (
	importCacheTTL = 15 * time.Minute
	importCacheMax = 8
	// maxImportUpload é o teto do arquivo enviado pela tela (o mesmo do endpoint da API).
	maxImportUpload = 32 << 20
)

type importEntry struct {
	doc  export.Document
	user domain.ID
	at   time.Time
}

// importCache guarda por alguns minutos o arquivo enviado, para a prévia, a troca de ações e a confirmação não
// exigirem reenviar um arquivo de dezenas de MiB. Fica em memória, por usuário, com teto de entradas.
type importCache struct {
	mu sync.Mutex
	m  map[string]importEntry
}

func (c *importCache) put(user domain.ID, doc export.Document, now time.Time) (string, error) {
	tok, _, err := auth.NewInviteToken()
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]importEntry{}
	}
	for k, e := range c.m {
		if now.Sub(e.at) > importCacheTTL {
			delete(c.m, k)
		}
	}
	for len(c.m) >= importCacheMax { // descarta o mais antigo
		oldest, oldestAt := "", now
		for k, e := range c.m {
			if oldest == "" || e.at.Before(oldestAt) {
				oldest, oldestAt = k, e.at
			}
		}
		delete(c.m, oldest)
	}
	c.m[tok] = importEntry{doc: doc, user: user, at: now}
	return tok, nil
}

func (c *importCache) get(tok string, user domain.ID, now time.Time) (export.Document, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[tok]
	if !ok || e.user != user { // de outra pessoa: não vale, e quem tentou não apaga a entrada do dono
		return export.Document{}, false
	}
	if now.Sub(e.at) > importCacheTTL {
		delete(c.m, tok)
		return export.Document{}, false
	}
	return e.doc, true
}

func (c *importCache) drop(tok string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, tok)
}

func (s *server) importCompanies(ctx context.Context) []views.ImportCompanyOption {
	cs, err := s.visibleCompanies(ctx)
	if err != nil {
		return nil
	}
	out := make([]views.ImportCompanyOption, 0, len(cs))
	for _, c := range cs {
		if !c.Archived() {
			out = append(out, views.ImportCompanyOption{Slug: c.Slug, Name: c.Name})
		}
	}
	return out
}

func (s *server) importVM(r *http.Request) views.ImportVM {
	return views.ImportVM{Version: s.version, Companies: s.importCompanies(r.Context())}
}

func (s *server) handleImportPage(w http.ResponseWriter, r *http.Request) {
	if !s.needLife(w, r, nil) {
		return
	}
	s.render(w, r, http.StatusOK, views.ImportPage(s.importVM(r)))
}

// importOptsFrom lê o formulário: destino, ação padrão, exceções por item e o "manter ligado".
func (s *server) importOptsFrom(r *http.Request) (service.ImportOptions, string, error) {
	opts := service.ImportOptions{Slug: strings.TrimSpace(r.PostFormValue("slug")), Active: r.PostFormValue("active") != "",
		OnConflict: domain.ConflictAction(r.PostFormValue("on_conflict"))}
	if opts.OnConflict != "" && !opts.OnConflict.Valid() {
		return opts, "", domain.Invalid("on_conflict", "want skip, rename or replace")
	}
	for k, vals := range r.PostForm {
		kr, ok := strings.CutPrefix(k, "r:")
		if !ok || len(vals) == 0 {
			continue
		}
		act := domain.ConflictAction(vals[0])
		if !act.Valid() {
			return opts, "", domain.Invalid("resolve", "unknown action")
		}
		if opts.Resolve == nil {
			opts.Resolve = map[string]domain.ConflictAction{}
		}
		opts.Resolve[kr] = act
	}
	into := strings.TrimSpace(r.PostFormValue("into"))
	if into != "" {
		co, err := s.be.GetCompany(r.Context(), into)
		if err != nil {
			return opts, into, err
		}
		id := co.ID
		opts.Into = &id
	}
	return opts, into, nil
}

// importWarningFormats são os formatos pt-BR dos avisos que o service devolve (import_merge_plan.go). Ficam marcados
// aqui, com i18n.N, para entrarem no catálogo; a tela e a CLI os traduzem em tempo de execução.
var importWarningFormats = []string{
	i18n.N("%d workspace(s) chegam sem caminho (o caminho do arquivo não vale nesta instalação); ajuste depois"),
	i18n.N("%d segredo(s) ${secret:…} citados nas personas precisam ser criados na empresa de destino"),
	i18n.N("%d integração(ões) entram desligadas e sem segredos; ligue depois de configurar"),
	i18n.N("%d persona(s) entram pausadas até alguém liberar"),
	i18n.N("%d fluxo(s) entram desligados"),
	i18n.N("%d item(ns) de histórico (tasks, execuções, artefatos) não entram numa empresa existente; só a configuração"),
}

var _ = importWarningFormats // só para o catálogo

// importCountLabels são os rótulos das contagens da prévia (as chaves vêm do service).
var importCountLabels = map[string]string{
	"workspaces": i18n.N("Workspaces"), "projects": i18n.N("Projetos"), "subscriptions": i18n.N("Assinaturas"), "personas": i18n.N("Personas"),
	"tasks": i18n.N("Tasks"), "comments": i18n.N("Comentários"), "flows": i18n.N("Fluxos"), "runs": i18n.N("Execuções"),
	"run_events": i18n.N("Eventos de execução"), "skills": i18n.N("Skills"), "integrations": i18n.N("Integrações"),
	"artifacts": i18n.N("Artefatos"), "workspace_paths_cleared": i18n.N("Workspaces sem caminho"), "secret_refs_to_create": i18n.N("Segredos a recriar"),
	"persona_tags": i18n.N("Tags de persona"),
}

func importCountLabel(ctx context.Context, k string) string {
	if l, ok := importCountLabels[k]; ok {
		return i18n.T(ctx, l)
	}
	return k
}

func importActionLabel(ctx context.Context, a string) string {
	switch a {
	case "create":
		return i18n.T(ctx, "criar")
	case "skip":
		return i18n.T(ctx, "pular")
	case "rename":
		return i18n.T(ctx, "renomear")
	case "replace":
		return i18n.T(ctx, "substituir")
	}
	return a
}

func (s *server) previewVM(ctx context.Context, res service.ImportResult, tok, into string, opts service.ImportOptions) *views.ImportPreviewVM {
	pv := &views.ImportPreviewVM{Token: tok, Into: into, Slug: opts.Slug, Active: opts.Active, Default: string(opts.OnConflict)}
	if res.Merged {
		pv.Title = i18n.Tf(ctx, "Mesclar a configuração em %s", res.Company.Name)
	} else {
		pv.Title = i18n.Tf(ctx, "Criar a empresa %s (%s)", res.Company.Name, res.Company.Slug)
	}
	keys := make([]string, 0, len(res.Counts))
	for k := range res.Counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		pv.Counts = append(pv.Counts, views.ImportCount{Label: importCountLabel(ctx, k), N: res.Counts[k]})
	}
	if res.Merged {
		for _, it := range res.Items {
			row := views.ImportItemRow{Kind: it.Kind, Key: it.Key, Name: it.Name, Action: string(it.Action),
				ActionLabel: importActionLabel(ctx, string(it.Action)), Target: it.Target, Conflict: it.Action != domain.ItemCreate}
			if row.Conflict {
				pv.Conflict++
			}
			pv.Items = append(pv.Items, row)
		}
	}
	for _, w := range res.Warnings {
		pv.Warnings = append(pv.Warnings, views.ImportWarningRow{Text: i18n.Tf(ctx, w.Message, w.Count)})
	}
	return pv
}

// importFail re-renderiza a tela com o erro de validação do arquivo ou do destino.
func (s *server) importFail(w http.ResponseWriter, r *http.Request, vm views.ImportVM, err error) {
	st := statusFor(err)
	if st >= 500 && st != http.StatusNotImplemented {
		s.fail(w, r, nil, err)
		return
	}
	vm.Error = lifeErrText(r.Context(), err)
	s.render(w, r, st, views.ImportPage(vm))
}

func (s *server) handleImportUpload(w http.ResponseWriter, r *http.Request) {
	if !s.needLife(w, r, nil) {
		return
	}
	vm := s.importVM(r)
	r.Body = http.MaxBytesReader(w, r.Body, maxImportUpload+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		vm.Error = i18n.T(r.Context(), "Não consegui ler o arquivo enviado (ou ele passa do limite de 32 MiB).")
		s.render(w, r, http.StatusRequestEntityTooLarge, views.ImportPage(vm))
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	f, _, err := r.FormFile("file")
	if err != nil {
		vm.Error = i18n.T(r.Context(), "Escolha o arquivo exportado (.json).")
		s.render(w, r, http.StatusUnprocessableEntity, views.ImportPage(vm))
		return
	}
	defer f.Close()
	var doc export.Document
	dec := json.NewDecoder(io.LimitReader(f, maxImportUpload))
	if err := dec.Decode(&doc); err != nil {
		vm.Error = i18n.T(r.Context(), "O arquivo não é um JSON válido de exportação do Hanko.")
		s.render(w, r, http.StatusUnprocessableEntity, views.ImportPage(vm))
		return
	}
	opts, into, err := s.importOptsFrom(r)
	vm.Into, vm.Slug, vm.Default, vm.Active = into, opts.Slug, string(opts.OnConflict), opts.Active
	if err != nil {
		s.importFail(w, r, vm, err)
		return
	}
	opts.DryRun = true
	res, err := s.life.ImportCompany(r.Context(), actorFrom(r.Context()), doc, opts)
	if err != nil {
		s.importFail(w, r, vm, err)
		return
	}
	var uid domain.ID
	if me, ok := auth.IdentityFrom(r.Context()); ok {
		uid = me.User.ID
	}
	tok, err := s.imports.put(uid, doc, s.now())
	if err != nil {
		s.fail(w, r, nil, err)
		return
	}
	vm.Preview = s.previewVM(r.Context(), res, tok, into, opts)
	s.render(w, r, http.StatusOK, views.ImportPage(vm))
}

func (s *server) handleImportApply(w http.ResponseWriter, r *http.Request) {
	if !s.needLife(w, r, nil) {
		return
	}
	vm := s.importVM(r)
	var uid domain.ID
	if me, ok := auth.IdentityFrom(r.Context()); ok {
		uid = me.User.ID
	}
	tok := r.PostFormValue("token")
	doc, ok := s.imports.get(tok, uid, s.now())
	if !ok {
		vm.Error = i18n.T(r.Context(), "A prévia expirou. Envie o arquivo de novo.")
		s.render(w, r, http.StatusGone, views.ImportPage(vm))
		return
	}
	opts, into, err := s.importOptsFrom(r)
	vm.Into, vm.Slug, vm.Default, vm.Active = into, opts.Slug, string(opts.OnConflict), opts.Active
	if err != nil {
		s.importFail(w, r, vm, err)
		return
	}
	if r.PostFormValue("intent") != "confirm" {
		opts.DryRun = true
		res, err := s.life.ImportCompany(r.Context(), actorFrom(r.Context()), doc, opts)
		if err != nil {
			s.importFail(w, r, vm, err)
			return
		}
		vm.Preview = s.previewVM(r.Context(), res, tok, into, opts)
		s.render(w, r, http.StatusOK, views.ImportPage(vm))
		return
	}
	res, err := s.life.ImportCompany(r.Context(), actorFrom(r.Context()), doc, opts)
	if err != nil {
		res2 := vm
		res2.Preview = nil
		s.importFail(w, r, res2, err)
		return
	}
	s.imports.drop(tok)
	vm = s.importVM(r)
	vm.Done, vm.DoneName = "/c/"+res.Company.Slug+"/", res.Company.Name
	s.render(w, r, http.StatusOK, views.ImportPage(vm))
}
