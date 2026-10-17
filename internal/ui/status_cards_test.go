package ui

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

// Cards de status (ADR-0032).

func TestStatusCardsEmptyAndNav(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	rec := e.get(t, "/c/hanko/cards-de-status")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Nenhum card de status ainda — crie um para ter um resumo vivo das tasks que importam.", "Novo card",
		`href="/c/hanko/cards-de-status/novo"`)
	// o item do menu deixou de ser "em breve"
	wantContains(t, e.get(t, "/c/hanko/"), `href="/c/hanko/cards-de-status"`)
	wantNotContains(t, e.get(t, "/c/hanko/"), "/em-breve/cards-status")
}

func TestStatusCardsGrid(t *testing.T) {
	f := uitest.Sample()
	uitest.SampleStatusCards(f)
	e := newEnv(t, f)
	rec := e.get(t, "/c/hanko/cards-de-status")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Bloqueios do Kubo", "Desatualizado", "espera o deploy", "Card sem título", "Montando o seu card…", "Montando",
		"Mostrar arquivados (1)", "Lançamento de agosto", "Atualizar agora", "/arquivar", "/restaurar",
		// há um card montando: a grade se recarrega
		`hx-get="/c/hanko/cards-de-status?parcial=1"`)
	// o fragmento da grade
	frag := e.get(t, "/c/hanko/cards-de-status?parcial=1")
	wantStatus(t, frag, http.StatusOK)
	if strings.Contains(frag.Body.String(), "<html") || !strings.Contains(frag.Body.String(), `id="sc-grid"`) {
		t.Fatalf("fragmento: %.300s", frag.Body.String())
	}
	// "Atualizar agora" da grade volta para a lista
	id := uitest.ID("card:bloqueios").String()
	r := e.post(t, "/c/hanko/cards-de-status/"+id+"/atualizar", url.Values{"de": {"lista"}}, true)
	wantStatus(t, r, http.StatusNoContent)
	if r.Header().Get("HX-Redirect") != "/c/hanko/cards-de-status?ok=atualizando" || f.Cards.LastRefreshFull {
		t.Fatalf("refresh: %v %v", r.Header(), f.Cards.LastRefreshFull)
	}
	// de novo: conflito vira aviso em #flash
	r = e.post(t, "/c/hanko/cards-de-status/"+id+"/atualizar", url.Values{"de": {"lista"}}, true)
	if r.Code != http.StatusConflict || r.Header().Get("HX-Retarget") != "#flash" {
		t.Fatalf("conflito: %d %v", r.Code, r.Header())
	}
}

func TestStatusCardDetailTabs(t *testing.T) {
	f := uitest.Sample()
	id := uitest.SampleStatusCards(f).String()
	e := newEnv(t, f)
	base := "/c/hanko/cards-de-status/" + id
	wantContains(t, e.get(t, base), "Bloqueios do Kubo", "tudo que está bloqueado no projeto Kubo", "versão 2", "Entrou nesta atualização",
		"mudou de status", "Citadas na última atualização", "<h2>Bloqueios</h2>", "Refazer do zero", "1 mudança(s) pendente(s)")
	// versão anterior pelo seletor
	wantContains(t, e.get(t, base+"?aba=resumo&rev=1"), "Primeira versão", "versão 1")
	wantContains(t, e.get(t, base+"?aba=configuracao"), "Só manual", "Em horário fixo, se algo mudou", "Assim que algo mudar (com espera)",
		"O que conta como mudança", "Atualizações por hora", "Teto diário de tokens", "Fuso", `value="reactive" checked`, "Apagar card")
	wantContains(t, e.get(t, base+"?aba=tasks"), "Tasks observadas", "consegue destravar hoje?")
	wantContains(t, e.get(t, base+"?aba=historico"), "Histórico", "Incremental", "5000", "um bloqueio novo", "Montagem", "Falhou", "Ágil_Falcão")
	wantContains(t, e.get(t, base+"?aba=filtros"), "bloqueadas — status: blocked", "prioridade: critical · só abertas", "Montar de novo")
	// fragmento do estado
	live := e.get(t, base+"/estado")
	wantStatus(t, live, http.StatusOK)
	if strings.Contains(live.Body.String(), "<html") || !strings.Contains(live.Body.String(), `id="sc-live"`) || strings.Contains(live.Body.String(), "hx-trigger") {
		t.Fatalf("estado ocioso: %.300s", live.Body.String())
	}
	// montando: polling e mensagem
	busy := e.get(t, "/c/hanko/cards-de-status/"+uitest.ID("card:montando").String())
	wantContains(t, busy, "Montando o seu card…", `hx-trigger="every 3s"`, "Card sem título")
	wantStatus(t, e.get(t, "/c/hanko/cards-de-status/"+domain.NewID().String()), http.StatusNotFound)
	wantStatus(t, e.get(t, "/c/hanko/cards-de-status/nao-uuid"), http.StatusNotFound)
}

