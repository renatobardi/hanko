package webpush_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/webpush"
	"github.com/renatobardi/hanko/internal/webpush/webpushtest"
)

func TestEncryptRoundTrip(t *testing.T) {
	d := webpushtest.NewDevice("https://push.example/abc")
	for _, payload := range [][]byte{[]byte("oi"), []byte(strings.Repeat("ã", 1000)), bytes.Repeat([]byte{0, 1, 2}, 100), {}} {
		body, err := webpush.Encrypt(d.Sub, payload)
		if err != nil {
			t.Fatal(err)
		}
		got, err := d.Decrypt(body)
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("round trip de %d bytes: %q %v", len(payload), got, err)
		}
	}
	// cada envio usa chave efêmera e sal novos
	a, _ := webpush.Encrypt(d.Sub, []byte("x"))
	b, _ := webpush.Encrypt(d.Sub, []byte("x"))
	if bytes.Equal(a, b) {
		t.Fatal("dois envios idênticos: sal/chave efêmera não variam")
	}
	// outro aparelho não decifra; adulterar o corpo quebra a autenticação
	other := webpushtest.NewDevice("https://push.example/other")
	if _, err := other.Decrypt(a); err == nil {
		t.Fatal("outro aparelho não deveria decifrar")
	}
	a[len(a)-1] ^= 1
	if _, err := d.Decrypt(a); err == nil {
		t.Fatal("corpo adulterado deveria falhar")
	}
}

func TestEncryptValidation(t *testing.T) {
	d := webpushtest.NewDevice("https://push.example/abc")
	if _, err := webpush.Encrypt(d.Sub, make([]byte, webpush.MaxPayload+1)); !errors.Is(err, webpush.ErrPayloadTooLarge) {
		t.Fatalf("grande demais: %v", err)
	}
	if _, err := webpush.Encrypt(d.Sub, make([]byte, webpush.MaxPayload)); err != nil {
		t.Fatalf("no limite cabe: %v", err)
	}
	for name, sub := range map[string]webpush.Subscription{
		"p256dh lixo":  {Endpoint: "https://x", P256dh: "!!!", Auth: d.Sub.Auth},
		"p256dh curto": {Endpoint: "https://x", P256dh: "AAAA", Auth: d.Sub.Auth},
		"auth vazio":   {Endpoint: "https://x", P256dh: d.Sub.P256dh, Auth: ""},
	} {
		if _, err := webpush.Encrypt(sub, []byte("x")); !errors.Is(err, webpush.ErrBadSubscription) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestVAPIDKeyAndAuthorization(t *testing.T) {
	v, err := webpush.GenerateVAPID()
	if err != nil || !v.Valid() {
		t.Fatal(err)
	}
	raw, err := v.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	back, err := webpush.ParseVAPID(raw)
	if err != nil || back.PublicKey() != v.PublicKey() {
		t.Fatalf("a chave sobrevive à serialização: %v", err)
	}
	now := time.Now()
	h, err := back.Authorization("https://fcm.example/send/abc", "mailto:ops@example.org", now)
	if err != nil {
		t.Fatal(err)
	}
	k, err := webpushtest.VerifyVAPID(h, "https://fcm.example", now)
	if err != nil || k != v.PublicKey() {
		t.Fatalf("verificação: %q %v", k, err)
	}
	if _, err := webpushtest.VerifyVAPID(h, "https://outro.example", now); err == nil {
		t.Fatal("audience errada deveria falhar")
	}
	if _, err := webpushtest.VerifyVAPID(h, "https://fcm.example", now.Add(13*time.Hour)); err == nil {
		t.Fatal("token vencido deveria falhar")
	}
	for _, bad := range [][]byte{nil, []byte("!!"), []byte("AAAA")} {
		if _, err := webpush.ParseVAPID(bad); err == nil {
			t.Errorf("ParseVAPID(%q) deveria falhar", bad)
		}
	}
	var zero webpush.VAPID
	if zero.Valid() || zero.PublicKey() != "" {
		t.Error("zero value sem chave")
	}
	if _, err := zero.Marshal(); err == nil {
		t.Error("zero value não serializa")
	}
	if _, err := zero.Authorization("https://x/y", "mailto:a@b.c", now); err == nil {
		t.Error("zero value não assina")
	}
	if _, err := v.Authorization("::nope", "mailto:a@b.c", now); !errors.Is(err, webpush.ErrBadSubscription) {
		t.Errorf("endpoint inválido: %v", err)
	}
}

func TestSenderAgainstFakeService(t *testing.T) {
	svc := webpushtest.NewService(t)
	dev := svc.Device("/push/ok")
	v, _ := webpush.GenerateVAPID()
	s := &webpush.Sender{HTTP: svc.Client(), VAPID: v, Subject: "mailto:ops@example.org", TTL: time.Hour}
	ctx := context.Background()

	if err := s.Send(ctx, dev.Sub, []byte(`{"title":"Olá"}`)); err != nil {
		t.Fatal(err)
	}
	msgs := svc.Messages()
	if len(msgs) != 1 || msgs[0].Err != "" || string(msgs[0].Payload) != `{"title":"Olá"}` || msgs[0].VAPIDKey != v.PublicKey() || msgs[0].TTL != "3600" {
		t.Fatalf("mensagens: %+v", msgs)
	}

	svc.SetStatus("/push/ok", http.StatusGone)
	if err := s.Send(ctx, dev.Sub, []byte("x")); !errors.Is(err, webpush.ErrGone) {
		t.Fatalf("410 = ErrGone, veio %v", err)
	}
	svc.SetStatus("/push/ok", http.StatusNotFound)
	if err := s.Send(ctx, dev.Sub, []byte("x")); !errors.Is(err, webpush.ErrGone) {
		t.Fatalf("404 = ErrGone, veio %v", err)
	}
	svc.SetStatus("/push/ok", http.StatusInternalServerError)
	err := s.Send(ctx, dev.Sub, []byte("x"))
	if err == nil || errors.Is(err, webpush.ErrGone) || !strings.Contains(err.Error(), "500") {
		t.Fatalf("500 é falha comum: %v", err)
	}
	if err := (&webpush.Sender{VAPID: v}).Send(ctx, dev.Sub, nil); err == nil {
		t.Fatal("sem cliente HTTP deveria falhar")
	}
	if err := (&webpush.Sender{HTTP: svc.Client()}).Send(ctx, dev.Sub, []byte("x")); err == nil {
		t.Fatal("sem chave VAPID deveria falhar")
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.Send(cctx, dev.Sub, []byte("x")); err == nil {
		t.Fatal("contexto cancelado deveria falhar")
	}
}
