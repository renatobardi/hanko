package service

import (
	"fmt"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// pickWorkspace chooses the workspace of a run and says where the choice came from. Precedence (ADR-0025): the
// workspace chosen on the task, then the project's, then the persona's; with none, the run gets a temporary
// directory (source none). A workspace id that is set always points to an existing workspace of the company (the
// foreign keys clear it when the workspace is deleted), so "the project's workspace exists" is "it is set".
func pickWorkspace(taskWS *domain.ID, project *store.Project, persona store.Persona) (*domain.ID, domain.WorkspaceSource) {
	switch {
	case taskWS != nil:
		return taskWS, domain.WorkspaceFromTask
	case project != nil && project.WorkspaceID != nil:
		return project.WorkspaceID, domain.WorkspaceFromProject
	case persona.WorkspaceID != nil:
		return persona.WorkspaceID, domain.WorkspaceFromPersona
	}
	return nil, domain.WorkspaceNone
}

// projectOf loads the project of a task (nil when the task has none).
func (t *txc) projectOf(companyID domain.ID, id *domain.ID) (*store.Project, error) {
	if id == nil {
		return nil, nil
	}
	pr, err := t.q.GetProjectByID(t.ctx, store.GetProjectByIDParams{CompanyID: companyID, ID: *id})
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get project: %w", err)
	}
	return &pr, nil
}
