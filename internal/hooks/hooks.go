// Package hooks são as rotas públicas de entrada sob /hooks/: o webhook dos gatilhos de fluxo (ADR-0047) e o dos canais
// externos (ADR-0046). Elas ficam fora do login: a autenticação é do próprio protocolo (HMAC, segredo do canal), feita
// pelo service. O rate limit por IP vem do middleware geral (ratelimit.WebhookPrefix).
package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/webhook"
)

// FlowBackend é o que a rota do gatilho de fluxo precisa do service.
type FlowBackend interface {
	FireFlowWebhook(ctx context.Context, in service.WebhookFireInput) (service.FlowFireResult, error)
}

// Padrões das rotas (net/http 1.22).
const (
	FlowPattern    = "POST /hooks/flows/{id}"
	ChannelPattern = "POST /hooks/channels/{token}"
)

// writeJSON responde com um objeto pequeno.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func problem(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// readBody lê o corpo até max bytes; false = já respondeu (413 ou erro de leitura).
func readBody(w http.ResponseWriter, r *http.Request, max int64) ([]byte, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, max))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			problem(w, http.StatusRequestEntityTooLarge, "corpo grande demais")
		} else {
			problem(w, http.StatusBadRequest, "não foi possível ler o corpo")
		}
		return nil, false
	}
	return body, true
}

// Flows é o handler de POST /hooks/flows/{id}: dispara o fluxo do gatilho (HMAC no cabeçalho X-Hanko-Signature).
func Flows(b FlowBackend, log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := domain.ParseID(r.PathValue("id"))
		if err != nil {
			problem(w, http.StatusNotFound, "gatilho não encontrado")
			return
		}
		body, ok := readBody(w, r, domain.MaxFlowPayloadBytes)
		if !ok {
			return
		}
		delivery := r.Header.Get(webhook.HeaderDelivery)
		if delivery == "" {
			delivery = r.Header.Get("Idempotency-Key")
		}
		res, err := b.FireFlowWebhook(r.Context(), service.WebhookFireInput{TriggerID: id, Body: body,
			Signature: r.Header.Get(webhook.HeaderSignature), Delivery: delivery})
		switch {
		case err == nil:
			out := map[string]any{"fire_id": res.Fire.ID, "duplicate": res.Duplicate}
			if res.Task != nil {
				out["task_key"] = res.Task.Key
			}
			status := http.StatusAccepted
			if res.Duplicate {
				status = http.StatusOK
			}
			writeJSON(w, status, out)
		case errors.Is(err, service.ErrWebhookUnauthorized):
			problem(w, http.StatusUnauthorized, "assinatura inválida")
		case errors.Is(err, service.ErrWebhookThrottled):
			w.Header().Set("Retry-After", "60")
			problem(w, http.StatusTooManyRequests, "limite do gatilho excedido")
		case errors.Is(err, domain.ErrNotFound):
			problem(w, http.StatusNotFound, "gatilho não encontrado")
		case errors.Is(err, domain.ErrInvalid):
			problem(w, http.StatusUnprocessableEntity, err.Error())
		case errors.Is(err, domain.ErrConflict):
			problem(w, http.StatusConflict, "o gatilho ou o fluxo está desativado ou arquivado")
		default:
			log.ErrorContext(r.Context(), "hooks: falha ao disparar fluxo", "trigger_id", id, "err", err)
			problem(w, http.StatusInternalServerError, "erro interno")
		}
	})
}
