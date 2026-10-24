package secretbox

import (
	"errors"
	"testing"
)

func TestMACIsDeterministicAndKeyed(t *testing.T) {
	a, err := New("chave-um-com-tamanho-suficiente", "")
	if err != nil {
		t.Fatal(err)
	}
	m1, err := a.MAC([]byte("valor"))
	if err != nil {
		t.Fatal(err)
	}
	m2, _ := a.MAC([]byte("valor"))
	if m1 != m2 {
		t.Fatal("o mesmo valor com a mesma chave tem de dar o mesmo MAC")
	}
	if len(m1) != 64 {
		t.Fatalf("MAC em hex tem %d caracteres, quer 64", len(m1))
	}
	if m3, _ := a.MAC([]byte("outro")); m3 == m1 {
		t.Fatal("valores diferentes deram o mesmo MAC")
	}
	b, _ := New("outra-chave-com-tamanho-suficiente", "")
	if mb, _ := b.MAC([]byte("valor")); mb == m1 {
		t.Fatal("chaves diferentes deram o mesmo MAC: o MAC não depende da chave")
	}
}

func TestMACWithoutKey(t *testing.T) {
	var b *Box
	if _, err := b.MAC([]byte("valor")); !errors.Is(err, ErrNoKey) {
		t.Fatalf("erro = %v, quer ErrNoKey", err)
	}
	empty, _ := New("", "")
	if _, err := empty.MAC([]byte("valor")); !errors.Is(err, ErrNoKey) {
		t.Fatalf("erro = %v, quer ErrNoKey", err)
	}
}
