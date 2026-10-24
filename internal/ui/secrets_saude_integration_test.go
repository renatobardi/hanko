//go:build integration

package ui_test

import (
	"net/url"
	"strings"
	"testing"
)

// A página de segredos liga o relatório de saúde e mostra o bloco do gerador; a criação com opções do gerador
// passa pelo formulário sem erro (a checagem do valor gerado fica com a API).
func TestUISecretsHealthLinkAndGeneratorFields(t *testing.T) {
	u := newSecretsUI(t)
	const page = "/c/acme/configuracoes/segredos"

	rec := u.get(page)
	if rec.Code != 200 {
		t.Fatalf("página de segredos: %d", rec.Code)
	}
	for _, want := range []string{page + "/saude", `name="gen_length"`, `name="gen_class"`, `name="gen_words"`} {
		if !strings.Contains(bodyOf(rec), want) {
			t.Fatalf("página de segredos sem %q", want)
		}
	}

	rec = u.get(page + "/saude")
	if rec.Code != 200 || !strings.Contains(bodyOf(rec), "Saúde dos segredos") || !strings.Contains(bodyOf(rec), "Nenhum achado.") {
		t.Fatalf("relatório de saúde sem segredos: %d %s", rec.Code, bodyOf(rec))
	}

	// com valor gerado, a própria resposta mostra o valor uma vez (sem redirecionar)
	rec = u.form(page, url.Values{"name": {"pin"}, "generate": {"1"}, "gen_length": {"12"}, "gen_class": {"digits"}})
	if rec.Code != 200 || !strings.Contains(bodyOf(rec), "Valor de pin") {
		t.Fatalf("criar com gerador: %d %s", rec.Code, bodyOf(rec))
	}
	rec = u.form(page, url.Values{"name": {"pin2"}, "generate": {"1"}, "gen_length": {"abc"}})
	if strings.Contains(rec.Header().Get("Location"), "ok=criado") {
		t.Fatalf("tamanho ilegível foi aceito: %s", rec.Header().Get("Location"))
	}
}
