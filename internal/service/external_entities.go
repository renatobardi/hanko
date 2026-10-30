package service

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Agentes e hosts externos: listar, ver, renomear, arquivar, sempre ligado e juntar (ADR-0075 item 2; ADR-0077 itens 3
// e 4). Alterações são de owner ou admin e geram `activity_event`. Renomear guarda o nome antigo como apelido, para
// fato com o nome antigo cair no mesmo agente. Juntar move apelidos e referências; o agente juntado fica arquivado e
// ganha o próprio nome como apelido do que fica.

// ExternalAgentView é um agente externo com os textos que já chegaram por apelido.
type ExternalAgentView struct {
	ID         domain.ID
	Name       string
	Aliases    []string
	ArchivedAt *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// ExternalHostView é um host externo com o sempre ligado e a hora do último fato.
type ExternalHostView struct {
	ID         domain.ID
	Name       string
	Aliases    []string
	AlwaysOn   bool
	LastSeenAt *time.Time
	ArchivedAt *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// checkExternalName valida o nome de agente ou host como o CHECK do banco (00208): 1 a 100 caracteres, sem espaço nas
// pontas.
func checkExternalName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" || utf8.RuneCountInString(name) > maxEntityName {
		return "", domain.Invalid("name", "entre 1 e 100 caracteres")
	}
	return name, nil
}

func agentViewOf(r store.GetExternalAgentViewRow) ExternalAgentView {
	return ExternalAgentView{ID: r.ID, Name: r.Name, Aliases: r.Aliases, ArchivedAt: r.ArchivedAt, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

func hostViewOf(r store.GetExternalHostViewRow) ExternalHostView {
	return ExternalHostView{ID: r.ID, Name: r.Name, Aliases: r.Aliases, AlwaysOn: r.AlwaysOn, LastSeenAt: r.LastSeenAt,
		ArchivedAt: r.ArchivedAt, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

// ListExternalAgents lista os agentes da empresa; os arquivados entram só com includeArchived.
func (s *Service) ListExternalAgents(ctx context.Context, companyID domain.ID, includeArchived bool, limit, offset int32) ([]ExternalAgentView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.read().ListExternalAgentViews(ctx, store.ListExternalAgentViewsParams{CompanyID: companyID, IncludeArchived: includeArchived, Lim: limit, Off: offset})
	if err != nil {
		return nil, mapErr(err, "list external agents")
	}
	out := make([]ExternalAgentView, 0, len(rows))
	for _, r := range rows {
		out = append(out, ExternalAgentView{ID: r.ID, Name: r.Name, Aliases: r.Aliases, ArchivedAt: r.ArchivedAt, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt})
	}
	return out, nil
}

// GetExternalAgent devolve um agente da empresa.
func (s *Service) GetExternalAgent(ctx context.Context, companyID, id domain.ID) (ExternalAgentView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return ExternalAgentView{}, err
	}
	r, err := s.read().GetExternalAgentView(ctx, store.GetExternalAgentViewParams{CompanyID: companyID, ID: id})
	if err != nil {
		return ExternalAgentView{}, mapErr(err, "external agent")
	}
	return agentViewOf(r), nil
}

// ListExternalHosts lista os hosts da empresa; os arquivados entram só com includeArchived.
func (s *Service) ListExternalHosts(ctx context.Context, companyID domain.ID, includeArchived bool, limit, offset int32) ([]ExternalHostView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.read().ListExternalHostViews(ctx, store.ListExternalHostViewsParams{CompanyID: companyID, IncludeArchived: includeArchived, Lim: limit, Off: offset})
	if err != nil {
		return nil, mapErr(err, "list external hosts")
	}
	out := make([]ExternalHostView, 0, len(rows))
	for _, r := range rows {
		out = append(out, ExternalHostView{ID: r.ID, Name: r.Name, Aliases: r.Aliases, AlwaysOn: r.AlwaysOn, LastSeenAt: r.LastSeenAt,
			ArchivedAt: r.ArchivedAt, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt})
	}
	return out, nil
}

// GetExternalHost devolve um host da empresa.
func (s *Service) GetExternalHost(ctx context.Context, companyID, id domain.ID) (ExternalHostView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return ExternalHostView{}, err
	}
	r, err := s.read().GetExternalHostView(ctx, store.GetExternalHostViewParams{CompanyID: companyID, ID: id})
	if err != nil {
		return ExternalHostView{}, mapErr(err, "external host")
	}
	return hostViewOf(r), nil
}

// ExternalAgentPatch é a alteração de um agente: os campos ausentes ficam como estão.
type ExternalAgentPatch struct {
	Name     *string
	Archived *bool
}

// UpdateExternalAgent renomeia e/ou arquiva (ou volta) um agente. O nome antigo vira apelido do mesmo agente.
func (s *Service) UpdateExternalAgent(ctx context.Context, actor domain.Actor, companyID, id domain.ID, p ExternalAgentPatch) (ExternalAgentView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return ExternalAgentView{}, err
	}
	var out ExternalAgentView
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetExternalAgentView(ctx, store.GetExternalAgentViewParams{CompanyID: companyID, ID: id})
		if err != nil {
			return mapErr(err, "external agent")
		}
		if err := applyAgentPatch(t, actor, companyID, id, cur, p); err != nil {
			return err
		}
		r, err := t.q.GetExternalAgentView(ctx, store.GetExternalAgentViewParams{CompanyID: companyID, ID: id})
		if err != nil {
			return mapErr(err, "external agent")
		}
		out = agentViewOf(r)
		return nil
	})
	return out, err
}

