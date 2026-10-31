package service

import (
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
)

// checkFileContent é a regra do envio de arquivo de campo (criação e troca, #117): sem DB.
func TestCheckFileContent(t *testing.T) {
	name, ct, err := checkFileContent([]byte("x"), "  kube.yaml  ", "")
	if err != nil || name != "kube.yaml" || ct != "application/octet-stream" {
		t.Fatalf("aparar nome e tipo padrão: %q %q %v", name, ct, err)
	}
	if _, ct, err := checkFileContent([]byte("x"), "a", " text/plain "); err != nil || ct != "text/plain" {
		t.Fatalf("tipo aparado: %q %v", ct, err)
	}
	cases := []struct {
		name  string
		file  []byte
		fname string
		ctype string
		field string
	}{
		{"vazio", nil, "a", "", "file"},
		{"grande demais", make([]byte, domain.MaxSecretFieldFileBytes+1), "a", "", "file"},
		{"nome vazio", []byte("x"), "   ", "", "file_name"},
		{"nome longo", []byte("x"), strings.Repeat("n", 256), "", "file_name"},
		{"tipo longo", []byte("x"), "a", strings.Repeat("t", 256), "content_type"},
	}
	for _, c := range cases {
		_, _, err := checkFileContent(c.file, c.fname, c.ctype)
		if err == nil || !strings.Contains(err.Error(), c.field) {
			t.Fatalf("%s: erro = %v, quer um sobre %q", c.name, err, c.field)
		}
	}
	if _, _, err := checkFileContent(make([]byte, domain.MaxSecretFieldFileBytes), "a", ""); err != nil {
		t.Fatalf("1 MiB exato tem de passar: %v", err)
	}
}

// fileSize converte o tamanho para int32 só dentro da faixa do campo arquivo (gosec G115).
func TestFileSizeConfereAFaixaAntesDeConverter(t *testing.T) {
	if n, err := fileSize(make([]byte, domain.MaxSecretFieldFileBytes)); err != nil || n != domain.MaxSecretFieldFileBytes {
		t.Fatalf("1 MiB exato: %d %v", n, err)
	}
	if _, err := fileSize(nil); err == nil {
		t.Fatal("vazio passou")
	}
	if _, err := fileSize(make([]byte, domain.MaxSecretFieldFileBytes+1)); err == nil {
		t.Fatal("acima do limite passou")
	}
}
