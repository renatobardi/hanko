//go:build integration

package ui_test

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Revisões das instruções, anexos, rascunhos e /new na UI contra o service e o banco reais (ADR-0052).

func (u *realUI) put(path string, v url.Values) *httptest.ResponseRecorder {
	u.t.Helper()
	rq := httptest.NewRequest("PUT", path, strings.NewReader(v.Encode()))
	rq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rq.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	u.h.ServeHTTP(rec, rq)
	return rec
}

// multi envia um multipart (htmx) com campos de texto e vários arquivos no campo "files".
func (u *realUI) multi(path string, fields map[string]string, files map[string]string) *httptest.ResponseRecorder {
	u.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			u.t.Fatal(err)
		}
	}
	for name, content := range files {
		fw, err := mw.CreateFormFile("files", name)
		if err != nil {
			u.t.Fatal(err)
		}
		_, _ = fw.Write([]byte(content))
	}
	if err := mw.Close(); err != nil {
		u.t.Fatal(err)
	}
	rq := httptest.NewRequest("POST", path, &buf)
	rq.Header.Set("Content-Type", mw.FormDataContentType())
	rq.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	u.h.ServeHTTP(rec, rq)
	return rec
}

func TestRealInstructionsPage(t *testing.T) {
	u := newRealUI(t)
	ctx := context.Background()
	p, err := u.svc.HirePersona(ctx, domain.HumanActor, u.co.ID, service.HirePersonaInput{Name: "Ada", Agent: "fake", Instructions: "Seja breve."})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, u.get("/c/acme/personas/ada"), 200, "revisão 1 · histórico e edição", "/c/acme/personas/ada/instrucoes")
	mustContain(t, u.get("/c/acme/personas/ada/instrucoes"), 200, "Editar instruções", "Seja breve.", "contratação", "r1")

	// salvar cria a revisão 2 com o motivo
	rec := u.form("/c/acme/personas/ada/instrucoes", url.Values{"instructions": {"Seja detalhista."}, "reason": {"ficou curto"}})
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "ok=salva") {
		t.Fatalf("salvar: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	mustContain(t, u.get("/c/acme/personas/ada/instrucoes?ok=salva"), 200, "uma revisão nova foi criada", "ficou curto", "edição", "atual")
	// mesmo texto: nenhuma revisão nova
	rec = u.form("/c/acme/personas/ada/instrucoes", url.Values{"instructions": {"Seja detalhista."}})
	if !strings.Contains(rec.Header().Get("Location"), "ok=igual") {
		t.Fatalf("mesmo texto: %s", rec.Header().Get("Location"))
	}
	// motivo longo demais volta o formulário com o erro e o que foi digitado
	rec = u.form("/c/acme/personas/ada/instrucoes", url.Values{"instructions": {"X novo"}, "reason": {strings.Repeat("a", 501)}})
	mustContain(t, rec, 422, "X novo", "Dados inválidos")

	// ver e comparar
	mustContain(t, u.get("/c/acme/personas/ada/instrucoes?ver=1"), 200, "Seja breve.", `id="revisao"`)
	mustContain(t, u.get("/c/acme/personas/ada/instrucoes?de=1&para=2"), 200, "Seja breve.", "Seja detalhista.", "+1")

	// restaurar a 1 cria a 3
	rec = u.form("/c/acme/personas/ada/instrucoes/restaurar", url.Values{"revision": {"1"}})
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "ok=restaurada") {
		t.Fatalf("restaurar: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if got, _ := u.svc.GetPersona(ctx, u.co.ID, p.ID.String()); got.InstructionRevision != 3 || got.Instructions != "Seja breve." {
		t.Fatalf("persona: %+v", got)
	}
	// proposta pendente aparece na página
	g, err := u.svc.ProposePersonaInstructions(ctx, domain.PersonaActor(p.ID), u.co.ID, p.ID, service.ProposeInstructionsInput{Body: "Outra", Reason: "teste"})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, u.get("/c/acme/personas/ada/instrucoes"), 200, "Proposta da persona esperando decisão", "/c/acme/gates/"+g.ID.String())
	mustContain(t, u.get("/c/acme/gates/"+g.ID.String()), 200, "propõe mudar", "Outra")

	// a run mostra a revisão com que rodou
	tk, _ := u.svc.CreateTask(ctx, domain.HumanActor, u.co.ID, service.CreateTaskInput{Title: "t", PersonaID: &p.ID})
	run, err := u.svc.CheckoutRun(ctx, service.Wake{CompanyID: u.co.ID, PersonaID: p.ID, TaskID: tk.ID, Trigger: domain.TriggerAssign})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := u.svc.MarkRunStarted(ctx, u.co.ID, run.ID, "tr", ""); err != nil {
		t.Fatal(err)
	}
	if err := u.svc.RecordRunInstructions(ctx, u.co.ID, run.ID, 3); err != nil {
		t.Fatal(err)
	}
	mustContain(t, u.get("/c/acme/execucoes/"+run.ID.String()), 200, "r3 · ", "/instrucoes?ver=3")
}

