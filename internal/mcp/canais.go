package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Canais externos (ADR-0046): a persona acordada por um comentário do GitHub publica a review no pull request.

type publishReviewIn struct {
	Company string `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	Event   string `json:"event" jsonschema:"COMMENT (comentário de review) ou REQUEST_CHANGES (pede mudanças); aprovar é ato de uma pessoa e esta tool recusa APPROVE"`
	Body    string `json:"body" jsonschema:"texto da review, em markdown"`
	Key     string `json:"key,omitempty" jsonschema:"chave da task nascida do comentário do GitHub; com token de run o padrão é a task da execução atual"`
	Channel string `json:"channel,omitempty" jsonschema:"nome do canal GitHub (opcional quando a task veio de um canal)"`
	Peer    string `json:"peer,omitempty" jsonschema:"pull request dono/repo#N (opcional quando a task veio de um canal)"`
}

type publishReviewOut struct {
	MessageID string `json:"message_id"`
	Peer      string `json:"peer"`
	Status    string `json:"status" jsonschema:"pending até o Hanko entregar; acompanhe em hanko channel messages"`
}

// reviewBackend é a parte do service que publica reviews; opcional.
type reviewBackend interface {
	PublishChannelReview(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.PublishReviewInput) (domain.ChannelMessage, error)
}

func registerChannelTools(s *sdk.Server, t *tools) {
	sdk.AddTool(s, &sdk.Tool{
		Name: "hanko_publish_review",
		Description: "Publica uma review no pull request do GitHub que acordou você (a task veio de um comentário de PR). " +
			"event=COMMENT deixa um comentário de review; event=REQUEST_CHANGES pede mudanças. Você NUNCA aprova: APPROVE é recusado, aprovar é ato de uma pessoa. " +
			"Só vale para pull requests de canais GitHub do Hanko que acordaram você, e para repositórios da lista do canal. " +
			"Sem key, vale a task da sua execução. O resumo final da execução já é publicado no PR como comentário; use esta tool quando o pedido for uma review formal.",
	}, t.publishReview)
}

func (t *tools) publishReview(ctx context.Context, _ *sdk.CallToolRequest, in publishReviewIn) (*sdk.CallToolResult, publishReviewOut, error) {
	id, company, err := t.scope(ctx, in.Company)
	if err != nil {
		return nil, publishReviewOut{}, err
	}
	rb, ok := t.b.(reviewBackend)
	if !ok {
		return nil, publishReviewOut{}, errors.New("canais indisponíveis neste servidor")
	}
	ev := domain.GitHubReviewEvent(strings.ToUpper(strings.TrimSpace(in.Event)))
	if !ev.Valid() {
		return nil, publishReviewOut{}, fmt.Errorf("event %q inválido; use COMMENT ou REQUEST_CHANGES", in.Event)
	}
	req := service.PublishReviewInput{Channel: strings.TrimSpace(in.Channel), Peer: strings.TrimSpace(in.Peer), Event: ev, Body: in.Body}
	switch key := strings.TrimSpace(in.Key); {
	case key != "":
		tv, err := t.b.GetTask(ctx, company, key)
		if err != nil {
			return nil, publishReviewOut{}, toolErr("task", key, err)
		}
		req.TaskID = &tv.ID
	case id.ViaToken && id.TaskID != nil:
		req.TaskID = id.TaskID
	}
	m, err := rb.PublishChannelReview(ctx, id.Actor, company, req)
	if err != nil {
		return nil, publishReviewOut{}, opErr("publicar review", err)
	}
	return textResult("Review %s enfileirada para %s (id %s).", ev, m.Peer, m.ID), publishReviewOut{MessageID: m.ID.String(), Peer: m.Peer, Status: string(m.Status)}, nil
}
