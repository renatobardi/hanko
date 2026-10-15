//go:build integration

package api_test

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/domain"
)

// Revisor automático da persona: outra persona ou o mesmo papel em harness/modelo alternativo,
// configurado pela API na contratação e depois. Um só tipo por vez, nunca a própria autora.
func TestAPIRevisorDaPersona(t *testing.T) {
	e := newAPIEnv(t)
	ps := e.base() + "/personas"
	var kenji, ada api.Persona
	e.want(201, "POST", ps, `{"name":"Kenji","agent":"codex","model":"gpt-5"}`, &kenji)
	e.want(201, "POST", ps, `{"name":"Ada","agent":"claude","model":"claude-sonnet-4-5","reviewer":"kenji","require_review":true}`, &ada)
	if ada.ReviewerPersonaID != kenji.ID || ada.ReviewerAgent != "" || ada.ReviewerModel != "" || !ada.RequireReview {
		t.Fatalf("contratada com revisor persona: %+v", ada)
	}

	// trocar por harness alternativo substitui a persona revisora
	var got api.Persona
	e.want(200, "PATCH", ps+"/ada", `{"reviewer_agent":"codex","reviewer_model":"gpt-5"}`, &got)
	if got.ReviewerPersonaID != "" || got.ReviewerAgent != "codex" || got.ReviewerModel != "gpt-5" {
		t.Fatalf("harness alternativo: %+v", got)
	}
	// só o modelo muda, harness fica
	e.want(200, "PATCH", ps+"/ada", `{"reviewer_model":"gpt-5-mini"}`, &got)
	if got.ReviewerAgent != "codex" || got.ReviewerModel != "gpt-5-mini" {
		t.Fatalf("só o modelo: %+v", got)
	}
	// uma persona revisora volta a substituir o harness alternativo
	e.want(200, "PATCH", ps+"/ada", `{"reviewer":"`+kenji.Slug+`"}`, &got)
	if got.ReviewerPersonaID != kenji.ID || got.ReviewerAgent != "" || got.ReviewerModel != "" {
		t.Fatalf("persona revisora: %+v", got)
	}
	// leitura reflete o que foi gravado
	e.want(200, "GET", ps+"/"+ada.ID, "", &got)
	if got.ReviewerPersonaID != kenji.ID {
		t.Fatalf("get: %+v", got)
	}
	// remover o revisor
	e.want(200, "PATCH", ps+"/ada", `{"clear_reviewer":true}`, &got)
	if got.ReviewerPersonaID != "" || got.ReviewerAgent != "" || got.ReviewerModel != "" {
		t.Fatalf("clear_reviewer: %+v", got)
	}

	bad := map[string]string{
		"ela mesma":                       `{"reviewer":"ada"}`,
		"persona e harness juntos":        `{"reviewer":"kenji","reviewer_agent":"codex"}`,
		"harness igual ao da autora":      `{"reviewer_agent":"claude","reviewer_model":"claude-sonnet-4-5"}`,
		"modelo sem harness":              `{"reviewer_model":"gpt-5"}`,
		"harness desconhecido":            `{"reviewer_agent":"gemini"}`,
		"limpar e definir ao mesmo tempo": `{"clear_reviewer":true,"reviewer":"kenji"}`,
	}
	for name, body := range bad {
		if status, b := e.call("PATCH", ps+"/ada", body, nil); status != http.StatusUnprocessableEntity {
			t.Errorf("patch %s: status %d, quer 422: %s", name, status, b)
		}
	}
	// revisor que não existe na empresa: 404 (referência não resolvida)
	if status, b := e.call("PATCH", ps+"/ada", `{"reviewer":"fantasma"}`, nil); status != http.StatusNotFound {
		t.Errorf("revisor inexistente: status %d: %s", status, b)
	}
	// o mesmo vale na contratação
	for name, body := range map[string]string{
		"persona e harness juntos":   `{"name":"X1","agent":"claude","reviewer":"kenji","reviewer_agent":"codex"}`,
		"modelo sem harness":         `{"name":"X2","agent":"claude","reviewer_model":"gpt-5"}`,
		"harness igual ao da autora": `{"name":"X3","agent":"claude","model":"m","reviewer_agent":"claude","reviewer_model":"m"}`,
		"harness desconhecido":       `{"name":"X4","agent":"claude","reviewer_agent":"gemini"}`,
	} {
		if status, b := e.call("POST", ps, body, nil); status != http.StatusUnprocessableEntity {
			t.Errorf("hire %s: status %d, quer 422: %s", name, status, b)
		}
	}
	if status, _ := e.call("POST", ps, `{"name":"X5","agent":"claude","reviewer":"fantasma"}`, nil); status != http.StatusNotFound {
		t.Errorf("hire com revisor inexistente: %d", status)
	}
	var list api.ListBody[api.Persona]
	e.want(200, "GET", ps, "", &list)
	if len(list.Items) != 2 {
		t.Fatalf("contratações inválidas não podem criar persona: %d", len(list.Items))
	}

	// o revisor é da empresa: persona de outra empresa não vale
	other := e.company("Beta", "beta")
	e.persona(other, "Estranha", nil)
	if status, _ := e.call("PATCH", ps+"/ada", `{"reviewer":"estranha"}`, nil); status != http.StatusNotFound {
		t.Errorf("revisor de outra empresa: %d", status)
	}

	// persona (run token) não configura revisor, nem o dela
	adaPersona, err := e.svc.GetPersona(context.Background(), e.co.ID, "ada")
	if err != nil {
		t.Fatal(err)
	}
	h := e.personaToken(e.co, adaPersona)
	if status, b := e.call("PATCH", ps+"/ada", `{"reviewer":"kenji"}`, h); status != http.StatusForbidden {
		t.Errorf("persona configurando o próprio revisor: status %d: %s", status, b)
	}
	e.want(200, "GET", ps+"/ada", "", &got)
	if got.ReviewerPersonaID != "" {
		t.Fatalf("o revisor não pode ter mudado: %+v", got)
	}
	if v := e.activityVerbs(); v["persona.updated"] != 4 {
		t.Fatalf("cada mudança de revisor é um fato de domínio: %v", v)
	}
}

