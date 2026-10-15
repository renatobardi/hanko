package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

func TestCreateCompany(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	if e.co.Slug != "acme-corp" || e.co.TaskPrefix != "HNK" || e.co.Timezone != "America/Sao_Paulo" {
		t.Fatalf("defaults wrong: %+v", e.co)
	}

	tests := []struct {
		name string
		in   service.CreateCompanyInput
		want error
	}{
		{"ok custom", service.CreateCompanyInput{Name: "Beta", Slug: "beta", TaskPrefix: "bt", Timezone: "UTC"}, nil},
		{"empty name", service.CreateCompanyInput{Name: "  "}, domain.ErrInvalid},
		{"bad slug", service.CreateCompanyInput{Name: "X", Slug: "Bad Slug"}, domain.ErrInvalid},
		{"unsluggable name", service.CreateCompanyInput{Name: "!!!"}, domain.ErrInvalid},
		{"bad prefix", service.CreateCompanyInput{Name: "Y", TaskPrefix: "1ABC"}, domain.ErrInvalid},
		{"bad tz", service.CreateCompanyInput{Name: "Z", Timezone: "Mars/Olympus"}, domain.ErrInvalid},
		{"duplicate slug", service.CreateCompanyInput{Name: "Acme Corp"}, domain.ErrConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := e.svc.CreateCompany(ctx, domain.HumanActor, tt.in)
			if tt.want != nil {
				wantErr(t, err, tt.want)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.TaskPrefix != "BT" {
				t.Errorf("prefix = %q, want BT", c.TaskPrefix)
			}
		})
	}

	// lookup by id and slug
	byID, err := e.svc.GetCompany(ctx, e.co.ID.String())
	if err != nil || byID.ID != e.co.ID {
		t.Fatalf("by id: %v %v", byID, err)
	}
	bySlug, err := e.svc.GetCompany(ctx, "acme-corp")
	if err != nil || bySlug.ID != e.co.ID {
		t.Fatalf("by slug: %v %v", bySlug, err)
	}
	_, err = e.svc.GetCompany(ctx, "nope")
	wantErr(t, err, domain.ErrNotFound)

	list, err := e.svc.ListCompanies(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %d %v", len(list), err)
	}

	acts, err := e.svc.ListActivity(ctx, e.co.ID, service.ActivityFilter{SubjectType: "company"})
	if err != nil || len(acts) != 1 || acts[0].Verb != "company.created" {
		t.Fatalf("activity: %+v %v", acts, err)
	}
}

func TestCreateWorkspaceRaiz(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.svc.WorkspacesRoot = t.TempDir()
	for _, p := range []string{"/etc", "relativo/x", e.svc.WorkspacesRoot + "/../x",
		e.svc.WorkspacesRoot + "/w",               // direto na raiz compartilhada, sem a subpasta da empresa
		e.svc.WorkspacesRoot + "/outra-empresa/w", // na pasta de outra empresa
		e.svc.WorkspacesRoot + "/" + e.co.Slug} {  // a própria pasta da empresa não é um workspace
		_, err := e.svc.CreateWorkspace(ctx, domain.HumanActor, e.co.ID, service.CreateWorkspaceInput{Name: "W", Path: p})
		wantErr(t, err, domain.ErrInvalid)
	}
	home := e.svc.WorkspacesRoot + "/" + e.co.Slug
	ws, err := e.svc.CreateWorkspace(ctx, domain.HumanActor, e.co.ID, service.CreateWorkspaceInput{Name: "W", Path: home + "/w"})
	if err != nil || ws.Path != home+"/w" {
		t.Fatalf("dentro da raiz: %+v %v", ws, err)
	}
}

