package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/gitrepo"
	"github.com/renatobardi/hanko/internal/store"
)

// Worktrees por task (ADR-0054): provisionar na hora da run, reconciliar, limpar ao concluir ou integrar, e a
// leitura da aba Git. Escritas de domínio (estado do worktree) geram activity_event na mesma transação; o git roda
// fora de transação, sob o lock do espelho.

// WorktreePrep é o worktree pronto para uma run.
type WorktreePrep struct {
	Dir    string
	Branch string
	// Warning é um aviso para o log da run (espelho desatualizado, worktree divergente).
	Warning string
}

// PrepareTaskWorktree devolve o diretório de trabalho de uma run de task quando o workspace tem Git ligado:
// provisiona o worktree (create) ou só devolve o que já existe (revisores leem o trabalho da autora sem criar).
// (nil, nil) significa "workspace sem Git": o chamador segue com o comportamento de sempre.
func (s *Service) PrepareTaskWorktree(ctx context.Context, companyID, taskID, workspaceID domain.ID, create bool) (*WorktreePrep, error) {
	if !s.GitWorkspaces.Enabled || s.WorkspacesRoot == "" {
		return nil, nil
	}
	q := s.read()
	wg, err := q.GetWorkspaceGit(ctx, store.GetWorkspaceGitParams{CompanyID: companyID, WorkspaceID: workspaceID})
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get workspace git: %w", err)
	}
	ctx, end := coordSpan(ctx, "hanko.git.worktree.prepare", attribute.String("hanko.task_id", taskID.String()), attribute.Bool("hanko.git.create", create))
	defer end()
	ws, err := q.GetWorkspaceByID(ctx, store.GetWorkspaceByIDParams{CompanyID: companyID, ID: workspaceID})
	if err != nil {
		return nil, mapErr(err, "get workspace")
	}
	co, err := q.GetCompanyByID(ctx, companyID)
	if err != nil {
		return nil, mapErr(err, "company")
	}
	tv, err := loadTaskView(ctx, q, companyID, taskID)
	if err != nil {
		return nil, err
	}
	branch, err := domain.WorktreeBranch(tv.Key)
	if err != nil {
		return nil, err
	}
	mirror, wtBase := s.gitPaths(co.Slug, ws.Slug)
	wtPath := filepath.Join(wtBase, branch)

	cur, err := q.GetTaskWorktree(ctx, store.GetTaskWorktreeParams{CompanyID: companyID, TaskID: taskID})
	switch {
	case err == nil:
		if prep, ok, rerr := s.reconcileWorktree(ctx, cur, mirror, ws.Slug); rerr != nil {
			return nil, rerr
		} else if ok {
			return prep, nil
		}
	case !isNoRows(err):
		return nil, fmt.Errorf("get task worktree: %w", err)
	}
	if !create {
		return nil, nil
	}
	return s.provisionWorktree(ctx, co, ws, wg, tv, branch, mirror, wtPath)
}

// reconcileWorktree confere um worktree que o banco diz estar em uso com o que há no disco. ok=true devolve o
// diretório a usar; ok=false pede (re)provisionamento (a pasta sumiu ou o registro não está mais em uso).
func (s *Service) reconcileWorktree(ctx context.Context, cur store.TaskWorktree, mirror, wsSlug string) (*WorktreePrep, bool, error) {
	state := domain.WorktreeState(cur.State)
	if state != domain.WorktreeReady && state != domain.WorktreeDiverged {
		return nil, false, nil
	}
	st, err := os.Stat(cur.Path)
	if err != nil || !st.IsDir() {
		if state == domain.WorktreeReady {
			if terr := s.markWorktree(ctx, cur, domain.WorktreeDiverged, "a pasta do worktree não existe mais no disco"); terr != nil {
				return nil, false, terr
			}
		}
		return nil, false, nil // pasta ausente: provisiona de novo (a branch no espelho é reaproveitada)
	}
	rctx, cancel := context.WithTimeout(ctx, gitReadTimeout)
	defer cancel()
	br, berr := s.gitRunner().CurrentBranch(rctx, cur.Path)
	if berr != nil {
		return nil, false, fmt.Errorf("conferir o worktree: %w", berr)
	}
	if br != cur.Branch {
		why := fmt.Sprintf("o worktree está em %q em vez de %q", br, cur.Branch)
		if br == "" {
			why = fmt.Sprintf("o worktree está com HEAD solto em vez de %q", cur.Branch)
		}
		if state == domain.WorktreeReady {
			if terr := s.markWorktree(ctx, cur, domain.WorktreeDiverged, why); terr != nil {
				return nil, false, terr
			}
		}
		return &WorktreePrep{Dir: cur.Path, Branch: cur.Branch, Warning: "worktree divergente: " + why}, true, nil
	}
	if state == domain.WorktreeDiverged { // voltou ao normal
		if terr := s.markWorktree(ctx, cur, domain.WorktreeReady, ""); terr != nil {
			return nil, false, terr
		}
	}
	return &WorktreePrep{Dir: cur.Path, Branch: cur.Branch}, true, nil
}

