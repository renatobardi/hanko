package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/search"
	"github.com/renatobardi/hanko/internal/service"
)

var searchRec struct {
	company domain.ID
	q       string
	f       service.SearchFilter
}

func (f *fakeSvc) Search(_ context.Context, company domain.ID, q string, fl service.SearchFilter) (service.SearchResult, error) {
	if f.failWith != nil {
		return service.SearchResult{}, f.failWith
	}
	searchRec.company, searchRec.q, searchRec.f = company, q, fl
	at := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	return service.SearchResult{Query: q, Total: 2, Groups: []service.SearchGroup{
		{Type: service.SearchTask, More: true, Hits: []service.SearchHit{{Type: service.SearchTask, ID: domain.NewID(), Ref: "HNK-1", Status: "todo", At: &at,
			Title: []search.Segment{{Text: "Fazer "}, {Text: "deploy", Match: true}}, Snippet: []search.Segment{{Text: "<b>x</b>"}}, Score: 0.5, Exact: true}}},
		{Type: service.SearchMemory, Hits: []service.SearchHit{{Type: service.SearchMemory, Ref: "hanko/x.md", Title: []search.Segment{{Text: "x"}}}}},
	}}, nil
}

func TestSearchEndpoint(t *testing.T) {
	f := &fakeSvc{}
	c := seedCompany(f)
	other := domain.Company{ID: domain.NewID(), Slug: "outra", Name: "Outra"}
	f.companies = append(f.companies, other)
	tokens := &auth.RunTokens{Secret: []byte("0123456789abcdef0123456789abcdef"), TTL: time.Hour}
	ts, _ := newTestServer(t, f, func(d *Deps) { d.Tokens = tokens })

	resp, body := do(t, "GET", ts.URL+"/api/v1/companies/acme/search?q=deploy&types=task,memory&limit=5", "", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("search: %d %s", resp.StatusCode, body)
	}
	var out SearchResult
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if out.Query != "deploy" || out.Total != 2 || len(out.Groups) != 2 || out.Groups[0].Type != "task" || !out.Groups[0].More {
		t.Fatalf("saída: %s", body)
	}
	h := out.Groups[0].Hits[0]
	if h.Ref != "HNK-1" || !h.Exact || len(h.Title) != 2 || !h.Title[1].Match || h.Snippet[0].Text != "<b>x</b>" || h.ID == "" {
		t.Fatalf("hit: %+v", h)
	}
	if out.Groups[1].Hits[0].ID != "" {
		t.Errorf("a memória não tem id: %+v", out.Groups[1].Hits[0])
	}
	if searchRec.company != c.ID || searchRec.f.Limit != 5 || len(searchRec.f.Types) != 2 || searchRec.f.Types[1] != service.SearchMemory {
		t.Errorf("filtro: %+v", searchRec)
	}

	// validações: q obrigatório, tipo desconhecido, limite fora da faixa
	for _, u := range []string{"/search", "/search?q=", "/search?q=x&types=bogus", "/search?q=x&limit=0", "/search?q=x&limit=51"} {
		if resp, body := do(t, "GET", ts.URL+"/api/v1/companies/acme"+u, "", nil); resp.StatusCode != 422 {
			t.Errorf("%s: %d %s", u, resp.StatusCode, body)
		}
	}
	if resp, _ := do(t, "GET", ts.URL+"/api/v1/companies/nao-existe/search?q=x", "", nil); resp.StatusCode != 404 {
		t.Errorf("empresa inexistente: %d", resp.StatusCode)
	}

	// run token: busca na empresa do token, nunca em outra
	rid := domain.NewID()
	f.runs = map[domain.ID]service.RunView{rid: {Run: domain.Run{ID: rid, CompanyID: c.ID, Status: domain.RunRunning}}}
	tok, err := tokens.Issue(c.ID, domain.NewID(), rid, domain.NewID())
	if err != nil {
		t.Fatal(err)
	}
	bearer := map[string]string{"Authorization": "Bearer " + tok}
	if resp, body := do(t, "GET", ts.URL+"/api/v1/companies/acme/search?q=deploy", "", bearer); resp.StatusCode != 200 {
		t.Errorf("persona busca na própria empresa: %d %s", resp.StatusCode, body)
	}
	if resp, _ := do(t, "GET", ts.URL+"/api/v1/companies/outra/search?q=deploy", "", bearer); resp.StatusCode != 403 {
		t.Errorf("persona em outra empresa: %d", resp.StatusCode)
	}
}
