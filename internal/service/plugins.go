package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/plugin"
	"github.com/renatobardi/hanko/internal/store"
)

// Plugins de terceiros (ADR-0038). Instalar, atualizar, aprovar capacidades, desligar na instalação e
// desinstalar são do dono da instalação; ligar, desligar e configurar por empresa são do admin dela. O processo
// do plugin é do gerenciador (internal/plugin); este arquivo decide quando ele deve estar de pé.

// PluginRuntime é o gerenciador de processos (plugin.Manager); nos testes pode ser um fake.
type PluginRuntime interface {
	Start(ctx context.Context, s plugin.Spec)
	Restart(ctx context.Context, s plugin.Spec)
	Stop(ctx context.Context, key string) error
	Ready(ctx context.Context, key string) error
	Status(key string) plugin.Status
	Call(ctx context.Context, key, method string, params any, timeout time.Duration) (json.RawMessage, error)
	Running() []string
}

// PluginsConfig é a política do operador para plugins. O zero value desliga a funcionalidade inteira.
type PluginsConfig struct {
	// Dir é HANKO_PLUGINS_DIR: o diretório local onde o operador coloca os plugins (um subdiretório por plugin,
	// com o mesmo nome do id do manifesto). Vazio = plugins desligados. O Hanko nunca baixa plugin.
	Dir string
	// DataDir é a raiz dos diretórios de trabalho: cada plugin roda em <DataDir>/plugins/<id>.
	DataDir string
	// Runtime sobe e conversa com os processos; nil = nenhum processo (as chamadas falham como "não está rodando").
	Runtime PluginRuntime

	rt *pluginRT
}

// Enabled informa se a funcionalidade está ligada.
func (c PluginsConfig) Enabled() bool { return c.Dir != "" }

// PluginsEnabled informa se o operador ligou os plugins de terceiros (HANKO_PLUGINS_DIR).
func (s *Service) PluginsEnabled() bool { return s.Plugins.Enabled() }

const (
	pluginProbeTimeout  = 45 * time.Second
	pluginToolTimeout   = 30 * time.Second
	pluginJobTimeout    = 5 * time.Minute
	pluginEventTimeout  = 30 * time.Second
	pluginWidgetTimeout = 3 * time.Second
	pluginWidgetTTL     = time.Minute
	pluginMaxDirs       = 200
	// pluginCallsPerMinute é o teto de chamadas de um plugin ao host por minuto (todas as empresas).
	pluginCallsPerMinute = 600
	// pluginLogsPerMinute é o teto de linhas de log (stderr e log) gravadas por minuto, por plugin.
	pluginLogsPerMinute = 120
)

// pluginRT é o estado em memória da funcionalidade (uma instância do servidor, ADR-0008).
type pluginRT struct {
	life sync.Mutex // serializa instalar/atualizar/desinstalar/reconciliar

	mu      sync.Mutex
	widgets map[string]widgetEntry
	calls   map[string]*window
	logs    map[string]*window
	secrets map[domain.ID][]string
}

type window struct {
	start   time.Time
	n       int
	dropped int
}

// allow conta uma ocorrência na janela de um minuto; false acima de max.
func (w *window) allow(now time.Time, max int) (ok bool, dropped int) {
	if now.Sub(w.start) >= time.Minute {
		dropped = w.dropped
		w.start, w.n, w.dropped = now, 0, 0
	}
	if w.n >= max {
		w.dropped++
		return false, 0
	}
	w.n++
	return true, dropped
}

var pluginInitMu sync.Mutex

func (s *Service) prt() *pluginRT {
	pluginInitMu.Lock()
	defer pluginInitMu.Unlock()
	if s.Plugins.rt == nil {
		s.Plugins.rt = &pluginRT{widgets: map[string]widgetEntry{}, calls: map[string]*window{}, logs: map[string]*window{}, secrets: map[domain.ID][]string{}}
	}
	return s.Plugins.rt
}

// ---------- views ----------

// PluginView é uma instalação de plugin para as telas e a API (sem segredos).
type PluginView struct {
	domain.Plugin
	// Companies é quantas empresas ligaram o plugin.
	Companies int `json:"companies"`
	// Process é o estado do processo (vazio quando não roda).
	Process plugin.Status `json:"process"`
}

