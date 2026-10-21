package uitest

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/gitrepo"
	"github.com/renatobardi/hanko/internal/service"
)

// GitFakeState é o estado em memória do workspace com Git (ADR-0054) do Fake.
type GitFakeState struct {
	// Operator liga o recurso (HANKO_GIT_WORKSPACES).
	Operator bool
	// Workspaces é a configuração Git por slug de workspace.
	Workspaces map[string]domain.WorkspaceGit
	// Worktrees é o worktree por task.
	Worktrees map[domain.ID]service.WorktreeView
	// Status, Patch, Commits, Dirs e Files alimentam a aba Git da task (Dirs por caminho; Files por caminho).
	Status  gitrepo.Status
	Patch   gitrepo.Patch
	Commits []gitrepo.Commit
	Dirs    map[string][]gitrepo.Entry
	Files   map[string]gitrepo.File
	// Defaults são os padrões por projeto; Resets, os pedidos de nova sessão.
	Defaults map[domain.ID]domain.ProjectDefaults
	Resets   []domain.TaskSessionReset
}

func (f *Fake) GitWorkspacesEnabled() bool { return f.Git.Operator }

func (f *Fake) GetWorkspaceGit(_ context.Context, _ domain.ID, ref string) (service.WorkspaceGitView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v := service.WorkspaceGitView{Available: f.Git.Operator, Worktrees: []service.WorktreeView{}}
	g, ok := f.Git.Workspaces[ref]
	if !ok {
		return v, fmt.Errorf("workspace git: %w", domain.ErrNotFound)
	}
	v.WorkspaceGit = g
	for _, w := range f.Git.Worktrees {
		v.Worktrees = append(v.Worktrees, w)
	}
	return v, nil
}

func (f *Fake) workspaceSlug(id domain.ID) string {
	for _, w := range f.Workspaces {
		if w.ID == id {
			return w.Slug
		}
	}
	return ""
}

func (f *Fake) EnableWorkspaceGit(_ context.Context, _ domain.Actor, _, workspaceID domain.ID, in service.EnableWorkspaceGitInput) (domain.WorkspaceGit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("EnableWorkspaceGit")
	if err := f.fail("EnableWorkspaceGit"); err != nil {
		return domain.WorkspaceGit{}, err
	}
	if f.Git.Workspaces == nil {
		f.Git.Workspaces = map[string]domain.WorkspaceGit{}
	}
	slug := f.workspaceSlug(workspaceID)
	g := f.Git.Workspaces[slug]
	g.WorkspaceID, g.CredentialRef = workspaceID, in.CredentialRef
	if g.MirrorState == "" {
		g.MirrorState = domain.MirrorIdle
	}
	f.Git.Workspaces[slug] = g
	return g, nil
}

func (f *Fake) DisableWorkspaceGit(_ context.Context, _ domain.Actor, _, workspaceID domain.ID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("DisableWorkspaceGit")
	delete(f.Git.Workspaces, f.workspaceSlug(workspaceID))
	return f.fail("DisableWorkspaceGit")
}

func (f *Fake) SyncWorkspaceMirror(_ context.Context, _ domain.Actor, _, workspaceID domain.ID) (domain.WorkspaceGit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("SyncWorkspaceMirror")
	if err := f.fail("SyncWorkspaceMirror"); err != nil {
		return domain.WorkspaceGit{}, err
	}
	slug := f.workspaceSlug(workspaceID)
	g := f.Git.Workspaces[slug]
	now := time.Now().UTC()
	g.MirrorState, g.BaseBranch, g.HeadSHA, g.LastFetchAt = domain.MirrorReady, "main", strings.Repeat("a", 40), &now
	f.Git.Workspaces[slug] = g
	return g, nil
}

func (f *Fake) ListProjectWorktrees(_ context.Context, _, _ domain.ID) ([]service.WorktreeView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []service.WorktreeView
	for _, w := range f.Git.Worktrees {
		out = append(out, w)
	}
	return out, nil
}

func (f *Fake) TaskGit(_ context.Context, _, taskID domain.ID) (service.TaskGitView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w, ok := f.Git.Worktrees[taskID]
	if !ok {
		return service.TaskGitView{}, fmt.Errorf("worktree da task: %w", domain.ErrNotFound)
	}
	live := w.State != domain.WorktreeRemoved
	v := service.TaskGitView{Worktree: &w, Live: live, Ahead: len(f.Git.Commits), HeadSHA: w.HeadSHA, MirrorState: domain.MirrorReady}
	if live {
		st := f.Git.Status
		v.Status = &st
	}
	return v, nil
}

func (f *Fake) TaskGitDiff(_ context.Context, _, _ domain.ID) (gitrepo.Patch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Git.Patch, f.fail("TaskGitDiff")
}

func (f *Fake) TaskGitLog(_ context.Context, _, _ domain.ID, _ int) ([]gitrepo.Commit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Git.Commits, nil
}

func (f *Fake) TaskGitTree(_ context.Context, _, _ domain.ID, path string) (service.TaskGitTree, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ents, ok := f.Git.Dirs[path]
	if !ok {
		return service.TaskGitTree{}, domain.Invalid("path", "pasta não encontrada")
	}
	return service.TaskGitTree{Path: path, Entries: ents}, nil
}

func (f *Fake) TaskGitFile(_ context.Context, _, _ domain.ID, path string) (gitrepo.File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	file, ok := f.Git.Files[path]
	if !ok {
		return gitrepo.File{}, domain.Invalid("path", "arquivo não encontrado")
	}
	return file, nil
}

func (f *Fake) RemoveTaskWorktree(_ context.Context, _ domain.Actor, _, taskID domain.ID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("RemoveTaskWorktree")
	w := f.Git.Worktrees[taskID]
	w.State = domain.WorktreeRemoved
	f.Git.Worktrees[taskID] = w
	return nil
}

func (f *Fake) RequestTaskNewSession(_ context.Context, actor domain.Actor, _, taskID domain.ID, reason string) (domain.TaskSessionReset, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("RequestTaskNewSession")
	if err := f.fail("RequestTaskNewSession"); err != nil {
		return domain.TaskSessionReset{}, err
	}
	r := domain.TaskSessionReset{ID: domain.NewID(), TaskID: taskID, Reason: reason, By: actor, CreatedAt: time.Now().UTC()}
	f.Git.Resets = append(f.Git.Resets, r)
	return r, nil
}

func (f *Fake) GetProjectDefaults(_ context.Context, _, projectID domain.ID) (domain.ProjectDefaults, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.Git.Defaults[projectID]
	if !ok {
		d = domain.ProjectDefaults{ProjectID: projectID}
	}
	return d, nil
}

func (f *Fake) SetProjectDefaults(_ context.Context, _ domain.Actor, _, projectID domain.ID, in service.SetProjectDefaultsInput) (domain.ProjectDefaults, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("SetProjectDefaults")
	if f.Git.Defaults == nil {
		f.Git.Defaults = map[domain.ID]domain.ProjectDefaults{}
	}
	d := f.Git.Defaults[projectID]
	d.ProjectID = projectID
	if in.Instructions != nil {
		d.Instructions = *in.Instructions
	}
	if in.ImportRepoSkills != nil {
		d.ImportRepoSkills = *in.ImportRepoSkills
	}
	if in.SkillsPath != nil {
		d.SkillsPath = *in.SkillsPath
	}
	switch {
	case in.ClearGoal:
		d.GoalID = nil
	case in.GoalID != nil:
		d.GoalID = in.GoalID
	}
	f.Git.Defaults[projectID] = d
	return d, nil
}
