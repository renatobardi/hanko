package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

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

	var out domain.Company
	err = s.withTx(ctx, func(t *txc) error {
		row, err := t.q.CreateCompany(ctx, store.CreateCompanyParams{
			ID: domain.NewID(), Slug: slug, Name: name, TaskPrefix: prefix, Timezone: tz, Now: t.now})
		if err != nil {
			return mapErr(err, "create company")
		}
		out = companyFromRow(row)
		return t.activity(actor, out.ID, "company.created", "company", out.ID,
			map[string]any{"slug": out.Slug, "name": out.Name}, "")
	})
	return out, err
}

// GetCompany resolves ref as UUID or slug.
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
	return companyFromRow(row), nil
}

func (s *Service) ListCompanies(ctx context.Context) ([]domain.Company, error) {
	rows, err := s.read().ListCompanies(ctx)
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
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return domain.Workspace{}, domain.Invalid("name", "required")
	}
	slug, err := resolveSlug(in.Slug, name)
	if err != nil {
		return domain.Workspace{}, err
	}
	var out domain.Workspace
	err = s.withTx(ctx, func(t *txc) error {
		if _, err := t.q.GetCompanyByID(ctx, companyID); err != nil {
			return mapErr(err, "company")
		}
		row, err := t.q.CreateWorkspace(ctx, store.CreateWorkspaceParams{ID: domain.NewID(), CompanyID: companyID,
			Slug: slug, Name: name, Path: strings.TrimSpace(in.Path), RepoUrl: strings.TrimSpace(in.RepoURL), Now: t.now})
		if err != nil {
			return mapErr(err, "create workspace")
		}
		out = workspaceFromRow(row)
		return t.activity(actor, companyID, "workspace.created", "workspace", out.ID,
			map[string]any{"slug": out.Slug, "name": out.Name}, "")
	})
	return out, err
}

func (s *Service) ListWorkspaces(ctx context.Context, companyID domain.ID) ([]domain.Workspace, error) {
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