// AvailablePlugin é um diretório de HANKO_PLUGINS_DIR com manifesto.
type AvailablePlugin struct {
	Key      string                 `json:"key"`
	Dir      string                 `json:"dir"`
	Manifest *domain.PluginManifest `json:"manifest,omitempty"`
	// Error explica por que o diretório não pode ser instalado (manifesto inválido, comando ausente…).
	Error string `json:"error,omitempty"`
	// Installed é a versão instalada, quando há; Upgrade marca versão nova no diretório.
	Installed string `json:"installed,omitempty"`
	Upgrade   bool   `json:"upgrade,omitempty"`
}

// PluginOverview é a tela da instalação: o que está disponível e o que está instalado.
type PluginOverview struct {
	Enabled   bool              `json:"enabled"`
	Dir       string            `json:"dir,omitempty"`
	Available []AvailablePlugin `json:"available"`
	Installed []PluginView      `json:"installed"`
}

func pluginFromRow(r store.Plugin) domain.Plugin {
	p := domain.Plugin{ID: r.ID, Key: r.PluginKey, Version: r.Version, APIVersion: int(r.ApiVersion), Dir: r.Dir,
		Status: domain.PluginStatus(r.Status), GrantedCapabilities: r.GrantedCapabilities, PendingCapabilities: r.PendingCapabilities,
		LastError: r.LastError, InstalledBy: r.InstalledBy, InstalledAt: r.InstalledAt, UpdatedAt: r.UpdatedAt}
	_ = json.Unmarshal(r.Manifest, &p.Manifest)
	if len(r.PendingManifest) > 0 {
		var pm domain.PluginManifest
		if json.Unmarshal(r.PendingManifest, &pm) == nil {
			p.PendingManifest = &pm
		}
	}
	if p.GrantedCapabilities == nil {
		p.GrantedCapabilities = []string{}
	}
	if p.PendingCapabilities == nil {
		p.PendingCapabilities = []string{}
	}
	return p
}

func (s *Service) pluginView(p domain.Plugin, companies int) PluginView {
	v := PluginView{Plugin: p, Companies: companies}
	if s.Plugins.Runtime != nil {
		v.Process = s.Plugins.Runtime.Status(p.Key)
	}
	return v
}

// ---------- autorização ----------

// requirePluginOwner: instalar e operar plugins na instalação é do dono da instalação (papel conferido no
// banco). Sem identidade no contexto (modo trusted, CLI local sem login) vale como operador; as personas não
// chegam aqui (a API recusa run tokens nestas rotas e o MCP não as expõe).
func (s *Service) requirePluginOwner(ctx context.Context) error {
	if _, ok := auth.IdentityFrom(ctx); !ok {
		return nil
	}
	return s.requireInstallOwner(ctx, s.read())
}

func (s *Service) requirePluginsOn() error {
	if !s.Plugins.Enabled() {
		return domain.Invalid("plugins", "plugins estão desligados neste servidor (defina HANKO_PLUGINS_DIR)")
	}
	return nil
}

// ---------- descoberta ----------

// pluginsRoot é a raiz real (links resolvidos) de HANKO_PLUGINS_DIR.
func (s *Service) pluginsRoot() (string, error) {
	root, err := filepath.EvalSymlinks(s.Plugins.Dir)
	if err != nil {
		return "", fmt.Errorf("HANKO_PLUGINS_DIR: %w", err)
	}
	return root, nil
}

// insideRoot confere que dir (já resolvido) está estritamente dentro de root.
func insideRoot(root, dir string) bool {
	return strings.HasPrefix(dir, root+string(filepath.Separator))
}

// readPluginDir lê e valida o manifesto de um diretório de plugin, já resolvido e dentro da raiz.
func readPluginDir(root, name string) (AvailablePlugin, error) {
	a := AvailablePlugin{Key: name}
	dir, err := filepath.EvalSymlinks(filepath.Join(root, name))
	if err != nil {
		return a, fmt.Errorf("diretório ilegível: %w", err)
	}
	a.Dir = dir
	if !insideRoot(root, dir) {
		return a, errors.New("o diretório aponta para fora de HANKO_PLUGINS_DIR")
	}
	f, err := os.Open(filepath.Join(dir, domain.ManifestFile)) //nolint:gosec // caminho dentro de HANKO_PLUGINS_DIR, conferido acima
	if err != nil {
		return a, fmt.Errorf("sem %s", domain.ManifestFile)
	}
	defer f.Close()
	buf := make([]byte, domain.PluginManifestMaxBytes+1)
	n, _ := f.Read(buf)
	m, err := domain.ParsePluginManifest(buf[:n])
	if err != nil {
		return a, err
	}
	if m.ID != name {
		return a, fmt.Errorf("o id do manifesto (%s) precisa ser igual ao nome do diretório (%s)", m.ID, name)
	}
	a.Manifest = &m
	if _, err := plugin.ResolveCommand(dir, m.Command); err != nil {
		return a, err
	}
	return a, nil
}

