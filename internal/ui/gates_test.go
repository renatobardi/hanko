package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

func gatePath(label string) string { return "/c/hanko/gates/" + uitest.ID("gate:"+label).String() }

func gateOf(f *uitest.Fake, label string) service.GateView {
	for _, g := range f.Gates {
		if g.ID == uitest.ID("gate:"+label) {
			return g
		}
	}
	return service.GateView{}
}

func TestGatesListFilters(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	rec := e.get(t, "/c/hanko/gates")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Aprovar o plano de migração do NAS 3D", "Revisar HNK-220", "Contratar Yuki (Dados)", "3 pendentes", `class="badge warn"`)
	wantNotContains(t, rec, "Publicar o digest semanal", "Fazer merge do coletor")

	rec = e.get(t, "/c/hanko/gates?status=aprovados")
	wantContains(t, rec, "Publicar o digest semanal")
	wantNotContains(t, rec, "Aprovar o plano de migração do NAS 3D")

	rec = e.get(t, "/c/hanko/gates?status=todos&tipo=contratacao")
	wantContains(t, rec, "Contratar Yuki (Dados)")
	wantNotContains(t, rec, "Aprovar o plano de migração do NAS 3D", "Publicar o digest")

	rec = e.get(t, "/c/hanko/gates?status=rejeitados&tipo=revisao")
	wantContains(t, rec, "Nenhum gate aqui.")

	// tabs carry counts for the other dimension
	rec = e.get(t, "/c/hanko/gates?tipo=aprovacao")
	wantContains(t, rec, `href="/c/hanko/gates?status=todos&amp;tipo=aprovacao"`)

	empty := newEnv(t, uitest.Empty())
	wantContains(t, empty.get(t, "/c/hanko/gates"), "Nada pendente agora", "Nenhum gate aqui.")
}

func TestGateDetail(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	rec := e.get(t, gatePath("plan"))
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Aprovar o plano de migração do NAS 3D", "<strong>4 etapas</strong>", "Pedir mudanças", "Rejeitar", "Aprovar",
		`name="note"`, `/gates/`+uitest.ID("gate:plan").String()+`/decidir`, "HNK-218", "Kenji", "Aprovação")
	// decided gate: shows the decision and the note, no form
	rec = e.get(t, gatePath("slides"))
	wantContains(t, rec, "Mudanças pedidas", "Troque o dado do slide 2", "Decisão")
	wantNotContains(t, rec, `name="decision"`)
	wantStatus(t, e.get(t, "/c/hanko/gates/nao-e-id"), http.StatusNotFound)
	wantStatus(t, e.get(t, "/c/hanko/gates/"+uitest.ID("gate:nope").String()), http.StatusNotFound)
}

