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
	cvid  = "01a13f51-34c3-7a81-86d8-0fe59e6316fd"
	humID = "01a13f51-0000-7000-8000-00000000000a"
	repID = "01a13f51-0000-7000-8000-00000000000b"
	runID = "01a13f51-3595-71a1-aab5-716ed5a272dc"
)

var (
	t0    = time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	conv  = api.Conversation{ID: cvid, PersonaID: "p1", PersonaName: "Ana", PersonaSlug: "ana", Title: "Qual o risco?", Status: "open", LastMessageAt: t0, CreatedAt: t0, UpdatedAt: t0}
	human = api.ConversationMessage{ID: humID, ConversationID: cvid, Author: api.ActorRef{Type: "human"}, Body: "Qual o risco?", CreatedAt: t0}
	reply = api.ConversationMessage{ID: repID, ConversationID: cvid, Author: api.ActorRef{Type: "persona"}, Body: "Baixo: **tudo** testado.", RunID: runID, CreatedAt: t0.Add(time.Minute)}
)

// all returns a copy of every request received so far.
func (f *fakeAPI) all() []hit {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]hit(nil), f.hits...)
}

func TestChatEnviaEEsperaAResposta(t *testing.T) {
	var polls atomic.Int32
	busy := conv
	busy.ActiveRunID = runID
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"POST /api/v1/companies/acme/conversations": jsonResp(201, busy),
		"GET /api/v1/companies/acme/conversations/" + cvid: func(w http.ResponseWriter, r *http.Request) {
			// busy on the first look, idle on the next
			if polls.Add(1) == 1 {
				jsonResp(200, busy)(w, r)
				return
			}
			jsonResp(200, conv)(w, r)
		},
		"GET /api/v1/companies/acme/conversations/" + cvid + "/messages": jsonResp(200, list(human, reply)),
	})
	code, out, errs := exec(t, Runtime{}, gateEnv(ts.URL), "", "chat", "ana", "Qual o risco?")
	if code != 0 {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	if !strings.Contains(out, "Baixo: **tudo** testado.") || !strings.Contains(out, "conversa "+cvid) || !strings.Contains(out, "execução "+runID) {
		t.Fatalf("out %q", out)
	}
	if strings.Contains(out, "Qual o risco?\n\nBaixo") {
		t.Fatalf("the question must not be echoed: %q", out)
	}
	if polls.Load() < 2 {
		t.Fatalf("expected polling, got %d looks", polls.Load())
	}
	var created string
	for _, h := range f.all() {
		if h.Method == "POST" {
			created = h.Body
		}
	}
	if !strings.Contains(created, `"persona":"ana"`) || !strings.Contains(created, `"message":"Qual o risco?"`) {
		t.Fatalf("create body %q", created)
	}
}

func TestChatNoWait(t *testing.T) {
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"POST /api/v1/companies/acme/conversations":                      jsonResp(201, conv),
		"GET /api/v1/companies/acme/conversations/" + cvid + "/messages": jsonResp(200, list(human)),
	})
	code, out, errs := exec(t, Runtime{}, gateEnv(ts.URL), "", "chat", "ana", "oi", "--no-wait")
	if code != 0 || !strings.Contains(out, cvid) || !strings.Contains(out, "hanko chat show") {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	for _, h := range f.all() {
		if h.Method == "GET" && !strings.HasSuffix(h.Path, "/messages") {
			t.Fatalf("--no-wait must not poll the conversation: %+v", h)
		}
	}
	_, out, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "chat", "ana", "oi", "--no-wait", "--json")
	if !strings.Contains(out, `"conversation_id": "`+cvid+`"`) || !strings.Contains(out, `"message_id": "`+humID+`"`) {
		t.Fatalf("json %q", out)
	}
}

