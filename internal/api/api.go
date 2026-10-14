// Package api expõe o service por HTTP: REST code-first com huma v2 sobre
// net/http (ADR-0006), mais SSE de execuções e sondas de saúde.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/service"
)

// Prefix é o prefixo de todas as rotas REST.
const Prefix = "/api/v1"

// SpecVersion é a versão da API publicada no OpenAPI (independente do binário,
// para que api/openapi.json seja estável entre builds).
const SpecVersion = "0.1.0"

// Pinger é o que o /readyz precisa do banco.
type Pinger interface{ Ping(context.Context) error }

// Deps são as dependências do pacote.
type Deps struct {
	Svc     *service.Service
	Broker  *events.Broker
	Logger  *slog.Logger
	Version string
	// Tokens habilita autenticação por run token (Bearer). Nil = só modo trusted.
	Tokens *auth.RunTokens
	// Authenticated é o modo authenticated: sem identidade do auth.Guard nem run token, 401.
	Authenticated bool
	// DB é usado pelo /readyz. Nil = sempre pronto.
	DB Pinger
	// CORSOrigins lista origens permitidas. Vazio = CORS desligado.
	CORSOrigins []string
}

type server struct {
	d   Deps
	svc backend
	log *slog.Logger
}

// backend é o subconjunto de *service.Service usado pelos handlers; existe para
// que os testes unitários troquem o service por um fake em memória.
type backend interface {
	CreateCompany(ctx context.Context, actor domain.Actor, in service.CreateCompanyInput) (domain.Company, error)
	GetCompany(ctx context.Context, ref string) (domain.Company, error)
	ListCompanies(ctx context.Context) ([]domain.Company, error)
	CreateWorkspace(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateWorkspaceInput) (domain.Workspace, error)
	ListWorkspaces(ctx context.Context, companyID domain.ID) ([]domain.Workspace, error)
	GetWorkspace(ctx context.Context, companyID domain.ID, ref string) (domain.Workspace, error)
	CreateProject(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateProjectInput) (domain.Project, error)
	ListProjects(ctx context.Context, companyID domain.ID) ([]domain.Project, error)
	GetProject(ctx context.Context, companyID domain.ID, ref string) (domain.Project, error)
	HirePersona(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.HirePersonaInput) (domain.Persona, error)
	ListPersonas(ctx context.Context, companyID domain.ID) ([]domain.Persona, error)
	GetPersona(ctx context.Context, companyID domain.ID, ref string) (domain.Persona, error)
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
	RequestGate(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.RequestGateInput) (service.GateView, error)
	DecideGate(ctx context.Context, actor domain.Actor, companyID, gateID domain.ID, in service.DecideGateInput) (service.GateView, error)
	ListGates(ctx context.Context, companyID domain.ID, f service.GateFilter) ([]service.GateView, error)
	GetGate(ctx context.Context, companyID, gateID domain.ID) (service.GateView, error)
	OrgChart(ctx context.Context, companyID domain.ID) (service.OrgChart, error)
	CostSummary(ctx context.Context, companyID domain.ID, f service.CostFilter) (service.CostSummary, error)
	CompanyBudget(ctx context.Context, companyID domain.ID) (service.BudgetStatus, error)
	SetCompanyBudget(ctx context.Context, actor domain.Actor, companyID domain.ID, cents *int64) (domain.Company, error)
	CreateFlow(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateFlowInput) (service.FlowView, error)
	UpdateFlow(ctx context.Context, actor domain.Actor, companyID, flowID domain.ID, in service.UpdateFlowInput) (service.FlowView, error)
	ListFlows(ctx context.Context, companyID domain.ID) ([]service.FlowView, error)
	GetFlow(ctx context.Context, companyID domain.ID, ref string) (service.FlowView, error)
	ListFlowFires(ctx context.Context, companyID, flowID domain.ID, limit int) ([]service.FlowFireView, error)
	RunFlowNow(ctx context.Context, actor domain.Actor, companyID, flowID domain.ID) (service.FlowFireResult, error)
}