func TestDecideGate(t *testing.T) {
	decide := func(t *testing.T, label string, form url.Values, htmx bool) (*uitest.Fake, *envRec) {
		t.Helper()
		f := uitest.Sample()
		e := newEnv(t, f)
		return f, &envRec{e: e, rec: e.post(t, gatePath(label)+"/decidir", form, htmx)}
	}
	t.Run("aprovar volta para a página do gate", func(t *testing.T) {
		f, r := decide(t, "plan", url.Values{"decision": {"approved"}, "note": {"Vai em frente."}}, false)
		wantStatus(t, r.rec, http.StatusSeeOther)
		if loc := r.rec.Header().Get("Location"); loc != gatePath("plan")+"?ok=aprovado" {
			t.Errorf("Location = %q", loc)
		}
		g := gateOf(f, "plan")
		if g.Status != domain.GateApproved || g.DecisionNote != "Vai em frente." || g.DecidedBy == nil || g.DecidedBy.Type != domain.ActorHuman {
			t.Errorf("gate = %+v", g.Gate)
		}
		wantContains(t, r.e.get(t, gatePath("plan")+"?ok=aprovado"), "Gate aprovado", "Vai em frente.", "Aprovado")
		// the task left blocked
		for _, tk := range f.Tasks {
			if tk.Key == "HNK-218" && tk.Status != domain.TaskInProgress {
				t.Errorf("task = %s", tk.Status)
			}
		}
	})
	t.Run("htmx redireciona", func(t *testing.T) {
		_, r := decide(t, "plan", url.Values{"decision": {"rejected"}, "next": {"caixa"}}, true)
		wantStatus(t, r.rec, http.StatusNoContent)
		if got := r.rec.Header().Get("HX-Redirect"); got != "/c/hanko/caixa?ok=rejeitado" {
			t.Errorf("HX-Redirect = %q", got)
		}
	})
	t.Run("destinos", func(t *testing.T) {
		for next, want := range map[string]string{"task": "/c/hanko/tasks/HNK-218?ok=mudancas", "painel": "/c/hanko/?ok=mudancas", "caixa": "/c/hanko/caixa?ok=mudancas"} {
			_, r := decide(t, "plan", url.Values{"decision": {"changes_requested"}, "note": {"Troque a etapa 3."}, "next": {next}}, false)
			if loc := r.rec.Header().Get("Location"); loc != want {
				t.Errorf("next=%s Location = %q, want %q", next, loc, want)
			}
		}
		// a gate without a task goes back to its own page
		_, r := decide(t, "hire", url.Values{"decision": {"approved"}, "next": {"task"}}, false)
		if loc := r.rec.Header().Get("Location"); loc != gatePath("hire")+"?ok=aprovado" {
			t.Errorf("Location = %q", loc)
		}
	})
	t.Run("pedir mudanças exige nota", func(t *testing.T) {
		f, r := decide(t, "plan", url.Values{"decision": {"changes_requested"}, "note": {"  "}}, false)
		wantStatus(t, r.rec, http.StatusUnprocessableEntity)
		wantContains(t, r.rec, "nota")
		if f.Called("DecideGate") {
			t.Error("DecideGate chamado sem nota")
		}
	})
	t.Run("decisão inválida", func(t *testing.T) {
		for _, d := range []string{"", "pending", "expired", "xyz"} {
			f, r := decide(t, "plan", url.Values{"decision": {d}}, false)
			wantStatus(t, r.rec, http.StatusUnprocessableEntity)
			if f.Called("DecideGate") {
				t.Errorf("decision %q chegou ao service", d)
			}
		}
	})
	t.Run("já decidido é conflito", func(t *testing.T) {
		_, r := decide(t, "digest", url.Values{"decision": {"approved"}}, false)
		wantStatus(t, r.rec, http.StatusConflict)
	})
	t.Run("inexistente", func(t *testing.T) {
		_, r := decide(t, "nope", url.Values{"decision": {"approved"}}, false)
		wantStatus(t, r.rec, http.StatusNotFound)
	})
	t.Run("htmx com erro vai para o flash", func(t *testing.T) {
		_, r := decide(t, "digest", url.Values{"decision": {"approved"}}, true)
		wantStatus(t, r.rec, http.StatusConflict)
		if r.rec.Header().Get("HX-Retarget") != "#flash" {
			t.Errorf("headers = %v", r.rec.Header())
		}
	})
}

type envRec struct {
	e   *env
	rec *httptest.ResponseRecorder
}

