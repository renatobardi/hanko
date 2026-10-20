package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/gitrepo"
	"github.com/renatobardi/hanko/internal/service"
)

// Workspace de execução com Git, padrões de projeto e "Nova sessão" (ADR-0054). Ligar o Git é de admin; sincronizar,
// de membro; ler (inclusive a aba Git) é de qualquer papel. Run tokens (personas) não administram nada aqui.

type WorkspaceGit struct {
	WorkspaceID string `json:"workspace_id" format:"uuid"`
	// Available: o operador liga o recurso neste servidor (HANKO_GIT_WORKSPACES).
	Available bool `json:"available"`
	// CredentialRef é a referência ${secret:NOME} da credencial do repositório; o valor nunca é devolvido.
	CredentialRef string     `json:"credential_ref,omitempty" example:"${secret:repo-token}"`
	MirrorState   string     `json:"mirror_state" enum:"idle,syncing,ready,failed" doc:"Estado do clone espelho."`
	BaseBranch    string     `json:"base_branch,omitempty" doc:"Branch de que os worktrees das tasks partem."`
	HeadSHA       string     `json:"head_sha,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
	LastFetchAt   *time.Time `json:"last_fetch_at,omitempty"`
	Worktrees     []Worktree `json:"worktrees,omitempty" doc:"Worktrees recentes das tasks deste workspace."`
}

type Worktree struct {
	TaskID        string     `json:"task_id" format:"uuid"`
	TaskKey       string     `json:"task_key,omitempty" example:"HNK-123"`
	TaskTitle     string     `json:"task_title,omitempty"`
	TaskStatus    string     `json:"task_status,omitempty"`
	ProjectID     string     `json:"project_id,omitempty" format:"uuid"`
	Branch        string     `json:"branch" example:"hnk-123"`
	BaseBranch    string     `json:"base_branch,omitempty"`
	BaseSHA       string     `json:"base_sha,omitempty"`
	HeadSHA       string     `json:"head_sha,omitempty"`
	State         string     `json:"state" enum:"provisioning,ready,failed,diverged,removed"`
	Error         string     `json:"error,omitempty"`
	RemovedReason string     `json:"removed_reason,omitempty" enum:"done,cancelled,merged,manual,workspace_deleted"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	RemovedAt     *time.Time `json:"removed_at,omitempty"`
}

type GitStatusEntry struct {
	XY   string `json:"xy" doc:"Duas letras do porcelain (índice e árvore); ?? = não rastreado."`
	Path string `json:"path"`
}

type GitStatus struct {
	Branch  string           `json:"branch,omitempty"`
	Head    string           `json:"head"`
	Dirty   bool             `json:"dirty"`
	Entries []GitStatusEntry `json:"entries"`
}

type TaskGit struct {
	Worktree *Worktree  `json:"worktree,omitempty"`
	Live     bool       `json:"live" doc:"A pasta do worktree existe; senão o status e os arquivos vêm da branch guardada no espelho."`
	Status   *GitStatus `json:"status,omitempty"`
	Ahead    int        `json:"ahead" doc:"Commits da branch desde a base."`
	Behind   int        `json:"behind" doc:"Commits que a base ganhou desde então."`
	Merged   bool       `json:"merged" doc:"A branch já está contida na base do remoto."`
	HeadSHA  string     `json:"head_sha,omitempty"`

	MirrorState string `json:"mirror_state"`
	MirrorError string `json:"mirror_error,omitempty"`
}

type GitPatch struct {
	Text      string `json:"text" doc:"Diff unificado contra a base."`
	Truncated bool   `json:"truncated" doc:"O diff passou do teto (512 KiB) e foi cortado."`
	Stat      string `json:"stat,omitempty"`
}

type GitCommit struct {
	SHA     string    `json:"sha"`
	Author  string    `json:"author"`
	At      time.Time `json:"at"`
	Subject string    `json:"subject"`
}

type GitEntry struct {
	Name string `json:"name"`
	Dir  bool   `json:"dir"`
	Size int64  `json:"size"`
}

type GitTree struct {
	Path    string     `json:"path"`
	Entries []GitEntry `json:"entries"`
}

type GitFile struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Content   string `json:"content,omitempty"`
	Binary    bool   `json:"binary"`
	Truncated bool   `json:"truncated"`
}

