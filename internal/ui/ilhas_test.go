package ui

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

// attr extracts the (unescaped) value of a data-* attribute of the first element that carries marker.
func attr(t *testing.T, body, marker, name string) string {
	t.Helper()
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("marker %q not found", marker)
	}
	// the opening tag of the island element spans a few lines; read up to the closing '>' of that tag
	start := strings.LastIndex(body[:i], "<div")
	end := strings.Index(body[i:], ">")
	if start < 0 || end < 0 {
		t.Fatalf("tag around %q not found", marker)
	}
	tag := body[start : i+end]
	m := regexp.MustCompile(name + `="([^"]*)"`).FindStringSubmatch(tag)
	if m == nil {
		t.Fatalf("attribute %s not found in %.200s", name, tag)
	}
	return html.UnescapeString(m[1])
}

func TestIslandRunLog(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	live := e.f.Runs[0]
	rec := e.get(t, "/c/hanko/execucoes/"+live.ID.String())
	wantStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	// mount point with props via data-*, and the no-JS log kept inside it
	marker := `data-island="run-log"`
	if got := attr(t, body, marker, "data-api"); got != "/api/v1/companies/hanko" {
		t.Errorf("data-api = %q", got)
	}
	if got := attr(t, body, marker, "data-run"); got != live.ID.String() {
		t.Errorf("data-run = %q", got)
	}
	if got := attr(t, body, marker, "data-live"); got != "1" {
		t.Errorf("data-live = %q, run em andamento", got)
	}
	var msgs map[string]string
	if err := json.Unmarshal([]byte(attr(t, body, marker, "data-msgs")), &msgs); err != nil {
		t.Fatal(err)
	}
	if msgs["log.search"] != "Buscar no log" || msgs["log.count.filtered"] != "{shown} de {total} eventos" {
		t.Errorf("msgs: %v", msgs)
	}
	wantContains(t, rec, `id="run-log"`, `sse-swap="run-event"`) // fallback sem JS (htmx) continua dentro da ilha
	// a run encerrada monta a ilha sem SSE próprio
	done := e.get(t, "/c/hanko/execucoes/"+e.f.Runs[4].ID.String()).Body.String()
	if got := attr(t, done, marker, "data-live"); got != "0" {
		t.Errorf("data-live = %q numa run encerrada", got)
	}
	// os textos vêm traduzidos do host
	en := e.get(t, "/c/hanko/execucoes/"+live.ID.String(), "Accept-Language", "en").Body.String()
	var enMsgs map[string]string
	if err := json.Unmarshal([]byte(attr(t, en, marker, "data-msgs")), &enMsgs); err != nil {
		t.Fatal(err)
	}
	if enMsgs["log.search"] != "Search the log" || enMsgs["log.older"] != "Load earlier events" {
		t.Errorf("en msgs: %v", enMsgs)
	}
}

func TestIslandOrgChart(t *testing.T) {
	for role, wantEdit := range map[domain.MemberRole]string{
		domain.MemberViewer: "0", domain.MemberMember: "0", domain.MemberAdmin: "1", domain.MemberOwner: "1",
	} {
		f := uitest.Sample()
		f.MyRoleIs = role
		e := newEnv(t, f)
		rec := e.get(t, "/c/hanko/organograma")
		wantStatus(t, rec, http.StatusOK)
		body := rec.Body.String()
		marker := `data-island="org-chart"`
		if got := attr(t, body, marker, "data-can-edit"); got != wantEdit {
			t.Errorf("%s: data-can-edit = %q, quer %q", role, got, wantEdit)
		}
		if got := attr(t, body, marker, "data-api"); got != "/api/v1/companies/hanko" {
			t.Errorf("data-api = %q", got)
		}
		var nodes []struct {
			ID, Slug, Name, Parent, Cls, Href string
		}
		if err := json.Unmarshal([]byte(attr(t, body, marker, "data-nodes")), &nodes); err != nil {
			t.Fatal(err)
		}
		if len(nodes) != 5 {
			t.Fatalf("nodes = %d, quer 5", len(nodes))
		}
		byID := map[string]string{}
		for _, n := range nodes {
			byID[n.ID] = n.Slug
			if n.ID == "" || n.Slug == "" || n.Href != "/c/hanko/personas/"+n.Slug {
				t.Errorf("node incompleto: %+v", n)
			}
		}
		// the parents sent are the reports_to of the chart
		chart, _ := f.OrgChart(context.Background(), domain.ID{})
		want := map[string]string{}
		var walk func(ns []*service.OrgNode, parent string)
		walk = func(ns []*service.OrgNode, parent string) {
			for _, n := range ns {
				want[n.Persona.Slug] = parent
				walk(n.Children, n.Persona.ID.String())
			}
		}
		walk(chart.Roots, "")
		for _, n := range nodes {
			if n.Parent != want[n.Slug] {
				t.Errorf("%s: parent = %q, quer %q", n.Slug, n.Parent, want[n.Slug])
			}
		}
		wantContains(t, rec, `class="org-svg"`) // organograma estático segue dentro da ilha (sem JS)
	}
}