func TestInboxPage(t *testing.T) {
	f := uitest.Sample()
	mika := personaOf(f, "mika")
	mika.Status = domain.PersonaError
	at := time.Now().UTC().Add(-10 * time.Minute)
	f.Health = map[domain.ID]service.PersonaHealth{mika.ID: {LastError: "limite de uso atingido", LastErrorAt: &at, ConsecutiveFailures: 2}}
	e := newEnv(t, f)

	rec := e.get(t, "/c/hanko/caixa")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec,
		"Precisa de você · gates", "Aprovar o plano de migração do NAS 3D", "Contratar Yuki (Dados)", `name="decision" value="approved"`, `name="decision" value="rejected"`,
		"Tasks bloqueadas", "HNK-209", "Voltar para A fazer", `/caixa/tasks/`+uitest.ID("task:HNK-209").String()+`/desbloquear`,
		"Execuções que falharam", "bloqueada por orçamento do projeto LinkedIn", "Acordar de novo",
		"Personas com erro", "limite de uso atingido", "2 falhas seguidas", `/caixa/personas/mika/reativar`,
		"Aguardando", "Envelhecendo", "Decididas hoje", "Histórico de decisões")
	// a blocked task that already has a pending gate is not repeated as "bloqueada"
	body := rec.Body.String()
	if strings.Contains(body, `href="/c/hanko/tasks/HNK-218"`) && strings.Count(body, "Voltar para A fazer") != 1 {
		t.Errorf("HNK-218 aparece como bloqueio sem gate")
	}
	// the inbox does not offer "Pedir mudanças" (it needs a note)
	wantNotContains(t, rec, `value="changes_requested"`)

	rec = e.get(t, "/c/hanko/caixa?aba=falhas")
	wantContains(t, rec, "Execuções que falharam")
	wantNotContains(t, rec, "Precisa de você · gates", "Tasks bloqueadas")
	rec = e.get(t, "/c/hanko/caixa?aba=personas")
	wantContains(t, rec, "Personas com erro")
	wantNotContains(t, rec, "Execuções que falharam")

	wantContains(t, e.get(t, "/c/hanko/caixa?ok=aprovado"), "Gate aprovado")
	wantContains(t, e.get(t, "/c/hanko/caixa?ok=desbloqueou"), "Task devolvida para A fazer")

	// all clear
	clean := newEnv(t, uitest.Empty())
	wantContains(t, clean.get(t, "/c/hanko/caixa"), "Você está em dia.", "Hoje, nada.")
}

func TestInboxAlerts(t *testing.T) {
	f := uitest.Sample()
	at := time.Now().UTC().Add(-5 * time.Minute)
	run := f.Runs[0]
	f.Alerts = []domain.Alert{
		{ID: domain.NewID(), Kind: domain.AlertRunStuck, SubjectType: "run", SubjectID: run.ID, Severity: domain.AlertCritical,
			Message: "Execução HNK-1 (Ada) sem eventos há 20 min (limite 15 min)", OpenedAt: at},
		{ID: domain.NewID(), Kind: domain.AlertPersonaSilent, SubjectType: "persona", SubjectID: domain.NewID(), Severity: domain.AlertWarning,
			Message: "Persona Mika sem heartbeat há 12 min", Data: []byte(`{"persona_slug":"mika"}`), OpenedAt: at},
		{ID: domain.NewID(), Kind: domain.AlertBudgetNearLimit, SubjectType: "company", SubjectID: domain.NewID(), Severity: domain.AlertWarning,
			Message: "Orçamento mensal da empresa em 85%", OpenedAt: at},
	}
	e := newEnv(t, f)
	rec := e.get(t, "/c/hanko/caixa")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Alertas operacionais", "sem eventos há 20 min", "Crítico", "Atenção", "Execução presa", "Persona sem heartbeat",
		"/c/hanko/execucoes/"+run.ID.String(), "/c/hanko/personas/mika", "/c/hanko/custos", "fecha sozinho")
	rec = e.get(t, "/c/hanko/caixa?aba=alertas")
	wantContains(t, rec, "Alertas operacionais")
	wantNotContains(t, rec, "Precisa de você · gates")
	wantNotContains(t, e.get(t, "/c/hanko/caixa?aba=falhas"), "Alertas operacionais")
}

