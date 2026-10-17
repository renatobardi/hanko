package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/mcpupstream"
	"github.com/renatobardi/hanko/internal/netguard"
	"github.com/renatobardi/hanko/internal/store"
)

// Gateway de tools (ADR-0036). As integrações MCP http de uma run deixam de ir direto para o agente: o mcpServers
// aponta para /mcp/run/{integração} no Hanko, autenticado pelo run token, e o gateway filtra o tools/list pelo perfil
// efetivo e decide cada tools/call (excluída ou em quarentena → erro; ask → gate tool; allow → repasse com os
// segredos guardados e a proteção SSRF). O gateway nomeado (/mcp/gw/{empresa}/{gateway}) faz o mesmo para clientes
// de fora, com token hkgw_ e o perfil do gateway, agregando as integrações com o prefixo "<integração>__".
//
// Toda chamada fica na auditoria (tool_invocations) sem os argumentos: só o sha256 e o tamanho.

const (
	// ToolNameSeparator separa a integração da tool no gateway nomeado (agregado): "github__create_issue".
	ToolNameSeparator = "__"

	maxToolArgsBytes     = 1 << 20
	maxToolResultBytes   = 8 << 20
	toolApprovalWindow   = 7 * 24 * time.Hour
	toolGateArgsPreview  = 8 << 10
	toolRateWindow       = time.Minute
	toolInvocationMaxAge = 30 * 24 * time.Hour
	toolInvocationCap    = 10000
)

// ToolCaller é quem chama pelo gateway: uma run (persona) ou um gateway nomeado (token hkgw_).
type ToolCaller struct {
	CompanyID domain.ID
	// Run: a persona da run, a task e o projeto (para resolver o perfil).
	RunID     *domain.ID
	RunName   string
	PersonaID *domain.ID
	Persona   string
	TaskID    *domain.ID
	TaskKey   string
	ProjectID *domain.ID
	// Gateway nomeado: o gateway, o perfil dele, o token e as ações que o token permite.
	GatewayID   *domain.ID
	GatewaySlug string
	GatewayName string
	ProfileID   *domain.ID
	TokenID     *domain.ID
	Actions     []string
}

// Named informa se o chamador é um gateway nomeado.
func (c ToolCaller) Named() bool { return c.GatewayID != nil }

// Can informa se o chamador pode fazer a ação (tools/list, tools/call). Run pode tudo; token, o que ele lista.
func (c ToolCaller) Can(action string) bool { return !c.Named() || slices.Contains(c.Actions, action) }

// systemRunTriggers são as runs de sistema: não recebem MCP da empresa e o gateway as recusa (ADR-0029/0036).
var systemRunTriggers = []domain.RunTrigger{domain.TriggerReview, domain.TriggerMemory, domain.TriggerStatusCard, domain.TriggerSkillTest}

// RunToolCaller monta o chamador de uma run ativa (o token já foi validado por quem chama). Uma run de sistema é
// recusada (ErrNotAllowed).
func (s *Service) RunToolCaller(ctx context.Context, companyID, runID domain.ID) (ToolCaller, error) {
	q := s.read()
	rv, err := loadRunView(ctx, q, companyID, runID)
	if err != nil {
		return ToolCaller{}, err
	}
	if slices.Contains(systemRunTriggers, rv.Trigger) {
		return ToolCaller{}, fmt.Errorf("runs de sistema não usam o gateway de tools: %w", domain.ErrNotAllowed)
	}
	if !rv.Status.Active() {
		return ToolCaller{}, fmt.Errorf("a run não está ativa: %w", domain.ErrNotAllowed)
	}
	pid, rid := rv.PersonaID, rv.ID
	c := ToolCaller{CompanyID: companyID, RunID: &rid, RunName: rv.Name, PersonaID: &pid, Persona: rv.PersonaName}
	if rv.TaskID != (domain.ID{}) {
		tid := rv.TaskID
		c.TaskID, c.TaskKey = &tid, rv.TaskKey
		task, err := q.GetTaskByID(ctx, store.GetTaskByIDParams{CompanyID: companyID, ID: tid})
		if err != nil && !isNoRows(err) {
			return ToolCaller{}, fmt.Errorf("get run task: %w", err)
		}
		if err == nil {
			c.ProjectID = task.ProjectID
		}
	}
	return c, nil
}

