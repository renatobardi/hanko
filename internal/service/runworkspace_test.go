package service

import (
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// A ordem de resolução do diretório de uma execução (ADR-0025): task > projeto > persona > nenhum.
func TestPickWorkspaceOrder(t *testing.T) {
	task, proj, pers := domain.NewID(), domain.NewID(), domain.NewID()
	withWS := func(id domain.ID) *store.Project { return &store.Project{WorkspaceID: &id} }
	persona := func(id *domain.ID) store.Persona { return store.Persona{WorkspaceID: id} }

	tests := []struct {
		name    string
		task    *domain.ID
		project *store.Project
		persona store.Persona
		want    *domain.ID
		source  domain.WorkspaceSource
	}{
		{"task vence tudo", &task, withWS(proj), persona(&pers), &task, domain.WorkspaceFromTask},
		{"projeto vence a persona", nil, withWS(proj), persona(&pers), &proj, domain.WorkspaceFromProject},
		{"persona quando o projeto não tem workspace", nil, &store.Project{}, persona(&pers), &pers, domain.WorkspaceFromPersona},
		{"persona quando a task não tem projeto", nil, nil, persona(&pers), &pers, domain.WorkspaceFromPersona},
		{"nenhum", nil, &store.Project{}, persona(nil), nil, domain.WorkspaceNone},
		{"nenhum sem projeto", nil, nil, persona(nil), nil, domain.WorkspaceNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, src := pickWorkspace(tt.task, tt.project, tt.persona)
			if src != tt.source {
				t.Errorf("origem = %q, quer %q", src, tt.source)
			}
			if (got == nil) != (tt.want == nil) || got != nil && *got != *tt.want {
				t.Errorf("workspace = %v, quer %v", got, tt.want)
			}
		})
	}
}
