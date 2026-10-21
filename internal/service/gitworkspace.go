package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/gitrepo"
	"github.com/renatobardi/hanko/internal/secretref"
	"github.com/renatobardi/hanko/internal/store"
)

// Workspace de execução com Git (ADR-0054, revisa o ADR-0025): o repositório do workspace é espelhado no disco da
// empresa e cada task ganha um worktree com branch própria. Clonar sai do servidor, então é interruptor do operador
// (HANKO_GIT_WORKSPACES, padrão desligado) e exige a raiz dos workspaces. A credencial é uma referência
// ${secret:NOME} (ADR-0050) resolvida só na hora da busca. Só admin liga o Git de um workspace; qualquer membro
// sincroniza e qualquer papel lê.

// GitWorkspacesConfig é a política do operador para os workspaces com Git.
type GitWorkspacesConfig struct {
	// Enabled liga o recurso (HANKO_GIT_WORKSPACES). Desligado, toda operação que clona responde 422.
	Enabled bool
	// AllowPrivate libera hosts internos (HANKO_INTEGRATIONS_ALLOW_PRIVATE).
	AllowPrivate bool
	// GitBin e Timeout ajustam o git; zero usa os padrões (git do PATH, 5 min por operação de rede).
	GitBin  string
	Timeout time.Duration
	// AllowFile aceita file:// como endereço do repositório. Só os testes ligam; a configuração do servidor nunca
	// o expõe.
	AllowFile bool
}

const (
	errGitWorkspacesOff = "os workspaces com Git estão desligados neste servidor: o operador liga com HANKO_GIT_WORKSPACES=true"
	// mirrorFreshFor é quanto tempo um espelho recém-sincronizado dispensa nova busca ao criar um worktree.
	mirrorFreshFor = 2 * time.Minute
	// gitStaleAfter é quando uma sincronização ou provisionamento "em andamento" é considerado abandonado.
	gitStaleAfter = 10 * time.Minute
	// gitReadTimeout limita as leituras da aba Git (status, diff, log, arquivos).
	gitReadTimeout = 30 * time.Second
	// maxWorktreeList é o teto de worktrees listados.
	maxWorktreeList = 200
	// gitSnapshotMessage é a mensagem do commit de segurança feito antes de apagar uma pasta com trabalho solto.
	gitSnapshotMessage = "hanko: alterações não commitadas ao encerrar a task"
	// gitReservedDir é a pasta reservada do Hanko dentro da pasta da empresa (espelhos e worktrees).
	gitReservedDir = ".hanko"
)

// gitOn diz se o recurso está ligado e utilizável (raiz configurada).
func (s *Service) gitOn() error {
	if !s.GitWorkspaces.Enabled {
		return domain.Invalid("git_workspaces", errGitWorkspacesOff)
	}
	if s.WorkspacesRoot == "" {
		return domain.Invalid("git_workspaces", "configure HANKO_WORKSPACES_ROOT: é nela que ficam os espelhos e os worktrees")
	}
	return nil
}

// GitWorkspacesEnabled diz se o operador ligou o recurso (a UI esconde os controles quando não).
func (s *Service) GitWorkspacesEnabled() bool { return s.gitOn() == nil }

// gitState guarda o executor do git (recriado se a configuração do operador mudar) e os locks por espelho. Fica atrás
// de um ponteiro para que cópias do Service (testes) compartilhem o mesmo estado.
type gitState struct {
	mu       sync.Mutex
	inst     *gitrepo.Git
	instCfg  GitWorkspacesConfig
	mirrorMu sync.Map // caminho do espelho -> *sync.Mutex
}

var gitStateFallback sync.Mutex

func (s *Service) gitState() *gitState {
	if s.gitSt == nil { // Service montado sem New (testes): estado criado uma vez, sob lock global
		gitStateFallback.Lock()
		defer gitStateFallback.Unlock()
		if s.gitSt == nil {
			s.gitSt = &gitState{}
		}
	}
	return s.gitSt
}

