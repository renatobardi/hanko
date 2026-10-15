package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/google/go-cmp/cmp"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// fake implementa Backend em memória e registra as chamadas de escrita.
type fake struct {
	mu       sync.Mutex
	company  domain.Company
	personas []domain.Persona
	tasks    []service.TaskView
	comments []domain.Comment
	runs     map[domain.ID]service.RunView
	skills   map[domain.ID][]domain.Skill // habilitadas por persona

	lastActor    domain.Actor
	lastCreate   service.CreateTaskInput
	lastUpdate   service.UpdateTaskInput
	lastComment  service.AddCommentInput
	lastFilter   service.TaskFilter
	createdByEvt map[string]service.TaskView
	gates        []service.GateView
	lastGate     service.RequestGateInput
}

func newFake() *fake {
	cid := domain.NewID()
	f := &fake{
		company: domain.Company{ID: cid, Slug: "acme", Name: "Acme", TaskPrefix: "HNK"},
		runs:    map[domain.ID]service.RunView{}, createdByEvt: map[string]service.TaskView{},
	}
	f.personas = []domain.Persona{
		{ID: domain.NewID(), CompanyID: cid, Slug: "cto", Name: "CTO", Title: "Chief", Agent: "claude", Status: domain.PersonaIdle},
		{ID: domain.NewID(), CompanyID: cid, Slug: "eng", Name: "Engenheira", Title: "Eng", Agent: "fake", Status: domain.PersonaActive},
	}
	for i, st := range []domain.TaskStatus{domain.TaskTodo, domain.TaskDone} {
		f.tasks = append(f.tasks, service.TaskView{Task: domain.Task{
			ID: domain.NewID(), CompanyID: cid, Number: int64(i + 1), Key: "HNK-" + string(rune('1'+i)),
			Title: "Tarefa " + string(rune('A'+i)), Status: st, Priority: domain.PriorityMedium, PersonaID: &f.personas[1].ID,
			UpdatedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		}, PersonaName: "Engenheira"})
	}
	return f
}

func (f *fake) GetCompany(_ context.Context, ref string) (domain.Company, error) {
	if ref == f.company.Slug || ref == f.company.ID.String() {
		return f.company, nil
	}
	return domain.Company{}, domain.ErrNotFound
}
func (f *fake) ListPersonas(context.Context, domain.ID) ([]domain.Persona, error) {
	return f.personas, nil
}
func (f *fake) GetPersona(_ context.Context, _ domain.ID, ref string) (domain.Persona, error) {
	for _, p := range f.personas {
		if p.Slug == ref || p.ID.String() == ref {
			return p, nil
		}
	}
	return domain.Persona{}, domain.ErrNotFound
}
func (f *fake) CreateTask(_ context.Context, a domain.Actor, _ domain.ID, in service.CreateTaskInput) (service.TaskView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastActor, f.lastCreate = a, in
	if in.EventID != "" {
		if v, ok := f.createdByEvt[in.EventID]; ok {
			return v, nil
		}
	}
	v := service.TaskView{Task: domain.Task{ID: domain.NewID(), CompanyID: f.company.ID, Key: "HNK-9", Title: in.Title,
		Status: domain.TaskTodo, Priority: domain.PriorityMedium, PersonaID: in.PersonaID, ParentID: in.ParentID}}
	if in.Priority != "" {
		v.Priority = in.Priority
	}
	if in.EventID != "" {
		f.createdByEvt[in.EventID] = v
	}
	f.tasks = append(f.tasks, v)
	return v, nil
}
func (f *fake) ListTasks(_ context.Context, _ domain.ID, fl service.TaskFilter) ([]service.TaskView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastFilter = fl
	return f.tasks, nil
}
func (f *fake) GetTask(_ context.Context, _ domain.ID, ref string) (service.TaskView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.tasks {
		if t.Key == ref || t.ID.String() == ref {
			return t, nil
		}
	}
	return service.TaskView{}, domain.ErrNotFound
}
func (f *fake) UpdateTask(_ context.Context, a domain.Actor, _, id domain.ID, in service.UpdateTaskInput) (service.TaskView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastActor, f.lastUpdate = a, in
	for i := range f.tasks {
		if f.tasks[i].ID == id {
			if in.Status != nil {
				if *in.Status == domain.TaskDone && f.tasks[i].Status == domain.TaskDone {
					return service.TaskView{}, domain.ErrConflict
				}
				f.tasks[i].Status = *in.Status
			}
			return f.tasks[i], nil
		}
	}
	return service.TaskView{}, domain.ErrNotFound
}
func (f *fake) AddComment(_ context.Context, a domain.Actor, _, id domain.ID, in service.AddCommentInput) (domain.Comment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastActor, f.lastComment = a, in
	c := domain.Comment{ID: domain.NewID(), TaskID: id, Author: a, Body: in.Body, RunID: in.RunID, EventID: in.EventID, CreatedAt: time.Now()}
	f.comments = append(f.comments, c)
	return c, nil
}
func (f *fake) ListComments(_ context.Context, _, id domain.ID) ([]domain.Comment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Comment
	for _, c := range f.comments {
		if c.TaskID == id {
			out = append(out, c)
		}
	}
	return out, nil
}
func (f *fake) GetRun(_ context.Context, _, id domain.ID) (service.RunView, error) {
	if r, ok := f.runs[id]; ok {
		return r, nil
	}
	return service.RunView{}, domain.ErrNotFound
}