func TestWorkspacesAndProjects(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	ws, err := e.svc.CreateWorkspace(ctx, domain.HumanActor, e.co.ID, service.CreateWorkspaceInput{Name: "Repo Principal", Path: "/srv/repo", RepoURL: "https://example.com/r.git"})
	if err != nil || ws.Slug != "repo-principal" {
		t.Fatalf("workspace: %+v %v", ws, err)
	}
	_, err = e.svc.CreateWorkspace(ctx, domain.HumanActor, e.co.ID, service.CreateWorkspaceInput{Name: "Repo Principal"})
	wantErr(t, err, domain.ErrConflict)
	_, err = e.svc.CreateWorkspace(ctx, domain.HumanActor, e.co.ID, service.CreateWorkspaceInput{})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.CreateWorkspace(ctx, domain.HumanActor, domain.NewID(), service.CreateWorkspaceInput{Name: "x"})
	wantErr(t, err, domain.ErrNotFound)

	if got, err := e.svc.GetWorkspace(ctx, e.co.ID, ws.ID.String()); err != nil || got.ID != ws.ID {
		t.Fatalf("get ws id: %v", err)
	}
	if got, err := e.svc.GetWorkspace(ctx, e.co.ID, "repo-principal"); err != nil || got.ID != ws.ID {
		t.Fatalf("get ws slug: %v", err)
	}
	_, err = e.svc.GetWorkspace(ctx, e.co.ID, "zzz")
	wantErr(t, err, domain.ErrNotFound)
	if l, err := e.svc.ListWorkspaces(ctx, e.co.ID); err != nil || len(l) != 1 {
		t.Fatalf("list ws: %v %v", l, err)
	}

	pr, err := e.svc.CreateProject(ctx, domain.HumanActor, e.co.ID, service.CreateProjectInput{Name: "Lançamento", Description: "d", WorkspaceID: &ws.ID})
	if err != nil || pr.Slug != "lancamento" || pr.WorkspaceID == nil {
		t.Fatalf("project: %+v %v", pr, err)
	}
	_, err = e.svc.CreateProject(ctx, domain.HumanActor, e.co.ID, service.CreateProjectInput{Name: "Lançamento"})
	wantErr(t, err, domain.ErrConflict)
	_, err = e.svc.CreateProject(ctx, domain.HumanActor, e.co.ID, service.CreateProjectInput{Name: "Outro", WorkspaceID: ptr(domain.NewID())})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.CreateProject(ctx, domain.HumanActor, e.co.ID, service.CreateProjectInput{})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.CreateProject(ctx, domain.HumanActor, domain.NewID(), service.CreateProjectInput{Name: "x"})
	wantErr(t, err, domain.ErrNotFound)
	if got, err := e.svc.GetProject(ctx, e.co.ID, pr.ID.String()); err != nil || got.ID != pr.ID {
		t.Fatalf("get project id: %v", err)
	}
	if got, err := e.svc.GetProject(ctx, e.co.ID, "lancamento"); err != nil || got.ID != pr.ID {
		t.Fatalf("get project slug: %v", err)
	}
	_, err = e.svc.GetProject(ctx, e.co.ID, "zzz")
	wantErr(t, err, domain.ErrNotFound)
	if l, err := e.svc.ListProjects(ctx, e.co.ID); err != nil || len(l) != 1 {
		t.Fatalf("list projects: %v %v", l, err)
	}

	// a task no longer copies the project's workspace: the run resolves it at checkout (ADR-0025)
	tv := e.task(t, service.CreateTaskInput{ProjectID: &pr.ID})
	if tv.WorkspaceID != nil || tv.ProjectName != "Lançamento" {
		t.Fatalf("task workspace/project: %+v", tv)
	}
}