func (s *Service) gitRunner() *gitrepo.Git {
	st := s.gitState()
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.inst == nil || st.instCfg != s.GitWorkspaces {
		c := s.GitWorkspaces
		st.inst = gitrepo.New(gitrepo.Options{GitBin: c.GitBin, Timeout: c.Timeout, AllowFile: c.AllowFile, AllowPrivate: c.AllowPrivate})
		st.instCfg = c
	}
	return st.inst
}

// lockMirror serializa, neste processo, as operações que escrevem num espelho (a configuração e as referências do
// git não toleram escritas simultâneas).
func (s *Service) lockMirror(dir string) func() {
	v, _ := s.gitState().mirrorMu.LoadOrStore(dir, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// gitPaths calcula onde ficam o espelho e a pasta de worktrees de um workspace. Tudo sob <raiz>/<empresa>/.hanko.
func (s *Service) gitPaths(companySlug, wsSlug string) (mirror, worktrees string) {
	root := s.CompanyWorkspacesRoot(companySlug)
	return filepath.Join(root, gitReservedDir, "mirrors", wsSlug+".git"), filepath.Join(root, gitReservedDir, "worktrees", wsSlug)
}

// WorktreeView é o worktree de uma task com o que a lista precisa mostrar.
type WorktreeView struct {
	domain.TaskWorktree
	TaskKey    string     `json:"task_key"`
	TaskTitle  string     `json:"task_title,omitempty"`
	TaskStatus string     `json:"task_status,omitempty"`
	ProjectID  *domain.ID `json:"project_id,omitempty"`
}

// WorkspaceGitView é a configuração Git de um workspace, o estado do espelho e os worktrees recentes.
type WorkspaceGitView struct {
	domain.WorkspaceGit
	Workspace domain.Workspace `json:"workspace"`
	// Available: o operador liga o recurso neste servidor.
	Available bool           `json:"available"`
	Worktrees []WorktreeView `json:"worktrees"`
}

func workspaceGitFromRow(r store.WorkspaceGit) domain.WorkspaceGit {
	g := domain.WorkspaceGit{WorkspaceID: r.WorkspaceID, CompanyID: r.CompanyID, CredentialRef: r.CredentialRef,
		MirrorState: domain.MirrorState(r.MirrorState), BaseBranch: r.BaseBranch, LastError: r.LastError, LastFetchAt: r.LastFetchAt,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	if r.HeadSha != nil {
		g.HeadSHA = *r.HeadSha
	}
	return g
}

func worktreeFromRow(r store.TaskWorktree) domain.TaskWorktree {
	w := domain.TaskWorktree{ID: r.ID, CompanyID: r.CompanyID, TaskID: r.TaskID, WorkspaceID: r.WorkspaceID, Branch: r.Branch,
		BaseBranch: r.BaseBranch, Path: r.Path, State: domain.WorktreeState(r.State), Error: r.Error,
		RemovedWhy: domain.RemoveReason(r.RemovedReason), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, RemovedAt: r.RemovedAt}
	if r.BaseSha != nil {
		w.BaseSHA = *r.BaseSha
	}
	if r.HeadSha != nil {
		w.HeadSHA = *r.HeadSha
	}
	return w
}

// EnableWorkspaceGitInput liga (ou ajusta) o Git de um workspace. CredentialRef é a referência `${secret:NOME}` da
// credencial (token https ou chave privada ssh); vazia = repositório público.
type EnableWorkspaceGitInput struct {
	CredentialRef string
}

// checkCredentialRef confere que a credencial é uma única referência a um segredo da empresa que existe.
func (s *Service) checkCredentialRef(ctx context.Context, q *store.Queries, companyID domain.ID, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", nil
	}
	if len(ref) > 200 || !secretref.IsRef(ref) {
		return "", domain.Invalid("credential_ref", "use uma referência de segredo no formato ${secret:NOME}")
	}
	if err := s.checkSecretRefs(ctx, q, companyID, "credential_ref", ref); err != nil {
		return "", err
	}
	return ref, nil
}

// EnableWorkspaceGit liga o Git do workspace (precisa de repo_url) e guarda a referência da credencial. A busca é
// uma ação à parte (SyncWorkspaceMirror).
func (s *Service) EnableWorkspaceGit(ctx context.Context, actor domain.Actor, companyID, workspaceID domain.ID, in EnableWorkspaceGitInput) (domain.WorkspaceGit, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.WorkspaceGit{}, err
	}
	if err := s.gitOn(); err != nil {
		return domain.WorkspaceGit{}, err
	}
	ctx, end := coordSpan(ctx, "hanko.git.enable", attribute.String("hanko.workspace_id", workspaceID.String()))
	defer end()
	var out domain.WorkspaceGit
	err := s.withTx(ctx, func(t *txc) error {
		ws, err := t.q.GetWorkspaceByID(ctx, store.GetWorkspaceByIDParams{CompanyID: companyID, ID: workspaceID})
		if err != nil {
			return mapErr(err, "get workspace")
		}
		if ws.RepoUrl == "" {
			return domain.Invalid("repo_url", "o workspace não tem repositório: informe o repo_url antes de ligar o Git")
		}
		if _, err := gitrepo.ParseRemote(ws.RepoUrl, s.gitRunnerOptions()); err != nil {
			return domain.Invalid("repo_url", err.Error())
		}
		if ws.DefaultBranch != "" && !gitrepo.ValidBranch(ws.DefaultBranch) {
			return domain.Invalid("default_branch", "branch inválido")
		}
		ref, err := s.checkCredentialRef(ctx, t.q, companyID, in.CredentialRef)
		if err != nil {
			return err
		}
		_, prevErr := t.q.GetWorkspaceGit(ctx, store.GetWorkspaceGitParams{CompanyID: companyID, WorkspaceID: workspaceID})
		row, err := t.q.UpsertWorkspaceGit(ctx, store.UpsertWorkspaceGitParams{WorkspaceID: workspaceID, CompanyID: companyID, CredentialRef: ref, Now: t.now})
		if err != nil {
			return mapErr(err, "enable workspace git")
		}
		out = workspaceGitFromRow(row)
		verb := "workspace.git_enabled"
		if prevErr == nil {
			verb = "workspace.git_updated"
		}
		return t.activity(actor, companyID, verb, "workspace", workspaceID,
			map[string]any{"slug": ws.Slug, "credential_ref": ref, "public": ref == ""}, "")
	})
	if err == nil {
		countCoord(ctx, "git", "enable")
	}
	return out, err
}

func (s *Service) gitRunnerOptions() gitrepo.Options {
	c := s.GitWorkspaces
	return gitrepo.Options{GitBin: c.GitBin, Timeout: c.Timeout, AllowFile: c.AllowFile, AllowPrivate: c.AllowPrivate}
}

// DisableWorkspaceGit desliga o Git do workspace. Recusa (409) enquanto houver worktree em uso; o espelho sai do
// disco depois do commit.
func (s *Service) DisableWorkspaceGit(ctx context.Context, actor domain.Actor, companyID, workspaceID domain.ID) error {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return err
	}
	var mirror string
	err := s.withTx(ctx, func(t *txc) error {
		ws, err := t.q.GetWorkspaceByID(ctx, store.GetWorkspaceByIDParams{CompanyID: companyID, ID: workspaceID})
		if err != nil {
			return mapErr(err, "get workspace")
		}
		live, err := t.q.ListWorkspaceWorktreesLive(ctx, store.ListWorkspaceWorktreesLiveParams{CompanyID: companyID, WorkspaceID: workspaceID})
		if err != nil {
			return fmt.Errorf("list live worktrees: %w", err)
		}
		if len(live) > 0 {
			return fmt.Errorf("há %d worktree(s) em uso neste workspace; conclua ou cancele as tasks antes de desligar o Git: %w", len(live), domain.ErrConflict)
		}
		n, err := t.q.DeleteWorkspaceGit(ctx, store.DeleteWorkspaceGitParams{CompanyID: companyID, WorkspaceID: workspaceID})
		if err != nil {
			return fmt.Errorf("delete workspace git: %w", err)
		}
		if n == 0 {
			return notFound("workspace git")
		}
		co, err := t.q.GetCompanyByID(ctx, companyID)
		if err != nil {
			return mapErr(err, "company")
		}
		mirror, _ = s.gitPaths(co.Slug, ws.Slug)
		return t.activity(actor, companyID, "workspace.git_disabled", "workspace", workspaceID, map[string]any{"slug": ws.Slug}, "")
	})
	if err == nil && mirror != "" {
		s.removeMirrorDir(mirror)
		countCoord(ctx, "git", "disable")
	}
	return err
}

