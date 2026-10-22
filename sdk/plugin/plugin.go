// Package plugin é o SDK Go para escrever plugins do Hanko (ADR-0038, ADR-0045). Só biblioteca padrão.
//
// Um plugin é um executável que fala JSON-RPC 2.0 por stdin/stdout, uma mensagem por linha. O SDK cuida do
// protocolo (initialize, health, shutdown, despacho concorrente, chamadas ao host) e deixa ao autor só o que o
// plugin faz:
//
//	p := plugin.New("meu-plugin")
//	p.Tool("minha_tool", func(ctx context.Context, h *plugin.Host, c plugin.ToolCall) (plugin.ToolResult, error) {
//		return plugin.ToolResult{Text: "oi"}, nil
//	})
//	p.Page("painel", func(ctx context.Context, h *plugin.Host, r plugin.PageRequest) (string, error) {
//		return "<h1>Olá</h1>", nil
//	})
//	log.Fatal(p.Run(context.Background()))
//
// O manifesto (hanko-plugin.json) declara o que o plugin contribui; o host só chama o que o manifesto declara e o
// dono da instalação concedeu como capacidade. `hanko plugin new` gera um esqueleto completo.
package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// APIVersion é a versão do protocolo que este SDK fala.
const APIVersion = 1

// Códigos de erro do host (docs/PLUGINS.md).
const (
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternal       = -32603
	CodeDenied         = -32001
	CodeBusy           = -32002
	CodeNotFound       = -32004
	CodeInvalid        = -32005
)

// Error é um erro JSON-RPC. Devolva-o dos handlers para escolher o código; qualquer outro erro vira -32000.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return fmt.Sprintf("%d: %s", e.Code, e.Message) }

// Errorf monta um *Error.
func Errorf(code int, format string, a ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, a...)}
}

// Company identifica a empresa de uma chamada.
type Company struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// User é quem abriu a página (nunca traz o e-mail). Role: viewer, member, admin ou owner.
type User struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

// Event é um fato da atividade entregue a OnEvent (pelo menos uma vez: deduplique por ID).
type Event struct {
	ID         string          `json:"id"`
	Verb       string          `json:"verb"`
	OccurredAt time.Time       `json:"occurred_at"`
	Actor      Ref             `json:"actor"`
	Subject    Ref             `json:"subject"`
	Data       json.RawMessage `json:"data"`
}

// Ref é um tipo e um id.
type Ref struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// ToolCall é a chamada de uma tool por uma persona.
type ToolCall struct {
	Company   Company         `json:"company"`
	Persona   Persona         `json:"persona"`
	RunID     string          `json:"run_id"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
}

// Persona é quem chamou a tool.
type Persona struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// ToolResult é a resposta de uma tool. IsError mostra o texto à persona como erro da tool.
type ToolResult struct {
	Text    string `json:"text"`
	Data    any    `json:"data,omitempty"`
	IsError bool   `json:"is_error,omitempty"`
}

// WidgetRequest pede o conteúdo de um widget; Context diz onde ele aparece (type: dashboard, task, persona).
type WidgetRequest struct {
	Company Company        `json:"company"`
	Key     string         `json:"key"`
	Context map[string]any `json:"context"`
}

// Widget é o conteúdo (dados) de um widget: markdown e/ou tabela. Nenhum HTML chega ao navegador por aqui.
type Widget struct {
	Title    string `json:"title,omitempty"`
	Markdown string `json:"markdown,omitempty"`
	Table    *Table `json:"table,omitempty"`
}

// Table é a tabela simples de um widget.
type Table struct {
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
}

// JobRequest é a rodada de um job periódico.
type JobRequest struct {
	Company Company `json:"company"`
	Key     string  `json:"key"`
}

// PageRequest pede o HTML de uma página (aba).
type PageRequest struct {
	Company Company `json:"company"`
	Page    string  `json:"page"`
	User    User    `json:"user"`
}

// BridgeCall é uma chamada feita pela página (via window.hanko.call) ao próprio plugin. O Hanko só entrega: User
// diz quem está na página e o plugin decide o que permitir.
type BridgeCall struct {
	Company Company         `json:"company"`
	Page    string          `json:"page"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	User    User            `json:"user"`
}

