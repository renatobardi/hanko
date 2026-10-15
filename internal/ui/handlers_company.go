package ui

import (
	"net/http"
	"strings"

	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

func (s *server) handleRoot(w http.ResponseWriter, r *http.Request) {
	cs, err := s.be.ListCompanies(r.Context())
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

func (s *server) handleOnboarding(w http.ResponseWriter, r *http.Request) {
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
	redirect(w, r, "/c/"+co.Slug+"/")
}