// discoverPlugins lista os diretórios de HANKO_PLUGINS_DIR (até pluginMaxDirs).
func (s *Service) discoverPlugins() ([]AvailablePlugin, error) {
	root, err := s.pluginsRoot()
	if err != nil {
		return nil, err
	}
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("HANKO_PLUGINS_DIR: %w", err)
	}
	var out []AvailablePlugin
	for _, e := range ents {
		name := e.Name()
		if strings.HasPrefix(name, ".") || (!e.IsDir() && e.Type()&os.ModeSymlink == 0) {
			continue
		}
		if len(out) >= pluginMaxDirs {
			break
		}
		a, err := readPluginDir(root, name)
		if err != nil {
			a.Error = err.Error()
		}
		out = append(out, a)
	}
	return out, nil
}

// ---------- leitura ----------

func (s *Service) pluginCounts(ctx context.Context) (map[domain.ID]int, error) {
	rows, err := s.read().CountPluginEnabledCompanies(ctx)
	if err != nil {
		return nil, fmt.Errorf("count plugin companies: %w", err)
	}
	out := map[domain.ID]int{}
	for _, r := range rows {
		out[r.PluginID] = int(r.Companies)
	}
	return out, nil
}

// PluginOverview devolve os plugins disponíveis no diretório e os instalados (dono da instalação).
func (s *Service) PluginOverview(ctx context.Context) (PluginOverview, error) {
	if err := s.requirePluginOwner(ctx); err != nil {
		return PluginOverview{}, err
	}
	ov := PluginOverview{Enabled: s.Plugins.Enabled(), Dir: s.Plugins.Dir, Available: []AvailablePlugin{}, Installed: []PluginView{}}
	if !ov.Enabled {
		return ov, nil
	}
	rows, err := s.read().ListPlugins(ctx)
	if err != nil {
		return ov, fmt.Errorf("list plugins: %w", err)
	}
	counts, err := s.pluginCounts(ctx)
	if err != nil {
		return ov, err
	}
	installed := map[string]domain.Plugin{}
	for _, r := range rows {
		p := pluginFromRow(r)
		if p.Status == domain.PluginUninstalled {
			continue
		}
		installed[p.Key] = p
		ov.Installed = append(ov.Installed, s.pluginView(p, counts[p.ID]))
	}
	avail, err := s.discoverPlugins()
	if err != nil {
		return ov, domain.Invalid("plugins", err.Error())
	}
	for _, a := range avail {
		if p, ok := installed[a.Key]; ok {
			a.Installed = p.Version
			a.Upgrade = a.Manifest != nil && domain.CompareSemver(a.Manifest.Version, p.Version) > 0 &&
				(p.PendingManifest == nil || p.PendingManifest.Version != a.Manifest.Version)
		}
		ov.Available = append(ov.Available, a)
	}
	return ov, nil
}

// getPlugin resolve um plugin instalado pelo id do manifesto (slug) ou pelo id da linha.
func (s *Service) getPlugin(ctx context.Context, q *store.Queries, ref string) (store.Plugin, error) {
	if id, err := domain.ParseID(ref); err == nil {
		r, err := q.GetPluginByID(ctx, id)
		return r, mapErr(err, "plugin")
	}
	r, err := q.GetPluginByKey(ctx, ref)
	return r, mapErr(err, "plugin")
}

// GetPlugin devolve uma instalação de plugin (dono da instalação).
func (s *Service) GetPlugin(ctx context.Context, ref string) (PluginView, error) {
	if err := s.requirePluginOwner(ctx); err != nil {
		return PluginView{}, err
	}
	r, err := s.getPlugin(ctx, s.read(), ref)
	if err != nil {
		return PluginView{}, err
	}
	counts, err := s.pluginCounts(ctx)
	if err != nil {
		return PluginView{}, err
	}
	return s.pluginView(pluginFromRow(r), counts[r.ID]), nil
}

// ---------- ciclo de vida ----------

func manifestJSON(m domain.PluginManifest) ([]byte, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}
	return b, nil
}