func TestInboxActions(t *testing.T) {
	t.Run("desbloquear", func(t *testing.T) {
		f := uitest.Sample()
		e := newEnv(t, f)
		id := uitest.ID("task:HNK-209").String()
		rec := e.post(t, "/c/hanko/caixa/tasks/"+id+"/desbloquear", nil, true)
		wantStatus(t, rec, http.StatusNoContent)
		if got := rec.Header().Get("HX-Redirect"); got != "/c/hanko/caixa?ok=desbloqueou" {
			t.Errorf("HX-Redirect = %q", got)
		}
		for _, tk := range f.Tasks {
			if tk.Key == "HNK-209" && tk.Status != domain.TaskTodo {
				t.Errorf("status = %s", tk.Status)
			}
		}
		wantStatus(t, e.post(t, "/c/hanko/caixa/tasks/xyz/desbloquear", nil, false), http.StatusNotFound)
	})
	t.Run("acordar", func(t *testing.T) {
		f := uitest.Sample()
		e := newEnv(t, f)
		rec := e.post(t, "/c/hanko/caixa/tasks/"+uitest.ID("task:HNK-222").String()+"/acordar", nil, false)
		wantStatus(t, rec, http.StatusSeeOther)
		if loc := rec.Header().Get("Location"); loc != "/c/hanko/caixa?ok=acordou" {
			t.Errorf("Location = %q", loc)
		}
		if !f.Called("WakeTask") {
			t.Error("WakeTask não chamado")
		}
	})
	t.Run("reativar", func(t *testing.T) {
		f := uitest.Sample()
		personaOf(f, "mika").Status = domain.PersonaError
		e := newEnv(t, f)
		rec := e.post(t, "/c/hanko/caixa/personas/mika/reativar", nil, false)
		wantStatus(t, rec, http.StatusSeeOther)
		if st := personaOf(f, "mika").Status; st != domain.PersonaActive {
			t.Errorf("status = %s", st)
		}
		// not in error → conflict, nothing changes
		wantStatus(t, e.post(t, "/c/hanko/caixa/personas/ada/reativar", nil, false), http.StatusConflict)
		wantStatus(t, e.post(t, "/c/hanko/caixa/personas/nada/reativar", nil, false), http.StatusNotFound)
	})
}

func TestDecisionsPage(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	rec := e.get(t, "/c/hanko/decisoes")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Publicar o digest semanal", "Fazer merge do coletor refatorado na main", "Enviar o rascunho dos slides",
		"Pode publicar. Confira o link do slide 3 antes.", "Ainda sem testes de carga", "Aprovado", "Rejeitado", "Mudanças pedidas", "Você")
	wantNotContains(t, rec, "Aprovar o plano de migração do NAS 3D") // pending, not a decision yet
	// newest decision first
	if i, j := strings.Index(rec.Body.String(), "Publicar o digest semanal"), strings.Index(rec.Body.String(), "Fazer merge do coletor"); i > j {
		t.Error("ordem das decisões")
	}
	rec = e.get(t, "/c/hanko/decisoes?d=rejeitadas")
	wantContains(t, rec, "Fazer merge do coletor")
	wantNotContains(t, rec, "Publicar o digest semanal")
	wantContains(t, newEnv(t, uitest.Empty()).get(t, "/c/hanko/decisoes"), "Nenhuma decisão ainda.")
}

func TestTaskGateBannerAndDashboard(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	rec := e.get(t, "/c/hanko/tasks/HNK-218")
	wantContains(t, rec, `class="gate-banner"`, "Gate pendente", "Aprovar o plano de migração do NAS 3D", "pedido por Kenji", `value="changes_requested"`,
		`name="next" value="task"`)
	wantNotContains(t, e.get(t, "/c/hanko/tasks/HNK-221"), "Gate pendente")
	wantContains(t, e.get(t, "/c/hanko/tasks/HNK-218?ok=rejeitado"), "Gate rejeitado")

	rec = e.get(t, "/c/hanko/")
	wantContains(t, rec, "Decisões pendentes", `class="kpi warn"`, "Aprovar o plano de migração do NAS 3D", `name="next" value="painel"`, "Gates pendentes")
	// without pending gates the block and the amber KPI disappear
	for i := range e.f.Gates {
		e.f.Gates[i].Status = domain.GateApproved
	}
	rec = e.get(t, "/c/hanko/")
	wantNotContains(t, rec, "Decisões pendentes", `class="kpi warn"`)
	wantContains(t, e.get(t, "/c/hanko/?ok=aprovado"), "Gate aprovado")
}

func TestActivityGateVerbs(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	rec := e.get(t, "/c/hanko/atividade")
	wantContains(t, rec, "pediu a decisão", "Aprovar o plano de migração do NAS 3D", "decidiu (aprovado)", `href="`+gatePath("plan")+`"`)
}

