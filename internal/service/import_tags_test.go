package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/export"
	"github.com/renatobardi/hanko/internal/service"
)

// Export e import levam o cadastro (com as arquivadas) e as ligações (ADR-0087 item 11). Na empresa nova as tags são
// recriadas; no merge, uma tag de mesmo nome sem caixa é reaproveitada, e a busca acha a persona pela tag.
func TestExportImportPersonaTags(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	ana := e.persona(t, "Ana", nil)
	back := e.tag(t, "Backend")
	legado := e.tag(t, "Legado")
	e.tagPersona(t, ana, back)
	e.tagPersona(t, ana, legado)
	if _, err := e.svc.ArchivePersonaTag(ctx, domain.HumanActor, e.co.ID, legado.ID); err != nil {
		t.Fatal(err)
	}
	doc, err := e.svc.ExportCompany(ctx, e.co.ID, service.ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.PersonaTags) != 2 || !doc.PersonaTags[1].Archived || !slices.Equal(doc.Personas[0].Tags, []string{"Backend", "Legado"}) {
		t.Fatalf("export: %+v %+v", doc.PersonaTags, doc.Personas[0].Tags)
	}

	res, err := e.svc.ImportCompany(ctx, domain.HumanActor, doc, service.ImportOptions{Slug: "copia"})
	if err != nil || res.Counts["persona_tags"] != 2 {
		t.Fatalf("import: %+v %v", res.Counts, err)
	}
	tags, err := e.svc.ListPersonaTags(ctx, res.Company.ID, true)
	if err != nil || len(tags) != 2 || tags[0].PersonaCount != 1 || !tags[1].Archived() {
		t.Fatalf("tags da cópia: %+v %v", tags, err)
	}
	if hits := searchIn(t, e, res.Company.ID, "backend"); hits != 1 {
		t.Fatalf("busca na cópia = %d, quer 1", hits)
	}
	importEvents(t, e, res.Company.ID, 2, 2)

	// merge numa empresa que já tem "BACKEND": a tag é a mesma, sem duplicata
	dest, err := e.svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Destino", Slug: "destino"})
	if err != nil {
		t.Fatal(err)
	}
	existing, err := e.svc.CreatePersonaTag(ctx, domain.HumanActor, dest.ID, service.PersonaTagInput{Name: "BACKEND", Color: domain.TagRed})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ImportCompany(ctx, domain.HumanActor, doc, service.ImportOptions{Into: &dest.ID}); err != nil {
		t.Fatal(err)
	}
	tags, _ = e.svc.ListPersonaTags(ctx, dest.ID, true)
	if len(tags) != 2 || tags[0].ID != existing.ID || tags[0].Color != domain.TagRed || tags[0].PersonaCount != 1 {
		t.Fatalf("tags do destino: %+v", tags)
	}
}

// O arquivo com tag desconhecida, repetida ou fora da paleta é recusado antes de gravar.
func TestImportPersonaTagsInvalidas(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.persona(t, "Ana", nil)
	doc, err := e.svc.ExportCompany(ctx, e.co.ID, service.ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for name, mod := range map[string]func(d *export.Document){
		"desconhecida": func(d *export.Document) { d.Personas[0].Tags = []string{"nada"} },
		"repetida":     func(d *export.Document) { d.PersonaTags = []export.PersonaTag{{Name: "a"}, {Name: "A"}} },
		"cor":          func(d *export.Document) { d.PersonaTags = []export.PersonaTag{{Name: "a", Color: "#fff"}} },
		"nome vazio":   func(d *export.Document) { d.PersonaTags = []export.PersonaTag{{Name: " "}} },
	} {
		d := doc
		d.Personas = slices.Clone(doc.Personas)
		mod(&d)
		_, err := e.svc.ImportCompany(ctx, domain.HumanActor, d, service.ImportOptions{Slug: "x", DryRun: true})
		if !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s: %v, quer ErrInvalid", name, err)
		}
	}
}

func searchIn(t *testing.T, e *env, cid domain.ID, q string) int {
	t.Helper()
	res, err := e.svc.Search(context.Background(), cid, q, service.SearchFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range res.Groups {
		if g.Type == service.SearchPersona {
			return len(g.Hits)
		}
	}
	return 0
}

// importEvents confere os eventos que o import gravou na empresa: um persona_tag.created por tag criada e um
// persona.tagged por ligação, com o ator system e origin=import (ADR-0087 item 12).
func importEvents(t *testing.T, e *env, cid domain.ID, created, tagged int) {
	t.Helper()
	n := map[string]int{}
	for _, st := range []string{"persona_tag", "persona"} {
		acts, err := e.svc.ListActivity(context.Background(), cid, service.ActivityFilter{SubjectType: st, Limit: 200})
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range acts {
			var data struct {
				Origin string `json:"origin"`
			}
			if json.Unmarshal(a.Data, &data) == nil && data.Origin == "import" && a.Actor.Type == domain.ActorSystem {
				n[a.Verb]++
			}
		}
	}
	if n[domain.VerbPersonaTagCreated] != created || n[domain.VerbPersonaTagged] != tagged {
		t.Fatalf("eventos do import = %v, quer %d created e %d tagged", n, created, tagged)
	}
}
