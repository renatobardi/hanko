package ui

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/events"
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

func TestOrgChartPersonaGlyph(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	rec := e.get(t, "/c/hanko/organograma")
	wantStatus(t, rec, http.StatusOK)
	// sprite com um símbolo por glifo; SVG estático e ilha desenham por <use>
	wantContains(t, rec, `class="svg-sprite"`, `id="pg-briefcase"`, `id="pg-bot"`, `class="org-glyph" href="#pg-briefcase"`, `href="#pg-cpu"`)
	wantNotContains(t, rec, `class="org-ini"`)
	if !strings.Contains(rec.Body.String(), "icon&#34;:&#34;briefcase") && !strings.Contains(rec.Body.String(), `"icon":"briefcase"`) {
		t.Error("data-nodes da ilha sem o ícone da persona")
	}
}

func TestTaskViewsGroupSortFilter(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	// agrupar por persona: um grupo por nome, ordem alfabética; prioridade filtra
	rec := e.get(t, "/c/hanko/tasks?aba=todas&agrupar=persona&ordem=titulo")
	wantStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	if i, j := strings.Index(body, ">Hana<"), strings.Index(body, ">Kenji<"); i < 0 || j < 0 || i > j {
		t.Errorf("grupos por persona fora de ordem (Hana=%d, Kenji=%d)", i, j)
	}
	if c := rec.Header().Get("Set-Cookie"); !strings.Contains(c, "hanko_tasks=") || !strings.Contains(c, "agrupar=persona") {
		t.Errorf("visão não salva: %q", c)
	}
	// sem parâmetros de visão, o cookie reabre como estava
	rec = e.get(t, "/c/hanko/tasks", "Cookie", "hanko_tasks=agrupar=prioridade&ordem=criada&visao=quadro")
	wantContains(t, rec, `class="board"`, `value="criada" selected`)
	// filtro de prioridade
	rec = e.get(t, "/c/hanko/tasks?aba=todas&prioridade=high&visao=lista")
	wantContains(t, rec, `value="high" selected`, "HNK-219")
	wantNotContains(t, rec, "HNK-217") // média
	// aba Minhas existe; quadro arrastável para quem escreve
	rec = e.get(t, "/c/hanko/tasks?visao=quadro")
	wantContains(t, rec, "aba=minhas", `draggable="true" data-task="HNK-219"`, `data-status="in_progress"`)
}

