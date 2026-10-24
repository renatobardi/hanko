package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Motor de política de tools (ADR-0053). Fica por cima do perfil de ferramentas do ADR-0036: a política mais
// específica que alcança a chamada decide (persona > empresa > perfil; tool > integração > curinga; off > ask >
// allow no empate). O mesmo motor vale para as tools de integração (gateway), as tools nativas do harness (hook do
// claude, permission request do ACP) e as de plugin (servidor MCP do Hanko).

var nativeToolNameRe = regexp.MustCompile(`^[A-Za-z0-9_.:\-]{1,128}$`)

func policyFromRow(r store.ListToolPoliciesRow) domain.ToolPolicy {
	p := domain.ToolPolicy{ID: r.ID, CompanyID: r.CompanyID, Scope: domain.ToolPolicyScope(r.Scope), PersonaID: r.PersonaID,
		PersonaName: r.PersonaName, Target: domain.ToolPolicyTarget(r.Target), IntegrationID: r.IntegrationID,
		IntegrationSlug: r.IntegrationSlug, ToolName: r.ToolName, Mode: domain.ToolPolicyMode(r.Mode),
		Origin: domain.ToolPolicyOrigin(r.Origin), CreatedBy: actorOf(r.CreatedByType, r.CreatedByID),
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	return p
}

// loadToolPolicies carrega as políticas que valem para a persona (as da empresa e as dela); persona nil, só as da empresa.
func loadToolPolicies(ctx context.Context, q *store.Queries, companyID domain.ID, personaID *domain.ID) ([]domain.ToolPolicy, error) {
	rows, err := q.ListToolPoliciesFor(ctx, store.ListToolPoliciesForParams{CompanyID: companyID, PersonaID: personaID})
	if err != nil {
		return nil, fmt.Errorf("list tool policies: %w", err)
	}
	out := make([]domain.ToolPolicy, len(rows))
	for i, r := range rows {
		out[i] = policyFromRow(store.ListToolPoliciesRow(r))
	}
	return out, nil
}

// toolVerdict é a decisão sobre uma tool de integração: o perfil, e por cima dele as políticas. No gateway nomeado
// (cliente de fora) as políticas só apertam: o perfil do gateway é a fronteira que o admin desenhou para o cliente, e um
// "permitir" da empresa não pode abri-la.
func toolVerdict(gi gatewayIntegration, pols []domain.ToolPolicy, c ToolCaller, tool string, risk domain.ToolRisk) domain.ToolVerdict {
	profile := domain.EvaluateTool(gi.profile, gi.row.ID, tool, risk)
	id := gi.row.ID
	v := domain.ApplyToolPolicies(profile, pols, domain.ToolSubject{Kind: domain.SubjectIntegration, PersonaID: c.PersonaID, IntegrationID: &id, Tool: tool})
	if c.Named() {
		switch {
		case !profile.Allowed:
			return profile
		case profile.Mode == domain.ToolModeAsk && v.Allowed && v.Mode == domain.ToolModeAllow:
			return profile
		}
	}
	return v
}

// hasIntegrationPolicy informa se alguma política alcança a integração (para não esconder a integração inteira quando o
// perfil a exclui mas uma política decide tool a tool).
func hasIntegrationPolicy(pols []domain.ToolPolicy, integrationID domain.ID) bool {
	for _, p := range pols {
		if p.Target == domain.PolicyTargetAll || p.IntegrationID != nil && *p.IntegrationID == integrationID {
			return true
		}
	}
	return false
}

// policyLabel descreve a política em uma linha (corpo do gate, mensagens).
func policyLabel(p domain.ToolPolicy) string {
	who := "empresa"
	if p.Scope == domain.PolicyScopePersona {
		who = "persona"
		if p.PersonaName != "" {
			who += " " + p.PersonaName
		}
	}
	what := "qualquer tool"
	switch p.Target {
	case domain.PolicyTargetIntegration:
		what = "integração " + p.IntegrationSlug
	case domain.PolicyTargetTool:
		what = "tool " + p.IntegrationSlug + "/" + p.ToolName
	case domain.PolicyTargetNative:
		what = "tool nativa " + p.ToolName
	}
	return fmt.Sprintf("%s › %s › %s", who, what, p.Mode)
}

// redactedResultPreview devolve o início do texto do resultado de uma tool, sem segredos da integração nem padrões de
// credencial (vai para a auditoria).
func (s *Service) redactedResultPreview(ctx context.Context, integ store.Integration, raw json.RawMessage) string {
	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &res) != nil {
		return ""
	}
	var b strings.Builder
	for _, c := range res.Content {
		if c.Type != "" && c.Type != "text" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(c.Text)
		if b.Len() > 4*toolResultPreview {
			break
		}
	}
	text := truncateRunes(b.String(), 2*toolResultPreview)
	if sec, ok := s.secretsForRedaction(ctx, integ.CompanyID, integ.ID, integ.SecretEnc); ok {
		text = redactSecrets(text, sec)
	} else {
		text = adapter.Redact(text)
	}
	return truncateRunes(text, toolResultPreview)
}

