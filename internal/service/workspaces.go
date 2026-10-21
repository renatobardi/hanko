package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Workspaces (ADR-0025): o diretório e o repositório onde as personas trabalham. O Hanko guarda caminho, URL do
// repositório e branch padrão como metadado; ele não clona nem troca de branch. O caminho é validado contra a raiz
// HANKO_WORKSPACES_ROOT; só administradores gerenciam workspaces.

// UpdateWorkspaceInput altera um workspace; campo nil fica como está. Path e RepoURL vazios (ponteiro para "")
// limpam o valor.
type UpdateWorkspaceInput struct {
	Name, Path, RepoURL, DefaultBranch *string
}

// UsageRef identifica quem usa um workspace.
type UsageRef struct {
	ID   domain.ID `json:"id"`
	Slug string    `json:"slug"`
	Name string    `json:"name"`
}

// WorkspaceView é um workspace com a contagem de quem o usa; no detalhe traz também quem são.
type WorkspaceView struct {
	domain.Workspace
	PersonaCount int `json:"persona_count"`
	ProjectCount int `json:"project_count"`
	// OpenTasks conta as tasks abertas que escolheram este workspace de propósito.
	OpenTasks int `json:"open_tasks"`
	// OutsideRoot: o caminho salvo está fora da subpasta da empresa na raiz dos workspaces (criado antes da regra);
	// segue funcionando, mas a UI avisa.
	OutsideRoot bool       `json:"outside_root,omitempty"`
	Personas    []UsageRef `json:"personas,omitempty"`
	Projects    []UsageRef `json:"projects,omitempty"`
}

// InUse diz se o workspace está em uso (e portanto não pode ser excluído sem desvincular).
func (v WorkspaceView) InUse() bool { return v.PersonaCount+v.ProjectCount+v.OpenTasks > 0 }

// WorkspaceInUseError: a exclusão foi recusada porque há quem use o workspace. Vale como domain.ErrConflict.
type WorkspaceInUseError struct {
	Slug                          string
	Personas, Projects, OpenTasks int
}

func (e *WorkspaceInUseError) Error() string {
	return fmt.Sprintf("workspace %q em uso por %d persona(s), %d projeto(s) e %d task(s) aberta(s); desvincule antes de excluir", e.Slug, e.Personas, e.Projects, e.OpenTasks)
}

func (e *WorkspaceInUseError) Unwrap() error { return domain.ErrConflict }

const (
	maxWorkspacePath = 1024
	maxRepoURL       = 500
	maxBranchName    = 100
)

