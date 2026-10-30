package ui

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

// tagged devolve um Fake com 4 tags e a Ada com todas (3 chips e "+1" na lista).
func tagged(t *testing.T) *uitest.Fake {
	t.Helper()
	f := uitest.Sample()
	mk := func(name string, color domain.PersonaTagColor) domain.PersonaTag {
		return domain.PersonaTag{ID: uitest.ID("tag:" + name), Name: name, Color: color}
	}
	tags := []domain.PersonaTag{mk("Backend", domain.TagBlue), mk("Dados", domain.TagGreen), mk("Gestão", domain.TagAmber), mk("Infra", domain.TagRed)}
	f.PersonaTags = tags
	f.Personas[0].Tags = slices.Clone(tags)
	return f
}

// A aba Tags lista o cadastro, cria pelo formulário e mostra o erro de nome repetido na própria aba (ADR-0087 itens 2 a 5).
func TestTagsPage(t *testing.T) {
	f := tagged(t)
	e := newEnv(t, f)
	rec := e.get(t, "/c/hanko/personas/tags")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `class="tag-chip tag-c-blue"`, "Backend", `action="/c/hanko/personas/tags"`, `href="/c/hanko/personas/tags?arquivadas=1"`,
		`action="/c/hanko/tags/`+uitest.ID("tag:Backend").String()+`/arquivar"`, "1 persona perde a tag «Backend»")

	rec = e.post(t, "/c/hanko/personas/tags", url.Values{"name": {"Pesquisa"}, "color": {"teal"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/c/hanko/personas/tags?ok=criada" {
		t.Fatalf("Location = %q", loc)
	}
	rec = e.post(t, "/c/hanko/personas/tags", url.Values{"name": {"backend"}}, false)
	wantStatus(t, rec, http.StatusConflict)
	wantContains(t, rec, `class="alert danger"`, `id="nova-tag"`)
}

// Deletar exige a confirmação; arquivar e editar chamam o service.
func TestTagsPageActions(t *testing.T) {
	f := tagged(t)
	e := newEnv(t, f)
	id := uitest.ID("tag:Dados").String()
	rec := e.post(t, "/c/hanko/tags/"+id+"/deletar", url.Values{}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	if slices.Contains(f.Calls, "DeletePersonaTag") {
		t.Fatal("deletou sem confirmar")
	}
	wantStatus(t, e.post(t, "/c/hanko/tags/"+id+"/deletar", url.Values{"confirm": {"1"}}, false), http.StatusSeeOther)
	wantStatus(t, e.post(t, "/c/hanko/tags/"+uitest.ID("tag:Infra").String()+"/arquivar", url.Values{}, false), http.StatusSeeOther)
	wantStatus(t, e.post(t, "/c/hanko/tags/"+uitest.ID("tag:Infra").String()+"/editar", url.Values{"name": {"Infraestrutura"}, "color": {"red"}}, false), http.StatusSeeOther)
	for _, c := range []string{"DeletePersonaTag", "ArchivePersonaTag", "UpdatePersonaTag"} {
		if !slices.Contains(f.Calls, c) {
			t.Errorf("%s não foi chamado: %v", c, f.Calls)
		}
	}
	rec = e.get(t, "/c/hanko/personas/tags?arquivadas=1")
	wantContains(t, rec, "Infraestrutura", "/desarquivar", "Esconder arquivadas")
	wantNotContains(t, rec, ">Dados<")
}

// Chips no card da lista (3 e "+N") e o cartão da ficha com tirar e o seletor que cria na hora.
func TestPersonaTagsOnPages(t *testing.T) {
	f := tagged(t)
	e := newEnv(t, f)
	rec := e.get(t, "/c/hanko/personas")
	wantContains(t, rec, `class="tag-chip more"`, ">+1<", `href="/c/hanko/personas/tags"`)

	rec = e.get(t, "/c/hanko/personas/ada")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `id="tags"`, `action="/c/hanko/personas/ada/tags"`, `list="tag-apply-options"`, `data-tag-apply="Criar tag «{name}»"`,
		`/c/hanko/personas/ada/tags/`+uitest.ID("tag:Gestão").String()+`/remover`)

	rec = e.post(t, "/c/hanko/personas/hana/tags", url.Values{"name": {"Nova"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/c/hanko/personas/hana?ok=tag") {
		t.Fatalf("Location = %q", loc)
	}
	if !slices.ContainsFunc(f.Personas[1].Tags, func(t domain.PersonaTag) bool { return t.Name == "Nova" }) {
		t.Fatal("a tag nova não foi aplicada à Hana")
	}
	wantStatus(t, e.post(t, "/c/hanko/personas/ada/tags/"+uitest.ID("tag:Gestão").String()+"/remover", url.Values{}, false), http.StatusSeeOther)
	if len(f.Personas[0].Tags) != 3 {
		t.Fatalf("tags da Ada depois de tirar = %d, quer 3", len(f.Personas[0].Tags))
	}
}

// Viewer vê os chips, mas não os formulários de criar, aplicar e tirar.
func TestPersonaTagsViewer(t *testing.T) {
	f := tagged(t)
	f.MyRoleIs = domain.MemberViewer
	e := newEnv(t, f)
	rec := e.get(t, "/c/hanko/personas/ada")
	wantContains(t, rec, `class="tag-chip tag-c-blue"`)
	wantNotContains(t, rec, `action="/c/hanko/personas/ada/tags"`, "/remover")
	rec = e.get(t, "/c/hanko/personas/tags")
	wantNotContains(t, rec, `id="nova-tag"`, "/arquivar", "/deletar")
}

// O organograma entrega o cadastro, as tags de cada nó e o filtro inicial: da URL, ou do cookie hanko_org sem filtro na
// URL (ADR-0087 itens 7 e 8).
func TestOrgChartTagFilter(t *testing.T) {
	f := tagged(t)
	e := newEnv(t, f)
	back := uitest.ID("tag:Backend").String()
	rec := e.get(t, "/c/hanko/organograma?tag="+back+",nao-e-uuid&todas=1&nivel=2,0,x&prof=3&modo=podar")
	wantStatus(t, rec, http.StatusOK)
	body := strings.ReplaceAll(rec.Body.String(), "&#34;", `"`)
	for _, want := range []string{`data-can-tag="1"`, `"tags":["` + back + `"],"all":true`, `"levels":[2],"maxDepth":3,"mode":"prune"`,
		`"name":"Backend","color":"blue"`, `data-tags-href="/c/hanko/personas/tags"`, "Gerenciar tags"} {
		if !strings.Contains(body, want) {
			t.Errorf("organograma sem %q", want)
		}
	}
	rec = e.get(t, "/c/hanko/organograma", "Cookie", "hanko_org="+url.QueryEscape("semtag=1&modo=podar"))
	body = strings.ReplaceAll(rec.Body.String(), "&#34;", `"`)
	if !strings.Contains(body, `"untagged":true`) || !strings.Contains(body, `"mode":"prune"`) {
		t.Error("sem filtro na URL, o organograma deve abrir o filtro do cookie")
	}
	rec = e.get(t, "/c/hanko/organograma?modo=destacar", "Cookie", "hanko_org="+url.QueryEscape("semtag=1"))
	body = strings.ReplaceAll(rec.Body.String(), "&#34;", `"`)
	if strings.Contains(body, `"untagged":true`) {
		t.Error("com filtro na URL, o cookie não vale")
	}
}
