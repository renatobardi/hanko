//go:build integration

package api_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
)

// Pipelines, cases, fila de revisão, aprendizados e exemplos pela API (ADR-0034).
func TestAPIPipelinesCases(t *testing.T) {
	e := newAPIEnv(t)
	ana := e.persona(e.co, "Ana", nil)
	b := e.base()

	body := fmt.Sprintf(`{"name":"Vendas","stages":[
		{"key":"novo","name":"Novo","config":{"fields":[{"key":"empresa","label":"Empresa","type":"text","required":true,"show_in_add_form":true}],
		  "automation":{"persona":"%s","instructions":"Pesquise o lead."}}},
		{"key":"revisao","name":"Revisão","kind":"review","config":{"review":{"approve_to":"ganho","reject_to":"perdido","request_changes_to":"novo"}}},
		{"key":"ganho","name":"Ganho","kind":"done"},
		{"key":"perdido","name":"Perdido","kind":"cancelled"}]}`, ana.Slug)
	var pl api.Pipeline
	e.want(201, "POST", b+"/pipelines", body, &pl)
	if pl.Key != "vendas" || len(pl.Stages) != 4 || pl.Stages[0].Config.Automation == nil || pl.Stages[0].Config.Automation.PersonaID != ana.ID.String() {
		t.Fatalf("pipeline: %+v", pl)
	}
	e.want(422, "POST", b+"/pipelines", `{"name":"X","stages":[{"name":"R","kind":"review"}]}`, nil)

	// persona com run token: lê, cria case (com a task da run ligada), não desenha pipeline
	hdr := e.personaToken(e.co, ana)
	if st, _ := e.call("POST", b+"/pipelines", `{"name":"Y"}`, hdr); st != 403 {
		t.Errorf("persona cria pipeline: %d", st)
	}
	st, raw := e.call("POST", b+"/pipelines/vendas/cases", `{"title":"Lead Acme","case_type":"lead","key":"acme.com","fields":{"empresa":"Acme"}}`, hdr)
	if st != 201 {
		t.Fatalf("persona cria case: %d %s", st, raw)
	}
	var cs api.Case
	_ = json.Unmarshal(raw, &cs)
	if cs.Created == nil || !*cs.Created || cs.StageKey != "novo" || cs.OriginRunID == "" || cs.Version != 1 {
		t.Fatalf("case: %+v", cs)
	}
	// upsert pelo par tipo+key
	st, raw = e.call("POST", b+"/cases", `{"pipeline":"vendas","title":"Lead Acme","case_type":"lead","key":"acme.com","summary":"200 pessoas"}`, hdr)
	if st != 201 {
		t.Fatalf("upsert: %d %s", st, raw)
	}
	var up api.Case
	_ = json.Unmarshal(raw, &up)
	if up.ID != cs.ID || up.Created == nil || *up.Created || up.Summary != "200 pessoas" {
		t.Fatalf("upsert: %+v", up)
	}
	// persona não decide revisão nem resolve sugestão
	if st, _ := e.call("POST", b+"/cases/"+cs.Identifier+"/review", `{"expected_version":2,"decision":"approve"}`, hdr); st != 403 {
		t.Errorf("persona revisa: %d", st)
	}
	var sg api.Case
	if st, raw := e.call("POST", b+"/cases/"+cs.Identifier+"/suggest", `{"expected_version":2,"to_stage":"revisao","rationale":"pronto","confidence":0.8}`, hdr); st != 200 {
		t.Fatalf("suggest: %d %s", st, raw)
	} else {
		_ = json.Unmarshal(raw, &sg)
	}
	if sg.PendingSuggestion == nil || sg.Version != 3 {
		t.Fatalf("sugestão: %+v", sg)
	}

	// versão otimista: 409 com a versão atual
	st, raw = e.call("PATCH", b+"/cases/"+cs.Identifier, `{"expected_version":1,"title":"x"}`, nil)
	if st != 409 || !strings.Contains(string(raw), "versão atual é 3") {
		t.Fatalf("conflito de versão: %d %s", st, raw)
	}
	e.want(422, "PATCH", b+"/cases/"+cs.Identifier, `{"title":"x"}`, nil)

	// fila de revisão mostra a sugestão; aceitar move para revisão
	var q api.ListBody[api.ReviewQueueItem]
	e.want(200, "GET", b+"/review-queue", "", &q)
	if len(q.Items) != 1 || q.Items[0].Kind != "suggestion" || q.Items[0].SuggestedStageName != "Revisão" {
		t.Fatalf("fila: %+v", q.Items)
	}
	var acc api.Case
	e.want(200, "POST", b+"/cases/"+cs.Identifier+"/resolve-suggestion", `{"expected_version":3,"accept":true}`, &acc)
	if acc.StageKey != "revisao" || acc.Status != "in_review" {
		t.Fatalf("aceitar: %+v", acc)
	}
	e.want(422, "POST", b+"/cases/"+cs.Identifier+"/review", fmt.Sprintf(`{"expected_version":%d,"decision":"reject"}`, acc.Version), nil)
	var rv api.Case
	e.want(200, "POST", b+"/cases/"+cs.Identifier+"/review", fmt.Sprintf(`{"expected_version":%d,"decision":"request_changes","reason":"falta o CNPJ"}`, acc.Version), &rv)
	if rv.StageKey != "novo" {
		t.Fatalf("mudanças: %+v", rv)
	}
	// o pacote de contexto traz o motivo e o aprendizado
	var pkg api.CaseContext
	e.want(200, "GET", b+"/cases/"+cs.Identifier+"/context", "", &pkg)
	if !strings.Contains(pkg.Markdown, "falta o CNPJ") || !strings.Contains(pkg.Markdown, "Aprendizados desta etapa") {
		t.Fatalf("contexto: %s", pkg.Markdown)
	}
	var ls api.ListBody[api.Learning]
	e.want(200, "GET", b+"/learnings?pipeline=vendas", "", &ls)
	if len(ls.Items) != 1 || ls.Items[0].Decision != "request_changes" || ls.Items[0].Reason != "falta o CNPJ" {
		t.Fatalf("aprendizados: %+v", ls.Items)
	}
	e.want(422, "GET", b+"/learnings?decision=talvez", "", nil)

	// detalhe e listas
	var full api.Case
	e.want(200, "GET", b+"/cases/"+cs.ID, "", &full)
	if len(full.Events) < 5 || len(full.Links) < 2 {
		t.Fatalf("detalhe: %d eventos, %d ligações", len(full.Events), len(full.Links))
	}
	var lst api.ListBody[api.Case]
	e.want(200, "GET", b+"/cases?type=lead&status=in_progress", "", &lst)
	if len(lst.Items) != 1 {
		t.Fatalf("lista filtrada: %d", len(lst.Items))
	}
	e.want(200, "GET", b+"/cases?q=nada-disso", "", &lst)
	if len(lst.Items) != 0 {
		t.Errorf("busca vazia: %d", len(lst.Items))
	}

	// lote com falha parcial
	var c1, c2 api.Case
	e.want(201, "POST", b+"/pipelines/vendas/cases", `{"title":"A","stage":"revisao"}`, &c1)
	e.want(201, "POST", b+"/pipelines/vendas/cases", `{"title":"B","stage":"revisao"}`, &c2)
	var bulk struct {
		Results []api.BulkResult `json:"results"`
		Failed  int              `json:"failed"`
	}
	e.want(200, "POST", b+"/review-queue/bulk", fmt.Sprintf(`{"items":[{"case":"%s","expected_version":1},{"case":"%s","expected_version":9}]}`, c1.Identifier, c2.Identifier), &bulk)
	if bulk.Failed != 1 || !bulk.Results[0].OK || bulk.Results[1].OK || bulk.Results[1].CurrentVersion != 1 {
		t.Fatalf("lote: %+v", bulk)
	}

	// exemplo de decisão da revisão e exportação JSONL
	e.want(200, "GET", b+"/learnings", "", &ls)
	var ex api.DecisionExample
	e.want(201, "POST", b+"/decision-examples", fmt.Sprintf(`{"source_kind":"case_review","source_id":"%s","notes":"bom"}`, ls.Items[0].EventID), &ex)
	if ex.DecisionOutcome != "approve" || ex.Snapshot == nil {
		t.Fatalf("exemplo: %+v", ex)
	}
	e.want(409, "POST", b+"/decision-examples", fmt.Sprintf(`{"source_kind":"case_review","source_id":"%s"}`, ls.Items[0].EventID), nil)
	e.want(200, "PATCH", b+"/decision-examples/"+ex.ID, `{"notes":"ótimo"}`, &ex)
	if ex.Notes != "ótimo" || len(ex.NotesHistory) != 1 {
		t.Fatalf("notas: %+v", ex)
	}
	st, raw = e.call("GET", b+"/decision-examples/export.jsonl", "", nil)
	if st != 200 || strings.Count(strings.TrimSpace(string(raw)), "\n") != 0 || !strings.Contains(string(raw), `"notes":"ótimo"`) {
		t.Fatalf("export: %d %s", st, raw)
	}
	if st, _ := e.call("GET", b+"/decision-examples/export.jsonl", "", hdr); st != 403 {
		t.Errorf("persona exporta: %d", st)
	}
	e.want(204, "DELETE", b+"/decision-examples/"+ex.ID, "", nil)

	// configuração: etapas e transições
	var p2 api.Pipeline
	e.want(201, "POST", b+"/pipelines/vendas/stages", `{"name":"Proposta","position":1}`, &p2)
	if p2.Stages[1].Key != "proposta" {
		t.Fatalf("add stage: %+v", p2.Stages)
	}
	e.want(200, "PUT", b+"/pipelines/vendas/stages/order", `{"stages":["novo","revisao","proposta","ganho","perdido"]}`, &p2)
	e.want(200, "PUT", b+"/pipelines/vendas/transitions", `{"transitions":[{"from":"novo","to":"proposta"}],"enforce":true}`, &p2)
	if !p2.EnforceTransitions || len(p2.Transitions) != 1 || p2.Transitions[0].From != "novo" {
		t.Fatalf("transições: %+v", p2)
	}
	e.want(422, "POST", b+"/cases/"+cs.Identifier+"/transition", fmt.Sprintf(`{"expected_version":%d,"to_stage":"ganho"}`, rv.Version), nil)
	e.want(200, "PATCH", b+"/pipelines/vendas/stages/proposta", `{"name":"Proposta comercial"}`, &p2)
	e.want(200, "DELETE", b+"/pipelines/vendas/stages/proposta", "", &p2)
	e.want(409, "DELETE", b+"/pipelines/vendas", "", nil)
	e.want(200, "PATCH", b+"/pipelines/vendas", `{"archived":true}`, &p2)
	if !p2.Archived {
		t.Errorf("arquivar")
	}
	var pls api.ListBody[api.Pipeline]
	e.want(200, "GET", b+"/pipelines", "", &pls)
	if len(pls.Items) != 0 {
		t.Errorf("arquivado na lista padrão")
	}
	// isolamento: outra empresa não vê o case
	other := e.company("Outra", "outra")
	e.want(404, "GET", "/api/v1/companies/"+other.Slug+"/cases/"+cs.ID, "", nil)
}
