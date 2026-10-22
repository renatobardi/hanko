package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/renatobardi/hanko/internal/backup"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Backups da instalação (ADR-0062). São da instância, não de uma empresa: só o dono da instalação lê e dispara; a
// execução (pg_dump, tar, conteúdo externo) é do pacote backup, injetado em Service.Backups. O agendamento é o fluxo
// de sistema "backup" (tabela system_flows), disparado pelo flow_tick do heartbeat com a mesma semântica de cron dos
// fluxos de empresa.

// BackupRunner é o que o service precisa do executor de backups.
type BackupRunner interface {
	Create(ctx context.Context, o backup.CreateOptions) (backup.Entry, error)
}

const (
	// staleBackupAfter: um backup "em andamento" há mais que isso é de um processo que morreu.
	staleBackupAfter = 2 * time.Hour
	// backupRunTimeout é o teto de um backup disparado pela tela.
	backupRunTimeout = 45 * time.Minute
	maxBackupLabel   = 120
	maxBackupError   = 1000
	// DefaultBackupCron é a sugestão da tela quando ainda não há agenda: todo dia às 03:00.
	DefaultBackupCron = "0 3 * * *"
	auditSystemFlow   = "system_flow.backup"
)

var backupTracer = otel.Tracer("hanko/backup")

// backupWG segue os backups em segundo plano (StartBackup). É do processo, não de um Service: o Service é copiado
// por valor em partes do código e um WaitGroup não pode ser.
var backupWG sync.WaitGroup

// BackupSchedule é o agendamento (fluxo de sistema "backup") como a tela e a API o veem.
type BackupSchedule struct {
	Enabled     bool       `json:"enabled"`
	Cron        string     `json:"cron"`
	Timezone    string     `json:"timezone"`
	LastFiredAt *time.Time `json:"last_fired_at,omitempty"`
	NextFireAt  *time.Time `json:"next_fire_at,omitempty"`
}

// SetBackupScheduleInput muda o agendamento; campos nulos ficam como estão.
type SetBackupScheduleInput struct {
	Enabled  *bool
	Cron     *string
	Timezone *string
}

// SystemFlowResult é o resultado de FireSystemFlow. Skipped diz por que nada rodou: "disabled", "running" (já há um
// backup em andamento), "duplicate" (a ocorrência já foi tratada) ou "unavailable" (sem executor neste servidor).
type SystemFlowResult struct {
	Skipped    bool
	SkipReason string
	Backup     *domain.InstanceBackup
}

func backupFromRow(r store.InstanceBackup) domain.InstanceBackup {
	return domain.InstanceBackup{ID: r.ID, Kind: r.Kind, Status: r.Status, Label: r.Label, FileName: r.FileName, SizeBytes: r.SizeBytes,
		SchemaVersion: r.SchemaVersion, Blobs: int(r.Blobs), Error: r.Error, ActorType: r.ActorType, ActorID: r.ActorID,
		ScheduledFor: r.ScheduledFor, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt}
}

func systemFlowFromRow(r store.SystemFlow) domain.SystemFlow {
	return domain.SystemFlow{Key: r.Key, Cron: r.Cron, Timezone: r.Timezone, Enabled: r.Enabled, LastFiredAt: r.LastFiredAt,
		NextFireAt: r.NextFireAt, UpdatedAt: r.UpdatedAt}
}

func (s *Service) requireBackupOwner(ctx context.Context, actor domain.Actor) (*domain.ID, error) {
	uid, err := requireInstanceOwner(ctx)
	if err != nil {
		return nil, err
	}
	if actor.Type == domain.ActorPersona {
		return nil, domain.ErrForbidden
	}
	return uid, nil
}

// ListBackups devolve o histórico, do mais novo ao mais antigo (dono da instalação).
func (s *Service) ListBackups(ctx context.Context, limit int) ([]domain.InstanceBackup, error) {
	if _, err := requireInstanceOwner(ctx); err != nil {
		return nil, err
	}
	rows, err := s.read().ListInstanceBackups(ctx, i32(clamp(limit, 30, 200)))
	if err != nil {
		return nil, fmt.Errorf("list instance backups: %w", err)
	}
	out := make([]domain.InstanceBackup, len(rows))
	for i, r := range rows {
		out[i] = backupFromRow(r)
	}
	return out, nil
}

// BackupsDir é o diretório dos backups (informativo).
func (s *Service) BackupsDir() string { return s.BackupDir }

