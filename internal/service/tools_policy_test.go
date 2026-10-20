package service_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Motor de política de tools (ADR-0053): persona x tool com padrão da empresa, "sempre permitir", tools nativas do
// harness, prazo do gate, teste como a persona X e aviso de quarentena na Caixa.

func (e *toolsEnv) policy(t *testing.T, in service.ToolPolicyInput) domain.ToolPolicy {
	t.Helper()
	p, err := e.svc.SetToolPolicy(context.Background(), domain.HumanActor, e.co.ID, in)
	if err != nil {
		t.Fatalf("set policy %+v: %v", in, err)
	}
	return p
}

func (e *toolsEnv) toolPolicy(persona *domain.Persona, target domain.ToolPolicyTarget, integ *domain.ID, tool string, mode domain.ToolPolicyMode) service.ToolPolicyInput {
	in := service.ToolPolicyInput{Scope: domain.PolicyScopeCompany, Target: target, IntegrationID: integ, ToolName: tool, Mode: mode}
	if persona != nil {
		in.Scope, in.PersonaID = domain.PolicyScopePersona, &persona.ID
	}
	return in
}

// Demo do M9: a persona tenta uma tool em ask; um admin aprova "sempre"; a segunda chamada passa direto; tudo no log.
func TestPolicyAskThenAlwaysAllowDemo(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	e.refresh(t)
	ana := e.persona(t, "Ana", nil)
	iid := e.integ.ID
	e.policy(t, e.toolPolicy(&ana, domain.PolicyTargetTool, &iid, "apagar", domain.PolicyAsk))
	c, _ := toolRun(t, e, ana)
	args := json.RawMessage(`{"x":1}`)

	// a política não esconde a tool: ela aparece, mas pede aprovação
	list, err := e.svc.GatewayListTools(ctx, c, "docs")
	if err != nil || !strings.Contains(names(list), "apagar") {
		t.Fatalf("list: %s %v", names(list), err)
	}
	res, err := e.svc.GatewayCallTool(ctx, c, "docs", "apagar", args)
	if err != nil || res.Decision != domain.ToolDecisionAsk || res.GateID == nil {
		t.Fatalf("ask: %+v %v", res, err)
	}
	gate := *res.GateID
	var expires *time.Time
	if err := e.svc.Pool.QueryRow(ctx, `SELECT expires_at FROM gates WHERE id=$1`, gate).Scan(&expires); err != nil || expires == nil {
		t.Fatalf("o gate tool deve ter prazo: %v %v", expires, err)
	}
	if len(e.up.callLog()) != 0 {
		t.Fatal("ask chamou o servidor")
	}

	// membro não grava política permanente: sempre permitir é de admin, e o gate segue pendente
	member := mkUser(t, e.env, "membro@x.com", domain.RoleMember)
	join(t, e.env, member, domain.MemberMember)
	_, err = e.svc.DecideGate(as(member), domain.HumanActor, e.co.ID, gate, service.DecideGateInput{Decision: domain.GateApproved, Always: true})
	wantErr(t, err, domain.ErrForbidden)
	// sempre permitir só vale ao aprovar e só em gate tool
	_, err = e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, gate, service.DecideGateInput{Decision: domain.GateRejected, Always: true})
	wantErr(t, err, domain.ErrInvalid)
	if g, _ := e.svc.GetGate(ctx, e.co.ID, gate); g.Status != domain.GatePending {
		t.Fatalf("gate deveria seguir pendente: %s", g.Status)
	}

	if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, gate, service.DecideGateInput{Decision: domain.GateApproved, Always: true}); err != nil {
		t.Fatal(err)
	}
	pols, err := e.svc.ListToolPolicies(ctx, e.co.ID, &ana.ID)
	if err != nil {
		t.Fatal(err)
	}
	var always *domain.ToolPolicy
	for i := range pols {
		if pols[i].Origin == domain.PolicyOriginAlways {
			always = &pols[i]
		}
	}
	if always == nil || always.Mode != domain.PolicyAllow || always.Target != domain.PolicyTargetTool || always.ToolName != "apagar" ||
		always.PersonaID == nil || *always.PersonaID != ana.ID {
		t.Fatalf("política do sempre permitir: %+v", pols)
	}
	if len(pols) != 1 {
		t.Fatalf("o sempre permitir troca a política de ask (mesma chave): %+v", pols)
	}

	// a primeira repetição consome a aprovação; a segunda e a terceira passam pela política, sem gate
	res, _ = e.svc.GatewayCallTool(ctx, c, "docs", "apagar", args)
	if res.Decision != domain.ToolDecisionAllow || !strings.Contains(string(res.Result), "ok apagar") {
		t.Fatalf("depois de aprovar: %+v", res)
	}
	for _, a := range []string{`{"x":1}`, `{"x":99}`} {
		res, _ = e.svc.GatewayCallTool(ctx, c, "docs", "apagar", json.RawMessage(a))
		if res.Decision != domain.ToolDecisionAllow || res.GateID != nil {
			t.Fatalf("chamada %s deveria passar direto: %+v", a, res)
		}
	}
	var gates int
	_ = e.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM gates WHERE kind='tool'`).Scan(&gates)
	if gates != 1 {
		t.Fatalf("só um gate no total, vieram %d", gates)
	}
	if n := len(e.up.callLog()); n != 3 {
		t.Fatalf("o servidor recebeu %d chamadas, queria 3", n)
	}

	// o log da conexão: ator, origem, duração, resultado redigido e de onde veio a decisão
	inv, err := e.svc.ListToolInvocations(ctx, e.co.ID, service.ToolInvocationFilter{Tool: "apagar"})
	if err != nil || len(inv) != 4 {
		t.Fatalf("auditoria: %d %v", len(inv), err)
	}
	last := inv[0] // do mais novo ao mais velho
	if last.Decision != domain.ToolDecisionAllow || last.PolicySource != "policy:persona" || last.Source != domain.SourceRun ||
		last.Actor == nil || last.Actor.Type != domain.ActorPersona || last.Actor.ID == nil || *last.Actor.ID != ana.ID ||
		last.ResultPreview != "ok apagar" || last.LatencyMs < 0 || last.Status != domain.ToolCallOK {
		t.Fatalf("última chamada: %+v", last)
	}
	first := inv[len(inv)-1]
	if first.Decision != domain.ToolDecisionAsk || first.Status != domain.ToolCallPending || first.PolicySource != "policy:persona" {
		t.Fatalf("primeira chamada: %+v", first)
	}
	// a decisão do gate e a política gravada ficam na atividade
	var n int
	_ = e.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM activity_events WHERE verb IN ('tool_policy.set','tool.call_decided')`).Scan(&n)
	if n < 3 {
		t.Fatalf("atividade: %d eventos", n)
	}
}