func TestStatusCardCreateAndConfigure(t *testing.T) {
	f := uitest.Sample()
	e := newEnv(t, f)
	wantContains(t, e.get(t, "/c/hanko/cards-de-status/novo"), "Novo card de status", "O que acompanhar", "Escolha a resumidora")
	sum := f.Personas[3].ID.String()
	form := url.Values{"interest": {"bugs críticos"}, "summarizer": {sum}, "mode": {"interval"}, "interval_minutes": {"30"},
		"tr_status": {"1"}, "hours_start": {"09:00"}, "hours_end": {"18:00"}, "hours_tz": {"America/Sao_Paulo"}}
	rec := e.post(t, "/c/hanko/cards-de-status/novo", form, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if len(f.Cards.Cards) != 1 {
		t.Fatal("card não criado")
	}
	c := f.Cards.Cards[0]
	if c.Policy.Mode != "interval" || c.Policy.IntervalMinutes != 30 || !c.Policy.Triggers.Status || c.Policy.Triggers.Membership ||
		c.Policy.ActiveHours == nil || c.Policy.ActiveHours.TZ != "America/Sao_Paulo" || c.SummarizerPersonaID == nil {
		t.Fatalf("política: %+v", c.Policy)
	}
	if loc := rec.Header().Get("Location"); loc != "/c/hanko/cards-de-status/"+c.ID.String()+"?ok=criado" {
		t.Fatalf("redirect: %s", loc)
	}
	wantContains(t, e.get(t, "/c/hanko/cards-de-status/"+c.ID.String()+"?ok=criado"), "A resumidora está montando os filtros.")

	// erros de validação voltam no campo
	bad := url.Values{"interest": {"x"}, "mode": {"reactive"}, "debounce_seconds": {"5"}, "tr_status": {"1"}}
	rec = e.post(t, "/c/hanko/cards-de-status/novo", bad, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, "Use de 30 a 3600 segundos.")
	rec = e.post(t, "/c/hanko/cards-de-status/novo", url.Values{"interest": {"x"}, "mode": {"reactive"}}, false)
	wantContains(t, rec, "Marque ao menos um tipo de mudança.")
	rec = e.post(t, "/c/hanko/cards-de-status/novo", url.Values{"interest": {"  "}, "tr_status": {"1"}}, false)
	wantContains(t, rec, "Descreva o que acompanhar")

	// configuração
	edit := url.Values{"interest": {"bugs críticos e bloqueios"}, "title": {"Riscos"}, "title_pinned": {"1"}, "summarizer": {sum}, "mode": {"manual"}, "tr_any": {"1"}}
	rec = e.post(t, "/c/hanko/cards-de-status/"+c.ID.String(), edit, false)
	wantStatus(t, rec, http.StatusSeeOther)
	c = f.Cards.Cards[0]
	if c.Title != "Riscos" || !c.TitlePinned || c.Policy.Mode != "manual" || !c.Policy.Triggers.AnyUpdate || c.Interest != "bugs críticos e bloqueios" {
		t.Fatalf("editado: %+v", c.StatusCard)
	}
	rec = e.post(t, "/c/hanko/cards-de-status/"+c.ID.String(), url.Values{"interest": {"x"}, "mode": {"manual"}, "tr_any": {"1"}, "daily_token_cap": {"5"}}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, "Use de 1.000 a 100.000.000 tokens.", "Configuração")

	// arquivar, restaurar, montar de novo e apagar
	wantStatus(t, e.post(t, "/c/hanko/cards-de-status/"+c.ID.String()+"/arquivar", nil, false), http.StatusSeeOther)
	if f.Cards.Cards[0].ArchivedAt == nil {
		t.Fatal("não arquivou")
	}
	wantContains(t, e.get(t, "/c/hanko/cards-de-status/"+c.ID.String()+"?aba=configuracao"), "Restaure o card para mudar a configuração.", "Arquivado")
	wantStatus(t, e.post(t, "/c/hanko/cards-de-status/"+c.ID.String()+"/restaurar", nil, false), http.StatusSeeOther)
	wantStatus(t, e.post(t, "/c/hanko/cards-de-status/"+c.ID.String()+"/montar", nil, false), http.StatusSeeOther)
	wantStatus(t, e.post(t, "/c/hanko/cards-de-status/"+c.ID.String()+"/excluir", nil, false), http.StatusSeeOther)
	if len(f.Cards.Cards) != 0 {
		t.Fatal("não apagou")
	}
	want := []string{"CreateStatusCard", "UpdateStatusCard", "ArchiveStatusCard", "RestoreStatusCard", "RecompileStatusCard", "DeleteStatusCard"}
	var got []string
	for _, c := range f.Calls {
		if strings.Contains(c, "StatusCard") {
			got = append(got, c)
		}
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("chamadas: %v", got)
	}
}

func TestStatusCardControlsByRole(t *testing.T) {
	for _, role := range allRoles {
		t.Run(string(role), func(t *testing.T) {
			f := uitest.Sample()
			id := uitest.SampleStatusCards(f).String()
			f.MyRoleIs = role
			e := newEnv(t, f)
			list := e.get(t, "/c/hanko/cards-de-status").Body.String()
			detail := e.get(t, "/c/hanko/cards-de-status/"+id).Body.String()
			config := e.get(t, "/c/hanko/cards-de-status/"+id+"?aba=configuracao").Body.String()
			member, admin := role.AtLeast(domain.MemberMember), role.AtLeast(domain.MemberAdmin)
			for name, c := range map[string]struct {
				body, mark string
				want       bool
			}{
				"novo":      {list, `href="/c/hanko/cards-de-status/novo"`, member},
				"atualizar": {detail, "/atualizar", member},
				// os cards de exemplo são do humano anônimo: só admin arquiva
				"arquivar": {detail, "/arquivar", admin},
				"form":     {config, `name="interest"`, member},
				"apagar":   {config, "/excluir", admin},
			} {
				if got := strings.Contains(c.body, c.mark); got != c.want {
					t.Errorf("%s: %q presente=%v, quer %v", name, c.mark, got, c.want)
				}
			}
			// o conteúdo continua legível
			if !strings.Contains(detail, "espera o deploy") {
				t.Error("resumo some para o papel")
			}
		})
	}
}

func TestStatusCardsEnglish(t *testing.T) {
	f := uitest.Sample()
	id := uitest.SampleStatusCards(f).String()
	e := newEnv(t, f)
	en := func(p string) string {
		rec := e.do(t, req{method: "GET", path: p, hdr: map[string]string{"Accept-Language": "en"}})
		wantStatus(t, rec, http.StatusOK)
		return rec.Body.String()
	}
	body := en("/c/hanko/cards-de-status")
	for _, w := range []string{"Status cards", "New card", "Refresh now", "Show archived (1)", "Out of date", "Building your card…"} {
		if !strings.Contains(body, w) {
			t.Errorf("lista em inglês sem %q", w)
		}
	}
	body = en("/c/hanko/cards-de-status/" + id + "?aba=configuracao")
	for _, w := range []string{"Manual only", "At a fixed interval, if something changed", "As soon as something changes (with a wait)", "What counts as a change", "Daily token cap"} {
		if !strings.Contains(body, w) {
			t.Errorf("configuração em inglês sem %q", w)
		}
	}
	if i18n.English()["Montando o seu card…"] == "" {
		t.Error("catálogo sem a montagem")
	}
}
