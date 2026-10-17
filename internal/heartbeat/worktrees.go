package heartbeat

import (
	"context"
	"time"

	"github.com/riverqueue/river"

	"github.com/renatobardi/hanko/internal/service"
)

// Worktrees Git (ADR-0054): o tick que limpa as pastas das tasks concluídas, canceladas ou integradas.

// WorktreeTickArgs é a limpeza periódica de worktrees (job "worktree_tick").
type WorktreeTickArgs struct{}

func (WorktreeTickArgs) Kind() string { return "worktree_tick" }

func (WorktreeTickArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 1} }

type worktreeTickBackend interface {
	ReapWorktrees(ctx context.Context, limit int) (service.ReapResult, error)
}

type worktreeTickWorker struct {
	river.WorkerDefaults[WorktreeTickArgs]
	h *Heartbeat
}

// Timeout cobre várias remoções seguidas (cada git tem o próprio teto).
func (w *worktreeTickWorker) Timeout(*river.Job[WorktreeTickArgs]) time.Duration {
	return 15 * time.Minute
}

func (w *worktreeTickWorker) Work(ctx context.Context, _ *river.Job[WorktreeTickArgs]) error {
	return w.h.worktreeTick(ctx)
}

// worktreeTick limpa os worktrees que acabaram. Uma remoção que falha não impede as outras (o erro volta no fim e
// a próxima rodada tenta de novo).
func (h *Heartbeat) worktreeTick(ctx context.Context) error {
	if h.svc == nil {
		return errNotAttached
	}
	wb, ok := h.svc.(worktreeTickBackend)
	if !ok {
		return nil
	}
	res, err := wb.ReapWorktrees(ctx, 100)
	if res.Removed > 0 || res.Failed > 0 {
		h.log.Info("worktree_tick: limpeza de worktrees", "removed", res.Removed, "failed", res.Failed, "kept", res.Kept)
	}
	return err
}