// SkillFile é uma skill devolvida por uma fonte: o caminho lógico e o SKILL.md completo.
type SkillFile struct {
	Path    string `json:"path"`
	SkillMD string `json:"skill_md"`
}

// SkillSnapshot é o retrato de uma fonte de skills. Revision muda quando o conteúdo muda (qualquer texto estável).
type SkillSnapshot struct {
	Revision string      `json:"revision"`
	Skills   []SkillFile `json:"skills"`
}

// EnvSpec é o pedido de um sandbox em nuvem.
type EnvSpec struct {
	Name         string            `json:"name"`
	Labels       map[string]string `json:"labels"`
	Snapshot     string            `json:"snapshot"`
	CPUs         int               `json:"cpus"`
	MemoryGiB    int               `json:"memory_gib"`
	DiskGiB      int               `json:"disk_gib"`
	BlockNetwork bool              `json:"block_network"`
	IdleStopSec  int               `json:"idle_stop_sec"`
	ReapSec      int               `json:"reap_sec"`
}

// SSHAccess é o acesso SSH temporário a um sandbox. User é o token de acesso (nunca vai para log).
type SSHAccess struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	User string `json:"user"`
}

// EnvProvider implementa um provider de sandbox em nuvem (capacidade environments.provider). apiKey é a chave
// do provider guardada na configuração do ambiente da empresa.
type EnvProvider interface {
	Ping(ctx context.Context, apiKey string) error
	Create(ctx context.Context, apiKey string, spec EnvSpec) (id string, err error)
	SSH(ctx context.Context, apiKey, id string, ttl time.Duration) (SSHAccess, error)
	Delete(ctx context.Context, apiKey, id string) error
}

// Handlers por contribuição.
type (
	// ToolFunc atende uma tool.
	ToolFunc func(ctx context.Context, h *Host, c ToolCall) (ToolResult, error)
	// WidgetFunc atende um widget.
	WidgetFunc func(ctx context.Context, h *Host, r WidgetRequest) (Widget, error)
	// JobFunc atende uma rodada de job.
	JobFunc func(ctx context.Context, h *Host, r JobRequest) error
	// EventFunc atende um evento da atividade.
	EventFunc func(ctx context.Context, h *Host, co Company, e Event) error
	// PageFunc devolve o HTML de uma página. O HTML roda num iframe sandboxed, sem rede: fale com o plugin por
	// window.hanko.call(método, params).
	PageFunc func(ctx context.Context, h *Host, r PageRequest) (html string, err error)
	// BridgeFunc atende window.hanko.call da página.
	BridgeFunc func(ctx context.Context, h *Host, c BridgeCall) (any, error)
	// SkillSourceFunc devolve o retrato de uma fonte de skills.
	SkillSourceFunc func(ctx context.Context, h *Host, co Company) (SkillSnapshot, error)
	// ValidateFunc valida a configuração de uma empresa; devolva campo → motivo (nil = ok).
	ValidateFunc func(ctx context.Context, co Company, cfg map[string]any) map[string]string
)

// Plugin reúne os handlers de um plugin.
type Plugin struct {
	name string

	tools   map[string]ToolFunc
	widgets map[string]WidgetFunc
	jobs    map[string]JobFunc
	pages   map[string]PageFunc
	sources map[string]SkillSourceFunc
	envs    map[string]EnvProvider
	event   EventFunc
	bridge  BridgeFunc
	valid   ValidateFunc

	// Log recebe os avisos do próprio SDK (padrão: stderr, que vira o log do plugin no Hanko).
	Log io.Writer
}

// New cria um plugin; name aparece no initialize.
func New(name string) *Plugin {
	return &Plugin{name: name, tools: map[string]ToolFunc{}, widgets: map[string]WidgetFunc{}, jobs: map[string]JobFunc{},
		pages: map[string]PageFunc{}, sources: map[string]SkillSourceFunc{}, envs: map[string]EnvProvider{}, Log: os.Stderr}
}

// Tool registra uma tool do manifesto (tools[].name).
func (p *Plugin) Tool(name string, f ToolFunc) { p.tools[name] = f }

// Widget registra um widget do manifesto (widgets[].key).
func (p *Plugin) Widget(key string, f WidgetFunc) { p.widgets[key] = f }

// Job registra um job do manifesto (jobs[].key).
func (p *Plugin) Job(key string, f JobFunc) { p.jobs[key] = f }

