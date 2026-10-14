package fake

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
)

type rec struct{ ev []adapter.Event }

func (r *rec) Emit(_ context.Context, e adapter.Event) error { r.ev = append(r.ev, e); return nil }

func TestRunDeterministic(t *testing.T) {
	r := &rec{}
	spec := adapter.Spec{RunID: domain.NewID(), TaskKey: "HNK-7"}
	res, err := New(Options{Delay: -1}).Run(context.Background(), spec, r)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []domain.RunEventKind
	for _, e := range r.ev {
		kinds = append(kinds, e.Kind)
	}
	want := []domain.RunEventKind{domain.EventSystem, domain.EventAssistant, domain.EventToolUse, domain.EventToolResult, domain.EventAssistant, domain.EventResult}
	if diff := cmp.Diff(want, kinds); diff != "" {
		t.Fatal(diff)
	}
	if r.ev[1].Text != "Analisando HNK-7…" || res.IsError || res.CostUSD == nil || *res.CostUSD != 0 || res.Summary != r.ev[5].Text {
		t.Fatalf("res=%+v ev=%+v", res, r.ev)
	}
	r2 := &rec{}
	res2, _ := New(Options{Delay: -1}).Run(context.Background(), spec, r2)
	if res2.Summary != res.Summary || res2.SessionRef != res.SessionRef {
		t.Fatal("not deterministic")
	}
}

func TestRunFailConfig(t *testing.T) {
	res, err := New(Options{Delay: -1}).Run(context.Background(), adapter.Spec{TaskKey: "X-1", Config: json.RawMessage(`{"fail":true}`)}, &rec{})
	if err != nil || !res.IsError || res.ExitCode != 1 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if _, err := New(Options{}).Run(context.Background(), adapter.Spec{Config: json.RawMessage(`{`)}, &rec{}); err == nil {
		t.Fatal("want error")
	}
}

func TestRunCancel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	cfg := json.RawMessage(`{"delay_ms":5000}`)
	t0 := time.Now()
	res, err := New(Options{}).Run(ctx, adapter.Spec{TaskKey: "X-1", Config: cfg}, &rec{})
	if err != nil || !res.IsError || time.Since(t0) > 2*time.Second {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}
