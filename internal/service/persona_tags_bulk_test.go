package service_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// O lote aplica a tag (criada na hora pelo nome) a várias personas, com um evento por persona alterada; quem já tinha a
// tag não conta nem gera evento. Tirar em lote faz o mesmo ao contrário (ADR-0087 item 5, fatia 2).
func TestBulkTagPersonas(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	ana, beto, caio := e.persona(t, "Ana", nil), e.persona(t, "Beto", nil), e.persona(t, "Caio", nil)
	res, err := e.svc.BulkTagPersonas(ctx, domain.HumanActor, e.co.ID, []domain.ID{ana.ID, beto.ID, ana.ID}, service.PersonaTagInput{Name: "Plantão", Color: domain.TagRed})
	if err != nil || res.Changed != 2 || res.Tag.Name != "Plantão" || res.Tag.Color != domain.TagRed {
		t.Fatalf("aplicar em lote: %+v %v", res, err)
	}
	res, err = e.svc.BulkTagPersonas(ctx, domain.HumanActor, e.co.ID, []domain.ID{ana.ID, caio.ID}, service.PersonaTagInput{Name: "PLANTÃO"})
	if err != nil || res.Changed != 1 {
		t.Fatalf("aplicar de novo, com a tag existente: %+v %v", res, err)
	}
	if got := e.searchPersonas(t, "plantao"); got != 3 {
		t.Fatalf("busca depois do lote = %d, quer 3", got)
	}
	n, _ := e.tagEvents(t)
	if n[domain.VerbPersonaTagged] != 3 || n[domain.VerbPersonaTagCreated] != 1 {
		t.Fatalf("eventos = %v, quer 3 tagged e 1 created", n)
	}

	res, err = e.svc.BulkUntagPersonas(ctx, domain.HumanActor, e.co.ID, []domain.ID{ana.ID, beto.ID}, res.Tag.ID)
	if err != nil || res.Changed != 2 {
		t.Fatalf("tirar em lote: %+v %v", res, err)
	}
	if got := e.searchPersonas(t, "plantao"); got != 1 {
		t.Fatalf("busca depois de tirar = %d, quer 1", got)
	}
	if n, _ := e.tagEvents(t); n[domain.VerbPersonaUntagged] != 2 {
		t.Fatalf("eventos = %v, quer 2 untagged", n)
	}
}

// Tudo ou nada: se uma persona do lote passaria do teto de 20, nenhuma recebe a tag, e a tag criada na hora também não
// fica. Tag arquivada não se aplica em lote, mas sai em lote.
func TestBulkTagPersonasTudoOuNada(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	ana, beto := e.persona(t, "Ana", nil), e.persona(t, "Beto", nil)
	for i := range domain.MaxTagsPerPersona {
		e.tagPersona(t, beto, e.tag(t, fmt.Sprintf("t%d", i)))
	}
	_, err := e.svc.BulkTagPersonas(ctx, domain.HumanActor, e.co.ID, []domain.ID{ana.ID, beto.ID}, service.PersonaTagInput{Name: "Nova"})
	wantErr(t, err, domain.ErrInvalid)
	if a, _ := e.svc.GetPersona(ctx, e.co.ID, ana.Slug); len(a.Tags) != 0 {
		t.Fatalf("a Ana recebeu a tag de um lote recusado: %+v", a.Tags)
	}
	if l, _ := e.svc.ListPersonaTags(ctx, e.co.ID, true); len(l) != domain.MaxTagsPerPersona {
		t.Fatalf("a tag do lote recusado ficou no cadastro: %d tags", len(l))
	}

	velha := e.tag(t, "Velha")
	e.tagPersona(t, ana, velha)
	if _, err := e.svc.ArchivePersonaTag(ctx, domain.HumanActor, e.co.ID, velha.ID); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.BulkTagPersonas(ctx, domain.HumanActor, e.co.ID, []domain.ID{beto.ID}, service.PersonaTagInput{Name: "velha"})
	wantErr(t, err, domain.ErrConflict)
	if res, err := e.svc.BulkUntagPersonas(ctx, domain.HumanActor, e.co.ID, []domain.ID{ana.ID, beto.ID}, velha.ID); err != nil || res.Changed != 1 {
		t.Fatalf("tirar arquivada em lote: %+v %v", res, err)
	}
}

// Lote vazio, persona de outra empresa, viewer e persona como ator são recusados.
func TestBulkTagPersonasRecusas(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	ana := e.persona(t, "Ana", nil)
	outra, err := e.svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Outra", Slug: "outra"})
	if err != nil {
		t.Fatal(err)
	}
	fora, err := e.svc.HirePersona(ctx, domain.HumanActor, outra.ID, service.HirePersonaInput{Name: "Fora", Agent: "fake", Model: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	in := service.PersonaTagInput{Name: "Lote"}
	_, err = e.svc.BulkTagPersonas(ctx, domain.HumanActor, e.co.ID, nil, in)
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.BulkTagPersonas(ctx, domain.HumanActor, e.co.ID, []domain.ID{ana.ID, fora.ID}, in)
	wantErr(t, err, domain.ErrNotFound)
	if a, _ := e.svc.GetPersona(ctx, e.co.ID, ana.Slug); len(a.Tags) != 0 {
		t.Fatalf("a Ana recebeu a tag de um lote recusado: %+v", a.Tags)
	}
	_, err = e.svc.BulkTagPersonas(ctx, domain.PersonaActor(ana.ID), e.co.ID, []domain.ID{ana.ID}, in)
	wantErr(t, err, domain.ErrForbidden)
	viewer := mkUser(t, e, "v@x.com", domain.RoleMember)
	join(t, e, viewer, domain.MemberViewer)
	_, err = e.svc.BulkTagPersonas(as(viewer), domain.HumanActor, e.co.ID, []domain.ID{ana.ID}, in)
	wantErr(t, err, domain.ErrForbidden)
	_, err = e.svc.BulkUntagPersonas(as(viewer), domain.HumanActor, e.co.ID, []domain.ID{ana.ID}, domain.NewID())
	wantErr(t, err, domain.ErrForbidden)
	big := make([]domain.ID, service.MaxBulkPersonas+1)
	for i := range big {
		big[i] = domain.NewID()
	}
	_, err = e.svc.BulkTagPersonas(ctx, domain.HumanActor, e.co.ID, big, in)
	wantErr(t, err, domain.ErrInvalid)
}
