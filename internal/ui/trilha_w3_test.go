package ui

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

// Trilha onda 3 (ADR-0061) na UI: assistente de primeiros passos, favoritos e recentes, perfil, novidades.

func locationOf(t *testing.T, e *env, path string, form url.Values) string {
	t.Helper()
	rec := e.post(t, path, form, false)
	wantStatus(t, rec, http.StatusSeeOther)
	return rec.Header().Get("Location")
}

// Empresa nova, sem persona nem run: o assistente começa no harness e o Painel convida a continuar.
func newCompanyEnv() *uitest.Fake {
	f := uitest.Sample()
	f.Personas, f.Runs, f.Tasks = nil, nil, nil
	return f
}

func TestCreatingACompanyLandsOnTheWizard(t *testing.T) {
	e := newEnv(t, uitest.None())
	rec := e.post(t, "/empresas", url.Values{"name": {"Acme"}, "prefix": {"ACM"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); !strings.HasSuffix(loc, "/primeiros-passos") {
		t.Fatalf("Location = %q", loc)
	}
}

func TestWizardWalksFromHarnessToFirstTask(t *testing.T) {
	f := newCompanyEnv()
	e := newEnv(t, f)

	// o Painel convida a continuar
	dash := e.get(t, "/c/hanko/")
	wantStatus(t, dash, http.StatusOK)
	wantContains(t, dash, "Assistente de primeiros passos", `href="/c/hanko/primeiros-passos"`)

	// passo 1: harness
	rec := e.get(t, "/c/hanko/primeiros-passos")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Qual harness vai executar as personas?", `action="/c/hanko/primeiros-passos/harness"`, `value="claude"`, "Pular este passo", "Dispensar o assistente")
	wantNotContains(t, rec, "Contratar uma persona")

	// o probe que falha mantém o passo aberto e mostra o motivo
	f.W3.ProbeStatus = domain.ReadinessNotReady
	if loc := locationOf(t, e, "/c/hanko/primeiros-passos/harness", url.Values{"agent": {"claude"}}); loc != "/c/hanko/primeiros-passos" {
		t.Fatalf("probe ruim volta ao assistente sem aviso de sucesso: %q", loc)
	}
	rec = e.get(t, "/c/hanko/primeiros-passos")
	wantContains(t, rec, "não está pronto", "auth: sem login", "Qual harness vai executar as personas?")

	// o probe que passa fecha o passo
	f.W3.ProbeStatus = domain.ReadinessReady
	if loc := locationOf(t, e, "/c/hanko/primeiros-passos/harness", url.Values{"agent": {"claude"}, "model": {"claude-sonnet-4-5"}}); !strings.Contains(loc, "ok=harness") {
		t.Fatalf("Location = %q", loc)
	}
	rec = e.get(t, "/c/hanko/primeiros-passos?ok=harness")
	wantContains(t, rec, "Harness testado e pronto.", "Contratar uma persona", "Ou instalar um pacote de time", "Instalar", `action="/c/hanko/primeiros-passos/pacote"`)
	wantNotContains(t, rec, "Qual harness vai executar as personas?")

	// passo 2: nome obrigatório mantém o que foi digitado
	rec = e.post(t, "/c/hanko/primeiros-passos/persona", url.Values{"name": {""}, "title": {"CTO"}}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, "Dê um nome à persona.", `value="CTO"`)

	if loc := locationOf(t, e, "/c/hanko/primeiros-passos/persona", url.Values{"name": {"Ada"}, "title": {"CTO"}, "agent": {"claude"}}); !strings.Contains(loc, "ok=persona") {
		t.Fatalf("Location = %q", loc)
	}
	// passo 3: a persona contratada recebe a task
	rec = e.get(t, "/c/hanko/primeiros-passos")
	wantContains(t, rec, "Peça a primeira task", `name="persona"`, "Ada")
	rec = e.post(t, "/c/hanko/primeiros-passos/task", url.Values{"persona": {"nao-e-um-id"}, "title": {"x"}}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	pid := f.Personas[0].ID.String()
	if loc := locationOf(t, e, "/c/hanko/primeiros-passos/task", url.Values{"persona": {pid}, "title": {"Resumir o README"}}); !strings.Contains(loc, "ok=task") {
		t.Fatalf("Location = %q", loc)
	}
	rec = e.get(t, "/c/hanko/primeiros-passos")
	wantContains(t, rec, "Tudo pronto.", `href="/c/hanko/tasks"`)

	// concluído, o Painel não oferece mais o assistente
	wantNotContains(t, e.get(t, "/c/hanko/"), "Assistente de primeiros passos")
}

func TestWizardTeamSkipAndDismiss(t *testing.T) {
	f := newCompanyEnv()
	e := newEnv(t, f)
	if loc := locationOf(t, e, "/c/hanko/primeiros-passos/pular", url.Values{}); !strings.Contains(loc, "ok=skip") {
		t.Fatalf("Location = %q", loc)
	}
	rec := e.get(t, "/c/hanko/primeiros-passos")
	wantContains(t, rec, "pulado", "Contratar uma persona")

	// instala o primeiro pacote do catálogo: a persona de topo fica ativa e o assistente passa à task
	rec = e.get(t, "/c/hanko/primeiros-passos")
	slug := between(rec.Body.String(), `name="slug" value="`, `"`)
	if slug == "" {
		t.Fatal("o catálogo deveria oferecer pacotes")
	}
	if loc := locationOf(t, e, "/c/hanko/primeiros-passos/pacote", url.Values{"slug": {slug}}); !strings.Contains(loc, "ok=team") {
		t.Fatalf("Location = %q", loc)
	}
	rec = e.get(t, "/c/hanko/primeiros-passos")
	wantContains(t, rec, "Peça a primeira task")
	if rec2 := e.post(t, "/c/hanko/primeiros-passos/pacote", url.Values{"slug": {"nao-existe"}}, false); rec2.Code != http.StatusNotFound {
		t.Fatalf("pacote inexistente = %d", rec2.Code)
	}

	// dispensar tira o convite do Painel; retomar devolve
	if loc := locationOf(t, e, "/c/hanko/primeiros-passos/dispensar", url.Values{}); loc != "/c/hanko/" {
		t.Fatalf("Location = %q", loc)
	}
	wantNotContains(t, e.get(t, "/c/hanko/"), "Assistente de primeiros passos")
	wantContains(t, e.get(t, "/c/hanko/primeiros-passos"), "Voltar a oferecer o assistente")
	locationOf(t, e, "/c/hanko/primeiros-passos/retomar", url.Values{})
	wantContains(t, e.get(t, "/c/hanko/"), "Assistente de primeiros passos")
}

func TestWizardRespectsRoles(t *testing.T) {
	f := newCompanyEnv()
	f.MyRoleIs = domain.MemberMember
	e := newEnv(t, f)
	rec := e.get(t, "/c/hanko/primeiros-passos")
	wantStatus(t, rec, http.StatusOK)
	wantNotContains(t, rec, `action="/c/hanko/primeiros-passos/harness"`, "Dispensar o assistente")
	wantNotContains(t, e.get(t, "/c/hanko/"), "Assistente de primeiros passos") // convite só para quem administra
}

func between(s, a, b string) string {
	i := strings.Index(s, a)
	if i < 0 {
		return ""
	}
	s = s[i+len(a):]
	j := strings.Index(s, b)
	if j < 0 {
		return ""
	}
	return s[:j]
}

func TestFavoritesAndRecentsInTheSidebar(t *testing.T) {
	f := uitest.Sample()
	e := newEnv(t, f)
	taskPath := "/c/hanko/tasks/" + f.Tasks[0].Key
	// visitar uma página de detalhe a registra como recente (na visita seguinte aparece na sidebar)
	wantStatus(t, e.get(t, taskPath), http.StatusOK)
	if len(f.W3.Recents) != 1 || f.W3.Recents[0].Path != "/tasks/"+f.Tasks[0].Key {
		t.Fatalf("recentes: %+v", f.W3.Recents)
	}
	rec := e.get(t, "/c/hanko/tasks")
	wantContains(t, rec, "Recentes", `href="`+taskPath+`"`, "Favoritar esta página")
	if len(f.W3.Recents) != 1 {
		t.Fatalf("lista não é detalhe: %+v", f.W3.Recents)
	}

	// favoritar a página atual: a estrela posta o caminho e a sidebar passa a listá-la
	loc := locationOf(t, e, "/c/hanko/favoritos", url.Values{"path": {"/tasks/" + f.Tasks[0].Key}, "title": {"Minha task"}, "favorite": {"1"}})
	if loc != taskPath {
		t.Fatalf("volta para a página: %q", loc)
	}
	rec = e.get(t, "/c/hanko/tasks")
	wantContains(t, rec, "Favoritos", "Minha task")
	rec = e.get(t, taskPath)
	wantContains(t, rec, "Remover dos favoritos", `aria-pressed="true"`)

	locationOf(t, e, "/c/hanko/favoritos", url.Values{"path": {"/tasks/" + f.Tasks[0].Key}, "favorite": {"0"}})
	wantNotContains(t, e.get(t, "/c/hanko/tasks"), `nav-label">Favoritos`)

	// caminho de fora é recusado
	rec = e.post(t, "/c/hanko/favoritos", url.Values{"path": {"https://evil.example/x"}, "favorite": {"1"}}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	// o Painel e as páginas de erro não oferecem estrela
	wantNotContains(t, e.get(t, "/c/hanko/"), "fav-form")
	wantStatus(t, e.get(t, "/c/hanko/tasks/NAO-EXISTE-99"), http.StatusNotFound)
}

func TestProfilePage(t *testing.T) {
	f := uitest.Sample()
	e := newPersonalEnv(t, f)
	// sem conta no Backend, a página explica
	rec := e.get(t, "/c/hanko/perfil")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Sem conta neste modo.")

	f.W3.Account = true
	f.W3.Profile = domain.UserProfile{Email: "ana@x.com", Name: "Ana"}
	rec = e.get(t, "/c/hanko/perfil")
	wantContains(t, rec, `name="name"`, `value="Ana"`, `name="avatar_glyph"`, `name="timezone"`, `name="locale"`, "Violeta", "Seguir o navegador")

	rec = e.post(t, "/c/hanko/perfil", url.Values{"name": {"Ana Souza"}, "avatar_glyph": {"AS"}, "avatar_tone": {"violet"}, "timezone": {"Europe/Lisbon"}, "locale": {"en"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if f.W3.Profile.Name != "Ana Souza" || f.W3.Profile.Timezone != "Europe/Lisbon" || f.W3.Profile.AvatarGlyph != "AS" {
		t.Fatalf("perfil: %+v", f.W3.Profile)
	}
	// o idioma do perfil vira o cookie
	found := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == "hanko_lang" && c.Value == "en" {
			found = true
		}
	}
	if !found {
		t.Errorf("cookie de idioma não gravado: %v", rec.Result().Cookies())
	}
	// o avatar na sidebar usa o glifo e a cor do perfil
	rec = e.get(t, "/c/hanko/tasks")
	wantContains(t, rec, `data-tone="violet"`, ">AS<")

	// erro de validação mantém o que foi digitado
	rec = e.post(t, "/c/hanko/perfil", url.Values{"name": {"Ana"}, "timezone": {"Marte/Olympus"}}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, `value="Marte/Olympus"`)
}

// Sem cookie, o idioma do perfil vale mais que o Accept-Language do navegador.
func TestProfileLocaleBeatsAcceptLanguage(t *testing.T) {
	f := uitest.Sample()
	f.W3.Account = true
	f.W3.Profile = domain.UserProfile{Email: "ana@x.com", Name: "Ana", Locale: "en"}
	e := newPersonalEnv(t, f)
	rec := e.get(t, "/c/hanko/perfil", "Accept-Language", "pt-BR")
	wantContains(t, rec, "My profile")
	// com cookie, o cookie vence
	rec = e.get(t, "/c/hanko/perfil", "Accept-Language", "pt-BR", "Cookie", "hanko_lang=pt-BR")
	wantContains(t, rec, "Meu perfil")
}

func TestWhatsNewCardAndPage(t *testing.T) {
	f := uitest.Sample()
	e := newEnv(t, f)
	dash := e.get(t, "/c/hanko/")
	wantContains(t, dash, "O que há de novo", `href="/c/hanko/novidades"`, `action="/c/hanko/novidades/dispensar"`)

	page := e.get(t, "/c/hanko/novidades")
	wantStatus(t, page, http.StatusOK)
	wantContains(t, page, "Versão instalada: test", "Unreleased") // a versão do teste não está no CHANGELOG: cai no [Unreleased]

	// dispensar some o cartão do Painel, não a página
	if loc := locationOf(t, e, "/c/hanko/novidades/dispensar", url.Values{"key": {"test"}}); loc == "" {
		t.Fatal("sem redirecionamento")
	}
	if f.W3.WhatsSeen != "test" {
		t.Fatalf("dispensa gravada: %q", f.W3.WhatsSeen)
	}
	wantNotContains(t, e.get(t, "/c/hanko/"), `action="/c/hanko/novidades/dispensar"`)
	wantStatus(t, e.get(t, "/c/hanko/novidades"), http.StatusOK)
}
