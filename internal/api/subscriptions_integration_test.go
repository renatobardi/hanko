//go:build integration

package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

func TestAPIAssinaturasCRUD(t *testing.T) {
	e := newAPIEnv(t)
	subs := e.base() + "/subscriptions"

	var max api.Subscription
	raw := e.want(201, "POST", subs, `{"name":"Claude Max","agent":"claude","monthly_usd":200,
		"model_prefixes":["claude-opus"," claude-opus",""],"weekly_limit_usd":50,"weekly_guard_pct":80}`, &max)
	if max.ID == "" || max.CompanyID != e.co.ID.String() || max.Name != "Claude Max" || max.Agent != "claude" || max.MonthlyUSD != 200 ||
		len(max.ModelPrefixes) != 1 || max.ModelPrefixes[0] != "claude-opus" || max.WeeklyLimitUSD == nil || *max.WeeklyLimitUSD != 50 ||
		max.WeeklyGuardPct != 80 || !max.Active || max.Blocked || max.WeekUsageUSD != 0 || max.WeekPercent != 0 {
		t.Fatalf("assinatura criada: %s", raw)
	}
	// a semana corrente vai de segunda a segunda no fuso da empresa (10/03/2026 é terça)
	if !max.WeekStart.Equal(time.Date(2026, 3, 9, 3, 0, 0, 0, time.UTC)) {
		t.Fatalf("week_start = %v", max.WeekStart)
	}
	if !max.WeekEnd.Equal(max.WeekStart.AddDate(0, 0, 7)) {
		t.Fatalf("a semana tem 7 dias: %v .. %v", max.WeekStart, max.WeekEnd)
	}

	// padrões: trava em 90%, sem teto semanal, lista de prefixos vazia (nunca null)
	var codex api.Subscription
	raw = e.want(201, "POST", subs, `{"name":"Codex Pro","agent":"codex","monthly_usd":20}`, &codex)
	if codex.WeeklyGuardPct != 90 || codex.WeeklyLimitUSD != nil || !codex.Active || !strings.Contains(string(raw), `"model_prefixes":[]`) {
		t.Fatalf("padrões: %s", raw)
	}
	var off api.Subscription
	e.want(201, "POST", subs, `{"name":"Parada","agent":"fake","monthly_usd":1,"inactive":true}`, &off)
	if off.Active {
		t.Fatal("inactive:true cria a assinatura desligada")
	}

	// leitura por id e por nome (com espaço), e listagem
	var got api.Subscription
	e.want(200, "GET", subs+"/"+max.ID, "", &got)
	if got.ID != max.ID {
		t.Fatalf("get por id: %+v", got)
	}
	e.want(200, "GET", subs+"/"+esc("Claude Max"), "", &got)
	if got.ID != max.ID {
		t.Fatalf("get por nome: %+v", got)
	}
	var list api.ListBody[api.Subscription]
	e.want(200, "GET", subs, "", &list)
	if len(list.Items) != 3 || list.Items[0].Name != "Claude Max" {
		t.Fatalf("lista (ordem de criação): %+v", list.Items)
	}
	e.want(200, "GET", subs+"?limit=1&offset=1", "", &list)
	if len(list.Items) != 1 || list.Items[0].Name != "Codex Pro" {
		t.Fatalf("paginação: %+v", list.Items)
	}
	e.want(404, "GET", subs+"/nao-existe", "", nil)

	// nome repetido na empresa: 409
	e.want(409, "POST", subs, `{"name":"Claude Max","agent":"claude","monthly_usd":1}`, nil)

	// validação: 422 com o campo no corpo do erro
	long := strings.Repeat("x", 121)
	for name, body := range map[string]string{
		"nome vazio":         `{"name":"","agent":"claude","monthly_usd":1}`,
		"nome longo":         `{"name":"` + long + `","agent":"claude","monthly_usd":1}`,
		"harness inválido":   `{"name":"a","agent":"gemini","monthly_usd":1}`,
		"sem harness":        `{"name":"a","monthly_usd":1}`,
		"mensalidade < 0":    `{"name":"a","agent":"claude","monthly_usd":-1}`,
		"teto zero":          `{"name":"a","agent":"claude","monthly_usd":1,"weekly_limit_usd":0}`,
		"teto negativo":      `{"name":"a","agent":"claude","monthly_usd":1,"weekly_limit_usd":-5}`,
		"trava acima de 100": `{"name":"a","agent":"claude","monthly_usd":1,"weekly_guard_pct":101}`,
		"trava negativa":     `{"name":"a","agent":"claude","monthly_usd":1,"weekly_guard_pct":-3}`,
		"tipo errado":        `{"name":"a","agent":"claude","monthly_usd":"muito"}`,
	} {
		status, b := e.call("POST", subs, body, nil)
		if status != http.StatusUnprocessableEntity {
			t.Errorf("%s: status %d, quer 422: %s", name, status, b)
		}
	}
	if status, _ := e.call("POST", subs, `{"name":`, nil); status != http.StatusBadRequest && status != http.StatusUnprocessableEntity {
		t.Errorf("JSON malformado: status %d", status)
	}
	if n := len(e.mustSubs()); n != 3 {
		t.Fatalf("pedidos inválidos não podem criar nada: %d assinaturas", n)
	}

	// atualização parcial, por nome; demais campos intactos
	var up api.Subscription
	e.want(200, "PATCH", subs+"/"+esc("Claude Max"), `{"monthly_usd":250,"weekly_limit_usd":60,"weekly_guard_pct":70}`, &up)
	if up.MonthlyUSD != 250 || *up.WeeklyLimitUSD != 60 || up.WeeklyGuardPct != 70 || up.Agent != "claude" || up.Name != "Claude Max" || !up.Active {
		t.Fatalf("patch: %+v", up)
	}
	e.want(200, "PATCH", subs+"/"+max.ID, `{"model_prefixes":["claude-opus","claude-sonnet"],"active":false}`, &up)
	if up.Active || len(up.ModelPrefixes) != 2 || up.Blocked {
		t.Fatalf("desligar: %+v", up)
	}
	e.want(200, "PATCH", subs+"/"+max.ID, `{"active":true,"clear_weekly_limit":true,"name":"Claude Max 20x"}`, &up)
	if !up.Active || up.WeeklyLimitUSD != nil || up.WeekPercent != 0 || up.Name != "Claude Max 20x" {
		t.Fatalf("reativar e limpar o teto: %+v", up)
	}
	for name, body := range map[string]string{
		"harness inválido": `{"agent":"gemini"}`,
		"nome vazio":       `{"name":""}`,
		"mensalidade < 0":  `{"monthly_usd":-1}`,
		"teto zero":        `{"weekly_limit_usd":0}`,
		"trava zero":       `{"weekly_guard_pct":0}`,
		"trava 101":        `{"weekly_guard_pct":101}`,
	} {
		if status, b := e.call("PATCH", subs+"/"+max.ID, body, nil); status != http.StatusUnprocessableEntity {
			t.Errorf("patch %s: status %d, quer 422: %s", name, status, b)
		}
	}
	e.want(409, "PATCH", subs+"/"+max.ID, `{"name":"Codex Pro"}`, nil) // renomear para um nome em uso
	e.want(404, "PATCH", subs+"/"+esc("Fantasma"), `{"monthly_usd":1}`, nil)

	// excluir: sem execuções, 204; com execuções cobertas, 409 (desligue)
	e.want(204, "DELETE", subs+"/"+esc("Parada"), "", nil)
	e.want(404, "GET", subs+"/"+off.ID, "", nil)
	e.want(404, "DELETE", subs+"/"+off.ID, "", nil)
	p := e.persona(e.co, "Eng", func(in *service.HirePersonaInput) { in.Agent = "codex" })
	cid, err := domain.ParseID(codex.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.UpdatePersona(context.Background(), domain.HumanActor, e.co.ID, p.ID, service.UpdatePersonaInput{SubscriptionID: &cid}); err != nil {
		t.Fatal(err)
	}
	e.spend(p, time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC), usdRes(1))
	e.want(409, "DELETE", subs+"/"+codex.ID, "", nil)
	e.want(200, "GET", subs+"/"+codex.ID, "", nil)

	// cada escrita deixou um activity_event
	verbs := e.activityVerbs()
	if verbs["subscription.created"] != 3 || verbs["subscription.updated"] < 3 {
		t.Fatalf("activity: %v", verbs)
	}
}

