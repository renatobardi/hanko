package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/service"
)

// Plugins, lacunas do M18 (ADR-0045): abas, ponte da página, gatilhos de fluxo e fontes de skills de plugin.
// O provider de ambiente usa a API de ambientes (cloud.provider = plugin:<id>/<key>).

// PluginTab é uma aba que um plugin acrescenta ao menu da empresa.
type PluginTab struct {
	Plugin     string `json:"plugin"`
	PluginName string `json:"plugin_name"`
	Key        string `json:"key"`
	Title      string `json:"title"`
	Icon       string `json:"icon,omitempty"`
	URL        string `json:"url" doc:"Caminho da página na interface web."`
}

// PluginFlowLink liga um gatilho do plugin a um fluxo da empresa.
type PluginFlowLink struct {
	Trigger  string `json:"trigger"`
	FlowID   string `json:"flow_id" format:"uuid"`
	FlowSlug string `json:"flow_slug"`
	FlowName string `json:"flow_name"`
}

// PluginFlowTriggers são os gatilhos declarados e as ligações da empresa.
type PluginFlowTriggers struct {
	Triggers []PluginFlowTriggerDef `json:"triggers"`
	Links    []PluginFlowLink       `json:"links"`
}

// PluginSkillSourceItem é uma fonte de skills oferecida por um plugin.
type PluginSkillSourceItem struct {
	Plugin      string  `json:"plugin"`
	Key         string  `json:"key"`
	Title       string  `json:"title"`
	Description string  `json:"description,omitempty"`
	URL         string  `json:"url" doc:"Use em POST /skill-sources como url."`
	SourceID    *string `json:"source_id,omitempty" format:"uuid" doc:"Fonte já conectada na empresa."`
}

// PluginPageCallResult é o que o plugin respondeu à chamada da ponte.
type PluginPageCallResult struct {
	Result json.RawMessage `json:"result" doc:"Resposta do método page_call do plugin."`
}

type pluginPageCallIn struct {
	CompanyPluginPath
	Page string `path:"page" pattern:"^[a-z][a-z0-9_-]{0,47}$" doc:"Key da página no manifesto."`
	Body struct {
		Method string          `json:"method" pattern:"^[a-z][a-z0-9_.]{0,63}$" doc:"Método da ponte (o plugin decide o que aceita)."`
		Params json.RawMessage `json:"params,omitempty" doc:"Parâmetros livres (até 256 KiB)."`
	}
}

type pluginFlowLinkIn struct {
	CompanyPluginPath
	Trigger string `path:"trigger" pattern:"^[a-z][a-z0-9_-]{0,47}$"`
	Flow    string `path:"flow" doc:"Slug ou id do fluxo."`
}

func (s *server) registerPluginContributions(api huma.API) {
	huma.Register(api, op("list-plugin-tabs", http.MethodGet, "/companies/{company}/plugin-tabs", "plugins",
		"Abas dos plugins ativos, ligados na empresa e com a capacidade pages concedida"),
		func(ctx context.Context, in *struct{ CompanyPath }) (*ListOut[PluginTab], error) {
			pb, err := s.plugins()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			tabs, err := pb.PluginTabs(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(tabs, Page{Limit: len(tabs)}, func(t service.PluginTabView) PluginTab {
				return PluginTab{Plugin: t.PluginKey, PluginName: t.PluginName, Key: t.Key, Title: t.Title, Icon: t.Icon,
					URL: "/c/" + sc.Company.Slug + "/plugins/" + t.PluginKey + "/" + t.Key}
			}), nil
		})

	huma.Register(api, op("plugin-page-call", http.MethodPost, "/companies/{company}/plugins/{plugin}/pages/{page}/call", "plugins",
		"Ponte da página: chama um método do próprio plugin (page_call); o plugin recebe quem chamou e decide o que permitir"),
		func(ctx context.Context, in *pluginPageCallIn) (*ItemOut[PluginPageCallResult], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			pb, err := s.plugins()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			res, err := pb.PluginPageCall(ctx, sc.Company.ID, in.Plugin, in.Page, in.Body.Method, in.Body.Params)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[PluginPageCallResult]{Body: PluginPageCallResult{Result: res}}, nil
		})

	huma.Register(api, op("plugin-flow-triggers", http.MethodGet, "/companies/{company}/plugins/{plugin}/flow-triggers", "plugins",
		"Gatilhos de fluxo declarados pelo plugin e os fluxos da empresa ligados a cada um"),
		func(ctx context.Context, in *CompanyPluginPath) (*ItemOut[PluginFlowTriggers], error) {
			pb, err := s.plugins()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			trig, links, err := pb.PluginFlowTriggers(ctx, sc.Company.ID, in.Plugin)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			out := PluginFlowTriggers{Triggers: []PluginFlowTriggerDef{}, Links: []PluginFlowLink{}}
			for _, t := range trig {
				out.Triggers = append(out.Triggers, PluginFlowTriggerDef(t))
			}
			for _, l := range links {
				out.Links = append(out.Links, PluginFlowLink(l))
			}
			return &ItemOut[PluginFlowTriggers]{Body: out}, nil
		})

	link := func(id, method, summary string, on bool) {
		huma.Register(api, noContent(op(id, method, "/companies/{company}/plugins/{plugin}/flow-triggers/{trigger}/flows/{flow}", "plugins", summary)),
			func(ctx context.Context, in *pluginFlowLinkIn) (*struct{}, error) {
				if err := forbidPersona(ctx); err != nil {
					return nil, err
				}
				pb, err := s.plugins()
				if err != nil {
					return nil, err
				}
				sc, err := s.scope(ctx, in.Company)
				if err != nil {
					return nil, err
				}
				if err := pb.LinkPluginFlowTrigger(ctx, sc.Actor, sc.Company.ID, in.Plugin, in.Trigger, in.Flow, on); err != nil {
					return nil, s.fail(ctx, err)
				}
				return nil, nil
			})
	}
	link("link-plugin-flow-trigger", http.MethodPut, "Liga um gatilho do plugin a um fluxo da empresa (admin); idempotente", true)
	link("unlink-plugin-flow-trigger", http.MethodDelete, "Desliga o gatilho do fluxo (admin)", false)

	huma.Register(api, op("list-plugin-skill-sources", http.MethodGet, "/companies/{company}/plugin-skill-sources", "plugins",
		"Fontes de skills oferecidas por plugins ativos e ligados na empresa (conecte com POST /skill-sources e a url devolvida)"),
		func(ctx context.Context, in *struct{ CompanyPath }) (*ListOut[PluginSkillSourceItem], error) {
			pb, err := s.plugins()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			srcs, err := pb.PluginSkillSources(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(srcs, Page{Limit: len(srcs)}, func(v service.PluginSkillSourceView) PluginSkillSourceItem {
				o := PluginSkillSourceItem{Plugin: v.PluginKey, Key: v.Key, Title: v.Title, Description: v.Description, URL: v.URL}
				if v.SourceID != nil {
					id := idStr(*v.SourceID)
					o.SourceID = &id
				}
				return o
			}), nil
		})
}
