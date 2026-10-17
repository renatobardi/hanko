package service_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Anexos (ADR-0052): artefatos com class=attachment, ligados a task/comentário ou a conversa/mensagem.

func attachTo(t *testing.T, e *env, actor domain.Actor, in service.PutArtifactInput) service.ArtifactView {
	t.Helper()
	in.Class = domain.ClassAttachment
	return e.put(t, actor, in)
}

func TestAttachmentToTaskAndComment(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	tk := e.task(t, service.CreateTaskInput{Title: "com anexo"})
	other := e.task(t, service.CreateTaskInput{Title: "outra"})
	cm, err := e.svc.AddComment(ctx, domain.HumanActor, e.co.ID, tk.ID, service.AddCommentInput{Body: "veja o arquivo"})
	if err != nil {
		t.Fatal(err)
	}
	otherCm, err := e.svc.AddComment(ctx, domain.HumanActor, e.co.ID, other.ID, service.AddCommentInput{Body: "de outra task"})
	if err != nil {
		t.Fatal(err)
	}

	a := attachTo(t, e, domain.HumanActor, service.PutArtifactInput{Name: "notas.txt", Content: []byte("olá"), TaskID: &tk.ID})
	if a.Class != domain.ClassAttachment || a.Version != 1 || a.TaskKey != tk.Key || a.Mime != "text/plain" || a.CommentID != nil || a.Versions != 1 {
		t.Fatalf("anexo: %+v", a)
	}
	// o nome repetido ganha sufixo (não vira versão), mesmo com conteúdo igual
	b := attachTo(t, e, domain.HumanActor, service.PutArtifactInput{Name: "notas.txt", Content: []byte("olá"), TaskID: &tk.ID, CommentID: &cm.ID})
	c := attachTo(t, e, domain.HumanActor, service.PutArtifactInput{Name: "notas.txt", Content: []byte("outro"), TaskID: &tk.ID})
	if b.Name != "notas (2).txt" || c.Name != "notas (3).txt" || b.CommentID == nil || *b.CommentID != cm.ID || b.Version != 1 {
		t.Fatalf("sufixo: %q %q comment=%v", b.Name, c.Name, b.CommentID)
	}
	// em outra task o nome volta a ser livre; e um artefato do mesmo nome não se mistura com o anexo
	if o := attachTo(t, e, domain.HumanActor, service.PutArtifactInput{Name: "notas.txt", Content: []byte("x"), TaskID: &other.ID}); o.Name != "notas.txt" {
		t.Fatalf("outra task: %q", o.Name)
	}
	art := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "notas.txt", Content: []byte("olá"), TaskID: &tk.ID})
	if art.Class != domain.ClassArtifact || art.Version != 1 || art.ID == a.ID {
		t.Fatalf("artefato de mesmo nome: %+v", art)
	}

	// ligações inválidas
	_, err = e.svc.PutArtifact(ctx, domain.HumanActor, e.co.ID, service.PutArtifactInput{Name: "x.txt", Content: []byte("x"), Class: domain.ClassAttachment, TaskID: &tk.ID, CommentID: &otherCm.ID})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.PutArtifact(ctx, domain.HumanActor, e.co.ID, service.PutArtifactInput{Name: "x.txt", Content: []byte("x"), Class: domain.ClassAttachment})
	wantErr(t, err, domain.ErrInvalid) // sem alvo
	_, err = e.svc.PutArtifact(ctx, domain.HumanActor, e.co.ID, service.PutArtifactInput{Name: "x.txt", Content: []byte("x"), TaskID: &tk.ID, CommentID: &cm.ID})
	wantErr(t, err, domain.ErrInvalid) // vínculo de anexo num artefato
	_, err = e.svc.PutArtifact(ctx, domain.HumanActor, e.co.ID, service.PutArtifactInput{Name: "x.txt", Content: []byte("x"), Class: "outra", TaskID: &tk.ID})
	wantErr(t, err, domain.ErrInvalid)

	// listagens: o padrão são os artefatos; anexos pelo filtro de classe, de comentário e de task
	arts, _ := e.svc.ListArtifacts(ctx, e.co.ID, service.ArtifactFilter{TaskID: &tk.ID})
	if len(arts) != 1 || arts[0].ID != art.ID {
		t.Fatalf("lista padrão deveria ter só o artefato: %+v", arts)
	}
	atts, _ := e.svc.ListArtifacts(ctx, e.co.ID, service.ArtifactFilter{TaskID: &tk.ID, Class: domain.ClassAttachment})
	if len(atts) != 3 {
		t.Fatalf("anexos da task: %d", len(atts))
	}
	byCm, _ := e.svc.ListArtifacts(ctx, e.co.ID, service.ArtifactFilter{CommentID: &cm.ID, Class: domain.ClassAttachment})
	if len(byCm) != 1 || byCm[0].ID != b.ID {
		t.Fatalf("anexos do comentário: %+v", byCm)
	}
	all, _ := e.svc.ListArtifacts(ctx, e.co.ID, service.ArtifactFilter{TaskID: &tk.ID, AllClasses: true})
	if len(all) != 4 {
		t.Fatalf("todas as classes: %d", len(all))
	}

	// conteúdo íntegro e sem cadeia
	got, body, err := e.svc.ArtifactContent(ctx, e.co.ID, b.ID)
	if err != nil || got.Class != domain.ClassAttachment || !bytes.Equal(body, []byte("olá")) {
		t.Fatalf("conteúdo: %+v %q %v", got, body, err)
	}
	if vs, err := e.svc.ArtifactVersions(ctx, e.co.ID, b.ID); err != nil || len(vs) != 1 {
		t.Fatalf("versões do anexo: %+v %v", vs, err)
	}

	// o fato fica na atividade e excluir apaga só aquele anexo (a cota é liberada)
	before, _ := e.svc.GetArtifactUsage(ctx, e.co.ID)
	if err := e.svc.DeleteArtifact(ctx, domain.HumanActor, e.co.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	after, _ := e.svc.GetArtifactUsage(ctx, e.co.ID)
	if after.UsedBytes != before.UsedBytes-int64(len("olá")) {
		t.Fatalf("cota: %d -> %d", before.UsedBytes, after.UsedBytes)
	}
	if _, _, err := e.svc.ArtifactContent(ctx, e.co.ID, b.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("anexo apagado: %v", err)
	}
	if _, _, err := e.svc.ArtifactContent(ctx, e.co.ID, a.ID); err != nil {
		t.Fatalf("o outro anexo deve ficar: %v", err)
	}
	acts, _ := e.svc.ListActivity(ctx, e.co.ID, service.ActivityFilter{SubjectType: "artifact", Limit: 100})
	seen := map[string]int{}
	for _, x := range acts {
		seen[x.Verb]++
	}
	if seen["attachment.added"] != 4 || seen["attachment.deleted"] != 1 {
		t.Fatalf("atividade: %v", seen)
	}

	// apagar o comentário leva os anexos dele (cascata do banco)
	c2 := attachTo(t, e, domain.HumanActor, service.PutArtifactInput{Name: "p.txt", Content: []byte("p"), TaskID: &tk.ID, CommentID: &cm.ID})
	if _, err := e.svc.Pool.Exec(ctx, `DELETE FROM task_comments WHERE id = $1`, cm.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.ArtifactContent(ctx, e.co.ID, c2.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("anexo do comentário apagado: %v", err)
	}
}

func TestAttachmentSharesQuotaAndLimits(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.svc.ArtifactMaxBytes, e.svc.ArtifactQuotaBytes = 10, 20
	tk := e.task(t, service.CreateTaskInput{Title: "t"})
	in := func(n int) service.PutArtifactInput {
		return service.PutArtifactInput{Name: "a.bin", Mime: "application/octet-stream", Content: bytes.Repeat([]byte("x"), n), TaskID: &tk.ID, Class: domain.ClassAttachment}
	}
	if _, err := e.svc.PutArtifact(ctx, domain.HumanActor, e.co.ID, in(11)); err == nil {
		t.Fatal("arquivo acima do limite deveria ser recusado")
	} else {
		var le *service.ArtifactLimitError
		if !errors.As(err, &le) || le.Quota {
			t.Fatalf("limite por arquivo: %v", err)
		}
	}
	if _, err := e.svc.PutArtifact(ctx, domain.HumanActor, e.co.ID, in(10)); err != nil {
		t.Fatal(err)
	}
	// um artefato conta na mesma cota
	e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "b.bin", Mime: "application/octet-stream", Content: bytes.Repeat([]byte("y"), 10), TaskID: &tk.ID})
	_, err := e.svc.PutArtifact(ctx, domain.HumanActor, e.co.ID, in(1))
	var le *service.ArtifactLimitError
	if !errors.As(err, &le) || !le.Quota {
		t.Fatalf("cota compartilhada: %v", err)
	}
	// conteúdo vazio e nome inválido seguem as regras do artefato
	_, err = e.svc.PutArtifact(ctx, domain.HumanActor, e.co.ID, service.PutArtifactInput{Name: "a/b", Content: []byte("x"), TaskID: &tk.ID, Class: domain.ClassAttachment})
	wantErr(t, err, domain.ErrInvalid)
}

