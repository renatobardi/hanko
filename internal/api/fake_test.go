package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/export"
	"github.com/renatobardi/hanko/internal/service"
)

// fakeSvc implementa só o que cada teste usa; o resto cai no nil embutido
// (panic → o recover do middleware responde 500).
type fakeSvc struct {
	backend

	mu        sync.Mutex
	companies []domain.Company
	personas  []domain.Persona
	tasks     []service.TaskView
	runs      map[domain.ID]service.RunView
	events    map[domain.ID][]domain.RunEvent

	lastActor   domain.Actor
	lastTaskIn  service.CreateTaskInput
	lastFilter  service.TaskFilter
	lastComment service.AddCommentInput
	lastRunF    service.RunFilter
	failWith    error
	// resolves conta as resoluções com efeito colateral (renovam sessão / último uso); checks, as sem efeito
	resolves, checks atomic.Int64

	gates         []service.GateView
	lastGateIn    service.RequestGateInput
	lastGateF     service.GateFilter
	lastDecide    service.DecideGateInput
	lastPersonaUp service.UpdatePersonaInput
	m4            costFlowFake // custos e fluxos
	chatF         *chatFake    // conversas (chat_test.go)
}

func (f *fakeSvc) GetCompany(_ context.Context, ref string) (domain.Company, error) {
	if f.failWith != nil {
		return domain.Company{}, f.failWith
	}
	for _, c := range f.companies {
		if c.Slug == ref || c.ID.String() == ref {
			return c, nil
		}
	}
	return domain.Company{}, domain.ErrNotFound
}

func (f *fakeSvc) ListCompanies(context.Context) ([]domain.Company, error) { return f.companies, nil }

func (f *fakeSvc) CreateCompany(_ context.Context, a domain.Actor, in service.CreateCompanyInput) (domain.Company, error) {
	f.lastActor = a
	if in.Name == "dup" {
		return domain.Company{}, domain.ErrConflict
	}
	c := domain.Company{ID: domain.NewID(), Slug: domain.Slugify(in.Name), Name: in.Name, TaskPrefix: "HNK", Timezone: "America/Sao_Paulo"}
	f.companies = append(f.companies, c)
	return c, nil
}

func (f *fakeSvc) GetPersona(_ context.Context, cid domain.ID, ref string) (domain.Persona, error) {
	for _, p := range f.personas {
		if p.CompanyID == cid && (p.Slug == ref || p.ID.String() == ref) {
			return p, nil
		}
	}
	return domain.Persona{}, domain.ErrNotFound
}

func (f *fakeSvc) ListPersonas(_ context.Context, cid domain.ID) ([]domain.Persona, error) {
	return f.personas, nil
}

func (f *fakeSvc) GetTask(_ context.Context, cid domain.ID, ref string) (service.TaskView, error) {
	for _, t := range f.tasks {
		if t.CompanyID == cid && (t.Key == ref || t.ID.String() == ref) {
			return t, nil
		}
	}
	return service.TaskView{}, domain.ErrNotFound
}

func (f *fakeSvc) CreateTask(_ context.Context, a domain.Actor, cid domain.ID, in service.CreateTaskInput) (service.TaskView, error) {
	f.lastActor, f.lastTaskIn = a, in
	t := service.TaskView{Task: domain.Task{ID: domain.NewID(), CompanyID: cid, Number: int64(len(f.tasks) + 1),
		Key: domain.TaskKey("HNK", int64(len(f.tasks)+1)), Title: in.Title, Status: domain.TaskTodo, Priority: domain.PriorityMedium,
		PersonaID: in.PersonaID, CreatedBy: a}}
	f.tasks = append(f.tasks, t)
	return t, nil
}

func (f *fakeSvc) ListTasks(_ context.Context, cid domain.ID, fl service.TaskFilter) ([]service.TaskView, error) {
	f.lastFilter = fl
	return f.tasks, nil
}

func (f *fakeSvc) AddComment(_ context.Context, a domain.Actor, cid, tid domain.ID, in service.AddCommentInput) (domain.Comment, error) {
	f.lastActor, f.lastComment = a, in
	return domain.Comment{ID: domain.NewID(), CompanyID: cid, TaskID: tid, Author: a, Body: in.Body, RunID: in.RunID}, nil
}

func (f *fakeSvc) WakeTask(_ context.Context, a domain.Actor, cid, tid domain.ID) error {
	f.lastActor = a
	return nil
}

func (f *fakeSvc) ListRuns(_ context.Context, cid domain.ID, fl service.RunFilter) ([]service.RunView, error) {
	f.lastRunF = fl
	var out []service.RunView
	for _, r := range f.runs {
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeSvc) GetRun(_ context.Context, cid, rid domain.ID) (service.RunView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.runs[rid]; ok && r.CompanyID == cid {
		return r, nil
	}
	return service.RunView{}, domain.ErrNotFound
}

func (f *fakeSvc) ListRunEvents(_ context.Context, cid, rid domain.ID, after int64, limit int) ([]domain.RunEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.RunEvent
	for _, e := range f.events[rid] {
		if e.Seq > after && len(out) < limit {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *fakeSvc) setStatus(rid domain.ID, st domain.RunStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.runs[rid]
	r.Status = st
	f.runs[rid] = r
}

func (f *fakeSvc) addEvent(rid domain.ID, seq int64, text string) domain.RunEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	e := domain.RunEvent{ID: domain.NewID(), RunID: rid, Seq: seq, Kind: domain.EventAssistant, Text: text}
	f.events[rid] = append(f.events[rid], e)
	return e
}

// newTestServer monta o servidor com o fake e devolve o httptest.Server.
func newTestServer(t *testing.T, f *fakeSvc, mod func(*Deps)) (*httptest.Server, *server) {
	t.Helper()
	d := Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Version: "test"}
	if mod != nil {
		mod(&d)
	}
	s := newServer(d)
	s.svc = f
	mux := http.NewServeMux()
	s.mount(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, s
}

func seedCompany(f *fakeSvc) domain.Company {
	c := domain.Company{ID: domain.NewID(), Slug: "acme", Name: "Acme", TaskPrefix: "HNK"}
	f.companies = append(f.companies, c)
	return c
}

func do(t *testing.T, method, url, body string, hdr map[string]string) (*http.Response, string) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func (f *fakeSvc) OpenExport(context.Context, domain.ID, service.ExportOptions) (*service.ExportStream, error) {
	if f.failWith == nil {
		return nil, errors.New("fakeSvc: OpenExport sem banco")
	}
	return nil, f.failWith
}

func (f *fakeSvc) ImportCompany(context.Context, domain.Actor, export.Document, service.ImportOptions) (service.ImportResult, error) {
	return service.ImportResult{}, f.failWith
}

func (f *fakeSvc) ResolveSession(context.Context, string) (domain.User, error) {
	f.resolves.Add(1)
	return domain.User{}, f.failWith
}

func (f *fakeSvc) ResolveAPIKey(context.Context, string) (domain.User, error) {
	f.resolves.Add(1)
	return domain.User{}, f.failWith
}

func (f *fakeSvc) CheckSession(context.Context, string) (domain.User, error) {
	f.checks.Add(1)
	return domain.User{}, f.failWith
}

func (f *fakeSvc) CheckAPIKey(context.Context, string) (domain.User, error) {
	f.checks.Add(1)
	return domain.User{}, f.failWith
}