func TestBoardDropUpdatesStatus(t *testing.T) {
	f := uitest.Sample()
	e := newEnv(t, f)
	rec := e.post(t, "/c/hanko/tasks/HNK-217", url.Values{"status": {"in_review"}}, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestTimeline(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	for _, rg := range []string{"", "?intervalo=hoje", "?intervalo=30d"} {
		rec := e.get(t, "/c/hanko/timeline"+rg)
		wantStatus(t, rec, http.StatusOK)
		wantContains(t, rec, "Timeline", `href="/c/hanko/timeline?intervalo=7d"`, `href="/c/hanko/timeline" aria-current`)
	}
	rec := e.get(t, "/c/hanko/timeline?intervalo=30d")
	wantContains(t, rec, `class="tl-svg"`, "tl-bar st-", `href="#pg-search"`, "/c/hanko/execucoes/")
}

func TestSettingsPlatformPages(t *testing.T) {
	old := lookPath
	lookPath = func(bin string) (string, error) {
		if bin == "claude" || bin == "git" {
			return "/usr/bin/" + bin, nil
		}
		return "", errors.New("não achou")
	}
	t.Cleanup(func() { lookPath = old })
	f := uitest.Sample()
	b := events.NewBroker()
	s := &server{be: f, broker: b, version: "test", now: uitest.Now, sseTick: 50 * time.Millisecond,
		platform: PlatformInfo{Mode: "authenticated", AuthProvider: "firebase", PublicURL: "https://hanko.example", ClaudeBin: "claude", CodexBin: "codex",
			MemoryProvider: "ai-memory", MemoryURL: "http://127.0.0.1:49374", Workers: 4}}
	s.log = testLogger(t)
	mux := http.NewServeMux()
	s.routes(mux)
	e := &env{f: f, broker: b, h: mux}

	rec := e.get(t, "/c/hanko/configuracoes")
	wantContains(t, rec, `href="/c/hanko/configuracoes/ambientes"`, `href="/c/hanko/configuracoes/adapters"`, `href="/c/hanko/configuracoes/plugins"`)

	rec = e.get(t, "/c/hanko/configuracoes/ambientes")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Ambientes de execução", "/usr/bin/claude", "codex não encontrado no PATH do servidor", "Com pendências", "https://hanko.example", "authenticated · login firebase")

	rec = e.get(t, "/c/hanko/configuracoes/adapters")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Claude Code", "Codex", "ACP", "HANKO_ACP_ALLOW desligado", "Ada", "claude-sonnet", "Tabela de preços")

	rec = e.get(t, "/c/hanko/configuracoes/plugins")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Servidor MCP do Hanko", "https://hanko.example/mcp", "ai-memory · http://127.0.0.1:49374", "sem HANKO_SECRET_KEY", "Plugins de terceiros ainda não são suportados")
}

func TestSettingsDataPage(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	rec := e.get(t, "/c/hanko/configuracoes/dados")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `href="/api/v1/companies/hanko/export"`, `download="hanko-hanko-`, `action="/api/v1/companies/import"`, `data-import`, "with_logs")
}

func TestTeamCatalogInstall(t *testing.T) {
	f := uitest.Sample()
	e := newEnv(t, f)
	rec := e.get(t, "/c/hanko/personas/catalogo")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Produto de software", "Conteúdo e marketing", `name="team" value="produto"`, "Tess")
	wantContains(t, e.get(t, "/c/hanko/personas"), `href="/c/hanko/personas/catalogo"`)

	before := len(f.Personas)
	rec = e.post(t, "/c/hanko/personas/catalogo", url.Values{"team": {"produto"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if got := len(f.Personas) - before; got != 4 {
		t.Fatalf("personas criadas = %d, quer 4", got)
	}
	bySlug := map[string]int{}
	for i, p := range f.Personas {
		bySlug[p.Slug] = i
	}
	cto := f.Personas[bySlug["cto"]]
	eng := f.Personas[bySlug["engenharia"]]
	if eng.ReportsTo == nil || *eng.ReportsTo != cto.ID || cto.ReportsTo != nil || eng.Icon != "code" || !eng.RequireReview {
		t.Errorf("organograma/glifo/revisão do time errados: cto=%+v eng=%+v", cto, eng)
	}
	// instalar de novo não colide: slugs ganham sufixo
	wantStatus(t, e.post(t, "/c/hanko/personas/catalogo", url.Values{"team": {"produto"}}, false), http.StatusSeeOther)
	found := false
	for _, p := range f.Personas {
		if p.Slug == "cto-2" {
			found = true
		}
	}
	if !found {
		t.Error("segunda instalação sem slug cto-2")
	}
	wantStatus(t, e.post(t, "/c/hanko/personas/catalogo", url.Values{"team": {"nao-existe"}}, false), http.StatusNotFound)
	wantContains(t, e.get(t, "/c/hanko/personas/catalogo?instalado=produto"), "Time Produto de software instalado")
}

func TestLiveBoard(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	rec := e.get(t, "/c/hanko/ao-vivo")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `id="live"`, `hx-trigger="every 5s"`, "rodando", "Abrir log", "/execucoes/")
	rec = e.get(t, "/c/hanko/ao-vivo", "HX-Request", "true")
	wantContains(t, rec, `id="live"`)
	wantNotContains(t, rec, "<html")
	wantContains(t, e.get(t, "/c/hanko/"), `href="/c/hanko/ao-vivo"`)
}