func TestRealTaskAttachmentsDraftsAndComment(t *testing.T) {
	u := newRealUI(t)
	ctx := context.Background()
	tk, err := u.svc.CreateTask(ctx, domain.HumanActor, u.co.ID, service.CreateTaskInput{Title: "Com anexos", Status: domain.TaskTodo, Priority: domain.PriorityMedium})
	if err != nil {
		t.Fatal(err)
	}
	page := "/c/acme/tasks/" + tk.Key
	mustContain(t, u.get(page), 200, "Anexos", "Nenhum anexo ainda.", `name="files"`, `enctype="multipart/form-data"`, `data-dropzone`,
		"/c/acme/tasks/"+tk.Key+"/anexos", "/c/acme/tasks/"+tk.Key+"/rascunho", "Até 16 MiB por arquivo")

	// arrastar e soltar: o cartão envia na hora e devolve o cartão atualizado
	rec := u.multi("/c/acme/tasks/"+tk.Key+"/anexos", nil, map[string]string{"foto.txt": "olá", "notas.md": "# notas"})
	mustContain(t, rec, 200, "foto.txt", "notas.md", `id="task-attachments"`)
	list, _ := u.svc.ListArtifacts(ctx, u.co.ID, service.ArtifactFilter{TaskID: &tk.ID, Class: domain.ClassAttachment})
	if len(list) != 2 {
		t.Fatalf("anexos: %d", len(list))
	}
	// o conteúdo sai pela rota segura de artefatos
	var fotoID string
	for _, a := range list {
		if a.Name == "foto.txt" {
			fotoID = a.ID.String()
		}
	}
	c := u.get("/c/acme/artefatos/" + fotoID + "/conteudo")
	if c.Code != 200 || c.Body.String() != "olá" || c.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("conteúdo: %d %q %v", c.Code, c.Body.String(), c.Header())
	}
	// nenhum arquivo: erro; arquivo grande demais: erro explicando o limite
	mustContain(t, u.multi("/c/acme/tasks/"+tk.Key+"/anexos", nil, nil), 422, "escolha ao menos um arquivo")

	// rascunho: salvar pelo autosave, reabrir a página (outro dispositivo) e enviar o comentário limpa
	rec = u.put("/c/acme/tasks/"+tk.Key+"/rascunho", url.Values{"body": {"comentário pela metade"}})
	mustContain(t, rec, 200, "Rascunho salvo às")
	mustContain(t, u.get(page), 200, "comentário pela metade")
	rec = u.multi("/c/acme/tasks/"+tk.Key+"/comentarios", map[string]string{"body": "comentário pronto"}, map[string]string{"ref.csv": "a,b\n1,2\n"})
	mustContain(t, rec, 200, "comentário pronto", "ref.csv", `id="task-attachments"`)
	if d, _ := u.svc.GetDraft(ctx, u.co.ID, domain.DraftTask, tk.ID); d.Exists() {
		t.Fatalf("o envio deveria limpar o rascunho: %+v", d)
	}
	pg := u.get(page).Body.String()
	if strings.Contains(pg, "comentário pela metade") {
		t.Error("o rascunho apagado ainda aparece na página")
	}
	// o anexo do comentário pertence a ele
	cms, _ := u.svc.ListComments(ctx, u.co.ID, tk.ID)
	withComment, _ := u.svc.ListArtifacts(ctx, u.co.ID, service.ArtifactFilter{CommentID: &cms[len(cms)-1].ID, Class: domain.ClassAttachment})
	if len(withComment) != 1 || withComment[0].Name != "ref.csv" {
		t.Fatalf("anexo do comentário: %+v", withComment)
	}
	// comentário sem texto nem arquivo: erro
	mustContain(t, u.multi("/c/acme/tasks/"+tk.Key+"/comentarios", map[string]string{"body": "  "}, nil), 422, "escreva algo antes de enviar")
	// só arquivos, sem texto: viram anexos da task, sem comentário novo
	before := len(cms)
	mustContain(t, u.multi("/c/acme/tasks/"+tk.Key+"/comentarios", map[string]string{"body": ""}, map[string]string{"solto.txt": "x"}), 200)
	after, _ := u.svc.ListComments(ctx, u.co.ID, tk.ID)
	if len(after) != before {
		t.Fatalf("só arquivos não deveria criar comentário (%d -> %d)", before, len(after))
	}

	// apagar o anexo (o dono local é admin)
	rec = u.do("POST", "/c/acme/tasks/"+tk.Key+"/anexos/"+fotoID+"/excluir", nil, "")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("apagar: %d", rec.Code)
	}
	if _, _, err := u.svc.ArtifactContent(ctx, u.co.ID, mustID(t, fotoID)); err == nil {
		t.Fatal("o anexo deveria ter sido apagado")
	}
	// apagar por outra task é recusado
	other, _ := u.svc.CreateTask(ctx, domain.HumanActor, u.co.ID, service.CreateTaskInput{Title: "outra", Status: domain.TaskTodo, Priority: domain.PriorityMedium})
	rest := list[0].ID
	if list[0].Name == "foto.txt" {
		rest = list[1].ID
	}
	if r := u.do("POST", "/c/acme/tasks/"+other.Key+"/anexos/"+rest.String()+"/excluir", nil, ""); r.Code != http.StatusNotFound {
		t.Fatalf("apagar pela task errada: %d", r.Code)
	}
}

