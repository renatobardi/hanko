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
		OpenAPI: api.Spec,
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
