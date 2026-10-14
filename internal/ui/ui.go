// Package ui is Hanko's server-rendered web interface: templ views, htmx
// fragments and an SSE feed for live run logs (ADR-0005). Handlers talk only to
// the service layer through the Backend interface.
package ui

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
	_ "time/tzdata" // company time zones must resolve in minimal images

	"github.com/a-h/templ"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Deps are the collaborators of the UI.
type Deps struct {
	Svc     *service.Service
	Broker  *events.Broker
	Logger  *slog.Logger
	Version string
	// Auth enables sign-in, sessions and API keys (authenticated mode). Nil = trusted mode.
	Auth *AuthDeps
	// Now overrides the clock (tests, previews). Optional.
	Now func() time.Time
}

// Backend is the subset of *service.Service the UI uses. It exists so handlers
// can be tested with a fake.
type Backend interface {
	CreateCompany(ctx context.Context, actor domain.Actor, in service.CreateCompanyInput) (domain.Company, error)
	GetCompany(ctx context.Context, ref string) (domain.Company, error)
	ListCompanies(ctx context.Context) ([]domain.Company, error)
	ListProjects(ctx context.Context, companyID domain.ID) ([]domain.Project, error)

	HirePersona(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.HirePersonaInput) (domain.Persona, error)
	ListPersonas(ctx context.Context, companyID domain.ID) ([]domain.Persona, error)
	GetPersona(ctx context.Context, companyID domain.ID, ref string) (domain.Persona, error)
	GetPersonaHealth(ctx context.Context, companyID, personaID domain.ID) (service.PersonaHealth, error)
	UpdatePersona(ctx context.Context, actor domain.Actor, companyID, personaID domain.ID, in service.UpdatePersonaInput) (domain.Persona, error)

	CreateTask(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateTaskInput) (service.TaskView, error)
	ListTasks(ctx context.Context, companyID domain.ID, f service.TaskFilter) ([]service.TaskView, error)
	GetTask(ctx context.Context, companyID domain.ID, ref string) (service.TaskView, error)
	UpdateTask(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID, in service.UpdateTaskInput) (service.TaskView, error)
	AddComment(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID, in service.AddCommentInput) (domain.Comment, error)
	ListComments(ctx context.Context, companyID, taskID domain.ID) ([]domain.Comment, error)
	WakeTask(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID) error

	ListRuns(ctx context.Context, companyID domain.ID, f service.RunFilter) ([]service.RunView, error)
	GetRun(ctx context.Context, companyID, runID domain.ID) (service.RunView, error)
	ListRunEvents(ctx context.Context, companyID, runID domain.ID, afterSeq int64, limit int) ([]domain.RunEvent, error)
	CancelRun(ctx context.Context, actor domain.Actor, companyID, runID domain.ID) (service.RunView, error)

	ListActivity(ctx context.Context, companyID domain.ID, f service.ActivityFilter) ([]domain.ActivityEvent, error)
	Dashboard(ctx context.Context, companyID domain.ID) (service.DashboardStats, error)

	ListGates(ctx context.Context, companyID domain.ID, f service.GateFilter) ([]service.GateView, error)
	GetGate(ctx context.Context, companyID, gateID domain.ID) (service.GateView, error)
	DecideGate(ctx context.Context, actor domain.Actor, companyID, gateID domain.ID, in service.DecideGateInput) (service.GateView, error)
	Inbox(ctx context.Context, companyID domain.ID) (service.Inbox, error)
	OrgChart(ctx context.Context, companyID domain.ID) (service.OrgChart, error)
	CostSummary(ctx context.Context, companyID domain.ID, f service.CostFilter) (service.CostSummary, error)
	CompanyBudget(ctx context.Context, companyID domain.ID) (service.BudgetStatus, error)
	PersonaBudget(ctx context.Context, companyID, personaID domain.ID) (service.BudgetStatus, error)
	SetCompanyBudget(ctx context.Context, actor domain.Actor, companyID domain.ID, cents *int64) (domain.Company, error)

	ListFlows(ctx context.Context, companyID domain.ID) ([]service.FlowView, error)
	GetFlow(ctx context.Context, companyID domain.ID, ref string) (service.FlowView, error)
	CreateFlow(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateFlowInput) (service.FlowView, error)
	UpdateFlow(ctx context.Context, actor domain.Actor, companyID, flowID domain.ID, in service.UpdateFlowInput) (service.FlowView, error)
	ListFlowFires(ctx context.Context, companyID, flowID domain.ID, limit int) ([]service.FlowFireView, error)
	RunFlowNow(ctx context.Context, actor domain.Actor, companyID, flowID domain.ID) (service.FlowFireResult, error)
}

