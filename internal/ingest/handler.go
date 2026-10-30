package ingest

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
)

const (
	// MaxBodyBytes é o maior corpo, comprimido ou não: acima disso é 400, e o collector não retenta (ADR-0076 item 7).
	MaxBodyBytes = 64 << 20
	// DefaultMaxInFlight é o número de requisições gravando ao mesmo tempo; a próxima recebe 503 e o collector retenta.
	DefaultMaxInFlight = 16
	retryAfterSeconds  = 5
	writeTimeout       = 60 * time.Second
)

// Errors que o resolvedor da chave devolve; o handler as traduz em 401 e 403.
var (
	ErrUnauthenticated = errors.New("chave de ingestão ausente, inválida ou revogada")
	ErrWrongScope      = errors.New("a chave não tem o escopo ingest")
)

// Key é a chave de ingestão que entregou o corpo: a empresa dona dos fatos e o id da chave ("entregue por").
type Key struct {
	ID        uuid.UUID
	CompanyID uuid.UUID
}

// Batch são as linhas de um corpo.
type Batch struct {
	Spans   []SpanRow
	Logs    []LogRow
	Metrics []MetricRow
}

// Len é o número de linhas do lote.
func (b Batch) Len() int { return len(b.Spans) + len(b.Logs) + len(b.Metrics) }

// Resolver acha a chave de ingestão de um token.
type Resolver interface {
	ResolveIngestKey(ctx context.Context, token string) (Key, error)
}

// Writer grava o lote de uma empresa. Um fato que já está gravado não duplica; a gravação é uma transação só, e o
// handler responde 2xx depois do commit (ADR-0076 item 7).
type Writer interface {
	WriteTelemetry(ctx context.Context, key Key, b Batch) error
}

// Deps são as dependências do handler da ingestão.
type Deps struct {
	Resolver Resolver
	Writer   Writer
	// Ready diz se a ingestão está pronta (por exemplo, o banco responde e há partição para o mês corrente).
	Ready       func(ctx context.Context) error
	Now         func() time.Time
	Log         *slog.Logger
	MaxInFlight int
}

type handler struct {
	Deps
	sem chan struct{}
}

// NewHandler devolve o handler do listener da ingestão: OTLP/HTTP em protobuf, o replay do bucket e a prontidão. Só
// aceita a chave de escopo `ingest` (ADR-0076 itens 1 e 3).
func NewHandler(d Deps) http.Handler {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	if d.MaxInFlight <= 0 {
		d.MaxInFlight = DefaultMaxInFlight
	}
	h := &handler{Deps: d, sem: make(chan struct{}, d.MaxInFlight)}
	mux := http.NewServeMux()
	for _, s := range []Signal{SignalTraces, SignalLogs, SignalMetrics} {
		mux.HandleFunc("POST /v1/"+string(s), h.ingest(s))
	}
	mux.HandleFunc("POST /v1/replay/{signal}", h.replay)
	mux.HandleFunc("GET /v1/ready", h.ready)
	return mux
}

func (h *handler) ready(w http.ResponseWriter, r *http.Request) {
	if h.Ready != nil {
		if err := h.Ready(r.Context()); err != nil {
			h.Log.WarnContext(r.Context(), "ingestão: não está pronta", "err", err)
			http.Error(w, "indisponível", http.StatusServiceUnavailable)
			return
		}
	}
	_, _ = w.Write([]byte("ok"))
}

func (h *handler) ingest(sig Signal) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "application/x-protobuf" {
			http.Error(w, "só application/x-protobuf", http.StatusUnsupportedMediaType)
			return
		}
		h.process(w, r, sig, func(body []byte) (any, error) { return FromProtobuf(sig, body) })
	}
}

func (h *handler) replay(w http.ResponseWriter, r *http.Request) {
	sig := Signal(r.PathValue("signal"))
	if !sig.Valid() {
		http.Error(w, "sinal desconhecido", http.StatusNotFound)
		return
	}
	h.process(w, r, sig, DecodeJSON)
}