// markWorktree muda o estado de um worktree existente (e registra), respeitando a máquina de estados.
func (s *Service) markWorktree(ctx context.Context, cur store.TaskWorktree, to domain.WorktreeState, why string) error {
	from := domain.WorktreeState(cur.State)
	if from == to {
		return nil
	}
	if !from.CanTransition(to) {
		return fmt.Errorf("worktree %s -> %s: %w", from, to, domain.ErrInvalid)
	}
	return s.withTx(context.WithoutCancel(ctx), func(t *txc) error {
		row, err := t.q.FinishTaskWorktree(t.ctx, store.FinishTaskWorktreeParams{CompanyID: cur.CompanyID, ID: cur.ID, State: string(to),
			Path: cur.Path, BaseBranch: cur.BaseBranch, Error: why, RemovedReason: cur.RemovedReason, RemovedAt: cur.RemovedAt, Now: t.now})
		if err != nil {
			return mapErr(err, "mark worktree")
		}
		return t.activity(domain.SystemActor, cur.CompanyID, "task.worktree_"+string(to), "task", cur.TaskID,
			map[string]any{"branch": row.Branch, "state": to, "why": why}, "")
	})
}

// ensureMirrorFresh garante um espelho utilizável para criar um worktree. Busca de novo se estiver velho; se a
// busca falhar mas já houve um espelho bom, segue com ele e avisa.
func (s *Service) ensureMirrorFresh(ctx context.Context, companyID domain.ID, wg store.WorkspaceGit) (domain.WorkspaceGit, string, error) {
	cur := workspaceGitFromRow(wg)
	fresh := cur.MirrorState == domain.MirrorReady && cur.LastFetchAt != nil && s.Now().UTC().Sub(*cur.LastFetchAt) < mirrorFreshFor
	if fresh {
		return cur, "", nil
	}
	got, err := s.syncMirror(ctx, domain.SystemActor, companyID, wg.WorkspaceID)
	if err == nil && got.MirrorState == domain.MirrorReady {
		return got, "", nil
	}
	reason := got.LastError
	if err != nil {
		reason = err.Error()
	}
	if cur.HeadSHA != "" && cur.BaseBranch != "" { // houve um espelho bom antes: usa-o
		return cur, "espelho do repositório desatualizado (a busca falhou: " + clipText(reason, 200) + ")", nil
	}
	return cur, "", fmt.Errorf("espelho do repositório indisponível: %s", clipText(reason, 300))
}

