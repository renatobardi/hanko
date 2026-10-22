package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// O fake registra o pedido de review e simula as regras do service (persona não aprova).
var lastReview = map[*fake]service.PublishReviewInput{}

func (f *fake) PublishChannelReview(_ context.Context, a domain.Actor, _ domain.ID, in service.PublishReviewInput) (domain.ChannelMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastActor = a
	lastReview[f] = in
	if in.Event == domain.GHReviewApprove && a.Type != domain.ActorHuman {
		return domain.ChannelMessage{}, domain.ErrForbidden
	}
	if in.TaskID == nil && in.Peer == "" {
		return domain.ChannelMessage{}, domain.Invalid("peer", "informe o pull request")
	}
	return domain.ChannelMessage{ID: domain.NewID(), Peer: "acme/api#7", Status: domain.MsgPending, Kind: "review"}, nil
}

func TestPublishReviewTool(t *testing.T) {
	e := setup(t, false)
	cs, _ := e.connect(t, e.tok)
	defer cs.Close()
	// com token de run, a task da execução resolve o pull request
	res := call(t, cs, "hanko_publish_review", map[string]any{"event": "request_changes", "body": "Falta tratar o erro."})
	if res.IsError {
		t.Fatal(text(res))
	}
	var out publishReviewOut
	structured(t, res, &out)
	if out.Peer != "acme/api#7" || out.Status != "pending" || out.MessageID == "" {
		t.Errorf("saída = %+v", out)
	}
	in := lastReview[e.f]
	if in.Event != domain.GHReviewRequestChanges || in.TaskID == nil || in.Body != "Falta tratar o erro." {
		t.Errorf("pedido = %+v", in)
	}
	if e.f.lastActor.Type != domain.ActorPersona {
		t.Errorf("ator = %+v", e.f.lastActor)
	}
	// a persona nunca aprova
	if r := call(t, cs, "hanko_publish_review", map[string]any{"event": "APPROVE", "body": "ok"}); !r.IsError {
		t.Errorf("APPROVE deveria ser recusado: %q", text(r))
	}
	// evento inválido
	if r := call(t, cs, "hanko_publish_review", map[string]any{"event": "TALVEZ", "body": "x"}); !r.IsError || !strings.Contains(text(r), "inválido") {
		t.Errorf("evento inválido: %q", text(r))
	}
	// task por chave
	if r := call(t, cs, "hanko_publish_review", map[string]any{"event": "COMMENT", "body": "x", "key": "HNK-2"}); r.IsError {
		t.Errorf("com key: %q", text(r))
	}
	if r := call(t, cs, "hanko_publish_review", map[string]any{"event": "COMMENT", "body": "x", "key": "HNK-404"}); !r.IsError {
		t.Error("task inexistente deveria falhar")
	}
}
