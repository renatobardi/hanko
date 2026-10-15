package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// chatFake keeps the conversations of the REST tests.
type chatFake struct {
	convs     []service.ConversationView
	msgs      []domain.ConversationMessage
	lastFilt  service.ConversationFilter
	lastLimit int
	lastIn    service.CreateConversationInput
	lastActor domain.Actor
}

type blockedErr struct{}

func (blockedErr) Error() string { return "a persona Ana está pausada e não pode responder agora" }
func (blockedErr) Unwrap() error { return domain.ErrPersonaUnavailable }

func (f *fakeSvc) chat() *chatFake {
	if f.chatF == nil {
		f.chatF = &chatFake{}
	}
	return f.chatF
}

func (f *fakeSvc) ListConversations(_ context.Context, _ domain.ID, fl service.ConversationFilter) ([]service.ConversationView, error) {
	f.chat().lastFilt = fl
	return f.chat().convs, nil
}

func (f *fakeSvc) GetConversation(_ context.Context, _, id domain.ID) (service.ConversationView, error) {
	for _, c := range f.chat().convs {
		if c.ID == id {
			return c, nil
		}
	}
	return service.ConversationView{}, domain.ErrNotFound
}

func (f *fakeSvc) ListConversationMessages(_ context.Context, _, id domain.ID, limit int) ([]domain.ConversationMessage, error) {
	f.chat().lastLimit = limit
	var out []domain.ConversationMessage
	for _, m := range f.chat().msgs {
		if m.ConversationID == id {
			out = append(out, m)
		}
	}
	return out, nil
}

func (f *fakeSvc) CreateConversation(_ context.Context, a domain.Actor, cid domain.ID, in service.CreateConversationInput) (service.ConversationView, error) {
	c := f.chat()
	c.lastIn, c.lastActor = in, a
	if in.Message == "bloqueada" {
		return service.ConversationView{}, blockedErr{}
	}
	title := in.Title
	if title == "" {
		title = domain.TitleFromMessage(in.Message)
	}
	cv := service.ConversationView{Conversation: domain.Conversation{ID: domain.NewID(), CompanyID: cid, PersonaID: in.PersonaID, Title: title,
		Status: domain.ConversationOpen, CreatedBy: a, CreatedAt: time.Now(), UpdatedAt: time.Now(), LastMessageAt: time.Now()},
		PersonaName: "Ana", PersonaSlug: "ana", PendingMessages: 1}
	c.convs = append(c.convs, cv)
	c.msgs = append(c.msgs, domain.ConversationMessage{ID: domain.NewID(), ConversationID: cv.ID, Author: a, Kind: domain.MessageHuman, Body: in.Message, CreatedAt: time.Now()})
	return cv, nil
}

func (f *fakeSvc) SendMessage(_ context.Context, a domain.Actor, cid, id domain.ID, body string) (domain.ConversationMessage, error) {
	f.chat().lastActor = a
	if a.Type != domain.ActorHuman {
		return domain.ConversationMessage{}, domain.ErrNotAllowed
	}
	if body == "bloqueada" {
		return domain.ConversationMessage{}, blockedErr{}
	}
	for _, c := range f.chat().convs {
		if c.ID == id {
			m := domain.ConversationMessage{ID: domain.NewID(), CompanyID: cid, ConversationID: id, Author: a, Kind: domain.MessageHuman, Body: body, CreatedAt: time.Now()}
			f.chat().msgs = append(f.chat().msgs, m)
			return m, nil
		}
	}
	return domain.ConversationMessage{}, domain.ErrNotFound
}

func (f *fakeSvc) SetConversationStatus(_ context.Context, _ domain.Actor, _, id domain.ID, st domain.ConversationStatus) (service.ConversationView, error) {
	for i, c := range f.chat().convs {
		if c.ID == id {
			f.chat().convs[i].Status = st
			return f.chat().convs[i], nil
		}
	}
	return service.ConversationView{}, domain.ErrNotFound
}