// Page registra uma página do manifesto (pages[].key).
func (p *Plugin) Page(key string, f PageFunc) { p.pages[key] = f }

// Bridge registra o atendedor das chamadas das páginas (window.hanko.call).
func (p *Plugin) Bridge(f BridgeFunc) { p.bridge = f }

// OnEvent registra o atendedor dos eventos de events[].
func (p *Plugin) OnEvent(f EventFunc) { p.event = f }

// SkillSource registra uma fonte de skills do manifesto (skill_sources[].key).
func (p *Plugin) SkillSource(key string, f SkillSourceFunc) { p.sources[key] = f }

// Environment registra um provider de ambiente do manifesto (environment_providers[].key).
func (p *Plugin) Environment(key string, e EnvProvider) { p.envs[key] = e }

// ValidateConfig registra a validação da configuração por empresa.
func (p *Plugin) ValidateConfig(f ValidateFunc) { p.valid = f }

// Run lê stdin e escreve em stdout até o host mandar shutdown ou o stdin fechar.
func (p *Plugin) Run(ctx context.Context) error { return p.Serve(ctx, os.Stdin, os.Stdout) }

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Host é o cliente das chamadas do plugin ao Hanko.
type Host struct {
	w       io.Writer
	wmu     sync.Mutex
	next    atomic.Int64
	pmu     sync.Mutex
	pending map[string]chan message
	timeout time.Duration
}

func (h *Host) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	h.wmu.Lock()
	defer h.wmu.Unlock()
	_, err = h.w.Write(append(b, '\n'))
	return err
}

// Call chama um método do host (docs/PLUGINS.md, seção 6) e decodifica o resultado em out (pode ser nil).
func (h *Host) Call(ctx context.Context, method string, params, out any) error {
	id := h.next.Add(1)
	key := fmt.Sprint(id)
	ch := make(chan message, 1)
	h.pmu.Lock()
	h.pending[key] = ch
	h.pmu.Unlock()
	drop := func() {
		h.pmu.Lock()
		delete(h.pending, key)
		h.pmu.Unlock()
	}
	if err := h.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		drop()
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()
	select {
	case m := <-ch:
		if m.Error != nil {
			return m.Error
		}
		if out != nil && len(m.Result) > 0 {
			return json.Unmarshal(m.Result, out)
		}
		return nil
	case <-ctx.Done():
		drop()
		return fmt.Errorf("%s: sem resposta do host: %w", method, ctx.Err())
	}
}

func (h *Host) answer(m message) {
	key := strings.Trim(string(m.ID), `"`)
	h.pmu.Lock()
	ch := h.pending[key]
	delete(h.pending, key)
	h.pmu.Unlock()
	if ch != nil {
		ch <- m
	}
}

// Log escreve uma linha no log do plugin (level: debug, info, warn, error).
func (h *Host) Log(ctx context.Context, level, msg string) {
	_ = h.Call(ctx, "log", map[string]any{"level": level, "message": msg}, nil)
}

// Config devolve a configuração completa da empresa (padrões, valores e segredos).
func (h *Host) Config(ctx context.Context, co Company) (map[string]any, error) {
	var out struct {
		Config map[string]any `json:"config"`
	}
	if err := h.Call(ctx, "config.get", map[string]any{"company": co.ID}, &out); err != nil {
		return nil, err
	}
	return out.Config, nil
}

// StateGet lê uma chave do estado do plugin na empresa (capacidade state).
func (h *Host) StateGet(ctx context.Context, co Company, key string, out any) (found bool, err error) {
	var res struct {
		Found bool            `json:"found"`
		Value json.RawMessage `json:"value"`
	}
	if err := h.Call(ctx, "state.get", map[string]any{"company": co.ID, "key": key}, &res); err != nil {
		return false, err
	}
	if res.Found && out != nil {
		return true, json.Unmarshal(res.Value, out)
	}
	return res.Found, nil
}

// StateSet grava uma chave do estado.
func (h *Host) StateSet(ctx context.Context, co Company, key string, value any) error {
	return h.Call(ctx, "state.set", map[string]any{"company": co.ID, "key": key, "value": value}, nil)
}

