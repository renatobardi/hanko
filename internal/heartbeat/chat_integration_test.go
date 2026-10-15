//go:build integration

package heartbeat

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/adapter/fake"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/testdb"
)

// spyAdapter runs the fake adapter and remembers every spec it received.
type spyAdapter struct {
	*fake.Adapter
	mu    sync.Mutex
	specs []adapter.Spec
}

func (s *spyAdapter) Run(ctx context.Context, spec adapter.Spec, sink adapter.Sink) (adapter.Result, error) {
	s.mu.Lock()
	s.specs = append(s.specs, spec)
	s.mu.Unlock()
	return s.Adapter.Run(ctx, spec, sink)
}

func (s *spyAdapter) all() []adapter.Spec {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]adapter.Spec(nil), s.specs...)
}

func newChatStack(t *testing.T, delay time.Duration) (*service.Service, *spyAdapter, domain.Company, domain.Persona) {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	broker := events.NewBroker()
	hb, err := New(pool, Options{Workers: 4, PeriodicInterval: time.Hour, RunTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	svc := service.New(pool, hb, broker)
	spy := &spyAdapter{Adapter: fake.New(fake.Options{Delay: delay})}
	hb.Attach(svc, adapter.Registry{"fake": spy}, memory.Noop{}, broker, authForTest())
	co, err := svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Acme", Slug: "acme", TaskPrefix: "ACM"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := svc.HirePersona(ctx, domain.HumanActor, co.ID, service.HirePersonaInput{Name: "Ana", Title: "CTO", Agent: "fake",
		Instructions: "Fale sempre em português."})
	if err != nil {
		t.Fatal(err)
	}
	if err := hb.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = hb.Stop(sctx)
	})
	return svc, spy, co, p
}