// Persona (run token) só lê: criar, alterar e remover assinatura é decisão de humano.
func TestAPIAssinaturasPersonaSoLe(t *testing.T) {
	e := newAPIEnv(t)
	subs := e.base() + "/subscriptions"
	var s api.Subscription
	e.want(201, "POST", subs, `{"name":"Claude Max","agent":"claude","monthly_usd":200}`, &s)
	ada := e.persona(e.co, "Ada", nil)
	h := e.personaToken(e.co, ada)

	for _, c := range []struct{ method, path, body string }{
		{"POST", subs, `{"name":"Intrusa","agent":"claude","monthly_usd":1}`},
		{"PATCH", subs + "/" + s.ID, `{"monthly_usd":0}`},
		{"PATCH", subs + "/" + esc("Claude Max"), `{"active":false}`},
		{"DELETE", subs + "/" + s.ID, ""},
	} {
		if status, b := e.call(c.method, c.path, c.body, h); status != http.StatusForbidden {
			t.Errorf("%s %s com run token: status %d, quer 403: %s", c.method, c.path, status, b)
		}
	}
	// leitura é permitida
	for _, path := range []string{subs, subs + "/" + s.ID} {
		if status, b := e.call("GET", path, "", h); status != 200 {
			t.Errorf("GET %s com run token: status %d: %s", path, status, b)
		}
	}
	// token inválido: 401, nada é criado
	if status, _ := e.call("POST", subs, `{"name":"X","agent":"claude","monthly_usd":1}`, map[string]string{"Authorization": "Bearer lixo"}); status != 401 {
		t.Errorf("token inválido: %d", status)
	}
	var got api.Subscription
	e.want(200, "GET", subs+"/"+s.ID, "", &got)
	if got.MonthlyUSD != 200 || !got.Active {
		t.Fatalf("a assinatura não pode ter mudado: %+v", got)
	}
	if n := len(e.mustSubs()); n != 1 {
		t.Fatalf("assinaturas = %d, quer 1", n)
	}
}

