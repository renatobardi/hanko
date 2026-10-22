package service

import (
	"context"
	"crypto/sha1" //nolint:gosec // identificador de revisão (40 hex, como um commit), não segurança
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/execenv"
	"github.com/renatobardi/hanko/internal/plugin"
	"github.com/renatobardi/hanko/internal/skillgit"
	"github.com/renatobardi/hanko/internal/skillmd"
	"github.com/renatobardi/hanko/internal/store"
)

// Plugins, lacunas do M18 (ADR-0045): concessão e revogação de capacidades, abas em iframe com ponte, gatilhos de
// fluxo, provider de ambiente e fonte de skills. O desenho do ADR-0038 não muda: processo externo, JSON-RPC por
// stdio, capacidades checadas pelo host a cada uso.

const (
	pluginPageTimeout  = 10 * time.Second
	pluginBridgeTimout = 30 * time.Second
	pluginEnvTimeout   = 3 * time.Minute
	pluginSnapTimeout  = 60 * time.Second
	pluginSnapMaxSkill = 200
)

func pluginSpan(ctx context.Context, op string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return otel.Tracer("hanko/plugins").Start(ctx, "hanko.plugin."+op, trace.WithAttributes(attrs...))
}

func endSpan(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

// ---------- concessão e revogação de capacidades ----------

// SetPluginCapability concede ou revoga uma capacidade do plugin (dono da instalação). Só se concede o que o
// manifesto vigente declara; revogar vale na hora para tudo o que a capacidade sustenta (tools, aba, widgets,
// gatilhos, provider, fonte de skills): o host confere a concessão a cada uso.
func (s *Service) SetPluginCapability(ctx context.Context, ref, capability string, granted bool) (PluginView, error) {
	if err := s.requirePluginsOn(); err != nil {
		return PluginView{}, err
	}
	if err := s.requirePluginOwner(ctx); err != nil {
		return PluginView{}, err
	}
	if !domain.ValidPluginCapability(capability) {
		return PluginView{}, domain.Invalid("capability", fmt.Sprintf("capacidade desconhecida %q", capability))
	}
	rt := s.prt()
	rt.life.Lock()
	defer rt.life.Unlock()
	var row store.Plugin
	err := s.withTx(ctx, func(t *txc) error {
		r, err := s.getPlugin(t.ctx, t.q, ref)
		if err != nil {
			return err
		}
		if r, err = t.q.GetPluginForUpdate(t.ctx, r.ID); err != nil {
			return err
		}
		p := pluginFromRow(r)
		if p.Status == domain.PluginUninstalled {
			return fmt.Errorf("plugin %s está desinstalado: %w", r.PluginKey, domain.ErrConflict)
		}
		if p.Has(capability) == granted {
			row = r
			return nil
		}
		if granted && !slices.Contains(p.Manifest.Capabilities, capability) {
			return domain.Invalid("capability", fmt.Sprintf("o manifesto do plugin não declara %s", capability))
		}
		next := slices.Clone(p.GrantedCapabilities)
		if granted {
			next = append(next, capability)
		} else {
			next = slices.DeleteFunc(next, func(c string) bool { return c == capability })
		}
		if row, err = t.q.SetPluginGrantedCapabilities(t.ctx, store.SetPluginGrantedCapabilitiesParams{ID: r.ID,
			GrantedCapabilities: domain.SortCapabilities(next), Now: t.now}); err != nil {
			return err
		}
		verb, action := "plugin.capability_revoked", "plugin.capability_revoke"
		if granted {
			verb, action = "plugin.capability_granted", "plugin.capability_grant"
		}
		if err := t.pluginCompanyActivity(r, verb, map[string]any{"capability": capability}); err != nil {
			return err
		}
		return s.recordAdmin(t.ctx, t.q, action, r.PluginKey+" "+capability, &r.ID, "ok")
	})
	if err != nil {
		return PluginView{}, err
	}
	s.dropWidgetCache(row.ID)
	verb := "revogada"
	if granted {
		verb = "concedida"
	}
	s.pluginLog(ctx, row.ID, nil, domain.PluginLogHost, "capacidade "+verb+": "+capability)
	// http.outbound muda o ambiente do processo: o reconciliador reinicia quando a especificação muda.
	s.reconcileLocked(ctx)
	return s.GetPlugin(ctx, row.PluginKey)
}

// activePluginFor resolve um plugin ativo, com a capacidade concedida e ligado na empresa. Qualquer falha dá a
// mesma resposta "não encontrado": quem pergunta não descobre o que existe.
func (s *Service) activePluginFor(ctx context.Context, companyID domain.ID, key, capability string) (domain.Plugin, store.Company, error) {
	if !s.Plugins.Enabled() {
		return domain.Plugin{}, store.Company{}, notFound("plugin")
	}
	q := s.read()
	r, err := q.GetPluginByKey(ctx, key)
	if err != nil {
		return domain.Plugin{}, store.Company{}, mapErr(err, "plugin")
	}
	p := pluginFromRow(r)
	if !p.Status.Active() || !p.Has(capability) {
		return domain.Plugin{}, store.Company{}, notFound("plugin")
	}
	st, err := q.GetPluginSettings(ctx, store.GetPluginSettingsParams{PluginID: p.ID, CompanyID: companyID})
	if err != nil || !st.Enabled {
		return domain.Plugin{}, store.Company{}, notFound("plugin")
	}
	co, err := q.GetCompanyByID(ctx, companyID)
	if err != nil {
		return domain.Plugin{}, store.Company{}, mapErr(err, "company")
	}
	return p, co, nil
}

// pluginCallError traduz o erro de uma chamada ao plugin para um erro da API (nunca vaza o interior do plugin).
func pluginCallError(what string, err error) error {
	var re *plugin.RPCError
	switch {
	case errors.As(err, &re):
		return domain.Invalid("plugin", what+": "+pluginClip(re.Message, 300))
	case errors.Is(err, plugin.ErrNotRunning):
		return fmt.Errorf("%s: o plugin não está rodando: %w", what, domain.ErrConflict)
	}
	return fmt.Errorf("%s: %s: %w", what, pluginClip(err.Error(), 300), domain.ErrConflict)
}

// ---------- abas (páginas em iframe) ----------

// PluginTabView é uma aba que um plugin acrescenta ao menu da empresa.
type PluginTabView struct {
	PluginKey  string `json:"plugin"`
	PluginName string `json:"plugin_name"`
	Key        string `json:"key"`
	Title      string `json:"title"`
	Icon       string `json:"icon,omitempty"`
}

// PluginTabs lista as abas dos plugins ativos, ligados na empresa e com a capacidade pages concedida (qualquer
// membro). Revogar pages (ou desligar o plugin) tira a aba na hora.
func (s *Service) PluginTabs(ctx context.Context, companyID domain.ID) ([]PluginTabView, error) {
	if !s.Plugins.Enabled() {
		return nil, nil
	}
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.read().ListActivePluginsForCompany(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list active plugins: %w", err)
	}
	var out []PluginTabView
	for _, r := range rows {
		p := pluginFromRow(r)
		if !p.Has(domain.CapPages) {
			continue
		}
		for _, pg := range p.Manifest.Pages {
			out = append(out, PluginTabView{PluginKey: p.Key, PluginName: p.Manifest.Name, Key: pg.Key, Title: pg.Title, Icon: pg.Icon})
		}
	}
	return out, nil
}

// pluginUser é o que o plugin sabe de quem abriu a página: id, nome e papel (nunca o e-mail).
func (s *Service) pluginUser(ctx context.Context, companyID domain.ID) map[string]string {
	u := map[string]string{"role": string(domain.MemberOwner)}
	if role, _, err := s.callerRole(ctx, companyID); err == nil {
		u["role"] = string(role)
	}
	if id, ok := auth.IdentityFrom(ctx); ok {
		u["id"], u["name"] = id.User.ID.String(), id.User.Name
	}
	return u
}

// PluginPageHTML pede ao plugin o HTML da página (get_page). O HTML é de terceiros: quem o serve o entrega num
// iframe sandboxed, com CSP sem rede (ADR-0045). Qualquer membro abre a página.
func (s *Service) PluginPageHTML(ctx context.Context, companyID domain.ID, pluginKey, pageKey string) (title, html string, err error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return "", "", err
	}
	ctx, span := pluginSpan(ctx, "page", attribute.String("hanko.plugin", pluginKey), attribute.String("hanko.plugin.page", pageKey))
	defer func() { endSpan(span, err) }()
	p, co, err := s.activePluginFor(ctx, companyID, pluginKey, domain.CapPages)
	if err != nil {
		return "", "", err
	}
	pg, ok := p.Manifest.Page(pageKey)
	if !ok {
		return "", "", notFound("page")
	}
	if s.Plugins.Runtime == nil {
		return "", "", pluginCallError("página", plugin.ErrNotRunning)
	}
	raw, err := s.Plugins.Runtime.Call(ctx, p.Key, "get_page", map[string]any{"company": refOf(co), "page": pg.Key, "user": s.pluginUser(ctx, companyID)}, pluginPageTimeout)
	if err != nil {
		return "", "", pluginCallError("página", err)
	}
	var res struct {
		HTML string `json:"html"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || strings.TrimSpace(res.HTML) == "" {
		return "", "", domain.Invalid("plugin", "página: resposta inválida do plugin (esperado {\"html\": \"…\"})")
	}
	if len(res.HTML) > domain.PluginPageHTMLMax {
		return "", "", domain.Invalid("plugin", fmt.Sprintf("página: HTML maior que %d KiB", domain.PluginPageHTMLMax>>10))
	}
	return pg.Title, res.HTML, nil
}

// PluginPageCall é a ponte: a página (no iframe) chama um método do próprio plugin (page_call), pelo Hanko. O
// plugin recebe quem chamou (id, nome, papel) e decide o que permitir; o Hanko só entrega.
func (s *Service) PluginPageCall(ctx context.Context, companyID domain.ID, pluginKey, pageKey, method string, params json.RawMessage) (_ json.RawMessage, err error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	if !domain.ValidPluginBridgeMethod(method) {
		return nil, domain.Invalid("method", "use ^[a-z][a-z0-9_.]{0,63}$")
	}
	if len(params) > domain.PluginPageCallMax {
		return nil, domain.Invalid("params", fmt.Sprintf("maior que %d KiB", domain.PluginPageCallMax>>10))
	}
	if len(params) == 0 {
		params = json.RawMessage(`null`)
	}
	ctx, span := pluginSpan(ctx, "page_call", attribute.String("hanko.plugin", pluginKey), attribute.String("hanko.plugin.page", pageKey),
		attribute.String("hanko.plugin.method", method))
	defer func() { endSpan(span, err) }()
	p, co, err := s.activePluginFor(ctx, companyID, pluginKey, domain.CapPages)
	if err != nil {
		return nil, err
	}
	if _, ok := p.Manifest.Page(pageKey); !ok {
		return nil, notFound("page")
	}
	if s.Plugins.Runtime == nil {
		return nil, pluginCallError("ponte", plugin.ErrNotRunning)
	}
	raw, err := s.Plugins.Runtime.Call(ctx, p.Key, "page_call", map[string]any{"company": refOf(co), "page": pageKey, "method": method,
		"params": params, "user": s.pluginUser(ctx, companyID)}, pluginBridgeTimout)
	if err != nil {
		return nil, pluginCallError("ponte", err)
	}
	if len(raw) > domain.PluginPageCallMax {
		return nil, domain.Invalid("plugin", "ponte: resposta maior que o teto")
	}
	return raw, nil
}

// ---------- gatilhos de fluxo ----------

// PluginFlowLink liga um gatilho do plugin a um fluxo da empresa.
type PluginFlowLink struct {
	Trigger  string `json:"trigger"`
	FlowID   string `json:"flow_id"`
	FlowSlug string `json:"flow_slug"`
	FlowName string `json:"flow_name"`
}

// PluginFlowTriggers lista os gatilhos declarados pelo plugin e as ligações da empresa (qualquer membro).
func (s *Service) PluginFlowTriggers(ctx context.Context, companyID domain.ID, ref string) ([]domain.PluginFlowTrigger, []PluginFlowLink, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, nil, err
	}
	p, err := s.installedPlugin(ctx, s.read(), ref)
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.read().ListPluginFlowTriggers(ctx, store.ListPluginFlowTriggersParams{PluginID: p.ID, CompanyID: companyID})
	if err != nil {
		return nil, nil, fmt.Errorf("list plugin flow triggers: %w", err)
	}
	links := make([]PluginFlowLink, 0, len(rows))
	for _, r := range rows {
		links = append(links, PluginFlowLink{Trigger: r.TriggerKey, FlowID: r.FlowID.String(), FlowSlug: r.FlowSlug, FlowName: r.FlowName})
	}
	return emptyIfNil(p.Manifest.FlowTriggers), links, nil
}

// LinkPluginFlowTrigger liga (ou desliga) um gatilho do plugin a um fluxo da empresa (admin). O gatilho precisa
// estar no manifesto; a ligação independe de o plugin estar ligado agora (volta quando religar).
func (s *Service) LinkPluginFlowTrigger(ctx context.Context, actor domain.Actor, companyID domain.ID, ref, trigger, flowRef string, link bool) error {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		p, err := s.installedPlugin(t.ctx, t.q, ref)
		if err != nil {
			return err
		}
		if _, ok := p.Manifest.FlowTrigger(trigger); !ok {
			return domain.Invalid("trigger", fmt.Sprintf("o plugin %s não declara o gatilho %q", p.Key, trigger))
		}
		fl, err := s.GetFlow(t.ctx, companyID, flowRef)
		if err != nil {
			return err
		}
		var created *domain.ID
		if actor.Type == domain.ActorHuman {
			created = actor.ID
		}
		verb := "plugin.flow_trigger_linked"
		if link {
			if _, err := t.q.LinkPluginFlowTrigger(t.ctx, store.LinkPluginFlowTriggerParams{PluginID: p.ID, CompanyID: companyID, TriggerKey: trigger,
				FlowID: fl.ID, CreatedBy: created, Now: t.now}); err != nil {
				return fmt.Errorf("link plugin flow trigger: %w", err)
			}
		} else {
			verb = "plugin.flow_trigger_unlinked"
			n, err := t.q.UnlinkPluginFlowTrigger(t.ctx, store.UnlinkPluginFlowTriggerParams{PluginID: p.ID, CompanyID: companyID, TriggerKey: trigger, FlowID: fl.ID})
			if err != nil {
				return fmt.Errorf("unlink plugin flow trigger: %w", err)
			}
			if n == 0 {
				return notFound("link")
			}
		}
		return t.activity(actor, companyID, verb, "plugin", p.ID, map[string]any{"plugin": p.Key, "trigger": trigger, "flow": fl.Slug}, "")
	})
}

// hostFlowsFire atende flows.fire: dispara os fluxos ligados ao gatilho. Fluxos desligados pelo admin ficam de
// fora; cada disparo cria a task do fluxo como qualquer disparo manual. Sem ligação, nada acontece (e o plugin é
// avisado com fired = 0).
func (s *Service) hostFlowsFire(ctx context.Context, p domain.Plugin, co store.Company, params json.RawMessage) (any, error) {
	in, err := decode[struct {
		Trigger string `json:"trigger"`
	}](params)
	if err != nil {
		return nil, err
	}
	if _, ok := p.Manifest.FlowTrigger(in.Trigger); !ok {
		return nil, domain.Invalid("trigger", fmt.Sprintf("gatilho %q não declarado no manifesto", in.Trigger))
	}
	ctx, span := pluginSpan(ctx, "flows_fire", attribute.String("hanko.plugin", p.Key), attribute.String("hanko.plugin.trigger", in.Trigger))
	defer span.End()
	ids, err := s.read().ListFlowsForPluginTrigger(ctx, store.ListFlowsForPluginTriggerParams{PluginID: p.ID, CompanyID: co.ID, TriggerKey: in.Trigger})
	if err != nil {
		return nil, fmt.Errorf("list flows for plugin trigger: %w", err)
	}
	type fired struct {
		Flow string `json:"flow"`
		Task string `json:"task,omitempty"`
	}
	out := []fired{}
	skipped := 0
	cid := co.ID
	for _, id := range ids {
		fv, err := s.GetFlow(ctx, cid, id.String())
		if err != nil {
			return nil, err
		}
		if !fv.Enabled {
			skipped++
			continue
		}
		res, err := s.fireFlow(ctx, pluginActor(p), &cid, id, s.Now().UTC(), false)
		if err != nil {
			return nil, err
		}
		f := fired{Flow: fv.Slug}
		if res.Task != nil {
			f.Task = res.Task.Key
		}
		out = append(out, f)
	}
	if err := s.withTx(ctx, func(t *txc) error {
		return t.activity(pluginActor(p), co.ID, "plugin.flow_triggered", "plugin", p.ID,
			map[string]any{"plugin": p.Key, "trigger": in.Trigger, "fired": len(out), "skipped_disabled": skipped}, "")
	}); err != nil {
		return nil, err
	}
	return map[string]any{"fired": len(out), "flows": out, "skipped_disabled": skipped}, nil
}

// ---------- provider de ambiente (sandbox em nuvem) ----------

// PluginCloudDriver resolve um provider plugin:<id>/<key> para o driver cloud (execenv.Config.CloudPlugins). A cada
// resolução confere que o plugin está ativo e com environments.provider concedida: revogar a capacidade corta o uso
// na próxima run.
func (s *Service) PluginCloudDriver(provider string) (execenv.CloudDriver, error) {
	id, key, ok := domain.ParsePluginProvider(provider)
	if !ok {
		return nil, fmt.Errorf("provider %q inválido", provider)
	}
	if !s.Plugins.Enabled() || s.Plugins.Runtime == nil {
		return nil, errors.New("os plugins estão desligados neste servidor (HANKO_PLUGINS_DIR não definido)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := s.read().GetPluginByKey(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("o plugin %q do provider não está instalado", id)
	}
	p := pluginFromRow(r)
	if !p.Status.Active() || !p.Has(domain.CapEnvProvider) {
		return nil, fmt.Errorf("o plugin %q não está ativo com a capacidade %s", id, domain.CapEnvProvider)
	}
	prov, ok := p.Manifest.EnvProvider(key)
	if !ok {
		return nil, fmt.Errorf("o plugin %q não declara o provider %q", id, key)
	}
	return &pluginCloudDriver{s: s, plugin: p.Key, key: key, name: provider, knownHosts: prov.KnownHosts}, nil
}

// checkCloudProvider confere, ao gravar ou testar um ambiente cloud, que o provider pode ser usado: o daytona só
// com o driver do operador; o de plugin só com o plugin ativo, a capacidade concedida e o plugin ligado na empresa.
func (s *Service) checkCloudProvider(ctx context.Context, companyID domain.ID, c *domain.CloudConfig) error {
	if c == nil {
		return nil
	}
	pid, key, isPlugin := domain.ParsePluginProvider(c.Provider)
	if !isPlugin {
		if s.Environments.Cloud == nil {
			return domain.Invalid("provider", execenv.ErrCloudOff.Error())
		}
		return nil
	}
	p, _, err := s.activePluginFor(ctx, companyID, pid, domain.CapEnvProvider)
	if err != nil {
		return domain.Invalid("provider", fmt.Sprintf("o plugin %q não está ativo, ligado nesta empresa e com a capacidade %s", pid, domain.CapEnvProvider))
	}
	if _, ok := p.Manifest.EnvProvider(key); !ok {
		return domain.Invalid("provider", fmt.Sprintf("o plugin %q não declara o provider %q", pid, key))
	}
	return nil
}

type pluginCloudDriver struct {
	s          *Service
	plugin     string
	key        string
	name       string
	knownHosts string
}

func (d *pluginCloudDriver) Name() string       { return d.name }
func (d *pluginCloudDriver) KnownHosts() string { return d.knownHosts }
func (d *pluginCloudDriver) New(apiKey string) execenv.CloudProvider {
	return &pluginCloudProvider{d: d, apiKey: apiKey}
}

type pluginCloudProvider struct {
	d      *pluginCloudDriver
	apiKey string
}

func (c *pluginCloudProvider) call(ctx context.Context, method string, params map[string]any) (_ json.RawMessage, err error) {
	rtm := c.d.s.Plugins.Runtime
	if rtm == nil {
		return nil, plugin.ErrNotRunning
	}
	ctx, span := pluginSpan(ctx, "env", attribute.String("hanko.plugin", c.d.plugin), attribute.String("hanko.plugin.method", method))
	defer func() { endSpan(span, err) }()
	// a capacidade é conferida a cada chamada, não só ao resolver o driver (a concessão pode cair no meio da run)
	r, gerr := c.d.s.read().GetPluginByKey(ctx, c.d.plugin)
	if gerr != nil {
		return nil, errors.New("plugin não instalado")
	}
	if p := pluginFromRow(r); !p.Status.Active() || !p.Has(domain.CapEnvProvider) {
		return nil, fmt.Errorf("a capacidade %s do plugin %s não está concedida", domain.CapEnvProvider, c.d.plugin)
	}
	params["provider"], params["api_key"] = c.d.key, c.apiKey
	cctx, cancel := context.WithTimeout(ctx, pluginEnvTimeout)
	defer cancel()
	raw, err := rtm.Call(cctx, c.d.plugin, method, params, pluginEnvTimeout)
	if err != nil {
		// a chave de API pode voltar na mensagem de erro do plugin: nunca vai adiante
		return nil, errors.New(strings.ReplaceAll(pluginClip(err.Error(), 400), c.apiKey, "***"))
	}
	return raw, nil
}

func (c *pluginCloudProvider) Ping(ctx context.Context) error {
	_, err := c.call(ctx, "env.ping", map[string]any{})
	return err
}

func (c *pluginCloudProvider) Create(ctx context.Context, spec execenv.CloudSpec) (execenv.CloudSandbox, error) {
	raw, err := c.call(ctx, "env.create", map[string]any{"spec": map[string]any{"name": spec.Name, "labels": spec.Labels, "snapshot": spec.Snapshot,
		"cpus": spec.CPUs, "memory_gib": spec.MemoryGiB, "disk_gib": spec.DiskGiB, "block_network": spec.BlockNetwork,
		"idle_stop_sec": int(spec.IdleStop / time.Second), "reap_sec": int(spec.Reap / time.Second)}})
	if err != nil {
		return execenv.CloudSandbox{}, err
	}
	var res struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || res.ID == "" || len(res.ID) > 200 || strings.ContainsAny(res.ID, " \t\r\n\x00") {
		return execenv.CloudSandbox{}, errors.New("o provider do plugin devolveu um id de sandbox inválido")
	}
	return execenv.CloudSandbox{ID: res.ID}, nil
}

func (c *pluginCloudProvider) SSH(ctx context.Context, id string, ttl time.Duration) (execenv.CloudSSH, error) {
	raw, err := c.call(ctx, "env.ssh", map[string]any{"id": id, "ttl_sec": int(ttl / time.Second)})
	if err != nil {
		return execenv.CloudSSH{}, err
	}
	var res struct {
		Host string `json:"host"`
		Port int    `json:"port"`
		User string `json:"user"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || res.Host == "" || res.User == "" {
		return execenv.CloudSSH{}, errors.New("o provider do plugin devolveu um acesso SSH inválido")
	}
	if res.Port == 0 {
		res.Port = domain.DefaultSSHPort
	}
	return execenv.CloudSSH{Host: res.Host, Port: res.Port, User: res.User}, nil
}

func (c *pluginCloudProvider) Delete(ctx context.Context, id string) error {
	_, err := c.call(ctx, "env.delete", map[string]any{"id": id})
	return err
}

// ---------- fonte de skills de plugin ----------

// PluginSkillSourceView é uma fonte de skills oferecida por um plugin ativo, com a fonte da empresa quando existe.
type PluginSkillSourceView struct {
	PluginKey   string `json:"plugin"`
	Key         string `json:"key"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url"`
	// SourceID é a fonte já criada na empresa (nil quando ainda não foi conectada).
	SourceID *domain.ID `json:"source_id,omitempty"`
}

// PluginSkillSources lista as fontes de skills dos plugins ativos e ligados na empresa (qualquer membro).
func (s *Service) PluginSkillSources(ctx context.Context, companyID domain.ID) ([]PluginSkillSourceView, error) {
	if !s.Plugins.Enabled() {
		return []PluginSkillSourceView{}, nil
	}
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.read().ListActivePluginsForCompany(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list active plugins: %w", err)
	}
	out := []PluginSkillSourceView{}
	for _, r := range rows {
		p := pluginFromRow(r)
		if !p.Has(domain.CapSkillSource) {
			continue
		}
		for _, ss := range p.Manifest.SkillSources {
			v := PluginSkillSourceView{PluginKey: p.Key, Key: ss.Key, Title: ss.Title, Description: ss.Description, URL: domain.PluginSkillURL(p.Key, ss.Key)}
			if src, err := s.read().GetSkillSourceByURL(ctx, store.GetSkillSourceByURLParams{CompanyID: companyID, Url: v.URL, TrackingRef: "HEAD"}); err == nil {
				id := src.ID
				v.SourceID = &id
			}
			out = append(out, v)
		}
	}
	return out, nil
}

// pluginSourceInput valida a URL plugin://<id>/<key> de uma fonte: plugin ativo, com skills.source, ligado na
// empresa e com a fonte declarada. A ref é sempre HEAD e não há prefixo nem exclusões (o plugin devolve o que quer).
func (s *Service) pluginSourceInput(ctx context.Context, companyID domain.ID, in SkillSourceInput) (SkillSourceInput, error) {
	pid, key, ok := domain.ParsePluginSkillURL(strings.TrimSpace(in.URL))
	if !ok {
		return in, domain.Invalid("url", "use plugin://<plugin>/<fonte>")
	}
	p, _, err := s.activePluginFor(ctx, companyID, pid, domain.CapSkillSource)
	if err != nil {
		return in, domain.Invalid("url", fmt.Sprintf("o plugin %q não está ativo, ligado nesta empresa e com a capacidade %s", pid, domain.CapSkillSource))
	}
	if _, ok := p.Manifest.SkillSource(key); !ok {
		return in, domain.Invalid("url", fmt.Sprintf("o plugin %q não declara a fonte %q", pid, key))
	}
	excl, err := cleanExcluded(in.Excluded)
	if err != nil {
		return in, err
	}
	prefix, err := skillgit.CleanPath(in.PathPrefix)
	if err != nil {
		return in, domain.Invalid("path_prefix", err.Error())
	}
	return SkillSourceInput{URL: domain.PluginSkillURL(pid, key), Ref: "HEAD", PathPrefix: prefix, Excluded: excl}, nil
}

// pluginSkillSnapshot pede ao plugin o retrato da fonte (skills.snapshot) e o converte no mesmo formato do clone
// git, para a sincronização (revisões, seleção, source_modified) ser uma só. Confere a cada sincronização que o
// plugin segue ativo, ligado na empresa e com a capacidade concedida.
func (s *Service) pluginSkillSnapshot(ctx context.Context, src store.SkillSource) (_ skillgit.Snapshot, err error) {
	pid, key, _ := domain.ParsePluginSkillURL(src.Url)
	ctx, span := pluginSpan(ctx, "skills_snapshot", attribute.String("hanko.plugin", pid), attribute.String("hanko.plugin.source", key))
	defer func() { endSpan(span, err) }()
	p, co, err := s.activePluginFor(ctx, src.CompanyID, pid, domain.CapSkillSource)
	if err != nil {
		return skillgit.Snapshot{}, fmt.Errorf("o plugin %q não está ativo, ligado nesta empresa e com a capacidade %s", pid, domain.CapSkillSource)
	}
	if _, ok := p.Manifest.SkillSource(key); !ok {
		return skillgit.Snapshot{}, fmt.Errorf("o plugin %q não declara a fonte %q", pid, key)
	}
	if s.Plugins.Runtime == nil {
		return skillgit.Snapshot{}, plugin.ErrNotRunning
	}
	raw, err := s.Plugins.Runtime.Call(ctx, p.Key, "skills.snapshot", map[string]any{"company": refOf(co), "source": key}, pluginSnapTimeout)
	if err != nil {
		return skillgit.Snapshot{}, fmt.Errorf("o plugin não entregou o retrato da fonte: %s", pluginClip(err.Error(), 300))
	}
	var res struct {
		Revision string `json:"revision"`
		Skills   []struct {
			Path    string `json:"path"`
			SkillMD string `json:"skill_md"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return skillgit.Snapshot{}, errors.New("resposta inválida do plugin (esperado {revision, skills: [{path, skill_md}]})")
	}
	snap := skillgit.Snapshot{}
	if len(res.Skills) > pluginSnapMaxSkill {
		res.Skills, snap.Truncated = res.Skills[:pluginSnapMaxSkill], true
	}
	seen := map[string]bool{}
	tree := sha256.New()
	for _, sk := range res.Skills {
		path, perr := skillgit.CleanPath(sk.Path)
		if path == "" && perr == nil {
			path = "."
		}
		f := skillgit.Found{Path: path}
		switch {
		case perr != nil:
			f.Path, f.Err = "invalid/"+hex.EncodeToString([]byte(pluginClip(sk.Path, 40))), "caminho inválido: "+perr.Error()
		case seen[path]:
			continue
		case len(sk.SkillMD) > skillmd.MaxSourceBytes:
			f.Err = fmt.Sprintf("SKILL.md maior que %d KiB", skillmd.MaxSourceBytes>>10)
		default:
			doc, derr := skillmd.Parse(sk.SkillMD)
			if derr != nil {
				f.Err = derr.Error()
			} else {
				f.Raw, f.Name, f.Description = sk.SkillMD, doc.Name, doc.Description
			}
		}
		seen[f.Path] = true
		snap.Skills = append(snap.Skills, f)
		_, _ = tree.Write([]byte(f.Path + "\x00" + f.Raw + "\x00"))
	}
	slices.SortFunc(snap.Skills, func(a, b skillgit.Found) int { return strings.Compare(a.Path, b.Path) })
	snap.Tree = hex.EncodeToString(tree.Sum(nil))
	// last_commit tem de ser 40 hex: revisão do plugin (ou o hash do conteúdo) passada por sha1
	rev := res.Revision
	if rev == "" {
		rev = snap.Tree
	}
	h := sha1.Sum([]byte(rev)) //nolint:gosec // identificador, não segurança
	snap.Commit = hex.EncodeToString(h[:])
	return snap, nil
}
