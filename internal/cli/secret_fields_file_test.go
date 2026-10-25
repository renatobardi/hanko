package cli

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
)

// Campo arquivo pela CLI: --kind file --file manda o conteúdo em base64 com o nome do arquivo; as recusas saem como uso
// (código 2) antes de qualquer chamada ao servidor.
func TestSecretFieldAddArquivo(t *testing.T) {
	const base = "/api/v1/companies/acme/secrets/gw/fields"
	saved := api.SecretField{ID: "f9", Name: "kubeconfig", Kind: "file", Sensitive: true, Version: 1}
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{"POST " + base: jsonResp(201, saved)})
	arq := filepath.Join(t.TempDir(), "kubeconfig")
	conteudo := []byte("apiVersion: v1\n")
	if err := os.WriteFile(arq, conteudo, 0o600); err != nil {
		t.Fatal(err)
	}

	code, out, errs := exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "field", "add", "gw", "kubeconfig", "--kind", "file", "--file", arq)
	if code != 0 || !strings.Contains(out, "Campo criado: kubeconfig") {
		t.Fatalf("add arquivo: %d %q %q", code, out, errs)
	}
	body := f.last().Body
	if !strings.Contains(body, `"kind":"file"`) || !strings.Contains(body, `"content_base64":"`+base64.StdEncoding.EncodeToString(conteudo)+`"`) ||
		!strings.Contains(body, `"file_name":"kubeconfig"`) || strings.Contains(body, `"value"`) {
		t.Fatalf("corpo do campo arquivo: %s", body)
	}

	// no limite exato (1 MiB) o pedido sai; um byte a mais é recusado (o teste abaixo)
	grande := filepath.Join(t.TempDir(), "limite")
	if err := os.WriteFile(grande, bytes.Repeat([]byte("a"), 1<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, errs = exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "field", "add", "gw", "kubeconfig", "--kind", "file", "--file", grande); code != 0 {
		t.Fatalf("1 MiB exato devia sair: %d %q %q", code, out, errs)
	}
}

// Recusas do campo arquivo: cada uma sai como erro de uso e não manda nada ao servidor.
func TestSecretFieldAddArquivoRecusas(t *testing.T) {
	const base = "/api/v1/companies/acme/secrets/gw/fields"
	ts, _ := newFakeAPI(t, map[string]http.HandlerFunc{"POST " + base: jsonResp(201, api.SecretField{Name: "kubeconfig"})})
	dir := t.TempDir()
	arq := filepath.Join(dir, "kubeconfig")
	if err := os.WriteFile(arq, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	vazio := filepath.Join(dir, "vazio")
	if err := os.WriteFile(vazio, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	grande := filepath.Join(dir, "grande")
	if err := os.WriteFile(grande, bytes.Repeat([]byte("a"), 1<<20+1), 0o600); err != nil {
		t.Fatal(err)
	}

	casos := map[string][]string{
		"sem --file":           {"secret", "field", "add", "gw", "kubeconfig", "--kind", "file"},
		"com --value":          {"secret", "field", "add", "gw", "kubeconfig", "--kind", "file", "--file", arq, "--value", "x"},
		"com --generate":       {"secret", "field", "add", "gw", "kubeconfig", "--kind", "file", "--file", arq, "--generate"},
		"com --visible":        {"secret", "field", "add", "gw", "kubeconfig", "--kind", "file", "--file", arq, "--visible"},
		"arquivo vazio":        {"secret", "field", "add", "gw", "kubeconfig", "--kind", "file", "--file", vazio},
		"mais de 1 MiB":        {"secret", "field", "add", "gw", "kubeconfig", "--kind", "file", "--file", grande},
		"--file com tipo text": {"secret", "field", "add", "gw", "password", "--value", "x", "--file", arq},
	}
	for nome, args := range casos {
		code, out, errs := exec(t, Runtime{}, gateEnv(ts.URL), "", args...)
		if code != 2 || strings.Contains(out, "Campo criado") {
			t.Errorf("%s: código %d, era 2 (uso) sem criar o campo; saída %q, erro %q", nome, code, out, errs)
		}
	}
}

// Arquivo que não se consegue ler é erro de execução, não de uso, e não cria o campo.
func TestSecretFieldAddArquivoIlegivel(t *testing.T) {
	const base = "/api/v1/companies/acme/secrets/gw/fields"
	ts, _ := newFakeAPI(t, map[string]http.HandlerFunc{"POST " + base: jsonResp(201, api.SecretField{Name: "kubeconfig"})})
	inexistente := filepath.Join(t.TempDir(), "nao-existe")
	code, out, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "field", "add", "gw", "kubeconfig", "--kind", "file", "--file", inexistente)
	if code == 0 || code == 2 || strings.Contains(out, "Campo criado") {
		t.Fatalf("arquivo inexistente: código %d, saída %q", code, out)
	}
}
