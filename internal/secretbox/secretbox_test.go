package secretbox

import (
	"errors"
	"strings"
	"testing"
)

const (
	k1 = "0123456789abcdef0123456789abcdef-one"
	k2 = "fedcba9876543210fedcba9876543210-two"
)

func TestSealOpenRoundTrip(t *testing.T) {
	b, err := New(k1, "")
	if err != nil {
		t.Fatal(err)
	}
	s, err := b.Seal([]byte("segredo"), "int-1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s, "segredo") {
		t.Fatal("texto cifrado contém o segredo em claro")
	}
	got, err := b.Open(s, "int-1")
	if err != nil || string(got) != "segredo" {
		t.Fatalf("Open = %q, %v", got, err)
	}
	s2, _ := b.Seal([]byte("segredo"), "int-1")
	if s == s2 {
		t.Fatal("nonce repetido")
	}
}

func TestAADBindsRow(t *testing.T) {
	b, _ := New(k1, "")
	s, _ := b.Seal([]byte("x"), "int-1")
	if _, err := b.Open(s, "int-2"); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("copiar para outra linha deve falhar, got %v", err)
	}
}

func TestTamperAndGarbage(t *testing.T) {
	b, _ := New(k1, "")
	s, _ := b.Seal([]byte("x"), "a")
	bad := s[:len(s)-2] + "AA"
	if _, err := b.Open(bad, "a"); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("adulterado: %v", err)
	}
	for _, in := range []string{"", "v1", "v2:abc:def", "v1:abc:!!!"} {
		if _, err := b.Open(in, "a"); err == nil {
			t.Errorf("Open(%q) deveria falhar", in)
		}
	}
}

func TestNoKey(t *testing.T) {
	b, _ := New("", "")
	if b.Enabled() {
		t.Fatal("sem chave não pode estar habilitado")
	}
	if _, err := b.Seal([]byte("x"), "a"); !errors.Is(err, ErrNoKey) {
		t.Fatalf("Seal sem chave: %v", err)
	}
	var nilBox *Box
	if nilBox.Enabled() {
		t.Fatal("nil Box")
	}
}

func TestRotation(t *testing.T) {
	old, _ := New(k1, "")
	s, _ := old.Seal([]byte("x"), "a")

	nw, _ := New(k2, "")
	if _, err := nw.Open(s, "a"); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("sem a anterior: %v", err)
	}
	both, _ := New(k2, k1)
	got, err := both.Open(s, "a")
	if err != nil || string(got) != "x" {
		t.Fatalf("Open com anterior: %q %v", got, err)
	}
	if !both.NeedsReseal(s) {
		t.Fatal("deveria pedir reseal")
	}
	re, _ := both.Seal(got, "a")
	if both.NeedsReseal(re) {
		t.Fatal("reseal com a chave atual não pede de novo")
	}
	if got, err := nw.Open(re, "a"); err != nil || string(got) != "x" {
		t.Fatalf("depois do reseal só a nova basta: %q %v", got, err)
	}
}
