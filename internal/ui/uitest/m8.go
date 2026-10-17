package uitest

import (
	"context"

	"github.com/renatobardi/hanko/internal/domain"
)

// SendMessageWith é SendMessage com anexos já enviados (ADR-0052); o fake não guarda a ligação.
func (f *Fake) SendMessageWith(ctx context.Context, actor domain.Actor, companyID, id domain.ID, body string, _ []domain.ID) (domain.ConversationMessage, error) {
	return f.SendMessage(ctx, actor, companyID, id, body)
}
