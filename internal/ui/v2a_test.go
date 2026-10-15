package ui

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

func TestCompanySwitcher(t *testing.T) {
	f := uitest.Sample()
	now := time.Now().UTC()
	f.Companies = append(f.Companies, domain.Company{ID: domain.NewID(), Slug: "acme", Name: "Acme", TaskPrefix: "ACM", Timezone: "UTC", CreatedAt: now, UpdatedAt: now})
	e := newEnv(t, f)

	rec := e.get(t, "/c/hanko/tasks")
	wantContains(t, rec, `class="co-switch"`, `href="/c/acme/tasks"`, `href="/c/hanko/tasks"`, `href="/bem-vindo"`, "Nova empresa")
	// A detail page does not exist in the other company: the switcher goes to its home.
	wantContains(t, e.get(t, "/c/hanko/personas/ada"), `href="/c/acme/"`)
	// Onboarding offers the way back.
	wantContains(t, e.get(t, "/bem-vindo"), `href="/c/acme/"`, `href="/c/hanko/"`, "Ou volte para uma empresa:")
}

func TestPersonaReviewerForm(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	page := "/c/hanko/personas/mika"
	wantContains(t, e.get(t, page), `id="revisor"`, `action="/c/hanko/personas/mika/revisor"`, "Sem revisor", `list="rv-models"`, `value="haiku"`)

	// another persona
	rec := e.post(t, page+"/revisor", url.Values{"mode": {"persona"}, "reviewer_persona": {uitest.ID("persona:ada").String()}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	wantContains(t, e.get(t, page), `href="/c/hanko/personas/ada"`)

	// same role, other harness and model
	rec = e.post(t, page+"/revisor", url.Values{"mode": {"harness"}, "reviewer_agent": {"codex"}, "reviewer_model": {"gpt-5"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	wantContains(t, e.get(t, page), "codex · gpt-5", "mesmo papel")

	// no reviewer
	wantStatus(t, e.post(t, page+"/revisor", url.Values{"mode": {"none"}}, false), http.StatusSeeOther)
	wantContains(t, e.get(t, page), "Sem revisor")

	// validation errors stay on the page with the typed values
	rec = e.post(t, page+"/revisor", url.Values{"mode": {"persona"}}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, "Escolha a persona revisora.", `id="revisor"`)
	rec = e.post(t, page+"/revisor", url.Values{"mode": {"harness"}, "reviewer_model": {"opus"}}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, "Escolha o harness do revisor.", `value="opus"`)
	rec = e.post(t, page+"/revisor", url.Values{"mode": {"persona"}, "reviewer_persona": {uitest.ID("persona:mika").String()}}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity) // the select never offers itself; the service rule still holds
	wantContains(t, rec, "cannot review itself")
	// same harness and model as the author
	rec = e.post(t, page+"/revisor", url.Values{"mode": {"harness"}, "reviewer_agent": {"claude"}, "reviewer_model": {"claude-haiku"}}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, "same as the author")
}

func TestTaskModelDatalist(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	wantContains(t, e.get(t, "/c/hanko/tasks/nova"), `list="t-models"`, `<datalist id="t-models">`, `value="opus"`)
}
