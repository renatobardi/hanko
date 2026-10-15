package service_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// chatFixture: persona "Ana" and an open conversation started with a first message (one queued chat run).
func chatFixture(t *testing.T) (*env, domain.Persona, service.ConversationView, domain.Run) {
	t.Helper()
	ctx := context.Background()
	e := newEnv(t)
	p := e.persona(t, "Ana", nil)
	e.jobs.reset()
	cv, err := e.svc.CreateConversation(ctx, domain.HumanActor, e.co.ID, service.CreateConversationInput{PersonaID: p.ID, Message: "Oi Ana, como está o deploy?"})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if cv.ActiveRunID == nil {
		t.Fatalf("no active run after the first message: %+v", cv)
	}
	runs, err := e.svc.ListRuns(ctx, e.co.ID, service.RunFilter{ConversationID: &cv.ID})
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs: %v %v", runs, err)
	}
	return e, p, cv, runs[0].Run
}

// answer plays the executor for the run: start, then finish with a summary.
func (e *env) answer(t *testing.T, runID domain.ID, status domain.RunStatus, res adapter.Result) domain.Run {
	t.Helper()
	ctx := context.Background()
	if _, err := e.svc.MarkRunStarted(ctx, e.co.ID, runID, "tr", ""); err != nil {
		t.Fatalf("start: %v", err)
	}
	r, err := e.svc.FinishRun(ctx, e.co.ID, runID, service.FinishRunInput{Status: status, Result: res})
	if err != nil {
		t.Fatalf("finish: %v", err)
	}
	return r
}

func TestConversationFirstMessageOpensChatRun(t *testing.T) {
	ctx := context.Background()
	e, p, cv, run := chatFixture(t)

	if run.Trigger != domain.TriggerChat || run.ConversationID == nil || *run.ConversationID != cv.ID || run.Status != domain.RunQueued {
		t.Fatalf("run: %+v", run)
	}
	if run.TaskID != (domain.ID{}) || run.PersonaID != p.ID {
		t.Fatalf("a chat run has no task: %+v", run)
	}
	if cv.Title != "Oi Ana, como está o deploy?" || cv.Status != domain.ConversationOpen || cv.PersonaName != "Ana" {
		t.Fatalf("conversation: %+v", cv)
	}
	if len(e.jobs.execs) != 1 || e.jobs.execs[0] != run.ID {
		t.Fatalf("execute_run not enqueued: %v", e.jobs.execs)
	}
	if e.jobs.wakeCount() != 0 {
		t.Fatalf("a chat message must not wake a task: %d", e.jobs.wakeCount())
	}
	msgs, err := e.svc.ListConversationMessages(ctx, e.co.ID, cv.ID, 0)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("messages: %v %v", msgs, err)
	}
	if m := msgs[0]; m.Kind != domain.MessageHuman || m.RunID == nil || *m.RunID != run.ID || m.Body != "Oi Ana, como está o deploy?" {
		t.Fatalf("first message should be claimed by the run: %+v", m)
	}
	// the run is visible as a chat run in the runs list, with the conversation instead of a task
	rv, err := e.svc.GetRun(ctx, e.co.ID, run.ID)
	if err != nil || rv.ConversationTitle != cv.Title || rv.TaskKey != "" {
		t.Fatalf("run view: %+v %v", rv, err)
	}
}

