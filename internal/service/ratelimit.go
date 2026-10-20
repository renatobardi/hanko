package service

import (
	"context"
	"fmt"
	"time"

	"github.com/renatobardi/hanko/internal/ratelimit"
	"github.com/renatobardi/hanko/internal/store"
)

// Persistência do rate limit (ADR-0058 §B). Os buckets são estado efêmero de infraestrutura, não dado de domínio:
// sem company_id e sem activity_event. Implementa ratelimit.Store.

// TakeRateToken recarrega, decide e consome um token do bucket numa única instrução atômica.
func (s *Service) TakeRateToken(ctx context.Context, key string, p ratelimit.Params, cost float64, now time.Time) (float64, bool, error) {
	row, err := s.read().TakeRateToken(ctx, store.TakeRateTokenParams{Key: key, Capacity: p.Capacity, Cost: cost, Now: now,
		RefillPerSec: p.RefillPerSec})
	if err != nil {
		return 0, false, fmt.Errorf("take rate token: %w", err)
	}
	return row.Tokens, row.LastAllowed, nil
}

// PurgeRateBuckets apaga os buckets sem uso desde before.
func (s *Service) PurgeRateBuckets(ctx context.Context, before time.Time) (int64, error) {
	n, err := s.read().DeleteStaleRateBuckets(ctx, before)
	if err != nil {
		return 0, fmt.Errorf("purge rate buckets: %w", err)
	}
	return n, nil
}

var _ ratelimit.Store = (*Service)(nil)