// GatewayTool é uma tool como o gateway a anuncia (já com o nome exposto).
type GatewayTool struct {
	Name        string
	Title       string
	Description string
	InputSchema json.RawMessage
	Annotations domain.ToolAnnotations
}

// GatewayCallResult é o desfecho de um tools/call no gateway.
type GatewayCallResult struct {
	// Result é o CallToolResult do servidor remoto (chamada repassada).
	Result json.RawMessage
	// Message explica o que impediu a chamada (vira um resultado com isError para o agente).
	Message string
	// Unknown: a tool não está disponível para o chamador (vira erro de parâmetro do protocolo).
	Unknown  bool
	Decision domain.ToolDecision
	GateID   *domain.ID
}

// gatewayIntegration é uma integração alcançável pelo chamador com o perfil que vale.
type gatewayIntegration struct {
	row     store.Integration
	profile *domain.ToolProfile
}

// callerIntegrations devolve as integrações MCP http que o chamador alcança. Para a run, slug escolhe uma (as
// da persona: de todas ou ligadas a ela); para o gateway nomeado, todas as ligadas da empresa.
func (s *Service) callerIntegrations(ctx context.Context, q *store.Queries, c ToolCaller, slug string) ([]gatewayIntegration, error) {
	var prof *domain.ToolProfile
	var rows []store.Integration
	var err error
	if c.Named() {
		if c.ProfileID == nil {
			return nil, nil
		}
		if prof, err = loadProfileByID(ctx, q, c.CompanyID, *c.ProfileID); err != nil {
			return nil, err
		}
		if rows, err = q.ListIntegrations(ctx, c.CompanyID); err != nil {
			return nil, fmt.Errorf("list integrations: %w", err)
		}
	} else {
		if c.PersonaID == nil {
			return nil, nil
		}
		if prof, _, err = resolveToolProfile(ctx, q, c.CompanyID, *c.PersonaID, c.ProjectID); err != nil {
			return nil, err
		}
		if rows, err = q.ListMCPIntegrationsForPersona(ctx, store.ListMCPIntegrationsForPersonaParams{CompanyID: c.CompanyID, PersonaID: *c.PersonaID}); err != nil {
			return nil, fmt.Errorf("list persona integrations: %w", err)
		}
	}
	var out []gatewayIntegration
	for _, r := range rows {
		cfg := parseIntegrationConfig(r.Config)
		if domain.IntegrationKind(r.Kind) != domain.IntegrationMCP || !r.Enabled || cfg.Transport != domain.TransportHTTP {
			continue
		}
		if slug != "" && r.Slug != slug {
			continue
		}
		out = append(out, gatewayIntegration{row: r, profile: prof})
	}
	return out, nil
}

func (c ToolCaller) exposed(integSlug, tool string) string {
	if c.Named() {
		return integSlug + ToolNameSeparator + tool
	}
	return tool
}

