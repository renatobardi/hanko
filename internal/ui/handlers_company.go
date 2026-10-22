package ui

import (
	"context"
	"net/http"
	"strings"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

func (s *server) handleRoot(w http.ResponseWriter, r *http.Request) {
	cs, err := s.visibleCompanies(r.Context())
	if err != nil {
		s.fail(w, r, nil, err)
		return
	}
	if len(cs) == 0 {
		http.Redirect(w, r, "/bem-vindo", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/c/"+cs[0].Slug+"/", http.StatusSeeOther)
}

// visibleCompanies are the companies the person may see and switch to: the service narrows them
// to the memberships of the signed-in user (all of them for the installation owner and in trusted mode).
func (s *server) visibleCompanies(ctx context.Context) ([]domain.Company, error) {
	return s.be.ListCompanies(ctx)
}

// withSwitcher attaches the company switcher data to the render context of company pages and of
// the onboarding. A failure to list companies only hides the switcher; it never breaks the page.
func (s *server) withSwitcher(ctx context.Context, r *http.Request) context.Context {
	path := r.URL.Path
	if !strings.HasPrefix(path, "/c/") && path != "/bem-vindo" && path != "/empresas" {
		return ctx
	}
	cs, err := s.visibleCompanies(ctx)
	if err != nil {
		return ctx
	}
	sw := views.Switcher{}
	for _, c := range cs {
		sw.Companies = append(sw.Companies, views.CompanyLink{Slug: c.Slug, Name: c.Name})
	}
	// "/c/{company}/{section}/…": keep the section only for list-level pages that every company has.
	if rest, ok := strings.CutPrefix(path, "/c/"); ok {
		if parts := strings.Split(rest, "/"); (len(parts) == 2 || (len(parts) == 3 && parts[2] == "")) && views.SwitchSections[parts[1]] {
			sw.Section = parts[1]
		}
	}
	return views.WithSwitcher(ctx, sw)
}

func (s *server) handleOnboarding(w http.ResponseWriter, r *http.Request) {
	// Em modo authenticated só dono/admin da instalação cria empresa. Quem não pode e não participa de
	// nenhuma vê "peça um convite" (e não um formulário que devolveria 403); quem já tem empresa volta.
	if id, ok := auth.IdentityFrom(r.Context()); ok && id.User.Role == domain.RoleMember {
		cs, err := s.visibleCompanies(r.Context())
		if err != nil {
			s.fail(w, r, nil, err)
			return
		}
		if len(cs) > 0 {
			http.Redirect(w, r, "/c/"+cs[0].Slug+"/", http.StatusSeeOther)
			return
		}
		s.render(w, r, http.StatusOK, views.NoCompanyPage(s.version))
		return
	}
	s.render(w, r, http.StatusOK, views.OnboardingPage(views.OnboardingVM{TaskPrefix: "HNK"}, s.version))
}

func (s *server) handleCreateCompany(w http.ResponseWriter, r *http.Request) {
	vm := views.OnboardingVM{Name: trimForm(r, "name"), TaskPrefix: strings.ToUpper(trimForm(r, "prefix"))}
	if vm.Name == "" {
		vm.Error = i18n.T(r.Context(), "Dê um nome à empresa.")
		s.render(w, r, http.StatusUnprocessableEntity, views.OnboardingPage(vm, s.version))
		return
	}
	co, err := s.be.CreateCompany(r.Context(), actorFrom(r.Context()), service.CreateCompanyInput{Name: vm.Name, TaskPrefix: vm.TaskPrefix})
	if err != nil {
		_, title, msg := friendly(r.Context(), err)
		vm.Error = title + ". " + msg
		s.render(w, r, statusFor(err), views.OnboardingPage(vm, s.version))
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/primeiros-passos")
}
