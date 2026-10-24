//go:build integration

package api_test

import (
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/secretbox"
)

// Gerador, relatório de saúde e PATCH do segredo (fatia 6, #61) pela API: o que liga o service responde, o valor
// gerado sai uma vez, e o relatório nunca leva valor de campo.
func TestAPIGeradorESaude(t *testing.T) {
	e := newAPIEnv(t)
	const sentinela = "SENTINELA-api-saude-7c2e9a4f"
	box, err := secretbox.New("0123456789abcdef0123456789abcdef-one", "")
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Integrations.Secrets = box
	base := e.base()
	const root = "/api/v1" // o gerador não é da empresa: fica fora de /companies/{company}

	var gerado api.GeneratedValue
	e.want(200, "POST", root+"/secrets/generate", `{"length":16,"classes":["digits"]}`, &gerado)
	if len(gerado.Value) != 16 || strings.Trim(gerado.Value, "0123456789") != "" {
		t.Fatalf("gerado = %+v, quer 16 dígitos", gerado)
	}
	var frase api.GeneratedValue
	e.want(200, "POST", root+"/secrets/generate", `{"passphrase_words":4}`, &frase)
	if len(strings.Split(frase.Value, "-")) != 4 || frase.EntropyBits <= 0 {
		t.Fatalf("frase = %+v", frase)
	}
	e.want(422, "POST", root+"/secrets/generate", `{"length":3}`, nil)
	e.want(422, "POST", root+"/secrets/generate", `{"passphrase_words":2}`, nil)

	var criado api.Secret
	e.want(201, "POST", base+"/secrets", `{"name":"pin","generate":true,"generator":{"length":12,"classes":["digits"]}}`, &criado)
	if len(criado.Value) != 12 || strings.Trim(criado.Value, "0123456789") != "" {
		t.Fatalf("valor gerado no cadastro = %q, quer 12 dígitos", criado.Value)
	}

	e.want(201, "POST", base+"/secrets", `{"name":"gh","value":"`+sentinela+`"}`, nil)
	var saude api.VaultHealth
	raw := e.want(200, "GET", base+"/vault/health", "", &saude)
	if strings.Contains(string(raw), sentinela) {
		t.Fatalf("relatório de saúde vazou o valor: %s", raw)
	}
	for _, kind := range []string{"duplicate", "stale", "unused", "orphan", "weak"} {
		if _, ok := saude.Counts[kind]; !ok {
			t.Fatalf("contagem sem a categoria %q: %+v", kind, saude.Counts)
		}
	}
	if saude.Counts["orphan"] < 1 {
		t.Fatalf("segredo sem uso não apareceu como órfão: %+v", saude.Counts)
	}

	var meta api.Secret
	raw = e.want(200, "PATCH", base+"/secrets/gh", `{"description":"conta de CI","rotate_after_days":30}`, &meta)
	if meta.RotateAfterDays == nil || *meta.RotateAfterDays != 30 || meta.Description != "conta de CI" {
		t.Fatalf("PATCH não gravou os metadados: %s", raw)
	}
	if strings.Contains(string(raw), sentinela) {
		t.Fatalf("PATCH devolveu o valor: %s", raw)
	}
	e.want(422, "PATCH", base+"/secrets/gh", `{"rotate_after_days":3651}`, nil)
}
