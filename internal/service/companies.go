package service

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

var prefixRe = regexp.MustCompile(`^[A-Z][A-Z0-9]{0,9}$`)

// resolveSlug validates an explicit slug or derives one from the name.
func resolveSlug(slug, name string) (string, error) {
	if slug == "" {
		slug = domain.Slugify(name)
	}
	if !domain.ValidSlug(slug) {
		return "", domain.Invalid("slug", "must be lowercase letters, digits and dashes (1-63 chars)")
	}
	return slug, nil
}

// CreateCompany creates a company. Slug unique → ErrConflict.
func (s *Service) CreateCompany(ctx context.Context, actor domain.Actor, in CreateCompanyInput) (domain.Company, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return domain.Company{}, domain.Invalid("name", "required")
	}
	slug, err := resolveSlug(in.Slug, name)
	if err != nil {
		return domain.Company{}, err
	}
	prefix := strings.ToUpper(strings.TrimSpace(in.TaskPrefix))
	if prefix == "" {
		prefix = "HNK"
	}
	if !prefixRe.MatchString(prefix) {
		return domain.Company{}, domain.Invalid("task_prefix", "must be 1-10 uppercase letters/digits starting with a letter")
	}
	tz := strings.TrimSpace(in.Timezone)
	if tz == "" {
		tz = "America/Sao_Paulo"
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return domain.Company{}, domain.Invalid("timezone", "unknown IANA time zone")
	}

	// Em modo authenticated só dono/admin da instalação cria empresa; o criador vira owner dela.
	var creator *domain.User
	if id, ok := auth.IdentityFrom(ctx); ok {
		if id.User.Role == domain.RoleMember {
			return domain.Company{}, domain.ErrForbidden
		}
		creator = &id.User
	}

	var out domain.Company
	err = s.withTx(ctx, func(t *txc) error {
		row, err := t.q.CreateCompany(ctx, store.CreateCompanyParams{
			ID: domain.NewID(), Slug: slug, Name: name, TaskPrefix: prefix, Timezone: tz, Now: t.now})
		if err != nil {
			return mapErr(err, "create company")
		}
		out = companyFromRow(row)
		if creator != nil {
			if _, err := t.q.AddMember(ctx, store.AddMemberParams{CompanyID: out.ID, UserID: creator.ID,
				Role: string(domain.MemberOwner), Now: t.now}); err != nil {
				return mapErr(err, "add owner")
			}
		}
		return t.activity(actor, out.ID, "company.created", "company", out.ID,
			map[string]any{"slug": out.Slug, "name": out.Name}, "")
	})
	return out, err
}

// GetCompany resolves ref as UUID or slug. A user who is not a member gets ErrNotFound.
func (s *Service) GetCompany(ctx context.Context, ref string) (domain.Company, error) {
	q := s.read()
	var row store.Company
	var err error
	if id, perr := uuid.Parse(ref); perr == nil {
		row, err = q.GetCompanyByID(ctx, id)
	} else {
		row, err = q.GetCompanyBySlug(ctx, ref)
	}
	if err != nil {
		return domain.Company{}, mapErr(err, "get company")
	}
	if err := s.require(ctx, row.ID, domain.MemberViewer); err != nil {
		return domain.Company{}, err
	}
	return companyFromRow(row), nil
}

