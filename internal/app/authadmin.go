package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
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
	publicURL string
	closePool func()
}

// OpenAuthAdmin conecta ao banco da configuração para os comandos `hanko auth`.
func OpenAuthAdmin(ctx context.Context, cfg config.Config) (cli.AuthAdmin, error) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pool, err := db.Open(ctx, cfg.DatabaseURL, logger)
	if err != nil {
		return nil, err
	}
	svc := service.New(pool, nil, nil)
	// o registro de auditoria das operações de conta vai para o stderr (o stdout é do resultado do comando)
	svc.Log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	return &authAdmin{Service: svc, provider: cfg.Auth.Provider, publicURL: cfg.PublicURL, closePool: pool.Close}, nil
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

// ClaimLink emite um novo link de claim do primeiro dono; só vale com o provider local.
func (a *authAdmin) ClaimLink(ctx context.Context) (string, time.Time, error) {
	if a.provider == config.AuthFirebase {
		return "", time.Time{}, errors.New("HANKO_AUTH_PROVIDER=firebase: o primeiro login pelo Firebase vira dono; claim-link é do provider local")
	}
	c, err := a.IssueOwnerClaim(ctx)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("emitir claim: %w", err)
	}
	return claimURL(a.publicURL, c.Token), c.ExpiresAt, nil
}

// ownerCtx resolve o dono da instalação que opera (por e-mail, ou o único) e põe a identidade dele no
// contexto. O service reconfere o papel no banco; aqui só se escolhe quem.
func (a *authAdmin) ownerCtx(ctx context.Context, ownerEmail string) (context.Context, error) {
	var u domain.User
	if strings.TrimSpace(ownerEmail) != "" {
		got, err := a.GetUserByEmail(ctx, ownerEmail)
		if err != nil {
			return nil, fmt.Errorf("dono %q: %w", ownerEmail, err)
		}
		u = got
	} else {
		us, err := a.ListUsers(ctx)
		if err != nil {
			return nil, err
		}
		var owners []domain.User
		for _, c := range us {
			if c.Role == domain.RoleOwner {
				owners = append(owners, c)
			}
		}
		if len(owners) != 1 {
			return nil, fmt.Errorf("há %d donos de instalação: informe --as: %w", len(owners), domain.ErrInvalid)
		}
		u = owners[0]
	}
	return auth.WithIdentity(ctx, auth.Identity{User: u, Via: auth.ViaSession}), nil
}

func (a *authAdmin) AdminListUsers(ctx context.Context, ownerEmail string) ([]domain.User, error) {
	ctx, err := a.ownerCtx(ctx, ownerEmail)
	if err != nil {
		return nil, err
	}
	return a.Service.AdminListUsers(ctx)
}

func (a *authAdmin) AdminResetPassword(ctx context.Context, ownerEmail, email, newPassword string) (string, error) {
	ctx, err := a.ownerCtx(ctx, ownerEmail)
	if err != nil {
		return "", err
	}
	_, tmp, err := a.Service.AdminResetPassword(ctx, email, newPassword)
	return tmp, err
}

func (a *authAdmin) AdminRemoveUser(ctx context.Context, ownerEmail, email string) error {
	ctx, err := a.ownerCtx(ctx, ownerEmail)
	if err != nil {
		return err
	}
	return a.Service.AdminRemoveUser(ctx, email)
}

func (a *authAdmin) AdminListAudit(ctx context.Context, ownerEmail string, limit int) ([]domain.AdminAuditEntry, error) {
	ctx, err := a.ownerCtx(ctx, ownerEmail)
	if err != nil {
		return nil, err
	}
	return a.Service.AdminListAudit(ctx, limit)
}

func (a *authAdmin) Close() { a.closePool() }
