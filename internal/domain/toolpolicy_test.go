package domain

import (
	"fmt"
	"math/rand"
	"testing"
)

// Testes do motor de política de tools (ADR-0053): de tabela e de propriedade (#56). As propriedades usam um gerador
// com semente fixa: o mesmo conjunto de casos em toda execução, e a semente vai na mensagem de falha.

func tid(n int) ID {
	var id ID
	id[15] = byte(n)
	id[14] = byte(n >> 8)
	return id
}

func pol(n int, scope ToolPolicyScope, persona *ID, target ToolPolicyTarget, integ *ID, tool string, mode ToolPolicyMode) ToolPolicy {
	return ToolPolicy{ID: tid(n), Scope: scope, PersonaID: persona, Target: target, IntegrationID: integ, ToolName: tool, Mode: mode}
}

func TestToolPolicyTable(t *testing.T) {
	pa, pb := tid(1001), tid(1002)
	gh, jira := tid(2001), tid(2002)
	company := func(n int, target ToolPolicyTarget, integ *ID, tool string, m ToolPolicyMode) ToolPolicy {
		return pol(n, PolicyScopeCompany, nil, target, integ, tool, m)
	}
	persona := func(n int, p *ID, target ToolPolicyTarget, integ *ID, tool string, m ToolPolicyMode) ToolPolicy {
		return pol(n, PolicyScopePersona, p, target, integ, tool, m)
	}
	call := func(p *ID, integ *ID, tool string) ToolSubject {
		return ToolSubject{Kind: SubjectIntegration, PersonaID: p, IntegrationID: integ, Tool: tool}
	}
	native := func(p *ID, tool string) ToolSubject {
		return ToolSubject{Kind: SubjectNative, PersonaID: p, Tool: tool}
	}
	plugin := func(p *ID, tool string) ToolSubject {
		return ToolSubject{Kind: SubjectPlugin, PersonaID: p, Tool: tool}
	}

	cases := []struct {
		name     string
		policies []ToolPolicy
		subject  ToolSubject
		want     ToolPolicyMode
		wantID   int // 0 = nenhuma política decide
	}{
		{"sem política", nil, call(&pa, &gh, "create_issue"), "", 0},
		{"padrão da empresa para tudo", []ToolPolicy{company(1, PolicyTargetAll, nil, "", PolicyAsk)}, call(&pa, &gh, "x"), PolicyAsk, 1},
		{"persona vence empresa", []ToolPolicy{
			company(1, PolicyTargetTool, &gh, "delete_repo", PolicyOff),
			persona(2, &pa, PolicyTargetTool, &gh, "delete_repo", PolicyAllow),
		}, call(&pa, &gh, "delete_repo"), PolicyAllow, 2},
		{"persona vence empresa só para a própria persona", []ToolPolicy{
			company(1, PolicyTargetTool, &gh, "delete_repo", PolicyOff),
			persona(2, &pa, PolicyTargetTool, &gh, "delete_repo", PolicyAllow),
		}, call(&pb, &gh, "delete_repo"), PolicyOff, 1},
		{"persona com curinga vence empresa com tool", []ToolPolicy{
			company(1, PolicyTargetTool, &gh, "x", PolicyAllow),
			persona(2, &pa, PolicyTargetAll, nil, "", PolicyOff),
		}, call(&pa, &gh, "x"), PolicyOff, 2},
		{"tool vence integração vence curinga", []ToolPolicy{
			company(1, PolicyTargetAll, nil, "", PolicyOff),
			company(2, PolicyTargetIntegration, &gh, "", PolicyAsk),
			company(3, PolicyTargetTool, &gh, "list", PolicyAllow),
		}, call(&pa, &gh, "list"), PolicyAllow, 3},
		{"integração vence curinga", []ToolPolicy{
			company(1, PolicyTargetAll, nil, "", PolicyOff),
			company(2, PolicyTargetIntegration, &gh, "", PolicyAsk),
		}, call(&pa, &gh, "other"), PolicyAsk, 2},
		{"outra integração cai no curinga", []ToolPolicy{
			company(1, PolicyTargetAll, nil, "", PolicyOff),
			company(2, PolicyTargetIntegration, &gh, "", PolicyAllow),
		}, call(&pa, &jira, "t"), PolicyOff, 1},
		{"empate: deny vence ask vence allow", []ToolPolicy{
			company(1, PolicyTargetTool, &gh, "t", PolicyAllow),
			company(2, PolicyTargetTool, &gh, "t", PolicyOff),
			company(3, PolicyTargetTool, &gh, "t", PolicyAsk),
		}, call(&pa, &gh, "t"), PolicyOff, 2},
		{"native por nome", []ToolPolicy{company(1, PolicyTargetNative, nil, "Bash", PolicyAsk)}, native(&pa, "Bash"), PolicyAsk, 1},
		{"native outro nome sem opinião", []ToolPolicy{company(1, PolicyTargetNative, nil, "Bash", PolicyAsk)}, native(&pa, "Edit"), "", 0},
		{"curinga cobre native", []ToolPolicy{company(1, PolicyTargetAll, nil, "", PolicyAsk)}, native(&pa, "Edit"), PolicyAsk, 1},
		{"curinga não cobre plugin", []ToolPolicy{company(1, PolicyTargetAll, nil, "", PolicyOff)}, plugin(&pa, "plugin_x_do"), "", 0},
		{"plugin por nome", []ToolPolicy{company(1, PolicyTargetNative, nil, "plugin_x_do", PolicyOff)}, plugin(&pa, "plugin_x_do"), PolicyOff, 1},
		{"native não alcança tool de integração de mesmo nome", []ToolPolicy{company(1, PolicyTargetNative, nil, "list", PolicyOff)}, call(&pa, &gh, "list"), "", 0},
		{"gateway nomeado (sem persona) ignora política de persona", []ToolPolicy{
			persona(1, &pa, PolicyTargetAll, nil, "", PolicyOff),
			company(2, PolicyTargetAll, nil, "", PolicyAsk),
		}, call(nil, &gh, "t"), PolicyAsk, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ResolveToolPolicy(c.policies, c.subject)
			if c.wantID == 0 {
				if ok {
					t.Fatalf("esperava nenhuma política, veio %v", got.ID)
				}
				return
			}
			if !ok || got.Mode != c.want || got.ID != tid(c.wantID) {
				t.Fatalf("got ok=%v id=%v mode=%s, quer id=%d mode=%s", ok, got.ID, got.Mode, c.wantID, c.want)
			}
		})
	}
}

