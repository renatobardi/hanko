package cli

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/api"
)

const (
	roomID = "01a14700-34c3-7a81-86d8-0fe59e6316fd"
	rHumID = "01a14700-0000-7000-8000-00000000000a"
)

var (
	room = api.Conversation{ID: roomID, Kind: "room", Title: "Planejar a semana", Status: "open", LastMessageAt: t0, CreatedAt: t0, UpdatedAt: t0,
		Room: &api.Room{Mode: "mentions", ModeratorPersonaID: "p-ana", MaxHops: 2, MaxRunsPerMessage: 2, Participants: []api.RoomParticipant{
			{PersonaID: "p-ana", Name: "Ana", Slug: "ana", Active: true}, {PersonaID: "p-beto", Name: "Beto", Slug: "beto", Active: true},
			{PersonaID: "p-caio", Name: "Caio", Slug: "caio", Active: false}}}}
	rHuman  = api.ConversationMessage{ID: rHumID, ConversationID: roomID, Author: api.ActorRef{Type: "human"}, Body: "@ana @beto status?", CreatedAt: t0}
	rAna    = api.ConversationMessage{ID: "m-ana", ConversationID: roomID, Author: api.ActorRef{Type: "persona"}, PersonaName: "Ana", Body: "Tudo certo.", CreatedAt: t0.Add(time.Minute)}
	rNotice = api.ConversationMessage{ID: "m-sys", ConversationID: roomID, Author: api.ActorRef{Type: "system"}, Body: "Beto não vai responder: pausada.", CreatedAt: t0.Add(time.Minute)}
)

func TestRoomSayEsperaAsRespostas(t *testing.T) {
	var polls atomic.Int32
	busy := room
	busy.ActiveRunID = runID
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/conversations":                          jsonResp(200, list(room)),
		"POST /api/v1/companies/acme/conversations/" + roomID + "/messages": jsonResp(201, rHuman),
		"GET /api/v1/companies/acme/conversations/" + roomID: func(w http.ResponseWriter, r *http.Request) {
			if polls.Add(1) == 1 {
				jsonResp(200, busy)(w, r)
				return
			}
			jsonResp(200, room)(w, r)
		},
		"GET /api/v1/companies/acme/conversations/" + roomID + "/messages": jsonResp(200, list(rHuman, rAna, rNotice)),
	})
	code, out, errs := exec(t, Runtime{}, gateEnv(ts.URL), "", "room", "say", "01a14700", "@ana @beto status?")
	if code != 0 {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	if !strings.Contains(out, "[Ana]\nTudo certo.") || !strings.Contains(out, "[sistema]\nBeto não vai responder") || !strings.Contains(out, "sala "+roomID) {
		t.Fatalf("out %q", out)
	}
	if polls.Load() < 2 {
		t.Fatalf("expected polling: %d", polls.Load())
	}
	var sent bool
	for _, h := range f.all() {
		if h.Method == "POST" && strings.Contains(h.Body, `"message":"@ana @beto status?"`) {
			sent = true
		}
	}
	if !sent {
		t.Fatal("message not sent")
	}

	// só avisos: falha
	ts, _ = newFakeAPI(t, map[string]http.HandlerFunc{
		"POST /api/v1/companies/acme/conversations/" + roomID + "/messages": jsonResp(201, rHuman),
		"GET /api/v1/companies/acme/conversations/" + roomID:                jsonResp(200, room),
		"GET /api/v1/companies/acme/conversations/" + roomID + "/messages":  jsonResp(200, list(rHuman, rNotice)),
	})
	code, _, errs = exec(t, Runtime{}, gateEnv(ts.URL), "", "room", "say", roomID, "oi")
	if code != 1 || !strings.Contains(errs, "nenhuma persona respondeu") {
		t.Fatalf("%d %q", code, errs)
	}
}

