package ratelimit

import (
	"math"
	"math/rand"
	"testing"
	"testing/quick"
	"time"
)

// Testes de propriedade do token bucket (#51, #56): entradas aleatórias com semente fixa, para a falha ser
// reproduzível. O mesmo modelo é o oráculo do teste contra o SQL (internal/service/ratelimit_test.go).

const propertySeed = 20261017

func quickCfg() *quick.Config {
	return &quick.Config{MaxCount: 3000, Rand: rand.New(rand.NewSource(propertySeed))}
}

var t0 = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

// params converte bytes aleatórios em um bucket realista: capacidade 1–1000, recarga 0,01–50/s.
func params(c uint16, r uint16) Params {
	return Params{Capacity: float64(c%1000) + 1, RefillPerSec: float64(r%5000+1) / 100}
}

func TestStepNeverExceedsCapacityNorGoesNegative(t *testing.T) {
	f := func(c, r uint16, gaps []uint16) bool {
		p := params(c, r)
		s, fresh, now := State{}, true, t0
		for _, g := range gaps {
			now = now.Add(time.Duration(g) * time.Millisecond)
			var ok bool
			s, ok = Step(s, fresh, p, 1, now)
			_ = ok
			fresh = false
			if s.Tokens < 0 || s.Tokens > p.Capacity+1e-9 {
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, quickCfg()); err != nil {
		t.Fatal(err)
	}
}

// Negar não consome nada: o saldo depois da negação é o saldo recarregado.
func TestDenialDoesNotConsume(t *testing.T) {
	f := func(c, r uint16, gap uint16) bool {
		p := params(c, r)
		s := State{Tokens: 0.3, At: t0}
		now := t0.Add(time.Duration(gap%50) * time.Millisecond)
		before := Refill(s, p, now)
		after, ok := Step(s, false, p, 1, now)
		if ok {
			return after.Tokens == before-1 || math.Abs(after.Tokens-(before-1)) < 1e-9
		}
		return math.Abs(after.Tokens-before) < 1e-9
	}
	if err := quick.Check(f, quickCfg()); err != nil {
		t.Fatal(err)
	}
}

// Esperar nunca piora: com mais tempo decorrido há pelo menos o mesmo saldo.
func TestRefillMonotonicInTime(t *testing.T) {
	f := func(c, r uint16, tokens uint16, g1, g2 uint16) bool {
		p := params(c, r)
		s := State{Tokens: math.Min(float64(tokens%1000), p.Capacity), At: t0}
		a := t0.Add(time.Duration(g1) * time.Millisecond)
		b := a.Add(time.Duration(g2) * time.Millisecond)
		return Refill(s, p, b)+1e-9 >= Refill(s, p, a)
	}
	if err := quick.Check(f, quickCfg()); err != nil {
		t.Fatal(err)
	}
}

// Numa janela W, o total admitido nunca passa de capacidade + taxa·W (o contrato do bucket), seja qual for o padrão
// de chegada.
func TestAdmittedWithinWindowBounded(t *testing.T) {
	f := func(c, r uint16, gaps []uint8) bool {
		p := params(c, r)
		s, fresh, now := State{}, true, t0
		admitted := 0
		for _, g := range gaps {
			now = now.Add(time.Duration(g) * time.Millisecond)
			var ok bool
			s, ok = Step(s, fresh, p, 1, now)
			fresh = false
			if ok {
				admitted++
			}
		}
		window := now.Sub(t0).Seconds()
		return float64(admitted) <= p.Capacity+p.RefillPerSec*window+1e-6
	}
	if err := quick.Check(f, quickCfg()); err != nil {
		t.Fatal(err)
	}
}

// Rajada inicial: um bucket novo admite exatamente Capacity requisições instantâneas e nega a seguinte.
func TestFreshBucketAdmitsExactlyCapacityThenDenies(t *testing.T) {
	for _, n := range []int64{1, 10, 600} {
		p := PerMinute(n)
		s, fresh := State{}, true
		for i := int64(0); i < n; i++ {
			var ok bool
			s, ok = Step(s, fresh, p, 1, t0)
			fresh = false
			if !ok {
				t.Fatalf("n=%d: requisição %d negada dentro da rajada", n, i+1)
			}
		}
		if _, ok := Step(s, false, p, 1, t0); ok {
			t.Fatalf("n=%d: a requisição %d deveria ser negada", n, n+1)
		}
	}
}

func TestRetryAfterMatchesRefill(t *testing.T) {
	f := func(c, r uint16, tok uint8) bool {
		p := params(c, r)
		tokens := float64(tok%100) / 100 // < 1
		d := RetryAfter(tokens, 1, p)
		if d <= 0 {
			return false
		}
		// esperado o Retry-After, a requisição passa
		s := State{Tokens: tokens, At: t0}
		_, ok := Step(s, false, p, 1, t0.Add(d))
		return ok
	}
	if err := quick.Check(f, quickCfg()); err != nil {
		t.Fatal(err)
	}
	if RetryAfter(2, 1, PerMinute(60)) != 0 {
		t.Error("com saldo suficiente o Retry-After é zero")
	}
}

func TestRetryAfterSeconds(t *testing.T) {
	cases := map[time.Duration]int{0: 1, time.Millisecond: 1, time.Second: 1, 1001 * time.Millisecond: 2, 90 * time.Second: 90}
	for in, want := range cases {
		if got := RetryAfterSeconds(in); got != want {
			t.Errorf("RetryAfterSeconds(%v) = %d, quer %d", in, got, want)
		}
	}
}

func TestClockGoingBackwardsNeverGrantsTokens(t *testing.T) {
	p := PerMinute(60)
	s := State{Tokens: 0.5, At: t0}
	if got := Refill(s, p, t0.Add(-time.Hour)); got != 0.5 {
		t.Errorf("relógio para trás deveria manter o saldo, veio %v", got)
	}
	next, _ := Step(s, false, p, 1, t0.Add(-time.Hour))
	if next.At.Before(s.At) {
		t.Error("o estado não pode regredir no tempo")
	}
}