var _ Backend = (*service.Service)(nil)

// ownerName is how the local human is shown in trusted mode (no accounts).
const ownerName = "Você"

type server struct {
	be      Backend
	broker  *events.Broker
	log     *slog.Logger
	version string
	now     func() time.Time
	auth    *authState // nil in trusted mode
	// sseTick is how often the SSE handler re-reads the database as a safety
	// net for dropped broker messages.
	sseTick time.Duration
}

// Mount registers the UI routes on mux using the real service.
func Mount(mux *http.ServeMux, d Deps) {
	if d.Svc == nil {
		panic("ui.Mount: Deps.Svc is nil")
	}
	MountBackend(mux, d.Svc, d)
}

// MountBackend is Mount with an explicit backend (tests, preview).
func MountBackend(mux *http.ServeMux, be Backend, d Deps) {
	log := d.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(discard{}, nil))
	}
	broker := d.Broker
	if broker == nil {
		broker = events.NewBroker()
	}
	now := d.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	s := &server{be: be, broker: broker, log: log, version: d.Version, now: now, sseTick: 2 * time.Second}
	if d.Auth != nil {
		ab := d.Auth.Backend
		if ab == nil {
			ab = d.Svc
		}
		s.auth = newAuthState(*d.Auth, ab, now)
	}
	s.routes(mux)
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

type companyHandler func(w http.ResponseWriter, r *http.Request, co domain.Company)

