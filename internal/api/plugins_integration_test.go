//go:build integration

package api_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/service"
)

const apiPluginManifest = `{"id":"demo","api_version":1,"version":"1.0.0","name":"Demo","command":"run.sh",
 "capabilities":["agent.tools","state"],
 "config_schema":{"properties":{"region":{"type":"string","enum":["sa","us"]},"token":{"type":"string","secret":true}}},
 "tools":[{"name":"ping","description":"Responde pong"}]}`

func TestAPIPlugins(t *testing.T) {
	e := newAPIEnv(t)
	root := t.TempDir()
	dir := filepath.Join(root, "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hanko-plugin.json"), []byte(apiPluginManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil { //nolint:gosec // script de teste
		t.Fatal(err)
	}
	box, err := secretbox.New("chave-de-teste-dos-plugins-com-32-bytes-ou-mais", "")
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Integrations = service.IntegrationsConfig{Secrets: box}
	// sem gerenciador de processos: o fluxo da API e do banco é o mesmo, o plugin fica em erro
	e.svc.Plugins = service.PluginsConfig{Dir: root, DataDir: t.TempDir()}

	var ov api.PluginsOverview
	e.want(200, "GET", "/api/v1/plugins", "", &ov)
	if !ov.Enabled || len(ov.Available) != 1 || ov.Available[0].Key != "demo" || strings.Join(ov.Available[0].Capabilities, ",") != "agent.tools,state" {
		t.Fatalf("visão: %+v", ov)
	}
	e.want(422, "POST", "/api/v1/plugins/install", `{"key":"nao-existe"}`, nil)
	var p api.Plugin
	e.want(201, "POST", "/api/v1/plugins/install", `{"key":"demo"}`, &p)
	if p.Key != "demo" || p.Status != "error" || p.Tools[0].MCPName != "plugin_demo_ping" {
		t.Fatalf("instalado: %+v", p)
	}
	e.want(409, "POST", "/api/v1/plugins/install", `{"key":"demo"}`, nil)

	// empresa: configurar com segredo; o valor nunca volta
	var cp api.CompanyPlugin
	e.want(200, "PATCH", e.base()+"/plugins/demo", `{"enabled":true,"config":{"region":"sa","token":"SEGREDO-API"}}`, &cp)
	raw := e.want(200, "GET", e.base()+"/plugins/demo", "", &cp)
	if !cp.Enabled || cp.Config["region"] != "sa" || strings.Join(cp.SecretsSet, ",") != "token" || strings.Contains(string(raw), "SEGREDO-API") {
		t.Fatalf("plugin na empresa: %s", raw)
	}
	if len(cp.Fields) != 2 || !cp.Fields[1].Secret {
		t.Fatalf("formulário: %+v", cp.Fields)
	}
	e.want(422, "PATCH", e.base()+"/plugins/demo", `{"config":{"region":"eu"}}`, nil)
	e.want(422, "PATCH", e.base()+"/plugins/demo", `{"config":{"nada":1}}`, nil)
	var list api.ListBody[api.CompanyPlugin]
	e.want(200, "GET", e.base()+"/plugins", "", &list)
	if len(list.Items) != 1 {
		t.Fatalf("lista: %+v", list)
	}
	var st api.ListBody[api.PluginStateItem]
	e.want(200, "GET", e.base()+"/plugins/demo/state", "", &st)
	var ws api.ListBody[api.PluginWidget]
	e.want(200, "GET", e.base()+"/plugins/demo/widgets?placement=dashboard", "", &ws)
	e.want(404, "GET", e.base()+"/plugins/outro", "", nil)

	// logs e saúde
	var logs api.ListBody[api.PluginLog]
	e.want(200, "GET", "/api/v1/plugins/demo/logs", "", &logs)
	if len(logs.Items) == 0 {
		t.Fatal("instalação sem linha no log")
	}
	var h api.PluginHealth
	e.want(200, "GET", "/api/v1/plugins/demo/health", "", &h)
	if h.Status != "error" || h.Companies != 1 || h.Process.Running {
		t.Fatalf("saúde: %+v", h)
	}

	// run token: nada de administrar plugins nem ler o estado
	hdr := e.personaToken(e.co, e.persona(e.co, "Ana", nil))
	for _, c := range [][2]string{{"GET", "/api/v1/plugins"}, {"POST", "/api/v1/plugins/demo/disable"}, {"GET", e.base() + "/plugins/demo/state"}} {
		if status, b := e.call(c[0], c[1], "", hdr); status != 403 {
			t.Errorf("%s %s com run token: %d %s", c[0], c[1], status, b)
		}
	}
	if status, b := e.call("PATCH", e.base()+"/plugins/demo", `{"enabled":false}`, hdr); status != 403 {
		t.Errorf("patch com run token: %d %s", status, b)
	}

	e.want(409, "POST", "/api/v1/plugins/demo/approve-upgrade", "", nil)
	e.want(409, "POST", "/api/v1/plugins/demo/upgrade", "", nil)
	e.want(204, "POST", "/api/v1/plugins/demo/uninstall", "", nil)
	e.want(404, "GET", e.base()+"/plugins/demo", "", nil)
	e.want(409, "POST", "/api/v1/plugins/demo/uninstall", "", nil)
}
