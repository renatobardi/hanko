//go:build integration

package api_test

import (
	"context"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/service"
)

// Tags de persona pela API (ADR-0087): cadastro, aplicar, criar e aplicar, recorte do organograma, arquivar e deletar.
func TestAPIPersonaTags(t *testing.T) {
	e := newAPIEnv(t)
	b := e.base()
	ceo := e.persona(e.co, "Ceo", nil)
	e.persona(e.co, "Eng", func(in *service.HirePersonaInput) { in.ReportsTo = &ceo.ID })

	var tag api.PersonaTag
	e.want(201, "POST", b+"/persona-tags", `{"name":" Backend ","color":"blue","description":"servidor"}`, &tag)
	if tag.Name != "Backend" || tag.Color != "blue" || tag.Archived || tag.PersonaCount != 0 {
		t.Fatalf("criada: %+v", tag)
	}
	e.want(409, "POST", b+"/persona-tags", `{"name":"BACKEND"}`, nil)
	e.want(422, "POST", b+"/persona-tags", `{"name":"x","color":"#ff0000"}`, nil)

	e.want(204, "PUT", b+"/personas/eng/tags/"+tag.ID, "", nil)
	var ref api.PersonaTagRef
	e.want(201, "POST", b+"/personas/ceo/tags", `{"name":"Gestão","color":"amber"}`, &ref)
	if ref.Name != "Gestão" || ref.Color != "amber" {
		t.Fatalf("criar e aplicar: %+v", ref)
	}

	var eng api.Persona
	e.want(200, "GET", b+"/personas/eng", "", &eng)
	if len(eng.Tags) != 1 || eng.Tags[0].ID != tag.ID || eng.Tags[0].Color != "blue" {
		t.Fatalf("tags na persona: %+v", eng.Tags)
	}

	var got api.PersonaTag
	e.want(200, "PATCH", b+"/persona-tags/"+tag.ID, `{"name":"Servidor"}`, &got)
	if got.ID != tag.ID || got.Name != "Servidor" || got.PersonaCount != 1 {
		t.Fatalf("editada: %+v", got)
	}

	var page api.ListBody[api.OrgPersona]
	e.want(200, "GET", b+"/org-chart/personas?tag="+tag.ID+"&level=2", "", &page)
	if len(page.Items) != 1 || page.Items[0].Persona.Slug != "eng" || page.Items[0].Level != 2 || len(page.Items[0].Tags) != 1 {
		t.Fatalf("recorte: %+v", page.Items)
	}
	e.want(200, "GET", b+"/org-chart/personas?untagged=true", "", &page)
	if len(page.Items) != 0 {
		t.Fatalf("sem tag: %+v", page.Items)
	}
	e.want(422, "GET", b+"/org-chart/personas?level=x", "", nil)
	e.want(422, "GET", b+"/org-chart/personas?tag=nao-e-uuid", "", nil)

	e.want(200, "POST", b+"/persona-tags/"+tag.ID+"/archive", "", &got)
	if !got.Archived || got.ArchivedAt == nil {
		t.Fatalf("arquivada: %+v", got)
	}
	var list api.ListBody[api.PersonaTag]
	e.want(200, "GET", b+"/persona-tags", "", &list)
	if len(list.Items) != 1 || list.Items[0].Name != "Gestão" {
		t.Fatalf("lista padrão: %+v", list.Items)
	}
	e.want(200, "GET", b+"/persona-tags?archived=true", "", &list)
	if len(list.Items) != 2 {
		t.Fatalf("lista com arquivadas: %+v", list.Items)
	}
	e.want(409, "PUT", b+"/personas/ceo/tags/"+tag.ID, "", nil)
	e.want(200, "POST", b+"/persona-tags/"+tag.ID+"/unarchive", "", &got)
	e.want(204, "DELETE", b+"/personas/eng/tags/"+tag.ID, "", nil)
	e.want(204, "DELETE", b+"/persona-tags/"+tag.ID, "", nil)
	e.want(404, "GET", b+"/persona-tags/"+tag.ID, "", nil)
}

// Um run token lê as tags, mas não cria nem aplica (ADR-0087 item 9).
func TestAPIPersonaTagsRunToken(t *testing.T) {
	e := newAPIEnv(t)
	b := e.base()
	p := e.persona(e.co, "Ana", nil)
	mine := e.task(e.co, "minha", &p, nil)
	run, err := e.svc.CheckoutRun(context.Background(), wakeOf(p, mine))
	if err != nil {
		t.Fatal(err)
	}
	tok, err := e.tokens.Issue(e.co.ID, p.ID, run.ID, mine.ID)
	if err != nil {
		t.Fatal(err)
	}
	hdr := map[string]string{"Authorization": "Bearer " + tok}
	if st, body := e.call("GET", b+"/persona-tags", "", hdr); st != 200 {
		t.Fatalf("persona lê: %d %s", st, body)
	}
	if st, body := e.call("POST", b+"/persona-tags", `{"name":"Agente"}`, hdr); st != 403 || !strings.Contains(string(body), "run tokens") {
		t.Fatalf("persona cria: %d %s", st, body)
	}
	if st, body := e.call("POST", b+"/personas/ana/tags", `{"name":"Agente"}`, hdr); st != 403 {
		t.Fatalf("persona aplica: %d %s", st, body)
	}
}
