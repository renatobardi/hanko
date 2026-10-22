package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/md"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Plugins de terceiros (ADR-0038): Configurações › Plugins (instalados, disponíveis, ligar por empresa,
// configurar pelo formulário gerado do config_schema, saúde e logs para o dono da instalação) e o bloco
// "De plugins" do Painel, da task e da persona. O service é acessado por uma interface opcional, como as skills.

type pluginsUIBackend interface {
	PluginOverview(ctx context.Context) (service.PluginOverview, error)
	GetPlugin(ctx context.Context, ref string) (service.PluginView, error)
	InstallPlugin(ctx context.Context, key string) (service.PluginView, error)
	SetPluginEnabled(ctx context.Context, ref string, on bool) (service.PluginView, error)
	UpgradePlugin(ctx context.Context, ref string) (service.PluginView, error)
	ApprovePluginUpgrade(ctx context.Context, ref string) (service.PluginView, error)
	UninstallPlugin(ctx context.Context, ref string) error
	ListPluginLogs(ctx context.Context, ref string, limit int) ([]domain.PluginLog, error)
	ListCompanyPlugins(ctx context.Context, companyID domain.ID) ([]service.CompanyPlugin, error)
	GetCompanyPlugin(ctx context.Context, companyID domain.ID, ref string) (service.CompanyPlugin, error)
	UpdateCompanyPlugin(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string, in service.UpdateCompanyPluginInput) (service.CompanyPlugin, error)
	ListPluginState(ctx context.Context, companyID domain.ID, ref, prefix string, limit int) ([]service.PluginStateItem, error)
	PluginWidgets(ctx context.Context, companyID domain.ID, w service.PluginWidgetRef) ([]service.PluginWidgetView, error)
	PluginsEnabled() bool
	// M18 (ADR-0045).
	SetPluginCapability(ctx context.Context, ref, capability string, granted bool) (service.PluginView, error)
	PluginTabs(ctx context.Context, companyID domain.ID) ([]service.PluginTabView, error)
	PluginPageHTML(ctx context.Context, companyID domain.ID, pluginKey, pageKey string) (title, html string, err error)
	PluginPageCall(ctx context.Context, companyID domain.ID, pluginKey, pageKey, method string, params json.RawMessage) (json.RawMessage, error)
	PluginFlowTriggers(ctx context.Context, companyID domain.ID, ref string) ([]domain.PluginFlowTrigger, []service.PluginFlowLink, error)
	LinkPluginFlowTrigger(ctx context.Context, actor domain.Actor, companyID domain.ID, ref, trigger, flowRef string, link bool) error
	ListFlows(ctx context.Context, companyID domain.ID) ([]service.FlowView, error)
}

func (s *server) pluginsBE() (pluginsUIBackend, bool) {
	b, ok := s.be.(pluginsUIBackend)
	return b, ok
}

func (s *server) pluginRoutes(mux langMux) {
	co := func(h companyHandler) http.HandlerFunc { return s.withCompany(h) }
	post := func(h http.HandlerFunc) http.HandlerFunc { return s.sameOrigin(h) }
	capped := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
			h(w, r)
		}
	}
	mux.HandleFunc("GET /c/{company}/configuracoes/plugins", co(s.handlePlugins))
	mux.HandleFunc("POST /c/{company}/configuracoes/plugins/instalar", post(co(s.handlePluginInstall)))
	mux.HandleFunc("GET /c/{company}/configuracoes/plugins/{plugin}", co(s.handlePluginDetail))
	mux.HandleFunc("POST /c/{company}/configuracoes/plugins/{plugin}", capped(post(co(s.handlePluginConfig))))
	mux.HandleFunc("POST /c/{company}/configuracoes/plugins/{plugin}/ativar", post(co(s.handlePluginToggle)))
	mux.HandleFunc("POST /c/{company}/configuracoes/plugins/{plugin}/instalacao/{acao}", post(co(s.handlePluginOwnerAction)))
	mux.HandleFunc("GET /c/{company}/plugins/widgets", co(s.handlePluginWidgets))
	// M18 (ADR-0045): abas de plugin (página hospedeira, documento do iframe e ponte) e gatilhos de fluxo.
	mux.HandleFunc("GET /c/{company}/plugins/{plugin}/{page}", co(s.handlePluginPage))
	mux.HandleFunc("GET /c/{company}/plugins/{plugin}/{page}/frame", co(s.handlePluginFrame))
	mux.HandleFunc("POST /c/{company}/plugins/{plugin}/{page}/ponte", capped(post(co(s.handlePluginBridge))))
	mux.HandleFunc("POST /c/{company}/configuracoes/plugins/{plugin}/gatilhos/{trigger}/{acao}", post(co(s.handlePluginTriggerLink)))
}

