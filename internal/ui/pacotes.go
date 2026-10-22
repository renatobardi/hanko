package ui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/export"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/teams"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Pacotes de time hanko.team.v1 (ADR-0044) dentro do Catálogo de times: o catálogo embutido, o envio de um arquivo, a
// prévia com o passo de assinaturas e a instalação (pausada), mais a exportação de um time.

// teamPkgBackend é o que as telas pedem ao service; opcional para que os fakes que não o usam sigam valendo.
type teamPkgBackend interface {
	InstallTeam(ctx context.Context, actor domain.Actor, companyID domain.ID, pkg export.TeamPackage, opts service.TeamInstallOptions) (service.TeamInstall, error)
	ExportTeam(ctx context.Context, companyID domain.ID, opts service.TeamExportOptions) (export.TeamPackage, error)
	ListTeamInstalls(ctx context.Context, companyID domain.ID) ([]service.TeamInstallRecord, error)
}

// teamPkgWarningFormats marca, para o catálogo de traduções, os avisos que o service devolve na instalação de pacotes.
var teamPkgWarningFormats = []string{
	i18n.N("%d persona(s) usam um harness sem assinatura ativa na empresa: rodam com o login do harness até você escolher uma assinatura"),
	i18n.N("%d segredo(s) citados pelo pacote não existem na empresa: crie-os antes de liberar as personas"),
}

var _ = teamPkgWarningFormats // só para o catálogo

const (
	teamCacheTTL = 15 * time.Minute
	teamCacheMax = 8
)

type teamEntry struct {
	pkg  export.TeamPackage
	user domain.ID
	at   time.Time
}

// teamCache guarda por alguns minutos o pacote enviado, para a prévia, o ajuste e a confirmação não pedirem o arquivo de novo.
type teamCache struct {
	mu sync.Mutex
	m  map[string]teamEntry
}

func (c *teamCache) put(user domain.ID, p export.TeamPackage, now time.Time) (string, error) {
	tok, _, err := auth.NewInviteToken()
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]teamEntry{}
	}
	for k, e := range c.m {
		if now.Sub(e.at) > teamCacheTTL {
			delete(c.m, k)
		}
	}
	for len(c.m) >= teamCacheMax {
		oldest, oldestAt := "", now
		for k, e := range c.m {
			if oldest == "" || e.at.Before(oldestAt) {
				oldest, oldestAt = k, e.at
			}
		}
		delete(c.m, oldest)
	}
	c.m[tok] = teamEntry{pkg: p, user: user, at: now}
	return tok, nil
}

func (c *teamCache) get(tok string, user domain.ID, now time.Time) (export.TeamPackage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[tok]
	if !ok || e.user != user {
		return export.TeamPackage{}, false
	}
	if now.Sub(e.at) > teamCacheTTL {
		delete(c.m, tok)
		return export.TeamPackage{}, false
	}
	return e.pkg, true
}

func (s *server) teamPkgRoutes(mux langMux) {
	co := func(h companyHandler) http.HandlerFunc { return s.withCompany(h) }
	post := func(h http.HandlerFunc) http.HandlerFunc { return s.sameOrigin(h) }
	const b = "/c/{company}/personas/catalogo"
	mux.HandleFunc("GET "+b+"/pacote/{slug}", co(s.handleCatalogPackage(false, false)))
	mux.HandleFunc("POST "+b+"/pacote/{slug}/previa", post(co(s.handleCatalogPackage(false, true))))
	mux.HandleFunc("POST "+b+"/pacote/{slug}/instalar", post(co(s.handleCatalogPackage(true, true))))
	mux.HandleFunc("POST "+b+"/arquivo", post(co(s.handleTeamUpload)))
	mux.HandleFunc("GET "+b+"/arquivo/{token}", co(s.handleFilePackage(false, false)))
	mux.HandleFunc("POST "+b+"/arquivo/{token}/previa", post(co(s.handleFilePackage(false, true))))
	mux.HandleFunc("POST "+b+"/arquivo/{token}/instalar", post(co(s.handleFilePackage(true, true))))
	mux.HandleFunc("GET "+b+"/exportar", co(s.handleTeamExport))
}

