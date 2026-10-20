package cli

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/api"
)

// Caixa por pessoa, avisos, atividade por ator e árvore de metas na CLI (ADR-0042).

var inboxFixture = api.InboxList{Count: 2, Unread: 1, Archived: 0, Personal: true, Items: []api.InboxItem{
	{Kind: "gate", ID: "11111111-1111-4111-8111-111111111111", Title: "Liberar verba?", TaskKey: "HNK-1", At: time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)},
	{Kind: "mention", ID: "22222222-2222-4222-8222-222222222222", Title: "Revisar contrato", TaskKey: "HNK-2", Read: true, At: time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)},
}}

func TestInboxCLI(t *testing.T) {
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/inbox":        jsonResp(200, inboxFixture),
		"POST /api/v1/companies/acme/inbox/state": jsonResp(200, api.InboxStateResult{Updated: 1}),
	})
	env := map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}

	code, out, errOut := exec(t, Runtime{}, env, "", "inbox", "list", "--view", "unread")
	if code != 0 {
		t.Fatalf("%d %s", code, errOut)
	}
	if q := f.last().Query; q != "view=unread" {
		t.Errorf("query %q", q)
	}
	for _, want := range []string{"gate:11111111-1111-4111-8111-111111111111", "novo", "lido", "HNK-1", "Liberar verba?", "2 aguardando, 1 não lidos, 0 arquivados"} {
		if !strings.Contains(out, want) {
			t.Errorf("saída sem %q:\n%s", want, out)
		}
	}
	if code, out, _ := exec(t, Runtime{}, env, "", "inbox", "list", "--json"); code != 0 || !strings.Contains(out, `"personal": true`) {
		t.Errorf("json: %d %s", code, out)
	}

	code, out, errOut = exec(t, Runtime{}, env, "", "inbox", "archive", "gate:11111111-1111-4111-8111-111111111111", "mention:22222222-2222-4222-8222-222222222222")
	if code != 0 || !strings.Contains(out, "1 item(ns) atualizado(s)") {
		t.Fatalf("archive: %d %s %s", code, out, errOut)
	}
	body := f.last().Body
	for _, frag := range []string{`"action":"archive"`, `"kind":"gate"`, `"kind":"mention"`, "11111111-1111-4111-8111-111111111111"} {
		if !strings.Contains(body, frag) {
			t.Errorf("corpo %q sem %q", body, frag)
		}
	}
	for _, c := range []struct{ cmd, action string }{{"read", "read"}, {"unread", "unread"}, {"unarchive", "unarchive"}} {
		if code, _, _ := exec(t, Runtime{}, env, "", "inbox", c.cmd, "gate:11111111-1111-4111-8111-111111111111"); code != 0 || !strings.Contains(f.last().Body, `"action":"`+c.action+`"`) {
			t.Errorf("%s: %d %s", c.cmd, code, f.last().Body)
		}
	}
	if code, _, _ := exec(t, Runtime{}, env, "", "inbox", "read-all"); code != 0 || !strings.Contains(f.last().Body, `"action":"read_all"`) || strings.Contains(f.last().Body, "items") {
		t.Errorf("read-all: %s", f.last().Body)
	}
	// item sem tipo, e sem item: erro de uso
	if code, _, errOut := exec(t, Runtime{}, env, "", "inbox", "read", "semtipo"); code != 2 || !strings.Contains(errOut, "tipo:id") {
		t.Errorf("item inválido: %d %s", code, errOut)
	}
	if code, _, _ := exec(t, Runtime{}, env, "", "inbox", "archive"); code != 2 {
		t.Errorf("sem item: %d", code)
	}
	// Caixa vazia e inglês
	empty := newEmptyInbox(t)
	if code, out, _ := exec(t, Runtime{}, empty, "", "inbox", "list"); code != 0 || !strings.Contains(out, "A Caixa está vazia.") {
		t.Errorf("vazia: %d %s", code, out)
	}
	env["HANKO_LANG"] = "en"
	if _, out, _ := exec(t, Runtime{}, env, "", "inbox", "list"); !strings.Contains(out, "2 waiting, 1 unread, 0 archived") {
		t.Errorf("inglês:\n%s", out)
	}
}

func newEmptyInbox(t *testing.T) map[string]string {
	ts, _ := newFakeAPI(t, map[string]http.HandlerFunc{"GET /api/v1/companies/acme/inbox": jsonResp(200, api.InboxList{Items: []api.InboxItem{}})})
	return map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}
}