// GatewayListTools devolve as tools que o chamador enxerga: ativas no catálogo e permitidas pelo perfil.
// integrationSlug escolhe a integração (run); vazio agrega todas (gateway nomeado).
func (s *Service) GatewayListTools(ctx context.Context, c ToolCaller, integrationSlug string) ([]GatewayTool, error) {
	if !c.Can(domain.GatewayActionList) {
		return nil, fmt.Errorf("o token não permite tools/list: %w", domain.ErrForbidden)
	}
	q := s.read()
	integs, err := s.callerIntegrations(ctx, q, c, integrationSlug)
	if err != nil {
		return nil, err
	}
	if !c.Named() && len(integs) == 0 {
		return nil, notFound("integration")
	}
	out := []GatewayTool{}
	for _, gi := range integs {
		if v := domain.EvaluateIntegration(gi.profile, gi.row.ID); !v.Allowed && v.Source == "exclude" {
			continue // a integração inteira está excluída
		}
		s.ensureCatalog(ctx, gi.row)
		rows, err := q.ListIntegrationTools(ctx, store.ListIntegrationToolsParams{CompanyID: c.CompanyID, IntegrationID: gi.row.ID})
		if err != nil {
			return nil, fmt.Errorf("list catalog: %w", err)
		}
		for _, r := range rows {
			if domain.ToolStatus(r.Status) != domain.ToolActive {
				continue
			}
			if !domain.EvaluateTool(gi.profile, gi.row.ID, r.Name, domain.ToolRisk(r.Risk)).Allowed {
				continue
			}
			name := c.exposed(gi.row.Slug, r.Name)
			if len(name) > maxToolNameLen {
				continue
			}
			tl := toolFromRow(r, gi.row.Slug)
			out = append(out, GatewayTool{Name: name, Title: tl.Title, Description: tl.Description, InputSchema: tl.InputSchema, Annotations: tl.Annotations})
		}
	}
	return out, nil
}

// toolCallHash é o sha256 da chamada: integração, tool e os argumentos em JSON canônico. Devolve também o tamanho
// dos argumentos e o JSON canônico (para repassar e para a pré-visualização do gate).
func toolCallHash(integrationID domain.ID, tool string, args json.RawMessage) (string, int, []byte, error) {
	canon, err := canonicalJSON(args)
	if err != nil {
		return "", 0, nil, domain.Invalid("arguments", "JSON inválido")
	}
	if string(canon) == "null" {
		canon = []byte("{}")
	}
	h := sha256.New()
	h.Write([]byte(integrationID.String()))
	h.Write([]byte{0})
	h.Write([]byte(tool))
	h.Write([]byte{0})
	h.Write(canon)
	return hex.EncodeToString(h.Sum(nil)), len(args), canon, nil
}

// invocation é o que a auditoria grava de uma chamada.
type invocation struct {
	integ    *store.Integration
	slug     string
	tool     string
	sha      string
	size     int
	decision domain.ToolDecision
	gateID   *domain.ID
	status   string
	latency  time.Duration
	errText  string
}

func (c ToolCaller) invocationParams(v invocation, now time.Time) store.InsertToolInvocationParams {
	p := store.InsertToolInvocationParams{ID: domain.NewID(), CompanyID: c.CompanyID, RunID: c.RunID, GatewayID: c.GatewayID,
		PersonaID: c.PersonaID, IntegrationSlug: v.slug, Tool: truncateRunes(v.tool, maxToolNameLen), ArgsSha256: v.sha,
		ArgsBytes: i32(v.size), Decision: string(v.decision), GateID: v.gateID, Status: v.status,
		LatencyMs: i32(int(v.latency / time.Millisecond)), Error: truncateRunes(v.errText, 300), Now: now}
	if v.integ != nil {
		id := v.integ.ID
		p.IntegrationID = &id
	}
	return p
}

// audit grava a chamada fora de transação (a auditoria não pode derrubar a resposta: o erro só é devolvido).
func (s *Service) audit(ctx context.Context, c ToolCaller, v invocation) error {
	if _, err := store.New(s.Pool).InsertToolInvocation(ctx, c.invocationParams(v, s.Now().UTC())); err != nil {
		return fmt.Errorf("record tool invocation: %w", err)
	}
	return nil
}

