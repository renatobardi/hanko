package domain

import (
	"errors"
	"strings"
	"testing"
)

const validManifest = `{
  "id": "hello", "api_version": 1, "version": "1.2.3", "name": "Hello", "description": "Exemplo",
  "author": "Equipe", "homepage": "https://example.com", "command": "bin/hello", "args": ["--stdio"],
  "capabilities": ["tasks.read", "tasks.write", "events.subscribe", "agent.tools", "widgets", "jobs", "state"],
  "config_schema": {"type": "object", "required": ["greeting"], "properties": {
    "greeting": {"type": "string", "title": "Saudação", "default": "Olá"},
    "mode": {"type": "string", "enum": ["a", "b"]},
    "limit": {"type": "number"},
    "loud": {"type": "boolean"},
    "token": {"type": "string", "secret": true}}},
  "tools": [{"name": "hello_echo", "description": "Ecoa", "input_schema": {"type": "object", "properties": {"text": {"type": "string"}}}}],
  "events": ["task.created"],
  "widgets": [{"key": "resumo", "title": "Resumo", "placement": "dashboard"}],
  "jobs": [{"key": "tick", "every_minutes": 5}]
}`

func TestParsePluginManifestValid(t *testing.T) {
	m, err := ParsePluginManifest([]byte(validManifest))
	if err != nil {
		t.Fatalf("manifesto válido recusado: %v", err)
	}
	if m.ID != "hello" || len(m.Tools) != 1 || m.ConfigSchema.SecretKeys()[0] != "token" {
		t.Fatalf("manifesto = %+v", m)
	}
	if got := strings.Join(m.ConfigSchema.Keys(), ","); got != "greeting,limit,loud,mode,token" {
		t.Errorf("keys = %s", got)
	}
	// tool sem input_schema ganha o objeto vazio
	m2, err := ParsePluginManifest([]byte(strings.Replace(validManifest, `, "input_schema": {"type": "object", "properties": {"text": {"type": "string"}}}`, "", 1)))
	if err != nil || string(m2.Tools[0].InputSchema) != `{"type":"object"}` {
		t.Fatalf("schema padrão = %s, %v", m2.Tools[0].InputSchema, err)
	}
}

func TestParsePluginManifestInvalid(t *testing.T) {
	cases := map[string][2]string{
		"id inválido":             {`"id": "hello"`, `"id": "Hello_1"`},
		"api_version":             {`"api_version": 1`, `"api_version": 2`},
		"versão não semver":       {`"version": "1.2.3"`, `"version": "v1"`},
		"sem nome":                {`"name": "Hello"`, `"name": " "`},
		"comando absoluto":        {`"command": "bin/hello"`, `"command": "/usr/bin/env"`},
		"comando fora do dir":     {`"command": "bin/hello"`, `"command": "../x"`},
		"capacidade desconhecida": {`"tasks.read",`, `"gates.decide",`},
		"capacidade repetida":     {`"tasks.read",`, `"tasks.read", "tasks.read",`},
		"tool sem capacidade":     {`"agent.tools", `, ``},
		"evento sem capacidade":   {`"events.subscribe", `, ``},
		"widget sem capacidade":   {`"widgets", `, ``},
		"job sem capacidade":      {`"jobs", `, ``},
		"campo desconhecido":      {`"author": "Equipe"`, `"autor": "Equipe"`},
		"tool nome":               {`"name": "hello_echo"`, `"name": "Hello-Echo"`},
		"tool schema":             {`"input_schema": {"type": "object",`, `"input_schema": {"type": "array",`},
		"job intervalo":           {`"every_minutes": 5`, `"every_minutes": 1`},
		"widget placement":        {`"placement": "dashboard"`, `"placement": "sidebar"`},
		"evento inválido":         {`"events": ["task.created"]`, `"events": ["TASK"]`},
		"tipo de campo":           {`"limit": {"type": "number"}`, `"limit": {"type": "array"}`},
		"segredo com padrão":      {`"token": {"type": "string", "secret": true}`, `"token": {"type": "string", "secret": true, "default": "x"}`},
		"segredo não texto":       {`"token": {"type": "string", "secret": true}`, `"token": {"type": "boolean", "secret": true}`},
		"required inexistente":    {`"required": ["greeting"]`, `"required": ["nada"]`},
		"padrão fora do enum":     {`"mode": {"type": "string", "enum": ["a", "b"]}`, `"mode": {"type": "string", "enum": ["a", "b"], "default": "c"}`},
		"homepage":                {`"https://example.com"`, `"javascript:alert(1)"`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			src := strings.Replace(validManifest, c[0], c[1], 1)
			if src == validManifest {
				t.Fatalf("substituição não aplicada: %q", c[0])
			}
			_, err := ParsePluginManifest([]byte(src))
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, queria inválido", err)
			}
		})
	}
	if _, err := ParsePluginManifest([]byte(strings.Repeat(" ", PluginManifestMaxBytes+1))); !errors.Is(err, ErrInvalid) {
		t.Error("manifesto grande demais aceito")
	}
	if _, err := ParsePluginManifest([]byte(validManifest + `{}`)); !errors.Is(err, ErrInvalid) {
		t.Error("lixo depois do objeto aceito")
	}
}

