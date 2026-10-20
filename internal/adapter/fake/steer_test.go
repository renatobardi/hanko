package fake

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
)

func TestSteerChangesCourse(t *testing.T) {
	ch := make(chan adapter.Steer, 1)
	acked := make(chan error, 1)
	ch <- adapter.Steer{ID: "f1", Text: "foque nos testes", Ack: func(err error) { acked <- err }}
	r := &rec{}
	spec := adapter.Spec{RunID: domain.NewID(), TaskKey: "HNK-9", Steer: ch, Config: json.RawMessage(`{"delay_ms":5,"hold_for_steer_ms":2000}`)}
	res, err := New(Options{}).Run(context.Background(), spec, r)
	if err != nil || res.IsError {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	select {
	case err := <-acked:
		if err != nil {
			t.Fatalf("ack: %v", err)
		}
	default:
		t.Fatal("steer not acknowledged")
	}
	if want := "Rumo alterado pelo operador: foque nos testes"; !strings.Contains(res.Summary, want) {
		t.Fatalf("summary does not carry the new direction: %q", res.Summary)
	}
	found := false
	for _, e := range r.ev {
		if e.Kind == domain.EventAssistant && strings.HasPrefix(e.Text, "Rumo alterado pelo operador") {
			found = true
		}
	}
	if !found {
		t.Fatal("log lacks the steer event")
	}
	if adapter.SteerModeOf(New(Options{})) != domain.SteerInject {
		t.Fatal("fake must inject")
	}
}

func TestSteerHoldGivesUp(t *testing.T) {
	spec := adapter.Spec{TaskKey: "X-1", Steer: make(chan adapter.Steer), Config: json.RawMessage(`{"delay_ms":1,"hold_for_steer_ms":20}`)}
	res, err := New(Options{}).Run(context.Background(), spec, &rec{})
	if err != nil || res.IsError || strings.Contains(res.Summary, "Rumo alterado") {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestResumedSessionIsVisibleInSummary(t *testing.T) {
	res, _ := New(Options{Delay: -1}).Run(context.Background(), adapter.Spec{TaskKey: "X-1", SessionRef: "fake-old"}, &rec{})
	if !strings.Contains(res.Summary, "sessão retomada") {
		t.Fatalf("summary = %q", res.Summary)
	}
}
