package mcp

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// cardFake acrescenta ao fake os cards de status (ADR-0032).
type cardFake struct {
	*fake
	cards     []service.StatusCardView
	lastRef   service.CardRunRef
	lastCard  domain.ID
	lastQuery service.SetCardQueryInput
	lastSum   service.SetCardSummaryInput
	lastNew   service.CreateStatusCardInput
	lastActor domain.Actor
}

func (c *cardFake) CreateStatusCard(_ context.Context, a domain.Actor, _ domain.ID, in service.CreateStatusCardInput) (service.StatusCardView, error) {
	c.lastActor, c.lastNew = a, in
	v := service.StatusCardView{StatusCard: domain.StatusCard{ID: domain.NewID(), Interest: in.Interest, State: domain.CardCompiling}, ViewState: domain.CardViewCompiling}
	c.cards = append(c.cards, v)
	return v, nil
}

func (c *cardFake) ListPersonaStatusCards(_ context.Context, _, _ domain.ID, _ *domain.ID) ([]service.StatusCardView, error) {
	return c.cards, nil
}

func (c *cardFake) SetStatusCardQuery(_ context.Context, a domain.Actor, _ domain.ID, ref service.CardRunRef, card domain.ID, in service.SetCardQueryInput) (service.StatusCardView, error) {
	c.lastActor, c.lastRef, c.lastCard, c.lastQuery = a, ref, card, in
	if len(in.Queries) == 0 {
		return service.StatusCardView{}, domain.Invalid("queries", "de 1 a 10 filtros")
	}
	return service.StatusCardView{StatusCard: domain.StatusCard{ID: card, QueryVersion: 1, Queries: make([]domain.StatusCardQuery, len(in.Queries))}}, nil
}

func (c *cardFake) SetStatusCardSummary(_ context.Context, a domain.Actor, _ domain.ID, ref service.CardRunRef, card domain.ID, in service.SetCardSummaryInput) (service.StatusCardView, []string, error) {
	c.lastActor, c.lastRef, c.lastCard, c.lastSum = a, ref, card, in
	return service.StatusCardView{StatusCard: domain.StatusCard{ID: card, SummaryRevision: 3, MentionedTaskIDs: []domain.ID{domain.NewID()}}}, []string{"XYZ-1"}, nil
}

func cardSetup(t *testing.T) (*env, *cardFake) {
	t.Helper()
	e := setup(t, false)
	cf := &cardFake{fake: e.f}
	srv := httptest.NewServer(newHandler(cf, Deps{Tokens: e.tokens, Version: "test"}))
	t.Cleanup(srv.Close)
	e.srv = srv
	return e, cf
}

func TestStatusCardToolsUseTheRunToken(t *testing.T) {
	e, cf := cardSetup(t)
	taskID := e.f.tasks[0].ID
	gen := service.StatusCardView{StatusCard: domain.StatusCard{ID: domain.NewID(), Title: "Bloqueios", GeneratingTaskID: &taskID}, ViewState: domain.CardViewUpdating}
	cf.cards = []service.StatusCardView{gen}
	cs, err := e.connect(t, e.tok)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	// sem card: o da geração da execução
	res := call(t, cs, "hanko_status_card_set_query", map[string]any{"queries": []map[string]any{{"status": []string{"blocked"}, "project": "site", "open": true}},
		"title": "Bloqueios do Site"})
	var out cardWriteOut
	structured(t, res, &out)
	if res.IsError || cf.lastCard != gen.ID || cf.lastRef.TaskID != taskID || cf.lastRef.RunID == nil || *cf.lastRef.RunID != e.runID ||
		cf.lastRef.PersonaID != e.f.personas[1].ID || cf.lastActor.Type != domain.ActorPersona || len(cf.lastQuery.Queries) != 1 ||
		cf.lastQuery.Queries[0].Project != "site" || !cf.lastQuery.Queries[0].OpenOnly || cf.lastQuery.Title != "Bloqueios do Site" {
		t.Fatalf("set_query: %v %s %+v %+v", res.IsError, text(res), cf.lastRef, cf.lastQuery)
	}
	if !strings.Contains(text(res), "versão 1") {
		t.Fatalf("texto: %s", text(res))
	}
	// erro de validação vira erro da tool
	res = call(t, cs, "hanko_status_card_set_query", map[string]any{"card": gen.ID.String(), "queries": []any{}})
	if !res.IsError || !strings.Contains(text(res), "entrada inválida") {
		t.Fatalf("filtro vazio: %s", text(res))
	}
	res = call(t, cs, "hanko_status_card_set_summary", map[string]any{"card": gen.ID.String(), "markdown": "# ok", "mentioned": []string{"HNK-1", "XYZ-1"}})
	if res.IsError || cf.lastSum.Markdown != "# ok" || len(cf.lastSum.Mentioned) != 2 || !strings.Contains(text(res), "revisão 3") || !strings.Contains(text(res), "XYZ-1") {
		t.Fatalf("set_summary: %s %+v", text(res), cf.lastSum)
	}
	res = call(t, cs, "hanko_status_card_set_summary", map[string]any{"card": "nao-uuid", "markdown": "x"})
	if !res.IsError {
		t.Fatal("card inválido deveria falhar")
	}

	var list cardListOut
	res = call(t, cs, "hanko_status_card_list", map[string]any{})
	structured(t, res, &list)
	if len(list.Cards) != 1 || !list.Cards[0].Generation || !strings.Contains(text(res), "geração da sua execução") {
		t.Fatalf("list: %+v %s", list, text(res))
	}

	res = call(t, cs, "hanko_status_card_create", map[string]any{"interest": "bugs críticos", "summarizer": "cto", "mode": "manual"})
	if res.IsError || cf.lastNew.Interest != "bugs críticos" || cf.lastNew.SummarizerPersonaID == nil || *cf.lastNew.SummarizerPersonaID != e.f.personas[0].ID ||
		cf.lastNew.Policy == nil || cf.lastNew.Policy.Mode != "manual" {
		t.Fatalf("create: %s %+v", text(res), cf.lastNew)
	}
}

func TestStatusCardToolsNeedAPersona(t *testing.T) {
	e, _ := cardSetup(t)
	trusted := httptest.NewServer(newHandler(&cardFake{fake: e.f}, Deps{Tokens: e.tokens, Trusted: true}))
	t.Cleanup(trusted.Close)
	e.srv = trusted
	cs, err := e.connect(t, "")
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res := call(t, cs, "hanko_status_card_list", map[string]any{"company": "acme"})
	if !res.IsError || !strings.Contains(text(res), "token de run") {
		t.Fatalf("sem persona: %s", text(res))
	}
}