func TestPolicyPrecedenceCompanyPersonaAndProfile(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	e.refresh(t)
	ana := e.persona(t, "Ana", nil)
	bia := e.persona(t, "Bia", nil)
	iid := e.integ.ID

	// padrão da empresa: tudo desligado; exceção da Ana: só ler e escrever
	e.policy(t, e.toolPolicy(nil, domain.PolicyTargetAll, nil, "", domain.PolicyOff))
	e.policy(t, e.toolPolicy(&ana, domain.PolicyTargetTool, &iid, "ler", domain.PolicyAllow))
	e.policy(t, e.toolPolicy(&ana, domain.PolicyTargetTool, &iid, "escrever", domain.PolicyAsk))
	ca, _ := toolRun(t, e, ana)
	cb, _ := toolRun(t, e, bia)
	la, _ := e.svc.GatewayListTools(ctx, ca, "docs")
	lb, _ := e.svc.GatewayListTools(ctx, cb, "docs")
	if names(la) != "escrever,ler" || names(lb) != "" {
		t.Fatalf("Ana=%q Bia=%q", names(la), names(lb))
	}
	// a Bia não chama nada; a tool desligada se comporta como desconhecida
	res, _ := e.svc.GatewayCallTool(ctx, cb, "docs", "ler", nil)
	if !res.Unknown || res.Decision != domain.ToolDecisionDeny {
		t.Fatalf("Bia: %+v", res)
	}
	// integração inteira da Bia em ask vence o curinga da empresa
	e.policy(t, e.toolPolicy(&bia, domain.PolicyTargetIntegration, &iid, "", domain.PolicyAsk))
	lb, _ = e.svc.GatewayListTools(ctx, cb, "docs")
	if names(lb) != "apagar,escrever,ler,misterio" {
		t.Fatalf("Bia com a integração em ask: %q", names(lb))
	}
	// deny vence: a tool da Bia desligada vence a integração dela em ask
	e.policy(t, e.toolPolicy(&bia, domain.PolicyTargetTool, &iid, "apagar", domain.PolicyOff))
	lb, _ = e.svc.GatewayListTools(ctx, cb, "docs")
	if names(lb) != "escrever,ler,misterio" {
		t.Fatalf("Bia sem apagar: %q", names(lb))
	}

	// a política vence o perfil (que exclui a tool) para a persona; em quarentena, nada abre
	prof, _ := e.svc.CreateToolProfile(ctx, domain.HumanActor, e.co.ID, service.CreateToolProfileInput{Name: "Fechado"})
	if _, err := e.svc.BindToolProfile(ctx, domain.HumanActor, e.co.ID, prof.ID, service.ToolBindingInput{Scope: domain.ToolScopeCompany}); err != nil {
		t.Fatal(err)
	}
	la, _ = e.svc.GatewayListTools(ctx, ca, "docs")
	if names(la) != "escrever,ler" {
		t.Fatalf("perfil fechado + política da Ana: %q", names(la))
	}
	e.up.add("nova", &sdk.ToolAnnotations{DestructiveHint: bptr(true)})
	e.refresh(t)
	e.policy(t, e.toolPolicy(&ana, domain.PolicyTargetTool, &iid, "nova", domain.PolicyAllow))
	res, _ = e.svc.GatewayCallTool(ctx, ca, "docs", "nova", nil)
	if res.Decision != domain.ToolDecisionQuarantined {
		t.Fatalf("a política não abre a quarentena: %+v", res)
	}

	// efetivas mostram a origem
	eff, err := e.svc.EffectiveToolsFor(ctx, e.co.ID, ana.ID, nil)
	if err != nil || len(eff.Policies) != 4 {
		t.Fatalf("efetivas: %d políticas %v", len(eff.Policies), err)
	}
	for _, ei := range eff.Integrations {
		for _, tl := range ei.Tools {
			if tl.Tool.Name == "ler" && (tl.Verdict.Source != "policy:persona" || tl.Verdict.Policy == nil) {
				t.Fatalf("ler: %+v", tl.Verdict)
			}
		}
	}

	// apagar uma política devolve a decisão ao perfil
	for _, p := range e.listPolicies(t, &ana) {
		if err := e.svc.DeleteToolPolicy(ctx, domain.HumanActor, e.co.ID, p.ID); err != nil {
			t.Fatal(err)
		}
	}
	e.svc.Pool.Exec(ctx, `DELETE FROM tool_policies`) //nolint:errcheck // limpeza do teste
	la, _ = e.svc.GatewayListTools(ctx, ca, "docs")
	if names(la) != "" {
		t.Fatalf("sem políticas vale o perfil fechado: %q", names(la))
	}
}

