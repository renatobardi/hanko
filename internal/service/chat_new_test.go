package service_test

import (
	"context"
	"testing"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// /new no chat (ADR-0052): um marcador de contexto; o histórico do prompt e a retomada de sessão recomeçam depois dele.

// finishChatRun leva a run de chat até done, com a sessão dada e uma resposta.
func finishChatRun(t *testing.T, e *env, runID domain.ID, session, reply string) {
	t.Helper()
	ctx := context.Background()
	if _, err := e.svc.MarkRunStarted(ctx, e.co.ID, runID, "tr", ""); err != nil {
		t.Fatalf("start %s: %v", runID, err)
	}
	if _, err := e.svc.FinishRun(ctx, e.co.ID, runID, service.FinishRunInput{Status: domain.RunDone,
		Result: adapter.Result{SessionRef: session, Summary: reply, Model: "m1"}}); err != nil {
		t.Fatalf("finish %s: %v", runID, err)
	}
}

func lastExec(e *env) domain.ID { return e.jobs.execs[len(e.jobs.execs)-1] }

func TestChatNewResetsContextAndSession(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := e.persona(t, "Ada", nil)
	cv, err := e.svc.CreateConversation(ctx, domain.HumanActor, e.co.ID, service.CreateConversationInput{PersonaID: p.ID, Message: "primeira"})
	if err != nil {
		t.Fatal(err)
	}
	finishChatRun(t, e, lastExec(e), "sess-1", "resposta 1")

	// sem /new a segunda mensagem retoma a sessão e vê o histórico
	e.say(t, cv.ID, "segunda")
	rc, err := e.svc.LoadRunContext(ctx, e.co.ID, lastExec(e))
	if err != nil || rc.Chat == nil || rc.Chat.ResumeSession != "sess-1" || len(rc.Chat.History) < 2 {
		t.Fatalf("antes do /new: %+v %v", rc.Chat, err)
	}
	finishChatRun(t, e, lastExec(e), "sess-2", "resposta 2")
	// "/newsletter" não é o comando: é uma mensagem comum (e abre uma run)
	if m := e.say(t, cv.ID, "/newsletter de ontem"); m.Marker != "" || m.Kind != domain.MessageHuman {
		t.Fatalf("/newsletter virou comando: %+v", m)
	}
	finishChatRun(t, e, lastExec(e), "sess-3", "resposta 3")
	runs := len(e.jobs.execs)

	// "/new" sozinho: só o marcador, nenhuma run nova
	mk := e.say(t, cv.ID, "/new")
	if mk.Marker != "context_reset" || mk.Kind != domain.MessageSystem || len(e.jobs.execs) != runs {
		t.Fatalf("marcador: %+v execs %d -> %d", mk, runs, len(e.jobs.execs))
	}

	// depois do marcador: histórico vazio de antes e nenhuma sessão retomada
	e.say(t, cv.ID, "recomeçando")
	rc, err = e.svc.LoadRunContext(ctx, e.co.ID, lastExec(e))
	if err != nil || rc.Chat.ResumeSession != "" {
		t.Fatalf("a sessão não deveria ser retomada depois do /new: %+v %v", rc.Chat, err)
	}
	for _, m := range rc.Chat.History {
		if m.Body == "primeira" || m.Body == "segunda" || m.Body == "resposta 1" {
			t.Fatalf("histórico de antes do /new vazou: %q", m.Body)
		}
	}
	if len(rc.Chat.New) != 1 || rc.Chat.New[0].Body != "recomeçando" {
		t.Fatalf("mensagens novas: %+v", rc.Chat.New)
	}
	finishChatRun(t, e, lastExec(e), "sess-4", "resposta 4")

	// a sessão volta a ser retomada para as runs seguintes (a anterior já é posterior ao marcador)
	e.say(t, cv.ID, "mais uma")
	rc, _ = e.svc.LoadRunContext(ctx, e.co.ID, lastExec(e))
	if rc.Chat.ResumeSession != "sess-4" {
		t.Fatalf("retomada depois do marcador: %q", rc.Chat.ResumeSession)
	}
	finishChatRun(t, e, lastExec(e), "sess-5", "resposta 5")

	// "/new texto": marcador e a mensagem juntos, com a run sem a sessão anterior
	before := len(e.jobs.execs)
	m := e.say(t, cv.ID, "/new  e agora outro assunto")
	if m.Kind != domain.MessageHuman || m.Body != "e agora outro assunto" || len(e.jobs.execs) != before+1 {
		t.Fatalf("/new com texto: %+v execs %d", m, len(e.jobs.execs))
	}
	rc, _ = e.svc.LoadRunContext(ctx, e.co.ID, lastExec(e))
	if rc.Chat.ResumeSession != "" || len(rc.Chat.New) != 1 {
		t.Fatalf("contexto do /new com texto: %+v", rc.Chat)
	}

	// o histórico completo continua na conversa, com os dois marcadores
	all, _ := e.svc.ListConversationMessages(ctx, e.co.ID, cv.ID, 0)
	marks, humans := 0, 0
	for _, x := range all {
		if x.Marker == "context_reset" {
			marks++
		}
		if x.Kind == domain.MessageHuman {
			humans++
		}
	}
	if marks != 2 || humans < 6 {
		t.Fatalf("a conversa deve manter tudo: marcadores=%d humanas=%d", marks, humans)
	}
	// o fato fica na atividade
	acts, _ := e.svc.ListActivity(ctx, e.co.ID, service.ActivityFilter{SubjectType: "conversation", SubjectID: &cv.ID, Limit: 100})
	n := 0
	for _, a := range acts {
		if a.Verb == "conversation.context_reset" {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("fatos conversation.context_reset = %d", n)
	}
}

func TestChatNewOnArchivedAndRoom(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := e.persona(t, "Ada", nil)
	cv, err := e.svc.CreateConversation(ctx, domain.HumanActor, e.co.ID, service.CreateConversationInput{PersonaID: p.ID, Message: "oi"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SetConversationStatus(ctx, domain.HumanActor, e.co.ID, cv.ID, domain.ConversationArchived); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SendMessage(ctx, domain.HumanActor, e.co.ID, cv.ID, "/new"); err == nil {
		t.Fatal("conversa arquivada não aceita /new")
	}
	// persona não envia /new
	if _, err := e.svc.SendMessage(ctx, domain.PersonaActor(p.ID), e.co.ID, cv.ID, "/new"); err == nil {
		t.Fatal("só humano envia mensagem")
	}

	// sala: o marcador vale para a conversa; as sessões por persona recomeçam
	re, ps, room := roomFixture(t, nil)
	re.say(t, room.ID, "@ana primeira da sala")
	runs := re.activeRuns(t, room.ID)
	ana, ok := runs[ps[0].ID]
	if !ok {
		t.Fatalf("Ana deveria ter uma run: %v", runs)
	}
	finishChatRun(t, re, ana.ID, "sala-1", "ok")
	mk := re.say(t, room.ID, "/new")
	if mk.Marker != "context_reset" {
		t.Fatalf("marcador na sala: %+v", mk)
	}
	re.say(t, room.ID, "@ana recomeço")
	ana2 := re.activeRuns(t, room.ID)[ps[0].ID]
	rc, err := re.svc.LoadRunContext(ctx, re.co.ID, ana2.ID)
	if err != nil || rc.Chat == nil || rc.Chat.ResumeSession != "" {
		t.Fatalf("sessão da sala depois do /new: %+v %v", rc.Chat, err)
	}
	for _, m := range rc.Chat.History {
		if m.Body == "@ana primeira da sala" {
			t.Fatalf("histórico de antes do /new vazou na sala: %q", m.Body)
		}
	}
}