var (
	branchRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
	scpRepoRe = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:[A-Za-z0-9._~/-]+$`)
)

// checkRepoURL aceita https://, ssh:// ou o formato scp (git@host:dono/repo.git). Credenciais embutidas na URL
// (usuário:senha@) são recusadas: o metadado aparece na UI, na API e nos exports. Vazio é aceito.
func checkRepoURL(raw string) (string, error) {
	u := strings.TrimSpace(raw)
	if u == "" {
		return "", nil
	}
	if len(u) > maxRepoURL {
		return "", domain.Invalid("repo_url", "too long")
	}
	for _, r := range u {
		if r <= ' ' || r == 0x7f {
			return "", domain.Invalid("repo_url", "must not contain spaces or control characters")
		}
	}
	if scpRepoRe.MatchString(u) {
		return u, nil
	}
	p, err := url.Parse(u)
	if err != nil || (p.Scheme != "https" && p.Scheme != "ssh") || p.Hostname() == "" {
		return "", domain.Invalid("repo_url", "must be an https:// or ssh:// URL, or git@host:owner/repo")
	}
	if _, hasPw := p.User.Password(); hasPw {
		return "", domain.Invalid("repo_url", "must not embed credentials")
	}
	if p.Scheme == "https" && p.User != nil {
		return "", domain.Invalid("repo_url", "must not embed credentials")
	}
	return u, nil
}

// checkBranch valida o nome de branch (subconjunto seguro das regras do git). Vazio é aceito.
func checkBranch(raw string) (string, error) {
	b := strings.TrimSpace(raw)
	if b == "" {
		return "", nil
	}
	if len(b) > maxBranchName || !branchRe.MatchString(b) || strings.Contains(b, "..") || strings.Contains(b, "//") ||
		strings.HasSuffix(b, "/") || strings.HasSuffix(b, ".") || strings.HasSuffix(b, ".lock") {
		return "", domain.Invalid("default_branch", "must be a valid git branch name")
	}
	return b, nil
}

// checkWorkspacePath valida o caminho de um workspace: vazio é aceito (workspace só de metadado); senão precisa
// ser absoluto, limpo (sem "..", "//" ou "/" final), sem NUL nem caracteres de controle e, havendo WorkspacesRoot,
// ficar estritamente dentro da subpasta da empresa (<raiz>/<slug-da-empresa>/...) depois de resolver symlinks do
// trecho que já existe no disco. A raiz é compartilhada pela instalação; a subpasta por empresa impede que uma
// empresa aponte para o diretório de outra (ADR-0025).
func (s *Service) checkWorkspacePath(companySlug, raw string) (string, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return "", nil
	}
	if len(p) > maxWorkspacePath {
		return "", domain.Invalid("path", "too long")
	}
	for _, r := range p {
		if r < ' ' || r == 0x7f {
			return "", domain.Invalid("path", "must not contain control characters")
		}
	}
	if !filepath.IsAbs(p) {
		return "", domain.Invalid("path", "must be an absolute path")
	}
	if filepath.Clean(p) != p {
		return "", domain.Invalid("path", "must be clean (no '..', '//' or trailing '/')")
	}
	if s.WorkspacesRoot == "" {
		return p, nil
	}
	root := resolveExisting(filepath.Join(filepath.Clean(s.WorkspacesRoot), companySlug))
	rel, err := filepath.Rel(root, resolveExisting(p))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", domain.Invalid("path", "must be inside the company folder of the workspaces root (<root>/<company-slug>/...)")
	}
	if rel == gitReservedDir || strings.HasPrefix(rel, gitReservedDir+string(filepath.Separator)) {
		return "", domain.Invalid("path", "the "+gitReservedDir+" folder of the company is reserved for Git mirrors and worktrees")
	}
	return p, nil
}

// CompanyWorkspacesRoot é a pasta da empresa dentro da raiz dos workspaces (<raiz>/<slug>); vazia sem raiz.
func (s *Service) CompanyWorkspacesRoot(companySlug string) string {
	if s.WorkspacesRoot == "" {
		return ""
	}
	return filepath.Join(filepath.Clean(s.WorkspacesRoot), companySlug)
}

// pathOutsideCompanyRoot diz se um caminho já salvo está fora da pasta da empresa (workspace criado antes da regra
// da subpasta): ele segue funcionando, mas a UI avisa. Comparação léxica; sem raiz, nada está fora.
func (s *Service) pathOutsideCompanyRoot(companySlug, path string) bool {
	root := s.CompanyWorkspacesRoot(companySlug)
	if root == "" || path == "" {
		return false
	}
	rel, err := filepath.Rel(root, filepath.Clean(path))
	return err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolveExisting resolve symlinks do maior prefixo existente de p e reanexa o resto.
func resolveExisting(p string) string {
	rest := ""
	cur := p
	for {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// WorkspacesRootPath é a raiz configurada dos workspaces (vazia = sem restrição), para a UI sugerir caminhos.
func (s *Service) WorkspacesRootPath() string { return s.WorkspacesRoot }

func (s *Service) CreateWorkspace(ctx context.Context, actor domain.Actor, companyID domain.ID, in CreateWorkspaceInput) (domain.Workspace, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.Workspace{}, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return domain.Workspace{}, domain.Invalid("name", "required")
	}
	if len(name) > 120 {
		return domain.Workspace{}, domain.Invalid("name", "too long (max 120)")
	}
	slug, err := resolveSlug(in.Slug, name)
	if err != nil {
		return domain.Workspace{}, err
	}
	co, err := s.read().GetCompanyByID(ctx, companyID)
	if err != nil {
		return domain.Workspace{}, mapErr(err, "company")
	}
	path, err := s.checkWorkspacePath(co.Slug, in.Path)
	if err != nil {
		return domain.Workspace{}, err
	}
	repo, err := checkRepoURL(in.RepoURL)
	if err != nil {
		return domain.Workspace{}, err
	}
	branch, err := checkBranch(in.DefaultBranch)
	if err != nil {
		return domain.Workspace{}, err
	}
	var out domain.Workspace
	err = s.withTx(ctx, func(t *txc) error {
		if _, err := t.q.GetCompanyByID(ctx, companyID); err != nil {
			return mapErr(err, "company")
		}
		row, err := t.q.CreateWorkspace(ctx, store.CreateWorkspaceParams{ID: domain.NewID(), CompanyID: companyID,
			Slug: slug, Name: name, Path: path, RepoUrl: repo, DefaultBranch: branch, Now: t.now})
		if err != nil {
			return mapErr(err, "create workspace")
		}
		out = workspaceFromRow(row)
		return t.activity(actor, companyID, "workspace.created", "workspace", out.ID,
			map[string]any{"slug": out.Slug, "name": out.Name}, "")
	})
	return out, err
}

// UpdateWorkspace altera nome, caminho, repositório e branch padrão; o slug não muda. O caminho passa pela mesma
// validação da criação.
func (s *Service) UpdateWorkspace(ctx context.Context, actor domain.Actor, companyID, workspaceID domain.ID, in UpdateWorkspaceInput) (domain.Workspace, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.Workspace{}, err
	}
	var out domain.Workspace
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetWorkspaceByID(ctx, store.GetWorkspaceByIDParams{CompanyID: companyID, ID: workspaceID})
		if err != nil {
			return mapErr(err, "get workspace")
		}
		p := store.UpdateWorkspaceParams{CompanyID: companyID, ID: workspaceID, Name: cur.Name, Path: cur.Path,
			RepoUrl: cur.RepoUrl, DefaultBranch: cur.DefaultBranch, Now: t.now}
		changed := map[string]any{}
		if in.Name != nil {
			n := strings.TrimSpace(*in.Name)
			if n == "" {
				return domain.Invalid("name", "must not be empty")
			}
			if len(n) > 120 {
				return domain.Invalid("name", "too long (max 120)")
			}
			if n != cur.Name {
				p.Name, changed["name"] = n, n
			}
		}
		if in.Path != nil {
			co, err := t.q.GetCompanyByID(ctx, companyID)
			if err != nil {
				return mapErr(err, "company")
			}
			path, err := s.checkWorkspacePath(co.Slug, *in.Path)
			if err != nil {
				return err
			}
			if path != cur.Path {
				p.Path, changed["path"] = path, path
			}
		}
		if in.RepoURL != nil {
			repo, err := checkRepoURL(*in.RepoURL)
			if err != nil {
				return err
			}
			if repo != cur.RepoUrl {
				p.RepoUrl, changed["repo_url"] = repo, repo
			}
		}
		if in.DefaultBranch != nil {
			br, err := checkBranch(*in.DefaultBranch)
			if err != nil {
				return err
			}
			if br != cur.DefaultBranch {
				p.DefaultBranch, changed["default_branch"] = br, br
			}
		}
		if len(changed) == 0 {
			out = workspaceFromRow(cur)
			return nil
		}
		row, err := t.q.UpdateWorkspace(ctx, p)
		if err != nil {
			return mapErr(err, "update workspace")
		}
		out = workspaceFromRow(row)
		return t.activity(actor, companyID, "workspace.updated", "workspace", out.ID, changed, "")
	})
	return out, err
}

// DeleteWorkspace exclui o workspace. Em uso (personas, projetos ou tasks abertas que o escolheram), recusa com
// *WorkspaceInUseError a menos que unlink seja true: então desvincula cada um explicitamente (cada desvínculo vira
// atividade) e exclui. As runs que o usaram guardam só o histórico (o vínculo vira nulo).
func (s *Service) DeleteWorkspace(ctx context.Context, actor domain.Actor, companyID, workspaceID domain.ID, unlink bool) error {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return err
	}
	var cleanup func()
	err := s.withTx(ctx, func(t *txc) error {
		ws, err := t.q.GetWorkspaceByID(ctx, store.GetWorkspaceByIDParams{CompanyID: companyID, ID: workspaceID})
		if err != nil {
			return mapErr(err, "get workspace")
		}
		use, err := workspaceUsage(ctx, t.q, companyID, ws.ID)
		if err != nil {
			return err
		}
		if use.InUse() && !unlink {
			return &WorkspaceInUseError{Slug: ws.Slug, Personas: use.PersonaCount, Projects: use.ProjectCount, OpenTasks: use.OpenTasks}
		}
		cleanup = s.gitFilesCleanup(ctx, t.q, companyID, ws.ID, ws.Slug)
		if unlink {
			ps, err := t.q.UnlinkWorkspacePersonas(ctx, store.UnlinkWorkspacePersonasParams{CompanyID: companyID, WorkspaceID: &ws.ID, Now: t.now})
			if err != nil {
				return fmt.Errorf("unlink personas: %w", err)
			}
			for _, id := range ps {
				if err := t.activity(actor, companyID, "persona.updated", "persona", id,
					map[string]any{"workspace_id": nil, "unlinked_workspace": ws.Slug}, ""); err != nil {
					return err
				}
			}
			prs, err := t.q.UnlinkWorkspaceProjects(ctx, store.UnlinkWorkspaceProjectsParams{CompanyID: companyID, WorkspaceID: &ws.ID, Now: t.now})
			if err != nil {
				return fmt.Errorf("unlink projects: %w", err)
			}
			for _, id := range prs {
				if err := t.activity(actor, companyID, "project.updated", "project", id,
					map[string]any{"workspace_id": nil, "unlinked_workspace": ws.Slug}, ""); err != nil {
					return err
				}
			}
			if _, err := t.q.UnlinkWorkspaceTasks(ctx, store.UnlinkWorkspaceTasksParams{CompanyID: companyID, WorkspaceID: &ws.ID}); err != nil {
				return fmt.Errorf("unlink tasks: %w", err)
			}
		}
		n, err := t.q.DeleteWorkspace(ctx, store.DeleteWorkspaceParams{CompanyID: companyID, ID: ws.ID})
		if err != nil {
			return fmt.Errorf("delete workspace: %w", err)
		}
		if n == 0 {
			return notFound("workspace")
		}
		return t.activity(actor, companyID, "workspace.deleted", "workspace", ws.ID,
			map[string]any{"slug": ws.Slug, "name": ws.Name, "unlinked": unlink && use.InUse()}, "")
	})
	if err == nil && cleanup != nil {
		cleanup() // espelho e worktrees saem do disco depois do commit (ADR-0054)
	}
	return err
}

func workspaceUsage(ctx context.Context, q *store.Queries, companyID, wsID domain.ID) (WorkspaceView, error) {
	var v WorkspaceView
	ps, err := q.ListWorkspacePersonas(ctx, store.ListWorkspacePersonasParams{CompanyID: companyID, WorkspaceID: &wsID})
	if err != nil {
		return v, fmt.Errorf("workspace personas: %w", err)
	}
	for _, p := range ps {
		v.Personas = append(v.Personas, UsageRef{ID: p.ID, Slug: p.Slug, Name: p.Name})
	}
	prs, err := q.ListWorkspaceProjects(ctx, store.ListWorkspaceProjectsParams{CompanyID: companyID, WorkspaceID: &wsID})
	if err != nil {
		return v, fmt.Errorf("workspace projects: %w", err)
	}
	for _, p := range prs {
		v.Projects = append(v.Projects, UsageRef{ID: p.ID, Slug: p.Slug, Name: p.Name})
	}
	n, err := q.CountWorkspaceOpenTasks(ctx, store.CountWorkspaceOpenTasksParams{CompanyID: companyID, WorkspaceID: &wsID})
	if err != nil {
		return v, fmt.Errorf("workspace open tasks: %w", err)
	}
	v.PersonaCount, v.ProjectCount, v.OpenTasks = len(v.Personas), len(v.Projects), int(n)
	return v, nil
}

func (s *Service) ListWorkspaces(ctx context.Context, companyID domain.ID) ([]domain.Workspace, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.read().ListWorkspaces(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}
	out := make([]domain.Workspace, len(rows))
	for i, r := range rows {
		out[i] = workspaceFromRow(r)
	}
	return out, nil
}

// ListWorkspaceViews lista os workspaces com a contagem de quem os usa.
func (s *Service) ListWorkspaceViews(ctx context.Context, companyID domain.ID) ([]WorkspaceView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	q := s.read()
	co, err := q.GetCompanyByID(ctx, companyID)
	if err != nil {
		return nil, mapErr(err, "company")
	}
	rows, err := q.ListWorkspaces(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}
	counts, err := q.WorkspaceUsageCounts(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("workspace usage: %w", err)
	}
	byID := make(map[domain.ID]store.WorkspaceUsageCountsRow, len(counts))
	for _, c := range counts {
		byID[c.ID] = c
	}
	out := make([]WorkspaceView, len(rows))
	for i, r := range rows {
		c := byID[r.ID]
		out[i] = WorkspaceView{Workspace: workspaceFromRow(r), PersonaCount: int(c.Personas), ProjectCount: int(c.Projects), OpenTasks: int(c.OpenTasks),
			OutsideRoot: s.pathOutsideCompanyRoot(co.Slug, r.Path)}
	}
	return out, nil
}

// GetWorkspaceView é o detalhe: o workspace e quem o usa (personas, projetos, tasks abertas).
func (s *Service) GetWorkspaceView(ctx context.Context, companyID domain.ID, ref string) (WorkspaceView, error) {
	ws, err := s.GetWorkspace(ctx, companyID, ref)
	if err != nil {
		return WorkspaceView{}, err
	}
	v, err := workspaceUsage(ctx, s.read(), companyID, ws.ID)
	if err != nil {
		return WorkspaceView{}, err
	}
	v.Workspace = ws
	if co, err := s.read().GetCompanyByID(ctx, companyID); err == nil {
		v.OutsideRoot = s.pathOutsideCompanyRoot(co.Slug, ws.Path)
	}
	return v, nil
}

// GetWorkspace resolves ref as UUID or slug within the company.
func (s *Service) GetWorkspace(ctx context.Context, companyID domain.ID, ref string) (domain.Workspace, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return domain.Workspace{}, err
	}
	q := s.read()
	var row store.Workspace
	var err error
	if id, perr := uuid.Parse(ref); perr == nil {
		row, err = q.GetWorkspaceByID(ctx, store.GetWorkspaceByIDParams{CompanyID: companyID, ID: id})
	} else {
		row, err = q.GetWorkspaceBySlug(ctx, store.GetWorkspaceBySlugParams{CompanyID: companyID, Slug: ref})
	}
	if err != nil {
		return domain.Workspace{}, mapErr(err, "get workspace")
	}
	return workspaceFromRow(row), nil
}

// IsWorkspaceInUse diz se err é a recusa de excluir um workspace em uso.
func IsWorkspaceInUse(err error) (*WorkspaceInUseError, bool) {
	var e *WorkspaceInUseError
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}
