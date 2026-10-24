//go:build integration

package api_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/secretbox"
)

// Campo arquivo pela API (ADR-0068 §5.1, #58): o conteúdo entra em base64 e nunca volta; as recusas saem com 422.
func TestAPICampoArquivo(t *testing.T) {
	e := newAPIEnv(t)
	const sentinela = "SENTINELA-api-arquivo-8b7a6c"
	box, err := secretbox.New("0123456789abcdef0123456789abcdef-one", "")
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Integrations.Secrets = box
	base := e.base()
	conteudo := base64.StdEncoding.EncodeToString([]byte(sentinela))

	e.want(201, "POST", base+"/secrets", `{"name":"prd","value":"principal"}`, nil)
	raw := e.want(201, "POST", base+"/secrets/prd/fields",
		`{"name":"kubeconfig","kind":"file","content_base64":"`+conteudo+`","file_name":"kubeconfig","content_type":"text/yaml"}`, nil)
	if strings.Contains(string(raw), sentinela) || strings.Contains(string(raw), conteudo) {
		t.Fatalf("criar campo arquivo devolveu o conteúdo: %s", raw)
	}
	if !strings.Contains(string(raw), `"file_name":"kubeconfig"`) {
		t.Fatalf("a metadata do arquivo não voltou: %s", raw)
	}

	e.want(422, "POST", base+"/secrets/prd/fields", `{"name":"chave","kind":"file","content_base64":"!!não-base64!!"}`, nil)
	e.want(422, "POST", base+"/secrets/prd/fields", `{"name":"texto","content_base64":"`+conteudo+`"}`, nil)
	e.want(422, "POST", base+"/secrets/prd/fields/kubeconfig/rotate", `{"content_base64":"`+conteudo+`"}`, nil)
}

// O limite de 1 MiB do arquivo cabe no corpo da criação: 1 MiB exato passa (201); 1 MiB + 1 byte chega ao service e sai com
// 422 do domínio, não com 413 do transporte.
func TestAPICampoArquivoNoLimiteDeUmMiB(t *testing.T) {
	e := newAPIEnv(t)
	box, err := secretbox.New("0123456789abcdef0123456789abcdef-one", "")
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Integrations.Secrets = box
	base := e.base()
	e.want(201, "POST", base+"/secrets", `{"name":"prd","value":"principal"}`, nil)

	exato := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 1<<20)))
	e.want(201, "POST", base+"/secrets/prd/fields",
		`{"name":"exato","kind":"file","content_base64":"`+exato+`","file_name":"exato"}`, nil)

	acima := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 1<<20+1)))
	e.want(422, "POST", base+"/secrets/prd/fields",
		`{"name":"acima","kind":"file","content_base64":"`+acima+`","file_name":"acima"}`, nil)
}
