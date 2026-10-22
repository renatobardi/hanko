package domain

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNormalizeFlowVariables(t *testing.T) {
	long := strings.Repeat("x", MaxFlowVariableValue+1)
	cases := []struct {
		name string
		in   []FlowVariable
		ok   bool
	}{
		{"vazio", nil, true},
		{"válida", []FlowVariable{{Name: " repo ", Required: true}, {Name: "env_1", Default: "prod"}}, true},
		{"maiúscula", []FlowVariable{{Name: "Repo"}}, false},
		{"começa com dígito", []FlowVariable{{Name: "1a"}}, false},
		{"embutida", []FlowVariable{{Name: "data"}}, false},
		{"repetida", []FlowVariable{{Name: "a"}, {Name: "a"}}, false},
		{"default longo", []FlowVariable{{Name: "a", Default: long}}, false},
		{"rótulo longo", []FlowVariable{{Name: "a", Label: strings.Repeat("r", MaxFlowVariableLabel+1)}}, false},
	}
	for _, c := range cases {
		out, err := NormalizeFlowVariables(c.in)
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v, ok = %v", c.name, err, c.ok)
		}
		if err != nil && !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: erro não é ErrInvalid", c.name)
		}
		if err == nil && out == nil {
			t.Errorf("%s: lista nula", c.name)
		}
	}
	many := make([]FlowVariable, MaxFlowVariables+1)
	for i := range many {
		many[i] = FlowVariable{Name: "v" + string(rune('a'+i))}
	}
	if _, err := NormalizeFlowVariables(many); err == nil {
		t.Error("passou do teto de variáveis")
	}
	out, _ := NormalizeFlowVariables([]FlowVariable{{Name: " repo "}})
	if out[0].Name != "repo" {
		t.Errorf("nome não foi aparado: %q", out[0].Name)
	}
}

func TestFlowTemplates(t *testing.T) {
	vars := []FlowVariable{{Name: "repo"}}
	if got := FlowVarRefs("a {{repo}} b {{ data }} {{repo}} {{x1}}"); strings.Join(got, ",") != "repo,data,x1" {
		t.Errorf("refs = %v", got)
	}
	if err := CheckFlowTemplate("task_title", "{{repo}} {{n}} {{semana}}", vars); err != nil {
		t.Error(err)
	}
	err := CheckFlowTemplate("task_title", "{{nada}} {{repo}}", vars)
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Field != "task_title" || !strings.Contains(ve.Reason, "{{nada}}") {
		t.Errorf("err = %v", err)
	}
	if CheckScheduleVariables([]FlowVariable{{Name: "a", Required: true, Default: "x"}, {Name: "b"}}) != nil {
		t.Error("agenda com default recusada")
	}
	if CheckScheduleVariables([]FlowVariable{{Name: "a", Required: true}}) == nil {
		t.Error("agenda sem default aceita")
	}
}

func TestResolveFlowVariables(t *testing.T) {
	decl := []FlowVariable{{Name: "a", Required: true}, {Name: "b", Default: "dflt"}, {Name: "c"}}
	got, err := ResolveFlowVariables(decl, map[string]string{"a": " 1 "}, true)
	if err != nil || got["a"] != "1" || got["b"] != "dflt" || got["c"] != "" {
		t.Fatalf("got %v %v", got, err)
	}
	if _, err := ResolveFlowVariables(decl, map[string]string{"b": "x"}, true); err == nil || !strings.Contains(err.Error(), "a") {
		t.Errorf("faltando obrigatória: %v", err)
	}
	if _, err := ResolveFlowVariables(decl, map[string]string{"a": "1", "z": "2"}, true); err == nil {
		t.Error("strict aceitou variável não declarada")
	}
	if _, err := ResolveFlowVariables(decl, map[string]string{"a": "1", "z": "2"}, false); err != nil {
		t.Errorf("não strict recusou excedente: %v", err)
	}
	if _, err := ResolveFlowVariables(decl, map[string]string{"a": strings.Repeat("é", MaxFlowVariableValue+1)}, false); err == nil {
		t.Error("valor longo aceito")
	}
	if _, err := ResolveFlowVariables(decl, map[string]string{"a": " "}, false); err == nil {
		t.Error("valor em branco conta como faltando")
	}
}

func TestRenderFlowTemplate(t *testing.T) {
	at := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC) // 31/12/2025 22:00 em São Paulo; semana ISO 2026-W01
	loc, _ := time.LoadLocation("America/Sao_Paulo")
	b := FlowBuiltins(at, loc, 7, FireWebhook)
	if b["data"] != "2025-12-31" || b["n"] != "7" || b["semana"] != "2026-W01" || b["disparo"] != "webhook" {
		t.Fatalf("builtins = %v", b)
	}
	if FlowBuiltins(at, nil, 1, FireSchedule)["data"] != "2026-01-01" {
		t.Error("sem fuso deveria usar UTC")
	}
	vars := map[string]string{"repo": "{{data}}", "x": "ok"}
	got := RenderFlowTemplate("{{repo}} {{ x }} {{data}} {{n}} {{ausente}}", b, vars)
	if got != "{{data}} ok 2025-12-31 7 {{ausente}}" {
		t.Errorf("render = %q", got)
	}
	if RenderFlowTemplate("sem marcador", b, vars) != "sem marcador" {
		t.Error("texto sem marcador mudou")
	}
	for src, want := range map[FlowFireSource]string{FireSchedule: "agenda", FireManual: "manual", FireWebhook: "webhook", FireEvent: "evento"} {
		if src.Label() != want || !src.Valid() {
			t.Errorf("source %q", src)
		}
	}
	if FlowFireSource("x").Valid() {
		t.Error("origem inválida aceita")
	}
}

