//go:build integration

package heartbeat

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

func integrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("HANKO_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("HANKO_TEST_DATABASE_URL vazio")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, pool); err != nil { // idempotent
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "TRUNCATE river_job"); err != nil {
		t.Fatal(err)
	}
	return pool
}

func countJobs(t *testing.T, pool *pgxpool.Pool, where string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM river_job WHERE "+where, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestJobsInsertTx(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	h, err := New(pool, Options{})
	if err != nil {
		t.Fatal(err)
	}
	w := service.Wake{CompanyID: domain.NewID(), PersonaID: domain.NewID(), TaskID: domain.NewID(), Trigger: domain.TriggerAssign}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, trig := range []domain.RunTrigger{domain.TriggerAssign, domain.TriggerComment, domain.TriggerManual} {
		w.Trigger = trig
		if err := h.WakeTx(ctx, tx, w); err != nil {
			t.Fatal(err)
		}
	}
	runID, outboxID := domain.NewID(), domain.NewID()
	if err := h.ExecuteRunTx(ctx, tx, w.CompanyID, runID); err != nil {
		t.Fatal(err)
	}
	if err := h.DeliverMemoryTx(ctx, tx, outboxID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if n := countJobs(t, pool, "kind='wake'"); n != 1 {
		t.Errorf("wake jobs = %d, want 1 (coalesced)", n)
	}
	if n := countJobs(t, pool, "kind='execute_run' AND queue='runs' AND max_attempts=10"); n != 1 {
		t.Errorf("execute_run jobs = %d", n)
	}
	if n := countJobs(t, pool, "kind='deliver_memory'"); n != 1 {
		t.Errorf("deliver_memory jobs = %d", n)
	}

	// rollback leaves nothing behind
	tx, _ = pool.Begin(ctx)
	_ = h.WakeTx(ctx, tx, service.Wake{CompanyID: w.CompanyID, PersonaID: domain.NewID(), TaskID: domain.NewID(), Trigger: domain.TriggerManual})
	_ = tx.Rollback(ctx)
	if n := countJobs(t, pool, "kind='wake'"); n != 1 {
		t.Errorf("wake jobs after rollback = %d", n)
	}
}

// TestRiverWorkersWithFakeBackend runs the real river client against a fake backend:
// a wake inserted in a tx reaches CheckoutRun, and the periodic tick wakes candidates.
func TestRiverWorkersWithFakeBackend(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	h, err := New(pool, Options{PeriodicInterval: 300 * time.Millisecond, Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan service.Wake, 8)
	cand := service.WakeCandidate{CompanyID: domain.NewID(), PersonaID: domain.NewID(), TaskID: domain.NewID()}
	be := &fakeBackend{orphans: 2, cands: []service.WakeCandidate{cand}}
	be.checkout = func(w service.Wake) (domain.Run, error) {
		got <- w
		return domain.Run{}, domain.ErrActiveRun // silent success
	}
	h.attach(be, nil, nil, nil, authForTest())
	if err := h.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		_ = h.Stop(sctx)
	})

	direct := service.Wake{CompanyID: domain.NewID(), PersonaID: domain.NewID(), TaskID: domain.NewID(), Trigger: domain.TriggerManual}
	tx, _ := pool.Begin(ctx)
	if err := h.WakeTx(ctx, tx, direct); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	seenDirect, seenTick := false, false
	deadline := time.After(20 * time.Second)
	for !(seenDirect && seenTick) {
		select {
		case w := <-got:
			if w.TaskID == direct.TaskID && w.Trigger == domain.TriggerManual {
				seenDirect = true
			}
			if w.TaskID == cand.TaskID && w.Trigger == domain.TriggerHeartbeat {
				seenTick = true
			}
		case <-deadline:
			t.Fatalf("timeout: direct=%v tick=%v", seenDirect, seenTick)
		}
	}
}
