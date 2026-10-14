package auth

import (
	"strings"
	"testing"
)

// fast mantém os testes rápidos; a produção usa DefaultPasswordParams.
var fast = PasswordParams{Memory: 64, Time: 1, Threads: 1, KeyLen: 32, SaltLen: 16}

func TestHashVerify(t *testing.T) {
	h, err := fast.Hash("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=64,t=1,p=1$") {
		t.Fatalf("formato PHC inesperado: %s", h)
	}
	if ok, err := VerifyPassword("correct horse battery", h); err != nil || !ok {
		t.Fatalf("senha certa: ok=%v err=%v", ok, err)
	}
	if ok, err := VerifyPassword("outra senha qualquer", h); err != nil || ok {
		t.Fatalf("senha errada: ok=%v err=%v", ok, err)
	}
	h2, _ := fast.Hash("correct horse battery")
	if h == h2 {
		t.Fatal("salt deveria variar entre hashes")
	}
}

func TestDefaultParamsAreStrong(t *testing.T) {
	p := DefaultPasswordParams
	if p.Memory < 19*1024 || p.Time < 2 || p.KeyLen < 32 || p.SaltLen < 16 {
		t.Fatalf("parâmetros abaixo do mínimo recomendado: %+v", p)
	}
	h, err := HashPassword("uma senha longa o bastante")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h, "m=65536,t=3,p=2") {
		t.Fatalf("hash de produção sem os parâmetros fortes: %s", h)
	}
	if ok, err := VerifyPassword("uma senha longa o bastante", h); err != nil || !ok {
		t.Fatal("verificação do hash de produção falhou")
	}
}

func TestVerifyMalformed(t *testing.T) {
	good, _ := fast.Hash("senha-de-teste-1")
	for name, enc := range map[string]string{
		"vazio":           "",
		"bcrypt":          "$2a$10$abcdefghijklmnopqrstuuabcdefghijklmnopqrstuvwxyz0123",
		"campos":          "$argon2id$v=19$m=64,t=1,p=1$abc",
		"versão":          strings.Replace(good, "v=19", "v=16", 1),
		"memória gigante": strings.Replace(good, "m=64", "m=99999999", 1),
		"tempo gigante":   strings.Replace(good, "t=1", "t=9999", 1),
		"threads zero":    strings.Replace(good, "p=1", "p=0", 1),
		"salt inválido":   strings.Replace(good, "$"+strings.Split(good, "$")[4]+"$", "$!!!$", 1),
	} {
		if ok, err := VerifyPassword("senha-de-teste-1", enc); ok || err == nil {
			t.Errorf("%s: esperava erro, ok=%v err=%v", name, ok, err)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	for pw, ok := range map[string]bool{
		"curta": false, "123456789": false, "1234567890": true,
		strings.Repeat("a", MaxPasswordLen): true, strings.Repeat("a", MaxPasswordLen+1): false,
		"çççççççççç": true, // 10 runas, 20 bytes
	} {
		if err := ValidatePassword(pw); (err == nil) != ok {
			t.Errorf("%q: err=%v, esperado ok=%v", pw, err, ok)
		}
	}
}
