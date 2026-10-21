package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/gitrepo"
	"github.com/renatobardi/hanko/internal/skillgit"
	"github.com/renatobardi/hanko/internal/store"
)

// Padrões de projeto (#34, ADR-0054): instruções do projeto (briefing que entra no system prompt depois das da
// persona), meta padrão das tasks novas e import automático das skills do repositório clonado do workspace. A
// política de revisão do projeto já existe (ADR-0040, project_review_policies) e é reaproveitada, não duplicada.

func projectDefaultsFromRow(r store.ProjectDefault) domain.ProjectDefaults {
	return domain.ProjectDefaults{ProjectID: r.ProjectID, Instructions: r.Instructions, GoalID: r.GoalID,
		ImportRepoSkills: r.ImportRepoSkills, SkillsPath: r.SkillsPath, UpdatedAt: r.UpdatedAt}
}

// GetProjectDefaults devolve os padrões do projeto (o zero value, com o id do projeto, se nunca foram definidos).
func (s *Service) GetProjectDefaults(ctx context.Context, companyID, projectID domain.ID) (domain.ProjectDefaults, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return domain.ProjectDefaults{}, err
	}
	q := s.read()
	if _, err := q.GetProjectByID(ctx, store.GetProjectByIDParams{CompanyID: companyID, ID: projectID}); err != nil {
		return domain.ProjectDefaults{}, mapErr(err, "get project")
	}
	row, err := q.GetProjectDefaults(ctx, store.GetProjectDefaultsParams{CompanyID: companyID, ProjectID: projectID})
	if isNoRows(err) {
		return domain.ProjectDefaults{ProjectID: projectID}, nil
	}
	if err != nil {
		return domain.ProjectDefaults{}, fmt.Errorf("get project defaults: %w", err)
	}
	return projectDefaultsFromRow(row), nil
}

// SetProjectDefaultsInput altera os padrões; campo nil fica como está.
type SetProjectDefaultsInput struct {
	Instructions     *string
	GoalID           *domain.ID
	ClearGoal        bool
	ImportRepoSkills *bool
	SkillsPath       *string
}

// SetProjectDefaults grava os padrões do projeto (admin: é governança). Ligar o import de skills já tenta importar
// se o repositório do workspace estiver clonado.
func (s *Service) SetProjectDefaults(ctx context.Context, actor domain.Actor, companyID, projectID domain.ID, in SetProjectDefaultsInput) (domain.ProjectDefaults, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.ProjectDefaults{}, err
	}
	var out domain.ProjectDefaults
	var wsForImport *domain.ID
	err := s.withTx(ctx, func(t *txc) error {
		pr, err := t.q.GetProjectByID(ctx, store.GetProjectByIDParams{CompanyID: companyID, ID: projectID})
		if err != nil {
			return mapErr(err, "get project")
		}
		cur := domain.ProjectDefaults{ProjectID: projectID}
		if row, gerr := t.q.GetProjectDefaults(ctx, store.GetProjectDefaultsParams{CompanyID: companyID, ProjectID: projectID}); gerr == nil {
			cur = projectDefaultsFromRow(row)
		} else if !isNoRows(gerr) {
			return fmt.Errorf("get project defaults: %w", gerr)
		}
		next := cur
		changed := map[string]any{}
		if in.Instructions != nil {
			v := strings.TrimSpace(*in.Instructions)
			if v != cur.Instructions {
				next.Instructions = v
				sum := sha256.Sum256([]byte(v))
				changed["instructions"] = map[string]any{"bytes": len(v), "sha256": hex.EncodeToString(sum[:8])}
			}
		}
		switch {
		case in.ClearGoal && cur.GoalID != nil:
			next.GoalID, changed["goal_id"] = nil, nil
		case in.GoalID != nil && !sameID(cur.GoalID, in.GoalID):
			if err := t.checkLinkableGoal(companyID, *in.GoalID); err != nil {
				return err
			}
			next.GoalID, changed["goal_id"] = in.GoalID, *in.GoalID
		}
		if in.SkillsPath != nil {
			c, err := domain.CleanRepoPath(*in.SkillsPath)
			if err != nil {
				return domain.Invalid("skills_path", "caminho inválido")
			}
			if c != cur.SkillsPath {
				next.SkillsPath, changed["skills_path"] = c, c
			}
		}
		if in.ImportRepoSkills != nil && *in.ImportRepoSkills != cur.ImportRepoSkills {
			next.ImportRepoSkills, changed["import_repo_skills"] = *in.ImportRepoSkills, *in.ImportRepoSkills
		}
		if err := next.Validate(); err != nil {
			return err
		}
		if len(changed) == 0 {
			out = cur
			return nil
		}
		row, err := t.q.UpsertProjectDefaults(ctx, store.UpsertProjectDefaultsParams{ProjectID: projectID, CompanyID: companyID,
			Instructions: next.Instructions, GoalID: next.GoalID, ImportRepoSkills: next.ImportRepoSkills, SkillsPath: next.SkillsPath, Now: t.now})
		if err != nil {
			return mapErr(err, "upsert project defaults")
		}
		out = projectDefaultsFromRow(row)
		if next.ImportRepoSkills && pr.WorkspaceID != nil {
			wsForImport = pr.WorkspaceID
		}
		changed["project"] = pr.Slug
		return t.activity(actor, companyID, "project.defaults_updated", "project", projectID, changed, "")
	})
	if err != nil {
		return domain.ProjectDefaults{}, err
	}
	if wsForImport != nil {
		s.importProjectSkills(ctx, companyID, *wsForImport)
	}
	return out, nil
}