// ---------- CRUD (admin) ----------

// ToolPolicyInput é uma política a gravar. Uma política com a mesma chave (escopo, persona, alvo, integração, tool)
// é substituída.
type ToolPolicyInput struct {
	Scope         domain.ToolPolicyScope
	PersonaID     *domain.ID
	Target        domain.ToolPolicyTarget
	IntegrationID *domain.ID
	ToolName      string
	Mode          domain.ToolPolicyMode
}

func (t *txc) validatePolicy(companyID domain.ID, in *ToolPolicyInput) (integSlug, personaName string, err error) {
	if !in.Scope.Valid() {
		return "", "", domain.Invalid("scope", "use company ou persona")
	}
	if !in.Target.Valid() {
		return "", "", domain.Invalid("target", "use all, integration, tool ou native")
	}
	if !in.Mode.Valid() {
		return "", "", domain.Invalid("mode", "use off, ask ou allow")
	}
	in.ToolName = strings.TrimSpace(in.ToolName)
	switch in.Scope {
	case domain.PolicyScopeCompany:
		if in.PersonaID != nil {
			return "", "", domain.Invalid("persona", "a política da empresa não tem persona")
		}
	case domain.PolicyScopePersona:
		if in.PersonaID == nil {
			return "", "", domain.Invalid("persona", "required")
		}
		p, err := t.q.GetPersonaByID(t.ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: *in.PersonaID})
		if err != nil {
			return "", "", domain.Invalid("persona", "persona not found in company")
		}
		personaName = p.Name
	}
	switch in.Target {
	case domain.PolicyTargetAll:
		if in.IntegrationID != nil || in.ToolName != "" {
			return "", "", domain.Invalid("target", "all não leva integração nem tool")
		}
	case domain.PolicyTargetIntegration, domain.PolicyTargetTool:
		if in.IntegrationID == nil {
			return "", "", domain.Invalid("integration", "required")
		}
		if in.Target == domain.PolicyTargetIntegration && in.ToolName != "" {
			return "", "", domain.Invalid("tool", "só vale para o alvo tool")
		}
		if in.Target == domain.PolicyTargetTool && !toolNameRe.MatchString(in.ToolName) {
			return "", "", domain.Invalid("tool", "nome de tool inválido")
		}
		r, err := t.q.GetIntegrationByID(t.ctx, store.GetIntegrationByIDParams{CompanyID: companyID, ID: *in.IntegrationID})
		if err != nil {
			return "", "", domain.Invalid("integration", "integration not found in company")
		}
		if domain.IntegrationKind(r.Kind) != domain.IntegrationMCP {
			return "", "", domain.Invalid("integration", "só servidores MCP têm política de tools")
		}
		if parseIntegrationConfig(r.Config).Transport == domain.TransportStdio {
			return "", "", domain.Invalid("integration", "MCP stdio não passa pelo gateway: use o perfil de ferramentas para incluir ou excluir a integração")
		}
		integSlug = r.Slug
	case domain.PolicyTargetNative:
		if in.IntegrationID != nil {
			return "", "", domain.Invalid("integration", "a tool nativa não tem integração")
		}
		if !nativeToolNameRe.MatchString(in.ToolName) {
			return "", "", domain.Invalid("tool", "nome de tool nativa inválido (ex.: Bash, Edit, WebFetch)")
		}
	}
	return integSlug, personaName, nil
}

