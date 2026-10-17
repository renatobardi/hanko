//go:build integration

package ui_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Pipelines e cases na UI contra o service e o banco reais (ADR-0034): criar pelo formulário, arrastar no quadro,
// revisar, aprendizados, painel da task e aba da Caixa de entrada.
func TestPipelinesUIReal(t *testing.T) {
	u := newRealUI(t)
	ctx := context.Background()
	rec := u.form("/c/acme/pipelines", url.Values{"name": {"Vendas"}, "stages": {"Novo | working\nRevisão | review\nGanho | done\nPerdido | cancelled"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("criar pipeline: %d %s", rec.Code, rec.Body.String())
	}
	rec = u.form("/c/acme/pipelines/vendas/cases", url.Values{"title": {"Lead Acme"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("criar case: %d %s", rec.Code, rec.Body.String())
	}
	if rec = u.get("/c/acme/pipelines/vendas"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `data-case="HNK-C1"`) {
		t.Fatalf("quadro: %d", rec.Code)
	}
	// arrastar: HX com a versão do card
	rq := url.Values{"to_stage": {"revisao"}, "expected_version": {"1"}}
	rec = u.do("POST", "/c/acme/cases/HNK-C1/mover", strings.NewReader(rq.Encode()), "application/x-www-form-urlencoded")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("mover: %d %s", rec.Code, rec.Body.String())
	}
	if rec = u.get("/c/acme/revisao"); !strings.Contains(rec.Body.String(), "Lead Acme") {
		t.Fatalf("fila: %s", rec.Body.String())
	}
	if rec = u.get("/c/acme/caixa"); !strings.Contains(rec.Body.String(), `href="/c/acme/revisao"`) {
		t.Errorf("aba Revisão na Caixa de entrada")
	}
	rec = u.form("/c/acme/cases/HNK-C1/revisar", url.Values{"decision": {"request_changes"}, "expected_version": {"2"}})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("mudanças sem motivo: %d", rec.Code)
	}
	rec = u.form("/c/acme/cases/HNK-C1/revisar", url.Values{"decision": {"request_changes"}, "expected_version": {"2"}, "reason": {"falta o CNPJ"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("revisar: %d %s", rec.Code, rec.Body.String())
	}
	if rec = u.get("/c/acme/aprendizados"); !strings.Contains(rec.Body.String(), "falta o CNPJ") {
		t.Errorf("aprendizados")
	}
	rec = u.get("/c/acme/cases/HNK-C1")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "pediu mudanças e devolveu para Novo") || !strings.Contains(rec.Body.String(), "Salvar como exemplo") {
		t.Fatalf("case: %d", rec.Code)
	}
	// conversa: task ligada e painel na task
	rec = u.form("/c/acme/cases/HNK-C1/conversa", url.Values{})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("conversa: %d", rec.Code)
	}
	if rec = u.get(rec.Header().Get("Location")); !strings.Contains(rec.Body.String(), "HNK-C1") {
		t.Errorf("painel Cases na task")
	}
	cv, err := u.svc.GetCaseView(ctx, u.co.ID, "HNK-C1")
	if err != nil || cv.Version != 3 || cv.Status != domain.CaseInProgress {
		t.Fatalf("estado: %+v %v", cv.Case, err)
	}
	if _, err := u.svc.ListLearnings(ctx, u.co.ID, service.LearningFilter{}); err != nil {
		t.Fatal(err)
	}
}
