package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/service"
)

// MemoryPath é onde o proxy de memória é montado no mux do Hanko.
const MemoryPath = "/memory-mcp"

const (
	defaultMemoryProject = "default"
	maxProxyBody         = 4 << 20
	lookupTimeout        = 5 * time.Second
)

// MemoryBackend é o que o proxy de memória precisa do service.
type MemoryBackend interface {
	GetCompany(ctx context.Context, ref string) (domain.Company, error)
	GetPersona(ctx context.Context, companyID domain.ID, ref string) (domain.Persona, error)
	GetTask(ctx context.Context, companyID domain.ID, ref string) (service.TaskView, error)
	GetWorkspace(ctx context.Context, companyID domain.ID, ref string) (domain.Workspace, error)
	GetRun(ctx context.Context, companyID, runID domain.ID) (service.RunView, error)
	// MemoryAccessTo: concessão da persona à memória de outro workspace (vazio = nenhuma). ADR-0030.
	MemoryAccessTo(ctx context.Context, companyID, personaID domain.ID, workspaceSlug string) (domain.MemoryAccess, error)
	// RecordMemoryProposal retém uma escrita da curadoria como proposta com gate humano. ADR-0030.
	RecordMemoryProposal(ctx context.Context, companyID domain.ID, in service.RecordProposalInput) (service.MemoryProposalView, bool, error)
}

var _ MemoryBackend = (*service.Service)(nil)

// MemoryProxyDeps configura o proxy de memória.
type MemoryProxyDeps struct {
	Backend MemoryBackend
	Tokens  auth.RunTokens
	// URL é a base do servidor de memória (ex.: http://127.0.0.1:49374); o proxy fala com <URL>/mcp.
	URL string
	// Token é o bearer do servidor de memória. Nunca chega ao agente.
	Token string
	// Pages é opcional: lê o conteúdo atual de uma página para o diff das propostas de curadoria.
	Pages memory.PageReader
	// Transport é opcional (testes).
	Transport http.RoundTripper
	Logger    *slog.Logger
}

// MemoryProxy devolve o handler de /memory-mcp: reverse proxy autenticado por run token
// de run ativo para <URL>/mcp. Descarta Authorization e X-Memory-* do cliente e injeta o
// bearer do servidor de memória e o escopo (empresa do token, projeto do workspace da task).
// O escopo vem sempre do token, nunca do cliente. Sem fallback trusted.
func MemoryProxy(d MemoryProxyDeps) (http.Handler, error) {
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(d.URL), "/"))
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") {
		return nil, fmt.Errorf("proxy de memória: URL inválida %q", d.URL)
	}
	log := d.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	p := &memoryProxy{d: d, log: log}
	p.rp = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			sc, _ := pr.In.Context().Value(memScopeKey{}).(memScope)
			pr.SetURL(base)
			pr.Out.URL.Path = strings.TrimRight(base.Path, "/") + "/mcp"
			pr.Out.URL.RawPath = ""
			pr.Out.URL.RawQuery = ""
			h := pr.Out.Header
			h.Del("Authorization")
			h.Del("Cookie")
			for k := range h {
				if strings.HasPrefix(strings.ToLower(k), "x-memory-") {
					h.Del(k)
				}
			}
			if d.Token != "" {
				h.Set("Authorization", "Bearer "+d.Token)
			}
			h.Set("X-Memory-Workspace", sc.workspace)
			h.Set("X-Memory-Project", sc.project)
		},
		FlushInterval: -1, // streaming (SSE) sem buffer
		Transport:     d.Transport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Warn("memory-mcp: upstream falhou", "err", err)
			http.Error(w, "memória indisponível", http.StatusBadGateway)
		},
	}
	return p, nil
}

type memScopeKey struct{}

type memScope struct{ workspace, project string }

type memoryProxy struct {
	d   MemoryProxyDeps
	log *slog.Logger
	rp  *httputil.ReverseProxy
}

func (p *memoryProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tok, ok := bearerToken(r)
	if !ok {
		p.unauthorized(w, "missing bearer run token")
		return
	}
	var run service.RunView
	claims, err := p.d.Tokens.VerifyActive(r.Context(), tok, func(ctx context.Context, cid, rid domain.ID) (domain.RunStatus, error) {
		rv, err := p.d.Backend.GetRun(ctx, cid, rid)
		run = rv
		return rv.Status, err
	})
	if err != nil {
		if errors.Is(err, auth.ErrBadToken) {
			p.unauthorized(w, "invalid or expired run token, or run no longer active")
			return
		}
		p.log.Error("memory-mcp: autenticação falhou", "err", err)
		http.Error(w, "autenticação indisponível", http.StatusServiceUnavailable)
		return
	}
	sc, err := p.scopeFor(r.Context(), claims, run)
	if err != nil {
		p.log.Error("memory-mcp: resolver escopo", "err", err, "run_id", claims.RunID)
		http.Error(w, "escopo de memória indisponível", http.StatusServiceUnavailable)
		return
	}
	if r.Body != nil && r.Method == http.MethodPost {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxProxyBody))
		if err != nil {
			http.Error(w, "corpo inválido ou grande demais", http.StatusRequestEntityTooLarge)
			return
		}
		var done bool
		home := sc
		body, sc, done = p.screen(r.Context(), w, claims, run, sc, body)
		if done {
			return // respondido aqui (proposta registrada, chamada recusada)
		}
		body = pinScopeArgs(body, sc, home)
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		r.Header.Del("Content-Length")
	}
	p.rp.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), memScopeKey{}, sc)))
}