func TestApplyToolPoliciesOverridesProfile(t *testing.T) {
	pa := tid(1)
	gh := tid(2)
	s := ToolSubject{Kind: SubjectIntegration, PersonaID: &pa, IntegrationID: &gh, Tool: "t"}
	excluded := ToolVerdict{Allowed: false, Source: "exclude"}
	asked := ToolVerdict{Allowed: true, Mode: ToolModeAsk, Source: "include"}

	if got := ApplyToolPolicies(asked, nil, s); got.Source != "include" || got.Mode != ToolModeAsk {
		t.Fatalf("sem política o perfil segue: %+v", got)
	}
	always := []ToolPolicy{pol(1, PolicyScopePersona, &pa, PolicyTargetTool, &gh, "t", PolicyAllow)}
	if got := ApplyToolPolicies(asked, always, s); !got.Allowed || got.Mode != ToolModeAllow || got.Source != "policy:persona" || got.Policy == nil {
		t.Fatalf("sempre permitir vence o ask do perfil: %+v", got)
	}
	off := []ToolPolicy{pol(2, PolicyScopeCompany, nil, PolicyTargetAll, nil, "", PolicyOff)}
	if got := ApplyToolPolicies(ToolVerdict{Allowed: true, Mode: ToolModeAllow}, off, s); got.Allowed || got.Source != "policy:company" {
		t.Fatalf("off da empresa recusa: %+v", got)
	}
	if got := ApplyToolPolicies(excluded, always, s); !got.Allowed {
		t.Fatalf("a política da persona vence o perfil: %+v", got)
	}
	if _, ok := NativeToolVerdict(nil, ToolSubject{Kind: SubjectNative, PersonaID: &pa, Tool: "Bash"}); ok {
		t.Fatal("sem política não há opinião sobre tool nativa")
	}
}

// ---- propriedades ----

type gen struct{ r *rand.Rand }

func (g gen) pick(n int) int { return g.r.Intn(n) }

// universo pequeno para as colisões acontecerem: 2 personas, 2 integrações, 2 tools, 2 nomes nativos.
func (g gen) policy(n int) ToolPolicy {
	personas := []ID{tid(1001), tid(1002)}
	integs := []ID{tid(2001), tid(2002)}
	tools := []string{"a", "b"}
	natives := []string{"Bash", "Edit"}
	p := ToolPolicy{ID: tid(n), Mode: PolicyModes[g.pick(3)]}
	if g.pick(2) == 0 {
		p.Scope = PolicyScopeCompany
	} else {
		p.Scope = PolicyScopePersona
		pp := personas[g.pick(2)]
		p.PersonaID = &pp
	}
	switch g.pick(4) {
	case 0:
		p.Target = PolicyTargetAll
	case 1:
		p.Target = PolicyTargetIntegration
		i := integs[g.pick(2)]
		p.IntegrationID = &i
	case 2:
		p.Target = PolicyTargetTool
		i := integs[g.pick(2)]
		p.IntegrationID, p.ToolName = &i, tools[g.pick(2)]
	default:
		p.Target, p.ToolName = PolicyTargetNative, natives[g.pick(2)]
	}
	return p
}