func (e *toolsEnv) listPolicies(t *testing.T, p *domain.Persona) []domain.ToolPolicy {
	t.Helper()
	var id *domain.ID
	if p != nil {
		id = &p.ID
	}
	ps, err := e.svc.ListToolPolicies(context.Background(), e.co.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	return ps
}

func TestPolicyValidationAndRoles(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	e.refresh(t)
	ana := e.persona(t, "Ana", nil)
	iid := e.integ.ID
	bad := []service.ToolPolicyInput{
		{Scope: "x", Target: domain.PolicyTargetAll, Mode: domain.PolicyOff},
		{Scope: domain.PolicyScopeCompany, Target: "x", Mode: domain.PolicyOff},
		{Scope: domain.PolicyScopeCompany, Target: domain.PolicyTargetAll, Mode: "talvez"},
		{Scope: domain.PolicyScopePersona, Target: domain.PolicyTargetAll, Mode: domain.PolicyOff},                                                 // sem persona
		{Scope: domain.PolicyScopeCompany, PersonaID: &ana.ID, Target: domain.PolicyTargetAll, Mode: domain.PolicyOff},                             // empresa com persona
		{Scope: domain.PolicyScopeCompany, Target: domain.PolicyTargetAll, ToolName: "x", Mode: domain.PolicyOff},                                  // curinga com tool
		{Scope: domain.PolicyScopeCompany, Target: domain.PolicyTargetTool, IntegrationID: &iid, Mode: domain.PolicyOff},                           // tool sem nome
		{Scope: domain.PolicyScopeCompany, Target: domain.PolicyTargetTool, ToolName: "ler", Mode: domain.PolicyOff},                               // tool sem integração
		{Scope: domain.PolicyScopeCompany, Target: domain.PolicyTargetNative, ToolName: "Ba sh", Mode: domain.PolicyOff},                           // nome inválido
		{Scope: domain.PolicyScopeCompany, Target: domain.PolicyTargetNative, ToolName: "Bash", IntegrationID: &iid, Mode: domain.PolicyOff},       // nativa com integração
		{Scope: domain.PolicyScopeCompany, Target: domain.PolicyTargetIntegration, IntegrationID: ptr(domain.NewID()), Mode: domain.PolicyOff},     // integração de fora
		{Scope: domain.PolicyScopePersona, PersonaID: ptr(domain.NewID()), Target: domain.PolicyTargetAll, Mode: domain.PolicyOff},                 // persona de fora
		{Scope: domain.PolicyScopeCompany, Target: domain.PolicyTargetIntegration, IntegrationID: &iid, ToolName: "ler", Mode: domain.PolicyOff},   // integração com tool
		{Scope: domain.PolicyScopeCompany, Target: domain.PolicyTargetTool, IntegrationID: &iid, ToolName: "tem espaço", Mode: domain.PolicyAllow}, // nome inválido
	}
	for i, in := range bad {
		if _, err := e.svc.SetToolPolicy(ctx, domain.HumanActor, e.co.ID, in); err == nil {
			t.Errorf("caso %d deveria falhar: %+v", i, in)
		}
	}
	// gravar a mesma chave duas vezes mantém uma linha só
	a := e.policy(t, e.toolPolicy(&ana, domain.PolicyTargetNative, nil, "Bash", domain.PolicyAsk))
	b := e.policy(t, e.toolPolicy(&ana, domain.PolicyTargetNative, nil, "Bash", domain.PolicyAllow))
	if a.ID != b.ID && len(e.listPolicies(t, &ana)) != 1 {
		t.Fatalf("a chave é única: %+v", e.listPolicies(t, &ana))
	}
	if ps := e.listPolicies(t, nil); len(ps) != 1 || ps[0].Mode != domain.PolicyAllow {
		t.Fatalf("lista: %+v", ps)
	}

	// papéis: viewer lê, só admin grava e apaga
	users := map[domain.MemberRole]domain.User{}
	for _, r := range []domain.MemberRole{domain.MemberViewer, domain.MemberMember, domain.MemberAdmin} {
		u := mkUser(t, e.env, string(r)+"@x.com", domain.RoleMember)
		join(t, e.env, u, r)
		users[r] = u
	}
	for role, u := range users {
		_, err := e.svc.ListToolPolicies(as(u), e.co.ID, nil)
		if err != nil {
			t.Errorf("listar como %s: %v", role, err)
		}
		_, err = e.svc.SetToolPolicy(as(u), domain.HumanActor, e.co.ID, e.toolPolicy(nil, domain.PolicyTargetAll, nil, "", domain.PolicyAsk))
		if role == domain.MemberAdmin && err != nil {
			t.Errorf("gravar como admin: %v", err)
		}
		if role != domain.MemberAdmin && err != domain.ErrForbidden {
			t.Errorf("gravar como %s: want ErrForbidden, got %v", role, err)
		}
		_, err = e.svc.TestToolCall(as(u), domain.HumanActor, e.co.ID, service.ToolTestInput{IntegrationID: iid, Tool: "ler", PersonaID: ana.ID})
		if role != domain.MemberAdmin && err != domain.ErrForbidden {
			t.Errorf("testar como %s: want ErrForbidden, got %v", role, err)
		}
	}
	// apagar
	for _, p := range e.listPolicies(t, nil) {
		if err := e.svc.DeleteToolPolicy(as(users[domain.MemberMember]), domain.HumanActor, e.co.ID, p.ID); err != domain.ErrForbidden {
			t.Fatalf("apagar como membro: %v", err)
		}
		if err := e.svc.DeleteToolPolicy(ctx, domain.HumanActor, e.co.ID, p.ID); err != nil {
			t.Fatal(err)
		}
		wantErr(t, e.svc.DeleteToolPolicy(ctx, domain.HumanActor, e.co.ID, p.ID), domain.ErrNotFound)
	}
}

func TestNamedGatewayPoliciesOnlyTighten(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	e.refresh(t)
	iid := e.integ.ID
	// o perfil do gateway: tudo permitido, apagar em ask, misterio excluída
	prof, _ := e.svc.CreateToolProfile(ctx, domain.HumanActor, e.co.ID, service.CreateToolProfileInput{Name: "IDE", DefaultAction: domain.ToolDefaultAllow})
	for _, r := range []service.ToolRuleInput{
		{Target: domain.ToolTargetTool, IntegrationID: &iid, ToolName: "apagar", Effect: domain.ToolInclude, Mode: domain.ToolModeAsk},
		{Target: domain.ToolTargetTool, IntegrationID: &iid, ToolName: "misterio", Effect: domain.ToolExclude},
	} {
		if _, err := e.svc.AddToolProfileRule(ctx, domain.HumanActor, e.co.ID, prof.ID, r); err != nil {
			t.Fatal(err)
		}
	}
	gw, err := e.svc.CreateToolGateway(ctx, domain.HumanActor, e.co.ID, service.CreateToolGatewayInput{Name: "IDE", ProfileID: prof.ID})
	if err != nil {
		t.Fatal(err)
	}
	_, tok, err := e.svc.CreateGatewayToken(ctx, domain.HumanActor, e.co.ID, gw.ID, service.CreateGatewayTokenInput{})
	if err != nil {
		t.Fatal(err)
	}
	// políticas da empresa: "permitir tudo" e "ler desligada". O permitir não abre o perfil; o desligar aperta.
	e.policy(t, e.toolPolicy(nil, domain.PolicyTargetAll, nil, "", domain.PolicyAllow))
	e.policy(t, e.toolPolicy(nil, domain.PolicyTargetTool, &iid, "ler", domain.PolicyOff))
	c, err := e.svc.GatewayCaller(ctx, e.co.Slug, gw.Slug, tok)
	if err != nil {
		t.Fatal(err)
	}
	list, err := e.svc.GatewayListTools(ctx, c, "")
	if err != nil || names(list) != "docs__apagar,docs__escrever" {
		t.Fatalf("gateway nomeado: %s %v", names(list), err)
	}
	res, _ := e.svc.GatewayCallTool(ctx, c, "", "docs__apagar", nil)
	if res.Decision != domain.ToolDecisionAsk {
		t.Fatalf("o permitir da empresa não relaxa o ask do perfil: %+v", res)
	}
	// o gateway nomeado pergunta em nome do sistema (sem persona) e a política da empresa que manda perguntar vale
	e.policy(t, e.toolPolicy(nil, domain.PolicyTargetTool, &iid, "escrever", domain.PolicyAsk))
	res, _ = e.svc.GatewayCallTool(ctx, c, "", "docs__escrever", nil)
	if res.Decision != domain.ToolDecisionAsk || res.GateID == nil {
		t.Fatalf("escrever em ask pela política: %+v", res)
	}
	inv, _ := e.svc.ListToolInvocations(ctx, e.co.ID, service.ToolInvocationFilter{Source: domain.SourceGateway})
	if len(inv) == 0 || inv[0].Actor == nil || inv[0].Actor.Type != domain.ActorSystem {
		t.Fatalf("auditoria do gateway: %+v", inv)
	}
	// "sempre permitir" não vale para o gateway nomeado (não há persona)
	_, err = e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, *res.GateID, service.DecideGateInput{Decision: domain.GateApproved, Always: true})
	wantErr(t, err, domain.ErrInvalid)
}