func (f *fake) RequestGate(_ context.Context, a domain.Actor, _ domain.ID, in service.RequestGateInput) (service.GateView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastActor, f.lastGate = a, in
	for _, g := range f.gates {
		if in.EventID != "" && g.EventID == in.EventID {
			return g, nil
		}
	}
	g := service.GateView{Gate: domain.Gate{ID: domain.NewID(), CompanyID: f.company.ID, Kind: in.Kind, Status: domain.GatePending,
		Title: in.Title, Body: in.Body, TaskID: in.TaskID, RunID: in.RunID, EventID: in.EventID, RequestedBy: a, CreatedAt: time.Now()}, TaskKey: "HNK-1"}
	f.gates = append(f.gates, g)
	return g, nil
}
func (f *fake) GetGate(_ context.Context, _, id domain.ID) (service.GateView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, g := range f.gates {
		if g.ID == id {
			return g, nil
		}
	}
	return service.GateView{}, domain.ErrNotFound
}
func (f *fake) ListGates(_ context.Context, _ domain.ID, fl service.GateFilter) ([]service.GateView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []service.GateView
	for _, g := range f.gates {
		if fl.TaskID != nil && (g.TaskID == nil || *g.TaskID != *fl.TaskID) {
			continue
		}
		if len(fl.Statuses) > 0 && g.Status != fl.Statuses[0] {
			continue
		}
		out = append(out, g)
	}
	return out, nil
}

type env struct {
	f      *fake
	srv    *httptest.Server
	tokens auth.RunTokens
	tok    string
	runID  domain.ID
}

func setup(t *testing.T, trusted bool) *env {
	t.Helper()
	f := newFake()
	tokens := auth.RunTokens{Secret: []byte("0123456789abcdef0123456789abcdef"), TTL: time.Hour}
	e := &env{f: f, tokens: tokens, runID: domain.NewID()}
	taskID := f.tasks[0].ID
	tok, err := tokens.Issue(f.company.ID, f.personas[1].ID, e.runID, taskID)
	if err != nil {
		t.Fatal(err)
	}
	e.tok = tok
	started := time.Now()
	f.runs[e.runID] = service.RunView{Run: domain.Run{ID: e.runID, Status: domain.RunRunning, Trigger: domain.TriggerAssign, Agent: "fake", CreatedAt: started, StartedAt: &started},
		TaskKey: "HNK-1", TaskTitle: "Tarefa A", PersonaName: "Engenheira"}
	e.srv = httptest.NewServer(newHandler(f, Deps{Tokens: tokens, Trusted: trusted, Version: "test"}))
	t.Cleanup(e.srv.Close)
	return e
}

