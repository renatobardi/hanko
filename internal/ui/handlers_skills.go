package ui

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Skills e integrações (ADR-0029). O service é acessado por interfaces opcionais: o Backend de testes e do
// preview não precisa implementá-las, e a tela responde "indisponível" em vez de quebrar.

type skillsBackend interface {
	CreateSkill(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateSkillInput) (domain.Skill, error)
	UpdateSkill(ctx context.Context, actor domain.Actor, companyID, skillID domain.ID, in service.UpdateSkillInput) (domain.Skill, error)
	DeleteSkill(ctx context.Context, actor domain.Actor, companyID, skillID domain.ID) error
	ListSkills(ctx context.Context, companyID domain.ID) ([]service.SkillView, error)
	GetSkill(ctx context.Context, companyID domain.ID, ref string) (domain.Skill, error)
	SetPersonaSkill(ctx context.Context, actor domain.Actor, companyID, personaID, skillID domain.ID, enabled bool) error
	ListPersonaSkills(ctx context.Context, companyID, personaID domain.ID) ([]domain.PersonaSkill, error)
	ListSkillPersonas(ctx context.Context, companyID, skillID domain.ID) ([]domain.SkillPersona, error)
}

func (s *server) skillsBE() (skillsBackend, bool) {
	b, ok := s.be.(skillsBackend)
	return b, ok
}

// unavailable responde às telas de skills/integrações quando o backend não as oferece.
func (s *server) unavailable(w http.ResponseWriter, r *http.Request) {
	http.Error(w, i18n.T(r.Context(), "recurso indisponível neste servidor"), http.StatusNotImplemented)
}

// skillRoutes registra as rotas de skills e integrações; chamada por routes().
// maxSkillFormBytes é o teto do corpo dos formulários de skill (corpo de 32 KiB mais os outros campos).
const maxSkillFormBytes = 128 << 10

func (s *server) skillRoutes(mux langMux) {
	co := func(h companyHandler) http.HandlerFunc { return s.withCompany(h) }
	post := func(h http.HandlerFunc) http.HandlerFunc { return s.sameOrigin(h) }
	// the forms that carry a skill body stop reading at maxSkillFormBytes
	capped := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxSkillFormBytes)
			h(w, r)
		}
	}

	mux.HandleFunc("GET /c/{company}/skills", co(s.handleSkills))
	s.skillStudioRoutes(mux) // versões, fontes e estúdio (ADR-0035)
	mux.HandleFunc("GET /c/{company}/skills/nova", co(s.handleNewSkill))
	mux.HandleFunc("POST /c/{company}/skills/nova", capped(post(co(s.handleCreateSkill))))
	mux.HandleFunc("POST /c/{company}/skills/preview", capped(post(co(s.handleSkillPreview))))
	mux.HandleFunc("GET /c/{company}/skills/{skill}", co(s.handleSkill))
	mux.HandleFunc("POST /c/{company}/skills/{skill}", capped(post(co(s.handleUpdateSkill))))
	mux.HandleFunc("POST /c/{company}/skills/{skill}/remover", post(co(s.handleDeleteSkill)))
	mux.HandleFunc("POST /c/{company}/skills/{skill}/bifurcar", post(co(s.handleForkSkill)))
	mux.HandleFunc("POST /c/{company}/skills/{skill}/personas/{persona}", post(co(s.handleSkillPersona(false))))
	mux.HandleFunc("POST /c/{company}/personas/{persona}/skills/{skill}", post(co(s.handleSkillPersona(true))))

	mux.HandleFunc("GET /c/{company}/integracoes", co(s.handleIntegrations))
	mux.HandleFunc("GET /c/{company}/integracoes/nova", co(s.handleNewIntegration))
	s.connectRoutes(mux) // assistente Conectar (ADR-0089)
	mux.HandleFunc("POST /c/{company}/integracoes/nova", post(co(s.handleCreateIntegration)))
	mux.HandleFunc("GET /c/{company}/integracoes/{integration}", co(s.handleIntegration))
	mux.HandleFunc("POST /c/{company}/integracoes/{integration}", post(co(s.handleUpdateIntegration)))
	mux.HandleFunc("POST /c/{company}/integracoes/{integration}/ativar", post(co(s.handleIntegrationEnabled)))
	mux.HandleFunc("POST /c/{company}/integracoes/{integration}/testar", post(co(s.handleTestIntegration)))
	mux.HandleFunc("POST /c/{company}/integracoes/{integration}/remover", post(co(s.handleDeleteIntegration)))
	mux.HandleFunc("POST /c/{company}/integracoes/{integration}/personas/{persona}", post(co(s.handleIntegrationPersona(false))))
	mux.HandleFunc("POST /c/{company}/personas/{persona}/integracoes/{integration}", post(co(s.handleIntegrationPersona(true))))
}