func TestChatRunContextAndReply(t *testing.T) {
	ctx := context.Background()
	e, _, cv, run := chatFixture(t)

	rc, err := e.svc.LoadRunContext(ctx, e.co.ID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rc.Chat == nil || len(rc.Chat.New) != 1 || len(rc.Chat.History) != 0 || rc.Chat.ResumeSession != "" || rc.Task.ID != (domain.ID{}) {
		t.Fatalf("chat context: %+v", rc.Chat)
	}

	e.answer(t, run.ID, domain.RunDone, adapter.Result{Summary: "O deploy está **verde**.", SessionRef: "sess-1", CostUSD: ptr(0.25)})
	msgs, _ := e.svc.ListConversationMessages(ctx, e.co.ID, cv.ID, 0)
	if len(msgs) != 2 || msgs[1].Kind != domain.MessagePersona || msgs[1].Body != "O deploy está **verde**." || msgs[1].RunID == nil || *msgs[1].RunID != run.ID {
		t.Fatalf("reply: %+v", msgs)
	}
	got, _ := e.svc.GetConversation(ctx, e.co.ID, cv.ID)
	if got.ActiveRunID != nil || got.PendingMessages != 0 || got.CostUSD != 0.25 {
		t.Fatalf("conversation after the reply: %+v", got)
	}

	// second message: a new run that resumes the session of the first
	if _, err := e.svc.SendMessage(ctx, domain.HumanActor, e.co.ID, cv.ID, "E o banco?"); err != nil {
		t.Fatal(err)
	}
	runs, _ := e.svc.ListRuns(ctx, e.co.ID, service.RunFilter{ConversationID: &cv.ID, Statuses: []domain.RunStatus{domain.RunQueued}})
	if len(runs) != 1 {
		t.Fatalf("queued runs: %d", len(runs))
	}
	rc, err = e.svc.LoadRunContext(ctx, e.co.ID, runs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if rc.Chat.ResumeSession != "sess-1" || len(rc.Chat.New) != 1 || rc.Chat.New[0].Body != "E o banco?" || len(rc.Chat.History) != 2 {
		t.Fatalf("second run context: %+v", rc.Chat)
	}
}

func TestChatMessagesDuringARunAreCoalesced(t *testing.T) {
	ctx := context.Background()
	e, _, cv, run := chatFixture(t)
	if _, err := e.svc.MarkRunStarted(ctx, e.co.ID, run.ID, "tr", ""); err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"segunda", "terceira"} {
		if _, err := e.svc.SendMessage(ctx, domain.HumanActor, e.co.ID, cv.ID, m); err != nil {
			t.Fatal(err)
		}
	}
	if len(e.jobs.execs) != 1 {
		t.Fatalf("no new run while one is active: %v", e.jobs.execs)
	}
	got, _ := e.svc.GetConversation(ctx, e.co.ID, cv.ID)
	if got.PendingMessages != 2 || got.ActiveRunID == nil {
		t.Fatalf("pending: %+v", got)
	}

	if _, err := e.svc.FinishRun(ctx, e.co.ID, run.ID, service.FinishRunInput{Status: domain.RunDone, Result: adapter.Result{Summary: "primeira resposta"}}); err != nil {
		t.Fatal(err)
	}
	// one single follow-up run takes both waiting messages
	if len(e.jobs.execs) != 2 {
		t.Fatalf("follow-up run not enqueued: %v", e.jobs.execs)
	}
	rc, err := e.svc.LoadRunContext(ctx, e.co.ID, e.jobs.execs[1])
	if err != nil {
		t.Fatal(err)
	}
	if len(rc.Chat.New) != 2 || rc.Chat.New[0].Body != "segunda" || rc.Chat.New[1].Body != "terceira" {
		t.Fatalf("new messages: %+v", rc.Chat.New)
	}
	if len(rc.Chat.History) != 2 || rc.Chat.History[1].Body != "primeira resposta" {
		t.Fatalf("history: %+v", rc.Chat.History)
	}
	got, _ = e.svc.GetConversation(ctx, e.co.ID, cv.ID)
	if got.PendingMessages != 0 {
		t.Fatalf("pending after the follow-up: %d", got.PendingMessages)
	}
}

func TestChatFailureCancelAndArchive(t *testing.T) {
	ctx := context.Background()
	e, _, cv, run := chatFixture(t)

	e.answer(t, run.ID, domain.RunFailed, adapter.Result{IsError: true, Error: "harness caiu", ExitCode: 1})
	msgs, _ := e.svc.ListConversationMessages(ctx, e.co.ID, cv.ID, 0)
	last := msgs[len(msgs)-1]
	if last.Kind != domain.MessageSystem || !strings.Contains(last.Body, "harness caiu") {
		t.Fatalf("failure notice: %+v", last)
	}

	// a queued chat run cancelled by the user leaves a notice and starts nothing else
	if _, err := e.svc.SendMessage(ctx, domain.HumanActor, e.co.ID, cv.ID, "tenta de novo"); err != nil {
		t.Fatal(err)
	}
	queued, _ := e.svc.ListRuns(ctx, e.co.ID, service.RunFilter{ConversationID: &cv.ID, Statuses: []domain.RunStatus{domain.RunQueued}})
	if len(queued) != 1 {
		t.Fatalf("queued: %d", len(queued))
	}
	if _, err := e.svc.CancelRun(ctx, domain.HumanActor, e.co.ID, queued[0].ID); err != nil {
		t.Fatal(err)
	}
	msgs, _ = e.svc.ListConversationMessages(ctx, e.co.ID, cv.ID, 0)
	if last = msgs[len(msgs)-1]; last.Kind != domain.MessageSystem || !strings.Contains(last.Body, "cancelada") {
		t.Fatalf("cancel notice: %+v", last)
	}
	if got, _ := e.svc.GetConversation(ctx, e.co.ID, cv.ID); got.ActiveRunID != nil {
		t.Fatalf("active run after cancel: %+v", got)
	}

	// archived conversations refuse messages until reopened
	if _, err := e.svc.SetConversationStatus(ctx, domain.HumanActor, e.co.ID, cv.ID, domain.ConversationArchived); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.SendMessage(ctx, domain.HumanActor, e.co.ID, cv.ID, "oi?")
	wantErr(t, err, domain.ErrInvalid)
	if _, err := e.svc.SetConversationStatus(ctx, domain.HumanActor, e.co.ID, cv.ID, domain.ConversationOpen); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SendMessage(ctx, domain.HumanActor, e.co.ID, cv.ID, "voltei"); err != nil {
		t.Fatal(err)
	}
	if n := countVerb(t, e, "conversation.archived"); n != 1 {
		t.Fatalf("archived activity = %d", n)
	}
}

func TestChatBlockedPersona(t *testing.T) {
	ctx := context.Background()
	e, p, cv, run := chatFixture(t)
	e.answer(t, run.ID, domain.RunDone, adapter.Result{Summary: "ok"})

	if _, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, p.ID, service.UpdatePersonaInput{Status: ptr(domain.PersonaPaused)}); err != nil {
		t.Fatal(err)
	}
	before, _ := e.svc.ListConversationMessages(ctx, e.co.ID, cv.ID, 0)
	_, err := e.svc.SendMessage(ctx, domain.HumanActor, e.co.ID, cv.ID, "ainda está aí?")
	wantErr(t, err, domain.ErrPersonaUnavailable)
	wantErr(t, err, domain.ErrConflict)
	if !strings.Contains(err.Error(), "pausada") || !strings.Contains(err.Error(), "Ana") {
		t.Fatalf("the reason must say who and why: %v", err)
	}
	after, _ := e.svc.ListConversationMessages(ctx, e.co.ID, cv.ID, 0)
	if len(after) != len(before) {
		t.Fatalf("a refused message must not be stored: %d -> %d", len(before), len(after))
	}

	// no empty conversation is left behind either
	list, _ := e.svc.ListConversations(ctx, e.co.ID, service.ConversationFilter{})
	_, err = e.svc.CreateConversation(ctx, domain.HumanActor, e.co.ID, service.CreateConversationInput{PersonaID: p.ID, Message: "nova"})
	wantErr(t, err, domain.ErrPersonaUnavailable)
	if again, _ := e.svc.ListConversations(ctx, e.co.ID, service.ConversationFilter{}); len(again) != len(list) {
		t.Fatalf("conversations: %d -> %d", len(list), len(again))
	}
}