func eventsOf(m domain.PluginManifest) []string {
	if m.Events == nil {
		return []string{}
	}
	return m.Events
}

// InstallPlugin instala o plugin do diretório key (dono da instalação): valida o manifesto, grava `installed`
// com as capacidades pedidas (o dono as aceita ao instalar), sobe o processo, faz initialize + health e fica
// `ready` ou `error`.
func (s *Service) InstallPlugin(ctx context.Context, key string) (PluginView, error) {
	if err := s.requirePluginsOn(); err != nil {
		return PluginView{}, err
	}
	if err := s.requirePluginOwner(ctx); err != nil {
		return PluginView{}, err
	}
	if !domain.ValidPluginID(key) {
		return PluginView{}, domain.Invalid("key", "id de plugin inválido")
	}
	rt := s.prt()
	rt.life.Lock()
	defer rt.life.Unlock()
	root, err := s.pluginsRoot()
	if err != nil {
		return PluginView{}, domain.Invalid("plugins", err.Error())
	}
	a, err := readPluginDir(root, key)
	if err != nil {
		return PluginView{}, domain.Invalid("manifest", err.Error())
	}
	m := *a.Manifest
	raw, err := manifestJSON(m)
	if err != nil {
		return PluginView{}, err
	}
	installer, _ := operatorOf(ctx)
	var row store.Plugin
	err = s.withTx(ctx, func(t *txc) error {
		caps := domain.SortCapabilities(m.Capabilities)
		cur, err := t.q.GetPluginByKey(t.ctx, key)
		switch {
		case isNoRows(err):
			row, err = t.q.InsertPlugin(t.ctx, store.InsertPluginParams{ID: domain.NewID(), PluginKey: key, Version: m.Version,
				ApiVersion: i32(m.APIVersion), Manifest: raw, Dir: a.Dir, GrantedCapabilities: caps, Events: eventsOf(m), InstalledBy: installer, Now: t.now})
			if isUnique(err) {
				return fmt.Errorf("plugin %s: %w", key, domain.ErrConflict)
			}
		case err != nil:
			return fmt.Errorf("get plugin: %w", err)
		case cur.Status != string(domain.PluginUninstalled):
			return fmt.Errorf("plugin %s já está instalado: %w", key, domain.ErrConflict)
		default:
			row, err = t.q.ReinstallPlugin(t.ctx, store.ReinstallPluginParams{ID: cur.ID, Version: m.Version, ApiVersion: i32(m.APIVersion),
				Manifest: raw, Dir: a.Dir, GrantedCapabilities: caps, Events: eventsOf(m), InstalledBy: installer, Now: t.now})
			if isNoRows(err) {
				return fmt.Errorf("plugin %s: %w", key, domain.ErrConflict)
			}
		}
		if err != nil {
			return fmt.Errorf("install plugin: %w", err)
		}
		return s.recordAdmin(t.ctx, t.q, "plugin.install", key+"@"+m.Version, &row.ID, "ok")
	})
	if err != nil {
		return PluginView{}, err
	}
	s.pluginLog(ctx, row.ID, nil, domain.PluginLogHost, "instalado: versão "+m.Version+"; capacidades: "+strings.Join(row.GrantedCapabilities, ", "))
	p := s.probePlugin(ctx, pluginFromRow(row))
	s.reconcileLocked(ctx)
	return s.pluginView(p, 0), nil
}

// probePlugin sobe (ou reinicia) o processo e espera initialize + health; grava ready ou error.
func (s *Service) probePlugin(ctx context.Context, p domain.Plugin) domain.Plugin {
	rtm := s.Plugins.Runtime
	if rtm == nil {
		return s.setPluginStatus(ctx, p, domain.PluginError, "sem gerenciador de processos neste servidor")
	}
	spec, err := s.pluginSpec(p)
	if err != nil {
		return s.setPluginStatus(ctx, p, domain.PluginError, err.Error())
	}
	rtm.Restart(ctx, spec)
	wctx, cancel := context.WithTimeout(ctx, pluginProbeTimeout)
	defer cancel()
	if err := rtm.Ready(wctx, p.Key); err != nil {
		return s.setPluginStatus(ctx, p, domain.PluginError, err.Error())
	}
	return s.setPluginStatus(ctx, p, domain.PluginReady, "")
}

