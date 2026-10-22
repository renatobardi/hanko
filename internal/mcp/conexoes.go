package mcp

import (
	"context"
	"errors"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// A persona pede uma conexão OAuth (ADR-0043): se ela ainda não está autorizada, abre-se um gate kind=connection e a task
// espera até um humano autorizar no navegador. A persona nunca autoriza nem vê token.

type connectionBackend interface {
	RequestConnection(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.RequestConnectionInput) (service.RequestConnectionResult, error)
}

type connectionRequestIn struct {
	Company string `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	Slug    string `json:"slug,omitempty" jsonschema:"slug de uma conexão que já existe na empresa"`
	Name    string `json:"name,omitempty" jsonschema:"nome da conexão que você propõe (junto com url), se ela ainda não existe"`
	URL     string `json:"url,omitempty" jsonschema:"endereço do servidor MCP remoto da conexão proposta"`
	Reason  string `json:"reason,omitempty" jsonschema:"por que você precisa dela, em uma ou duas frases (aparece para o humano)"`
}

type connectionRequestOut struct {
	Slug      string `json:"slug"`
	Status    string `json:"status"`
	Connected bool   `json:"connected" jsonschema:"true se já estava autorizada: use as tools dela normalmente"`
	GateID    string `json:"gate_id,omitempty" jsonschema:"o gate aberto; um humano precisa autorizar a conexão"`
}

func registerConnectionTools(s *sdk.Server, t *tools) {
	if _, ok := t.b.(connectionBackend); !ok {
		return
	}
	sdk.AddTool(s, &sdk.Tool{
		Name: "hanko_connection_request",
		Description: "Pede acesso a um servidor MCP remoto que exige autorização (OAuth). Informe o slug de uma conexão existente, ou nome e url para propor uma. " +
			"Se já estiver autorizada, responde connected=true e as tools dela já estão disponíveis. Senão abre um gate para um humano autorizar no navegador e a sua task " +
			"fica bloqueada até lá; quando ele autorizar, a task retoma e você é acordada. Você nunca recebe o token.",
	}, t.connectionRequest)
}

func (t *tools) connectionRequest(ctx context.Context, _ *sdk.CallToolRequest, in connectionRequestIn) (*sdk.CallToolResult, connectionRequestOut, error) {
	cb, ok := t.b.(connectionBackend)
	if !ok {
		return nil, connectionRequestOut{}, errors.New("conexões indisponíveis neste servidor")
	}
	id, company, err := t.scope(ctx, in.Company)
	if err != nil {
		return nil, connectionRequestOut{}, err
	}
	if id.PersonaID == nil {
		return nil, connectionRequestOut{}, errors.New("só uma persona (token de run) pede conexão; um humano cria e autoriza em Integrações › Conexões")
	}
	r, err := cb.RequestConnection(ctx, id.Actor, company, service.RequestConnectionInput{Slug: in.Slug, Name: in.Name, URL: in.URL,
		Reason: in.Reason, TaskID: id.TaskID, RunID: id.RunID})
	if err != nil {
		return nil, connectionRequestOut{}, opErr("pedir conexão", err)
	}
	out := connectionRequestOut{Slug: r.Connection.Slug, Status: string(r.Connection.Status), Connected: r.Connected}
	if r.Gate != nil {
		out.GateID = r.Gate.ID.String()
	}
	if r.Connected {
		return textResult("A conexão %s já está autorizada.", r.Connection.Slug), out, nil
	}
	return textResult("Aberto o gate %s: um humano precisa autorizar a conexão %s. A task fica bloqueada até lá.", out.GateID, r.Connection.Slug), out, nil
}
