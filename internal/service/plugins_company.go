package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/plugin"
	"github.com/renatobardi/hanko/internal/store"
)

// Plugins por empresa (ADR-0038): ligar, configurar (campos do config_schema; os secretos cifrados), widgets e
// tools para as personas. Ler é de qualquer membro; ligar e configurar é de admin.

// CompanyPlugin é um plugin instalado visto de uma empresa (sem valores secretos).
type CompanyPlugin struct {
	PluginID     domain.ID                 `json:"plugin_id"`
	Key          string                    `json:"key"`
	Name         string                    `json:"name"`
	Description  string                    `json:"description,omitempty"`
	Author       string                    `json:"author,omitempty"`
	Homepage     string                    `json:"homepage,omitempty"`
	Version      string                    `json:"version"`
	Status       domain.PluginStatus       `json:"status"`
	Capabilities []string                  `json:"capabilities"`
	ConfigSchema domain.PluginConfigSchema `json:"config_schema"`
	Tools        []domain.PluginTool       `json:"tools"`
	Events       []string                  `json:"events"`
	Widgets      []domain.PluginWidget     `json:"widgets"`
	Jobs         []domain.PluginJob        `json:"jobs"`
	// Contribuições do M18 (ADR-0045).
	Pages        []domain.PluginPage        `json:"pages"`
	FlowTriggers []domain.PluginFlowTrigger `json:"flow_triggers"`
	EnvProviders []domain.PluginEnvProvider `json:"environment_providers"`
	SkillSources []domain.PluginSkillSource `json:"skill_sources"`
	// Enabled: a empresa ligou o plugin. Active: além disso o plugin está atendendo (ready ou upgrade pendente).
	Enabled bool `json:"enabled"`
	Active  bool `json:"active"`
	// Config são os valores não secretos; SecretsSet, os nomes dos campos secretos definidos.
	Config     map[string]any        `json:"config"`
	SecretsSet []string              `json:"secrets_set"`
	JobRuns    []domain.PluginJobRun `json:"job_runs,omitempty"`
	UpdatedAt  *time.Time            `json:"updated_at,omitempty"`
}

func companyPluginFrom(p domain.Plugin, st *store.PluginCompanySetting, secretNames []string) CompanyPlugin {
	m := p.Manifest
	cp := CompanyPlugin{PluginID: p.ID, Key: p.Key, Name: m.Name, Description: m.Description, Author: m.Author, Homepage: m.Homepage,
		Version: p.Version, Status: p.Status, Capabilities: p.GrantedCapabilities, ConfigSchema: m.ConfigSchema,
		Tools: emptyIfNil(m.Tools), Events: emptyIfNil(m.Events), Widgets: emptyIfNil(m.Widgets), Jobs: emptyIfNil(m.Jobs),
		Pages: emptyIfNil(m.Pages), FlowTriggers: emptyIfNil(m.FlowTriggers), EnvProviders: emptyIfNil(m.EnvProviders), SkillSources: emptyIfNil(m.SkillSources),
		Config: map[string]any{}, SecretsSet: emptyIfNil(secretNames)}
	if st != nil {
		cp.Enabled = st.Enabled
		_ = json.Unmarshal(st.Config, &cp.Config)
		t := st.UpdatedAt
		cp.UpdatedAt = &t
	}
	cp.Active = cp.Enabled && p.Status.Active()
	return cp
}

func emptyIfNil[T any](in []T) []T {
	if in == nil {
		return []T{}
	}
	return in
}

func pluginAAD(pluginID, companyID domain.ID) string {
	return "plugin:" + pluginID.String() + ":" + companyID.String()
}

func (s *Service) openPluginSecrets(pluginID, companyID domain.ID, enc string) (map[string]string, error) {
	out := map[string]string{}
	if enc == "" {
		return out, nil
	}
	raw, err := s.Integrations.Secrets.Open(enc, pluginAAD(pluginID, companyID))
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("plugin secrets: %w", err)
	}
	return out, nil
}