// setPolicy grava a política dentro da transação do chamador (CRUD e "sempre permitir").
func (t *txc) setPolicy(actor domain.Actor, companyID domain.ID, in ToolPolicyInput, origin domain.ToolPolicyOrigin) (domain.ToolPolicy, error) {
	integSlug, personaName, err := t.validatePolicy(companyID, &in)
	if err != nil {
		return domain.ToolPolicy{}, err
	}
	n, err := t.q.CountToolPolicies(t.ctx, companyID)
	if err != nil {
		return domain.ToolPolicy{}, fmt.Errorf("count tool policies: %w", err)
	}
	if int(n) >= domain.MaxToolPolicies {
		return domain.ToolPolicy{}, domain.Invalid("policy", fmt.Sprintf("no máximo %d políticas por empresa", domain.MaxToolPolicies))
	}
	row, err := t.q.UpsertToolPolicy(t.ctx, store.UpsertToolPolicyParams{ID: domain.NewID(), CompanyID: companyID, Scope: string(in.Scope),
		PersonaID: in.PersonaID, Target: string(in.Target), IntegrationID: in.IntegrationID, ToolName: in.ToolName, Mode: string(in.Mode),
		Origin: string(origin), CreatedByType: string(actor.Type), CreatedByID: actor.ID, Now: t.now})
	if err != nil {
		return domain.ToolPolicy{}, mapErr(err, "upsert tool policy")
	}
	p := policyFromRow(store.ListToolPoliciesRow{ID: row.ID, CompanyID: row.CompanyID, Scope: row.Scope, PersonaID: row.PersonaID, Target: row.Target,
		IntegrationID: row.IntegrationID, ToolName: row.ToolName, Mode: row.Mode, Origin: row.Origin, CreatedByType: row.CreatedByType,
		CreatedByID: row.CreatedByID, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, IntegrationSlug: integSlug, PersonaName: personaName})
	if err := t.activity(actor, companyID, "tool_policy.set", "tool_policy", p.ID, map[string]any{"scope": p.Scope, "persona_id": p.PersonaID,
		"target": p.Target, "integration": integSlug, "tool": p.ToolName, "mode": p.Mode, "origin": p.Origin}, ""); err != nil {
		return domain.ToolPolicy{}, err
	}
	return p, nil
}

// SetToolPolicy grava (ou troca) uma política (admin).
func (s *Service) SetToolPolicy(ctx context.Context, actor domain.Actor, companyID domain.ID, in ToolPolicyInput) (domain.ToolPolicy, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.ToolPolicy{}, err
	}
	ctx, end := toolSpan(ctx, "service.set_tool_policy", attribute.String("target", string(in.Target)), attribute.String("mode", string(in.Mode)))
	defer end()
	var out domain.ToolPolicy
	err := s.withTx(ctx, func(t *txc) error {
		var err error
		out, err = t.setPolicy(actor, companyID, in, domain.PolicyOriginManual)
		return err
	})
	return out, err
}

// DeleteToolPolicy tira uma política (admin): a chamada volta a valer o que o perfil (ou o harness) diz.
func (s *Service) DeleteToolPolicy(ctx context.Context, actor domain.Actor, companyID, policyID domain.ID) error {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetToolPolicy(ctx, store.GetToolPolicyParams{CompanyID: companyID, ID: policyID})
		if err != nil {
			return mapErr(err, "get tool policy")
		}
		if _, err := t.q.DeleteToolPolicy(ctx, store.DeleteToolPolicyParams{CompanyID: companyID, ID: policyID}); err != nil {
			return fmt.Errorf("delete tool policy: %w", err)
		}
		return t.activity(actor, companyID, "tool_policy.deleted", "tool_policy", policyID, map[string]any{"scope": cur.Scope,
			"persona_id": cur.PersonaID, "target": cur.Target, "integration_id": cur.IntegrationID, "tool": cur.ToolName, "mode": cur.Mode}, "")
	})
}

// ListToolPolicies lista as políticas da empresa (de uma persona, quando informada). Qualquer membro lê.
func (s *Service) ListToolPolicies(ctx context.Context, companyID domain.ID, personaID *domain.ID) ([]domain.ToolPolicy, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.read().ListToolPolicies(ctx, store.ListToolPoliciesParams{CompanyID: companyID, PersonaID: personaID})
	if err != nil {
		return nil, fmt.Errorf("list tool policies: %w", err)
	}
	out := make([]domain.ToolPolicy, len(rows))
	for i, r := range rows {
		out[i] = policyFromRow(r)
	}
	return out, nil
}

// NativeToolPolicies informa se há política que alcança as tools nativas do harness para a persona (o executor só liga o
// hook do claude e o guarda do ACP quando há).
func (s *Service) NativeToolPolicies(ctx context.Context, companyID, personaID domain.ID) (bool, error) {
	ok, err := s.read().HasNativeToolPolicies(ctx, store.HasNativeToolPoliciesParams{CompanyID: companyID, PersonaID: &personaID})
	if err != nil {
		return false, fmt.Errorf("has native tool policies: %w", err)
	}
	return ok, nil
}

// ---------- tools nativas do harness (e de plugin) ----------