// ListCompanies lists the companies the caller can see: all of them without a user in the
// context or for the installation owner, otherwise only those the user is a member of.
func (s *Service) ListCompanies(ctx context.Context) ([]domain.Company, error) {
	var rows []store.Company
	var err error
	if id, ok := auth.IdentityFrom(ctx); ok && id.User.Role != domain.RoleOwner {
		rows, err = s.read().ListCompaniesForUser(ctx, id.User.ID)
	} else {
		rows, err = s.read().ListCompanies(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("list companies: %w", err)
	}
	out := make([]domain.Company, len(rows))
	for i, r := range rows {
		out[i] = companyFromRow(r)
	}
	return out, nil
}

func (s *Service) CreateWorkspace(ctx context.Context, actor domain.Actor, companyID domain.ID, in CreateWorkspaceInput) (domain.Workspace, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.Workspace{}, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return domain.Workspace{}, domain.Invalid("name", "required")
	}
	slug, err := resolveSlug(in.Slug, name)
	if err != nil {
		return domain.Workspace{}, err
	}
	path, err := s.checkWorkspacePath(in.Path)
	if err != nil {
		return domain.Workspace{}, err
	}
	var out domain.Workspace
	err = s.withTx(ctx, func(t *txc) error {
		if _, err := t.q.GetCompanyByID(ctx, companyID); err != nil {
			return mapErr(err, "company")
		}
		row, err := t.q.CreateWorkspace(ctx, store.CreateWorkspaceParams{ID: domain.NewID(), CompanyID: companyID,
			Slug: slug, Name: name, Path: path, RepoUrl: strings.TrimSpace(in.RepoURL), Now: t.now})
		if err != nil {
			return mapErr(err, "create workspace")
		}
		out = workspaceFromRow(row)
		return t.activity(actor, companyID, "workspace.created", "workspace", out.ID,
			map[string]any{"slug": out.Slug, "name": out.Name}, "")
	})
	return out, err
}

// checkWorkspacePath valida o caminho de um workspace: vazio é aceito; senão precisa
// ser absoluto, limpo (sem "..", "//" ou "/" final) e, havendo WorkspacesRoot, ficar
// estritamente dentro dela depois de resolver symlinks do trecho que já existe no disco.
func (s *Service) checkWorkspacePath(raw string) (string, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return "", nil
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
	root := resolveExisting(filepath.Clean(s.WorkspacesRoot))
	rel, err := filepath.Rel(root, resolveExisting(p))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", domain.Invalid("path", "must be inside the workspaces root")
	}
	return p, nil
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

func (s *Service) CreateProject(ctx context.Context, actor domain.Actor, companyID domain.ID, in CreateProjectInput) (domain.Project, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return domain.Project{}, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return domain.Project{}, domain.Invalid("name", "required")
	}
	slug, err := resolveSlug(in.Slug, name)
	if err != nil {
		return domain.Project{}, err
	}
	var out domain.Project
	err = s.withTx(ctx, func(t *txc) error {
		if _, err := t.q.GetCompanyByID(ctx, companyID); err != nil {
			return mapErr(err, "company")
		}
		if in.WorkspaceID != nil {
			if _, err := t.q.GetWorkspaceByID(ctx, store.GetWorkspaceByIDParams{CompanyID: companyID, ID: *in.WorkspaceID}); err != nil {
				return domain.Invalid("workspace_id", "workspace not found in company")
			}
		}
		row, err := t.q.CreateProject(ctx, store.CreateProjectParams{ID: domain.NewID(), CompanyID: companyID,
			Slug: slug, Name: name, Description: in.Description, WorkspaceID: in.WorkspaceID, Now: t.now})
		if err != nil {
			return mapErr(err, "create project")
		}
		out = projectFromRow(row)
		return t.activity(actor, companyID, "project.created", "project", out.ID,
			map[string]any{"slug": out.Slug, "name": out.Name}, "")
	})
	return out, err
}

func (s *Service) ListProjects(ctx context.Context, companyID domain.ID) ([]domain.Project, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.read().ListProjects(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	out := make([]domain.Project, len(rows))
	for i, r := range rows {
		out[i] = projectFromRow(r)
	}
	return out, nil
}

// GetProject resolves ref as UUID or slug within the company.
func (s *Service) GetProject(ctx context.Context, companyID domain.ID, ref string) (domain.Project, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return domain.Project{}, err
	}
	q := s.read()
	var row store.Project
	var err error
	if id, perr := uuid.Parse(ref); perr == nil {
		row, err = q.GetProjectByID(ctx, store.GetProjectByIDParams{CompanyID: companyID, ID: id})
	} else {
		row, err = q.GetProjectBySlug(ctx, store.GetProjectBySlugParams{CompanyID: companyID, Slug: ref})
	}
	if err != nil {
		return domain.Project{}, mapErr(err, "get project")
	}
	return projectFromRow(row), nil
}
