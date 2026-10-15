package ui

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

func TestCostsPageShowsFourCostsAndSubscriptions(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	rec := e.get(t, "/c/hanko/custos")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Os quatro custos", "Real", "Estimado", "Lista", "Pago", "sem uso", "Custos por dia",
		`id="assinaturas"`, "Claude Max", "Nova assinatura", `name="weekly_limit"`)
	// no subscription: the paid column explains itself, nothing says "sem uso"
	empty := newEnv(t, uitest.Empty()).get(t, "/c/hanko/custos")
	wantContains(t, empty, "sem assinaturas cadastradas", "Nenhuma assinatura")
	wantNotContains(t, empty, ">sem uso<")
}

func TestSubscriptionFormsRoundTrip(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	rec := e.post(t, "/c/hanko/custos/assinaturas", url.Values{"name": {"Codex Pro"}, "agent": {"codex"}, "monthly": {"200"},
		"weekly_limit": {"50,50"}, "guard_pct": {"80"}, "models": {"gpt-5, gpt-4"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/c/hanko/custos?ok=assinatura-nova#assinaturas" {
		t.Fatalf("Location = %q", loc)
	}
	var id domain.ID
	for _, s := range e.f.Subs {
		if s.Name == "Codex Pro" {
			id = s.ID
			if s.Agent != "codex" || s.MonthlyUSD != 200 || s.WeeklyLimitUSD == nil || *s.WeeklyLimitUSD != 50.5 || s.WeeklyGuardPct != 80 || len(s.ModelPrefixes) != 2 {
				t.Fatalf("created: %+v", s)
			}
		}
	}
	if id == (domain.ID{}) {
		t.Fatal("subscription not created")
	}
	// invalid money is a 422, not a crash
	wantStatus(t, e.post(t, "/c/hanko/custos/assinaturas", url.Values{"name": {"x"}, "agent": {"codex"}, "monthly": {"abc"}}, false), http.StatusUnprocessableEntity)
	wantStatus(t, e.post(t, "/c/hanko/custos/assinaturas", url.Values{"name": {"x"}, "agent": {"codex"}}, false), http.StatusUnprocessableEntity)

	// update: empty weekly limit removes the guard; unchecked "active" switches it off
	rec = e.post(t, "/c/hanko/custos/assinaturas/"+id.String(), url.Values{"monthly": {"150"}, "weekly_limit": {""}, "guard_pct": {"90"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	for _, s := range e.f.Subs {
		if s.ID == id && (s.MonthlyUSD != 150 || s.WeeklyLimitUSD != nil || s.Active) {
			t.Fatalf("updated: %+v", s)
		}
	}
	wantContains(t, e.get(t, "/c/hanko/custos?ok=assinatura"), "Assinatura salva.", "desligada")

	wantStatus(t, e.post(t, "/c/hanko/custos/assinaturas/"+id.String()+"/remover", nil, false), http.StatusSeeOther)
	for _, s := range e.f.Subs {
		if s.ID == id {
			t.Fatal("subscription not removed")
		}
	}
	wantStatus(t, e.post(t, "/c/hanko/custos/assinaturas/not-an-id", url.Values{"monthly": {"1"}}, false), http.StatusNotFound)
	rec = e.do(t, req{method: "POST", path: "/c/hanko/custos/assinaturas", form: url.Values{"name": {"x"}}, hdr: map[string]string{"Sec-Fetch-Site": "cross-site"}})
	wantStatus(t, rec, http.StatusForbidden)
}

func TestPersonaSubscriptionLink(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	sub := uitest.ID("sub:Claude Max")
	wantContains(t, e.get(t, "/c/hanko/personas/ada"), `id="assinatura"`, "Sem assinatura", "Claude Max")
	wantStatus(t, e.post(t, "/c/hanko/personas/ada/assinatura", url.Values{"subscription": {sub.String()}}, false), http.StatusSeeOther)
	for _, p := range e.f.Personas {
		if p.Slug == "ada" && (p.SubscriptionID == nil || *p.SubscriptionID != sub) {
			t.Fatalf("persona not linked: %v", p.SubscriptionID)
		}
	}
	wantStatus(t, e.post(t, "/c/hanko/personas/ada/assinatura", url.Values{"subscription": {""}}, false), http.StatusSeeOther)
	for _, p := range e.f.Personas {
		if p.Slug == "ada" && p.SubscriptionID != nil {
			t.Fatal("persona not unlinked")
		}
	}
	wantStatus(t, e.post(t, "/c/hanko/personas/ada/assinatura", url.Values{"subscription": {"xx"}}, false), http.StatusUnprocessableEntity)
}

func TestInboxShowsGuardedSubscription(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	wantNotContains(t, e.get(t, "/c/hanko/caixa"), "Assinaturas travadas")
	limit := 60.0
	e.f.Subs[0].Blocked, e.f.Subs[0].WeeklyLimitUSD, e.f.Subs[0].WeekUsageUSD, e.f.Subs[0].WeekPercent = true, &limit, 58, 96.7
	wantContains(t, e.get(t, "/c/hanko/caixa"), "Assinaturas travadas", "Claude Max", "Trava semanal", "passou da trava de 90%")
	wantContains(t, e.get(t, "/c/hanko/caixa?aba=assinaturas"), "Assinaturas travadas")
	wantNotContains(t, e.get(t, "/c/hanko/caixa?aba=gates"), "Assinaturas travadas")
}

func TestTaskManualModelAndRunModelSource(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	wantContains(t, e.get(t, "/c/hanko/tasks/nova"), `name="model"`, "Modelo (opcional)")
	rec := e.post(t, "/c/hanko/tasks/nova", url.Values{"title": {"Com modelo"}, "model": {"claude-opus-4-5"}, "status": {"todo"}, "priority": {"medium"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	found := false
	for _, tk := range e.f.Tasks {
		if tk.Title == "Com modelo" {
			found = tk.Model == "claude-opus-4-5"
		}
	}
	if !found {
		t.Fatal("manual model not stored on the task")
	}
	run := e.f.Runs[0]
	e.f.Runs[0].ModelSource = domain.ModelFromTask
	wantContains(t, e.get(t, "/c/hanko/execucoes/"+run.ID.String()), "Origem do modelo", "escolha manual na task", "Custo de lista", "Origem do custo")
}
