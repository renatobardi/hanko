//go:build integration

package api_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Anexos, rascunhos, /new e proposta de instruções pela API (ADR-0052).

func TestAPIAttachments(t *testing.T) {
	e := newAPIEnv(t)
	base := e.base()
	tk := e.task(e.co, "com anexo", nil, nil)
	var cm api.Comment
	e.want(201, "POST", base+"/tasks/"+tk.Key+"/comments", `{"body":"veja"}`, &cm)

	var a, b api.Artifact
	e.want(201, "POST", base+"/attachments", fmt.Sprintf(`{"name":"spec.txt","content":%q,"task":%q}`, b64("conteúdo"), tk.Key), &a)
	if a.Class != "attachment" || a.Version != 1 || a.TaskKey != tk.Key || a.Mime != "text/plain" || a.Author.Type != "human" || a.CommentID != "" {
		t.Fatalf("anexo: %+v", a)
	}
	e.want(201, "POST", base+"/attachments", fmt.Sprintf(`{"name":"spec.txt","content":%q,"task":%q,"comment_id":%q}`, b64("outro"), tk.Key, cm.ID), &b)
	if b.Name != "spec (2).txt" || b.CommentID != cm.ID {
		t.Fatalf("segundo anexo: %+v", b)
	}

	var list struct {
		Items []api.Artifact `json:"items"`
	}
	e.want(200, "GET", base+"/attachments?task="+tk.Key, "", &list)
	if len(list.Items) != 2 {
		t.Fatalf("lista: %+v", list.Items)
	}
	e.want(200, "GET", base+"/attachments?comment_id="+cm.ID, "", &list)
	if len(list.Items) != 1 || list.Items[0].ID != b.ID {
		t.Fatalf("lista do comentário: %+v", list.Items)
	}
	// a lista de artefatos não mostra anexos, a menos que peça a classe
	e.want(200, "GET", base+"/artifacts?task="+tk.Key, "", &list)
	if len(list.Items) != 0 {
		t.Fatalf("artefatos não deveriam incluir anexos: %+v", list.Items)
	}
	e.want(200, "GET", base+"/artifacts?task="+tk.Key+"&class=all", "", &list)
	if len(list.Items) != 2 {
		t.Fatalf("class=all: %+v", list.Items)
	}

	// o conteúdo sai pelo mesmo caminho seguro dos artefatos
	resp, body := e.raw("/api/v1/companies/"+e.co.Slug+"/artifacts/"+a.ID+"/content", nil)
	if resp.StatusCode != 200 || string(body) != "conteúdo" || resp.Header.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("conteúdo: %d %q %v", resp.StatusCode, body, resp.Header)
	}

	// entradas inválidas
	for name, body := range map[string]string{
		"sem alvo":             fmt.Sprintf(`{"name":"x.txt","content":%q}`, b64("x")),
		"comentário alheio":    fmt.Sprintf(`{"name":"x.txt","content":%q,"task":%q,"comment_id":%q}`, b64("x"), tk.Key, idStr(domain.NewID())),
		"task e conversa":      fmt.Sprintf(`{"name":"x.txt","content":%q,"task":%q,"conversation":%q}`, b64("x"), tk.Key, idStr(domain.NewID())),
		"nome com barra":       fmt.Sprintf(`{"name":"a/b","content":%q,"task":%q}`, b64("x"), tk.Key),
		"uuid de comentário":   fmt.Sprintf(`{"name":"x.txt","content":%q,"task":%q,"comment_id":"nao"}`, b64("x"), tk.Key),
		"conversa inexistente": fmt.Sprintf(`{"name":"x.txt","content":%q,"conversation":%q}`, b64("x"), idStr(domain.NewID())),
	} {
		if status, out := e.call("POST", base+"/attachments", body, nil); status < 400 || status >= 500 {
			t.Errorf("%s: status %d: %s", name, status, out)
		}
	}

	// excluir (admin humano) apaga só aquele anexo
	e.want(204, "DELETE", base+"/artifacts/"+a.ID, "", nil)
	e.want(200, "GET", base+"/attachments?task="+tk.Key, "", &list)
	if len(list.Items) != 1 || list.Items[0].ID != b.ID {
		t.Fatalf("depois de excluir: %+v", list.Items)
	}
}

