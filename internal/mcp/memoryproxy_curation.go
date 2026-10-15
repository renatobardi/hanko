package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/service"
)

// Governança da memória no proxy (ADR-0030):
//
//   - run de curadoria (trigger memory): leitura e escrita aditiva seguem; escrita que remove, funde ou
//     reescreve vira proposta com gate humano e a run recebe uma resposta sintética; o que não cabe numa
//     curadoria (handoff, sessão) é recusado;
//   - demais runs: o escopo é o do token; só uma concessão (memory_grants) alcança outro workspace da
//     empresa, e escrever exige concessão de escrita. Outra empresa nunca é alcançável.

const (
	rpcCodeForbidden = -32001
	rpcCodeInternal  = -32603
)

type toolCall struct {
	id   json.RawMessage
	name string
	args map[string]any
}

// parseToolCalls extrai as chamadas tools/call do corpo (objeto ou lote). batch diz se veio em lote.
func parseToolCalls(body []byte) (calls []toolCall, batch bool) {
	trim := bytes.TrimSpace(body)
	if len(trim) == 0 {
		return nil, false
	}
	one := func(raw json.RawMessage) {
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		if json.Unmarshal(raw, &m) != nil || m.Method != "tools/call" {
			return
		}
		calls = append(calls, toolCall{id: m.ID, name: m.Params.Name, args: m.Params.Arguments})
	}
	switch trim[0] {
	case '{':
		one(trim)
	case '[':
		batch = true
		var arr []json.RawMessage
		if json.Unmarshal(trim, &arr) != nil {
			return nil, false
		}
		for _, raw := range arr {
			one(raw)
		}
	}
	return calls, batch
}

// screen aplica a política sobre as chamadas de tool do corpo. Devolve o corpo a encaminhar, o escopo efetivo
// e done=true quando já respondeu ao cliente.
func (p *memoryProxy) screen(ctx context.Context, w http.ResponseWriter, claims auth.RunClaims, run service.RunView,
	sc memScope, body []byte) ([]byte, memScope, bool) {
	calls, batch := parseToolCalls(body)
	if len(calls) == 0 {
		return body, sc, false
	}
	cid, err1 := domain.ParseID(claims.CompanyID)
	pid, err2 := domain.ParseID(claims.PersonaID)
	rid, err3 := domain.ParseID(claims.RunID)
	if err1 != nil || err2 != nil || err3 != nil {
		p.log.Error("memory-mcp: claims inválidas", "run_id", claims.RunID)
		http.Error(w, "escopo de memória indisponível", http.StatusServiceUnavailable)
		return nil, sc, true
	}

	if run.Trigger == domain.TriggerMemory {
		return p.screenCuration(ctx, w, cid, rid, sc, body, calls, batch)
	}

	// Run comum: só uma chamada única pode mirar outro workspace, e só com concessão.
	if batch || len(calls) != 1 {
		return body, sc, false
	}
	c := calls[0]
	proj, _ := c.args["project"].(string)
	proj = strings.TrimSpace(proj)
	if proj == "" || proj == sc.project {
		return body, sc, false
	}
	access, err := p.lookupAccess(ctx, cid, pid, proj)
	if err != nil {
		p.log.Error("memory-mcp: consultar concessão", "err", err, "run_id", claims.RunID)
		http.Error(w, "escopo de memória indisponível", http.StatusServiceUnavailable)
		return nil, sc, true
	}
	if access == "" {
		return body, sc, false // sem concessão: continua fixado ao próprio escopo (pinScopeArgs)
	}
	want := domain.MemoryWrite
	if memory.Classify(c.name, c.args) == memory.ClassRead {
		want = domain.MemoryRead
	}
	if !access.Allows(want) {
		writeRPCError(w, c.id, rpcCodeForbidden,
			fmt.Sprintf("sem permissão de escrita na memória do workspace %q: a persona só pode ler", proj))
		return nil, sc, true
	}
	return body, memScope{workspace: sc.workspace, project: proj}, false
}

func (p *memoryProxy) lookupAccess(ctx context.Context, cid, pid domain.ID, slug string) (domain.MemoryAccess, error) {
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	return p.d.Backend.MemoryAccessTo(ctx, cid, pid, slug)
}

