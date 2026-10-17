package ui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/textdiff"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Versões, fontes e estúdio de skills (ADR-0035). Tudo vive dentro de Skills, em abas: Biblioteca, Fontes e
// Estúdio; a página de cada skill ganha as abas Visão geral, Versões e Personas. Como o resto de Skills, o service
// é acessado por uma interface opcional: o Backend dos testes e do preview não precisa implementá-la.

type skillStudioBackend interface {
	ListSkillVersions(ctx context.Context, companyID, skillID domain.ID) ([]domain.SkillVersion, error)
	GetSkillVersion(ctx context.Context, companyID, skillID domain.ID, revision int) (domain.SkillVersion, error)
	DiffSkillVersions(ctx context.Context, companyID, skillID domain.ID, from, to int) (textdiff.Result, error)
	RestoreSkillVersion(ctx context.Context, actor domain.Actor, companyID, skillID domain.ID, revision int) (domain.Skill, error)
	SetPersonaSkillPin(ctx context.Context, actor domain.Actor, companyID, personaID, skillID domain.ID, revision *int) error

	SkillSourcesEnabled() bool
	DiscoverSkillSource(ctx context.Context, companyID domain.ID, in service.SkillSourceInput) (service.SkillSourceDiscovery, error)
	CreateSkillSource(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateSkillSourceInput) (service.SkillSourceView, service.SkillSyncResult, error)
	ListSkillSources(ctx context.Context, companyID domain.ID) ([]service.SkillSourceView, error)
	GetSkillSource(ctx context.Context, companyID, id domain.ID) (service.SkillSourceView, error)
	UpdateSkillSource(ctx context.Context, actor domain.Actor, companyID, id domain.ID, in service.UpdateSkillSourceInput) (service.SkillSourceView, error)
	DeleteSkillSource(ctx context.Context, actor domain.Actor, companyID, id domain.ID, keepSkills bool) error
	SyncSkillSource(ctx context.Context, actor domain.Actor, companyID, id domain.ID, force bool) (service.SkillSyncResult, error)

	ListSkillTestInputs(ctx context.Context, companyID, skillID domain.ID) ([]domain.SkillTestInput, error)
	CreateSkillTestInput(ctx context.Context, actor domain.Actor, companyID, skillID domain.ID, name, content string) (domain.SkillTestInput, error)
	DeleteSkillTestInput(ctx context.Context, actor domain.Actor, companyID, skillID, id domain.ID) error
	ListSkillTestRuns(ctx context.Context, companyID domain.ID, skillID *domain.ID, limit int) ([]domain.SkillTestRun, error)
	GetSkillTestRun(ctx context.Context, companyID, id domain.ID) (domain.SkillTestRun, error)
	RunSkillTest(ctx context.Context, actor domain.Actor, companyID, skillID domain.ID, in service.RunSkillTestInput) (domain.SkillTestRun, error)
	RerunSkillTest(ctx context.Context, actor domain.Actor, companyID, testID domain.ID) (domain.SkillTestRun, error)
	CancelSkillTest(ctx context.Context, actor domain.Actor, companyID, testID domain.ID) (domain.SkillTestRun, error)
}

func (s *server) studioBE() (skillStudioBackend, bool) {
	b, ok := s.be.(skillStudioBackend)
	return b, ok
}

// skillStudioRoutes registra as rotas; chamada por skillRoutes. Os caminhos fixos (fontes, estudio) são slugs
// reservados, e os subcaminhos usam um segmento literal que nenhuma rota de skill usa.
func (s *server) skillStudioRoutes(mux langMux) {
	co := func(h companyHandler) http.HandlerFunc { return s.withCompany(h) }
	post := func(h http.HandlerFunc) http.HandlerFunc { return s.sameOrigin(h) }
	capped := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxSkillFormBytes)
			h(w, r)
		}
	}
	mux.HandleFunc("GET /c/{company}/skills/fontes", co(s.handleSkillSources))
	mux.HandleFunc("GET /c/{company}/skills/fontes/nova", co(s.handleNewSkillSource))
	mux.HandleFunc("POST /c/{company}/skills/fontes/descobrir", post(co(s.handleDiscoverSkillSource)))
	mux.HandleFunc("POST /c/{company}/skills/fontes/nova", post(co(s.handleCreateSkillSource)))
	mux.HandleFunc("GET /c/{company}/skills/fontes/f/{source}", co(s.handleSkillSource))
	mux.HandleFunc("POST /c/{company}/skills/fontes/f/{source}/sincronizar", post(co(s.handleSyncSkillSource)))
	mux.HandleFunc("POST /c/{company}/skills/fontes/f/{source}/configurar", post(co(s.handleSkillSourceSettings)))
	mux.HandleFunc("POST /c/{company}/skills/fontes/f/{source}/selecao", post(co(s.handleSkillSourceSelection)))
	mux.HandleFunc("POST /c/{company}/skills/fontes/f/{source}/desconectar", post(co(s.handleDeleteSkillSource)))

	mux.HandleFunc("GET /c/{company}/skills/estudio", co(s.handleSkillStudio))
	mux.HandleFunc("POST /c/{company}/skills/estudio/executar", capped(post(co(s.handleRunSkillTest))))
	mux.HandleFunc("POST /c/{company}/skills/estudio/entradas", capped(post(co(s.handleSaveSkillTestInput))))
	mux.HandleFunc("POST /c/{company}/skills/estudio/entradas/{input}/remover", post(co(s.handleDeleteSkillTestInput)))
	mux.HandleFunc("GET /c/{company}/skills/estudio/comparar", co(s.handleCompareSkillTests))
	mux.HandleFunc("GET /c/{company}/skills/estudio/t/{test}", co(s.handleSkillTest))
	mux.HandleFunc("POST /c/{company}/skills/estudio/t/{test}/cancelar", post(co(s.handleSkillTestAction(false))))
	mux.HandleFunc("POST /c/{company}/skills/estudio/t/{test}/reexecutar", post(co(s.handleSkillTestAction(true))))

	mux.HandleFunc("GET /c/{company}/skills/{skill}/versoes", co(s.handleSkillVersions))
	mux.HandleFunc("POST /c/{company}/skills/{skill}/restaurar", post(co(s.handleRestoreSkill)))
	mux.HandleFunc("GET /c/{company}/skills/{skill}/personas", co(s.handleSkillPersonasTab))
	mux.HandleFunc("POST /c/{company}/skills/{skill}/personas/{persona}/fixar", post(co(s.handlePinSkill)))
}

