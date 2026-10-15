// Package aimemory implementa memory.Provider sobre o servidor de memória ai-memory
// (ADR-0015): cliente MCP streamable HTTP em <URL>/mcp mais hooks HTTP.
package aimemory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/renatobardi/hanko/internal/memory"
)

const (
	// Name é o nome do provider.
	Name = "ai-memory"
	// MaxBriefingBytes limita o texto de briefing/handoff devolvido ao executor.
	MaxBriefingBytes = 8 << 10

	defaultTimeout = 5 * time.Second
	defaultProject = "default"
	truncMarker    = "\n\n[…truncado]"
)

// Options configura o provider.
type Options struct {
	URL   string // base do ai-memory, ex. http://ai-memory:8080
	Token string // bearer token (só do Hanko ao ai-memory; nunca vai ao agente)
	// PublicURL é a base pela qual o agente alcança o Hanko (proxy /memory-mcp em AgentMCP).
	PublicURL string
	HTTP      *http.Client  // opcional
	Logger    *slog.Logger  // opcional
	Timeout   time.Duration // por chamada; padrão 5s
	// ReadPageTool é o nome da tool que lê uma página por caminho (ex. a do ai-memory), usada só para mostrar o
	// conteúdo atual no diff das propostas de curadoria. Vazio = o diff mostra só o conteúdo proposto.
	ReadPageTool string
}

type provider struct {
	base    string
	token   string
	public  string
	hc      *http.Client // com Authorization injetado, usado no MCP e nos hooks
	log     *slog.Logger
	timeout time.Duration

	readTool string

	mu   sync.Mutex
	sess *sdk.ClientSession

	now       func() time.Time
	capMu     sync.Mutex
	capAt     time.Time
	capKnown  bool // a lista de tools foi lida (capOK vale); senão o palpite é "suportado"
	capOK     bool
	capLogged bool
}

