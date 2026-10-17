package domain

import (
	"reflect"
	"testing"
	"time"
)

func boolp(b bool) *bool { return &b }

func TestToolRiskDerivation(t *testing.T) {
	tests := []struct {
		name string
		a    ToolAnnotations
		want ToolRisk
		decl bool
	}{
		{"somente leitura", ToolAnnotations{ReadOnlyHint: boolp(true)}, ToolRiskRead, true},
		{"leitura vence destrutiva", ToolAnnotations{ReadOnlyHint: boolp(true), DestructiveHint: boolp(true)}, ToolRiskRead, true},
		{"escreve sem destruir", ToolAnnotations{DestructiveHint: boolp(false)}, ToolRiskWrite, true},
		{"destrutiva", ToolAnnotations{DestructiveHint: boolp(true)}, ToolRiskDestructive, true},
		{"readOnly=false sem destructive vale destrutiva", ToolAnnotations{ReadOnlyHint: boolp(false)}, ToolRiskDestructive, true},
		{"idempotente só", ToolAnnotations{IdempotentHint: boolp(true)}, ToolRiskUnknown, true},
		{"nada declarado", ToolAnnotations{Title: "x"}, ToolRiskUnknown, false},
	}
	for _, tt := range tests {
		if got := DeriveToolRisk(tt.a); got != tt.want {
			t.Errorf("%s: risco = %s, quer %s", tt.name, got, tt.want)
		}
		if got := tt.a.Declared(); got != tt.decl {
			t.Errorf("%s: Declared = %v", tt.name, got)
		}
	}
	for _, r := range ToolRisks {
		if !r.Valid() || r.Writes() != (r != ToolRiskRead) {
			t.Errorf("risco %s", r)
		}
	}
	if ToolRisk("x").Valid() {
		t.Error("risco inválido")
	}
}

func TestToolEnumsValid(t *testing.T) {
	ok := []bool{
		ToolActive.Valid(), ToolQuarantined.Valid(), ToolDisabled.Valid(), ToolRemoved.Valid(),
		ToolDefaultDeny.Valid(), ToolDefaultAllow.Valid(),
		ToolTargetIntegration.Valid(), ToolTargetTool.Valid(), ToolTargetRisk.Valid(),
		ToolInclude.Valid(), ToolExclude.Valid(), ToolModeAllow.Valid(), ToolModeAsk.Valid(),
		ToolScopeCompany.Valid(), ToolScopeProject.Valid(), ToolScopePersona.Valid(),
		ToolDecisionAllow.Valid(), ToolDecisionDeny.Valid(), ToolDecisionAsk.Valid(), ToolDecisionQuarantined.Valid(), ToolDecisionError.Valid(),
		ConversationDirect.Valid(), ConversationRoom.Valid(), RoomMentions.Valid(), RoomAll.Valid(),
	}
	for i, v := range ok {
		if !v {
			t.Errorf("valor %d deveria ser válido", i)
		}
	}
	bad := []bool{
		ToolStatus("x").Valid(), ToolDefaultAction("x").Valid(), ToolRuleTarget("x").Valid(), ToolRuleEffect("x").Valid(),
		ToolMode("x").Valid(), ToolBindingScope("x").Valid(), ToolDecision("x").Valid(), ConversationKind("x").Valid(), RoomMode("x").Valid(),
	}
	for i, v := range bad {
		if v {
			t.Errorf("valor inválido %d aceito", i)
		}
	}
}

func TestBindingWins(t *testing.T) {
	t0 := time.Now()
	mk := func(s ToolBindingScope, prio int, at time.Time, id ID) ToolBinding {
		return ToolBinding{ID: id, Scope: s, Priority: prio, CreatedAt: at}
	}
	lo, hi := ID{1}, ID{2}
	tests := []struct {
		name string
		a, b ToolBinding
		want bool
	}{
		{"persona vence projeto", mk(ToolScopePersona, 0, t0, lo), mk(ToolScopeProject, 9, t0, lo), true},
		{"projeto vence empresa", mk(ToolScopeProject, 0, t0, lo), mk(ToolScopeCompany, 9, t0, lo), true},
		{"empresa perde para persona", mk(ToolScopeCompany, 9, t0, lo), mk(ToolScopePersona, 0, t0, lo), false},
		{"prioridade", mk(ToolScopeCompany, 2, t0, lo), mk(ToolScopeCompany, 1, t0, hi), true},
		{"mais novo", mk(ToolScopeCompany, 1, t0.Add(time.Second), lo), mk(ToolScopeCompany, 1, t0, hi), true},
		{"id maior desempata", mk(ToolScopeCompany, 1, t0, hi), mk(ToolScopeCompany, 1, t0, lo), true},
		{"id menor perde", mk(ToolScopeCompany, 1, t0, lo), mk(ToolScopeCompany, 1, t0, hi), false},
		{"escopo desconhecido é o mais fraco", mk("x", 0, t0, lo), mk(ToolScopeCompany, 0, t0, lo), false},
	}
	for _, tt := range tests {
		if got := BindingWins(tt.a, tt.b); got != tt.want {
			t.Errorf("%s: %v", tt.name, got)
		}
	}
}

