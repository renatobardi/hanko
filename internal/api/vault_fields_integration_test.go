//go:build integration

package api_test

import (
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/secretbox"
)

// Campos e coleções da fatia 1 (#56) pela API: o que liga o service responde, nenhum valor de campo secreto sai nas
// listagens e as ações sensíveis (revelar, histórico, restaurar, apagar, concessões) passam pela reautenticação que
// o service exige (ADR-0070).
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

	// revelar, apagar campo, histórico, restaurar e concessões exigem reautenticação recente (ADR-0070 §1). Este
	// servidor de teste roda sem login (modo trusted): a prova passa (ADR-0070 §5). O gate por sessão, que devolve
	// 401 reauth_required, é TestAcoesSensiveisDoCofrePedemReauth no service.
	const nova = "SENTINELA-api-campo-nova-5f6a7b"
	e.want(200, "POST", base+"/secrets/gh/fields/senha/rotate", `{"value":"`+nova+`"}`, nil)

	var rev api.SecretField
	raw = e.want(200, "POST", base+"/secrets/gh/fields/senha/reveal", "", &rev)
	if rev.Value != nova || strings.Contains(string(raw), sentinela) {
		t.Fatalf("revelar campo = %q (corpo %s)", rev.Value, raw)
	}
	if status, body := e.call("POST", base+"/secrets/gh/fields/senha/reveal", "", nil); status != 409 {
		t.Errorf("revelar a mesma versão duas vezes = %d %s, quer 409", status, body)
	}

	var hist api.ListBody[api.SecretFieldVersion]
	raw = e.want(200, "GET", base+"/secrets/gh/fields/senha/history", "", &hist)
	// v2 veio de rotacionar com valor gerado, que é mostrado na hora: a versão já conta como revelada (secrets.go:218).
	// v1 nunca foi revelada. O histórico lista só as versões substituídas, então a atual (v3) não aparece.
	if len(hist.Items) != 2 || hist.Items[0].Version != 2 || !hist.Items[0].Revealed || hist.Items[1].Version != 1 || hist.Items[1].Revealed || hist.Items[1].Value != "" {
		t.Fatalf("histórico do campo senha: %+v", hist.Items)
	}
	if strings.Contains(string(raw), sentinela) {
		t.Fatalf("histórico vazou valor: %s", raw)
	}

	var v1 api.SecretFieldVersion
	e.want(200, "POST", base+"/secrets/gh/fields/senha/history/1/reveal", "", &v1)
	if v1.Value != sentinela || v1.Version != 1 {
		t.Fatalf("revelar a versão 1 = %+v, quer a sentinela", v1)
	}
	if status, body := e.call("POST", base+"/secrets/gh/fields/senha/history/1/reveal", "", nil); status != 409 {
		t.Errorf("revelar a versão 1 duas vezes = %d %s, quer 409", status, body)
	}
	if status, body := e.call("POST", base+"/secrets/gh/fields/senha/history/9/reveal", "", nil); status != 404 {
		t.Errorf("revelar versão inexistente = %d %s, quer 404", status, body)
	}

	// restaurar a versão 1 cria a versão 4 (nova, não revelada) e o valor atual volta a ser o dela
	var res api.SecretField
	e.want(200, "POST", base+"/secrets/gh/fields/senha/history/1/restore", "", &res)
	if res.Version != 4 || res.Revealed {
		t.Fatalf("restaurar a versão 1 = %+v, quer versão 4 não revelada", res)
	}
	var rev2 api.SecretField
	e.want(200, "POST", base+"/secrets/gh/fields/senha/reveal", "", &rev2)
	if rev2.Value != sentinela {
		t.Fatalf("revelar depois de restaurar = %q, quer a sentinela da versão 1", rev2.Value)
	}

	// apagar campo: o value não se apaga (é o padrão da referência); um campo comum some com o histórico
	if status, body := e.call("DELETE", base+"/secrets/gh/fields/value", "", nil); status != 422 {
		t.Errorf("apagar o campo value = %d %s, quer 422", status, body)
	}
	e.want(204, "DELETE", base+"/secrets/gh/fields/usuario", "", nil)
	if status, _ := e.call("DELETE", base+"/secrets/gh/fields/usuario", "", nil); status != 404 {
		t.Errorf("apagar campo inexistente = %d, quer 404", status)
	}

	// conceder e revogar (admin): persona recebe use, humano view ou manage
	ana := e.persona(e.co, "ana", nil)
	var g api.CollectionGrant
	e.want(201, "POST", base+"/secret-collections/padrão/grants",
		`{"subject_type":"persona","subject":"`+ana.Slug+`","permission":"use"}`, &g)
	if g.SubjectName != ana.Slug || g.Permission != "use" || g.SubjectID == "" {
		t.Fatalf("concessão = %+v", g)
	}
	if status, body := e.call("POST", base+"/secret-collections/padrão/grants",
		`{"subject_type":"persona","subject":"`+ana.Slug+`","permission":"view"}`, nil); status != 422 {
		t.Errorf("persona com view = %d %s, quer 422", status, body)
	}
	if status, body := e.call("POST", base+"/secret-collections/sem-nome/grants",
		`{"subject_type":"all_personas","permission":"use"}`, nil); status != 404 {
		t.Errorf("conceder em coleção inexistente = %d %s, quer 404", status, body)
	}
	e.want(204, "DELETE", base+"/secret-collections/padrão/grants/"+g.ID, "", nil)
	if status, _ := e.call("DELETE", base+"/secret-collections/padrão/grants/"+g.ID, "", nil); status != 404 {
		t.Errorf("revogar concessão já revogada = %d, quer 404", status)
	}
}