// setPluginStatus grava a transição, se permitida a partir do estado atual no banco. Devolve o plugin como ficou.
func (s *Service) setPluginStatus(ctx context.Context, p domain.Plugin, to domain.PluginStatus, lastErr string) domain.Plugin {
	out := p
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetPluginForUpdate(t.ctx, p.ID)
		if err != nil {
			return err
		}
		from := domain.PluginStatus(cur.Status)
		if from != to && !from.CanBecome(to) {
			if lastErr != "" {
				_ = t.q.SetPluginError(t.ctx, store.SetPluginErrorParams{ID: p.ID, LastError: pluginClip(lastErr, 2000), Now: t.now})
			}
			cur, err = t.q.GetPluginByID(t.ctx, p.ID)
			out = pluginFromRow(cur)
			return err
		}
		row, err := t.q.SetPluginStatus(t.ctx, store.SetPluginStatusParams{ID: p.ID, Status: string(to), LastError: pluginClip(lastErr, 2000), Now: t.now})
		if err != nil {
			return err
		}
		out = pluginFromRow(row)
		return nil
	})
	if err != nil && s.Log != nil {
		s.Log.Warn("plugin: gravar estado", "plugin", p.Key, "err", err)
	}
	return out
}

func pluginClip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8Start(s[n]) {
		n--
	}
	return s[:n]
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

// PluginProcessState é o OnState do gerenciador: o processo ficou pronto, falhou ao subir ou caiu.
func (s *Service) PluginProcessState(key string, ready bool, errText string) {
	ctx := context.Background()
	r, err := s.read().GetPluginByKey(ctx, key)
	if err != nil {
		return
	}
	p := pluginFromRow(r)
	switch {
	case ready && (p.Status == domain.PluginError || p.Status == domain.PluginInstalled):
		s.setPluginStatus(ctx, p, domain.PluginReady, "")
		s.pluginLog(ctx, p.ID, nil, domain.PluginLogHost, "processo pronto")
	case !ready && (p.Status == domain.PluginReady || p.Status == domain.PluginInstalled):
		s.setPluginStatus(ctx, p, domain.PluginError, errText)
	case !ready:
		// upgrade pendente, desligado ou já em erro: só o último erro muda
		_ = store.New(s.Pool).SetPluginError(ctx, store.SetPluginErrorParams{ID: p.ID, LastError: pluginClip(errText, 2000), Now: s.Now().UTC()})
	}
}

// pluginSpec monta como subir o processo: o diretório precisa continuar dentro de HANKO_PLUGINS_DIR.
func (s *Service) pluginSpec(p domain.Plugin) (plugin.Spec, error) {
	root, err := s.pluginsRoot()
	if err != nil {
		return plugin.Spec{}, err
	}
	dir, err := filepath.EvalSymlinks(p.Dir)
	if err != nil || !insideRoot(root, dir) {
		return plugin.Spec{}, errors.New("o diretório do plugin não está mais dentro de HANKO_PLUGINS_DIR")
	}
	work := ""
	if s.Plugins.DataDir != "" {
		work = filepath.Join(s.Plugins.DataDir, "plugins", p.Key)
	}
	return plugin.Spec{Key: p.Key, Version: p.Version, Dir: dir, Command: p.Manifest.Command, Args: p.Manifest.Args, WorkDir: work,
		Env: plugin.Env(os.Environ(), work, p.Has(domain.CapHTTPOutbound))}, nil
}

// SetPluginEnabled liga ou desliga o plugin na instalação inteira (dono): desligado, o processo para e nenhuma
// empresa o usa, mas a configuração de cada empresa fica guardada.
func (s *Service) SetPluginEnabled(ctx context.Context, ref string, on bool) (PluginView, error) {
	if err := s.requirePluginsOn(); err != nil {
		return PluginView{}, err
	}
	if err := s.requirePluginOwner(ctx); err != nil {
		return PluginView{}, err
	}
	rt := s.prt()
	rt.life.Lock()
	defer rt.life.Unlock()
	to, verb, action := domain.PluginDisabled, "plugin.suspended", "plugin.disable"
	if on {
		to, verb, action = domain.PluginReady, "plugin.resumed", "plugin.enable"
	}
	var row store.Plugin
	err := s.withTx(ctx, func(t *txc) error {
		r, err := s.getPlugin(t.ctx, t.q, ref)
		if err != nil {
			return err
		}
		if r, err = t.q.GetPluginForUpdate(t.ctx, r.ID); err != nil {
			return err
		}
		from := domain.PluginStatus(r.Status)
		if from == to {
			row = r
			return nil
		}
		if !from.CanBecome(to) {
			return fmt.Errorf("plugin %s está %s: %w", r.PluginKey, from, domain.ErrConflict)
		}
		if row, err = t.q.SetPluginStatus(t.ctx, store.SetPluginStatusParams{ID: r.ID, Status: string(to), Now: t.now}); err != nil {
			return err
		}
		if err := t.pluginCompanyActivity(r, verb, nil); err != nil {
			return err
		}
		return s.recordAdmin(t.ctx, t.q, action, r.PluginKey, &r.ID, "ok")
	})
	if err != nil {
		return PluginView{}, err
	}
	s.reconcileLocked(ctx)
	return s.GetPlugin(ctx, row.PluginKey)
}