func (s *Service) sealPluginSecrets(pluginID, companyID domain.ID, v map[string]string) (string, error) {
	if len(v) == 0 {
		return "", nil
	}
	if !s.Integrations.Secrets.Enabled() {
		return "", domain.Invalid("secrets", "HANKO_SECRET_KEY não está configurada neste servidor; sem ela não há como guardar segredos")
	}
	raw, err := json.Marshal(v) //nolint:gosec // serializa os segredos só para cifrar logo abaixo (secretbox)
	if err != nil {
		return "", fmt.Errorf("marshal plugin secrets: %w", err)
	}
	return s.Integrations.Secrets.Seal(raw, pluginAAD(pluginID, companyID))
}

// installedPlugin resolve um plugin instalado (não desinstalado) pelo slug ou id.
func (s *Service) installedPlugin(ctx context.Context, q *store.Queries, ref string) (domain.Plugin, error) {
	r, err := s.getPlugin(ctx, q, ref)
	if err != nil {
		return domain.Plugin{}, err
	}
	if domain.PluginStatus(r.Status) == domain.PluginUninstalled {
		return domain.Plugin{}, notFound("plugin")
	}
	return pluginFromRow(r), nil
}

func (s *Service) secretNames(p domain.Plugin, companyID domain.ID, enc string) []string {
	sec, err := s.openPluginSecrets(p.ID, companyID, enc)
	if err != nil {
		return nil
	}
	var names []string
	for k := range sec {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// ListCompanyPlugins devolve os plugins instalados com o estado na empresa (qualquer membro).
func (s *Service) ListCompanyPlugins(ctx context.Context, companyID domain.ID) ([]CompanyPlugin, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	if !s.Plugins.Enabled() {
		return []CompanyPlugin{}, nil
	}
	q := s.read()
	rows, err := q.ListPlugins(ctx)
	if err != nil {
		return nil, fmt.Errorf("list plugins: %w", err)
	}
	sets, err := q.ListPluginSettingsForCompany(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list plugin settings: %w", err)
	}
	byPlugin := map[domain.ID]store.PluginCompanySetting{}
	for _, st := range sets {
		byPlugin[st.PluginID] = st
	}
	out := []CompanyPlugin{}
	for _, r := range rows {
		p := pluginFromRow(r)
		if p.Status == domain.PluginUninstalled {
			continue
		}
		var stp *store.PluginCompanySetting
		var names []string
		if st, ok := byPlugin[p.ID]; ok {
			stp = &st
			names = s.secretNames(p, companyID, st.SecretEnc)
		}
		out = append(out, companyPluginFrom(p, stp, names))
	}
	return out, nil
}

// GetCompanyPlugin devolve um plugin com o estado na empresa e as últimas execuções de job.
func (s *Service) GetCompanyPlugin(ctx context.Context, companyID domain.ID, ref string) (CompanyPlugin, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return CompanyPlugin{}, err
	}
	if err := s.requirePluginsOn(); err != nil {
		return CompanyPlugin{}, notFound("plugin")
	}
	q := s.read()
	p, err := s.installedPlugin(ctx, q, ref)
	if err != nil {
		return CompanyPlugin{}, err
	}
	var stp *store.PluginCompanySetting
	var names []string
	st, err := q.GetPluginSettings(ctx, store.GetPluginSettingsParams{PluginID: p.ID, CompanyID: companyID})
	switch {
	case err == nil:
		stp = &st
		names = s.secretNames(p, companyID, st.SecretEnc)
	case !isNoRows(err):
		return CompanyPlugin{}, fmt.Errorf("get plugin settings: %w", err)
	}
	cp := companyPluginFrom(p, stp, names)
	runs, err := q.ListPluginJobRuns(ctx, store.ListPluginJobRunsParams{PluginID: p.ID, CompanyID: companyID, Lim: 20})
	if err == nil {
		for _, r := range runs {
			cp.JobRuns = append(cp.JobRuns, jobRunFromRow(r))
		}
	}
	return cp, nil
}

func jobRunFromRow(r store.PluginJobRun) domain.PluginJobRun {
	return domain.PluginJobRun{ID: r.ID, CompanyID: r.CompanyID, JobKey: r.JobKey, Status: r.Status, StartedAt: r.StartedAt,
		FinishedAt: r.FinishedAt, Error: r.Error}
}

// UpdateCompanyPluginInput liga/desliga e configura o plugin na empresa.
type UpdateCompanyPluginInput struct {
	Enabled *bool
	// Config define campos do config_schema (valores dos campos secretos inclusive). Um campo não secreto com
	// nil ou "" volta ao padrão; um secreto com "" fica como está (use ClearSecrets para apagar).
	Config map[string]any
	// ClearSecrets apaga os campos secretos nomeados.
	ClearSecrets []string
}

// UpdateCompanyPlugin grava a configuração e o estado do plugin na empresa (admin). A configuração é validada
// pelo config_schema e, se o plugin está ativo, pelo próprio plugin (validate_config) antes de ser gravada.
func (s *Service) UpdateCompanyPlugin(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string, in UpdateCompanyPluginInput) (CompanyPlugin, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return CompanyPlugin{}, err
	}
	if err := s.requirePluginsOn(); err != nil {
		return CompanyPlugin{}, err
	}
	q := s.read()
	p, err := s.installedPlugin(ctx, q, ref)
	if err != nil {
		return CompanyPlugin{}, err
	}
	co, err := q.GetCompanyByID(ctx, companyID)
	if err != nil {
		return CompanyPlugin{}, mapErr(err, "company")
	}
	schema := p.Manifest.ConfigSchema
	var prev *store.PluginCompanySetting
	if st, err := q.GetPluginSettings(ctx, store.GetPluginSettingsParams{PluginID: p.ID, CompanyID: companyID}); err == nil {
		prev = &st
	} else if !isNoRows(err) {
		return CompanyPlugin{}, fmt.Errorf("get plugin settings: %w", err)
	}
	cfg := map[string]any{}
	secrets := map[string]string{}
	enabled := false
	if prev != nil {
		enabled = prev.Enabled
		_ = json.Unmarshal(prev.Config, &cfg)
		if secrets, err = s.openPluginSecrets(p.ID, companyID, prev.SecretEnc); err != nil {
			return CompanyPlugin{}, domain.Invalid("secrets", "os segredos guardados não abrem com a chave atual; defina-os de novo")
		}
	}
	changed, secretChanged, err := applyPluginConfig(schema, cfg, secrets, in)
	if err != nil {
		return CompanyPlugin{}, err
	}
	if err := s.checkEnvRefs(ctx, companyID, secrets); err != nil {
		return CompanyPlugin{}, err
	}
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	if enabled {
		if missing := missingRequired(schema, cfg, secrets); len(missing) > 0 {
			return CompanyPlugin{}, domain.Invalid("config", "preencha antes de ligar: "+strings.Join(missing, ", "))
		}
	}
	toggled := prev == nil && enabled || prev != nil && prev.Enabled != enabled
	if enabled && (len(changed)+len(secretChanged) > 0 || toggled) && p.Status.Active() {
		if err := s.validatePluginConfig(ctx, p, co, fullPluginConfig(schema, cfg, secrets)); err != nil {
			return CompanyPlugin{}, err
		}
	}
	cfgRaw, err := json.Marshal(cfg)
	if err != nil {
		return CompanyPlugin{}, fmt.Errorf("marshal plugin config: %w", err)
	}
	enc, err := s.sealPluginSecrets(p.ID, companyID, secrets)
	if err != nil {
		return CompanyPlugin{}, err
	}
	updatedBy, _ := operatorOf(ctx)
	err = s.withTx(ctx, func(t *txc) error {
		if _, err := t.q.UpsertPluginSettings(t.ctx, store.UpsertPluginSettingsParams{PluginID: p.ID, CompanyID: companyID, Enabled: enabled,
			Config: cfgRaw, SecretEnc: enc, UpdatedBy: updatedBy, Now: t.now}); err != nil {
			return fmt.Errorf("save plugin settings: %w", err)
		}
		data := map[string]any{"plugin": p.Key, "version": p.Version}
		if toggled {
			verb := "plugin.disabled"
			if enabled {
				verb = "plugin.enabled"
			}
			if err := t.activity(actor, companyID, verb, "plugin", p.ID, data, ""); err != nil {
				return err
			}
		}
		if len(changed)+len(secretChanged) > 0 {
			// só os nomes dos campos: nem os valores secretos nem os outros entram na atividade
			if err := t.activity(actor, companyID, "plugin.configured", "plugin", p.ID,
				map[string]any{"plugin": p.Key, "fields": changed, "secrets": secretChanged}, ""); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return CompanyPlugin{}, err
	}
	s.dropWidgetCache(p.ID)
	s.dropSecretsCache(p.ID)
	if toggled {
		s.ReconcilePlugins(ctx)
	}
	return s.GetCompanyPlugin(ctx, companyID, p.Key)
}

// applyPluginConfig aplica in sobre cfg/secrets e devolve os campos mudados.
func applyPluginConfig(schema domain.PluginConfigSchema, cfg map[string]any, secrets map[string]string, in UpdateCompanyPluginInput) (changed, secretChanged []string, err error) {
	keys := make([]string, 0, len(in.Config))
	for k := range in.Config {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := in.Config[k]
		f, ok := schema.Properties[k]
		if !ok {
			return nil, nil, domain.Invalid("config."+k, "campo desconhecido")
		}
		if f.Secret {
			sv, ok := v.(string)
			if v != nil && !ok {
				return nil, nil, domain.Invalid("config."+k, "esperado texto")
			}
			if sv == "" {
				continue
			}
			if len(sv) > domain.PluginConfigValueMax || !utf8.ValidString(sv) {
				return nil, nil, domain.Invalid("config."+k, "valor inválido ou grande demais")
			}
			if secrets[k] != sv {
				secrets[k] = sv
				secretChanged = append(secretChanged, k)
			}
			continue
		}
		if v == nil || v == "" {
			if _, had := cfg[k]; had {
				delete(cfg, k)
				changed = append(changed, k)
			}
			continue
		}
		cv, err := f.Coerce(v)
		if err != nil {
			return nil, nil, domain.Invalid("config."+k, err.Error())
		}
		if old, had := cfg[k]; !had || fmt.Sprint(old) != fmt.Sprint(cv) {
			cfg[k] = cv
			changed = append(changed, k)
		}
	}
	for _, k := range in.ClearSecrets {
		f, ok := schema.Properties[k]
		if !ok || !f.Secret {
			return nil, nil, domain.Invalid("clear_secrets", fmt.Sprintf("%q não é um campo secreto", k))
		}
		if _, had := secrets[k]; had {
			delete(secrets, k)
			if !slices.Contains(secretChanged, k) {
				secretChanged = append(secretChanged, k)
			}
		}
	}
	// campos que saíram do schema numa atualização não ficam para trás
	for k := range cfg {
		if f, ok := schema.Properties[k]; !ok || f.Secret {
			delete(cfg, k)
		}
	}
	for k := range secrets {
		if f, ok := schema.Properties[k]; !ok || !f.Secret {
			delete(secrets, k)
		}
	}
	return changed, secretChanged, nil
}

func missingRequired(schema domain.PluginConfigSchema, cfg map[string]any, secrets map[string]string) []string {
	var out []string
	for _, k := range schema.Required {
		f := schema.Properties[k]
		if f.Secret {
			if secrets[k] == "" {
				out = append(out, k)
			}
			continue
		}
		if _, ok := cfg[k]; !ok && f.Default == nil {
			out = append(out, k)
		}
	}
	return out
}

// fullPluginConfig é o que o plugin recebe: padrões, valores e segredos.
func fullPluginConfig(schema domain.PluginConfigSchema, cfg map[string]any, secrets map[string]string) map[string]any {
	out := map[string]any{}
	for k, f := range schema.Properties {
		if f.Default != nil {
			out[k] = f.Default
		}
	}
	for k, v := range cfg {
		out[k] = v
	}
	for k, v := range secrets {
		out[k] = v
	}
	return out
}

// companyRef é a empresa como o plugin a vê.
type companyRef struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

func refOf(c store.Company) companyRef {
	return companyRef{ID: c.ID.String(), Slug: c.Slug, Name: c.Name}
}

// validatePluginConfig pede ao plugin que confira a configuração. O processo é subido se preciso (a empresa
// pode ser a primeira a ligar); se não ficar pronto, a gravação é recusada.
func (s *Service) validatePluginConfig(ctx context.Context, p domain.Plugin, co store.Company, cfg map[string]any) error {
	rtm := s.Plugins.Runtime
	if rtm == nil {
		return nil
	}
	if !rtm.Status(p.Key).Ready {
		spec, err := s.pluginSpec(p)
		if err != nil {
			return domain.Invalid("plugin", err.Error())
		}
		// a trava do ciclo de vida impede que um reconciliar no meio pare o processo de prova
		rt := s.prt()
		rt.life.Lock()
		rtm.Start(ctx, spec)
		wctx, cancel := context.WithTimeout(ctx, pluginProbeTimeout)
		err = rtm.Ready(wctx, p.Key)
		cancel()
		rt.life.Unlock()
		if err != nil {
			go s.ReconcilePlugins(context.WithoutCancel(ctx)) // ninguém ligado: o processo de prova não fica de pé
			return fmt.Errorf("o plugin não respondeu para validar a configuração (%s): %w", err.Error(), domain.ErrConflict)
		}
	}
	raw, err := rtm.Call(ctx, p.Key, "validate_config", map[string]any{"company": refOf(co), "config": cfg}, pluginToolTimeout)
	if err != nil {
		var re *plugin.RPCError
		if errors.As(err, &re) && re.Code == plugin.CodeMethodNotFound {
			return nil // o plugin não valida; o schema já validou
		}
		if errors.As(err, &re) {
			return domain.Invalid("config", "o plugin recusou: "+pluginClip(re.Message, 500))
		}
		return fmt.Errorf("validar configuração no plugin: %w", err)
	}
	var vr struct {
		OK     *bool             `json:"ok"`
		Error  string            `json:"error"`
		Errors map[string]string `json:"errors"`
	}
	if json.Unmarshal(raw, &vr) == nil && vr.OK != nil && !*vr.OK {
		msg := vr.Error
		for k, v := range vr.Errors {
			msg = strings.TrimSpace(msg + " " + k + ": " + v)
		}
		if msg == "" {
			msg = "configuração inválida"
		}
		return domain.Invalid("config", "o plugin recusou: "+pluginClip(msg, 500))
	}
	return nil
}

// pluginConfigFor devolve a configuração completa (com segredos) do plugin na empresa, para o próprio plugin.
func (s *Service) pluginConfigFor(ctx context.Context, p domain.Plugin, companyID domain.ID) (map[string]any, error) {
	st, err := s.read().GetPluginSettings(ctx, store.GetPluginSettingsParams{PluginID: p.ID, CompanyID: companyID})
	if err != nil {
		return nil, mapErr(err, "plugin settings")
	}
	cfg := map[string]any{}
	_ = json.Unmarshal(st.Config, &cfg)
	sec, err := s.openPluginSecrets(p.ID, companyID, st.SecretEnc)
	if err != nil {
		sec = map[string]string{} // segredo ilegível (chave trocada): o plugin recebe sem ele
	}
	// ${secret:X} num campo secreto do plugin (ADR-0050): resolvido só aqui, quando o plugin recebe a configuração
	if sec, _, err = s.expandStringMap(ctx, companyID, sec); err != nil {
		return nil, fmt.Errorf("o plugin %q referencia um segredo indisponível: %w", p.Key, err)
	}
	return fullPluginConfig(p.Manifest.ConfigSchema, cfg, sec), nil
}

// ListPluginState lista as chaves de estado do plugin na empresa (admin).
func (s *Service) ListPluginState(ctx context.Context, companyID domain.ID, ref, prefix string, limit int) ([]PluginStateItem, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return nil, err
	}
	p, err := s.installedPlugin(ctx, s.read(), ref)
	if err != nil {
		return nil, err
	}
	return s.listState(ctx, p.ID, companyID, prefix, limit)
}

// PluginStateItem é uma chave de estado de plugin.
type PluginStateItem struct {
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	UpdatedAt time.Time       `json:"updated_at"`
}

func (s *Service) listState(ctx context.Context, pluginID, companyID domain.ID, prefix string, limit int) ([]PluginStateItem, error) {
	rows, err := s.read().ListPluginState(ctx, store.ListPluginStateParams{PluginID: pluginID, CompanyID: companyID,
		Prefix: escapeLike(prefix), Lim: i32(clamp(limit, 100, domain.PluginStateMaxKeys))})
	if err != nil {
		return nil, fmt.Errorf("list plugin state: %w", err)
	}
	out := make([]PluginStateItem, len(rows))
	for i, r := range rows {
		out[i] = PluginStateItem{Key: r.Key, Value: json.RawMessage(r.Value), UpdatedAt: r.UpdatedAt}
	}
	return out, nil
}

// ---------- widgets ----------

// PluginWidgetRef é onde o widget aparece: o Painel, uma task ou uma persona.
type PluginWidgetRef struct {
	Placement string
	// Ref é a chave da task (HNK-12) ou o slug/id da persona; vazio no Painel.
	Ref string
}

// PluginTable é uma tabela simples devolvida por um widget.
type PluginTable struct {
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
}

// PluginWidgetView é um widget já resolvido: markdown (renderizado pelo Hanko, sanitizado) e/ou tabela.
type PluginWidgetView struct {
	PluginKey  string       `json:"plugin"`
	PluginName string       `json:"plugin_name"`
	Key        string       `json:"key"`
	Title      string       `json:"title"`
	Markdown   string       `json:"markdown,omitempty"`
	Table      *PluginTable `json:"table,omitempty"`
	Error      string       `json:"error,omitempty"`
}

type widgetEntry struct {
	at time.Time
	v  PluginWidgetView
}

const (
	widgetMarkdownMax = 32 << 10
	widgetMaxColumns  = 20
	widgetMaxRows     = 200
	widgetCellMax     = 500
	widgetCacheMax    = 2000
)

// PluginWidgets devolve os widgets dos plugins ligados para o lugar pedido (qualquer membro). Cada widget é
// pedido ao plugin com timeout curto e fica 60 s em cache; um plugin lento ou fora do ar vira um aviso no bloco,
// nunca uma página quebrada.
func (s *Service) PluginWidgets(ctx context.Context, companyID domain.ID, w PluginWidgetRef) ([]PluginWidgetView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	if !domain.ValidWidgetPlacement(w.Placement) {
		return nil, domain.Invalid("placement", "use dashboard, task ou persona")
	}
	if !s.Plugins.Enabled() {
		return []PluginWidgetView{}, nil
	}
	q := s.read()
	co, err := q.GetCompanyByID(ctx, companyID)
	if err != nil {
		return nil, mapErr(err, "company")
	}
	wctx := map[string]any{"type": w.Placement}
	switch w.Placement {
	case domain.WidgetTask:
		t, err := s.GetTask(ctx, companyID, w.Ref)
		if err != nil {
			return nil, err
		}
		wctx["id"], wctx["key"] = t.ID.String(), t.Key
	case domain.WidgetPersona:
		p, err := s.GetPersona(ctx, companyID, w.Ref)
		if err != nil {
			return nil, err
		}
		wctx["id"], wctx["slug"] = p.ID.String(), p.Slug
	}
	rows, err := q.ListActivePluginsForCompany(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list active plugins: %w", err)
	}
	out := []PluginWidgetView{}
	for _, r := range rows {
		p := pluginFromRow(r)
		if !p.Has(domain.CapWidgets) {
			continue
		}
		for _, wd := range p.Manifest.Widgets {
			if wd.Placement != w.Placement {
				continue
			}
			out = append(out, s.widget(ctx, p, co, wd, wctx))
		}
	}
	return out, nil
}

func (s *Service) widget(ctx context.Context, p domain.Plugin, co store.Company, wd domain.PluginWidget, wctx map[string]any) PluginWidgetView {
	ck := fmt.Sprintf("%s|%s|%s|%v|%v", p.ID, co.ID, wd.Key, wctx["type"], wctx["id"])
	rt := s.prt()
	now := s.Now()
	rt.mu.Lock()
	if e, ok := rt.widgets[ck]; ok && now.Sub(e.at) < pluginWidgetTTL {
		rt.mu.Unlock()
		return e.v
	}
	rt.mu.Unlock()
	v := PluginWidgetView{PluginKey: p.Key, PluginName: p.Manifest.Name, Key: wd.Key, Title: wd.Title}
	if s.Plugins.Runtime == nil {
		v.Error = "plugin fora do ar"
		return v
	}
	raw, err := s.Plugins.Runtime.Call(ctx, p.Key, "get_widget", map[string]any{"company": refOf(co), "key": wd.Key, "context": wctx}, pluginWidgetTimeout)
	if err != nil {
		v.Error = "o plugin não respondeu"
		if errors.Is(err, plugin.ErrNotRunning) {
			v.Error = "plugin fora do ar"
		}
		return v // erro não entra no cache: a próxima página tenta de novo
	}
	var res struct {
		Title    string          `json:"title"`
		Markdown string          `json:"markdown"`
		Table    *PluginTable    `json:"table"`
		Data     json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		v.Error = "resposta inválida do plugin"
		return v
	}
	if t := strings.TrimSpace(res.Title); t != "" && utf8.RuneCountInString(t) <= 120 {
		v.Title = t
	}
	v.Markdown = pluginClip(adapter.Redact(res.Markdown), widgetMarkdownMax)
	if res.Table != nil {
		v.Table = clampTable(*res.Table)
	}
	rt.mu.Lock()
	if len(rt.widgets) >= widgetCacheMax {
		rt.widgets = map[string]widgetEntry{}
	}
	rt.widgets[ck] = widgetEntry{at: now, v: v}
	rt.mu.Unlock()
	return v
}

func clampTable(t PluginTable) *PluginTable {
	cell := func(s string) string { return pluginClip(adapter.Redact(s), widgetCellMax) }
	out := &PluginTable{}
	for i, c := range t.Columns {
		if i >= widgetMaxColumns {
			break
		}
		out.Columns = append(out.Columns, cell(c))
	}
	for i, r := range t.Rows {
		if i >= widgetMaxRows {
			break
		}
		row := make([]string, 0, len(out.Columns))
		for j := 0; j < len(out.Columns); j++ {
			v := ""
			if j < len(r) {
				v = cell(r[j])
			}
			row = append(row, v)
		}
		out.Rows = append(out.Rows, row)
	}
	return out
}

func (s *Service) dropWidgetCache(pluginID domain.ID) {
	rt := s.prt()
	prefix := pluginID.String() + "|"
	rt.mu.Lock()
	defer rt.mu.Unlock()
	for k := range rt.widgets {
		if strings.HasPrefix(k, prefix) {
			delete(rt.widgets, k)
		}
	}
}

// ---------- tools para as personas ----------

// PluginToolView é uma tool de plugin exposta no MCP como plugin_<id>_<tool>.
type PluginToolView struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	PluginKey   string          `json:"plugin"`
	Version     string          `json:"version"`
}