func TestEvaluateTool(t *testing.T) {
	intA, intB := NewID(), NewID()
	rule := func(target ToolRuleTarget, integ *ID, tool string, risk ToolRisk, eff ToolRuleEffect, mode ToolMode) ToolRule {
		return ToolRule{Target: target, IntegrationID: integ, ToolName: tool, Risk: risk, Effect: eff, Mode: mode}
	}
	if v := EvaluateTool(nil, intA, "t", ToolRiskRead); !v.Allowed || v.Source != "no_profile" {
		t.Errorf("sem perfil: %+v", v)
	}
	tests := []struct {
		name    string
		p       ToolProfile
		integ   ID
		tool    string
		risk    ToolRisk
		allowed bool
		mode    ToolMode
		source  string
	}{
		{"padrão nega", ToolProfile{DefaultAction: ToolDefaultDeny}, intA, "t", ToolRiskRead, false, "", "default"},
		{"padrão permite", ToolProfile{DefaultAction: ToolDefaultAllow}, intA, "t", ToolRiskRead, true, ToolModeAllow, "default"},
		{"integração incluída", ToolProfile{Rules: []ToolRule{rule(ToolTargetIntegration, &intA, "", "", ToolInclude, ToolModeAllow)}}, intA, "t", ToolRiskRead, true, ToolModeAllow, "include"},
		{"outra integração não casa", ToolProfile{Rules: []ToolRule{rule(ToolTargetIntegration, &intA, "", "", ToolInclude, ToolModeAllow)}}, intB, "t", ToolRiskRead, false, "", "default"},
		{"excluir vence incluir", ToolProfile{Rules: []ToolRule{
			rule(ToolTargetIntegration, &intA, "", "", ToolInclude, ToolModeAllow),
			rule(ToolTargetTool, &intA, "t", "", ToolExclude, ToolModeAllow),
		}}, intA, "t", ToolRiskRead, false, "", "exclude"},
		{"tool exata não casa outro nome", ToolProfile{Rules: []ToolRule{rule(ToolTargetTool, &intA, "x", "", ToolInclude, ToolModeAllow)}}, intA, "t", ToolRiskRead, false, "", "default"},
		{"risco global", ToolProfile{Rules: []ToolRule{rule(ToolTargetRisk, nil, "", ToolRiskRead, ToolInclude, ToolModeAllow)}}, intB, "t", ToolRiskRead, true, ToolModeAllow, "include"},
		{"risco de outra integração", ToolProfile{Rules: []ToolRule{rule(ToolTargetRisk, &intA, "", ToolRiskRead, ToolInclude, ToolModeAllow)}}, intB, "t", ToolRiskRead, false, "", "default"},
		{"mais específica vence (tool ask sobre integração allow)", ToolProfile{Rules: []ToolRule{
			rule(ToolTargetIntegration, &intA, "", "", ToolInclude, ToolModeAllow),
			rule(ToolTargetTool, &intA, "t", "", ToolInclude, ToolModeAsk),
		}}, intA, "t", ToolRiskWrite, true, ToolModeAsk, "include"},
		{"empate: ask vence allow", ToolProfile{Rules: []ToolRule{
			rule(ToolTargetIntegration, &intA, "", "", ToolInclude, ToolModeAllow),
			rule(ToolTargetIntegration, &intA, "", "", ToolInclude, ToolModeAsk),
		}}, intA, "t", ToolRiskRead, true, ToolModeAsk, "include"},
		{"alvo desconhecido não casa", ToolProfile{Rules: []ToolRule{{Target: "x", Effect: ToolInclude}}}, intA, "t", ToolRiskRead, false, "", "default"},
	}
	for _, tt := range tests {
		v := EvaluateTool(&tt.p, tt.integ, tt.tool, tt.risk)
		if v.Allowed != tt.allowed || v.Mode != tt.mode || v.Source != tt.source {
			t.Errorf("%s: %+v", tt.name, v)
		}
		if (tt.source == "include" || tt.source == "exclude") && v.Rule == nil {
			t.Errorf("%s: faltou a regra decisora", tt.name)
		}
	}
	// especificidade: risco da integração (3) > integração (2) > risco global (1)
	for want, r := range map[int]ToolRule{
		4: {Target: ToolTargetTool}, 3: {Target: ToolTargetRisk, IntegrationID: &intA}, 1: {Target: ToolTargetRisk},
		2: {Target: ToolTargetIntegration}, 0: {Target: "x"},
	} {
		if r.specificity() != want {
			t.Errorf("specificity(%s) = %d, quer %d", r.Target, r.specificity(), want)
		}
	}
}