// provisionWorktree cria o worktree da task: reivindica a linha, roda o git e grava o resultado.
func (s *Service) provisionWorktree(ctx context.Context, co store.Company, ws store.Workspace, wg store.WorkspaceGit, tv TaskView, branch, mirror, wtPath string) (*WorktreePrep, error) {
	mg, warn, err := s.ensureMirrorFresh(ctx, co.ID, wg)
	if err != nil {
		return nil, err
	}
	now := s.Now().UTC()
	var claimed store.TaskWorktree
	err = s.withTx(ctx, func(t *txc) error {
		row, cerr := t.q.ClaimTaskWorktree(t.ctx, store.ClaimTaskWorktreeParams{ID: domain.NewID(), CompanyID: co.ID, TaskID: tv.ID,
			WorkspaceID: ws.ID, Branch: branch, BaseBranch: mg.BaseBranch, Path: wtPath, Now: &now, StaleBefore: ptr(now.Add(-gitStaleAfter))})
		if cerr != nil {
			return cerr
		}
		claimed = row
		return t.activity(domain.SystemActor, co.ID, "task.worktree_provisioning", "task", tv.ID,
			map[string]any{"branch": branch, "workspace": ws.Slug, "base_branch": mg.BaseBranch}, "")
	})
	if isNoRows(err) { // outro processo (ou a própria run anterior) já cuida dele
		cur, gerr := s.read().GetTaskWorktree(ctx, store.GetTaskWorktreeParams{CompanyID: co.ID, TaskID: tv.ID})
		if gerr != nil {
			return nil, mapErr(gerr, "get task worktree")
		}
		if prep, ok, rerr := s.reconcileWorktree(ctx, cur, mirror, ws.Slug); rerr != nil {
			return nil, rerr
		} else if ok {
			return prep, nil
		}
		return nil, fmt.Errorf("o worktree de %s está sendo criado por outra execução; tente de novo: %w", tv.Key, domain.ErrConflict)
	}
	if err != nil {
		return nil, mapErr(err, "claim worktree")
	}

	unlock := s.lockMirror(mirror)
	baseSHA, headSHA, gerr := s.gitRunner().AddWorktree(ctx, mirror, wtPath, branch, gitrepo.RemoteRef(mg.BaseBranch))
	unlock()
	fctx := context.WithoutCancel(ctx)
	if gerr != nil {
		_ = os.RemoveAll(wtPath) // sobra de um add pela metade
		msg := clipText(gerr.Error(), maxSourceError)
		ferr := s.withTx(fctx, func(t *txc) error {
			if _, err := t.q.FinishTaskWorktree(t.ctx, store.FinishTaskWorktreeParams{CompanyID: co.ID, ID: claimed.ID, State: string(domain.WorktreeFailed),
				Path: wtPath, BaseBranch: mg.BaseBranch, Error: msg, Now: t.now}); err != nil {
				return mapErr(err, "finish worktree")
			}
			return t.activity(domain.SystemActor, co.ID, "task.worktree_failed", "task", tv.ID, map[string]any{"branch": branch, "error": msg}, "")
		})
		countCoord(ctx, "git", "worktree_failed")
		return nil, errors.Join(fmt.Errorf("criar o worktree de %s: %w", tv.Key, gerr), ferr)
	}
	err = s.withTx(fctx, func(t *txc) error {
		if _, err := t.q.FinishTaskWorktree(t.ctx, store.FinishTaskWorktreeParams{CompanyID: co.ID, ID: claimed.ID, State: string(domain.WorktreeReady),
			BaseSha: &baseSHA, HeadSha: &headSHA, Path: wtPath, BaseBranch: mg.BaseBranch, Now: t.now}); err != nil {
			return mapErr(err, "finish worktree")
		}
		return t.activity(domain.SystemActor, co.ID, "task.worktree_ready", "task", tv.ID,
			map[string]any{"branch": branch, "base_branch": mg.BaseBranch, "base_sha": baseSHA}, "")
	})
	if err != nil {
		return nil, err
	}
	countCoord(ctx, "git", "worktree_ready")
	return &WorktreePrep{Dir: wtPath, Branch: branch, Warning: warn}, nil
}

// ---------- limpeza ----------

// ReapResult resume uma rodada de limpeza.
type ReapResult struct {
	Removed, Failed, Kept int
}

// ReapWorktrees limpa os worktrees que acabaram (task concluída ou cancelada) ou cuja branch já foi integrada à
// base, e encerra provisionamentos abandonados. Roda no tick do heartbeat; é idempotente. Trabalho solto numa pasta
// que vai sumir é registrado num commit de segurança na branch antes (nada se perde). A pasta sai do disco; a
// branch fica no espelho (apagada só quando integrada).
func (s *Service) ReapWorktrees(ctx context.Context, limit int) (ReapResult, error) {
	var res ReapResult
	if !s.GitWorkspaces.Enabled || s.WorkspacesRoot == "" {
		return res, nil
	}
	now := s.Now().UTC()
	rows, err := s.read().ListLiveWorktrees(ctx, store.ListLiveWorktreesParams{StaleBefore: ptr(now.Add(-gitStaleAfter)), Lim: i32(clamp(limit, 50, 500))})
	if err != nil {
		return res, fmt.Errorf("list live worktrees: %w", err)
	}
	var errs []error
	for _, r := range rows {
		if ctx.Err() != nil {
			break
		}
		reason, remove := reapDecision(r)
		if !remove {
			merged, merr := s.branchMerged(ctx, r)
			switch {
			case merr != nil:
				res.Kept++
				continue
			case merged:
				reason, remove = domain.RemovedMerged, true
			}
		}
		if !remove {
			res.Kept++
			continue
		}
		if err := s.removeWorktree(ctx, r, reason); err != nil {
			res.Failed++
			errs = append(errs, fmt.Errorf("%s: %w", domain.TaskKey(r.TaskPrefix, r.TaskNumber), err))
			continue
		}
		res.Removed++
	}
	return res, errors.Join(errs...)
}

