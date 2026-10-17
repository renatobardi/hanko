package api

import (
	"context"

	"github.com/renatobardi/hanko/internal/domain"
)

// SendMessageWith é SendMessage com anexos já enviados (ADR-0052); o fake não guarda a ligação.
func (f *fakeSvc) SendMessageWith(ctx context.Context, a domain.Actor, cid, id domain.ID, body string, _ []domain.ID) (domain.ConversationMessage, error) {
	return f.SendMessage(ctx, a, cid, id, body)
}
