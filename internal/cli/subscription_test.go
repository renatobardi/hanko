package cli

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/api"
)

const subsPath = "/api/v1/companies/acme/subscriptions"

var weekly = 50.0

var sub1 = api.Subscription{ID: "0198f2a0-bbbb-7000-8000-000000000001", Name: "Claude Max", Agent: "claude", MonthlyUSD: 200,
	ModelPrefixes: []string{"claude-opus"}, WeeklyLimitUSD: &weekly, WeeklyGuardPct: 80, Active: true, WeekUsageUSD: 42, WeekPercent: 84,
	Blocked: true, WeekStart: time.Date(2026, 10, 12, 3, 0, 0, 0, time.UTC), WeekEnd: time.Date(2026, 10, 19, 3, 0, 0, 0, time.UTC)}

func TestSubscriptionCreate(t *testing.T) {
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{"POST " + subsPath: jsonResp(201, sub1)})
	code, out, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "subscription", "create", "Claude Max", "--agent", "claude", "--monthly", "200",
		"--models", "claude-opus,claude-sonnet", "--weekly-limit", "50", "--guard-pct", "80")
	if code != 0 || !strings.Contains(out, "Assinatura criada: Claude Max — claude, US$ 200.00 por mês, cobre claude-opus") {
		t.Fatalf("%d %q", code, out)
	}
	b := f.last().Body
	for _, want := range []string{`"name":"Claude Max"`, `"agent":"claude"`, `"monthly_usd":200`, `"model_prefixes":["claude-opus","claude-sonnet"]`,
		`"weekly_limit_usd":50`, `"weekly_guard_pct":80`} {
		if !strings.Contains(b, want) {
			t.Errorf("corpo sem %s: %s", want, b)
		}
	}
	// sem --weekly-limit o teto não vai no corpo (sem trava); sem --inactive nasce ligada
	exec(t, Runtime{}, gateEnv(ts.URL), "", "sub", "create", "Codex", "--agent", "codex")
	if b := f.last().Body; strings.Contains(b, "weekly_limit_usd") || strings.Contains(b, "inactive") {
		t.Errorf("corpo: %s", b)
	}
	exec(t, Runtime{}, gateEnv(ts.URL), "", "sub", "create", "Parada", "--agent", "fake", "--inactive")
	if b := f.last().Body; !strings.Contains(b, `"inactive":true`) {
		t.Errorf("corpo: %s", b)
	}
	// --json imprime a assinatura
	_, out, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "sub", "create", "Claude Max", "--agent", "claude", "--json")
	if !strings.Contains(out, `"weekly_guard_pct": 80`) {
		t.Errorf("json: %q", out)
	}
	// --agent é obrigatório: erro de uso, nada é enviado
	n := len(f.hits)
	code, _, errOut := exec(t, Runtime{}, gateEnv(ts.URL), "", "subscription", "create", "Sem agente")
	if code != 2 || !strings.Contains(errOut, "agent") || len(f.hits) != n {
		t.Fatalf("sem --agent: %d %q hits=%d", code, errOut, len(f.hits)-n)
	}
	if code, _, errOut = exec(t, Runtime{}, map[string]string{"HANKO_URL": ts.URL}, "", "subscription", "create", "X", "--agent", "claude"); code != 2 || !strings.Contains(errOut, "empresa não informada") {
		t.Fatalf("sem empresa: %d %q", code, errOut)
	}
}

