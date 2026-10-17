package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	_ "time/tzdata" // zone database for company timezones, independent of the host

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/store"
)

// txc is the state of one use-case transaction: the sqlc queries bound to it, a
// stable timestamp, and notifications to publish on the broker after commit.
type txc struct {
	s     *Service
	ctx   context.Context
	tx    pgx.Tx
	q     *store.Queries
	now   time.Time
	after []func()
	// onRollback desfaz efeitos de fora do banco (objetos já enviados ao S3) se a transação falhar.
	onRollback []func()
	// cancelled: runs whose cancellation this transaction already recorded (cancelRun is idempotent).
	cancelled map[domain.ID]bool
	// wakeCount: quantos wakes esta transação realmente enfileirou (wake pula task pausada, bloqueada ou persona parada).
	wakeCount int
}

// withTx runs fn in a single transaction. Broker notifications queued by fn are
// published only after a successful commit.
func (s *Service) withTx(ctx context.Context, fn func(t *txc) error) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	t := &txc{s: s, ctx: ctx, tx: tx, q: store.New(tx), now: s.Now().UTC()}
	if err := fn(t); err != nil {
		rollbackTx(tx)
		for _, f := range t.onRollback {
			f()
		}
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	for _, f := range t.after {
		f()
	}
	return nil
}

// rollbackTx desfaz a transação sem depender do ctx da requisição (que pode já estar cancelado), mas com
// teto de rollbackTimeout: um banco travado não pode prender a goroutine nem a conexão para sempre.
func rollbackTx(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
	defer cancel()
	_ = tx.Rollback(ctx)
}

// read returns queries bound to the pool for read-only use cases.
func (s *Service) read() *store.Queries { return store.New(s.Pool) }

func (t *txc) publish(topic, typ string, data any) {
	if t.s.Broker == nil {
		return
	}
	b := t.s.Broker
	t.after = append(t.after, func() { b.Publish(events.Message{Topic: topic, Type: typ, Data: data}) })
}

func (t *txc) publishRunStatus(r domain.Run) {
	t.publish(events.RunTopic(r.ID.String()), "run.status", r)
}

// activity records a domain fact in the current transaction and queues its publication.
func (t *txc) activity(actor domain.Actor, companyID domain.ID, verb, subjectType string, subjectID domain.ID, data any, eventID string) error {
	raw := json.RawMessage(`{}`)
	if data != nil {
		b, err := json.Marshal(data)
		if err != nil {
			return fmt.Errorf("marshal activity data: %w", err)
		}
		raw = b
	}
	var eid *string
	if eventID != "" {
		eid = &eventID
	}
	row, err := t.q.CreateActivity(t.ctx, store.CreateActivityParams{
		ID: domain.NewID(), CompanyID: companyID, ActorType: string(actor.Type), ActorID: actor.ID,
		Verb: verb, SubjectType: subjectType, SubjectID: subjectID, Data: raw, EventID: eid, OccurredAt: t.now,
	})
	if err != nil {
		return fmt.Errorf("record activity %s: %w", verb, err)
	}
	if err := t.enqueueWebhooks(companyID, verb, row.ID, subjectType, subjectID, raw); err != nil {
		return err
	}
	if err := t.enqueuePluginEvents(companyID, verb, actor, row.ID); err != nil {
		return err
	}
	t.publish(events.CompanyTopic(companyID.String()), "activity", activityFromRow(row))
	return nil
}

// memoryFact writes a memory_outbox row (idempotent on eventID) and enqueues its delivery.
func (t *txc) memoryFact(companyID domain.ID, workspaceID *domain.ID, f memory.Fact, eventID string) error {
	payload, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("marshal memory fact: %w", err)
	}
	id, err := t.q.InsertMemoryOutbox(t.ctx, store.InsertMemoryOutboxParams{
		ID: domain.NewID(), CompanyID: companyID, WorkspaceID: workspaceID, Kind: f.Kind,
		Payload: payload, EventID: eventID, Now: t.now,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // already recorded
	}
	if err != nil {
		return fmt.Errorf("insert memory outbox: %w", err)
	}
	if t.s.Jobs != nil {
		if err := t.s.Jobs.DeliverMemoryTx(t.ctx, t.tx, id); err != nil {
			return fmt.Errorf("enqueue memory delivery: %w", err)
		}
	}
	return nil
}

