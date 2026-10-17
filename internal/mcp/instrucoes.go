package mcp

import (
	"context"
	"errors"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// A persona propõe mudar as próprias instruções (ADR-0052): vira um gate kind=instruction; um humano decide.

type instructionBackend interface {
	ProposePersonaInstructions(ctx context.Context, actor domain.Actor, companyID, personaID domain.ID, in service.ProposeInstructionsInput) (service.GateView, error)
}

type instructionProposeIn struct {
	Company      string `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	Instructions string `json:"instructions" jsonschema:"o texto NOVO e COMPLETO das suas instruções (não um trecho): ele substitui o atual se um humano aprovar"`
	Reason       string `json:"reason,omitempty" jsonschema:"por que mudar, em uma ou duas frases (até 500 caracteres)"`
	EventID      string `json:"event_id,omitempty" jsonschema:"chave de idempotência: repetir a chamada com o mesmo event_id não abre outro gate"`
}

type instructionProposeOut struct {
	GateID string `json:"gate_id"`
	Status string `json:"status"`
}

func registerInstructionTools(s *sdk.Server, t *tools) {
	if _, ok := t.b.(instructionBackend); !ok {
		return
	}
	sdk.AddTool(s, &sdk.Tool{
		Name: "hanko_persona_instruction_propose",
		Description: "Propõe uma mudança nas SUAS instruções permanentes (o que você aprendeu que deveria valer sempre). Envie o texto novo completo e o motivo. " +
			"Abre um gate para um humano aprovar (com o diff); nada muda até a aprovação, e você NÃO fica bloqueada: siga com a task. " +
			"Se as instruções mudarem enquanto o gate está aberto, a aprovação falha e é preciso propor de novo. Não use para registrar fatos da task (isso é memória).",
	}, t.instructionPropose)
}

func (t *tools) instructionPropose(ctx context.Context, _ *sdk.CallToolRequest, in instructionProposeIn) (*sdk.CallToolResult, instructionProposeOut, error) {
	ib, ok := t.b.(instructionBackend)
	if !ok {
		return nil, instructionProposeOut{}, errors.New("revisões de instruções indisponíveis neste servidor")
	}
	id, company, err := t.scope(ctx, in.Company)
	if err != nil {
		return nil, instructionProposeOut{}, err
	}
	if id.PersonaID == nil {
		return nil, instructionProposeOut{}, errors.New("só uma persona (token de run) propõe mudar as próprias instruções; um humano edita direto")
	}
	pi := service.ProposeInstructionsInput{Body: in.Instructions, Reason: in.Reason, EventID: in.EventID, RunID: id.RunID}
	g, err := ib.ProposePersonaInstructions(ctx, id.Actor, company, *id.PersonaID, pi)
	if err != nil {
		return nil, instructionProposeOut{}, opErr("propor instruções", err)
	}
	return textResult("Proposta aberta (gate %s, %s). Um humano decide; siga com o seu trabalho.", g.ID, g.Status),
		instructionProposeOut{GateID: g.ID.String(), Status: string(g.Status)}, nil
}