var skillNotices = map[string]string{
	"criada":    i18n.N("Skill criada."),
	"salva":     i18n.N("Skill atualizada."),
	"apagada":   i18n.N("Skill apagada."),
	"bifurcada": i18n.N("Cópia local criada."),
}

// enabledForm lê o campo enabled (1 liga, qualquer outra coisa desliga) dos interruptores.
func enabledForm(r *http.Request) bool { return r.PostFormValue("enabled") == "1" }

func (s *server) handleSkills(w http.ResponseWriter, r *http.Request, co domain.Company) {
	sb, ok := s.skillsBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	list, err := sb.ListSkills(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	base := "/c/" + co.Slug
	loc := loadLoc(co.Timezone)
	now := s.now()
	vm := views.SkillsVM{NewHref: base + "/skills/nova", Notice: i18n.T(ctx, skillNotices[r.URL.Query().Get("ok")]),
		Subtitle: i18n.T(ctx, "Habilidades reutilizáveis em markdown. Escreva uma vez e ligue para as personas que devem usá-las.")}
	if _, ok := s.studioBE(); ok { // abas e filtro de origem (ADR-0035)
		vm.Tabs = skillsTabs(ctx, base, "biblioteca")
		list, vm.Filters = skillLibraryFilter(ctx, base, r.URL.Query().Get("origem"), list)
	}
	for _, k := range list {
		vm.Rows = append(vm.Rows, views.SkillRow{Name: k.Name, Slug: k.Slug, Href: base + "/skills/" + url.PathEscape(k.Slug),
			Description: k.Description, Version: k.Version, Personas: k.Personas, Updated: agoCtx(ctx, k.UpdatedAt, now, loc),
			FromSource: k.SourceID != nil, Modified: k.SourceModified})
	}
	sh := s.shell(r, co, "skills", i18n.T(ctx, "Skills"), views.Crumb{Label: i18n.T(ctx, "Skills")})
	s.render(w, r, http.StatusOK, views.SkillsPage(sh, vm))
}

func (s *server) blankSkillVM(r *http.Request, co domain.Company) views.SkillEditorVM {
	base := "/c/" + co.Slug
	return views.SkillEditorVM{Action: base + "/skills/nova", CancelHref: base + "/skills", PreviewHref: base + "/skills/preview",
		MaxBodyKiB: domain.MaxSkillBodyBytes >> 10, FieldErr: map[string]string{}}
}

func (s *server) renderSkillEditor(w http.ResponseWriter, r *http.Request, co domain.Company, status int, vm views.SkillEditorVM) {
	base := "/c/" + co.Slug
	ctx := r.Context()
	title := i18n.T(ctx, "Nova skill")
	crumb := views.Crumb{Label: i18n.T(ctx, "Nova")}
	if vm.Edit {
		title, crumb = vm.Name, views.Crumb{Label: vm.Name}
	}
	vm.Preview = renderMD(vm.Body)
	sh := s.shell(r, co, "skills", title, views.Crumb{Label: i18n.T(ctx, "Skills"), Href: base + "/skills"}, crumb)
	s.render(w, r, status, views.SkillEditorPage(sh, vm))
}

func (s *server) handleNewSkill(w http.ResponseWriter, r *http.Request, co domain.Company) {
	if _, ok := s.skillsBE(); !ok {
		s.unavailable(w, r)
		return
	}
	s.renderSkillEditor(w, r, co, http.StatusOK, s.blankSkillVM(r, co))
}

// skillFormError põe o erro do service no campo certo do formulário.
func skillFormError(ctx context.Context, vm *views.SkillEditorVM, err error) {
	var ve *domain.ValidationError
	if errors.As(err, &ve) {
		msg := map[string]string{
			"name":        i18n.T(ctx, "Dê um nome à skill."),
			"slug":        i18n.T(ctx, "Identificador inválido ou reservado: use letras minúsculas, números e hífen."),
			"description": i18n.T(ctx, "Descrição longa demais."),
			"body":        i18n.T(ctx, "Escreva as instruções (até o limite de tamanho) em markdown válido."),
		}[ve.Field]
		if msg == "" {
			msg = ve.Field + ": " + ve.Reason + "."
		}
		vm.FieldErr[ve.Field] = msg
		vm.Error = msg
		return
	}
	_, title, msg := friendly(ctx, err)
	vm.Error = title + ". " + msg
}

func (s *server) handleCreateSkill(w http.ResponseWriter, r *http.Request, co domain.Company) {
	sb, ok := s.skillsBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	vm := s.blankSkillVM(r, co)
	vm.Name, vm.Slug, vm.Description, vm.Body = trimForm(r, "name"), trimForm(r, "slug"), trimForm(r, "description"), r.PostFormValue("body")
	k, err := sb.CreateSkill(r.Context(), actorFrom(r.Context()), co.ID, service.CreateSkillInput{Name: vm.Name, Slug: vm.Slug, Description: vm.Description, Body: vm.Body})
	if err != nil {
		skillFormError(r.Context(), &vm, err)
		s.renderSkillEditor(w, r, co, statusFor(err), vm)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/skills/"+url.PathEscape(k.Slug)+"?ok=criada")
}

func (s *server) skillEditorVM(r *http.Request, co domain.Company, sb skillsBackend, k domain.Skill) (views.SkillEditorVM, error) {
	ctx := r.Context()
	base := "/c/" + co.Slug
	ps, err := sb.ListSkillPersonas(ctx, co.ID, k.ID)
	if err != nil {
		return views.SkillEditorVM{}, err
	}
	loc := loadLoc(co.Timezone)
	vm := s.blankSkillVM(r, co)
	vm.Edit, vm.Name, vm.Slug, vm.Description, vm.Body = true, k.Name, k.Slug, k.Description, k.Body
	vm.Action = base + "/skills/" + url.PathEscape(k.Slug)
	vm.DeleteHref = vm.Action + "/remover"
	vm.Version, vm.Updated, vm.UpdatedBy = k.Version, agoCtx(ctx, k.UpdatedAt, s.now(), loc), actorLabel(ctx, k.UpdatedBy)
	vm.Notice = i18n.T(ctx, skillNotices[r.URL.Query().Get("ok")])
	if _, ok := s.studioBE(); ok { // abas Visão geral/Versões/Personas e o aviso de skill de fonte (ADR-0035)
		vm.Tabs = skillPageTabs(ctx, base, k.Slug, "visao")
		vm.SourceNote = s.sourceNote(ctx, co, k)
	}
	if fb, ok := s.be.(skillForkBackend); ok { // fork de skill (ADR-0044)
		vm.ForkHref = base + "/skills/" + url.PathEscape(k.Slug) + "/bifurcar"
		if forks, err := fb.ListSkillForks(ctx, co.ID, k.ID); err == nil {
			for _, f := range forks {
				vm.Forks = append(vm.Forks, views.ForkLink{Name: f.Name, Slug: f.Slug, Href: base + "/skills/" + url.PathEscape(f.Slug)})
			}
		}
		if k.ForkedFromID != nil {
			if o, err := sb.GetSkill(ctx, co.ID, k.ForkedFromID.String()); err == nil {
				vm.ForkedFrom, vm.ForkedFromHref = o.Name+" (v"+strconv.Itoa(k.ForkedFromRevision)+")", base+"/skills/"+url.PathEscape(o.Slug)
			}
		}
	}
	for _, p := range ps {
		vm.Personas = append(vm.Personas, views.ToggleRow{Name: p.Name, Slug: p.Slug, Href: base + "/personas/" + url.PathEscape(p.Slug), Enabled: p.Enabled,
			ToggleHref: base + "/skills/" + url.PathEscape(k.Slug) + "/personas/" + url.PathEscape(p.Slug)})
	}
	return vm, nil
}

func actorLabel(ctx context.Context, a domain.Actor) string {
	switch a.Type {
	case domain.ActorPersona:
		return i18n.T(ctx, "persona")
	case domain.ActorSystem:
		return i18n.T(ctx, "sistema")
	}
	return i18n.T(ctx, "humano")
}

func (s *server) handleSkill(w http.ResponseWriter, r *http.Request, co domain.Company) {
	sb, ok := s.skillsBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	k, err := sb.GetSkill(r.Context(), co.ID, r.PathValue("skill"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm, err := s.skillEditorVM(r, co, sb, k)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.renderSkillEditor(w, r, co, http.StatusOK, vm)
}

func (s *server) handleUpdateSkill(w http.ResponseWriter, r *http.Request, co domain.Company) {
	sb, ok := s.skillsBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	k, err := sb.GetSkill(ctx, co.ID, r.PathValue("skill"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	name, desc, body := trimForm(r, "name"), trimForm(r, "description"), r.PostFormValue("body")
	if _, err := sb.UpdateSkill(ctx, actorFrom(ctx), co.ID, k.ID, service.UpdateSkillInput{Name: &name, Description: &desc, Body: &body}); err != nil {
		vm, verr := s.skillEditorVM(r, co, sb, k)
		if verr != nil {
			s.fail(w, r, &co, verr)
			return
		}
		vm.Name, vm.Description, vm.Body = name, desc, body
		skillFormError(ctx, &vm, err)
		s.renderSkillEditor(w, r, co, statusFor(err), vm)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/skills/"+url.PathEscape(k.Slug)+"?ok=salva")
}

func (s *server) handleDeleteSkill(w http.ResponseWriter, r *http.Request, co domain.Company) {
	sb, ok := s.skillsBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	k, err := sb.GetSkill(ctx, co.ID, r.PathValue("skill"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	if err := sb.DeleteSkill(ctx, actorFrom(ctx), co.ID, k.ID); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/skills?ok=apagada")
}

// handleSkillPreview devolve o markdown renderizado (mesmo renderizador sanitizado das tasks).
func (s *server) handleSkillPreview(w http.ResponseWriter, r *http.Request, _ domain.Company) {
	s.render(w, r, http.StatusOK, views.SkillPreview(renderMD(r.PostFormValue("body"))))
}

// handleSkillPersona liga ou desliga uma skill para uma persona. fromPersona diz de qual página veio o
// POST, e para onde voltar.
func (s *server) handleSkillPersona(fromPersona bool) companyHandler {
	return func(w http.ResponseWriter, r *http.Request, co domain.Company) {
		sb, ok := s.skillsBE()
		if !ok {
			s.unavailable(w, r)
			return
		}
		ctx := r.Context()
		p, err := s.be.GetPersona(ctx, co.ID, r.PathValue("persona"))
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		k, err := sb.GetSkill(ctx, co.ID, r.PathValue("skill"))
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		if err := sb.SetPersonaSkill(ctx, actorFrom(ctx), co.ID, p.ID, k.ID, enabledForm(r)); err != nil {
			s.fail(w, r, &co, err)
			return
		}
		base := "/c/" + co.Slug
		if fromPersona {
			redirect(w, r, base+"/personas/"+url.PathEscape(p.Slug)+"#skills")
			return
		}
		redirect(w, r, base+"/skills/"+url.PathEscape(k.Slug)+"#personas")
	}
}

// ---- painéis da página da persona ----

// personaExtras monta os painéis de skills e servidores MCP da página da persona.
func (s *server) personaExtras(ctx context.Context, co domain.Company, p domain.Persona) views.PersonaExtrasVM {
	base := "/c/" + co.Slug
	vm := views.PersonaExtrasVM{SkillsHref: base + "/skills", IntegrationsHref: base + "/integracoes"}
	if sb, ok := s.skillsBE(); ok {
		vm.Show = true
		if ps, err := sb.ListPersonaSkills(ctx, co.ID, p.ID); err == nil {
			for _, k := range ps {
				note := k.Description
				if k.PinnedRevision != nil { // versão fixada (ADR-0035)
					note = i18n.Tf(ctx, "versão fixada: v%d", *k.PinnedRevision) + " · " + note
				}
				vm.Skills = append(vm.Skills, views.ToggleRow{Name: k.Name, Slug: k.Slug, Note: note, Enabled: k.Enabled,
					Href:       base + "/skills/" + url.PathEscape(k.Slug),
					ToggleHref: base + "/personas/" + url.PathEscape(p.Slug) + "/skills/" + url.PathEscape(k.Slug)})
			}
		}
	}
	if ib, ok := s.integrationsBE(); ok {
		vm.Show = true
		if ms, err := ib.ListPersonaIntegrations(ctx, co.ID, p.ID); err == nil {
			sort.SliceStable(ms, func(i, j int) bool { return ms[i].Slug < ms[j].Slug })
			for _, m := range ms {
				note := ""
				if !m.Enabled {
					note = i18n.T(ctx, "integração desligada: não entra nas execuções")
				}
				vm.MCPs = append(vm.MCPs, views.ToggleRow{Name: m.Name, Slug: m.Slug, Note: note, Enabled: m.Linked, Fixed: m.AllPersonas,
					Href:       base + "/integracoes/" + url.PathEscape(m.Slug),
					ToggleHref: base + "/personas/" + url.PathEscape(p.Slug) + "/integracoes/" + url.PathEscape(m.Slug)})
			}
		}
	}
	return vm
}

// skillForkBackend é o fork de skills (ADR-0044); opcional como os demais.
type skillForkBackend interface {
	ForkSkill(ctx context.Context, actor domain.Actor, companyID, skillID domain.ID, in service.ForkSkillInput) (domain.Skill, error)
	ListSkillForks(ctx context.Context, companyID, skillID domain.ID) ([]domain.Skill, error)
}

func (s *server) handleForkSkill(w http.ResponseWriter, r *http.Request, co domain.Company) {
	sb, ok := s.skillsBE()
	fb, ok2 := s.be.(skillForkBackend)
	if !ok || !ok2 {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	k, err := sb.GetSkill(ctx, co.ID, r.PathValue("skill"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	f, err := fb.ForkSkill(ctx, actorFrom(ctx), co.ID, k.ID, service.ForkSkillInput{Slug: trimForm(r, "slug"), MovePersonas: trimForm(r, "move_personas") == "1"})
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/skills/"+url.PathEscape(f.Slug)+"?ok=bifurcada")
}