var _ backend = (*service.Service)(nil)

// Mount registra a API no mux: /api/v1/*, /openapi.json, /docs, /healthz e /readyz.
func Mount(mux *http.ServeMux, d Deps) { newServer(d).mount(mux) }

func newServer(d Deps) *server {
	s := &server{d: d, log: d.Logger}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if d.Svc != nil {
		s.svc = d.Svc
	}
	return s
}

func (s *server) mount(mux *http.ServeMux) {
	wm := wrapMux{mux: mux, wrap: s.middleware}
	api := humago.New(wm, config())
	s.register(api)
	s.registerStream(wm, api)

	if len(s.d.CORSOrigins) > 0 { // preflight cai aqui; o middleware responde quando a origem é permitida
		wm.HandleFunc("OPTIONS "+Prefix+"/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	}
	wm.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok"))
	})
	wm.HandleFunc("GET /readyz", s.readyz)
}

func (s *server) readyz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if s.d.DB != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.d.DB.Ping(ctx); err != nil {
			s.log.WarnContext(r.Context(), "readyz: banco indisponível", "err", err)
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("indisponível"))
			return
		}
	}
	_, _ = w.Write([]byte("ok"))
}

func config() huma.Config {
	c := huma.DefaultConfig("Hanko API", SpecVersion)
	c.Info.Description = "API REST do Hanko: control plane para orquestrar times de agentes de IA. " +
		"`{company}` aceita id ou slug; `{persona}` id ou slug; `{task}` id ou chave (HNK-12)."
	c.Info.License = &huma.License{Name: "Apache-2.0", Identifier: "Apache-2.0"}
	c.CreateHooks = nil // sem campo $schema nas respostas
	c.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"runToken": {Type: "http", Scheme: "bearer", BearerFormat: "JWT",
			Description: "Run token de uma execução. Sem credencial vale o modo trusted (humano local)."},
		"apiKey": {Type: "http", Scheme: "bearer", BearerFormat: "hk_…",
			Description: "API key de usuário (hanko auth token create). Obrigatória fora de sessão no modo authenticated."},
		"session": {Type: "apiKey", In: "cookie", Name: "hanko_session",
			Description: "Cookie de sessão do navegador (modo authenticated); métodos que mudam estado exigem mesma origem."},
	}
	c.Security = []map[string][]string{{"runToken": {}}, {"apiKey": {}}, {"session": {}}, {}}
	return c
}

// Spec devolve o OpenAPI gerado, indentado e estável, sem precisar de banco.
func Spec() ([]byte, error) {
	mux := http.NewServeMux()
	api := humago.New(wrapMux{mux: mux, wrap: func(h http.Handler) http.Handler { return h }}, config())
	s := &server{log: slog.New(slog.DiscardHandler)}
	s.register(api)
	s.registerStreamDoc(api)
	b, err := json.MarshalIndent(api.OpenAPI(), "", "  ")
	if err != nil {
		return nil, fmt.Errorf("openapi: %w", err)
	}
	return append(b, '\n'), nil
}

func (s *server) register(api huma.API) {
	s.registerCompanies(api)
	s.registerPersonas(api)
	s.registerTasks(api)
	s.registerRuns(api)
	s.registerGates(api)
	s.registerMisc(api)
	s.registerCosts(api)
	s.registerFlows(api)
}

// op monta uma huma.Operation com o prefixo da API.
func op(id, method, path, tag, summary string) huma.Operation {
	return huma.Operation{OperationID: id, Method: method, Path: Prefix + path, Tags: []string{tag}, Summary: summary,
		Errors: []int{401, 403, 404, 409, 422, 500}}
}

func created(o huma.Operation) huma.Operation { o.DefaultStatus = http.StatusCreated; return o }
