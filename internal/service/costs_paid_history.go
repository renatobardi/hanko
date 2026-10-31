package service

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Mensalidade por dia e peso das chamadas externas no rateio (ADR-0082 itens 5 e 6).

// monthlyLine é uma linha do histórico de mensalidade de uma assinatura.
type monthlyLine struct {
	startsOn   string // AAAA-MM-DD
	recordedAt time.Time
	monthly    float64
}

type monthlyHistory map[domain.ID][]monthlyLine

func (s *Service) loadMonthlyHistory(ctx context.Context, companyID domain.ID) (monthlyHistory, error) {
	rows, err := s.Pool.Query(ctx, `SELECT subscription_id, to_char(starts_on, 'YYYY-MM-DD'), recorded_at, monthly_usd::float8
		FROM subscription_monthly_history WHERE company_id = $1 ORDER BY subscription_id, starts_on, recorded_at, id`, companyID)
	if err != nil {
		return nil, fmt.Errorf("monthly history: %w", err)
	}
	defer rows.Close()
	h := monthlyHistory{}
	for rows.Next() {
		var id domain.ID
		var l monthlyLine
		if err := rows.Scan(&id, &l.startsOn, &l.recordedAt, &l.monthly); err != nil {
			return nil, fmt.Errorf("monthly history: %w", err)
		}
		h[id] = append(h[id], l)
	}
	return h, rows.Err()
}

// valueOn é a mensalidade de um dia: a da linha com o maior início menor ou igual a ele, e a registrada por último desempata
// (as linhas vêm ordenadas por início e registro). Antes da primeira linha vale a primeira: um dia que a assinatura já
// cobrava não fica sem valor. Sem nenhuma linha (assinatura de antes do histórico), vale `fallback`.
func (h monthlyHistory) valueOn(sub domain.ID, day string, fallback float64) float64 {
	lines := h[sub]
	if len(lines) == 0 {
		return fallback
	}
	i := sort.Search(len(lines), func(i int) bool { return lines[i].startsOn > day })
	if i == 0 {
		return lines[0].monthly
	}
	return lines[i-1].monthly
}

// externalWeights soma, por assinatura e dia, o custo de lista das chamadas externas que a assinatura cobre (pela chave
// externa), para entrar no mesmo rateio dos runs. O dia das chamadas externas é o dia UTC do fato.
func (s *Service) externalWeights(ctx context.Context, companyID domain.ID, subs []store.Subscription, from, to time.Time) (map[paidKey]float64, error) {
	byKey := map[string]domain.ID{}
	for _, sub := range subs {
		if sub.ExternalKey != nil {
			byKey[*sub.ExternalKey] = sub.ID
		}
	}
	out := map[paidKey]float64{}
	if len(byKey) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT to_char(day, 'YYYY-MM-DD'), subscription_key, sum(cost_list_usd)::float8 FROM calls_daily
		WHERE company_id = $1 AND day >= $2::date AND day <= $3::date AND subscription_key <> '' GROUP BY 1, 2`,
		companyID, from.UTC(), to.UTC())
	if err != nil {
		return nil, fmt.Errorf("external weights: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var day, key string
		var w float64
		if err := rows.Scan(&day, &key, &w); err != nil {
			return nil, fmt.Errorf("external weights: %w", err)
		}
		if id, ok := byKey[key]; ok {
			out[paidKey{id, day}] += w
		}
	}
	return out, rows.Err()
}