func mustID(t *testing.T, s string) domain.ID {
	t.Helper()
	id, err := domain.ParseID(s)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestRealChatAttachmentsDraftAndNew(t *testing.T) {
	u := newRealUI(t)
	ctx := context.Background()
	p, err := u.svc.HirePersona(ctx, domain.HumanActor, u.co.ID, service.HirePersonaInput{Name: "Ada", Agent: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	cv, err := u.svc.CreateConversation(ctx, domain.HumanActor, u.co.ID, service.CreateConversationInput{PersonaID: p.ID, Message: "oi"})
	if err != nil {
		t.Fatal(err)
	}
	page := "/c/acme/chat/" + cv.ID.String()
	mustContain(t, u.get(page), 200, `name="files"`, `data-dropzone`, "/rascunho", "/new reinicia o contexto")

	// rascunho da conversa
	mustContain(t, u.put(page+"/rascunho", url.Values{"message": {"mensagem pela metade"}}), 200, "Rascunho salvo")
	mustContain(t, u.get(page), 200, "mensagem pela metade")

	// mensagem com anexo: o anexo aparece sob a mensagem e o rascunho some
	rec := u.multi(page+"/mensagens", map[string]string{"message": "olha o arquivo"}, map[string]string{"spec.txt": "especificação"})
	mustContain(t, rec, 200, "olha o arquivo", "spec.txt")
	if d, _ := u.svc.GetDraft(ctx, u.co.ID, domain.DraftConversation, cv.ID); d.Exists() {
		t.Fatal("o envio deveria limpar o rascunho da conversa")
	}
	atts, _ := u.svc.ListArtifacts(ctx, u.co.ID, service.ArtifactFilter{ConversationID: &cv.ID, Class: domain.ClassAttachment})
	if len(atts) != 1 || atts[0].MessageID == nil {
		t.Fatalf("anexo da conversa: %+v", atts)
	}
	// só arquivo, sem texto: a mensagem curta
	rec = u.multi(page+"/mensagens", map[string]string{"message": ""}, map[string]string{"so-arquivo.txt": "x"})
	mustContain(t, rec, 200, "Enviei 1 anexo(s).", "so-arquivo.txt")
	mustContain(t, u.multi(page+"/mensagens", map[string]string{"message": ""}, nil), 422, "escreva algo antes de enviar")

	// /new vira um divisor "Contexto reiniciado"
	rec = u.multi(page+"/mensagens", map[string]string{"message": "/new"}, nil)
	mustContain(t, rec, 200, "Contexto reiniciado", "context-reset")
}

func TestRealRoomAttachmentsAndNew(t *testing.T) {
	u := newRealUI(t)
	ctx := context.Background()
	a, _ := u.svc.HirePersona(ctx, domain.HumanActor, u.co.ID, service.HirePersonaInput{Name: "Ana", Agent: "fake"})
	b, _ := u.svc.HirePersona(ctx, domain.HumanActor, u.co.ID, service.HirePersonaInput{Name: "Beto", Agent: "fake"})
	room, err := u.svc.CreateRoom(ctx, domain.HumanActor, u.co.ID, service.CreateRoomInput{Title: "Planejamento", PersonaIDs: []domain.ID{a.ID, b.ID}})
	if err != nil {
		t.Fatal(err)
	}
	page := "/c/acme/sala/" + room.ID.String()
	mustContain(t, u.get(page), 200, `name="files"`, `data-dropzone`, "/rascunho", "/new reinicia o contexto")
	mustContain(t, u.put(page+"/rascunho", url.Values{"message": {"@ana rascunho"}}), 200, "Rascunho salvo")
	rec := u.multi(page+"/mensagens", map[string]string{"message": "@ana veja isto"}, map[string]string{"plano.md": "# plano"})
	mustContain(t, rec, 200, "veja isto", "plano.md")
	rec = u.multi(page+"/mensagens", map[string]string{"message": "/new"}, nil)
	mustContain(t, rec, 200, "Contexto reiniciado")
	if d, _ := u.svc.GetDraft(ctx, u.co.ID, domain.DraftConversation, room.ID); d.Exists() {
		t.Fatal("o envio deveria limpar o rascunho da sala")
	}
}