// wake enqueues a Wake for the persona when it can work. requireWorkable additionally
// demands a Workable task status (assign/manual) instead of just an open task (comment).
func (t *txc) wake(companyID, taskID domain.ID, personaID *domain.ID, status domain.TaskStatus, trigger domain.RunTrigger, requireWorkable bool) error {
	if t.s.Jobs == nil || personaID == nil {
		return nil
	}
	if requireWorkable && !status.Workable() || !requireWorkable && !status.Open() {
		return nil
	}
	if trigger == domain.TriggerComment || trigger == domain.TriggerManual {
		// A task de sistema da curadoria só acorda pela máquina da consolidação (memory) ou pela rede de
		// segurança do heartbeat, nunca por comentário nem à mão (ADR-0030).
		if system, err := t.isSystemTask(taskID); err != nil {
			return err
		} else if system {
			return nil
		}
		if test, err := t.isSkillTestTask(taskID); err != nil { // idem para o teste do estúdio (ADR-0035)
			return err
		} else if test {
			return nil
		}
		// Idem para as tasks de sistema dos cards de status (ADR-0032).
		if card, err := t.isStatusCardTask(taskID); err != nil {
			return err
		} else if card {
			return nil
		}
	}
	p, err := t.q.GetPersonaByID(t.ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: *personaID})
	if err != nil {
		return fmt.Errorf("load persona for wake: %w", mapErr(err, "persona"))
	}
	if !domain.PersonaStatus(p.Status).CanWork() {
		return nil
	}
	// Coordenação (ADR-0040): task pausada guarda o wake para o "retomar"; task bloqueada por dependência espera o
	// fechamento da última dependência, que a acorda (nada a guardar).
	switch berr := t.dispatchBlocker(companyID, taskID); {
	case errors.Is(berr, domain.ErrTaskHeld):
		return t.recordHeldWake(companyID, taskID, *personaID, trigger)
	case errors.Is(berr, domain.ErrTaskBlocked):
		return nil
	case berr != nil:
		return berr
	}
	if err := t.s.Jobs.WakeTx(t.ctx, t.tx, Wake{CompanyID: companyID, PersonaID: *personaID, TaskID: taskID, Trigger: trigger}); err != nil {
		return fmt.Errorf("enqueue wake: %w", err)
	}
	t.wakeCount++
	return nil
}

// ---------- error helpers ----------

// mapErr converts store errors into domain errors.
func mapErr(err error, what string) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("%s: %w", what, domain.ErrNotFound)
	case isUnique(err):
		return fmt.Errorf("%s: %w: %w", what, domain.ErrConflict, err) // keep the pg error for race handling
	}
	return fmt.Errorf("%s: %w", what, err)
}

func isUnique(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

func isUniqueOn(err error, constraint string) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505" && pe.ConstraintName == constraint
}

func notFound(what string) error { return fmt.Errorf("%s: %w", what, domain.ErrNotFound) }

// maxHeartbeatIntervalSec é o maior intervalo de heartbeat aceito (30 dias); acima disso a
// persona é, na prática, sob demanda (intervalo 0).
const maxHeartbeatIntervalSec = 30 * 24 * 3600

// i32 converte para int32 saturando nos limites, para parâmetros de consulta e colunas int4.
func i32(n int) int32 {
	if n > math.MaxInt32 {
		return math.MaxInt32
	}
	if n < math.MinInt32 {
		return math.MinInt32
	}
	return int32(n)
}

func clamp(n, def, max int) int {
	if n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

func companyTopic(id domain.ID) string { return events.CompanyTopic(id.String()) }
