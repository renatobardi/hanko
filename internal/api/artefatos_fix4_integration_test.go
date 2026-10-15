//go:build integration

package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Correções da revisão de Artefatos (ADR-0028).

// O token de uma run de chat (sem task) publica pela API: o artefato fica só com a run; informar task é recusado.
func TestAPIArtifactChatRunToken(t *testing.T) {
	e := newAPIEnv(t)
	ctx := context.Background()
	base := e.base()
	p := e.persona(e.co, "Ana", nil)
	other := e.task(e.co, "outra", nil, nil)
	cv, err := e.svc.CreateConversation(ctx, domain.HumanActor, e.co.ID, service.CreateConversationInput{PersonaID: p.ID, Message: "oi"})
	if err != nil {
		t.Fatal(err)
	}
	runs, err := e.svc.ListRuns(ctx, e.co.ID, service.RunFilter{ConversationID: &cv.ID})
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs: %v %v", runs, err)
	}
	run := runs[0].Run
	tok, err := e.tokens.IssueForConversation(e.co.ID, p.ID, run.ID, cv.ID)
	if err != nil {
		t.Fatal(err)
	}
	hdr := map[string]string{"Authorization": "Bearer " + tok}

	st, b := e.call("POST", base+"/artifacts", putBody("resposta.md", "", "# do chat", ""), hdr)
	if st != http.StatusCreated {
		t.Fatalf("run de chat publica: %d %s", st, b)
	}
	var a api.Artifact
	if err := json.Unmarshal(b, &a); err != nil {
		t.Fatal(err)
	}
	if a.TaskID != "" || a.RunID != run.ID.String() || a.PersonaID != p.ID.String() || a.Author.Type != "persona" {
		t.Fatalf("escopo da run de chat: %+v", a)
	}
	// task informada: proibido; run alheia: proibido
	if st, _ := e.call("POST", base+"/artifacts", putBody("x.md", "", "x", fmt.Sprintf(`,"task":%q`, other.Key)), hdr); st != http.StatusForbidden {
		t.Errorf("task em run de chat: %d", st)
	}
	if st, _ := e.call("POST", base+"/artifacts", putBody("x.md", "", "x", fmt.Sprintf(`,"run_id":%q`, domain.NewID())), hdr); st != http.StatusForbidden {
		t.Errorf("run alheia: %d", st)
	}
}

// O corpo da rota de envio vale o limite configurado, não o máximo absoluto: um arquivo acima do limite é recusado
// com 413 antes de ser lido por inteiro.
func TestAPIArtifactBodyUsesConfiguredLimit(t *testing.T) {
	e := newAPIEnvWith(t, func(d *api.Deps) { d.Svc.ArtifactMaxBytes = 1 << 20 })
	base := e.base()
	// 3 MiB de conteúdo: bem abaixo dos 16 MiB absolutos, acima de 1 MiB configurado (+ base64)
	big := strings.Repeat("A", 4<<20)
	st, b := e.call("POST", base+"/artifacts", `{"name":"x.txt","content":"`+big+`"}`, nil)
	if st != http.StatusRequestEntityTooLarge {
		t.Errorf("acima do configurado: %d %.200s", st, b)
	}
	// dentro do limite configurado passa
	e.want(201, "POST", base+"/artifacts", putBody("ok.txt", "", string(bytes.Repeat([]byte("a"), 1<<20)), ""), nil)
}
