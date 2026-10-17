package ui

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

// Pipelines, cases, fila de revisão e aprendizados na UI, contra o fake (ADR-0034).

func newPipesEnv(t *testing.T, role domain.MemberRole) (*env, *uitest.Pipes) {
	t.Helper()
	p := uitest.WithPipelines(uitest.Sample())
	p.MyRoleIs = role
	e := newEnv(t, p.Fake)
	e.h = serveWith(t, p)
	return e, p
}

func TestPipelinesUnavailableWithoutBackend(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	for _, path := range []string{"/c/hanko/pipelines", "/c/hanko/cases", "/c/hanko/revisao", "/c/hanko/aprendizados"} {
		wantStatus(t, e.get(t, path), http.StatusNotImplemented)
	}
	// as telas que já existiam continuam sem o painel novo
	wantStatus(t, e.get(t, "/c/hanko/caixa"), http.StatusOK)
	wantStatus(t, e.get(t, "/c/hanko/tasks/"+e.f.Tasks[0].Key), http.StatusOK)
}

func TestPipelinesListAndBoard(t *testing.T) {
	e, _ := newPipesEnv(t, domain.MemberOwner)
	rec := e.get(t, "/c/hanko/pipelines")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Conteúdo", "Rascunho → Revisão → Publicado → Descartado", "Novo pipeline", "Criar pipeline")

	rec = e.get(t, "/c/hanko/pipelines/conteudo")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `data-stage="rascunho"`, `data-case="HNK-C1"`, `data-move="/c/hanko/cases/HNK-C1/mover"`, `data-version="1"`, `draggable="true"`,
		"Gates como contrato", "Novo item", `name="f.tema"`, "Entra na etapa Rascunho.", "sugestão", "Sato")

	rec = e.get(t, "/c/hanko/pipelines/conteudo?aba=config")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Transições", "Salvar etapa", "tema | Tema | text |  | obrigatorio, formulario", `value="rascunho&gt;revisao" checked`, "Aprovar move para")
	wantNotContains(t, rec, "<fieldset class=\"props-fs\" disabled")

	rec = e.get(t, "/c/hanko/pipelines/conteudo?aba=atividade")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "moveu de Rascunho para Revisão")
	wantStatus(t, e.get(t, "/c/hanko/pipelines/nada"), http.StatusNotFound)
}

func TestPipelinesViewerSeesNoControls(t *testing.T) {
	e, p := newPipesEnv(t, domain.MemberViewer)
	rec := e.get(t, "/c/hanko/pipelines/conteudo")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `draggable="false"`, `data-need="member"`)
	wantNotContains(t, rec, "Criar case")
	rec = e.get(t, "/c/hanko/pipelines/conteudo?aba=config")
	wantContains(t, rec, `<fieldset class="props-fs" disabled`, `data-need="admin"`)
	rec = e.post(t, "/c/hanko/pipelines/conteudo/cases", url.Values{"title": {"x"}, "f.tema": {"y"}}, false)
	wantStatus(t, rec, http.StatusForbidden)
	if p.Called("CreateCase") {
		t.Error("viewer criou case")
	}
	rec = e.get(t, "/c/hanko/cases/HNK-C2")
	wantContains(t, rec, "Só o aprovador desta etapa decide a revisão.")
	wantNotContains(t, rec, "Aprovar e mover para")
}