func TestChatBudgetHardStop(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := e.persona(t, "Eng", func(in *service.HirePersonaInput) { in.BudgetMonthlyCents = ptr(int64(1000)) }) // US$ 10
	march := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	e.spend(t, p, march, nil, usd(10.5)) // over the budget; sets e.svc.Now

	_, err := e.svc.CreateConversation(ctx, domain.HumanActor, e.co.ID, service.CreateConversationInput{PersonaID: p.ID, Message: "oi"})
	wantErr(t, err, domain.ErrBudgetExceeded)
	got, _ := e.svc.GetPersona(ctx, e.co.ID, p.ID.String())
	if got.Status != domain.PersonaPaused {
		t.Fatalf("the refusal should pause the persona like any checkout: %s", got.Status)
	}
	if runs, _ := e.svc.ListRuns(ctx, e.co.ID, service.RunFilter{Statuses: []domain.RunStatus{domain.RunQueued, domain.RunRunning}}); len(runs) != 0 {
		t.Fatalf("a run was opened: %+v", runs)
	}
}

func TestChatCostsAreIdentifiedAsChat(t *testing.T) {
	ctx := context.Background()
	e, _, _, run := chatFixture(t)
	e.answer(t, run.ID, domain.RunDone, adapter.Result{Summary: "ok", CostUSD: ptr(1.5)})

	cs, err := e.svc.CostSummary(ctx, e.co.ID, service.CostFilter{GroupBy: service.GroupProject})
	if err != nil {
		t.Fatal(err)
	}
	if cs.Runs != 1 || !approx(cs.TotalUSD, 1.5) || len(cs.Rows) != 1 || cs.Rows[0].Key != "chat" || cs.Rows[0].Label != "Chat" {
		t.Fatalf("cost by project: %+v", cs)
	}
	if mc, _ := e.svc.MonthSpend(ctx, e.co.ID); !approx(mc, 1.5) {
		t.Fatalf("month spend = %v", mc)
	}
}