// pluginCompanyActivity registra verb em cada empresa que ligou o plugin (o fato é da instalação, mas cada
// empresa precisa ver que o plugin dela mudou).
func (t *txc) pluginCompanyActivity(r store.Plugin, verb string, extra map[string]any) error {
	cos, err := t.q.ListPluginEnabledCompanies(t.ctx, r.ID)
	if err != nil {
		return fmt.Errorf("list plugin companies: %w", err)
	}
	data := map[string]any{"plugin": r.PluginKey, "version": r.Version}
	for k, v := range extra {
		data[k] = v
	}
	for _, c := range cos {
		if err := t.activity(domain.SystemActor, c.ID, verb, "plugin", r.ID, data, ""); err != nil {
			return err
		}
	}
	return nil
}

// UpgradePlugin aplica a versão nova que está no diretório do plugin (dono). Sem capacidade a mais, aplica e
// reinicia o processo; pedindo capacidade a mais, o plugin fica `upgrade_pending` (continuando a atender com o
// que já foi aprovado) até ApprovePluginUpgrade.
func (s *Service) UpgradePlugin(ctx context.Context, ref string) (PluginView, error) {
	if err := s.requirePluginsOn(); err != nil {
		return PluginView{}, err
	}
	if err := s.requirePluginOwner(ctx); err != nil {
		return PluginView{}, err
	}
	rt := s.prt()
	rt.life.Lock()
	defer rt.life.Unlock()
	cur, err := s.getPlugin(ctx, s.read(), ref)
	if err != nil {
		return PluginView{}, err
	}
	p := pluginFromRow(cur)
	switch p.Status {
	case domain.PluginUninstalled, domain.PluginInstalled:
		return PluginView{}, fmt.Errorf("plugin %s está %s: %w", p.Key, p.Status, domain.ErrConflict)
	}
	root, err := s.pluginsRoot()
	if err != nil {
		return PluginView{}, domain.Invalid("plugins", err.Error())
	}
	a, err := readPluginDir(root, p.Key)
	if err != nil {
		return PluginView{}, domain.Invalid("manifest", err.Error())
	}
	m := *a.Manifest
	if domain.CompareSemver(m.Version, p.Version) <= 0 {
		return PluginView{}, fmt.Errorf("o diretório tem a versão %s; instalada: %s (nada a atualizar): %w", m.Version, p.Version, domain.ErrConflict)
	}
	extra := domain.ExtraCapabilities(p.GrantedCapabilities, m.Capabilities)
	raw, err := manifestJSON(m)
	if err != nil {
		return PluginView{}, err
	}
	applied := false
	err = s.withTx(ctx, func(t *txc) error {
		r, err := t.q.GetPluginForUpdate(t.ctx, p.ID)
		if err != nil {
			return err
		}
		from := domain.PluginStatus(r.Status)
		if len(extra) > 0 {
			if from != domain.PluginReady && from != domain.PluginUpgradePending {
				return fmt.Errorf("a versão %s pede capacidades novas (%s); o plugin precisa estar ligado e pronto para a aprovação: %w",
					m.Version, strings.Join(extra, ", "), domain.ErrConflict)
			}
			if _, err := t.q.SetPluginPending(t.ctx, store.SetPluginPendingParams{ID: r.ID, PendingManifest: raw,
				PendingCapabilities: domain.SortCapabilities(m.Capabilities), Now: t.now}); err != nil {
				return err
			}
			return s.recordAdmin(t.ctx, t.q, "plugin.upgrade_pending", r.PluginKey+"@"+m.Version, &r.ID, "ok")
		}
		status := from
		if status == domain.PluginUpgradePending { // versão nova sem capacidade a mais substitui a pendente
			status = domain.PluginReady
		}
		if _, err := t.q.ApplyPluginManifest(t.ctx, store.ApplyPluginManifestParams{ID: r.ID, Version: m.Version, ApiVersion: i32(m.APIVersion),
			Manifest: raw, GrantedCapabilities: domain.SortCapabilities(m.Capabilities), Events: eventsOf(m), Status: string(status), Now: t.now}); err != nil {
			return err
		}
		applied = true
		if err := t.pluginCompanyActivity(r, "plugin.upgraded", map[string]any{"from": r.Version, "to": m.Version}); err != nil {
			return err
		}
		return s.recordAdmin(t.ctx, t.q, "plugin.upgrade", r.PluginKey+"@"+m.Version, &r.ID, "ok")
	})
	if err != nil {
		return PluginView{}, err
	}
	if applied {
		s.pluginLog(ctx, p.ID, nil, domain.PluginLogHost, "atualizado: "+p.Version+" → "+m.Version)
		s.dropWidgetCache(p.ID)
	} else {
		s.pluginLog(ctx, p.ID, nil, domain.PluginLogHost, "versão "+m.Version+" aguarda aprovação das capacidades: "+strings.Join(extra, ", "))
	}
	s.reconcileLocked(ctx) // versão nova muda a spec: o gerenciador reinicia o processo
	return s.GetPlugin(ctx, p.Key)
}

