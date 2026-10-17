package service_test

import (
	"context"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/ratelimit"
)

// O SQL do bucket (store.TakeRateToken) tem de decidir exatamente como o modelo puro (ADR-0058 §B).

func TestRateBucketSQLMatchesModel(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	rng := rand.New(rand.NewSource(20261017))
	base := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

	for round := 0; round < 25; round++ {
		p := ratelimit.Params{Capacity: float64(rng.Intn(30) + 1), RefillPerSec: float64(rng.Intn(500)+1) / 100}
		key := "t|" + string(rune('a'+round))
		var model ratelimit.State
		fresh := true
		now := base
		for i := 0; i < 80; i++ {
			// ora andando, ora parado, ora (raramente) voltando no tempo
			switch r := rng.Intn(10); {
			case r < 6:
				now = now.Add(time.Duration(rng.Intn(400)) * time.Millisecond)
			case r == 9:
				now = now.Add(-time.Duration(rng.Intn(200)) * time.Millisecond)
			}
			next, wantOK := ratelimit.Step(model, fresh, p, 1, now)
			tokens, gotOK, err := e.svc.TakeRateToken(ctx, key, p, 1, now)
			if err != nil {
				t.Fatal(err)
			}
			if gotOK != wantOK {
				t.Fatalf("round %d passo %d: SQL=%v modelo=%v (p=%+v)", round, i, gotOK, wantOK, p)
			}
			if math.Abs(tokens-next.Tokens) > 1e-6 {
				t.Fatalf("round %d passo %d: tokens SQL=%v modelo=%v", round, i, tokens, next.Tokens)
			}
			model, fresh = next, false
		}
	}
}

// Concorrência: com 60 chegadas simultâneas num bucket de 20, exatamente 20 passam (a linha é o lock).
func TestRateBucketConcurrentExactCapacity(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := ratelimit.Params{Capacity: 20, RefillPerSec: 0}
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	var wg sync.WaitGroup
	var allowed atomic.Int32
	for i := 0; i < 60; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, ok, err := e.svc.TakeRateToken(ctx, "conc|k", p, 1, now)
			if err != nil {
				t.Error(err)
				return
			}
			if ok {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if allowed.Load() != 20 {
		t.Fatalf("admitidas = %d, quer exatamente 20", allowed.Load())
	}
}

func TestRateBucketPurge(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := ratelimit.PerMinute(60)
	old := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)
	fresh := old.Add(3 * time.Hour)
	if _, _, err := e.svc.TakeRateToken(ctx, "p|velho", p, 1, old); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.TakeRateToken(ctx, "p|novo", p, 1, fresh); err != nil {
		t.Fatal(err)
	}
	n, err := e.svc.PurgeRateBuckets(ctx, fresh.Add(-time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("purge = %d %v, quer 1", n, err)
	}
}