func TestChatRoles(t *testing.T) {
	e := newEnv(t)
	p := e.persona(t, "Ana", nil)
	viewer := mkUser(t, e, "v@x.com", domain.RoleMember)
	member := mkUser(t, e, "m@x.com", domain.RoleMember)
	join(t, e, viewer, domain.MemberViewer)
	join(t, e, member, domain.MemberMember)
	outsider := mkUser(t, e, "o@x.com", domain.RoleMember)

	cv, err := e.svc.CreateConversation(as(member), domain.HumanActor, e.co.ID, service.CreateConversationInput{PersonaID: p.ID, Message: "oi"})
	if err != nil {
		t.Fatalf("member creates: %v", err)
	}
	// every member reads, only member+ writes
	if _, err := e.svc.GetConversation(as(viewer), e.co.ID, cv.ID); err != nil {
		t.Fatalf("viewer reads: %v", err)
	}
	if ms, err := e.svc.ListConversationMessages(as(viewer), e.co.ID, cv.ID, 0); err != nil || len(ms) != 1 {
		t.Fatalf("viewer reads messages: %v %v", ms, err)
	}
	if l, err := e.svc.ListConversations(as(viewer), e.co.ID, service.ConversationFilter{}); err != nil || len(l) != 1 {
		t.Fatalf("viewer lists: %v %v", l, err)
	}
	_, err = e.svc.SendMessage(as(viewer), domain.HumanActor, e.co.ID, cv.ID, "posso?")
	wantErr(t, err, domain.ErrForbidden)
	_, err = e.svc.CreateConversation(as(viewer), domain.HumanActor, e.co.ID, service.CreateConversationInput{PersonaID: p.ID, Message: "x"})
	wantErr(t, err, domain.ErrForbidden)
	_, err = e.svc.SetConversationStatus(as(viewer), domain.HumanActor, e.co.ID, cv.ID, domain.ConversationArchived)
	wantErr(t, err, domain.ErrForbidden)
	// a non-member does not even see the company
	_, err = e.svc.GetConversation(as(outsider), e.co.ID, cv.ID)
	wantErr(t, err, domain.ErrNotFound)
	// a persona never writes in a conversation through the service
	_, err = e.svc.SendMessage(context.Background(), domain.PersonaActor(p.ID), e.co.ID, cv.ID, "eu mesma")
	wantErr(t, err, domain.ErrNotAllowed)
}

