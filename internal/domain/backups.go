package domain

import "time"

// Tipos de backup (quem o originou), espelho de instance_backups.kind (ADR-0062).
const (
	BackupManual    = "manual"
	BackupScheduled = "scheduled"
	BackupPreUpdate = "pre_update"
	BackupPreDeploy = "pre_deploy"
)

// Estados de um backup.
const (
	BackupRunning = "running"
	BackupOK      = "ok"
	BackupFailed  = "failed"
)

// SystemFlowBackup é a chave do fluxo de sistema que agenda o backup.
const SystemFlowBackup = "backup"

// ValidBackupKind diz se k é um tipo de backup conhecido.
func ValidBackupKind(k string) bool {
	switch k {
	case BackupManual, BackupScheduled, BackupPreUpdate, BackupPreDeploy:
		return true
	}
	return false
}

// ValidSystemFlowKey diz se k é um fluxo de sistema conhecido.
func ValidSystemFlowKey(k string) bool { return k == SystemFlowBackup }

// InstanceBackup é uma linha do histórico de backups da instalação. Não tem company_id: um backup leva o banco inteiro.
type InstanceBackup struct {
	ID            ID         `json:"id"`
	Kind          string     `json:"kind"`
	Status        string     `json:"status"`
	Label         string     `json:"label,omitempty"`
	FileName      string     `json:"file_name,omitempty"`
	SizeBytes     int64      `json:"size_bytes"`
	SchemaVersion int64      `json:"schema_version"`
	Blobs         int        `json:"blobs"`
	Error         string     `json:"error,omitempty"`
	ActorType     string     `json:"actor_type"`
	ActorID       *ID        `json:"actor_id,omitempty"`
	ScheduledFor  *time.Time `json:"scheduled_for,omitempty"`
	StartedAt     time.Time  `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
}

// Duration é quanto o backup levou (zero enquanto roda).
func (b InstanceBackup) Duration() time.Duration {
	if b.FinishedAt == nil {
		return 0
	}
	return b.FinishedAt.Sub(b.StartedAt)
}

// SystemFlow é uma tarefa periódica da instalação (sem persona nem task).
type SystemFlow struct {
	Key         string     `json:"key"`
	Cron        string     `json:"cron"`
	Timezone    string     `json:"timezone"`
	Enabled     bool       `json:"enabled"`
	LastFiredAt *time.Time `json:"last_fired_at,omitempty"`
	NextFireAt  *time.Time `json:"next_fire_at,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at"`
}