func (s *server) teamPkgBE(w http.ResponseWriter, r *http.Request, co *domain.Company) (teamPkgBackend, bool) {
	b, ok := s.be.(teamPkgBackend)
	if !ok {
		s.fail(w, r, co, domain.ErrNotFound)
	}
	return b, ok
}

// packagesVM monta a seção de pacotes do Catálogo de times.
func (s *server) packagesVM(ctx context.Context, co domain.Company) views.PackagesVM {
	base := "/c/" + co.Slug + "/personas/catalogo"
	b, ok := s.be.(teamPkgBackend)
	vm := views.PackagesVM{Unavailable: !ok, UploadHref: base + "/arquivo", ExportHref: base + "/exportar", CanAdmin: views.RoleFrom(ctx).AtLeast(domain.MemberAdmin)}
	if !ok {
		return vm
	}
	if list, err := teams.List(); err == nil {
		for _, p := range list {
			r := p.ComputeRequires()
			vm.Cards = append(vm.Cards, views.PackageCard{Slug: p.Package.Slug, Name: p.Package.Name, Desc: p.Package.Description, Version: p.Package.Version,
				Personas: len(p.Personas), Skills: len(p.Skills), Flows: len(p.Flows), Agents: strings.Join(r.Agents, ", "),
				PreviewHref: base + "/pacote/" + url.PathEscape(p.Package.Slug)})
		}
	}
	if ps, err := s.be.ListPersonas(ctx, co.ID); err == nil {
		for _, p := range ps {
			vm.ExportOpts = append(vm.ExportOpts, views.Option{Value: p.Slug, Label: p.Name})
		}
	}
	if recs, err := b.ListTeamInstalls(ctx, co.ID); err == nil {
		loc := loadLoc(co.Timezone)
		for _, rec := range recs {
			when := rec.CreatedAt
			if t, err := time.Parse("2006-01-02T15:04:05Z", rec.CreatedAt); err == nil {
				when = agoCtx(ctx, t, s.now(), loc)
			}
			src := i18n.T(ctx, "arquivo")
			if rec.Source == service.TeamSourceCatalog {
				src = i18n.T(ctx, "catálogo")
			}
			vm.Installs = append(vm.Installs, views.PackageInstallRow{When: when, Name: rec.Name, Version: rec.Version, Source: src})
		}
	}
	return vm
}

// teamOptsFrom lê o formulário: ação padrão, exceções por item (só as que diferem do padrão) e as assinaturas por harness.
func teamOptsFrom(r *http.Request) (service.TeamInstallOptions, error) {
	opts := service.TeamInstallOptions{OnConflict: domain.ConflictAction(r.PostFormValue("on_conflict"))}
	if opts.OnConflict == "" {
		opts.OnConflict = domain.ConflictSkip
	}
	if !opts.OnConflict.Valid() {
		return opts, domain.Invalid("on_conflict", "want skip, rename or replace")
	}
	for k, vals := range r.PostForm {
		if len(vals) == 0 || vals[0] == "" {
			continue
		}
		if kr, ok := strings.CutPrefix(k, "r:"); ok {
			act := domain.ConflictAction(vals[0])
			if !act.Valid() {
				return opts, domain.Invalid("resolve", "unknown action")
			}
			if act != opts.OnConflict {
				if opts.Resolve == nil {
					opts.Resolve = map[string]domain.ConflictAction{}
				}
				opts.Resolve[kr] = act
			}
		}
		if agent, ok := strings.CutPrefix(k, "sub:"); ok {
			if opts.Subscriptions == nil {
				opts.Subscriptions = map[string]string{}
			}
			opts.Subscriptions[agent] = vals[0]
		}
	}
	return opts, nil
}

func conflictOpts(ctx context.Context, cur string) []views.Option {
	return []views.Option{
		{Value: "skip", Label: i18n.T(ctx, "pular"), Selected: cur == "skip"},
		{Value: "rename", Label: i18n.T(ctx, "renomear"), Selected: cur == "rename"},
		{Value: "replace", Label: i18n.T(ctx, "substituir"), Selected: cur == "replace"},
	}
}