func (s *server) routes(mux *http.ServeMux) {
	co := func(h companyHandler) http.HandlerFunc { return s.withCompany(h) }
	post := func(h http.HandlerFunc) http.HandlerFunc { return s.sameOrigin(h) }

	mux.Handle("GET /static/", staticHandler())
	mux.HandleFunc("GET /{$}", s.handleRoot)
	mux.HandleFunc("GET /bem-vindo", s.handleOnboarding)
	mux.HandleFunc("POST /empresas", post(s.handleCreateCompany))

	mux.HandleFunc("GET /c/{company}/{$}", co(s.handleDashboard))
	mux.HandleFunc("GET /c/{company}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/c/"+r.PathValue("company")+"/", http.StatusMovedPermanently)
	})

	mux.HandleFunc("GET /c/{company}/tasks", co(s.handleTasks))
	mux.HandleFunc("GET /c/{company}/tasks/nova", co(s.handleNewTask))
	mux.HandleFunc("POST /c/{company}/tasks/nova", post(co(s.handleCreateTask)))
	mux.HandleFunc("GET /c/{company}/tasks/{task}", co(s.handleTask))
	mux.HandleFunc("POST /c/{company}/tasks/{task}", post(co(s.handleUpdateTask)))
	mux.HandleFunc("POST /c/{company}/tasks/{task}/comentarios", post(co(s.handleComment)))
	mux.HandleFunc("POST /c/{company}/tasks/{task}/acordar", post(co(s.handleWake)))

	mux.HandleFunc("GET /c/{company}/personas", co(s.handlePersonas))
	mux.HandleFunc("GET /c/{company}/personas/nova", co(s.handleNewPersona))
	mux.HandleFunc("POST /c/{company}/personas/nova", post(co(s.handleHirePersona)))
	mux.HandleFunc("GET /c/{company}/personas/{persona}", co(s.handlePersona))
	mux.HandleFunc("POST /c/{company}/personas/{persona}/pausar", post(co(s.handlePersonaControl(domain.PersonaPaused, "pausada"))))
	mux.HandleFunc("POST /c/{company}/personas/{persona}/retomar", post(co(s.handlePersonaControl(domain.PersonaActive, "retomada"))))
	mux.HandleFunc("POST /c/{company}/personas/{persona}/reativar", post(co(s.handlePersonaControl(domain.PersonaActive, "reativada"))))

	mux.HandleFunc("POST /c/{company}/personas/{persona}/orcamento", post(co(s.handleSetPersonaBudget)))

	mux.HandleFunc("GET /c/{company}/custos", co(s.handleCosts))
	mux.HandleFunc("POST /c/{company}/custos/orcamento", post(co(s.handleSetCompanyBudget)))

	mux.HandleFunc("GET /c/{company}/fluxos", co(s.handleFlows))
	mux.HandleFunc("GET /c/{company}/fluxos/novo", co(s.handleNewFlow))
	mux.HandleFunc("POST /c/{company}/fluxos/novo", post(co(s.handleCreateFlow)))
	mux.HandleFunc("GET /c/{company}/fluxos/cron", co(s.handleCronHint))
	mux.HandleFunc("GET /c/{company}/fluxos/{flow}", co(s.handleFlow))
	mux.HandleFunc("POST /c/{company}/fluxos/{flow}", post(co(s.handleUpdateFlow)))
	mux.HandleFunc("POST /c/{company}/fluxos/{flow}/ativar", post(co(s.handleFlowEnabled(true))))
	mux.HandleFunc("POST /c/{company}/fluxos/{flow}/desativar", post(co(s.handleFlowEnabled(false))))
	mux.HandleFunc("POST /c/{company}/fluxos/{flow}/executar", post(co(s.handleRunFlow)))

	mux.HandleFunc("GET /c/{company}/execucoes", co(s.handleRuns))
	mux.HandleFunc("GET /c/{company}/execucoes/{run}", co(s.handleRun))
	mux.HandleFunc("GET /c/{company}/execucoes/{run}/sse", co(s.handleRunSSE))
	mux.HandleFunc("POST /c/{company}/execucoes/{run}/cancelar", post(co(s.handleCancelRun)))

	mux.HandleFunc("POST /c/{company}/personas/{persona}/organizacao", post(co(s.handlePersonaOrg)))

	mux.HandleFunc("GET /c/{company}/caixa", co(s.handleInbox))
	mux.HandleFunc("POST /c/{company}/caixa/tasks/{task}/desbloquear", post(co(s.handleInboxUnblock)))
	mux.HandleFunc("POST /c/{company}/caixa/tasks/{task}/acordar", post(co(s.handleInboxWake)))
	mux.HandleFunc("POST /c/{company}/caixa/personas/{persona}/reativar", post(co(s.handleInboxReactivate)))
	mux.HandleFunc("GET /c/{company}/decisoes", co(s.handleDecisions))
	mux.HandleFunc("GET /c/{company}/gates", co(s.handleGates))
	mux.HandleFunc("GET /c/{company}/gates/{gate}", co(s.handleGate))
	mux.HandleFunc("POST /c/{company}/gates/{gate}/decidir", post(co(s.handleDecideGate)))
	mux.HandleFunc("GET /c/{company}/organograma", co(s.handleOrg))

	mux.HandleFunc("GET /c/{company}/configuracoes", co(s.handleSettings))
	mux.HandleFunc("POST /c/{company}/configuracoes/chaves", post(co(s.handleCreateKey)))
	mux.HandleFunc("POST /c/{company}/configuracoes/chaves/{key}/revogar", post(co(s.handleRevokeKey)))
	s.authRoutes(mux)

	mux.HandleFunc("GET /c/{company}/atividade", co(s.handleActivity))
	mux.HandleFunc("GET /c/{company}/em-breve/{item}", co(s.handleSoon))
}

// ---------- middleware ----------

// sameOrigin is the CSRF gate for POSTs: htmx requests carry HX-Request (a
// custom header browsers never send cross-origin without preflight); plain form
// posts must be same-origin per Sec-Fetch-Site or Origin.
func (s *server) sameOrigin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !originOK(r) {
			s.fail(w, r, nil, domain.ErrNotAllowed)
			return
		}
		next(w, r)
	}
}

func originOK(r *http.Request) bool {
	if r.Header.Get("HX-Request") == "true" {
		return true
	}
	if sfs := r.Header.Get("Sec-Fetch-Site"); sfs != "" {
		return sfs == "same-origin"
	}
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		return err == nil && u.Host == r.Host
	}
	return false
}

func (s *server) withCompany(h companyHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		co, err := s.be.GetCompany(r.Context(), r.PathValue("company"))
		if err != nil {
			s.fail(w, r, nil, err)
			return
		}
		h(w, r, co)
	}
}

// ---------- rendering ----------

func isHX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-Boosted") != "true"
}