var pluginNotices = map[string]string{
	"instalado":    i18n.N("Plugin instalado."),
	"ligado":       i18n.N("Plugin ligado nesta empresa."),
	"desligado":    i18n.N("Plugin desligado nesta empresa."),
	"salvo":        i18n.N("Configuração salva."),
	"suspenso":     i18n.N("Plugin desligado na instalação: o processo parou e nenhuma empresa o usa até religar."),
	"religado":     i18n.N("Plugin religado na instalação."),
	"atualizado":   i18n.N("Plugin atualizado."),
	"pendente":     i18n.N("A versão nova pede capacidades a mais e aguarda a aprovação."),
	"aprovado":     i18n.N("Capacidades aprovadas e versão aplicada."),
	"desinstalado": i18n.N("Plugin desinstalado: configuração e estado das empresas apagados."),
	// M18 (ADR-0045)
	"concedida":         i18n.N("Capacidade concedida."),
	"revogada":          i18n.N("Capacidade revogada: o que dependia dela deixou de funcionar."),
	"gatilho-ligado":    i18n.N("Gatilho ligado ao fluxo."),
	"gatilho-desligado": i18n.N("Gatilho desligado do fluxo."),
}

func capLabel(ctx context.Context, c string) views.PluginCap {
	labels := map[string]string{
		domain.CapTasksRead:       i18n.T(ctx, "Ler tasks"),
		domain.CapTasksWrite:      i18n.T(ctx, "Criar tasks, comentar e mudar status"),
		domain.CapPersonasRead:    i18n.T(ctx, "Ler personas"),
		domain.CapProjectsRead:    i18n.T(ctx, "Ler projetos"),
		domain.CapActivityRead:    i18n.T(ctx, "Ler a atividade"),
		domain.CapEventsSubscribe: i18n.T(ctx, "Receber eventos"),
		domain.CapAgentTools:      i18n.T(ctx, "Oferecer tools às personas"),
		domain.CapWidgets:         i18n.T(ctx, "Mostrar widgets"),
		domain.CapJobs:            i18n.T(ctx, "Rodar jobs periódicos"),
		domain.CapState:           i18n.T(ctx, "Guardar estado próprio"),
		domain.CapHTTPOutbound:    i18n.T(ctx, "Acessar a rede (declarado)"),
		domain.CapPages:           i18n.T(ctx, "Abas na barra lateral (a página do plugin roda isolada)"),
		domain.CapFlowTriggers:    i18n.T(ctx, "Disparar fluxos ligados pelo admin"),
		domain.CapEnvProvider:     i18n.T(ctx, "Oferecer sandboxes em nuvem (recebe a chave de API do ambiente)"),
		domain.CapSkillSource:     i18n.T(ctx, "Oferecer fontes de skills"),
	}
	l := labels[c]
	if l == "" {
		l = c
	}
	return views.PluginCap{Name: c, Label: l, Warn: c == domain.CapTasksWrite || c == domain.CapHTTPOutbound || c == domain.CapAgentTools || c == domain.CapPages || c == domain.CapEnvProvider}
}

func capsOf(ctx context.Context, cs []string) []views.PluginCap {
	out := make([]views.PluginCap, 0, len(cs))
	for _, c := range domain.SortCapabilities(cs) {
		out = append(out, capLabel(ctx, c))
	}
	return out
}

func pluginBadge(ctx context.Context, st domain.PluginStatus) views.Badge {
	switch st {
	case domain.PluginReady:
		return views.Badge{Label: i18n.T(ctx, "Pronto"), Class: "done"}
	case domain.PluginError:
		return views.Badge{Label: i18n.T(ctx, "Com erro"), Class: "danger"}
	case domain.PluginDisabled:
		return views.Badge{Label: i18n.T(ctx, "Desligado na instalação"), Class: "soft"}
	case domain.PluginUpgradePending:
		return views.Badge{Label: i18n.T(ctx, "Atualização pendente"), Class: "warn"}
	case domain.PluginInstalled:
		return views.Badge{Label: i18n.T(ctx, "Instalado"), Class: "outline"}
	}
	return views.Badge{Label: string(st), Class: "outline"}
}

func pluginsBase(co domain.Company) string { return "/c/" + co.Slug + "/configuracoes/plugins" }