func (p *memoryProxy) screenCuration(ctx context.Context, w http.ResponseWriter, cid, rid domain.ID, sc memScope,
	body []byte, calls []toolCall, batch bool) ([]byte, memScope, bool) {
	classes := make([]memory.Class, len(calls))
	intercepted := false
	for i, c := range calls {
		classes[i] = memory.Classify(c.name, c.args)
		if classes[i] == memory.ClassHeld || classes[i] == memory.ClassDeny {
			intercepted = true
		}
	}
	if !intercepted {
		return body, sc, false
	}
	if batch || len(calls) != 1 {
		writeRPCErrors(w, calls, rpcCodeForbidden,
			"numa curadoria, envie cada escrita que remove, funde ou reescreve como uma chamada separada (sem lote)")
		return nil, sc, true
	}
	c := calls[0]
	if classes[0] == memory.ClassDeny {
		writeRPCError(w, c.id, rpcCodeForbidden,
			fmt.Sprintf("a tool %q não cabe numa curadoria de memória (handoff e sessão não são usados aqui)", c.name))
		return nil, sc, true
	}
	// Só sobrepõe workspace/project quando a chamada original já os traz (como pinScopeArgs): o hash da proposta
	// cobre a chamada que o agente fez, não argumentos que a tool não declara. O escopo da aplicação vem do
	// workspace da proposta.
	args := make(map[string]any, len(c.args))
	for k, v := range c.args {
		args[k] = v
	}
	if _, has := args["workspace"]; has {
		args["workspace"] = sc.workspace
	}
	if _, has := args["project"]; has {
		args["project"] = sc.project
	}
	current := p.readCurrent(ctx, sc, memory.TargetsOf(args))
	rctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	view, created, err := p.d.Backend.RecordMemoryProposal(rctx, cid, service.RecordProposalInput{
		RunID: rid, Tool: c.name, Arguments: args, Current: current})
	if err != nil {
		p.log.Error("memory-mcp: registrar proposta", "err", err, "tool", c.name, "run_id", rid)
		writeRPCError(w, c.id, rpcCodeInternal, "não foi possível registrar a proposta de curadoria; nada foi alterado")
		return nil, sc, true
	}
	p.log.Info("memory-mcp: escrita retida como proposta", "proposal_id", view.ID, "tool", c.name, "created", created)
	text := fmt.Sprintf("Escrita retida: a chamada %s (%s) virou a proposta %s e aguarda a decisão de um humano no Inbox. "+
		"Nada foi alterado na memória. Siga com as próximas consolidações e não repita esta chamada.",
		c.name, view.Kind, view.ID)
	writeRPCResult(w, c.id, map[string]any{"content": []map[string]any{{"type": "text", "text": text}}})
	return nil, sc, true
}

const maxCurrentTargets = 5

// readCurrent lê (melhor esforço) o conteúdo atual dos alvos, para o diff da proposta.
func (p *memoryProxy) readCurrent(ctx context.Context, sc memScope, targets []string) string {
	if p.d.Pages == nil || len(targets) == 0 {
		return ""
	}
	if len(targets) > maxCurrentTargets {
		targets = targets[:maxCurrentTargets]
	}
	var parts []string
	for _, t := range targets {
		rctx, cancel := context.WithTimeout(ctx, lookupTimeout)
		txt, err := p.d.Pages.ReadPage(rctx, memory.Scope{Workspace: sc.workspace, Project: sc.project}, t)
		cancel()
		if err != nil || strings.TrimSpace(txt) == "" {
			continue
		}
		if len(targets) == 1 {
			return txt
		}
		parts = append(parts, "=== "+t+" ===\n"+txt)
	}
	return strings.Join(parts, "\n\n")
}

// ---- respostas JSON-RPC locais ----

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func rawID(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage("null")
	}
	return id
}

func writeRPCResult(w http.ResponseWriter, id json.RawMessage, result any) {
	writeJSON(w, map[string]any{"jsonrpc": "2.0", "id": rawID(id), "result": result})
}

func writeRPCError(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	writeJSON(w, map[string]any{"jsonrpc": "2.0", "id": rawID(id), "error": map[string]any{"code": code, "message": msg}})
}

func writeRPCErrors(w http.ResponseWriter, calls []toolCall, code int, msg string) {
	out := make([]map[string]any, 0, len(calls))
	for _, c := range calls {
		out = append(out, map[string]any{"jsonrpc": "2.0", "id": rawID(c.id), "error": map[string]any{"code": code, "message": msg}})
	}
	writeJSON(w, out)
}
