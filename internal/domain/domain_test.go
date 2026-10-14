package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestSlugify(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Acme Corp", "acme-corp"},
		{"  Ação & Coração  ", "acao-coracao"},
		{"Olá, Mundo!", "ola-mundo"},
		{"já é ôníbus úteis", "ja-e-onibus-uteis"},
		{"---x---", "x"},
		{"", ""},
		{"!!!", ""},
		{"a__b", "a-b"},
		{strings.Repeat("a", 100), strings.Repeat("a", 63)},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := Slugify(tt.in); got != tt.want {
				t.Errorf("Slugify(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestValidSlug(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{{"acme", true}, {"a-b-1", true}, {"", false}, {"-a", false}, {"A", false}, {"a b", false}, {strings.Repeat("a", 64), false}}
	for _, tt := range tests {
		if got := ValidSlug(tt.in); got != tt.want {
			t.Errorf("ValidSlug(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestTaskKey(t *testing.T) {
	if got := TaskKey("HNK", 42); got != "HNK-42" {
		t.Fatalf("TaskKey = %q", got)
	}
}

func TestParseTaskKey(t *testing.T) {
	tests := []struct {
		in     string
		prefix string
		num    int64
		ok     bool
	}{
		{"HNK-12", "HNK", 12, true},
		{" hnk-7 ", "HNK", 7, true},
		{"A1-100", "A1", 100, true},
		{"HNK12", "", 0, false},
		{"HNK-", "", 0, false},
		{"1HNK-3", "", 0, false},
		{"", "", 0, false},
		{"HNK-99999999999999999999", "", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			p, n, ok := ParseTaskKey(tt.in)
			if ok != tt.ok || (ok && (p != tt.prefix || n != tt.num)) {
				t.Errorf("ParseTaskKey(%q) = %q,%d,%v", tt.in, p, n, ok)
			}
		})
	}
}

func TestCanTransition(t *testing.T) {
	tests := []struct {
		from, to TaskStatus
		want     bool
	}{
		{TaskBacklog, TaskTodo, true},
		{TaskTodo, TaskInProgress, true},
		{TaskInProgress, TaskDone, true},
		{TaskInReview, TaskBlocked, true},
		{TaskTodo, TaskTodo, false},
		{TaskTodo, "bogus", false},
		{TaskDone, TaskTodo, true},
		{TaskDone, TaskInProgress, false},
		{TaskCancelled, TaskTodo, true},
		{TaskCancelled, TaskDone, false},
	}
	for _, tt := range tests {
		if got := tt.from.CanTransition(tt.to); got != tt.want {
			t.Errorf("%s → %s = %v, want %v", tt.from, tt.to, got, tt.want)
		}
	}
}

func TestValidAndPredicates(t *testing.T) {
	for _, s := range []TaskStatus{TaskBacklog, TaskTodo, TaskInProgress, TaskInReview, TaskBlocked, TaskDone, TaskCancelled} {
		if !s.Valid() {
			t.Errorf("task status %q should be valid", s)
		}
	}
	if TaskStatus("x").Valid() {
		t.Error("bogus task status valid")
	}
	if !TaskTodo.Workable() || !TaskInProgress.Workable() || TaskBacklog.Workable() || TaskDone.Workable() {
		t.Error("Workable wrong")
	}
	if !TaskBlocked.Open() || TaskDone.Open() || TaskCancelled.Open() {
		t.Error("Open wrong")
	}
	for _, p := range []Priority{PriorityCritical, PriorityHigh, PriorityMedium, PriorityLow} {
		if !p.Valid() {
			t.Errorf("priority %q should be valid", p)
		}
	}
	if Priority("x").Valid() {
		t.Error("bogus priority valid")
	}
	for _, s := range []PersonaStatus{PersonaActive, PersonaIdle, PersonaRunning, PersonaPaused, PersonaError, PersonaAwaitingGate, PersonaOff} {
		if !s.Valid() {
			t.Errorf("persona status %q should be valid", s)
		}
	}
	if PersonaStatus("x").Valid() {
		t.Error("bogus persona status valid")
	}
	for s, want := range map[PersonaStatus]bool{PersonaActive: true, PersonaIdle: true, PersonaRunning: true, PersonaPaused: false, PersonaError: false, PersonaOff: false} {
		if s.CanWork() != want {
			t.Errorf("%s CanWork = %v", s, !want)
		}
	}
	for _, s := range []RunStatus{RunQueued, RunRunning, RunDone, RunInterrupted, RunFailed, RunCancelled, RunExpired} {
		if !s.Valid() {
			t.Errorf("run status %q should be valid", s)
		}
		if s.Active() == s.Terminal() {
			t.Errorf("run status %q cannot be both/neither active and terminal", s)
		}
	}
	if RunStatus("x").Valid() {
		t.Error("bogus run status valid")
	}
	if !RunQueued.Active() || !RunRunning.Active() || RunDone.Active() {
		t.Error("Active wrong")
	}
	for _, a := range []ActorType{ActorHuman, ActorPersona, ActorSystem} {
		if !a.Valid() {
			t.Errorf("actor %q should be valid", a)
		}
	}
	if ActorType("x").Valid() {
		t.Error("bogus actor valid")
	}
}

func TestValidAgent(t *testing.T) {
	for _, a := range []string{"claude", "codex", "fake"} {
		if !ValidAgent(a) {
			t.Errorf("%q should be valid", a)
		}
	}
	if ValidAgent("gpt") || ValidAgent("") {
		t.Error("unknown agent accepted")
	}
}

func TestIDsAndActors(t *testing.T) {
	a, b := NewID(), NewID()
	if a == b || a.Version() != 7 {
		t.Fatalf("NewID not unique UUIDv7: %v %v", a, b)
	}
	got, err := ParseID(a.String())
	if err != nil || got != a {
		t.Fatalf("ParseID roundtrip: %v %v", got, err)
	}
	if _, err := ParseID("nope"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ParseID(bad) = %v, want ErrInvalid", err)
	}
	pa := PersonaActor(a)
	if pa.Type != ActorPersona || pa.ID == nil || *pa.ID != a {
		t.Fatalf("PersonaActor = %+v", pa)
	}
	if HumanActor.ID != nil || SystemActor.Type != ActorSystem {
		t.Fatal("static actors wrong")
	}
}

func TestValidationError(t *testing.T) {
	err := Invalid("name", "required")
	if !errors.Is(err, ErrInvalid) {
		t.Fatal("Invalid should unwrap to ErrInvalid")
	}
	if err.Error() != "name: required" {
		t.Fatalf("Error() = %q", err.Error())
	}
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Field != "name" {
		t.Fatal("errors.As ValidationError failed")
	}
}