// GatewayCallTool decide e, se for o caso, repassa uma chamada. Só um erro de infraestrutura (banco) volta como
// error; as decisões de política voltam no resultado.
func (s *Service) GatewayCallTool(ctx context.Context, c ToolCaller, integrationSlug, name string, args json.RawMessage) (GatewayCallResult, error) {
	if !c.Can(domain.GatewayActionCall) {
		return GatewayCallResult{}, fmt.Errorf("o token não permite tools/call: %w", domain.ErrForbidden)
	}
	if len(args) > maxToolArgsBytes {
		return GatewayCallResult{Decision: domain.ToolDecisionDeny, Message: fmt.Sprintf("argumentos grandes demais (máximo de %d KiB)", maxToolArgsBytes>>10)}, nil
	}
	tool := name
	if c.Named() {
		var ok bool
		if integrationSlug, tool, ok = strings.Cut(name, ToolNameSeparator); !ok {
			integrationSlug, tool = "", name
		}
	}
	v := invocation{slug: integrationSlug, tool: tool, size: len(args), decision: domain.ToolDecisionDeny, status: domain.ToolCallBlocked}
	deny := func(msg string, unknown bool) (GatewayCallResult, error) {
		v.errText = msg
		if err := s.audit(ctx, c, v); err != nil {
			return GatewayCallResult{}, err
		}
		return GatewayCallResult{Decision: v.decision, Message: msg, Unknown: unknown, GateID: v.gateID}, nil
	}
	q := s.read()
	integs, err := s.callerIntegrations(ctx, q, c, integrationSlug)
	if err != nil {
		return GatewayCallResult{}, err
	}
	if integrationSlug == "" || len(integs) == 0 {
		return deny(fmt.Sprintf("tool %q não está disponível", name), true)
	}
	gi := integs[0]
	v.integ = &gi.row
	sha, size, canon, err := toolCallHash(gi.row.ID, tool, args)
	if err != nil {
		return deny("argumentos inválidos: JSON malformado", true)
	}
	v.sha, v.size = sha, size

	s.ensureCatalog(ctx, gi.row)
	tr, err := q.GetIntegrationToolByName(ctx, store.GetIntegrationToolByNameParams{CompanyID: c.CompanyID, IntegrationID: gi.row.ID, Name: tool})
	if err != nil {
		if isNoRows(err) {
			return deny(fmt.Sprintf("tool %q não está no catálogo da integração %q", tool, gi.row.Slug), true)
		}
		return GatewayCallResult{}, fmt.Errorf("get catalog tool: %w", err)
	}
	switch domain.ToolStatus(tr.Status) {
	case domain.ToolQuarantined:
		v.decision = domain.ToolDecisionQuarantined
		return deny(fmt.Sprintf("a tool %q está em quarentena: ela é nova ou mudou e pode escrever; um admin precisa revisá-la em Integrações › Tools", tool), false)
	case domain.ToolDisabled, domain.ToolRemoved:
		return deny(fmt.Sprintf("tool %q não está disponível", name), true)
	}
	verdict := domain.EvaluateTool(gi.profile, gi.row.ID, tool, domain.ToolRisk(tr.Risk))
	if !verdict.Allowed {
		return deny(fmt.Sprintf("tool %q não está disponível", name), true)
	}
	if verdict.Mode == domain.ToolModeAsk {
		res, pass, err := s.askApproval(ctx, c, gi, toolFromRow(tr, gi.row.Slug), sha, size, canon, gi.profile)
		if err != nil || !pass {
			return res, err
		}
		v.gateID = res.GateID
	}

	// teto de taxa por (persona ou gateway, integração)
	limit := domain.DefaultToolRateLimit
	if gi.profile != nil {
		limit = gi.profile.RateLimitPerMin
	}
	n, err := q.CountToolCallsSince(ctx, store.CountToolCallsSinceParams{IntegrationID: &gi.row.ID, Since: s.Now().UTC().Add(-toolRateWindow),
		PersonaID: c.PersonaID, GatewayID: c.GatewayID})
	if err != nil {
		return GatewayCallResult{}, fmt.Errorf("count tool calls: %w", err)
	}
	if int(n) >= limit {
		v.decision = domain.ToolDecisionDeny
		return deny(fmt.Sprintf("limite de %d chamadas por minuto para a integração %q atingido; espere um pouco e tente de novo", limit, gi.row.Slug), false)
	}

	// repasse
	v.decision = domain.ToolDecisionAllow
	ep, err := s.toolEndpoint(ctx, gi.row)
	if err != nil {
		v.decision, v.status = domain.ToolDecisionError, domain.ToolCallFailed
		return deny("a integração não pode ser alcançada: "+err.Error(), false)
	}
	start := time.Now()
	raw, cerr := mcpupstream.CallTool(ctx, ep, tool, canon)
	v.latency = time.Since(start)
	if cerr == nil && len(raw) > maxToolResultBytes {
		cerr = fmt.Errorf("resultado grande demais (%d MiB no máximo)", maxToolResultBytes>>20)
	}
	if cerr != nil {
		msg := cerr.Error()
		if sec, ok := s.secretsForRedaction(ctx, gi.row.CompanyID, gi.row.ID, gi.row.SecretEnc); ok {
			msg = redactSecrets(msg, sec)
		}
		v.status, v.errText = domain.ToolCallFailed, msg
		var re *mcpupstream.RemoteError
		if !errors.As(cerr, &re) {
			v.decision = domain.ToolDecisionError
		}
		if err := s.audit(ctx, c, v); err != nil {
			return GatewayCallResult{}, err
		}
		return GatewayCallResult{Decision: v.decision, Message: "a chamada falhou: " + truncateRunes(msg, 500), GateID: v.gateID}, nil
	}
	v.status = domain.ToolCallOK
	var probe struct {
		IsError bool `json:"isError"`
	}
	if json.Unmarshal(raw, &probe) == nil && probe.IsError {
		v.status = domain.ToolCallFailed
	}
	if err := s.audit(ctx, c, v); err != nil {
		return GatewayCallResult{}, err
	}
	return GatewayCallResult{Result: raw, Decision: domain.ToolDecisionAllow, GateID: v.gateID}, nil
}