func TestAggregateRunEndpointPrefixesTools(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	e.refresh(t)
	ana := e.persona(t, "Ana", nil)
	c, _ := toolRun(t, e, ana)
	c.Aggregate = true
	list, err := e.svc.GatewayListTools(ctx, c, "")
	if err != nil || names(list) != "docs__apagar,docs__escrever,docs__ler,docs__misterio" {
		t.Fatalf("agregado: %s %v", names(list), err)
	}
	res, err := e.svc.GatewayCallTool(ctx, c, "", "docs__ler", json.RawMessage(`{"x":1}`))
	if err != nil || res.Decision != domain.ToolDecisionAllow || !strings.Contains(string(res.Result), "ok ler") {
		t.Fatalf("ler: %+v %v", res, err)
	}
	res, _ = e.svc.GatewayCallTool(ctx, c, "", "ler", nil)
	if !res.Unknown {
		t.Fatalf("sem prefixo a tool não existe no endpoint único: %+v", res)
	}
	// a persona sem nenhuma integração enxerga uma lista vazia, não um erro
	outra := e.persona(t, "Outra", nil)
	if _, err := e.svc.Pool.Exec(ctx, `UPDATE integrations SET all_personas = false`); err != nil {
		t.Fatal(err)
	}
	c2, _ := toolRun(t, e, outra)
	c2.Aggregate = true
	list, err = e.svc.GatewayListTools(ctx, c2, "")
	if err != nil || len(list) != 0 {
		t.Fatalf("sem integração: %v %v", list, err)
	}
}

