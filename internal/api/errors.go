package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/telemetry"
)

// statusFor é o único mapeador de erro de domínio para status HTTP.
// known é false para erros inesperados (viram 500 sem detalhes).
func statusFor(err error) (status int, known bool) {
	switch {
	case err == nil:
		return http.StatusOK, true
	case isReauthRequired(err), errors.Is(err, domain.ErrUnauthenticated):
		return http.StatusUnauthorized, true
	case errors.Is(err, domain.ErrTooManyAttempts):
		return http.StatusTooManyRequests, true
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound, true
	case errors.Is(err, domain.ErrInvalid):
		return http.StatusUnprocessableEntity, true
	case errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrActiveRun):
		return http.StatusConflict, true
	case errors.Is(err, domain.ErrNotAllowed):
		return http.StatusForbidden, true
	case errors.Is(err, auth.ErrBadToken):
		return http.StatusUnauthorized, true
	case errors.Is(err, service.ErrNotImplemented):
		return http.StatusNotImplemented, true
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, true
	}
	return http.StatusInternalServerError, false
}

// isReauthRequired diz se o erro pede uma prova recente de identidade (ADR-0070).
func isReauthRequired(err error) bool {
	var re *domain.ReauthError
	return errors.As(err, &re)
}

var titles = map[int]string{
	http.StatusUnauthorized:        "Não autenticado",
	http.StatusForbidden:           "Proibido",
	http.StatusNotFound:            "Não encontrado",
	http.StatusConflict:            "Conflito",
	http.StatusUnprocessableEntity: "Entrada inválida",
	http.StatusInternalServerError: "Erro interno",
	http.StatusNotImplemented:      "Não implementado",
	http.StatusGatewayTimeout:      "Tempo esgotado",
}

func titleFor(status int) string {
	if t, ok := titles[status]; ok {
		return t
	}
	return http.StatusText(status)
}

// problem monta o corpo de erro (RFC 9457) padrão da API.
func problem(status int, detail string, errs ...*huma.ErrorDetail) *huma.ErrorModel {
	return &huma.ErrorModel{Title: titleFor(status), Status: status, Detail: detail, Errors: errs}
}

// toProblem converte um erro em corpo de resposta. Erros desconhecidos viram 500
// com mensagem genérica: nada interno vaza para o cliente.
func toProblem(err error) (*huma.ErrorModel, bool) {
	var sm huma.StatusError
	if errors.As(err, &sm) {
		if em, ok := sm.(*huma.ErrorModel); ok {
			return em, true
		}
		return problem(sm.GetStatus(), sm.Error()), true
	}
	var re *domain.ReauthError
	if errors.As(err, &re) {
		// reauth_required: a UI abre o pedido de prova e repete a ação (ADR-0070 §2).
		return problem(http.StatusUnauthorized, "reauth_required: prova de identidade recente exigida, "+
			"válida por "+strconv.Itoa(re.WindowMinutes)+" min"), true
	}
	status, known := statusFor(err)
	if !known {
		return problem(status, "erro interno do servidor"), false
	}
	var ve *domain.ValidationError
	if errors.As(err, &ve) {
		return problem(status, "entrada inválida: "+ve.Error(),
			&huma.ErrorDetail{Message: ve.Reason, Location: ve.Field}), true
	}
	if status == http.StatusGatewayTimeout {
		return problem(status, "a operação excedeu o tempo limite"), true
	}
	if status == http.StatusNotImplemented {
		return problem(status, "operação ainda não implementada"), true
	}
	return problem(status, err.Error()), true
}

// fail converte e registra (apenas erros inesperados) o erro de um handler.
func (s *server) fail(ctx context.Context, err error) error {
	p, known := toProblem(err)
	if !known && !errors.Is(err, context.Canceled) { // cliente que saiu no meio não é exceção do servidor
		telemetry.Record(ctx, s.log, telemetry.FromError(telemetry.SourceServer, err), slog.String("request_id", requestIDFrom(ctx)))
	}
	return p
}
