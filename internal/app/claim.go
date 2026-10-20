package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/config"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// claimURL monta o link de claim a partir da URL pública.
func claimURL(publicURL, token string) string {
	return strings.TrimRight(publicURL, "/") + auth.ClaimPath + token
}

// announceOwnerClaim, quando a instalação não tem usuário (authenticated + provider local), emite o claim do primeiro
// dono e imprime o link em w (o stderr do processo). O token NÃO passa pelo logger estruturado: o logger também
// exporta por OTLP e um token de posse não deve ir para o armazenamento de logs (ADR-0058 §A). Falha aqui só avisa.
func announceOwnerClaim(ctx context.Context, svc *service.Service, cfg config.Config, logger *slog.Logger, w io.Writer) {
	if !cfg.Authenticated() || cfg.Auth.Provider != config.AuthLocal {
		return
	}
	c, err := svc.IssueOwnerClaim(ctx)
	switch {
	case errors.Is(err, domain.ErrConflict):
		return // já há usuário
	case err != nil:
		logger.Warn("claim do primeiro dono: não foi possível emitir (use `hanko auth create-owner`)", "err", err)
		return
	}
	logger.Info("sem usuários: claim do primeiro dono emitido (link no stderr do processo)", "expires_at", c.ExpiresAt.Format(time.RFC3339))
	bar := strings.Repeat("=", 72)
	_, _ = fmt.Fprintf(w, "\n%s\n Hanko: ainda não há nenhum usuário. Abra o link abaixo para criar o primeiro dono\n (uso único, vale até %s):\n\n   %s\n\n Perdeu o link? `hanko auth claim-link` emite outro; `hanko auth create-owner` também serve.\n%s\n\n",
		bar, c.ExpiresAt.Local().Format("15:04"), claimURL(cfg.PublicURL, c.Token), bar)
}