func TestIslandAnchoredComments(t *testing.T) {
	f := uitest.Sample()
	tv := &f.Tasks[0]
	tv.Description = "Faça o login.\r\nUse OAuth com PKCE."
	runID := f.Runs[0].ID
	mk := func(body, quote string, st string, start, end int) domain.Comment {
		return domain.Comment{ID: domain.NewID(), TaskID: tv.ID, Author: domain.HumanActor, Body: body, CreatedAt: uitest.Now(),
			Anchor:      &domain.Anchor{Target: domain.AnchorTaskDescription, Quote: quote, Start: start, End: end},
			AnchorState: &domain.AnchorState{Status: st, Start: start, End: end}}
	}
	f.Comments = append(f.Comments,
		mk("Por que PKCE?", "OAuth com PKCE", domain.AnchorExact, 15, 29),
		mk("E o Google?", "login com Google", domain.AnchorLost, 0, 0),
		domain.Comment{ID: domain.NewID(), TaskID: tv.ID, Author: domain.HumanActor, Body: "comentário sem âncora", CreatedAt: uitest.Now()},
	)
	// a comment anchored to a run summary is not part of the description's anchors
	other := mk("sobre a entrega", "entrega", domain.AnchorExact, 0, 7)
	other.Anchor.Target, other.Anchor.RunID = domain.AnchorRunSummary, &runID
	f.Comments = append(f.Comments, other)

	for role, wantComment := range map[domain.MemberRole]string{domain.MemberViewer: "0", domain.MemberMember: "1"} {
		f.MyRoleIs = role
		e := newEnv(t, f)
		rec := e.get(t, "/c/hanko/tasks/HNK-221")
		wantStatus(t, rec, http.StatusOK)
		body := rec.Body.String()
		marker := `data-island="anchor-editor"`
		for name, want := range map[string]string{
			"data-api": "/api/v1/companies/hanko", "data-task": "HNK-221", "data-target": "task_description", "data-can-comment": wantComment,
			"data-doc": "Faça o login.\nUse OAuth com PKCE.", // fins de linha como o navegador mostra
		} {
			if got := attr(t, body, marker, name); got != want {
				t.Errorf("%s: %s = %q, quer %q", role, name, got, want)
			}
		}
		var anchors []struct{ ID, Quote, Status, Body string }
		if err := json.Unmarshal([]byte(attr(t, body, marker, "data-anchors")), &anchors); err != nil {
			t.Fatal(err)
		}
		if len(anchors) != 2 || anchors[0].Status != "exact" || anchors[1].Status != "lost" || anchors[1].Quote != "login com Google" {
			t.Errorf("anchors: %+v", anchors)
		}
		// the thread (version without JS) shows the citation, and flags the lost anchor
		wantContains(t, rec, `class="msg-quote"`, "OAuth com PKCE", `class="msg-quote is-lost"`, "login com Google", "âncora perdida", "comentário sem âncora")
	}
	// run page: its summary is its own anchored document
	f.MyRoleIs = domain.MemberMember
	e := newEnv(t, f)
	rid := f.Runs[4].ID // finished run with a summary in the sample
	rec := e.get(t, "/c/hanko/execucoes/"+rid.String())
	wantStatus(t, rec, http.StatusOK)
	if f.Runs[4].Summary != "" {
		body := rec.Body.String()
		if got := attr(t, body, `data-island="anchor-editor"`, "data-target"); got != "run_summary" {
			t.Errorf("data-target = %q", got)
		}
		if got := attr(t, body, `data-island="anchor-editor"`, "data-run"); got != rid.String() {
			t.Errorf("data-run = %q", got)
		}
	}
}

// O host serve as ilhas como estáticos same-origin, e o teto de 3 ilhas do ADR-0005 vale no build e no loader.
func TestIslandBundlesAndCeiling(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	entries, err := os.ReadDir("static/islands")
	if err != nil {
		t.Fatalf("rode `make islands`: %v", err)
	}
	var islands []string
	for _, en := range entries {
		if en.IsDir() {
			t.Errorf("subdiretório inesperado em static/islands: %s", en.Name())
			continue
		}
		if !strings.HasPrefix(en.Name(), "vendor-") {
			islands = append(islands, strings.TrimSuffix(en.Name(), ".js"))
		}
		rec := e.get(t, "/static/islands/"+en.Name())
		wantStatus(t, rec, http.StatusOK)
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
			t.Errorf("%s: Content-Type = %q", en.Name(), ct)
		}
		if strings.Contains(rec.Body.String(), "sourceMappingURL") {
			t.Errorf("%s: sem sourcemap no commit", en.Name())
		}
	}
	if len(islands) > 3 {
		t.Errorf("teto de 3 ilhas (ADR-0005): %v", islands)
	}
	// the loader in hanko.js lists exactly the islands that exist
	js, err := os.ReadFile("static/hanko.js")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`var ISLANDS = \{([^}]*)\}`).FindStringSubmatch(string(js))
	if m == nil {
		t.Fatal("ISLANDS não encontrado em hanko.js")
	}
	listed := regexp.MustCompile(`"([a-z-]+)":`).FindAllStringSubmatch(m[1], -1)
	if len(listed) != len(islands) {
		t.Errorf("hanko.js lista %d ilhas, static/islands tem %d (%v)", len(listed), len(islands), islands)
	}
	for _, l := range listed {
		found := false
		for _, n := range islands {
			found = found || n == l[1]
		}
		if !found {
			t.Errorf("hanko.js lista %q, que não existe em static/islands", l[1])
		}
	}
	// scripts same-origin, nada inline: o layout só carrega /static/… e as ilhas entram por import() do hanko.js
	page := e.get(t, "/c/hanko/organograma").Body.String()
	for _, part := range strings.Split(page, "<script")[1:] {
		if !strings.HasPrefix(part, ` src="/static/`) {
			t.Errorf("<script inesperado: %.80s", part)
		}
	}
}
