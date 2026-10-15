package service

import (
	"context"
	"fmt"
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