func TestEvaluateIntegration(t *testing.T) {
	a, b := NewID(), NewID()
	inc := func(m ToolMode) ToolRule {
		return ToolRule{Target: ToolTargetIntegration, IntegrationID: &a, Effect: ToolInclude, Mode: m}
	}
	if v := EvaluateIntegration(nil, a); !v.Allowed || v.Source != "no_profile" {
		t.Errorf("sem perfil: %+v", v)
	}
	if v := EvaluateIntegration(&ToolProfile{Rules: []ToolRule{inc(ToolModeAllow)}}, a); !v.Allowed || v.Source != "include" {
		t.Errorf("include allow: %+v", v)
	}
	if v := EvaluateIntegration(&ToolProfile{Rules: []ToolRule{inc(ToolModeAsk)}}, a); v.Allowed || v.Mode != ToolModeAsk {
		t.Errorf("ask deixa o stdio de fora: %+v", v)
	}
	exc := ToolRule{Target: ToolTargetIntegration, IntegrationID: &a, Effect: ToolExclude}
	if v := EvaluateIntegration(&ToolProfile{Rules: []ToolRule{inc(ToolModeAllow), exc}}, a); v.Allowed || v.Source != "exclude" {
		t.Errorf("exclude: %+v", v)
	}
	// regras de tool/risco não contam para a integração inteira
	toolRule := ToolRule{Target: ToolTargetTool, IntegrationID: &a, ToolName: "t", Effect: ToolInclude}
	if v := EvaluateIntegration(&ToolProfile{DefaultAction: ToolDefaultDeny, Rules: []ToolRule{toolRule}}, a); v.Allowed || v.Source != "default" {
		t.Errorf("padrão deny: %+v", v)
	}
	if v := EvaluateIntegration(&ToolProfile{DefaultAction: ToolDefaultAllow}, b); !v.Allowed || v.Source != "default" {
		t.Errorf("padrão allow: %+v", v)
	}
}

func TestGatewayTokenActive(t *testing.T) {
	now := time.Now()
	tok := ToolGatewayToken{ExpiresAt: now.Add(time.Hour)}
	if !tok.Active(now) {
		t.Error("token válido")
	}
	if tok.Active(now.Add(2 * time.Hour)) {
		t.Error("token expirado")
	}
	rev := now
	tok.RevokedAt = &rev
	if tok.Active(now) {
		t.Error("token revogado")
	}
}

func TestRoomSettings(t *testing.T) {
	got := RoomSettings{Mode: "x", MaxHops: 99, MaxRunsPerMessage: -1}.Normalized()
	if got.Mode != RoomMentions || got.MaxHops != DefaultRoomHops || got.MaxRunsPerMessage != 0 {
		t.Errorf("Normalized = %+v", got)
	}
	got = RoomSettings{Mode: RoomAll, MaxHops: 3, MaxRunsPerMessage: 10}.Normalized()
	if got.Mode != RoomAll || got.MaxHops != 3 || got.MaxRunsPerMessage != 10 {
		t.Errorf("valores válidos mudaram: %+v", got)
	}
	if got := (RoomSettings{MaxRunsPerMessage: MaxRoomRunsPerMessage + 1}).Normalized(); got.MaxRunsPerMessage != 0 {
		t.Errorf("acima do teto = %d", got.MaxRunsPerMessage)
	}
	if (RoomSettings{MaxRunsPerMessage: 7}).EffectiveMaxRuns(3) != 7 || (RoomSettings{}).EffectiveMaxRuns(3) != 3 || (RoomSettings{}).EffectiveMaxRuns(0) != 1 {
		t.Error("EffectiveMaxRuns")
	}
	now := time.Now()
	if !(RoomParticipant{}).Active() || (RoomParticipant{RemovedAt: &now}).Active() {
		t.Error("RoomParticipant.Active")
	}
}

func TestParseMentions(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"@Ana e @bruno-dev, vejam", []string{"ana", "bruno-dev"}},
		{"oi @ana @ana de novo", []string{"ana"}},
		{"mande para ana@acme.io", nil},
		{"veja `@ana` no código", nil},
		{"```\n@ana\n```\n@bia", []string{"bia"}},
		{"(@carla) fim", []string{"carla"}},
		{"@x- ok", []string{"x"}},
		{"sem menção", nil},
	}
	for _, tt := range tests {
		if got := ParseMentions(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("ParseMentions(%q) = %v, quer %v", tt.in, got, tt.want)
		}
	}
}
