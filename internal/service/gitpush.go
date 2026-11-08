package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel/attribute"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/gitrepo"
	"github.com/renatobardi/hanko/internal/store"
)

// Push da branch do worktree (ADR-0091). O Hanko envia a branch da task ao repositório remoto só por ação explícita: um
// membro pela aba Git, pela API ou pela CLI, ou a persona pela tool hanko_git_push, que passa pela política de tools
// (sem política, pergunta ao gate). Nunca no fim da run. A credencial é a do workspace, resolvida na hora, como na
// busca do espelho.

// HankoGitPushTool é o nome da tool MCP que a persona usa para enviar a branch.
const HankoGitPushTool = "hanko_git_push"

// PushResult descreve um envio.
type PushResult struct {
	Branch string `json:"branch"`
	// SHA é o commit da branch que está no remoto depois do envio.
	SHA string `json:"sha"`
	// Commits é quantos commits a branch tem desde a base (o que um PR dela levaria).
	Commits int `json:"commits"`
	// UpToDate: o remoto já tinha este commit; nada foi enviado.
	UpToDate bool `json:"up_to_date"`
	// Committed: havia alterações soltas no worktree e elas viraram um commit antes do envio.
	Committed bool `json:"committed"`
}

// PushTaskWorktree envia a branch do worktree da task ao remoto (membro). Alterações soltas no worktree viram um
// commit antes ("nada se perde", como na limpeza). Sem Git ligado, ErrNotFound; sem worktree, ErrNotFound.
func (s *Service) PushTaskWorktree(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID) (PushResult, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return PushResult{}, err
	}
	if err := s.gitOn(); err != nil {
		return PushResult{}, err
	}
	return s.pushWorktree(ctx, actor, companyID, taskID)
}

// GitPushInput é a chamada da tool hanko_git_push: a run identifica a task e a persona.
type GitPushInput struct {
	RunID domain.ID
}

// GitPushResult é a resposta da tool: a decisão da política (allow, ask ou deny) e, em allow, o envio.
type GitPushResult struct {
	Decision string
	Reason   string
	Push     PushResult
}

// GitPushForRun é hanko_git_push: a persona pede para enviar a branch da task da sua run. A chamada passa pela
// política de tools como tool do Hanko (ADR-0053; sem política, pergunta ao gate, o padrão da empresa) e fica na
// auditoria; em allow, envia com a persona como autora do evento.
func (s *Service) GitPushForRun(ctx context.Context, companyID domain.ID, in GitPushInput) (GitPushResult, error) {
	if err := s.gitOn(); err != nil {
		return GitPushResult{}, err
	}
	c, err := s.RunToolCaller(ctx, companyID, in.RunID)
	if err != nil {
		return GitPushResult{}, err
	}
	if c.PersonaID == nil || c.TaskID == nil {
		return GitPushResult{}, domain.Invalid("run", HankoGitPushTool+" só roda numa run de task de uma persona")
	}
	row, err := s.read().GetTaskWorktree(ctx, store.GetTaskWorktreeParams{CompanyID: companyID, TaskID: *c.TaskID})
	if err != nil {
		return GitPushResult{}, mapErr(err, "worktree da task")
	}
	args, err := json.Marshal(map[string]string{"task": c.TaskKey, "branch": row.Branch})
	if err != nil {
		return GitPushResult{}, fmt.Errorf("argumentos da tool: %w", err)
	}
	d, err := s.NativeToolCheck(ctx, c, NativeToolRequest{Name: HankoGitPushTool, Title: "enviar a branch " + row.Branch, Input: args, Hanko: true})
	if err != nil {
		return GitPushResult{}, err
	}
	if d.Decision != NativeAllow {
		return GitPushResult{Decision: d.Decision, Reason: d.Reason}, nil
	}
	res, err := s.pushWorktree(ctx, domain.PersonaActor(*c.PersonaID), companyID, *c.TaskID)
	if err != nil {
		return GitPushResult{}, err
	}
	return GitPushResult{Decision: NativeAllow, Push: res}, nil
}

// pushWorktree faz o envio: commit de segurança do que está solto, push da branch a partir do espelho (sob o lock
// dele) e registro do commit enviado, com o evento na mesma transação.
func (s *Service) pushWorktree(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID) (PushResult, error) {
	a, err := s.worktreeAccessFor(ctx, companyID, taskID)
	if err != nil {
		if errors.Is(err, errNoMirror) {
			return PushResult{}, domain.Invalid("worktree", "o espelho do workspace não está no disco; sincronize o Git do workspace antes")
		}
		return PushResult{}, err
	}
	switch domain.WorktreeState(a.row.State) {
	case domain.WorktreeProvisioning, domain.WorktreeFailed:
		return PushResult{}, domain.Invalid("worktree", "o worktree ainda não está pronto (estado "+a.row.State+")")
	}
	ws, err := s.read().GetWorkspaceByID(ctx, store.GetWorkspaceByIDParams{CompanyID: companyID, ID: a.row.WorkspaceID})
	if err != nil {
		return PushResult{}, mapErr(err, "get workspace")
	}
	ctx, end := coordSpan(ctx, "hanko.git.push", attribute.String("hanko.task_id", taskID.String()))
	defer end()
	g := s.gitRunner()
	res := PushResult{Branch: a.row.Branch}
	if a.live {
		if res.Committed, err = g.CommitAll(ctx, a.row.Path, "hanko: alterações soltas antes do envio da branch"); err != nil {
			return PushResult{}, fmt.Errorf("commit de segurança: %w", err)
		}
	}
	var cred *gitrepo.Credential
	if a.wg.CredentialRef != "" {
		val, _, rerr := s.ResolveSecretText(ctx, companyID, a.wg.CredentialRef)
		if rerr != nil {
			return PushResult{}, fmt.Errorf("credencial do repositório: %w", rerr)
		}
		cred = &gitrepo.Credential{Value: val}
	}
	unlock := s.lockMirror(a.mirror)
	p, err := g.PushBranch(ctx, a.mirror, ws.RepoUrl, a.row.Branch, cred)
	unlock()
	if err != nil {
		return PushResult{}, domain.Invalid("push", err.Error())
	}
	res.SHA, res.UpToDate = p.SHA, p.UpToDate
	if a.row.BaseSha != nil {
		if n, cerr := g.CountBetween(ctx, a.mirror, *a.row.BaseSha, p.SHA); cerr == nil {
			res.Commits = n
		}
	}
	err = s.withTx(ctx, func(t *txc) error {
		if _, err := t.q.SetTaskWorktreePushed(ctx, store.SetTaskWorktreePushedParams{PushedSha: &p.SHA, Now: &t.now, CompanyID: companyID, TaskID: taskID}); err != nil {
			return fmt.Errorf("registrar envio: %w", err)
		}
		return t.activity(actor, companyID, "task.worktree_pushed", "task", taskID,
			map[string]any{"branch": a.row.Branch, "sha": p.SHA, "commits": res.Commits, "up_to_date": p.UpToDate, "remote": gitrepo.RemoteHostOf(ws.RepoUrl)}, "")
	})
	if err != nil {
		return PushResult{}, err
	}
	return res, nil
}
