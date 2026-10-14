package api

import (
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/renatobardi/hanko/internal/domain"
)

func TestParseTaskStatuses(t *testing.T) {
	tests := []struct {
		in      string
		want    []domain.TaskStatus
		wantErr bool
	}{
		{"", nil, false},
		{"todo", []domain.TaskStatus{domain.TaskTodo}, false},
		{"todo, in_progress,,blocked", []domain.TaskStatus{domain.TaskTodo, domain.TaskInProgress, domain.TaskBlocked}, false},
		{"todo,xyz", nil, true},
	}
	for _, tc := range tests {
		got, err := parseTaskStatuses(tc.in)
		if (err != nil) != tc.wantErr || (err != nil && !errors.Is(err, domain.ErrInvalid)) {
			t.Fatalf("%q: err = %v", tc.in, err)
		}
		if diff := cmp.Diff(tc.want, got); diff != "" {
			t.Fatalf("%q: %s", tc.in, diff)
		}
	}
}

func TestParseRunStatuses(t *testing.T) {
	got, err := parseRunStatuses("queued,running")
	if err != nil || len(got) != 2 || got[1] != domain.RunRunning {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := parseRunStatuses("done,nope"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("esperava ErrInvalid, veio %v", err)
	}
}

func TestParseTime(t *testing.T) {
	if got, err := parseTime("before", ""); got != nil || err != nil {
		t.Fatalf("vazio: %v %v", got, err)
	}
	got, err := parseTime("before", "2026-10-13T10:00:00Z")
	if err != nil || !got.Equal(time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := parseTime("before", "ontem"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("esperava ErrInvalid, veio %v", err)
	}
}

func TestListOfPagina(t *testing.T) {
	out := listOf([]int{1, 2, 3, 4, 5}, Page{Limit: 2, Offset: 3}, func(i int) int { return i * 10 })
	if diff := cmp.Diff([]int{40, 50}, out.Body.Items); diff != "" {
		t.Fatal(diff)
	}
	out = listOf([]int{1}, Page{Limit: 5, Offset: 9}, func(i int) int { return i })
	if out.Body.Items == nil || len(out.Body.Items) != 0 {
		t.Fatalf("esperava slice vazio não-nulo, veio %#v", out.Body.Items)
	}
}
