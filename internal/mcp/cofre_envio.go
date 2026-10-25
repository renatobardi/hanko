package mcp

import (
	"context"
	"errors"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// hanko_send_create (ADR-0069 §4): a persona cria um envio por link de um campo do cofre que ela alcança. Ela não digita o
// valor: o servidor lê o campo, cifra com uma chave nova e devolve o link, com a chave só no fragmento. A política de tools
// decide antes (padrão ask): sem aprovação, a chamada volta com erro dizendo para repetir com os mesmos argumentos.

// sendCreateBackend é o que a tool de envio pede ao service; opcional como as outras tools de grupo.
type sendCreateBackend interface {
	PersonaSendCreate(ctx context.Context, companyID, runID domain.ID, in service.PersonaSendInput) (service.PersonaSendResult, error)
}

var _ sendCreateBackend = (*service.Service)(nil)

type sendCreateIn struct {
	Company    string `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	Item       string `json:"item" jsonschema:"nome do item do cofre que a persona alcança (ex. api-token)"`
	Campo      string `json:"campo,omitempty" jsonschema:"campo do item (ex. password); vazio = value"`
	ExpiraEm   int    `json:"expira_em,omitempty" jsonschema:"segundos até o envio vencer (60 a 2592000); vazio = 604800 (7 dias)"`
	MaxAcessos *int   `json:"max_acessos,omitempty" jsonschema:"acessos antes de o envio ser apagado (1 a 1000); vazio = sem limite"`
}

type sendCreateOut struct {
	Decision  string `json:"decision" jsonschema:"allow (link criado), ask (aguardando aprovação) ou deny (negado pela política)"`
	Link      string `json:"link,omitempty" jsonschema:"o link do envio; a chave está no fragmento. É um segredo: entregue só ao destinatário"`
	ExpiresAt string `json:"expires_at,omitempty" jsonschema:"quando o envio vence (RFC 3339)"`
}

func registerSendTools(s *sdk.Server, t *tools) {
	if _, ok := t.b.(sendCreateBackend); !ok {
		return
	}
	sdk.AddTool(s, &sdk.Tool{
		Name: service.HankoSendCreateTool,
		Description: "Cria um envio por link de um campo do cofre que a sua persona alcança. O servidor lê o campo, cifra e devolve o link: " +
			"você não digita o valor. O link é um segredo: entregue-o só ao destinatário e não o grave em comentário, artefato ou memória. " +
			"Pode pedir aprovação do administrador: nesse caso repita a chamada com os mesmos argumentos depois da decisão.",
	}, t.sendCreate)
}

func (t *tools) sendCreate(ctx context.Context, _ *sdk.CallToolRequest, in sendCreateIn) (*sdk.CallToolResult, sendCreateOut, error) {
	sb, ok := t.b.(sendCreateBackend)
	if !ok {
		return nil, sendCreateOut{}, errors.New("envios indisponíveis neste servidor")
	}
	id, cid, err := t.scope(ctx, in.Company)
	if err != nil {
		return nil, sendCreateOut{}, err
	}
	if !id.ViaToken || id.RunID == nil {
		return nil, sendCreateOut{}, errors.New("hanko_send_create só roda com o token de uma run da persona")
	}
	res, err := sb.PersonaSendCreate(ctx, cid, *id.RunID, service.PersonaSendInput{Item: in.Item, Field: in.Campo,
		ExpiresIn: time.Duration(in.ExpiraEm) * time.Second, MaxAccesses: in.MaxAcessos})
	if err != nil {
		return nil, sendCreateOut{}, opErr("criar envio", err)
	}
	if res.Decision != service.NativeAllow {
		msg := res.Reason
		if res.Decision == service.NativeAsk {
			msg += "; repita a chamada com exatamente os mesmos argumentos depois da decisão"
		}
		return toolError(msg), sendCreateOut{Decision: res.Decision}, nil
	}
	expira := res.ExpiresAt.Format(time.RFC3339)
	return textResult("Envio de %s criado, vence em %s. Link: %s", in.Item, expira, res.Link),
		sendCreateOut{Decision: res.Decision, Link: res.Link, ExpiresAt: expira}, nil
}