type headerRT struct{ h http.Header }

func (r headerRT) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	for k, v := range r.h {
		req.Header[k] = v
	}
	return http.DefaultTransport.RoundTrip(req)
}

func (e *env) connect(t *testing.T, token string) (*sdk.ClientSession, error) {
	t.Helper()
	hc := &http.Client{}
	if token != "" {
		hc.Transport = headerRT{http.Header{"Authorization": {"Bearer " + token}}}
	}
	c := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "0"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return c.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: e.srv.URL, HTTPClient: hc, DisableStandaloneSSE: true}, nil)
}

func call(t *testing.T, cs *sdk.ClientSession, name string, args any) *sdk.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: erro de protocolo: %v", name, err)
	}
	return res
}

func text(r *sdk.CallToolResult) string {
	var sb strings.Builder
	for _, c := range r.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

func structured(t *testing.T, r *sdk.CallToolResult, v any) {
	t.Helper()
	b, err := json.Marshal(r.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatal(err)
	}
}

func TestListTools(t *testing.T) {
	e := setup(t, false)
	cs, err := e.connect(t, e.tok)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, tl := range res.Tools {
		got = append(got, tl.Name)
		if tl.Description == "" {
			t.Errorf("tool %s sem descrição", tl.Name)
		}
	}
	sort.Strings(got)
	want := []string{"hanko_gate_request", "hanko_gate_status", "hanko_persona_list", "hanko_run_get", "hanko_skill_get", "hanko_skill_list", "hanko_task_comment", "hanko_task_create", "hanko_task_get", "hanko_task_list", "hanko_task_update", "hanko_whoami"}
	if d := cmp.Diff(want, got); d != "" {
		t.Errorf("tools (-want +got):\n%s", d)
	}
}

func TestAuth(t *testing.T) {
	forged, _ := auth.RunTokens{Secret: []byte("another-secret-another-secret!!")}.Issue(domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID())
	tests := []struct {
		name    string
		trusted bool
		token   func(e *env) string
		wantOK  bool
	}{
		{"token válido", false, func(e *env) string { return e.tok }, true},
		{"token inválido", false, func(*env) string { return "lixo" }, false},
		{"token de outro segredo", false, func(*env) string { return forged }, false},
		{"sem token, não trusted", false, func(*env) string { return "" }, false},
		{"sem token, trusted", true, func(*env) string { return "" }, true},
		{"token inválido, trusted", true, func(*env) string { return "lixo" }, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := setup(t, tc.trusted)
			cs, err := e.connect(t, tc.token(e))
			if tc.wantOK {
				if err != nil {
					t.Fatalf("esperava sucesso: %v", err)
				}
				cs.Close()
				return
			}
			if err == nil {
				cs.Close()
				t.Fatal("esperava falha de conexão")
			}
		})
	}
}

