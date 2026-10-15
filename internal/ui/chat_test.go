package ui

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

// chatEnv is the sample company with one open conversation with Ada, already answered once.
func chatEnv(t *testing.T) (*env, service.ConversationView) {
	t.Helper()
	f := uitest.Sample()
	e := newEnv(t, f)
	ada := uitest.ID("persona:ada")
	cv, err := f.CreateConversation(t.Context(), domain.HumanActor, f.Companies[0].ID, service.CreateConversationInput{PersonaID: ada, Message: "Qual o plano de hoje?"})
	if err != nil {
		t.Fatal(err)
	}
	run := uitest.ID("run:chat")
	f.PersonaReplies(cv.ID, &run, "Hoje: **revisar** o gate do NAS.")
	cv, err = f.GetConversation(t.Context(), f.Companies[0].ID, cv.ID)
	if err != nil {
		t.Fatal(err)
	}
	return e, cv
}

func TestChatNavigationAndList(t *testing.T) {
	e, cv := chatEnv(t)
	rec := e.get(t, "/c/hanko/chat")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Nova conversa", "Qual o plano de hoje?", `href="/c/hanko/chat/`+cv.ID.String()+`"`, `name="persona"`, `name="message"`,
		`href="/c/hanko/chat"`, "Abertas", "Arquivadas")
	wantNotContains(t, rec, "Em breve")

	// archived tab
	wantContains(t, e.get(t, "/c/hanko/chat?s=arquivadas"), "Nenhuma conversa aqui.")
	// the persona is preselected from the link on the persona page
	wantContains(t, e.get(t, "/c/hanko/chat?persona=ada"), `value="`+uitest.ID("persona:ada").String()+`" selected`)

	// the persona page lists its conversations and offers a new one
	rec = e.get(t, "/c/hanko/personas/ada")
	wantContains(t, rec, "Conversas", "Qual o plano de hoje?", `/c/hanko/chat?persona=ada`)
	wantContains(t, e.get(t, "/c/hanko/personas/hana"), "Nenhuma conversa com esta persona.")

	wantContains(t, newEnv(t, uitest.Empty()).get(t, "/c/hanko/chat"), "Nenhuma persona para conversar.")
}

func TestChatThreadPage(t *testing.T) {
	e, cv := chatEnv(t)
	rec := e.get(t, "/c/hanko/chat/"+cv.ID.String())
	wantStatus(t, rec, http.StatusOK)
	// markdown is rendered, the reply links to its run, the human bubble and the composer are there
	wantContains(t, rec, "Qual o plano de hoje?", "<strong>revisar</strong>", `/c/hanko/execucoes/`+uitest.ID("run:chat").String(),
		`id="chat-thread"`, `hx-post="/c/hanko/chat/`+cv.ID.String()+`/mensagens"`, "Arquivar", "Ada")
	wantNotContains(t, rec, "pensando…", `hx-trigger="every 2s"`)

	wantStatus(t, e.get(t, "/c/hanko/chat/nao-e-id"), http.StatusNotFound)
	wantStatus(t, e.get(t, "/c/hanko/chat/"+uitest.ID("conv:nope").String()), http.StatusNotFound)
}

// Mensagens esperando sem run (cancelada/interrompida) não mantêm a thread em polling eterno: aviso, sem hx-trigger.
func TestChatStrandedMessagesDoNotPollForever(t *testing.T) {
	e, cv := chatEnv(t)
	path := "/c/hanko/chat/" + cv.ID.String()
	e.f.Stranded(cv.ID, 2)
	rec := e.get(t, path)
	wantContains(t, rec, "Mensagens esperando resposta: 2.", "recolhê-las")
	wantNotContains(t, rec, "pensando…", `hx-trigger="every 2s"`)
	rec = e.get(t, path+"/mensagens", "HX-Request", "true")
	wantNotContains(t, rec, `hx-trigger="every 2s"`)
}

