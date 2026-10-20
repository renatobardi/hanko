package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/telemetry"
)

type ctxKey int

const (
	keyRequestID ctxKey = iota
	keyPrincipal
)

// principal é quem faz a requisição: humano (modo trusted) ou persona de um run token.
type principal struct {
	Actor  domain.Actor
	Claims *auth.RunClaims
}

func requestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(keyRequestID).(string)
	return id
}

func principalFrom(ctx context.Context) principal {
	if p, ok := ctx.Value(keyPrincipal).(principal); ok {
		return p
	}
	return principal{Actor: domain.HumanActor}
}

// statusWriter captura o status e preserva Flush (SSE) e Unwrap.
type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wrote {
		w.status, w.wrote = code, true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.status, w.wrote = http.StatusOK, true
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		if !w.wrote {
			w.status, w.wrote = http.StatusOK, true
		}
		f.Flush()
	}
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func validRequestID(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if r < 0x21 || r > 0x7e {
			return false
		}
	}
	return true
}

// middleware aplica, de fora para dentro: request-id, log, recover, CORS e autenticação.
func (s *server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := r.Header.Get("X-Request-ID")
		if !validRequestID(id) {
			id = domain.NewID().String()
		}
		ctx := context.WithValue(r.Context(), keyRequestID, id)
		r = r.WithContext(ctx)
		w.Header().Set("X-Request-ID", id)
		if s.d.Version != "" {
			w.Header().Set("X-Hanko-Version", s.d.Version)
		}
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}

		defer func() {
			aborted := false
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler {
					// o handler quer romper a conexão (ex.: export interrompido no meio do arquivo): registra a
					// requisição e re-panica para o net/http fechar o transporte sem terminar a resposta
					aborted = true
				} else {
					telemetry.Record(ctx, s.log, telemetry.FromPanic(telemetry.SourceServer, rec, debug.Stack()), slog.String("request_id", id))
					if !sw.wrote {
						writeProblem(sw, problem(http.StatusInternalServerError, "erro interno do servidor"))
					} else {
						sw.status = http.StatusInternalServerError
					}
				}
			}
			level := slog.LevelInfo
			switch {
			case sw.status >= 500:
				level = slog.LevelError
			case r.URL.Path == "/healthz" || r.URL.Path == "/readyz":
				level = slog.LevelDebug
			}
			route := r.Pattern
			if route == "" {
				route = auth.RedactPath(r.URL.Path)
			}
			s.log.LogAttrs(ctx, level, "http",
				slog.String("method", r.Method), slog.String("route", route), slog.Int("status", sw.status),
				slog.Duration("duration", time.Since(start)), slog.String("request_id", id), slog.Bool("aborted", aborted))
			if aborted {
				panic(http.ErrAbortHandler)
			}
		}()

		if s.cors(sw, r) {
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			p, err := s.authenticate(r)
			if err != nil {
				if errors.Is(err, auth.ErrBadToken) {
					writeProblem(sw, problem(http.StatusUnauthorized, "credencial ausente, inválida, expirada ou de run encerrado"))
				} else {
					s.log.ErrorContext(ctx, "api: autenticação falhou", "err", err, "request_id", id)
					writeProblem(sw, problem(http.StatusServiceUnavailable, "autenticação indisponível"))
				}
				return
			}
			r = r.WithContext(context.WithValue(r.Context(), keyPrincipal, p))
		}
		next.ServeHTTP(sw, r)
	})
}

// authenticate resolve o ator. Com identidade do Guard (modo authenticated) o ator é o
// usuário. Sem header vale o modo trusted (humano local); em authenticated, é 401. Com
// header Authorization, o Bearer precisa ser um run token válido de um run ativo;
// sem Tokens configurado, qualquer Authorization é rejeitado (nunca ignorado).
func (s *server) authenticate(r *http.Request) (principal, error) {
	// Usuário já resolvido pelo auth.Guard (sessão ou API key): ator humano com o ID dele.
	if id, ok := auth.IdentityFrom(r.Context()); ok {
		return principal{Actor: id.User.Actor()}, nil
	}
	h := r.Header.Get("Authorization")
	if h == "" {
		if s.d.Authenticated {
			return principal{}, auth.ErrBadToken
		}
		return principal{Actor: domain.HumanActor}, nil
	}
	if s.d.Tokens == nil || s.svc == nil {
		return principal{}, auth.ErrBadToken
	}
	tok, ok := strings.CutPrefix(h, "Bearer ")
	if !ok {
		tok, ok = strings.CutPrefix(h, "bearer ")
	}
	if !ok || strings.TrimSpace(tok) == "" {
		return principal{}, auth.ErrBadToken
	}
	claims, err := s.d.Tokens.VerifyActive(r.Context(), strings.TrimSpace(tok), func(ctx context.Context, cid, rid domain.ID) (domain.RunStatus, error) {
		run, err := s.svc.GetRun(ctx, cid, rid)
		return run.Status, err
	})
	if err != nil {
		return principal{}, err
	}
	actor, err := claims.Actor()
	if err != nil {
		return principal{}, err
	}
	return principal{Actor: actor, Claims: &claims}, nil
}

// cors devolve true quando a requisição foi encerrada (preflight). Desligado se
// Deps.CORSOrigins estiver vazio; "*" libera qualquer origem.
func (s *server) cors(w http.ResponseWriter, r *http.Request) bool {
	if len(s.d.CORSOrigins) == 0 {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	allowed := ""
	for _, o := range s.d.CORSOrigins {
		if o == "*" || o == origin {
			allowed = o
			if o != "*" {
				allowed = origin
			}
			break
		}
	}
	if allowed == "" {
		return false
	}
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", allowed)
	h.Add("Vary", "Origin")
	h.Set("Access-Control-Expose-Headers", "X-Request-ID")
	if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
		h.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Last-Event-ID, X-Request-ID")
		h.Set("Access-Control-Max-Age", "600")
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	return false
}

// writeProblem escreve um erro fora do huma (middleware, SSE).
func writeProblem(w http.ResponseWriter, p *huma.ErrorModel) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	b, _ := json.Marshal(p)
	_, _ = w.Write(b)
}

// wrapMux injeta o middleware em toda rota registrada (inclusive as do huma).
type wrapMux struct {
	mux  *http.ServeMux
	wrap func(http.Handler) http.Handler
}

func (m wrapMux) HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request)) {
	m.mux.Handle(pattern, m.wrap(http.HandlerFunc(h)))
}

func (m wrapMux) ServeHTTP(w http.ResponseWriter, r *http.Request) { m.mux.ServeHTTP(w, r) }