// ApprovePluginUpgrade aprova as capacidades pedidas pela versão pendente e a aplica (dono).
func (s *Service) ApprovePluginUpgrade(ctx context.Context, ref string) (PluginView, error) {
	if err := s.requirePluginsOn(); err != nil {
		return PluginView{}, err
	}
	if err := s.requirePluginOwner(ctx); err != nil {
		return PluginView{}, err
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
		if domain.PluginStatus(r.Status) != domain.PluginUpgradePending || len(r.PendingManifest) == 0 {
			return fmt.Errorf("plugin %s não tem atualização pendente: %w", r.PluginKey, domain.ErrConflict)
		}
		m, err := domain.ParsePluginManifest(r.PendingManifest)
		if err != nil {
			return err
		}
		raw, err := manifestJSON(m)
		if err != nil {
			return err
		}
		old := r.Version
		if row, err = t.q.ApplyPluginManifest(t.ctx, store.ApplyPluginManifestParams{ID: r.ID, Version: m.Version, ApiVersion: i32(m.APIVersion),
			Manifest: raw, GrantedCapabilities: domain.SortCapabilities(m.Capabilities), Events: eventsOf(m), Status: string(domain.PluginReady), Now: t.now}); err != nil {
			return err
		}
		if err := t.pluginCompanyActivity(row, "plugin.upgraded", map[string]any{"from": old, "to": m.Version,
			"capabilities": row.GrantedCapabilities}); err != nil {
			return err
		}
		return s.recordAdmin(t.ctx, t.q, "plugin.approve_upgrade", r.PluginKey+"@"+m.Version, &r.ID, "ok")
	})
	if err != nil {
		return PluginView{}, err
	}
	s.pluginLog(ctx, row.ID, nil, domain.PluginLogHost, "capacidades aprovadas: "+strings.Join(row.GrantedCapabilities, ", "))
	s.dropWidgetCache(row.ID)
	s.reconcileLocked(ctx)
	return s.GetPlugin(ctx, row.PluginKey)
}