func TestSubscriptionListShow(t *testing.T) {
	free := api.Subscription{ID: "0198f2a0-bbbb-7000-8000-000000000002", Name: "Codex", Agent: "codex", MonthlyUSD: 20, ModelPrefixes: []string{}, WeeklyGuardPct: 90, Active: false}
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET " + subsPath:                 jsonResp(200, list(sub1, free)),
		"GET " + subsPath + "/Claude Max": jsonResp(200, sub1),
		"GET " + subsPath + "/Codex":      jsonResp(200, free),
	})
	code, out, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "subscription", "list")
	if code != 0 {
		t.Fatalf("%d %q", code, out)
	}
	for _, want := range []string{"NOME", "Claude Max", "US$ 200.00", "ligada", "US$ 42.00 / US$ 50.00 TRAVADA", "Codex", "desligada"} {
		if !strings.Contains(out, want) {
			t.Errorf("lista sem %q:\n%s", want, out)
		}
	}
	_, out, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "sub", "show", "Claude Max")
	for _, want := range []string{"Assinatura:", "Claude Max", "claude-opus", "TRAVADA: novas execuções bloqueadas", "trava em 80%"} {
		if !strings.Contains(out, want) {
			t.Errorf("show sem %q:\n%s", want, out)
		}
	}
	if f.last().Path != subsPath+"/Claude Max" {
		t.Errorf("rota: %s", f.last().Path)
	}
	_, out, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "sub", "show", "Codex")
	if !strings.Contains(out, "todos os modelos do harness") || !strings.Contains(out, "sem trava semanal") {
		t.Errorf("show sem teto:\n%s", out)
	}
	_, out, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "sub", "list", "--json")
	if !strings.Contains(out, `"week_usage_usd": 42`) {
		t.Errorf("json: %q", out)
	}
	// vazio ensina o comando
	ts2, _ := newFakeAPI(t, map[string]http.HandlerFunc{"GET " + subsPath: jsonResp(200, list[api.Subscription]())})
	_, out, _ = exec(t, Runtime{}, gateEnv(ts2.URL), "", "subscription", "ls")
	if !strings.Contains(out, "Nenhuma assinatura") || !strings.Contains(out, "hanko subscription create") {
		t.Errorf("vazio: %q", out)
	}
	// inexistente: erro da API, código 1
	code, _, errOut := exec(t, Runtime{}, gateEnv(ts.URL), "", "sub", "show", "Fantasma")
	if code != 1 || !strings.Contains(errOut, "404") {
		t.Errorf("inexistente: %d %q", code, errOut)
	}
}

func TestSubscriptionUpdate(t *testing.T) {
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{"PATCH " + subsPath + "/Claude Max": jsonResp(200, sub1)})
	env := gateEnv(ts.URL)
	code, out, _ := exec(t, Runtime{}, env, "", "subscription", "update", "Claude Max", "--monthly", "250", "--weekly-limit", "60", "--guard-pct", "70",
		"--models", "claude-opus, claude-sonnet", "--name", "Max 20x", "--agent", "claude", "--off")
	if code != 0 || !strings.Contains(out, "Assinatura atualizada: Claude Max") {
		t.Fatalf("%d %q", code, out)
	}
	b := f.last().Body
	for _, want := range []string{`"name":"Max 20x"`, `"agent":"claude"`, `"monthly_usd":250`, `"weekly_limit_usd":60`, `"weekly_guard_pct":70`,
		`"model_prefixes":["claude-opus","claude-sonnet"]`, `"active":false`} {
		if !strings.Contains(b, want) {
			t.Errorf("corpo sem %s: %s", want, b)
		}
	}
	// só o que foi pedido vai no corpo
	exec(t, Runtime{}, env, "", "sub", "update", "Claude Max", "--on")
	if b := f.last().Body; b != `{"active":true}` {
		t.Errorf("corpo: %s", b)
	}
	exec(t, Runtime{}, env, "", "sub", "update", "Claude Max", "--no-weekly-limit")
	if b := f.last().Body; b != `{"clear_weekly_limit":true}` {
		t.Errorf("corpo: %s", b)
	}
	// --models vazio limpa os prefixos (todos os modelos do harness)
	exec(t, Runtime{}, env, "", "sub", "update", "Claude Max", "--models", "")
	if b := f.last().Body; b != `{"model_prefixes":[]}` {
		t.Errorf("corpo: %s", b)
	}
	// uso incorreto: código 2, nada enviado
	n := len(f.hits)
	for _, args := range [][]string{
		{"sub", "update", "Claude Max"},
		{"sub", "update", "Claude Max", "--on", "--off"},
		{"sub", "update"},
	} {
		if code, _, _ := exec(t, Runtime{}, env, "", args...); code != 2 {
			t.Errorf("%v: código %d, quer 2", args, code)
		}
	}
	if len(f.hits) != n {
		t.Errorf("uso incorreto enviou %d requisição(ões)", len(f.hits)-n)
	}
	// erro de validação da API vira mensagem e código 1
	ts2, _ := newFakeAPI(t, map[string]http.HandlerFunc{"PATCH " + subsPath + "/Claude Max": func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(422)
		_, _ = w.Write([]byte(`{"title":"Entrada inválida","status":422,"detail":"entrada inválida: weekly_limit_usd: must be > 0"}`))
	}})
	code, _, errOut := exec(t, Runtime{}, gateEnv(ts2.URL), "", "sub", "update", "Claude Max", "--weekly-limit", "0")
	if code != 1 || !strings.Contains(errOut, "weekly_limit_usd") || !strings.Contains(errOut, "422") {
		t.Errorf("422: %d %q", code, errOut)
	}
}