func replies(t *testing.T, svc *service.Service, co, conv domain.ID) []domain.ConversationMessage {
	t.Helper()
	ms, err := svc.ListConversationMessages(context.Background(), co, conv, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []domain.ConversationMessage
	for _, m := range ms {
		if m.Kind != domain.MessageHuman {
			out = append(out, m)
		}
	}
	return out
}

// A human message becomes a chat run executed by the real worker with the fake adapter; the final
// summary comes back as the persona's reply, the run log stays reachable and the second message
// resumes the session of the first.
func TestChatEndToEnd(t *testing.T) {
	ctx := context.Background()
	svc, spy, co, p := newChatStack(t, -1)

	cv, err := svc.CreateConversation(ctx, domain.HumanActor, co.ID, service.CreateConversationInput{PersonaID: p.ID, Message: "Qual o plano para hoje?"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "primeira resposta", func() bool { return len(replies(t, svc, co.ID, cv.ID)) == 1 })
	rep := replies(t, svc, co.ID, cv.ID)[0]
	if rep.Kind != domain.MessagePersona || !strings.Contains(rep.Body, "Fake respondeu na conversa") || rep.RunID == nil {
		t.Fatalf("reply: %+v", rep)
	}
	run, err := svc.GetRun(ctx, co.ID, *rep.RunID)
	if err != nil || run.Status != domain.RunDone || run.Trigger != domain.TriggerChat || run.ConversationTitle != cv.Title {
		t.Fatalf("run: %+v %v", run, err)
	}
	if evs, err := svc.ListRunEvents(ctx, co.ID, run.ID, 0, 0); err != nil || len(evs) == 0 {
		t.Fatalf("the run log must stay reachable: %d events %v", len(evs), err)
	}

	specs := spy.all()
	if len(specs) != 1 {
		t.Fatalf("specs: %d", len(specs))
	}
	sp := specs[0]
	if sp.ConversationID != cv.ID || sp.TaskID != (domain.ID{}) || sp.SessionRef != "" {
		t.Fatalf("spec: conv=%s task=%s session=%q", sp.ConversationID, sp.TaskID, sp.SessionRef)
	}
	if !strings.Contains(sp.System, "Fale sempre em português.") || !strings.Contains(sp.System, "Protocolo Hanko (conversa)") {
		t.Fatalf("system prompt: %s", sp.System)
	}
	if !strings.Contains(sp.Prompt, "Qual o plano para hoje?") || !strings.Contains(sp.Prompt, "## Mensagens novas") {
		t.Fatalf("prompt: %s", sp.Prompt)
	}
	if sp.Env["HANKO_CONVERSATION_ID"] != cv.ID.String() || sp.Env["HANKO_RUN_TOKEN"] == "" {
		t.Fatalf("env: %v", sp.Env)
	}
	claims, err := authForTest().Verify(sp.Env["HANKO_RUN_TOKEN"])
	if err != nil || claims.ConversationID != cv.ID.String() || claims.TaskID != "" {
		t.Fatalf("token claims: %+v %v", claims, err)
	}
	if !strings.Contains(string(sp.MCPConfig), `"hanko"`) {
		t.Fatalf("a chat run gets the Hanko MCP like any run: %s", sp.MCPConfig)
	}

	// second turn: resumes the session and sends only the new message
	if _, err := svc.SendMessage(ctx, domain.HumanActor, co.ID, cv.ID, "E amanhã?"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "segunda resposta", func() bool { return len(replies(t, svc, co.ID, cv.ID)) == 2 })
	specs = spy.all()
	if len(specs) != 2 {
		t.Fatalf("specs: %d", len(specs))
	}
	if specs[1].SessionRef != "fake-"+rep.RunID.String() {
		t.Fatalf("session not resumed: %q", specs[1].SessionRef)
	}
	// the first message only shows up in the title (derived from it), never as history
	if !strings.Contains(specs[1].Prompt, "Sessão anterior retomada") || strings.Count(specs[1].Prompt, "Qual o plano para hoje?") != 1 ||
		!strings.Contains(specs[1].Prompt, "E amanhã?") {
		t.Fatalf("resumed prompt repeats the history: %s", specs[1].Prompt)
	}
	if got := replies(t, svc, co.ID, cv.ID)[1].Body; !strings.Contains(got, "sessão retomada") {
		t.Fatalf("second reply: %s", got)
	}
}

// Messages sent while the persona is answering wait and are answered together by one more run.
func TestChatCoalescesMessagesSentWhileRunning(t *testing.T) {
	ctx := context.Background()
	svc, spy, co, p := newChatStack(t, 150*time.Millisecond)

	cv, err := svc.CreateConversation(ctx, domain.HumanActor, co.ID, service.CreateConversationInput{PersonaID: p.ID, Message: "primeira"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "run em andamento", func() bool { return len(spy.all()) == 1 })
	for _, m := range []string{"segunda", "terceira"} {
		if _, err := svc.SendMessage(ctx, domain.HumanActor, co.ID, cv.ID, m); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, 30*time.Second, "duas respostas", func() bool { return len(replies(t, svc, co.ID, cv.ID)) == 2 })
	specs := spy.all()
	if len(specs) != 2 {
		t.Fatalf("exactly two runs expected (one for the first message, one for the two that waited): %d", len(specs))
	}
	if !strings.Contains(specs[1].Prompt, "segunda") || !strings.Contains(specs[1].Prompt, "terceira") {
		t.Fatalf("the follow-up run must carry both messages: %s", specs[1].Prompt)
	}
	got, _ := svc.GetConversation(ctx, co.ID, cv.ID)
	if got.PendingMessages != 0 || got.ActiveRunID != nil {
		t.Fatalf("conversation after: %+v", got)
	}
}

// A failing harness leaves a notice in the conversation instead of a reply.
func TestChatFailedRunPostsNotice(t *testing.T) {
	ctx := context.Background()
	svc, _, co, _ := newChatStack(t, -1)
	failing, err := svc.HirePersona(ctx, domain.HumanActor, co.ID, service.HirePersonaInput{Name: "Falha", Agent: "fake",
		AdapterConfig: []byte(`{"fail":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	cv, err := svc.CreateConversation(ctx, domain.HumanActor, co.ID, service.CreateConversationInput{PersonaID: failing.ID, Message: "oi"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "aviso de falha", func() bool { return len(replies(t, svc, co.ID, cv.ID)) == 1 })
	if m := replies(t, svc, co.ID, cv.ID)[0]; m.Kind != domain.MessageSystem || !strings.Contains(m.Body, "falhou") {
		t.Fatalf("notice: %+v", m)
	}
}
