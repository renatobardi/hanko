//go:build integration

package api_test

import (
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/secretbox"
)

// Campos e coleções da fatia 1 (#56) pela API: o que liga o service responde, nenhum valor de campo secreto sai e
// as operações que pedem reautenticação seguem em 501 até a fatia 2 (#57).
func TestAPICamposECollecoes(t *testing.T) {
	e := newAPIEnv(t)
	const sentinela = "SENTINELA-api-campo-3e9d1b7a"
	box, err := secretbox.New("0123456789abcdef0123456789abcdef-one", "")
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Integrations.Secrets = box
	base := e.base()

	e.want(201, "POST", base+"/secrets", `{"name":"gh","value":"`+sentinela+`"}`, nil)
	e.want(201, "POST", base+"/secrets/gh/fields", `{"name":"usuario","sensitive":false,"value":"ana@exemplo.com"}`, nil)
	raw := e.want(201, "POST", base+"/secrets/gh/fields", `{"name":"senha","value":"`+sentinela+`"}`, nil)
	if strings.Contains(string(raw), sentinela) {
		t.Fatalf("criar campo devolveu o valor secreto: %s", raw)
	}

	var list api.ListBody[api.SecretField]
	raw = e.want(200, "GET", base+"/secrets/gh/fields", "", &list)
	if strings.Contains(string(raw), sentinela) {
		t.Fatalf("listar campos vazou o valor: %s", raw)
	}
	visiveis := map[string]string{}
	for _, f := range list.Items {
		visiveis[f.Name] = f.VisibleText
	}
	if visiveis["usuario"] != "ana@exemplo.com" || visiveis["senha"] != "" || len(list.Items) != 3 { // value (de todo segredo), usuario e senha
		t.Fatalf("campos: %+v", list.Items)
	}

	raw = e.want(200, "POST", base+"/secrets/gh/fields/senha/rotate", `{"generate":true}`, nil)
	if strings.Contains(string(raw), sentinela) {
		t.Fatalf("rotacionar devolveu o valor antigo: %s", raw)
	}
	e.want(200, "PATCH", base+"/secrets/gh/fields/senha", `{"sensitive":true,"position":3}`, nil)

	var col api.SecretCollection
	e.want(201, "POST", base+"/secret-collections", `{"name":"infra","description":"servidores"}`, &col)
	e.want(204, "PUT", base+"/secret-collections/infra/secrets/gh", "", nil)
	var itens api.ListBody[api.Secret]
	e.want(200, "GET", base+"/secret-collections/infra/secrets", "", &itens)
	if len(itens.Items) != 1 || itens.Items[0].Name != "gh" {
		t.Fatalf("itens da coleção: %+v", itens.Items)
	}
	e.want(204, "DELETE", base+"/secret-collections/infra/secrets/gh", "", nil)
	e.want(204, "DELETE", base+"/secret-collections/infra", "", nil)

	// a padrão não se apaga: o service recusa, e a API devolve erro de validação
	if status, body := e.call("DELETE", base+"/secret-collections/padrão", "", nil); status < 400 || status >= 500 {
		t.Fatalf("apagar a padrão: %d %s", status, body)
	}

	// concessão e revelar campo exigem reautenticação (ADR-0070): seguem em 501
	for _, c := range []struct{ method, path, body string }{
		{"POST", base + "/secret-collections/padrão/grants", `{"subject_type":"all_personas","permission":"use"}`},
		{"POST", base + "/secrets/gh/fields/senha/reveal", ""},
		{"GET", base + "/secrets/gh/fields/senha/history", ""},
	} {
		if status, body := e.call(c.method, c.path, c.body, nil); status != 501 {
			t.Errorf("%s %s: %d %s, quer 501 (reautenticação, fatia 2)", c.method, c.path, status, body)
		}
	}
}
