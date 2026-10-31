package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/service"
)

// Histórico de mensalidade de uma assinatura (ADR-0082 item 6).

func (s *server) registerSubscriptionHistory(api huma.API) {
	const tag = "external-cost"
	path := "/companies/{company}/subscriptions/{subscription}/monthly-history"
	huma.Register(api, op("list-subscription-monthly-history", http.MethodGet, path, tag, "Lista o histórico de mensalidade de uma assinatura"),
		s.listMonthlyHistory)
	huma.Register(api, created(op("add-subscription-monthly-value", http.MethodPost, path, tag,
		"Acrescenta uma linha ao histórico de mensalidade (owner ou admin, com reautenticação recente)")), s.addMonthlyValue)
}

func monthlyValueOut(v service.SubscriptionMonthlyValueView) SubscriptionMonthlyValue {
	out := SubscriptionMonthlyValue{ID: v.ID.String(), MonthlyUSD: v.MonthlyUSD, StartsOn: v.StartsOn.UTC().Format("2006-01-02"),
		RecordedAt: v.RecordedAt, Origin: v.Origin, Actor: ActorRef{Type: v.ActorType}}
	if v.ActorID != nil {
		out.Actor.ID = v.ActorID.String()
	}
	return out
}

func (s *server) listMonthlyHistory(ctx context.Context, in *SubscriptionPath) (*ListOut[SubscriptionMonthlyValue], error) {
	sc, err := s.externalScope(ctx, in.Company)
	if err != nil {
		return nil, err
	}
	rows, err := s.svc.ListSubscriptionMonthlyHistory(ctx, sc.Company.ID, in.Subscription)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	items := make([]SubscriptionMonthlyValue, len(rows))
	for i, r := range rows {
		items[i] = monthlyValueOut(r)
	}
	return &ListOut[SubscriptionMonthlyValue]{Body: ListBody[SubscriptionMonthlyValue]{Items: items, Limit: len(items)}}, nil
}

func (s *server) addMonthlyValue(ctx context.Context, in *addMonthlyValueIn) (*ItemOut[SubscriptionMonthlyValue], error) {
	sc, err := s.externalScope(ctx, in.Company)
	if err != nil {
		return nil, err
	}
	startsOn, perr := time.Parse("2006-01-02", in.Body.StartsOn)
	if perr != nil {
		return nil, problem(http.StatusUnprocessableEntity, "starts_on não é uma data (AAAA-MM-DD)")
	}
	v, err := s.svc.AddSubscriptionMonthlyValue(ctx, sc.Actor, sc.Company.ID, in.Subscription, in.Body.MonthlyUSD, startsOn)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	return &ItemOut[SubscriptionMonthlyValue]{Body: monthlyValueOut(v)}, nil
}
