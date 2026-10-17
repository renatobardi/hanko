package ui

import (
	"net/url"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

// Aba Política (ADR-0053): lista, grava, apaga e testa uma tool como uma persona.
func TestToolPolicyPage(t *testing.T) {
	f := uitest.Sample()
	e := newEnv(t, f)
	b := "/c/hanko/ferramentas/politica"
	rec := e.get(t, b)
	wantStatus(t, rec, 200)
	wantContains(t, rec, "Nenhuma política.", "Nova política", "Testar uma tool como uma persona", `href="/c/hanko/ferramentas/politica"`)

	p := f.Personas[0]
	// persona + tool de integração em ask
	rec = e.post(t, b, url.Values{"persona": {p.Slug}, "target": {"tool"}, "integration": {"github"}, "tool": {"delete_repo"}, "mode": {"ask"}}, true)
	wantStatus(t, rec, 204)
	if loc := rec.Header().Get("HX-Redirect"); !strings.Contains(loc, "politica?ok=gravada") {
		t.Fatalf("redirect: %q", loc)
	}
	in := f.Tools.LastPolicy
	if in.Scope != domain.PolicyScopePersona || in.PersonaID == nil || *in.PersonaID != p.ID || in.Target != domain.PolicyTargetTool ||
		in.IntegrationID == nil || in.ToolName != "delete_repo" || in.Mode != domain.PolicyAsk {
		t.Fatalf("política enviada ao service: %+v", in)
	}
	// padrão da empresa para tools do harness: o nome vale, a integração é ignorada
	e.post(t, b, url.Values{"persona": {""}, "target": {"native"}, "integration": {"github"}, "tool": {"Bash"}, "mode": {"off"}}, true)
	in = f.Tools.LastPolicy
	if in.Scope != domain.PolicyScopeCompany || in.PersonaID != nil || in.Target != domain.PolicyTargetNative || in.IntegrationID != nil || in.ToolName != "Bash" || in.Mode != domain.PolicyOff {
		t.Fatalf("política nativa: %+v", in)
	}
	// o curinga ignora o nome da tool
	e.post(t, b, url.Values{"target": {"all"}, "tool": {"x"}, "mode": {"allow"}}, true)
	if in = f.Tools.LastPolicy; in.Target != domain.PolicyTargetAll || in.ToolName != "" {
		t.Fatalf("curinga: %+v", in)
	}

	rec = e.get(t, b)
	wantContains(t, rec, "github/delete_repo", "tool nativa Bash", "qualquer tool", "perguntar antes", "desligada", p.Name, "Empresa (padrão)")
	wantNotContains(t, rec, "Nenhuma política.")

	// apagar
	id := f.Tools.Policies[0].ID
	rec = e.post(t, b+"/"+id.String()+"/remover", url.Values{}, true)
	wantStatus(t, rec, 204)
	if len(f.Tools.Policies) != 2 || !f.Called("DeleteToolPolicy") {
		t.Fatalf("apagar: %d", len(f.Tools.Policies))
	}
	rec = e.post(t, b+"/"+domain.NewID().String()+"/remover", url.Values{}, true)
	wantStatus(t, rec, 404)

	// entrada inválida volta como erro, sem chamar o service
	rec = e.post(t, b, url.Values{"target": {"x"}, "mode": {"ask"}}, true)
	wantStatus(t, rec, 422)
}

func TestToolPolicyTestAsPersona(t *testing.T) {
	f := uitest.Sample()
	e := newEnv(t, f)
	p := f.Personas[0]
	b := "/c/hanko/ferramentas/politica/testar"
	rec := e.post(t, b, url.Values{"persona": {p.Slug}, "integration": {"github"}, "tool": {"create_issue"}, "args": {`{"title":"x"}`}}, false)
	wantStatus(t, rec, 200)
	wantContains(t, rec, "Resultado do teste", "simulação", "create_issue", "perguntar antes", "política da persona", "ativa")
	in := f.Tools.LastTest
	if in.Tool != "create_issue" || in.PersonaID != p.ID || in.Execute || !strings.Contains(string(in.Arguments), `"title"`) {
		t.Fatalf("teste: %+v", in)
	}
	// chamar de verdade mostra a saída do servidor
	rec = e.post(t, b, url.Values{"persona": {p.Slug}, "integration": {"github"}, "tool": {"create_issue"}, "execute": {"1"}}, false)
	wantStatus(t, rec, 200)
	wantContains(t, rec, "issue #42 aberta")
	if !f.Tools.LastTest.Execute {
		t.Fatal("execute não chegou ao service")
	}
	// JSON inválido, persona ou integração desconhecidas
	rec = e.post(t, b, url.Values{"persona": {p.Slug}, "integration": {"github"}, "tool": {"x"}, "args": {"{"}}, false)
	wantStatus(t, rec, 422)
	rec = e.post(t, b, url.Values{"persona": {"ninguem"}, "integration": {"github"}, "tool": {"x"}}, false)
	wantStatus(t, rec, 404)
	rec = e.post(t, b, url.Values{"persona": {p.Slug}, "integration": {"nao-existe"}, "tool": {"x"}}, false)
	wantStatus(t, rec, 404)
}

func TestToolPolicyRoles(t *testing.T) {
	f := uitest.Sample()
	f.MyRoleIs = domain.MemberMember
	e := newEnv(t, f)
	rec := e.get(t, "/c/hanko/ferramentas/politica")
	wantStatus(t, rec, 200)
	wantNotContains(t, rec, "Nova política", "Testar uma tool como uma persona", "Gravar política")
	for _, path := range []string{"/c/hanko/ferramentas/politica", "/c/hanko/ferramentas/politica/testar"} {
		rec = e.post(t, path, url.Values{"target": {"all"}, "mode": {"allow"}, "persona": {f.Personas[0].Slug}, "integration": {"github"}, "tool": {"x"}}, true)
		wantStatus(t, rec, 403)
	}
}

// Tool nova em quarentena aparece como aviso na aba Tools da Caixa de entrada.
func TestInboxQuarantineWarning(t *testing.T) {
	f := uitest.Sample()
	f.Tools.InboxQuarantine = true
	e := newEnv(t, f)
	for _, path := range []string{"/c/hanko/caixa?aba=tools", "/c/hanko/caixa"} {
		rec := e.get(t, path)
		wantStatus(t, rec, 200)
		wantContains(t, rec, "Tools em quarentena", "github/delete_repo", "destrutiva", "/c/hanko/ferramentas?integracao=github", "Revisar")
	}
	rec := e.get(t, "/c/hanko/caixa?aba=gates")
	wantNotContains(t, rec, "Tools em quarentena")
	f2 := uitest.Sample()
	rec = newEnv(t, f2).get(t, "/c/hanko/caixa?aba=tools")
	wantNotContains(t, rec, "Tools em quarentena")
}

// "Aprovar e sempre permitir" aparece só no gate tool de uma persona, para admin, e envia Always.
func TestGateApproveAlwaysButton(t *testing.T) {
	f := uitest.Sample()
	p := f.Personas[0]
	pid := p.ID
	tg := service.GateView{Gate: domain.Gate{ID: domain.NewID(), CompanyID: p.CompanyID, Kind: domain.GateTool, Status: domain.GatePending,
		Title: "Tool github/delete_repo: aprovar a chamada", Body: "corpo", PersonaID: &pid, RequestedBy: domain.PersonaActor(pid), CreatedAt: uitest.Now()}, PersonaName: p.Name}
	gw := tg // gate aberto por um gateway nomeado: sem persona
	gw.ID, gw.PersonaID, gw.PersonaName, gw.RequestedBy = domain.NewID(), nil, "", domain.SystemActor
	f.Gates = append(f.Gates, tg, gw)
	var other string
	for _, g := range f.Gates {
		if g.Kind != domain.GateTool && g.Status == domain.GatePending {
			other = g.ID.String()
			break
		}
	}
	e := newEnv(t, f)
	rec := e.get(t, "/c/hanko/gates/"+tg.ID.String())
	wantContains(t, rec, "Aprovar e sempre permitir", `value="approved_always"`)
	wantNotContains(t, e.get(t, "/c/hanko/gates/"+gw.ID.String()), "Aprovar e sempre permitir")
	if other != "" {
		wantNotContains(t, e.get(t, "/c/hanko/gates/"+other), "Aprovar e sempre permitir")
	}
	rec = e.post(t, "/c/hanko/gates/"+tg.ID.String()+"/decidir", url.Values{"decision": {"approved_always"}}, true)
	wantStatus(t, rec, 204)
	if d := f.Tools.LastDecision; d.Decision != domain.GateApproved || !d.Always {
		t.Fatalf("decisão: %+v", d)
	}
	// membro não vê o botão
	f2 := uitest.Sample()
	f2.MyRoleIs = domain.MemberMember
	f2.Gates = append(f2.Gates, tg)
	wantNotContains(t, newEnv(t, f2).get(t, "/c/hanko/gates/"+tg.ID.String()), "Aprovar e sempre permitir")
}

// "Tools efetivas" e a auditoria dizem de onde veio a decisão, inclusive quando foi uma política.
func TestVerdictTextShowsPolicyOrigin(t *testing.T) {
	ctx := t.Context()
	for src, want := range map[string]string{"policy:persona": "política da persona", "policy:company": "política da empresa"} {
		d, o := verdictText(ctx, domain.ToolVerdict{Allowed: true, Mode: domain.ToolModeAsk, Source: src})
		if d != "Perguntar antes" || o != want {
			t.Errorf("%s: %q / %q", src, d, o)
		}
	}
	if d, o := verdictText(ctx, domain.ToolVerdict{Allowed: false, Source: "policy:company"}); d != "Desligado" || o != "política da empresa" {
		t.Errorf("off: %q / %q", d, o)
	}
	for src, want := range map[domain.ToolInvocationSource]string{domain.SourceRun: "execução", domain.SourceGateway: "gateway", domain.SourceTest: "teste",
		domain.SourceHook: "hook do harness", domain.SourceACP: "ACP", domain.SourcePlugin: "plugin"} {
		if got := invocationSourceLabel(ctx, src); got != want {
			t.Errorf("%s: %q", src, got)
		}
	}
}
