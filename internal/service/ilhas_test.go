package service_test

import (
	"context"
	"testing"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// TestListRunEventsBefore: o cursor por seq anda para trás sem perder nem repetir eventos.
func TestListRunEventsBefore(t *testing.T) {
	ctx := context.Background()
	e, p, tv := runFixture(t)
	run, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.MarkRunStarted(ctx, e.co.ID, run.ID, "t", ""); err != nil {
		t.Fatal(err)
	}
	const total = 25
	for i := 0; i < total; i++ {
		if _, err := e.svc.AppendRunEvent(ctx, e.co.ID, run.ID, adapter.Event{Kind: domain.EventStdout, Text: "linha"}); err != nil {
			t.Fatal(err)
		}
	}

	// from the end: the last page, ascending
	page, err := e.svc.ListRunEventsBefore(ctx, e.co.ID, run.ID, 0, 10)
	if err != nil || len(page) != 10 || page[0].Seq != 16 || page[9].Seq != total {
		t.Fatalf("tail page: %v %v", seqs(page), err)
	}
	// walking backwards covers every event exactly once
	seen := map[int64]bool{}
	for _, ev := range page {
		seen[ev.Seq] = true
	}
	cursor := page[0].Seq
	for cursor > 1 {
		older, err := e.svc.ListRunEventsBefore(ctx, e.co.ID, run.ID, cursor, 10)
		if err != nil || len(older) == 0 {
			t.Fatalf("older before %d: %v %v", cursor, seqs(older), err)
		}
		for i, ev := range older {
			if seen[ev.Seq] || ev.Seq >= cursor || (i > 0 && ev.Seq <= older[i-1].Seq) {
				t.Fatalf("page before %d repeats or is out of order: %v", cursor, seqs(older))
			}
			seen[ev.Seq] = true
		}
		cursor = older[0].Seq
	}
	if len(seen) != total {
		t.Fatalf("saw %d events, want %d", len(seen), total)
	}
	// nothing before the first event
	if none, err := e.svc.ListRunEventsBefore(ctx, e.co.ID, run.ID, 1, 10); err != nil || len(none) != 0 {
		t.Fatalf("before 1: %v %v", seqs(none), err)
	}
	// the forward cursor continues where the tail page ended (live events after the initial load)
	if _, err := e.svc.AppendRunEvent(ctx, e.co.ID, run.ID, adapter.Event{Kind: domain.EventStdout, Text: "nova"}); err != nil {
		t.Fatal(err)
	}
	fwd, err := e.svc.ListRunEvents(ctx, e.co.ID, run.ID, page[9].Seq, 10)
	if err != nil || len(fwd) != 1 || fwd[0].Seq != total+1 {
		t.Fatalf("forward: %v %v", seqs(fwd), err)
	}
	// events of another run (or company) never leak into the page
	if foreign, err := e.svc.ListRunEventsBefore(ctx, e.co.ID, domain.NewID(), 0, 10); err != nil || len(foreign) != 0 {
		t.Fatalf("foreign run: %v %v", seqs(foreign), err)
	}
}

func seqs(es []domain.RunEvent) []int64 {
	out := make([]int64, len(es))
	for i, e := range es {
		out[i] = e.Seq
	}
	return out
}

// TestAnchoredComments: âncora na descrição e no resumo de uma run; reencontrada quando o texto muda; perdida quando some.
func TestAnchoredComments(t *testing.T) {
	ctx := context.Background()
	e, p, tv := runFixture(t)
	desc := "Implemente o login com e-mail.\nDepois adicione o login com OAuth.\nPor fim, documente."
	if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tv.ID, service.UpdateTaskInput{Description: &desc}); err != nil {
		t.Fatal(err)
	}

	// comment anchored to the description: the server checks the quote and fills offsets and hash
	c, err := e.svc.AddComment(ctx, domain.HumanActor, e.co.ID, tv.ID, service.AddCommentInput{Body: "Qual provedor?",
		Anchor: &domain.Anchor{Target: domain.AnchorTaskDescription, Quote: "login com OAuth", Start: 0}})
	if err != nil {
		t.Fatal(err)
	}
	wantStart := len([]rune("Implemente o login com e-mail.\nDepois adicione o "))
	if c.Anchor == nil || c.Anchor.Start != wantStart || c.Anchor.End != wantStart+len([]rune("login com OAuth")) || c.Anchor.DocHash != domain.DocHash(desc) {
		t.Fatalf("anchor not canonical: %+v", c.Anchor)
	}
	if c.AnchorState == nil || c.AnchorState.Status != domain.AnchorExact {
		t.Fatalf("state: %+v", c.AnchorState)
	}

	// invalid anchors are rejected and nothing is stored
	for name, a := range map[string]*domain.Anchor{
		"quote not in document": {Target: domain.AnchorTaskDescription, Quote: "texto que não existe"},
		"empty quote":           {Target: domain.AnchorTaskDescription},
		"unknown target":        {Target: "other", Quote: "login"},
		"run target without id": {Target: domain.AnchorRunSummary, Quote: "login"},
		"run that does not exist": {Target: domain.AnchorRunSummary, Quote: "login",
			RunID: func() *domain.ID { id := domain.NewID(); return &id }()},
	} {
		_, err := e.svc.AddComment(ctx, domain.HumanActor, e.co.ID, tv.ID, service.AddCommentInput{Body: name, Anchor: a})
		if err == nil {
			t.Errorf("%s: expected an error", name)
			continue
		}
		wantErr(t, err, domain.ErrInvalid)
	}

	// the delivery (run summary) is a valid target
	run, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.MarkRunStarted(ctx, e.co.ID, run.ID, "t", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.FinishRun(ctx, e.co.ID, run.ID, service.FinishRunInput{Status: domain.RunDone,
		Result: adapter.Result{Summary: "Criei o endpoint /login. Falta o teste de integração."}}); err != nil {
		t.Fatal(err)
	}
	rc, err := e.svc.AddComment(ctx, domain.HumanActor, e.co.ID, tv.ID, service.AddCommentInput{Body: "Escreva o teste.",
		Anchor: &domain.Anchor{Target: domain.AnchorRunSummary, RunID: &run.ID, Quote: "teste de integração"}})
	if err != nil || rc.Anchor == nil || rc.Anchor.RunID == nil || *rc.Anchor.RunID != run.ID {
		t.Fatalf("run anchor: %+v %v", rc.Anchor, err)
	}
	// a run of another task cannot be the target
	other := e.task(t, service.CreateTaskInput{Title: "Outra", PersonaID: &p.ID})
	_, err = e.svc.AddComment(ctx, domain.HumanActor, e.co.ID, other.ID, service.AddCommentInput{Body: "x",
		Anchor: &domain.Anchor{Target: domain.AnchorRunSummary, RunID: &run.ID, Quote: "endpoint"}})
	wantErr(t, err, domain.ErrInvalid)

	// plain comments stay plain
	plain, err := e.svc.AddComment(ctx, domain.HumanActor, e.co.ID, tv.ID, service.AddCommentInput{Body: "sem âncora"})
	if err != nil || plain.Anchor != nil || plain.AnchorState != nil {
		t.Fatalf("plain: %+v %v", plain, err)
	}

	state := func() map[string]string {
		cs, err := e.svc.ListComments(ctx, e.co.ID, tv.ID)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, c := range cs {
			if c.Anchor == nil {
				out[c.Body] = "-"
				continue
			}
			out[c.Body] = c.AnchorState.Status
		}
		return out
	}
	if got := state(); got["Qual provedor?"] != domain.AnchorExact || got["Escreva o teste."] != domain.AnchorExact || got["sem âncora"] != "-" {
		t.Fatalf("before edit: %v", got)
	}

	// the description changes: the quote moves (found again) ...
	moved := "Texto novo no início.\n" + desc
	if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tv.ID, service.UpdateTaskInput{Description: &moved}); err != nil {
		t.Fatal(err)
	}
	cs, _ := e.svc.ListComments(ctx, e.co.ID, tv.ID)
	for _, c := range cs {
		if c.Body == "Qual provedor?" {
			shift := len([]rune("Texto novo no início.\n"))
			if c.AnchorState.Status != domain.AnchorMoved || c.AnchorState.Start != wantStart+shift {
				t.Fatalf("moved: %+v", c.AnchorState)
			}
		}
	}
	// ... and then disappears: "âncora perdida"
	gone := "Reescrita completa sem o trecho citado."
	if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tv.ID, service.UpdateTaskInput{Description: &gone}); err != nil {
		t.Fatal(err)
	}
	if got := state(); got["Qual provedor?"] != domain.AnchorLost || got["Escreva o teste."] != domain.AnchorExact {
		t.Fatalf("after edit: %v", got)
	}
}