func TestOrgPageAndLayout(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	rec := e.get(t, "/c/hanko/organograma")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `class="org-svg"`, `href="/c/hanko/personas/ada"`, `href="/c/hanko/personas/mika"`, "Aguarda gate", "org-node st-gate gate",
		"5 personas em 3 níveis", "Chief of staff", `aria-label="Kenji, Engenheiro de plataforma, Aguarda gate"`)
	wantContains(t, newEnv(t, uitest.Empty()).get(t, "/c/hanko/organograma"), "Nenhuma persona ainda.", "/c/hanko/personas/nova")

	chart, _ := e.f.OrgChart(context.Background(), domain.ID{})
	vm := layoutOrg(chart, "/c/hanko")
	if len(vm.Nodes) != 5 || len(vm.Edges) != 4 {
		t.Fatalf("nodes=%d edges=%d", len(vm.Nodes), len(vm.Edges))
	}
	// no two cards overlap and everything fits in the viewbox
	for i, a := range vm.Nodes {
		if a.X < 0 || a.Y < 0 || a.X+a.W > vm.Width || a.Y+a.H > vm.Height {
			t.Errorf("%s fora do viewBox %dx%d: %+v", a.Name, vm.Width, vm.Height, a)
		}
		for _, b := range vm.Nodes[i+1:] {
			if a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H {
				t.Errorf("%s e %s se sobrepõem", a.Name, b.Name)
			}
		}
	}
	// Ada (root) is centred over her children: Hana, Kenji, Sato
	pos := map[string]int{}
	for _, n := range vm.Nodes {
		pos[n.Name] = n.X
	}
	if mid := (pos["Hana"] + pos["Sato"]) / 2; pos["Ada"] != mid {
		t.Errorf("Ada x=%d, filhos Hana=%d Sato=%d", pos["Ada"], pos["Hana"], pos["Sato"])
	}
	if pos["Mika"] != pos["Kenji"] {
		t.Errorf("Mika (única subordinada) deveria ficar sob Kenji: %d x %d", pos["Mika"], pos["Kenji"])
	}
}