func TestNativeToolPolicyAskApproveAlways(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	ana := e.persona(t, "Ana", nil)
	c, _ := toolRun(t, e, ana)
	c.Source = domain.SourceHook
	cmd := json.RawMessage(`{"command":"rm -rf /tmp/x","token":"sk-ant-api03-ABCDEFGHIJKLMNOPQRSTUVWX"}`)

	// sem política: sem opinião e nada auditado
	d, err := e.svc.NativeToolCheck(ctx, c, service.NativeToolRequest{Name: "Bash", Input: cmd})
	if err != nil || d.Decision != service.NativeNone {
		t.Fatalf("sem política: %+v %v", d, err)
	}
	if ps, _ := e.svc.NativeToolPolicies(ctx, e.co.ID, ana.ID); ps {
		t.Fatal("não há política nativa ainda")
	}
	e.policy(t, e.toolPolicy(nil, domain.PolicyTargetNative, nil, "Bash", domain.PolicyAsk))
	if ps, _ := e.svc.NativeToolPolicies(ctx, e.co.ID, ana.ID); !ps {
		t.Fatal("agora há política nativa")
	}
	other := e.persona(t, "Bia", nil)
	if ps, _ := e.svc.NativeToolPolicies(ctx, e.co.ID, other.ID); !ps {
		t.Fatal("a política da empresa vale para todas as personas")
	}
	// outra tool nativa continua sem opinião
	d, _ = e.svc.NativeToolCheck(ctx, c, service.NativeToolRequest{Name: "Read", Input: nil})
	if d.Decision != service.NativeNone {
		t.Fatalf("Read: %+v", d)
	}

	d, err = e.svc.NativeToolCheck(ctx, c, service.NativeToolRequest{Name: "Bash", Input: cmd})
	if err != nil || d.Decision != service.NativeAsk || d.GateID == nil {
		t.Fatalf("ask: %+v %v", d, err)
	}
	gate := *d.GateID
	g, _ := e.svc.GetGate(ctx, e.co.ID, gate)
	if g.Kind != domain.GateTool || g.Status != domain.GatePending || g.PersonaID == nil || *g.PersonaID != ana.ID {
		t.Fatalf("gate: %+v", g)
	}
	if strings.Contains(g.Body, "ABCDEFGHIJKLMNOPQRSTUVWX") || !strings.Contains(g.Body, "rm -rf") {
		t.Fatalf("o corpo do gate mostra o comando e esconde a credencial: %s", g.Body)
	}
	var expires time.Time
	_ = e.svc.Pool.QueryRow(ctx, `SELECT expires_at FROM gates WHERE id=$1`, gate).Scan(&expires)
	if d := time.Until(expires); d < 5*time.Minute || d > 11*time.Minute {
		t.Fatalf("prazo do gate nativo: %v", d)
	}
	// a mesma chamada de novo reaproveita o gate; a persona não fica presa
	d2, _ := e.svc.NativeToolCheck(ctx, c, service.NativeToolRequest{Name: "Bash", Input: json.RawMessage(`{"token":"sk-ant-api03-ABCDEFGHIJKLMNOPQRSTUVWX","command":"rm -rf /tmp/x"}`)})
	if d2.Decision != service.NativeAsk || d2.GateID == nil || *d2.GateID != gate {
		t.Fatalf("repetição: %+v", d2)
	}
	st, err := e.svc.NativeToolGateState(ctx, c, gate)
	if err != nil || st.State != "pending" {
		t.Fatalf("estado: %+v %v", st, err)
	}
	// outra persona não enxerga o gate
	oc, _ := toolRun(t, e, other)
	_, err = e.svc.NativeToolGateState(ctx, oc, gate)
	wantErr(t, err, domain.ErrNotFound)

	wakes := e.jobs.wakeCount()
	if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, gate, service.DecideGateInput{Decision: domain.GateApproved, Always: true}); err != nil {
		t.Fatal(err)
	}
	if e.jobs.wakeCount() != wakes {
		t.Fatal("a chamada nativa espera dentro da run: não há o que acordar")
	}
	st, _ = e.svc.NativeToolGateState(ctx, c, gate)
	if st.State != "allow" {
		t.Fatalf("depois de aprovar: %+v", st)
	}
	// sempre permitir gravou a política da persona para a tool nativa; a seguinte passa direto
	d, _ = e.svc.NativeToolCheck(ctx, c, service.NativeToolRequest{Name: "Bash", Input: json.RawMessage(`{"command":"ls"}`)})
	if d.Decision != service.NativeAllow || d.GateID != nil || d.Policy != "policy:persona" {
		t.Fatalf("sempre permitir: %+v", d)
	}
	// a Bia segue com o padrão da empresa
	d, _ = e.svc.NativeToolCheck(ctx, oc, service.NativeToolRequest{Name: "Bash", Input: json.RawMessage(`{"command":"ls"}`)})
	if d.Decision != service.NativeAsk {
		t.Fatalf("Bia: %+v", d)
	}
	inv, _ := e.svc.ListToolInvocations(ctx, e.co.ID, service.ToolInvocationFilter{Source: domain.SourceHook})
	if len(inv) < 4 {
		t.Fatalf("auditoria dos hooks: %d", len(inv))
	}
	for _, i := range inv {
		if i.IntegrationSlug != "" || i.Tool != "Bash" || i.Actor == nil || i.Actor.Type != domain.ActorPersona {
			t.Fatalf("linha nativa: %+v", i)
		}
	}
	var dump string
	_ = e.svc.Pool.QueryRow(ctx, `SELECT coalesce(string_agg(t::text, ' '), '') FROM tool_invocations t`).Scan(&dump)
	if strings.Contains(dump, "ABCDEFGHIJKLMNOPQRSTUVWX") || strings.Contains(dump, "rm -rf") {
		t.Fatalf("a auditoria guardou o comando: %s", dump)
	}
}

