package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/renatobardi/hanko/internal/config"
	"github.com/renatobardi/hanko/internal/db"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/settings"
	"github.com/renatobardi/hanko/internal/storage"
	"github.com/renatobardi/hanko/internal/store"
)

// settingsBase devolve o valor efetivo de cada chave de instance_settings vindo da configuração (env > yaml > padrão).
// O gerenciador soma o banco por cima, a menos que a variável de ambiente da chave esteja definida (ADR-0032).
func settingsBase(cfg config.Config) map[string]any {
	return map[string]any{
		settings.ACPAllow:               cfg.ACP.Allow,
		settings.ACPCommands:            cfg.ACP.Commands,
		settings.IntegrationsAllowStdio: cfg.Integrations.AllowStdio,
		settings.IntegrationsAllowPriv:  cfg.Integrations.AllowPrivateDestinations,
		settings.ArtifactsMaxBytes:      cfg.ArtifactMaxBytes,
		settings.ArtifactsQuotaBytes:    cfg.ArtifactQuotaBytes,
		settings.RunReserveUSD:          cfg.RunReserveUSD,
		settings.AlertsSilentFactor:     int64(cfg.AlertSilentFactor),
		settings.AlertsRunStuckAfter:    cfg.AlertRunStuckAfter,
	}
}

// NewBlobRegistry monta os drivers de conteúdo de artefatos (ADR-0033): postgres sempre; s3 quando configurado.
// O driver ativo (HANKO_BLOB_DRIVER) recebe as escritas novas.
func NewBlobRegistry(cfg config.Config, pool *pgxpool.Pool) (*storage.Registry, error) {
	pg := storage.NewPostgres(pool)
	if !cfg.S3Configured() {
		if cfg.Storage.Driver == config.BlobS3 {
			return nil, fmt.Errorf("app: HANKO_BLOB_DRIVER=s3 sem a configuração HANKO_S3_*")
		}
		return storage.NewRegistry(pg), nil
	}
	s3, err := storage.NewS3(storage.S3Config{Endpoint: cfg.Storage.S3.Endpoint, Region: cfg.Storage.S3.Region, Bucket: cfg.Storage.S3.Bucket,
		AccessKey: cfg.Storage.S3.AccessKey, SecretKey: cfg.Storage.S3.SecretKey, Prefix: cfg.Storage.S3.Prefix, PathStyle: cfg.Storage.S3.PathStyle})
	if err != nil {
		return nil, fmt.Errorf("app: driver s3: %w", err)
	}
	if cfg.Storage.Driver == config.BlobS3 {
		return storage.NewRegistry(s3, pg), nil
	}
	return storage.NewRegistry(pg, s3), nil
}

// migratePersonaEnvAtBoot move o env em claro das personas para segredos cifrados quando há chave (idempotente); sem
// chave, só avisa quantas personas ainda guardam valor em claro. Nunca derruba o boot.
func migratePersonaEnvAtBoot(ctx context.Context, svc *service.Service, hasKey bool, logger *slog.Logger) {
	if !hasKey {
		if n, err := svc.CountPersonasWithPlainEnv(ctx); err == nil && n > 0 {
			logger.Warn("segredos: personas com variáveis de ambiente em claro no banco; defina HANKO_SECRET_KEY para cifrá-las", "personas", n)
		}
		return
	}
	n, err := svc.MigratePersonaEnv(ctx)
	switch {
	case err != nil:
		logger.Warn("segredos: migração do env das personas falhou (tenta de novo no próximo boot)", "err", err)
	case n > 0:
		logger.Info("segredos: env das personas movido para segredos cifrados", "personas", n)
	}
}

// BlobsMigrate move o conteúdo dos artefatos entre drivers (comando `hanko blobs migrate`).
func BlobsMigrate(ctx context.Context, cfg config.Config, version, from, to, company string, keepSource, dryRun bool, batch int) (storage.MigrateReport, error) {
	tel, logger, err := setupTelemetry(ctx, cfg, version)
	if err != nil {
		return storage.MigrateReport{}, err
	}
	defer shutdownTelemetry(tel)
	pool, err := db.Open(ctx, cfg.DatabaseURL, logger)
	if err != nil {
		return storage.MigrateReport{}, err
	}
	defer pool.Close()
	reg, err := NewBlobRegistry(cfg, pool)
	if err != nil {
		return storage.MigrateReport{}, err
	}
	o := storage.MigrateOptions{From: from, To: to, KeepSource: keepSource, DryRun: dryRun, Batch: batch, Log: logger}
	if company != "" {
		q := store.New(pool)
		var c store.Company
		if id, perr := uuid.Parse(company); perr == nil {
			c, err = q.GetCompanyByID(ctx, id)
		} else {
			c, err = q.GetCompanyBySlug(ctx, company)
		}
		if err != nil {
			return storage.MigrateReport{}, fmt.Errorf("app: empresa %q: %w", company, err)
		}
		o.CompanyID = &c.ID
	}
	return storage.Migrate(ctx, pool, reg, o)
}