type ProjectDefaults struct {
	ProjectID        string    `json:"project_id" format:"uuid"`
	Instructions     string    `json:"instructions" doc:"Briefing do projeto: entra no system prompt depois das instruções da persona."`
	Goal             string    `json:"goal_id,omitempty" format:"uuid" doc:"Meta padrão das tasks novas do projeto."`
	ImportRepoSkills bool      `json:"import_repo_skills" doc:"Importa as skills do repositório clonado do workspace do projeto."`
	SkillsPath       string    `json:"skills_path,omitempty" doc:"Caminho do repositório onde procurar skills; vazio = o repositório todo."`
	UpdatedAt        time.Time `json:"updated_at,omitempty"`
}

type TaskSessionReset struct {
	ID            string     `json:"id" format:"uuid"`
	TaskID        string     `json:"task_id" format:"uuid"`
	Reason        string     `json:"reason,omitempty"`
	By            ActorRef   `json:"by"`
	CreatedAt     time.Time  `json:"created_at"`
	ConsumedRunID string     `json:"consumed_run_id,omitempty" format:"uuid" doc:"Run que aplicou o pedido; ausente = ainda pendente."`
	ConsumedAt    *time.Time `json:"consumed_at,omitempty"`
}

func worktreeOut(w service.WorktreeView) Worktree {
	return Worktree{TaskID: idStr(w.TaskID), TaskKey: w.TaskKey, TaskTitle: w.TaskTitle, TaskStatus: w.TaskStatus, ProjectID: idPtr(w.ProjectID),
		Branch: w.Branch, BaseBranch: w.BaseBranch, BaseSHA: w.BaseSHA, HeadSHA: w.HeadSHA, State: string(w.State), Error: w.Error,
		RemovedReason: string(w.RemovedWhy), CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt, RemovedAt: w.RemovedAt}
}

func worktreesOut(ws []service.WorktreeView) []Worktree {
	out := make([]Worktree, len(ws))
	for i, w := range ws {
		out[i] = worktreeOut(w)
	}
	return out
}

func workspaceGitOut(v service.WorkspaceGitView) WorkspaceGit {
	return WorkspaceGit{WorkspaceID: idStr(v.WorkspaceID), Available: v.Available, CredentialRef: v.CredentialRef, MirrorState: string(v.MirrorState),
		BaseBranch: v.BaseBranch, HeadSHA: v.HeadSHA, LastError: v.LastError, LastFetchAt: v.LastFetchAt, Worktrees: worktreesOut(v.Worktrees)}
}

func workspaceGitFromDomain(g domain.WorkspaceGit, available bool) WorkspaceGit {
	return WorkspaceGit{WorkspaceID: idStr(g.WorkspaceID), Available: available, CredentialRef: g.CredentialRef, MirrorState: string(g.MirrorState),
		BaseBranch: g.BaseBranch, HeadSHA: g.HeadSHA, LastError: g.LastError, LastFetchAt: g.LastFetchAt}
}

func taskGitOut(v service.TaskGitView) TaskGit {
	o := TaskGit{Live: v.Live, Ahead: v.Ahead, Behind: v.Behind, Merged: v.Merged, HeadSHA: v.HeadSHA, MirrorState: string(v.MirrorState), MirrorError: v.MirrorError}
	if v.Worktree != nil {
		w := worktreeOut(*v.Worktree)
		o.Worktree = &w
	}
	if v.Status != nil {
		o.Status = &GitStatus{Branch: v.Status.Branch, Head: v.Status.Head, Dirty: v.Status.Dirty(), Entries: make([]GitStatusEntry, len(v.Status.Entries))}
		for i, e := range v.Status.Entries {
			o.Status.Entries[i] = GitStatusEntry{XY: e.XY, Path: e.Path}
		}
	}
	return o
}

func patchOut(p gitrepo.Patch) GitPatch {
	return GitPatch{Text: p.Text, Truncated: p.Truncated, Stat: p.Stat}
}

func commitsOut(cs []gitrepo.Commit) []GitCommit {
	out := make([]GitCommit, len(cs))
	for i, c := range cs {
		out[i] = GitCommit{SHA: c.SHA, Author: c.Author, At: c.At, Subject: c.Subject}
	}
	return out
}

func treeOut(t service.TaskGitTree) GitTree {
	out := GitTree{Path: t.Path, Entries: make([]GitEntry, len(t.Entries))}
	for i, e := range t.Entries {
		out.Entries[i] = GitEntry{Name: e.Name, Dir: e.Dir, Size: e.Size}
	}
	return out
}

