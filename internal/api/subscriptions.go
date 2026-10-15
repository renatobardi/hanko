package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

type SubscriptionPath struct {
	CompanyPath
	Subscription string `path:"subscription" doc:"Id ou nome da assinatura." example:"Claude Max"`
}

type createSubscriptionIn struct {
	CompanyPath
	Body struct {
		Name           string   `json:"name" minLength:"1" maxLength:"120" example:"Claude Max"`
		Agent          string   `json:"agent" enum:"claude,codex,fake" example:"claude" doc:"Harness/provedor do plano."`
		MonthlyUSD     float64  `json:"monthly_usd" minimum:"0" example:"200" doc:"Mensalidade em dólares."`
		ModelPrefixes  []string `json:"model_prefixes,omitempty" doc:"Prefixos de modelo que o plano cobre; vazio = todos os modelos do harness."`
		WeeklyLimitUSD *float64 `json:"weekly_limit_usd,omitempty" exclusiveMinimum:"0" doc:"Teto semanal de uso estimado (custo de lista), em dólares; ausente = sem trava."`
		WeeklyGuardPct int      `json:"weekly_guard_pct,omitempty" minimum:"1" maximum:"100" doc:"Trava semanal: a partir deste percentual do teto, novas execuções ficam bloqueadas. Padrão 90."`
		Inactive       bool     `json:"inactive,omitempty" doc:"Cria a assinatura desligada."`
	}
}

type updateSubscriptionIn struct {
	SubscriptionPath
	Body struct {
		Name             *string   `json:"name,omitempty" minLength:"1"`
		Agent            *string   `json:"agent,omitempty" enum:"claude,codex,fake"`
		MonthlyUSD       *float64  `json:"monthly_usd,omitempty" minimum:"0"`
		ModelPrefixes    *[]string `json:"model_prefixes,omitempty" doc:"Substitui a lista de prefixos."`
		WeeklyLimitUSD   *float64  `json:"weekly_limit_usd,omitempty" exclusiveMinimum:"0"`
		ClearWeeklyLimit bool      `json:"clear_weekly_limit,omitempty" doc:"Remove o teto semanal (e a trava)."`
		WeeklyGuardPct   *int      `json:"weekly_guard_pct,omitempty" minimum:"1" maximum:"100"`
		Active           *bool     `json:"active,omitempty" doc:"Liga ou desliga a assinatura."`
	}
}

type listSubscriptionsIn struct {
	CompanyPath
	Page
}

func (s *server) registerSubscriptions(api huma.API) {
	huma.Register(api, created(op("create-subscription", http.MethodPost, "/companies/{company}/subscriptions", "subscriptions", "Cria uma assinatura")),
		func(ctx context.Context, in *createSubscriptionIn) (*ItemOut[Subscription], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			b := in.Body
			sub, err := s.svc.CreateSubscription(ctx, sc.Actor, sc.Company.ID, service.CreateSubscriptionInput{
				Name: b.Name, Agent: b.Agent, MonthlyUSD: b.MonthlyUSD, ModelPrefixes: b.ModelPrefixes,
				WeeklyLimitUSD: b.WeeklyLimitUSD, WeeklyGuardPct: b.WeeklyGuardPct, Inactive: b.Inactive})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			st, err := s.svc.GetSubscription(ctx, sc.Company.ID, sub.ID.String())
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Subscription]{Body: subscriptionOut(st)}, nil
		})

	huma.Register(api, op("list-subscriptions", http.MethodGet, "/companies/{company}/subscriptions", "subscriptions", "Lista assinaturas, com o uso da semana"),
		func(ctx context.Context, in *listSubscriptionsIn) (*ListOut[Subscription], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			subs, err := s.svc.ListSubscriptions(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(subs, in.Page, subscriptionOut), nil
		})

	huma.Register(api, op("get-subscription", http.MethodGet, "/companies/{company}/subscriptions/{subscription}", "subscriptions", "Obtém uma assinatura"),
		func(ctx context.Context, in *SubscriptionPath) (*ItemOut[Subscription], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			st, err := s.svc.GetSubscription(ctx, sc.Company.ID, in.Subscription)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Subscription]{Body: subscriptionOut(st)}, nil
		})

	huma.Register(api, op("update-subscription", http.MethodPatch, "/companies/{company}/subscriptions/{subscription}", "subscriptions", "Atualiza uma assinatura"),
		func(ctx context.Context, in *updateSubscriptionIn) (*ItemOut[Subscription], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cur, err := s.svc.GetSubscription(ctx, sc.Company.ID, in.Subscription)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			b := in.Body
			if _, err := s.svc.UpdateSubscription(ctx, sc.Actor, sc.Company.ID, cur.ID, service.UpdateSubscriptionInput{
				Name: b.Name, Agent: b.Agent, MonthlyUSD: b.MonthlyUSD, ModelPrefixes: b.ModelPrefixes,
				WeeklyLimitUSD: b.WeeklyLimitUSD, ClearWeeklyLimit: b.ClearWeeklyLimit, WeeklyGuardPct: b.WeeklyGuardPct,
				Active: b.Active}); err != nil {
				return nil, s.fail(ctx, err)
			}
			st, err := s.svc.GetSubscription(ctx, sc.Company.ID, cur.ID.String())
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Subscription]{Body: subscriptionOut(st)}, nil
		})

	huma.Register(api, func() huma.Operation {
		o := op("delete-subscription", http.MethodDelete, "/companies/{company}/subscriptions/{subscription}", "subscriptions",
			"Remove uma assinatura sem execuções (com histórico, desligue-a)")
		o.DefaultStatus = http.StatusNoContent
		return o
	}(), func(ctx context.Context, in *SubscriptionPath) (*struct{}, error) {
		if err := forbidPersona(ctx); err != nil {
			return nil, err
		}
		sc, err := s.scope(ctx, in.Company)
		if err != nil {
			return nil, err
		}
		cur, err := s.svc.GetSubscription(ctx, sc.Company.ID, in.Subscription)
		if err != nil {
			return nil, s.fail(ctx, err)
		}
		if err := s.svc.DeleteSubscription(ctx, sc.Actor, sc.Company.ID, cur.ID); err != nil {
			return nil, s.fail(ctx, err)
		}
		return &struct{}{}, nil
	})
}

// subscriptionRef resolve uma assinatura opcional por id ou nome.
func (s *server) subscriptionRef(ctx context.Context, cid domain.ID, ref string) (*domain.ID, error) {
	return optID(ctx, s, ref, func(ctx context.Context, r string) (service.SubscriptionStatus, error) {
		return s.svc.GetSubscription(ctx, cid, r)
	}, func(st service.SubscriptionStatus) domain.ID { return st.ID })
}