// defaultGoalFor devolve a meta padrão do projeto quando ela ainda aceita tasks novas (senão nil, sem erro: uma
// meta concluída não pode impedir a criação de tasks).
func (t *txc) defaultGoalFor(companyID domain.ID, projectID *domain.ID) *domain.ID {
	if projectID == nil {
		return nil
	}
	row, err := t.q.GetProjectDefaults(t.ctx, store.GetProjectDefaultsParams{CompanyID: companyID, ProjectID: *projectID})
	if err != nil || row.GoalID == nil {
		return nil
	}
	if t.checkLinkableGoal(companyID, *row.GoalID) != nil {
		return nil
	}
	return row.GoalID
}

// projectInstructions devolve o briefing do projeto para o prompt da run ("" se não houver).
func projectInstructions(ctx context.Context, q *store.Queries, companyID domain.ID, projectID *domain.ID) (string, error) {
	if projectID == nil {
		return "", nil
	}
	row, err := q.GetProjectDefaults(ctx, store.GetProjectDefaultsParams{CompanyID: companyID, ProjectID: *projectID})
	if isNoRows(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get project defaults: %w", err)
	}
	return row.Instructions, nil
}

// ---------- skills do repositório clonado ----------

const (
	workspaceSkillScheme = "workspace://"
	// workspaceSkillRef é o tracking_ref das fontes de workspace (o branch real vem do espelho).
	workspaceSkillRef = "HEAD"
)

// workspaceSkillURL é o "endereço" de uma fonte de skills que vive no espelho de um workspace.
func workspaceSkillURL(workspaceID domain.ID, path string) string {
	u := workspaceSkillScheme + workspaceID.String()
	if path != "" {
		u += "/" + path
	}
	return u
}

// parseWorkspaceSkillURL reconhece o endereço de uma fonte de workspace.
func parseWorkspaceSkillURL(raw string) (domain.ID, string, bool) {
	rest, ok := strings.CutPrefix(raw, workspaceSkillScheme)
	if !ok {
		return domain.ID{}, "", false
	}
	idPart, path, _ := strings.Cut(rest, "/")
	id, err := uuid.Parse(idPart)
	if err != nil {
		return domain.ID{}, "", false
	}
	return id, path, true
}