func (p *memoryProxy) unauthorized(w http.ResponseWriter, reason string) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="hanko"`)
	http.Error(w, "unauthorized: "+reason, http.StatusUnauthorized)
}

// scopeFor deriva workspace (slug da empresa do token) e project (slug do workspace que a run usou, na ordem task,
// projeto, persona; senão "default"). Run antiga, sem a origem gravada, segue pela task e pela persona.
func (p *memoryProxy) scopeFor(ctx context.Context, c auth.RunClaims, run service.RunView) (memScope, error) {
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	cid, err := domain.ParseID(c.CompanyID)
	if err != nil {
		return memScope{}, err
	}
	co, err := p.d.Backend.GetCompany(ctx, cid.String())
	if err != nil {
		return memScope{}, fmt.Errorf("empresa: %w", err)
	}
	sc := memScope{workspace: co.Slug, project: defaultMemoryProject}
	if run.WorkspaceSource != "" {
		return p.scopeOfRun(ctx, cid, sc, run)
	}
	var wsID *domain.ID
	if task, err := p.d.Backend.GetTask(ctx, cid, c.TaskID); err == nil {
		wsID = task.WorkspaceID
	} else if !errors.Is(err, domain.ErrNotFound) {
		return memScope{}, fmt.Errorf("task: %w", err)
	}
	if wsID == nil {
		if per, err := p.d.Backend.GetPersona(ctx, cid, c.PersonaID); err == nil {
			wsID = per.WorkspaceID
		} else if !errors.Is(err, domain.ErrNotFound) {
			return memScope{}, fmt.Errorf("persona: %w", err)
		}
	}
	if wsID != nil {
		ws, err := p.d.Backend.GetWorkspace(ctx, cid, wsID.String())
		switch {
		case err == nil && ws.Slug != "":
			sc.project = ws.Slug
		case err != nil && !errors.Is(err, domain.ErrNotFound):
			return memScope{}, fmt.Errorf("workspace: %w", err)
		}
	}
	return sc, nil
}

func (p *memoryProxy) scopeOfRun(ctx context.Context, cid domain.ID, sc memScope, run service.RunView) (memScope, error) {
	if run.WorkspaceID == nil {
		return sc, nil
	}
	ws, err := p.d.Backend.GetWorkspace(ctx, cid, run.WorkspaceID.String())
	switch {
	case err == nil && ws.Slug != "":
		sc.project = ws.Slug
	case err != nil && !errors.Is(err, domain.ErrNotFound):
		return memScope{}, fmt.Errorf("workspace: %w", err)
	}
	return sc, nil
}

func bearerToken(r *http.Request) (string, bool) {
	scheme, tok, ok := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
	tok = strings.TrimSpace(tok)
	return tok, ok && strings.EqualFold(scheme, "bearer") && tok != ""
}

// pinScopeArgs reescreve o corpo com o que o proxy leu e fixa o escopo de cada tools/call. O escopo vai sempre nos
// argumentos: o servidor ignora os headers X-Memory-* e, sem argumentos, resolve um escopo padrão compartilhado. home é
// o escopo da run; sc pode ser outro workspace, com concessão. O corpo já passou por parseToolCalls.
func pinScopeArgs(body []byte, sc, home memScope) []byte {
	trim := bytes.TrimSpace(body)
	if len(trim) == 0 {
		return body
	}
	var (
		out any
		err error
	)
	if trim[0] == '[' {
		var batch []map[string]json.RawMessage
		if err = json.Unmarshal(trim, &batch); err == nil {
			for _, msg := range batch {
				pinMessage(msg, sc, home)
			}
			out = batch
		}
	} else {
		var msg map[string]json.RawMessage
		if err = json.Unmarshal(trim, &msg); err == nil {
			pinMessage(msg, sc, home)
			out = msg
		}
	}
	if err != nil {
		return body
	}
	b, err := json.Marshal(out)
	if err != nil {
		return body
	}
	return b
}

func pinMessage(msg map[string]json.RawMessage, sc, home memScope) {
	var method string
	if json.Unmarshal(msg["method"], &method) != nil || method != "tools/call" {
		return
	}
	var params map[string]json.RawMessage
	if json.Unmarshal(msg["params"], &params) != nil {
		return
	}
	var name string
	if json.Unmarshal(params["name"], &name) != nil {
		return
	}
	args := map[string]json.RawMessage{}
	if raw, has := params["arguments"]; has {
		if json.Unmarshal(raw, &args) != nil || args == nil {
			args = map[string]json.RawMessage{}
		}
	}
	pinToolArgs(name, args, sc, home)
	params["arguments"], _ = json.Marshal(args)
	msg["params"], _ = json.Marshal(params)
}

func pinToolArgs(name string, args map[string]json.RawMessage, sc, home memScope) {
	set := func(k, v string) { args[k], _ = json.Marshal(v) }
	switch {
	case name == memory.QueryTool:
		// Um item em scopes busca só nesse escopo e deixa de fora os global_scope_hits.
		delete(args, "workspace")
		delete(args, "project")
		args["scopes"], _ = json.Marshal([]map[string]string{{"workspace": sc.workspace, "project": sc.project}})
	case name == memory.MessageSendTool:
		set("to_workspace", sc.workspace)
		set("to_project", sc.project)
		set("from_workspace", home.workspace)
		set("from_project", home.project)
	case memory.ScopedTool(name):
		set("workspace", sc.workspace)
		set("project", sc.project)
	}
}