// pluginCard monta o cartão de um plugin na empresa. owner traz o estado da instalação (só para o dono).
func pluginCard(ctx context.Context, co domain.Company, cp service.CompanyPlugin, owner *service.PluginView) views.PluginCardVM {
	href := pluginsBase(co) + "/" + url.PathEscape(cp.Key)
	c := views.PluginCardVM{Key: cp.Key, Name: cp.Name, Desc: cp.Description, Version: cp.Version, Author: cp.Author, Homepage: cp.Homepage,
		Badge: pluginBadge(ctx, cp.Status), Caps: capsOf(ctx, cp.Capabilities), Enabled: cp.Enabled, Active: cp.Active,
		ToggleHref: href + "/ativar", Href: href, Tools: len(cp.Tools), Widgets: len(cp.Widgets), Jobs: len(cp.Jobs), Events: len(cp.Events),
		Pages: len(cp.Pages), Triggers: len(cp.FlowTriggers), Providers: len(cp.EnvProviders), Sources: len(cp.SkillSources)}
	if !strings.HasPrefix(c.Homepage, "https://") && !strings.HasPrefix(c.Homepage, "http://") {
		c.Homepage = ""
	}
	if owner != nil {
		c.OwnerBase = href + "/instalacao"
		c.CanSuspend = owner.Status.CanBecome(domain.PluginDisabled)
		c.CanResume = owner.Status == domain.PluginDisabled
		c.LastError = owner.LastError
		c.CapToggles = capToggles(ctx, c.OwnerBase, *owner)
		if owner.PendingManifest != nil {
			c.PendingVersion = owner.PendingManifest.Version
			c.PendingCaps = capsOf(ctx, domain.ExtraCapabilities(owner.GrantedCapabilities, owner.PendingCapabilities))
		}
	}
	return c
}