func TestPersonas(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ws, _ := e.svc.CreateWorkspace(ctx, domain.HumanActor, e.co.ID, service.CreateWorkspaceInput{Name: "W"})

	boss := e.persona(t, "Chefe Técnico", nil)
	if boss.Slug != "chefe-tecnico" || boss.Status != domain.PersonaActive || string(boss.AdapterConfig) != "{}" {
		t.Fatalf("boss: %+v", boss)
	}

	tests := []struct {
		name string
		in   service.HirePersonaInput
		want error
	}{
		{"ok", service.HirePersonaInput{Name: "Eng", Agent: "claude", ReportsTo: &boss.ID, WorkspaceID: &ws.ID, HeartbeatIntervalSec: 60, BudgetMonthlyCents: ptr(int64(1000)), AdapterConfig: []byte(`{"permission_mode":"plan"}`)}, nil},
		{"no name", service.HirePersonaInput{Agent: "fake"}, domain.ErrInvalid},
		{"bad agent", service.HirePersonaInput{Name: "A", Agent: "gpt"}, domain.ErrInvalid},
		{"neg interval", service.HirePersonaInput{Name: "B", Agent: "fake", HeartbeatIntervalSec: -1}, domain.ErrInvalid},
		{"neg budget", service.HirePersonaInput{Name: "C", Agent: "fake", BudgetMonthlyCents: ptr(int64(-1))}, domain.ErrInvalid},
		{"bad json", service.HirePersonaInput{Name: "D", Agent: "fake", AdapterConfig: []byte(`{`)}, domain.ErrInvalid},
		{"bad reports_to", service.HirePersonaInput{Name: "E", Agent: "fake", ReportsTo: ptr(domain.NewID())}, domain.ErrInvalid},
		{"bad workspace", service.HirePersonaInput{Name: "F", Agent: "fake", WorkspaceID: ptr(domain.NewID())}, domain.ErrInvalid},
		{"dup slug", service.HirePersonaInput{Name: "Chefe Técnico", Agent: "fake"}, domain.ErrConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := e.svc.HirePersona(ctx, domain.HumanActor, e.co.ID, tt.in)
			if tt.want == nil && err != nil {
				t.Fatal(err)
			}
			if tt.want != nil {
				wantErr(t, err, tt.want)
			}
		})
	}
	_, err := e.svc.HirePersona(ctx, domain.HumanActor, domain.NewID(), service.HirePersonaInput{Name: "G", Agent: "fake"})
	wantErr(t, err, domain.ErrNotFound)

	if l, err := e.svc.ListPersonas(ctx, e.co.ID); err != nil || len(l) != 2 {
		t.Fatalf("list: %d %v", len(l), err)
	}
	if got, err := e.svc.GetPersona(ctx, e.co.ID, boss.ID.String()); err != nil || got.ID != boss.ID {
		t.Fatalf("get by id: %v", err)
	}
	if got, err := e.svc.GetPersona(ctx, e.co.ID, "chefe-tecnico"); err != nil || got.ID != boss.ID {
		t.Fatalf("get by slug: %v", err)
	}
	_, err = e.svc.GetPersona(ctx, e.co.ID, "nada")
	wantErr(t, err, domain.ErrNotFound)

	// update
	up, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, boss.ID, service.UpdatePersonaInput{
		Name: ptr("Chefe"), Title: ptr("CTO"), Instructions: ptr("seja breve"), Model: ptr("m2"),
		Status: ptr(domain.PersonaPaused), HeartbeatIntervalSec: ptr(30), WorkspaceID: &ws.ID,
	})
	if err != nil || up.Name != "Chefe" || up.Status != domain.PersonaPaused || up.WorkspaceID == nil || up.HeartbeatIntervalSec != 30 {
		t.Fatalf("update: %+v %v", up, err)
	}
	up, err = e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, boss.ID, service.UpdatePersonaInput{ClearWorkspace: true})
	if err != nil || up.WorkspaceID != nil {
		t.Fatalf("clear ws: %+v %v", up, err)
	}
	same, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, boss.ID, service.UpdatePersonaInput{Title: ptr("CTO")})
	if err != nil || !same.UpdatedAt.Equal(up.UpdatedAt) {
		t.Fatalf("no-op update should not touch row: %v", err)
	}
	bad := []service.UpdatePersonaInput{
		{Name: ptr(" ")}, {Status: ptr(domain.PersonaStatus("x"))}, {HeartbeatIntervalSec: ptr(-5)},
		{ClearWorkspace: true, WorkspaceID: &ws.ID}, {WorkspaceID: ptr(domain.NewID())},
	}
	for i, in := range bad {
		if _, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, boss.ID, in); !isInvalid(err) {
			t.Errorf("bad[%d]: err = %v, want invalid", i, err)
		}
	}
	_, err = e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, domain.NewID(), service.UpdatePersonaInput{Title: ptr("x")})
	wantErr(t, err, domain.ErrNotFound)
}

func isInvalid(err error) bool { return errors.Is(err, domain.ErrInvalid) }
