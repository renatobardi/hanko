package api

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// roomCalls guarda o que os testes da sala precisam conferir, por fake (sem mexer no fakeSvc compartilhado).
var (
	roomMu    sync.Mutex
	roomCalls = map[*fakeSvc]*roomCall{}
)

type roomCall struct {
	create  service.CreateRoomInput
	added   []domain.ID
	removed []domain.ID
	patch   service.RoomSettingsPatch
}

func (f *fakeSvc) room() *roomCall {
	roomMu.Lock()
	defer roomMu.Unlock()
	if roomCalls[f] == nil {
		roomCalls[f] = &roomCall{}
	}
	return roomCalls[f]
}

func (f *fakeSvc) CreateRoom(_ context.Context, actor domain.Actor, cid domain.ID, in service.CreateRoomInput) (service.ConversationView, error) {
	f.room().create = in
	if len(in.PersonaIDs) < 2 {
		return service.ConversationView{}, domain.Invalid("personas", "uma sala tem de 2 a 12 personas")
	}
	now := time.Now()
	rv := &service.RoomView{Settings: domain.RoomSettings{Mode: domain.RoomMentions, MaxHops: 2}, MaxRunsPerMessage: len(in.PersonaIDs)}
	for _, id := range in.PersonaIDs {
		p := service.RoomParticipantView{RoomParticipant: domain.RoomParticipant{PersonaID: id, AddedAt: now}, Status: domain.PersonaIdle}
		for _, x := range f.personas {
			if x.ID == id {
				p.Name, p.Slug = x.Name, x.Slug
			}
		}
		rv.Participants = append(rv.Participants, p)
	}
	rv.ModeratorPersonaID = &in.PersonaIDs[0]
	title := in.Title
	if title == "" {
		title = domain.TitleFromMessage(in.Message)
	}
	cv := service.ConversationView{Conversation: domain.Conversation{ID: domain.NewID(), CompanyID: cid, Kind: domain.ConversationRoom,
		Title: title, Status: domain.ConversationOpen, CreatedBy: actor, CreatedAt: now, UpdatedAt: now, LastMessageAt: now}, Room: rv}
	f.chat().convs = append(f.chat().convs, cv)
	return cv, nil
}

func (f *fakeSvc) findRoom(id domain.ID) (*service.ConversationView, error) {
	for i := range f.chat().convs {
		if f.chat().convs[i].ID == id {
			if f.chat().convs[i].Room == nil {
				return nil, domain.Invalid("conversation", "esta conversa é um chat direto, não uma sala")
			}
			return &f.chat().convs[i], nil
		}
	}
	return nil, domain.ErrNotFound
}

func (f *fakeSvc) AddRoomParticipant(_ context.Context, _ domain.Actor, _, convID, pid domain.ID) (service.ConversationView, error) {
	cv, err := f.findRoom(convID)
	if err != nil {
		return service.ConversationView{}, err
	}
	f.room().added = append(f.room().added, pid)
	cv.Room.Participants = append(cv.Room.Participants, service.RoomParticipantView{RoomParticipant: domain.RoomParticipant{PersonaID: pid}})
	return *cv, nil
}

func (f *fakeSvc) RemoveRoomParticipant(_ context.Context, _ domain.Actor, _, convID, pid domain.ID) (service.ConversationView, error) {
	cv, err := f.findRoom(convID)
	if err != nil {
		return service.ConversationView{}, err
	}
	f.room().removed = append(f.room().removed, pid)
	now := time.Now()
	for i := range cv.Room.Participants {
		if cv.Room.Participants[i].PersonaID == pid {
			cv.Room.Participants[i].RemovedAt = &now
		}
	}
	return *cv, nil
}

func (f *fakeSvc) UpdateRoomSettings(_ context.Context, _ domain.Actor, _, convID domain.ID, in service.RoomSettingsPatch) (service.ConversationView, error) {
	cv, err := f.findRoom(convID)
	if err != nil {
		return service.ConversationView{}, err
	}
	f.room().patch = in
	if in.Mode != nil {
		cv.Room.Settings.Mode = *in.Mode
	}
	return *cv, nil
}

