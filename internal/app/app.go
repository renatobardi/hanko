// Package app monta o processo hanko: telemetria, banco, heartbeat, service, API, UI e MCP.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/adapter/acp"
	"github.com/renatobardi/hanko/internal/adapter/claude"
	"github.com/renatobardi/hanko/internal/adapter/codex"
	"github.com/renatobardi/hanko/internal/adapter/fake"
	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/config"
	"github.com/renatobardi/hanko/internal/db"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/execenv"
	"github.com/renatobardi/hanko/internal/heartbeat"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/memory/aimemory"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/settings"
	"github.com/renatobardi/hanko/internal/telemetry"
	"github.com/renatobardi/hanko/internal/vault"
)

const serviceName = "hanko"

// Serve sobe o Hanko e bloqueia até ctx ser cancelado (SIGINT/SIGTERM no cmd),
// encerrando na ordem inversa da montagem, em até ShutdownGrace.
func Serve(ctx context.Context, cfg config.Config, version string) error {
	tel, logger, err := setupTelemetry(ctx, cfg, version)
	if err != nil {
		return err
	}
	defer shutdownTelemetry(tel)

	pool, err := db.Open(ctx, cfg.DatabaseURL, logger)
	if err != nil {
		return err
	}
	defer pool.Close()
	db.CheckLocale(ctx, pool, logger)

	if cfg.MigrateOnBoot {
		if err := db.Migrate(ctx, pool, logger, migrateOptions()); err != nil {
			return err
		}
	} else {
		logger.Info("migrations no boot desligadas (HANKO_MIGRATE_ON_BOOT=false)")
	}

	broker := events.NewBroker()
	hb, err := heartbeat.New(pool, heartbeat.Options{
		Workers:          cfg.Workers,
		PublicURL:        cfg.AgentURL,
		RunTimeout:       cfg.RunTimeout,
		PeriodicInterval: cfg.HeartbeatInterval,
		WorkspacesRoot:   cfg.WorkspacesRoot,
		Logger:           logger,

		ToolsSingleEndpoint: cfg.ToolsSingleEndpoint,
	})
	if err != nil {
		return fmt.Errorf("app: heartbeat: %w", err)
	}
	svc := service.New(pool, hb, broker)
	svc.Log = logger
	svc.WorkspacesRoot = cfg.WorkspacesRoot
	svc.RunReserveUSD = cfg.RunReserveUSD
	svc.ArtifactMaxBytes, svc.ArtifactQuotaBytes = cfg.ArtifactMaxBytes, cfg.ArtifactQuotaBytes
	svc.MemoryProvider = cfg.Memory.Provider
	svc.Alerts = service.AlertConfig{SilentFactor: cfg.AlertSilentFactor, RunStuckAfter: cfg.AlertRunStuckAfter}
	box, err := secretbox.New(cfg.SecretKey, cfg.SecretKeyPrevious)
	if err != nil {
		return fmt.Errorf("app: HANKO_SECRET_KEY: %w", err)
	}
	svc.Integrations = service.IntegrationsConfig{
		AllowStdio: cfg.Integrations.AllowStdio, AllowPrivate: cfg.Integrations.AllowPrivateDestinations, Secrets: box,
	}
	svc.ACP = acp.Policy{Allow: cfg.ACP.Allow, Commands: cfg.ACP.Commands}

	// Settings da instância (ADR-0050): env > banco > yaml/padrão; leitura em tempo de uso, então mudar vale sem restart.
	sm := settings.NewManager(settingsBase(cfg), cfg.EnvSet)
	sm.SetMaxArtifactBytes(cfg.MaxArtifactBytes())
	svc.Settings = sm
	svc.Integrations.Settings = sm
	if skipped, err := svc.LoadInstanceSettings(ctx); err != nil {
		logger.Warn("instance_settings: não foi possível ler o banco; valem env e padrões", "err", err)
	} else if len(skipped) > 0 {
		logger.Warn("instance_settings: chaves ignoradas (desconhecidas ou inválidas)", "keys", skipped)
	}

	// Conteúdo de artefatos (ADR-0051) e cofre externo de segredos (ADR-0050).
	blobs, err := NewBlobRegistry(cfg, pool)
	if err != nil {
		return err
	}
	svc.Blobs = blobs
	logger.Info("artefatos: driver de conteúdo", "active", blobs.Active().Name(), "available", blobs.Names())
	if cfg.Vault.Addr != "" {
		v, err := vault.NewKV2(vault.KV2Options{Addr: cfg.Vault.Addr, Token: cfg.Vault.Token, Mount: cfg.Vault.Mount, Namespace: cfg.Vault.Namespace})
		if err != nil {
			return fmt.Errorf("app: cofre externo: %w", err)
		}
		svc.Vault = v
		logger.Info("segredos: cofre externo configurado", "provider", v.Name(), "addr", cfg.Vault.Addr)
	}
	svc.SkillSources = service.SkillSourcesConfig{Enabled: cfg.SkillSources, AllowPrivate: cfg.Integrations.AllowPrivateDestinations}
	if cfg.SkillSources {
		logger.Info("skills: fontes ligadas (HANKO_SKILL_SOURCES); o servidor clona repositórios públicos por https")
	}
	svc.PublicURL = cfg.PublicURL
	svc.Environments = execenv.Config{SSHAllowHosts: cfg.Environments.SSHAllowHosts, Sandbox: cfg.Environments.Sandbox,
		SandboxHostRoot: cfg.Environments.SandboxHostRoot, WorkspacesRoot: cfg.WorkspacesRoot, RemoteURL: cfg.PublicURL}
	if len(cfg.Environments.SSHAllowHosts) > 0 || cfg.Environments.Sandbox != "" {
		logger.Warn("ambientes remotos liberados pelo operador (ADR-0037)", "ssh_allow_hosts", cfg.Environments.SSHAllowHosts, "sandbox", cfg.Environments.Sandbox)
	}
	if cfg.ACP.Allow {
		logger.Warn("acp: harness liberado (HANKO_ACP_ALLOW); personas acp executam comandos nesta máquina", "commands", cfg.ACP.Commands)
	}
	switch {
	case !box.Enabled():
		logger.Info("integrações: sem HANKO_SECRET_KEY; integrações com segredo (headers de MCP, webhooks) não podem ser salvas")
	case cfg.Integrations.AllowStdio:
		logger.Warn("integrações: servidores MCP stdio liberados (HANKO_INTEGRATIONS_ALLOW_STDIO); eles executam comandos nesta máquina")
	}
	startPlugins, stopPlugins := setupPlugins(cfg, svc, logger, version)
	defer stopPlugins()
	if err := registerAlertMetrics(otel.Meter(serviceName), svc); err != nil {
		return err
	}
	migratePersonaEnvAtBoot(ctx, svc, box.Enabled(), logger)

	// Runs de antes dos nomes amigáveis ganham o delas; falha aqui só deixa o nome vazio (a UI cai no id curto).
	if n, err := svc.BackfillRunNames(ctx); err != nil {
		logger.Warn("nomes amigáveis de execução: backfill falhou", "err", err)
	} else if n > 0 {
		logger.Info("nomes amigáveis de execução atribuídos", "count", n)
	}

	mem := newMemory(ctx, cfg, logger)
	reg := adapter.Registry{
		"claude": claude.New(claude.Options{Bin: cfg.ClaudeBin, Logger: logger, EnvAllow: cfg.AgentEnvAllow}),
		"codex":  codex.New(codex.Options{Bin: cfg.CodexBin, Logger: logger, EnvAllow: cfg.AgentEnvAllow}),
		"acp": acp.New(acp.Options{Logger: logger, Version: version, EnvAllow: cfg.AgentEnvAllow,
			Policy: acp.Policy{Allow: cfg.ACP.Allow, Commands: cfg.ACP.Commands},
			PolicyFunc: func() acp.Policy {
				return acp.Policy{Allow: sm.Bool(settings.ACPAllow), Commands: sm.Strings(settings.ACPCommands)}
			}}),
		"fake": fake.New(fake.Options{}),
	}
	tokens := auth.RunTokens{Secret: []byte(cfg.Secret), TTL: cfg.RunTimeout + 5*time.Minute}
	hb.Attach(svc, reg, mem, broker, tokens)
	svc.Memory = mem

	handler, err := buildHandler(handlerDeps{Cfg: cfg, Version: version, Logger: logger, Svc: svc, Broker: broker,
		Pool: pool, Tokens: tokens, Metrics: tel.MetricsHandler(), Memory: mem})
	if err != nil {
		return err
	}
	srv := newHTTPServer(cfg.Addr, handler, logger)

	var hbStarted bool
	err = serve(ctx, srv, ShutdownGrace, logger, func(_ net.Addr) error {
		if err := hb.Start(ctx); err != nil {
			return fmt.Errorf("app: iniciar heartbeat: %w", err)
		}
		hbStarted = true
		startPlugins(ctx)
		logger.Info("hanko pronto", "version", version, "public_url", cfg.PublicURL, "mode", cfg.Mode, "memory", mem.Name(), "workers", cfg.Workers)
		return nil
	})

	if hbStarted {
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ShutdownGrace)
		defer cancel()
		if herr := hb.Stop(sctx); herr != nil {
			err = errors.Join(err, fmt.Errorf("app: parar heartbeat: %w", herr))
		}
	}
	return err
}

