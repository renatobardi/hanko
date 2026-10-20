package telemetry

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/renatobardi/hanko/internal/auth"
)

// BrowserErrorPath é onde hanko.js envia os erros de JavaScript (ADR-0058 §C).
const BrowserErrorPath = "/telemetry/errors"

const (
	maxBrowserBody  = 8 << 10
	maxMessageRunes = 500
	maxStackRunes   = 4000
	maxFieldRunes   = 300
)

// browserReport é o corpo enviado por hanko.js. Tudo é tratado como não confiável: limitado, sem controles e sem
// query/fragmento de URL (podem carregar token).
type browserReport struct {
	Kind    string `json:"kind"`    // error | unhandledrejection
	Name    string `json:"name"`    // TypeError, Error…
	Message string `json:"message"` // texto da exceção
	Stack   string `json:"stack"`
	Source  string `json:"source"` // arquivo do script
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	URL     string `json:"url"` // página onde ocorreu
}

// BrowserErrors devolve o handler de POST /telemetry/errors: valida, sanitiza e converte o relato em um span
// "browser.error" com o evento exception, log ERROR com a pilha e o contador hanko.errors{source=browser}. Nada é
// persistido. Autenticação e taxa são do middleware de fora (Guard e ratelimit); aqui só mesma origem.
func BrowserErrors(log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	tracer := otel.Tracer("hanko/browser")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !auth.SameOrigin(r) {
			http.Error(w, "cross-origin blocked", http.StatusForbidden)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBrowserBody))
		if err != nil {
			http.Error(w, "body too large or unreadable", http.StatusRequestEntityTooLarge)
			return
		}
		var in browserReport
		if err := json.Unmarshal(body, &in); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		rep := sanitizeBrowserReport(in)
		if rep.Message == "" && rep.Stack == "" {
			http.Error(w, "empty report", http.StatusUnprocessableEntity)
			return
		}
		ctx, span := tracer.Start(r.Context(), "browser.error", trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(
			attribute.String("browser.error.kind", rep.Kind),
			attribute.String("url.path", rep.URL),
			attribute.String("code.filepath", rep.Source),
			attribute.Int("code.lineno", rep.Line),
			attribute.Int("code.column", rep.Col),
		))
		Record(ctx, log, Exception{Source: SourceBrowser, Type: rep.Name, Message: rep.Message, Stacktrace: rep.Stack},
			slog.String("url.path", rep.URL), slog.String("browser.error.kind", rep.Kind))
		span.End()
		w.WriteHeader(http.StatusNoContent)
	})
}

func sanitizeBrowserReport(in browserReport) browserReport {
	out := in
	out.Kind = "error"
	if in.Kind == "unhandledrejection" {
		out.Kind = "unhandledrejection"
	}
	out.Name = MetricType(clean(in.Name, 64, false))
	if out.Name == "other" && strings.TrimSpace(in.Name) == "" {
		out.Name = "Error"
	}
	out.Message = clean(in.Message, maxMessageRunes, false)
	out.Stack = clean(in.Stack, maxStackRunes, true)
	out.Source = pathOnly(clean(in.Source, maxFieldRunes, false))
	out.URL = pathOnly(clean(in.URL, maxFieldRunes, false))
	out.Line, out.Col = clampInt(in.Line), clampInt(in.Col)
	return out
}

func clampInt(n int) int {
	if n < 0 || n > 1_000_000 {
		return 0
	}
	return n
}

// pathOnly devolve só o caminho de uma URL (sem origem, query nem fragmento), com o segredo de caminho mascarado.
func pathOnly(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return auth.RedactPath(u.Path)
}

// clean limita a n runas e remove caracteres de controle (newline e tab só quando keepNL).
func clean(s string, n int, keepNL bool) string {
	var b strings.Builder
	count := 0
	for _, r := range s {
		if count >= n {
			break
		}
		if r == utf8.RuneError {
			continue
		}
		if r < 0x20 || r == 0x7f {
			if !keepNL || (r != '\n' && r != '\t') {
				r = ' '
			}
		}
		b.WriteRune(r)
		count++
	}
	return strings.TrimSpace(b.String())
}