// UninstallPlugin para o processo e as tools e apaga a configuração (com os segredos) e o estado do plugin em
// todas as empresas (dono). Os logs e o histórico de jobs ficam; reinstalar começa do zero.
func (s *Service) UninstallPlugin(ctx context.Context, ref string) error {
	if err := s.requirePluginsOn(); err != nil {
		return err
	}
	if err := s.requirePluginOwner(ctx); err != nil {
		return err
	}
	rt := s.prt()
	rt.life.Lock()
	defer rt.life.Unlock()
	var key string
	var id domain.ID
	err := s.withTx(ctx, func(t *txc) error {
		r, err := s.getPlugin(t.ctx, t.q, ref)
		if err != nil {
			return err
		}
		if r, err = t.q.GetPluginForUpdate(t.ctx, r.ID); err != nil {
			return err
		}
		if !domain.PluginStatus(r.Status).CanBecome(domain.PluginUninstalled) {
			return fmt.Errorf("plugin %s já está desinstalado: %w", r.PluginKey, domain.ErrConflict)
		}
		key, id = r.PluginKey, r.ID
		if err := t.pluginCompanyActivity(r, "plugin.uninstalled", nil); err != nil {
			return err
		}
		if err := t.q.DeletePluginSettings(t.ctx, r.ID); err != nil {
			return err
		}
		if err := t.q.DeleteAllPluginState(t.ctx, r.ID); err != nil {
			return err
		}
		if _, err := t.q.SetPluginStatus(t.ctx, store.SetPluginStatusParams{ID: r.ID, Status: string(domain.PluginUninstalled), Now: t.now}); err != nil {
			return err
		}
		return s.recordAdmin(t.ctx, t.q, "plugin.uninstall", r.PluginKey, &r.ID, "ok")
	})
	if err != nil {
		return err
	}
	if s.Plugins.Runtime != nil {
		_ = s.Plugins.Runtime.Stop(ctx, key)
	}
	s.pluginLog(ctx, id, nil, domain.PluginLogHost, "desinstalado")
	s.dropWidgetCache(id)
	s.dropSecretsCache(id)
	s.reconcileLocked(ctx)
	return nil
}

// ReconcilePlugins sobe os processos que devem estar de pé (plugin ativo ou em erro com ao menos uma empresa
// ligada) e para os demais. Roda no boot, depois de cada mudança e a cada tick (rede de segurança).
func (s *Service) ReconcilePlugins(ctx context.Context) {
	if !s.Plugins.Enabled() || s.Plugins.Runtime == nil {
		return
	}
	rt := s.prt()
	rt.life.Lock()
	defer rt.life.Unlock()
	s.reconcileLocked(ctx)
}

func (s *Service) reconcileLocked(ctx context.Context) {
	rtm := s.Plugins.Runtime
	if rtm == nil {
		return
	}
	rows, err := s.read().ListPlugins(ctx)
	if err != nil {
		if s.Log != nil {
			s.Log.Warn("plugins: reconciliar", "err", err)
		}
		return
	}
	counts, err := s.pluginCounts(ctx)
	if err != nil {
		return
	}
	want := map[string]bool{}
	for _, r := range rows {
		p := pluginFromRow(r)
		if !p.Status.WantsProcess() || counts[p.ID] == 0 {
			continue
		}
		spec, err := s.pluginSpec(p)
		if err != nil {
			s.setPluginStatus(ctx, p, domain.PluginError, err.Error())
			continue
		}
		want[p.Key] = true
		rtm.Start(ctx, spec)
	}
	running := rtm.Running()
	sort.Strings(running)
	for _, k := range running {
		if !want[k] {
			_ = rtm.Stop(ctx, k)
		}
	}
}

// PluginHealth é o retrato da saúde de um plugin (dono).
type PluginHealth struct {
	Status    domain.PluginStatus `json:"status"`
	LastError string              `json:"last_error,omitempty"`
	Process   plugin.Status       `json:"process"`
	Companies int                 `json:"companies"`
}

// GetPluginHealth devolve o estado gravado e o do processo.
func (s *Service) GetPluginHealth(ctx context.Context, ref string) (PluginHealth, error) {
	v, err := s.GetPlugin(ctx, ref)
	if err != nil {
		return PluginHealth{}, err
	}
	return PluginHealth{Status: v.Status, LastError: v.LastError, Process: v.Process, Companies: v.Companies}, nil
}

// ListPluginLogs devolve as últimas linhas do log do plugin (dono): stderr do processo, chamadas a log e avisos
// do host. Os valores secretos conhecidos do plugin saem mascarados já na gravação.
func (s *Service) ListPluginLogs(ctx context.Context, ref string, limit int) ([]domain.PluginLog, error) {
	if err := s.requirePluginOwner(ctx); err != nil {
		return nil, err
	}
	r, err := s.getPlugin(ctx, s.read(), ref)
	if err != nil {
		return nil, err
	}
	rows, err := s.read().ListPluginLogs(ctx, store.ListPluginLogsParams{PluginID: r.ID, Lim: i32(clamp(limit, 100, domain.PluginLogsKeep))})
	if err != nil {
		return nil, fmt.Errorf("list plugin logs: %w", err)
	}
	out := make([]domain.PluginLog, len(rows))
	for i, l := range rows {
		out[i] = domain.PluginLog{ID: l.ID, CompanyID: l.CompanyID, Level: l.Level, Message: l.Message, CreatedAt: l.CreatedAt}
	}
	return out, nil
}
