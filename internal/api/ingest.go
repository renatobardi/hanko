package api

import (
	"context"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"time"

	"google.golang.org/protobuf/proto"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/ingest"
	"github.com/renatobardi/hanko/internal/service"
)

// Listener de ingestão (ADR-0076). Atende só as rotas de ingestão, só com a chave de escopo `ingest`. O listener
// público não monta estas rotas: ele fica em HANKO_INGEST_ADDR, na tailnet.

// IngestService é o que o listener usa do serviço.
type IngestService interface {
	ResolveIngestKey(ctx context.Context, key string) (companyID, keyID domain.ID, err error)
	IngestTraces(ctx context.Context, companyID, keyID domain.ID, spans []ingest.Span) (service.TraceIngestResult, error)
}

// ingestRetryAfter são os segundos que o collector espera antes de tentar de novo (503, ADR-0076 item 7).
const ingestRetryAfter = "5"

type ingestAPI struct {
	svc IngestService
	log *slog.Logger
}

// IngestHandler monta o listener de ingestão. Sem chave ingest válida, nenhuma rota responde além do 401 e do 403.
func IngestHandler(svc IngestService, logger *slog.Logger) http.Handler {
	h := &ingestAPI{svc: svc, log: logger}
	mux := http.NewServeMux()
	mux.Handle("POST /v1/traces", h.authed(h.traces))
	for _, route := range []string{"POST /v1/logs", "POST /v1/metrics", "POST /v1/replay/{signal}", "GET /v1/ready"} {
		mux.Handle(route, h.authed(notImplementedIngest))
	}
	return mux
}

type ingestHandlerFunc func(w http.ResponseWriter, r *http.Request, companyID, keyID domain.ID)

// authed confere a chave ingest antes da rota: 401 sem chave válida, 403 para chave de outro escopo.
func (h *ingestAPI) authed(next ingestHandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := auth.BearerToken(r.Header.Get("Authorization"))
		if !ok || !auth.IsAPIKey(token) {
			http.Error(w, "sem chave de ingestão", http.StatusUnauthorized)
			return
		}
		companyID, keyID, err := h.svc.ResolveIngestKey(r.Context(), token)
		switch {
		case err == nil:
			next(w, r, companyID, keyID)
		case errors.Is(err, domain.ErrUnauthenticated):
			http.Error(w, "chave de ingestão inválida ou revogada", http.StatusUnauthorized)
		case errors.Is(err, domain.ErrNotAllowed):
			http.Error(w, "a chave não tem o escopo ingest", http.StatusForbidden)
		default:
			h.unavailable(w, "resolver a chave de ingestão", err)
		}
	})
}

// traces recebe um ExportTraceServiceRequest em protobuf, com gzip ou sem. 2xx só depois do commit.
func (h *ingestAPI) traces(w http.ResponseWriter, r *http.Request, companyID, keyID domain.ID) {
	if !isProtobuf(r.Header.Get("Content-Type")) {
		http.Error(w, "só application/x-protobuf", http.StatusUnsupportedMediaType)
		return
	}
	gzipped, ok := contentEncoding(r.Header.Get("Content-Encoding"))
	if !ok {
		http.Error(w, "Content-Encoding aceita: gzip ou ausente", http.StatusBadRequest)
		return
	}
	raw, err := ingest.Inflate(r.Body, gzipped)
	if err != nil {
		h.badOrUnavailable(w, "ler o corpo", err)
		return
	}
	spans, err := ingest.DecodeTraces(raw, time.Now().UnixNano())
	if err != nil {
		h.badOrUnavailable(w, "decodificar os spans", err)
		return
	}
	if _, err := h.svc.IngestTraces(r.Context(), companyID, keyID, spans); err != nil {
		h.unavailable(w, "gravar os spans", err)
		return
	}
	ack, err := proto.Marshal(&collectortrace.ExportTraceServiceResponse{})
	if err != nil {
		h.unavailable(w, "montar a resposta", err)
		return
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	_, _ = w.Write(ack)
}

func notImplementedIngest(w http.ResponseWriter, _ *http.Request, _, _ domain.ID) {
	http.Error(w, "ainda não implementada", http.StatusNotImplemented)
}

// unavailable responde 503 com Retry-After: a gravação falhou ou a ingestão está cheia; o collector retenta sem prazo.
func (h *ingestAPI) unavailable(w http.ResponseWriter, what string, err error) {
	h.log.Warn("ingestão: falha ao "+what, "err", err)
	w.Header().Set("Retry-After", ingestRetryAfter)
	http.Error(w, "ingestão indisponível, tente de novo", http.StatusServiceUnavailable)
}

// badOrUnavailable responde 400 para corpo inválido (o collector não retenta) e 503 para o resto.
func (h *ingestAPI) badOrUnavailable(w http.ResponseWriter, what string, err error) {
	if errors.Is(err, ingest.ErrBadPayload) {
		http.Error(w, "corpo inválido", http.StatusBadRequest)
		return
	}
	h.unavailable(w, what, err)
}

func isProtobuf(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	return err == nil && mt == "application/x-protobuf"
}

// contentEncoding diz se o corpo é gzip. Qualquer outra codificação é recusada.
func contentEncoding(v string) (gzipped, ok bool) {
	switch v {
	case "":
		return false, true
	case "gzip":
		return true, true
	}
	return false, false
}
