package ui

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

// Projetos e workspaces na UI (ADR-0025).

func TestProjectPagesRender(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	pages := []struct {
		path string
		want []string
	}{
		{"/c/hanko/projetos", []string{"Projetos", "Novo projeto", "Kubo", "LinkedIn", "Progresso", "Site institucional"}},
		{"/c/hanko/projetos?aba=arquivados", []string{"Nenhum projeto aqui."}},
		{"/c/hanko/projetos/novo", []string{"Novo projeto", `name="name"`, `name="workspace"`, `name="budget"`}},
		{"/c/hanko/projetos/kubo", []string{"Kubo", "Tasks por status", "Personas envolvidas", "Tasks recentes", "Orçamento do mês", "Arquivar projeto",
			`href="/c/hanko/tasks?aba=todas&amp;projeto=kubo"`, `href="/c/hanko/custos?projeto=kubo"`, `href="/c/hanko/workspaces/site"`, "Salvar alterações"}},
		{"/c/hanko/workspaces", []string{"Workspaces", "Site institucional", "/srv/hanko/workspaces/hanko/site", "https://github.com/hanko/site", "diretório ausente"}},
		{"/c/hanko/workspaces/novo", []string{"Novo workspace", `name="path"`, `name="repo_url"`, `name="branch"`, "/srv/hanko/workspaces/hanko"}},
		{"/c/hanko/workspaces/site", []string{"Site institucional", "Em uso por", "Kubo", "Excluir workspace", "Desvincular quem usa e excluir"}},
	}
	for _, p := range pages {
		t.Run(p.path, func(t *testing.T) {
			rec := e.get(t, p.path)
			wantStatus(t, rec, http.StatusOK)
			wantContains(t, rec, p.want...)
		})
	}
	t.Run("em inglês", func(t *testing.T) {
		rec := e.get(t, "/c/hanko/projetos/kubo", "Cookie", "hanko_lang=en")
		wantStatus(t, rec, http.StatusOK)
		wantContains(t, rec, "Tasks by status", "Personas involved", "Archive project")
		rec = e.get(t, "/c/hanko/workspaces", "Cookie", "hanko_lang=en")
		wantContains(t, rec, "Used by", "directory missing")
	})
	t.Run("menu sem Em breve", func(t *testing.T) {
		rec := e.get(t, "/c/hanko/")
		wantContains(t, rec, `href="/c/hanko/projetos"`, `href="/c/hanko/workspaces"`)
		wantNotContains(t, rec, "/em-breve/projetos", "/em-breve/workspaces")
	})
	t.Run("não encontrado", func(t *testing.T) {
		wantStatus(t, e.get(t, "/c/hanko/projetos/nao-existe"), http.StatusNotFound)
		wantStatus(t, e.get(t, "/c/hanko/workspaces/nao-existe"), http.StatusNotFound)
	})
}

func TestProjectsEmpty(t *testing.T) {
	e := newEnv(t, uitest.Empty())
	wantContains(t, e.get(t, "/c/hanko/projetos"), "Você ainda não tem projetos.", "Criar projeto")
	wantContains(t, e.get(t, "/c/hanko/workspaces"), "Você ainda não tem workspaces.", "Criar workspace")
}