func projectDefaultsOut(d domain.ProjectDefaults) ProjectDefaults {
	return ProjectDefaults{ProjectID: idStr(d.ProjectID), Instructions: d.Instructions, Goal: idPtr(d.GoalID),
		ImportRepoSkills: d.ImportRepoSkills, SkillsPath: d.SkillsPath, UpdatedAt: d.UpdatedAt}
}

func sessionResetOut(r domain.TaskSessionReset) TaskSessionReset {
	o := TaskSessionReset{ID: idStr(r.ID), TaskID: idStr(r.TaskID), Reason: r.Reason, By: ActorRef{Type: string(r.By.Type)},
		CreatedAt: r.CreatedAt, ConsumedRunID: idPtr(r.ConsumedRunID), ConsumedAt: r.ConsumedAt}
	if r.By.ID != nil {
		o.By.ID = idStr(*r.By.ID)
	}
	return o
}

type enableWorkspaceGitIn struct {
	WorkspacePath
	Body struct {
		CredentialRef string `json:"credential_ref,omitempty" maxLength:"200" doc:"Referência ${secret:NOME} da credencial (token https ou chave ssh); vazio = repositório público."`
		Sync          bool   `json:"sync,omitempty" doc:"Já clona/atualiza o espelho depois de gravar."`
	}
}

type taskGitFileIn struct {
	TaskPath
	Path string `query:"path" doc:"Caminho relativo à raiz do repositório."`
}

type taskGitLogIn struct {
	TaskPath
	Limit int `query:"limit" minimum:"1" maximum:"500" default:"100"`
}

type setProjectDefaultsIn struct {
	ProjectPath
	Body struct {
		Instructions     *string `json:"instructions,omitempty" doc:"Briefing do projeto (até 16 KiB); vazio limpa."`
		Goal             string  `json:"goal,omitempty" doc:"Slug ou id da meta padrão."`
		ClearGoal        bool    `json:"clear_goal,omitempty"`
		ImportRepoSkills *bool   `json:"import_repo_skills,omitempty"`
		SkillsPath       *string `json:"skills_path,omitempty"`
	}
}

type newSessionIn struct {
	TaskPath
	Body struct {
		Reason string `json:"reason,omitempty" maxLength:"500" doc:"Por que recomeçar a sessão. Fica na atividade e no briefing do próximo run."`
	}
}

func (s *server) registerGitWorkspaces(api huma.API) {
	huma.Register(api, op("get-workspace-git", http.MethodGet, "/companies/{company}/workspaces/{workspace}/git", "workspaces",
		"Configuração Git do workspace: espelho, credencial (só a referência) e worktrees recentes"),
		func(ctx context.Context, in *WorkspacePath) (*ItemOut[WorkspaceGit], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			v, err := s.svc.GetWorkspaceGit(ctx, sc.Company.ID, in.Workspace)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[WorkspaceGit]{Body: workspaceGitOut(v)}, nil
		})

	huma.Register(api, op("enable-workspace-git", http.MethodPut, "/companies/{company}/workspaces/{workspace}/git", "workspaces",
		"Liga (ou ajusta) o Git do workspace: clone espelho e worktree por task (admin; precisa de HANKO_GIT_WORKSPACES)"),
		func(ctx context.Context, in *enableWorkspaceGitIn) (*ItemOut[WorkspaceGit], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			id, err := s.workspaceRef(ctx, sc.Company.ID, in.Workspace)
			if err != nil || id == nil {
				return nil, s.fail(ctx, orNotFound(err))
			}
			g, err := s.svc.EnableWorkspaceGit(ctx, sc.Actor, sc.Company.ID, *id, service.EnableWorkspaceGitInput{CredentialRef: in.Body.CredentialRef})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			if in.Body.Sync {
				if g, err = s.svc.SyncWorkspaceMirror(ctx, sc.Actor, sc.Company.ID, *id); err != nil {
					return nil, s.fail(ctx, err)
				}
			}
			return &ItemOut[WorkspaceGit]{Body: workspaceGitFromDomain(g, true)}, nil
		})

	huma.Register(api, noContent(op("disable-workspace-git", http.MethodDelete, "/companies/{company}/workspaces/{workspace}/git", "workspaces",
		"Desliga o Git do workspace (admin); 409 enquanto houver worktree em uso")),
		func(ctx context.Context, in *WorkspacePath) (*struct{}, error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			id, err := s.workspaceRef(ctx, sc.Company.ID, in.Workspace)
			if err != nil || id == nil {
				return nil, s.fail(ctx, orNotFound(err))
			}
			if err := s.svc.DisableWorkspaceGit(ctx, sc.Actor, sc.Company.ID, *id); err != nil {
				return nil, s.fail(ctx, err)
			}
			return nil, nil
		})

	huma.Register(api, op("sync-workspace-git", http.MethodPost, "/companies/{company}/workspaces/{workspace}/git/sync", "workspaces",
		"Clona ou atualiza o espelho do repositório (membro). Falha de rede não é erro da chamada: o estado vem em mirror_state"),
		func(ctx context.Context, in *WorkspacePath) (*ItemOut[WorkspaceGit], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			id, err := s.workspaceRef(ctx, sc.Company.ID, in.Workspace)
			if err != nil || id == nil {
				return nil, s.fail(ctx, orNotFound(err))
			}
			g, err := s.svc.SyncWorkspaceMirror(ctx, sc.Actor, sc.Company.ID, *id)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[WorkspaceGit]{Body: workspaceGitFromDomain(g, true)}, nil
		})

	huma.Register(api, op("list-project-worktrees", http.MethodGet, "/companies/{company}/projects/{project}/git", "projects",
		"Worktrees das tasks do projeto (a aba Git do projeto)"),
		func(ctx context.Context, in *ProjectPath) (*ListOut[Worktree], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			id, err := s.projectRef(ctx, sc.Company.ID, in.Project)
			if err != nil || id == nil {
				return nil, s.fail(ctx, orNotFound(err))
			}
			ws, err := s.svc.ListProjectWorktrees(ctx, sc.Company.ID, *id)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ListOut[Worktree]{Body: ListBody[Worktree]{Items: worktreesOut(ws), Limit: len(ws)}}, nil
		})

	s.registerTaskGit(api)
	s.registerProjectDefaults(api)
}

