package ui

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/ui/uitest"
)

// Aderência ao Kubo DS: quadro de tasks, glifo por persona, tema claro/escuro e Configurações no rodapé.

func TestTaskBoard(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	rec := e.get(t, "/c/hanko/tasks?visao=quadro")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `class="board"`, "A fazer", "Em andamento", "Em revisão", "Bloqueadas", "HNK-219",
		`name="visao" value="quadro"`, "aba=backlog", "&amp;visao=quadro")
	// A aba "ativas" não tem coluna de backlog nem de concluídas.
	wantNotContains(t, rec, "st-backlog", "st-done")
	// Lista continua o padrão, com o atalho para o quadro.
	rec = e.get(t, "/c/hanko/tasks")
	wantContains(t, rec, `<table class="table">`, "visao=quadro")
	wantNotContains(t, rec, `class="board"`)
	// O fragmento htmx da busca respeita a visão.
	rec = e.get(t, "/c/hanko/tasks?visao=quadro&q=entidades", "HX-Request", "true")
	wantContains(t, rec, `class="board"`, "HNK-219")
}

func TestPersonaIcon(t *testing.T) {
	f := uitest.Sample()
	e := newEnv(t, f)
	rec := e.get(t, "/c/hanko/personas/ada")
	wantContains(t, rec, `value="briefcase" checked`, `action="/c/hanko/personas/ada/icone"`)

	wantStatus(t, e.post(t, "/c/hanko/personas/ada/icone", url.Values{"icon": {"rocket"}}, false), http.StatusSeeOther)
	for _, p := range f.Personas {
		if p.Slug == "ada" && p.Icon != "rocket" {
			t.Errorf("icon = %q, quer rocket", p.Icon)
		}
	}
	if rec := e.post(t, "/c/hanko/personas/ada/icone", url.Values{"icon": {"emoji"}}, false); rec.Code < 400 {
		t.Errorf("ícone fora do catálogo aceito: %d", rec.Code)
	}

	form := url.Values{"name": {"Zed"}, "agent": {"claude"}, "icon": {"code"}}
	wantStatus(t, e.post(t, "/c/hanko/personas/nova", form, false), http.StatusSeeOther)
	found := false
	for _, p := range f.Personas {
		if p.Name == "Zed" {
			found = true
			if p.Icon != "code" {
				t.Errorf("icon = %q, quer code", p.Icon)
			}
		}
	}
	if !found {
		t.Fatal("persona Zed não contratada")
	}
	form.Set("name", "Yan")
	form.Set("icon", "nao-existe")
	wantStatus(t, e.post(t, "/c/hanko/personas/nova", form, false), http.StatusUnprocessableEntity)
}

func TestThemeSwitch(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	rec := e.post(t, "/tema", url.Values{"theme": {"dark"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if c := rec.Header().Get("Set-Cookie"); !strings.Contains(c, "hanko_theme=dark") || !strings.Contains(c, "HttpOnly") {
		t.Errorf("cookie = %q", c)
	}
	rec = e.get(t, "/c/hanko/", "Cookie", "hanko_theme=dark")
	wantContains(t, rec, `data-theme="dark"`, `class="theme-switch"`)
	rec = e.get(t, "/c/hanko/")
	wantNotContains(t, rec, "data-theme=")

	rec = e.post(t, "/tema", url.Values{"theme": {"system"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if c := rec.Header().Get("Set-Cookie"); !strings.Contains(c, "Max-Age=0") {
		t.Errorf("sistema deveria apagar o cookie: %q", c)
	}
	if rec := e.post(t, "/tema", url.Values{"theme": {"roxo"}}, false); rec.Code < 400 {
		t.Errorf("tema desconhecido aceito: %d", rec.Code)
	}
}

func TestSettingsLivesInSidebarFoot(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	rec := e.get(t, "/c/hanko/tasks")
	// fora do grupo Organização: um único link, o do rodapé
	if n := strings.Count(rec.Body.String(), `href="/c/hanko/configuracoes"`); n != 1 {
		t.Errorf("links para Configurações = %d, quer 1 (rodapé)", n)
	}
	wantContains(t, rec, `class="sidebar-foot" href="/c/hanko/configuracoes"`)
}
