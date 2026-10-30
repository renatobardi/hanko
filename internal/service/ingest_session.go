package service

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/ingest"
	"github.com/renatobardi/hanko/internal/store"
)

// Sessão externa (ADR-0078 item 2): o `oute.task.id` que a fonte declara agrupa as conversas. Cada atributo da sessão
// guarda a hora do fato mais antigo que o trouxe (00213), como a conversa.

const (
	maxSessionTask = 200 // external_session.task_id: 1 a 200
	maxRoundName   = 200 // external_session.round: texto livre, limite do lote
)

// attr é um atributo da sessão: o texto e a hora do fato mais antigo que o trouxe no lote.
type attr struct {
	text string
	at   int64
	ok   bool
}

func (a *attr) offer(text string, at int64) {
	if !a.ok || at < a.at {
		a.text, a.at, a.ok = text, at, true
	}
}

// sessionFacts é o que um lote traz para uma sessão.
type sessionFacts struct {
	repo, agent, round attr
	first, last        int64
}

func (s *sessionFacts) note(f ingest.Fixed, at int64) {
	if t, ok := entityText(textOf(f.OuteRepo), maxRepoKey); ok {
		s.repo.offer(t, at)
	}
	if t, ok := entityText(textOf(f.OuteAgent), maxEntityName); ok {
		s.agent.offer(t, at)
	}
	if t, ok := entityText(textOf(f.OuteSwarmRound), maxRoundName); ok {
		s.round.offer(t, at)
	}
	s.first = min(s.first, at)
	s.last = max(s.last, at)
}

// upsertSession cria a sessão do task (com `activity_event`, uma vez) e aplica os atributos do lote pela regra do
// UPDATE, que só troca quando o fato é mais antigo que o guardado.
func upsertSession(t *txc, companyID domain.ID, task string, s *sessionFacts) error {
	newID := domain.NewID()
	id, err := t.q.InsertExternalSession(t.ctx, store.InsertExternalSessionParams{ID: newID, CompanyID: companyID, TaskID: task, Now: t.now})
	if errors.Is(err, pgx.ErrNoRows) {
		id, err = t.q.ExternalSessionIDByTask(t.ctx, store.ExternalSessionIDByTaskParams{CompanyID: companyID, TaskID: task})
	} else if err == nil {
		if err := t.activity(domain.SystemActor, companyID, "external_session.created", "external_session", id,
			map[string]any{"task_id": task}, ""); err != nil {
			return err
		}
	}
	if err != nil {
		return fmt.Errorf("ingest: sessão %q: %w", task, err)
	}
	p := store.UpdateExternalSessionFactsParams{ID: id, Now: t.now, FirstAt: s.first, LastAt: s.last}
	if s.repo.ok {
		at := s.repo.at
		p.RepoAt, p.OuteRepo = &at, s.repo.text
		ws, err := repoWorkspace(t, companyID, s.repo.text)
		if err != nil {
			return err
		}
		p.WorkspaceID = ws
	}
	if s.agent.ok {
		agentID, err := resolveEntity(t, companyID, s.agent.text, agentEntity)
		if err != nil {
			return err
		}
		at := s.agent.at
		p.AgentAt, p.AgentID = &at, &agentID
	}
	if s.round.ok {
		at := s.round.at
		p.RoundAt, p.Round = &at, s.round.text
	}
	if err := t.q.UpdateExternalSessionFacts(t.ctx, p); err != nil {
		return fmt.Errorf("ingest: atualizar a sessão %q: %w", task, err)
	}
	return nil
}

// repoWorkspace devolve o workspace do emparelhamento do repositório, ou nil quando ele não tem um.
func repoWorkspace(t *txc, companyID domain.ID, repo string) (*domain.ID, error) {
	id, err := t.q.RepoPairingWorkspaceID(t.ctx, store.RepoPairingWorkspaceIDParams{CompanyID: companyID, RepoKey: repo})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ingest: workspace do repositório: %w", err)
	}
	return &id, nil
}
