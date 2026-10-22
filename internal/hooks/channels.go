package hooks

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// MaxChannelBody é o maior corpo aceito de um provedor de canal.
const MaxChannelBody = 2 << 20

// ChannelBackend é o que a rota de entrada de canal precisa do service.
type ChannelBackend interface {
	IngestChannel(ctx context.Context, in service.IngestInput) (service.IngestResult, error)
}

// Channels é o handler de POST /hooks/channels/{token}: o provedor (Telegram, GitHub) entrega um evento. O service
// autentica com o segredo do canal, grava e dispara o processamento; a resposta é 200 assim que o evento está gravado
// (o provedor não espera a persona nem o modelo). Corpo inválido depois de autenticado também é 200: um evento
// envenenado repetido pelo provedor travaria a fila de quem vem depois dele.
func Channels(b ChannelBackend, log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := readBody(w, r, MaxChannelBody)
		if !ok {
			return
		}
		res, err := b.IngestChannel(r.Context(), service.IngestInput{Token: r.PathValue("token"), Header: r.Header, Body: body})
		switch {
		case err == nil:
			writeJSON(w, http.StatusOK, res)
		case errors.Is(err, service.ErrWebhookUnauthorized):
			problem(w, http.StatusUnauthorized, "não autenticado")
		case errors.Is(err, domain.ErrNotFound):
			problem(w, http.StatusNotFound, "canal não encontrado")
		case errors.Is(err, domain.ErrInvalid):
			log.WarnContext(r.Context(), "hooks: evento de canal inválido", "err", err)
			writeJSON(w, http.StatusOK, map[string]string{"error": "evento inválido"})
		default:
			log.ErrorContext(r.Context(), "hooks: falha ao receber evento de canal", "err", err)
			problem(w, http.StatusInternalServerError, "erro interno")
		}
	})
}