// reapDecision diz se o worktree deve sair só pelo estado da task ou do próprio worktree.
func reapDecision(r store.ListLiveWorktreesRow) (domain.RemoveReason, bool) {
	switch domain.TaskStatus(r.TaskStatus) {
	case domain.TaskDone:
		return domain.RemovedDone, true
	case domain.TaskCancelled:
		return domain.RemovedCancelled, true
	}
	if domain.WorktreeState(r.State) == domain.WorktreeProvisioning || domain.WorktreeState(r.State) == domain.WorktreeFailed {
		return domain.RemovedManual, true // provisionamento abandonado ou falho: nada a preservar
	}
	return "", false
}

// branchMerged diz se a branch da task já está contida na base do remoto (e teve trabalho) e a pasta está limpa.
func (s *Service) branchMerged(ctx context.Context, r store.ListLiveWorktreesRow) (bool, error) {
	if domain.WorktreeState(r.State) != domain.WorktreeReady || r.BaseSha == nil || r.MirrorBase == "" {
		return false, nil
	}
	co, err := s.read().GetCompanyByID(ctx, r.CompanyID)
	if err != nil {
		return false, err
	}
	mirror, _ := s.gitPaths(co.Slug, r.WorkspaceSlug)
	rctx, cancel := context.WithTimeout(ctx, gitReadTimeout)
	defer cancel()
	g := s.gitRunner()
	head, err := g.RevParse(rctx, mirror, "refs/heads/"+r.Branch)
	if err != nil || head == *r.BaseSha {
		return false, nil // sem commits próprios: ainda não há o que integrar
	}
	if st, serr := g.WorktreeStatus(rctx, r.Path); serr != nil || st.Dirty() {
		return false, serr
	}
	return g.IsAncestor(rctx, mirror, head, gitrepo.RemoteRef(r.MirrorBase))
}

// removeWorktree tira a pasta do disco e grava o fim do worktree.
func (s *Service) removeWorktree(ctx context.Context, r store.ListLiveWorktreesRow, reason domain.RemoveReason) error {
	ctx, end := coordSpan(ctx, "hanko.git.worktree.remove", attribute.String("hanko.task_id", r.TaskID.String()), attribute.String("hanko.git.reason", string(reason)))
	defer end()
	co, err := s.read().GetCompanyByID(ctx, r.CompanyID)
	if err != nil {
		return mapErr(err, "company")
	}
	mirror, wtBase := s.gitPaths(co.Slug, r.WorkspaceSlug)
	if r.Path != "" && !strings.HasPrefix(r.Path, wtBase+string(filepath.Separator)) {
		return fmt.Errorf("o caminho %q está fora da pasta de worktrees: %w", r.Path, domain.ErrInvalid)
	}
	g := s.gitRunner()
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), gitReadTimeout*2)
	defer cancel()
	unlock := s.lockMirror(mirror)
	snapshot := false
	var head string
	if _, serr := os.Stat(r.Path); serr == nil && r.Path != "" {
		if domain.WorktreeState(r.State) != domain.WorktreeProvisioning && domain.WorktreeState(r.State) != domain.WorktreeFailed {
			ok, cerr := g.CommitAll(rctx, r.Path, gitSnapshotMessage)
			if cerr != nil {
				unlock()
				return fmt.Errorf("guardar o trabalho solto antes de limpar: %w", cerr)
			}
			snapshot = ok
		}
		if h, herr := g.RevParse(rctx, r.Path, "HEAD"); herr == nil {
			head = h
		}
	}
	if head == "" {
		if h, herr := g.RevParse(rctx, mirror, "refs/heads/"+r.Branch); herr == nil {
			head = h
		}
	}
	rmErr := g.RemoveWorktree(rctx, mirror, r.Path)
	if rmErr == nil && reason == domain.RemovedMerged {
		rmErr = g.DeleteBranch(rctx, mirror, r.Branch)
	}
	unlock()
	if rmErr != nil {
		return fmt.Errorf("remover o worktree: %w", rmErr)
	}
	err = s.withTx(context.WithoutCancel(ctx), func(t *txc) error {
		p := store.FinishTaskWorktreeParams{CompanyID: r.CompanyID, ID: r.ID, State: string(domain.WorktreeRemoved), Path: r.Path,
			BaseBranch: r.BaseBranch, RemovedReason: string(reason), RemovedAt: &t.now, Now: t.now}
		if head != "" {
			p.HeadSha = &head
		}
		if _, err := t.q.FinishTaskWorktree(t.ctx, p); err != nil {
			return mapErr(err, "finish worktree")
		}
		return t.activity(domain.SystemActor, r.CompanyID, "task.worktree_removed", "task", r.TaskID,
			map[string]any{"branch": r.Branch, "reason": reason, "head_sha": head, "snapshot": snapshot, "branch_deleted": reason == domain.RemovedMerged}, "")
	})
	if err == nil {
		countCoord(ctx, "git", "worktree_removed")
	}
	return err
}

