package domain

import (
	"strings"
	"testing"
)

func m18Manifest(extra string) []byte {
	return []byte(`{"id":"demo","api_version":1,"version":"1.0.0","name":"Demo","command":"demo",` + extra + `}`)
}

func TestManifestM18Contributions(t *testing.T) {
	ok := m18Manifest(`"capabilities":["pages","flows.trigger","environments.provider","skills.source"],
	  "pages":[{"key":"painel","title":"Painel","icon":"puzzle"}],
	  "flow_triggers":[{"key":"novo-pedido","title":"Novo pedido","description":"d"}],
	  "environment_providers":[{"key":"principal","title":"Nuvem","known_hosts":"gw.example ssh-ed25519 AAAA\n# comentário\n"}],
	  "skill_sources":[{"key":"catalogo","title":"Catálogo"}]`)
	m, err := ParsePluginManifest(ok)
	if err != nil {
		t.Fatalf("manifesto válido: %v", err)
	}
	if p, found := m.Page("painel"); !found || p.Title != "Painel" {
		t.Errorf("Page = %+v %v", p, found)
	}
	if _, found := m.FlowTrigger("novo-pedido"); !found {
		t.Error("FlowTrigger")
	}
	if e, found := m.EnvProvider("principal"); !found || !strings.Contains(e.KnownHosts, "gw.example") {
		t.Errorf("EnvProvider = %+v", e)
	}
	if _, found := m.SkillSource("catalogo"); !found {
		t.Error("SkillSource")
	}
	if _, found := m.Page("x"); found {
		t.Error("Page inexistente")
	}
	if _, found := m.FlowTrigger("x"); found {
		t.Error("FlowTrigger inexistente")
	}
	if _, found := m.EnvProvider("x"); found {
		t.Error("EnvProvider inexistente")
	}
	if _, found := m.SkillSource("x"); found {
		t.Error("SkillSource inexistente")
	}

	cases := map[string]string{
		"página sem a capacidade":       `"capabilities":[],"pages":[{"key":"a","title":"A"}]`,
		"gatilho sem a capacidade":      `"capabilities":[],"flow_triggers":[{"key":"a","title":"A"}]`,
		"provider sem a capacidade":     `"capabilities":[],"environment_providers":[{"key":"a","title":"A","known_hosts":"h t k"}]`,
		"fonte sem a capacidade":        `"capabilities":[],"skill_sources":[{"key":"a","title":"A"}]`,
		"key inválida":                  `"capabilities":["pages"],"pages":[{"key":"A b","title":"A"}]`,
		"key repetida":                  `"capabilities":["pages"],"pages":[{"key":"a","title":"A"},{"key":"a","title":"B"}]`,
		"título vazio":                  `"capabilities":["pages"],"pages":[{"key":"a","title":" "}]`,
		"ícone inválido":                `"capabilities":["pages"],"pages":[{"key":"a","title":"A","icon":"../x"}]`,
		"known_hosts ausente":           `"capabilities":["environments.provider"],"environment_providers":[{"key":"a","title":"A"}]`,
		"known_hosts com coringa":       `"capabilities":["environments.provider"],"environment_providers":[{"key":"a","title":"A","known_hosts":"* ssh-ed25519 AAAA"}]`,
		"known_hosts malformado":        `"capabilities":["environments.provider"],"environment_providers":[{"key":"a","title":"A","known_hosts":"só-um-campo"}]`,
		"campo desconhecido":            `"capabilities":["pages"],"pages":[{"key":"a","title":"A","url":"http://x"}]`,
		"descrição longa do gatilho":    `"capabilities":["flows.trigger"],"flow_triggers":[{"key":"a","title":"A","description":"` + strings.Repeat("x", 501) + `"}]`,
		"descrição longa da fonte":      `"capabilities":["skills.source"],"skill_sources":[{"key":"a","title":"A","description":"` + strings.Repeat("x", 501) + `"}]`,
		"capacidade com erro de grafia": `"capabilities":["page"]`,
	}
	for name, extra := range cases {
		if _, err := ParsePluginManifest(m18Manifest(extra)); err == nil {
			t.Errorf("%s: deveria ser recusado", name)
		}
	}
	// tetos
	var pages []string
	for i := 0; i <= PluginMaxPages; i++ {
		pages = append(pages, `{"key":"p`+string(rune('a'+i))+`","title":"P"}`)
	}
	if _, err := ParsePluginManifest(m18Manifest(`"capabilities":["pages"],"pages":[` + strings.Join(pages, ",") + `]`)); err == nil {
		t.Error("mais páginas que o teto deveria ser recusado")
	}
}

func TestPluginCapabilitiesM18(t *testing.T) {
	for _, c := range []string{CapPages, CapFlowTriggers, CapEnvProvider, CapSkillSource} {
		if !ValidPluginCapability(c) {
			t.Errorf("%s deveria ser capacidade", c)
		}
	}
	if got := SortCapabilities([]string{CapSkillSource, CapTasksRead, CapPages}); strings.Join(got, ",") != "tasks.read,pages,skills.source" {
		t.Errorf("ordem = %v", got)
	}
	// um upgrade que pede capacidade nova do M18 depende de aprovação
	if got := ExtraCapabilities([]string{CapTasksRead}, []string{CapTasksRead, CapPages}); len(got) != 1 || got[0] != CapPages {
		t.Errorf("extra = %v", got)
	}
	if PluginMethodCapability["flows.fire"] != CapFlowTriggers {
		t.Error("flows.fire exige flows.trigger")
	}
}

func TestPluginProviderAndSkillURL(t *testing.T) {
	if n := PluginProviderName("nuvem-x", "principal"); n != "plugin:nuvem-x/principal" {
		t.Errorf("nome = %q", n)
	}
	id, key, ok := ParsePluginProvider("plugin:nuvem-x/principal")
	if !ok || id != "nuvem-x" || key != "principal" {
		t.Errorf("parse = %q %q %v", id, key, ok)
	}
	for _, bad := range []string{"", "daytona", "plugin:", "plugin:x", "plugin:/k", "plugin:X/k", "plugin:a-b/K", "plugin:a-b/k/extra", "plugin:../x"} {
		if _, _, ok := ParsePluginProvider(bad); ok {
			t.Errorf("ParsePluginProvider(%q) deveria falhar", bad)
		}
	}
	if u := PluginSkillURL("demo", "catalogo"); u != "plugin://demo/catalogo" {
		t.Errorf("url = %q", u)
	}
	id, key, ok = ParsePluginSkillURL("plugin://demo/catalogo")
	if !ok || id != "demo" || key != "catalogo" {
		t.Errorf("parse url = %q %q %v", id, key, ok)
	}
	for _, bad := range []string{"", "https://x/y", "plugin://", "plugin://demo", "plugin://D/x", "plugin://demo/", "workspace://demo/x"} {
		if _, _, ok := ParsePluginSkillURL(bad); ok {
			t.Errorf("ParsePluginSkillURL(%q) deveria falhar", bad)
		}
	}
	for m, want := range map[string]bool{"registrar": true, "a.b_c": true, "": false, "A": false, "1a": false, "a-b": false, strings.Repeat("a", 65): false} {
		if got := ValidPluginBridgeMethod(m); got != want {
			t.Errorf("ValidPluginBridgeMethod(%q) = %v", m, got)
		}
	}
}