func TestNativeToolPolicyOffRejectAndPlugin(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	ana := e.persona(t, "Ana", nil)
	c, _ := toolRun(t, e, ana)
	c.Source = domain.SourceACP

	e.policy(t, e.toolPolicy(&ana, domain.PolicyTargetNative, nil, "Write", domain.PolicyOff))
	d, _ := e.svc.NativeToolCheck(ctx, c, service.NativeToolRequest{Name: "Write"})
	if d.Decision != service.NativeDeny || !strings.Contains(d.Reason, "desligada") {
		t.Fatalf("off: %+v", d)
	}

	// rejeitar no gate nega a chamada e a repetição igual
	e.policy(t, e.toolPolicy(&ana, domain.PolicyTargetNative, nil, "Edit", domain.PolicyAsk))
	d, _ = e.svc.NativeToolCheck(ctx, c, service.NativeToolRequest{Name: "Edit", Input: json.RawMessage(`{"f":"a"}`)})
	if d.Decision != service.NativeAsk {
		t.Fatalf("Edit: %+v", d)
	}
	if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, *d.GateID, service.DecideGateInput{Decision: domain.GateRejected, Note: "não"}); err != nil {
		t.Fatal(err)
	}
	st, _ := e.svc.NativeToolGateState(ctx, c, *d.GateID)
	if st.State != "deny" {
		t.Fatalf("rejeitado: %+v", st)
	}
	d, _ = e.svc.NativeToolCheck(ctx, c, service.NativeToolRequest{Name: "Edit", Input: json.RawMessage(`{"f":"a"}`)})
	if d.Decision != service.NativeDeny {
		t.Fatalf("repetição de chamada rejeitada: %+v", d)
	}
	// entrada diferente = chamada nova
	d, _ = e.svc.NativeToolCheck(ctx, c, service.NativeToolRequest{Name: "Edit", Input: json.RawMessage(`{"f":"b"}`)})
	if d.Decision != service.NativeAsk {
		t.Fatalf("entrada diferente: %+v", d)
	}

	// tool de plugin: o curinga não a cobre; a política pelo nome sim, e o ask segue o modelo "repita depois"
	e.policy(t, e.toolPolicy(nil, domain.PolicyTargetAll, nil, "", domain.PolicyOff))
	d, _ = e.svc.NativeToolCheck(ctx, c, service.NativeToolRequest{Name: "plugin_demo_hello", Plugin: true})
	if d.Decision != service.NativeNone {
		t.Fatalf("o curinga não cobre plugin: %+v", d)
	}
	e.policy(t, e.toolPolicy(&ana, domain.PolicyTargetNative, nil, "plugin_demo_hello", domain.PolicyAsk))
	pc := c
	pc.Source = domain.SourcePlugin
	d, _ = e.svc.NativeToolCheck(ctx, pc, service.NativeToolRequest{Name: "plugin_demo_hello", Plugin: true, Input: json.RawMessage(`{"a":1}`)})
	if d.Decision != service.NativeAsk || d.GateID == nil {
		t.Fatalf("plugin em ask: %+v", d)
	}
	if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, *d.GateID, service.DecideGateInput{Decision: domain.GateApproved}); err != nil {
		t.Fatal(err)
	}
	d, _ = e.svc.NativeToolCheck(ctx, pc, service.NativeToolRequest{Name: "plugin_demo_hello", Plugin: true, Input: json.RawMessage(`{"a":1}`)})
	if d.Decision != service.NativeAllow {
		t.Fatalf("a repetição aprovada passa: %+v", d)
	}
	d, _ = e.svc.NativeToolCheck(ctx, pc, service.NativeToolRequest{Name: "plugin_demo_hello", Plugin: true, Input: json.RawMessage(`{"a":1}`)})
	if d.Decision != service.NativeAsk {
		t.Fatalf("aprovação de uso único: %+v", d)
	}
}

