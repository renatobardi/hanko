package ratelimit

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Class é o tipo de tráfego limitado; cada classe tem o próprio teto por minuto (instance_settings).
type Class string

const (
	// ClassWrite: escritas (POST/PUT/PATCH/DELETE) na API e na UI.
	ClassWrite Class = "write"
	// ClassIP: teto por IP que soma todas as escritas, qualquer que seja a identidade.
	ClassIP Class = "ip"
	// ClassMCP: chamadas ao /mcp, ao gateway de tools e ao proxy de memória.
	ClassMCP Class = "mcp"
	// ClassWebhook: webhooks de entrada (/hooks/…, reservado ao M19).
	ClassWebhook Class = "webhook"
	// ClassBrowserError: relatos de erro do navegador (/telemetry/errors).
	ClassBrowserError Class = "browser_error"
)

// Outcome é o resultado de uma decisão, rótulo da métrica.
const (
	OutcomeAllowed = "allowed"
	OutcomeLimited = "limited"
	OutcomeError   = "error"
)

// Store grava o estado dos buckets. Implementado por *service.Service sobre a tabela rate_limit_buckets.
type Store interface {
	// TakeRateToken recarrega, decide e consome de forma atômica; devolve o saldo depois da decisão e se passou.
	TakeRateToken(ctx context.Context, key string, p Params, cost float64, now time.Time) (tokens float64, allowed bool, err error)
	// PurgeRateBuckets apaga os buckets sem uso desde before (equivalem a buckets cheios).
	PurgeRateBuckets(ctx context.Context, before time.Time) (int64, error)
}

// Decision é o resultado de uma tomada.
type Decision struct {
	Allowed    bool
	Limit      int64
	Remaining  int64
	RetryAfter time.Duration
}

// Limiter aplica os buckets. Seguro para uso concorrente.
type Limiter struct {
	Store Store
	Now   func() time.Time
	Log   *slog.Logger

	decisions metric.Int64Counter
}

// New monta o limitador e registra o contador hanko.ratelimit.decisions.
func New(store Store, log *slog.Logger) (*Limiter, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	c, err := otel.Meter("hanko/ratelimit").Int64Counter("hanko.ratelimit.decisions",
		metric.WithDescription("Decisões do rate limit por classe e resultado (allowed, limited, error)"))
	if err != nil {
		return nil, fmt.Errorf("ratelimit: métrica: %w", err)
	}
	return &Limiter{Store: store, Log: log, decisions: c}, nil
}

func (l *Limiter) now() time.Time {
	if l.Now != nil {
		return l.Now().UTC()
	}
	return time.Now().UTC()
}

// Take consome um token do bucket (classe, chave) com teto de perMin requisições por minuto. Erro do banco abre o
// limite (fail-open) e vira outcome=error: o limitador nunca derruba a instalação.
func (l *Limiter) Take(ctx context.Context, class Class, key string, perMin int64) Decision {
	p := PerMinute(perMin)
	d := Decision{Allowed: true, Limit: perMin}
	tokens, ok, err := l.Store.TakeRateToken(ctx, string(class)+"|"+key, p, 1, l.now())
	switch {
	case err != nil:
		l.Log.WarnContext(ctx, "ratelimit: bucket indisponível; requisição liberada", "class", string(class), "err", err)
		l.record(ctx, class, OutcomeError)
		return d
	case ok:
		d.Remaining = int64(tokens)
		l.record(ctx, class, OutcomeAllowed)
	default:
		d.Allowed = false
		d.RetryAfter = RetryAfter(tokens, 1, p)
		l.record(ctx, class, OutcomeLimited)
	}
	return d
}

func (l *Limiter) record(ctx context.Context, class Class, outcome string) {
	if l.decisions != nil {
		l.decisions.Add(ctx, 1, metric.WithAttributes(attribute.String("class", string(class)), attribute.String("outcome", outcome)))
	}
}

// PurgeLoop apaga de hora em hora os buckets parados há mais de uma hora, até ctx terminar.
func (l *Limiter) PurgeLoop(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = time.Hour
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := l.Store.PurgeRateBuckets(ctx, l.now().Add(-time.Hour))
			if err != nil {
				l.Log.WarnContext(ctx, "ratelimit: limpeza de buckets falhou", "err", err)
				continue
			}
			if n > 0 {
				l.Log.DebugContext(ctx, "ratelimit: buckets parados removidos", "count", n)
			}
		}
	}
}
