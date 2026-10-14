package auth

import (
	"strings"
	"sync"
	"time"
)

// LoginLimiter limita tentativas de login falhas, em memória: no máximo MaxFails por
// IP+e-mail dentro de Window; como rede de segurança contra rotação de e-mails, MaxPerIP por IP;
// e, contra força bruta distribuída (muitos IPs, um e-mail), MaxPerEmail por e-mail somando todos
// os IPs, com backoff progressivo: a partir da 6ª falha do e-mail cada nova falha dobra a espera
// (1 s, 2 s, 4 s… até 5 min). Um login bem-sucedido zera os contadores do par e do e-mail.
type LoginLimiter struct {
	MaxFails    int           // por IP+e-mail (default 5)
	MaxPerIP    int           // por IP (default 5×MaxFails)
	MaxPerEmail int           // por e-mail, de qualquer IP (default 20)
	Window      time.Duration // default 15 min
	Now         func() time.Time

	mu     sync.Mutex
	pairs  map[string]*bucket
	ips    map[string]*bucket
	emails map[string]*bucket
}

type bucket struct {
	fails int
	reset time.Time
	until time.Time // backoff progressivo (só no bucket por e-mail)
}

const (
	emailBackoffAfter = 5 // falhas do e-mail antes de o backoff começar
	emailBackoffBase  = time.Second
	emailBackoffMax   = 5 * time.Minute
)

const maxLimiterKeys = 20000

func (l *LoginLimiter) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l *LoginLimiter) params() (m, p, e int, w time.Duration) {
	m, p, e, w = l.MaxFails, l.MaxPerIP, l.MaxPerEmail, l.Window
	if m <= 0 {
		m = 5
	}
	if p <= 0 {
		p = 5 * m
	}
	if e <= 0 {
		e = 20
	}
	if w <= 0 {
		w = 15 * time.Minute
	}
	return m, p, e, w
}

func normEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

func pairKey(ip, email string) string { return ip + "|" + normEmail(email) }

// Allow informa se uma nova tentativa pode ser avaliada; se não, quanto falta para liberar.
// E-mail vazio não entra no bucket por e-mail (o fluxo do Firebase não tem e-mail antes do token).
func (l *LoginLimiter) Allow(ip, email string) (bool, time.Duration) {
	m, p, e, _ := l.params()
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	var wait time.Duration
	check := func(b *bucket, max int) {
		if b == nil || !now.Before(b.reset) {
			return
		}
		if b.fails >= max {
			wait = max2(wait, b.reset.Sub(now))
		}
		if now.Before(b.until) {
			wait = max2(wait, b.until.Sub(now))
		}
	}
	check(l.pairs[pairKey(ip, email)], m)
	check(l.ips[ip], p)
	if em := normEmail(email); em != "" {
		check(l.emails[em], e)
	}
	return wait == 0, wait
}

// Fail registra uma falha.
func (l *LoginLimiter) Fail(ip, email string) {
	_, _, _, w := l.params()
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pairs == nil {
		l.pairs, l.ips, l.emails = map[string]*bucket{}, map[string]*bucket{}, map[string]*bucket{}
	}
	l.purgeLocked(now)
	hit := func(m map[string]*bucket, k string) *bucket {
		b := m[k]
		if b == nil || !now.Before(b.reset) {
			b = &bucket{reset: now.Add(w)}
			m[k] = b
		}
		b.fails++
		return b
	}
	hit(l.pairs, pairKey(ip, email))
	hit(l.ips, ip)
	if em := normEmail(email); em != "" {
		b := hit(l.emails, em)
		if b.fails > emailBackoffAfter {
			delay := emailBackoffMax
			if shift := b.fails - emailBackoffAfter - 1; shift < 20 {
				delay = min(emailBackoffBase<<shift, emailBackoffMax)
			}
			b.until = now.Add(delay)
		}
	}
}

// Success zera os contadores do par IP+e-mail e do e-mail.
func (l *LoginLimiter) Success(ip, email string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.pairs, pairKey(ip, email))
	if em := normEmail(email); em != "" {
		delete(l.emails, em)
	}
}

// purgeLocked descarta buckets vencidos quando o mapa cresce; sob ataque de chaves
// distintas, o mapa nunca passa de maxLimiterKeys (descarta tudo, o que só afrouxa).
func (l *LoginLimiter) purgeLocked(now time.Time) {
	if len(l.pairs)+len(l.ips)+len(l.emails) < maxLimiterKeys {
		return
	}
	for _, m := range []map[string]*bucket{l.pairs, l.ips, l.emails} {
		for k, b := range m {
			if !now.Before(b.reset) {
				delete(m, k)
			}
		}
	}
	if len(l.pairs)+len(l.ips)+len(l.emails) >= maxLimiterKeys {
		l.pairs, l.ips, l.emails = map[string]*bucket{}, map[string]*bucket{}, map[string]*bucket{}
	}
}

func max2(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}