// BackupsAvailable diz se este servidor sabe fazer backups (executor configurado).
func (s *Service) BackupsAvailable() bool { return s.Backups != nil }

// StartBackup registra um backup manual e o executa em segundo plano; devolve a linha em andamento. 409 se já há um
// em andamento. Use WaitBackups para esperar o término (desligamento, testes).
func (s *Service) StartBackup(ctx context.Context, actor domain.Actor, label string) (domain.InstanceBackup, error) {
	uid, err := s.requireBackupOwner(ctx, actor)
	if err != nil {
		return domain.InstanceBackup{}, err
	}
	if s.Backups == nil {
		return domain.InstanceBackup{}, domain.Invalid("backups", "backups indisponíveis neste servidor")
	}
	rec, err := s.openBackup(ctx, actor, uid, domain.BackupManual, label, nil)
	if err != nil {
		return domain.InstanceBackup{}, err
	}
	backupWG.Add(1)
	go func() {
		defer backupWG.Done()
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), backupRunTimeout)
		defer cancel()
		s.executeBackup(rctx, rec, domain.BackupManual, label)
	}()
	return rec, nil
}

// RunBackup é StartBackup esperando o término; devolve a linha final (ok ou failed).
func (s *Service) RunBackup(ctx context.Context, actor domain.Actor, label string) (domain.InstanceBackup, error) {
	uid, err := s.requireBackupOwner(ctx, actor)
	if err != nil {
		return domain.InstanceBackup{}, err
	}
	if s.Backups == nil {
		return domain.InstanceBackup{}, domain.Invalid("backups", "backups indisponíveis neste servidor")
	}
	rec, err := s.openBackup(ctx, actor, uid, domain.BackupManual, label, nil)
	if err != nil {
		return domain.InstanceBackup{}, err
	}
	return s.executeBackup(ctx, rec, domain.BackupManual, label), nil
}

// WaitBackups espera os backups em segundo plano terminarem.
func (s *Service) WaitBackups() { backupWG.Wait() }

// openBackup grava a linha "em andamento". A vaga é única (índice parcial): outro backup rodando vira 409, e a mesma
// ocorrência agendada nunca entra duas vezes. Linhas presas de um processo que morreu são liberadas antes.
func (s *Service) openBackup(ctx context.Context, actor domain.Actor, uid *domain.ID, kind, label string, scheduledFor *time.Time) (domain.InstanceBackup, error) {
	label = truncateRunes(strings.TrimSpace(label), maxBackupLabel)
	var rec domain.InstanceBackup
	err := s.withTx(ctx, func(t *txc) error {
		if _, err := t.q.FailStaleInstanceBackups(ctx, store.FailStaleInstanceBackupsParams{Now: &t.now, Before: t.now.Add(-staleBackupAfter)}); err != nil {
			return fmt.Errorf("fail stale backups: %w", err)
		}
		r, err := t.q.InsertInstanceBackup(ctx, store.InsertInstanceBackupParams{ID: domain.NewID(), Kind: kind, Label: label,
			ActorType: string(actor.Type), ActorID: uid, ScheduledFor: scheduledFor, Now: t.now})
		if isNoRows(err) {
			running, rerr := t.q.HasRunningInstanceBackup(ctx)
			if rerr != nil {
				return fmt.Errorf("has running backup: %w", rerr)
			}
			if running {
				return fmt.Errorf("já há um backup em andamento: %w", domain.ErrConflict)
			}
			return errBackupDuplicate
		}
		if err != nil {
			return fmt.Errorf("insert instance backup: %w", err)
		}
		rec = backupFromRow(r)
		return nil
	})
	return rec, err
}

var errBackupDuplicate = errors.New("a ocorrência já foi tratada")

