package ui

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

// skillsFake estende o Fake com os métodos opcionais de skills e integrações (ADR-0029). Fica no teste de
// propósito: o Fake compartilhado continua sem esses métodos, e o teste "indisponível" usa o Fake puro.
type skillsFake struct {
	*uitest.Fake
	skills   []domain.Skill
	ints     []service.IntegrationView
	skillOn  map[[2]domain.ID]bool
	intOn    map[[2]domain.ID]bool
	revealed string
}

func newSkillsFake() *skillsFake {
	f := &skillsFake{Fake: uitest.Sample(), skillOn: map[[2]domain.ID]bool{}, intOn: map[[2]domain.ID]bool{}}
	now := uitest.Now()
	f.skills = []domain.Skill{{ID: uitest.ID("skill:pr"), Slug: "revisar-pr", Name: "Revisar PR", Description: "Use ao revisar PR.",
		Body: "# Checklist\n\n- testes\n- <script>alert(1)</script>\n", Version: 2, CreatedAt: now, UpdatedAt: now}}
	f.ints = []service.IntegrationView{{Integration: domain.Integration{ID: uitest.ID("int:mcp"), Kind: domain.IntegrationMCP, Slug: "github", Name: "GitHub",
		Enabled: true, Config: domain.IntegrationConfig{Transport: "http", URL: "https://mcp.example.com/mcp", HeaderNames: []string{"Authorization"}}, SecretSet: true, CreatedAt: now, UpdatedAt: now}}}
	return f
}

func (f *skillsFake) deny(ctx context.Context) error {
	if r, _ := f.MyRole(ctx, uuid.Nil); !r.AtLeast(domain.MemberAdmin) {
		return domain.ErrForbidden
	}
	return nil
}

func (f *skillsFake) CreateSkill(ctx context.Context, _ domain.Actor, _ domain.ID, in service.CreateSkillInput) (domain.Skill, error) {
	if err := f.deny(ctx); err != nil {
		return domain.Skill{}, err
	}
	k := domain.Skill{ID: uitest.ID("skill:" + in.Slug), Slug: in.Slug, Name: in.Name, Description: in.Description, Body: in.Body, Version: 1}
	if k.Slug == "" {
		k.Slug = strings.ToLower(strings.ReplaceAll(in.Name, " ", "-"))
	}
	f.skills = append(f.skills, k)
	return k, nil
}

func (f *skillsFake) UpdateSkill(ctx context.Context, _ domain.Actor, _, id domain.ID, in service.UpdateSkillInput) (domain.Skill, error) {
	if err := f.deny(ctx); err != nil {
		return domain.Skill{}, err
	}
	for i := range f.skills {
		if f.skills[i].ID == id {
			if in.Body != nil {
				f.skills[i].Body = *in.Body
			}
			if in.Name != nil {
				f.skills[i].Name = *in.Name
			}
			f.skills[i].Version++
			return f.skills[i], nil
		}
	}
	return domain.Skill{}, domain.ErrNotFound
}