func TestPipelineCreateAndAddCase(t *testing.T) {
	e, p := newPipesEnv(t, domain.MemberOwner)
	rec := e.post(t, "/c/hanko/pipelines", url.Values{"name": {"Vendas"}, "stages": {"Novo | working\nRevisão | review\nGanho | done\nPerdido | cancelled"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/c/hanko/pipelines/vendas?ok=criado" {
		t.Fatalf("location = %q", loc)
	}
	v, _ := p.GetPipeline(t.Context(), domain.ID{}, "vendas")
	if r := v.Stages[1].Config.Review; r == nil || r.ApproveTo != "ganho" || r.RejectTo != "perdido" || r.RequestChangesTo != "novo" {
		t.Fatalf("destinos padrão: %+v", v.Stages[1].Config.Review)
	}
	rec = e.post(t, "/c/hanko/pipelines", url.Values{"name": {"X"}, "stages": {"A | estranho"}}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, "estranho")

	// campo obrigatório do formulário Novo item
	rec = e.post(t, "/c/hanko/pipelines/conteudo/cases", url.Values{"title": {"Post novo"}}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, "Tema", `value="Post novo"`)
	rec = e.post(t, "/c/hanko/pipelines/conteudo/cases", url.Values{"title": {"Post novo"}, "f.tema": {"agentes"}, "f.formato": {"post"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if !strings.HasPrefix(rec.Header().Get("Location"), "/c/hanko/cases/HNK-C5") {
		t.Fatalf("location = %q", rec.Header().Get("Location"))
	}
	// etapa: salvar com campos no editor
	rec = e.post(t, "/c/hanko/pipelines/conteudo/etapas/rascunho", url.Values{"name": {"Rascunho"}, "key": {"rascunho"}, "kind": {"working"},
		"fields": {"tema | Tema | text |  | obrigatorio\nnivel | Nível | select | baixo, alto | formulario"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	v, _ = p.GetPipeline(t.Context(), domain.ID{}, "conteudo")
	if fs := v.Stages[0].Config.Fields; len(fs) != 2 || fs[1].Type != domain.FieldSelect || len(fs[1].Options) != 2 || !fs[1].ShowInAddForm {
		t.Fatalf("campos: %+v", fs)
	}
	rec = e.post(t, "/c/hanko/pipelines/conteudo/etapas/rascunho", url.Values{"name": {"Rascunho"}, "kind": {"working"}, "fields": {"a | A | text | | talvez"}}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	// transições e reordenar
	rec = e.post(t, "/c/hanko/pipelines/conteudo/transicoes", url.Values{"edge": {"rascunho>revisao", "revisao>publicado"}, "strict": {"1"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	v, _ = p.GetPipeline(t.Context(), domain.ID{}, "conteudo")
	if len(v.Transitions) != 2 || !v.EnforceTransitions {
		t.Fatalf("transições: %+v", v)
	}
	wantStatus(t, e.post(t, "/c/hanko/pipelines/conteudo/etapas/revisao/subir", nil, false), http.StatusSeeOther)
	v, _ = p.GetPipeline(t.Context(), domain.ID{}, "conteudo")
	if v.Stages[0].Key != "revisao" {
		t.Errorf("subir: %s", v.Stages[0].Key)
	}
}

func TestCaseBoardDragAndConflicts(t *testing.T) {
	e, p := newPipesEnv(t, domain.MemberMember)
	rec := e.post(t, "/c/hanko/cases/HNK-C1/mover", url.Values{"to_stage": {"revisao"}, "expected_version": {"1"}}, true)
	wantStatus(t, rec, http.StatusNoContent)
	if v := rec.Header().Get("X-Case-Version"); v != "2" {
		t.Fatalf("X-Case-Version = %q", v)
	}
	// o quadro guardou a versão antiga: o servidor recusa e o erro vai para #flash
	rec = e.post(t, "/c/hanko/cases/HNK-C2/mover", url.Values{"to_stage": {"rascunho"}, "expected_version": {"1"}}, true)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	if rec.Header().Get("HX-Retarget") != "#flash" {
		t.Errorf("HX-Retarget = %q", rec.Header().Get("HX-Retarget"))
	}
	p.PipesErr["TransitionCase"] = domain.ErrCaseBlocked
	rec = e.post(t, "/c/hanko/cases/HNK-C3/mover", url.Values{"to_stage": {"rascunho"}, "expected_version": {"1"}}, false)
	wantStatus(t, rec, http.StatusConflict)
	wantContains(t, rec, "O case está bloqueado")
	delete(p.PipesErr, "TransitionCase")
	// formulário da página (sem htmx): versão velha mostra a atual
	rec = e.post(t, "/c/hanko/cases/HNK-C1/mover", url.Values{"to_stage": {"rascunho"}, "expected_version": {"1"}}, false)
	wantStatus(t, rec, http.StatusConflict)
	wantContains(t, rec, "agora está na versão 2")
}

func TestCasePageReviewAndSuggestion(t *testing.T) {
	e, p := newPipesEnv(t, domain.MemberOwner)
	rec := e.get(t, "/c/hanko/cases/HNK-C2")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Aprovar e mover para Publicado", "Pedir mudanças", "Recusar", "tema", "Histórico", "Abrir conversa")
	rec = e.get(t, "/c/hanko/cases/HNK-C1")
	wantContains(t, rec, "Sugestão: mover para Revisão", "82%", "Rascunho com <strong>três</strong> argumentos.", "Mover para")
	// recusar sem motivo
	rec = e.post(t, "/c/hanko/cases/HNK-C2/revisar", url.Values{"decision": {"reject"}, "expected_version": {"1"}}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, "rejecting needs a reason")
	rec = e.post(t, "/c/hanko/cases/HNK-C2/revisar", url.Values{"decision": {"approve"}, "expected_version": {"1"}, "reason": {"bom"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	rec = e.get(t, "/c/hanko/cases/HNK-C2")
	wantContains(t, rec, "aprovou e moveu para Publicado", "Salvar como exemplo")
	// aceitar a sugestão
	rec = e.post(t, "/c/hanko/cases/HNK-C1/sugestao", url.Values{"accept": {"1"}, "expected_version": {"1"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	c, _ := p.GetCaseView(t.Context(), domain.ID{}, "HNK-C1")
	if c.StageKey != "revisao" || c.PendingSuggestion != nil {
		t.Fatalf("sugestão aceita: %+v", c.Case)
	}
	// editar: campos chave = valor substituem o mapa
	rec = e.post(t, "/c/hanko/cases/HNK-C1", url.Values{"expected_version": {"2"}, "title": {"Novo título"}, "summary": {"x"}, "fields": {"tema = heartbeat\nnota = 10"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	c, _ = p.GetCaseView(t.Context(), domain.ID{}, "HNK-C1")
	if c.Title != "Novo título" || c.Fields["nota"] != "10" || c.Fields["formato"] != nil {
		t.Fatalf("editar: %+v", c.Case)
	}
	rec = e.post(t, "/c/hanko/cases/HNK-C1", url.Values{"expected_version": {"3"}, "title": {"x"}, "fields": {"sem igual"}}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	// abrir conversa leva para a task
	rec = e.post(t, "/c/hanko/cases/HNK-C1/conversa", nil, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if !strings.HasPrefix(rec.Header().Get("Location"), "/c/hanko/tasks/HNK-") {
		t.Fatalf("conversa: %q", rec.Header().Get("Location"))
	}
	// o painel Cases aparece na task
	rec = e.get(t, rec.Header().Get("Location"))
	wantContains(t, rec, "HNK-C1", "conversa")
}

func TestReviewQueueBulkPartialFailure(t *testing.T) {
	e, p := newPipesEnv(t, domain.MemberMember)
	rec := e.get(t, "/c/hanko/revisao")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Gates como contrato", "Mover para Revisão (82%)", "Aprovar selecionados", `name="item"`)
	c2, _ := p.GetCaseView(t.Context(), domain.ID{}, "HNK-C2")
	c5, _, _ := p.CreateCase(t.Context(), domain.HumanActor, domain.ID{}, service.CreateCaseInput{Title: "Outro", PipelineID: c2.PipelineID, Fields: map[string]any{"tema": "x"}})
	c5v, _ := p.TransitionCase(t.Context(), domain.HumanActor, domain.ID{}, c5.ID, service.TransitionCaseInput{ExpectedVersion: 1, ToStage: "revisao"})
	rec = e.post(t, "/c/hanko/revisao/lote", url.Values{"item": {c2.ID.String() + ":1", c5v.ID.String() + ":1"}}, false)
	wantStatus(t, rec, http.StatusConflict)
	wantContains(t, rec, "Alguns itens não puderam ser aprovados.", "HNK-C5")
	if c, _ := p.GetCaseView(t.Context(), domain.ID{}, "HNK-C2"); c.StageKey != "publicado" {
		t.Errorf("o item válido do lote não foi aprovado: %s", c.StageKey)
	}
	wantStatus(t, e.post(t, "/c/hanko/revisao/lote", url.Values{}, false), http.StatusUnprocessableEntity)
	// decidir e dispensar pela fila
	rec = e.post(t, "/c/hanko/cases/HNK-C5/revisar", url.Values{"decision": {"approve"}, "expected_version": {"2"}, "next": {"revisao"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if rec.Header().Get("Location") != "/c/hanko/revisao?ok=revisado" {
		t.Errorf("volta para a fila: %q", rec.Header().Get("Location"))
	}
	rec = e.post(t, "/c/hanko/cases/HNK-C1/sugestao", url.Values{"accept": {"0"}, "expected_version": {"1"}, "next": {"revisao"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	rec = e.get(t, "/c/hanko/revisao")
	wantContains(t, rec, "Nada precisa de você agora.")
	// a Caixa de entrada mostra a aba da fila
	rec = e.get(t, "/c/hanko/caixa")
	wantContains(t, rec, `href="/c/hanko/revisao"`)
}

func TestCasesListTreeAndEmpty(t *testing.T) {
	e, p := newPipesEnv(t, domain.MemberMember)
	rec := e.get(t, "/c/hanko/cases")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Série sobre agentes", "Episódio 1: personas", "Conteúdo › Rascunho", "solto", "Novo case")
	rec = e.get(t, "/c/hanko/cases?visao=arvore")
	wantContains(t, rec, `class="ttl pl-d1"`)
	rec = e.get(t, "/c/hanko/cases?q=gates")
	wantContains(t, rec, "Gates como contrato")
	wantNotContains(t, rec, "Série sobre agentes")
	rec = e.get(t, "/c/hanko/cases?agrupar=pipeline")
	wantContains(t, rec, "Soltos")
	rec = e.post(t, "/c/hanko/cases", url.Values{"title": {"Lead Acme"}, "case_type": {"lead"}, "status": {"in_progress"}, "fields": {"empresa = Acme"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	p.Cases = nil
	rec = e.get(t, "/c/hanko/cases")
	wantContains(t, rec, "Nenhum case ainda — as personas criam cases com hanko_case_create, ou crie um aqui.")
}

func TestLearningsAndExamples(t *testing.T) {
	e, p := newPipesEnv(t, domain.MemberAdmin)
	c2, _ := p.GetCaseView(t.Context(), domain.ID{}, "HNK-C2")
	if _, err := p.ReviewCase(t.Context(), domain.HumanActor, domain.ID{}, c2.ID, service.ReviewCaseInput{ExpectedVersion: 1, Decision: domain.ReviewRequestChanges, Reason: "falta o gancho"}); err != nil {
		t.Fatal(err)
	}
	rec := e.get(t, "/c/hanko/aprendizados")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `pediu mudanças em &#34;Gates como contrato&#34;, que voltou para Rascunho`, "falta o gancho", "Salvar como exemplo", "Exportar JSONL")
	c, _ := p.GetCaseView(t.Context(), domain.ID{}, "HNK-C2")
	ev := c.Events[len(c.Events)-1]
	rec = e.post(t, "/c/hanko/exemplos", url.Values{"source_kind": {"case_review"}, "source_id": {ev.ID.String()}, "notes": {"gancho é obrigatório"}, "next": {"/c/hanko/aprendizados"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if rec.Header().Get("Location") != "/c/hanko/aprendizados?ok=exemplo" {
		t.Errorf("next: %q", rec.Header().Get("Location"))
	}
	// next de fora da empresa é ignorado
	rec = e.post(t, "/c/hanko/exemplos", url.Values{"source_kind": {"gate"}, "source_id": {uitest.ID("gate:x").String()}, "next": {"https://mal.example"}}, false)
	if loc := rec.Header().Get("Location"); strings.Contains(loc, "mal.example") {
		t.Errorf("open redirect: %q", loc)
	}
	rec = e.get(t, "/c/hanko/aprendizados?aba=exemplos")
	wantContains(t, rec, "gancho é obrigatório", "Salvar notas", "Revisão de case")
	rec = e.get(t, "/c/hanko/exemplos/export.jsonl")
	wantStatus(t, rec, http.StatusOK)
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/x-ndjson") {
		t.Errorf("content-type: %q", ct)
	}
	p.MyRoleIs = domain.MemberMember
	wantStatus(t, e.get(t, "/c/hanko/exemplos/export.jsonl"), http.StatusForbidden)
	// gate decidido oferece salvar como exemplo
	rec = e.get(t, "/c/hanko/gates/"+uitest.ID("gate:g4").String())
	if rec.Code == http.StatusOK {
		wantContains(t, rec, "Salvar como exemplo")
	}
}

func TestPipelinesEnglish(t *testing.T) {
	e, _ := newPipesEnv(t, domain.MemberOwner)
	for path, want := range map[string][]string{
		"/c/hanko/pipelines":                     {"Pipelines", "New pipeline", "Create pipeline"},
		"/c/hanko/pipelines/conteudo":            {"Board", "New item", "Create case"},
		"/c/hanko/pipelines/conteudo?aba=config": {"Transitions", "Save stage", "Strict mode"},
		"/c/hanko/cases":                         {"Cases", "New case", "Filter"},
		"/c/hanko/cases/HNK-C2":                  {"Approve and move to Publicado", "Request changes", "Reject", "History"},
		"/c/hanko/revisao":                       {"Review queue", "Approve selected"},
		"/c/hanko/aprendizados":                  {"Learnings", "Decision examples"},
	} {
		rec := e.get(t, path, "Accept-Language", "en")
		wantStatus(t, rec, http.StatusOK)
		wantContains(t, rec, want...)
	}
}