// Decisões do motor para uma tool nativa.
const (
	NativeAllow = "allow" // passa direto
	NativeDeny  = "deny"  // recusada (política off, rejeitada no gate ou prazo esgotado)
	NativeAsk   = "ask"   // aguarda a decisão do gate
	NativeNone  = "none"  // nenhuma política alcança a chamada: vale o que o harness já fazia
)

// NativeToolRequest é a chamada de uma tool nativa vista por quem a intercepta.
type NativeToolRequest struct {
	// Name é o nome da tool: Bash, Edit... no claude; o tipo da chamada (execute, edit...) no ACP; plugin_<id>_<tool> nas de plugin.
	Name   string
	Title  string // texto curto para o gate (ACP traz o título da chamada)
	Input  json.RawMessage
	Plugin bool // tool de plugin: só política por nome, sem o curinga
	// Hanko: tool do próprio servidor MCP do Hanko que expõe segredo (hanko_secret_get, ADR-0068 §4.2): só por nome, e sem
	// política a chamada pergunta antes (ask), o padrão da empresa.
	Hanko bool
}

// NativeToolDecision é a decisão do motor.
type NativeToolDecision struct {
	Decision string
	GateID   *domain.ID
	Reason   string
	Policy   string
}

func nativeToolKey(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '.', r == ':', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		out = "other"
	}
	return truncateRunes(out, 128)
}

func nativeCallHash(name string, input json.RawMessage) (string, int, []byte, error) {
	canon, err := canonicalJSON(input)
	if err != nil || len(input) == 0 {
		canon, err = []byte("{}"), nil
	}
	if string(canon) == "null" {
		canon = []byte("{}")
	}
	h := sha256.New()
	h.Write([]byte("native"))
	h.Write([]byte{0})
	h.Write([]byte(name))
	h.Write([]byte{0})
	h.Write(canon)
	return hex.EncodeToString(h.Sum(nil)), len(input), canon, err
}

// NativeToolCheck aplica a política a uma tool nativa (hook PreToolUse do claude, permission request do ACP) ou de
// plugin. NativeNone: nenhuma política alcança a chamada (nada é auditado). A chamada interceptada fica na auditoria.
// Em ask, abre (ou reaproveita) o gate; quem chama espera a decisão por NativeToolGateState ou AwaitNativeToolGate.
func (s *Service) NativeToolCheck(ctx context.Context, c ToolCaller, req NativeToolRequest) (NativeToolDecision, error) {
	ctx, end := toolSpan(ctx, "service.native_tool_check", attribute.String("source", string(c.source())))
	defer end()
	if c.PersonaID == nil {
		return NativeToolDecision{Decision: NativeNone}, nil
	}
	name := nativeToolKey(req.Name)
	q := s.read()
	pols, err := loadToolPolicies(ctx, q, c.CompanyID, c.PersonaID)
	if err != nil {
		return NativeToolDecision{}, err
	}
	kind := domain.SubjectNative
	switch {
	case req.Hanko:
		kind = domain.SubjectHanko
	case req.Plugin:
		kind = domain.SubjectPlugin
	}
	subject := domain.ToolSubject{Kind: kind, PersonaID: c.PersonaID, Tool: name}
	verdict, opinion := domain.NativeToolVerdict(pols, subject)
	if req.Hanko {
		verdict, opinion = domain.HankoToolVerdict(pols, subject), true
	}
	if !opinion {
		return NativeToolDecision{Decision: NativeNone}, nil
	}
	sha, size, canon, _ := nativeCallHash(name, req.Input)
	v := invocation{tool: name, sha: sha, size: size, native: true, policy: verdict.Source, decision: domain.ToolDecisionDeny, status: domain.ToolCallBlocked}
	label := verdictLabel(verdict)
	switch {
	case !verdict.Allowed:
		v.errText = "desligada pela política (" + label + ")"
		if err := s.audit(ctx, c, v); err != nil {
			return NativeToolDecision{}, err
		}
		return NativeToolDecision{Decision: NativeDeny, Reason: fmt.Sprintf("a tool %s está desligada para esta persona pela política: %s", name, label), Policy: verdict.Source}, nil
	case verdict.Mode == domain.ToolModeAllow:
		v.decision, v.status = domain.ToolDecisionAllow, domain.ToolCallOK
		if err := s.audit(ctx, c, v); err != nil {
			return NativeToolDecision{}, err
		}
		return NativeToolDecision{Decision: NativeAllow, Policy: verdict.Source}, nil
	}
	return s.nativeAsk(ctx, c, req, name, sha, size, canon, v, verdict)
}