func TestAttachmentPersonaScope(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := e.persona(t, "Eng", nil)
	q := e.persona(t, "Outra", nil)
	mine := e.task(t, service.CreateTaskInput{Title: "minha", PersonaID: &p.ID})
	theirs := e.task(t, service.CreateTaskInput{Title: "dela", PersonaID: &q.ID})
	self := domain.PersonaActor(p.ID)

	a, err := e.svc.PutArtifact(ctx, self, e.co.ID, service.PutArtifactInput{Name: "log.txt", Content: []byte("ok"), TaskID: &mine.ID, Class: domain.ClassAttachment})
	if err != nil || a.PersonaID == nil || *a.PersonaID != p.ID || a.Author.Type != domain.ActorPersona {
		t.Fatalf("persona anexa à própria task: %+v %v", a, err)
	}
	_, err = e.svc.PutArtifact(ctx, self, e.co.ID, service.PutArtifactInput{Name: "log.txt", Content: []byte("ok"), TaskID: &theirs.ID, Class: domain.ClassAttachment})
	wantErr(t, err, domain.ErrNotAllowed)
}

func TestAttachmentInChatAndBriefing(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := e.persona(t, "Ada", nil)
	cv, err := e.svc.CreateConversation(ctx, domain.HumanActor, e.co.ID, service.CreateConversationInput{PersonaID: p.ID, Message: "olha o anexo"})
	if err != nil {
		t.Fatal(err)
	}
	msgs, _ := e.svc.ListConversationMessages(ctx, e.co.ID, cv.ID, 10)
	if len(msgs) == 0 {
		t.Fatal("sem mensagens")
	}
	att := attachTo(t, e, domain.HumanActor, service.PutArtifactInput{Name: "foto.png", Mime: "image/png", Content: []byte("\x89PNG\r\n"),
		ConversationID: &cv.ID, MessageID: &msgs[0].ID})
	if att.ConversationID == nil || *att.ConversationID != cv.ID || att.MessageID == nil || att.TaskKey != "" {
		t.Fatalf("anexo do chat: %+v", att)
	}
	// vínculo cruzado é recusado
	_, err = e.svc.PutArtifact(ctx, domain.HumanActor, e.co.ID, service.PutArtifactInput{Name: "x.txt", Content: []byte("x"), Class: domain.ClassAttachment,
		ConversationID: &cv.ID, CommentID: &msgs[0].ID})
	wantErr(t, err, domain.ErrInvalid)
	tk := e.task(t, service.CreateTaskInput{Title: "t"})
	_, err = e.svc.PutArtifact(ctx, domain.HumanActor, e.co.ID, service.PutArtifactInput{Name: "x.txt", Content: []byte("x"), Class: domain.ClassAttachment,
		TaskID: &tk.ID, ConversationID: &cv.ID})
	wantErr(t, err, domain.ErrInvalid)

	// a run de chat recebe o anexo da mensagem que ela recolheu, marcado como novo
	if len(e.jobs.execs) != 1 {
		t.Fatalf("execs = %d", len(e.jobs.execs))
	}
	rc, err := e.svc.LoadRunContext(ctx, e.co.ID, e.jobs.execs[0])
	if err != nil || len(rc.Attachments) != 1 || rc.Attachments[0].ID != att.ID || !rc.Attachments[0].New || rc.Attachments[0].Name != "foto.png" {
		t.Fatalf("anexos no contexto: %+v %v", rc.Attachments, err)
	}
	// a persona anexa na conversa só pelo token da própria run de chat
	_, err = e.svc.PutArtifact(ctx, domain.PersonaActor(p.ID), e.co.ID, service.PutArtifactInput{Name: "r.md", Content: []byte("# r"), Class: domain.ClassAttachment, ConversationID: &cv.ID})
	wantErr(t, err, domain.ErrNotAllowed)
	run := e.jobs.execs[0]
	own, err := e.svc.PutArtifact(ctx, domain.PersonaActor(p.ID), e.co.ID, service.PutArtifactInput{Name: "r.md", Content: []byte("# r"), Class: domain.ClassAttachment, RunID: &run})
	if err != nil || own.ConversationID == nil || *own.ConversationID != cv.ID {
		t.Fatalf("persona anexa na própria run de chat: %+v %v", own, err)
	}
}