func TestNotifyCLI(t *testing.T) {
	prefs := list(api.NotifyPref{Kind: "mention", Toast: true, Push: true}, api.NotifyPref{Kind: "gate", Toast: true, Push: false})
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/notifications":       jsonResp(200, prefs),
		"PUT /api/v1/companies/acme/notifications/gate":  jsonResp(200, api.NotifyPref{Kind: "gate", Toast: true, Push: true}),
		"POST /api/v1/companies/acme/notifications/test": jsonResp(204, nil),
	})
	env := map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}
	code, out, errOut := exec(t, Runtime{}, env, "", "notify", "prefs")
	if code != 0 || !strings.Contains(out, "mention") || !strings.Contains(out, "ligado") || !strings.Contains(out, "desligado") {
		t.Fatalf("prefs: %d %s %s", code, out, errOut)
	}
	// set liga só o push; o toast fica como está (veio ligado do servidor)
	code, out, errOut = exec(t, Runtime{}, env, "", "notify", "set", "gate", "--push=true")
	if code != 0 || !strings.Contains(out, "gate: toast=true push=true") {
		t.Fatalf("set: %d %s %s", code, out, errOut)
	}
	if b := f.last().Body; !strings.Contains(b, `"push":true`) || !strings.Contains(b, `"toast":true`) {
		t.Errorf("corpo: %s", b)
	}
	if code, out, _ := exec(t, Runtime{}, env, "", "notify", "test"); code != 0 || !strings.Contains(out, "Aviso de teste enviado") {
		t.Errorf("test: %d %s", code, out)
	}
}

func TestActivityCLIAndMineFlags(t *testing.T) {
	acts := list(api.Activity{Verb: "task.created", SubjectType: "task", SubjectID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Actor: api.ActorRef{Type: "human"},
		OccurredAt: time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)})
	tasks := list(api.Task{Key: "HNK-1", Title: "Feita por mim", Status: "todo", Priority: "medium", Participation: []string{"created", "commented"}})
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/activity": jsonResp(200, acts),
		"GET /api/v1/companies/acme/tasks":    jsonResp(200, tasks),
		"GET /api/v1/companies/acme/gates":    jsonResp(200, list[api.Gate]()),
	})
	env := map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}

	code, out, errOut := exec(t, Runtime{}, env, "", "activity", "--actor", "persona:ana", "--subject-type", "task", "--limit", "5")
	if code != 0 || !strings.Contains(out, "task.created") {
		t.Fatalf("activity: %d %s %s", code, out, errOut)
	}
	for _, frag := range []string{"actor=persona%3Aana", "subject_type=task", "limit=5"} {
		if !strings.Contains(f.last().Query, frag) {
			t.Errorf("query %q sem %q", f.last().Query, frag)
		}
	}
	code, out, _ = exec(t, Runtime{}, env, "", "task", "list", "--mine")
	if code != 0 || !strings.Contains(f.last().Query, "mine=true") || !strings.Contains(out, "PARTICIPAÇÃO") || !strings.Contains(out, "created,commented") {
		t.Errorf("task list --mine: %d %s (%s)", code, out, f.last().Query)
	}
	if code, _, _ := exec(t, Runtime{}, env, "", "gate", "list", "--mine"); code != 0 || !strings.Contains(f.last().Query, "mine=true") {
		t.Errorf("gate list --mine: %s", f.last().Query)
	}
}

func TestGoalTreeCLI(t *testing.T) {
	root := api.Goal{Slug: "visao", Title: "Visão", Status: "active", ProgressPercent: 25, TargetValue: 100, Depth: 1, Rollup: true,
		Children: []api.GoalRef{{Slug: "receita", Title: "Receita"}}}
	child := api.Goal{Slug: "receita", Title: "Receita", Status: "active", ProgressPercent: 50, TargetValue: 100, CurrentValue: 50, Depth: 2, ParentSlug: "visao",
		Path: []api.GoalRef{{Slug: "visao", Title: "Visão"}}}
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/goals":           jsonResp(200, list(root, child)),
		"GET /api/v1/companies/acme/goals/visao":     jsonResp(200, root),
		"GET /api/v1/companies/acme/goals/receita":   jsonResp(200, child),
		"POST /api/v1/companies/acme/goals":          jsonResp(201, child),
		"PATCH /api/v1/companies/acme/goals/receita": jsonResp(200, child),
	})
	env := map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}

	code, out, errOut := exec(t, Runtime{}, env, "", "goal", "list")
	if code != 0 || !strings.Contains(out, "└ Receita") {
		t.Fatalf("list em árvore: %d %s %s", code, out, errOut)
	}
	code, out, _ = exec(t, Runtime{}, env, "", "goal", "show", "receita")
	if code != 0 || !strings.Contains(out, "Visão › Receita") {
		t.Errorf("show com cadeia: %d %s", code, out)
	}
	code, out, _ = exec(t, Runtime{}, env, "", "goal", "show", "visao")
	if code != 0 || !strings.Contains(out, "Submetas (1):") || !strings.Contains(out, "receita") {
		t.Errorf("show com submetas: %d %s", code, out)
	}
	if code, _, errOut := exec(t, Runtime{}, env, "", "goal", "create", "Receita", "--target", "100", "--parent", "visao"); code != 0 || !strings.Contains(f.last().Body, `"parent":"visao"`) {
		t.Errorf("create --parent: %d %s %s", code, f.last().Body, errOut)
	}
	if code, _, _ := exec(t, Runtime{}, env, "", "goal", "update", "receita", "--parent", "outra"); code != 0 || !strings.Contains(f.last().Body, `"parent":"outra"`) {
		t.Errorf("update --parent: %s", f.last().Body)
	}
	if code, _, _ := exec(t, Runtime{}, env, "", "goal", "update", "receita", "--clear-parent"); code != 0 || !strings.Contains(f.last().Body, `"clear_parent":true`) {
		t.Errorf("update --clear-parent: %s", f.last().Body)
	}
}
