package service

import (
	"reflect"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/export"
)

func mergeDoc() export.Document {
	return export.Document{
		Format:        export.Format,
		Workspaces:    []export.Workspace{{Slug: "api", Name: "API"}, {Slug: "web", Name: "Web"}},
		Projects:      []export.Project{{Slug: "lancamento", Name: "Lançamento"}},
		Subscriptions: []export.Subscription{{Name: "Plano Pro"}},
		Skills:        []export.Skill{{Slug: "revisar", Name: "Revisar"}},
		Personas:      []export.Persona{{Slug: "dev", Name: "Dev"}, {Slug: "qa", Name: "QA"}},
		Flows:         []export.Flow{{Slug: "diario", Name: "Diário"}},
		Integrations:  []export.Integration{{Slug: "hook", Name: "Hook"}},
	}
}

func TestPlanMergeSemConflito(t *testing.T) {
	plan, err := planMerge(mergeDoc(), existingKeys{}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.items) != 9 {
		t.Fatalf("itens: %d", len(plan.items))
	}
	for _, it := range plan.items {
		if it.Action != domain.ItemCreate || it.Target != it.Key {
			t.Errorf("item %+v deveria ser create", it)
		}
	}
	if got := plan.written()[KindPersonas]; got != 2 {
		t.Errorf("written personas = %d", got)
	}
}

func TestPlanMergeAcoes(t *testing.T) {
	ex := existingKeys{
		KindWorkspaces:    {"api": true, "api-2": true},
		KindPersonas:      {"dev": true, "qa": true},
		KindSubscriptions: {"Plano Pro": true},
		KindFlows:         {"diario": true},
	}
	resolve := map[string]domain.ConflictAction{"personas/qa": domain.ConflictReplace, "subscriptions/Plano Pro": domain.ConflictRename}
	plan, err := planMerge(mergeDoc(), ex, domain.ConflictRename, resolve)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]mergeDecision{
		"workspaces/api":          {domain.ItemRename, "api-3"}, // api e api-2 já existem
		"workspaces/web":          {domain.ItemCreate, "web"},
		"personas/dev":            {domain.ItemRename, "dev-2"},
		"personas/qa":             {domain.ItemReplace, "qa"},
		"subscriptions/Plano Pro": {domain.ItemRename, "Plano Pro (2)"},
		"flows/diario":            {domain.ItemRename, "diario-2"},
		"skills/revisar":          {domain.ItemCreate, "revisar"},
	}
	for k, w := range want {
		kind, key, _ := strings.Cut(k, "/")
		if got := plan.decision(kind, key); got != w {
			t.Errorf("%s = %+v, quer %+v", k, got, w)
		}
	}
	if tl := plan.tally(); tl["rename"] != 4 || tl["replace"] != 1 || tl["create"] != 4 || tl["skip"] != 0 {
		t.Errorf("tally: %v", tl)
	}
}

func TestPlanMergePularEhPadrao(t *testing.T) {
	ex := existingKeys{KindPersonas: {"dev": true}}
	plan, err := planMerge(mergeDoc(), ex, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if d := plan.decision(KindPersonas, "dev"); d.Action != domain.ItemSkip || d.Target != "dev" {
		t.Errorf("dev: %+v", d)
	}
	if w := plan.written(); w[KindPersonas] != 1 {
		t.Errorf("written: %v", w)
	}
}

func TestPlanMergeRenomeiaSemColidirComOArquivo(t *testing.T) {
	doc := mergeDoc()
	doc.Personas = append(doc.Personas, export.Persona{Slug: "dev-2", Name: "Dev 2"})
	plan, err := planMerge(doc, existingKeys{KindPersonas: {"dev": true}}, domain.ConflictRename, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d := plan.decision(KindPersonas, "dev"); d.Target != "dev-3" { // dev-2 é do próprio arquivo
		t.Errorf("dev: %+v", d)
	}
}

func TestPlanMergeErros(t *testing.T) {
	cases := map[string]struct {
		def     domain.ConflictAction
		resolve map[string]domain.ConflictAction
	}{
		"ação padrão ruim":  {def: "merge"},
		"tipo desconhecido": {resolve: map[string]domain.ConflictAction{"tasks/1": domain.ConflictSkip}},
		"chave fora":        {resolve: map[string]domain.ConflictAction{"personas/zzz": domain.ConflictSkip}},
		"ação ruim":         {resolve: map[string]domain.ConflictAction{"personas/dev": "x"}},
	}
	for name, c := range cases {
		if _, err := planMerge(mergeDoc(), existingKeys{}, c.def, c.resolve); err == nil {
			t.Errorf("%s: esperava erro", name)
		}
	}
}

func TestCreatePlanEWarnings(t *testing.T) {
	doc := mergeDoc()
	doc.Flows[0].Enabled = true
	doc.Tasks = []export.Task{{Number: 1}, {Number: 2}}
	doc.Runs = []export.Run{{Key: "r1"}}
	if got := createPlan(doc); len(got) != 9 {
		t.Errorf("createPlan: %d", len(got))
	}
	p := importPlan{clearedPaths: map[string]bool{"api": true}}
	w := importWarnings(doc, p, false, true)
	codes := map[string]int{}
	for _, x := range w {
		codes[x.Code] = x.Count
	}
	want := map[string]int{"workspace_paths_cleared": 1, "integrations_disabled": 1, "personas_paused": 2, "flows_disabled": 1, "history_not_merged": 3}
	if !reflect.DeepEqual(codes, want) {
		t.Errorf("avisos: %v", codes)
	}
	// ativo e empresa nova: sem aviso de pausa nem de histórico
	codes = map[string]int{}
	for _, x := range importWarnings(doc, p, true, false) {
		codes[x.Code] = x.Count
	}
	if _, ok := codes["personas_paused"]; ok {
		t.Error("não deveria avisar de pausa")
	}
	if _, ok := codes["history_not_merged"]; ok {
		t.Error("empresa nova importa o histórico")
	}
}