func TestChatAPI(t *testing.T) {
	f := &fakeSvc{}
	c := seedCompany(f)
	ana := domain.Persona{ID: domain.NewID(), CompanyID: c.ID, Slug: "ana", Name: "Ana", Status: domain.PersonaIdle}
	f.personas = append(f.personas, ana)
	ts, _ := newTestServer(t, f, nil)
	base := ts.URL + "/api/v1/companies/acme"

	// open with the first message: the persona is resolved by slug, the author is the human
	resp, body := do(t, "POST", base+"/conversations", `{"persona":"ana","message":"Qual o plano?"}`, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create: %d %s", resp.StatusCode, body)
	}
	var cv Conversation
	if err := json.Unmarshal([]byte(body), &cv); err != nil || cv.Title != "Qual o plano?" || cv.PersonaSlug != "ana" || cv.PendingMessages != 1 || cv.Status != "open" || cv.CreatedBy.Type != "human" {
		t.Fatalf("conversation: %+v %v", cv, err)
	}
	if f.chat().lastIn.PersonaID != ana.ID || f.chat().lastIn.Message != "Qual o plano?" {
		t.Fatalf("input: %+v", f.chat().lastIn)
	}
	for _, bad := range []string{`{"persona":""}`, `{"message":"x"}`, `{"persona":"nao-existe","message":"x"}`} {
		resp, _ = do(t, "POST", base+"/conversations", bad, nil)
		if resp.StatusCode != 422 && resp.StatusCode != 404 {
			t.Fatalf("%s: %d", bad, resp.StatusCode)
		}
	}

	// a persona that cannot answer refuses with 409 and the reason
	resp, body = do(t, "POST", base+"/conversations", `{"persona":"ana","message":"bloqueada"}`, nil)
	if resp.StatusCode != 409 || !strings.Contains(body, "pausada") {
		t.Fatalf("blocked create: %d %s", resp.StatusCode, body)
	}

	// send, then read the thread back
	resp, body = do(t, "POST", base+"/conversations/"+cv.ID+"/messages", `{"message":"E amanhã?"}`, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("send: %d %s", resp.StatusCode, body)
	}
	var m ConversationMessage
	if err := json.Unmarshal([]byte(body), &m); err != nil || m.Body != "E amanhã?" || m.Author.Type != "human" || m.ConversationID != cv.ID {
		t.Fatalf("message: %+v %v", m, err)
	}
	if resp, body = do(t, "POST", base+"/conversations/"+cv.ID+"/messages", `{"message":"bloqueada"}`, nil); resp.StatusCode != 409 || !strings.Contains(body, "não pode responder agora") {
		t.Fatalf("blocked send: %d %s", resp.StatusCode, body)
	}
	if resp, _ = do(t, "POST", base+"/conversations/"+cv.ID+"/messages", `{"message":""}`, nil); resp.StatusCode != 422 {
		t.Fatalf("empty message: %d", resp.StatusCode)
	}

	resp, body = do(t, "GET", base+"/conversations/"+cv.ID+"/messages?limit=10", "", nil)
	var ms ListBody[ConversationMessage]
	if resp.StatusCode != 200 || json.Unmarshal([]byte(body), &ms) != nil || len(ms.Items) != 2 || ms.Items[0].Body != "Qual o plano?" || f.chat().lastLimit != 10 {
		t.Fatalf("messages: %d %s limit=%d", resp.StatusCode, body, f.chat().lastLimit)
	}

	// list filters, get, unknown and malformed ids
	resp, body = do(t, "GET", base+"/conversations?persona=ana&status=open", "", nil)
	var l ListBody[Conversation]
	if resp.StatusCode != 200 || json.Unmarshal([]byte(body), &l) != nil || len(l.Items) != 1 {
		t.Fatalf("list: %d %s", resp.StatusCode, body)
	}
	if f.chat().lastFilt.PersonaID == nil || *f.chat().lastFilt.PersonaID != ana.ID || f.chat().lastFilt.Status != domain.ConversationOpen {
		t.Fatalf("filter: %+v", f.chat().lastFilt)
	}
	if resp, _ = do(t, "GET", base+"/conversations?status=nope", "", nil); resp.StatusCode != 422 {
		t.Fatalf("bad status: %d", resp.StatusCode)
	}
	if resp, _ = do(t, "GET", base+"/conversations/"+cv.ID, "", nil); resp.StatusCode != 200 {
		t.Fatalf("get: %d", resp.StatusCode)
	}
	if resp, _ = do(t, "GET", base+"/conversations/"+domain.NewID().String(), "", nil); resp.StatusCode != 404 {
		t.Fatalf("unknown: %d", resp.StatusCode)
	}
	if resp, _ = do(t, "GET", base+"/conversations/not-a-uuid", "", nil); resp.StatusCode == 200 {
		t.Fatal("bad id accepted")
	}

	// archive and reopen
	resp, body = do(t, "POST", base+"/conversations/"+cv.ID+"/archive", "", nil)
	if resp.StatusCode != 200 || !strings.Contains(body, `"status":"archived"`) {
		t.Fatalf("archive: %d %s", resp.StatusCode, body)
	}
	if resp, body = do(t, "POST", base+"/conversations/"+cv.ID+"/reopen", "", nil); resp.StatusCode != 200 || !strings.Contains(body, `"status":"open"`) {
		t.Fatalf("reopen: %d %s", resp.StatusCode, body)
	}
}

// A persona's run token never talks through the chat API: the conversation is between a human and a persona.
func TestChatAPIRefusesRunTokens(t *testing.T) {
	f := &fakeSvc{}
	c := seedCompany(f)
	tokens := &auth.RunTokens{Secret: []byte("0123456789abcdef0123456789abcdef"), TTL: time.Hour}
	ts, _ := newTestServer(t, f, func(d *Deps) { d.Tokens = tokens })
	pid, rid := domain.NewID(), domain.NewID()
	f.runs = map[domain.ID]service.RunView{rid: {Run: domain.Run{ID: rid, CompanyID: c.ID, Status: domain.RunRunning}}}
	tok, err := tokens.IssueForConversation(c.ID, pid, rid, domain.NewID())
	if err != nil {
		t.Fatal(err)
	}
	bearer := map[string]string{"Authorization": "Bearer " + tok}
	base := ts.URL + "/api/v1/companies/acme"
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/conversations", ""},
		{"POST", "/conversations", `{"persona":"ana","message":"oi"}`},
		{"GET", "/conversations/" + domain.NewID().String(), ""},
		{"GET", "/conversations/" + domain.NewID().String() + "/messages", ""},
		{"POST", "/conversations/" + domain.NewID().String() + "/messages", `{"message":"oi"}`},
		{"POST", "/conversations/" + domain.NewID().String() + "/archive", ""},
	} {
		if resp, b := do(t, tc.method, base+tc.path, tc.body, bearer); resp.StatusCode != 403 {
			t.Errorf("%s %s: %d %s", tc.method, tc.path, resp.StatusCode, b)
		}
	}
}