var studioNotices = map[string]string{
	"conectada":    i18n.N("Fonte conectada."),
	"sincronizada": i18n.N("Fonte sincronizada."),
	"salva":        i18n.N("Alterações salvas."),
	"desconectada": i18n.N("Fonte desconectada."),
	"restaurada":   i18n.N("Versão restaurada: uma versão nova foi criada com o conteúdo dela."),
	"fixada":       i18n.N("Versão fixada atualizada."),
	"entrada":      i18n.N("Entrada salva."),
	"apagada":      i18n.N("Entrada apagada."),
	"cancelado":    i18n.N("Teste cancelado."),
}

// skillsTabs são as abas da página Skills.
func skillsTabs(ctx context.Context, base, cur string) []views.TabLink {
	return []views.TabLink{
		{Key: "biblioteca", Label: i18n.T(ctx, "Biblioteca"), Href: base + "/skills", On: cur == "biblioteca"},
		{Key: "fontes", Label: i18n.T(ctx, "Fontes"), Href: base + "/skills/fontes", On: cur == "fontes"},
		{Key: "estudio", Label: i18n.T(ctx, "Estúdio"), Href: base + "/skills/estudio", On: cur == "estudio"},
	}
}

// skillPageTabs são as abas da página de uma skill.
func skillPageTabs(ctx context.Context, base, slug, cur string) []views.TabLink {
	h := base + "/skills/" + url.PathEscape(slug)
	return []views.TabLink{
		{Key: "visao", Label: i18n.T(ctx, "Visão geral"), Href: h, On: cur == "visao"},
		{Key: "versoes", Label: i18n.T(ctx, "Versões"), Href: h + "/versoes", On: cur == "versoes"},
		{Key: "personas", Label: i18n.T(ctx, "Personas"), Href: h + "/personas", On: cur == "personas"},
	}
}

// sourceNote é o aviso da página de uma skill que veio de fonte.
func (s *server) sourceNote(ctx context.Context, co domain.Company, k domain.Skill) string {
	if k.SourceID == nil {
		return ""
	}
	label := ""
	if st, ok := s.studioBE(); ok {
		if v, err := st.GetSkillSource(ctx, co.ID, *k.SourceID); err == nil {
			label = v.Label
		}
	}
	commit := k.SourceCommit
	if len(commit) > 7 {
		commit = commit[:7]
	}
	msg := i18n.Tf(ctx, "Skill de fonte (%s, %s, commit %s).", label, k.SourcePath, commit)
	if k.SourceModified {
		return msg + " " + i18n.T(ctx, "Modificada localmente: a sincronização não a sobrescreve sem forçar.")
	}
	return msg + " " + i18n.T(ctx, "Editar aqui a marca como modificada localmente e a sincronização deixa de atualizá-la.")
}

func shortCommit(c string) string {
	if len(c) > 7 {
		return c[:7]
	}
	return c
}

func sourceBadge(ctx context.Context, v domain.SkillSource) (views.Badge, string) {
	switch v.Status {
	case domain.SourceOK:
		if !v.AutoUpdate {
			return views.Badge{Label: i18n.T(ctx, "Atualização automática pausada"), Class: "soft"}, ""
		}
		return views.Badge{Label: i18n.T(ctx, "Sincronizada"), Class: "done"}, v.LastError
	case domain.SourcePaused:
		return views.Badge{Label: i18n.T(ctx, "Atualização automática pausada"), Class: "soft"}, v.LastError
	case domain.SourceError:
		return views.Badge{Label: i18n.T(ctx, "Erro"), Class: "danger"}, oneLineUI(v.LastError, 120)
	case domain.SourceSyncing:
		return views.Badge{Label: i18n.T(ctx, "Sincronizando"), Class: "run", Live: true}, ""
	}
	return views.Badge{Label: i18n.T(ctx, "Nunca sincronizada"), Class: "soft"}, ""
}

