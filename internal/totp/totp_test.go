package totp

import (
	"errors"
	"testing"
	"time"
)

// semente da RFC 6238 (SHA-1): ASCII "12345678901234567890" em base32.
const rfcSeed = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

func TestRFC6238SHA1Vectors(t *testing.T) {
	// vetores da RFC 6238, apêndice B, com 8 dígitos e passo de 30 s
	s, err := Parse("otpauth://totp/hanko:teste?secret=" + rfcSeed + "&digits=8")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	cases := []struct {
		unix int64
		want string
	}{
		{59, "94287082"},
		{1111111109, "07081804"},
		{1111111111, "14050471"},
		{1234567890, "89005924"},
		{2000000000, "69279037"},
		{20000000000, "65353130"},
	}
	for _, c := range cases {
		got, _ := s.At(time.Unix(c.unix, 0))
		if got != c.want {
			t.Fatalf("T=%d: código %s, quero %s", c.unix, got, c.want)
		}
	}
}

func TestSeedBase32DefaultsAndRemaining(t *testing.T) {
	// semente como os apps a mostram (minúsculas, espaços) e padrões de 6 dígitos e 30 s
	s, err := Parse("gezd gnbv gy3t qojq gezd gnbv gy3t qojq")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	code, remaining := s.At(time.Unix(59, 0))
	if code != "287082" {
		t.Fatalf("código de 6 dígitos em T=59 = %s, quero 287082", code)
	}
	if remaining != 1 { // 59 s estão no passo 30..59; faltam 1 s para o 60
		t.Fatalf("segundos restantes = %d, quero 1", remaining)
	}
	if _, r := s.At(time.Unix(60, 0)); r != 30 {
		t.Fatalf("no começo de um passo faltam %d s, quero 30", r)
	}
}

func TestParseURIParameters(t *testing.T) {
	s, err := Parse("otpauth://totp/x?secret=" + rfcSeed + "&algorithm=sha256&digits=8&period=60")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if s.digits != 8 || s.period != 60 || s.algorithm != "SHA256" {
		t.Fatalf("parâmetros lidos = %d/%d/%s", s.digits, s.period, s.algorithm)
	}
}

func TestParseRecusa(t *testing.T) {
	cases := map[string]string{
		"vazia":           "",
		"não é base32":    "!!!!",
		"esquema errado":  "https://totp/x?secret=" + rfcSeed,
		"hotp":            "otpauth://hotp/x?secret=" + rfcSeed,
		"digits fora":     "otpauth://totp/x?secret=" + rfcSeed + "&digits=5",
		"period zero":     "otpauth://totp/x?secret=" + rfcSeed + "&period=0",
		"algoritmo ruim":  "otpauth://totp/x?secret=" + rfcSeed + "&algorithm=MD5",
		"secret ausente":  "otpauth://totp/x?digits=6",
		"secret sem base": "otpauth://totp/x?secret=%%%",
	}
	for name, v := range cases {
		if _, err := Parse(v); !errors.Is(err, ErrSeed) {
			t.Errorf("%s: erro = %v, quero ErrSeed", name, err)
		}
	}
}
