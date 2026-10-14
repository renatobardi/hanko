//go:build !wiring

package app

import (
	"context"
	"errors"

	"github.com/renatobardi/hanko/internal/config"
)

// ErrNotWired indica que a montagem final (app.go, tag `wiring`) ainda não foi
// habilitada: ela depende dos pacotes das demais trilhas. O orquestrador remove
// a tag de app.go e apaga este arquivo ao mesclar as trilhas.
var ErrNotWired = errors.New("app: wiring completo ainda não habilitado (build tag wiring)")

// Serve sobe o Hanko. Stub até o merge das trilhas.
func Serve(context.Context, config.Config, string) error { return ErrNotWired }

// Migrate aplica as migrations. Stub até o merge das trilhas.
func Migrate(context.Context, config.Config, string) error { return ErrNotWired }
