package client

import (
	"context"
	"net/http"

	"github.com/renatobardi/hanko/internal/api"
)

// GetBackupSchedule devolve o agendamento do backup (dono da instalação).
func (c *Client) GetBackupSchedule(ctx context.Context) (api.BackupSchedule, error) {
	return get[api.BackupSchedule](ctx, c, http.MethodGet, "/instance/backup-schedule", nil)
}

// SetBackupSchedule muda o agendamento; req leva só os campos a mudar (enabled, cron, timezone).
func (c *Client) SetBackupSchedule(ctx context.Context, req map[string]any) (api.BackupSchedule, error) {
	return get[api.BackupSchedule](ctx, c, http.MethodPut, "/instance/backup-schedule", req)
}

// ListInstanceBackups lista o histórico de backups.
func (c *Client) ListInstanceBackups(ctx context.Context) ([]api.InstanceBackup, error) {
	return list[api.InstanceBackup](ctx, c, "/instance/backups", nil)
}

// StartInstanceBackup dispara um backup em segundo plano.
func (c *Client) StartInstanceBackup(ctx context.Context, label string) (api.InstanceBackup, error) {
	return get[api.InstanceBackup](ctx, c, http.MethodPost, "/instance/backups", map[string]string{"label": label})
}