// process faz o que as quatro rotas de gravação têm em comum: autentica, lê o corpo, monta as linhas e grava.
func (h *handler) process(w http.ResponseWriter, r *http.Request, sig Signal, decode func([]byte) (any, error)) {
	key, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	select {
	case h.sem <- struct{}{}:
		defer func() { <-h.sem }()
	default:
		h.busy(w)
		return
	}
	body, err := readBody(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	doc, err := decode(body)
	if err == nil {
		var batch Batch
		if batch, err = h.rows(sig, doc); err == nil {
			h.write(w, r, key, batch)
			return
		}
	}
	h.Log.WarnContext(r.Context(), "ingestão: corpo recusado", "signal", string(sig), "err", err)
	http.Error(w, "corpo inválido", http.StatusBadRequest)
}

func (h *handler) rows(sig Signal, doc any) (Batch, error) {
	received := h.Now().UnixNano()
	var b Batch
	var err error
	switch sig {
	case SignalTraces:
		b.Spans, err = SpanRows(doc, received)
	case SignalLogs:
		b.Logs, err = LogRows(doc, received)
	case SignalMetrics:
		b.Metrics, err = MetricRows(doc, received)
	}
	return b, err
}

func (h *handler) write(w http.ResponseWriter, r *http.Request, key Key, b Batch) {
	ctx, cancel := context.WithTimeout(r.Context(), writeTimeout)
	defer cancel()
	if b.Len() > 0 {
		if err := h.Writer.WriteTelemetry(ctx, key, b); err != nil {
			h.Log.ErrorContext(r.Context(), "ingestão: gravar falhou", "err", err)
			h.busy(w)
			return
		}
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK) // resposta OTLP vazia: sem sucesso parcial
}

func (h *handler) busy(w http.ResponseWriter) {
	w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds))
	http.Error(w, "ingestão ocupada ou gravação falhou; tente de novo", http.StatusServiceUnavailable)
}

func (h *handler) authenticate(w http.ResponseWriter, r *http.Request) (Key, bool) {
	const prefix = "Bearer "
	auth := r.Header.Get("Authorization")
	if len(auth) <= len(prefix) || !equalFold(auth[:len(prefix)], prefix) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="hanko-ingest"`)
		http.Error(w, "chave ausente", http.StatusUnauthorized)
		return Key{}, false
	}
	key, err := h.Resolver.ResolveIngestKey(r.Context(), auth[len(prefix):])
	switch {
	case err == nil:
		return key, true
	case errors.Is(err, ErrWrongScope):
		http.Error(w, "a chave não tem o escopo ingest", http.StatusForbidden)
	case errors.Is(err, ErrUnauthenticated):
		w.Header().Set("WWW-Authenticate", `Bearer realm="hanko-ingest"`)
		http.Error(w, "chave inválida ou revogada", http.StatusUnauthorized)
	default:
		h.Log.ErrorContext(r.Context(), "ingestão: resolver chave falhou", "err", err)
		h.busy(w)
	}
	return Key{}, false
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if 'A' <= x && x <= 'Z' {
			x += 'a' - 'A'
		}
		if 'A' <= y && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

// readBody lê o corpo e, com Content-Encoding: gzip, o descomprime; os dois lados têm o teto de MaxBodyBytes.
func readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	if err != nil {
		return nil, errors.New("corpo ilegível ou acima do limite")
	}
	switch enc := r.Header.Get("Content-Encoding"); enc {
	case "", "identity":
		return raw, nil
	case "gzip":
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, errors.New("gzip inválido")
		}
		defer zr.Close()
		out, err := io.ReadAll(io.LimitReader(zr, MaxBodyBytes+1))
		if err != nil {
			return nil, errors.New("gzip inválido")
		}
		if len(out) > MaxBodyBytes {
			return nil, errors.New("corpo acima de 64 MB descomprimido")
		}
		return out, nil
	default:
		return nil, errors.New("Content-Encoding não suportado")
	}
}
