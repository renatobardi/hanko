package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Backups da instalação (ADR-0060): histórico, disparo manual e agendamento (fluxo de sistema). Tudo é do dono da
// instalação; o backup leva o banco inteiro, não uma empresa.

type backupBackend interface {
	ListBackups(ctx context.Context, limit int) ([]domain.InstanceBackup, error)
	StartBackup(ctx context.Context, actor domain.Actor, label string) (domain.InstanceBackup, error)
	GetBackupSchedule(ctx context.Context) (service.BackupSchedule, error)
	SetBackupSchedule(ctx context.Context, actor domain.Actor, in service.SetBackupScheduleInput) (service.BackupSchedule, error)
	BackupsAvailable() bool
}

func (s *server) backups() (backupBackend, error) {
	b, ok := s.svc.(backupBackend)
	if !ok {
		return nil, problem(http.StatusNotImplemented, "backups indisponíveis neste servidor")
	}
	return b, nil
}

// InstanceBackup é um backup da instalação.
type InstanceBackup struct {
	ID            string     `json:"id" format:"uuid"`
	Kind          string     `json:"kind" enum:"manual,scheduled,pre_update,pre_deploy"`
	Status        string     `json:"status" enum:"running,ok,failed"`
	Label         string     `json:"label,omitempty"`
	FileName      string     `json:"file_name,omitempty" doc:"Arquivo em HANKO_BACKUP_DIR; ausente enquanto roda ou se falhou."`
	SizeBytes     int64      `json:"size_bytes"`
	SchemaVersion int64      `json:"schema_version" doc:"Última migration aplicada quando o backup foi feito."`
	Blobs         int        `json:"blobs" doc:"Conteúdos de artefatos em driver externo incluídos no arquivo."`
	Error         string     `json:"error,omitempty"`
	ActorType     string     `json:"actor_type" enum:"human,persona,system"`
	ActorID       string     `json:"actor_id,omitempty" format:"uuid"`
	ScheduledFor  *time.Time `json:"scheduled_for,omitempty"`
	StartedAt     time.Time  `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
}

func backupOut(b domain.InstanceBackup) InstanceBackup {
	o := InstanceBackup{ID: idStr(b.ID), Kind: b.Kind, Status: b.Status, Label: b.Label, FileName: b.FileName, SizeBytes: b.SizeBytes,
		SchemaVersion: b.SchemaVersion, Blobs: b.Blobs, Error: b.Error, ActorType: b.ActorType, ScheduledFor: b.ScheduledFor,
		StartedAt: b.StartedAt, FinishedAt: b.FinishedAt}
	if b.ActorID != nil {
		o.ActorID = idStr(*b.ActorID)
	}
	return o
}

// BackupSchedule é o agendamento do backup (fluxo de sistema).
type BackupSchedule struct {
	Enabled     bool       `json:"enabled"`
	Cron        string     `json:"cron" example:"0 3 * * *" doc:"Cron de 5 campos."`
	Timezone    string     `json:"timezone" example:"America/Sao_Paulo"`
	LastFiredAt *time.Time `json:"last_fired_at,omitempty"`
	NextFireAt  *time.Time `json:"next_fire_at,omitempty"`
	Available   bool       `json:"available" doc:"Este servidor sabe fazer backups (HANKO_BACKUP_DIR e o cliente do PostgreSQL)."`
}

func scheduleOut(b service.BackupSchedule, available bool) BackupSchedule {
	return BackupSchedule{Enabled: b.Enabled, Cron: b.Cron, Timezone: b.Timezone, LastFiredAt: b.LastFiredAt, NextFireAt: b.NextFireAt, Available: available}
}

type startBackupIn struct {
	Body struct {
		Label string `json:"label,omitempty" maxLength:"120" doc:"Rótulo livre, ex.: antes do upgrade."`
	}
}

type setScheduleIn struct {
	Body struct {
		Enabled  *bool   `json:"enabled,omitempty"`
		Cron     *string `json:"cron,omitempty" example:"0 3 * * *"`
		Timezone *string `json:"timezone,omitempty" example:"America/Sao_Paulo"`
	}
}

func (s *server) registerBackups(api huma.API) {
	huma.Register(api, op("list-instance-backups", http.MethodGet, "/instance/backups", "instance", "Histórico de backups da instalação (dono da instalação)"),
		func(ctx context.Context, in *auditIn) (*ListOut[InstanceBackup], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			bb, err := s.backups()
			if err != nil {
				return nil, err
			}
			list, err := bb.ListBackups(ctx, in.Limit)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(list, Page{Limit: len(list)}, backupOut), nil
		})

	start := op("start-instance-backup", http.MethodPost, "/instance/backups", "instance", "Dispara um backup agora (roda em segundo plano; 409 se já há um em andamento)")
	start.DefaultStatus = http.StatusAccepted
	huma.Register(api, start,
		func(ctx context.Context, in *startBackupIn) (*ItemOut[InstanceBackup], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			bb, err := s.backups()
			if err != nil {
				return nil, err
			}
			rec, err := bb.StartBackup(ctx, principalFrom(ctx).Actor, in.Body.Label)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[InstanceBackup]{Body: backupOut(rec)}, nil
		})

	huma.Register(api, op("get-backup-schedule", http.MethodGet, "/instance/backup-schedule", "instance", "Agendamento do backup (fluxo de sistema)"),
		func(ctx context.Context, _ *struct{}) (*ItemOut[BackupSchedule], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			bb, err := s.backups()
			if err != nil {
				return nil, err
			}
			sch, err := bb.GetBackupSchedule(ctx)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[BackupSchedule]{Body: scheduleOut(sch, bb.BackupsAvailable())}, nil
		})

	huma.Register(api, op("set-backup-schedule", http.MethodPut, "/instance/backup-schedule", "instance", "Liga, desliga ou muda o agendamento do backup"),
		func(ctx context.Context, in *setScheduleIn) (*ItemOut[BackupSchedule], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			bb, err := s.backups()
			if err != nil {
				return nil, err
			}
			sch, err := bb.SetBackupSchedule(ctx, principalFrom(ctx).Actor, service.SetBackupScheduleInput{Enabled: in.Body.Enabled, Cron: in.Body.Cron, Timezone: in.Body.Timezone})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[BackupSchedule]{Body: scheduleOut(sch, bb.BackupsAvailable())}, nil
		})
}