func (s *server) handlePlugins(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	vm := views.PluginsPageVM{Sections: settingsSections(ctx, "/c/"+co.Slug, "plugins"), Builtin: s.builtinPluginItems(ctx, co),
		Notice: i18n.T(ctx, pluginNotices[r.URL.Query().Get("ok")])}
	if e := r.URL.Query().Get("erro"); e != "" {
		vm.Error = pluginClip(e, 400)
	}
	pb, ok := s.pluginsBE()
	if !ok {
		s.render(w, r, http.StatusOK, views.PluginsPage(s.settingsShell(r, co, i18n.T(ctx, "Plugins")), vm))
		return
	}
	ov, err := pb.PluginOverview(ctx)
	owners := map[string]*service.PluginView{}
	switch {
	case err == nil:
		vm.Owner, vm.On, vm.Dir = true, ov.Enabled, ov.Dir
		for i := range ov.Installed {
			owners[ov.Installed[i].Key] = &ov.Installed[i]
		}
	case errors.Is(err, domain.ErrForbidden):
	case errors.Is(err, domain.ErrInvalid):
		vm.Owner, vm.On = true, true
		vm.Error = errText(err)
	default:
		s.fail(w, r, &co, err)
		return
	}
	list, err := pb.ListCompanyPlugins(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	if !vm.Owner {
		vm.On = pb.PluginsEnabled()
	}
	for _, cp := range list {
		vm.Installed = append(vm.Installed, pluginCard(ctx, co, cp, owners[cp.Key]))
	}
	for _, a := range ov.Available {
		if a.Installed != "" && !a.Upgrade && a.Error == "" {
			continue
		}
		av := views.AvailablePluginVM{Key: a.Key, Dir: a.Dir, Error: a.Error, Installed: a.Installed, Upgrade: a.Upgrade, Name: a.Key,
			InstallAction: pluginsBase(co) + "/instalar?key=" + url.QueryEscape(a.Key),
			UpgradeAction: pluginsBase(co) + "/" + url.PathEscape(a.Key) + "/instalacao/atualizar"}
		if m := a.Manifest; m != nil {
			av.Name, av.Version, av.Desc, av.Caps = m.Name, m.Version, m.Description, capsOf(ctx, m.Capabilities)
		}
		vm.Available = append(vm.Available, av)
	}
	s.render(w, r, http.StatusOK, views.PluginsPage(s.settingsShell(r, co, i18n.T(ctx, "Plugins")), vm))
}

func errText(err error) string {
	var ve *domain.ValidationError
	if errors.As(err, &ve) {
		return ve.Reason
	}
	return err.Error()
}

func pluginClip(v string, n int) string {
	r := []rune(v)
	if len(r) > n {
		return string(r[:n])
	}
	return v
}

// pluginFail volta para a lista com o erro do service (formulários sem htmx) ou o devolve no #flash (htmx).
func (s *server) pluginFail(w http.ResponseWriter, r *http.Request, co domain.Company, back string, err error) {
	if isHX(r) || !errors.Is(err, domain.ErrInvalid) && !errors.Is(err, domain.ErrConflict) {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, back+"?erro="+url.QueryEscape(pluginClip(errText(err), 400)))
}

func (s *server) handlePluginInstall(w http.ResponseWriter, r *http.Request, co domain.Company) {
	pb, ok := s.pluginsBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if key == "" {
		key = trimForm(r, "key")
	}
	if _, err := pb.InstallPlugin(r.Context(), key); err != nil {
		s.pluginFail(w, r, co, pluginsBase(co), err)
		return
	}
	redirect(w, r, pluginsBase(co)+"?ok=instalado")
}

func (s *server) handlePluginOwnerAction(w http.ResponseWriter, r *http.Request, co domain.Company) {
	pb, ok := s.pluginsBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	ref := r.PathValue("plugin")
	var v service.PluginView
	var err error
	ok2 := ""
	switch r.PathValue("acao") {
	case "desligar":
		v, err = pb.SetPluginEnabled(ctx, ref, false)
		ok2 = "suspenso"
	case "ligar":
		v, err = pb.SetPluginEnabled(ctx, ref, true)
		ok2 = "religado"
	case "atualizar":
		v, err = pb.UpgradePlugin(ctx, ref)
		ok2 = "atualizado"
		if err == nil && v.Status == domain.PluginUpgradePending {
			ok2 = "pendente"
		}
	case "aprovar":
		_, err = pb.ApprovePluginUpgrade(ctx, ref)
		ok2 = "aprovado"
	case "desinstalar":
		err = pb.UninstallPlugin(ctx, ref)
		ok2 = "desinstalado"
	case "conceder", "revogar":
		s.handlePluginCapability(w, r, co, r.PathValue("acao") == "conceder")
		return
	default:
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	if err != nil {
		s.pluginFail(w, r, co, pluginsBase(co), err)
		return
	}
	redirect(w, r, pluginsBase(co)+"?ok="+ok2)
}

func (s *server) handlePluginToggle(w http.ResponseWriter, r *http.Request, co domain.Company) {
	pb, ok := s.pluginsBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	on := r.PostFormValue("enabled") == "1"
	if _, err := pb.UpdateCompanyPlugin(ctx, actorFrom(ctx), co.ID, r.PathValue("plugin"), service.UpdateCompanyPluginInput{Enabled: &on}); err != nil {
		s.pluginFail(w, r, co, pluginsBase(co), err)
		return
	}
	notice := "desligado"
	if on {
		notice = "ligado"
	}
	back := pluginsBase(co)
	if strings.Contains(r.Header.Get("Referer"), "/configuracoes/plugins/"+r.PathValue("plugin")) {
		back += "/" + url.PathEscape(r.PathValue("plugin"))
	}
	redirect(w, r, back+"?ok="+notice)
}

// pluginFields monta o formulário a partir do config_schema e dos valores (sem segredos).
func pluginFields(ctx context.Context, cp service.CompanyPlugin) []views.PluginFieldVM {
	req := map[string]bool{}
	for _, k := range cp.ConfigSchema.Required {
		req[k] = true
	}
	set := map[string]bool{}
	for _, k := range cp.SecretsSet {
		set[k] = true
	}
	var out []views.PluginFieldVM
	for _, k := range cp.ConfigSchema.Keys() {
		f := cp.ConfigSchema.Properties[k]
		label := f.Title
		if label == "" {
			label = k
		}
		fv := views.PluginFieldVM{Key: k, Label: label, Desc: f.Description, Type: f.Type, Required: req[k], Secret: f.Secret, SecretSet: set[k]}
		cur, has := cp.Config[k]
		if !has && f.Default != nil {
			fv.Placeholder = fmt.Sprint(f.Default)
		}
		if has {
			fv.Value = fmt.Sprint(cur)
		}
		if f.Type == domain.ConfigBoolean {
			v := cur
			if !has {
				v = f.Default
			}
			fv.Checked = v == true
		}
		if len(f.Enum) > 0 {
			sel := fv.Value
			if sel == "" && f.Default != nil {
				sel = fmt.Sprint(f.Default)
			}
			fv.Options = append(fv.Options, views.Option{Value: "", Label: i18n.T(ctx, "(padrão)"), Selected: sel == ""})
			for _, e := range f.Enum {
				fv.Options = append(fv.Options, views.Option{Value: e, Label: e, Selected: e == sel})
			}
		}
		out = append(out, fv)
	}
	return out
}

func (s *server) pluginDetailVM(r *http.Request, co domain.Company, pb pluginsUIBackend, cp service.CompanyPlugin) views.PluginDetailVM {
	ctx := r.Context()
	loc := loadLoc(co.Timezone)
	var owner *service.PluginView
	if v, err := pb.GetPlugin(ctx, cp.Key); err == nil {
		owner = &v
	}
	vm := views.PluginDetailVM{Sections: settingsSections(ctx, "/c/"+co.Slug, "plugins"), Card: pluginCard(ctx, co, cp, owner),
		BackHref: pluginsBase(co), FormAction: pluginsBase(co) + "/" + url.PathEscape(cp.Key), Fields: pluginFields(ctx, cp),
		Events: cp.Events, StateKeys: -1, Notice: i18n.T(ctx, pluginNotices[r.URL.Query().Get("ok")])}
	if e := r.URL.Query().Get("erro"); e != "" {
		vm.Error = pluginClip(e, 400)
	}
	s.pluginContribVM(ctx, co, pb, cp, &vm)
	for _, t := range cp.Tools {
		vm.Tools = append(vm.Tools, views.PluginToolRow{Name: t.Name, MCPName: domain.PluginToolName(cp.Key, t.Name), Desc: t.Description})
	}
	for _, j := range cp.JobRuns {
		row := views.PluginJobRow{Key: j.JobKey, Started: pluginWhen(ctx, j.StartedAt.In(loc)), Error: j.Error}
		if j.FinishedAt != nil {
			row.Finished = pluginWhen(ctx, j.FinishedAt.In(loc))
		}
		switch j.Status {
		case domain.PluginJobDone:
			row.Badge = views.Badge{Label: i18n.T(ctx, "Concluído"), Class: "done"}
		case domain.PluginJobFailed:
			row.Badge = views.Badge{Label: i18n.T(ctx, "Falhou"), Class: "danger"}
		default:
			row.Badge = views.Badge{Label: i18n.T(ctx, "Rodando"), Class: "run", Live: true}
		}
		vm.Jobs = append(vm.Jobs, row)
	}
	if items, err := pb.ListPluginState(ctx, co.ID, cp.Key, "", 1000); err == nil {
		vm.StateKeys = len(items)
	}
	if owner != nil {
		h := &views.PluginHealthVM{PID: "—", Uptime: "—", Restarts: fmt.Sprint(owner.Process.Restarts), LastError: owner.LastError}
		switch {
		case owner.Process.Ready:
			h.Badge = views.Badge{Label: i18n.T(ctx, "No ar"), Class: "done"}
		case owner.Process.Running:
			h.Badge = views.Badge{Label: i18n.T(ctx, "Subindo"), Class: "warn"}
		default:
			h.Badge = views.Badge{Label: i18n.T(ctx, "Parado"), Class: "soft"}
		}
		if owner.Process.PID > 0 {
			h.PID = fmt.Sprint(owner.Process.PID)
		}
		if owner.Process.StartedAt != nil {
			h.Uptime = fmtDuration(s.now().Sub(*owner.Process.StartedAt).Truncate(time.Second))
		}
		if owner.Process.NextRestart != nil {
			h.Next = pluginWhen(ctx, owner.Process.NextRestart.In(loc))
		}
		if owner.Process.LastError != "" {
			h.LastError = owner.Process.LastError
		}
		vm.Health = h
		if logs, err := pb.ListPluginLogs(ctx, cp.Key, 100); err == nil {
			for _, l := range logs {
				vm.Logs = append(vm.Logs, views.PluginLogRow{When: pluginWhen(ctx, l.CreatedAt.In(loc)), Level: l.Level, Message: l.Message})
			}
		}
	}
	return vm
}

func (s *server) handlePluginDetail(w http.ResponseWriter, r *http.Request, co domain.Company) {
	pb, ok := s.pluginsBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	cp, err := pb.GetCompanyPlugin(ctx, co.ID, r.PathValue("plugin"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.renderPluginDetail(w, r, co, http.StatusOK, s.pluginDetailVM(r, co, pb, cp))
}

func (s *server) renderPluginDetail(w http.ResponseWriter, r *http.Request, co domain.Company, status int, vm views.PluginDetailVM) {
	ctx := r.Context()
	base := "/c/" + co.Slug
	sh := s.shell(r, co, "configuracoes", vm.Card.Name, views.Crumb{Label: i18n.T(ctx, "Configurações"), Href: base + "/configuracoes"},
		views.Crumb{Label: i18n.T(ctx, "Plugins"), Href: pluginsBase(co)}, views.Crumb{Label: vm.Card.Name})
	s.render(w, r, status, views.PluginDetailPage(sh, vm))
}

func (s *server) handlePluginConfig(w http.ResponseWriter, r *http.Request, co domain.Company) {
	pb, ok := s.pluginsBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	cp, err := pb.GetCompanyPlugin(ctx, co.ID, r.PathValue("plugin"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, &co, domain.Invalid("form", "formulário inválido"))
		return
	}
	in := service.UpdateCompanyPluginInput{Config: map[string]any{}}
	for _, k := range cp.ConfigSchema.Keys() {
		f := cp.ConfigSchema.Properties[k]
		switch {
		case f.Type == domain.ConfigBoolean:
			if r.PostFormValue("has_"+k) == "" {
				continue
			}
			in.Config[k] = r.PostFormValue("cfg_"+k) == "true"
		case f.Secret:
			if r.PostFormValue("clear_"+k) != "" {
				in.ClearSecrets = append(in.ClearSecrets, k)
				continue
			}
			if v := r.PostFormValue("cfg_" + k); v != "" {
				in.Config[k] = v
			}
		default:
			if _, sent := r.PostForm["cfg_"+k]; sent {
				in.Config[k] = strings.TrimSpace(r.PostFormValue("cfg_" + k))
			}
		}
	}
	updated, err := pb.UpdateCompanyPlugin(ctx, actorFrom(ctx), co.ID, cp.Key, in)
	if err != nil {
		if !errors.Is(err, domain.ErrInvalid) && !errors.Is(err, domain.ErrConflict) {
			s.fail(w, r, &co, err)
			return
		}
		vm := s.pluginDetailVM(r, co, pb, cp)
		// devolve o que foi digitado (menos os segredos) e marca o campo com problema
		for i := range vm.Fields {
			f := &vm.Fields[i]
			if f.Secret {
				continue
			}
			if v, sent := r.PostForm["cfg_"+f.Key]; sent && f.Type != domain.ConfigBoolean {
				f.Value = strings.TrimSpace(v[0])
			}
			var ve *domain.ValidationError
			if errors.As(err, &ve) && ve.Field == "config."+f.Key {
				f.Err = ve.Reason
			}
		}
		vm.Error = errText(err)
		s.renderPluginDetail(w, r, co, statusFor(err), vm)
		return
	}
	redirect(w, r, pluginsBase(co)+"/"+url.PathEscape(updated.Key)+"?ok=salvo")
}

// handlePluginWidgets devolve o bloco "De plugins" (fragmento htmx). Sem widget, nada (o lugar some).
func (s *server) handlePluginWidgets(w http.ResponseWriter, r *http.Request, co domain.Company) {
	pb, ok := s.pluginsBE()
	if !ok {
		w.WriteHeader(http.StatusOK)
		return
	}
	ctx := r.Context()
	ws, err := pb.PluginWidgets(ctx, co.ID, service.PluginWidgetRef{Placement: r.URL.Query().Get("lugar"), Ref: r.URL.Query().Get("ref")})
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrInvalid) {
			w.WriteHeader(http.StatusOK)
			return
		}
		s.log.Warn("ui: widgets de plugin", "err", err)
		w.WriteHeader(http.StatusOK)
		return
	}
	out := make([]views.PluginWidgetVM, 0, len(ws))
	for _, x := range ws {
		v := views.PluginWidgetVM{Plugin: x.PluginName, Title: x.Title, Body: views.HTML(md.RenderArtifact(x.Markdown))}
		if x.Error != "" {
			v.Error = i18n.T(ctx, "O plugin não respondeu a tempo; tente de novo em instantes.")
		}
		if x.Table != nil {
			v.Columns, v.Rows = x.Table.Columns, x.Table.Rows
		}
		out = append(out, v)
	}
	s.render(w, r, http.StatusOK, views.PluginWidgets(out))
}

// pluginWhen é dia, mês e hora no idioma da página ("13 out 14:05").
func pluginWhen(ctx context.Context, t time.Time) string {
	return shortDate(ctx, t) + " " + t.Format("15:04")
}
