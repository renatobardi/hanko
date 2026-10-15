package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/search"
	"github.com/renatobardi/hanko/internal/service"
)

// searchRec guarda a última chamada de hanko_search recebida pelo fake.
type searchRec struct {
	company domain.ID
	q       string
	f       service.SearchFilter
	res     service.SearchResult
	err     error
}

func (f *fake) Search(_ context.Context, company domain.ID, q string, fl service.SearchFilter) (service.SearchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.search.company, f.search.q, f.search.f = company, q, fl
	return f.search.res, f.search.err
}

func TestSearchTool(t *testing.T) {
	e := setup(t, false)
	e.f.search.res = service.SearchResult{Query: "deploy", Total: 2, Groups: []service.SearchGroup{
		{Type: service.SearchTask, Hits: []service.SearchHit{{Type: service.SearchTask, Ref: "HNK-1", Exact: true,
			Title:   []search.Segment{{Text: "Fazer o "}, {Text: "deploy", Match: true}},
			Snippet: []search.Segment{{Text: "ver "}, {Text: "deploy", Match: true}, {Text: " <b>x</b>"}}}}, More: true},
		{Type: service.SearchMemory, Unavailable: true, Hits: []service.SearchHit{}},
	}}
	cs, _ := e.connect(t, e.tok)
	defer cs.Close()
	res := call(t, cs, "hanko_search", map[string]any{"query": "deploy", "types": "task, memory", "limit": 5})
	if res.IsError {
		t.Fatal(text(res))
	}
	// o escopo é a empresa do token; o filtro chega ao service
	if e.f.search.company != e.f.company.ID || e.f.search.q != "deploy" || e.f.search.f.Limit != 5 ||
		len(e.f.search.f.Types) != 2 || e.f.search.f.Types[1] != service.SearchMemory {
		t.Errorf("chamada = %+v", e.f.search)
	}
	var out searchOut
	structured(t, res, &out)
	if out.Total != 2 || len(out.Groups) != 2 || out.Groups[0].Hits[0].Title != "Fazer o «deploy»" || !out.Groups[0].More || !out.Groups[1].Unavailable {
		t.Errorf("saída: %+v", out)
	}
	if !strings.Contains(text(res), "## task (1)") || !strings.Contains(text(res), "HNK-1 Fazer o «deploy»") {
		t.Errorf("texto: %s", text(res))
	}

	// empresa de outro lugar, query vazia e tipo inválido são erros
	for _, args := range []map[string]any{{"query": "x", "company": "outra"}, {"query": "  "}, {"query": "x", "types": "bogus"}} {
		if r := call(t, cs, "hanko_search", args); !r.IsError {
			t.Errorf("%v deveria falhar", args)
		}
	}
	// nada encontrado
	e.f.search.res = service.SearchResult{Query: "nada", Groups: []service.SearchGroup{}}
	if r := call(t, cs, "hanko_search", map[string]any{"query": "nada"}); r.IsError || !strings.Contains(text(r), "Nada encontrado") {
		t.Errorf("sem resultado: %s", text(r))
	}
}

func TestSearchToolTrustedPrecisaDeEmpresa(t *testing.T) {
	e := setup(t, true)
	cs, _ := e.connect(t, "")
	defer cs.Close()
	if r := call(t, cs, "hanko_search", map[string]any{"query": "x"}); !r.IsError {
		t.Error("sem token a empresa é obrigatória")
	}
}
