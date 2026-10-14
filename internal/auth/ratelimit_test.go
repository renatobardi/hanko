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
