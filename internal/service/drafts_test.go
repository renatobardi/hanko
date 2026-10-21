package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Rascunhos no servidor (ADR-0052).

// asUser devolve um contexto com a identidade de um usuário com o papel dado na empresa.
func asUser(t *testing.T, e *env, email string, role domain.MemberRole) context.Context {
	t.Helper()
	u := mkUser(t, e, email, domain.RoleMember)
	join(t, e, u, role)
	return as(u)
}

func TestDraftTaskSaveGetClearOnComment(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	tk := e.task(t, service.CreateTaskInput{Title: "t"})

	d, err := e.svc.GetDraft(ctx, e.co.ID, domain.DraftTask, tk.ID)
	if err != nil || d.Exists() || d.Body != "" {
		t.Fatalf("sem rascunho: %+v %v", d, err)
	}
	d, err = e.svc.SaveDraft(ctx, e.co.ID, domain.DraftTask, tk.ID, "comentário pela metade")
	if err != nil || !d.Exists() || d.Body != "comentário pela metade" {
		t.Fatalf("salvar: %+v %v", d, err)
	}
	// regravar troca o texto (um rascunho só por alvo)
	if _, err = e.svc.SaveDraft(ctx, e.co.ID, domain.DraftTask, tk.ID, "comentário quase pronto"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := e.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM drafts`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("linhas: %d %v", n, err)
	}
	d, _ = e.svc.GetDraft(ctx, e.co.ID, domain.DraftTask, tk.ID)
	if d.Body != "comentário quase pronto" {
		t.Fatalf("lido: %+v", d)
	}

	// enviar o comentário limpa o rascunho na mesma transação
	if _, err := e.svc.AddComment(ctx, domain.HumanActor, e.co.ID, tk.ID, service.AddCommentInput{Body: "comentário quase pronto"}); err != nil {
		t.Fatal(err)
	}
	if d, _ = e.svc.GetDraft(ctx, e.co.ID, domain.DraftTask, tk.ID); d.Exists() {
		t.Fatalf("o envio deveria limpar o rascunho: %+v", d)
	}
	// o comentário de uma persona não mexe no rascunho do humano
	if _, err := e.svc.SaveDraft(ctx, e.co.ID, domain.DraftTask, tk.ID, "outro"); err != nil {
		t.Fatal(err)
	}
	p := e.persona(t, "Eng", nil)
	if _, err := e.svc.AddComment(ctx, domain.PersonaActor(p.ID), e.co.ID, tk.ID, service.AddCommentInput{Body: "resposta"}); err != nil {
		t.Fatal(err)
	}
	if d, _ = e.svc.GetDraft(ctx, e.co.ID, domain.DraftTask, tk.ID); !d.Exists() {
		t.Fatal("comentário da persona não deveria limpar o rascunho do humano")
	}

	// texto em branco apaga; apagar de novo não é erro
	if d, err = e.svc.SaveDraft(ctx, e.co.ID, domain.DraftTask, tk.ID, "  \n "); err != nil || d.Exists() {
		t.Fatalf("em branco: %+v %v", d, err)
	}
	if err := e.svc.DeleteDraft(ctx, e.co.ID, domain.DraftTask, tk.ID); err != nil {
		t.Fatal(err)
	}

	// alvo inexistente e entradas inválidas
	_, err = e.svc.SaveDraft(ctx, e.co.ID, domain.DraftTask, domain.NewID(), "x")
	wantErr(t, err, domain.ErrNotFound)
	_, err = e.svc.SaveDraft(ctx, e.co.ID, "outro", tk.ID, "x")
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.SaveDraft(ctx, e.co.ID, domain.DraftTask, tk.ID, strings.Repeat("a", domain.MaxDraftRunes+1))
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.SaveDraft(ctx, e.co.ID, domain.DraftTask, tk.ID, "ab\xffcd")
	wantErr(t, err, domain.ErrInvalid)
}

func TestDraftsArePerUserAndPerCompany(t *testing.T) {
	e := newEnv(t)
	tk := e.task(t, service.CreateTaskInput{Title: "t"})
	ana := asUser(t, e, "ana@acme.test", domain.MemberMember)
	beto := asUser(t, e, "beto@acme.test", domain.MemberMember)
	viewer := asUser(t, e, "vera@acme.test", domain.MemberViewer)

	if _, err := e.svc.SaveDraft(ana, e.co.ID, domain.DraftTask, tk.ID, "da Ana"); err != nil {
		t.Fatal(err)
	}
	if d, _ := e.svc.GetDraft(beto, e.co.ID, domain.DraftTask, tk.ID); d.Exists() {
		t.Fatalf("o rascunho da Ana vazou para o Beto: %+v", d)
	}
	if _, err := e.svc.SaveDraft(beto, e.co.ID, domain.DraftTask, tk.ID, "do Beto"); err != nil {
		t.Fatal(err)
	}
	if d, _ := e.svc.GetDraft(ana, e.co.ID, domain.DraftTask, tk.ID); d.Body != "da Ana" {
		t.Fatalf("Ana: %+v", d)
	}
	// o Beto enviando o comentário limpa só o dele
	if _, err := e.svc.AddComment(beto, domain.HumanActor, e.co.ID, tk.ID, service.AddCommentInput{Body: "do Beto"}); err != nil {
		t.Fatal(err)
	}
	if d, _ := e.svc.GetDraft(beto, e.co.ID, domain.DraftTask, tk.ID); d.Exists() {
		t.Fatal("o envio do Beto deveria limpar o rascunho dele")
	}
	if d, _ := e.svc.GetDraft(ana, e.co.ID, domain.DraftTask, tk.ID); d.Body != "da Ana" {
		t.Fatalf("o rascunho da Ana deve ficar: %+v", d)
	}
	// o modo trusted (sem usuário) tem o dono "local", separado dos usuários
	if d, _ := e.svc.GetDraft(context.Background(), e.co.ID, domain.DraftTask, tk.ID); d.Exists() {
		t.Fatalf("local não vê o da Ana: %+v", d)
	}
	// viewer não escreve nem lê rascunho (não comenta)
	_, err := e.svc.SaveDraft(viewer, e.co.ID, domain.DraftTask, tk.ID, "x")
	wantErr(t, err, domain.ErrForbidden)

	// outra empresa: o mesmo alvo não existe nela
	other, err := e.svc.CreateCompany(context.Background(), domain.HumanActor, service.CreateCompanyInput{Name: "Outra"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.SaveDraft(context.Background(), other.ID, domain.DraftTask, tk.ID, "x")
	wantErr(t, err, domain.ErrNotFound)

	// apagar a task leva os rascunhos
	if _, err := e.svc.Pool.Exec(context.Background(), `DELETE FROM tasks WHERE id = $1`, tk.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := e.svc.Pool.QueryRow(context.Background(), `SELECT count(*) FROM drafts`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rascunhos após apagar a task: %d %v", n, err)
	}
}

func TestDraftConversationClearedOnSend(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.persona(t, "Ada", nil)
	cv, err := e.svc.CreateConversation(ctx, domain.HumanActor, e.co.ID, service.CreateConversationInput{PersonaID: p.ID, Message: "oi"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SaveDraft(ctx, e.co.ID, domain.DraftConversation, cv.ID, "segunda mensagem"); err != nil {
		t.Fatal(err)
	}
	if d, _ := e.svc.GetDraft(ctx, e.co.ID, domain.DraftConversation, cv.ID); d.Body != "segunda mensagem" {
		t.Fatalf("rascunho da conversa: %+v", d)
	}
	if _, err := e.svc.SendMessage(ctx, domain.HumanActor, e.co.ID, cv.ID, "segunda mensagem"); err != nil {
		t.Fatal(err)
	}
	if d, _ := e.svc.GetDraft(ctx, e.co.ID, domain.DraftConversation, cv.ID); d.Exists() {
		t.Fatalf("enviar a mensagem deveria limpar o rascunho: %+v", d)
	}
	// uma mensagem recusada (persona pausada, sem run ativa) não limpa o rascunho
	// cancelar a run ativa abre a seguinte com as mensagens em espera (ADR-0039); cancela-se também essa
	for i := 0; i < 3; i++ {
		v, err := e.svc.GetConversation(ctx, e.co.ID, cv.ID)
		if err != nil {
			t.Fatal(err)
		}
		if v.ActiveRunID == nil {
			break
		}
		if _, err := e.svc.CancelRun(ctx, domain.HumanActor, e.co.ID, *v.ActiveRunID); err != nil {
			t.Fatal(err)
		}
	}
	paused := domain.PersonaPaused
	if _, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, p.ID, service.UpdatePersonaInput{Status: &paused}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SaveDraft(ctx, e.co.ID, domain.DraftConversation, cv.ID, "terceira"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SendMessage(ctx, domain.HumanActor, e.co.ID, cv.ID, "terceira"); err == nil {
		t.Fatal("persona pausada deveria recusar a mensagem")
	}
	if d, _ := e.svc.GetDraft(ctx, e.co.ID, domain.DraftConversation, cv.ID); d.Body != "terceira" {
		t.Fatalf("a mensagem recusada não deve limpar o rascunho: %+v", d)
	}
}
