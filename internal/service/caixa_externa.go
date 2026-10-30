package service

import (
	"context"
	"fmt"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Caixa: agentes e hosts externos novos (ADR-0075 item 2; ADR-0077). Informativos: ninguém precisa agir neles, então
// ficam fora de Count e do selo. Lido e arquivado seguem o de todo item da Caixa. "Novo" é o criado nos últimos
// externalNewWindow; a janela é uma escolha do autor, a registrar.

const (
	externalNewWindow = 30 * 24 * time.Hour
	maxExternalNew    = 50
)

// ExternalNew é um agente ou host externo novo que aparece na Caixa.
type ExternalNew struct {
	ID   domain.ID
	Name string
	At   time.Time
}

// addExternalNew preenche os agentes e hosts novos da empresa no Inbox.
func (s *Service) addExternalNew(ctx context.Context, companyID domain.ID, in *Inbox) error {
	since := s.Now().UTC().Add(-externalNewWindow)
	q := s.read()
	agents, err := q.ListNewExternalAgents(ctx, store.ListNewExternalAgentsParams{CompanyID: companyID, Since: since, Lim: maxExternalNew})
	if err != nil {
		return fmt.Errorf("list new external agents: %w", err)
	}
	for _, a := range agents {
		in.NewAgents = append(in.NewAgents, ExternalNew{ID: a.ID, Name: a.Name, At: a.CreatedAt})
	}
	hosts, err := q.ListNewExternalHosts(ctx, store.ListNewExternalHostsParams{CompanyID: companyID, Since: since, Lim: maxExternalNew})
	if err != nil {
		return fmt.Errorf("list new external hosts: %w", err)
	}
	for _, h := range hosts {
		in.NewHosts = append(in.NewHosts, ExternalNew{ID: h.ID, Name: h.Name, At: h.CreatedAt})
	}
	return nil
}