// verdictLabel diz de onde veio o veredito: a política que decidiu ou, sem política, o padrão de perguntar antes.
func verdictLabel(v domain.ToolVerdict) string {
	if v.Policy == nil {
		return "padrão: pergunta antes"
	}
	return policyLabel(*v.Policy)
}

func (s *Service) nativeAsk(ctx context.Context, c ToolCaller, req NativeToolRequest, name, sha string, size int, canon []byte,
	v invocation, verdict domain.ToolVerdict) (NativeToolDecision, error) {
	var out NativeToolDecision
	err := s.withTx(ctx, func(t *txc) error {
		out = NativeToolDecision{Policy: verdict.Source}
		if err := t.q.LockToolCallKey(ctx, "tool_native:"+c.PersonaID.String()+":"+name+":"+sha); err != nil {
			return fmt.Errorf("lock native tool call: %w", err)
		}
		rows, err := t.q.FindNativeToolApprovals(ctx, store.FindNativeToolApprovalsParams{CompanyID: c.CompanyID, NativeTool: name,
			ArgsSha256: sha, PersonaID: c.PersonaID, Since: t.now.Add(-toolApprovalWindow)})
		if err != nil {
			return fmt.Errorf("find native approvals: %w", err)
		}
		if len(rows) > 0 {
			last := rows[0]
			gid := last.GateID
			switch domain.GateStatus(last.GateStatus) {
			case domain.GateApproved:
				if last.ConsumedAt == nil {
					n, err := t.q.ConsumeToolCallApproval(ctx, store.ConsumeToolCallApprovalParams{ID: last.ID, Now: &t.now})
					if err != nil {
						return fmt.Errorf("consume native approval: %w", err)
					}
					if n == 1 {
						v.decision, v.status, v.gateID = domain.ToolDecisionAllow, domain.ToolCallOK, &gid
						out.Decision, out.GateID = NativeAllow, &gid
						_, err := t.q.InsertToolInvocation(ctx, c.invocationParams(v, t.now))
						return err
					}
				}
			case domain.GatePending:
				v.decision, v.status, v.gateID = domain.ToolDecisionAsk, domain.ToolCallPending, &gid
				out.Decision, out.GateID = NativeAsk, &gid
				out.Reason = fmt.Sprintf("aguardando aprovação: gate %s", gid)
				_, err := t.q.InsertToolInvocation(ctx, c.invocationParams(v, t.now))
				return err
			case domain.GateRejected, domain.GateChangesRequested:
				v.gateID, v.errText = &gid, "recusada no gate"
				out.Decision, out.GateID = NativeDeny, &gid
				out.Reason = fmt.Sprintf("esta chamada foi recusada no gate %s", gid)
				_, err := t.q.InsertToolInvocation(ctx, c.invocationParams(v, t.now))
				return err
			}
		}
		gid, err := t.openNativeGate(c, req, name, sha, size, canon, verdict)
		if err != nil {
			return err
		}
		v.decision, v.status, v.gateID = domain.ToolDecisionAsk, domain.ToolCallPending, &gid
		out.Decision, out.GateID = NativeAsk, &gid
		out.Reason = fmt.Sprintf("aguardando aprovação: a chamada abriu o gate %s", gid)
		_, err = t.q.InsertToolInvocation(ctx, c.invocationParams(v, t.now))
		return err
	})
	return out, err
}