func (s *server) render(w http.ResponseWriter, r *http.Request, status int, c templ.Component) {
	var buf bytes.Buffer
	ctx := r.Context()
	if id, ok := auth.IdentityFrom(ctx); ok {
		ctx = views.WithUser(ctx, views.UserRef{Name: id.User.DisplayName(), Email: id.User.Email})
	}
	if err := c.Render(ctx, &buf); err != nil {
		s.log.Error("ui render", "path", r.URL.Path, "err", err)
		http.Error(w, "erro ao renderizar a página", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// redirect answers a successful POST: 303 for plain forms, HX-Redirect for htmx.
func redirect(w http.ResponseWriter, r *http.Request, to string) {
	if isHX(r) {
		w.Header().Set("HX-Redirect", to)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func statusFor(err error) int {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, domain.ErrInvalid):
		return http.StatusUnprocessableEntity
	case errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrActiveRun):
		return http.StatusConflict
	case errors.Is(err, domain.ErrNotAllowed):
		return http.StatusForbidden
	case errors.Is(err, service.ErrNotImplemented):
		return http.StatusNotImplemented
	}
	return http.StatusInternalServerError
}

// friendly turns an error into a title and a message for people.
func friendly(err error) (int, string, string) {
	st := statusFor(err)
	switch st {
	case http.StatusNotFound:
		return st, "Não encontrado", "O registro que você procura não existe nesta empresa."
	case http.StatusUnprocessableEntity:
		var ve *domain.ValidationError
		if errors.As(err, &ve) {
			return st, "Dados inválidos", ve.Field + ": " + ve.Reason + "."
		}
		return st, "Dados inválidos", "Confira os campos e tente de novo."
	case http.StatusConflict:
		if errors.Is(err, domain.ErrBudgetExceeded) {
			return st, "Orçamento estourado", "O limite mensal foi atingido; nenhuma execução nova abre até o limite subir ou o mês virar."
		}
		if errors.Is(err, domain.ErrActiveRun) {
			return st, "Já existe uma execução ativa", "Esta task já tem uma execução em andamento; espere ela terminar."
		}
		return st, "Conflito", "Esse registro já existe ou foi alterado por outra pessoa."
	case http.StatusForbidden:
		return st, "Ação não permitida", "A requisição foi bloqueada por segurança (origem diferente)."
	case http.StatusNotImplemented:
		return st, "Ainda não implementado", "Esta função depende de uma parte do servidor que ainda não está disponível."
	}
	return st, "Algo deu errado", "Erro interno. Tente novamente em instantes."
}

// fail renders an error as a page (full navigation) or fragment (htmx).
func (s *server) fail(w http.ResponseWriter, r *http.Request, co *domain.Company, err error) {
	st, title, msg := friendly(err)
	if st >= 500 {
		s.log.Error("ui handler", "method", r.Method, "path", r.URL.Path, "err", err)
	}
	vm := views.ErrorVM{Status: st, Title: title, Message: msg}
	if isHX(r) {
		w.Header().Set("HX-Retarget", "#flash")
		w.Header().Set("HX-Reswap", "innerHTML")
		s.render(w, r, st, views.ErrorFragment(vm))
		return
	}
	if co != nil {
		vm.Back = "/c/" + co.Slug + "/"
		s.render(w, r, st, views.ErrorPage(s.shell(*co, "", title), vm))
		return
	}
	vm.Back = "/"
	s.render(w, r, st, views.ErrorBare(vm, s.version))
}

func (s *server) shell(co domain.Company, active, title string, crumbs ...views.Crumb) views.Shell {
	base := "/c/" + co.Slug
	cs := []views.Crumb{{Label: co.Name, Href: base + "/"}}
	cs = append(cs, crumbs...)
	return views.Shell{
		Title:   title,
		Company: views.CompanyRef{Slug: co.Slug, Name: co.Name},
		Active:  active,
		Crumbs:  cs,
		Version: s.version,
		Owner:   ownerName,
	}
}

func trimForm(r *http.Request, key string) string { return strings.TrimSpace(r.PostFormValue(key)) }

// actorFrom is the human actor of the request: the signed-in user in authenticated
// mode, the anonymous local human in trusted mode.
func actorFrom(ctx context.Context) domain.Actor {
	if id, ok := auth.IdentityFrom(ctx); ok {
		return id.User.Actor()
	}
	return domain.HumanActor
}
