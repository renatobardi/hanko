package ui

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

// Papéis na UI (ADR-0021): cada página mostra os controles que o papel pode usar e não mostra os
// outros. Só UX: o service continua sendo a barreira (o último teste prova que um POST forjado
// ainda volta 403 com a mensagem amigável).

var allRoles = []domain.MemberRole{domain.MemberViewer, domain.MemberMember, domain.MemberAdmin, domain.MemberOwner}

type control struct {
	page string // GET
	mark string // trecho que existe no HTML só quando o papel pode usar o controle
	min  domain.MemberRole
}

func activeRunID(f *uitest.Fake) string {
	for _, r := range f.Runs {
		if r.Status == domain.RunRunning {
			return r.ID.String()
		}
	}
	return ""
}

func TestControlsByRole(t *testing.T) {
	f := uitest.Sample()
	run := activeRunID(f)
	if run == "" {
		t.Fatal("o Sample não tem execução ativa")
	}
	hireDecide := "/gates/" + uitest.ID("gate:hire").String() + "/decidir"
	planDecide := "/gates/" + uitest.ID("gate:plan").String() + "/decidir"
	reviewDecide := "/gates/" + uitest.ID("gate:review").String() + "/decidir"
	M, A := domain.MemberMember, domain.MemberAdmin
	controls := []control{
		// painel
		{"/c/hanko/", `href="/c/hanko/tasks/nova"`, M},
		{"/c/hanko/", `href="/c/hanko/personas/nova"`, A},
		{"/c/hanko/", planDecide, M},
		{"/c/hanko/", reviewDecide, M},
		{"/c/hanko/", hireDecide, A},
		// tasks
		{"/c/hanko/tasks", `href="/c/hanko/tasks/nova"`, M},
		{"/c/hanko/tasks/nova", `action="/c/hanko/tasks/nova"`, M},
		// task
		{"/c/hanko/tasks/HNK-217", `/tasks/HNK-217/comentarios"`, M},
		{"/c/hanko/tasks/HNK-217", `/tasks/HNK-217/acordar"`, M},
		{"/c/hanko/tasks/HNK-218", planDecide, M},
		// execução
		{"/c/hanko/execucoes/" + run, "/cancelar", M},
		// personas
		{"/c/hanko/personas", `href="/c/hanko/personas/nova"`, A},
		{"/c/hanko/personas/nova", `action="/c/hanko/personas/nova"`, A},
		{"/c/hanko/personas/ada", "/personas/ada/pausar", A},
		{"/c/hanko/personas/ada", "/personas/ada/organizacao", A},
		{"/c/hanko/personas/ada", "/personas/ada/revisor", A},
		{"/c/hanko/personas/ada", "/personas/ada/assinatura", A},
		{"/c/hanko/personas/ada", `name="budget"`, A},
		{"/c/hanko/personas/ada", `href="/c/hanko/tasks/nova?persona=`, M},
		{"/c/hanko/organograma", `href="/c/hanko/personas/nova"`, A},
		// custos
		{"/c/hanko/custos", `action="/c/hanko/custos/orcamento"`, A},
		{"/c/hanko/custos", `name="budget"`, A},
		{"/c/hanko/custos", "/custos/assinaturas", A},
		{"/c/hanko/custos", `id="sub-new-name"`, A},
		// fluxos
		{"/c/hanko/fluxos", `href="/c/hanko/fluxos/novo"`, A},
		{"/c/hanko/fluxos", "/executar", A},
		{"/c/hanko/fluxos", `class="switch-form"`, A},
		{"/c/hanko/fluxos/novo", `action="/c/hanko/fluxos/novo"`, A},
		{"/c/hanko/fluxos/digest-semanal", "/executar", A},
		{"/c/hanko/fluxos/digest-semanal", `action="/c/hanko/fluxos/digest-semanal"`, A},
		// gates
		{"/c/hanko/gates/" + uitest.ID("gate:plan").String(), `name="decision"`, M},
		{"/c/hanko/gates/" + uitest.ID("gate:review").String(), `name="decision"`, M},
		{"/c/hanko/gates/" + uitest.ID("gate:hire").String(), `name="decision"`, A},
		// caixa
		{"/c/hanko/caixa", planDecide, M},
		{"/c/hanko/caixa", hireDecide, A},
	}
	for _, role := range allRoles {
		t.Run(string(role), func(t *testing.T) {
			f := uitest.Sample()
			f.MyRoleIs = role
			e := newEnv(t, f)
			bodies := map[string]string{}
			for _, c := range controls {
				body, ok := bodies[c.page]
				if !ok {
					rec := e.get(t, c.page)
					wantStatus(t, rec, http.StatusOK)
					body = rec.Body.String()
					bodies[c.page] = body
				}
				want := role.AtLeast(c.min)
				if got := strings.Contains(body, c.mark); got != want {
					t.Errorf("%s: %q presente=%v, quer %v (papel %s)", c.page, c.mark, got, want, role)
				}
			}
		})
	}
}