// CommentTask comenta numa task (capacidade tasks.write). eventID torna a escrita idempotente.
func (h *Host) CommentTask(ctx context.Context, co Company, taskKey, body, eventID string) error {
	return h.Call(ctx, "tasks.comment", map[string]any{"company": co.ID, "key": taskKey, "body": body, "event_id": eventID}, nil)
}

// FireTrigger dispara um gatilho de fluxo do manifesto (capacidade flows.trigger): o Hanko dispara os fluxos que o
// admin da empresa ligou a ele. Devolve quantos fluxos dispararam.
func (h *Host) FireTrigger(ctx context.Context, co Company, trigger string) (int, error) {
	var out struct {
		Fired int `json:"fired"`
	}
	err := h.Call(ctx, "flows.fire", map[string]any{"company": co.ID, "trigger": trigger}, &out)
	return out.Fired, err
}

// Serve atende o protocolo em r/w. Termina no shutdown, no fim de r ou quando ctx é cancelado.
func (p *Plugin) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	h := &Host{w: w, pending: map[string]chan message{}, timeout: 30 * time.Second}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(done) }) }
	var wg sync.WaitGroup
	lines := make(chan []byte)
	rerr := make(chan error, 1)
	go func() {
		br := bufio.NewReaderSize(r, 64<<10)
		for {
			line, err := br.ReadBytes('\n')
			if len(strings.TrimSpace(string(line))) > 0 {
				select {
				case lines <- line:
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				rerr <- err
				return
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return ctx.Err()
		case <-done:
			wg.Wait()
			return nil
		case err := <-rerr:
			cancel()
			wg.Wait()
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		case line := <-lines:
			var m message
			if json.Unmarshal(line, &m) != nil {
				fmt.Fprintln(p.Log, "plugin: linha inválida ignorada")
				continue
			}
			if m.Method == "" {
				h.answer(m)
				continue
			}
			wg.Add(1)
			// cada pedido numa goroutine: o handler pode chamar o host e esperar a resposta
			go func() {
				defer wg.Done()
				p.handle(ctx, h, m, stop)
			}()
		}
	}
}

func (p *Plugin) handle(ctx context.Context, h *Host, m message, stop func()) {
	res, err := p.dispatch(ctx, h, m)
	if len(m.ID) == 0 {
		return // notificação
	}
	if err != nil {
		var pe *Error
		if !errors.As(err, &pe) {
			pe = &Error{Code: -32000, Message: err.Error()}
		}
		_ = h.write(map[string]any{"jsonrpc": "2.0", "id": m.ID, "error": pe})
	} else {
		_ = h.write(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": res})
	}
	if m.Method == "shutdown" {
		stop()
	}
}

func decodeParams[T any](m message) (T, error) {
	var v T
	if len(m.Params) == 0 {
		return v, nil
	}
	if err := json.Unmarshal(m.Params, &v); err != nil {
		return v, Errorf(CodeInvalidParams, "params inválidos: %v", err)
	}
	return v, nil
}

func (p *Plugin) dispatch(ctx context.Context, h *Host, m message) (any, error) {
	switch m.Method {
	case "initialize":
		return map[string]any{"api_version": APIVersion, "name": p.name}, nil
	case "health":
		return map[string]any{"ok": true}, nil
	case "shutdown":
		return map[string]any{}, nil
	case "validate_config":
		in, err := decodeParams[struct {
			Company Company        `json:"company"`
			Config  map[string]any `json:"config"`
		}](m)
		if err != nil {
			return nil, err
		}
		if p.valid != nil {
			if errs := p.valid(ctx, in.Company, in.Config); len(errs) > 0 {
				return map[string]any{"ok": false, "errors": errs}, nil
			}
		}
		return map[string]any{"ok": true}, nil
	case "on_event":
		in, err := decodeParams[struct {
			Company Company `json:"company"`
			Event   Event   `json:"event"`
		}](m)
		if err != nil {
			return nil, err
		}
		if p.event != nil {
			if err := p.event(ctx, h, in.Company, in.Event); err != nil {
				return nil, err
			}
		}
		return map[string]any{}, nil
	case "execute_tool":
		in, err := decodeParams[ToolCall](m)
		if err != nil {
			return nil, err
		}
		f, ok := p.tools[in.Tool]
		if !ok {
			return nil, Errorf(CodeInvalidParams, "tool desconhecida: %s", in.Tool)
		}
		return f(ctx, h, in)
	case "get_widget":
		in, err := decodeParams[WidgetRequest](m)
		if err != nil {
			return nil, err
		}
		f, ok := p.widgets[in.Key]
		if !ok {
			return nil, Errorf(CodeInvalidParams, "widget desconhecido: %s", in.Key)
		}
		return f(ctx, h, in)
	case "run_job":
		in, err := decodeParams[JobRequest](m)
		if err != nil {
			return nil, err
		}
		f, ok := p.jobs[in.Key]
		if !ok {
			return nil, Errorf(CodeInvalidParams, "job desconhecido: %s", in.Key)
		}
		if err := f(ctx, h, in); err != nil {
			return nil, err
		}
		return map[string]any{}, nil
	case "get_page":
		in, err := decodeParams[PageRequest](m)
		if err != nil {
			return nil, err
		}
		f, ok := p.pages[in.Page]
		if !ok {
			return nil, Errorf(CodeInvalidParams, "página desconhecida: %s", in.Page)
		}
		html, err := f(ctx, h, in)
		if err != nil {
			return nil, err
		}
		return map[string]any{"html": html}, nil
	case "page_call":
		in, err := decodeParams[BridgeCall](m)
		if err != nil {
			return nil, err
		}
		if p.bridge == nil {
			return nil, Errorf(CodeMethodNotFound, "o plugin não atende chamadas de página")
		}
		return p.bridge(ctx, h, in)
	case "skills.snapshot":
		in, err := decodeParams[struct {
			Company Company `json:"company"`
			Source  string  `json:"source"`
		}](m)
		if err != nil {
			return nil, err
		}
		f, ok := p.sources[in.Source]
		if !ok {
			return nil, Errorf(CodeInvalidParams, "fonte de skills desconhecida: %s", in.Source)
		}
		return f(ctx, h, in.Company)
	case "env.ping", "env.create", "env.ssh", "env.delete":
		return p.dispatchEnv(ctx, m)
	}
	return nil, Errorf(CodeMethodNotFound, "método desconhecido: %s", m.Method)
}

func (p *Plugin) dispatchEnv(ctx context.Context, m message) (any, error) {
	in, err := decodeParams[struct {
		Provider string  `json:"provider"`
		APIKey   string  `json:"api_key"`
		ID       string  `json:"id"`
		TTLSec   int     `json:"ttl_sec"`
		Spec     EnvSpec `json:"spec"`
	}](m)
	if err != nil {
		return nil, err
	}
	e, ok := p.envs[in.Provider]
	if !ok {
		return nil, Errorf(CodeInvalidParams, "provider de ambiente desconhecido: %s", in.Provider)
	}
	switch m.Method {
	case "env.ping":
		return map[string]any{"ok": true}, e.Ping(ctx, in.APIKey)
	case "env.create":
		id, err := e.Create(ctx, in.APIKey, in.Spec)
		return map[string]any{"id": id}, err
	case "env.ssh":
		return e.SSH(ctx, in.APIKey, in.ID, time.Duration(in.TTLSec)*time.Second)
	default:
		return map[string]any{"ok": true}, e.Delete(ctx, in.APIKey, in.ID)
	}
}

// Names devolve, em ordem, o que o plugin registrou por tipo (útil para conferir com o manifesto em testes).
func (p *Plugin) Names() map[string][]string {
	keys := func(n int, each func(add func(string))) []string {
		out := make([]string, 0, n)
		each(func(s string) { out = append(out, s) })
		sort.Strings(out)
		return out
	}
	return map[string][]string{
		"tools":   keys(len(p.tools), func(add func(string)) { forEach(p.tools, add) }),
		"widgets": keys(len(p.widgets), func(add func(string)) { forEach(p.widgets, add) }),
		"jobs":    keys(len(p.jobs), func(add func(string)) { forEach(p.jobs, add) }),
		"pages":   keys(len(p.pages), func(add func(string)) { forEach(p.pages, add) }),
		"sources": keys(len(p.sources), func(add func(string)) { forEach(p.sources, add) }),
		"envs":    keys(len(p.envs), func(add func(string)) { forEach(p.envs, add) }),
	}
}

func forEach[V any](m map[string]V, add func(string)) {
	for k := range m {
		add(k)
	}
}
