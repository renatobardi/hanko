// Package ratelimit é o rate limit geral do Hanko (ADR-0058 §B): token bucket guardado no Postgres, por API key,
// usuário, run ou IP, para escritas, webhooks de entrada, MCP e o endpoint de erros do navegador.
//
// Este arquivo é o modelo puro do bucket. O SQL de store.TakeRateToken implementa a mesma conta numa única instrução
// atômica; os testes de propriedade comparam os dois.
package ratelimit

import (
	"math"
	"time"
)

// Params define um bucket: Capacity é a rajada máxima (tokens) e RefillPerSec, a recarga contínua.
type Params struct {
	Capacity     float64
	RefillPerSec float64
}

// PerMinute devolve o bucket de n requisições por minuto, com rajada de 1 minuto (n tokens).
func PerMinute(n int64) Params {
	return Params{Capacity: float64(n), RefillPerSec: float64(n) / 60}
}

// State é o estado de um bucket: tokens disponíveis em At.
type State struct {
	Tokens float64
	At     time.Time
}

// Refill devolve os tokens disponíveis em now: o saldo mais a recarga do intervalo, limitado à capacidade. Relógio
// que anda para trás não tira nem dá token (intervalo negativo vale zero).
func Refill(s State, p Params, now time.Time) float64 {
	elapsed := now.Sub(s.At).Seconds()
	if elapsed < 0 {
		elapsed = 0
	}
	return math.Min(p.Capacity, s.Tokens+elapsed*p.RefillPerSec)
}

// Step avalia uma tomada de cost tokens em now. Bucket novo (fresh) começa cheio. Negar não consome nada. O novo
// estado nunca regride no tempo.
func Step(s State, fresh bool, p Params, cost float64, now time.Time) (State, bool) {
	if fresh {
		s = State{Tokens: p.Capacity, At: now}
	}
	avail := Refill(s, p, now)
	at := now
	if s.At.After(now) {
		at = s.At
	}
	if avail >= cost {
		return State{Tokens: avail - cost, At: at}, true
	}
	return State{Tokens: avail, At: at}, false
}

// RetryAfter é quanto falta para haver cost tokens, dado o saldo atual. Zero se já há.
func RetryAfter(tokens, cost float64, p Params) time.Duration {
	if tokens >= cost {
		return 0
	}
	if p.RefillPerSec <= 0 {
		return time.Hour
	}
	return time.Duration(math.Ceil((cost-tokens)/p.RefillPerSec*float64(time.Second)/float64(time.Millisecond))) * time.Millisecond
}

// RetryAfterSeconds arredonda para cima, em segundos inteiros (mínimo 1), para o cabeçalho Retry-After.
func RetryAfterSeconds(d time.Duration) int {
	s := int(math.Ceil(d.Seconds()))
	if s < 1 {
		return 1
	}
	return s
}