// New cria o provider. Não conecta: a sessão MCP é aberta na primeira chamada.
func New(o Options) memory.Provider {
	p := &provider{
		base:    strings.TrimRight(strings.TrimSpace(o.URL), "/"),
		token:   o.Token,
		public:  strings.TrimRight(strings.TrimSpace(o.PublicURL), "/"),
		log:     o.Logger,
		timeout: o.Timeout,
		now:     time.Now,

		readTool: strings.TrimSpace(o.ReadPageTool),
	}
	if p.log == nil {
		p.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if p.timeout <= 0 {
		p.timeout = defaultTimeout
	}
	base := o.HTTP
	if base == nil {
		base = &http.Client{}
	}
	hc := *base
	rt := hc.Transport
	if rt == nil {
		rt = http.DefaultTransport
	}
	hc.Transport = &bearerTransport{token: o.Token, next: rt}
	p.hc = &hc
	return p
}

type bearerTransport struct {
	token string
	next  http.RoundTripper
}

func (b *bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if b.token != "" && r.Header.Get("Authorization") == "" {
		r = r.Clone(r.Context())
		r.Header.Set("Authorization", "Bearer "+b.token)
	}
	return b.next.RoundTrip(r)
}

func (p *provider) Name() string { return Name }

func scopeArgs(s memory.Scope) map[string]any {
	proj := s.Project
	if proj == "" {
		proj = defaultProject
	}
	return map[string]any{"workspace": s.Workspace, "project": proj}
}

// ---- sessão MCP ----

// rejectedError: o servidor respondeu, mas recusou a chamada (erro de tool ou JSON-RPC).
type rejectedError struct{ msg string }

func (e *rejectedError) Error() string { return e.msg }

func (p *provider) session(ctx context.Context) (*sdk.ClientSession, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sess != nil {
		return p.sess, false, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	c := sdk.NewClient(&sdk.Implementation{Name: "hanko", Title: "Hanko", Version: "0"}, nil)
	// O SDK pode bloquear no Close de um Connect que falhou (espera o servidor);
	// abandonamos a chamada no deadline para nunca passar do timeout.
	cs, err := bounded(ctx, func() (*sdk.ClientSession, error) {
		return c.Connect(ctx, &sdk.StreamableClientTransport{
			Endpoint:             p.base + "/mcp",
			HTTPClient:           p.hc,
			MaxRetries:           -1,
			DisableStandaloneSSE: true,
		}, nil)
	}, func(cs *sdk.ClientSession) { _ = cs.Close() })
	if err != nil {
		return nil, true, fmt.Errorf("conectar MCP: %w", err)
	}
	p.sess = cs
	return cs, true, nil
}

func (p *provider) drop(cs *sdk.ClientSession) {
	p.mu.Lock()
	if p.sess == cs {
		p.sess = nil
	}
	p.mu.Unlock()
	if cs != nil {
		go cs.Close()
	}
}

// callTool chama uma tool e devolve o texto. Reconecta uma vez se a sessão reutilizada falhar.
func (p *provider) callTool(ctx context.Context, tool string, args map[string]any) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	for attempt := 0; ; attempt++ {
		cs, fresh, err := p.session(ctx)
		if err != nil {
			return "", fmt.Errorf("ai-memory %s: %w", tool, err)
		}
		res, err := bounded(ctx, func() (*sdk.CallToolResult, error) {
			return cs.CallTool(ctx, &sdk.CallToolParams{Name: tool, Arguments: args})
		}, nil)
		if err != nil {
			var je *jsonrpc.Error
			if errors.As(err, &je) {
				return "", &rejectedError{fmt.Sprintf("ai-memory %s: %v", tool, err)}
			}
			p.drop(cs)
			if !fresh && attempt == 0 && ctx.Err() == nil {
				p.log.Debug("ai-memory: sessão perdida, reconectando", "tool", tool)
				continue
			}
			return "", fmt.Errorf("ai-memory %s: %w", tool, err)
		}
		txt := resultText(res)
		if res.IsError {
			return "", &rejectedError{fmt.Sprintf("ai-memory %s: erro da tool: %s", tool, clip(txt, 300))}
		}
		return txt, nil
	}
}

// bounded executa f e retorna no deadline de ctx mesmo que f demore a voltar.
// Se f terminar depois, o valor tardio é entregue a discard (quando não nil).
func bounded[T any](ctx context.Context, f func() (T, error), discard func(T)) (T, error) {
	type result struct {
		v   T
		err error
	}
	ch := make(chan result, 1)
	go func() {
		v, err := f()
		ch <- result{v, err}
	}()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-ctx.Done():
		go func() {
			if r := <-ch; r.err == nil && discard != nil {
				discard(r.v)
			}
		}()
		var zero T
		return zero, ctx.Err()
	}
}

