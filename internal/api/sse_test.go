package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/service"
)

// sseFrame é um evento SSE lido do fluxo.
type sseFrame struct{ ID, Event, Data string }

// readFrames lê frames em goroutine; comentários `: ping` viram Event "ping".
func readFrames(t *testing.T, body io.Reader) <-chan sseFrame {
	t.Helper()
	out := make(chan sseFrame, 64)
	go func() {
		defer close(out)
		sc := bufio.NewScanner(body)
		var cur sseFrame
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if cur.Event != "" {
					out <- cur
				}
				cur = sseFrame{}
			case strings.HasPrefix(line, ": ping"):
				out <- sseFrame{Event: "ping"}
			case strings.HasPrefix(line, "id: "):
				cur.ID = line[4:]
			case strings.HasPrefix(line, "event: "):
				cur.Event = line[7:]
			case strings.HasPrefix(line, "data: "):
				cur.Data = line[6:]
			}
		}
	}()
	return out
}

func next(t *testing.T, ch <-chan sseFrame) sseFrame {
	t.Helper()
	select {
	case f, ok := <-ch:
		if !ok {
			t.Fatal("stream encerrado antes do esperado")
		}
		return f
	case <-time.After(3 * time.Second):
		t.Fatal("timeout esperando frame SSE")
	}
	return sseFrame{}
}

func expectClosed(t *testing.T, ch <-chan sseFrame) {
	t.Helper()
	select {
	case f, ok := <-ch:
		if ok {
			t.Fatalf("frame inesperado após o fim: %+v", f)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stream não encerrou")
	}
}

func openStream(t *testing.T, url string, hdr map[string]string) (*http.Response, <-chan sseFrame, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); resp.Body.Close() })
	return resp, readFrames(t, resp.Body), cancel
}

func streamFixture(t *testing.T, status domain.RunStatus) (*fakeSvc, *events.Broker, domain.Company, domain.ID, string) {
	t.Helper()
	f := &fakeSvc{runs: map[domain.ID]service.RunView{}, events: map[domain.ID][]domain.RunEvent{}}
	c := seedCompany(f)
	rid := domain.NewID()
	f.runs[rid] = service.RunView{Run: domain.Run{ID: rid, CompanyID: c.ID, Status: status}, TaskKey: "HNK-1"}
	br := events.NewBroker()
	ts, _ := newTestServer(t, f, func(d *Deps) { d.Broker = br })
	return f, br, c, rid, ts.URL + "/api/v1/companies/acme/runs/" + rid.String() + "/stream"
}

func TestSSEReplayEFimQuandoTerminal(t *testing.T) {
	f, _, _, rid, url := streamFixture(t, domain.RunDone)
	for i := int64(1); i <= 3; i++ {
		f.addEvent(rid, i, "e"+strconv.FormatInt(i, 10))
	}
	resp, ch, _ := openStream(t, url, nil)
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}
	if resp.Header.Get("X-Accel-Buffering") != "no" || resp.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("headers: %v", resp.Header)
	}
	for i := int64(1); i <= 3; i++ {
		fr := next(t, ch)
		if fr.Event != "run.event" || fr.ID != strconv.FormatInt(i, 10) {
			t.Fatalf("frame %d: %+v", i, fr)
		}
		var e RunEvent
		if err := json.Unmarshal([]byte(fr.Data), &e); err != nil || e.Seq != i || e.Text != "e"+strconv.FormatInt(i, 10) {
			t.Fatalf("data: %s %v", fr.Data, err)
		}
	}
	fr := next(t, ch)
	var r Run
	if fr.Event != "run.status" || json.Unmarshal([]byte(fr.Data), &r) != nil || r.Status != "done" {
		t.Fatalf("status final: %+v", fr)
	}
	expectClosed(t, ch)
}

func TestSSEAfterELastEventID(t *testing.T) {
	f, _, _, rid, url := streamFixture(t, domain.RunDone)
	for i := int64(1); i <= 4; i++ {
		f.addEvent(rid, i, "x")
	}
	_, ch, _ := openStream(t, url+"?after=2", nil)
	if fr := next(t, ch); fr.ID != "3" {
		t.Fatalf("after=2 deveria começar no 3: %+v", fr)
	}
	// Last-Event-ID tem precedência sobre ?after
	_, ch, _ = openStream(t, url+"?after=0", map[string]string{"Last-Event-ID": "3"})
	if fr := next(t, ch); fr.ID != "4" {
		t.Fatalf("Last-Event-ID=3 deveria começar no 4: %+v", fr)
	}
}