// removeMirrorDir apaga um espelho do disco (melhor esforço), só se estiver dentro da pasta reservada.
func (s *Service) removeMirrorDir(mirror string) {
	if !strings.Contains(filepath.ToSlash(mirror), "/"+gitReservedDir+"/mirrors/") || !strings.HasSuffix(mirror, ".git") {
		return
	}
	unlock := s.lockMirror(mirror)
	defer unlock()
	_ = os.RemoveAll(mirror)
}

// SyncWorkspaceMirror clona (primeira vez) ou atualiza o espelho do workspace. Qualquer membro pode pedir. Uma
// falha de rede ou de credencial não é erro da chamada: o espelho fica em `failed` com o motivo.
func (s *Service) SyncWorkspaceMirror(ctx context.Context, actor domain.Actor, companyID, workspaceID domain.ID) (domain.WorkspaceGit, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return domain.WorkspaceGit{}, err
	}
	if err := s.gitOn(); err != nil {
		return domain.WorkspaceGit{}, err
	}
	return s.syncMirror(ctx, actor, companyID, workspaceID)
}

// syncMirror é a sincronização: reivindica o espelho, busca fora de transação e grava o resultado.
func (s *Service) syncMirror(ctx context.Context, actor domain.Actor, companyID, workspaceID domain.ID) (domain.WorkspaceGit, error) {
	ctx, end := coordSpan(ctx, "hanko.git.sync", attribute.String("hanko.workspace_id", workspaceID.String()))
	defer end()
	q := s.read()
	ws, err := q.GetWorkspaceByID(ctx, store.GetWorkspaceByIDParams{CompanyID: companyID, ID: workspaceID})
	if err != nil {
		return domain.WorkspaceGit{}, mapErr(err, "get workspace")
	}
	co, err := q.GetCompanyByID(ctx, companyID)
	if err != nil {
		return domain.WorkspaceGit{}, mapErr(err, "company")
	}
	now := s.Now().UTC()
	wg, err := q.ClaimMirrorSync(ctx, store.ClaimMirrorSyncParams{CompanyID: companyID, WorkspaceID: workspaceID, Now: &now, StaleBefore: ptr(now.Add(-gitStaleAfter))})
	if isNoRows(err) {
		if _, gerr := q.GetWorkspaceGit(ctx, store.GetWorkspaceGitParams{CompanyID: companyID, WorkspaceID: workspaceID}); gerr != nil {
			return domain.WorkspaceGit{}, mapErr(gerr, "o Git deste workspace não está ligado")
		}
		return domain.WorkspaceGit{}, fmt.Errorf("o espelho já está sincronizando: %w", domain.ErrConflict)
	}
	if err != nil {
		return domain.WorkspaceGit{}, mapErr(err, "claim mirror sync")
	}

	mirror, _ := s.gitPaths(co.Slug, ws.Slug)
	m, ferr := s.fetchMirror(ctx, companyID, mirror, ws, wg.CredentialRef)
	var out domain.WorkspaceGit
	err = s.withTx(context.WithoutCancel(ctx), func(t *txc) error {
		p := store.FinishMirrorSyncParams{CompanyID: companyID, WorkspaceID: workspaceID, Now: t.now}
		verb := "workspace.git_synced"
		data := map[string]any{"slug": ws.Slug}
		if ferr != nil {
			msg := clipText(ferr.Error(), maxSourceError)
			p.MirrorState, p.BaseBranch, p.LastError = string(domain.MirrorFailed), wg.BaseBranch, msg
			p.HeadSha = wg.HeadSha
			verb, data["error"] = "workspace.git_sync_failed", msg
		} else {
			p.MirrorState, p.BaseBranch, p.HeadSha, p.LastFetchAt = string(domain.MirrorReady), m.BaseBranch, &m.HeadSHA, &t.now
			data["base_branch"], data["head_sha"] = m.BaseBranch, m.HeadSHA
		}
		row, err := t.q.FinishMirrorSync(t.ctx, p)
		if err != nil {
			return mapErr(err, "finish mirror sync")
		}
		out = workspaceGitFromRow(row)
		return t.activity(actor, companyID, verb, "workspace", workspaceID, data, "")
	})
	if err != nil {
		return domain.WorkspaceGit{}, err
	}
	countCoord(ctx, "git", "sync")
	if ferr == nil {
		s.importProjectSkills(ctx, companyID, workspaceID) // melhor esforço: falhar aqui não derruba a sincronização
	}
	return out, nil
}