func (t *txc) openNativeGate(c ToolCaller, req NativeToolRequest, name, sha string, size int, canon []byte, verdict domain.ToolVerdict) (domain.ID, error) {
	what := "a tool nativa"
	switch {
	case req.Hanko:
		what = "a tool do Hanko"
	case req.Plugin:
		what = "a tool de plugin"
	}
	repeat := req.Plugin || req.Hanko // a chamada volta com erro e a persona a repete depois da decisão
	var b strings.Builder
	fmt.Fprintf(&b, "A persona **%s** quer usar %s `%s` e a política (%s) pede aprovação antes.\n\n", c.Persona, what, name, verdictLabel(verdict))
	if req.Title != "" {
		fmt.Fprintf(&b, "- Pedido: %s\n", truncateRunes(adapter.Redact(req.Title), 300))
	}
	if c.RunName != "" {
		fmt.Fprintf(&b, "- Execução: %s\n", c.RunName)
	}
	if c.TaskKey != "" {
		fmt.Fprintf(&b, "- Task: %s\n", c.TaskKey)
	}
	fmt.Fprintf(&b, "- sha256 da chamada: `%s` (%d bytes de entrada)\n\n", sha, size)
	if repeat {
		b.WriteString("Aprovar libera **uma** repetição desta chamada com exatamente os mesmos argumentos.\n")
	} else {
		b.WriteString("A execução da persona está parada esperando esta decisão e segue se você aprovar. Rejeitar (ou deixar o prazo passar) nega a chamada.\n")
	}
	b.WriteString("Um administrador pode escolher **sempre permitir**: grava uma política desta persona para esta tool e as próximas chamadas passam direto.\n\n")
	preview := adapter.Redact(string(canon))
	if len(preview) > toolGateArgsPreview {
		preview = toolClipBytes(preview, toolGateArgsPreview) + "\n… (cortado)"
	}
	b.WriteString("## Entrada\n\n")
	b.WriteString(codeBlock("json", preview))
	title := truncateRunes(fmt.Sprintf("Tool %s: aprovar a chamada", name), 300)
	g, err := t.createGate(domain.PersonaActor(*c.PersonaID), c.CompanyID, RequestGateInput{Kind: domain.GateTool, Title: title, Body: b.String(), RunID: c.RunID}, c.PersonaID)
	if err != nil {
		return domain.ID{}, err
	}
	ttl := nativeGateTTL
	if repeat {
		ttl = toolGateTTL
	}
	if err := t.q.SetGateExpiry(t.ctx, store.SetGateExpiryParams{CompanyID: c.CompanyID, ID: g.ID, ExpiresAt: ptr(t.now.Add(ttl))}); err != nil {
		return domain.ID{}, fmt.Errorf("set gate expiry: %w", err)
	}
	if _, err := t.q.CreateToolCallApproval(t.ctx, store.CreateToolCallApprovalParams{ID: domain.NewID(), CompanyID: c.CompanyID, GateID: g.ID,
		NativeTool: name, Tool: name, ArgsSha256: sha, RunID: c.RunID, TaskID: c.TaskID, PersonaID: c.PersonaID, Now: t.now}); err != nil {
		return domain.ID{}, mapErr(err, "create native tool approval")
	}
	return g.ID, nil
}

// NativeGateState é o estado de um gate de chamada nativa visto por quem espera.
type NativeGateState struct {
	State  string // pending | allow | deny
	Reason string
}

// NativeToolGateState diz se o gate de uma chamada nativa foi decidido. Aprovado: consome a aprovação (uso único) e
// libera. Rejeitado, com mudanças pedidas ou vencido: nega. Só o gate aberto para a persona do chamador responde.
func (s *Service) NativeToolGateState(ctx context.Context, c ToolCaller, gateID domain.ID) (NativeGateState, error) {
	q := s.read()
	a, err := q.GetToolCallApprovalByGate(ctx, store.GetToolCallApprovalByGateParams{CompanyID: c.CompanyID, GateID: gateID})
	if err != nil {
		return NativeGateState{}, mapErr(err, "get tool approval")
	}
	if a.NativeTool == "" || a.PersonaID == nil || c.PersonaID == nil || *a.PersonaID != *c.PersonaID {
		return NativeGateState{}, notFound("gate")
	}
	g, err := q.GetGateByID(ctx, store.GetGateByIDParams{CompanyID: c.CompanyID, ID: gateID})
	if err != nil {
		return NativeGateState{}, mapErr(err, "get gate")
	}
	now := s.Now().UTC()
	v := invocation{tool: a.NativeTool, native: true, sha: a.ArgsSha256, gateID: &gateID, decision: domain.ToolDecisionDeny, status: domain.ToolCallBlocked}
	switch domain.GateStatus(g.Status) {
	case domain.GatePending:
		if g.ExpiresAt != nil && !now.Before(*g.ExpiresAt) {
			return NativeGateState{State: "deny", Reason: "o prazo do gate esgotou sem decisão"}, nil
		}
		return NativeGateState{State: "pending"}, nil
	case domain.GateApproved:
		if a.ConsumedAt == nil {
			if _, err := q.ConsumeToolCallApproval(ctx, store.ConsumeToolCallApprovalParams{ID: a.ID, Now: &now}); err != nil {
				return NativeGateState{}, fmt.Errorf("consume native approval: %w", err)
			}
			v.decision, v.status = domain.ToolDecisionAllow, domain.ToolCallOK
			if err := s.audit(ctx, c, v); err != nil {
				return NativeGateState{}, err
			}
		}
		return NativeGateState{State: "allow"}, nil
	}
	reason := "a chamada foi recusada no gate"
	if domain.GateStatus(g.Status) == domain.GateExpired {
		reason = "o prazo do gate esgotou sem decisão"
	}
	v.errText = reason
	if err := s.audit(ctx, c, v); err != nil {
		return NativeGateState{}, err
	}
	return NativeGateState{State: "deny", Reason: reason}, nil
}