func (g gen) subject() ToolSubject {
	personas := []ID{tid(1001), tid(1002)}
	integs := []ID{tid(2001), tid(2002)}
	tools := []string{"a", "b"}
	natives := []string{"Bash", "Edit"}
	s := ToolSubject{}
	if g.pick(5) > 0 {
		pp := personas[g.pick(2)]
		s.PersonaID = &pp
	}
	if g.pick(3) > 0 {
		i := integs[g.pick(2)]
		s.Kind, s.IntegrationID, s.Tool = SubjectIntegration, &i, tools[g.pick(2)]
	} else {
		s.Kind, s.Tool = SubjectNative, natives[g.pick(2)]
	}
	return s
}

func (g gen) policies(max int) []ToolPolicy {
	n := g.pick(max + 1)
	out := make([]ToolPolicy, n)
	for i := range out {
		out[i] = g.policy(i + 1)
	}
	return out
}

func forSeeds(t *testing.T, n int, f func(t *testing.T, g gen, seed int64)) {
	t.Helper()
	for seed := int64(1); seed <= int64(n); seed++ {
		f(t, gen{r: rand.New(rand.NewSource(seed))}, seed)
	}
}

// A decisão não depende da ordem em que as políticas chegam do banco.
func TestPropertyResolveIndependentOfOrder(t *testing.T) {
	forSeeds(t, 400, func(t *testing.T, g gen, seed int64) {
		ps, s := g.policies(10), g.subject()
		want, wok := ResolveToolPolicy(ps, s)
		for k := 0; k < 5; k++ {
			shuffled := append([]ToolPolicy(nil), ps...)
			g.r.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
			got, ok := ResolveToolPolicy(shuffled, s)
			if ok != wok || got.ID != want.ID {
				t.Fatalf("seed %d: ordem mudou a decisão (%v/%v vs %v/%v)", seed, ok, got.ID, wok, want.ID)
			}
		}
	})
}

// Precedência: se existe política da persona que alcança a chamada, a decisão é de escopo persona, nunca da empresa.
func TestPropertyPersonaBeatsCompany(t *testing.T) {
	forSeeds(t, 600, func(t *testing.T, g gen, seed int64) {
		ps, s := g.policies(12), g.subject()
		hasPersona := false
		for _, p := range ps {
			if p.Scope == PolicyScopePersona && p.Matches(s) {
				hasPersona = true
			}
		}
		got, ok := ResolveToolPolicy(ps, s)
		if hasPersona && (!ok || got.Scope != PolicyScopePersona) {
			t.Fatalf("seed %d: havia política da persona e venceu %s", seed, got.Scope)
		}
		if ok && !got.Matches(s) {
			t.Fatalf("seed %d: a política escolhida não alcança a chamada", seed)
		}
	})
}

// Deny vence: entre políticas de mesmo escopo e mesma especificidade que alcançam a chamada, havendo um off, o
// resultado é off (e ask vence allow).
func TestPropertyDenyWinsAtSameLevel(t *testing.T) {
	forSeeds(t, 600, func(t *testing.T, g gen, seed int64) {
		ps, s := g.policies(12), g.subject()
		got, ok := ResolveToolPolicy(ps, s)
		if !ok {
			return
		}
		for _, p := range ps {
			if !p.Matches(s) || p.Scope != got.Scope || p.Target.specificity() != got.Target.specificity() {
				continue
			}
			if p.Mode.restriction() > got.Mode.restriction() {
				t.Fatalf("seed %d: %s do mesmo nível perdeu para %s", seed, p.Mode, got.Mode)
			}
		}
	})
}

