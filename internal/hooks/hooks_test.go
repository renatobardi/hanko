package hooks

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

type fakeFlow struct {
	in  service.WebhookFireInput
	res service.FlowFireResult
	err error
}

func (f *fakeFlow) FireFlowWebhook(_ context.Context, in service.WebhookFireInput) (service.FlowFireResult, error) {
	f.in = in
	return f.res, f.err
}

type fakeChan struct {
	in  service.IngestInput
	res service.IngestResult
	err error
}

func (f *fakeChan) IngestChannel(_ context.Context, in service.IngestInput) (service.IngestResult, error) {
	f.in = in
	return f.res, f.err
}

func serve(t *testing.T, pattern string, h http.Handler) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(pattern, h)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func post(t *testing.T, url string, hdr map[string]string, body string) (int, string, http.Header) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return resp.StatusCode, sb.String(), resp.Header
}

func TestFlowsHandlerStatuses(t *testing.T) {
	id := domain.NewID()
	task := &service.TaskView{}
	task.Key = "ACM-7"
	cases := []struct {
		name   string
		res    service.FlowFireResult
		err    error
		status int
		body   string
	}{
		{"ok", service.FlowFireResult{Fire: domain.FlowFire{ID: id}, Task: task}, nil, 202, "ACM-7"},
		{"duplicado", service.FlowFireResult{Fire: domain.FlowFire{ID: id}, Duplicate: true}, nil, 200, `"duplicate":true`},
		{"assinatura", service.FlowFireResult{}, service.ErrWebhookUnauthorized, 401, "assinatura inválida"},
		{"limite", service.FlowFireResult{}, service.ErrWebhookThrottled, 429, "limite"},
		{"inexistente", service.FlowFireResult{}, domain.ErrNotFound, 404, "não encontrado"},
		{"variável", service.FlowFireResult{}, domain.Invalid("variables", "faltam variáveis obrigatórias: repo"), 422, "repo"},
		{"desativado", service.FlowFireResult{}, fmt.Errorf("x: %w", domain.ErrConflict), 409, "desativado"},
		{"erro", service.FlowFireResult{}, errors.New("banco caiu"), 500, "erro interno"},
	}
	for _, c := range cases {
		b := &fakeFlow{res: c.res, err: c.err}
		ts := serve(t, FlowPattern, Flows(b, nil))
		status, body, hdr := post(t, ts.URL+"/hooks/flows/"+id.String(), map[string]string{"X-Hanko-Signature": "t=1,v1=ab", "X-Hanko-Delivery": "d1"}, `{"a":1}`)
		if status != c.status || !strings.Contains(body, c.body) {
			t.Errorf("%s: %d %s", c.name, status, body)
		}
		if c.status == 429 && hdr.Get("Retry-After") == "" {
			t.Errorf("%s: sem Retry-After", c.name)
		}
		if b.in.TriggerID != id || b.in.Signature != "t=1,v1=ab" || b.in.Delivery != "d1" || string(b.in.Body) != `{"a":1}` {
			t.Errorf("%s: entrada = %+v", c.name, b.in)
		}
	}
	// Idempotency-Key vale como id da entrega; id inválido nem chega ao service
	b := &fakeFlow{res: service.FlowFireResult{Fire: domain.FlowFire{ID: id}}}
	ts := serve(t, FlowPattern, Flows(b, nil))
	post(t, ts.URL+"/hooks/flows/"+id.String(), map[string]string{"Idempotency-Key": "k9"}, `{}`)
	if b.in.Delivery != "k9" {
		t.Errorf("Idempotency-Key: %q", b.in.Delivery)
	}
	b.in = service.WebhookFireInput{}
	if status, _, _ := post(t, ts.URL+"/hooks/flows/nao-e-id", nil, `{}`); status != 404 || b.in.Body != nil {
		t.Errorf("id inválido: %d", status)
	}
	// corpo acima do teto
	if status, _, _ := post(t, ts.URL+"/hooks/flows/"+id.String(), nil, strings.Repeat("x", domain.MaxFlowPayloadBytes+10)); status != 413 {
		t.Errorf("corpo grande: %d", status)
	}
}

func TestChannelsHandlerStatuses(t *testing.T) {
	cases := []struct {
		name   string
		res    service.IngestResult
		err    error
		status int
		body   string
	}{
		{"ok", service.IngestResult{Accepted: 1}, nil, 200, `"accepted":1`},
		{"não autenticado", service.IngestResult{}, service.ErrWebhookUnauthorized, 401, "não autenticado"},
		{"inexistente", service.IngestResult{}, domain.ErrNotFound, 404, "não encontrado"},
		{"inválido segue 200", service.IngestResult{}, domain.Invalid("body", "x"), 200, "evento inválido"},
		{"erro", service.IngestResult{}, errors.New("banco"), 500, "erro interno"},
	}
	for _, c := range cases {
		b := &fakeChan{res: c.res, err: c.err}
		ts := serve(t, ChannelPattern, Channels(b, nil))
		status, body, _ := post(t, ts.URL+"/hooks/channels/tok123", map[string]string{"X-Telegram-Bot-Api-Secret-Token": "s"}, `{"update_id":1}`)
		if status != c.status || !strings.Contains(body, c.body) {
			t.Errorf("%s: %d %s", c.name, status, body)
		}
		if b.in.Token != "tok123" || b.in.Header.Get("X-Telegram-Bot-Api-Secret-Token") != "s" || string(b.in.Body) != `{"update_id":1}` {
			t.Errorf("%s: entrada = %+v", c.name, b.in)
		}
	}
	b := &fakeChan{}
	ts := serve(t, ChannelPattern, Channels(b, nil))
	if status, _, _ := post(t, ts.URL+"/hooks/channels/tok", nil, strings.Repeat("x", MaxChannelBody+10)); status != 413 {
		t.Errorf("corpo grande: %d", status)
	}
}
