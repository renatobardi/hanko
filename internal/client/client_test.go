package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestErrorFormatacao(t *testing.T) {
	tests := []struct {
		e    *Error
		want string
	}{
		{&Error{Status: 404, Detail: "task não encontrada"}, "task não encontrada [HTTP 404]"},
		{&Error{Status: 422, Title: "Entrada inválida", Fields: []string{"title: vazio"}}, "Entrada inválida (title: vazio) [HTTP 422]"},
		{&Error{Status: 500}, "Internal Server Error [HTTP 500]"},
	}
	for _, tc := range tests {
		if got := tc.e.Error(); got != tc.want {
			t.Errorf("got %q, quer %q", got, tc.want)
		}
	}
}

func TestDoEnviaTokenEDecodificaErro(t *testing.T) {
	var gotAuth, gotCT string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotCT = r.Header.Get("Authorization"), r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(409)
		_, _ = w.Write([]byte(`{"title":"Conflito","status":409,"detail":"slug em uso"}`))
	}))
	defer ts.Close()
	c := New(ts.URL+"/", "tok")
	_, err := c.CreateCompany(context.Background(), CreateCompanyReq{Name: "x"})
	var ae *Error
	if !errors.As(err, &ae) || ae.Status != 409 || ae.Detail != "slug em uso" {
		t.Fatalf("err = %v", err)
	}
	if gotAuth != "Bearer tok" || gotCT != "application/json" {
		t.Fatalf("headers: %q %q", gotAuth, gotCT)
	}
}

func TestNewUsaPadrao(t *testing.T) {
	if c := New("", ""); c.BaseURL != DefaultURL {
		t.Fatalf("BaseURL = %q", c.BaseURL)
	}
}

func TestReadFrames(t *testing.T) {
	in := "retry: 3000\n\n" +
		": ping\n\n" +
		"id: 7\nevent: run.event\ndata: {\"a\":1}\n\n" +
		"data: linha1\ndata: linha2\n\n" +
		"event: run.status\ndata: {}\n\n" +
		"id: 99\nevent: run.event\ndata: nunca lido\n\n"
	var got []Frame
	err := readFrames(strings.NewReader(in), func(f Frame) error { got = append(got, f); return nil })
	if err != nil {
		t.Fatal(err)
	}
	want := []Frame{
		{ID: "7", Event: "run.event", Data: `{"a":1}`},
		{Event: "message", Data: "linha1\nlinha2"},
		{Event: "run.status", Data: "{}"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatal(diff)
	}
}

func TestReadFramesSemStatusEErroDoCallback(t *testing.T) {
	err := readFrames(strings.NewReader("event: run.event\ndata: {}\n\n"), func(Frame) error { return nil })
	if !errors.Is(err, ErrStreamEnded) {
		t.Fatalf("err = %v", err)
	}
	boom := errors.New("boom")
	err = readFrames(strings.NewReader("event: run.event\ndata: {}\n\n"), func(Frame) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}

func TestReadFramesLinhaGigante(t *testing.T) {
	big := strings.Repeat("x", 300_000)
	var got string
	err := readFrames(strings.NewReader("event: run.status\ndata: "+big+"\n\n"), func(f Frame) error { got = f.Data; return nil })
	if err != nil || got != big {
		t.Fatalf("err=%v len=%d", err, len(got))
	}
}