func TestSubscriptionDelete(t *testing.T) {
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"DELETE " + subsPath + "/Parada": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) },
		"DELETE " + subsPath + "/Usada": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(409)
			_, _ = w.Write([]byte(`{"title":"Conflito","status":409,"detail":"assinatura com execuções: desligue-a"}`))
		},
	})
	code, out, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "subscription", "rm", "Parada")
	if code != 0 || !strings.Contains(out, "Assinatura Parada removida.") || f.last().Method != "DELETE" {
		t.Fatalf("%d %q", code, out)
	}
	code, _, errOut := exec(t, Runtime{}, gateEnv(ts.URL), "", "subscription", "delete", "Usada")
	if code != 1 || !strings.Contains(errOut, "desligue-a") || !strings.Contains(errOut, "409") {
		t.Fatalf("%d %q", code, errOut)
	}
}

// persona update: as flags de revisor e de assinatura viram os campos certos do PATCH, e as
// combinações contraditórias são erro de uso.
func TestPersonaUpdateRevisorEAssinatura(t *testing.T) {
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{"PATCH /api/v1/companies/acme/personas/ada": jsonResp(200, api.Persona{Name: "Ada", Slug: "ada"})})
	env := gateEnv(ts.URL)
	for _, c := range []struct {
		args []string
		body string
	}{
		{[]string{"--reviewer", "kenji"}, `{"reviewer":"kenji"}`},
		{[]string{"--reviewer-agent", "codex", "--reviewer-model", "gpt-5"}, `{"reviewer_agent":"codex","reviewer_model":"gpt-5"}`},
		{[]string{"--reviewer-model", "gpt-5-mini"}, `{"reviewer_model":"gpt-5-mini"}`},
		{[]string{"--no-reviewer"}, `{"clear_reviewer":true}`},
		{[]string{"--subscription", "Claude Max"}, `{"subscription":"Claude Max"}`},
		{[]string{"--no-subscription"}, `{"clear_subscription":true}`},
	} {
		code, out, errOut := exec(t, Runtime{}, env, "", append([]string{"persona", "update", "ada"}, c.args...)...)
		if code != 0 || !strings.Contains(out, "Persona atualizada: Ada (ada)") {
			t.Fatalf("%v: %d %q %q", c.args, code, out, errOut)
		}
		if b := f.last().Body; b != c.body {
			t.Errorf("%v: corpo %s, quer %s", c.args, b, c.body)
		}
	}
	n := len(f.hits)
	if code, _, errOut := exec(t, Runtime{}, env, "", "persona", "update", "ada", "--subscription", "x", "--no-subscription"); code != 2 || !strings.Contains(errOut, "não combinam") {
		t.Errorf("assinatura contraditória: %d %q", code, errOut)
	}
	if code, _, _ := exec(t, Runtime{}, env, "", "persona", "update", "ada", "--require-review", "talvez"); code != 2 {
		t.Errorf("--require-review inválido: %d", code)
	}
	if len(f.hits) != n {
		t.Errorf("uso incorreto enviou requisição")
	}
	// erro da API (revisor inválido) chega ao usuário
	ts2, _ := newFakeAPI(t, map[string]http.HandlerFunc{"PATCH /api/v1/companies/acme/personas/ada": func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(422)
		_, _ = w.Write([]byte(`{"title":"Entrada inválida","status":422,"detail":"entrada inválida: reviewer: a persona cannot review itself"}`))
	}})
	code, _, errOut := exec(t, Runtime{}, gateEnv(ts2.URL), "", "persona", "update", "ada", "--reviewer", "ada")
	if code != 1 || !strings.Contains(errOut, "cannot review itself") {
		t.Errorf("revisor inválido: %d %q", code, errOut)
	}
}

// run list filtra por nome e run logs/show/cancel aceitam o nome no lugar do id.
func TestRunListMostraNome(t *testing.T) {
	r := api.Run{ID: "0198f2a0-0000-7000-8000-0000000000bb", Name: "Veloz_Raposa", TaskKey: "HNK-1", PersonaName: "Ada", Status: "done", Trigger: "assign"}
	ts, _ := newFakeAPI(t, map[string]http.HandlerFunc{"GET /api/v1/companies/acme/runs": jsonResp(200, list(r))})
	_, out, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "run", "list")
	if !strings.Contains(out, "NOME") || !strings.Contains(out, "Veloz_Raposa") {
		t.Errorf("a lista mostra o nome:\n%s", out)
	}
}