// nativeGatePoll é o intervalo com que AwaitNativeToolGate consulta o gate (variável para os testes).
var nativeGatePoll = 2 * time.Second

// AwaitNativeToolGate espera a decisão do gate (ou o prazo/cancelamento) e devolve a decisão final. É o que o guarda
// do ACP usa dentro da execução, que fica parada na resposta do permission request.
func (s *Service) AwaitNativeToolGate(ctx context.Context, c ToolCaller, gateID domain.ID) (NativeToolDecision, error) {
	ctx, end := toolSpan(ctx, "service.await_native_tool_gate")
	defer end()
	tick := time.NewTicker(nativeGatePoll)
	defer tick.Stop()
	for {
		st, err := s.NativeToolGateState(ctx, c, gateID)
		if err != nil {
			if ctx.Err() != nil {
				return NativeToolDecision{Decision: NativeDeny, GateID: &gateID, Reason: "execução interrompida antes da decisão"}, nil
			}
			return NativeToolDecision{}, err
		}
		switch st.State {
		case "allow":
			return NativeToolDecision{Decision: NativeAllow, GateID: &gateID}, nil
		case "deny":
			return NativeToolDecision{Decision: NativeDeny, GateID: &gateID, Reason: st.Reason}, nil
		}
		select {
		case <-ctx.Done():
			return NativeToolDecision{Decision: NativeDeny, GateID: &gateID, Reason: "execução interrompida antes da decisão"}, nil
		case <-tick.C:
		}
	}
}

// ExpireToolGates vira expired os gates tool pendentes cujo prazo passou (varredura periódica; ADR-0053). A próxima
// chamada igual abre um gate novo.
func (s *Service) ExpireToolGates(ctx context.Context) (int, error) {
	ctx, end := toolSpan(ctx, "service.expire_tool_gates")
	defer end()
	n := 0
	err := s.withTx(ctx, func(t *txc) error {
		rows, err := t.q.ExpireDueGates(ctx, &t.now)
		if err != nil {
			return fmt.Errorf("expire due gates: %w", err)
		}
		n = len(rows)
		for _, r := range rows {
			g := gateFromRow(r)
			if err := t.activity(domain.SystemActor, g.CompanyID, "gate.expired", "gate", g.ID, map[string]any{"kind": g.Kind, "title": g.Title,
				"persona_id": g.PersonaID}, ""); err != nil {
				return err
			}
			t.publish(companyTopic(g.CompanyID), "gate.updated", g)
		}
		return nil
	})
	return n, err
}

// ---------- testar como a persona X ----------

// ToolTestInput pede o teste de uma tool como uma persona.
type ToolTestInput struct {
	IntegrationID domain.ID
	Tool          string
	PersonaID     domain.ID
	Arguments     json.RawMessage
	// Execute: chama de verdade (passa pelo gate, pelo teto de taxa e pela auditoria como origem "test"). Sem ele é só
	// a simulação da decisão.
	Execute bool
}

// ToolTestResult é o que a persona veria.
type ToolTestResult struct {
	Persona     string
	Integration string
	Tool        string
	Verdict     domain.ToolVerdict
	Status      domain.ToolStatus
	Risk        domain.ToolRisk
	Decision    domain.ToolDecision
	Message     string
	GateID      *domain.ID
	Executed    bool
	IsError     bool
	// Output é o texto do resultado, sem segredos (vazio na simulação).
	Output    string
	LatencyMs int
}