func TestPersonaOrganisation(t *testing.T) {
	t.Run("página mostra reporta a e exige revisão", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		rec := e.get(t, "/c/hanko/personas/kenji")
		wantContains(t, rec, "Reporta a", `href="/c/hanko/personas/ada"`, "Exige revisão", `name="require_review"`, `action="/c/hanko/personas/kenji/organizacao"`, "Equipe", "Mika")
		wantContains(t, e.get(t, "/c/hanko/personas/ada"), "Você (dono)")
		wantContains(t, e.get(t, "/c/hanko/personas"), "reporta a Ada")
		wantContains(t, e.get(t, "/c/hanko/personas/nova"), `name="reports_to"`, `name="require_review"`, "Exige revisão")
	})
	t.Run("editar", func(t *testing.T) {
		f := uitest.Sample()
		e := newEnv(t, f)
		rec := e.post(t, "/c/hanko/personas/mika/organizacao", url.Values{"reports_to": {uitest.ID("persona:ada").String()}, "require_review": {"1"}}, false)
		wantStatus(t, rec, http.StatusSeeOther)
		if loc := rec.Header().Get("Location"); loc != "/c/hanko/personas/mika?ok=organizacao" {
			t.Errorf("Location = %q", loc)
		}
		m := personaOf(f, "mika")
		if m.ReportsTo == nil || *m.ReportsTo != uitest.ID("persona:ada") || !m.RequireReview {
			t.Errorf("mika = %+v", m)
		}
		wantContains(t, e.get(t, "/c/hanko/personas/mika?ok=organizacao"), "Organização atualizada")
		// back to the owner, review off
		wantStatus(t, e.post(t, "/c/hanko/personas/mika/organizacao", url.Values{"reports_to": {""}}, true), http.StatusNoContent)
		if m := personaOf(f, "mika"); m.ReportsTo != nil || m.RequireReview {
			t.Errorf("mika = %+v", m)
		}
	})
	t.Run("ciclo é recusado", func(t *testing.T) {
		f := uitest.Sample()
		e := newEnv(t, f)
		// Ada → reports to Mika (Mika is below Ada)
		rec := e.post(t, "/c/hanko/personas/ada/organizacao", url.Values{"reports_to": {uitest.ID("persona:mika").String()}}, false)
		wantStatus(t, rec, http.StatusUnprocessableEntity)
		if personaOf(f, "ada").ReportsTo != nil {
			t.Error("ciclo aplicado")
		}
		// the select does not even offer the persona itself or its subordinates
		page := e.get(t, "/c/hanko/personas/ada").Body.String()
		sel := page[strings.Index(page, `id="o-reports"`):]
		sel = sel[:strings.Index(sel, "</select>")]
		for _, n := range []string{"Ada", "Kenji", "Mika", "Hana", "Sato"} {
			if strings.Contains(sel, ">"+n+"<") {
				t.Errorf("select de Ada oferece %s", n)
			}
		}
		wantStatus(t, e.post(t, "/c/hanko/personas/ada/organizacao", url.Values{"reports_to": {"lixo"}}, false), http.StatusUnprocessableEntity)
	})
	t.Run("sem mudança não escreve", func(t *testing.T) {
		f := uitest.Sample()
		e := newEnv(t, f)
		rec := e.post(t, "/c/hanko/personas/kenji/organizacao", url.Values{"reports_to": {uitest.ID("persona:ada").String()}, "require_review": {"1"}}, false)
		wantStatus(t, rec, http.StatusSeeOther)
		if f.Called("UpdatePersona") {
			t.Error("UpdatePersona chamado sem mudança")
		}
	})
	t.Run("contratar com superior e revisão", func(t *testing.T) {
		f := uitest.Sample()
		e := newEnv(t, f)
		rec := e.post(t, "/c/hanko/personas/nova", url.Values{"name": {"Yuki Tanaka"}, "agent": {"fake"}, "reports_to": {uitest.ID("persona:ada").String()}, "require_review": {"1"}}, false)
		wantStatus(t, rec, http.StatusSeeOther)
		y := personaOf(f, "yuki-tanaka")
		if y == nil || y.ReportsTo == nil || *y.ReportsTo != uitest.ID("persona:ada") || !y.RequireReview {
			t.Errorf("yuki = %+v", y)
		}
		rec = e.post(t, "/c/hanko/personas/nova", url.Values{"name": {"Zed"}, "agent": {"fake"}, "reports_to": {"lixo"}}, false)
		wantStatus(t, rec, http.StatusUnprocessableEntity)
		wantContains(t, rec, "Superior inválido")
	})
}

func TestShellNavigationActivated(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	rec := e.get(t, "/c/hanko/gates")
	wantContains(t, rec, `href="/c/hanko/caixa"`, `href="/c/hanko/decisoes"`, `href="/c/hanko/organograma"`, `href="/c/hanko/gates" aria-current`)
	wantNotContains(t, rec, `/em-breve/gates`, `/em-breve/caixa`, `/em-breve/decisoes`, `/em-breve/organograma`)
	wantStatus(t, e.get(t, "/c/hanko/em-breve/gates"), http.StatusNotFound)
}

func TestTriggerLabelGate(t *testing.T) {
	if got := triggerLabel(domain.TriggerGate); got != "decisão de gate" {
		t.Errorf("triggerLabel = %q", got)
	}
}

func TestGateRevisaoAutomatica(t *testing.T) {
	f := uitest.Sample()
	id := uitest.ID("gate:plan")
	for i := range f.Gates {
		if f.Gates[i].ID == id {
			f.Gates[i].Review = &domain.GateReview{Reviewer: "Kenji (codex · m)", Verdict: domain.ReviewRejected,
				Justification: "Faltam testes.", ArtifactSHA256: "abc123"}
		}
	}
	e := newEnv(t, f)
	wantContains(t, e.get(t, gatePath("plan")), "Reprovado pelo revisor", "Faltam testes.", "abc123", "A decisão continua sendo sua")
	wantContains(t, e.get(t, "/c/hanko/caixa"), "Reprovado pelo revisor")
	wantNotContains(t, e.get(t, gatePath("slides")), "pelo revisor")
}
