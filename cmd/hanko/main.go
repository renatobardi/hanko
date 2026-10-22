// Comando hanko: servidor e CLI do Hanko.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/app"
	"github.com/renatobardi/hanko/internal/cli"
	"github.com/renatobardi/hanko/internal/config"
	"github.com/renatobardi/hanko/internal/storage"
)

// version é injetada no build: -ldflags "-X main.version=v0.1.0".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Execute(ctx, version, cli.Runtime{
		Serve: func(ctx context.Context) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			return app.Serve(ctx, cfg, version)
		},
		Migrate: func(ctx context.Context) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			return app.Migrate(ctx, cfg, version)
		},
		BlobsMigrate: func(ctx context.Context, a cli.BlobsMigrateArgs) (storage.MigrateReport, error) {
			cfg, err := config.Load()
			if err != nil {
				return storage.MigrateReport{}, err
			}
			return app.BlobsMigrate(ctx, cfg, version, a.From, a.To, a.Company, a.KeepSource, a.DryRun, a.Batch)
		},
		Doctor: app.Doctor,
		Backup: func(ctx context.Context) (cli.BackupAdmin, error) {
			cfg, err := config.Load()
			if err != nil {
				return nil, err
			}
			return app.OpenBackupAdmin(ctx, cfg, version)
		},
		ServeEmbedded: func(ctx context.Context, o cli.EmbeddedOptions) error { return app.ServeEmbedded(ctx, version, o) },
		OpenAPI:       api.Spec,
		Auth: func(ctx context.Context) (cli.AuthAdmin, error) {
			cfg, err := config.Load()
			if err != nil {
				return nil, err
			}
			return app.OpenAuthAdmin(ctx, cfg)
		},
	})
	stop()
	os.Exit(code)
}