// Migrate aplica goose + river e termina (comando `hanko migrate`).
func Migrate(ctx context.Context, cfg config.Config, version string) error {
	tel, logger, err := setupTelemetry(ctx, cfg, version)
	if err != nil {
		return err
	}
	defer shutdownTelemetry(tel)

	pool, err := db.Open(ctx, cfg.DatabaseURL, logger)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool, logger, migrateOptions()); err != nil {
		return err
	}
	logger.Info("migrations concluídas")
	return nil
}

func migrateOptions() db.MigrateOptions {
	return db.MigrateOptions{Extra: []func(context.Context, *pgxpool.Pool) error{heartbeat.Migrate}}
}

func setupTelemetry(ctx context.Context, cfg config.Config, version string) (*telemetry.Telemetry, *slog.Logger, error) {
	tel, err := telemetry.Setup(ctx, telemetry.Options{
		ServiceName: serviceName, Version: version, LogLevel: cfg.LogLevel, LogFormat: cfg.LogFormat,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("app: telemetria: %w", err)
	}
	for _, w := range cfg.Warnings {
		tel.Logger.Warn(w)
	}
	return tel, tel.Logger, nil
}

func shutdownTelemetry(tel *telemetry.Telemetry) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tel.Shutdown(ctx) // collector fora nunca vira erro do processo
}

// newMemory escolhe o provedor (ADR-0015). Ping no boot só avisa: o Hanko degrada sem memória.
func newMemory(ctx context.Context, cfg config.Config, logger *slog.Logger) memory.Provider {
	if cfg.Memory.Provider != config.MemoryAIMemory {
		logger.Info("memória desligada (HANKO_MEMORY_PROVIDER=none)")
		return memory.Noop{}
	}
	p := aimemory.New(aimemory.Options{URL: cfg.Memory.URL, Token: cfg.Memory.Token, PublicURL: cfg.AgentURL, ReadPageTool: cfg.Memory.ReadTool, Logger: logger})
	go func() {
		pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if err := p.Ping(pctx); err != nil {
			logger.Warn("ai-memory indisponível no boot; runs abrem sem briefing e fatos ficam no outbox até voltar",
				"url", cfg.Memory.URL, "error", err.Error())
		}
	}()
	return p
}