func idStr(id domain.ID) string { return id.String() }

func TestAPIAttachmentsWithRunToken(t *testing.T) {
	e := newAPIEnv(t)
	base := e.base()
	p := e.persona(e.co, "Eng", nil)
	hdr := e.personaToken(e.co, p) // run token de uma task da própria persona
	other := e.task(e.co, "de outra", nil, nil)

	// o token anexa à task da própria execução, mesmo sem informar o alvo
	status, out := e.call("POST", base+"/attachments", fmt.Sprintf(`{"name":"log.txt","content":%q}`, b64("ok")), hdr)
	if status != 201 {
		t.Fatalf("anexo com token: %d %s", status, out)
	}
	// e é recusado em outra task
	if status, out := e.call("POST", base+"/attachments", fmt.Sprintf(`{"name":"log.txt","content":%q,"task":%q}`, b64("ok"), other.Key), hdr); status != http.StatusForbidden {
		t.Fatalf("task alheia: %d %s", status, out)
	}

	// token de chat: anexa na própria conversa
	cv, err := e.svc.CreateConversation(context.Background(), domain.HumanActor, e.co.ID, service.CreateConversationInput{PersonaID: p.ID, Message: "oi"})
	if err != nil {
		t.Fatal(err)
	}
	runs, _ := e.svc.ListRuns(context.Background(), e.co.ID, service.RunFilter{ConversationID: &cv.ID})
	if len(runs) != 1 {
		t.Fatalf("runs do chat: %d", len(runs))
	}
	tok, err := e.tokens.IssueForConversation(e.co.ID, p.ID, runs[0].ID, cv.ID)
	if err != nil {
		t.Fatal(err)
	}
	ch := map[string]string{"Authorization": "Bearer " + tok}
	var a api.Artifact
	status, out = e.call("POST", base+"/attachments", fmt.Sprintf(`{"name":"nota.md","content":%q}`, b64("# nota")), ch)
	if status != 201 {
		t.Fatalf("anexo no chat: %d %s", status, out)
	}
	_ = a
	var list struct {
		Items []api.Artifact `json:"items"`
	}
	e.want(200, "GET", base+"/attachments?conversation="+cv.ID.String(), "", &list)
	if len(list.Items) != 1 || list.Items[0].ConversationID != cv.ID.String() || list.Items[0].Author.Type != "persona" {
		t.Fatalf("anexos da conversa: %+v", list.Items)
	}
	// outra conversa: recusado
	if status, out := e.call("POST", base+"/attachments", fmt.Sprintf(`{"name":"x.md","content":%q,"conversation":%q}`, b64("x"), domain.NewID().String()), ch); status != http.StatusForbidden {
		t.Fatalf("conversa alheia: %d %s", status, out)
	}
}