// RemoveTaskWorktree limpa à mão o worktree de uma task (admin), por exemplo para recomeçar do zero.
func (s *Service) RemoveTaskWorktree(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID) error {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return err
	}
	if err := s.gitOn(); err != nil {
		return err
	}
	cur, err := s.read().GetTaskWorktree(ctx, store.GetTaskWorktreeParams{CompanyID: companyID, TaskID: taskID})
	if err != nil {
		return mapErr(err, "worktree da task")
	}
	if domain.WorktreeState(cur.State) == domain.WorktreeRemoved {
		return nil
	}
	ws, err := s.read().GetWorkspaceByID(ctx, store.GetWorkspaceByIDParams{CompanyID: companyID, ID: cur.WorkspaceID})
	if err != nil {
		return mapErr(err, "get workspace")
	}
	row := store.ListLiveWorktreesRow{ID: cur.ID, CompanyID: cur.CompanyID, TaskID: cur.TaskID, WorkspaceID: cur.WorkspaceID, Branch: cur.Branch,
		BaseBranch: cur.BaseBranch, BaseSha: cur.BaseSha, HeadSha: cur.HeadSha, Path: cur.Path, State: cur.State, WorkspaceSlug: ws.Slug}
	_ = actor // a atividade sai com o ator sistema do removeWorktree; o ator humano entra no evento abaixo
	if err := s.removeWorktree(ctx, row, domain.RemovedManual); err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		return t.activity(actor, companyID, "task.worktree_cleaned", "task", taskID, map[string]any{"branch": cur.Branch}, "")
	})
}

// ---------- leitura (aba Git) ----------

// TaskGitView é o retrato Git de uma task.
type TaskGitView struct {
	Worktree *WorktreeView `json:"worktree,omitempty"`
	// Live: a pasta existe (status e arquivos vêm dela); senão vêm da branch no espelho.
	Live   bool            `json:"live"`
	Status *gitrepo.Status `json:"status,omitempty"`
	// Ahead são os commits da branch desde a base; Behind, os que a base ganhou desde então.
	Ahead   int    `json:"ahead"`
	Behind  int    `json:"behind"`
	Merged  bool   `json:"merged"`
	HeadSHA string `json:"head_sha,omitempty"`
	// MirrorState e MirrorError explicam um espelho indisponível.
	MirrorState domain.MirrorState `json:"mirror_state"`
	MirrorError string             `json:"mirror_error,omitempty"`
}

// worktreeAccess é o que as leituras precisam para falar com o git.
type worktreeAccess struct {
	row    store.TaskWorktree
	view   WorktreeView
	mirror string
	live   bool
	head   string
	wg     store.WorkspaceGit
}

