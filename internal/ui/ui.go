// Package ui is Hanko's server-rendered web interface: templ views, htmx
// fragments and an SSE feed for live run logs (ADR-0005). Handlers talk only to
// the service layer through the Backend interface.
package ui

import (
	"bytes"
	"context"
	"errors"
	"github.com/renatobardi/hanko/internal/store"
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
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/textdiff"
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
	// Secure marks the language cookie Secure (PUBLIC_URL is https). Authenticated mode also
	// honours Auth.Secure.
	Secure bool
	// Platform descreve a instalação (telas Ambientes, Adapters e Plugins). Opcional.
	Platform PlatformInfo
}

// Backend is the subset of *service.Service the UI uses. It exists so handlers
// can be tested with a fake.
type Backend interface {
	CreateCompany(ctx context.Context, actor domain.Actor, in service.CreateCompanyInput) (domain.Company, error)
	GetCompany(ctx context.Context, ref string) (domain.Company, error)
	ListCompanies(ctx context.Context) ([]domain.Company, error)
	ListProjects(ctx context.Context, companyID domain.ID) ([]domain.Project, error)
	ListProjectViews(ctx context.Context, companyID domain.ID) ([]service.ProjectView, error)
	GetProjectView(ctx context.Context, companyID domain.ID, ref string) (service.ProjectView, error)
	CreateProject(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateProjectInput) (domain.Project, error)
	UpdateProject(ctx context.Context, actor domain.Actor, companyID, projectID domain.ID, in service.UpdateProjectInput) (domain.Project, error)

	ListWorkspaceViews(ctx context.Context, companyID domain.ID) ([]service.WorkspaceView, error)
	GetWorkspaceView(ctx context.Context, companyID domain.ID, ref string) (service.WorkspaceView, error)
	CreateWorkspace(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateWorkspaceInput) (domain.Workspace, error)
	UpdateWorkspace(ctx context.Context, actor domain.Actor, companyID, workspaceID domain.ID, in service.UpdateWorkspaceInput) (domain.Workspace, error)
	DeleteWorkspace(ctx context.Context, actor domain.Actor, companyID, workspaceID domain.ID, unlink bool) error
	WorkspacesRootPath() string

	HirePersona(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.HirePersonaInput) (domain.Persona, error)
	ListPersonas(ctx context.Context, companyID domain.ID) ([]domain.Persona, error)
	GetPersona(ctx context.Context, companyID domain.ID, ref string) (domain.Persona, error)
	GetPersonaHealth(ctx context.Context, companyID, personaID domain.ID) (service.PersonaHealth, error)
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
	Search(ctx context.Context, companyID domain.ID, q string, f service.SearchFilter) (service.SearchResult, error)
	MemorySearchAvailable(ctx context.Context) bool
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

	ListSubscriptions(ctx context.Context, companyID domain.ID) ([]service.SubscriptionStatus, error)
	ListModelPrices(ctx context.Context) ([]store.ModelPrice, error)
	CreateSubscription(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateSubscriptionInput) (domain.Subscription, error)
	UpdateSubscription(ctx context.Context, actor domain.Actor, companyID, id domain.ID, in service.UpdateSubscriptionInput) (domain.Subscription, error)
	DeleteSubscription(ctx context.Context, actor domain.Actor, companyID, id domain.ID) error

	ListFlows(ctx context.Context, companyID domain.ID) ([]service.FlowView, error)
	GetFlow(ctx context.Context, companyID domain.ID, ref string) (service.FlowView, error)
	CreateFlow(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateFlowInput) (service.FlowView, error)
	UpdateFlow(ctx context.Context, actor domain.Actor, companyID, flowID domain.ID, in service.UpdateFlowInput) (service.FlowView, error)
	ListFlowFires(ctx context.Context, companyID, flowID domain.ID, limit int) ([]service.FlowFireView, error)
	RunFlowNow(ctx context.Context, actor domain.Actor, companyID, flowID domain.ID) (service.FlowFireResult, error)

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
	ListGoalViews(ctx context.Context, companyID domain.ID, f service.GoalFilter) ([]service.GoalView, error)
	GetGoalView(ctx context.Context, companyID domain.ID, ref string) (service.GoalView, error)
	ListConversations(ctx context.Context, companyID domain.ID, f service.ConversationFilter) ([]service.ConversationView, error)
	GetConversation(ctx context.Context, companyID, id domain.ID) (service.ConversationView, error)
	ListConversationMessages(ctx context.Context, companyID, id domain.ID, limit int) ([]domain.ConversationMessage, error)
	CreateConversation(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateConversationInput) (service.ConversationView, error)
	SendMessage(ctx context.Context, actor domain.Actor, companyID, conversationID domain.ID, body string) (domain.ConversationMessage, error)
	SendMessageWith(ctx context.Context, actor domain.Actor, companyID, conversationID domain.ID, body string, attachments []domain.ID) (domain.ConversationMessage, error)
	SetConversationStatus(ctx context.Context, actor domain.Actor, companyID, id domain.ID, status domain.ConversationStatus) (service.ConversationView, error)

	// sala (ADR-0033)
	CreateRoom(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateRoomInput) (service.ConversationView, error)
	AddRoomParticipant(ctx context.Context, actor domain.Actor, companyID, convID, personaID domain.ID) (service.ConversationView, error)
	RemoveRoomParticipant(ctx context.Context, actor domain.Actor, companyID, convID, personaID domain.ID) (service.ConversationView, error)
	UpdateRoomSettings(ctx context.Context, actor domain.Actor, companyID, convID domain.ID, in service.RoomSettingsPatch) (service.ConversationView, error)

	MyRole(ctx context.Context, companyID domain.ID) (domain.MemberRole, error)
	ListMembers(ctx context.Context, companyID domain.ID) ([]domain.Member, error)
	ListInvites(ctx context.Context, companyID domain.ID) ([]domain.Invite, error)
	InviteMember(ctx context.Context, actor domain.Actor, companyID domain.ID, email string, role domain.MemberRole) (domain.Invite, string, error)
	RevokeInvite(ctx context.Context, actor domain.Actor, companyID, inviteID domain.ID) error
	SetMemberRole(ctx context.Context, actor domain.Actor, companyID, userID domain.ID, role domain.MemberRole) (domain.Member, error)
	RemoveMember(ctx context.Context, actor domain.Actor, companyID, userID domain.ID) error

	// Ambientes de execução (ADR-0037).
	ListEnvironments(ctx context.Context, companyID domain.ID) ([]service.EnvironmentView, error)
	GetEnvironment(ctx context.Context, companyID domain.ID, ref string) (service.EnvironmentView, error)
	CreateEnvironment(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateEnvironmentInput) (service.EnvironmentView, error)
	UpdateEnvironment(ctx context.Context, actor domain.Actor, companyID, id domain.ID, in service.UpdateEnvironmentInput) (service.EnvironmentView, error)
	ArchiveEnvironment(ctx context.Context, actor domain.Actor, companyID, id domain.ID, moveTo *domain.ID) (service.EnvironmentView, error)
	SetDefaultEnvironment(ctx context.Context, actor domain.Actor, companyID domain.ID, id *domain.ID) error
	SetPersonaEnvironment(ctx context.Context, actor domain.Actor, companyID, personaID domain.ID, envID *domain.ID) (domain.Persona, error)
	ProbeEnvironment(ctx context.Context, actor domain.Actor, companyID, id domain.ID) (service.EnvironmentView, error)
	ProbeEnvironmentDraft(ctx context.Context, companyID domain.ID, in service.CreateEnvironmentInput) (domain.ProbeReport, error)
	ListEnvironmentLeases(ctx context.Context, companyID, envID domain.ID, limit int) ([]service.EnvironmentLeaseView, error)
	RunEnvironmentOf(ctx context.Context, companyID, runID domain.ID) (service.RunEnvironmentInfo, error)
	EnvironmentDrivers(ctx context.Context) map[domain.EnvironmentDriver]string
	// Cards de status (ADR-0032).
	ListStatusCards(ctx context.Context, companyID domain.ID, archived *bool) ([]service.StatusCardView, error)
	GetStatusCard(ctx context.Context, companyID, cardID domain.ID) (service.StatusCardView, error)
	CreateStatusCard(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateStatusCardInput) (service.StatusCardView, error)
	UpdateStatusCard(ctx context.Context, actor domain.Actor, companyID, cardID domain.ID, in service.UpdateStatusCardInput) (service.StatusCardView, error)
	DeleteStatusCard(ctx context.Context, actor domain.Actor, companyID, cardID domain.ID) error
	RecompileStatusCard(ctx context.Context, actor domain.Actor, companyID, cardID domain.ID) (service.StatusCardView, error)
	RefreshStatusCard(ctx context.Context, actor domain.Actor, companyID, cardID domain.ID, full bool) (service.StatusCardView, error)
	ListStatusCardUpdates(ctx context.Context, companyID, cardID domain.ID, limit int) ([]service.StatusCardUpdateView, error)
	ListStatusCardRevisions(ctx context.Context, companyID, cardID domain.ID, limit int) ([]domain.StatusCardRevision, error)
	PreviewStatusCard(ctx context.Context, companyID, cardID domain.ID) (service.StatusCardPreview, error)
	// ---- apps, gateway de tools e perfis de ferramentas (ADR-0036; ui/tools.go, uitest/tools.go) ----
	ToolIntegrations(ctx context.Context, companyID domain.ID) ([]service.ToolIntegration, error)
	ListIntegrationTools(ctx context.Context, companyID, integrationID domain.ID) (service.IntegrationToolsView, error)
	RefreshIntegrationTools(ctx context.Context, actor domain.Actor, companyID, integrationID domain.ID) (service.ToolCatalogResult, error)
	SetToolStatus(ctx context.Context, actor domain.Actor, companyID, integrationID domain.ID, tool string, status domain.ToolStatus) (domain.IntegrationTool, error)
	ReviewQuarantinedTools(ctx context.Context, actor domain.Actor, companyID, integrationID domain.ID, tools []string) (int, error)
	CreateToolProfile(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateToolProfileInput) (service.ToolProfileView, error)
	GetToolProfile(ctx context.Context, companyID domain.ID, ref string) (service.ToolProfileView, error)
	ListToolProfiles(ctx context.Context, companyID domain.ID) ([]service.ToolProfileView, error)
	UpdateToolProfile(ctx context.Context, actor domain.Actor, companyID, profileID domain.ID, in service.UpdateToolProfileInput) (service.ToolProfileView, error)
	DeleteToolProfile(ctx context.Context, actor domain.Actor, companyID, profileID domain.ID) error
	AddToolProfileRule(ctx context.Context, actor domain.Actor, companyID, profileID domain.ID, in service.ToolRuleInput) (domain.ToolRule, error)
	DeleteToolProfileRule(ctx context.Context, actor domain.Actor, companyID, profileID, ruleID domain.ID) error
	SetIntegrationToolChoices(ctx context.Context, actor domain.Actor, companyID, profileID domain.ID, in service.IntegrationToolChoices) (service.ToolProfileView, error)
	BindToolProfile(ctx context.Context, actor domain.Actor, companyID, profileID domain.ID, in service.ToolBindingInput) (domain.ToolBinding, error)
	UnbindToolProfile(ctx context.Context, actor domain.Actor, companyID, profileID, bindingID domain.ID) error
	EffectiveToolsFor(ctx context.Context, companyID, personaID domain.ID, projectID *domain.ID) (service.EffectiveTools, error)
	CreateToolGateway(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateToolGatewayInput) (service.ToolGatewayView, error)
	ListToolGateways(ctx context.Context, companyID domain.ID) ([]domain.ToolGateway, error)
	GetToolGateway(ctx context.Context, companyID domain.ID, ref string) (service.ToolGatewayView, error)
	UpdateToolGateway(ctx context.Context, actor domain.Actor, companyID, gatewayID domain.ID, in service.UpdateToolGatewayInput) (service.ToolGatewayView, error)
	DeleteToolGateway(ctx context.Context, actor domain.Actor, companyID, gatewayID domain.ID) error
	CreateGatewayToken(ctx context.Context, actor domain.Actor, companyID, gatewayID domain.ID, in service.CreateGatewayTokenInput) (domain.ToolGatewayToken, string, error)
	RevokeGatewayToken(ctx context.Context, actor domain.Actor, companyID, gatewayID, tokenID domain.ID) (domain.ToolGatewayToken, error)
	ListToolInvocations(ctx context.Context, companyID domain.ID, f service.ToolInvocationFilter) ([]domain.ToolInvocation, error)
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
	secure  bool       // language cookie is Secure
	// platform é o que a instalação sabe de si (Configurações › Ambientes, Adapters, Plugins).
	platform PlatformInfo
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
	s := &server{be: be, broker: broker, log: log, version: d.Version, now: now, sseTick: 2 * time.Second, secure: d.Secure, platform: d.Platform}
	if d.Auth != nil {
		ab := d.Auth.Backend
		if ab == nil {
			ab = d.Svc
		}
		s.auth = newAuthState(*d.Auth, ab, now)
		s.secure = s.secure || d.Auth.Secure
	}
	s.routes(mux)
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

type companyHandler func(w http.ResponseWriter, r *http.Request, co domain.Company)

func (s *server) routes(root *http.ServeMux) {
	mux := langMux{root}
	co := func(h companyHandler) http.HandlerFunc { return s.withCompany(h) }
	post := func(h http.HandlerFunc) http.HandlerFunc { return s.sameOrigin(h) }

	mux.Handle("GET /static/", staticHandler())
	mux.HandleFunc("GET "+manifestPath, s.handleManifest)
	mux.HandleFunc("GET "+swPath, s.handleSW)
	mux.HandleFunc("GET "+offlinePath, s.handleOffline)
	mux.HandleFunc("GET /{$}", s.handleRoot)
	mux.HandleFunc("GET /bem-vindo", s.handleOnboarding)
	mux.HandleFunc("POST /empresas", post(s.handleCreateCompany))
	mux.HandleFunc("POST "+langPath, post(s.handleLang))
	mux.HandleFunc("POST "+themePath, post(s.handleTheme))

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
	mux.HandleFunc("PUT /c/{company}/tasks/{task}/rascunho", post(co(s.handleTaskDraft)))
	mux.HandleFunc("POST /c/{company}/tasks/{task}/anexos", post(co(s.handleTaskAttach)))
	mux.HandleFunc("PUT /c/{company}/chat/{conversation}/rascunho", post(co(s.handleChatDraft)))
	mux.HandleFunc("PUT /c/{company}/sala/{conversation}/rascunho", post(co(s.handleRoomDraft)))
	mux.HandleFunc("POST /c/{company}/tasks/{task}/anexos/{attachment}/excluir", post(co(s.handleTaskAttachmentDelete)))
	mux.HandleFunc("POST /c/{company}/tasks/{task}/acordar", post(co(s.handleWake)))
	mux.HandleFunc("POST /c/{company}/tasks/{task}/dependencias", post(co(s.handleAddDependency)))
	mux.HandleFunc("POST /c/{company}/tasks/{task}/dependencias/{dep}/remover", post(co(s.handleRemoveDependency)))
	mux.HandleFunc("POST /c/{company}/tasks/{task}/pausar", post(co(s.handleHold)))
	mux.HandleFunc("POST /c/{company}/tasks/{task}/retomar", post(co(s.handleResume)))
	mux.HandleFunc("POST /c/{company}/tasks/{task}/forcar-conclusao", post(co(s.handleForceClose)))
	mux.HandleFunc("POST /c/{company}/tasks/{task}/pai", post(co(s.handleSetParent)))
	mux.HandleFunc("POST /c/{company}/tasks/{task}/politica-revisao", post(co(s.handleReviewPolicy)))

	mux.HandleFunc("GET /c/{company}/personas", co(s.handlePersonas))
	mux.HandleFunc("GET /c/{company}/personas/nova", co(s.handleNewPersona))
	mux.HandleFunc("GET /c/{company}/personas/catalogo", co(s.handleTeamCatalog))
	mux.HandleFunc("POST /c/{company}/personas/catalogo", post(co(s.handleInstallTeam)))
	mux.HandleFunc("POST /c/{company}/personas/nova", post(co(s.handleHirePersona)))
	mux.HandleFunc("GET /c/{company}/personas/{persona}", co(s.handlePersona))
	s.instructionsRoutes(mux) // revisões das instruções (ADR-0052)
	mux.HandleFunc("POST /c/{company}/personas/{persona}/pausar", post(co(s.handlePersonaControl(domain.PersonaPaused, "pausada"))))
	mux.HandleFunc("POST /c/{company}/personas/{persona}/retomar", post(co(s.handlePersonaControl(domain.PersonaActive, "retomada"))))
	mux.HandleFunc("POST /c/{company}/personas/{persona}/reativar", post(co(s.handlePersonaControl(domain.PersonaActive, "reativada"))))

	mux.HandleFunc("POST /c/{company}/personas/{persona}/orcamento", post(co(s.handleSetPersonaBudget)))

	mux.HandleFunc("GET /c/{company}/projetos", co(s.handleProjects))
	mux.HandleFunc("GET /c/{company}/projetos/novo", co(s.handleNewProject))
	mux.HandleFunc("POST /c/{company}/projetos/novo", post(co(s.handleCreateProject)))
	mux.HandleFunc("GET /c/{company}/projetos/{project}", co(s.handleProject))
	mux.HandleFunc("POST /c/{company}/projetos/{project}", post(co(s.handleUpdateProject)))
	mux.HandleFunc("POST /c/{company}/projetos/{project}/arquivar", post(co(s.handleProjectArchive(true))))
	mux.HandleFunc("POST /c/{company}/projetos/{project}/reativar", post(co(s.handleProjectArchive(false))))
	mux.HandleFunc("POST /c/{company}/projetos/{project}/orcamento", post(co(s.handleSetProjectBudget)))

	mux.HandleFunc("GET /c/{company}/workspaces", co(s.handleWorkspaces))
	mux.HandleFunc("GET /c/{company}/workspaces/novo", co(s.handleNewWorkspace))
	mux.HandleFunc("POST /c/{company}/workspaces/novo", post(co(s.handleCreateWorkspace)))
	mux.HandleFunc("GET /c/{company}/workspaces/{workspace}", co(s.handleWorkspace))
	mux.HandleFunc("POST /c/{company}/workspaces/{workspace}", post(co(s.handleUpdateWorkspace)))
	mux.HandleFunc("POST /c/{company}/workspaces/{workspace}/excluir", post(co(s.handleDeleteWorkspace)))

	mux.HandleFunc("GET /c/{company}/custos", co(s.handleCosts))
	mux.HandleFunc("POST /c/{company}/custos/orcamento", post(co(s.handleSetCompanyBudget)))
	mux.HandleFunc("POST /c/{company}/custos/assinaturas", post(co(s.handleCreateSubscription)))
	mux.HandleFunc("POST /c/{company}/custos/assinaturas/{sub}", post(co(s.handleUpdateSubscription)))
	mux.HandleFunc("POST /c/{company}/custos/assinaturas/{sub}/remover", post(co(s.handleDeleteSubscription)))
	mux.HandleFunc("POST /c/{company}/personas/{persona}/assinatura", post(co(s.handlePersonaSubscription)))

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

	mux.HandleFunc("POST /c/{company}/personas/{persona}/revisor", post(co(s.handlePersonaReviewer)))
	mux.HandleFunc("POST /c/{company}/personas/{persona}/organizacao", post(co(s.handlePersonaOrg)))
	mux.HandleFunc("POST /c/{company}/personas/{persona}/icone", post(co(s.handlePersonaIcon)))

	mux.HandleFunc("GET /c/{company}/caixa", co(s.handleInbox))
	mux.HandleFunc("POST /c/{company}/caixa/tasks/{task}/desbloquear", post(co(s.handleInboxUnblock)))
	mux.HandleFunc("POST /c/{company}/caixa/tasks/{task}/acordar", post(co(s.handleInboxWake)))
	mux.HandleFunc("POST /c/{company}/caixa/tasks/{task}/retomar", post(co(s.handleInboxResume)))
	mux.HandleFunc("POST /c/{company}/caixa/personas/{persona}/reativar", post(co(s.handleInboxReactivate)))
	mux.HandleFunc("POST /c/{company}/caixa/estado", post(co(s.handleInboxState)))
	mux.HandleFunc("GET /c/{company}/caixa/selo", co(s.handleInboxBadge))
	mux.HandleFunc("GET /c/{company}/caixa/eventos", co(s.handleInboxEvents))
	mux.HandleFunc("GET /c/{company}/configuracoes/notificacoes", co(s.handleNotifySettings))
	mux.HandleFunc("POST /c/{company}/configuracoes/notificacoes", post(co(s.handleNotifySave)))
	mux.HandleFunc("POST /c/{company}/configuracoes/notificacoes/teste", post(co(s.handleNotifyTest)))
	mux.HandleFunc("POST /c/{company}/configuracoes/notificacoes/aparelhos", post(co(s.handlePushSubscribe)))
	mux.HandleFunc("POST /c/{company}/configuracoes/notificacoes/aparelhos/remover", post(co(s.handlePushRemove)))
	mux.HandleFunc("GET /c/{company}/decisoes", co(s.handleDecisions))
	mux.HandleFunc("GET /c/{company}/gates", co(s.handleGates))
	mux.HandleFunc("GET /c/{company}/gates/{gate}", co(s.handleGate))
	mux.HandleFunc("POST /c/{company}/gates/{gate}/decidir", post(co(s.handleDecideGate)))
	mux.HandleFunc("GET /c/{company}/chat", co(s.handleChats))
	mux.HandleFunc("POST /c/{company}/chat/nova", post(co(s.handleCreateChat)))
	mux.HandleFunc("GET /c/{company}/chat/{conversation}", co(s.handleChat))
	mux.HandleFunc("GET /c/{company}/chat/{conversation}/mensagens", co(s.handleChatThread))
	mux.HandleFunc("POST /c/{company}/chat/{conversation}/mensagens", post(co(s.handleChatSend)))
	mux.HandleFunc("POST /c/{company}/chat/{conversation}/arquivar", post(co(s.handleChatStatus(domain.ConversationArchived))))
	mux.HandleFunc("POST /c/{company}/chat/{conversation}/reabrir", post(co(s.handleChatStatus(domain.ConversationOpen))))
	mux.HandleFunc("GET /c/{company}/organograma", co(s.handleOrg))

	mux.HandleFunc("GET /c/{company}/configuracoes", co(s.handleSettings))
	mux.HandleFunc("GET /c/{company}/configuracoes/ambientes", co(s.handleEnvironments))
	mux.HandleFunc("GET /c/{company}/configuracoes/adapters", co(s.handleAdapters))
	mux.HandleFunc("GET /c/{company}/configuracoes/dados", co(s.handleData))
	mux.HandleFunc("GET /c/{company}/configuracoes/segredos", co(s.handleSecrets))
	mux.HandleFunc("POST /c/{company}/configuracoes/segredos", post(co(s.handleCreateSecret)))
	mux.HandleFunc("POST /c/{company}/configuracoes/segredos/{name}/rotacionar", post(co(s.handleRotateSecret)))
	mux.HandleFunc("POST /c/{company}/configuracoes/segredos/{name}/revelar", post(co(s.handleRevealSecret)))
	mux.HandleFunc("POST /c/{company}/configuracoes/segredos/{name}/apagar", post(co(s.handleDeleteSecret)))
	mux.HandleFunc("GET /c/{company}/configuracoes/instancia", co(s.handleInstance))
	mux.HandleFunc("POST /c/{company}/configuracoes/instancia/{key}", post(co(s.handleSetInstanceSetting)))
	mux.HandleFunc("POST /c/{company}/configuracoes/instancia/{key}/padrao", post(co(s.handleUnsetInstanceSetting)))
	mux.HandleFunc("POST /c/{company}/configuracoes/senha", post(co(s.handleChangePassword)))
	mux.HandleFunc("POST /c/{company}/configuracoes/chaves", post(co(s.handleCreateKey)))
	mux.HandleFunc("POST /c/{company}/configuracoes/chaves/{key}/revogar", post(co(s.handleRevokeKey)))
	mux.HandleFunc("POST /c/{company}/configuracoes/memoria", post(co(s.handleMemorySave)))
	mux.HandleFunc("POST /c/{company}/configuracoes/memoria/consolidar", post(co(s.handleMemoryConsolidate)))
	mux.HandleFunc("POST /c/{company}/configuracoes/memoria/propostas/{proposal}/repetir", post(co(s.handleMemoryRetry)))
	mux.HandleFunc("POST /c/{company}/configuracoes/membros/convites", post(co(s.handleInviteMember)))
	mux.HandleFunc("POST /c/{company}/configuracoes/membros/convites/{invite}/revogar", post(co(s.handleRevokeInvite)))
	mux.HandleFunc("POST /c/{company}/configuracoes/membros/{user}/papel", post(co(s.handleMemberRole)))
	mux.HandleFunc("POST /c/{company}/configuracoes/membros/{user}/remover", post(co(s.handleMemberRemove)))
	s.authRoutes(mux)

	mux.HandleFunc("GET /c/{company}/atividade", co(s.handleActivity))
	mux.HandleFunc("GET /c/{company}/timeline", co(s.handleTimeline))
	mux.HandleFunc("GET /c/{company}/ao-vivo", co(s.handleLive))
	mux.HandleFunc("GET /c/{company}/artefatos", co(s.handleArtifacts))
	mux.HandleFunc("GET /c/{company}/artefatos/novo", co(s.handleNewArtifact))
	mux.HandleFunc("POST /c/{company}/artefatos/novo", post(co(s.handleUploadArtifact)))
	mux.HandleFunc("GET /c/{company}/artefatos/{artifact}", co(s.handleArtifact))
	mux.HandleFunc("GET /c/{company}/artefatos/{artifact}/conteudo", co(s.handleArtifactContent))
	mux.HandleFunc("POST /c/{company}/artefatos/{artifact}/excluir", post(co(s.handleDeleteArtifact)))

	mux.HandleFunc("GET /c/{company}/metas", co(s.handleGoals))
	mux.HandleFunc("GET /c/{company}/metas/nova", co(s.handleNewGoal))
	mux.HandleFunc("POST /c/{company}/metas/nova", post(co(s.handleCreateGoal)))
	mux.HandleFunc("GET /c/{company}/metas/{goal}", co(s.handleGoal))
	mux.HandleFunc("POST /c/{company}/metas/{goal}", post(co(s.handleUpdateGoal)))
	mux.HandleFunc("POST /c/{company}/metas/{goal}/progresso", post(co(s.handleGoalProgress)))
	mux.HandleFunc("POST /c/{company}/metas/{goal}/status", post(co(s.handleGoalStatus)))
	mux.HandleFunc("POST /c/{company}/metas/{goal}/tasks", post(co(s.handleGoalLinkTask)))

	mux.HandleFunc("GET /c/{company}/buscar", co(s.handleSearch))
	mux.HandleFunc("GET /c/{company}/em-breve/{item}", co(s.handleSoon))
	s.skillRoutes(mux)
	s.roomRoutes(mux) // sala (ADR-0033)
	s.pluginRoutes(mux)
	s.environmentRoutes(mux)
	s.statusCardRoutes(mux)
	s.pipelineRoutes(mux) // pipelines, cases, revisão e aprendizados (ADR-0034)
	s.toolRoutes(mux)     // apps, gateway de tools e perfis (ADR-0036)
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
		role, err := s.be.MyRole(r.Context(), co.ID)
		if err != nil {
			s.fail(w, r, nil, err)
			return
		}
		ctx := views.WithRole(r.Context(), role)
		// Glifo de cada persona para avatares e chips (Kubo DS); sem ele, o padrão. Falha não quebra a página.
		if ps, err := s.be.ListPersonas(ctx, co.ID); err == nil {
			icons := make(map[string]string, 2*len(ps))
			for _, p := range ps {
				icons[p.Name], icons[p.Slug] = p.Icon, p.Icon
			}
			ctx = views.WithPersonaIcons(ctx, icons)
		}
		h(w, r.WithContext(ctx), co)
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
	ctx = s.withSwitcher(ctx, r)
	ctx = views.WithTheme(ctx, requestTheme(r))
	if err := c.Render(ctx, &buf); err != nil {
		s.log.Error("ui render", "path", auth.RedactPath(r.URL.Path), "err", err)
		http.Error(w, i18n.T(ctx, "erro ao renderizar a página"), http.StatusInternalServerError)
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
func friendly(ctx context.Context, err error) (int, string, string) {
	st := statusFor(err)
	switch st {
	case http.StatusNotFound:
		return st, i18n.T(ctx, "Não encontrado"), i18n.T(ctx, "O registro que você procura não existe nesta empresa.")
	case http.StatusUnprocessableEntity:
		var lim *service.ArtifactLimitError
		if errors.As(err, &lim) {
			return st, i18n.T(ctx, "Arquivo recusado"), artifactLimitMessage(ctx, lim)
		}
		var ve *domain.ValidationError
		if errors.As(err, &ve) {
			return st, i18n.T(ctx, "Dados inválidos"), ve.Field + ": " + ve.Reason + "."
		}
		return st, i18n.T(ctx, "Dados inválidos"), i18n.T(ctx, "Confira os campos e tente de novo.")
	case http.StatusConflict:
		var inUse *service.WorkspaceInUseError
		if errors.As(err, &inUse) {
			return st, i18n.T(ctx, "Workspace em uso"), i18n.T(ctx, "Há personas, projetos ou tasks abertas usando este workspace. Desvincule-os ou marque a opção de desvincular ao excluir.")
		}
		var blocked *service.BudgetBlockedError
		if errors.As(err, &blocked) && blocked.Scope == "project" {
			return st, i18n.T(ctx, "Orçamento do projeto estourado"), i18n.T(ctx, "O limite mensal do projeto foi atingido; nenhuma execução das tasks dele abre até o limite subir ou o mês virar.")
		}
		if errors.Is(err, domain.ErrBudgetExceeded) {
			return st, i18n.T(ctx, "Orçamento estourado"), i18n.T(ctx, "O limite mensal foi atingido; nenhuma execução nova abre até o limite subir ou o mês virar.")
		}
		if errors.Is(err, domain.ErrSubscriptionGuard) {
			return st, i18n.T(ctx, "Trava semanal da assinatura"), i18n.T(ctx, "O uso da semana chegou ao limite da assinatura; nenhuma execução nova abre até a semana virar ou o limite subir.")
		}
		if errors.Is(err, domain.ErrActiveRun) {
			return st, i18n.T(ctx, "Já existe uma execução ativa"), i18n.T(ctx, "Esta task já tem uma execução em andamento; espere ela terminar.")
		}
		if errors.Is(err, domain.ErrPersonaUnavailable) {
			return st, i18n.T(ctx, "Persona indisponível"), strings.ToUpper(err.Error()[:1]) + err.Error()[1:] + "."
		}
		return st, i18n.T(ctx, "Conflito"), i18n.T(ctx, "Esse registro já existe ou foi alterado por outra pessoa.")
	case http.StatusForbidden:
		if errors.Is(err, domain.ErrForbidden) {
			return st, i18n.T(ctx, "Ação não permitida"), i18n.T(ctx, "Seu papel nesta empresa não permite esta ação.")
		}
		return st, i18n.T(ctx, "Ação não permitida"), i18n.T(ctx, "A requisição foi bloqueada por segurança (origem diferente).")
	case http.StatusNotImplemented:
		return st, i18n.T(ctx, "Ainda não implementado"), i18n.T(ctx, "Esta função depende de uma parte do servidor que ainda não está disponível.")
	}
	return st, i18n.T(ctx, "Algo deu errado"), i18n.T(ctx, "Erro interno. Tente novamente em instantes.")
}

// fail renders an error as a page (full navigation) or fragment (htmx).
func (s *server) fail(w http.ResponseWriter, r *http.Request, co *domain.Company, err error) {
	st, title, msg := friendly(r.Context(), err)
	if st >= 500 {
		s.log.Error("ui handler", "method", r.Method, "path", auth.RedactPath(r.URL.Path), "err", err)
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
		s.render(w, r, st, views.ErrorPage(s.shell(r, *co, "", title), vm))
		return
	}
	vm.Back = "/"
	s.render(w, r, st, views.ErrorBare(vm, s.version))
}

func (s *server) shell(r *http.Request, co domain.Company, active, title string, crumbs ...views.Crumb) views.Shell {
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
		Role:    views.RoleFrom(r.Context()),
		// com pessoa autenticada a Caixa tem selo e toasts ao vivo (ADR-0042)
		Personal: personal(r.Context()),
	}
}

func trimForm(r *http.Request, key string) string { return strings.TrimSpace(r.PostFormValue(key)) }

// personal diz se a requisição vem de uma pessoa autenticada (sessão ou API key), e não do modo trusted.
func personal(ctx context.Context) bool {
	_, ok := auth.IdentityFrom(ctx)
	return ok
}

// actorFrom is the human actor of the request: the signed-in user in authenticated
// mode, the anonymous local human in trusted mode.
func actorFrom(ctx context.Context) domain.Actor {
	if id, ok := auth.IdentityFrom(ctx); ok {
		return id.User.Actor()
	}
	return domain.HumanActor
}
