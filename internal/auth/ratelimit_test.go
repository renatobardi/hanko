package auth

import (
	"testing"
	"time"
)

func TestLoginLimiter(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	l := &LoginLimiter{Now: func() time.Time { return now }}

	for i := 0; i < 5; i++ {
		if ok, _ := l.Allow("1.1.1.1", "A@x.com"); !ok {
			t.Fatalf("tentativa %d deveria passar", i+1)
		}
		l.Fail("1.1.1.1", "a@x.com") // e-mail sem diferenciar caixa
	}
	ok, wait := l.Allow("1.1.1.1", "a@x.com")
	if ok || wait <= 0 || wait > 15*time.Minute {
		t.Fatalf("6ª tentativa deveria bloquear: ok=%v wait=%v", ok, wait)
	}
	if ok, _ := l.Allow("1.1.1.1", "outro@x.com"); !ok {
		t.Fatal("outro e-mail do mesmo IP ainda deveria passar")
	}
	if ok, _ := l.Allow("2.2.2.2", "a@x.com"); !ok {
		t.Fatal("o mesmo e-mail de outro IP ainda deveria passar")
	}

	now = now.Add(15*time.Minute + time.Second)
	if ok, _ := l.Allow("1.1.1.1", "a@x.com"); !ok {
		t.Fatal("após a janela deveria liberar")
	}
}

func TestLoginLimiterSuccessResets(t *testing.T) {
	l := &LoginLimiter{}
	for i := 0; i < 4; i++ {
		l.Fail("ip", "a@x.com")
	}
	l.Success("ip", "a@x.com")
	for i := 0; i < 4; i++ {
		l.Fail("ip", "a@x.com")
	}
	if ok, _ := l.Allow("ip", "a@x.com"); !ok {
		t.Fatal("sucesso deveria ter zerado o contador do par")
	}
}

func TestLoginLimiterPerIPCap(t *testing.T) {
	l := &LoginLimiter{MaxFails: 2} // teto por IP = 10
	for i := 0; i < 10; i++ {
		l.Fail("9.9.9.9", string(rune('a'+i))+"@x.com")
	}
	if ok, _ := l.Allow("9.9.9.9", "novo@x.com"); ok {
		t.Fatal("rotacionar e-mails não pode escapar do teto por IP")
	}
}

func TestLoginLimiterPerEmailDistributed(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	l := &LoginLimiter{Now: func() time.Time { return now }}
	// 20 IPs diferentes, uma falha cada: nenhum par nem IP chega ao próprio limite
	for i := 0; i < 20; i++ {
		ip := "10.1.0." + string(rune('a'+i))
		l.Fail(ip, "Alvo@x.com")
	}
	now = now.Add(10 * time.Minute) // passa o backoff (≤ 5 min), segue na janela de 15 min
	ok, wait := l.Allow("10.9.9.9", "alvo@x.com")
	if ok || wait <= 0 || wait > 15*time.Minute {
		t.Fatalf("20 falhas do e-mail (de IPs distintos) deveriam bloquear qualquer IP: ok=%v wait=%v", ok, wait)
	}
	if ok, _ := l.Allow("10.9.9.9", "outro@x.com"); !ok {
		t.Fatal("outro e-mail não deve ser afetado")
	}
	if ok, _ := l.Allow("10.9.9.9", ""); !ok {
		t.Fatal("e-mail vazio (fluxo Firebase) não tem bucket por e-mail")
	}
	now = now.Add(6 * time.Minute) // total 16 min depois da 1ª falha
	if ok, _ := l.Allow("10.9.9.9", "alvo@x.com"); !ok {
		t.Fatal("após a janela deveria liberar")
	}
}

func TestLoginLimiterEmailBackoff(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	l := &LoginLimiter{Now: func() time.Time { return now }}
	// até 5 falhas do e-mail: sem espera
	for i := 0; i < 5; i++ {
		l.Fail("1.1.1."+string(rune('a'+i)), "a@x.com")
		if ok, _ := l.Allow("2.2.2.2", "a@x.com"); !ok {
			t.Fatalf("falha %d ainda não deveria impor espera", i+1)
		}
	}
	var prev time.Duration
	for i := 0; i < 4; i++ { // 6ª, 7ª, 8ª, 9ª: 1 s, 2 s, 4 s, 8 s
		l.Fail("3.3.3."+string(rune('a'+i)), "a@x.com")
		ok, wait := l.Allow("2.2.2.2", "a@x.com")
		want := time.Second << i
		if ok || wait != want {
			t.Fatalf("falha %d: ok=%v wait=%v, quer %v", 6+i, ok, wait, want)
		}
		if wait <= prev {
			t.Fatalf("o backoff deveria crescer: %v depois de %v", wait, prev)
		}
		prev = wait
		now = now.Add(wait) // espera passar e tenta de novo
		if ok, _ := l.Allow("2.2.2.2", "a@x.com"); !ok {
			t.Fatalf("após %v deveria liberar", wait)
		}
	}
}

func TestLoginLimiterSuccessResetsEmail(t *testing.T) {
	l := &LoginLimiter{}
	for i := 0; i < 20; i++ {
		l.Fail("ip"+string(rune('a'+i)), "a@x.com")
	}
	l.Success("ipz", "a@x.com")
	if ok, _ := l.Allow("novo", "a@x.com"); !ok {
		t.Fatal("login bem-sucedido deveria zerar o bucket do e-mail")
	}
}