// askApproval aplica o modo "perguntar antes". pass=true: há uma aprovação válida para esta chamada exata
// (consumida agora); senão o resultado explica (gate aberto agora, gate pendente, ou recusado).
func (s *Service) askApproval(ctx context.Context, c ToolCaller, gi gatewayIntegration, tl domain.IntegrationTool, sha string, size int,
	canon []byte, prof *domain.ToolProfile) (GatewayCallResult, bool, error) {
	var res GatewayCallResult
	pass := false
	who := ""
	if c.Named() {
		who = "gw:" + c.GatewayID.String()
	} else {
		who = "p:" + c.PersonaID.String()
	}
	err := s.withTx(ctx, func(t *txc) error {
		res, pass = GatewayCallResult{}, false
		if err := t.q.LockToolCallKey(ctx, "tool_call:"+who+":"+gi.row.ID.String()+":"+tl.Name+":"+sha); err != nil {
			return fmt.Errorf("lock tool call: %w", err)
		}
		rows, err := t.q.FindToolCallApprovals(ctx, store.FindToolCallApprovalsParams{CompanyID: c.CompanyID, IntegrationID: gi.row.ID,
			Tool: tl.Name, ArgsSha256: sha, PersonaID: c.PersonaID, GatewayID: c.GatewayID, Since: t.now.Add(-toolApprovalWindow)})
		if err != nil {
			return fmt.Errorf("find approvals: %w", err)
		}
		v := invocation{integ: &gi.row, slug: gi.row.Slug, tool: tl.Name, sha: sha, size: size}
		if len(rows) > 0 {
			last := rows[0]
			gid := last.GateID
			switch domain.GateStatus(last.GateStatus) {
			case domain.GateApproved:
				if last.ConsumedAt == nil {
					n, err := t.q.ConsumeToolCallApproval(ctx, store.ConsumeToolCallApprovalParams{ID: last.ID, Now: &t.now})
					if err != nil {
						return fmt.Errorf("consume approval: %w", err)
					}
					if n == 1 {
						pass, res.GateID = true, &gid
						return nil
					}
				}
			case domain.GatePending:
				v.decision, v.status, v.gateID = domain.ToolDecisionAsk, domain.ToolCallPending, &gid
				res = GatewayCallResult{Decision: domain.ToolDecisionAsk, GateID: &gid, Message: fmt.Sprintf(
					"aguardando aprovação: gate %s (Caixa de entrada › Tools); repita a chamada com os mesmos argumentos depois da decisão", gid)}
				_, err := t.q.InsertToolInvocation(ctx, c.invocationParams(v, t.now))
				return err
			case domain.GateRejected, domain.GateChangesRequested:
				v.decision, v.status, v.gateID, v.errText = domain.ToolDecisionDeny, domain.ToolCallBlocked, &gid, "recusada no gate"
				res = GatewayCallResult{Decision: domain.ToolDecisionDeny, GateID: &gid, Message: fmt.Sprintf(
					"esta chamada foi recusada no gate %s; não a repita com os mesmos argumentos", gid)}
				_, err := t.q.InsertToolInvocation(ctx, c.invocationParams(v, t.now))
				return err
			}
		}
		gid, err := t.openToolGate(c, gi, tl, sha, size, canon, prof)
		if err != nil {
			return err
		}
		v.decision, v.status, v.gateID = domain.ToolDecisionAsk, domain.ToolCallPending, &gid
		res = GatewayCallResult{Decision: domain.ToolDecisionAsk, GateID: &gid, Message: fmt.Sprintf(
			"aguardando aprovação: a chamada abriu o gate %s (Caixa de entrada › Tools); repita a chamada com exatamente os mesmos argumentos depois da decisão — argumentos diferentes pedem uma nova aprovação", gid)}
		_, err = t.q.InsertToolInvocation(ctx, c.invocationParams(v, t.now))
		return err
	})
	if err != nil {
		return GatewayCallResult{}, false, err
	}
	return res, pass, nil
}

