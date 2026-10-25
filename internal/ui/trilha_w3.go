package ui

import (
	"context"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/renatobardi/hanko"
	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/changelog"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/teams"
	"github.com/renatobardi/hanko/internal/ui/md"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Trilha contínua, onda 3 (ADR-0061): assistente de primeiros passos, favoritos e recentes, perfil próprio e
// "O que há de novo". Tudo opcional no Backend (o fake dos testes também implementa).

// w3Backend é o que as telas desta fatia pedem ao service.
type w3Backend interface {
	NavItems(ctx context.Context, companyID domain.ID) (favorites, recents []domain.NavItem, err error)
	SetFavorite(ctx context.Context, actor domain.Actor, companyID domain.ID, path, title string, on bool) error
	TouchRecent(ctx context.Context, companyID domain.ID, path, title string) error
	GetProfile(ctx context.Context) (domain.UserProfile, error)
	UpdateProfile(ctx context.Context, in domain.ProfileInput) (domain.UserProfile, error)
	ProfileOf(ctx context.Context, userID domain.ID) domain.UserProfile
	WhatsNewSeen(ctx context.Context) (string, error)
	DismissWhatsNew(ctx context.Context, version string) error
	OnboardingStatus(ctx context.Context, companyID domain.ID) (domain.OnboardingStatus, error)
	OnboardingLastProbe(ctx context.Context, companyID domain.ID) (*service.OnboardingProbeResult, error)
	OnboardingProbe(ctx context.Context, actor domain.Actor, companyID domain.ID, agent, model string) (service.OnboardingProbeResult, error)
	OnboardingSkip(ctx context.Context, actor domain.Actor, companyID domain.ID, step domain.OnboardingStepKey) (domain.OnboardingStatus, error)
	OnboardingDismiss(ctx context.Context, actor domain.Actor, companyID domain.ID, dismissed bool) (domain.OnboardingStatus, error)
	OnboardingInstallTeam(ctx context.Context, actor domain.Actor, companyID domain.ID, slug string) (service.OnboardingTeamResult, error)
	OnboardingStartTask(ctx context.Context, actor domain.Actor, companyID, personaID domain.ID, title, description string) (service.TaskView, error)
}

var _ w3Backend = (*service.Service)(nil)

func (s *server) w3be() (w3Backend, bool) {
	b, ok := s.be.(w3Backend)
	return b, ok
}

func (s *server) w3Routes(mux handlerRegistry) {
	co := func(h companyHandler) http.HandlerFunc { return s.withCompany(h) }
	post := func(h http.HandlerFunc) http.HandlerFunc { return s.sameOrigin(h) }
	mux.HandleFunc("GET /c/{company}/primeiros-passos", co(s.handleWizard))
	mux.HandleFunc("POST /c/{company}/primeiros-passos/harness", post(co(s.handleWizardHarness)))
	mux.HandleFunc("POST /c/{company}/primeiros-passos/pular", post(co(s.handleWizardSkip)))
	mux.HandleFunc("POST /c/{company}/primeiros-passos/pacote", post(co(s.handleWizardTeam)))
	mux.HandleFunc("POST /c/{company}/primeiros-passos/persona", post(co(s.handleWizardPersona)))
	mux.HandleFunc("POST /c/{company}/primeiros-passos/task", post(co(s.handleWizardTask)))
	mux.HandleFunc("POST /c/{company}/primeiros-passos/dispensar", post(co(s.handleWizardDismiss)))
	mux.HandleFunc("POST /c/{company}/primeiros-passos/retomar", post(co(s.handleWizardResume)))
	mux.HandleFunc("GET /c/{company}/perfil", co(s.handleProfile))
	mux.HandleFunc("POST /c/{company}/perfil", post(co(s.handleProfileSave)))
	mux.HandleFunc("GET /c/{company}/novidades", co(s.handleWhatsNew))
	mux.HandleFunc("POST /c/{company}/novidades/dispensar", post(co(s.handleWhatsNewDismiss)))
	mux.HandleFunc("POST /c/{company}/favoritos", post(co(s.handleFavorite)))
}

// ---------- assistente de primeiros passos ----------

func (s *server) wizardTeams(ctx context.Context) []views.WizardTeamVM {
	list, err := teams.List()
	if err != nil {
		return nil
	}
	out := make([]views.WizardTeamVM, 0, len(list))
	for _, p := range list {
		out = append(out, views.WizardTeamVM{Slug: p.Package.Slug, Name: p.Package.Name, Desc: p.Package.Description, Personas: len(p.Personas),
			Agents: strings.Join(p.Requires.Agents, ", ")})
	}
	return out
}

func (s *server) wizardVM(ctx context.Context, b w3Backend, co domain.Company) (views.WizardVM, error) {
	st, err := b.OnboardingStatus(ctx, co.ID)
	if err != nil {
		return views.WizardVM{}, err
	}
	vm := views.WizardVM{Current: string(st.Current), Complete: st.Complete, Dismissed: st.Dismissed, Total: len(st.Steps), Done: st.Done(), Harness: st.HarnessAgent}
	for i, x := range st.Steps {
		vm.Steps = append(vm.Steps, views.WizardStepVM{N: i + 1, Key: string(x.Key), Label: wizardLabel(ctx, x.Key), Done: x.Done, Skipped: x.Skipped, Current: x.Key == st.Current})
	}
	if p, err := b.OnboardingLastProbe(ctx, co.ID); err == nil && p != nil {
		pv := &views.WizardProbeVM{Agent: p.Agent, Model: p.Model, Ready: p.Status == domain.ReadinessReady, Reason: p.Reason}
		for _, x := range p.Steps {
			pv.Steps = append(pv.Steps, views.WizardProbeStep{Name: wizardProbeLabel(ctx, x.Name), Status: string(x.Status), Detail: x.Detail})
		}
		vm.Probe, vm.Model = pv, p.Model
	}
	vm.Teams = s.wizardTeams(ctx)
	vm.HarnessOptions = []views.Option{{Value: "claude", Label: "Claude Code"}, {Value: "codex", Label: "Codex"}, {Value: "fake", Label: i18n.T(ctx, "Teste (fake)")}}
	for i := range vm.HarnessOptions {
		vm.HarnessOptions[i].Selected = vm.HarnessOptions[i].Value == st.HarnessAgent
	}
	if st.Current == domain.StepTask {
		ps, err := s.be.ListPersonas(ctx, co.ID)
		if err != nil {
			return vm, err
		}
		for _, p := range ps {
			if p.Status == domain.PersonaActive || p.Status == domain.PersonaIdle {
				vm.Personas = append(vm.Personas, views.Option{Value: p.ID.String(), Label: p.Name})
			}
		}
	}
	return vm, nil
}

func wizardLabel(ctx context.Context, k domain.OnboardingStepKey) string {
	switch k {
	case domain.StepCompany:
		return i18n.T(ctx, "Criar a empresa")
	case domain.StepHarness:
		return i18n.T(ctx, "Escolher e testar o harness")
	case domain.StepPersona:
		return i18n.T(ctx, "Contratar a primeira persona ou instalar um time")
	case domain.StepTask:
		return i18n.T(ctx, "Pedir a primeira task")
	}
	return string(k)
}

func wizardProbeLabel(ctx context.Context, name string) string {
	switch name {
	case domain.StepEnvironment:
		return i18n.T(ctx, "ambiente")
	case domain.StepBinary:
		return i18n.T(ctx, "binário")
	case domain.StepAuth:
		return i18n.T(ctx, "login ou chave")
	case domain.StepModel:
		return i18n.T(ctx, "modelo")
	}
	return name
}

// wizardPage renderiza o assistente (erro e rascunho opcionais) com o status HTTP dado.
func (s *server) wizardPage(w http.ResponseWriter, r *http.Request, co domain.Company, status int, mod func(*views.WizardVM)) {
	b, ok := s.w3be()
	if !ok {
		s.fail(w, r, &co, domain.ErrNotAllowed)
		return
	}
	ctx := r.Context()
	vm, err := s.wizardVM(ctx, b, co)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	if mod != nil {
		mod(&vm)
	}
	sh := s.shell(r, co, "painel", i18n.T(ctx, "Primeiros passos"), views.Crumb{Label: i18n.T(ctx, "Primeiros passos")})
	s.render(w, r, status, views.WizardPage(sh, vm))
}

func (s *server) handleWizard(w http.ResponseWriter, r *http.Request, co domain.Company) {
	s.wizardPage(w, r, co, http.StatusOK, func(vm *views.WizardVM) {
		vm.Notice = i18n.T(r.Context(), wizardNotices[r.URL.Query().Get("ok")])
	})
}

var wizardNotices = map[string]string{
	"harness": i18n.N("Harness testado e pronto."),
	"skip":    i18n.N("Passo do harness pulado."),
	"persona": i18n.N("Persona contratada."),
	"team":    i18n.N("Pacote instalado e persona de topo ativada."),
	"task":    i18n.N("Task criada: a persona já foi acordada."),
}

// wizardFail mostra o erro de uma ação no próprio assistente, conservando o que a pessoa digitou.
func (s *server) wizardFail(w http.ResponseWriter, r *http.Request, co domain.Company, err error, mod func(*views.WizardVM)) {
	_, title, msg := friendly(r.Context(), err)
	s.wizardPage(w, r, co, statusFor(err), func(vm *views.WizardVM) {
		vm.Error = title + ". " + msg
		if mod != nil {
			mod(vm)
		}
	})
}

func (s *server) wizardNext(w http.ResponseWriter, r *http.Request, co domain.Company, ok string) {
	redirect(w, r, "/c/"+co.Slug+"/primeiros-passos?ok="+url.QueryEscape(ok))
}

func (s *server) handleWizardHarness(w http.ResponseWriter, r *http.Request, co domain.Company) {
	b, ok := s.w3be()
	if !ok {
		s.fail(w, r, &co, domain.ErrNotAllowed)
		return
	}
	agent, model := trimForm(r, "agent"), trimForm(r, "model")
	res, err := b.OnboardingProbe(r.Context(), actorFrom(r.Context()), co.ID, agent, model)
	if err != nil {
		s.wizardFail(w, r, co, err, func(vm *views.WizardVM) { vm.Harness, vm.Model = agent, model })
		return
	}
	if res.Status == domain.ReadinessReady {
		s.wizardNext(w, r, co, "harness")
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/primeiros-passos") // o resultado do teste (com o motivo) está na página
}

func (s *server) handleWizardSkip(w http.ResponseWriter, r *http.Request, co domain.Company) {
	b, ok := s.w3be()
	if !ok {
		s.fail(w, r, &co, domain.ErrNotAllowed)
		return
	}
	if _, err := b.OnboardingSkip(r.Context(), actorFrom(r.Context()), co.ID, domain.StepHarness); err != nil {
		s.wizardFail(w, r, co, err, nil)
		return
	}
	s.wizardNext(w, r, co, "skip")
}

func (s *server) handleWizardTeam(w http.ResponseWriter, r *http.Request, co domain.Company) {
	b, ok := s.w3be()
	if !ok {
		s.fail(w, r, &co, domain.ErrNotAllowed)
		return
	}
	if _, err := b.OnboardingInstallTeam(r.Context(), actorFrom(r.Context()), co.ID, trimForm(r, "slug")); err != nil {
		s.wizardFail(w, r, co, err, nil)
		return
	}
	s.wizardNext(w, r, co, "team")
}

func (s *server) handleWizardPersona(w http.ResponseWriter, r *http.Request, co domain.Company) {
	name, title, instr, agent := trimForm(r, "name"), trimForm(r, "title"), strings.TrimSpace(r.PostFormValue("instructions")), trimForm(r, "agent")
	keep := func(vm *views.WizardVM) { vm.PersonaName, vm.PersonaTitle, vm.PersonaInstr = name, title, instr }
	if name == "" {
		s.wizardFail(w, r, co, domain.Invalid("name", i18n.T(r.Context(), "Dê um nome à persona.")), keep)
		return
	}
	if !slices.Contains([]string{"claude", "codex", "fake"}, agent) {
		agent = "claude"
	}
	if _, err := s.be.HirePersona(r.Context(), actorFrom(r.Context()), co.ID, service.HirePersonaInput{Name: name, Title: title, Instructions: instr, Agent: agent}); err != nil {
		s.wizardFail(w, r, co, err, keep)
		return
	}
	s.wizardNext(w, r, co, "persona")
}

func (s *server) handleWizardTask(w http.ResponseWriter, r *http.Request, co domain.Company) {
	b, ok := s.w3be()
	if !ok {
		s.fail(w, r, &co, domain.ErrNotAllowed)
		return
	}
	title, desc := trimForm(r, "title"), strings.TrimSpace(r.PostFormValue("description"))
	keep := func(vm *views.WizardVM) { vm.TaskTitle, vm.TaskDesc = title, desc }
	pid, err := domain.ParseID(trimForm(r, "persona"))
	if err != nil {
		s.wizardFail(w, r, co, domain.Invalid("persona", i18n.T(r.Context(), "Escolha a persona que recebe a task.")), keep)
		return
	}
	if _, err := b.OnboardingStartTask(r.Context(), actorFrom(r.Context()), co.ID, pid, title, desc); err != nil {
		s.wizardFail(w, r, co, err, keep)
		return
	}
	s.wizardNext(w, r, co, "task")
}

func (s *server) handleWizardDismiss(w http.ResponseWriter, r *http.Request, co domain.Company) {
	s.wizardDismiss(w, r, co, true)
}

func (s *server) handleWizardResume(w http.ResponseWriter, r *http.Request, co domain.Company) {
	s.wizardDismiss(w, r, co, false)
}

func (s *server) wizardDismiss(w http.ResponseWriter, r *http.Request, co domain.Company, dismissed bool) {
	b, ok := s.w3be()
	if !ok {
		s.fail(w, r, &co, domain.ErrNotAllowed)
		return
	}
	if _, err := b.OnboardingDismiss(r.Context(), actorFrom(r.Context()), co.ID, dismissed); err != nil {
		s.wizardFail(w, r, co, err, nil)
		return
	}
	if dismissed {
		redirect(w, r, "/c/"+co.Slug+"/")
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/primeiros-passos")
}

// wizardCard é o convite do Painel: só para quem administra e enquanto o assistente está ativo.
func (s *server) wizardCard(ctx context.Context, co domain.Company) *views.WizardCardVM {
	b, ok := s.w3be()
	if !ok || !views.RoleFrom(ctx).AtLeast(domain.MemberAdmin) {
		return nil
	}
	st, err := b.OnboardingStatus(ctx, co.ID)
	if err != nil || !st.Active() {
		return nil
	}
	return &views.WizardCardVM{Done: st.Done(), Total: len(st.Steps), Next: wizardLabel(ctx, st.Current)}
}

// ---------- perfil ----------

func (s *server) profileVM(ctx context.Context, b w3Backend) views.ProfileVM {
	vm := views.ProfileVM{}
	p, err := b.GetProfile(ctx)
	if err != nil {
		vm.Trusted = true
		return vm
	}
	vm.Email, vm.Name, vm.AvatarGlyph, vm.AvatarTone, vm.Timezone, vm.Locale = p.Email, p.Name, p.AvatarGlyph, p.AvatarTone, p.Timezone, p.Locale
	// a prova pelo Google (ADR-0070 §3) precisa da configuração do SDK na página; nil no modo trusted
	if s.auth != nil && s.auth.Provider == "firebase" {
		vm.Firebase = s.auth.Firebase
	}
	vm.Tones = append(vm.Tones, views.Option{Value: "", Label: i18n.T(ctx, "Padrão"), Selected: p.AvatarTone == ""})
	for _, t := range domain.AvatarTones {
		vm.Tones = append(vm.Tones, views.Option{Value: t, Label: toneLabel(ctx, t), Selected: p.AvatarTone == t})
	}
	vm.Locales = []views.Option{{Value: "", Label: i18n.T(ctx, "Seguir o navegador"), Selected: p.Locale == ""},
		{Value: "pt-BR", Label: "Português (Brasil)", Selected: p.Locale == "pt-BR"}, {Value: "en", Label: "English", Selected: p.Locale == "en"}}
	return vm
}

func toneLabel(ctx context.Context, t string) string {
	switch t {
	case "accent":
		return i18n.T(ctx, "Neutra")
	case "green":
		return i18n.T(ctx, "Verde")
	case "amber":
		return i18n.T(ctx, "Âmbar")
	case "red":
		return i18n.T(ctx, "Vermelha")
	case "blue":
		return i18n.T(ctx, "Azul")
	case "violet":
		return i18n.T(ctx, "Violeta")
	case "gray":
		return i18n.T(ctx, "Cinza")
	}
	return t
}

func (s *server) handleProfile(w http.ResponseWriter, r *http.Request, co domain.Company) {
	b, ok := s.w3be()
	if !ok {
		s.fail(w, r, &co, domain.ErrNotAllowed)
		return
	}
	vm := s.profileVM(r.Context(), b)
	if r.URL.Query().Get("ok") == "1" {
		vm.Notice = i18n.T(r.Context(), "Perfil salvo.")
	}
	s.render(w, r, http.StatusOK, views.ProfilePage(s.shell(r, co, "perfil", i18n.T(r.Context(), "Meu perfil"), views.Crumb{Label: i18n.T(r.Context(), "Meu perfil")}), vm))
}

func (s *server) handleProfileSave(w http.ResponseWriter, r *http.Request, co domain.Company) {
	b, ok := s.w3be()
	if !ok {
		s.fail(w, r, &co, domain.ErrNotAllowed)
		return
	}
	ctx := r.Context()
	name, glyph, tone, tz, loc := trimForm(r, "name"), trimForm(r, "avatar_glyph"), trimForm(r, "avatar_tone"), trimForm(r, "timezone"), trimForm(r, "locale")
	saved, err := b.UpdateProfile(ctx, domain.ProfileInput{Name: &name, AvatarGlyph: &glyph, AvatarTone: &tone, Timezone: &tz, Locale: &loc})
	if err != nil {
		vm := s.profileVM(ctx, b)
		if !vm.Trusted {
			vm.Name, vm.AvatarGlyph, vm.AvatarTone, vm.Timezone, vm.Locale = name, glyph, tone, tz, loc
		}
		_, title, msg := friendly(ctx, err)
		vm.Error = title + ". " + msg
		s.render(w, r, statusFor(err), views.ProfilePage(s.shell(r, co, "perfil", i18n.T(ctx, "Meu perfil"), views.Crumb{Label: i18n.T(ctx, "Meu perfil")}), vm))
		return
	}
	// o idioma do perfil também vira o cookie, para o resto das páginas não precisar consultar o perfil
	if l, ok := i18n.Parse(saved.Locale); ok {
		s.setLangCookie(w, l)
	}
	redirect(w, r, "/c/"+co.Slug+"/perfil?ok=1")
}

// ---------- o que há de novo ----------

var (
	changelogOnce sync.Once
	changelogRels []changelog.Release
)

func installedRelease(version string) (changelog.Release, bool) {
	changelogOnce.Do(func() { changelogRels = changelog.Parse(hanko.Changelog) })
	return changelog.For(changelogRels, version)
}

func (s *server) whatsNewVM(ctx context.Context, b w3Backend) (views.WhatsNewVM, bool) {
	rel, ok := installedRelease(s.version)
	if !ok {
		return views.WhatsNewVM{}, false
	}
	key := changelog.Key(s.version)
	seen, _ := b.WhatsNewSeen(ctx)
	return views.WhatsNewVM{Installed: devOr(s.version), Version: rel.Version, Date: rel.Date, BodyHTML: views.HTML(md.Render(rel.Body)), Key: key, Dismissed: seen == key}, true
}

func devOr(s string) string {
	if s == "" {
		return "dev"
	}
	return s
}

func (s *server) handleWhatsNew(w http.ResponseWriter, r *http.Request, co domain.Company) {
	b, ok := s.w3be()
	if !ok {
		s.fail(w, r, &co, domain.ErrNotAllowed)
		return
	}
	vm, ok := s.whatsNewVM(r.Context(), b)
	if !ok {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	s.render(w, r, http.StatusOK, views.WhatsNewPage(s.shell(r, co, "novidades", i18n.T(r.Context(), "O que há de novo"), views.Crumb{Label: i18n.T(r.Context(), "O que há de novo")}), vm))
}

func (s *server) handleWhatsNewDismiss(w http.ResponseWriter, r *http.Request, co domain.Company) {
	b, ok := s.w3be()
	if !ok {
		s.fail(w, r, &co, domain.ErrNotAllowed)
		return
	}
	if err := b.DismissWhatsNew(r.Context(), trimForm(r, "key")); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, backTo(r))
}

// whatsNewCard é o aviso do Painel: só aparece enquanto a pessoa não dispensou as novidades desta versão.
func (s *server) whatsNewCard(ctx context.Context) *views.WhatsNewVM {
	b, ok := s.w3be()
	if !ok {
		return nil
	}
	vm, ok := s.whatsNewVM(ctx, b)
	if !ok || vm.Dismissed {
		return nil
	}
	return &vm
}

// ---------- favoritos e recentes ----------

func (s *server) handleFavorite(w http.ResponseWriter, r *http.Request, co domain.Company) {
	b, ok := s.w3be()
	if !ok {
		s.fail(w, r, &co, domain.ErrNotAllowed)
		return
	}
	path := trimForm(r, "path")
	if err := b.SetFavorite(r.Context(), actorFrom(r.Context()), co.ID, path, trimForm(r, "title"), trimForm(r, "favorite") == "1"); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	clean, err := domain.NormalizeNavPath(path)
	if err != nil {
		redirect(w, r, "/c/"+co.Slug+"/")
		return
	}
	redirect(w, r, "/c/"+co.Slug+clean)
}

var titleRe = regexp.MustCompile(`<title>([^<]*?) · Hanko</title>`)

// navVisit lembra, entre a montagem da sidebar e o fim da renderização, qual página de qual empresa foi vista.
type navVisit struct {
	b    w3Backend
	coID domain.ID
	path string
}

// withNavPrefs põe os atalhos da sidebar no contexto. Só age em página completa (GET, sem htmx) de empresa; qualquer
// falha apenas deixa a sidebar sem atalhos. Devolve também a visita a registrar como "recente" depois de renderizar.
func (s *server) withNavPrefs(ctx context.Context, r *http.Request, status int) (context.Context, *navVisit) {
	b, ok := s.w3be()
	rest, isCompany := strings.CutPrefix(r.URL.Path, "/c/")
	if !ok || !isCompany || r.Method != http.MethodGet || isHX(r) {
		return ctx, nil
	}
	slug, rel, _ := strings.Cut(rest, "/")
	if slug == "" {
		return ctx, nil
	}
	co, err := s.be.GetCompany(ctx, slug)
	if err != nil {
		return ctx, nil
	}
	favs, recs, err := b.NavItems(ctx, co.ID)
	if err != nil {
		return ctx, nil
	}
	base := "/c/" + co.Slug
	p := views.NavPrefs{}
	var visit *navVisit
	cur, curErr := domain.NormalizeNavPath("/" + rel)
	if curErr == nil && status == http.StatusOK {
		p.Current = cur
		if domain.IsNavDetail(cur) {
			visit = &navVisit{b: b, coID: co.ID, path: cur}
		}
	}
	for _, f := range favs {
		p.Favorites = append(p.Favorites, views.NavLink{Href: base + f.Path, Title: navTitle(f)})
		if f.Path == p.Current {
			p.IsFavorite = true
		}
	}
	for _, f := range recs {
		p.Recents = append(p.Recents, views.NavLink{Href: base + f.Path, Title: navTitle(f)})
	}
	return views.WithNavPrefs(ctx, p), visit
}

// record registra a página renderizada como recente (título lido do <title> da própria página). Best-effort.
func (v *navVisit) record(ctx context.Context, body []byte) {
	if v == nil {
		return
	}
	title := ""
	if m := titleRe.FindSubmatch(body[:min(len(body), 4096)]); m != nil {
		title = html.UnescapeString(string(m[1]))
	}
	_ = v.b.TouchRecent(ctx, v.coID, v.path, title)
}

func navTitle(it domain.NavItem) string {
	if it.Title != "" {
		return it.Title
	}
	return strings.TrimPrefix(it.Path, "/")
}

// ---------- idioma e fuso do perfil ----------

type profKey struct{}

// profMemo carrega o perfil da pessoa uma vez por requisição (idioma, fuso e avatar usam o mesmo).
type profMemo struct {
	once sync.Once
	load func(context.Context) domain.UserProfile
	p    domain.UserProfile
}

func (m *profMemo) get(ctx context.Context) domain.UserProfile {
	m.once.Do(func() { m.p = m.load(ctx) })
	return m.p
}

// profileFor devolve o perfil da pessoa autenticada (vazio sem conta ou sem suporte no Backend).
func (s *server) profileFor(ctx context.Context) domain.UserProfile {
	if m, ok := ctx.Value(profKey{}).(*profMemo); ok {
		return m.get(ctx)
	}
	return s.loadProfile(ctx)
}

func (s *server) loadProfile(ctx context.Context) domain.UserProfile {
	b, ok := s.w3be()
	if !ok {
		return domain.UserProfile{}
	}
	id, ok := auth.IdentityFrom(ctx)
	if !ok {
		return domain.UserProfile{}
	}
	return b.ProfileOf(ctx, id.User.ID)
}

// setLangCookie grava o cookie de idioma (mesmas regras do /idioma).
func (s *server) setLangCookie(w http.ResponseWriter, l i18n.Lang) {
	http.SetCookie(w, &http.Cookie{Name: i18n.Cookie, Value: string(l), Path: "/", MaxAge: int(langCookieTTL / time.Second), //nolint:gosec // Secure vem da config
		Expires: s.now().Add(langCookieTTL), HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode})
}
