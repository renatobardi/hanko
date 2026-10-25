package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/secretref"
	"github.com/renatobardi/hanko/internal/store"
)

// hanko_send_create (ADR-0069 §4): a persona cria um envio por link de um campo do cofre que ela alcança. Ela não digita
// o valor: o servidor lê o campo, cifra com uma chave nova, monta o link e o devolve. Antes de tudo, o alcance (um campo
// fora das coleções dela nem abre gate); depois, a política de tools (padrão ask, ADR-0053 e ADR-0070).

// HankoSendCreateTool é o nome da tool MCP de envio por persona.
const HankoSendCreateTool = "hanko_send_create"

// PersonaSendResult é a resposta da tool. Decision diz o que a política decidiu: allow entrega o link; ask e deny vêm
// com Reason (e o gate, quando abriu). Só o allow traz Link e ExpiresAt.
type PersonaSendResult struct {
	Decision  string
	Reason    string
	Link      string
	ExpiresAt time.Time
}

// PersonaSendCreate cria, para a tool hanko_send_create, um envio do campo que a run da persona pediu. Prazo e limite
// são conferidos antes do gate, para um pedido inválido não abrir aprovação.
func (s *Service) PersonaSendCreate(ctx context.Context, companyID, runID domain.ID, in PersonaSendInput) (PersonaSendResult, error) {
	if in.ExpiresIn == 0 {
		in.ExpiresIn = domain.DefaultSendTTL // prazo padrão do envio (ADR-0069 §1)
	}
	if err := domain.ValidateSend(domain.SendText, in.ExpiresIn, in.MaxAccesses); err != nil {
		return PersonaSendResult{}, err
	}
	if !secretref.ValidName(in.Item) {
		return PersonaSendResult{}, domain.Invalid("item", "nome do item inválido")
	}
	field := in.Field
	if field == "" {
		field = secretref.DefaultField
	}
	if !secretref.ValidFieldName(field) {
		return PersonaSendResult{}, domain.Invalid("campo", "nome de campo inválido")
	}
	c, err := s.RunToolCaller(ctx, companyID, runID)
	if err != nil {
		return PersonaSendResult{}, err
	}
	if c.PersonaID == nil {
		return PersonaSendResult{}, domain.Invalid("run", "hanko_send_create só roda numa run de persona")
	}
	// o item tem que existir antes do alcance e do gate: um nome inexistente não abre aprovação (como hanko_secret_get)
	if _, err := s.read().GetCompanySecretByName(ctx, store.GetCompanySecretByNameParams{CompanyID: companyID, Name: in.Item}); err != nil {
		return PersonaSendResult{}, mapErr(err, "segredo "+in.Item)
	}
	ref := secretref.Ref{Name: in.Item, Field: field}
	if err := s.requirePersonaUse(ctx, companyID, *c.PersonaID, ref); err != nil {
		return PersonaSendResult{}, err
	}
	args, err := json.Marshal(map[string]any{"item": in.Item, "campo": field, "expira_em": int(in.ExpiresIn / time.Second),
		"max_acessos": in.MaxAccesses})
	if err != nil {
		return PersonaSendResult{}, fmt.Errorf("argumentos da tool: %w", err)
	}
	d, err := s.NativeToolCheck(ctx, c, NativeToolRequest{Name: HankoSendCreateTool, Title: "criar envio de " + ref.String(), Input: args, Hanko: true})
	if err != nil {
		return PersonaSendResult{}, err
	}
	if d.Decision != NativeAllow {
		return PersonaSendResult{Decision: d.Decision, Reason: d.Reason}, nil
	}
	v, err := s.CreatePersonaSend(ctx, domain.PersonaActor(*c.PersonaID), companyID, in)
	if err != nil {
		return PersonaSendResult{}, err
	}
	// o link leva a chave no fragmento: entra na redação do log da run, como o valor de hanko_secret_get (ADR-0068 §4.4)
	s.runSecrets.add(runID, v.Link)
	return PersonaSendResult{Decision: NativeAllow, Link: v.Link, ExpiresAt: v.ExpiresAt}, nil
}