func TestRoomAPI(t *testing.T) {
	f := &fakeSvc{}
	c := seedCompany(f)
	ana := domain.Persona{ID: domain.NewID(), CompanyID: c.ID, Slug: "ana", Name: "Ana", Status: domain.PersonaIdle}
	beto := domain.Persona{ID: domain.NewID(), CompanyID: c.ID, Slug: "beto", Name: "Beto", Status: domain.PersonaIdle}
	caio := domain.Persona{ID: domain.NewID(), CompanyID: c.ID, Slug: "caio", Name: "Caio", Status: domain.PersonaIdle}
	f.personas = append(f.personas, ana, beto, caio)
	ts, _ := newTestServer(t, f, nil)
	base := ts.URL + "/api/v1/companies/acme"

	// kind=room: personas e moderadora por slug, modo e limites passam ao service
	resp, body := do(t, "POST", base+"/conversations", `{"kind":"room","title":"Semana","personas":["ana","beto"],"moderator":"beto","mode":"all","max_hops":3,"max_runs_per_message":4}`, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create room: %d %s", resp.StatusCode, body)
	}
	in := f.room().create
	if len(in.PersonaIDs) != 2 || in.PersonaIDs[1] != beto.ID || in.ModeratorID == nil || *in.ModeratorID != beto.ID ||
		in.Mode != domain.RoomAll || in.MaxHops != 3 || in.MaxRunsPerMessage != 4 || in.Title != "Semana" {
		t.Fatalf("room input: %+v", in)
	}
	var cv Conversation
	if err := json.Unmarshal([]byte(body), &cv); err != nil || cv.Kind != "room" || cv.PersonaID != "" || cv.Room == nil ||
		len(cv.Room.Participants) != 2 || cv.Room.Participants[0].Slug != "ana" || !cv.Room.Participants[0].Active {
		t.Fatalf("room out: %+v %v", cv, err)
	}
	for _, bad := range []string{
		`{"kind":"room","title":"x","personas":["ana","nao-existe"]}`,
		`{"kind":"room","title":"x","personas":["ana"]}`,
		`{"kind":"room","title":"x","personas":["ana","beto"],"mode":"todos"}`,
		`{"kind":"room","title":"x","personas":["ana","beto"],"max_hops":9}`,
		`{"kind":"sala","title":"x"}`,
		`{"title":"sem persona"}`,
	} {
		if resp, b := do(t, "POST", base+"/conversations", bad, nil); resp.StatusCode != 404 && resp.StatusCode != 422 {
			t.Errorf("%s: %d %s", bad, resp.StatusCode, b)
		}
	}

	// filtro por tipo na lista
	if resp, _ = do(t, "GET", base+"/conversations?kind=room", "", nil); resp.StatusCode != 200 || f.chat().lastFilt.Kind != domain.ConversationRoom {
		t.Fatalf("kind filter: %d %+v", resp.StatusCode, f.chat().lastFilt)
	}
	if resp, _ = do(t, "GET", base+"/conversations?kind=grupo", "", nil); resp.StatusCode != 422 {
		t.Fatalf("bad kind: %d", resp.StatusCode)
	}

	// participantes
	resp, body = do(t, "POST", base+"/conversations/"+cv.ID+"/participants", `{"persona":"caio"}`, nil)
	if resp.StatusCode != 200 || len(f.room().added) != 1 || f.room().added[0] != caio.ID {
		t.Fatalf("add: %d %s", resp.StatusCode, body)
	}
	resp, body = do(t, "DELETE", base+"/conversations/"+cv.ID+"/participants/ana", "", nil)
	if resp.StatusCode != 200 || len(f.room().removed) != 1 || f.room().removed[0] != ana.ID || !strings.Contains(body, `"removed_at"`) {
		t.Fatalf("remove: %d %s", resp.StatusCode, body)
	}
	if resp, _ = do(t, "DELETE", base+"/conversations/"+cv.ID+"/participants/ninguem", "", nil); resp.StatusCode != 404 {
		t.Fatalf("remove unknown persona: %d", resp.StatusCode)
	}

	// configuração
	resp, body = do(t, "PATCH", base+"/conversations/"+cv.ID+"/room", `{"mode":"mentions","moderator":"caio","max_hops":1}`, nil)
	p := f.room().patch
	if resp.StatusCode != 200 || p.Mode == nil || *p.Mode != domain.RoomMentions || p.ModeratorID == nil || *p.ModeratorID != caio.ID ||
		p.MaxHops == nil || *p.MaxHops != 1 || p.MaxRunsPerMessage != nil {
		t.Fatalf("patch: %d %s %+v", resp.StatusCode, body, p)
	}
	if resp, _ = do(t, "PATCH", base+"/conversations/"+cv.ID+"/room", `{"max_hops":0}`, nil); resp.StatusCode != 422 {
		t.Fatalf("bad hops: %d", resp.StatusCode)
	}

	// mensagens da sala trazem a persona, as menções e as entregas
	rid := domain.NewID()
	cid, _ := domain.ParseID(cv.ID)
	f.chat().msgs = append(f.chat().msgs,
		domain.ConversationMessage{ID: domain.NewID(), ConversationID: cid, Kind: domain.MessageHuman, Author: domain.HumanActor, Body: "@beto oi",
			Mentions: []domain.ID{beto.ID}, Deliveries: []domain.MessageDelivery{{PersonaID: beto.ID, RunID: &rid}, {PersonaID: caio.ID}}, CreatedAt: time.Now()},
		domain.ConversationMessage{ID: domain.NewID(), ConversationID: cid, Kind: domain.MessagePersona, Author: domain.PersonaActor(beto.ID), Body: "oi",
			PersonaID: &beto.ID, PersonaName: "Beto", RunID: &rid, CreatedAt: time.Now()})
	resp, body = do(t, "GET", base+"/conversations/"+cv.ID+"/messages", "", nil)
	var ms ListBody[ConversationMessage]
	if resp.StatusCode != 200 || json.Unmarshal([]byte(body), &ms) != nil || len(ms.Items) != 2 {
		t.Fatalf("messages: %d %s", resp.StatusCode, body)
	}
	h, r := ms.Items[0], ms.Items[1]
	if len(h.Mentions) != 1 || len(h.Deliveries) != 2 || h.Deliveries[0].RunID != rid.String() || h.Deliveries[1].RunID != "" ||
		r.PersonaID != beto.ID.String() || r.PersonaName != "Beto" {
		t.Fatalf("room messages: %+v", ms.Items)
	}

	// operações de sala num chat direto: 422
	direct := service.ConversationView{Conversation: domain.Conversation{ID: domain.NewID(), CompanyID: c.ID, PersonaID: ana.ID, Kind: domain.ConversationDirect, Title: "d"}}
	f.chat().convs = append(f.chat().convs, direct)
	if resp, _ = do(t, "PATCH", base+"/conversations/"+direct.ID.String()+"/room", `{"mode":"all"}`, nil); resp.StatusCode != 422 {
		t.Fatalf("patch a direct chat: %d", resp.StatusCode)
	}
	resp, body = do(t, "GET", base+"/conversations/"+direct.ID.String(), "", nil)
	if resp.StatusCode != 200 || !strings.Contains(body, `"kind":"direct"`) || strings.Contains(body, `"room"`) {
		t.Fatalf("direct out: %s", body)
	}
}

// O run token de uma persona não usa as rotas de sala (só um humano escreve).
func TestRoomAPIRefusesRunTokens(t *testing.T) {
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
	base := ts.URL + "/api/v1/companies/acme/conversations/" + domain.NewID().String()
	for _, tc := range []struct{ method, path, body string }{
		{"POST", "/participants", `{"persona":"ana"}`},
		{"DELETE", "/participants/ana", ""},
		{"PATCH", "/room", `{"mode":"all"}`},
	} {
		if resp, b := do(t, tc.method, base+tc.path, tc.body, bearer); resp.StatusCode != 403 {
			t.Errorf("%s %s: %d %s", tc.method, tc.path, resp.StatusCode, b)
		}
	}
	if resp, _ := do(t, "POST", ts.URL+"/api/v1/companies/acme/conversations", `{"kind":"room","title":"x","personas":["a","b"]}`, bearer); resp.StatusCode != 403 {
		t.Errorf("create room with a run token: %d", resp.StatusCode)
	}
}