// executeBackup roda o backup e fecha a linha. Nunca devolve erro: falha vira status failed com a mensagem (a tela e o
// histórico mostram), e o erro vai ao span.
func (s *Service) executeBackup(ctx context.Context, rec domain.InstanceBackup, kind, label string) domain.InstanceBackup {
	ctx, span := backupTracer.Start(ctx, "hanko.backup.run", trace.WithAttributes(attribute.String("hanko.backup.kind", kind)))
	defer span.End()
	opts := backup.CreateOptions{Kind: kind, Label: label, Prune: kind != domain.BackupManual}
	ent, err := s.Backups.Create(ctx, opts)
	fin := store.FinishInstanceBackupParams{ID: rec.ID, Now: ptrTime(s.Now().UTC())}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "backup falhou")
		fin.Status, fin.Error = domain.BackupFailed, truncateRunes(err.Error(), maxBackupError)
		// O texto do erro vem de processos externos (pg_dump, psql) e não entra no log: o detalhe, já sem a senha do
		// banco (backup.secretFree), fica no histórico de backups, na tela e no span.
		if s.Log != nil {
			s.Log.Warn("backup falhou: o detalhe está no histórico de backups", "id", rec.ID, "kind", kind)
		}
	} else {
		fin.Status, fin.FileName, fin.SizeBytes, fin.SchemaVersion, fin.Blobs = domain.BackupOK, ent.Name, ent.Size, ent.Manifest.SchemaVersion, i32(len(ent.Manifest.Blobs))
	}
	// o ctx pode ter sido cancelado (desligamento): fechar a linha não pode depender dele
	row, ferr := store.New(s.Pool).FinishInstanceBackup(context.WithoutCancel(ctx), fin)
	if ferr != nil {
		span.RecordError(ferr)
		if s.Log != nil {
			s.Log.Error("backup: não consegui fechar o registro", "id", rec.ID, "err", ferr)
		}
		return rec
	}
	return backupFromRow(row)
}

// GetBackupSchedule devolve o agendamento (a sugestão padrão, desligada, se nunca foi configurado).
func (s *Service) GetBackupSchedule(ctx context.Context) (BackupSchedule, error) {
	if _, err := requireInstanceOwner(ctx); err != nil {
		return BackupSchedule{}, err
	}
	r, err := s.read().GetSystemFlow(ctx, domain.SystemFlowBackup)
	if isNoRows(err) {
		return BackupSchedule{Cron: DefaultBackupCron, Timezone: "UTC"}, nil
	}
	if err != nil {
		return BackupSchedule{}, fmt.Errorf("get system flow: %w", err)
	}
	return scheduleFromRow(r), nil
}

func scheduleFromRow(r store.SystemFlow) BackupSchedule {
	return BackupSchedule{Enabled: r.Enabled, Cron: r.Cron, Timezone: r.Timezone, LastFiredAt: r.LastFiredAt, NextFireAt: r.NextFireAt}
}

// SetBackupSchedule liga, desliga ou muda o agendamento. A próxima ocorrência é recalculada a partir de agora (sem
// reposição do que passou enquanto estava desligado). A mudança vai para a trilha de auditoria da instalação.
func (s *Service) SetBackupSchedule(ctx context.Context, actor domain.Actor, in SetBackupScheduleInput) (BackupSchedule, error) {
	uid, err := s.requireBackupOwner(ctx, actor)
	if err != nil {
		return BackupSchedule{}, err
	}
	var out BackupSchedule
	err = s.withTx(ctx, func(t *txc) error {
		cur := BackupSchedule{Cron: DefaultBackupCron, Timezone: "UTC"}
		var oldVal json.RawMessage
		row, err := t.q.GetSystemFlowForUpdate(ctx, domain.SystemFlowBackup)
		switch {
		case err == nil:
			cur = scheduleFromRow(row)
			oldVal = scheduleJSON(cur)
		case !isNoRows(err):
			return fmt.Errorf("get system flow: %w", err)
		}
		next := cur
		if in.Cron != nil {
			next.Cron = strings.Join(strings.Fields(*in.Cron), " ")
		}
		if in.Timezone != nil {
			next.Timezone = strings.TrimSpace(*in.Timezone)
		}
		if in.Enabled != nil {
			next.Enabled = *in.Enabled
		}
		nf, err := NextCron(next.Cron, next.Timezone, t.now) // valida cron e fuso mesmo desligado
		if err != nil {
			return err
		}
		reschedule := next.Enabled && (!cur.Enabled || next.Cron != cur.Cron || next.Timezone != cur.Timezone || cur.NextFireAt == nil)
		fire := cur.NextFireAt
		switch {
		case !next.Enabled:
			fire = nil
		case reschedule:
			fire = &nf
		}
		if oldVal != nil && next.Enabled == cur.Enabled && next.Cron == cur.Cron && next.Timezone == cur.Timezone {
			out = cur
			return nil // nada mudou
		}
		saved, err := t.q.UpsertSystemFlow(ctx, store.UpsertSystemFlowParams{Key: domain.SystemFlowBackup, Cron: next.Cron, Timezone: next.Timezone,
			Enabled: next.Enabled, NextFireAt: fire, UpdatedBy: uid, Now: t.now})
		if err != nil {
			return fmt.Errorf("upsert system flow: %w", err)
		}
		out = scheduleFromRow(saved)
		if err := t.q.CreateInstanceSettingAudit(ctx, store.CreateInstanceSettingAuditParams{ID: domain.NewID(), Key: auditSystemFlow,
			OldValue: oldVal, NewValue: scheduleJSON(out), ActorType: string(actor.Type), ActorID: uid, Now: t.now}); err != nil {
			return fmt.Errorf("audit system flow: %w", err)
		}
		return nil
	})
	return out, err
}