// openToolGate abre o gate tool e o pedido de aprovação da chamada.
func (t *txc) openToolGate(c ToolCaller, gi gatewayIntegration, tl domain.IntegrationTool, sha string, size int, canon []byte,
	prof *domain.ToolProfile) (domain.ID, error) {
	actor := domain.SystemActor
	who := "O gateway **" + c.GatewayName + "**"
	if !c.Named() {
		actor = domain.PersonaActor(*c.PersonaID)
		who = "A persona **" + c.Persona + "**"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s quer chamar a tool `%s` da integração **%s** (`%s`)", who, tl.Name, gi.row.Name, gi.row.Slug)
	if prof != nil {
		fmt.Fprintf(&b, " e o perfil **%s** pede aprovação antes", prof.Name)
	}
	b.WriteString(".\n\n")
	if c.RunName != "" {
		fmt.Fprintf(&b, "- Execução: %s\n", c.RunName)
	}
	if c.TaskKey != "" {
		fmt.Fprintf(&b, "- Task: %s\n", c.TaskKey)
	}
	fmt.Fprintf(&b, "- Risco declarado: %s\n- sha256 da chamada: `%s` (%d bytes de argumentos)\n\n", tl.Risk, sha, size)
	b.WriteString("Aprovar libera **uma** repetição desta chamada com exatamente os mesmos argumentos. Argumentos diferentes pedem uma nova aprovação.\n\n")
	preview := string(canon)
	if sec, ok := t.s.secretsForRedaction(t.ctx, gi.row.CompanyID, gi.row.ID, gi.row.SecretEnc); ok {
		preview = redactSecrets(preview, sec)
	} else {
		preview = adapter.Redact(preview)
	}
	if len(preview) > toolGateArgsPreview {
		preview = toolClipBytes(preview, toolGateArgsPreview) + "\n… (cortado)"
	}
	b.WriteString("## Argumentos\n\n")
	b.WriteString(codeBlock("json", preview))
	title := truncateRunes(fmt.Sprintf("Tool %s/%s: aprovar a chamada", gi.row.Slug, tl.Name), 300)
	var personaID *domain.ID
	if !c.Named() {
		personaID = c.PersonaID
	}
	g, err := t.createGate(actor, c.CompanyID, RequestGateInput{Kind: domain.GateTool, Title: title, Body: b.String(), RunID: c.RunID}, personaID)
	if err != nil {
		return domain.ID{}, err
	}
	if _, err := t.q.CreateToolCallApproval(t.ctx, store.CreateToolCallApprovalParams{ID: domain.NewID(), CompanyID: c.CompanyID, GateID: g.ID,
		IntegrationID: gi.row.ID, Tool: tl.Name, ArgsSha256: sha, RunID: c.RunID, TaskID: c.TaskID, PersonaID: personaID,
		GatewayID: c.GatewayID, Now: t.now}); err != nil {
		return domain.ID{}, mapErr(err, "create tool approval")
	}
	return g.ID, nil
}

// toolGateDecided roda dentro do DecideGate de um gate tool: aprovado, acorda a persona na task da run que pediu
// (gatilho gate) para ela repetir a chamada. Rejeitar só registra (a próxima chamada igual é recusada).
func (t *txc) toolGateDecided(g domain.Gate, decision domain.GateStatus) error {
	a, err := t.q.GetToolCallApprovalByGate(t.ctx, store.GetToolCallApprovalByGateParams{CompanyID: g.CompanyID, GateID: g.ID})
	if err != nil {
		if isNoRows(err) {
			return nil
		}
		return fmt.Errorf("get tool approval: %w", err)
	}
	if err := t.activity(domain.SystemActor, g.CompanyID, "tool.call_decided", "gate", g.ID, map[string]any{"decision": decision,
		"integration_id": a.IntegrationID, "tool": a.Tool, "args_sha256": a.ArgsSha256, "persona_id": a.PersonaID, "gateway_id": a.GatewayID}, ""); err != nil {
		return err
	}
	if decision != domain.GateApproved || a.TaskID == nil || a.PersonaID == nil {
		return nil
	}
	task, err := t.q.GetTaskByID(t.ctx, store.GetTaskByIDParams{CompanyID: g.CompanyID, ID: *a.TaskID})
	if err != nil {
		if isNoRows(err) {
			return nil
		}
		return fmt.Errorf("get tool gate task: %w", err)
	}
	if task.PersonaID == nil || *task.PersonaID != *a.PersonaID {
		return nil
	}
	return t.wake(g.CompanyID, task.ID, task.PersonaID, domain.TaskStatus(task.Status), domain.TriggerGate, false)
}

// ToolInvocationFilter filtra a auditoria.
type ToolInvocationFilter struct {
	IntegrationID *domain.ID
	PersonaID     *domain.ID
	GatewayID     *domain.ID
	RunID         *domain.ID
	Decision      domain.ToolDecision
	Tool          string
	Since         *time.Time
	Limit         int // padrão 100, máximo 500
	Offset        int
}

// ListToolInvocations lista a auditoria do gateway (só admin: mostra o que as personas fazem fora do Hanko).
func (s *Service) ListToolInvocations(ctx context.Context, companyID domain.ID, f ToolInvocationFilter) ([]domain.ToolInvocation, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return nil, err
	}
	p := store.ListToolInvocationsParams{CompanyID: companyID, IntegrationID: f.IntegrationID, PersonaID: f.PersonaID, GatewayID: f.GatewayID,
		RunID: f.RunID, Since: f.Since, Lim: i32(clamp(f.Limit, 100, 500)), Off: i32(max(f.Offset, 0))}
	if f.Decision != "" {
		if !f.Decision.Valid() {
			return nil, domain.Invalid("decision", "use allow, deny, ask, quarantined ou error")
		}
		p.Decision = ptr(string(f.Decision))
	}
	if t := strings.TrimSpace(f.Tool); t != "" {
		p.Tool = &t
	}
	rows, err := s.read().ListToolInvocations(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("list tool invocations: %w", err)
	}
	out := make([]domain.ToolInvocation, len(rows))
	for i, r := range rows {
		out[i] = domain.ToolInvocation{ID: r.ID, RunID: r.RunID, GatewayID: r.GatewayID, GatewaySlug: r.GatewaySlug, PersonaID: r.PersonaID,
			PersonaName: r.PersonaName, IntegrationID: r.IntegrationID, IntegrationSlug: r.IntegrationSlug, Tool: r.Tool, ArgsSHA256: r.ArgsSha256,
			ArgsBytes: int(r.ArgsBytes), Decision: domain.ToolDecision(r.Decision), GateID: r.GateID, Status: r.Status,
			LatencyMs: int(r.LatencyMs), Error: r.Error, CreatedAt: r.CreatedAt}
	}
	return out, nil
}