// Assinaturas são da empresa: outra empresa não vê, não altera, não remove nem liga persona a ela.
func TestAPIAssinaturasIsolamentoPorEmpresa(t *testing.T) {
	e := newAPIEnv(t)
	other := e.company("Beta", "beta")
	var a api.Subscription
	e.want(201, "POST", e.base()+"/subscriptions", `{"name":"Plano","agent":"claude","monthly_usd":100}`, &a)
	bsubs := "/api/v1/companies/beta/subscriptions"

	var list api.ListBody[api.Subscription]
	e.want(200, "GET", bsubs, "", &list)
	if len(list.Items) != 0 {
		t.Fatalf("a outra empresa enxerga %d assinatura(s)", len(list.Items))
	}
	for _, ref := range []string{a.ID, esc("Plano")} {
		e.want(404, "GET", bsubs+"/"+ref, "", nil)
		e.want(404, "PATCH", bsubs+"/"+ref, `{"monthly_usd":1}`, nil)
		e.want(404, "DELETE", bsubs+"/"+ref, "", nil)
	}
	// o mesmo nome vale em outra empresa
	var b api.Subscription
	e.want(201, "POST", bsubs, `{"name":"Plano","agent":"claude","monthly_usd":5}`, &b)
	if b.ID == a.ID || b.CompanyID != other.ID.String() {
		t.Fatalf("assinatura da outra empresa: %+v", b)
	}
	// persona de B não pode apontar para a assinatura de A (por id), e o id de B não existe em A
	e.persona(other, "Bia", nil)
	if status, b := e.call("PATCH", "/api/v1/companies/beta/personas/bia", `{"subscription":"`+a.ID+`"}`, nil); status != 404 && status != 422 {
		t.Errorf("persona de B -> assinatura de A: status %d: %s", status, b)
	}
	if status, b := e.call("POST", "/api/v1/companies/beta/personas", `{"name":"Caio","agent":"fake","subscription":"`+a.ID+`"}`, nil); status != 404 && status != 422 {
		t.Errorf("contratar em B com assinatura de A: status %d: %s", status, b)
	}
	var personas api.ListBody[api.Persona]
	e.want(200, "GET", "/api/v1/companies/beta/personas", "", &personas)
	for _, p := range personas.Items {
		if p.SubscriptionID != "" {
			t.Fatalf("persona %s ficou ligada a %s", p.Slug, p.SubscriptionID)
		}
	}
	// os custos de B não enxergam a assinatura de A
	var cs api.CostSummary
	e.want(200, "GET", "/api/v1/companies/beta/costs?from=2026-03-01&to=2026-03-31", "", &cs)
	for _, sc := range cs.Subscriptions {
		if sc.ID == a.ID {
			t.Fatalf("custos de B listam a assinatura de A: %+v", cs.Subscriptions)
		}
	}
	// e A continua intacta
	var still api.Subscription
	e.want(200, "GET", e.base()+"/subscriptions/"+a.ID, "", &still)
	if still.MonthlyUSD != 100 {
		t.Fatalf("assinatura de A mudou: %+v", still)
	}
}

func (e *apiEnv) mustSubs() []api.Subscription {
	e.t.Helper()
	var list api.ListBody[api.Subscription]
	e.want(200, "GET", e.base()+"/subscriptions?limit=500", "", &list)
	return list.Items
}

// activityVerbs conta os fatos de domínio da empresa por verbo, lidos pela API.
func (e *apiEnv) activityVerbs() map[string]int {
	e.t.Helper()
	var list api.ListBody[api.Activity]
	e.want(200, "GET", e.base()+"/activity?limit=500", "", &list)
	out := map[string]int{}
	for _, a := range list.Items {
		out[a.Verb]++
	}
	return out
}
