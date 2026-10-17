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
	"github.com/renatobardi/hanko/internal/export"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/textdiff"
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
	// Transfer ajusta os tetos de export/import (zero = padrões; os testes encurtam os prazos).
	Transfer TransferLimits
}

type server struct {
	d   Deps
	svc backend
	log *slog.Logger
	// xfer são os tetos de export/import já com os padrões; exportGate/importGate limitam quantos rodam ao mesmo tempo.
	xfer                   TransferLimits
	exportGate, importGate gate
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
	ListWorkspaceViews(ctx context.Context, companyID domain.ID) ([]service.WorkspaceView, error)
	GetWorkspaceView(ctx context.Context, companyID domain.ID, ref string) (service.WorkspaceView, error)
	UpdateWorkspace(ctx context.Context, actor domain.Actor, companyID, workspaceID domain.ID, in service.UpdateWorkspaceInput) (domain.Workspace, error)
	DeleteWorkspace(ctx context.Context, actor domain.Actor, companyID, workspaceID domain.ID, unlink bool) error
	CreateProject(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateProjectInput) (domain.Project, error)
	ListProjects(ctx context.Context, companyID domain.ID) ([]domain.Project, error)
	GetProject(ctx context.Context, companyID domain.ID, ref string) (domain.Project, error)
	ListProjectViews(ctx context.Context, companyID domain.ID) ([]service.ProjectView, error)
	GetProjectView(ctx context.Context, companyID domain.ID, ref string) (service.ProjectView, error)
	UpdateProject(ctx context.Context, actor domain.Actor, companyID, projectID domain.ID, in service.UpdateProjectInput) (domain.Project, error)
	HirePersona(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.HirePersonaInput) (domain.Persona, error)
	ListPersonas(ctx context.Context, companyID domain.ID) ([]domain.Persona, error)
	GetPersona(ctx context.Context, companyID domain.ID, ref string) (domain.Persona, error)
	UpdatePersona(ctx context.Context, actor domain.Actor, companyID, personaID domain.ID, in service.UpdatePersonaInput) (domain.Persona, error)
	CreateTask(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateTaskInput) (service.TaskView, error)
	ListTasks(ctx context.Context, companyID domain.ID, f service.TaskFilter) ([]service.TaskView, error)
	GetTask(ctx context.Context, companyID domain.ID, ref string) (service.TaskView, error)
	UpdateTask(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID, in service.UpdateTaskInput) (service.TaskView, error)
	TaskRelations(ctx context.Context, companyID, taskID domain.ID) (service.TaskRelations, error)
	AddTaskDependency(ctx context.Context, actor domain.Actor, companyID, taskID, dependsOnID domain.ID) (service.TaskView, error)
	RemoveTaskDependency(ctx context.Context, actor domain.Actor, companyID, taskID, dependsOnID domain.ID) (service.TaskView, error)
	HoldTask(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID, reason string) (service.TaskView, error)
	ResumeTask(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID) (service.TaskView, error)
	ListHeldTasks(ctx context.Context, companyID domain.ID) ([]service.HeldTask, error)
	TaskReviewPolicy(ctx context.Context, companyID, taskID domain.ID) (service.ReviewPolicyView, error)
	SetTaskReviewPolicy(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID, in domain.ReviewOverride) (service.ReviewPolicyView, error)
	ProjectReviewPolicy(ctx context.Context, companyID, projectID domain.ID) (domain.ReviewOverride, error)
	SetProjectReviewPolicy(ctx context.Context, actor domain.Actor, companyID, projectID domain.ID, in domain.ReviewOverride) (domain.ReviewOverride, error)
	AddComment(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID, in service.AddCommentInput) (domain.Comment, error)
	ListComments(ctx context.Context, companyID, taskID domain.ID) ([]domain.Comment, error)
	WakeTask(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID) error
	ListRuns(ctx context.Context, companyID domain.ID, f service.RunFilter) ([]service.RunView, error)
	GetRun(ctx context.Context, companyID, runID domain.ID) (service.RunView, error)
	ListRunEvents(ctx context.Context, companyID, runID domain.ID, afterSeq int64, limit int) ([]domain.RunEvent, error)
	ListRunEventsBefore(ctx context.Context, companyID, runID domain.ID, beforeSeq int64, limit int) ([]domain.RunEvent, error)
	CancelRun(ctx context.Context, actor domain.Actor, companyID, runID domain.ID) (service.RunView, error)
	ListActivity(ctx context.Context, companyID domain.ID, f service.ActivityFilter) ([]domain.ActivityEvent, error)
	Dashboard(ctx context.Context, companyID domain.ID) (service.DashboardStats, error)
	Search(ctx context.Context, companyID domain.ID, q string, f service.SearchFilter) (service.SearchResult, error)
	RequestGate(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.RequestGateInput) (service.GateView, error)
	DecideGate(ctx context.Context, actor domain.Actor, companyID, gateID domain.ID, in service.DecideGateInput) (service.GateView, error)
	ListGates(ctx context.Context, companyID domain.ID, f service.GateFilter) ([]service.GateView, error)
	GetGate(ctx context.Context, companyID, gateID domain.ID) (service.GateView, error)
	OrgChart(ctx context.Context, companyID domain.ID) (service.OrgChart, error)
	CostSummary(ctx context.Context, companyID domain.ID, f service.CostFilter) (service.CostSummary, error)
	CreateSubscription(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateSubscriptionInput) (domain.Subscription, error)
	ListSubscriptions(ctx context.Context, companyID domain.ID) ([]service.SubscriptionStatus, error)
	GetSubscription(ctx context.Context, companyID domain.ID, ref string) (service.SubscriptionStatus, error)
	UpdateSubscription(ctx context.Context, actor domain.Actor, companyID, id domain.ID, in service.UpdateSubscriptionInput) (domain.Subscription, error)
	DeleteSubscription(ctx context.Context, actor domain.Actor, companyID, id domain.ID) error
	CompanyBudget(ctx context.Context, companyID domain.ID) (service.BudgetStatus, error)
	SetCompanyBudget(ctx context.Context, actor domain.Actor, companyID domain.ID, cents *int64) (domain.Company, error)
	CreateFlow(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateFlowInput) (service.FlowView, error)
	UpdateFlow(ctx context.Context, actor domain.Actor, companyID, flowID domain.ID, in service.UpdateFlowInput) (service.FlowView, error)
	ListFlows(ctx context.Context, companyID domain.ID) ([]service.FlowView, error)
	GetFlow(ctx context.Context, companyID domain.ID, ref string) (service.FlowView, error)
	ListFlowFires(ctx context.Context, companyID, flowID domain.ID, limit int) ([]service.FlowFireView, error)
	RunFlowNow(ctx context.Context, actor domain.Actor, companyID, flowID domain.ID) (service.FlowFireResult, error)
	ResolveSession(ctx context.Context, token string) (domain.User, error)
	ResolveAPIKey(ctx context.Context, key string) (domain.User, error)
	CheckSession(ctx context.Context, token string) (domain.User, error)
	CheckAPIKey(ctx context.Context, key string) (domain.User, error)
	OpenExport(ctx context.Context, companyID domain.ID, opts service.ExportOptions) (*service.ExportStream, error)
	ImportCompany(ctx context.Context, actor domain.Actor, doc export.Document, opts service.ImportOptions) (service.ImportResult, error)
	ListMembers(ctx context.Context, companyID domain.ID) ([]domain.Member, error)
	ListInvites(ctx context.Context, companyID domain.ID) ([]domain.Invite, error)
	InviteMember(ctx context.Context, actor domain.Actor, companyID domain.ID, email string, role domain.MemberRole) (domain.Invite, string, error)
	RevokeInvite(ctx context.Context, actor domain.Actor, companyID, inviteID domain.ID) error
	SetMemberRole(ctx context.Context, actor domain.Actor, companyID, userID domain.ID, role domain.MemberRole) (domain.Member, error)
	RemoveMember(ctx context.Context, actor domain.Actor, companyID, userID domain.ID) error
	PutArtifact(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.PutArtifactInput) (service.ArtifactView, error)
	ListArtifacts(ctx context.Context, companyID domain.ID, f service.ArtifactFilter) ([]service.ArtifactView, error)
	GetArtifactView(ctx context.Context, companyID, id domain.ID) (service.ArtifactView, error)
	ArtifactVersions(ctx context.Context, companyID, id domain.ID) ([]domain.Artifact, error)
	ArtifactContent(ctx context.Context, companyID, id domain.ID) (domain.Artifact, []byte, error)
	ArtifactDiff(ctx context.Context, companyID, from, to domain.ID) (textdiff.Result, error)
	DeleteArtifact(ctx context.Context, actor domain.Actor, companyID, id domain.ID) error
	GetArtifactUsage(ctx context.Context, companyID domain.ID) (service.ArtifactUsage, error)
	CreateGoal(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateGoalInput) (domain.Goal, error)
	UpdateGoal(ctx context.Context, actor domain.Actor, companyID, goalID domain.ID, in service.UpdateGoalInput) (domain.Goal, error)
	RecordGoalProgress(ctx context.Context, actor domain.Actor, companyID, goalID domain.ID, value float64, note string) (service.GoalView, error)
	GetGoal(ctx context.Context, companyID domain.ID, ref string) (domain.Goal, error)
	ListGoalViews(ctx context.Context, companyID domain.ID, f service.GoalFilter) ([]service.GoalView, error)
	GetGoalView(ctx context.Context, companyID domain.ID, ref string) (service.GoalView, error)
	ListConversations(ctx context.Context, companyID domain.ID, f service.ConversationFilter) ([]service.ConversationView, error)
	GetConversation(ctx context.Context, companyID, id domain.ID) (service.ConversationView, error)
	ListConversationMessages(ctx context.Context, companyID, id domain.ID, limit int) ([]domain.ConversationMessage, error)
	CreateConversation(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateConversationInput) (service.ConversationView, error)
	SendMessage(ctx context.Context, actor domain.Actor, companyID, conversationID domain.ID, body string) (domain.ConversationMessage, error)
	SetConversationStatus(ctx context.Context, actor domain.Actor, companyID, id domain.ID, status domain.ConversationStatus) (service.ConversationView, error)
	// sala (ADR-0033)
	CreateRoom(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateRoomInput) (service.ConversationView, error)
	AddRoomParticipant(ctx context.Context, actor domain.Actor, companyID, convID, personaID domain.ID) (service.ConversationView, error)
	RemoveRoomParticipant(ctx context.Context, actor domain.Actor, companyID, convID, personaID domain.ID) (service.ConversationView, error)
	UpdateRoomSettings(ctx context.Context, actor domain.Actor, companyID, convID domain.ID, in service.RoomSettingsPatch) (service.ConversationView, error)
}