// PluginTools lista as tools dos plugins ativos e ligados na empresa (só quem tem agent.tools).
func (s *Service) PluginTools(ctx context.Context, companyID domain.ID) ([]PluginToolView, error) {
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
	var out []PluginToolView
	for _, r := range rows {
		p := pluginFromRow(r)
		if !p.Has(domain.CapAgentTools) {
			continue
		}
		for _, t := range p.Manifest.Tools {
			out = append(out, PluginToolView{Name: domain.PluginToolName(p.Key, t.Name), PluginKey: p.Key, Version: p.Version,
				Description: "[plugin " + p.Manifest.Name + "] " + t.Description, InputSchema: t.InputSchema})
		}
	}
	return out, nil
}

// PluginToolCall é uma chamada de tool de plugin feita por uma persona.
type PluginToolCall struct {
	Name      string
	Arguments json.RawMessage
	PersonaID *domain.ID
	RunID     *domain.ID
}

// PluginToolResult é o que a tool devolveu.
type PluginToolResult struct {
	Text    string          `json:"text"`
	Data    json.RawMessage `json:"data,omitempty"`
	IsError bool            `json:"is_error,omitempty"`
}

const pluginToolTextMax = 256 << 10

// CallPluginTool encaminha a chamada ao execute_tool do plugin, com o contexto da run. Erro ou timeout viram
// erro da tool; cada chamada fica na atividade (plugin.tool_called).
func (s *Service) CallPluginTool(ctx context.Context, actor domain.Actor, companyID domain.ID, in PluginToolCall) (PluginToolResult, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return PluginToolResult{}, err
	}
	key, tool, ok := domain.ParsePluginToolName(in.Name)
	if !ok || !s.Plugins.Enabled() {
		return PluginToolResult{}, notFound("tool")
	}
	q := s.read()
	r, err := q.GetPluginByKey(ctx, key)
	if err != nil {
		return PluginToolResult{}, mapErr(err, "tool")
	}
	p := pluginFromRow(r)
	t, ok := p.Manifest.Tool(tool)
	if !ok || !p.Status.Active() || !p.Has(domain.CapAgentTools) {
		return PluginToolResult{}, notFound("tool")
	}
	st, err := q.GetPluginSettings(ctx, store.GetPluginSettingsParams{PluginID: p.ID, CompanyID: companyID})
	if err != nil || !st.Enabled {
		return PluginToolResult{}, notFound("tool")
	}
	if blocked := s.pluginToolPolicy(ctx, companyID, in); blocked != nil {
		return *blocked, nil
	}
	co, err := q.GetCompanyByID(ctx, companyID)
	if err != nil {
		return PluginToolResult{}, mapErr(err, "company")
	}
	args := in.Arguments
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage(`{}`)
	}
	params := map[string]any{"company": refOf(co), "tool": tool, "arguments": args}
	var persona map[string]string
	if in.PersonaID != nil {
		if pr, err := q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: *in.PersonaID}); err == nil {
			persona = map[string]string{"id": pr.ID.String(), "slug": pr.Slug, "name": pr.Name}
			params["persona"] = persona
		}
	}
	if in.RunID != nil {
		params["run_id"] = in.RunID.String()
	}
	timeout := pluginToolTimeout
	if t.TimeoutSec > 0 {
		timeout = time.Duration(t.TimeoutSec) * time.Second
	}
	start := time.Now()
	var res PluginToolResult
	var callErr error
	if s.Plugins.Runtime == nil {
		callErr = plugin.ErrNotRunning
	} else {
		raw, err := s.Plugins.Runtime.Call(ctx, p.Key, "execute_tool", params, timeout)
		switch {
		case err != nil:
			callErr = err
		case json.Unmarshal(raw, &res) != nil:
			callErr = errors.New("resposta inválida do plugin")
		}
	}
	res.Text = pluginClip(res.Text, pluginToolTextMax)
	if len(res.Data) > pluginToolTextMax {
		res.Data = nil
	}
	ok = callErr == nil && !res.IsError
	data := map[string]any{"plugin": p.Key, "tool": tool, "ok": ok, "duration_ms": time.Since(start).Milliseconds()}
	if persona != nil {
		data["persona"] = persona["slug"]
	}
	if in.RunID != nil {
		data["run_id"] = in.RunID.String()
	}
	if callErr != nil {
		data["error"] = pluginClip(callErr.Error(), 300)
	}
	if err := s.withTx(ctx, func(t *txc) error {
		return t.activity(actor, companyID, "plugin.tool_called", "plugin", p.ID, data, "")
	}); err != nil && s.Log != nil {
		s.Log.Warn("plugin: registrar chamada de tool", "plugin", p.Key, "err", err)
	}
	if callErr != nil {
		var re *plugin.RPCError
		if errors.As(callErr, &re) {
			return PluginToolResult{}, fmt.Errorf("a tool %s falhou: %s", in.Name, pluginClip(re.Message, 500))
		}
		if errors.Is(callErr, plugin.ErrNotRunning) {
			return PluginToolResult{}, fmt.Errorf("a tool %s está indisponível: o plugin %s não está rodando", in.Name, p.Key)
		}
		return PluginToolResult{}, fmt.Errorf("a tool %s falhou: %s", in.Name, pluginClip(callErr.Error(), 500))
	}
	return res, nil
}

// pluginToolPolicy aplica o motor de política (ADR-0053) à chamada de uma tool de plugin feita por uma run: política off
// recusa; ask abre o gate e a persona repete a chamada depois da decisão (como nas tools de integração). nil = segue.
func (s *Service) pluginToolPolicy(ctx context.Context, companyID domain.ID, in PluginToolCall) *PluginToolResult {
	if in.RunID == nil || in.PersonaID == nil {
		return nil
	}
	c, err := s.RunToolCaller(ctx, companyID, *in.RunID)
	if err != nil {
		return nil // run de sistema ou encerrada: o servidor MCP já exigiu o run token
	}
	c.Source = domain.SourcePlugin
	d, err := s.NativeToolCheck(ctx, c, NativeToolRequest{Name: in.Name, Input: in.Arguments, Plugin: true})
	if err != nil {
		return &PluginToolResult{IsError: true, Text: "não foi possível aplicar a política de tools: " + err.Error()}
	}
	switch d.Decision {
	case NativeDeny:
		return &PluginToolResult{IsError: true, Text: d.Reason}
	case NativeAsk:
		return &PluginToolResult{IsError: true, Text: d.Reason + "; repita a chamada com exatamente os mesmos argumentos depois da decisão"}
	}
	return nil
}
