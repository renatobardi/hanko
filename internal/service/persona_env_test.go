package service

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
)

// decodeAdapter lê a configuração do adapter gravada, para conferir as chaves sem depender da ordem do JSON.
func decodeAdapter(t *testing.T, cfg []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(cfg, &doc); err != nil {
		t.Fatalf("configuração gravada não é JSON: %v (%s)", err, cfg)
	}
	return doc
}

func TestWithEnvRefSetsReplacesAndRemovesOneVariable(t *testing.T) {
	cfg, err := withEnvRef([]byte(`{"command":"agente","env":{"TOKEN":"${secret:velho}","LEGADO":"valor-em-claro"}}`), "KUBECONFIG", "${secretfile:kube#config}")
	if err != nil {
		t.Fatal(err)
	}
	doc := decodeAdapter(t, cfg)
	env := doc["env"].(map[string]any)
	if env["KUBECONFIG"] != "${secretfile:kube#config}" || env["TOKEN"] != "${secret:velho}" || env["LEGADO"] != "valor-em-claro" {
		t.Fatalf("ao pôr uma variável, as outras ficam: %v", env)
	}
	if doc["command"] != "agente" {
		t.Fatalf("chave fora do env mudou: %v", doc)
	}

	cfg, err = withEnvRef(cfg, "TOKEN", "${secret:novo#token}")
	if err != nil {
		t.Fatal(err)
	}
	if env := decodeAdapter(t, cfg)["env"].(map[string]any); env["TOKEN"] != "${secret:novo#token}" || env["KUBECONFIG"] == nil {
		t.Fatalf("trocar uma variável: %v", env)
	}

	cfg, err = withEnvRef(cfg, "TOKEN", "")
	if err != nil {
		t.Fatal(err)
	}
	env = decodeAdapter(t, cfg)["env"].(map[string]any)
	if _, ok := env["TOKEN"]; ok || env["KUBECONFIG"] == nil || env["LEGADO"] == nil {
		t.Fatalf("remover uma variável só tira ela: %v", env)
	}
}

func TestWithEnvRefDropsTheEnvKeyWhenNoVariableIsLeft(t *testing.T) {
	cfg, err := withEnvRef([]byte(`{"command":"agente","env":{"TOKEN":"${secret:x}"}}`), "TOKEN", "")
	if err != nil {
		t.Fatal(err)
	}
	if doc := decodeAdapter(t, cfg); doc["command"] != "agente" || doc["env"] != nil {
		t.Fatalf("sem variável, a chave env some: %v", doc)
	}
	// configuração vazia (persona sem adapter_config) segue vazia ao tirar uma variável que não existe
	cfg, err = withEnvRef(nil, "X", "")
	if err != nil {
		t.Fatal(err)
	}
	if doc := decodeAdapter(t, cfg); len(doc) != 0 {
		t.Fatalf("configuração vazia: %v", doc)
	}
}

func TestWithEnvRefRefusesInvalidJSON(t *testing.T) {
	if _, err := withEnvRef([]byte(`[1]`), "X", "${secret:x}"); err == nil {
		t.Fatal("adapter_config que não é objeto não pode ser alterado")
	}
}

func TestCheckPersonaEnvInput(t *testing.T) {
	for _, c := range []struct{ name, ref string }{
		{"API_TOKEN", "${secret:gw#token}"},
		{"KUBECONFIG", "${secretfile:kube#config}"},
		{"X", ""}, // remoção
	} {
		if err := checkPersonaEnvInput(c.name, c.ref); err != nil {
			t.Errorf("%q = %q recusado: %v", c.name, c.ref, err)
		}
	}
	for _, c := range []struct{ name, ref, field string }{
		{"1X", "${secret:x}", "name"},
		{"A-B", "${secret:x}", "name"},
		{"", "${secret:x}", "name"},
		{"TOKEN", "texto em claro", "reference"},
		{"TOKEN", "${secret:x} sobra", "reference"},
		{"TOKEN", "prefixo ${secretfile:kube#config}", "reference"},
	} {
		err := checkPersonaEnvInput(c.name, c.ref)
		var ve *domain.ValidationError
		if !errors.As(err, &ve) || ve.Field != c.field {
			t.Errorf("%q = %q: erro %v, want campo %s", c.name, c.ref, err, c.field)
		}
	}
}
