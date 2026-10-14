package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/renatobardi/hanko/internal/cli"
	"github.com/renatobardi/hanko/internal/config"
	"github.com/renatobardi/hanko/internal/db"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// authAdmin é o cli.AuthAdmin sobre o banco: sem servidor, sem heartbeat.
type authAdmin struct {
	*service.Service
	provider  string
	closePool func()
}

// OpenAuthAdmin conecta ao banco da configuração para os comandos `hanko auth`.
func OpenAuthAdmin(ctx context.Context, cfg config.Config) (cli.AuthAdmin, error) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pool, err := db.Open(ctx, cfg.DatabaseURL, logger)
	if err != nil {
		return nil, err
	}
	return &authAdmin{Service: service.New(pool, nil, nil), provider: cfg.Auth.Provider, closePool: pool.Close}, nil
}

// CreateOwner só vale com o provider local: no firebase, o primeiro login vira dono.
func (a *authAdmin) CreateOwner(ctx context.Context, email, name, password string) (domain.User, error) {
	if a.provider == config.AuthFirebase {
		return domain.User{}, errors.New("HANKO_AUTH_PROVIDER=firebase: o primeiro login pelo Firebase (ou um e-mail em HANKO_AUTH_ALLOWED_EMAILS) vira dono; create-owner é do provider local")
	}
	u, err := a.Service.CreateOwner(ctx, email, name, password)
	if err != nil {
		return domain.User{}, fmt.Errorf("criar dono: %w", err)
	}
	return u, nil
}

func (a *authAdmin) Close() { a.closePool() }
