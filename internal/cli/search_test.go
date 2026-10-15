package cli

import (
	"net/http"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
)

var searchResp = api.SearchResult{Query: "deploy", Total: 3, Groups: []api.SearchGroup{
	{Type: "task", More: true, Hits: []api.SearchHit{{Type: "task", Ref: "HNK-1", Status: "todo",
		Title:   []api.SearchSegment{{Text: "Fazer o "}, {Text: "deploy", Match: true}},
		Snippet: []api.SearchSegment{{Text: "ver o "}, {Text: "deploy", Match: true}, {Text: " de sexta"}}}}},
	{Type: "comment", Hits: []api.SearchHit{{Type: "comment", Ref: "HNK-1", TaskKey: "HNK-1", TaskTitle: "Fazer o deploy", Author: "Ada",
		Title: []api.SearchSegment{{Text: "Fazer o deploy"}}, Snippet: []api.SearchSegment{{Text: "falhou"}}}}},
	{Type: "memory", Unavailable: true, Hits: []api.SearchHit{}},
}}

func TestSearchCLI(t *testing.T) {
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{"GET /api/v1/companies/acme/search": jsonResp(200, searchResp)})
	env := map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}

	code, out, errOut := exec(t, Runtime{}, env, "", "search", "deploy", "sexta", "--type", "task,comment", "--type", "memory", "--limit", "5")
	if code != 0 {
		t.Fatalf("%d %s", code, errOut)
	}
	q := f.last().Query
	for _, frag := range []string{"q=deploy+sexta", "types=task%2Ccomment%2Cmemory", "limit=5"} {
		if !strings.Contains(q, frag) {
			t.Errorf("query %q sem %q", q, frag)
		}
	}
	for _, want := range []string{"Tasks (1+)", "HNK-1", "Fazer o deploy", "ver o deploy de sexta", "Comentários (1)", "Fazer o deploy · Ada", "Memória: indisponível agora"} {
		if !strings.Contains(out, want) {
			t.Errorf("saída sem %q:\n%s", want, out)
		}
	}

	// --json devolve o resultado do servidor
	code, out, _ = exec(t, Runtime{}, env, "", "search", "deploy", "--json")
	if code != 0 || !strings.Contains(out, `"groups"`) || !strings.Contains(out, `"match": true`) {
		t.Errorf("json: %d %s", code, out)
	}

	// em inglês
	env["HANKO_LANG"] = "en"
	_, out, _ = exec(t, Runtime{}, env, "", "search", "deploy")
	if !strings.Contains(out, "Comments (1)") || !strings.Contains(out, "Memory: unavailable right now") {
		t.Errorf("inglês:\n%s", out)
	}
}

func TestSearchCLISemResultadoEUso(t *testing.T) {
	ts, _ := newFakeAPI(t, map[string]http.HandlerFunc{"GET /api/v1/companies/acme/search": jsonResp(200, api.SearchResult{Query: "x", Groups: []api.SearchGroup{}})})
	env := map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}
	if code, out, _ := exec(t, Runtime{}, env, "", "search", "x"); code != 0 || !strings.Contains(out, "Nada encontrado.") {
		t.Errorf("%d %q", code, out)
	}
	if code, _, _ := exec(t, Runtime{}, env, "", "search"); code != 2 {
		t.Errorf("sem termo deve ser erro de uso: %d", code)
	}
	if code, _, errOut := exec(t, Runtime{}, map[string]string{"HANKO_URL": ts.URL}, "", "search", "x"); code != 2 || !strings.Contains(errOut, "empresa não informada") {
		t.Errorf("sem empresa: %d %q", code, errOut)
	}
}
