package telemetry

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

// fanout envia cada registro a vários handlers, filtrando por nível.
type fanout struct {
	level    slog.Level
	handlers []slog.Handler
}

func newFanout(level slog.Level, hs ...slog.Handler) *fanout {
	return &fanout{level: level, handlers: hs}
}

func (f *fanout) Enabled(_ context.Context, l slog.Level) bool { return l >= f.level }

func (f *fanout) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, h := range f.handlers {
		if h.Enabled(ctx, r.Level) {
			if err := h.Handle(ctx, r.Clone()); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (f *fanout) WithAttrs(attrs []slog.Attr) slog.Handler {
	hs := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		hs[i] = h.WithAttrs(attrs)
	}
	return &fanout{level: f.level, handlers: hs}
}

func (f *fanout) WithGroup(name string) slog.Handler {
	if name == "" {
		return f
	}
	hs := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		hs[i] = h.WithGroup(name)
	}
	return &fanout{level: f.level, handlers: hs}
}

// localHandler escreve JSON/text e acrescenta trace_id/span_id quando há span no ctx.
type localHandler struct{ slog.Handler }

func newLocalHandler(w io.Writer, format string, level slog.Level) slog.Handler {
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler
	if strings.EqualFold(format, "text") {
		h = slog.NewTextHandler(w, opts)
	} else {
		h = slog.NewJSONHandler(w, opts)
	}
	return localHandler{h}
}

func (l localHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))
	}
	return l.Handler.Handle(ctx, r)
}

func (l localHandler) WithAttrs(a []slog.Attr) slog.Handler {
	return localHandler{l.Handler.WithAttrs(a)}
}
func (l localHandler) WithGroup(n string) slog.Handler { return localHandler{l.Handler.WithGroup(n)} }
