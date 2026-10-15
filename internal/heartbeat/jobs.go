package heartbeat

import (
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/renatobardi/hanko/internal/domain"
)

// Queue names.
const (
	QueueDefault = river.QueueDefault
	QueueRuns    = "runs"
)

// WakeArgs asks the heartbeat to consider running a persona on a task (job "wake").
// Only persona and task take part in the uniqueness hash, so wakes for the same pair
// coalesce regardless of trigger.
type WakeArgs struct {
	CompanyID domain.ID         `json:"company_id"`
	PersonaID domain.ID         `json:"persona_id" river:"unique"`
	TaskID    domain.ID         `json:"task_id" river:"unique"`
	Trigger   domain.RunTrigger `json:"trigger"`
}

func (WakeArgs) Kind() string { return "wake" }

// InsertOpts coalesces wakes: while one is waiting (or running), another is a no-op.
// River requires "running" in the unique states.
func (WakeArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		MaxAttempts: 3,
		UniqueOpts: river.UniqueOpts{
			ByArgs: true,
			ByState: []rivertype.JobState{
				rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateScheduled,
				rivertype.JobStateRetryable, rivertype.JobStateRunning,
			},
		},
	}
}

// ExecuteRunArgs executes one queued run (job "execute_run"). The harness is never run
// twice: once the run left "queued" a retry can only finalize it from the result saved in
// runs.pending_result (finalization failures snooze the job). A run that died with the
// process is marked interrupted at boot, not replayed.
type ExecuteRunArgs struct {
	CompanyID domain.ID `json:"company_id"`
	RunID     domain.ID `json:"run_id"`
}

func (ExecuteRunArgs) Kind() string { return "execute_run" }

func (ExecuteRunArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueRuns, MaxAttempts: 10}
}

// DeliverMemoryArgs delivers one memory_outbox row to the memory provider (job "deliver_memory").
type DeliverMemoryArgs struct {
	OutboxID domain.ID `json:"outbox_id"`
}

func (DeliverMemoryArgs) Kind() string { return "deliver_memory" }

func (DeliverMemoryArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		MaxAttempts: 10,
		// Completed jobs are not unique: the sweep must be able to re-enqueue a row that
		// went back to pending after an earlier delivery job finished.
		UniqueOpts: river.UniqueOpts{
			ByArgs: true,
			ByState: []rivertype.JobState{
				rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateScheduled,
				rivertype.JobStateRetryable, rivertype.JobStateRunning,
			},
		},
	}
}

// HeartbeatTickArgs is the periodic scan for workable tasks (job "heartbeat_tick").
type HeartbeatTickArgs struct{}

func (HeartbeatTickArgs) Kind() string { return "heartbeat_tick" }

func (HeartbeatTickArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 1} }

// MemorySweepArgs is the periodic re-enqueue of pending memory deliveries (job "memory_sweep").
type MemorySweepArgs struct{}

func (MemorySweepArgs) Kind() string { return "memory_sweep" }

func (MemorySweepArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 1} }

// AlertTickArgs is the periodic evaluation of operational alerts (job "alert_tick").
type AlertTickArgs struct{}

func (AlertTickArgs) Kind() string { return "alert_tick" }

func (AlertTickArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 1} }