// Idempotência: gravar a mesma política duas vezes (UPSERT) dá o mesmo conjunto e a mesma decisão; e aplicar o
// veredito sobre o veredito não muda nada.
func TestPropertyUpsertIdempotent(t *testing.T) {
	forSeeds(t, 400, func(t *testing.T, g gen, seed int64) {
		ps, s := g.policies(8), g.subject()
		extra := g.policy(100)
		once := UpsertPolicy(ps, extra)
		twice := UpsertPolicy(once, extra)
		if fmt.Sprint(once) != fmt.Sprint(twice) {
			t.Fatalf("seed %d: o upsert não é idempotente", seed)
		}
		a, aok := ResolveToolPolicy(once, s)
		b, bok := ResolveToolPolicy(twice, s)
		if aok != bok || a.ID != b.ID {
			t.Fatalf("seed %d: a decisão mudou no segundo upsert", seed)
		}
		// no máximo uma política por chave
		seen := map[PolicyKey]bool{}
		for _, p := range once {
			if p.Key() == extra.Key() {
				if seen[p.Key()] {
					t.Fatalf("seed %d: chave duplicada após o upsert", seed)
				}
				seen[p.Key()] = true
			}
		}
		profile := ToolVerdict{Allowed: true, Mode: ToolModeAllow, Source: "include"}
		v1 := ApplyToolPolicies(profile, once, s)
		v2 := ApplyToolPolicies(v1, once, s)
		if v1.Allowed != v2.Allowed || v1.Mode != v2.Mode || v1.Source != v2.Source {
			t.Fatalf("seed %d: aplicar duas vezes mudou o veredito (%+v vs %+v)", seed, v1, v2)
		}
	})
}

// "Sempre permitir" grava uma política de persona; depois dela a chamada passa direto, salvo política mais forte
// (alvo mais específico ou off no mesmo nível): nunca vira ask de novo por causa do perfil ou do padrão da empresa.
func TestPropertyAlwaysAllowStopsAsking(t *testing.T) {
	forSeeds(t, 400, func(t *testing.T, g gen, seed int64) {
		ps := g.policies(8)
		s := g.subject()
		if s.PersonaID == nil || s.Kind != SubjectIntegration {
			return
		}
		always := ToolPolicy{ID: tid(500), Scope: PolicyScopePersona, PersonaID: s.PersonaID, Target: PolicyTargetTool,
			IntegrationID: s.IntegrationID, ToolName: s.Tool, Mode: PolicyAllow, Origin: PolicyOriginAlways}
		after := UpsertPolicy(ps, always)
		got := ApplyToolPolicies(ToolVerdict{Allowed: true, Mode: ToolModeAsk, Source: "include"}, after, s)
		if !got.Allowed || got.Mode != ToolModeAllow {
			t.Fatalf("seed %d: depois do sempre permitir a chamada deveria passar: %+v", seed, got)
		}
	})
}

// Tool do próprio Hanko (ADR-0068 §4.2): sem política, pergunta antes; a política por nome manda; o curinga não a alcança.
func TestHankoToolVerdict(t *testing.T) {
	pa := tid(1001)
	hanko := func(p *ID, tool string) ToolSubject {
		return ToolSubject{Kind: SubjectHanko, PersonaID: p, Tool: tool}
	}
	byName := func(n int, scope ToolPolicyScope, persona *ID, m ToolPolicyMode) ToolPolicy {
		return pol(n, scope, persona, PolicyTargetNative, nil, "hanko_secret_get", m)
	}
	cases := []struct {
		name     string
		policies []ToolPolicy
		s        ToolSubject
		want     ToolVerdict
	}{
		{"sem política pergunta", nil, hanko(&pa, "hanko_secret_get"),
			ToolVerdict{Allowed: true, Mode: ToolModeAsk, Source: "default"}},
		{"política da empresa off nega", []ToolPolicy{byName(1, PolicyScopeCompany, nil, PolicyOff)}, hanko(&pa, "hanko_secret_get"),
			ToolVerdict{Allowed: false, Source: "policy:company"}},
		{"política da persona allow libera", []ToolPolicy{byName(2, PolicyScopePersona, &pa, PolicyAllow)}, hanko(&pa, "hanko_secret_get"),
			ToolVerdict{Allowed: true, Mode: ToolModeAllow, Source: "policy:persona"}},
		{"curinga de integração não alcança a tool do Hanko",
			[]ToolPolicy{pol(3, PolicyScopeCompany, nil, PolicyTargetAll, nil, "", PolicyOff)}, hanko(&pa, "hanko_secret_get"),
			ToolVerdict{Allowed: true, Mode: ToolModeAsk, Source: "default"}},
		{"outra tool não é alcançada pela política do nome", []ToolPolicy{byName(4, PolicyScopeCompany, nil, PolicyOff)}, hanko(&pa, "hanko_task_get"),
			ToolVerdict{Allowed: true, Mode: ToolModeAsk, Source: "default"}},
	}
	for _, c := range cases {
		got := HankoToolVerdict(c.policies, c.s)
		if got.Allowed != c.want.Allowed || got.Mode != c.want.Mode || got.Source != c.want.Source {
			t.Errorf("%s: veredito = %+v, quero %+v", c.name, got, c.want)
		}
	}
}
