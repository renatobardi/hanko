package ui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Plugins, lacunas do M18 (ADR-0045): abas em iframe sandboxed com ponte postMessage, concessão de capacidades pelo
// dono da instalação e ligação de gatilhos de fluxo pelo admin da empresa.

// pluginFrameCSP isola o documento do plugin: sem rede (nem connect-src), sem frames, sem formulários; só script e
// estilo inline, imagens e fontes data:. O "sandbox allow-scripts" repete o do iframe (origem opaca) para valer
// também se o documento for aberto fora dele.
const pluginFrameCSP = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; font-src data:; " +
	"base-uri 'none'; form-action 'none'; frame-ancestors 'self'; sandbox allow-scripts"

// pluginBridgeShim é injetado antes do HTML do plugin: window.hanko.call(method, params) → Promise e
// window.hanko.resize(altura). Só fala com o pai (a página hospedeira) por postMessage.
const pluginBridgeShim = `(function(){var n=0,p={};window.hanko={context:__CTX__,call:function(m,a){return new Promise(function(ok,no){var i=++n;p[i]={ok:ok,no:no};` +
	`parent.postMessage({hanko:1,type:"call",id:i,method:m,params:a===undefined?null:a},"*")})},` +
	`resize:function(h){parent.postMessage({hanko:1,type:"resize",height:h},"*")}};` +
	`window.addEventListener("message",function(e){if(e.source!==parent)return;var m=e.data;if(!m||m.hanko!==1||!p[m.id])return;var q=p[m.id];delete p[m.id];` +
	`if(m.ok){q.ok(m.result)}else{q.no(new Error(m.error||"erro"))}});` +
	`if(window.ResizeObserver){new ResizeObserver(function(){window.hanko.resize(document.documentElement.scrollHeight)}).observe(document.documentElement)}})();`

// pluginFrameDocument monta o documento do iframe: a ponte e, depois, o HTML do plugin.
func pluginFrameDocument(co domain.Company, pluginKey, page, pluginHTML string) string {
	ctx, _ := json.Marshal(map[string]string{"company": co.Slug, "plugin": pluginKey, "page": page})
	shim := strings.Replace(pluginBridgeShim, "__CTX__", strings.ReplaceAll(string(ctx), "<", `<`), 1)
	return "<!doctype html><html><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">" +
		"<style>html,body{margin:0}</style><script>" + shim + "</script></head><body>" + pluginHTML + "</body></html>"
}

func pluginPageHref(co domain.Company, pluginKey, page string) string {
	return "/c/" + co.Slug + "/plugins/" + url.PathEscape(pluginKey) + "/" + url.PathEscape(page)
}

// pluginTabLinks lista as abas de plugin da empresa para a barra lateral. Qualquer falha dá "sem abas": o menu nunca
// quebra uma página.
func (s *server) pluginTabLinks(ctx context.Context, co domain.Company) []views.PluginTab {
	pb, ok := s.pluginsBE()
	if !ok || !pb.PluginsEnabled() {
		return nil
	}
	tabs, err := pb.PluginTabs(ctx, co.ID)
	if err != nil {
		return nil
	}
	out := make([]views.PluginTab, 0, len(tabs))
	for _, t := range tabs {
		out = append(out, views.PluginTab{Title: t.Title, Href: pluginPageHref(co, t.PluginKey, t.Key), Icon: t.Icon})
	}
	return out
}

func (s *server) findPluginTab(ctx context.Context, pb pluginsUIBackend, co domain.Company, pluginKey, page string) (service.PluginTabView, error) {
	tabs, err := pb.PluginTabs(ctx, co.ID)
	if err != nil {
		return service.PluginTabView{}, err
	}
	for _, t := range tabs {
		if t.PluginKey == pluginKey && t.Key == page {
			return t, nil
		}
	}
	return service.PluginTabView{}, domain.ErrNotFound
}

