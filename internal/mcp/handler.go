// Package mcp expõe o Hanko como servidor MCP (streamable HTTP) para as personas.
// Camada de handler: só chama o pacote service (via a interface Backend).
package mcp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Backend é o subconjunto de *service.Service usado pelas tools.
type Backend interface {
	GetCompany(ctx context.Context, ref string) (domain.Company, error)
	ListPersonas(ctx context.Context, companyID domain.ID) ([]domain.Persona, error)
	ListProjectViews(ctx context.Context, companyID domain.ID) ([]service.ProjectView, error)
	GetProject(ctx context.Context, companyID domain.ID, ref string) (domain.Project, error)
	GetPersona(ctx context.Context, companyID domain.ID, ref string) (domain.Persona, error)
	CreateTask(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateTaskInput) (service.TaskView, error)
	ListTasks(ctx context.Context, companyID domain.ID, f service.TaskFilter) ([]service.TaskView, error)
	GetTask(ctx context.Context, companyID domain.ID, ref string) (service.TaskView, error)
	UpdateTask(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID, in service.UpdateTaskInput) (service.TaskView, error)
	AddComment(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID, in service.AddCommentInput) (domain.Comment, error)
	ListComments(ctx context.Context, companyID, taskID domain.ID) ([]domain.Comment, error)
	GetRun(ctx context.Context, companyID, runID domain.ID) (service.RunView, error)
	RequestGate(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.RequestGateInput) (service.GateView, error)
	GetGate(ctx context.Context, companyID, gateID domain.ID) (service.GateView, error)
	ListGates(ctx context.Context, companyID domain.ID, f service.GateFilter) ([]service.GateView, error)
	Search(ctx context.Context, companyID domain.ID, q string, f service.SearchFilter) (service.SearchResult, error)
}

var _ Backend = (*service.Service)(nil)

// Deps são as dependências do servidor MCP.
type Deps struct {
	Svc     *service.Service
	Tokens  auth.RunTokens
	Logger  *slog.Logger
	Version string
	// Trusted permite chamadas sem token (loopback): ator humano, empresa por argumento.
	// No modo authenticated é false: só run token ou API key (via auth.Guard).
	Trusted bool
}

type ctxKey struct{}

// identity é quem está chamando, resolvido por request.
type identity struct {
	Actor     domain.Actor
	CompanyID *domain.ID // nil no modo trusted sem token
	PersonaID *domain.ID
	RunID     *domain.ID
	TaskID    *domain.ID
	ViaToken  bool
}

func identityFrom(ctx context.Context) (identity, bool) {
	id, ok := ctx.Value(ctxKey{}).(identity)
	return id, ok
}

// Handler devolve o http.Handler do endpoint /mcp.
func Handler(d Deps) http.Handler {
	return newHandler(d.Svc, d)
}

func newHandler(b Backend, d Deps) http.Handler {
	logger := d.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	version := d.Version
	if version == "" {
		version = "dev"
	}
	srv := sdk.NewServer(&sdk.Implementation{Name: "hanko", Title: "Hanko", Version: version}, &sdk.ServerOptions{
		Instructions: "Hanko: control plane de agentes. Use as tools hanko_* para ler e atualizar tasks, comentar e consultar execuções.",
		Logger:       logger,
	})
	registerTools(srv, &tools{b: b, log: logger})

	inner := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return srv },
		// A proteção contra DNS rebinding é do app (host check 421 com HANKO_PUBLIC_URL/HANKO_ALLOWED_HOSTS);
		// a do SDK recusaria o Host público quando um proxy reverso fala com o Hanko em loopback.
		&sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true, Logger: logger, DisableLocalhostProtection: true})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := authenticate(r, d, b)
		if err != nil {
			logger.Warn("mcp: auth negada", "reason", err.Error(), "remote", r.RemoteAddr)
			if errors.Is(err, errUnavailable) {
				http.Error(w, "autenticação indisponível", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("WWW-Authenticate", `Bearer realm="hanko"`)
			http.Error(w, "unauthorized: "+err.Error(), http.StatusUnauthorized)
			return
		}
		inner.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
	})
}

var (
	errNoToken     = errors.New("missing bearer run token")
	errUnavailable = errors.New("auth backend unavailable")
)

// runStatus adapta Backend.GetRun para auth.RunStatusFunc.
func runStatus(b interface {
	GetRun(ctx context.Context, companyID, runID domain.ID) (service.RunView, error)
	RequestGate(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.RequestGateInput) (service.GateView, error)
	GetGate(ctx context.Context, companyID, gateID domain.ID) (service.GateView, error)
	ListGates(ctx context.Context, companyID domain.ID, f service.GateFilter) ([]service.GateView, error)
}) auth.RunStatusFunc {
	return func(ctx context.Context, cid, rid domain.ID) (domain.RunStatus, error) {
		run, err := b.GetRun(ctx, cid, rid)
		return run.Status, err
	}
}

func authenticate(r *http.Request, d Deps, b Backend) (identity, error) {
	// API key de usuário já validada pelo auth.Guard (modo authenticated): ator humano com o ID do usuário.
	if uid, ok := auth.IdentityFrom(r.Context()); ok && uid.Via == auth.ViaAPIKey {
		return identity{Actor: uid.User.Actor()}, nil
	}
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if h == "" {
		if d.Trusted {
			return identity{Actor: domain.HumanActor}, nil
		}
		return identity{}, errNoToken
	}
	scheme, tok, ok := strings.Cut(h, " ")
	if !ok || !strings.EqualFold(scheme, "bearer") || strings.TrimSpace(tok) == "" {
		return identity{}, errors.New("malformed Authorization header")
	}
	claims, err := d.Tokens.VerifyActive(r.Context(), strings.TrimSpace(tok), runStatus(b))
	if err != nil {
		if errors.Is(err, auth.ErrBadToken) {
			return identity{}, errors.New("invalid or expired run token, or run no longer active")
		}
		return identity{}, errUnavailable
	}
	actor, err := claims.Actor()
	if err != nil {
		return identity{}, errors.New("invalid run token claims")
	}
	cid, err := domain.ParseID(claims.CompanyID)
	if err != nil {
		return identity{}, errors.New("invalid run token claims")
	}
	id := identity{Actor: actor, CompanyID: &cid, PersonaID: actor.ID, ViaToken: true}
	if rid, err := domain.ParseID(claims.RunID); err == nil {
		id.RunID = &rid
	}
	if tid, err := domain.ParseID(claims.TaskID); err == nil {
		id.TaskID = &tid
	}
	return id, nil
}