// fetchSkillSnapshot lê as skills de uma fonte: do espelho do workspace (sem rede) ou clonando o endereço https.
func (s *Service) fetchSkillSnapshot(ctx context.Context, src store.SkillSource) (skillgit.Snapshot, error) {
	wsID, _, ok := parseWorkspaceSkillURL(src.Url)
	if !ok {
		return skillgit.Fetch(ctx, src.Url, src.TrackingRef, skillgit.Scan{Prefix: src.PathPrefix, Excluded: src.ExcludedPaths}, s.gitOptions())
	}
	q := s.read()
	wg, err := q.GetWorkspaceGit(ctx, store.GetWorkspaceGitParams{CompanyID: src.CompanyID, WorkspaceID: wsID})
	if err != nil {
		return skillgit.Snapshot{}, fmt.Errorf("o Git do workspace não está ligado: %w", mapErr(err, "workspace git"))
	}
	if wg.MirrorState != string(domain.MirrorReady) || wg.BaseBranch == "" {
		return skillgit.Snapshot{}, errNoMirror
	}
	ws, err := q.GetWorkspaceByID(ctx, store.GetWorkspaceByIDParams{CompanyID: src.CompanyID, ID: wsID})
	if err != nil {
		return skillgit.Snapshot{}, mapErr(err, "get workspace")
	}
	co, err := q.GetCompanyByID(ctx, src.CompanyID)
	if err != nil {
		return skillgit.Snapshot{}, mapErr(err, "company")
	}
	mirror, _ := s.gitPaths(co.Slug, ws.Slug)
	return skillgit.ScanLocal(ctx, mirror, gitrepo.RemoteRef(wg.BaseBranch), skillgit.Scan{Prefix: src.PathPrefix, Excluded: src.ExcludedPaths}, s.gitOptions())
}

// importProjectSkills importa (ou atualiza) as skills do repositório clonado para os projetos do workspace que
// pediram o import. Melhor esforço: o resultado fica na fonte e na atividade; uma falha nunca derruba quem chamou.
func (s *Service) importProjectSkills(ctx context.Context, companyID, workspaceID domain.ID) {
	rows, err := s.read().ListProjectsImportingSkills(ctx, store.ListProjectsImportingSkillsParams{CompanyID: companyID, WorkspaceID: &workspaceID})
	if err != nil || len(rows) == 0 {
		return
	}
	seen := map[string]bool{}
	for _, r := range rows {
		if seen[r.SkillsPath] {
			continue
		}
		seen[r.SkillsPath] = true
		if err := s.syncWorkspaceSkills(ctx, companyID, workspaceID, r.SkillsPath); err != nil && s.Log != nil {
			s.Log.Warn("skills do repositório do workspace: sincronização falhou", "workspace_id", workspaceID, "err", err)
		}
	}
}

// syncWorkspaceSkills garante a fonte de skills do espelho (url workspace://<id>[/caminho]) e a sincroniza.
func (s *Service) syncWorkspaceSkills(ctx context.Context, companyID, workspaceID domain.ID, path string) error {
	url := workspaceSkillURL(workspaceID, path)
	q := s.read()
	src, err := q.GetSkillSourceByURL(ctx, store.GetSkillSourceByURLParams{CompanyID: companyID, Url: url, TrackingRef: workspaceSkillRef})
	var initial map[string]bool
	if isNoRows(err) {
		err = s.withTx(ctx, func(t *txc) error {
			row, cerr := t.q.CreateSkillSource(t.ctx, store.CreateSkillSourceParams{ID: domain.NewID(), CompanyID: companyID, Url: url,
				TrackingRef: workspaceSkillRef, PathPrefix: path, ExcludedPaths: []string{}, AutoUpdate: false, ImportNew: true,
				Status: string(domain.SourcePaused), ActorType: string(domain.ActorSystem), Now: t.now})
			if cerr != nil {
				return mapErr(cerr, "create workspace skill source")
			}
			src = row
			return t.activity(domain.SystemActor, companyID, "skill_source.created", "skill_source", row.ID,
				map[string]any{"url": url, "ref": workspaceSkillRef, "path_prefix": path, "workspace_id": workspaceID, "origin": "project_defaults"}, "")
		})
		initial = map[string]bool{selectAll: true}
	}
	if err != nil {
		return err
	}
	res, err := s.syncSource(ctx, domain.SystemActor, companyID, src.ID, initial, false)
	if err != nil {
		return err
	}
	if res.Error != "" {
		return fmt.Errorf("%s", res.Error)
	}
	return nil
}
