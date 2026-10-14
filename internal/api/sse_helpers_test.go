package api

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// flushRecorder é um ResponseWriter com Flush cujo corpo pode ser lido em fluxo.
type flushRecorder struct {
	mu   sync.Mutex
	hdr  http.Header
	buf  bytes.Buffer
	code int
}

func newFlushRecorder() *flushRecorder { return &flushRecorder{hdr: http.Header{}} }

func (r *flushRecorder) Header() http.Header { return r.hdr }
func (r *flushRecorder) WriteHeader(c int)   { r.code = c }
func (r *flushRecorder) Flush()              {}
func (r *flushRecorder) Write(b []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.Write(b)
}

// reader devolve um io.Reader que bloqueia (com polling) até haver bytes novos.
func (r *flushRecorder) reader() io.Reader { return &recReader{r: r} }

type recReader struct{ r *flushRecorder }

func (rr *recReader) Read(p []byte) (int, error) {
	for range 600 {
		rr.r.mu.Lock()
		n, _ := rr.r.buf.Read(p)
		rr.r.mu.Unlock()
		if n > 0 {
			return n, nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return 0, io.EOF
}