func (s *Service) worktreeAccessFor(ctx context.Context, companyID, taskID domain.ID) (worktreeAccess, error) {
	var a worktreeAccess
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return a, err
	}
	if s.WorkspacesRoot == "" {
		return a, notFound("worktree da task")
	}
	q := s.read()
	row, err := q.GetTaskWorktree(ctx, store.GetTaskWorktreeParams{CompanyID: companyID, TaskID: taskID})
	if err != nil {
		return a, mapErr(err, "worktree da task")
	}
	tv, err := loadTaskView(ctx, q, companyID, taskID)
	if err != nil {
		return a, err
	}
	ws, err := q.GetWorkspaceByID(ctx, store.GetWorkspaceByIDParams{CompanyID: companyID, ID: row.WorkspaceID})
	if err != nil {
		return a, mapErr(err, "get workspace")
	}
	co, err := q.GetCompanyByID(ctx, companyID)
	if err != nil {
		return a, mapErr(err, "company")
	}
	wg, err := q.GetWorkspaceGit(ctx, store.GetWorkspaceGitParams{CompanyID: companyID, WorkspaceID: row.WorkspaceID})
	if err != nil {
		return a, mapErr(err, "workspace git")
	}
	a.row, a.wg = row, wg
	a.view = WorktreeView{TaskWorktree: worktreeFromRow(row), TaskKey: tv.Key, TaskTitle: tv.Title, TaskStatus: string(tv.Status), ProjectID: tv.ProjectID}
	a.mirror, _ = s.gitPaths(co.Slug, ws.Slug)
	if domain.WorktreeState(row.State) != domain.WorktreeRemoved && row.Path != "" {
		if st, serr := os.Stat(row.Path); serr == nil && st.IsDir() {
			a.live = true
		}
	}
	if _, serr := os.Stat(filepath.Join(a.mirror, "HEAD")); serr != nil {
		return a, errNoMirror
	}
	switch {
	case a.live:
		rctx, cancel := context.WithTimeout(ctx, gitReadTimeout)
		defer cancel()
		if h, herr := s.gitRunner().RevParse(rctx, row.Path, "HEAD"); herr == nil {
			a.head = h
		}
	case row.HeadSha != nil:
		a.head = *row.HeadSha
	}
	return a, nil
}

// TaskGit devolve o retrato Git da task: o worktree, o status e quantos commits a branch tem à frente e atrás da
// base. Sem worktree, ErrNotFound.
func (s *Service) TaskGit(ctx context.Context, companyID, taskID domain.ID) (TaskGitView, error) {
	a, err := s.worktreeAccessFor(ctx, companyID, taskID)
	if err != nil && !errors.Is(err, errNoMirror) {
		return TaskGitView{}, err
	}
	v := TaskGitView{Worktree: &a.view, Live: a.live, HeadSHA: a.head, MirrorState: domain.MirrorState(a.wg.MirrorState), MirrorError: a.wg.LastError}
	if err != nil { // espelho sumiu do disco: a aba mostra o registro, sem git
		v.Live = false
		return v, nil
	}
	ctx, end := coordSpan(ctx, "hanko.git.inspect", attribute.String("hanko.task_id", taskID.String()))
	defer end()
	rctx, cancel := context.WithTimeout(ctx, gitReadTimeout)
	defer cancel()
	g := s.gitRunner()
	dir := a.mirror
	if a.live {
		dir = a.row.Path
		st, serr := g.WorktreeStatus(rctx, dir)
		if serr != nil {
			return v, fmt.Errorf("status: %w", serr)
		}
		v.Status = &st
	}
	if a.head != "" && a.row.BaseSha != nil {
		if n, cerr := g.CountBetween(rctx, dir, *a.row.BaseSha, a.head); cerr == nil {
			v.Ahead = n
		}
		if a.wg.BaseBranch != "" {
			ref := gitrepo.RemoteRef(a.wg.BaseBranch)
			if n, cerr := g.CountBetween(rctx, dir, a.head, ref); cerr == nil {
				v.Behind = n
			}
			if v.Ahead > 0 {
				if ok, merr := g.IsAncestor(rctx, dir, a.head, ref); merr == nil {
					v.Merged = ok
				}
			}
		}
	}
	return v, nil
}