// TestToolCall faz uma chamada de teste a uma tool "como a persona X" (admin): usa o perfil, as políticas e o catálogo
// dela, e só repassa ao servidor com Execute. Fica na auditoria com origem "test" e o admin como ator.
func (s *Service) TestToolCall(ctx context.Context, actor domain.Actor, companyID domain.ID, in ToolTestInput) (ToolTestResult, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return ToolTestResult{}, err
	}
	ctx, end := toolSpan(ctx, "service.test_tool_call", attribute.Bool("execute", in.Execute))
	defer end()
	q := s.read()
	persona, err := q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: in.PersonaID})
	if err != nil {
		return ToolTestResult{}, mapErr(err, "get persona")
	}
	integ, err := q.GetIntegrationByID(ctx, store.GetIntegrationByIDParams{CompanyID: companyID, ID: in.IntegrationID})
	if err != nil {
		return ToolTestResult{}, mapErr(err, "get integration")
	}
	if domain.IntegrationKind(integ.Kind) != domain.IntegrationMCP || parseIntegrationConfig(integ.Config).Transport == domain.TransportStdio {
		return ToolTestResult{}, domain.Invalid("integration", "só servidores MCP http passam pelo gateway de tools")
	}
	tool := strings.TrimSpace(in.Tool)
	if !toolNameRe.MatchString(tool) {
		return ToolTestResult{}, domain.Invalid("tool", "nome de tool inválido")
	}
	pid := persona.ID
	a := actor
	c := ToolCaller{CompanyID: companyID, PersonaID: &pid, Persona: persona.Name, Source: domain.SourceTest, Actor: &a, DryRun: !in.Execute}
	out := ToolTestResult{Persona: persona.Name, Integration: integ.Slug, Tool: tool, Executed: in.Execute}

	// o que a persona vê: o veredito (perfil + política) e o estado no catálogo
	gis, err := s.callerIntegrations(ctx, q, c, integ.Slug)
	if err != nil {
		return ToolTestResult{}, err
	}
	if len(gis) == 0 {
		out.Decision = domain.ToolDecisionDeny
		out.Message = fmt.Sprintf("a persona %s não tem acesso à integração %s (ligue-a na persona, ou ela está desligada)", persona.Name, integ.Slug)
		out.Executed = false
		return out, nil
	}
	if tr, err := q.GetIntegrationToolByName(ctx, store.GetIntegrationToolByNameParams{CompanyID: companyID, IntegrationID: integ.ID, Name: tool}); err == nil {
		out.Status, out.Risk = domain.ToolStatus(tr.Status), domain.ToolRisk(tr.Risk)
		pols, err := loadToolPolicies(ctx, q, companyID, &pid)
		if err != nil {
			return ToolTestResult{}, err
		}
		out.Verdict = toolVerdict(gis[0], pols, c, tool, out.Risk)
	}
	args := in.Arguments
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	res, err := s.GatewayCallTool(ctx, c, integ.Slug, tool, args)
	if err != nil {
		return ToolTestResult{}, err
	}
	out.Decision, out.Message, out.GateID = res.Decision, res.Message, res.GateID
	if res.Decision == "" {
		out.Decision = domain.ToolDecisionDeny
	}
	if res.Unknown && out.Message == "" {
		out.Message = "tool não disponível para a persona"
	}
	if res.Result != nil {
		out.Output, out.IsError = s.testOutput(ctx, integ, res.Result)
	}
	return out, nil
}

func (s *Service) testOutput(ctx context.Context, integ store.Integration, raw json.RawMessage) (string, bool) {
	var res struct {
		IsError bool `json:"isError"`
	}
	_ = json.Unmarshal(raw, &res)
	text := s.redactedResultPreview(ctx, integ, raw)
	return text, res.IsError
}

// ---------- aviso na Caixa de entrada ----------

// QuarantinedToolItem é uma tool que espera a revisão de um admin (item da Caixa de entrada).
type QuarantinedToolItem struct {
	Tool            domain.IntegrationTool
	IntegrationName string
}

// quarantinedTools lista as tools em quarentena (só para quem administra a empresa).
func (s *Service) quarantinedTools(ctx context.Context, companyID domain.ID) ([]QuarantinedToolItem, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return nil, nil //nolint:nilerr // quem não administra não vê o aviso; não é erro
	}
	rows, err := s.read().ListQuarantinedTools(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list quarantined tools: %w", err)
	}
	out := make([]QuarantinedToolItem, len(rows))
	for i, r := range rows {
		out[i] = QuarantinedToolItem{IntegrationName: r.IntegrationName, Tool: toolFromRow(store.IntegrationTool{ID: r.ID, CompanyID: r.CompanyID,
			IntegrationID: r.IntegrationID, Name: r.Name, Title: r.Title, Description: r.Description, InputSchema: r.InputSchema,
			Annotations: r.Annotations, SchemaSha256: r.SchemaSha256, Risk: r.Risk, Status: r.Status, FirstSeenAt: r.FirstSeenAt,
			LastSeenAt: r.LastSeenAt, ReviewedByType: r.ReviewedByType, ReviewedByID: r.ReviewedByID, ReviewedAt: r.ReviewedAt,
			UpdatedAt: r.UpdatedAt}, r.IntegrationSlug)}
	}
	return out, nil
}

// toolActor monta o ator da auditoria (nil quando a linha é anterior ao ADR-0053).
func toolActor(typ *string, id *domain.ID) *domain.Actor {
	if typ == nil {
		return nil
	}
	a := actorOf(*typ, id)
	return &a
}