// applyAgentPatch aplica a alteração de nome e de arquivado de um agente, cada um com seu evento.
func applyAgentPatch(t *txc, actor domain.Actor, companyID, id domain.ID, cur store.GetExternalAgentViewRow, p ExternalAgentPatch) error {
	ctx := t.ctx
	if p.Name != nil {
		name, err := checkExternalName(*p.Name)
		if err != nil {
			return err
		}
		if name != cur.Name {
			if _, err := t.q.RenameExternalAgent(ctx, store.RenameExternalAgentParams{CompanyID: companyID, ID: id, Name: name, Now: t.now}); err != nil {
				return mapErr(err, "rename external agent")
			}
			if err := t.q.InsertExternalAgentAlias(ctx, store.InsertExternalAgentAliasParams{CompanyID: companyID, Alias: cur.Name, AgentID: id, Now: t.now}); err != nil {
				return fmt.Errorf("ingest: apelido do nome antigo: %w", err)
			}
			if err := t.activity(actor, companyID, "external_agent.renamed", "external_agent", id,
				map[string]any{"from": cur.Name, "to": name}, ""); err != nil {
				return err
			}
		}
	}
	if p.Archived != nil && (cur.ArchivedAt != nil) != *p.Archived {
		var at *time.Time
		verb := "external_agent.unarchived"
		if *p.Archived {
			now := t.now
			at = &now
			verb = "external_agent.archived"
		}
		if err := t.q.SetExternalAgentArchived(ctx, store.SetExternalAgentArchivedParams{CompanyID: companyID, ID: id, ArchivedAt: at, Now: t.now}); err != nil {
			return mapErr(err, "archive external agent")
		}
		if err := t.activity(actor, companyID, verb, "external_agent", id, map[string]any{"name": cur.Name}, ""); err != nil {
			return err
		}
	}
	return nil
}

// ExternalHostPatch é a alteração de um host: os campos ausentes ficam como estão.
type ExternalHostPatch struct {
	Name     *string
	AlwaysOn *bool
	Archived *bool
}

// UpdateExternalHost renomeia, marca o sempre ligado e/ou arquiva (ou volta) um host. O nome antigo vira apelido.
func (s *Service) UpdateExternalHost(ctx context.Context, actor domain.Actor, companyID, id domain.ID, p ExternalHostPatch) (ExternalHostView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return ExternalHostView{}, err
	}
	var out ExternalHostView
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetExternalHostView(ctx, store.GetExternalHostViewParams{CompanyID: companyID, ID: id})
		if err != nil {
			return mapErr(err, "external host")
		}
		if err := applyHostPatch(t, actor, companyID, id, cur, p); err != nil {
			return err
		}
		r, err := t.q.GetExternalHostView(ctx, store.GetExternalHostViewParams{CompanyID: companyID, ID: id})
		if err != nil {
			return mapErr(err, "external host")
		}
		out = hostViewOf(r)
		return nil
	})
	return out, err
}

