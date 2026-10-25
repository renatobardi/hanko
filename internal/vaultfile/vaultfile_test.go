package vaultfile

import (
	"bytes"
	"errors"
	"testing"
)

const pass = "uma senha longa 1"

func TestSealOpenIdaEVolta(t *testing.T) {
	plain := []byte("SENTINELA-vaultfile-1234")
	f, err := Seal(plain, pass)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(f, plain) {
		t.Fatal("o arquivo guarda o conteúdo em claro")
	}
	got, err := Open(f, pass)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("Open = %q, %v", got, err)
	}
	f2, _ := Seal(plain, pass)
	if bytes.Equal(f, f2) {
		t.Fatal("dois arquivos iguais: sal e nonce têm de mudar a cada gravação")
	}
}

func TestOpenSenhaErradaEAdulteracao(t *testing.T) {
	f, err := Seal([]byte("x"), pass)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(f, "outra senha longa 1"); !errors.Is(err, ErrPassword) {
		t.Fatalf("senha errada: %v", err)
	}
	// mexer no último byte (tag) e no cabeçalho (iterações) falha igual
	bad := bytes.Clone(f)
	bad[len(bad)-1] ^= 1
	if _, err := Open(bad, pass); !errors.Is(err, ErrPassword) {
		t.Fatalf("conteúdo alterado: %v", err)
	}
	bad = bytes.Replace(f, []byte(`"iterations":600000`), []byte(`"iterations":600001`), 1)
	if _, err := Open(bad, pass); !errors.Is(err, ErrPassword) {
		t.Fatalf("cabeçalho alterado: %v", err)
	}
}

func TestOpenFormatoInvalido(t *testing.T) {
	for name, in := range map[string][]byte{
		"vazio":      nil,
		"sem magic":  []byte("outra coisa\n{}\n"),
		"sem header": []byte(Magic),
		"json ruim":  []byte(Magic + "{x\nabc"),
		"versão":     []byte(Magic + `{"v":2,"kdf":"pbkdf2-sha256","iterations":600000,"salt":"AAAAAAAAAAAAAAAAAAAAAA==","cipher":"aes-256-gcm","nonce":"AAAAAAAAAAAAAAAA"}` + "\nabc"),
		"iterações":  []byte(Magic + `{"v":1,"kdf":"pbkdf2-sha256","iterations":1,"salt":"AAAAAAAAAAAAAAAAAAAAAA==","cipher":"aes-256-gcm","nonce":"AAAAAAAAAAAAAAAA"}` + "\nabc"),
	} {
		if _, err := Open(in, pass); !errors.Is(err, ErrFormat) {
			t.Errorf("%s: %v, quer ErrFormat", name, err)
		}
	}
}

func TestSealSenhaCurta(t *testing.T) {
	if _, err := Seal([]byte("x"), "curta"); err == nil {
		t.Fatal("senha curta devia ser recusada")
	}
}
