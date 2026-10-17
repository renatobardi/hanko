package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Visões por pessoa e atividade por ator (ADR-0042 §B).

// fillParticipation preenche, nas tasks listadas, os motivos da participação da pessoa.
func (s *Service) fillParticipation(ctx context.Context, companyID, userID domain.ID, tasks []TaskView) error {
	ids := make([]uuid.UUID, len(tasks))
	for i, t := range tasks {
		ids[i] = t.ID
	}
	rows, err := s.read().ListTaskParticipation(ctx, store.ListTaskParticipationParams{CompanyID: companyID, UserID: userID, TaskIds: ids})
	if err != nil {
		return fmt.Errorf("list task participation: %w", err)
	}
	byTask := make(map[domain.ID][]string, len(rows))
	for _, r := range rows {
		byTask[r.TaskID] = domain.Participation{Created: r.Created, Assigned: r.Assigned, Mentioned: r.Mentioned,
			Commented: r.Commented, Gate: r.Gate}.Reasons()
	}
	for i := range tasks {
		tasks[i].Participation = byTask[tasks[i].ID]
	}
	return nil
}

// ActorFilter é um ator resolvido para filtrar a atividade.
type ActorFilter struct {
	Type string
	ID   *domain.ID
}

// ResolveActor converte a referência de um ator em filtro: "system"; "human:<e-mail ou id>" (membro da empresa);
// "persona:<slug ou id>". "human" e "persona" sem referência valem para todos do tipo.
func (s *Service) ResolveActor(ctx context.Context, companyID domain.ID, ref string) (ActorFilter, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return ActorFilter{}, err
	}
	typ, val, _ := strings.Cut(strings.TrimSpace(ref), ":")
	typ, val = strings.ToLower(strings.TrimSpace(typ)), strings.TrimSpace(val)
	switch typ {
	case "system":
		return ActorFilter{Type: "system"}, nil
	case "persona":
		if val == "" {
			return ActorFilter{Type: "persona"}, nil
		}
		p, err := s.GetPersona(ctx, companyID, val)
		if err != nil {
			return ActorFilter{}, domain.Invalid("actor", "persona não encontrada nesta empresa")
		}
		return ActorFilter{Type: "persona", ID: &p.ID}, nil
	case "human":
		if val == "" {
			return ActorFilter{Type: "human"}, nil
		}
		members, err := s.read().ListMemberHandles(ctx, companyID)
		if err != nil {
			return ActorFilter{}, fmt.Errorf("list members: %w", err)
		}
		for _, m := range members {
			if strings.EqualFold(m.Email, val) || m.UserID.String() == strings.ToLower(val) {
				id := m.UserID
				return ActorFilter{Type: "human", ID: &id}, nil
			}
		}
		return ActorFilter{}, domain.Invalid("actor", "pessoa não encontrada nesta empresa")
	}
	return ActorFilter{}, domain.Invalid("actor", "use system, persona:<slug> ou human:<e-mail>")
}