// taskID resolve a task e devolve o id.
func (s *server) taskIDOf(ctx context.Context, company, ref string) (domain.ID, domain.ID, error) {
	sc, err := s.scope(ctx, company)
	if err != nil {
		return domain.ID{}, domain.ID{}, err
	}
	tv, err := s.svc.GetTask(ctx, sc.Company.ID, ref)
	if err != nil {
		return domain.ID{}, domain.ID{}, s.fail(ctx, err)
	}
	return sc.Company.ID, tv.ID, nil
}

func (s *server) registerTaskGit(api huma.API) {
	huma.Register(api, op("get-task-git", http.MethodGet, "/companies/{company}/tasks/{task}/git", "tasks",
		"Aba Git da task: worktree, branch, status e quanto a branch está à frente/atrás da base"),
		func(ctx context.Context, in *TaskPath) (*ItemOut[TaskGit], error) {
			cid, tid, err := s.taskIDOf(ctx, in.Company, in.Task)
			if err != nil {
				return nil, err
			}
			v, err := s.svc.TaskGit(ctx, cid, tid)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[TaskGit]{Body: taskGitOut(v)}, nil
		})

	huma.Register(api, op("get-task-git-diff", http.MethodGet, "/companies/{company}/tasks/{task}/git/diff", "tasks",
		"Diff da branch da task contra a base (inclui alterações ainda não commitadas)"),
		func(ctx context.Context, in *TaskPath) (*ItemOut[GitPatch], error) {
			cid, tid, err := s.taskIDOf(ctx, in.Company, in.Task)
			if err != nil {
				return nil, err
			}
			p, err := s.svc.TaskGitDiff(ctx, cid, tid)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[GitPatch]{Body: patchOut(p)}, nil
		})

	huma.Register(api, op("get-task-git-log", http.MethodGet, "/companies/{company}/tasks/{task}/git/log", "tasks",
		"Commits da branch da task desde a base"),
		func(ctx context.Context, in *taskGitLogIn) (*ListOut[GitCommit], error) {
			cid, tid, err := s.taskIDOf(ctx, in.Company, in.Task)
			if err != nil {
				return nil, err
			}
			cs, err := s.svc.TaskGitLog(ctx, cid, tid, in.Limit)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ListOut[GitCommit]{Body: ListBody[GitCommit]{Items: commitsOut(cs), Limit: in.Limit}}, nil
		})

	huma.Register(api, op("get-task-git-tree", http.MethodGet, "/companies/{company}/tasks/{task}/git/tree", "tasks",
		"Browser de arquivos somente leitura: lista uma pasta do worktree da task"),
		func(ctx context.Context, in *taskGitFileIn) (*ItemOut[GitTree], error) {
			cid, tid, err := s.taskIDOf(ctx, in.Company, in.Task)
			if err != nil {
				return nil, err
			}
			t, err := s.svc.TaskGitTree(ctx, cid, tid, in.Path)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[GitTree]{Body: treeOut(t)}, nil
		})

	huma.Register(api, op("get-task-git-file", http.MethodGet, "/companies/{company}/tasks/{task}/git/file", "tasks",
		"Browser de arquivos somente leitura: conteúdo de um arquivo do worktree da task (até 256 KiB; binário não devolve conteúdo)"),
		func(ctx context.Context, in *taskGitFileIn) (*ItemOut[GitFile], error) {
			cid, tid, err := s.taskIDOf(ctx, in.Company, in.Task)
			if err != nil {
				return nil, err
			}
			f, err := s.svc.TaskGitFile(ctx, cid, tid, in.Path)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[GitFile]{Body: GitFile{Path: f.Path, Size: f.Size, Content: f.Content, Binary: f.Binary, Truncated: f.Truncated}}, nil
		})

	huma.Register(api, noContent(op("remove-task-worktree", http.MethodDelete, "/companies/{company}/tasks/{task}/git/worktree", "tasks",
		"Limpa o worktree da task (admin); a branch e o trabalho solto ficam no espelho")),
		func(ctx context.Context, in *TaskPath) (*struct{}, error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			tv, err := s.svc.GetTask(ctx, sc.Company.ID, in.Task)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			if err := s.svc.RemoveTaskWorktree(ctx, sc.Actor, sc.Company.ID, tv.ID); err != nil {
				return nil, s.fail(ctx, err)
			}
			return nil, nil
		})

	huma.Register(api, created(op("task-new-session", http.MethodPost, "/companies/{company}/tasks/{task}/new-session", "tasks",
		"Nova sessão: o próximo run da task ignora a sessão anterior e usa só o briefing e a memória (membro)")),
		func(ctx context.Context, in *newSessionIn) (*ItemOut[TaskSessionReset], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			tv, err := s.svc.GetTask(ctx, sc.Company.ID, in.Task)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			r, err := s.svc.RequestTaskNewSession(ctx, sc.Actor, sc.Company.ID, tv.ID, in.Body.Reason)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[TaskSessionReset]{Body: sessionResetOut(r)}, nil
		})
}