func scheduleJSON(b BackupSchedule) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"enabled": b.Enabled, "cron": b.Cron, "timezone": b.Timezone})
	return raw
}

// DueSystemFlows lista os fluxos de sistema ligados cuja próxima ocorrência já chegou.
func (s *Service) DueSystemFlows(ctx context.Context, now time.Time) ([]domain.SystemFlow, error) {
	n := now.UTC()
	rows, err := s.read().DueSystemFlows(ctx, &n)
	if err != nil {
		return nil, fmt.Errorf("due system flows: %w", err)
	}
	out := make([]domain.SystemFlow, len(rows))
	for i, r := range rows {
		out[i] = systemFlowFromRow(r)
	}
	return out, nil
}

// FireSystemFlow dispara a ocorrência scheduledFor de um fluxo de sistema como o sistema. A agenda avança antes de
// o backup rodar (no máximo uma vez por ocorrência, mesmo se o processo morrer no meio); depois de uma parada longa
// só a ocorrência mais recente dispara. O backup em si roda aqui, de forma síncrona.
func (s *Service) FireSystemFlow(ctx context.Context, key string, scheduledFor time.Time) (SystemFlowResult, error) {
	if !domain.ValidSystemFlowKey(key) {
		return SystemFlowResult{}, domain.Invalid("key", "fluxo de sistema desconhecido")
	}
	if s.Backups == nil {
		return SystemFlowResult{Skipped: true, SkipReason: "unavailable"}, nil
	}
	var res SystemFlowResult
	var rec domain.InstanceBackup
	err := s.withTx(ctx, func(t *txc) error {
		f, err := t.q.GetSystemFlowForUpdate(ctx, key)
		if err != nil {
			return mapErr(err, "get system flow")
		}
		if !f.Enabled {
			res.Skipped, res.SkipReason = true, "disabled"
			return nil
		}
		latest, missed, err := latestDue(f.Cron, f.Timezone, scheduledFor.UTC(), t.now)
		if err != nil {
			return err
		}
		after := t.now
		if latest.After(after) {
			after = latest
		}
		nx, err := NextCron(f.Cron, f.Timezone, after)
		if err != nil {
			return err
		}
		if err := t.q.MarkSystemFlowFired(ctx, store.MarkSystemFlowFiredParams{Key: key, LastFiredAt: &t.now, NextFireAt: &nx, Now: t.now}); err != nil {
			return fmt.Errorf("mark system flow fired: %w", err)
		}
		if missed > 0 && s.Log != nil {
			s.Log.Warn("fluxo de sistema: ocorrências perdidas durante a parada; dispara só a mais recente", "key", key, "missed", missed)
		}
		if _, err := t.q.FailStaleInstanceBackups(ctx, store.FailStaleInstanceBackupsParams{Now: &t.now, Before: t.now.Add(-staleBackupAfter)}); err != nil {
			return fmt.Errorf("fail stale backups: %w", err)
		}
		sf := latest.UTC().Truncate(time.Microsecond)
		row, err := t.q.InsertInstanceBackup(ctx, store.InsertInstanceBackupParams{ID: domain.NewID(), Kind: domain.BackupScheduled,
			ActorType: string(domain.SystemActor.Type), ScheduledFor: &sf, Now: t.now})
		if isNoRows(err) {
			running, rerr := t.q.HasRunningInstanceBackup(ctx)
			if rerr != nil {
				return fmt.Errorf("has running backup: %w", rerr)
			}
			res.Skipped, res.SkipReason = true, "duplicate"
			if running {
				res.SkipReason = "running"
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("insert scheduled backup: %w", err)
		}
		rec = backupFromRow(row)
		return nil
	})
	if err != nil || res.Skipped {
		return res, err
	}
	done := s.executeBackup(ctx, rec, domain.BackupScheduled, "")
	res.Backup = &done
	return res, nil
}
