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
// O gerenciador soma o banco por cima, a menos que a variável de ambiente da chave esteja definida (ADR-0050).
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
		settings.RateLimitEnabled:       cfg.RateLimit.Enabled,
		settings.RateLimitWrites:        cfg.RateLimit.WritesPerMin,
		settings.RateLimitIP:            cfg.RateLimit.IPPerMin,
		settings.RateLimitMCP:           cfg.RateLimit.MCPPerMin,
		settings.RateLimitWebhooks:      cfg.RateLimit.WebhooksPerMin,
		settings.RateLimitBrowserErrors: cfg.RateLimit.BrowserErrPerMin,
	}
}

// NewBlobRegistry monta os drivers de conteúdo de artefatos (ADR-0051, ADR-0065): postgres sempre; s3 e fs quando
// configurados (HANKO_S3_*, HANKO_BLOB_DIR). O driver ativo (HANKO_BLOB_DRIVER) recebe as escritas novas; os demais
// seguem legíveis e servem de origem ou destino de `hanko blobs migrate`.
func NewBlobRegistry(cfg config.Config, pool *pgxpool.Pool) (*storage.Registry, error) {
	byName := map[string]storage.BlobStore{config.BlobPostgres: storage.NewPostgres(pool)}
	if cfg.S3Configured() {
		s3, err := storage.NewS3(storage.S3Config{Endpoint: cfg.Storage.S3.Endpoint, Region: cfg.Storage.S3.Region, Bucket: cfg.Storage.S3.Bucket,
			AccessKey: cfg.Storage.S3.AccessKey, SecretKey: cfg.Storage.S3.SecretKey, Prefix: cfg.Storage.S3.Prefix, PathStyle: cfg.Storage.S3.PathStyle})
		if err != nil {
			return nil, fmt.Errorf("app: driver s3: %w", err)
		}
		byName[config.BlobS3] = s3
	}
	if cfg.Storage.Dir != "" {
		fs, err := storage.NewFS(cfg.Storage.Dir)
		if err != nil {
			return nil, fmt.Errorf("app: driver fs: %w", err)
		}
		byName[config.BlobFS] = fs
	}
	active, ok := byName[cfg.Storage.Driver]
	if !ok {
		switch cfg.Storage.Driver {
		case config.BlobS3:
			return nil, fmt.Errorf("app: HANKO_BLOB_DRIVER=s3 sem a configuração HANKO_S3_*")
		case config.BlobFS:
			return nil, fmt.Errorf("app: HANKO_BLOB_DRIVER=fs sem HANKO_BLOB_DIR")
		}
		return nil, fmt.Errorf("app: HANKO_BLOB_DRIVER %q desconhecido", cfg.Storage.Driver)
	}
	var others []storage.BlobStore
	for _, n := range []string{config.BlobPostgres, config.BlobS3, config.BlobFS} {
		if d, ok := byName[n]; ok && n != cfg.Storage.Driver {
			others = append(others, d)
		}
	}
	return storage.NewRegistry(active, others...), nil
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
