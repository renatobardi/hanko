//go:build integration

package ui_test

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// UI dos pacotes de time (ADR-0044): seção no Catálogo de times, prévia, passo de assinaturas, instalação, exportação e
// envio de um arquivo.
func TestUITeamPackages(t *testing.T) {
	u := newRealUI(t)
	ctx := context.Background()
	const cat = "/c/acme/personas/catalogo"

	rec := u.get(cat)
	mustContain(t, rec, 200, "Pacotes de time", "Mesa de conteúdo", "Squad de produto", cat+"/pacote/mesa-de-conteudo")

	// prévia: nada gravado, itens e o seletor de assinatura
	rec = u.get(cat + "/pacote/mesa-de-conteudo")
	mustContain(t, rec, 200, "O que entra", "editora", "Harness e assinaturas", "Instalar pausado", "guia-de-estilo")
	if ps, _ := u.svc.ListPersonas(ctx, u.co.ID); len(ps) != 0 {
		t.Fatalf("a prévia gravou %d personas", len(ps))
	}
	if rec = u.get(cat + "/pacote/nao-existe"); rec.Code != 404 {
		t.Fatalf("pacote inexistente: %d", rec.Code)
	}

	// com assinatura da empresa escolhida
	if _, err := u.svc.CreateSubscription(ctx, domain.HumanActor, u.co.ID, service.CreateSubscriptionInput{Name: "Plano Claude", Agent: "claude", MonthlyUSD: 100}); err != nil {
		t.Fatal(err)
	}
	rec = u.form(cat+"/pacote/mesa-de-conteudo/previa", url.Values{"on_conflict": {"skip"}, "sub:claude": {"Plano Claude"}})
	mustContain(t, rec, 200, "Plano Claude", "O que entra")

	rec = u.form(cat+"/pacote/mesa-de-conteudo/instalar", url.Values{"on_conflict": {"skip"}, "sub:claude": {"Plano Claude"}})
	mustContain(t, rec, 200, "Pacote instalado", "Nada trabalha até você liberar")
	ps, _ := u.svc.ListPersonas(ctx, u.co.ID)
	if len(ps) != 3 {
		t.Fatalf("personas = %d", len(ps))
	}
	for _, p := range ps {
		if p.Status != domain.PersonaPaused || p.SubscriptionID == nil {
			t.Errorf("%s: %s %v", p.Slug, p.Status, p.SubscriptionID)
		}
	}

	// segunda instalação: tudo existe, o seletor por item aparece e renomear funciona
	rec = u.get(cat + "/pacote/mesa-de-conteudo")
	mustContain(t, rec, 200, `name="r:personas/editora"`)
	rec = u.form(cat+"/pacote/mesa-de-conteudo/instalar", url.Values{"on_conflict": {"rename"}})
	mustContain(t, rec, 200, "editora-2")
	if ps, _ := u.svc.ListPersonas(ctx, u.co.ID); len(ps) != 6 {
		t.Fatalf("personas = %d", len(ps))
	}

	// a lista de instalações aparece na seção
	rec = u.get(cat)
	mustContain(t, rec, 200, "Pacote instalado", "Mesa de conteúdo")

	// exportar baixa um JSON hanko.team.v1
	rec = u.get(cat + "/exportar?personas=editora&personas=redatora&name=Dupla")
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Disposition"), "dupla.team.json") || !strings.Contains(rec.Body.String(), `"hanko.team.v1"`) {
		t.Fatalf("exportar: %d %s %q", rec.Code, rec.Header().Get("Content-Disposition"), rec.Body.String()[:min(120, rec.Body.Len())])
	}
	exported := rec.Body.Bytes()

	// enviar um arquivo: prévia a partir do cache e instalação
	rec = u.upload(cat+"/arquivo", nil, "dupla.team.json", exported)
	loc := rec.Header().Get("Location")
	if rec.Code != 303 || !strings.HasPrefix(loc, cat+"/arquivo/") {
		t.Fatalf("upload: %d %q %s", rec.Code, loc, rec.Body.String())
	}
	rec = u.get(loc)
	mustContain(t, rec, 200, "Dupla", "arquivo enviado")
	rec = u.form(loc+"/instalar", url.Values{"on_conflict": {"rename"}})
	mustContain(t, rec, 200, "Pacote instalado")

	// arquivo inválido e token inexistente
	if rec = u.upload(cat+"/arquivo", nil, "x.json", []byte(`{"format":"hanko.company.v1"}`)); rec.Code != 422 {
		t.Fatalf("arquivo inválido: %d", rec.Code)
	}
	if rec = u.get(cat + "/arquivo/token-inexistente"); rec.Code != 422 {
		t.Fatalf("token inexistente: %d", rec.Code)
	}
}