func oneLineUI(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func sourceID(r *http.Request) (domain.ID, bool) {
	id, err := domain.ParseID(r.PathValue("source"))
	return id, err == nil
}

func pathDepth(p string) int {
	if p == "." || p == "" {
		return 0
	}
	return strings.Count(p, "/")
}

func linesOf(v string) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(v, ",", "\n"), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// ---- fontes ----

func (s *server) handleSkillSources(w http.ResponseWriter, r *http.Request, co domain.Company) {
	st, ok := s.studioBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	base := "/c/" + co.Slug
	list, err := st.ListSkillSources(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm := views.SkillSourcesVM{Tabs: skillsTabs(ctx, base, "fontes"), Enabled: st.SkillSourcesEnabled(), NewHref: base + "/skills/fontes/nova",
		Notice: i18n.T(ctx, studioNotices[r.URL.Query().Get("ok")]), Error: r.URL.Query().Get("erro")}
	for _, v := range list {
		b, txt := sourceBadge(ctx, v.SkillSource)
		href := base + "/skills/fontes/f/" + v.ID.String()
		vm.Rows = append(vm.Rows, views.SourceRow{Label: v.Label, URL: v.URL, Ref: v.TrackingRef, Href: href, Badge: b, StatusText: txt,
			Commit: shortCommit(v.LastCommit), Imported: v.Imported, Found: v.Found, SyncHref: href + "/sincronizar"})
	}
	sh := s.shell(r, co, "skills", i18n.T(ctx, "Fontes"), views.Crumb{Label: i18n.T(ctx, "Skills"), Href: base + "/skills"}, views.Crumb{Label: i18n.T(ctx, "Fontes")})
	s.render(w, r, http.StatusOK, views.SkillSourcesPage(sh, vm))
}

func (s *server) sourceFormVM(r *http.Request, co domain.Company, st skillStudioBackend) views.SkillSourceFormVM {
	ctx := r.Context()
	base := "/c/" + co.Slug
	return views.SkillSourceFormVM{Tabs: skillsTabs(ctx, base, "fontes"), Enabled: st.SkillSourcesEnabled(), Action: base + "/skills/fontes/nova",
		DiscoverAction: base + "/skills/fontes/descobrir", CancelHref: base + "/skills/fontes", AutoUpdate: true, FieldErr: map[string]string{}}
}

func (s *server) renderSourceForm(w http.ResponseWriter, r *http.Request, co domain.Company, status int, vm views.SkillSourceFormVM) {
	ctx := r.Context()
	base := "/c/" + co.Slug
	sh := s.shell(r, co, "skills", i18n.T(ctx, "Conectar fonte de skills"), views.Crumb{Label: i18n.T(ctx, "Skills"), Href: base + "/skills"},
		views.Crumb{Label: i18n.T(ctx, "Fontes"), Href: base + "/skills/fontes"}, views.Crumb{Label: i18n.T(ctx, "Nova")})
	s.render(w, r, status, views.SkillSourceNewPage(sh, vm))
}

func (s *server) handleNewSkillSource(w http.ResponseWriter, r *http.Request, co domain.Company) {
	st, ok := s.studioBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	s.renderSourceForm(w, r, co, http.StatusOK, s.sourceFormVM(r, co, st))
}

// sourceFormError põe o erro do service no formulário da fonte.
func sourceFormError(ctx context.Context, vm *views.SkillSourceFormVM, err error) {
	var ve *domain.ValidationError
	if errors.As(err, &ve) {
		vm.FieldErr[ve.Field] = ve.Reason
		vm.Error = ve.Reason
		return
	}
	_, title, msg := friendly(ctx, err)
	vm.Error = title + ". " + msg
}

func readSourceForm(r *http.Request, vm *views.SkillSourceFormVM) service.SkillSourceInput {
	vm.URL, vm.Ref, vm.PathPrefix, vm.Excluded = trimForm(r, "url"), trimForm(r, "ref"), trimForm(r, "path_prefix"), r.PostFormValue("excluded")
	return service.SkillSourceInput{URL: vm.URL, Ref: vm.Ref, PathPrefix: vm.PathPrefix, Excluded: linesOf(vm.Excluded)}
}

func (s *server) handleDiscoverSkillSource(w http.ResponseWriter, r *http.Request, co domain.Company) {
	st, ok := s.studioBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	vm := s.sourceFormVM(r, co, st)
	in := readSourceForm(r, &vm)
	d, err := st.DiscoverSkillSource(ctx, co.ID, in)
	if err != nil {
		sourceFormError(ctx, &vm, err)
		s.renderSourceForm(w, r, co, statusFor(err), vm)
		return
	}
	vm.Discovered, vm.Commit, vm.Truncated, vm.URL, vm.Ref = true, shortCommit(d.Commit), d.Truncated, d.URL, d.Ref
	for _, k := range d.Skills {
		row := views.DiscoverRow{Path: k.Path, Name: k.Name, Slug: k.Slug, Description: k.Description, Depth: pathDepth(k.Path), HasScripts: k.HasScripts,
			Checked: k.Error == "" && !k.Conflict}
		switch {
		case k.Error != "":
			row.Note, row.Disabled = k.Error, true
		case k.Conflict:
			row.Note = i18n.Tf(ctx, "Já existe uma skill %q na empresa: esta não será importada.", k.Slug)
		}
		vm.Rows = append(vm.Rows, row)
	}
	s.renderSourceForm(w, r, co, http.StatusOK, vm)
}

func (s *server) handleCreateSkillSource(w http.ResponseWriter, r *http.Request, co domain.Company) {
	st, ok := s.studioBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	vm := s.sourceFormVM(r, co, st)
	in := readSourceForm(r, &vm)
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, &co, domain.Invalid("form", err.Error()))
		return
	}
	auto := r.PostFormValue("auto_update") == "1"
	sel := r.PostForm["select"]
	if sel == nil {
		sel = []string{}
	}
	v, res, err := st.CreateSkillSource(ctx, actorFrom(ctx), co.ID, service.CreateSkillSourceInput{SkillSourceInput: in, AutoUpdate: &auto,
		ImportNew: r.PostFormValue("import_new") == "1", Select: sel})
	if err != nil {
		sourceFormError(ctx, &vm, err)
		s.renderSourceForm(w, r, co, statusFor(err), vm)
		return
	}
	to := "/c/" + co.Slug + "/skills/fontes/f/" + v.ID.String() + "?ok=conectada"
	if res.Error != "" {
		to = "/c/" + co.Slug + "/skills/fontes/f/" + v.ID.String()
	}
	redirect(w, r, to)
}