func TestCreateAndArchiveProject(t *testing.T) {
	f := uitest.Sample()
	e := newEnv(t, f)
	ws := uitest.ID("workspace:site").String()

	rec := e.post(t, "/c/hanko/projetos/novo", url.Values{"name": {"Lançamento"}, "description": {"v1"}, "workspace": {ws}, "budget": {"120,50"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/c/hanko/projetos/lancamento?ok=criado" {
		t.Fatalf("Location = %q", loc)
	}
	var got *domain.Project
	for i := range f.Projects {
		if f.Projects[i].Slug == "lancamento" {
			got = &f.Projects[i]
		}
	}
	if got == nil || got.BudgetMonthlyCents == nil || *got.BudgetMonthlyCents != 12050 || got.WorkspaceID == nil {
		t.Fatalf("projeto = %+v", got)
	}
	wantContains(t, e.get(t, "/c/hanko/projetos/lancamento?ok=criado"), "Projeto criado.", "120,50")

	t.Run("nome vazio é 422 e mantém o formulário", func(t *testing.T) {
		rec := e.post(t, "/c/hanko/projetos/novo", url.Values{"name": {""}, "description": {"mantém"}}, false)
		wantStatus(t, rec, http.StatusUnprocessableEntity)
		wantContains(t, rec, "Dê um nome ao projeto", `value="mantém"`)
	})
	t.Run("orçamento inválido é 422", func(t *testing.T) {
		rec := e.post(t, "/c/hanko/projetos/novo", url.Values{"name": {"X"}, "budget": {"muito"}}, false)
		wantStatus(t, rec, http.StatusUnprocessableEntity)
		wantContains(t, rec, "Informe um valor em dólares")
	})
	t.Run("a raiz sem a subpasta da empresa é recusada", func(t *testing.T) {
		rec := e.post(t, "/c/hanko/workspaces/novo", url.Values{"name": {"Solto"}, "path": {"/srv/hanko/workspaces/solto"}}, false)
		wantStatus(t, rec, http.StatusUnprocessableEntity)
		wantContains(t, rec, "Caminho inválido", "/srv/hanko/workspaces/hanko")
	})
	t.Run("workspace antigo fora da pasta da empresa segue funcionando com aviso", func(t *testing.T) {
		f.Workspaces[0].Path = "/srv/hanko/workspaces/site" // criado antes da regra da subpasta
		wantContains(t, e.get(t, "/c/hanko/workspaces"), "fora da pasta da empresa")
		wantContains(t, e.get(t, "/c/hanko/workspaces/site"), "fora da pasta da empresa", "/srv/hanko/workspaces/hanko")
		f.Workspaces[0].Path = "/srv/hanko/workspaces/hanko/site"
		wantNotContains(t, e.get(t, "/c/hanko/workspaces"), "fora da pasta da empresa")
	})
	t.Run("editar", func(t *testing.T) {
		rec := e.post(t, "/c/hanko/projetos/lancamento", url.Values{"name": {"Lançamento v2"}, "description": {""}, "workspace": {""}, "budget": {""}}, false)
		wantStatus(t, rec, http.StatusSeeOther)
		if got.Name != "Lançamento v2" || got.BudgetMonthlyCents != nil || got.WorkspaceID != nil {
			t.Errorf("projeto = %+v", got)
		}
	})
	t.Run("arquivar e reativar", func(t *testing.T) {
		rec := e.post(t, "/c/hanko/projetos/lancamento/arquivar", nil, false)
		wantStatus(t, rec, http.StatusSeeOther)
		if got.Status != domain.ProjectArchived {
			t.Fatalf("status = %q", got.Status)
		}
		wantContains(t, e.get(t, "/c/hanko/projetos/lancamento"), "Reativar projeto", "Arquivado")
		wantNotContains(t, e.get(t, "/c/hanko/projetos"), "Lançamento v2")
		wantContains(t, e.get(t, "/c/hanko/projetos?aba=arquivados"), "Lançamento v2")
		// o projeto arquivado não aparece para novas tasks
		wantNotContains(t, e.get(t, "/c/hanko/tasks/nova"), "Lançamento v2")
		wantStatus(t, e.post(t, "/c/hanko/projetos/lancamento/reativar", nil, false), http.StatusSeeOther)
		if got.Status != domain.ProjectActive {
			t.Fatalf("status = %q", got.Status)
		}
	})
	t.Run("erro do service chega como página amigável", func(t *testing.T) {
		f.SetErr("UpdateProject", domain.ErrForbidden)
		rec := e.post(t, "/c/hanko/projetos/lancamento/arquivar", nil, false)
		wantStatus(t, rec, http.StatusForbidden)
		wantContains(t, rec, "Seu papel nesta empresa não permite esta ação.")
		f.SetErr("UpdateProject", nil)
	})
}

func TestWorkspaceCRUD(t *testing.T) {
	f := uitest.Sample()
	e := newEnv(t, f)

	rec := e.post(t, "/c/hanko/workspaces/novo", url.Values{"name": {"API"}, "path": {"/srv/hanko/workspaces/hanko/api"}, "repo_url": {"https://github.com/hanko/api"}, "branch": {"main"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/c/hanko/workspaces/api?ok=w-criado" {
		t.Fatalf("Location = %q", loc)
	}
	wantContains(t, e.get(t, "/c/hanko/workspaces/api?ok=w-criado"), "Workspace criado.", "Nenhuma persona, projeto ou task aberta usa este workspace.", "O diretório no disco não é apagado")

	t.Run("caminho fora da raiz é 422", func(t *testing.T) {
		rec := e.post(t, "/c/hanko/workspaces/novo", url.Values{"name": {"Ruim"}, "path": {"/etc"}}, false)
		wantStatus(t, rec, http.StatusUnprocessableEntity)
		wantContains(t, rec, "Caminho inválido", `value="/etc"`)
	})
	t.Run("editar", func(t *testing.T) {
		rec := e.post(t, "/c/hanko/workspaces/api", url.Values{"name": {"API v2"}, "path": {"/srv/hanko/workspaces/hanko/api"}, "repo_url": {""}, "branch": {"develop"}}, false)
		wantStatus(t, rec, http.StatusSeeOther)
		wantContains(t, e.get(t, "/c/hanko/workspaces/api"), "API v2", `value="develop"`)
	})
	t.Run("em uso não exclui sem desvincular", func(t *testing.T) {
		rec := e.post(t, "/c/hanko/workspaces/site/excluir", url.Values{}, false)
		wantStatus(t, rec, http.StatusConflict)
		wantContains(t, rec, "Workspace em uso")
		if len(f.Workspaces) != 2 {
			t.Fatalf("workspaces = %d", len(f.Workspaces))
		}
	})
	t.Run("desvincular e excluir", func(t *testing.T) {
		rec := e.post(t, "/c/hanko/workspaces/site/excluir", url.Values{"unlink": {"1"}}, false)
		wantStatus(t, rec, http.StatusSeeOther)
		if loc := rec.Header().Get("Location"); loc != "/c/hanko/workspaces?ok=w-excluido" {
			t.Errorf("Location = %q", loc)
		}
		for _, p := range f.Projects {
			if p.Slug == "kubo" && p.WorkspaceID != nil {
				t.Error("o projeto continua apontando para o workspace excluído")
			}
		}
	})
	t.Run("sem uso exclui direto", func(t *testing.T) {
		wantStatus(t, e.post(t, "/c/hanko/workspaces/api/excluir", url.Values{}, false), http.StatusSeeOther)
		if len(f.Workspaces) != 0 {
			t.Fatalf("workspaces = %d", len(f.Workspaces))
		}
	})
}

func TestTaskProjectField(t *testing.T) {
	f := uitest.Sample()
	e := newEnv(t, f)
	nas := uitest.ID("project:nas").String()

	rec := e.get(t, "/c/hanko/tasks/HNK-217")
	wantContains(t, rec, `name="project"`, `href="/c/hanko/projetos/kubo"`)

	t.Run("trocar de projeto", func(t *testing.T) {
		rec := e.post(t, "/c/hanko/tasks/HNK-217", url.Values{"project": {nas}}, true)
		wantStatus(t, rec, http.StatusOK)
		got, _ := f.GetTask(context.Background(), domain.ID{}, "HNK-217")
		if got.ProjectName != "NAS 3D" {
			t.Errorf("projeto = %q", got.ProjectName)
		}
	})
	t.Run("tirar do projeto", func(t *testing.T) {
		wantStatus(t, e.post(t, "/c/hanko/tasks/HNK-217", url.Values{"project": {""}}, true), http.StatusOK)
		got, _ := f.GetTask(context.Background(), domain.ID{}, "HNK-217")
		if got.ProjectID != nil {
			t.Error("a task continua no projeto")
		}
	})
	t.Run("id inválido é 422", func(t *testing.T) {
		wantStatus(t, e.post(t, "/c/hanko/tasks/HNK-217", url.Values{"project": {"nao-uuid"}}, true), http.StatusUnprocessableEntity)
	})
	t.Run("sem o campo não mexe no projeto", func(t *testing.T) {
		f2 := uitest.Sample()
		e2 := newEnv(t, f2)
		wantStatus(t, e2.post(t, "/c/hanko/tasks/HNK-217", url.Values{"status": {"blocked"}}, true), http.StatusOK)
		got, _ := f2.GetTask(context.Background(), domain.ID{}, "HNK-217")
		if got.ProjectName != "Kubo" {
			t.Errorf("projeto = %q", got.ProjectName)
		}
	})
}

func TestTasksFilterByProject(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	rec := e.get(t, "/c/hanko/tasks?aba=todas&projeto=kubo")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "HNK-217", "HNK-219", "limpar filtro", `name="projeto" value="kubo"`, "projeto=kubo")
	wantNotContains(t, rec, "HNK-221", "HNK-218")
	// o filtro vai junto nas abas e no link de nova task
	wantContains(t, rec, `aba=backlog&amp;projeto=kubo`, `/tasks/nova?projeto=kubo`)

	wantStatus(t, e.get(t, "/c/hanko/tasks?projeto=nao-existe"), http.StatusNotFound)

	t.Run("nova task já vem com o projeto", func(t *testing.T) {
		rec := e.get(t, "/c/hanko/tasks/nova?projeto=kubo")
		wantContains(t, rec, `value="`+uitest.ID("project:kubo").String()+`" selected`)
	})
}

func TestCostsByProject(t *testing.T) {
	f := uitest.Sample()
	e := newEnv(t, f)
	rec := e.get(t, "/c/hanko/custos")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Orçamento por projeto", `action="/c/hanko/projetos/kubo/orcamento?next=custos"`, `href="/c/hanko/custos?projeto=kubo"`)

	rec = e.get(t, "/c/hanko/custos?projeto=kubo&p=anterior")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "limpar filtro", "mensalidades não são rateadas por projeto", `href="/c/hanko/custos?p=30d&amp;projeto=kubo"`)
	wantStatus(t, e.get(t, "/c/hanko/custos?projeto=nao-existe"), http.StatusNotFound)

	t.Run("orçamento do projeto pela tela de custos", func(t *testing.T) {
		rec := e.post(t, "/c/hanko/projetos/kubo/orcamento?next=custos", url.Values{"budget": {"80"}}, false)
		wantStatus(t, rec, http.StatusSeeOther)
		if loc := rec.Header().Get("Location"); loc != "/c/hanko/custos?ok=projeto" {
			t.Errorf("Location = %q", loc)
		}
		for _, p := range f.Projects {
			if p.Slug == "kubo" && (p.BudgetMonthlyCents == nil || *p.BudgetMonthlyCents != 8000) {
				t.Errorf("orçamento = %v", p.BudgetMonthlyCents)
			}
		}
		rec = e.post(t, "/c/hanko/projetos/kubo/orcamento", url.Values{"budget": {""}}, false)
		wantStatus(t, rec, http.StatusSeeOther)
	})
}

// O orçamento estourado de um projeto e o workspace em uso têm mensagem própria.
func TestFriendlyProjectErrors(t *testing.T) {
	ctx := context.Background()
	st, title, _ := friendly(ctx, &service.WorkspaceInUseError{Slug: "x", Personas: 1})
	if st != http.StatusConflict || title != "Workspace em uso" {
		t.Errorf("workspace em uso: %d %q", st, title)
	}
	blocked := &service.BudgetBlockedError{Scope: "project", Name: "Kubo"}
	if !errors.Is(blocked, domain.ErrBudgetExceeded) {
		t.Fatal("BudgetBlockedError deveria ser ErrBudgetExceeded")
	}
	st, title, msg := friendly(ctx, blocked)
	if st != http.StatusConflict || title != "Orçamento do projeto estourado" || !strings.Contains(msg, "projeto") {
		t.Errorf("orçamento do projeto: %d %q %q", st, title, msg)
	}
}

// Papéis (ADR-0021): admin e owner administram projetos e workspaces; os demais só leem.
func TestProjectControlsByRole(t *testing.T) {
	A, M := domain.MemberAdmin, domain.MemberMember
	controls := []control{
		{"/c/hanko/projetos", `href="/c/hanko/projetos/novo"`, A},
		{"/c/hanko/projetos/novo", `action="/c/hanko/projetos/novo"`, A},
		{"/c/hanko/projetos/kubo", `action="/c/hanko/projetos/kubo"`, A},
		{"/c/hanko/projetos/kubo", "/projetos/kubo/arquivar", A},
		{"/c/hanko/projetos/kubo", `href="/c/hanko/tasks/nova?projeto=kubo"`, M},
		{"/c/hanko/workspaces", `href="/c/hanko/workspaces/novo"`, A},
		{"/c/hanko/workspaces/novo", `action="/c/hanko/workspaces/novo"`, A},
		{"/c/hanko/workspaces/site", `action="/c/hanko/workspaces/site"`, A},
		{"/c/hanko/workspaces/site", "/workspaces/site/excluir", A},
		{"/c/hanko/custos", `action="/c/hanko/projetos/kubo/orcamento?next=custos"`, A},
	}
	for _, role := range allRoles {
		t.Run(string(role), func(t *testing.T) {
			f := uitest.Sample()
			f.MyRoleIs = role
			e := newEnv(t, f)
			for _, c := range controls {
				rec := e.get(t, c.page)
				wantStatus(t, rec, http.StatusOK)
				want := role.AtLeast(c.min)
				if got := strings.Contains(rec.Body.String(), c.mark); got != want {
					t.Errorf("%s: %q presente=%v, quer %v (papel %s)", c.page, c.mark, got, want, role)
				}
			}
		})
	}
}