func TestAPIDrafts(t *testing.T) {
	e := newAPIEnv(t)
	base := e.base()
	tk := e.task(e.co, "t", nil, nil)
	url := base + "/drafts/task/" + tk.ID.String()

	var d api.Draft
	e.want(200, "GET", url, "", &d)
	if d.Exists || d.Body != "" || d.TargetType != "task" {
		t.Fatalf("sem rascunho: %+v", d)
	}
	e.want(200, "PUT", url, `{"body":"quase pronto"}`, &d)
	if !d.Exists || d.Body != "quase pronto" || d.UpdatedAt == nil {
		t.Fatalf("gravado: %+v", d)
	}
	e.want(200, "GET", url, "", &d)
	if d.Body != "quase pronto" {
		t.Fatalf("lido: %+v", d)
	}
	// enviar o comentário limpa
	e.want(201, "POST", base+"/tasks/"+tk.Key+"/comments", `{"body":"quase pronto"}`, nil)
	e.want(200, "GET", url, "", &d)
	if d.Exists {
		t.Fatalf("o envio deveria limpar: %+v", d)
	}
	e.want(200, "PUT", url, `{"body":"outra"}`, &d)
	e.want(204, "DELETE", url, "", nil)
	e.want(204, "DELETE", url, "", nil) // idempotente
	e.want(200, "GET", url, "", &d)
	if d.Exists {
		t.Fatalf("apagado: %+v", d)
	}
	// em branco apaga
	e.want(200, "PUT", url, `{"body":"x"}`, &d)
	e.want(200, "PUT", url, `{"body":"   "}`, &d)
	if d.Exists {
		t.Fatalf("em branco: %+v", d)
	}
	// alvo inexistente, tipo inválido e uuid inválido
	for path, want := range map[string]int{
		base + "/drafts/task/" + domain.NewID().String(): 404,
		base + "/drafts/conversation/nao-e-uuid":         422,
		base + "/drafts/foo/" + tk.ID.String():           422,
	} {
		if status, out := e.call("PUT", path, `{"body":"x"}`, nil); status != want {
			t.Errorf("PUT %s: %d, quer %d: %s", path, status, want, out)
		}
	}
	// run token (persona) não tem rascunho
	p := e.persona(e.co, "Eng", nil)
	hdr := e.personaToken(e.co, p)
	if status, out := e.call("PUT", url, `{"body":"x"}`, hdr); status != http.StatusForbidden {
		t.Fatalf("persona: %d %s", status, out)
	}
}

func TestAPIChatNew(t *testing.T) {
	e := newAPIEnv(t)
	base := e.base()
	p := e.persona(e.co, "Ada", nil)
	var cv api.Conversation
	e.want(201, "POST", base+"/conversations", fmt.Sprintf(`{"persona":%q,"message":"oi"}`, p.Slug), &cv)
	var m api.ConversationMessage
	e.want(201, "POST", base+"/conversations/"+cv.ID+"/messages", `{"message":"/new"}`, &m)
	if m.Marker != "context_reset" || m.Author.Type != "system" {
		t.Fatalf("marcador: %+v", m)
	}
	var list struct {
		Items []api.ConversationMessage `json:"items"`
	}
	e.want(200, "GET", base+"/conversations/"+cv.ID+"/messages", "", &list)
	marks := 0
	for _, x := range list.Items {
		if x.Marker == "context_reset" {
			marks++
		}
	}
	if marks != 1 {
		t.Fatalf("marcadores na lista: %d (%+v)", marks, list.Items)
	}
}

func TestAPIPersonaProposesInstructions(t *testing.T) {
	e := newAPIEnv(t)
	base := e.base()
	p := e.persona(e.co, "Ada", func(in *service.HirePersonaInput) { in.Instructions = "Seja breve." })
	hdr := e.personaToken(e.co, p)

	var g api.Gate
	status, out := e.call("POST", base+"/personas/ada/instructions/proposals", `{"instructions":"Seja breve e cite fontes.","reason":"fontes","event_id":"p1"}`, hdr)
	if status != 201 {
		t.Fatalf("propor: %d %s", status, out)
	}
	e.want(200, "GET", base+"/gates?kind=instruction&status=pending", "", nil)
	var gl struct {
		Items []api.Gate `json:"items"`
	}
	e.want(200, "GET", base+"/gates?kind=instruction", "", &gl)
	if len(gl.Items) != 1 || gl.Items[0].Kind != "instruction" || !strings.Contains(gl.Items[0].Body, "+ Seja breve e cite fontes.") {
		t.Fatalf("gates: %+v", gl.Items)
	}
	g = gl.Items[0]
	// um humano decide
	e.want(200, "POST", base+"/gates/"+g.ID+"/decide", `{"decision":"approved"}`, nil)
	var pe api.Persona
	e.want(200, "GET", base+"/personas/ada", "", &pe)
	if pe.InstructionRevision != 2 || pe.Instructions != "Seja breve e cite fontes." {
		t.Fatalf("persona: %+v", pe)
	}
	// a proposta de outra persona é recusada
	q := e.persona(e.co, "Bia", nil)
	if status, out := e.call("POST", base+"/personas/"+q.Slug+"/instructions/proposals", `{"instructions":"x"}`, hdr); status != http.StatusForbidden {
		t.Fatalf("persona propondo para outra: %d %s", status, out)
	}
}
