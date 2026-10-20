// Package httpagent é o harness `http` (ADR-0056): o próprio Hanko roda o loop de tool-use sobre um endpoint
// OpenAI-compatível (chat completions com tool_calls), sem CLI nenhuma. As tools são as do servidor MCP do Hanko da
// run (`hanko_*`, mesma URL e run token das outras personas); o modelo escolhe, o Hanko executa e devolve o
// resultado, até a resposta final ou o teto de passos. Reusa o cliente `internal/provider` do decision model.
//
// v1 deliberadamente sem execução de comandos nem escrita em disco: é o harness da persona operacional (ler a task,
// comentar, delegar, pedir decisão) e o caminho para modelos locais com tool calling. Sessão não é retomada.
package httpagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/provider"
)

const (
	maxToolResultBytes = 8 << 10 // o que volta ao modelo
	maxEventBytes      = 4 << 10 // o que vai ao log da run
)

// Options configura o adapter.
type Options struct {
	Logger *slog.Logger
	// Client devolve o cliente HTTP de saída com o prazo pedido; o app injeta o do netguard (SSRF). Nil usa um
	// cliente comum (testes com httptest).
	Client func(timeout time.Duration) *http.Client
	// Connect substitui a conexão ao MCP do Hanko (testes). Nil usa o transporte HTTP do SDK.
	Connect func(ctx context.Context, ep mcpEndpoint) (toolHost, error)
}

// Adapter implementa adapter.Adapter para o harness http.
type Adapter struct {
	log     *slog.Logger
	client  func(time.Duration) *http.Client
	connect func(context.Context, mcpEndpoint) (toolHost, error)
}

// New devolve o adapter http.
func New(o Options) *Adapter {
	log := o.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	cl := o.Client
	if cl == nil {
		cl = func(t time.Duration) *http.Client { return &http.Client{Timeout: t} }
	}
	cn := o.Connect
	if cn == nil {
		cn = connectMCP
	}
	return &Adapter{log: log, client: cl, connect: cn}
}

// Name implementa adapter.Adapter.
func (a *Adapter) Name() string { return "http" }

func (a *Adapter) provider(cfg Config, timeout time.Duration) *provider.Client {
	return &provider.Client{BaseURL: cfg.BaseURL, APIKey: cfg.APIKeyRef, HTTP: a.client(timeout), Headers: map[string]string{"X-Title": "Hanko"}}
}

func modelOf(spec adapter.Spec, cfg Config) string {
	if m := strings.TrimSpace(spec.Model); m != "" {
		return m
	}
	return cfg.Model
}

// Run executa o loop. Cancelar ctx interrompe a chamada em andamento; o Result parcial volta com erro nil.
func (a *Adapter) Run(ctx context.Context, spec adapter.Spec, sink adapter.Sink) (adapter.Result, error) {
	cfg, err := ParseConfig(spec.Config)
	if err != nil {
		return adapter.Result{ExitCode: -1, IsError: true, Error: err.Error()}, fmt.Errorf("http: adapter_config: %w", err)
	}
	model := modelOf(spec, cfg)
	if model == "" {
		err := errors.New("nenhum modelo: defina o modelo da persona ou \"model\" no adapter_config")
		return adapter.Result{ExitCode: -1, IsError: true, Error: err.Error()}, fmt.Errorf("http: %w", err)
	}
	secrets := append(adapter.SpecSecrets(spec), cfg.APIKeyRef)
	sink = adapter.RedactingSink(sink, secrets...)
	res, rerr := a.loop(ctx, spec, cfg, model, sink)
	return adapter.RedactResult(res, secrets...), rerr
}