type mirrorResult struct {
	BaseBranch string
	HeadSHA    string
}

// fetchMirror resolve a credencial e roda a busca sob o lock do espelho.
func (s *Service) fetchMirror(ctx context.Context, companyID domain.ID, mirror string, ws store.Workspace, credRef string) (mirrorResult, error) {
	var cred *gitrepo.Credential
	if credRef != "" {
		val, _, err := s.ResolveSecretText(ctx, companyID, credRef)
		if err != nil {
			return mirrorResult{}, fmt.Errorf("credencial do repositório: %w", err)
		}
		cred = &gitrepo.Credential{Value: val}
	}
	unlock := s.lockMirror(mirror)
	defer unlock()
	m, err := s.gitRunner().SyncMirror(ctx, mirror, ws.RepoUrl, ws.DefaultBranch, cred)
	if err != nil {
		return mirrorResult{}, err
	}
	return mirrorResult{BaseBranch: m.BaseBranch, HeadSHA: m.HeadSHA}, nil
}

// GetWorkspaceGit devolve a configuração Git do workspace, o estado do espelho e os worktrees recentes. Sem Git
// ligado, ErrNotFound.
func (s *Service) GetWorkspaceGit(ctx context.Context, companyID domain.ID, ref string) (WorkspaceGitView, error) {
	ws, err := s.GetWorkspace(ctx, companyID, ref)
	if err != nil {
		return WorkspaceGitView{}, err
	}
	q := s.read()
	row, err := q.GetWorkspaceGit(ctx, store.GetWorkspaceGitParams{CompanyID: companyID, WorkspaceID: ws.ID})
	if err != nil {
		return WorkspaceGitView{Workspace: ws, Available: s.GitWorkspacesEnabled(), Worktrees: []WorktreeView{}}, mapErr(err, "workspace git")
	}
	wts, err := q.ListTaskWorktrees(ctx, store.ListTaskWorktreesParams{CompanyID: companyID, WorkspaceID: ws.ID, Lim: maxWorktreeList})
	if err != nil {
		return WorkspaceGitView{}, fmt.Errorf("list worktrees: %w", err)
	}
	v := WorkspaceGitView{WorkspaceGit: workspaceGitFromRow(row), Workspace: ws, Available: s.GitWorkspacesEnabled(), Worktrees: make([]WorktreeView, len(wts))}
	for i, w := range wts {
		v.Worktrees[i] = worktreeViewFromRow(w)
	}
	return v, nil
}