func TestChatSendAndPolling(t *testing.T) {
	e, cv := chatEnv(t)
	path := "/c/hanko/chat/" + cv.ID.String()

	// htmx: the answer is the thread fragment, now polling with the "pensando…" indicator
	rec := e.post(t, path+"/mensagens", url.Values{"message": {"E amanhã?"}}, true)
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "E amanhã?", "pensando…", `hx-get="`+path+`/mensagens"`, `hx-trigger="every 2s"`)
	wantNotContains(t, rec, "<html")

	// the fragment keeps polling while busy, then goes quiet when the reply lands
	wantContains(t, e.get(t, path+"/mensagens", "HX-Request", "true"), `hx-trigger="every 2s"`)
	run := uitest.ID("run:chat2")
	e.f.PersonaReplies(cv.ID, &run, "Amanhã: *nada* urgente.")
	rec = e.get(t, path+"/mensagens", "HX-Request", "true")
	wantContains(t, rec, "<em>nada</em>", "Amanhã: ")
	wantNotContains(t, rec, "pensando…", `hx-trigger="every 2s"`)

	// plain form post redirects back to the thread
	rec = e.post(t, path+"/mensagens", url.Values{"message": {"Obrigado"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != path+"#chat-thread" {
		t.Errorf("Location = %q", loc)
	}

	// an empty message is a friendly 422, not a stored message
	n := len(e.f.ConvMessages)
	rec = e.post(t, path+"/mensagens", url.Values{"message": {"   "}}, true)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	if len(e.f.ConvMessages) != n {
		t.Error("empty message stored")
	}
}

func TestChatNewConversation(t *testing.T) {
	f := uitest.Sample()
	e := newEnv(t, f)
	ada := uitest.ID("persona:ada").String()

	rec := e.post(t, "/c/hanko/chat/nova", url.Values{"persona": {ada}, "message": {"Oi Ada, tudo certo?"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if len(f.Conversations) != 1 || !strings.HasPrefix(rec.Header().Get("Location"), "/c/hanko/chat/"+f.Conversations[0].ID.String()) {
		t.Fatalf("conversations=%d location=%q", len(f.Conversations), rec.Header().Get("Location"))
	}
	if f.Conversations[0].Title != "Oi Ada, tudo certo?" || f.Conversations[0].PersonaID != uitest.ID("persona:ada") {
		t.Errorf("conversation = %+v", f.Conversations[0])
	}

	// without a message or persona the form comes back with the error and what was typed
	rec = e.post(t, "/c/hanko/chat/nova", url.Values{"persona": {ada}, "message": {""}}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, "Escreva a primeira mensagem.")
	rec = e.post(t, "/c/hanko/chat/nova", url.Values{"message": {"texto digitado"}}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, "Escolha a persona.", "texto digitado")
	if len(f.Conversations) != 1 {
		t.Errorf("conversations = %d", len(f.Conversations))
	}
}

func TestChatBlockedPersona(t *testing.T) {
	f := uitest.Sample()
	e := newEnv(t, f)
	sato := uitest.ID("persona:sato") // paused in the sample

	// the new-conversation form shows why the persona refuses, keeping the text
	rec := e.post(t, "/c/hanko/chat/nova", url.Values{"persona": {sato.String()}, "message": {"Alô?"}}, false)
	wantStatus(t, rec, http.StatusConflict)
	wantContains(t, rec, "Persona indisponível", "Sato", "pausada", "Alô?")
	if len(f.Conversations) != 0 {
		t.Fatal("blocked conversation stored")
	}

	// in an existing conversation the page warns and disables sending; a forged send is refused with the reason
	cv, err := f.CreateConversation(t.Context(), domain.HumanActor, f.Companies[0].ID, service.CreateConversationInput{PersonaID: sato, Title: "Pauta"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/c/hanko/chat/" + cv.ID.String()
	wantContains(t, e.get(t, path), "não pode responder agora", "disabled")
	rec = e.post(t, path+"/mensagens", url.Values{"message": {"oi"}}, true)
	wantStatus(t, rec, http.StatusConflict)
	wantContains(t, rec, "Persona indisponível")
}

func TestChatArchive(t *testing.T) {
	e, cv := chatEnv(t)
	path := "/c/hanko/chat/" + cv.ID.String()
	rec := e.post(t, path+"/arquivar", url.Values{}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	wantContains(t, e.get(t, "/c/hanko/chat?s=arquivadas"), "Qual o plano de hoje?")
	wantNotContains(t, e.get(t, "/c/hanko/chat"), "Qual o plano de hoje?")
	page := e.get(t, path)
	wantContains(t, page, "Conversa arquivada: reabra para continuar.", "Reabrir")
	wantNotContains(t, page, `name="message"`)

	wantStatus(t, e.post(t, path+"/reabrir", url.Values{}, false), http.StatusSeeOther)
	wantContains(t, e.get(t, path), `name="message"`, "Arquivar")
}

func TestChatRoles(t *testing.T) {
	for _, role := range allRoles {
		f := uitest.Sample()
		c, err := f.CreateConversation(t.Context(), domain.HumanActor, f.Companies[0].ID, service.CreateConversationInput{PersonaID: uitest.ID("persona:ada"), Message: "oi"})
		if err != nil {
			t.Fatal(err)
		}
		f.MyRoleIs = role
		e := newEnv(t, f)
		p := "/c/hanko/chat/" + c.ID.String()
		canWrite := role == domain.MemberMember || role == domain.MemberAdmin || role == domain.MemberOwner
		page, list := e.get(t, p), e.get(t, "/c/hanko/chat")
		wantStatus(t, page, http.StatusOK)
		if got := strings.Contains(page.Body.String(), `name="message"`); got != canWrite {
			t.Errorf("%s: composer on the thread = %v, want %v", role, got, canWrite)
		}
		if got := strings.Contains(list.Body.String(), `action="/c/hanko/chat/nova"`); got != canWrite {
			t.Errorf("%s: new-conversation form = %v, want %v", role, got, canWrite)
		}
		// the viewer reads everything but a forged post is refused with the friendly 403
		rec := e.post(t, p+"/mensagens", url.Values{"message": {"oi"}}, true)
		if canWrite {
			wantStatus(t, rec, http.StatusOK)
		} else {
			wantStatus(t, rec, http.StatusForbidden)
		}
	}
}
