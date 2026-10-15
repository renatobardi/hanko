package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/export"
	"github.com/renatobardi/hanko/internal/service"
)

// Export/import com projetos e workspaces (ADR-0022 + ADR-0025): o formato continua hanko.company.v1; os campos
// novos são opcionais; o caminho de uma máquina que esta não aceita entra vazio.

func TestExportImportProjectsAndWorkspaces(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	root := t.TempDir()
	e.svc.WorkspacesRoot = root
	home := root + "/" + e.co.Slug // pasta da empresa na raiz compartilhada

	ws := e.workspace(t, "Site", func(in *service.CreateWorkspaceInput) {
		in.Path, in.RepoURL, in.DefaultBranch = home+"/site", "https://github.com/o/site", "main"
	})
	pr := e.project(t, "Lançamento", func(in *service.CreateProjectInput) {
		in.WorkspaceID, in.BudgetMonthlyCents = &ws.ID, ptr(int64(7500))
	})
	arch := e.project(t, "Velho", nil)
	if _, err := e.svc.UpdateProject(ctx, domain.HumanActor, e.co.ID, arch.ID, service.UpdateProjectInput{Status: ptr(domain.ProjectArchived)}); err != nil {
		t.Fatal(err)
	}
	e.task(t, service.CreateTaskInput{Title: "no projeto", ProjectID: &pr.ID})

	doc, err := e.svc.ExportCompany(ctx, e.co.ID, service.ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Format != "hanko.company.v1" {
		t.Fatalf("formato = %q", doc.Format)
	}
	if len(doc.Workspaces) != 1 || doc.Workspaces[0].DefaultBranch != "main" || doc.Workspaces[0].Path != home+"/site" {
		t.Fatalf("workspaces: %+v", doc.Workspaces)
	}
	byslug := map[string]export.Project{}
	for _, p := range doc.Projects {
		byslug[p.Slug] = p
	}
	if p := byslug["lancamento"]; p.Status != "active" || p.BudgetMonthlyCents == nil || *p.BudgetMonthlyCents != 7500 || p.Workspace != "site" {
		t.Fatalf("projeto: %+v", p)
	}
	if p := byslug["velho"]; p.Status != "archived" || p.BudgetMonthlyCents != nil {
		t.Fatalf("projeto arquivado: %+v", p)
	}

	res, err := e.svc.ImportCompany(ctx, domain.HumanActor, doc, service.ImportOptions{Slug: "copia"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Counts["workspaces"] != 1 || res.Counts["projects"] != 2 || res.Counts["tasks"] != 1 {
		t.Fatalf("counts: %+v", res.Counts)
	}
	// o caminho era da pasta da empresa de origem: a cópia não pode apontar para o diretório de outra empresa
	if res.Counts["workspace_paths_cleared"] != 1 {
		t.Fatalf("o caminho da outra empresa deveria ser limpo: %+v", res.Counts)
	}
	cp := res.Company.ID
	w2, err := e.svc.GetWorkspace(ctx, cp, "site")
	if err != nil || w2.Path != "" || w2.RepoURL != "https://github.com/o/site" || w2.DefaultBranch != "main" {
		t.Fatalf("workspace importado: %+v %v", w2, err)
	}
	p2, err := e.svc.GetProject(ctx, cp, "lancamento")
	if err != nil || p2.WorkspaceID == nil || *p2.WorkspaceID != w2.ID || p2.BudgetMonthlyCents == nil || *p2.BudgetMonthlyCents != 7500 || p2.Status != domain.ProjectActive {
		t.Fatalf("projeto importado: %+v %v", p2, err)
	}
	if v, err := e.svc.GetProject(ctx, cp, "velho"); err != nil || v.Status != domain.ProjectArchived {
		t.Fatalf("projeto arquivado importado: %+v %v", v, err)
	}
	tasks, err := e.svc.ListTasks(ctx, cp, service.TaskFilter{ProjectID: &p2.ID})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("task do projeto importado: %+v %v", tasks, err)
	}
}

func TestImportWorkspacePathFromAnotherMachine(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.svc.WorkspacesRoot = t.TempDir()

	doc := export.Document{Format: export.Format, ExportedAt: time.Now().UTC(), Company: export.Company{Name: "Vinda de fora"},
		Workspaces: []export.Workspace{
			{Slug: "ok", Name: "Dentro", Path: e.svc.WorkspacesRoot + "/estrangeira/ok", CreatedAt: time.Now().UTC()},
			{Slug: "solto", Name: "Direto na raiz", Path: e.svc.WorkspacesRoot + "/solto", CreatedAt: time.Now().UTC()},
			{Slug: "outra", Name: "Outra máquina", Path: "/home/alguem/repo", RepoURL: "https://github.com/o/r", DefaultBranch: "dev", CreatedAt: time.Now().UTC()},
			{Slug: "trav", Name: "Travessia", Path: e.svc.WorkspacesRoot + "/../etc", CreatedAt: time.Now().UTC()},
		},
		Projects: []export.Project{{Slug: "p", Name: "P", Workspace: "outra", CreatedAt: time.Now().UTC()}}}
	res, err := e.svc.ImportCompany(ctx, domain.HumanActor, doc, service.ImportOptions{Slug: "estrangeira"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Counts["workspaces"] != 4 || res.Counts["workspace_paths_cleared"] != 3 {
		t.Fatalf("counts: %+v", res.Counts)
	}
	for slug, wantPath := range map[string]string{"ok": e.svc.WorkspacesRoot + "/estrangeira/ok", "solto": "", "outra": "", "trav": ""} {
		w, err := e.svc.GetWorkspace(ctx, res.Company.ID, slug)
		if err != nil || w.Path != wantPath {
			t.Errorf("%s: path = %q (%v), quer %q", slug, w.Path, err, wantPath)
		}
	}
	// o resto do workspace (repositório, branch) e o vínculo do projeto sobrevivem
	w, _ := e.svc.GetWorkspace(ctx, res.Company.ID, "outra")
	if w.RepoURL != "https://github.com/o/r" || w.DefaultBranch != "dev" {
		t.Errorf("metadado perdido: %+v", w)
	}
	if p, err := e.svc.GetProject(ctx, res.Company.ID, "p"); err != nil || p.WorkspaceID == nil || *p.WorkspaceID != w.ID {
		t.Errorf("projeto perdeu o workspace: %+v %v", p, err)
	}
}

// Arquivos exportados antes do ADR-0025 não têm os campos novos e continuam importando.
func TestImportOldFormatWithoutNewFields(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	doc := export.Document{Format: export.Format, ExportedAt: time.Now().UTC(), Company: export.Company{Name: "Antiga"},
		Workspaces: []export.Workspace{{Slug: "w", Name: "W", Path: "/srv/w", CreatedAt: time.Now().UTC()}},
		Projects:   []export.Project{{Slug: "p", Name: "P", Workspace: "w", CreatedAt: time.Now().UTC()}}}
	res, err := e.svc.ImportCompany(ctx, domain.HumanActor, doc, service.ImportOptions{Slug: "antiga"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.svc.GetProject(ctx, res.Company.ID, "p")
	if err != nil || p.Status != domain.ProjectActive || p.BudgetMonthlyCents != nil {
		t.Fatalf("defaults: %+v %v", p, err)
	}
	if w, _ := e.svc.GetWorkspace(ctx, res.Company.ID, "w"); w.DefaultBranch != "" || w.Path != "/srv/w" {
		t.Fatalf("workspace: %+v", w)
	}
}

func TestImportRejectsBadProjectsAndWorkspaces(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	now := time.Now().UTC()
	base := func() export.Document {
		return export.Document{Format: export.Format, ExportedAt: now, Company: export.Company{Name: "Ruim"},
			Workspaces: []export.Workspace{{Slug: "w", Name: "W", CreatedAt: now}},
			Projects:   []export.Project{{Slug: "p", Name: "P", CreatedAt: now}}}
	}
	cases := map[string]func(*export.Document){
		"status desconhecido":    func(d *export.Document) { d.Projects[0].Status = "pausado" },
		"orçamento negativo":     func(d *export.Document) { d.Projects[0].BudgetMonthlyCents = ptr(int64(-1)) },
		"workspace inexistente":  func(d *export.Document) { d.Projects[0].Workspace = "nao-existe" },
		"repo com credencial":    func(d *export.Document) { d.Workspaces[0].RepoURL = "https://u:s@github.com/o/r" },
		"repo com esquema ruim":  func(d *export.Document) { d.Workspaces[0].RepoURL = "file:///etc/passwd" },
		"branch inválida":        func(d *export.Document) { d.Workspaces[0].DefaultBranch = "a b;c" },
		"slug de projeto ruim":   func(d *export.Document) { d.Projects[0].Slug = "Ruim Slug" },
		"workspace sem nome":     func(d *export.Document) { d.Workspaces[0].Name = " " },
		"slug de workspace dup.": func(d *export.Document) { d.Workspaces = append(d.Workspaces, d.Workspaces[0]) },
	}
	n := 0
	for name, mod := range cases {
		t.Run(name, func(t *testing.T) {
			n++
			doc := base()
			mod(&doc)
			slug := "ruim-" + string(rune('a'+n))
			_, err := e.svc.ImportCompany(ctx, domain.HumanActor, doc, service.ImportOptions{Slug: slug})
			wantErr(t, err, domain.ErrInvalid)
			if _, err := e.svc.GetCompany(ctx, slug); err == nil {
				t.Error("a recusa deixou uma empresa para trás")
			}
		})
	}
}