func TestTaskRunContextListsAttachments(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := e.persona(t, "Eng", nil)
	tk := e.task(t, service.CreateTaskInput{Title: "t", PersonaID: &p.ID})
	cm, _ := e.svc.AddComment(ctx, domain.HumanActor, e.co.ID, tk.ID, service.AddCommentInput{Body: "segue"})
	attachTo(t, e, domain.HumanActor, service.PutArtifactInput{Name: "spec.pdf", Content: []byte("%PDF-1.4"), TaskID: &tk.ID, CommentID: &cm.ID})
	run, err := e.svc.CheckoutRun(ctx, wakeOf(p, tk, domain.TriggerAssign))
	if err != nil {
		t.Fatal(err)
	}
	rc, err := e.svc.LoadRunContext(ctx, e.co.ID, run.ID)
	if err != nil || len(rc.Attachments) != 1 {
		t.Fatalf("contexto: %+v %v", rc.Attachments, err)
	}
	a := rc.Attachments[0]
	if a.Name != "spec.pdf" || a.Mime != "application/pdf" || a.CommentID == nil || *a.CommentID != cm.ID || a.Author.Type != domain.ActorHuman {
		t.Fatalf("anexo: %+v", a)
	}
}

func TestExportLeavesAttachmentsOut(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	tk := e.task(t, service.CreateTaskInput{Title: "t"})
	e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "plano.md", Content: []byte("# p"), TaskID: &tk.ID})
	attachTo(t, e, domain.HumanActor, service.PutArtifactInput{Name: "anexo.txt", Content: []byte("a"), TaskID: &tk.ID})
	doc, err := e.svc.ExportCompany(ctx, e.co.ID, service.ExportOptions{WithArtifacts: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Artifacts) != 1 || !strings.HasPrefix(doc.Artifacts[0].Name, "plano") {
		t.Fatalf("export: %+v", doc.Artifacts)
	}
}
