package ui

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/ui/uitest"
)

func TestParseUSD(t *testing.T) {
	for in, want := range map[string]int64{"400": 40000, "400,50": 40050, "1.200,50": 120050, "US$ 80": 8000, "12.5": 1250, "0": 0} {
		got, err := parseUSD(in)
		if err != nil || got == nil || *got != want {
			t.Errorf("parseUSD(%q) = %v, %v; want %d", in, got, err, want)
		}
	}
	if got, err := parseUSD("  "); err != nil || got != nil {
		t.Errorf("vazio deve remover o limite: %v %v", got, err)
	}
	for _, bad := range []string{"abc", "-5", "1e99"} {
		if _, err := parseUSD(bad); err == nil {
			t.Errorf("parseUSD(%q) deveria falhar", bad)
		}
	}
}

func TestDescribeCron(t *testing.T) {
	for in, want := range map[string]string{
		"0 7 * * 5": "Toda sexta às 07:00", "0 */2 * * *": "A cada 2 horas", "*/15 * * * *": "A cada 15 minutos",
		"0 8 * * 1-5": "Dias úteis às 08:00", "30 23 * * *": "Todo dia às 23:30", "0 9 1 * *": "Todo dia 1 do mês às 09:00",
		"0 8 * * 1,3,5": "seg, qua e sex às 08:00", "5 4 * 2 *": "Agenda personalizada",
	} {
		if got := describeCron(in); got != want {
			t.Errorf("describeCron(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCostsPage(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	rec := e.get(t, "/c/hanko/custos")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Limite atingido", "est.", "Reportado pelo harness", `class="bar peak"`)
	for _, p := range []string{"?p=anterior", "?p=30d"} {
		wantStatus(t, e.get(t, "/c/hanko/custos"+p), http.StatusOK)
	}
	wantContains(t, e.get(t, "/c/hanko/custos?p=anterior"), "Nenhum gasto no período.")
	wantContains(t, newEnv(t, uitest.Empty()).get(t, "/c/hanko/custos"), "Sem limite")
}

func TestSetBudgets(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	rec := e.post(t, "/c/hanko/custos/orcamento", url.Values{"budget": {"250,50"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if c := e.f.Companies[0].BudgetMonthlyCents; c == nil || *c != 25050 {
		t.Fatalf("orçamento da empresa = %v", c)
	}
	e.post(t, "/c/hanko/custos/orcamento", url.Values{"budget": {""}}, false)
	if e.f.Companies[0].BudgetMonthlyCents != nil {
		t.Error("vazio deveria remover o limite")
	}
	rec = e.post(t, "/c/hanko/custos/orcamento", url.Values{"budget": {"xx"}}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)

	rec = e.post(t, "/c/hanko/personas/ada/orcamento?next=custos", url.Values{"budget": {"99"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/c/hanko/custos?ok=persona" {
		t.Errorf("Location = %q", loc)
	}
	for _, p := range e.f.Personas {
		if p.Slug == "ada" && (p.BudgetMonthlyCents == nil || *p.BudgetMonthlyCents != 9900) {
			t.Errorf("orçamento da persona = %v", p.BudgetMonthlyCents)
		}
	}
	// cross-site posts are rejected
	rec = e.do(t, req{method: "POST", path: "/c/hanko/custos/orcamento", form: url.Values{"budget": {"1"}}, hdr: map[string]string{"Sec-Fetch-Site": "cross-site"}})
	wantStatus(t, rec, http.StatusForbidden)
}

func TestDashboardAndPersonaBudget(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	wantContains(t, e.get(t, "/c/hanko/"), "Orçamento do mês", "Gasto do mês · 76% de US$ 400,00")
	wantContains(t, e.get(t, "/c/hanko/personas/sato"), "Orçamento do mês", "a persona foi pausada")
	// exceeding the company budget raises the alert
	c := int64(1000)
	e.f.Companies[0].BudgetMonthlyCents = &c
	wantContains(t, e.get(t, "/c/hanko/"), "Orçamento do mês estourado")
}

func TestFlowsUI(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	pid := uitest.ID("persona:ada").String()

	rec := e.post(t, "/c/hanko/fluxos/novo", url.Values{"name": {"Relatório diário"}, "persona": {pid}, "cron": {"0 8 * * 1-5"}, "timezone": {"America/Sao_Paulo"},
		"priority": {"medium"}, "task_title": {"Relatório {{data}}"}, "enabled": {"1"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/c/hanko/fluxos/relatorio-diario?ok=criado" {
		t.Fatalf("Location = %q", loc)
	}
	wantContains(t, e.get(t, "/c/hanko/fluxos/relatorio-diario?ok=criado"), "Fluxo criado.", "Dias úteis às 08:00")

	// invalid cron re-renders the form with the error
	rec = e.post(t, "/c/hanko/fluxos/novo", url.Values{"name": {"X"}, "persona": {pid}, "cron": {"99 * * * *"}, "timezone": {"America/Sao_Paulo"}}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, "Agenda inválida")
	// missing persona
	rec = e.post(t, "/c/hanko/fluxos/novo", url.Values{"name": {"X"}, "cron": {"0 8 * * *"}, "timezone": {"UTC"}}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)

	// toggle
	rec = e.post(t, "/c/hanko/fluxos/relatorio-diario/desativar", nil, true)
	wantStatus(t, rec, http.StatusNoContent)
	if g, _ := e.f.GetFlow(context.Background(), e.f.Companies[0].ID, "relatorio-diario"); g.Enabled {
		t.Error("fluxo deveria estar desativado")
	}
	rec = e.post(t, "/c/hanko/fluxos/relatorio-diario/ativar?de=detalhe", nil, false)
	if loc := rec.Header().Get("Location"); loc != "/c/hanko/fluxos/relatorio-diario?ok=ativado" {
		t.Errorf("Location = %q", loc)
	}

	// edit
	rec = e.post(t, "/c/hanko/fluxos/relatorio-diario", url.Values{"name": {"Relatório diário"}, "persona": {pid}, "cron": {"0 9 * * *"}, "timezone": {"UTC"}, "priority": {"high"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	g, _ := e.f.GetFlow(context.Background(), e.f.Companies[0].ID, "relatorio-diario")
	if g.Cron != "0 9 * * *" || g.Enabled {
		t.Errorf("edição não aplicada: %+v", g.Flow)
	}

	// run now creates a task and lands on the history
	before := len(e.f.Tasks)
	rec = e.post(t, "/c/hanko/fluxos/relatorio-diario/executar", nil, true)
	wantStatus(t, rec, http.StatusNoContent)
	if len(e.f.Tasks) != before+1 || !e.f.Called("RunFlowNow") {
		t.Error("Rodar agora deveria criar uma task")
	}
	wantContains(t, e.get(t, "/c/hanko/fluxos/relatorio-diario"), "Histórico de disparos · 1")
	wantStatus(t, e.get(t, "/c/hanko/fluxos/nao-existe"), http.StatusNotFound)
}

func TestCronHintFragment(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	rec := e.get(t, "/c/hanko/fluxos/cron?cron="+url.QueryEscape("0 7 * * 5")+"&timezone=UTC", "HX-Request", "true")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Toda sexta às 07:00", "próximas")
	if strings.Contains(rec.Body.String(), "<html") {
		t.Error("deveria ser fragmento")
	}
	wantContains(t, e.get(t, "/c/hanko/fluxos/cron?cron=bad"), "Agenda inválida")
}
