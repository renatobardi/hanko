package mcp

import (
	"context"
	"errors"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// hanko_secret_get (ADR-0068 §4): a persona lê um campo de um item do cofre que as coleções dela alcançam. Só com o token
// de uma run. A política de tools decide antes (padrão ask): sem aprovação a chamada volta com erro dizendo para repetir
// com os mesmos argumentos depois da decisão, como as tools de plugin. Um TOTP volta só o código do momento.

// secretBackend é o que a tool de segredo pede ao service; opcional como as outras tools de grupo.
type secretBackend interface {
	SecretGet(ctx context.Context, companyID domain.ID, in service.SecretGetInput) (service.SecretGetResult, error)
}

var _ secretBackend = (*service.Service)(nil)

type secretGetIn struct {
	Company string `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	Item    string `json:"item" jsonschema:"nome do item do cofre (ex. prd-cluster); item pessoal delegado a esta run: user/NOME"`
	Campo   string `json:"campo,omitempty" jsonschema:"campo do item (ex. kubeconfig, password, totp); vazio = value"`
}

type secretGetOut struct {
	Decision  string `json:"decision" jsonschema:"allow (entregue), ask (aguardando aprovação) ou deny (negado pela política)"`
	Kind      string `json:"kind,omitempty" jsonschema:"tipo do campo: text, totp ou file"`
	Value     string `json:"value,omitempty" jsonschema:"o valor do campo (texto ou arquivo de texto)"`
	Code      string `json:"code,omitempty" jsonschema:"campo TOTP: o código atual; a semente não sai"`
	ExpiresIn int    `json:"expires_in,omitempty" jsonschema:"campo TOTP: segundos até o código vencer"`
}

func registerSecretTools(s *sdk.Server, t *tools) {
	if _, ok := t.b.(secretBackend); !ok {
		return
	}
	sdk.AddTool(s, &sdk.Tool{
		Name: service.HankoSecretGetTool,
		Description: "Lê um campo de um item do cofre que a sua persona alcança (as coleções concedidas a ela). Campo texto volta em value; " +
			"campo TOTP volta só o código do momento (code, com expires_in), nunca a semente; campo arquivo de texto volta em value. " +
			"Cada chamada fica registrada no cofre. Pode pedir aprovação do administrador: nesse caso repita a chamada com os mesmos argumentos depois da decisão. " +
			"O valor é um segredo: use-o só para a tarefa e nunca o escreva em comentário, artefato, memória ou resposta.",
	}, t.secretGet)
}

func (t *tools) secretGet(ctx context.Context, _ *sdk.CallToolRequest, in secretGetIn) (*sdk.CallToolResult, secretGetOut, error) {
	sb, ok := t.b.(secretBackend)
	if !ok {
		return nil, secretGetOut{}, errors.New("cofre indisponível neste servidor")
	}
	id, cid, err := t.scope(ctx, in.Company)
	if err != nil {
		return nil, secretGetOut{}, err
	}
	if !id.ViaToken || id.RunID == nil {
		return nil, secretGetOut{}, errors.New("hanko_secret_get só roda com o token de uma run da persona")
	}
	res, err := sb.SecretGet(ctx, cid, service.SecretGetInput{RunID: *id.RunID, Item: in.Item, Field: in.Campo})
	if err != nil {
		return nil, secretGetOut{}, opErr("ler segredo", err)
	}
	if res.Decision != service.NativeAllow {
		msg := res.Reason
		if res.Decision == service.NativeAsk {
			msg += "; repita a chamada com exatamente os mesmos argumentos depois da decisão"
		}
		return toolError(msg), secretGetOut{Decision: res.Decision}, nil
	}
	out := secretGetOut{Decision: res.Decision, Kind: string(res.Kind)}
	if res.Kind == domain.SecretFieldTOTP {
		out.Code, out.ExpiresIn = res.Code, res.ExpiresIn
		return textResult("Código TOTP de %s: %s (vence em %d s)", in.Item, res.Code, res.ExpiresIn), out, nil
	}
	out.Value = res.Value
	campo := in.Campo
	if campo == "" {
		campo = "value"
	}
	return textResult("Campo %s de %s:\n\n%s", campo, in.Item, res.Value), out, nil
}