func TestAuthStatus401(t *testing.T) {
	e := setup(t, false)
	for _, h := range []string{"", "Basic abc", "Bearer ", "Bearer lixo"} {
		req, _ := http.NewRequest(http.MethodPost, e.srv.URL, strings.NewReader("{}"))
		if h != "" {
			req.Header.Set("Authorization", h)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized || resp.Header.Get("WWW-Authenticate") == "" {
			t.Errorf("header %q: status %d", h, resp.StatusCode)
		}
	}
}

func TestWhoami(t *testing.T) {
	e := setup(t, true)
	cs, _ := e.connect(t, e.tok)
	defer cs.Close()
	res := call(t, cs, "hanko_whoami", map[string]any{})
	var out whoamiOut
	structured(t, res, &out)
	if out.Actor != "persona" || out.PersonaSlug != "eng" || out.CompanySlug != "acme" || out.TaskKey != "HNK-1" || out.RunID != e.runID.String() {
		t.Errorf("whoami = %+v (%s)", out, text(res))
	}

	// trusted sem token
	cs2, _ := e.connect(t, "")
	defer cs2.Close()
	res = call(t, cs2, "hanko_whoami", map[string]any{"company": "acme"})
	structured(t, res, &out)
	if out.Actor != "human" || out.CompanySlug != "acme" {
		t.Errorf("whoami human = %+v", out)
	}
	if res := call(t, cs2, "hanko_whoami", map[string]any{"company": "nope"}); !res.IsError {
		t.Error("empresa inexistente deveria ser erro de tool")
	}
}

func TestTrustedRequiresCompany(t *testing.T) {
	e := setup(t, true)
	cs, _ := e.connect(t, "")
	defer cs.Close()
	res := call(t, cs, "hanko_task_list", map[string]any{})
	if !res.IsError || !strings.Contains(text(res), "company") {
		t.Fatalf("esperava erro pedindo company: %q", text(res))
	}
	res = call(t, cs, "hanko_task_list", map[string]any{"company": "acme"})
	if res.IsError {
		t.Fatalf("erro: %s", text(res))
	}
	var out taskListOut
	structured(t, res, &out)
	if len(out.Tasks) != 2 {
		t.Errorf("tasks = %d", len(out.Tasks))
	}
}

func TestTokenCompanyArgValidated(t *testing.T) {
	e := setup(t, false)
	cs, _ := e.connect(t, e.tok)
	defer cs.Close()
	if res := call(t, cs, "hanko_task_list", map[string]any{"company": "acme"}); res.IsError {
		t.Errorf("company igual à do token deve passar: %s", text(res))
	}
	if res := call(t, cs, "hanko_task_list", map[string]any{"company": e.f.company.ID.String()}); res.IsError {
		t.Errorf("company por id deve passar: %s", text(res))
	}
	res := call(t, cs, "hanko_task_list", map[string]any{"company": "outra"})
	if !res.IsError {
		t.Error("company de outra empresa deve ser erro")
	}
}

func TestTaskList(t *testing.T) {
	e := setup(t, false)
	cs, _ := e.connect(t, e.tok)
	defer cs.Close()
	res := call(t, cs, "hanko_task_list", map[string]any{"status": "todo, in_progress", "persona": "me", "limit": 1000})
	if res.IsError {
		t.Fatal(text(res))
	}
	f := e.f.lastFilter
	if len(f.Statuses) != 2 || f.Statuses[1] != domain.TaskInProgress || f.PersonaID == nil || *f.PersonaID != e.f.personas[1].ID || f.Limit != 200 {
		t.Errorf("filtro = %+v", f)
	}
	if !strings.Contains(text(res), "HNK-1 [todo/medium]") {
		t.Errorf("texto: %s", text(res))
	}
	call(t, cs, "hanko_task_list", map[string]any{"persona": "cto"})
	if e.f.lastFilter.PersonaID == nil || *e.f.lastFilter.PersonaID != e.f.personas[0].ID || e.f.lastFilter.Limit != 50 {
		t.Errorf("filtro por slug = %+v", e.f.lastFilter)
	}
	for _, args := range []map[string]any{{"status": "xyz"}, {"persona": "ninguem"}} {
		if r := call(t, cs, "hanko_task_list", args); !r.IsError {
			t.Errorf("%v deveria falhar", args)
		}
	}
}

func TestTaskListMeWithoutPersona(t *testing.T) {
	e := setup(t, true)
	cs, _ := e.connect(t, "")
	defer cs.Close()
	if r := call(t, cs, "hanko_task_list", map[string]any{"company": "acme", "persona": "me"}); !r.IsError {
		t.Error("persona=me sem persona deveria falhar")
	}
}

func TestTaskGet(t *testing.T) {
	e := setup(t, false)
	cs, _ := e.connect(t, e.tok)
	defer cs.Close()
	for i := 0; i < 25; i++ {
		call(t, cs, "hanko_task_comment", map[string]any{"key": "HNK-1", "body": "c"})
	}
	res := call(t, cs, "hanko_task_get", map[string]any{"key": "HNK-1"})
	var out taskGetOut
	structured(t, res, &out)
	if out.Task.Key != "HNK-1" || len(out.Thread) != 20 {
		t.Errorf("task=%s thread=%d", out.Task.Key, len(out.Thread))
	}
	// por id
	if r := call(t, cs, "hanko_task_get", map[string]any{"key": e.f.tasks[1].ID.String()}); r.IsError {
		t.Error(text(r))
	}
	r := call(t, cs, "hanko_task_get", map[string]any{"key": "HNK-404"})
	if !r.IsError || !strings.Contains(text(r), "não encontrad") {
		t.Errorf("esperava erro de domínio útil: %q", text(r))
	}
	if r := call(t, cs, "hanko_task_get", map[string]any{"key": " "}); !r.IsError {
		t.Error("key vazia deveria falhar")
	}
}

func TestTaskCreate(t *testing.T) {
	e := setup(t, false)
	cs, _ := e.connect(t, e.tok)
	defer cs.Close()
	args := map[string]any{"title": "Revisar PR", "priority": "high", "assignee": "cto", "event_id": "evt-1"}
	res := call(t, cs, "hanko_task_create", args)
	if res.IsError {
		t.Fatal(text(res))
	}
	in := e.f.lastCreate
	if in.Title != "Revisar PR" || in.Priority != domain.PriorityHigh || in.PersonaID == nil || *in.PersonaID != e.f.personas[0].ID || in.EventID != "evt-1" {
		t.Errorf("input = %+v", in)
	}
	if in.ParentID == nil || *in.ParentID != e.f.tasks[0].ID {
		t.Errorf("parent padrão deveria ser a task do run: %v", in.ParentID)
	}
	if e.f.lastActor.Type != domain.ActorPersona || *e.f.lastActor.ID != e.f.personas[1].ID {
		t.Errorf("ator = %+v", e.f.lastActor)
	}
	var first taskWriteOut
	structured(t, res, &first)
	// idempotência: mesma chave devolve a mesma task
	var second taskWriteOut
	structured(t, call(t, cs, "hanko_task_create", args), &second)
	if first.Task.ID != second.Task.ID {
		t.Errorf("event_id repetido criou outra task: %s vs %s", first.Task.ID, second.Task.ID)
	}
	// parent explícito
	call(t, cs, "hanko_task_create", map[string]any{"title": "x", "parent": "HNK-2"})
	if e.f.lastCreate.ParentID == nil || *e.f.lastCreate.ParentID != e.f.tasks[1].ID {
		t.Errorf("parent explícito ignorado")
	}
	for _, bad := range []map[string]any{{"title": ""}, {"title": "x", "priority": "urgentissima"}, {"title": "x", "assignee": "fantasma"}, {"title": "x", "parent": "HNK-404"}} {
		if r := call(t, cs, "hanko_task_create", bad); !r.IsError {
			t.Errorf("%v deveria falhar", bad)
		}
	}
}

func TestTaskCreateTrustedNoParent(t *testing.T) {
	e := setup(t, true)
	cs, _ := e.connect(t, "")
	defer cs.Close()
	if r := call(t, cs, "hanko_task_create", map[string]any{"company": "acme", "title": "Humana"}); r.IsError {
		t.Fatal(text(r))
	}
	if e.f.lastCreate.ParentID != nil || e.f.lastActor.Type != domain.ActorHuman {
		t.Errorf("parent=%v ator=%+v", e.f.lastCreate.ParentID, e.f.lastActor)
	}
}

func TestTaskUpdate(t *testing.T) {
	e := setup(t, false)
	cs, _ := e.connect(t, e.tok)
	defer cs.Close()
	res := call(t, cs, "hanko_task_update", map[string]any{"key": "HNK-1", "status": "in_review", "priority": "low", "assignee": "cto", "event_id": "u-1"})
	if res.IsError {
		t.Fatal(text(res))
	}
	u := e.f.lastUpdate
	if u.Status == nil || *u.Status != domain.TaskInReview || *u.Priority != domain.PriorityLow || u.PersonaID == nil || u.EventID != "u-1" || u.Unassign {
		t.Errorf("update = %+v", u)
	}
	call(t, cs, "hanko_task_update", map[string]any{"key": "HNK-1", "assignee": "none"})
	if !e.f.lastUpdate.Unassign || e.f.lastUpdate.PersonaID != nil {
		t.Errorf("unassign = %+v", e.f.lastUpdate)
	}
	// erro de domínio (conflito) vira erro de tool
	r := call(t, cs, "hanko_task_update", map[string]any{"key": "HNK-2", "status": "done"})
	if !r.IsError || !strings.Contains(text(r), "conflito") {
		t.Errorf("esperava conflito: %q", text(r))
	}
	for _, bad := range []map[string]any{{"key": "HNK-1"}, {"key": "HNK-1", "status": "x"}, {"key": "HNK-1", "priority": "x"}, {"key": "HNK-1", "assignee": "fantasma"}, {"key": "HNK-404", "status": "todo"}, {"status": "todo", "key": ""}} {
		if r := call(t, cs, "hanko_task_update", bad); !r.IsError {
			t.Errorf("%v deveria falhar", bad)
		}
	}
}

func TestTaskComment(t *testing.T) {
	e := setup(t, false)
	cs, _ := e.connect(t, e.tok)
	defer cs.Close()
	res := call(t, cs, "hanko_task_comment", map[string]any{"key": "HNK-1", "body": "feito", "event_id": "c-1"})
	if res.IsError {
		t.Fatal(text(res))
	}
	c := e.f.lastComment
	if c.Body != "feito" || c.EventID != "c-1" || c.RunID == nil || *c.RunID != e.runID {
		t.Errorf("comment = %+v", c)
	}
	for _, bad := range []map[string]any{{"key": "HNK-1", "body": " "}, {"key": "", "body": "x"}, {"key": "HNK-404", "body": "x"}} {
		if r := call(t, cs, "hanko_task_comment", bad); !r.IsError {
			t.Errorf("%v deveria falhar", bad)
		}
	}
}

func TestPersonaList(t *testing.T) {
	e := setup(t, false)
	cs, _ := e.connect(t, e.tok)
	defer cs.Close()
	var out personaListOut
	structured(t, call(t, cs, "hanko_persona_list", map[string]any{}), &out)
	if len(out.Personas) != 2 || out.Personas[0].Slug != "cto" {
		t.Errorf("personas = %+v", out.Personas)
	}
}

func TestRunGet(t *testing.T) {
	e := setup(t, false)
	cs, _ := e.connect(t, e.tok)
	defer cs.Close()
	var out runGetOut
	structured(t, call(t, cs, "hanko_run_get", map[string]any{}), &out)
	if out.Run.ID != e.runID.String() || out.Run.Status != "running" || out.Run.TaskKey != "HNK-1" {
		t.Errorf("run = %+v", out.Run)
	}
	if r := call(t, cs, "hanko_run_get", map[string]any{"id": domain.NewID().String()}); !r.IsError {
		t.Error("run inexistente deveria falhar")
	}
	if r := call(t, cs, "hanko_run_get", map[string]any{"id": "nao-uuid"}); !r.IsError {
		t.Error("id inválido deveria falhar")
	}
}

func TestRunGetTrustedNeedsID(t *testing.T) {
	e := setup(t, true)
	cs, _ := e.connect(t, "")
	defer cs.Close()
	if r := call(t, cs, "hanko_run_get", map[string]any{"company": "acme"}); !r.IsError {
		t.Error("sem id e sem token deveria falhar")
	}
	if r := call(t, cs, "hanko_run_get", map[string]any{"company": "acme", "id": e.runID.String()}); r.IsError {
		t.Error(text(r))
	}
}

func TestErrorHelpers(t *testing.T) {
	for _, err := range []error{domain.ErrNotFound, domain.Invalid("f", "r"), domain.ErrConflict, domain.ErrActiveRun, domain.ErrNotAllowed, errors.New("boom"), service.ErrNotImplemented} {
		if opErr("op", err) == nil || toolErr("x", "y", err) == nil {
			t.Error("erro nulo")
		}
	}
}

func TestHandlerWithService(t *testing.T) {
	// Handler(Deps) com *service.Service nil não deve panicar na construção.
	h := Handler(Deps{Trusted: true})
	if h == nil {
		t.Fatal("nil")
	}
}

func TestAuthRunInativo(t *testing.T) {
	for _, st := range []domain.RunStatus{domain.RunDone, domain.RunFailed, domain.RunCancelled, domain.RunInterrupted, domain.RunExpired} {
		e := setup(t, false)
		r := e.f.runs[e.runID]
		r.Status = st
		e.f.runs[e.runID] = r
		req, _ := http.NewRequest(http.MethodPost, e.srv.URL, strings.NewReader("{}"))
		req.Header.Set("Authorization", "Bearer "+e.tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("run %s: status %d, quer 401", st, resp.StatusCode)
		}
	}
	// queued ainda vale
	e := setup(t, false)
	r := e.f.runs[e.runID]
	r.Status = domain.RunQueued
	e.f.runs[e.runID] = r
	cs, err := e.connect(t, e.tok)
	if err != nil {
		t.Fatalf("queued deveria valer: %v", err)
	}
	cs.Close()
}

func TestGateRequestAndStatus(t *testing.T) {
	e := setup(t, false)
	cs, _ := e.connect(t, e.tok)
	defer cs.Close()
	var out gateWriteOut
	res := call(t, cs, "hanko_gate_request", map[string]any{"title": "Publicar release", "body": "impacto", "event_id": "g-1"})
	if res.IsError {
		t.Fatal(text(res))
	}
	structured(t, res, &out)
	in := e.f.lastGate
	if out.Gate.Status != "pending" || in.Kind != domain.GateApproval || in.TaskID == nil || *in.TaskID != e.f.tasks[0].ID || in.RunID == nil || *in.RunID != e.runID || in.EventID != "g-1" {
		t.Errorf("out=%+v in=%+v", out, in)
	}
	if e.f.lastActor.Type != domain.ActorPersona {
		t.Errorf("actor = %+v", e.f.lastActor)
	}
	var again gateWriteOut
	structured(t, call(t, cs, "hanko_gate_request", map[string]any{"title": "Publicar release", "event_id": "g-1"}), &again)
	if again.Gate.ID != out.Gate.ID {
		t.Error("event_id deveria ser idempotente")
	}
	// pendentes da task do token
	var st gateStatusOut
	structured(t, call(t, cs, "hanko_gate_status", map[string]any{}), &st)
	if len(st.Gates) != 1 || st.Gates[0].ID != out.Gate.ID {
		t.Errorf("status = %+v", st)
	}
	// por id
	structured(t, call(t, cs, "hanko_gate_status", map[string]any{"id": out.Gate.ID}), &st)
	if len(st.Gates) != 1 || st.Gates[0].Title != "Publicar release" {
		t.Errorf("por id = %+v", st)
	}
	for _, bad := range []map[string]any{{"title": " "}, {"title": "x", "kind": "hire"}, {"title": "x", "task": "HNK-404"}} {
		if r := call(t, cs, "hanko_gate_request", bad); !r.IsError {
			t.Errorf("%v deveria falhar", bad)
		}
	}
	for _, bad := range []map[string]any{{"id": "nope"}, {"id": domain.NewID().String()}} {
		if r := call(t, cs, "hanko_gate_status", bad); !r.IsError {
			t.Errorf("%v deveria falhar", bad)
		}
	}
}

func TestGateStatusTrustedNeedsTask(t *testing.T) {
	e := setup(t, true)
	cs, _ := e.connect(t, "")
	defer cs.Close()
	if r := call(t, cs, "hanko_gate_status", map[string]any{"company": "acme"}); !r.IsError {
		t.Error("sem id nem task deveria falhar")
	}
	var st gateStatusOut
	structured(t, call(t, cs, "hanko_gate_status", map[string]any{"company": "acme", "task": "HNK-1"}), &st)
	if len(st.Gates) != 0 {
		t.Errorf("gates = %+v", st.Gates)
	}
}