// Nome amigável da execução: Adjetivo_Substantivo, único na empresa, aceito como filtro (sem
// diferenciar caixa nem acento) e invisível a outras empresas.
func TestAPINomeAmigavelDeExecucao(t *testing.T) {
	e := newAPIEnv(t)
	p := e.persona(e.co, "Eng", nil)
	var runs []domain.Run
	for range 4 {
		runs = append(runs, e.spend(p, time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC), usdRes(1)))
	}
	shape := regexp.MustCompile(`^\p{L}+_\p{L}+(_[1-9][0-9]{2})?$`)
	names := map[string]bool{}
	for _, r := range runs {
		var got api.Run
		e.want(200, "GET", e.base()+"/runs/"+r.ID.String(), "", &got)
		if got.Name == "" || !shape.MatchString(got.Name) {
			t.Fatalf("nome inválido: %q", got.Name)
		}
		if names[got.Name] {
			t.Fatalf("nome repetido na empresa: %q", got.Name)
		}
		names[got.Name] = true
	}

	target := runs[2]
	var tr api.Run
	e.want(200, "GET", e.base()+"/runs/"+target.ID.String(), "", &tr)
	variants := []string{tr.Name, strings.ToLower(tr.Name), strings.ToUpper(tr.Name), fold(tr.Name)}
	for _, v := range variants {
		var list api.ListBody[api.Run]
		e.want(200, "GET", e.base()+"/runs?name="+esc(v), "", &list)
		if len(list.Items) != 1 || list.Items[0].ID != target.ID.String() || list.Items[0].Name != tr.Name {
			t.Errorf("filtro name=%q: %+v", v, list.Items)
		}
	}
	// combina com os outros filtros e não acha o que não existe
	var list api.ListBody[api.Run]
	e.want(200, "GET", e.base()+"/runs?name="+esc(tr.Name)+"&status=failed", "", &list)
	if len(list.Items) != 0 {
		t.Errorf("name + status errado: %+v", list.Items)
	}
	e.want(200, "GET", e.base()+"/runs?name="+esc(tr.Name)+"&persona=eng&status=done", "", &list)
	if len(list.Items) != 1 {
		t.Errorf("name + persona + status: %+v", list.Items)
	}
	e.want(200, "GET", e.base()+"/runs?name=Inexistente_Nome", "", &list)
	if len(list.Items) != 0 {
		t.Errorf("nome que não existe: %+v", list.Items)
	}
	e.want(200, "GET", e.base()+"/runs", "", &list)
	if len(list.Items) != 4 {
		t.Errorf("sem filtro devolve todas: %d", len(list.Items))
	}
	// o nome de outra empresa não aparece aqui
	e.company("Beta", "beta")
	e.want(200, "GET", "/api/v1/companies/beta/runs?name="+esc(tr.Name), "", &list)
	if len(list.Items) != 0 {
		t.Errorf("nome vazou para outra empresa: %+v", list.Items)
	}
}

func fold(s string) string {
	return strings.NewReplacer("Á", "A", "á", "a", "Ã", "A", "ã", "a", "É", "E", "é", "e", "Í", "I", "í", "i", "Ó", "O", "ó", "o",
		"Ô", "O", "ô", "o", "Ú", "U", "ú", "u", "Ç", "C", "ç", "c", "Â", "A", "â", "a", "Ê", "E", "ê", "e").Replace(s)
}