// applyHostPatch aplica nome, sempre ligado e arquivado de um host, cada um com seu evento.
func applyHostPatch(t *txc, actor domain.Actor, companyID, id domain.ID, cur store.GetExternalHostViewRow, p ExternalHostPatch) error {
	ctx := t.ctx
	if p.Name != nil {
		name, err := checkExternalName(*p.Name)
		if err != nil {
			return err
		}
		if name != cur.Name {
			if _, err := t.q.RenameExternalHost(ctx, store.RenameExternalHostParams{CompanyID: companyID, ID: id, Name: name, Now: t.now}); err != nil {
				return mapErr(err, "rename external host")
			}
			if err := t.q.InsertExternalHostAlias(ctx, store.InsertExternalHostAliasParams{CompanyID: companyID, Alias: cur.Name, HostID: id, Now: t.now}); err != nil {
				return fmt.Errorf("ingest: apelido do nome antigo do host: %w", err)
			}
			if err := t.activity(actor, companyID, "external_host.renamed", "external_host", id,
				map[string]any{"from": cur.Name, "to": name}, ""); err != nil {
				return err
			}
		}
	}
	if p.AlwaysOn != nil && cur.AlwaysOn != *p.AlwaysOn {
		if err := t.q.SetExternalHostAlwaysOn(ctx, store.SetExternalHostAlwaysOnParams{CompanyID: companyID, ID: id, AlwaysOn: *p.AlwaysOn, Now: t.now}); err != nil {
			return mapErr(err, "always on external host")
		}
		if err := t.activity(actor, companyID, "external_host.always_on_changed", "external_host", id,
			map[string]any{"name": cur.Name, "always_on": *p.AlwaysOn}, ""); err != nil {
			return err
		}
	}
	if p.Archived != nil && (cur.ArchivedAt != nil) != *p.Archived {
		var at *time.Time
		verb := "external_host.unarchived"
		if *p.Archived {
			now := t.now
			at = &now
			verb = "external_host.archived"
		}
		if err := t.q.SetExternalHostArchived(ctx, store.SetExternalHostArchivedParams{CompanyID: companyID, ID: id, ArchivedAt: at, Now: t.now}); err != nil {
			return mapErr(err, "archive external host")
		}
		if err := t.activity(actor, companyID, verb, "external_host", id, map[string]any{"name": cur.Name}, ""); err != nil {
			return err
		}
	}
	return nil
}

// MergeExternalAgent junta `from` em `into`: os apelidos e as sessões e conversas de `from` passam para `into`, o nome
// de `from` vira apelido de `into`, e `from` fica arquivado. Os dois precisam ser da empresa e diferentes.
func (s *Service) MergeExternalAgent(ctx context.Context, actor domain.Actor, companyID, from, into domain.ID) (ExternalAgentView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return ExternalAgentView{}, err
	}
	if from == into {
		return ExternalAgentView{}, domain.Invalid("into", "o agente juntado e o que fica são o mesmo")
	}
	var out ExternalAgentView
	err := s.withTx(ctx, func(t *txc) error {
		src, err := t.q.GetExternalAgentView(ctx, store.GetExternalAgentViewParams{CompanyID: companyID, ID: from})
		if err != nil {
			return mapErr(err, "external agent")
		}
		if _, err := t.q.GetExternalAgentView(ctx, store.GetExternalAgentViewParams{CompanyID: companyID, ID: into}); err != nil {
			return mapErr(err, "external agent")
		}
		if err := t.q.MoveExternalAgentAliases(ctx, store.MoveExternalAgentAliasesParams{CompanyID: companyID, FromID: from, ToID: into}); err != nil {
			return fmt.Errorf("ingest: mover apelidos: %w", err)
		}
		if err := t.q.InsertExternalAgentAlias(ctx, store.InsertExternalAgentAliasParams{CompanyID: companyID, Alias: src.Name, AgentID: into, Now: t.now}); err != nil {
			return fmt.Errorf("ingest: apelido do agente juntado: %w", err)
		}
		if err := t.q.MoveExternalSessionsAgent(ctx, store.MoveExternalSessionsAgentParams{CompanyID: companyID, FromID: &from, ToID: &into, Now: t.now}); err != nil {
			return fmt.Errorf("ingest: mover sessões: %w", err)
		}
		if err := t.q.MoveExternalConversationsAgent(ctx, store.MoveExternalConversationsAgentParams{CompanyID: companyID, FromID: &from, ToID: &into, Now: t.now}); err != nil {
			return fmt.Errorf("ingest: mover conversas: %w", err)
		}
		now := t.now
		if err := t.q.SetExternalAgentArchived(ctx, store.SetExternalAgentArchivedParams{CompanyID: companyID, ID: from, ArchivedAt: &now, Now: t.now}); err != nil {
			return mapErr(err, "archive external agent")
		}
		if err := t.activity(actor, companyID, "external_agent.merged", "external_agent", into,
			map[string]any{"from": src.Name, "from_id": from.String()}, ""); err != nil {
			return err
		}
		r, err := t.q.GetExternalAgentView(ctx, store.GetExternalAgentViewParams{CompanyID: companyID, ID: into})
		if err != nil {
			return mapErr(err, "external agent")
		}
		out = agentViewOf(r)
		return nil
	})
	return out, err
}