func TestRoomCreateListShowEParticipantes(t *testing.T) {
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"POST /api/v1/companies/acme/conversations":                                    jsonResp(201, room),
		"GET /api/v1/companies/acme/conversations":                                     jsonResp(200, list(room)),
		"GET /api/v1/companies/acme/conversations/" + roomID:                           jsonResp(200, room),
		"GET /api/v1/companies/acme/conversations/" + roomID + "/messages":             jsonResp(200, list(rHuman, rAna)),
		"POST /api/v1/companies/acme/conversations/" + roomID + "/participants":        jsonResp(200, room),
		"DELETE /api/v1/companies/acme/conversations/" + roomID + "/participants/caio": jsonResp(200, room),
		"PATCH /api/v1/companies/acme/conversations/" + roomID + "/room":               jsonResp(200, room),
	})
	env := gateEnv(ts.URL)
	if code, _, _ := exec(t, Runtime{}, env, "", "room", "create", "--persona", "ana", "x"); code != 2 {
		t.Fatalf("one persona: %d", code)
	}
	code, out, errs := exec(t, Runtime{}, env, "", "room", "create", "--persona", "ana, beto", "--mode", "all", "--moderator", "beto", "--max-hops", "3", "Planejar a semana")
	if code != 0 || !strings.Contains(out, "hanko room say "+roomID) {
		t.Fatalf("create: %d %q %q", code, out, errs)
	}
	var body string
	for _, h := range f.all() {
		if h.Method == "POST" && strings.HasSuffix(h.Path, "/conversations") {
			body = h.Body
		}
	}
	for _, want := range []string{`"kind":"room"`, `"personas":["ana","beto"]`, `"mode":"all"`, `"moderator":"beto"`, `"max_hops":3`, `"title":"Planejar a semana"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("create body lacks %s: %s", want, body)
		}
	}

	code, out, _ = exec(t, Runtime{}, env, "", "room", "list")
	if code != 0 || !strings.Contains(out, "01a14700") || !strings.Contains(out, "ana,beto") || strings.Contains(out, "caio") {
		t.Fatalf("list: %d %q", code, out)
	}
	var listQuery string
	for _, h := range f.all() {
		if h.Method == "GET" && strings.HasSuffix(h.Path, "/conversations") {
			listQuery = h.Query
		}
	}
	if !strings.Contains(listQuery, "kind=room") {
		t.Fatalf("list must ask for rooms: %q", listQuery)
	}

	code, out, _ = exec(t, Runtime{}, env, "", "room", "show", roomID)
	if code != 0 || !strings.Contains(out, "Ana (@ana) moderadora") || !strings.Contains(out, "Tudo certo.") || !strings.Contains(out, "2 saltos") || strings.Contains(out, "Caio") {
		t.Fatalf("show: %d %q", code, out)
	}

	if code, out, errs = exec(t, Runtime{}, env, "", "room", "add", roomID, "caio"); code != 0 || !strings.Contains(out, "2 personas") {
		t.Fatalf("add: %d %q %q", code, out, errs)
	}
	if code, _, errs = exec(t, Runtime{}, env, "", "room", "remove", roomID, "caio"); code != 0 {
		t.Fatalf("remove: %d %q", code, errs)
	}
	if code, _, _ = exec(t, Runtime{}, env, "", "room", "set", roomID); code != 2 {
		t.Fatalf("set without flags: %d", code)
	}
	if code, _, errs = exec(t, Runtime{}, env, "", "room", "set", roomID, "--mode", "mentions", "--max-runs", "0"); code != 0 {
		t.Fatalf("set: %d %q", code, errs)
	}
	for _, h := range f.all() {
		if h.Method == "PATCH" && (!strings.Contains(h.Body, `"mode":"mentions"`) || !strings.Contains(h.Body, `"max_runs_per_message":0`) || strings.Contains(h.Body, "max_hops")) {
			t.Fatalf("patch body: %s", h.Body)
		}
	}
	// chat list só pede os chats diretos
	_, _, _ = exec(t, Runtime{}, env, "", "chat", "list")
	last := f.all()[len(f.all())-1]
	if !strings.Contains(last.Query, "kind=direct") {
		t.Fatalf("chat list query: %q", last.Query)
	}
}
