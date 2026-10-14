package auth

import (
	"strings"
	"sync"
	"time"
)

// LoginLimiter limita tentativas de login falhas, em memória: no máximo MaxFails por
// IP+e-mail dentro de Window e, como rede de segurança contra rotação de e-mails,
// MaxPerIP por IP. Um login bem-sucedido zera o contador do par.
type LoginLimiter struct {
	MaxFails int           // por IP+e-mail (default 5)
	MaxPerIP int           // por IP (default 5×MaxFails)
	Window   time.Duration // default 15 min
	Now      func() time.Time

	mu    sync.Mutex
	pairs map[string]*bucket
	ips   map[string]*bucket
}

type bucket struct {
	fails int
	reset time.Time
}

const maxLimiterKeys = 20000

func (l *LoginLimiter) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l *LoginLimiter) params() (int, int, time.Duration) {
	m, p, w := l.MaxFails, l.MaxPerIP, l.Window
	if m <= 0 {
		m = 5
	}
	if p <= 0 {
		p = 5 * m
	}
	if w <= 0 {
		w = 15 * time.Minute
	}
	return m, p, w
}

func pairKey(ip, email string) string { return ip + "|" + strings.ToLower(strings.TrimSpace(email)) }

// Allow informa se uma nova tentativa pode ser avaliada; se não, quanto falta para liberar.
func (l *LoginLimiter) Allow(ip, email string) (bool, time.Duration) {
	m, p, _ := l.params()
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	var wait time.Duration
	check := func(b *bucket, max int) {
		if b != nil && now.Before(b.reset) && b.fails >= max {
			wait = max2(wait, b.reset.Sub(now))
		}
	}
	check(l.pairs[pairKey(ip, email)], m)
	check(l.ips[ip], p)
	return wait == 0, wait
}

// Fail registra uma falha.
func (l *LoginLimiter) Fail(ip, email string) {
	_, _, w := l.params()
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pairs == nil {
		l.pairs, l.ips = map[string]*bucket{}, map[string]*bucket{}
	}
	l.purgeLocked(now)
	hit := func(m map[string]*bucket, k string) {
		b := m[k]
		if b == nil || !now.Before(b.reset) {
			b = &bucket{reset: now.Add(w)}
			m[k] = b
		}
		b.fails++
	}
	hit(l.pairs, pairKey(ip, email))
	hit(l.ips, ip)
}

// Success zera o contador do par IP+e-mail.
func (l *LoginLimiter) Success(ip, email string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.pairs, pairKey(ip, email))
}

// purgeLocked descarta buckets vencidos quando o mapa cresce; sob ataque de chaves
// distintas, o mapa nunca passa de maxLimiterKeys (descarta tudo, o que só afrouxa).
func (l *LoginLimiter) purgeLocked(now time.Time) {
	if len(l.pairs)+len(l.ips) < maxLimiterKeys {
		return
	}
	for _, m := range []map[string]*bucket{l.pairs, l.ips} {
		for k, b := range m {
			if !now.Before(b.reset) {
				delete(m, k)
			}
		}
	}
	if len(l.pairs)+len(l.ips) >= maxLimiterKeys {
		l.pairs, l.ips = map[string]*bucket{}, map[string]*bucket{}
	}
}

func max2(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}