// PruneToolInvocations aplica a retenção da auditoria (30 dias e 10 mil por empresa). Chamado pelo tick periódico,
// sem identidade de usuário.
func (s *Service) PruneToolInvocations(ctx context.Context) (int64, error) {
	q := store.New(s.Pool)
	a, err := q.PruneToolInvocationsBefore(ctx, s.Now().UTC().Add(-toolInvocationMaxAge))
	if err != nil {
		return 0, fmt.Errorf("prune old tool invocations: %w", err)
	}
	b, err := q.PruneToolInvocationsOverCap(ctx, toolInvocationCap)
	if err != nil {
		return a, fmt.Errorf("prune tool invocations over cap: %w", err)
	}
	return a + b, nil
}

// runToolProfile resolve o perfil de ferramentas de uma run (nil = sem perfil).
func (s *Service) runToolProfile(ctx context.Context, q *store.Queries, companyID, personaID domain.ID, projectID *domain.ID) (*domain.ToolProfile, error) {
	prof, _, err := resolveToolProfile(ctx, q, companyID, personaID, projectID)
	return prof, err
}

// toolProfileSkips diz se uma integração MCP fica de fora da run e por quê: excluída inteira pelo perfil, ou stdio
// que o perfil não inclui (stdio não passa pelo gateway, então só a integração inteira conta).
func toolProfileSkips(prof *domain.ToolProfile, r store.Integration, cfg domain.IntegrationConfig) (string, bool) {
	v := domain.EvaluateIntegration(prof, r.ID)
	if v.Allowed {
		return "", false
	}
	if cfg.Transport == domain.TransportStdio {
		if v.Mode == domain.ToolModeAsk {
			return fmt.Sprintf("integração %q fora desta execução: stdio não passa pelo gateway, então \"perguntar antes\" não vale para ela", r.Slug), true
		}
		return fmt.Sprintf("integração %q fora desta execução pelo perfil de ferramentas %q", r.Slug, prof.Slug), true
	}
	if v.Source == "exclude" {
		return fmt.Sprintf("integração %q fora desta execução pelo perfil de ferramentas %q", r.Slug, prof.Slug), true
	}
	return "", false // http: o gateway filtra tool a tool
}