func TestChatValidation(t *testing.T) {
	ctx := context.Background()
	e, p, cv, _ := chatFixture(t)
	for name, f := range map[string]func() error{
		"empty message": func() error {
			_, err := e.svc.SendMessage(ctx, domain.HumanActor, e.co.ID, cv.ID, "  \n ")
			return err
		},
		"huge message": func() error {
			_, err := e.svc.SendMessage(ctx, domain.HumanActor, e.co.ID, cv.ID, strings.Repeat("a", domain.MaxMessageBytes+1))
			return err
		},
		"no title and no message": func() error {
			_, err := e.svc.CreateConversation(ctx, domain.HumanActor, e.co.ID, service.CreateConversationInput{PersonaID: p.ID})
			return err
		},
		"long title": func() error {
			_, err := e.svc.CreateConversation(ctx, domain.HumanActor, e.co.ID, service.CreateConversationInput{PersonaID: p.ID, Title: strings.Repeat("t", 201)})
			return err
		},
		"bad status filter": func() error {
			_, err := e.svc.ListConversations(ctx, e.co.ID, service.ConversationFilter{Status: "x"})
			return err
		},
	} {
		if err := f(); !isInvalid(err) {
			t.Errorf("%s: err = %v, want invalid", name, err)
		}
	}
	_, err := e.svc.SendMessage(ctx, domain.HumanActor, e.co.ID, domain.NewID(), "oi")
	wantErr(t, err, domain.ErrNotFound)
	_, err = e.svc.CreateConversation(ctx, domain.HumanActor, e.co.ID, service.CreateConversationInput{PersonaID: domain.NewID(), Title: "x"})
	wantErr(t, err, domain.ErrNotFound)

	// an empty conversation (title only) opens no run
	n := len(e.jobs.execs)
	cv2, err := e.svc.CreateConversation(ctx, domain.HumanActor, e.co.ID, service.CreateConversationInput{PersonaID: p.ID, Title: "Planejamento"})
	if err != nil || cv2.ActiveRunID != nil || len(e.jobs.execs) != n {
		t.Fatalf("title-only conversation: %+v %v", cv2, err)
	}
}

func TestChatInterruptedOnRestart(t *testing.T) {
	ctx := context.Background()
	e, _, cv, run := chatFixture(t)
	if _, err := e.svc.MarkRunStarted(ctx, e.co.ID, run.ID, "tr", ""); err != nil {
		t.Fatal(err)
	}
	n, err := e.svc.InterruptOrphanRuns(ctx)
	if err != nil || n != 1 {
		t.Fatalf("interrupt: %d %v", n, err)
	}
	msgs, _ := e.svc.ListConversationMessages(ctx, e.co.ID, cv.ID, 0)
	if last := msgs[len(msgs)-1]; last.Kind != domain.MessageSystem || !strings.Contains(last.Body, "interrompida") {
		t.Fatalf("notice: %+v", last)
	}
	if e.jobs.wakeCount() != 0 {
		t.Fatal("a chat has no task to wake")
	}
}

func TestChatRunsStayOutOfTaskPaths(t *testing.T) {
	ctx := context.Background()
	e, p, _, run := chatFixture(t)
	e.answer(t, run.ID, domain.RunDone, adapter.Result{Summary: "ok"})

	// the inbox, the dashboard and the exports ignore runs without task
	if in, err := e.svc.Inbox(ctx, e.co.ID); err != nil || len(in.FailedRuns) != 0 {
		t.Fatalf("inbox: %+v %v", in.FailedRuns, err)
	}
	if _, err := e.svc.Dashboard(ctx, e.co.ID); err != nil {
		t.Fatal(err)
	}
	doc, err := e.svc.ExportCompany(ctx, e.co.ID, service.ExportOptions{WithRuns: true, WithLogs: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Runs) != 0 {
		t.Fatalf("runs without a task are not exported: %+v", doc.Runs)
	}
	// the persona keeps working on tasks next to its conversations
	tv := e.task(t, service.CreateTaskInput{Title: "coisa", PersonaID: &p.ID})
	if _, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign)); err != nil {
		t.Fatal(err)
	}
}
