//go:build integration

package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
)

// Revisões das instruções pela API (ADR-0052): histórico, diff, restauração e a revisão vigente na persona.
func TestAPIRevisoesDeInstrucoes(t *testing.T) {
	e := newAPIEnv(t)
	ps := e.base() + "/personas"
	var ada api.Persona
	e.want(201, "POST", ps, `{"name":"Ada","agent":"fake","instructions":"Seja breve."}`, &ada)
	if ada.InstructionRevision != 1 {
		t.Fatalf("revisão ao contratar: %+v", ada)
	}

	e.want(200, "PATCH", ps+"/ada", `{"instructions":"Seja detalhista.","instructions_reason":"ficou curto"}`, &ada)
	if ada.InstructionRevision != 2 || ada.Instructions != "Seja detalhista." {
		t.Fatalf("depois de editar: %+v", ada)
	}

	var list struct {
		Items []api.InstructionRevision `json:"items"`
	}
	e.want(200, "GET", ps+"/ada/instructions/revisions", "", &list)
	if len(list.Items) != 2 || list.Items[0].Revision != 2 || !list.Items[0].Current || list.Items[0].Reason != "ficou curto" ||
		list.Items[0].Body != "" || list.Items[1].Origin != "hire" || list.Items[1].Current {
		t.Fatalf("lista: %+v", list.Items)
	}

	var one api.InstructionRevision
	e.want(200, "GET", ps+"/ada/instructions/revisions/2?against=1", "", &one)
	if one.Body != "Seja detalhista." || one.Diff == nil || one.Diff.Added != 1 || one.Diff.Removed != 1 || one.Diff.From != 1 || one.Diff.To != 2 {
		t.Fatalf("revisão 2 com diff: %+v", one)
	}
	if status, b := e.call("GET", ps+"/ada/instructions/revisions/9", "", nil); status != http.StatusNotFound {
		t.Fatalf("revisão inexistente: %d %s", status, b)
	}

	var restored api.Persona
	e.want(200, "POST", ps+"/ada/instructions/restore", `{"revision":1,"reason":"voltar"}`, &restored)
	if restored.InstructionRevision != 3 || restored.Instructions != "Seja breve." {
		t.Fatalf("restaurar: %+v", restored)
	}
	e.want(200, "GET", ps+"/ada/instructions/revisions/3", "", &one)
	if one.Origin != "restore" || one.RestoredFrom != 1 || one.Reason != "voltar" {
		t.Fatalf("revisão restaurada: %+v", one)
	}

	// motivo longo demais: validação do esquema
	long := strings.Repeat("a", 501)
	if status, b := e.call("PATCH", ps+"/ada", `{"instructions":"x","instructions_reason":"`+long+`"}`, nil); status != http.StatusUnprocessableEntity {
		t.Fatalf("motivo longo: %d %s", status, b)
	}
	// a proposta da persona só vale com o token da própria persona: um humano é recusado
	if status, b := e.call("POST", ps+"/ada/instructions/proposals", `{"instructions":"novo"}`, nil); status != http.StatusForbidden {
		t.Fatalf("humano propondo: %d %s", status, b)
	}
}