func (a *Adapter) loop(ctx context.Context, spec adapter.Spec, cfg Config, model string, sink adapter.Sink) (adapter.Result, error) {
	var sinkErr error
	emit := func(kind domain.RunEventKind, text string, payload any) {
		if sinkErr != nil {
			return
		}
		ectx := ctx
		if ctx.Err() != nil {
			var c context.CancelFunc
			ectx, c = context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer c()
		}
		e := adapter.Event{Kind: kind, Text: text, OccurredAt: time.Now().UTC()}
		if payload != nil {
			if b, err := json.Marshal(payload); err == nil {
				e.Payload = b
			}
		}
		if err := sink.Emit(ectx, e); err != nil {
			sinkErr = err
			a.log.Warn("http: sink falhou", "run_id", spec.RunID, "err", err)
		}
	}

	res := adapter.Result{Model: model}
	fail := func(msg string) (adapter.Result, error) {
		res.IsError, res.Error = true, msg
		text := res.Summary
		if text == "" {
			text = msg
		}
		emit(domain.EventResult, text, nil)
		if sinkErr != nil && ctx.Err() == nil {
			return res, fmt.Errorf("http: sink: %w", sinkErr)
		}
		return res, nil
	}

	timeout := cfg.timeout()
	pc := a.provider(cfg, timeout)

	// tools: as do servidor MCP do Hanko da run; revisão (ReadOnly) e run sem credencial não têm nenhuma
	var host toolHost
	var tools []provider.Tool
	if ep, ok := hankoEndpoint(spec.MCPConfig); ok && !spec.ReadOnly {
		h, err := a.connect(ctx, ep)
		if err != nil {
			emit(domain.EventSystem, "aviso: tools do Hanko indisponíveis nesta execução: "+err.Error(), nil)
		} else {
			defer h.Close()
			host = h
			if tools, err = h.List(ctx, cfg.Tools); err != nil {
				emit(domain.EventSystem, "aviso: não foi possível listar as tools do Hanko: "+err.Error(), nil)
				host, tools = nil, nil
			}
		}
	}
	emit(domain.EventSystem, fmt.Sprintf("harness http: modelo %s em %s com %d tools", model, hostOf(cfg.BaseURL), len(tools)),
		map[string]any{"model": model, "tools": len(tools), "max_steps": cfg.maxSteps()})

	msgs := []provider.Message{}
	if s := strings.TrimSpace(spec.System); s != "" {
		msgs = append(msgs, provider.Message{Role: "system", Content: s})
	}
	msgs = append(msgs, provider.Message{Role: "user", Content: spec.Prompt})

	var cost float64
	sawCost := false
	for step := 1; step <= cfg.maxSteps(); step++ {
		resp, err := pc.Chat(ctx, provider.ChatRequest{Model: model, Messages: msgs, Tools: tools, Temperature: cfg.Temperature})
		if err != nil {
			finish(&res, cost, sawCost)
			if ctx.Err() != nil {
				return fail("execução interrompida: " + ctx.Err().Error())
			}
			return fail("modelo: " + err.Error())
		}
		res.Usage.InputTokens += resp.Usage.PromptTokens
		res.Usage.OutputTokens += resp.Usage.CompletionTokens
		if resp.Usage.CostUSD != nil {
			cost += *resp.Usage.CostUSD
			sawCost = true
		}
		if resp.Model != "" {
			res.Model = resp.Model
		}
		m := resp.Message
		if text := strings.TrimSpace(m.Content); text != "" {
			res.Summary = text
			emit(domain.EventAssistant, text, nil)
		}
		if len(m.ToolCalls) == 0 {
			finish(&res, cost, sawCost)
			if res.Summary == "" {
				return fail("o modelo terminou sem resposta")
			}
			emit(domain.EventResult, res.Summary, map[string]any{"steps": step, "finish_reason": resp.FinishReason})
			if sinkErr != nil && ctx.Err() == nil {
				return res, fmt.Errorf("http: sink: %w", sinkErr)
			}
			return res, nil
		}
		msgs = append(msgs, m)
		for _, tc := range m.ToolCalls {
			emit(domain.EventToolUse, tc.Function.Name+" "+clip(tc.Function.Arguments, 300), map[string]any{"id": tc.ID, "name": tc.Function.Name})
			out, isErr := a.execTool(ctx, host, tools, tc)
			if isErr {
				emit(domain.EventToolResult, tc.Function.Name+": erro: "+clip(out, maxEventBytes), map[string]any{"id": tc.ID, "is_error": true})
			} else {
				emit(domain.EventToolResult, tc.Function.Name+": "+clip(out, maxEventBytes), map[string]any{"id": tc.ID})
			}
			msgs = append(msgs, provider.Message{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: clip(out, maxToolResultBytes)})
		}
		if ctx.Err() != nil {
			finish(&res, cost, sawCost)
			return fail("execução interrompida: " + ctx.Err().Error())
		}
	}
	finish(&res, cost, sawCost)
	return fail(fmt.Sprintf("limite de %d passos de tool-use atingido sem resposta final (max_steps)", cfg.maxSteps()))
}