func TestChatContinuaConversaPorPrefixo(t *testing.T) {
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/conversations":                        jsonResp(200, list(conv)),
		"POST /api/v1/companies/acme/conversations/" + cvid + "/messages": jsonResp(201, human),
		"GET /api/v1/companies/acme/conversations/" + cvid:                jsonResp(200, conv),
		"GET /api/v1/companies/acme/conversations/" + cvid + "/messages":  jsonResp(200, list(human, reply)),
	})
	code, out, errs := exec(t, Runtime{}, gateEnv(ts.URL), "", "chat", "ana", "E o plano B?", "--conversation", "01a13f51")
	if code != 0 || !strings.Contains(out, "Baixo") {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	var sent bool
	for _, h := range f.all() {
		if h.Method == "POST" && strings.HasSuffix(h.Path, "/messages") && strings.Contains(h.Body, `"message":"E o plano B?"`) {
			sent = true
		}
		if h.Method == "POST" && strings.HasSuffix(h.Path, "/conversations") {
			t.Fatalf("--conversation must not open a new conversation")
		}
	}
	if !sent {
		t.Fatal("message not sent to the existing conversation")
	}
}

func TestChatTimeoutEFalha(t *testing.T) {
	busy := conv
	busy.ActiveRunID = runID
	ts, _ := newFakeAPI(t, map[string]http.HandlerFunc{
		"POST /api/v1/companies/acme/conversations":                      jsonResp(201, busy),
		"GET /api/v1/companies/acme/conversations/" + cvid:               jsonResp(200, busy),
		"GET /api/v1/companies/acme/conversations/" + cvid + "/messages": jsonResp(200, list(human)),
	})
	code, _, errs := exec(t, Runtime{}, gateEnv(ts.URL), "", "chat", "ana", "oi", "--timeout", "150ms")
	if code != 1 || !strings.Contains(errs, "tempo esgotado") || !strings.Contains(errs, cvid) {
		t.Fatalf("%d %q", code, errs)
	}

	// a failed run leaves a notice from the system: it is printed and the command fails
	notice := api.ConversationMessage{ID: repID, ConversationID: cvid, Author: api.ActorRef{Type: "system"}, Body: "A execução falhou.", RunID: runID, CreatedAt: t0}
	ts, _ = newFakeAPI(t, map[string]http.HandlerFunc{
		"POST /api/v1/companies/acme/conversations":                      jsonResp(201, conv),
		"GET /api/v1/companies/acme/conversations/" + cvid:               jsonResp(200, conv),
		"GET /api/v1/companies/acme/conversations/" + cvid + "/messages": jsonResp(200, list(human, notice)),
	})
	code, out, errs := exec(t, Runtime{}, gateEnv(ts.URL), "", "chat", "ana", "oi")
	if code != 1 || !strings.Contains(out, "A execução falhou.") || !strings.Contains(errs, "a persona não respondeu") {
		t.Fatalf("%d %q %q", code, out, errs)
	}
}

func TestChatUsoEListagem(t *testing.T) {
	ts, _ := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/conversations":                       jsonResp(200, list(conv)),
		"GET /api/v1/companies/acme/conversations/" + cvid:               jsonResp(200, conv),
		"GET /api/v1/companies/acme/conversations/" + cvid + "/messages": jsonResp(200, list(human, reply)),
	})
	// message is required and not blank
	if code, _, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "chat", "ana"); code != 2 {
		t.Fatalf("código = %d", code)
	}
	if code, _, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "chat", "ana", "   "); code != 2 {
		t.Fatalf("código = %d", code)
	}
	code, out, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "chat", "list", "--persona", "ana")
	if code != 0 || !strings.Contains(out, "01a13f51") || !strings.Contains(out, "Qual o risco?") || !strings.Contains(out, "ana") {
		t.Fatalf("%d %q", code, out)
	}
	code, out, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "chat", "show", "01a13f51")
	if code != 0 || !strings.Contains(out, "Qual o risco?") || !strings.Contains(out, "Baixo: **tudo** testado.") || !strings.Contains(out, "Ana") {
		t.Fatalf("%d %q", code, out)
	}
	// without a company
	if code, _, _ := exec(t, Runtime{}, map[string]string{"HANKO_URL": ts.URL}, "", "chat", "ana", "oi"); code != 2 {
		t.Fatalf("código = %d", code)
	}
}
