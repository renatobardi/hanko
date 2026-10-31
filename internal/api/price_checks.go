package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/service"
)

// Conferência diária de preços (ADR-0082 item 7). A tabela de preços é de todas as empresas; qualquer membro lê.

func (s *server) registerPriceChecks(api huma.API) {
	const tag = "external-cost"
	b := "/companies/{company}/price-checks"
	huma.Register(api, op("list-price-checks", http.MethodGet, b, tag, "Lista as conferências de preços, da mais recente para a mais antiga"), s.listPriceChecks)
	huma.Register(api, op("get-price-check", http.MethodGet, b+"/{run}", tag, "Mostra uma conferência de preços com o resultado de cada modelo"), s.getPriceCheck)
}

func priceCheckOut(v service.PriceCheckView) PriceCheckRun {
	out := PriceCheckRun{ID: v.ID.String(), Reason: v.Reason, StartedAt: v.StartedAt, FinishedAt: v.FinishedAt, Sources: []PriceCheckSource{}}
	for _, s := range v.Sources {
		out.Sources = append(out.Sources, PriceCheckSource{Source: s.Source, Status: s.Status, ErrorCode: s.ErrorCode})
	}
	return out
}

func (s *server) listPriceChecks(ctx context.Context, in *listExternalNamedIn) (*ListOut[PriceCheckRun], error) {
	sc, err := s.externalScope(ctx, in.Company)
	if err != nil {
		return nil, err
	}
	rows, err := s.svc.ListPriceChecks(ctx, sc.Company.ID, in.Limit, in.Offset)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	items := make([]PriceCheckRun, len(rows))
	for i, r := range rows {
		items[i] = priceCheckOut(r)
	}
	return &ListOut[PriceCheckRun]{Body: ListBody[PriceCheckRun]{Items: items, Limit: in.Limit, Offset: in.Offset}}, nil
}

func (s *server) getPriceCheck(ctx context.Context, in *PriceCheckPath) (*ItemOut[PriceCheckDetail], error) {
	sc, err := s.externalScope(ctx, in.Company)
	if err != nil {
		return nil, err
	}
	id, err := parseIDParam("conferência", in.Run)
	if err != nil {
		return nil, err
	}
	v, err := s.svc.GetPriceCheck(ctx, sc.Company.ID, id)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	results := make([]PriceCheckResult, len(v.Results))
	for i, r := range v.Results {
		results[i] = PriceCheckResult{Model: r.Model, Source: r.Source, Status: r.Status, InputPerMTok: r.Input, OutputPerMTok: r.Output,
			CacheReadPerMTok: r.CacheRead, CacheCreationPerMTok: r.CacheCreation, PriceID: idPtrStr(r.PriceID)}
	}
	return &ItemOut[PriceCheckDetail]{Body: PriceCheckDetail{PriceCheckRun: priceCheckOut(v), Results: results}}, nil
}
