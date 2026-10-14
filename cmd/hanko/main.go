// Comando hanko: servidor e CLI do Hanko.
package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/cli"
)

// version é injetada no build: -ldflags "-X main.version=v0.1.0".
var version = "dev"

// errNotWired será substituído na integração por internal/app (Serve/Migrate).
var errNotWired = errors.New("não conectado: serve/migrate dependem de internal/app (integração pendente)")

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Execute(ctx, version, cli.Runtime{
		Serve:   func(context.Context) error { return errNotWired },
		Migrate: func(context.Context) error { return errNotWired },
		OpenAPI: api.Spec,
	})
	stop()
	os.Exit(code)
}