// TaskGitDiff devolve o diff da branch contra a base (commits e alterações soltas, se a pasta existe).
func (s *Service) TaskGitDiff(ctx context.Context, companyID, taskID domain.ID) (gitrepo.Patch, error) {
	a, err := s.worktreeAccessFor(ctx, companyID, taskID)
	if err != nil {
		return gitrepo.Patch{}, err
	}
	if a.row.BaseSha == nil || a.head == "" {
		return gitrepo.Patch{}, notFound("diff da task")
	}
	rctx, cancel := context.WithTimeout(ctx, gitReadTimeout)
	defer cancel()
	if a.live {
		return s.gitRunner().DiffWorktree(rctx, a.row.Path, *a.row.BaseSha, domain.MaxGitOutput)
	}
	return s.gitRunner().DiffCommits(rctx, a.mirror, *a.row.BaseSha, a.head, domain.MaxGitOutput)
}

// TaskGitLog lista os commits da branch desde a base, do mais novo para o mais antigo.
func (s *Service) TaskGitLog(ctx context.Context, companyID, taskID domain.ID, limit int) ([]gitrepo.Commit, error) {
	a, err := s.worktreeAccessFor(ctx, companyID, taskID)
	if err != nil {
		return nil, err
	}
	if a.row.BaseSha == nil || a.head == "" {
		return []gitrepo.Commit{}, nil
	}
	rctx, cancel := context.WithTimeout(ctx, gitReadTimeout)
	defer cancel()
	dir := a.mirror
	if a.live {
		dir = a.row.Path
	}
	return s.gitRunner().Log(rctx, dir, *a.row.BaseSha, a.head, limit)
}

// TaskGitTree é a pasta de um browser de arquivos somente leitura.
type TaskGitTree struct {
	Path    string          `json:"path"`
	Entries []gitrepo.Entry `json:"entries"`
}

// TaskGitTree lista uma pasta do worktree (ou da branch, se a pasta já foi limpa).
func (s *Service) TaskGitTree(ctx context.Context, companyID, taskID domain.ID, path string) (TaskGitTree, error) {
	clean, err := domain.CleanRepoPath(path)
	if err != nil {
		return TaskGitTree{}, err
	}
	a, err := s.worktreeAccessFor(ctx, companyID, taskID)
	if err != nil {
		return TaskGitTree{}, err
	}
	if a.live {
		ents, lerr := gitrepo.ListDir(a.row.Path, clean)
		if lerr != nil {
			return TaskGitTree{}, domain.Invalid("path", lerr.Error())
		}
		return TaskGitTree{Path: clean, Entries: ents}, nil
	}
	if a.head == "" {
		return TaskGitTree{}, notFound("arquivos da task")
	}
	rctx, cancel := context.WithTimeout(ctx, gitReadTimeout)
	defer cancel()
	spec := a.head
	if clean != "" {
		spec = a.head + ":" + clean
	}
	ents, err := s.gitRunner().ListTree(rctx, a.mirror, spec, "")
	if err != nil {
		return TaskGitTree{}, domain.Invalid("path", "pasta não encontrada")
	}
	return TaskGitTree{Path: clean, Entries: ents}, nil
}

// TaskGitFile lê um arquivo do worktree (ou da branch), somente leitura, até domain.MaxBrowseFile bytes.
func (s *Service) TaskGitFile(ctx context.Context, companyID, taskID domain.ID, path string) (gitrepo.File, error) {
	clean, err := domain.CleanRepoPath(path)
	if err != nil {
		return gitrepo.File{}, err
	}
	if clean == "" {
		return gitrepo.File{}, domain.Invalid("path", "informe o arquivo")
	}
	a, err := s.worktreeAccessFor(ctx, companyID, taskID)
	if err != nil {
		return gitrepo.File{}, err
	}
	if a.live {
		f, rerr := gitrepo.ReadFile(a.row.Path, clean, domain.MaxBrowseFile)
		if rerr != nil {
			return gitrepo.File{}, domain.Invalid("path", rerr.Error())
		}
		return f, nil
	}
	if a.head == "" {
		return gitrepo.File{}, notFound("arquivos da task")
	}
	rctx, cancel := context.WithTimeout(ctx, gitReadTimeout)
	defer cancel()
	f, err := s.gitRunner().ShowFile(rctx, a.mirror, a.head, clean, domain.MaxBrowseFile)
	if err != nil {
		return gitrepo.File{}, domain.Invalid("path", "arquivo não encontrado")
	}
	return f, nil
}

// WorktreeReapInterval é de quanto em quanto tempo o tick do heartbeat limpa worktrees.
const WorktreeReapInterval = 5 * time.Minute
