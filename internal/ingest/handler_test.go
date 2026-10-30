package ingest

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/protobuf/proto"
)

type fakeResolver struct{}

func (fakeResolver) ResolveIngestKey(_ context.Context, token string) (Key, error) {
	switch token {
	case "hk_ok":
		return Key{ID: uuid.MustParse("00000000-0000-0000-0000-0000000000e1"), CompanyID: uuid.MustParse("00000000-0000-0000-0000-000000000001")}, nil
	case "hk_full":
		return Key{}, ErrWrongScope
	case "hk_boom":
		return Key{}, errors.New("banco fora")
	}
	return Key{}, ErrUnauthenticated
}

type fakeWriter struct {
	mu      sync.Mutex
	batches []Batch
	keys    []Key
	err     error
	block   chan struct{}
	entered chan struct{}
}

func (f *fakeWriter) WriteTelemetry(_ context.Context, k Key, b Batch) error {
	if f.entered != nil {
		close(f.entered)
	}
	if f.block != nil {
		<-f.block
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.batches = append(f.batches, b)
	f.keys = append(f.keys, k)
	return nil
}

func logsBody(t *testing.T) []byte {
	t.Helper()
	b, err := proto.Marshal(&collogs.ExportLogsServiceRequest{ResourceLogs: []*logspb.ResourceLogs{{
		ScopeLogs: []*logspb.ScopeLogs{{LogRecords: []*logspb.LogRecord{{TimeUnixNano: 7, Body: sv("x")}}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func post(t *testing.T, h http.Handler, path, token, ct string, body []byte, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHandlerAuthAndFormat(t *testing.T) {
	w := &fakeWriter{}
	h := NewHandler(Deps{Resolver: fakeResolver{}, Writer: w})
	body := logsBody(t)
	for _, c := range []struct {
		name, token, ct string
		want            int
	}{
		{"sem chave", "", "application/x-protobuf", 401},
		{"chave inválida", "hk_nope", "application/x-protobuf", 401},
		{"chave sem escopo ingest", "hk_full", "application/x-protobuf", 403},
		{"erro ao resolver a chave", "hk_boom", "application/x-protobuf", 503},
		{"JSON no lugar de protobuf", "hk_ok", "application/json", 415},
		{"ok", "hk_ok", "application/x-protobuf", 200},
	} {
		if rec := post(t, h, "/v1/logs", c.token, c.ct, body, nil); rec.Code != c.want {
			t.Errorf("%s: %d, quer %d (%s)", c.name, rec.Code, c.want, strings.TrimSpace(rec.Body.String()))
		}
	}
	if len(w.batches) != 1 || len(w.batches[0].Logs) != 1 || w.keys[0].CompanyID.String() != "00000000-0000-0000-0000-000000000001" {
		t.Fatalf("lote gravado = %+v", w.batches)
	}
	if rec := post(t, h, "/v1/logs", "hk_ok", "application/x-protobuf", []byte{0xff, 0xff}, nil); rec.Code != 400 {
		t.Errorf("protobuf inválido: %d, quer 400", rec.Code)
	}
}

func TestHandlerGzipAndLimits(t *testing.T) {
	w := &fakeWriter{}
	h := NewHandler(Deps{Resolver: fakeResolver{}, Writer: w})
	var zb bytes.Buffer
	zw := gzip.NewWriter(&zb)
	_, _ = zw.Write(logsBody(t))
	_ = zw.Close()
	if rec := post(t, h, "/v1/logs", "hk_ok", "application/x-protobuf", zb.Bytes(), map[string]string{"Content-Encoding": "gzip"}); rec.Code != 200 {
		t.Errorf("gzip: %d, quer 200", rec.Code)
	}
	if rec := post(t, h, "/v1/logs", "hk_ok", "application/x-protobuf", []byte("não é gzip"), map[string]string{"Content-Encoding": "gzip"}); rec.Code != 400 {
		t.Errorf("gzip inválido: %d, quer 400", rec.Code)
	}
	if rec := post(t, h, "/v1/logs", "hk_ok", "application/x-protobuf", logsBody(t), map[string]string{"Content-Encoding": "br"}); rec.Code != 400 {
		t.Errorf("codificação desconhecida: %d, quer 400", rec.Code)
	}
}

func TestHandlerWriteFailureAndBusy(t *testing.T) {
	w := &fakeWriter{err: errors.New("banco fora")}
	h := NewHandler(Deps{Resolver: fakeResolver{}, Writer: w})
	rec := post(t, h, "/v1/logs", "hk_ok", "application/x-protobuf", logsBody(t), nil)
	if rec.Code != 503 || rec.Header().Get("Retry-After") == "" {
		t.Errorf("gravação falhou: %d Retry-After=%q, quer 503 com Retry-After", rec.Code, rec.Header().Get("Retry-After"))
	}

	block, entered := make(chan struct{}), make(chan struct{})
	bw := &fakeWriter{block: block, entered: entered}
	h = NewHandler(Deps{Resolver: fakeResolver{}, Writer: bw, MaxInFlight: 1})
	done := make(chan int, 1)
	go func() { done <- post(t, h, "/v1/logs", "hk_ok", "application/x-protobuf", logsBody(t), nil).Code }()
	<-entered // a primeira requisição está gravando e segura a única vaga
	rec = post(t, h, "/v1/logs", "hk_ok", "application/x-protobuf", logsBody(t), nil)
	if rec.Code != 503 || rec.Header().Get("Retry-After") == "" {
		t.Errorf("ingestão cheia: %d, quer 503 com Retry-After", rec.Code)
	}
	close(block)
	if code := <-done; code != 200 {
		t.Errorf("a primeira requisição: %d, quer 200", code)
	}
}

func TestHandlerReplayAndReady(t *testing.T) {
	w := &fakeWriter{}
	called := 0
	h := NewHandler(Deps{Resolver: fakeResolver{}, Writer: w, Ready: func(context.Context) error {
		called++
		if called > 1 {
			return errors.New("sem partição")
		}
		return nil
	}})
	g, sets := loadGolden(t)
	_ = g
	rec := post(t, h, "/v1/replay/logs", "hk_ok", "application/json", sets["logs"].Payload, nil)
	if rec.Code != 200 || len(w.batches) != 1 || len(w.batches[0].Logs) != 4 {
		t.Fatalf("replay: %d, lotes %d", rec.Code, len(w.batches))
	}
	if rec := post(t, h, "/v1/replay/foo", "hk_ok", "application/json", []byte("{}"), nil); rec.Code != 404 {
		t.Errorf("sinal desconhecido: %d, quer 404", rec.Code)
	}
	if rec := post(t, h, "/v1/replay/logs", "hk_ok", "application/json", []byte("{"), nil); rec.Code != 400 {
		t.Errorf("JSON inválido: %d, quer 400", rec.Code)
	}
	get := func() int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/ready", nil))
		return rec.Code
	}
	if get() != 200 || get() != 503 {
		t.Error("/v1/ready deve seguir o Ready: 200 e depois 503")
	}
}