func resultText(r *sdk.CallToolResult) string {
	var parts []string
	for _, c := range r.Content {
		if tc, ok := c.(*sdk.TextContent); ok && tc.Text != "" {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// truncate limita s a max bytes (incluindo o marcador), sem cortar runas.
func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	cut := max - len(truncMarker)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + truncMarker
}

// ---- memory.Provider ----

func (p *provider) Briefing(ctx context.Context, s memory.Scope) (string, error) {
	args := scopeArgs(s)
	args["recent_pages_limit"] = 10
	txt, err := p.callTool(ctx, "memory_briefing", args)
	if err != nil {
		return "", err
	}
	return truncate(txt, MaxBriefingBytes), nil
}

func (p *provider) AcceptHandoff(ctx context.Context, s memory.Scope) (string, error) {
	txt, err := p.callTool(ctx, "memory_handoff_accept", scopeArgs(s))
	if err != nil {
		return "", err
	}
	return truncate(txt, MaxBriefingBytes), nil
}

func (p *provider) Record(ctx context.Context, s memory.Scope, f memory.Fact) error {
	args := scopeArgs(s)
	args["path"], args["body"], args["title"] = f.Path, f.Body, f.Title
	args["kind"] = "hanko"
	if len(f.Tags) > 0 {
		args["tag"] = f.Tags
	}
	_, err := p.callTool(ctx, "memory_write_page", args)
	var rej *rejectedError
	if err != nil && errors.As(err, &rej) {
		// Servidor sem suporte a kind/tag: reenvia só com o essencial.
		p.log.Debug("ai-memory: write_page recusada, reenviando sem kind/tag", "path", f.Path)
		delete(args, "kind")
		delete(args, "tag")
		_, err = p.callTool(ctx, "memory_write_page", args)
	}
	if err != nil {
		return fmt.Errorf("registrar %s: %w", f.Path, err)
	}
	return nil
}

// SearchTool é o nome da tool de busca do ai-memory. O contrato (nome, argumentos workspace/query/limit e formato do
// resultado) NÃO está confirmado contra um servidor real: o teste usa um servidor MCP falso e parseSearch aceita
// JSON ({"results":[…]} ou […]) ou texto livre. Se o nome real for outro, é só esta constante (ADR-0024).
const SearchTool = "memory_search"

// Search consulta a memória da empresa inteira (todos os projetos do workspace).
func (p *provider) Search(ctx context.Context, s memory.Scope, query string, limit int) ([]memory.Hit, error) {
	if limit <= 0 {
		limit = 10
	}
	txt, err := p.callTool(ctx, SearchTool, map[string]any{"workspace": s.Workspace, "query": query, "limit": limit})
	if err != nil {
		return nil, err
	}
	hits := parseSearch(txt)
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

const (
	capTTL    = 5 * time.Minute  // validade da lista de tools lida com sucesso
	capErrTTL = 30 * time.Second // validade do "não sei" quando listar falha (evita um timeout por tecla)
)

// SearchSupported diz se o servidor expõe a tool de busca (SearchTool). Lê tools/list e guarda o resultado por
// alguns minutos; se não conseguir listar (servidor fora), responde que sim — a busca em si marcará o grupo como
// indisponível. Quando a tool não existe, registra um aviso uma única vez (não a cada tecla).
func (p *provider) SearchSupported(ctx context.Context) bool {
	p.capMu.Lock()
	defer p.capMu.Unlock()
	if !p.capAt.IsZero() {
		ttl := capTTL
		if !p.capKnown {
			ttl = capErrTTL
		}
		if p.now().Sub(p.capAt) < ttl {
			return !p.capKnown || p.capOK
		}
	}
	names, err := p.listTools(ctx)
	p.capAt = p.now()
	if err != nil {
		p.capKnown = false
		p.log.Debug("ai-memory: não foi possível listar as tools", "err", err.Error())
		return true
	}
	p.capKnown = true
	p.capOK = false
	for _, n := range names {
		if n == SearchTool {
			p.capOK = true
			break
		}
	}
	if !p.capOK && !p.capLogged {
		p.capLogged = true
		p.log.Warn("ai-memory não expõe a tool de busca; o grupo Memória fica fora da busca", "tool", SearchTool)
	}
	if p.capOK {
		p.capLogged = false
	}
	return p.capOK
}

func (p *provider) listTools(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	cs, _, err := p.session(ctx)
	if err != nil {
		return nil, err
	}
	res, err := bounded(ctx, func() (*sdk.ListToolsResult, error) { return cs.ListTools(ctx, nil) }, nil)
	if err != nil {
		p.drop(cs)
		return nil, err
	}
	names := make([]string, 0, len(res.Tools))
	for _, t := range res.Tools {
		names = append(names, t.Name)
	}
	return names, nil
}

// parseSearch entende o resultado da tool de busca: JSON (lista, ou objeto com results/hits/pages/items) com
// path/title/snippet; ou texto livre, em que cada bloco separado por linha em branco vira um resultado.
func parseSearch(txt string) []memory.Hit {
	txt = strings.TrimSpace(txt)
	if txt == "" {
		return nil
	}
	type raw struct {
		Path    string  `json:"path"`
		Title   string  `json:"title"`
		Snippet string  `json:"snippet"`
		Excerpt string  `json:"excerpt"`
		Content string  `json:"content"`
		Body    string  `json:"body"`
		Score   float64 `json:"score"`
	}
	conv := func(rs []raw) []memory.Hit {
		out := make([]memory.Hit, 0, len(rs))
		for _, r := range rs {
			sn := firstNonEmpty(r.Snippet, r.Excerpt, r.Content, r.Body)
			if r.Path == "" && r.Title == "" && sn == "" {
				continue
			}
			out = append(out, memory.Hit{Path: r.Path, Title: r.Title, Snippet: clip(strings.TrimSpace(sn), 2000), Score: r.Score})
		}
		return out
	}
	if txt[0] == '[' {
		var rs []raw
		if json.Unmarshal([]byte(txt), &rs) == nil {
			return conv(rs)
		}
	}
	if txt[0] == '{' {
		var env struct {
			Results []raw `json:"results"`
			Hits    []raw `json:"hits"`
			Pages   []raw `json:"pages"`
			Items   []raw `json:"items"`
		}
		if json.Unmarshal([]byte(txt), &env) == nil {
			for _, rs := range [][]raw{env.Results, env.Hits, env.Pages, env.Items} {
				if len(rs) > 0 {
					return conv(rs)
				}
			}
			return nil
		}
	}
	var out []memory.Hit
	for _, blk := range strings.Split(txt, "\n\n") {
		blk = strings.TrimSpace(blk)
		if blk == "" {
			continue
		}
		first, rest, _ := strings.Cut(blk, "\n")
		out = append(out, memory.Hit{Title: clip(strings.TrimLeft(strings.TrimSpace(first), "#-* "), 200), Snippet: clip(strings.TrimSpace(rest), 2000)})
	}
	return out
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

type hookItem struct {
	URL  string         `json:"url"`
	Body map[string]any `json:"body"`
}

func (p *provider) FinalizeSession(ctx context.Context, s memory.Scope, agent, sessionID string) error {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	proj := s.Project
	if proj == "" {
		proj = defaultProject
	}
	q := url.Values{}
	q.Set("event", "session-end")
	q.Set("agent", agent)
	q.Set("cwd", s.Cwd)
	q.Set("workspace", s.Workspace)
	q.Set("project", proj)
	payload, err := json.Marshal([]hookItem{{
		URL:  p.base + "/hook?" + q.Encode(),
		Body: map[string]any{"session_id": sessionID, "cwd": s.Cwd},
	}})
	if err != nil {
		return fmt.Errorf("finalizar sessão: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.base+"/hook/batch", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("finalizar sessão: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.hc.Do(req)
	if err != nil {
		return fmt.Errorf("finalizar sessão: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("finalizar sessão: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	return nil
}

type mcpEntry struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

// AgentMCP aponta o agente para o proxy /memory-mcp do Hanko, autenticado pelo run token
// do próprio run. O token e o escopo do ai-memory ficam só no servidor: o proxy os injeta.
// Sem PublicURL ou sem run token não há o que injetar (nunca um "Bearer " vazio).
func (p *provider) AgentMCP(_ memory.Scope, _, runToken string) map[string]json.RawMessage {
	if p.public == "" || runToken == "" {
		return nil
	}
	b, err := json.Marshal(mcpEntry{Type: "http", URL: p.public + "/memory-mcp",
		Headers: map[string]string{"Authorization": "Bearer " + runToken}})
	if err != nil {
		return nil
	}
	return map[string]json.RawMessage{Name: b}
}

func (p *provider) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base+"/health", nil)
	if err != nil {
		return fmt.Errorf("ping ai-memory: %w", err)
	}
	var healthErr error
	resp, err := p.hc.Do(req)
	if err == nil {
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		if resp.StatusCode/100 == 2 {
			return nil
		}
		healthErr = fmt.Errorf("/health: HTTP %d", resp.StatusCode)
	} else {
		healthErr = err
	}
	if ctx.Err() != nil {
		return fmt.Errorf("ping ai-memory: %w", healthErr)
	}
	if _, err := p.callTool(ctx, "memory_status", map[string]any{}); err != nil {
		return fmt.Errorf("ping ai-memory: %v; fallback: %w", healthErr, err)
	}
	return nil
}