func finish(res *adapter.Result, cost float64, sawCost bool) {
	if sawCost {
		c := cost
		res.CostUSD = &c
	}
}

// execTool executa uma chamada pedida pelo modelo. Tool desconhecida ou fora da lista, argumentos inválidos e
// falha da tool viram texto de erro devolvido ao modelo (ele pode corrigir e tentar de novo).
func (a *Adapter) execTool(ctx context.Context, host toolHost, offered []provider.Tool, tc provider.ToolCall) (string, bool) {
	name := tc.Function.Name
	if host == nil {
		return "nenhuma tool disponível nesta execução", true
	}
	known := false
	for _, t := range offered {
		if t.Function.Name == name {
			known = true
			break
		}
	}
	if !known {
		return fmt.Sprintf("tool %q não existe ou não está liberada para esta persona", name), true
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	text, isErr, err := host.Call(cctx, name, json.RawMessage(tc.Function.Arguments))
	if err != nil {
		return err.Error(), true
	}
	if text == "" {
		text = "(sem saída)"
	}
	return text, isErr
}

func hostOf(base string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://")
	if i := strings.IndexAny(s, "/?"); i >= 0 {
		s = s[:i]
	}
	return s
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + fmt.Sprintf("…[truncado, %d bytes]", len(s))
}

// Probe confere a configuração e o endpoint sem enviar prompt: GET {base}/models (autenticação) e presença do modelo.
// Um servidor local sem /models fica "indisponível" no catálogo, não reprovado.
func (a *Adapter) Probe(ctx context.Context, spec adapter.Spec) adapter.ProbeResult {
	var out adapter.ProbeResult
	cfg, err := ParseConfig(spec.Config)
	if err != nil {
		out.Add("configuração", false, err.Error())
		return out
	}
	model := modelOf(spec, cfg)
	if model == "" {
		out.Add("configuração", false, "nenhum modelo: defina o modelo da persona ou \"model\" no adapter_config")
		return out
	}
	out.Add("configuração", true, hostOf(cfg.BaseURL)+" · "+model)
	pctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ms, err := a.provider(cfg, 20*time.Second).Catalog(pctx)
	var he *provider.HTTPError
	switch {
	case errors.As(err, &he) && (he.Status == http.StatusUnauthorized || he.Status == http.StatusForbidden):
		out.Add("endpoint", true, "responde")
		out.Add("autenticação", false, he.Error())
		return out
	case errors.As(err, &he) && he.Status == http.StatusNotFound:
		out.Add("endpoint", true, "responde, sem catálogo de modelos (/models)")
		return out
	case err != nil:
		out.Add("endpoint", false, adapter.Redact(err.Error(), cfg.APIKeyRef))
		return out
	}
	out.Add("endpoint", true, fmt.Sprintf("%d modelos no catálogo", len(ms)))
	out.Add("autenticação", true, "")
	found := false
	for _, m := range ms {
		if m.ID == model {
			found = true
			break
		}
	}
	if found {
		out.Add("modelo", true, model)
	} else {
		out.Add("modelo", false, fmt.Sprintf("o modelo %q não está no catálogo do endpoint", model))
	}
	return out
}