// handlePluginPage é a página hospedeira da aba: o iframe sandboxed apontando para /frame. Sem a capacidade pages, sem
// plugin ligado ou sem a página no manifesto, é 404.
func (s *server) handlePluginPage(w http.ResponseWriter, r *http.Request, co domain.Company) {
	pb, ok := s.pluginsBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	t, err := s.findPluginTab(ctx, pb, co, r.PathValue("plugin"), r.PathValue("page"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	href := pluginPageHref(co, t.PluginKey, t.Key)
	vm := views.PluginPageVM{Title: t.Title, PluginName: t.PluginName, FrameSrc: href + "/frame", BridgeURL: href + "/ponte", Company: co.Slug}
	sh := s.shell(r, co, "plugin:"+href, t.Title, views.Crumb{Label: t.PluginName}, views.Crumb{Label: t.Title})
	s.render(w, r, http.StatusOK, views.PluginPageHostPage(sh, vm))
}

// handlePluginFrame serve o documento do plugin (o HTML é de terceiros) com CSP sem rede e nosniff. É o único lugar
// por onde HTML de plugin chega ao navegador.
func (s *server) handlePluginFrame(w http.ResponseWriter, r *http.Request, co domain.Company) {
	pb, ok := s.pluginsBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	h := w.Header()
	h.Set("Content-Security-Policy", pluginFrameCSP)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Content-Type", "text/html; charset=utf-8")
	pluginKey, page := r.PathValue("plugin"), r.PathValue("page")
	_, html, err := pb.PluginPageHTML(ctx, co.ID, pluginKey, page)
	if err != nil {
		msg, status := i18n.T(ctx, "Página indisponível."), http.StatusBadGateway
		switch {
		case errors.Is(err, domain.ErrNotFound):
			status = http.StatusNotFound
		case errors.Is(err, domain.ErrForbidden):
			status = http.StatusForbidden
		case errors.Is(err, domain.ErrInvalid), errors.Is(err, domain.ErrConflict):
			msg += " " + pluginClip(errText(err), 300)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, "<!doctype html><meta charset=\"utf-8\"><body style=\"font:14px system-ui;margin:16px\"><p>"+htmlEscape(msg)+"</p>")
		return
	}
	_, _ = io.WriteString(w, pluginFrameDocument(co, pluginKey, page, html))
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&#34;", "'", "&#39;").Replace(s)
}

// handlePluginBridge recebe as chamadas da ponte (fetch same-origin do script da hospedeira) e as entrega ao plugin.
func (s *server) handlePluginBridge(w http.ResponseWriter, r *http.Request, co domain.Company) {
	pb, ok := s.pluginsBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, domain.PluginPageCallMax+4096)
	var in struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	reply := func(status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		reply(http.StatusBadRequest, map[string]string{"error": i18n.T(r.Context(), "Chamada inválida.")})
		return
	}
	res, err := pb.PluginPageCall(r.Context(), co.ID, r.PathValue("plugin"), r.PathValue("page"), in.Method, in.Params)
	if err != nil {
		status := statusFor(err)
		msg := i18n.T(r.Context(), "O plugin não respondeu.")
		if errors.Is(err, domain.ErrInvalid) || errors.Is(err, domain.ErrConflict) {
			msg = pluginClip(errText(err), 300)
		}
		if status >= http.StatusInternalServerError {
			status = http.StatusBadGateway
		}
		reply(status, map[string]string{"error": msg})
		return
	}
	if len(res) == 0 {
		res = json.RawMessage("null")
	}
	reply(http.StatusOK, map[string]json.RawMessage{"result": res})
}

// ---------- concessão de capacidades (dono da instalação) ----------

func (s *server) handlePluginCapability(w http.ResponseWriter, r *http.Request, co domain.Company, grant bool) {
	pb, ok := s.pluginsBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ref := r.PathValue("plugin")
	if _, err := pb.SetPluginCapability(r.Context(), ref, trimForm(r, "cap"), grant); err != nil {
		s.pluginFail(w, r, co, pluginsBase(co)+"/"+url.PathEscape(ref), err)
		return
	}
	notice := "revogada"
	if grant {
		notice = "concedida"
	}
	redirect(w, r, pluginsBase(co)+"/"+url.PathEscape(ref)+"?ok="+notice)
}

// capToggles lista as capacidades declaradas no manifesto com o estado da concessão (só o dono da instalação vê).
func capToggles(ctx context.Context, base string, p service.PluginView) []views.PluginCapToggle {
	out := make([]views.PluginCapToggle, 0, len(p.Manifest.Capabilities))
	for _, c := range domain.SortCapabilities(p.Manifest.Capabilities) {
		granted := p.Has(c)
		action := base + "/conceder"
		if granted {
			action = base + "/revogar"
		}
		out = append(out, views.PluginCapToggle{PluginCap: capLabel(ctx, c), Granted: granted, Action: action})
	}
	return out
}

// ---------- gatilhos de fluxo ----------

func (s *server) handlePluginTriggerLink(w http.ResponseWriter, r *http.Request, co domain.Company) {
	pb, ok := s.pluginsBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	ref, trigger := r.PathValue("plugin"), r.PathValue("trigger")
	link := r.PathValue("acao") == "ligar"
	flow := trimForm(r, "flow")
	if flow == "" {
		flow = strings.TrimSpace(r.URL.Query().Get("flow"))
	}
	if err := pb.LinkPluginFlowTrigger(ctx, actorFrom(ctx), co.ID, ref, trigger, flow, link); err != nil {
		s.pluginFail(w, r, co, pluginsBase(co)+"/"+url.PathEscape(ref), err)
		return
	}
	notice := "gatilho-desligado"
	if link {
		notice = "gatilho-ligado"
	}
	redirect(w, r, pluginsBase(co)+"/"+url.PathEscape(ref)+"?ok="+notice)
}

// pluginContribVM acrescenta ao detalhe do plugin o que ele contribui (abas, gatilhos, providers e fontes).
func (s *server) pluginContribVM(ctx context.Context, co domain.Company, pb pluginsUIBackend, cp service.CompanyPlugin, vm *views.PluginDetailVM) {
	if cp.Active {
		for _, pg := range cp.Pages {
			if slices.Contains(cp.Capabilities, domain.CapPages) {
				vm.Pages = append(vm.Pages, views.PluginPageRow{Title: pg.Title, Href: pluginPageHref(co, cp.Key, pg.Key)})
			}
		}
	}
	for _, e := range cp.EnvProviders {
		vm.Providers = append(vm.Providers, views.PluginProviderRow{Title: e.Title, Provider: domain.PluginProviderName(cp.Key, e.Key)})
	}
	for _, sc := range cp.SkillSources {
		vm.Sources = append(vm.Sources, views.PluginSourceRow{Title: sc.Title, URL: domain.PluginSkillURL(cp.Key, sc.Key)})
	}
	if len(cp.FlowTriggers) == 0 {
		return
	}
	trigs, links, err := pb.PluginFlowTriggers(ctx, co.ID, cp.Key)
	if err != nil {
		return
	}
	flows, _ := pb.ListFlows(ctx, co.ID)
	base := pluginsBase(co) + "/" + url.PathEscape(cp.Key) + "/gatilhos/"
	for _, t := range trigs {
		tv := views.PluginTriggerVM{Key: t.Key, Title: t.Title, Desc: t.Description, Admin: canAdminCtx(ctx), LinkAction: base + url.PathEscape(t.Key) + "/ligar"}
		linked := map[string]bool{}
		for _, l := range links {
			if l.Trigger == t.Key {
				linked[l.FlowSlug] = true
				tv.Links = append(tv.Links, views.PluginTriggerLink{Name: l.FlowName, Slug: l.FlowSlug, UnlinkAction: base + url.PathEscape(t.Key) + "/desligar?flow=" + url.QueryEscape(l.FlowSlug)})
			}
		}
		for _, f := range flows {
			if !linked[f.Slug] {
				tv.FlowOptions = append(tv.FlowOptions, views.Option{Value: f.Slug, Label: f.Name})
			}
		}
		vm.Triggers = append(vm.Triggers, tv)
	}
}

func canAdminCtx(ctx context.Context) bool { return views.RoleFrom(ctx).AtLeast(domain.MemberAdmin) }