func worktreeViewFromRow(w store.ListTaskWorktreesRow) WorktreeView {
	tw := worktreeFromRow(store.TaskWorktree{ID: w.ID, CompanyID: w.CompanyID, TaskID: w.TaskID, WorkspaceID: w.WorkspaceID, Branch: w.Branch,
		BaseBranch: w.BaseBranch, BaseSha: w.BaseSha, HeadSha: w.HeadSha, Path: w.Path, State: w.State, Error: w.Error,
		RemovedReason: w.RemovedReason, ClaimedAt: w.ClaimedAt, RemovedAt: w.RemovedAt, CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt})
	return WorktreeView{TaskWorktree: tw, TaskKey: domain.TaskKey(w.TaskPrefix, w.TaskNumber), TaskTitle: w.TaskTitle, TaskStatus: w.TaskStatus, ProjectID: w.ProjectID}
}

// ListProjectWorktrees lista os worktrees das tasks de um projeto (a aba Git do projeto).
func (s *Service) ListProjectWorktrees(ctx context.Context, companyID, projectID domain.ID) ([]WorktreeView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.read().ListProjectTaskWorktrees(ctx, store.ListProjectTaskWorktreesParams{CompanyID: companyID, ProjectID: &projectID, Lim: maxWorktreeList})
	if err != nil {
		return nil, fmt.Errorf("list project worktrees: %w", err)
	}
	out := make([]WorktreeView, len(rows))
	for i, w := range rows {
		out[i] = worktreeViewFromRow(store.ListTaskWorktreesRow(w))
	}
	return out, nil
}