var _ backend = (*service.Service)(nil)

// Mount registra a API no mux: /api/v1/*, /openapi.json, /docs, /healthz e /readyz.
func Mount(mux *http.ServeMux, d Deps) { newServer(d).mount(mux) }

func newServer(d Deps) *server {
	s := &server{d: d, log: d.Logger, xfer: d.Transfer.withDefaults()}
	s.exportGate, s.importGate = newGate(s.xfer.MaxExports), newGate(s.xfer.MaxImports)
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
	s.registerImportDoc(api)
	wm.HandleFunc("POST "+importPath, s.handleImport)

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
	s.registerImportDoc(api)
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
	s.registerCoordination(api)
	s.registerRuns(api)
	s.registerGates(api)
	s.registerMisc(api)
	s.registerCosts(api)
	s.registerProjects(api)
	s.registerSubscriptions(api)
	s.registerFlows(api)
	s.registerMemory(api)
	s.registerMembers(api)
	s.registerExport(api)
	s.registerArtifacts(api)
	s.registerGoals(api)
	s.registerSkills(api)
	s.registerSkillStudio(api)
	s.registerIntegrations(api)
	s.registerEnvironments(api)
	s.registerTools(api)
	s.registerChat(api)
	s.registerRoom(api) // sala (ADR-0033)
	s.registerSearch(api)
	s.registerPlugins(api)
	s.registerStatusCards(api)
	s.registerPipelines(api)
}

// op monta uma huma.Operation com o prefixo da API.
func op(id, method, path, tag, summary string) huma.Operation {
	return huma.Operation{OperationID: id, Method: method, Path: Prefix + path, Tags: []string{tag}, Summary: summary,
		Errors: []int{401, 403, 404, 409, 422, 500}}
}

func created(o huma.Operation) huma.Operation { o.DefaultStatus = http.StatusCreated; return o }
