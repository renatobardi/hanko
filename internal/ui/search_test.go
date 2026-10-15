package ui

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/search"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

func sampleSearch() service.SearchResult {
	at := uitest.Now().Add(-2 * time.Hour)
	run := domain.NewID()
	return service.SearchResult{Query: "deploy", Total: 5, Groups: []service.SearchGroup{
		{Type: service.SearchTask, More: true, Hits: []service.SearchHit{{Type: service.SearchTask, Ref: "HNK-7", Status: "in_progress", At: &at, Exact: true,
			Title:   []search.Segment{{Text: "Fazer o "}, {Text: "deploy", Match: true}, {Text: " <script>alert(1)</script>"}},
			Snippet: []search.Segment{{Text: "…rodar o "}, {Text: "deploy", Match: true}, {Text: " de sexta…"}}}}},
		{Type: service.SearchComment, Hits: []service.SearchHit{{Type: service.SearchComment, Ref: "HNK-7", TaskKey: "HNK-7", Author: "human", At: &at,
			Title: []search.Segment{{Text: "Fazer o deploy"}}, Snippet: []search.Segment{{Text: "o "}, {Text: "deploy", Match: true}, {Text: " falhou"}}}}},
		{Type: service.SearchPersona, Hits: []service.SearchHit{{Type: service.SearchPersona, Ref: "ada", Status: "idle", Title: []search.Segment{{Text: "Ada"}}}}},
		{Type: service.SearchRun, Hits: []service.SearchHit{{Type: service.SearchRun, ID: run, Ref: run.String(), TaskKey: "HNK-7", Persona: "Ada", Status: "done", Title: []search.Segment{{Text: "Corajoso_Falcão"}}}}},
		{Type: service.SearchFlow, Hits: []service.SearchHit{{Type: service.SearchFlow, Ref: "digest", Status: "disabled", Title: []search.Segment{{Text: "Digest"}}}}},
		{Type: service.SearchMemory, Hits: []service.SearchHit{{Type: service.SearchMemory, Ref: "hanko/runs/x.md", Title: []search.Segment{{Text: "Deploy de maio"}}}}},
	}}
}

func TestSearchPage(t *testing.T) {
	f := uitest.Sample()
	f.Memory = true
	f.SearchRes = sampleSearch()
	e := newEnv(t, f)

	// sem consulta: estado inicial, sem chamar o service
	rec := e.get(t, "/c/hanko/buscar")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Busque em tudo da empresa", `id="q-search"`, `hx-get="/c/hanko/buscar"`, `hx-push-url="true"`, `hx-trigger="input changed delay:250ms, search"`,
		`id="search-results"`, "Memória")
	if f.SearchQ != "" {
		t.Errorf("sem q não deve buscar: %q", f.SearchQ)
	}

	// página completa com consulta
	rec = e.get(t, "/c/hanko/buscar?q=deploy")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "<mark>deploy</mark>", "5 resultados para “deploy”", `href="/c/hanko/tasks/HNK-7"`, `href="/c/hanko/personas/ada"`,
		`href="/c/hanko/fluxos/digest"`, "Comentário de Você", "Corajoso_Falcão", "Deploy de maio", "<!doctype html>", `class="hit exact"`)
	// o HTML do usuário é escapado; só o <mark> do app aparece
	wantNotContains(t, rec, "<script>alert(1)</script>")
	wantContains(t, rec, "&lt;script&gt;alert(1)&lt;/script&gt;")
	if f.SearchQ != "deploy" || len(f.SearchF.Types) != 0 {
		t.Errorf("chamada: %q %+v", f.SearchQ, f.SearchF)
	}
	// a memória não tem link
	if strings.Contains(rec.Body.String(), `href="/c/hanko/memoria`) {
		t.Error("memória sem tela própria")
	}

	// htmx: só o fragmento
	rec = e.get(t, "/c/hanko/buscar?q=deploy", "HX-Request", "true")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `id="search-results"`, "<mark>deploy</mark>")
	wantNotContains(t, rec, "<!doctype html>", "<aside")

	// filtro de tipo
	e.get(t, "/c/hanko/buscar?q=deploy&tipo=run")
	if len(f.SearchF.Types) != 1 || f.SearchF.Types[0] != service.SearchRun {
		t.Errorf("tipo: %+v", f.SearchF)
	}
	e.get(t, "/c/hanko/buscar?q=deploy&tipo=bogus") // tipo desconhecido = todos
	if len(f.SearchF.Types) != 0 {
		t.Errorf("tipo inválido deve virar todos: %+v", f.SearchF)
	}
}

func TestSearchEstados(t *testing.T) {
	f := uitest.Sample()
	e := newEnv(t, f)
	// sem resultado
	f.SearchRes = service.SearchResult{Query: "zzz", Groups: []service.SearchGroup{}}
	rec := e.get(t, "/c/hanko/buscar?q=zzz")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Nada encontrado.", "Nenhum resultado para “zzz”")
	// sem memória configurada o filtro não aparece
	wantNotContains(t, rec, "Memória")
	// consulta inválida (só pontuação)
	f.Err = map[string]error{"Search": domain.Invalid("q", "informe ao menos um termo")}
	rec = e.get(t, "/c/hanko/buscar?q=!!!", "HX-Request", "true")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Use ao menos uma palavra")
	// erro de verdade vira a página de erro
	f.Err = map[string]error{"Search": domain.ErrNotFound}
	wantStatus(t, e.get(t, "/c/hanko/buscar?q=x"), http.StatusNotFound)
}

func TestSearchMemoriaForaDoAr(t *testing.T) {
	f := uitest.Sample()
	f.Memory = true
	f.SearchRes = service.SearchResult{Query: "x", Groups: []service.SearchGroup{{Type: service.SearchMemory, Unavailable: true, Hits: []service.SearchHit{}}}}
	rec := newEnv(t, f).get(t, "/c/hanko/buscar?q=x")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "A memória não respondeu agora")
}

func TestSearchNavEAtalho(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	rec := e.get(t, "/c/hanko/")
	wantContains(t, rec, `href="/c/hanko/buscar"`)
	wantNotContains(t, rec, `/em-breve/buscar`)
	wantStatus(t, e.get(t, "/c/hanko/em-breve/buscar"), http.StatusNotFound)

	js, err := os.ReadFile("static/hanko.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`e.key !== "/"`, `getElementById("q-search")`, `a[href$="/buscar"]`} {
		if !strings.Contains(string(js), want) {
			t.Errorf("hanko.js sem o atalho %q", want)
		}
	}
}

func TestSearchEmIngles(t *testing.T) {
	f := uitest.Sample()
	f.SearchRes = sampleSearch()
	e := newEnv(t, f)
	rec := e.get(t, "/c/hanko/buscar?q=deploy", "Accept-Language", "en")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "5 results for “deploy”", "Comments", "Runs", "Flows", "Comment by You")
	rec = e.get(t, "/c/hanko/buscar", "Accept-Language", "en")
	wantContains(t, rec, "Search everything in the company", "Search")
}