// RunExtrasForRun é RunExtras para uma run: resolve o projeto da task da run para o perfil de ferramentas e avisa
// quando o destino de uma integração http é interno e a política do operador o bloqueia.
func (s *Service) RunExtrasForRun(ctx context.Context, companyID, personaID, runID domain.ID) (RunExtras, error) {
	var projectID *domain.ID
	q := s.read()
	if run, err := q.GetRunByID(ctx, store.GetRunByIDParams{CompanyID: companyID, ID: runID}); err == nil && run.TaskID != nil {
		if task, err := q.GetTaskByID(ctx, store.GetTaskByIDParams{CompanyID: companyID, ID: *run.TaskID}); err == nil {
			projectID = task.ProjectID
		}
	}
	ex, err := s.RunExtrasForProject(ctx, companyID, personaID, projectID)
	if err != nil {
		return ex, err
	}
	for _, slug := range ex.ToolGateway {
		integ, err := q.GetIntegrationBySlug(ctx, store.GetIntegrationBySlugParams{CompanyID: companyID, Slug: slug})
		if err != nil {
			continue
		}
		if _, err := netguard.ValidateURL(parseIntegrationConfig(integ.Config).URL, true, s.Integrations.policy()); err != nil {
			ex.Warnings = append(ex.Warnings, fmt.Sprintf("integração %q: o destino é interno e este servidor bloqueia destinos internos "+
				"(HANKO_INTEGRATIONS_ALLOW_PRIVATE); as chamadas pelo gateway de tools vão falhar", slug))
		}
	}
	return ex, nil
}