func TestAwaitNativeToolGateReturnsOnDecisionAndCancel(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	ana := e.persona(t, "Ana", nil)
	c, _ := toolRun(t, e, ana)
	e.policy(t, e.toolPolicy(nil, domain.PolicyTargetNative, nil, "Bash", domain.PolicyAsk))
	d, _ := e.svc.NativeToolCheck(ctx, c, service.NativeToolRequest{Name: "Bash", Input: json.RawMessage(`{"c":1}`)})
	if d.GateID == nil {
		t.Fatalf("ask: %+v", d)
	}
	service.SetNativeGatePoll(20 * time.Millisecond)
	t.Cleanup(func() { service.SetNativeGatePoll(2 * time.Second) })

	done := make(chan service.NativeToolDecision, 1)
	go func() {
		r, _ := e.svc.AwaitNativeToolGate(ctx, c, *d.GateID)
		done <- r
	}()
	time.Sleep(80 * time.Millisecond)
	select {
	case r := <-done:
		t.Fatalf("saiu antes da decisão: %+v", r)
	default:
	}
	if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, *d.GateID, service.DecideGateInput{Decision: domain.GateApproved}); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if r.Decision != service.NativeAllow {
			t.Fatalf("depois de aprovar: %+v", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a espera não terminou")
	}

	// cancelar a run (contexto) encerra a espera com negação
	d, _ = e.svc.NativeToolCheck(ctx, c, service.NativeToolRequest{Name: "Bash", Input: json.RawMessage(`{"c":2}`)})
	cctx, cancel := context.WithCancel(ctx)
	go func() {
		r, _ := e.svc.AwaitNativeToolGate(cctx, c, *d.GateID)
		done <- r
	}()
	time.Sleep(60 * time.Millisecond)
	cancel()
	select {
	case r := <-done:
		if r.Decision != service.NativeDeny {
			t.Fatalf("cancelada: %+v", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a espera cancelada não terminou")
	}
}

func TestToolGateTimeoutExpiresAndOpensNewGate(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	e.refresh(t)
	ana := e.persona(t, "Ana", nil)
	iid := e.integ.ID
	t0 := time.Now().UTC().Truncate(time.Second)
	e.svc.Now = func() time.Time { return t0 }
	e.policy(t, e.toolPolicy(nil, domain.PolicyTargetNative, nil, "Bash", domain.PolicyAsk))
	e.policy(t, e.toolPolicy(&ana, domain.PolicyTargetTool, &iid, "apagar", domain.PolicyAsk))
	c, _ := toolRun(t, e, ana)

	nat, _ := e.svc.NativeToolCheck(ctx, c, service.NativeToolRequest{Name: "Bash", Input: json.RawMessage(`{"c":1}`)})
	call, _ := e.svc.GatewayCallTool(ctx, c, "docs", "apagar", json.RawMessage(`{"x":1}`))
	if nat.GateID == nil || call.GateID == nil {
		t.Fatalf("gates: %+v %+v", nat, call)
	}
	// nada vence antes do prazo
	if n, err := e.svc.ExpireToolGates(ctx); err != nil || n != 0 {
		t.Fatalf("antes do prazo: %d %v", n, err)
	}
	// o gate da chamada nativa (10 min) vence primeiro; o da integração (24 h) segue pendente
	e.svc.Now = func() time.Time { return t0.Add(11 * time.Minute) }
	if n, err := e.svc.ExpireToolGates(ctx); err != nil || n != 1 {
		t.Fatalf("expirar nativo: %d %v", n, err)
	}
	g, _ := e.svc.GetGate(ctx, e.co.ID, *nat.GateID)
	if g.Status != domain.GateExpired || g.DecidedAt == nil {
		t.Fatalf("gate nativo: %+v", g)
	}
	if g2, _ := e.svc.GetGate(ctx, e.co.ID, *call.GateID); g2.Status != domain.GatePending {
		t.Fatalf("gate da integração: %s", g2.Status)
	}
	if st, _ := e.svc.NativeToolGateState(ctx, c, *nat.GateID); st.State != "deny" || !strings.Contains(st.Reason, "prazo") {
		t.Fatalf("estado do vencido: %+v", st)
	}
	_, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, *nat.GateID, service.DecideGateInput{Decision: domain.GateApproved})
	wantErr(t, err, domain.ErrConflict)
	// vencido, a mesma chamada abre um gate novo
	again, _ := e.svc.NativeToolCheck(ctx, c, service.NativeToolRequest{Name: "Bash", Input: json.RawMessage(`{"c":1}`)})
	if again.Decision != service.NativeAsk || again.GateID == nil || *again.GateID == *nat.GateID {
		t.Fatalf("gate novo: %+v", again)
	}

	e.svc.Now = func() time.Time { return t0.Add(25 * time.Hour) }
	if n, _ := e.svc.ExpireToolGates(ctx); n != 2 {
		t.Fatalf("expirar o restante: %d", n)
	}
	res, _ := e.svc.GatewayCallTool(ctx, c, "docs", "apagar", json.RawMessage(`{"x":1}`))
	if res.Decision != domain.ToolDecisionAsk || res.GateID == nil || *res.GateID == *call.GateID {
		t.Fatalf("a integração também abre gate novo: %+v", res)
	}
	var n int
	_ = e.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM activity_events WHERE verb='gate.expired'`).Scan(&n)
	if n != 3 {
		t.Fatalf("gate.expired na atividade: %d", n)
	}
}

func TestTestToolAsPersona(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	e.refresh(t)
	ana := e.persona(t, "Ana", nil)
	iid := e.integ.ID
	e.policy(t, e.toolPolicy(&ana, domain.PolicyTargetTool, &iid, "apagar", domain.PolicyAsk))
	e.policy(t, e.toolPolicy(&ana, domain.PolicyTargetTool, &iid, "misterio", domain.PolicyOff))

	// simulação: diz o que aconteceria e não toca em nada
	before := e.up.requests()
	r, err := e.svc.TestToolCall(ctx, domain.HumanActor, e.co.ID, service.ToolTestInput{IntegrationID: iid, Tool: "apagar", PersonaID: ana.ID})
	if err != nil || r.Decision != domain.ToolDecisionAsk || r.GateID != nil || r.Executed && false || r.Verdict.Source != "policy:persona" ||
		r.Status != domain.ToolActive || r.Risk != domain.ToolRiskDestructive || !strings.Contains(r.Message, "simulação") {
		t.Fatalf("simulação ask: %+v %v", r, err)
	}
	r, _ = e.svc.TestToolCall(ctx, domain.HumanActor, e.co.ID, service.ToolTestInput{IntegrationID: iid, Tool: "ler", PersonaID: ana.ID})
	if r.Decision != domain.ToolDecisionAllow || r.Output != "" {
		t.Fatalf("simulação allow: %+v", r)
	}
	r, _ = e.svc.TestToolCall(ctx, domain.HumanActor, e.co.ID, service.ToolTestInput{IntegrationID: iid, Tool: "misterio", PersonaID: ana.ID})
	if r.Decision != domain.ToolDecisionDeny || r.Verdict.Allowed {
		t.Fatalf("simulação off: %+v", r)
	}
	r, _ = e.svc.TestToolCall(ctx, domain.HumanActor, e.co.ID, service.ToolTestInput{IntegrationID: iid, Tool: "naoexiste", PersonaID: ana.ID})
	if r.Decision != domain.ToolDecisionDeny {
		t.Fatalf("tool fora do catálogo: %+v", r)
	}
	var gates, rows int
	_ = e.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM gates`).Scan(&gates)
	_ = e.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM tool_invocations`).Scan(&rows)
	if e.up.requests() != before || gates != 0 || rows != 0 {
		t.Fatalf("a simulação não pode ter efeito: servidor %d→%d, gates %d, auditoria %d", before, e.up.requests(), gates, rows)
	}

	// chamada de verdade: abre o gate em nome da persona, e depois da aprovação o teste repetido passa
	args := json.RawMessage(`{"x":3}`)
	r, err = e.svc.TestToolCall(ctx, domain.HumanActor, e.co.ID, service.ToolTestInput{IntegrationID: iid, Tool: "apagar", PersonaID: ana.ID, Arguments: args, Execute: true})
	if err != nil || r.Decision != domain.ToolDecisionAsk || r.GateID == nil || !r.Executed {
		t.Fatalf("execute ask: %+v %v", r, err)
	}
	g, _ := e.svc.GetGate(ctx, e.co.ID, *r.GateID)
	if g.Kind != domain.GateTool || g.PersonaID == nil || *g.PersonaID != ana.ID || g.RunID != nil {
		t.Fatalf("gate do teste: %+v", g)
	}
	if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, g.ID, service.DecideGateInput{Decision: domain.GateApproved}); err != nil {
		t.Fatal(err)
	}
	r, _ = e.svc.TestToolCall(ctx, domain.HumanActor, e.co.ID, service.ToolTestInput{IntegrationID: iid, Tool: "apagar", PersonaID: ana.ID, Arguments: args, Execute: true})
	if r.Decision != domain.ToolDecisionAllow || r.Output != "ok apagar" || r.IsError {
		t.Fatalf("execute allow: %+v", r)
	}
	if calls := e.up.callLog(); len(calls) != 1 || !strings.HasPrefix(calls[0], "apagar") {
		t.Fatalf("servidor: %v", calls)
	}
	inv, _ := e.svc.ListToolInvocations(ctx, e.co.ID, service.ToolInvocationFilter{Source: domain.SourceTest})
	if len(inv) != 2 {
		t.Fatalf("auditoria do teste: %+v", inv)
	}
	if inv[0].Actor == nil || inv[0].Actor.Type != domain.ActorHuman || inv[0].PersonaID == nil || *inv[0].PersonaID != ana.ID || inv[0].RunID != nil {
		t.Fatalf("ator do teste: %+v", inv[0])
	}

	// persona sem acesso à integração
	outra := e.mcp(t, service.CreateIntegrationInput{Name: "Outra", URL: e.up.hs.URL + "/mcp", AllPersonas: false})
	r, err = e.svc.TestToolCall(ctx, domain.HumanActor, e.co.ID, service.ToolTestInput{IntegrationID: outra.ID, Tool: "ler", PersonaID: ana.ID})
	if err != nil || r.Decision != domain.ToolDecisionDeny || !strings.Contains(r.Message, "não tem acesso") {
		t.Fatalf("sem acesso: %+v %v", r, err)
	}
	// entradas inválidas
	_, err = e.svc.TestToolCall(ctx, domain.HumanActor, e.co.ID, service.ToolTestInput{IntegrationID: iid, Tool: "tem espaço", PersonaID: ana.ID})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.TestToolCall(ctx, domain.HumanActor, e.co.ID, service.ToolTestInput{IntegrationID: iid, Tool: "ler", PersonaID: domain.NewID()})
	wantErr(t, err, domain.ErrNotFound)
}

func TestResultPreviewIsRedacted(t *testing.T) {
	e := newIntEnv(t, service.IntegrationsConfig{AllowPrivate: true})
	up := newUpstreamMCP(t)
	up.srv.AddTool(&sdk.Tool{Name: "vaza", Description: "devolve segredo", InputSchema: json.RawMessage(`{"type":"object"}`),
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}},
		func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "ok; " + upstreamSecret + " e sk-ant-api03-ZZZZZZZZZZZZZZZZZZZZZZZZ"}}}, nil
		})
	integ := e.mcp(t, service.CreateIntegrationInput{Name: "Docs", URL: up.hs.URL + "/mcp", AllPersonas: true,
		Headers: map[string]string{"Authorization": upstreamSecret}})
	te := &toolsEnv{intEnv: e, up: up, integ: integ}
	ctx := context.Background()
	te.refresh(t)
	ana := e.persona(t, "Ana", nil)
	c, _ := toolRun(t, te, ana)
	res, err := e.svc.GatewayCallTool(ctx, c, "docs", "vaza", nil)
	if err != nil || res.Decision != domain.ToolDecisionAllow {
		t.Fatalf("vaza: %+v %v", res, err)
	}
	inv, _ := e.svc.ListToolInvocations(ctx, e.co.ID, service.ToolInvocationFilter{})
	if len(inv) != 1 || inv[0].ResultPreview == "" || strings.Contains(inv[0].ResultPreview, "up-secret-token") ||
		strings.Contains(inv[0].ResultPreview, "ZZZZZZZZZZZZZZ") || !strings.HasPrefix(inv[0].ResultPreview, "ok;") {
		t.Fatalf("preview: %+v", inv)
	}
	// e o teste na UI redige do mesmo jeito
	r, err := e.svc.TestToolCall(ctx, domain.HumanActor, e.co.ID, service.ToolTestInput{IntegrationID: integ.ID, Tool: "vaza", PersonaID: ana.ID, Execute: true})
	if err != nil || strings.Contains(r.Output, "up-secret-token") || strings.Contains(r.Output, "ZZZZZZZZZZZZZZ") || r.Output == "" {
		t.Fatalf("saída do teste: %+v %v", r, err)
	}
}

func TestInboxWarnsAboutQuarantinedTools(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	e.refresh(t) // linha de base: tudo ativo
	in, err := e.svc.Inbox(ctx, e.co.ID)
	if err != nil || len(in.QuarantinedTools) != 0 {
		t.Fatalf("antes: %+v %v", in.QuarantinedTools, err)
	}
	e.up.add("nova_escrita", &sdk.ToolAnnotations{DestructiveHint: bptr(true)})
	e.up.add("nova_leitura", &sdk.ToolAnnotations{ReadOnlyHint: true})
	e.refresh(t)
	in, err = e.svc.Inbox(ctx, e.co.ID)
	if err != nil || len(in.QuarantinedTools) != 1 || in.QuarantinedTools[0].Tool.Name != "nova_escrita" ||
		in.QuarantinedTools[0].Tool.IntegrationSlug != "docs" || in.Count() != 1 {
		t.Fatalf("depois: %+v %v (count %d)", in.QuarantinedTools, err, in.Count())
	}
	// só admin vê o aviso
	member := mkUser(t, e.env, "membro@x.com", domain.RoleMember)
	join(t, e.env, member, domain.MemberMember)
	in, err = e.svc.Inbox(as(member), e.co.ID)
	if err != nil || len(in.QuarantinedTools) != 0 {
		t.Fatalf("membro: %+v %v", in.QuarantinedTools, err)
	}
	// revisada, o aviso some
	if _, err := e.svc.ReviewQuarantinedTools(ctx, domain.HumanActor, e.co.ID, e.integ.ID, nil); err != nil {
		t.Fatal(err)
	}
	in, _ = e.svc.Inbox(ctx, e.co.ID)
	if len(in.QuarantinedTools) != 0 || in.Count() != 0 {
		t.Fatalf("revisada: %+v", in.QuarantinedTools)
	}
}
