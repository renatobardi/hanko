//go:build integration

package api_test

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Log virtualizado (ilha run-log): a primeira carga lê o fim (tail), o histórico vem por cursor de seq para trás
// (before) e o que chega depois vem por after; as três janelas juntas cobrem o log sem perder nem repetir.
func TestAPIEventosPorCursor(t *testing.T) {
	e := newAPIEnv(t)
	e.seedEventsRun(e.co, 30, 20)
	var runs api.ListBody[api.Run]
	e.want(200, "GET", e.base()+"/runs", "", &runs)
	if len(runs.Items) != 1 {
		t.Fatalf("runs: %d", len(runs.Items))
	}
	ev := e.base() + "/runs/" + runs.Items[0].ID + "/events"
	page := func(q string) []int64 {
		t.Helper()
		var out api.ListBody[api.RunEvent]
		e.want(200, "GET", ev+q, "", &out)
		seqs := make([]int64, len(out.Items))
		for i, it := range out.Items {
			seqs[i] = it.Seq
		}
		return seqs
	}
	first := func(s []int64) int64 {
		if len(s) == 0 {
			return -1
		}
		return s[0]
	}
	last := func(s []int64) int64 {
		if len(s) == 0 {
			return -1
		}
		return s[len(s)-1]
	}

	tail := page("?tail=true&limit=10")
	if len(tail) != 10 || first(tail) != 21 || last(tail) != 30 {
		t.Fatalf("tail: %v", tail)
	}
	seen := map[int64]bool{}
	for _, s := range tail {
		seen[s] = true
	}
	cursor := first(tail)
	for cursor > 1 {
		older := page("?before=" + strconv.FormatInt(cursor, 10) + "&limit=10")
		if len(older) == 0 || last(older) != cursor-1 {
			t.Fatalf("before %d: %v", cursor, older)
		}
		for _, s := range older {
			if seen[s] {
				t.Fatalf("seq %d repetido", s)
			}
			seen[s] = true
		}
		cursor = first(older)
	}
	if len(seen) != 30 {
		t.Fatalf("cobertura: %d de 30", len(seen))
	}
	if got := page("?before=1"); len(got) != 0 {
		t.Fatalf("antes do primeiro: %v", got)
	}
	if got := page("?after=28"); len(got) != 2 || first(got) != 29 {
		t.Fatalf("after: %v", got)
	}
	if got := page("?tail=true&limit=1000"); len(got) != 30 || first(got) != 1 {
		t.Fatalf("tail inteiro: %v", got)
	}
	// janelas para frente e para trás não se combinam
	if status, b := e.call("GET", ev+"?tail=true&after=5", "", nil); status != http.StatusUnprocessableEntity {
		t.Fatalf("tail+after: %d %s", status, b)
	}
}

// Comentário ancorado pela API: citação validada contra o documento, estado devolvido na leitura e
// "âncora perdida" quando o trecho some da descrição.
func TestAPIComentarioAncorado(t *testing.T) {
	e := newAPIEnv(t)
	p := e.persona(e.co, "Eng", nil)
	tv := e.task(e.co, "Login", &p, func(in *service.CreateTaskInput) { in.Description = "Implemente o login.\nUse OAuth com PKCE." })
	cm := e.base() + "/tasks/" + tv.Key + "/comments"

	var c api.Comment
	e.want(201, "POST", cm, `{"body":"Por que PKCE?","anchor":{"target":"task_description","quote":"OAuth com PKCE","start":0,"end":0}}`, &c)
	if c.Anchor == nil || c.Anchor.Quote != "OAuth com PKCE" || c.Anchor.Start != 24 || c.Anchor.End != 38 || c.Anchor.DocHash == "" {
		t.Fatalf("âncora canônica: %+v", c.Anchor)
	}
	if c.AnchorState == nil || c.AnchorState.Status != "exact" {
		t.Fatalf("estado: %+v", c.AnchorState)
	}
	for name, body := range map[string]string{
		"trecho inexistente": `{"body":"x","anchor":{"target":"task_description","quote":"não está lá","start":0,"end":0}}`,
		"alvo desconhecido":  `{"body":"x","anchor":{"target":"outro","quote":"login","start":0,"end":0}}`,
		"run sem id":         `{"body":"x","anchor":{"target":"run_summary","quote":"login","start":0,"end":0}}`,
	} {
		if status, b := e.call("POST", cm, body, nil); status != http.StatusUnprocessableEntity {
			t.Errorf("%s: status %d, quer 422: %s", name, status, b)
		}
	}
	var list api.ListBody[api.Comment]
	e.want(200, "GET", cm, "", &list)
	if len(list.Items) != 1 {
		t.Fatalf("só o comentário válido fica: %d", len(list.Items))
	}

	// a descrição muda e o trecho some: âncora perdida (o comentário continua na thread)
	e.want(200, "PATCH", e.base()+"/tasks/"+tv.Key, `{"description":"Reescrita total."}`, nil)
	e.want(200, "GET", cm, "", &list)
	if len(list.Items) != 1 || list.Items[0].AnchorState == nil || list.Items[0].AnchorState.Status != domain.AnchorLost {
		t.Fatalf("âncora perdida: %+v", list.Items)
	}
}

// Organograma interativo: arrastar uma persona para outra é PATCH reports_to; o ciclo é recusado (422) e
// "ao topo" é clear_reports_to. A ilha usa só este endpoint.
func TestAPIMoverNoOrganograma(t *testing.T) {
	e := newAPIEnv(t)
	ps := e.base() + "/personas"
	var ceo, cto, eng api.Persona
	e.want(201, "POST", ps, `{"name":"Ceo","agent":"fake","model":"m"}`, &ceo)
	e.want(201, "POST", ps, `{"name":"Cto","agent":"fake","model":"m","reports_to":"ceo"}`, &cto)
	e.want(201, "POST", ps, `{"name":"Eng","agent":"fake","model":"m","reports_to":"cto"}`, &eng)

	var got api.Persona
	e.want(200, "PATCH", ps+"/eng", `{"reports_to":"ceo"}`, &got)
	if got.ReportsTo != ceo.ID {
		t.Fatalf("mover para o CEO: %+v", got)
	}
	if status, b := e.call("PATCH", ps+"/ceo", `{"reports_to":"eng"}`, nil); status != http.StatusUnprocessableEntity {
		t.Fatalf("ciclo direto: %d %s", status, b)
	}
	e.want(200, "PATCH", ps+"/cto", `{"reports_to":"eng"}`, &got) // cto sob eng (que reporta a ceo) é válido
	if status, b := e.call("PATCH", ps+"/ceo", `{"reports_to":"cto"}`, nil); status != http.StatusUnprocessableEntity {
		t.Fatalf("ciclo indireto: %d %s", status, b)
	}
	e.want(200, "PATCH", ps+"/eng", `{"clear_reports_to":true}`, &got)
	if got.ReportsTo != "" {
		t.Fatalf("ao topo: %+v", got)
	}
}
