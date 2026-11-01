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
//   - toda run: tool ou argumento fora do contrato (memory/args.go) é recusado;
//   - demais runs: o escopo é o do token; só uma concessão (memory_grants) alcança outro workspace da
//     empresa, e escrever exige concessão de escrita. Outra empresa nunca é alcançável.

const (
	rpcCodeForbidden = -32001
	rpcCodeInvalid   = -32600
	rpcCodeInternal  = -32603
)

type toolCall struct {
	id   json.RawMessage
	name string
	args map[string]any
}

// parseToolCalls extrai as chamadas tools/call do corpo (objeto ou lote). batch diz se veio em lote; ok=false diz que
// o corpo não pôde ser lido e não deve seguir. As chaves são exatas, de propósito: encoding/json em struct casaria
// "METHOD" com "method", e o proxy leria outra chamada que não a que o servidor recebe.
func parseToolCalls(body []byte) (calls []toolCall, batch, ok bool) {
	trim := bytes.TrimSpace(body)
	if len(trim) == 0 {
		return nil, false, true
	}
	var msgs []json.RawMessage
	switch trim[0] {
	case '{':
		msgs = []json.RawMessage{trim}
	case '[':
		batch = true
		if json.Unmarshal(trim, &msgs) != nil {
			return nil, batch, false
		}
	default:
		return nil, false, false
	}
	for _, raw := range msgs {
		c, isCall, valid := parseToolCall(raw)
		if !valid {
			return nil, batch, false
		}
		if isCall {
			calls = append(calls, c)
		}
	}
	return calls, batch, true
}

func parseToolCall(raw json.RawMessage) (c toolCall, isCall, valid bool) {
	var msg map[string]json.RawMessage
	if json.Unmarshal(raw, &msg) != nil || msg == nil {
		return c, false, false
	}
	methodRaw, has := msg["method"]
	if !has {
		return c, false, true // resposta do cliente: não é chamada
	}
	var method string
	if json.Unmarshal(methodRaw, &method) != nil {
		return c, false, false
	}
	if method != "tools/call" {
		return c, false, true
	}
	var params map[string]json.RawMessage
	if json.Unmarshal(msg["params"], &params) != nil || params == nil || json.Unmarshal(params["name"], &c.name) != nil {
		return c, true, false
	}
	if a, has := params["arguments"]; has && json.Unmarshal(a, &c.args) != nil {
		return c, true, false
	}
	c.id = msg["id"]
	return c, true, true
}

// screen aplica a política sobre as chamadas de tool do corpo. Devolve o corpo a encaminhar, o escopo efetivo
// e done=true quando já respondeu ao cliente.
func (p *memoryProxy) screen(ctx context.Context, w http.ResponseWriter, claims auth.RunClaims, run service.RunView,
	sc memScope, body []byte) ([]byte, memScope, bool) {
	calls, batch, ok := parseToolCalls(body)
	if !ok {
		p.log.Warn("memory-mcp: corpo recusado", "run_id", claims.RunID)
		writeRPCError(w, nil, rpcCodeInvalid, "chamada inválida para a memória")
		return nil, sc, true
	}
	if len(calls) == 0 {
		return body, sc, false
	}
	if p.refuseOutOfContract(w, claims, sc, calls, batch) {
		return nil, sc, true
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
	if batch {
		return body, sc, p.refuseBatchTarget(w, claims, sc, calls)
	}
	c := calls[0]
	proj := targetProject(c)
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

// refuseBatchTarget recusa o lote inteiro quando uma chamada mira outro workspace: a concessão vale por chamada.
func (p *memoryProxy) refuseBatchTarget(w http.ResponseWriter, claims auth.RunClaims, sc memScope, calls []toolCall) bool {
	for _, c := range calls {
		if proj := targetProject(c); proj != "" && proj != sc.project {
			p.log.Warn("memory-mcp: lote fora do escopo recusado", "tool", c.name, "run_id", claims.RunID)
			writeRPCErrors(w, calls, rpcCodeForbidden, "um lote não pode mirar outro workspace; envie uma chamada por vez")
			return true
		}
	}
	return false
}

// targetProject é o workspace do Hanko que a chamada pede (o project do ai-memory).
func targetProject(c toolCall) string {
	key := "project"
	if c.name == memory.MessageSendTool {
		key = "to_project"
	}
	proj, _ := c.args[key].(string)
	return strings.TrimSpace(proj)
}

// refuseOutOfContract recusa tool ou argumento fora do contrato (memory/args.go) e recado para outra empresa.
// O log leva só os nomes, para mostrar o que as personas tentam sem guardar o conteúdo.
func (p *memoryProxy) refuseOutOfContract(w http.ResponseWriter, claims auth.RunClaims, sc memScope, calls []toolCall, batch bool) bool {
	for _, c := range calls {
		msg := ""
		refused := memory.RefusedArgs(c.name, c.args)
		dest, _ := c.args["to_workspace"].(string)
		switch {
		case !memory.KnownTool(c.name):
			msg = fmt.Sprintf("a tool %q não está liberada para as personas", c.name)
		case len(refused) > 0:
			msg = fmt.Sprintf("a tool %q não aceita %s aqui: a memória da run fica no workspace dela", c.name, strings.Join(refused, ", "))
		case c.name == memory.MessageSendTool && dest != "" && dest != sc.workspace:
			msg = "recado só vale dentro da própria empresa"
		default:
			continue
		}
		p.log.Warn("memory-mcp: chamada fora do contrato", "tool", c.name, "args", refused, "run_id", claims.RunID)
		if batch {
			writeRPCErrors(w, calls, rpcCodeForbidden, msg)
		} else {
			writeRPCError(w, c.id, rpcCodeForbidden, msg)
		}
		return true
	}
	return false
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