func (s *server) registerProjectDefaults(api huma.API) {
	huma.Register(api, op("get-project-defaults", http.MethodGet, "/companies/{company}/projects/{project}/defaults", "projects",
		"Padrões do projeto: instruções, meta padrão e import das skills do repositório (a política de revisão fica em /review-policy)"),
		func(ctx context.Context, in *ProjectPath) (*ItemOut[ProjectDefaults], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			id, err := s.projectRef(ctx, sc.Company.ID, in.Project)
			if err != nil || id == nil {
				return nil, s.fail(ctx, orNotFound(err))
			}
			d, err := s.svc.GetProjectDefaults(ctx, sc.Company.ID, *id)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[ProjectDefaults]{Body: projectDefaultsOut(d)}, nil
		})

	huma.Register(api, op("set-project-defaults", http.MethodPatch, "/companies/{company}/projects/{project}/defaults", "projects",
		"Altera os padrões do projeto (admin); campo ausente fica como está"),
		func(ctx context.Context, in *setProjectDefaultsIn) (*ItemOut[ProjectDefaults], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			id, err := s.projectRef(ctx, sc.Company.ID, in.Project)
			if err != nil || id == nil {
				return nil, s.fail(ctx, orNotFound(err))
			}
			goal, err := s.goalRef(ctx, sc.Company.ID, in.Body.Goal)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			d, err := s.svc.SetProjectDefaults(ctx, sc.Actor, sc.Company.ID, *id, service.SetProjectDefaultsInput{Instructions: in.Body.Instructions,
				GoalID: goal, ClearGoal: in.Body.ClearGoal, ImportRepoSkills: in.Body.ImportRepoSkills, SkillsPath: in.Body.SkillsPath})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[ProjectDefaults]{Body: projectDefaultsOut(d)}, nil
		})
}
