package mcp

import (
	"context"
	"errors"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// hanko_git_push (ADR-0091): a persona pede para enviar a branch do worktree da sua task ao repositório remoto. A
// chamada passa pela política de tools (sem política, pergunta ao gate) e fica na auditoria.

type gitPusher interface {
	GitPushForRun(ctx context.Context, companyID domain.ID, in service.GitPushInput) (service.GitPushResult, error)
}

var _ gitPusher = (*service.Service)(nil)

type gitPushIn struct {
	Company string `json:"company,omitempty" jsonschema:"ignorado: a task e a branch são as da sua execução"`
}

type gitPushOut struct {
	Decision  string `json:"decision" jsonschema:"allow (enviada), ask (aguardando aprovação) ou deny (negado pela política)"`
	Branch    string `json:"branch,omitempty"`
	SHA       string `json:"sha,omitempty" jsonschema:"commit da branch que está no remoto"`
	Commits   int    `json:"commits,omitempty" jsonschema:"commits da branch desde a base"`
	UpToDate  bool   `json:"up_to_date,omitempty" jsonschema:"o remoto já tinha esse commit"`
	Committed bool   `json:"committed,omitempty" jsonschema:"alterações soltas viraram um commit antes do envio"`
}

func registerGitPushTools(s *sdk.Server, t *tools) {
	if _, ok := t.b.(gitPusher); !ok {
		return
	}
	sdk.AddTool(s, &sdk.Tool{
		Name: service.HankoGitPushTool,
		Description: "Envia a branch Git da sua task (o worktree em que você trabalha) ao repositório remoto do workspace, com a credencial do workspace. " +
			"Use quando o trabalho estiver commitado e pronto para um pull request; depois abra o PR com a integração do repositório (a branch já existe no remoto). " +
			"Alterações soltas viram um commit antes do envio. Pode pedir aprovação de um humano: nesse caso encerre a execução e repita a chamada quando for acordada com a decisão. " +
			"Nunca faça push por conta própria dentro do worktree.",
		Annotations: &sdk.ToolAnnotations{IdempotentHint: true},
	}, t.gitPush)
}

func (t *tools) gitPush(ctx context.Context, _ *sdk.CallToolRequest, in gitPushIn) (*sdk.CallToolResult, gitPushOut, error) {
	gb, ok := t.b.(gitPusher)
	if !ok {
		return nil, gitPushOut{}, errors.New("git indisponível neste servidor")
	}
	id, cid, err := t.scope(ctx, in.Company)
	if err != nil {
		return nil, gitPushOut{}, err
	}
	if !id.ViaToken || id.RunID == nil {
		return nil, gitPushOut{}, errors.New(service.HankoGitPushTool + " só roda com o token de uma run da persona")
	}
	res, err := gb.GitPushForRun(ctx, cid, service.GitPushInput{RunID: *id.RunID})
	if err != nil {
		return nil, gitPushOut{}, opErr("enviar a branch", err)
	}
	if res.Decision != service.NativeAllow {
		msg := res.Reason
		if res.Decision == service.NativeAsk {
			msg += "; encerre a execução e repita a chamada quando for acordada com a decisão"
		}
		return toolError(msg), gitPushOut{Decision: res.Decision}, nil
	}
	p := res.Push
	out := gitPushOut{Decision: res.Decision, Branch: p.Branch, SHA: p.SHA, Commits: p.Commits, UpToDate: p.UpToDate, Committed: p.Committed}
	if p.UpToDate {
		return textResult("A branch %s já estava no remoto em %s (%d commits desde a base).", p.Branch, short7(p.SHA), p.Commits), out, nil
	}
	return textResult("Branch %s enviada ao remoto em %s (%d commits desde a base). Agora você pode abrir o pull request a partir dela.", p.Branch, short7(p.SHA), p.Commits), out, nil
}

func short7(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
