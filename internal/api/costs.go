package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

type costsIn struct {
	CompanyPath
	From    string `query:"from" doc:"Início do período: data (AAAA-MM-DD, no fuso da empresa) ou RFC 3339. Padrão: início do mês corrente." example:"2026-03-01"`
	To      string `query:"to" doc:"Fim do período: data (inclusiva, no fuso da empresa) ou RFC 3339 (exclusivo). Padrão: fim do mês corrente." example:"2026-03-31"`
	GroupBy string `query:"group_by" enum:"day,persona,project,model,harness" default:"day" doc:"Dimensão de agrupamento."`
	Project string `query:"project" doc:"Id ou slug do projeto: só as execuções das tasks dele. O custo pago (assinaturas) não é rateado por projeto."`
}

type updateCompanyIn struct {
	CompanyPath
	Body struct {
		BudgetMonthlyCents *int64 `json:"budget_monthly_cents,omitempty" minimum:"0" doc:"Orçamento mensal da empresa em centavos de dólar."`
		ClearBudget        bool   `json:"clear_budget,omitempty" doc:"Remove o teto de orçamento da empresa."`
	}
}

// parseCostBound lê from/to: data pura vale no fuso da empresa (to é inclusivo e
// avança um dia); caso contrário, RFC 3339.
func parseCostBound(field, s string, loc *time.Location, isTo bool) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if d, err := time.ParseInLocation("2006-01-02", s, loc); err == nil {
		if isTo {
			d = d.AddDate(0, 0, 1)
		}
		return d.UTC(), nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, domain.Invalid(field, "esperado AAAA-MM-DD ou RFC 3339")
	}
	return t.UTC(), nil
}

func (s *server) registerCosts(api huma.API) {
	huma.Register(api, op("get-costs", http.MethodGet, "/companies/{company}/costs", "costs", "Custos agregados por dimensão"),
		func(ctx context.Context, in *costsIn) (*ItemOut[CostSummary], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			loc, lerr := time.LoadLocation(sc.Company.Timezone)
			if lerr != nil {
				loc = time.UTC
			}
			from, err := parseCostBound("from", in.From, loc, false)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			to, err := parseCostBound("to", in.To, loc, true)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			if from.IsZero() != to.IsZero() { // um lado só: completa pelo mês do outro
				ref := from
				if ref.IsZero() {
					ref = to.Add(-time.Second)
				}
				l := ref.In(loc)
				ms := time.Date(l.Year(), l.Month(), 1, 0, 0, 0, 0, loc)
				if from.IsZero() {
					from = ms.UTC()
				} else {
					to = ms.AddDate(0, 1, 0).UTC()
				}
			}
			project, err := s.projectRef(ctx, sc.Company.ID, in.Project)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			sum, err := s.svc.CostSummary(ctx, sc.Company.ID, service.CostFilter{From: from, To: to, GroupBy: in.GroupBy, ProjectID: project})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			b, err := s.svc.CompanyBudget(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[CostSummary]{Body: costSummaryOut(sum, b)}, nil
		})

	huma.Register(api, op("update-company", http.MethodPatch, "/companies/{company}", "companies", "Atualiza o orçamento da empresa"),
		func(ctx context.Context, in *updateCompanyIn) (*ItemOut[Company], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			if in.Body.ClearBudget && in.Body.BudgetMonthlyCents != nil {
				return nil, s.fail(ctx, domain.Invalid("budget_monthly_cents", "não combine com clear_budget"))
			}
			c := sc.Company
			if in.Body.ClearBudget || in.Body.BudgetMonthlyCents != nil {
				c, err = s.svc.SetCompanyBudget(ctx, sc.Actor, sc.Company.ID, in.Body.BudgetMonthlyCents)
				if err != nil {
					return nil, s.fail(ctx, err)
				}
			}
			return &ItemOut[Company]{Body: companyOut(c)}, nil
		})
}