func (f *skillsFake) DeleteSkill(ctx context.Context, _ domain.Actor, _, id domain.ID) error {
	if err := f.deny(ctx); err != nil {
		return err
	}
	for i := range f.skills {
		if f.skills[i].ID == id {
			f.skills = append(f.skills[:i], f.skills[i+1:]...)
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *skillsFake) ListSkills(context.Context, domain.ID) ([]service.SkillView, error) {
	var out []service.SkillView
	for _, k := range f.skills {
		n := 0
		for key, on := range f.skillOn {
			if on && key[1] == k.ID {
				n++
			}
		}
		out = append(out, service.SkillView{Skill: k, Personas: n})
	}
	return out, nil
}

func (f *skillsFake) GetSkill(_ context.Context, _ domain.ID, ref string) (domain.Skill, error) {
	for _, k := range f.skills {
		if k.Slug == ref || k.ID.String() == ref {
			return k, nil
		}
	}
	return domain.Skill{}, domain.ErrNotFound
}

func (f *skillsFake) SetPersonaSkill(ctx context.Context, _ domain.Actor, _, personaID, skillID domain.ID, on bool) error {
	if err := f.deny(ctx); err != nil {
		return err
	}
	f.skillOn[[2]domain.ID{personaID, skillID}] = on
	return nil
}

func (f *skillsFake) ListPersonaSkills(_ context.Context, _, personaID domain.ID) ([]domain.PersonaSkill, error) {
	var out []domain.PersonaSkill
	for _, k := range f.skills {
		out = append(out, domain.PersonaSkill{SkillID: k.ID, Slug: k.Slug, Name: k.Name, Description: k.Description, Enabled: f.skillOn[[2]domain.ID{personaID, k.ID}]})
	}
	return out, nil
}

func (f *skillsFake) ListSkillPersonas(_ context.Context, _, skillID domain.ID) ([]domain.SkillPersona, error) {
	var out []domain.SkillPersona
	ps, _ := f.ListPersonas(context.Background(), uuid.Nil)
	for _, p := range ps {
		out = append(out, domain.SkillPersona{PersonaID: p.ID, Slug: p.Slug, Name: p.Name, Enabled: f.skillOn[[2]domain.ID{p.ID, skillID}]})
	}
	return out, nil
}

func (f *skillsFake) CreateIntegration(ctx context.Context, _ domain.Actor, _ domain.ID, in service.CreateIntegrationInput) (service.IntegrationView, error) {
	if err := f.deny(ctx); err != nil {
		return service.IntegrationView{}, err
	}
	v := service.IntegrationView{Integration: domain.Integration{ID: uitest.ID("int:" + in.Name), Kind: in.Kind, Slug: strings.ToLower(in.Name), Name: in.Name,
		Enabled: true, Events: in.Events, Config: domain.IntegrationConfig{URL: in.URL}, SecretSet: true}}
	if in.Kind == domain.IntegrationWebhook && in.Secret == "" {
		v.RevealedSecret = f.revealed
		v.SecretHint = "whsec_ab"
	}
	f.ints = append(f.ints, v)
	return v, nil
}

func (f *skillsFake) UpdateIntegration(ctx context.Context, _ domain.Actor, _, id domain.ID, in service.UpdateIntegrationInput) (service.IntegrationView, error) {
	if err := f.deny(ctx); err != nil {
		return service.IntegrationView{}, err
	}
	for i := range f.ints {
		if f.ints[i].ID == id {
			if in.Enabled != nil {
				f.ints[i].Enabled = *in.Enabled
			}
			return f.ints[i], nil
		}
	}
	return service.IntegrationView{}, domain.ErrNotFound
}

func (f *skillsFake) DeleteIntegration(ctx context.Context, _ domain.Actor, _, id domain.ID) error {
	if err := f.deny(ctx); err != nil {
		return err
	}
	for i := range f.ints {
		if f.ints[i].ID == id {
			f.ints = append(f.ints[:i], f.ints[i+1:]...)
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *skillsFake) ListIntegrations(context.Context, domain.ID) ([]service.IntegrationView, error) {
	return f.ints, nil
}

func (f *skillsFake) GetIntegration(_ context.Context, _ domain.ID, ref string) (service.IntegrationView, error) {
	for _, v := range f.ints {
		if v.Slug == ref || v.ID.String() == ref {
			return v, nil
		}
	}
	return service.IntegrationView{}, domain.ErrNotFound
}

func (f *skillsFake) ListIntegrationPersonas(_ context.Context, _, id domain.ID) ([]domain.IntegrationPersona, error) {
	var out []domain.IntegrationPersona
	ps, _ := f.ListPersonas(context.Background(), uuid.Nil)
	for _, p := range ps {
		out = append(out, domain.IntegrationPersona{PersonaID: p.ID, Slug: p.Slug, Name: p.Name, Enabled: f.intOn[[2]domain.ID{p.ID, id}]})
	}
	return out, nil
}

func (f *skillsFake) ListPersonaIntegrations(_ context.Context, _, personaID domain.ID) ([]service.PersonaIntegration, error) {
	var out []service.PersonaIntegration
	for _, v := range f.ints {
		if v.Kind == domain.IntegrationMCP {
			out = append(out, service.PersonaIntegration{IntegrationID: v.ID, Slug: v.Slug, Name: v.Name, Enabled: v.Enabled, Linked: f.intOn[[2]domain.ID{personaID, v.ID}]})
		}
	}
	return out, nil
}

func (f *skillsFake) SetPersonaIntegration(ctx context.Context, _ domain.Actor, _, personaID, id domain.ID, on bool) error {
	if err := f.deny(ctx); err != nil {
		return err
	}
	f.intOn[[2]domain.ID{personaID, id}] = on
	return nil
}

func (f *skillsFake) ListWebhookDeliveries(context.Context, domain.ID, domain.ID, int) ([]domain.WebhookDelivery, error) {
	code, ms := 200, 42
	return []domain.WebhookDelivery{{ID: uitest.ID("del"), Event: domain.EventTaskCreated, Status: domain.DeliverySuccess, Attempts: 1, HTTPStatus: &code, LatencyMs: &ms, CreatedAt: time.Now()}}, nil
}

func (f *skillsFake) TestIntegration(ctx context.Context, _ domain.Actor, _, _ domain.ID) (service.IntegrationTest, error) {
	if err := f.deny(ctx); err != nil {
		return service.IntegrationTest{}, err
	}
	return service.IntegrationTest{OK: true, Status: 200, LatencyMs: 12}, nil
}

func newSkillsEnv(t *testing.T, role domain.MemberRole) (*env, *skillsFake) {
	t.Helper()
	f := newSkillsFake()
	f.MyRoleIs = role
	e := newEnv(t, f.Fake)
	e.h = serveWith(t, f)
	return e, f
}

// serveWith monta o servidor sobre o backend estendido (newEnv só aceita o Fake puro).
func serveWith(t *testing.T, be Backend) http.Handler {
	t.Helper()
	s := &server{be: be, broker: events.NewBroker(), version: "test", now: uitest.Now, sseTick: 50 * time.Millisecond}
	s.log = testLogger(t)
	mux := http.NewServeMux()
	s.routes(mux)
	return mux
}

func TestSkillsList(t *testing.T) {
	e, _ := newSkillsEnv(t, domain.MemberOwner)
	rec := e.get(t, "/c/hanko/skills")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Revisar PR", "revisar-pr", `href="/c/hanko/skills/nova"`)
	wantNotContains(t, rec, "Em breve")
}

func TestSkillEditorAndPreview(t *testing.T) {
	e, _ := newSkillsEnv(t, domain.MemberAdmin)
	rec := e.get(t, "/c/hanko/skills/revisar-pr")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `id="md-preview"`, "Checklist", `action="/c/hanko/skills/revisar-pr"`)
	// o markdown é sanitizado: o <script> do corpo não vira tag
	wantNotContains(t, rec, "<script>alert(1)</script>")

	rec = e.post(t, "/c/hanko/skills/preview", url.Values{"body": {"# Título\n\n<script>x</script>"}}, true)
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "<h1")
	wantNotContains(t, rec, "<script>")
}

// O corpo dos formulários de skill tem teto: um POST gigante não é lido inteiro.
func TestSkillFormBodyIsCapped(t *testing.T) {
	e, f := newSkillsEnv(t, domain.MemberAdmin)
	rec := e.post(t, "/c/hanko/skills/preview", url.Values{"body": {strings.Repeat("x", 300<<10)}}, true)
	wantNotContains(t, rec, strings.Repeat("x", 1000))
	// o backend de teste não valida nada: o que chega a ele mostra até onde o formulário foi lido
	rec = e.post(t, "/c/hanko/skills/nova", url.Values{"name": {"Grande"}, "description": {"d"}, "body": {strings.Repeat("x", 300<<10)}}, false)
	for _, k := range f.skills {
		if len(k.Body) > maxSkillFormBytes {
			t.Fatalf("o formulário foi lido além do teto: %d bytes (code %d)", len(k.Body), rec.Code)
		}
	}
}

func TestSkillCreateUpdateDelete(t *testing.T) {
	e, f := newSkillsEnv(t, domain.MemberAdmin)
	rec := e.post(t, "/c/hanko/skills/nova", url.Values{"name": {"Escrever testes"}, "description": {"Use ao testar."}, "body": {"Corpo."}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if len(f.skills) != 2 {
		t.Fatalf("skills = %d, quer 2", len(f.skills))
	}
	rec = e.post(t, "/c/hanko/skills/revisar-pr", url.Values{"name": {"Revisar PR"}, "description": {"d"}, "body": {"novo"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if f.skills[0].Version != 3 || f.skills[0].Body != "novo" {
		t.Errorf("skill = v%d %q", f.skills[0].Version, f.skills[0].Body)
	}
	rec = e.post(t, "/c/hanko/skills/revisar-pr/remover", url.Values{}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if len(f.skills) != 1 {
		t.Errorf("skills = %d após apagar, quer 1", len(f.skills))
	}
}

func TestSkillToggleOnPersonaAndSkillPages(t *testing.T) {
	e, f := newSkillsEnv(t, domain.MemberAdmin)
	ada := uitest.ID("persona:ada")
	ps, _ := f.ListPersonas(context.Background(), uuid.Nil)
	for _, p := range ps {
		if p.Slug == "ada" {
			ada = p.ID
		}
	}
	wantContains(t, e.get(t, "/c/hanko/personas/ada"), "Revisar PR", "/personas/ada/skills/revisar-pr")
	rec := e.post(t, "/c/hanko/personas/ada/skills/revisar-pr", url.Values{"enabled": {"1"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if !f.skillOn[[2]domain.ID{ada, uitest.ID("skill:pr")}] {
		t.Fatal("skill não foi habilitada para a persona")
	}
	rec = e.post(t, "/c/hanko/skills/revisar-pr/personas/ada", url.Values{"enabled": {"0"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if f.skillOn[[2]domain.ID{ada, uitest.ID("skill:pr")}] {
		t.Fatal("skill não foi desabilitada")
	}
}

func TestSkillsIntegrationsControlsByRole(t *testing.T) {
	marks := []struct {
		page, mark string
		min        domain.MemberRole
	}{
		{"/c/hanko/skills", `href="/c/hanko/skills/nova"`, domain.MemberAdmin},
		{"/c/hanko/skills/revisar-pr", `action="/c/hanko/skills/revisar-pr"`, domain.MemberAdmin},
		{"/c/hanko/skills/revisar-pr", "/skills/revisar-pr/remover", domain.MemberAdmin},
		{"/c/hanko/personas/ada", "/personas/ada/skills/revisar-pr", domain.MemberAdmin},
		{"/c/hanko/personas/ada", "/personas/ada/integracoes/github", domain.MemberAdmin},
		{"/c/hanko/integracoes", `/integracoes/nova?tipo=`, domain.MemberAdmin},
		{"/c/hanko/integracoes/github", "/integracoes/github/testar", domain.MemberAdmin},
		{"/c/hanko/integracoes/github", "/integracoes/github/remover", domain.MemberAdmin},
		{"/c/hanko/integracoes/github", "/integracoes/github/ativar", domain.MemberAdmin},
	}
	for _, role := range allRoles {
		t.Run(string(role), func(t *testing.T) {
			e, _ := newSkillsEnv(t, role)
			bodies := map[string]string{}
			for _, c := range marks {
				body, ok := bodies[c.page]
				if !ok {
					rec := e.get(t, c.page)
					wantStatus(t, rec, http.StatusOK)
					body = rec.Body.String()
					bodies[c.page] = body
				}
				if got, want := strings.Contains(body, c.mark), role.AtLeast(c.min); got != want {
					t.Errorf("%s: %q presente=%v, quer %v (papel %s)", c.page, c.mark, got, want, role)
				}
			}
			// todos leem o conteúdo
			wantContains(t, e.get(t, "/c/hanko/skills"), "Revisar PR")
			wantContains(t, e.get(t, "/c/hanko/integracoes"), "GitHub")
		})
	}
}

func TestForgedSkillPostForbidden(t *testing.T) {
	e, f := newSkillsEnv(t, domain.MemberMember)
	rec := e.post(t, "/c/hanko/skills/nova", url.Values{"name": {"X"}, "body": {"y"}}, false)
	wantStatus(t, rec, http.StatusForbidden)
	rec = e.post(t, "/c/hanko/integracoes/github/ativar", url.Values{"enabled": {"0"}}, false)
	wantStatus(t, rec, http.StatusForbidden)
	if len(f.skills) != 1 || !f.ints[0].Enabled {
		t.Error("um POST forjado alterou o estado")
	}
}

func TestIntegrationsListNeverShowsSecrets(t *testing.T) {
	e, f := newSkillsEnv(t, domain.MemberOwner)
	f.ints[0].SecretHint = "whsec_ab"
	body := e.get(t, "/c/hanko/integracoes").Body.String() + e.get(t, "/c/hanko/integracoes/github").Body.String()
	for _, want := range []string{"GitHub", "Authorization", "mcp.example.com"} {
		if !strings.Contains(body, want) {
			t.Errorf("falta %q", want)
		}
	}
}

func TestWebhookSecretShownOnce(t *testing.T) {
	e, f := newSkillsEnv(t, domain.MemberAdmin)
	f.revealed = "whsec_REVEALED_ONCE_0123456789"
	rec := e.post(t, "/c/hanko/integracoes/nova?tipo=webhook", url.Values{"name": {"Deploy"}, "url": {"https://hooks.example.com/x"}, "events": {"task.created"}}, false)
	wantStatus(t, rec, http.StatusCreated)
	wantContains(t, rec, f.revealed)
	// depois, a página da integração não traz mais o segredo
	rec = e.get(t, "/c/hanko/integracoes/deploy")
	wantStatus(t, rec, http.StatusOK)
	wantNotContains(t, rec, f.revealed)
	wantContains(t, rec, "whsec_ab")
}

func TestIntegrationTestAndDeliveries(t *testing.T) {
	e, f := newSkillsEnv(t, domain.MemberAdmin)
	f.ints = append(f.ints, service.IntegrationView{Integration: domain.Integration{ID: uitest.ID("int:wh"), Kind: domain.IntegrationWebhook, Slug: "deploy", Name: "Deploy", Enabled: true,
		Events: []string{domain.EventTaskCreated}, Config: domain.IntegrationConfig{URL: "https://hooks.example.com/x"}}})
	wantContains(t, e.get(t, "/c/hanko/integracoes/deploy"), "task.created", "42")
	rec := e.post(t, "/c/hanko/integracoes/deploy/testar", url.Values{}, true)
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `class="test-result"`)
}

func TestIntegrationPersonaToggleAndEnabled(t *testing.T) {
	e, f := newSkillsEnv(t, domain.MemberAdmin)
	rec := e.post(t, "/c/hanko/integracoes/github/ativar", url.Values{"enabled": {"0"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if f.ints[0].Enabled {
		t.Error("integração continuou ligada")
	}
	rec = e.post(t, "/c/hanko/personas/ada/integracoes/github", url.Values{"enabled": {"1"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	on := false
	for _, v := range f.intOn {
		on = on || v
	}
	if !on {
		t.Error("servidor MCP não foi ligado para a persona")
	}
}

func TestSkillsUnavailableWithoutBackend(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	for _, p := range []string{"/c/hanko/skills", "/c/hanko/integracoes"} {
		wantStatus(t, e.get(t, p), http.StatusNotImplemented)
	}
	// a persona continua abrindo, sem os painéis
	wantStatus(t, e.get(t, "/c/hanko/personas/ada"), http.StatusOK)
}
