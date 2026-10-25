//go:build integration

package api_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/secretbox"
)

// Delegação de segredo pessoal e uso do segredo da empresa pela API (ADR-0068 §6, issue #60): as rotas respondem o que
// o service decide, só o dono delega e vê o uso do item pessoal, e nenhuma resposta leva o valor.
func TestAPIDelegacaoEUsoDoCofre(t *testing.T) {
	e := setupAuth(t)
	box, err := secretbox.New("0123456789abcdef0123456789abcdef-one", "")
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Integrations.Secrets = box
	const sentinela = "SENTINELA-api-delegacao-9a8b7c"
	const base = "/companies/acme"

	// o membro cria o item pessoal e a task que pediu
	if status, b := e.call(t, "member", "POST", base+"/user-secrets", `{"name":"meu-token","value":"`+sentinela+`"}`); status != 201 {
		t.Fatalf("criar item pessoal: %d %s", status, b)
	}
	status, b := e.call(t, "member", "POST", base+"/tasks", `{"title":"tarefa do membro"}`)
	if status != 201 {
		t.Fatalf("criar task: %d %s", status, b)
	}
	var tk api.Task
	if err := json.Unmarshal([]byte(b), &tk); err != nil || tk.ID == "" {
		t.Fatalf("task sem id: %v %s", err, b)
	}

	// delegar à task que ele pediu: 201 e a delegação ativa; quem não é membro não delega
	var del api.SecretDelegation
	status, b = e.call(t, "member", "POST", base+"/user-secrets/meu-token/delegations", `{"target_type":"task","target_id":"`+tk.ID+`"}`)
	if status != 201 || json.Unmarshal([]byte(b), &del) != nil || !del.Active || del.ID == "" {
		t.Fatalf("delegar à task: %d %s", status, b)
	}
	if status, b := e.call(t, "stranger", "POST", base+"/user-secrets/meu-token/delegations", `{"target_type":"task","target_id":"`+tk.ID+`"}`); status < 400 || status >= 500 {
		t.Fatalf("estranho delegando: %d %s", status, b)
	}

	// as delegações do membro: só as dele, com a ativa na lista
	var mine api.ListBody[api.SecretDelegation]
	status, b = e.call(t, "member", "GET", base+"/me/delegations", "")
	if status != 200 || json.Unmarshal([]byte(b), &mine) != nil || len(mine.Items) != 1 || mine.Items[0].ID != del.ID {
		t.Fatalf("minhas delegações: %d %s", status, b)
	}

	// o uso do item pessoal é do dono: o membro vê a lista vazia (nada foi usado), o admin não acha o item dele
	status, b = e.call(t, "member", "GET", base+"/user-secrets/meu-token/usage", "")
	if status != 200 || strings.Contains(b, sentinela) {
		t.Fatalf("uso do item pessoal: %d %s", status, b)
	}
	if status, b := e.call(t, "admin", "GET", base+"/user-secrets/meu-token/usage", ""); status != 404 {
		t.Fatalf("admin vendo o uso do item do membro: %d %s", status, b)
	}

	// revogar: 204; de novo, 404 (a delegação já não está ativa)
	if status, b := e.call(t, "member", "DELETE", base+"/me/delegations/"+del.ID, ""); status != 204 {
		t.Fatalf("revogar: %d %s", status, b)
	}
	if status, b := e.call(t, "member", "DELETE", base+"/me/delegations/"+del.ID, ""); status != 404 {
		t.Fatalf("revogar de novo: %d %s", status, b)
	}

	// uso do segredo da empresa: o dono (admin) vê; o membro sem concessão de coleção não
	if status, b := e.call(t, "owner", "POST", base+"/secrets", `{"name":"gh","value":"`+sentinela+`"}`); status != 201 {
		t.Fatalf("criar segredo da empresa: %d %s", status, b)
	}
	var uses api.ListBody[api.SecretAccess]
	status, b = e.call(t, "owner", "GET", base+"/secrets/gh/usage", "")
	if status != 200 || json.Unmarshal([]byte(b), &uses) != nil || len(uses.Items) != 0 {
		t.Fatalf("uso do segredo da empresa pelo dono: %d %s", status, b)
	}
	if status, b := e.call(t, "member", "GET", base+"/secrets/gh/usage", ""); status != 403 {
		t.Fatalf("membro sem concessão vendo o uso: %d %s", status, b)
	}
}