// Uma página de caixa/tasks/personas… com dados que acionam os controles condicionais da caixa.
func TestInboxActionsByRole(t *testing.T) {
	for _, role := range allRoles {
		f := uitest.Sample()
		f.MyRoleIs = role
		e := newEnv(t, f)
		body := e.get(t, "/c/hanko/caixa").Body.String()
		for mark, min := range map[string]domain.MemberRole{"/desbloquear": domain.MemberMember, "/caixa/tasks/": domain.MemberMember, "/reativar": domain.MemberAdmin, "Ajustar teto": domain.MemberAdmin} {
			got := strings.Contains(body, mark)
			if got && !role.AtLeast(min) {
				t.Errorf("%s: %q aparece na caixa", role, mark)
			}
			if role == domain.MemberOwner && !got && mark != "/reativar" && mark != "Ajustar teto" {
				t.Errorf("owner: %q deveria aparecer na caixa", mark)
			}
		}
	}
}

func TestReadOnlyHints(t *testing.T) {
	f := uitest.Sample()
	f.MyRoleIs = domain.MemberViewer
	e := newEnv(t, f)
	wantContains(t, e.get(t, "/c/hanko/"), "somente leitura", "requer admin")
	wantContains(t, e.get(t, "/c/hanko/custos"), "requer admin")
	wantContains(t, e.get(t, "/c/hanko/tasks/HNK-217"), "somente leitura")
	wantContains(t, e.get(t, "/c/hanko/gates/"+uitest.ID("gate:plan").String()), "somente leitura")
	wantContains(t, e.get(t, "/c/hanko/tasks/HNK-217"), `class="props-fs" disabled`)
	// o conteúdo continua legível
	wantContains(t, e.get(t, "/c/hanko/tasks/HNK-217"), "Templates de e-mail do digest")
	wantContains(t, e.get(t, "/c/hanko/fluxos"), "Digest semanal de fontes")

	// member: gate de contratação pede admin
	f.MyRoleIs = domain.MemberMember
	wantContains(t, e.get(t, "/c/hanko/gates/"+uitest.ID("gate:hire").String()), "requer admin")
	wantNotContains(t, e.get(t, "/c/hanko/gates/"+uitest.ID("gate:plan").String()), "somente leitura")
	wantNotContains(t, e.get(t, "/c/hanko/tasks/HNK-217"), `class="props-fs" disabled`)

	// owner e admin: nenhum aviso de papel
	for _, r := range []domain.MemberRole{domain.MemberAdmin, domain.MemberOwner} {
		f.MyRoleIs = r
		for _, p := range []string{"/c/hanko/", "/c/hanko/custos", "/c/hanko/fluxos", "/c/hanko/personas/ada"} {
			wantNotContains(t, e.get(t, p), `class="role-note"`)
		}
	}
}

func TestSidebarRoleTag(t *testing.T) {
	for role, want := range map[domain.MemberRole]string{
		domain.MemberViewer: "você: leitor", domain.MemberMember: "você: membro", domain.MemberAdmin: "você: admin", domain.MemberOwner: "você: dono",
	} {
		e, c, _ := membersEnv(t, role)
		rec := e.withCookie(t, c, req{method: "GET", path: "/c/hanko/tasks"})
		wantStatus(t, rec, http.StatusOK)
		wantContains(t, rec, want)
		if role == domain.MemberViewer {
			wantNotContains(t, rec, `href="/c/hanko/tasks/nova"`)
		}
	}
	// modo trusted: sem conta, a barra mostra o dono local e nenhum "você: …"
	e := newEnv(t, uitest.Sample())
	wantNotContains(t, e.get(t, "/c/hanko/tasks"), "você: ")
}

func TestSettingsByRole(t *testing.T) {
	for _, role := range allRoles {
		e, c, _ := membersEnv(t, role)
		rec := e.withCookie(t, c, req{method: "GET", path: "/c/hanko/configuracoes"})
		wantStatus(t, rec, http.StatusOK)
		invite := strings.Contains(rec.Body.String(), `configuracoes/membros/convites"`)
		if invite != role.AtLeast(domain.MemberAdmin) {
			t.Errorf("%s: formulário de convite presente=%v", role, invite)
		}
	}
}

// O service continua sendo a barreira: um POST forjado por quem não vê o botão volta 403 com a
// mensagem amigável (a mesma página de erro, sem vazar o motivo técnico).
func TestForgedPostStillForbidden(t *testing.T) {
	const msg = "Seu papel nesta empresa não permite esta ação."
	gateID := uitest.ID("gate:plan").String()
	cases := []struct {
		name, method, path string
		form               url.Values
	}{
		{"CreateTask", "POST", "/c/hanko/tasks/nova", url.Values{"title": {"x"}}},
		{"AddComment", "POST", "/c/hanko/tasks/HNK-217/comentarios", url.Values{"body": {"oi"}}},
		{"WakeTask", "POST", "/c/hanko/tasks/HNK-217/acordar", nil},
		{"DecideGate", "POST", "/c/hanko/gates/" + gateID + "/decidir", url.Values{"decision": {"approved"}}},
		{"SetCompanyBudget", "POST", "/c/hanko/custos/orcamento", url.Values{"budget": {"10"}}},
		{"RunFlowNow", "POST", "/c/hanko/fluxos/digest-semanal/executar", nil},
	}
	for _, c := range cases {
		for _, htmx := range []bool{false, true} {
			f := uitest.Sample()
			f.MyRoleIs = domain.MemberViewer
			f.SetErr(c.name, domain.ErrForbidden)
			e := newEnv(t, f)
			rec := e.post(t, c.path, c.form, htmx)
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s (htmx=%v): status %d, quer 403\n%.300s", c.name, htmx, rec.Code, rec.Body.String())
				continue
			}
			wantContains(t, rec, msg)
			wantNotContains(t, rec, "papel insuficiente nesta empresa")
		}
	}
}