// WorkspaceGitEnabledFor diz se o workspace tem Git ligado (para a UI marcar na lista).
func (s *Service) WorkspaceGitEnabledFor(ctx context.Context, companyID, workspaceID domain.ID) bool {
	_, err := s.read().GetWorkspaceGit(ctx, store.GetWorkspaceGitParams{CompanyID: companyID, WorkspaceID: workspaceID})
	return err == nil
}

// cleanupWorkspaceGitFiles tira do disco o espelho e os worktrees de um workspace que está sendo excluído.
func (s *Service) cleanupWorkspaceGitFiles(ctx context.Context, companySlug, wsSlug string, worktrees []string) {
	if s.WorkspacesRoot == "" {
		return
	}
	mirror, wtBase := s.gitPaths(companySlug, wsSlug)
	for _, p := range worktrees {
		if p == "" || !strings.HasPrefix(p, wtBase+string(filepath.Separator)) {
			continue
		}
		if err := s.gitRunner().RemoveWorktree(ctx, mirror, p); err != nil {
			_ = os.RemoveAll(p)
		}
	}
	_ = os.RemoveAll(wtBase)
	s.removeMirrorDir(mirror)
}

// gitFilesCleanup prepara (dentro da transação, antes de apagar o workspace) a limpeza do disco do espelho e dos
// worktrees vivos; nil se o workspace não tem Git ligado. A função devolvida roda depois do commit.
func (s *Service) gitFilesCleanup(ctx context.Context, q *store.Queries, companyID, workspaceID domain.ID, wsSlug string) func() {
	if s.WorkspacesRoot == "" {
		return nil
	}
	if _, err := q.GetWorkspaceGit(ctx, store.GetWorkspaceGitParams{CompanyID: companyID, WorkspaceID: workspaceID}); err != nil {
		return nil
	}
	co, err := q.GetCompanyByID(ctx, companyID)
	if err != nil {
		return nil
	}
	live, _ := q.ListWorkspaceWorktreesLive(ctx, store.ListWorkspaceWorktreesLiveParams{CompanyID: companyID, WorkspaceID: workspaceID})
	paths := make([]string, 0, len(live))
	for _, w := range live {
		paths = append(paths, w.Path)
	}
	return func() {
		s.cleanupWorkspaceGitFiles(context.WithoutCancel(ctx), co.Slug, wsSlug, paths)
	}
}

var errNoMirror = errors.New("o espelho do repositório ainda não foi sincronizado")
