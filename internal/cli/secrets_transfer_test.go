package cli

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
)

const (
	transferBase = "/api/v1/companies/acme/vault"
	envSentinela = "SENTINELA-cli-env-8e41"
)

func writeEnvFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "app.env")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSecretImportEnvPrevinaEGrava(t *testing.T) {
	plan := api.EnvImportPlan{Collection: "padrão", Items: []api.EnvImportItem{
		{Secret: "TOKEN", Fields: []string{"value"}}, {Secret: "DB", Fields: []string{"value"}, Exists: true}},
		Skipped: []string{"linha 3: valor vazio"}}
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"POST " + transferBase + "/import-env/preview": jsonResp(200, plan),
		"POST " + transferBase + "/import-env":         jsonResp(200, api.VaultImportResult{Secrets: 1, Fields: 1, Existing: 1}),
	})
	file := writeEnvFile(t, "TOKEN="+envSentinela+"\n")

	code, out, errs := exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "import-env", file, "--dry-run")
	if code != 0 || !strings.Contains(out, "TOKEN: value") || !strings.Contains(out, "DB (já existe)") ||
		!strings.Contains(out, "linha 3: valor vazio") || !strings.Contains(out, "Nada foi gravado") {
		t.Fatalf("dry-run: %d %q %q", code, out, errs)
	}
	if h := f.last(); h.Path != transferBase+"/import-env/preview" || !strings.Contains(h.Body, `"mode":"items"`) {
		t.Fatalf("o dry-run só pode pedir a prévia: %+v", h)
	}

	code, out, errs = exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "import-env", file, "--item", "app", "--collection", "infra", "--overwrite")
	if code != 0 || !strings.Contains(out, "Importado: 1 segredo(s) novo(s), 1 campo(s), 0 atualizado(s), 1 já existente(s)") {
		t.Fatalf("grava: %d %q %q", code, out, errs)
	}
	h := f.last()
	for _, want := range []string{`"mode":"single_item"`, `"item_name":"app"`, `"collection":"infra"`, `"overwrite":true`, envSentinela} {
		if h.Path != transferBase+"/import-env" || !strings.Contains(h.Body, want) {
			t.Fatalf("corpo do import sem %s: %+v", want, h)
		}
	}
	if strings.Contains(out+errs, envSentinela) {
		t.Fatal("a CLI não pode ecoar o valor do .env")
	}
}

func TestSecretImportEnvLeDoStdinEErroDeArquivo(t *testing.T) {
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"POST " + transferBase + "/import-env/preview": jsonResp(200, api.EnvImportPlan{}),
		"POST " + transferBase + "/import-env":         jsonResp(200, api.VaultImportResult{}),
	})
	if code, _, errs := exec(t, Runtime{}, gateEnv(ts.URL), "A=1\n", "secret", "import-env", "-"); code != 0 || !strings.Contains(f.last().Body, `"content":"A=1\n"`) {
		t.Fatalf("stdin: %d %q %+v", code, errs, f.last())
	}
	if code, _, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "import-env", filepath.Join(t.TempDir(), "não-existe.env")); code == 0 {
		t.Fatal("arquivo inexistente devia falhar")
	}
	big := writeEnvFile(t, strings.Repeat("A=1\n", maxEnvFileBytes/4+10))
	if code, _, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "import-env", big); code != 2 {
		t.Fatalf("arquivo grande: código %d, quer 2", code)
	}
}

func TestSecretExportGravaOArquivoEConfereOHash(t *testing.T) {
	data := []byte("HKV1\nconteúdo-cifrado-qualquer")
	sum := sha256.Sum256(data)
	exp := api.VaultExport{FileBase64: base64.StdEncoding.EncodeToString(data), SHA256: hex.EncodeToString(sum[:]), Secrets: 3}
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{"POST " + transferBase + "/export": jsonResp(200, exp)})
	out := filepath.Join(t.TempDir(), "cofre.hkv")

	code, so, errs := exec(t, Runtime{}, gateEnv(ts.URL), "senha-do-arquivo-1234\nresto\n", "secret", "export", "--out", out, "--password-stdin")
	if code != 0 || !strings.Contains(so, "3 segredo(s)") || !strings.Contains(so, exp.SHA256) {
		t.Fatalf("export: %d %q %q", code, so, errs)
	}
	if h := f.last(); !strings.Contains(h.Body, `"password":"senha-do-arquivo-1234"`) || strings.Contains(h.Body, "resto") {
		t.Fatalf("a senha é só a primeira linha do stdin: %+v", h)
	}
	got, err := os.ReadFile(out)
	if err != nil || string(got) != string(data) {
		t.Fatalf("arquivo = %q (%v)", got, err)
	}
	if st, _ := os.Stat(out); st.Mode().Perm() != 0o600 {
		t.Fatalf("permissão = %v, quer 0600", st.Mode().Perm())
	}
	if strings.Contains(so+errs, "senha-do-arquivo-1234") {
		t.Fatal("a CLI não pode ecoar a senha")
	}

	// não sobrescreve sem --force; com --force sobrescreve
	if code, _, _ := exec(t, Runtime{}, gateEnv(ts.URL), "senha-do-arquivo-1234\n", "secret", "export", "--out", out, "--password-stdin"); code == 0 {
		t.Fatal("devia recusar sobrescrever sem --force")
	}
	if code, _, errs := exec(t, Runtime{}, gateEnv(ts.URL), "senha-do-arquivo-1234\n", "secret", "export", "--out", out, "--password-stdin", "--force"); code != 0 {
		t.Fatalf("--force: %d %q", code, errs)
	}
}