func TestSSEAoVivoSemDuplicar(t *testing.T) {
	f, br, _, rid, url := streamFixture(t, domain.RunRunning)
	f.addEvent(rid, 1, "um")
	f.addEvent(rid, 2, "dois")
	_, ch, _ := openStream(t, url, nil)
	if fr := next(t, ch); fr.ID != "1" {
		t.Fatalf("%+v", fr)
	}
	if fr := next(t, ch); fr.ID != "2" {
		t.Fatalf("%+v", fr)
	}

	topic := events.RunTopic(rid.String())
	// republica o seq 2 (duplicata) e depois o 3: só o 3 deve sair
	e2 := f.events[rid][1]
	br.Publish(events.Message{Topic: topic, Type: "run.event", Data: e2})
	e3 := f.addEvent(rid, 3, "tres")
	br.Publish(events.Message{Topic: topic, Type: "run.event", Data: e3})
	if fr := next(t, ch); fr.ID != "3" || fr.Event != "run.event" {
		t.Fatalf("esperava seq 3, veio %+v", fr)
	}

	// lacuna: publica o 5 sem o 4 no broker; o 4 vem do banco (catch-up) antes do 5
	f.addEvent(rid, 4, "quatro")
	e5 := f.addEvent(rid, 5, "cinco")
	br.Publish(events.Message{Topic: topic, Type: "run.event", Data: e5})
	if fr := next(t, ch); fr.ID != "4" {
		t.Fatalf("esperava seq 4 do catch-up, veio %+v", fr)
	}
	if fr := next(t, ch); fr.ID != "5" {
		t.Fatalf("esperava seq 5, veio %+v", fr)
	}

	// status terminal encerra o stream, depois de entregar o que faltava
	f.addEvent(rid, 6, "seis")
	f.setStatus(rid, domain.RunDone)
	br.Publish(events.Message{Topic: topic, Type: "run.status", Data: domain.Run{ID: rid, Status: domain.RunDone}})
	if fr := next(t, ch); fr.ID != "6" {
		t.Fatalf("esperava seq 6 antes do status, veio %+v", fr)
	}
	if fr := next(t, ch); fr.Event != "run.status" {
		t.Fatalf("esperava run.status, veio %+v", fr)
	}
	expectClosed(t, ch)
}

func TestSSEStatusIntermediarioNaoEncerra(t *testing.T) {
	f, br, _, rid, url := streamFixture(t, domain.RunQueued)
	_, ch, _ := openStream(t, url, nil)
	// dá tempo da assinatura (o handler assina antes de responder; a resposta já saiu)
	f.setStatus(rid, domain.RunRunning)
	br.Publish(events.Message{Topic: events.RunTopic(rid.String()), Type: "run.status", Data: map[string]any{"status": "running"}})
	fr := next(t, ch)
	var r Run
	if fr.Event != "run.status" || json.Unmarshal([]byte(fr.Data), &r) != nil || r.Status != "running" {
		t.Fatalf("%+v", fr)
	}
	e := f.addEvent(rid, 1, "oi")
	br.Publish(events.Message{Topic: events.RunTopic(rid.String()), Type: "run.event", Data: &e})
	if fr := next(t, ch); fr.ID != "1" {
		t.Fatalf("%+v", fr)
	}
}

func TestSSEHeartbeatERecuperaDescartes(t *testing.T) {
	f := &fakeSvc{runs: map[domain.ID]service.RunView{}, events: map[domain.ID][]domain.RunEvent{}}
	c := seedCompany(f)
	rid := domain.NewID()
	f.runs[rid] = service.RunView{Run: domain.Run{ID: rid, CompanyID: c.ID, Status: domain.RunRunning}}
	br := events.NewBroker()

	rec := newFlushRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		streamRun(ctx, rec, f, br, discardLogger(), c.ID, rid, 0, 20*time.Millisecond, nil)
	}()
	ch := readFrames(t, rec.reader())
	if fr := next(t, ch); fr.Event != "ping" {
		t.Fatalf("esperava ping, veio %+v", fr)
	}
	// evento só no "banco" (broker nunca soube): o tick o recupera
	f.addEvent(rid, 1, "perdido")
	for {
		fr := next(t, ch)
		if fr.Event == "run.event" {
			if fr.ID != "1" {
				t.Fatalf("%+v", fr)
			}
			break
		}
	}
	// run termina sem run.status no broker: o tick percebe e encerra
	f.setStatus(rid, domain.RunFailed)
	for {
		fr := next(t, ch)
		if fr.Event == "run.status" {
			break
		}
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("streamRun não retornou")
	}
	cancel()
}

func TestSSERespeitaContexto(t *testing.T) {
	f, _, _, rid, url := streamFixture(t, domain.RunRunning)
	_ = f
	_ = rid
	_, ch, cancel := openStream(t, url, nil)
	cancel()
	expectClosed(t, ch)
}

func TestSSEErrosAntesDoStream(t *testing.T) {
	f, _, _, _, url := streamFixture(t, domain.RunRunning)
	_ = f
	base := strings.TrimSuffix(url, url[strings.LastIndex(url, "/runs/"):])
	tests := []struct {
		name, url string
		hdr       map[string]string
		want      int
	}{
		{"run inexistente", base + "/runs/" + domain.NewID().String() + "/stream", nil, 404},
		{"run não é uuid", base + "/runs/xyz/stream", nil, 422},
		{"empresa inexistente", strings.Replace(url, "/acme/", "/nada/", 1), nil, 404},
		{"after inválido", url + "?after=abc", nil, 422},
		{"Last-Event-ID inválido", url, map[string]string{"Last-Event-ID": "-3"}, 422},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := do(t, "GET", tc.url, "", tc.hdr)
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, quer %d: %s", resp.StatusCode, tc.want, body)
			}
		})
	}
}