func TestPluginTransitions(t *testing.T) {
	allowed := map[PluginStatus][]PluginStatus{
		PluginInstalled:      {PluginReady, PluginError, PluginUninstalled},
		PluginReady:          {PluginDisabled, PluginError, PluginUpgradePending, PluginUninstalled},
		PluginDisabled:       {PluginReady, PluginUninstalled},
		PluginError:          {PluginReady, PluginUninstalled},
		PluginUpgradePending: {PluginReady, PluginError, PluginUninstalled},
		PluginUninstalled:    {PluginInstalled},
	}
	all := []PluginStatus{PluginInstalled, PluginReady, PluginDisabled, PluginError, PluginUpgradePending, PluginUninstalled}
	for _, from := range all {
		for _, to := range all {
			want := false
			for _, a := range allowed[from] {
				want = want || a == to
			}
			if from.CanBecome(to) != want {
				t.Errorf("%s → %s = %v, queria %v", from, to, !want, want)
			}
		}
	}
	if !PluginUpgradePending.Active() || PluginDisabled.Active() || !PluginError.WantsProcess() {
		t.Error("Active/WantsProcess")
	}
}

func TestPluginHelpers(t *testing.T) {
	if got := ExtraCapabilities([]string{CapTasksRead}, []string{CapState, CapTasksRead, CapTasksWrite}); strings.Join(got, ",") != "tasks.write,state" {
		t.Errorf("extra = %v", got)
	}
	n := PluginToolName("meu-plugin", "busca_doc")
	id, tool, ok := ParsePluginToolName(n)
	if !ok || id != "meu-plugin" || tool != "busca_doc" {
		t.Errorf("parse %s = %s %s %v", n, id, tool, ok)
	}
	for _, bad := range []string{"hanko_task_get", "plugin_", "plugin_x", "plugin_Bad_x"} {
		if _, _, ok := ParsePluginToolName(bad); ok {
			t.Errorf("%s aceito", bad)
		}
	}
	for _, c := range []struct {
		a, b string
		want int
	}{{"1.0.0", "1.0.1", -1}, {"2.0.0", "1.9.9", 1}, {"1.0.0-rc.1", "1.0.0", -1}, {"1.0.0+b", "1.0.0", 0}, {"1.10.0", "1.9.0", 1}} {
		if got := CompareSemver(c.a, c.b); got != c.want {
			t.Errorf("Compare(%s,%s) = %d", c.a, c.b, got)
		}
	}
	f := PluginConfigField{Type: ConfigNumber}
	if v, err := f.Coerce("2.5"); err != nil || v != 2.5 {
		t.Errorf("coerce número = %v %v", v, err)
	}
	if _, err := (PluginConfigField{Type: ConfigBoolean}).Coerce("talvez"); err == nil {
		t.Error("booleano inválido aceito")
	}
}