func TestSecretExportSenhaPorAmbienteEErrosDeUso(t *testing.T) {
	data := []byte("HKV1\nx")
	sum := sha256.Sum256(data)
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{"POST " + transferBase + "/export": jsonResp(200,
		api.VaultExport{FileBase64: base64.StdEncoding.EncodeToString(data), SHA256: hex.EncodeToString(sum[:]), Secrets: 1})})
	dir := t.TempDir()
	env := gateEnv(ts.URL)
	env["HANKO_VAULT_PASSWORD"] = "senha-por-ambiente-12"
	if code, _, errs := exec(t, Runtime{}, env, "", "secret", "export", "--out", filepath.Join(dir, "a.hkv")); code != 0 || !strings.Contains(f.last().Body, "senha-por-ambiente-12") {
		t.Fatalf("senha por ambiente: %d %q", code, errs)
	}
	// sem senha, sem --out e senha vazia no stdin são erros de uso e não chamam o servidor
	n := len(f.hits)
	for name, c := range map[string]struct {
		stdin string
		args  []string
	}{
		"sem senha":   {"", []string{"secret", "export", "--out", filepath.Join(dir, "b.hkv")}},
		"sem --out":   {"x\n", []string{"secret", "export", "--password-stdin"}},
		"stdin vazio": {"\n", []string{"secret", "export", "--out", filepath.Join(dir, "c.hkv"), "--password-stdin"}},
	} {
		if code, _, _ := exec(t, Runtime{}, gateEnv(ts.URL), c.stdin, c.args...); code != 2 {
			t.Errorf("%s: código %d, quer 2", name, code)
		}
	}
	if len(f.hits) != n {
		t.Fatal("erro de uso não pode chamar o servidor")
	}
	// o hash que não bate impede de gravar o arquivo
	bad, _ := newFakeAPI(t, map[string]http.HandlerFunc{"POST " + transferBase + "/export": jsonResp(200,
		api.VaultExport{FileBase64: base64.StdEncoding.EncodeToString(data), SHA256: strings.Repeat("0", 64), Secrets: 1})})
	out := filepath.Join(dir, "d.hkv")
	if code, _, _ := exec(t, Runtime{}, gateEnv(bad.URL), "senha-do-arquivo-1234\n", "secret", "export", "--out", out, "--password-stdin"); code == 0 {
		t.Fatal("hash divergente devia falhar")
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("o arquivo foi gravado apesar do hash divergente")
	}
}

func TestSecretImportDoCofre(t *testing.T) {
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{"POST " + transferBase + "/import": jsonResp(200,
		api.VaultImportResult{Secrets: 2, Fields: 5, Existing: 1, Collections: 1, Grants: 2, Skipped: 1})})
	file := filepath.Join(t.TempDir(), "cofre.hkv")
	data := []byte("HKV1\ncifrado")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errs := exec(t, Runtime{}, gateEnv(ts.URL), "senha-do-arquivo-1234\n", "secret", "import", file, "--password-stdin")
	if code != 0 || !strings.Contains(out, "Importado: 2 segredo(s) novo(s), 5 campo(s), 1 já existente(s) mantido(s), 1 coleção(ões), 2 concessão(ões), 1 concessão(ões) não aplicada(s).") {
		t.Fatalf("import: %d %q %q", code, out, errs)
	}
	h := f.last()
	if !strings.Contains(h.Body, `"file_base64":"`+base64.StdEncoding.EncodeToString(data)+`"`) || !strings.Contains(h.Body, `"password":"senha-do-arquivo-1234"`) {
		t.Fatalf("corpo = %s", h.Body)
	}
	if code, _, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "import", file); code != 2 {
		t.Fatalf("sem senha: código %d, quer 2", code)
	}
	if code, _, _ := exec(t, Runtime{}, gateEnv(ts.URL), "x\n", "secret", "import", filepath.Join(t.TempDir(), "nada.hkv"), "--password-stdin"); code == 0 {
		t.Fatal("arquivo inexistente devia falhar")
	}
}