// MergeExternalHost junta `from` em `into`, como o agente: apelidos e conversas passam, o sempre ligado de qualquer um
// dos dois vale para `into`, a última hora de fato é a mais nova, e `from` fica arquivado.
func (s *Service) MergeExternalHost(ctx context.Context, actor domain.Actor, companyID, from, into domain.ID) (ExternalHostView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return ExternalHostView{}, err
	}
	if from == into {
		return ExternalHostView{}, domain.Invalid("into", "o host juntado e o que fica são o mesmo")
	}
	var out ExternalHostView
	err := s.withTx(ctx, func(t *txc) error {
		src, err := t.q.GetExternalHostView(ctx, store.GetExternalHostViewParams{CompanyID: companyID, ID: from})
		if err != nil {
			return mapErr(err, "external host")
		}
		if _, err := t.q.GetExternalHostView(ctx, store.GetExternalHostViewParams{CompanyID: companyID, ID: into}); err != nil {
			return mapErr(err, "external host")
		}
		if err := t.q.MoveExternalHostAliases(ctx, store.MoveExternalHostAliasesParams{CompanyID: companyID, FromID: from, ToID: into}); err != nil {
			return fmt.Errorf("ingest: mover apelidos do host: %w", err)
		}
		if err := t.q.InsertExternalHostAlias(ctx, store.InsertExternalHostAliasParams{CompanyID: companyID, Alias: src.Name, HostID: into, Now: t.now}); err != nil {
			return fmt.Errorf("ingest: apelido do host juntado: %w", err)
		}
		if err := t.q.MoveExternalConversationsHost(ctx, store.MoveExternalConversationsHostParams{CompanyID: companyID, FromID: &from, ToID: &into, Now: t.now}); err != nil {
			return fmt.Errorf("ingest: mover conversas do host: %w", err)
		}
		if err := t.q.MergeExternalHostFacts(ctx, store.MergeExternalHostFactsParams{CompanyID: companyID, ID: into, FromAlwaysOn: src.AlwaysOn, FromSeen: src.LastSeenAt, Now: t.now}); err != nil {
			return fmt.Errorf("ingest: juntar fatos do host: %w", err)
		}
		now := t.now
		if err := t.q.SetExternalHostArchived(ctx, store.SetExternalHostArchivedParams{CompanyID: companyID, ID: from, ArchivedAt: &now, Now: t.now}); err != nil {
			return mapErr(err, "archive external host")
		}
		if err := t.activity(actor, companyID, "external_host.merged", "external_host", into,
			map[string]any{"from": src.Name, "from_id": from.String()}, ""); err != nil {
			return err
		}
		r, err := t.q.GetExternalHostView(ctx, store.GetExternalHostViewParams{CompanyID: companyID, ID: into})
		if err != nil {
			return mapErr(err, "external host")
		}
		out = hostViewOf(r)
		return nil
	})
	return out, err
}