func itemBadge(ctx context.Context, a string) views.Badge {
	switch a {
	case "create":
		return views.Badge{Label: i18n.T(ctx, "criar"), Class: "done"}
	case "skip":
		return views.Badge{Label: i18n.T(ctx, "pular"), Class: "outline"}
	case "rename":
		return views.Badge{Label: i18n.T(ctx, "renomear"), Class: "warn"}
	}
	return views.Badge{Label: i18n.T(ctx, "substituir"), Class: "danger"}
}

// packageVM monta a página de prévia/instalação a partir do resultado do service.
func (s *server) packageVM(ctx context.Context, pkg export.TeamPackage, res service.TeamInstall, opts service.TeamInstallOptions, source, back, preview, install string, installed bool) views.PackagePreviewVM {
	vm := views.PackagePreviewVM{Name: pkg.Package.Name, Version: pkg.Package.Version, Desc: pkg.Package.Description, Author: pkg.Package.Author,
		Source: source, BackHref: back, PreviewHref: preview, InstallHref: install, Installed: installed,
		DefaultOpts: conflictOpts(ctx, string(opts.OnConflict))}
	for _, it := range res.Result.Items {
		row := views.PackageItemVM{Kind: importKindLabel(ctx, it.Kind), Key: it.Key, Name: it.Name, Target: it.Target, Action: itemBadge(ctx, string(it.Action))}
		if it.Action != domain.ItemCreate {
			row.Conflict, row.Select, row.Actions = true, "r:"+it.Kind+"/"+it.Key, conflictOpts(ctx, string(it.Action))
		}
		vm.Items = append(vm.Items, row)
	}
	keys := make([]string, 0, len(res.Result.Counts))
	for k := range res.Result.Counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		vm.Counts = append(vm.Counts, views.CountVM{Label: importCountLabel(ctx, k), N: res.Result.Counts[k]})
	}
	for _, a := range res.Agents {
		ag := views.PackageAgentVM{Agent: a.Agent, Field: "sub:" + a.Agent, Active: strings.Join(a.Subscriptions, ", ")}
		if ag.Active == "" {
			ag.Active = i18n.T(ctx, "nenhuma assinatura ativa")
		}
		ag.Opts = []views.Option{{Value: "", Label: i18n.T(ctx, "— usar o login do harness —"), Selected: a.Chosen == ""}}
		for _, n := range a.Subscriptions {
			ag.Opts = append(ag.Opts, views.Option{Value: n, Label: n, Selected: a.Chosen == n})
		}
		vm.Agents = append(vm.Agents, ag)
	}
	for _, sc := range res.Secrets {
		vm.Secrets = append(vm.Secrets, views.PackageSecretVM{Name: sc.Name, Exists: sc.Exists})
	}
	for _, w := range res.Warnings {
		vm.Warnings = append(vm.Warnings, i18n.Tf(ctx, w.Message, w.Count))
	}
	return vm
}

func importKindLabel(ctx context.Context, k string) string {
	switch k {
	case service.KindPersonas:
		return i18n.T(ctx, "Persona")
	case service.KindSkills:
		return "Skill"
	case service.KindFlows:
		return i18n.T(ctx, "Rotina")
	case service.KindProjects:
		return i18n.T(ctx, "Projeto")
	case service.KindSubscriptions:
		return i18n.T(ctx, "Assinatura")
	}
	return k
}

// runPackage faz a prévia (install=false) ou a instalação e renderiza a página.
func (s *server) runPackage(w http.ResponseWriter, r *http.Request, co domain.Company, pkg export.TeamPackage, source, src, back, preview, install string, doInstall, fromForm bool) {
	b, ok := s.teamPkgBE(w, r, &co)
	if !ok {
		return
	}
	ctx := r.Context()
	opts := service.TeamInstallOptions{OnConflict: domain.ConflictSkip}
	if fromForm {
		var err error
		if opts, err = teamOptsFrom(r); err != nil {
			s.fail(w, r, &co, err)
			return
		}
	}
	opts.Source, opts.DryRun = src, !doInstall
	res, err := b.InstallTeam(ctx, actorFrom(ctx), co.ID, pkg, opts)
	sh := s.shell(r, co, "personas", pkg.Package.Name, views.Crumb{Label: i18n.T(ctx, "Personas"), Href: "/c/" + co.Slug + "/personas"},
		views.Crumb{Label: i18n.T(ctx, "Catálogo de times"), Href: back}, views.Crumb{Label: pkg.Package.Name})
	if err != nil {
		if errors.Is(err, domain.ErrInvalid) || errors.Is(err, domain.ErrConflict) {
			vm := views.PackagePreviewVM{Name: pkg.Package.Name, Version: pkg.Package.Version, Desc: pkg.Package.Description, Source: source, BackHref: back,
				PreviewHref: preview, InstallHref: install, Error: err.Error(), DefaultOpts: conflictOpts(ctx, string(opts.OnConflict))}
			s.render(w, r, statusFor(err), views.PackagePreviewPage(sh, vm))
			return
		}
		s.fail(w, r, &co, err)
		return
	}
	vm := s.packageVM(ctx, pkg, res, opts, source, back, preview, install, doInstall)
	if doInstall {
		vm.Notice = i18n.T(ctx, "Pacote instalado: personas pausadas e rotinas desligadas.")
	}
	s.render(w, r, http.StatusOK, views.PackagePreviewPage(sh, vm))
}

