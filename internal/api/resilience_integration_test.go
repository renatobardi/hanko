//go:build integration

package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/service"
)

// Run actions, steer and the follow-up queue over HTTP (ADR-0039).
func TestResilienceAPI(t *testing.T) {
	svc, c, broker := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	co, err := svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Acme", Slug: "acme", TaskPrefix: "ACM"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := svc.HirePersona(ctx, domain.HumanActor, co.ID, service.HirePersonaInput{Name: "Ana", Agent: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	tv, err := svc.CreateTask(ctx, domain.HumanActor, co.ID, service.CreateTaskInput{Title: "Publicar", PersonaID: &p.ID})
	if err != nil {
		t.Fatal(err)
	}
	checkout := func() domain.Run {
		run, err := svc.CheckoutRun(ctx, service.Wake{CompanyID: co.ID, PersonaID: p.ID, TaskID: tv.ID, Trigger: domain.TriggerAssign})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.MarkRunStarted(ctx, co.ID, run.ID, "", ""); err != nil {
			t.Fatal(err)
		}
		return run
	}
	event := func(run domain.Run, kind domain.RunEventKind, text, payload, session string) {
		ev := adapter.Event{Kind: kind, Text: text, SessionRef: session}
		if payload != "" {
			ev.Payload = json.RawMessage(payload)
		}
		if _, err := svc.AppendRunEvent(ctx, co.ID, run.ID, ev); err != nil {
			t.Fatal(err)
		}
	}

	// 1. a process killed with an external effect in flight: the diagnosis card is in the API
	run := checkout()
	event(run, domain.EventSystem, "sessão", "", "sess-api")
	event(run, domain.EventToolUse, "Bash: git push", `{"id":"b1","name":"Bash"}`, "")
	if n, err := svc.InterruptOrphanRuns(ctx); err != nil || n != 1 {
		t.Fatalf("orphans = %d %v", n, err)
	}
	r, err := c.Run(ctx, co.Slug, run.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	d := r.Diagnosis
	if r.Status != "interrupted" || !r.DecisionPending || d == nil || d.Cause != "process_restart" || d.NextAction != "human_decision" || d.Owner != "human" ||
		d.Uncertain == nil || d.Uncertain.Name != "Bash" || r.SteerMode != "inject" || r.SessionRef != "sess-api" {
		t.Fatalf("run = %+v diagnosis = %+v", r, d)
	}

	// 2. the operator decides: invalid action → 422; Continuar → recorded
	if _, err := c.RunAction(ctx, co.Slug, run.ID.String(), "explodir", ""); err == nil {
		t.Fatal("unknown action accepted")
	}
	r, err = c.RunAction(ctx, co.Slug, run.ID.String(), "continue", "conferi o remoto")
	if err != nil || r.Decision != "continue" || r.DecisionPending || r.DecisionNote != "conferi o remoto" {
		t.Fatalf("action = %+v %v", r, err)
	}
	if _, err := c.RunAction(ctx, co.Slug, run.ID.String(), "close", ""); err == nil {
		t.Fatal("a decided run cannot be decided again")
	}
	cont, err := svc.CheckoutRun(ctx, service.Wake{CompanyID: co.ID, PersonaID: p.ID, TaskID: tv.ID, Trigger: domain.TriggerManual})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.MarkRunStarted(ctx, co.ID, cont.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	r, _ = c.Run(ctx, co.Slug, run.ID.String())
	if r.ContinuedByRunID != cont.ID.String() {
		t.Fatalf("continued_by = %q", r.ContinuedByRunID)
	}
	cr, _ := c.Run(ctx, co.Slug, cont.ID.String())
	if cr.ContinuationOfRunID != run.ID.String() || cr.ContinuationReason != "continue" {
		t.Fatalf("continuation = %+v", cr)
	}

	// 3. steer the active continuation: the fake injects, so the message goes out on the broker
	sub, unsub := broker.Subscribe(events.RunTopic(cont.ID.String()), 8)
	defer unsub()
	res, err := c.SteerRun(ctx, co.Slug, cont.ID.String(), "agora foque nos testes")
	if err != nil || res.Mode != "inject" || res.Followup.Delivery != "steer" || res.Followup.WaitReason != "steer_pending" {
		t.Fatalf("steer = %+v %v", res, err)
	}
	if m := <-sub; m.Type != service.RunSteerMsg {
		t.Fatalf("message = %+v", m)
	}
	if _, err := c.SteerRun(ctx, co.Slug, cont.ID.String(), ""); err == nil {
		t.Fatal("empty steer accepted")
	}

	// 4. the follow-up queue of the task: queue, list with order and reason, edit, discard
	f1, err := c.QueueTaskFollowup(ctx, co.Slug, tv.Key, "primeiro")
	if err != nil || f1.Kind != "task" || f1.WaitReason != "run_active" || f1.WaitDetail == "" {
		t.Fatalf("queue = %+v %v", f1, err)
	}
	f2, _ := c.QueueTaskFollowup(ctx, co.Slug, tv.Key, "segundo")
	list, err := c.TaskFollowups(ctx, co.Slug, tv.Key)
	if err != nil || len(list) != 3 || list[1].ID != f1.ID || list[1].Position != 2 || list[2].ID != f2.ID {
		t.Fatalf("list = %+v %v", list, err)
	}
	ed, err := c.EditFollowup(ctx, co.Slug, f1.ID, "primeiro (editado)")
	if err != nil || ed.Body != "primeiro (editado)" || ed.EditedAt == nil {
		t.Fatalf("edit = %+v %v", ed, err)
	}
	if err := c.DiscardFollowup(ctx, co.Slug, f2.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.DiscardFollowup(ctx, co.Slug, f2.ID); err == nil {
		t.Fatal("discarding twice must fail")
	}
	list, _ = c.TaskFollowups(ctx, co.Slug, tv.Key)
	if len(list) != 2 {
		t.Fatalf("list after discard = %+v", list)
	}
	if _, err := c.EditFollowup(ctx, co.Slug, "nao-e-uuid", "x"); err == nil {
		t.Fatal("bad id accepted")
	}

	// 5. the conversation queue
	cv, err := svc.CreateConversation(ctx, domain.HumanActor, co.ID, service.CreateConversationInput{PersonaID: p.ID, Message: "oi"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SendMessage(ctx, domain.HumanActor, co.ID, cv.ID, "e depois?"); err != nil {
		t.Fatal(err)
	}
	cf, err := c.ConversationFollowups(ctx, co.Slug, cv.ID.String())
	if err != nil || len(cf) != 1 || cf[0].Kind != "chat" || cf[0].Body != "e depois?" || cf[0].ConversationID != cv.ID.String() {
		t.Fatalf("conversation followups = %+v %v", cf, err)
	}
	if err := c.DiscardFollowup(ctx, co.Slug, cf[0].ID); err != nil {
		t.Fatal(err)
	}
	if cf, _ = c.ConversationFollowups(ctx, co.Slug, cv.ID.String()); len(cf) != 0 {
		t.Fatalf("after discard: %+v", cf)
	}

	// the OpenAPI document describes the new operations
	spec, err := api.Spec()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"run-action", "steer-run", "list-task-followups", "queue-task-followup", "list-conversation-followups", "edit-followup", "discard-followup"} {
		if !strings.Contains(string(spec), `"operationId": "`+id+`"`) {
			t.Errorf("openapi lacks %s", id)
		}
	}
}