func (s *server) handleSkillSource(w http.ResponseWriter, r *http.Request, co domain.Company) {
	st, ok := s.studioBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	id, ok := sourceID(r)
	if !ok {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	v, err := st.GetSkillSource(ctx, co.ID, id)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	base := "/c/" + co.Slug
	loc := loadLoc(co.Timezone)
	self := base + "/skills/fontes/f/" + v.ID.String()
	b, txt := sourceBadge(ctx, v.SkillSource)
	vm := views.SkillSourceVM{Tabs: skillsTabs(ctx, base, "fontes"), Enabled: st.SkillSourcesEnabled(), Label: v.Label, URL: v.URL, Ref: v.TrackingRef,
		Badge: b, Commit: shortCommit(v.LastCommit), Notice: i18n.T(ctx, studioNotices[r.URL.Query().Get("ok")]),
		SyncHref: self + "/sincronizar", SettingsAction: self + "/configurar", SelectionAction: self + "/selecao", DeleteAction: self + "/desconectar",
		PathPrefix: v.PathPrefix, Excluded: strings.Join(v.ExcludedPaths, "\n"), AutoUpdate: v.AutoUpdate, ImportNew: v.ImportNew, SourceEnabled: v.Enabled}
	if v.Status == domain.SourceError {
		vm.LastError = v.LastError
	} else {
		vm.StatusText = txt
	}
	var parts []string
	if v.LastSuccessAt != nil {
		parts = append(parts, i18n.T(ctx, "última sincronização")+" "+agoCtx(ctx, *v.LastSuccessAt, s.now(), loc))
	}
	if v.NextSyncAt != nil && v.AutoUpdate && v.Enabled {
		parts = append(parts, i18n.T(ctx, "próxima")+" "+v.NextSyncAt.In(loc).Format("02/01 15:04"))
	}
	if len(parts) > 0 {
		vm.StatusText = strings.TrimSpace(vm.StatusText + " " + strings.Join(parts, " · "))
	}
	if msg := r.URL.Query().Get("sync"); msg != "" {
		vm.Notice = msg
	}
	for _, e := range v.Entries {
		row := views.SourceEntryRow{Path: e.Path, Name: e.Name, Description: e.Description, Depth: pathDepth(e.Path), Selection: string(e.Selection),
			Present: e.Present, HasScripts: e.HasScripts, Modified: e.Modified, SkillSlug: e.SkillSlug, Error: e.Error}
		if e.SkillSlug != "" {
			row.SkillHref = base + "/skills/" + url.PathEscape(e.SkillSlug)
		}
		row.Options = []views.Option{{Value: "selected", Label: i18n.T(ctx, "Importar"), Selected: e.Selection == domain.EntrySelected},
			{Value: "excluded", Label: i18n.T(ctx, "Ignorar"), Selected: e.Selection == domain.EntryExcluded}}
		if e.Selection == domain.EntryNew {
			row.Options = append([]views.Option{{Value: "", Label: i18n.T(ctx, "Nova: revisar"), Selected: true}}, row.Options...)
		}
		vm.Entries = append(vm.Entries, row)
	}
	sh := s.shell(r, co, "skills", v.Label, views.Crumb{Label: i18n.T(ctx, "Skills"), Href: base + "/skills"},
		views.Crumb{Label: i18n.T(ctx, "Fontes"), Href: base + "/skills/fontes"}, views.Crumb{Label: v.Label})
	s.render(w, r, http.StatusOK, views.SkillSourcePage(sh, vm))
}

func (s *server) handleSyncSkillSource(w http.ResponseWriter, r *http.Request, co domain.Company) {
	st, ok := s.studioBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	id, ok := sourceID(r)
	if !ok {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	res, err := st.SyncSkillSource(ctx, actorFrom(ctx), co.ID, id, r.PostFormValue("force") == "1")
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	self := "/c/" + co.Slug + "/skills/fontes/f/" + id.String()
	if res.Error != "" {
		redirect(w, r, self) // o erro fica na fonte e aparece na página
		return
	}
	msg := i18n.Tf(ctx, "Sincronizada no commit %s: %d importada(s), %d atualizada(s), %d sem mudança, %d ignorada(s).",
		shortCommit(res.Commit), res.Imported, res.Updated, res.Unchanged, res.Skipped)
	redirect(w, r, self+"?sync="+url.QueryEscape(msg))
}

func (s *server) handleSkillSourceSettings(w http.ResponseWriter, r *http.Request, co domain.Company) {
	st, ok := s.studioBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	id, ok := sourceID(r)
	if !ok {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	prefix := trimForm(r, "path_prefix")
	excl := linesOf(r.PostFormValue("excluded"))
	auto, importNew, enabled := r.PostFormValue("auto_update") == "1", r.PostFormValue("import_new") == "1", r.PostFormValue("enabled") == "1"
	if _, err := st.UpdateSkillSource(ctx, actorFrom(ctx), co.ID, id, service.UpdateSkillSourceInput{PathPrefix: &prefix, Excluded: &excl,
		AutoUpdate: &auto, ImportNew: &importNew, Enabled: &enabled}); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/skills/fontes/f/"+id.String()+"?ok=salva")
}

// handleSkillSourceSelection grava a seleção (campos sel:<caminho>) e sincroniza, para as novas escolhas valerem já.
func (s *server) handleSkillSourceSelection(w http.ResponseWriter, r *http.Request, co domain.Company) {
	st, ok := s.studioBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	id, ok := sourceID(r)
	if !ok {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, &co, domain.Invalid("form", err.Error()))
		return
	}
	sel := map[string]domain.EntrySelection{}
	for k, vs := range r.PostForm {
		p, ok := strings.CutPrefix(k, "sel:")
		if !ok || len(vs) == 0 || vs[0] == "" {
			continue
		}
		sel[p] = domain.EntrySelection(vs[0])
	}
	if _, err := st.UpdateSkillSource(ctx, actorFrom(ctx), co.ID, id, service.UpdateSkillSourceInput{Selection: sel}); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	self := "/c/" + co.Slug + "/skills/fontes/f/" + id.String()
	if st.SkillSourcesEnabled() {
		if res, err := st.SyncSkillSource(ctx, actorFrom(ctx), co.ID, id, false); err == nil && res.Error == "" {
			msg := i18n.Tf(ctx, "Sincronizada no commit %s: %d importada(s), %d atualizada(s), %d sem mudança, %d ignorada(s).",
				shortCommit(res.Commit), res.Imported, res.Updated, res.Unchanged, res.Skipped)
			redirect(w, r, self+"?sync="+url.QueryEscape(msg))
			return
		}
	}
	redirect(w, r, self+"?ok=salva")
}

func (s *server) handleDeleteSkillSource(w http.ResponseWriter, r *http.Request, co domain.Company) {
	st, ok := s.studioBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	id, ok := sourceID(r)
	if !ok {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	if err := st.DeleteSkillSource(ctx, actorFrom(ctx), co.ID, id, r.PostFormValue("keep") != "0"); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/skills/fontes?ok=desconectada")
}

// ---- estúdio ----

func testBadge(ctx context.Context, st domain.SkillTestStatus) views.Badge {
	switch st {
	case domain.SkillTestRunning:
		return views.Badge{Label: i18n.T(ctx, "rodando"), Class: "run", Live: true}
	case domain.SkillTestDone:
		return views.Badge{Label: i18n.T(ctx, "concluído"), Class: "done"}
	case domain.SkillTestFailed:
		return views.Badge{Label: i18n.T(ctx, "falhou"), Class: "danger"}
	case domain.SkillTestCancelled:
		return views.Badge{Label: i18n.T(ctx, "cancelado"), Class: "outline"}
	}
	return views.Badge{Label: i18n.T(ctx, "na fila"), Class: "soft", Live: true}
}

func (s *server) testRow(ctx context.Context, co domain.Company, t domain.SkillTestRun) views.TestRunRow {
	base := "/c/" + co.Slug + "/skills/estudio/t/" + t.ID.String()
	row := views.TestRunRow{ID: t.ID.String(), Href: base, Skill: t.SkillSlug, Revision: t.SkillRevision, Persona: t.PersonaName,
		Badge: testBadge(ctx, t.Status), When: agoCtx(ctx, t.CreatedAt, s.now(), loadLoc(co.Timezone)), Active: !t.Status.Terminal(),
		CancelHref: base + "/cancelar", RerunHref: base + "/reexecutar"}
	if t.CostUSD != nil {
		row.Cost = fmtCostPtrCtx(ctx, t.CostUSD)
	}
	if t.Status.Terminal() {
		row.Excerpt = oneLineUI(t.Output, 140)
		if t.Error != "" && t.Status != domain.SkillTestDone {
			row.Excerpt = oneLineUI(t.Error, 140)
		}
	}
	return row
}

func (s *server) studioVM(r *http.Request, co domain.Company, st skillStudioBackend, sb skillsBackend) (views.SkillStudioVM, *domain.Skill, error) {
	ctx := r.Context()
	base := "/c/" + co.Slug
	vm := views.SkillStudioVM{Tabs: skillsTabs(ctx, base, "estudio"), RunAction: base + "/skills/estudio/executar",
		SaveInputAction: base + "/skills/estudio/entradas", CompareAction: base + "/skills/estudio/comparar",
		Notice: i18n.T(ctx, studioNotices[r.URL.Query().Get("ok")]), MaxInputKiB: domain.MaxSkillTestInputBytes >> 10}
	skills, err := sb.ListSkills(ctx, co.ID)
	if err != nil {
		return vm, nil, err
	}
	vm.HasSkills = len(skills) > 0
	if !vm.HasSkills {
		return vm, nil, nil
	}
	want := r.URL.Query().Get("skill")
	if want == "" {
		want = r.PostFormValue("skill")
	}
	var cur *domain.Skill
	for i := range skills {
		if skills[i].Slug == want {
			cur = &skills[i].Skill
		}
	}
	if cur == nil {
		cur = &skills[0].Skill
	}
	vm.SkillSlug = cur.Slug
	for _, k := range skills {
		vm.Skills = append(vm.Skills, views.Option{Value: k.Slug, Label: k.Name + " (" + k.Slug + ")", Selected: k.ID == cur.ID})
	}
	ps, err := s.be.ListPersonas(ctx, co.ID)
	if err != nil {
		return vm, nil, err
	}
	for _, p := range ps {
		vm.Personas = append(vm.Personas, views.Option{Value: p.Slug, Label: p.Name})
	}
	vs, err := st.ListSkillVersions(ctx, co.ID, cur.ID)
	if err != nil {
		return vm, nil, err
	}
	for _, v := range vs {
		vm.Revisions = append(vm.Revisions, views.Option{Value: strconv.Itoa(v.Revision), Label: "v" + strconv.Itoa(v.Revision), Selected: v.Revision == cur.Version})
	}
	ins, err := st.ListSkillTestInputs(ctx, co.ID, cur.ID)
	if err != nil {
		return vm, nil, err
	}
	vm.Inputs = []views.Option{{Value: "", Label: i18n.T(ctx, "— texto livre —")}}
	for _, in := range ins {
		vm.Inputs = append(vm.Inputs, views.Option{Value: in.ID.String(), Label: in.Name})
		vm.DeleteInputs = append(vm.DeleteInputs, views.SavedInputRow{Name: in.Name,
			DeleteHref: base + "/skills/estudio/entradas/" + in.ID.String() + "/remover?skill=" + url.QueryEscape(cur.Slug)})
	}
	vm.SaveInputAction += "?skill=" + url.QueryEscape(cur.Slug)
	vm.RunAction += "?skill=" + url.QueryEscape(cur.Slug)
	vm.RunsHref = base + "/skills/estudio?skill=" + url.QueryEscape(cur.Slug) + "&fragmento=execucoes"
	runs, err := st.ListSkillTestRuns(ctx, co.ID, &cur.ID, 50)
	if err != nil {
		return vm, nil, err
	}
	for _, t := range runs {
		row := s.testRow(ctx, co, t)
		vm.AnyActive = vm.AnyActive || row.Active
		vm.Runs = append(vm.Runs, row)
	}
	return vm, cur, nil
}

func (s *server) renderStudio(w http.ResponseWriter, r *http.Request, co domain.Company, status int, vm views.SkillStudioVM) {
	ctx := r.Context()
	base := "/c/" + co.Slug
	if r.URL.Query().Get("fragmento") == "execucoes" {
		s.render(w, r, status, views.SkillTestRuns(vm))
		return
	}
	sh := s.shell(r, co, "skills", i18n.T(ctx, "Estúdio"), views.Crumb{Label: i18n.T(ctx, "Skills"), Href: base + "/skills"}, views.Crumb{Label: i18n.T(ctx, "Estúdio")})
	s.render(w, r, status, views.SkillStudioPage(sh, vm))
}

func (s *server) handleSkillStudio(w http.ResponseWriter, r *http.Request, co domain.Company) {
	st, ok := s.studioBE()
	sb, ok2 := s.skillsBE()
	if !ok || !ok2 {
		s.unavailable(w, r)
		return
	}
	vm, _, err := s.studioVM(r, co, st, sb)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.renderStudio(w, r, co, http.StatusOK, vm)
}

func (s *server) handleRunSkillTest(w http.ResponseWriter, r *http.Request, co domain.Company) {
	st, ok := s.studioBE()
	sb, ok2 := s.skillsBE()
	if !ok || !ok2 {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	vm, cur, err := s.studioVM(r, co, st, sb)
	if err != nil || cur == nil {
		if err == nil {
			err = domain.ErrNotFound
		}
		s.fail(w, r, &co, err)
		return
	}
	in := service.RunSkillTestInput{Input: r.PostFormValue("input")}
	p, err := s.be.GetPersona(ctx, co.ID, trimForm(r, "persona"))
	if err == nil {
		in.PersonaID = p.ID
		if v := trimForm(r, "input_id"); v != "" {
			if id, perr := domain.ParseID(v); perr == nil {
				in.InputID = &id
			}
		}
		if rev, perr := strconv.Atoi(trimForm(r, "revision")); perr == nil && rev > 0 {
			in.Revision = &rev
		}
		var tr domain.SkillTestRun
		if tr, err = st.RunSkillTest(ctx, actorFrom(ctx), co.ID, cur.ID, in); err == nil {
			redirect(w, r, "/c/"+co.Slug+"/skills/estudio/t/"+tr.ID.String())
			return
		}
	}
	var ve *domain.ValidationError
	if errors.As(err, &ve) {
		vm.Error = ve.Reason
	} else {
		_, title, msg := friendly(ctx, err)
		vm.Error = title + ". " + msg
	}
	vm.Input = in.Input
	s.renderStudio(w, r, co, statusFor(err), vm)
}

func (s *server) handleSaveSkillTestInput(w http.ResponseWriter, r *http.Request, co domain.Company) {
	st, ok := s.studioBE()
	sb, ok2 := s.skillsBE()
	if !ok || !ok2 {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	k, err := sb.GetSkill(ctx, co.ID, r.URL.Query().Get("skill"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	if _, err := st.CreateSkillTestInput(ctx, actorFrom(ctx), co.ID, k.ID, trimForm(r, "name"), r.PostFormValue("content")); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/skills/estudio?skill="+url.QueryEscape(k.Slug)+"&ok=entrada")
}

func (s *server) handleDeleteSkillTestInput(w http.ResponseWriter, r *http.Request, co domain.Company) {
	st, ok := s.studioBE()
	sb, ok2 := s.skillsBE()
	if !ok || !ok2 {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	k, err := sb.GetSkill(ctx, co.ID, r.URL.Query().Get("skill"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	id, err := domain.ParseID(r.PathValue("input"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	if err := st.DeleteSkillTestInput(ctx, actorFrom(ctx), co.ID, k.ID, id); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/skills/estudio?skill="+url.QueryEscape(k.Slug)+"&ok=apagada")
}

func (s *server) testVM(ctx context.Context, co domain.Company, t domain.SkillTestRun) views.SkillTestVM {
	base := "/c/" + co.Slug
	vm := views.SkillTestVM{Row: s.testRow(ctx, co, t), Input: t.Input, OutputText: t.Output, Output: renderMD(t.Output),
		Self: base + "/skills/estudio/t/" + t.ID.String(), BackHref: base + "/skills/estudio?skill=" + url.QueryEscape(t.SkillSlug), RunName: t.RunName}
	if t.Output == "" {
		vm.Output = ""
	}
	if t.Status != domain.SkillTestDone {
		vm.Error = t.Error
	}
	if t.RunID != nil {
		vm.RunHref = base + "/execucoes/" + t.RunID.String()
		if vm.RunName == "" {
			vm.RunName = i18n.T(ctx, "execução")
		}
	}
	return vm
}

func testID(r *http.Request) (domain.ID, bool) {
	id, err := domain.ParseID(r.PathValue("test"))
	return id, err == nil
}

func (s *server) handleSkillTest(w http.ResponseWriter, r *http.Request, co domain.Company) {
	st, ok := s.studioBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	id, ok := testID(r)
	if !ok {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	t, err := st.GetSkillTestRun(ctx, co.ID, id)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm := s.testVM(ctx, co, t)
	if isHX(r) {
		s.render(w, r, http.StatusOK, views.SkillTestBody(vm))
		return
	}
	base := "/c/" + co.Slug
	title := fmt.Sprintf("%s v%d", t.SkillSlug, t.SkillRevision)
	sh := s.shell(r, co, "skills", title, views.Crumb{Label: i18n.T(ctx, "Skills"), Href: base + "/skills"},
		views.Crumb{Label: i18n.T(ctx, "Estúdio"), Href: vm.BackHref}, views.Crumb{Label: title})
	s.render(w, r, http.StatusOK, views.SkillTestPage(sh, vm))
}

// handleSkillTestAction cancela ou reexecuta um teste.
func (s *server) handleSkillTestAction(rerun bool) companyHandler {
	return func(w http.ResponseWriter, r *http.Request, co domain.Company) {
		st, ok := s.studioBE()
		if !ok {
			s.unavailable(w, r)
			return
		}
		ctx := r.Context()
		id, ok := testID(r)
		if !ok {
			s.fail(w, r, &co, domain.ErrNotFound)
			return
		}
		var (
			t   domain.SkillTestRun
			err error
		)
		if rerun {
			t, err = st.RerunSkillTest(ctx, actorFrom(ctx), co.ID, id)
		} else {
			t, err = st.CancelSkillTest(ctx, actorFrom(ctx), co.ID, id)
		}
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		redirect(w, r, "/c/"+co.Slug+"/skills/estudio/t/"+t.ID.String())
	}
}

func (s *server) handleCompareSkillTests(w http.ResponseWriter, r *http.Request, co domain.Company) {
	st, ok := s.studioBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	refs := r.URL.Query()["t"]
	if len(refs) != 2 {
		s.fail(w, r, &co, domain.Invalid("t", i18n.T(ctx, "marque exatamente dois testes para comparar")))
		return
	}
	var tests [2]domain.SkillTestRun
	for i, ref := range refs {
		id, err := domain.ParseID(ref)
		if err != nil {
			s.fail(w, r, &co, domain.ErrNotFound)
			return
		}
		if tests[i], err = st.GetSkillTestRun(ctx, co.ID, id); err != nil {
			s.fail(w, r, &co, err)
			return
		}
	}
	// o mais antigo à esquerda
	sort.SliceStable(tests[:], func(i, j int) bool { return tests[i].CreatedAt.Before(tests[j].CreatedAt) })
	base := "/c/" + co.Slug
	vm := views.SkillCompareVM{A: s.testVM(ctx, co, tests[0]), B: s.testVM(ctx, co, tests[1]),
		BackHref: base + "/skills/estudio?skill=" + url.QueryEscape(tests[1].SkillSlug)}
	res := textdiff.Lines(tests[0].Output, tests[1].Output)
	vm.Diff = views.DiffVM{FromVersion: tests[0].SkillRevision, ToVersion: tests[1].SkillRevision, Added: res.Added, Removed: res.Removed,
		Coarse: res.Coarse, Same: !res.Changed(), CloseHref: vm.BackHref}
	vm.Diff.Lines, vm.Diff.Cut = diffLines(res)
	sh := s.shell(r, co, "skills", i18n.T(ctx, "Comparar testes"), views.Crumb{Label: i18n.T(ctx, "Skills"), Href: base + "/skills"},
		views.Crumb{Label: i18n.T(ctx, "Estúdio"), Href: vm.BackHref}, views.Crumb{Label: i18n.T(ctx, "Comparar")})
	s.render(w, r, http.StatusOK, views.SkillComparePage(sh, vm))
}

// ---- versões e personas da skill ----

func (s *server) skillForTab(w http.ResponseWriter, r *http.Request, co domain.Company) (skillStudioBackend, skillsBackend, domain.Skill, bool) {
	st, ok := s.studioBE()
	sb, ok2 := s.skillsBE()
	if !ok || !ok2 {
		s.unavailable(w, r)
		return nil, nil, domain.Skill{}, false
	}
	k, err := sb.GetSkill(r.Context(), co.ID, r.PathValue("skill"))
	if err != nil {
		s.fail(w, r, &co, err)
		return nil, nil, domain.Skill{}, false
	}
	return st, sb, k, true
}

func (s *server) handleSkillVersions(w http.ResponseWriter, r *http.Request, co domain.Company) {
	st, _, k, ok := s.skillForTab(w, r, co)
	if !ok {
		return
	}
	ctx := r.Context()
	base := "/c/" + co.Slug
	self := base + "/skills/" + url.PathEscape(k.Slug) + "/versoes"
	vs, err := st.ListSkillVersions(ctx, co.ID, k.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	loc := loadLoc(co.Timezone)
	vm := views.SkillVersionsVM{Tabs: skillPageTabs(ctx, base, k.Slug, "versoes"), Name: k.Name, Slug: k.Slug,
		Notice: i18n.T(ctx, studioNotices[r.URL.Query().Get("ok")])}
	for i, v := range vs {
		row := views.SkillVersionRow{Revision: v.Revision, Name: v.Name, Size: fmt.Sprintf("%.1f KiB", float64(v.BodyBytes)/1024), SHA: v.SHA256[:12],
			Commit: shortCommit(v.SourceCommit), Author: actorLabel(ctx, v.Author), When: agoCtx(ctx, v.CreatedAt, s.now(), loc),
			ViewHref: self + "?ver=" + strconv.Itoa(v.Revision) + "#versao", RestoreAction: base + "/skills/" + url.PathEscape(k.Slug) + "/restaurar",
			Current: v.Revision == k.Version}
		if i+1 < len(vs) {
			row.DiffHref = self + "?de=" + strconv.Itoa(vs[i+1].Revision) + "&para=" + strconv.Itoa(v.Revision) + "#diff"
		}
		vm.Rows = append(vm.Rows, row)
	}
	q := r.URL.Query()
	if rev, err := strconv.Atoi(q.Get("ver")); err == nil && rev > 0 {
		v, err := st.GetSkillVersion(ctx, co.ID, k.ID, rev)
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		vm.View = &views.SkillVersionView{Revision: v.Revision, Name: v.Name, Description: v.Description, Body: renderMD(v.Body), CloseHref: self}
	}
	from, ferr := strconv.Atoi(q.Get("de"))
	to, terr := strconv.Atoi(q.Get("para"))
	if ferr == nil && terr == nil && from > 0 && to > 0 {
		d := &views.DiffVM{FromVersion: from, ToVersion: to, CloseHref: self}
		res, err := st.DiffSkillVersions(ctx, co.ID, k.ID, from, to)
		if err != nil {
			_, _, d.Error = friendly(ctx, err)
		} else {
			d.Added, d.Removed, d.Coarse, d.Same = res.Added, res.Removed, res.Coarse, !res.Changed()
			d.Lines, d.Cut = diffLines(res)
		}
		vm.Diff = d
	}
	sh := s.shell(r, co, "skills", k.Name, views.Crumb{Label: i18n.T(ctx, "Skills"), Href: base + "/skills"},
		views.Crumb{Label: k.Name, Href: base + "/skills/" + url.PathEscape(k.Slug)}, views.Crumb{Label: i18n.T(ctx, "Versões")})
	s.render(w, r, http.StatusOK, views.SkillVersionsPage(sh, vm))
}

func (s *server) handleRestoreSkill(w http.ResponseWriter, r *http.Request, co domain.Company) {
	st, _, k, ok := s.skillForTab(w, r, co)
	if !ok {
		return
	}
	ctx := r.Context()
	rev, err := strconv.Atoi(trimForm(r, "revision"))
	if err != nil || rev < 1 {
		s.fail(w, r, &co, domain.Invalid("revision", i18n.T(ctx, "versão inválida")))
		return
	}
	if _, err := st.RestoreSkillVersion(ctx, actorFrom(ctx), co.ID, k.ID, rev); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/skills/"+url.PathEscape(k.Slug)+"/versoes?ok=restaurada")
}

func (s *server) handleSkillPersonasTab(w http.ResponseWriter, r *http.Request, co domain.Company) {
	st, sb, k, ok := s.skillForTab(w, r, co)
	if !ok {
		return
	}
	ctx := r.Context()
	base := "/c/" + co.Slug
	ps, err := sb.ListSkillPersonas(ctx, co.ID, k.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vs, err := st.ListSkillVersions(ctx, co.ID, k.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm := views.SkillPersonasVM{Tabs: skillPageTabs(ctx, base, k.Slug, "personas"), Name: k.Name, Slug: k.Slug, Version: k.Version,
		Notice: i18n.T(ctx, studioNotices[r.URL.Query().Get("ok")])}
	skillHref := base + "/skills/" + url.PathEscape(k.Slug)
	for _, p := range ps {
		row := views.SkillPinRow{Name: p.Name, Href: base + "/personas/" + url.PathEscape(p.Slug), Enabled: p.Enabled,
			ToggleHref: skillHref + "/personas/" + url.PathEscape(p.Slug), PinAction: skillHref + "/personas/" + url.PathEscape(p.Slug) + "/fixar"}
		row.Options = []views.Option{{Value: "0", Label: i18n.T(ctx, "Sempre a mais nova"), Selected: p.PinnedRevision == nil}}
		for _, v := range vs {
			sel := p.PinnedRevision != nil && *p.PinnedRevision == v.Revision
			row.Options = append(row.Options, views.Option{Value: strconv.Itoa(v.Revision), Label: "v" + strconv.Itoa(v.Revision), Selected: sel})
		}
		if p.PinnedRevision != nil {
			row.Pinned = "v" + strconv.Itoa(*p.PinnedRevision)
		}
		vm.Rows = append(vm.Rows, row)
	}
	sh := s.shell(r, co, "skills", k.Name, views.Crumb{Label: i18n.T(ctx, "Skills"), Href: base + "/skills"},
		views.Crumb{Label: k.Name, Href: skillHref}, views.Crumb{Label: i18n.T(ctx, "Personas")})
	s.render(w, r, http.StatusOK, views.SkillPersonasPage(sh, vm))
}

func (s *server) handlePinSkill(w http.ResponseWriter, r *http.Request, co domain.Company) {
	st, _, k, ok := s.skillForTab(w, r, co)
	if !ok {
		return
	}
	ctx := r.Context()
	p, err := s.be.GetPersona(ctx, co.ID, r.PathValue("persona"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	var rev *int
	if n, err := strconv.Atoi(trimForm(r, "revision")); err == nil && n > 0 {
		rev = &n
	}
	if err := st.SetPersonaSkillPin(ctx, actorFrom(ctx), co.ID, p.ID, k.ID, rev); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/skills/"+url.PathEscape(k.Slug)+"/personas?ok=fixada")
}

// skillLibraryFilter aplica o filtro da Biblioteca (todas, locais, de fonte) e monta as abas do filtro.
func skillLibraryFilter(ctx context.Context, base, cur string, list []service.SkillView) ([]service.SkillView, []views.TabLink) {
	var local, fromSource int
	for _, k := range list {
		if k.SourceID != nil {
			fromSource++
		} else {
			local++
		}
	}
	tabs := []views.TabLink{
		{Key: "", Label: i18n.T(ctx, "Todas"), Href: base + "/skills", Count: len(list), On: cur != "local" && cur != "fonte"},
		{Key: "local", Label: i18n.T(ctx, "Locais"), Href: base + "/skills?origem=local", Count: local, On: cur == "local"},
		{Key: "fonte", Label: i18n.T(ctx, "De fonte"), Href: base + "/skills?origem=fonte", Count: fromSource, On: cur == "fonte"},
	}
	if cur != "local" && cur != "fonte" {
		return list, tabs
	}
	out := list[:0:0]
	for _, k := range list {
		if (cur == "fonte") == (k.SourceID != nil) {
			out = append(out, k)
		}
	}
	return out, tabs
}