func (s *server) handleCatalogPackage(doInstall, fromForm bool) companyHandler {
	return func(w http.ResponseWriter, r *http.Request, co domain.Company) {
		slug := r.PathValue("slug")
		pkg, ok, err := teams.Get(slug)
		if err != nil || !ok {
			s.fail(w, r, &co, domain.ErrNotFound)
			return
		}
		back := "/c/" + co.Slug + "/personas/catalogo"
		base := back + "/pacote/" + url.PathEscape(slug)
		s.runPackage(w, r, co, pkg, i18n.T(r.Context(), "catálogo embutido"), service.TeamSourceCatalog, back, base+"/previa", base+"/instalar", doInstall, fromForm)
	}
}

// handleTeamUpload recebe o arquivo do pacote, valida e guarda em memória; segue para a prévia.
func (s *server) handleTeamUpload(w http.ResponseWriter, r *http.Request, co domain.Company) {
	if _, ok := s.teamPkgBE(w, r, &co); !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, export.MaxTeamBytes+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		s.fail(w, r, &co, domain.Invalid("file", i18n.T(r.Context(), "arquivo grande demais ou ilegível")))
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		s.fail(w, r, &co, domain.Invalid("file", i18n.T(r.Context(), "envie o arquivo do pacote")))
		return
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, export.MaxTeamBytes+1))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	pkg, err := teams.Parse(raw)
	if err != nil {
		s.fail(w, r, &co, domain.Invalid("file", err.Error()))
		return
	}
	var user domain.ID
	if id, ok := auth.IdentityFrom(r.Context()); ok {
		user = id.User.ID
	}
	tok, err := s.teamPkgs.put(user, pkg, s.now())
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/personas/catalogo/arquivo/"+tok)
}

func (s *server) handleFilePackage(doInstall, fromForm bool) companyHandler {
	return func(w http.ResponseWriter, r *http.Request, co domain.Company) {
		tok := r.PathValue("token")
		var user domain.ID
		if id, ok := auth.IdentityFrom(r.Context()); ok {
			user = id.User.ID
		}
		pkg, ok := s.teamPkgs.get(tok, user, s.now())
		if !ok {
			s.fail(w, r, &co, domain.Invalid("file", i18n.T(r.Context(), "A prévia expirou. Envie o arquivo de novo.")))
			return
		}
		back := "/c/" + co.Slug + "/personas/catalogo"
		base := back + "/arquivo/" + tok
		s.runPackage(w, r, co, pkg, i18n.T(r.Context(), "arquivo enviado"), service.TeamSourceFile, back, base+"/previa", base+"/instalar", doInstall, fromForm)
	}
}

// handleTeamExport baixa o pacote das personas marcadas (todas, se nenhuma).
func (s *server) handleTeamExport(w http.ResponseWriter, r *http.Request, co domain.Company) {
	b, ok := s.teamPkgBE(w, r, &co)
	if !ok {
		return
	}
	q := r.URL.Query()
	p, err := b.ExportTeam(r.Context(), co.ID, service.TeamExportOptions{Personas: q["personas"], Name: strings.TrimSpace(q.Get("name"))})
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+p.Package.Slug+`.team.json"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(append(raw, '\n'))
}