func TestEventVerbs(t *testing.T) {
	for _, v := range []string{"task.created", "gate.*", "run.status_changed"} {
		if err := ValidEventVerb(v); err != nil {
			t.Errorf("%q: %v", v, err)
		}
	}
	for _, v := range []string{"", "task", "Task.created", "flow.fired", "flow.*", "a..b", "x.*.y", strings.Repeat("a", 90) + ".b"} {
		if ValidEventVerb(v) == nil {
			t.Errorf("%q aceito", v)
		}
	}
	if !MatchEventVerb("gate.*", "gate.decided") || MatchEventVerb("gate.*", "gates.x") || MatchEventVerb("gate.*", "gate") {
		t.Error("prefixo")
	}
	if !MatchEventVerb("task.created", "task.created") || MatchEventVerb("task.created", "task.created2") {
		t.Error("exato")
	}
	if !FlowTriggerKind("webhook").Valid() || !FlowTriggerKind("event").Valid() || FlowTriggerKind("x").Valid() {
		t.Error("kinds")
	}
}

func TestJSONPathAndMapping(t *testing.T) {
	var doc any
	_ = json.Unmarshal([]byte(`{"repo":{"name":"api","tags":["a","b"],"stars":12,"on":true,"x":{"k":1}},"title":"  oi ","nulo":null}`), &doc)
	cases := map[string]string{"repo.name": "api", "repo.tags.1": "b", "repo.stars": "12", "repo.on": "true", "repo.x": `{"k":1}`, "title": "  oi "}
	for path, want := range cases {
		v, ok := JSONPath(doc, path)
		if !ok || JSONText(v) != want {
			t.Errorf("%s = %q ok=%v", path, JSONText(v), ok)
		}
	}
	for _, path := range []string{"repo.nada", "repo.tags.9", "repo.tags.x", "title.x", "nulo.x"} {
		if _, ok := JSONPath(doc, path); ok {
			t.Errorf("%s existe", path)
		}
	}
	if JSONText(nil) != "" || JSONText(1.5) != "1.5" || len([]rune(JSONText(strings.Repeat("a", 5000)))) != MaxFlowVariableValue {
		t.Error("JSONText")
	}
	decl := []FlowVariable{{Name: "nome"}, {Name: "estrelas"}, {Name: "title"}, {Name: "ausente"}, {Name: "vazia"}}
	got := MapVariables(decl, map[string]string{"nome": "repo.name", "estrelas": "repo.stars", "vazia": "nulo"}, doc)
	if got["nome"] != "api" || got["estrelas"] != "12" || got["title"] != "  oi " || len(got) != 3 {
		t.Errorf("map = %v", got)
	}
	if !EventFilterMatches(nil, doc) || !EventFilterMatches(map[string]string{"repo.name": "api", "repo.stars": "12"}, doc) ||
		EventFilterMatches(map[string]string{"repo.name": "x"}, doc) || EventFilterMatches(map[string]string{"nada": "x"}, doc) {
		t.Error("filtro")
	}
}

func TestNormalizeTriggerInputs(t *testing.T) {
	decl := []FlowVariable{{Name: "a"}}
	if m, err := NormalizeTriggerMapping(map[string]string{" a ": " x.y "}, decl); err != nil || m["a"] != "x.y" {
		t.Errorf("%v %v", m, err)
	}
	if _, err := NormalizeTriggerMapping(map[string]string{"b": "x"}, decl); err == nil {
		t.Error("variável não declarada")
	}
	if _, err := NormalizeTriggerMapping(map[string]string{"a": " "}, decl); err == nil {
		t.Error("caminho vazio")
	}
	if _, err := NormalizeEventFilter(map[string]string{"": "x"}); err == nil {
		t.Error("chave vazia")
	}
	big := map[string]string{}
	for i := 0; i < 11; i++ {
		big[string(rune('a'+i))] = "x"
	}
	if _, err := NormalizeEventFilter(big); err == nil {
		t.Error("filtro grande")
	}
	if f, err := NormalizeEventFilter(map[string]string{"k": "v"}); err != nil || f["k"] != "v" {
		t.Error("filtro ok")
	}
}

func TestDiffFlowSnapshots(t *testing.T) {
	p := NewID()
	a := FlowSnapshot{Name: "a", PersonaID: p, Priority: "low", Variables: []FlowVariable{{Name: "x"}}, Triggers: []FlowSnapshotTrigger{}}
	if d := DiffFlowSnapshots(a, a); len(d) != 0 {
		t.Errorf("iguais: %v", d)
	}
	b := a
	b.Name, b.Cron, b.Timezone, b.Description = "b", "0 9 * * *", "UTC", "d"
	b.TaskTitle, b.TaskDescription, b.Priority = "t", "td", "high"
	b.PersonaID = NewID()
	pr := NewID()
	b.ProjectID = &pr
	b.Variables = []FlowVariable{{Name: "y"}}
	b.Triggers = []FlowSnapshotTrigger{{Kind: TriggerKindEvent, Name: "e"}}
	got := strings.Join(DiffFlowSnapshots(a, b), ",")
	if got != "name,description,cron,timezone,persona_id,project_id,priority,task_title,task_description,variables,triggers" {
		t.Errorf("diff = %s", got)
	}
}
