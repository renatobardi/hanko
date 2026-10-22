package service

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Pedido de conexão por uma persona (ADR-0043): a persona cita uma conexão existente (slug) ou propõe nome e URL (nasce
// rascunho); se a conexão não está conectada, abre-se um gate kind=connection e a task fica parada até um humano
// autorizar. Nunca é a persona que autoriza.

// RequestConnectionInput é o pedido da persona.
type RequestConnectionInput struct {
	Slug   string // conexão existente da empresa
	Name   string // proposta de conexão nova (com URL)
	URL    string
	Reason string // por que a persona precisa dela (aparece no gate)
	TaskID *domain.ID
	RunID  *domain.ID
}

// RequestConnectionResult diz se já estava conectada ou o gate que foi aberto.
type RequestConnectionResult struct {
	Connection ConnectionView
	// Connected: a conexão já está autorizada; nenhum gate foi aberto.
	Connected bool
	Gate      *GateView
}

const maxConnectionReasonRunes = 1000

// RequestConnection é chamado por uma persona (run token, ou MCP). Idempotente: um gate pendente por conexão.
func (s *Service) RequestConnection(ctx context.Context, actor domain.Actor, companyID domain.ID, in RequestConnectionInput) (out RequestConnectionResult, err error) {
	ctx, done := connSpan(ctx, "request")
	defer func() { done(err) }()
	if actor.Type != domain.ActorPersona || actor.ID == nil {
		return out, fmt.Errorf("request connection: só uma persona pede conexão: %w", domain.ErrNotAllowed)
	}
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return out, err
	}
	if utf8.RuneCountInString(in.Reason) > maxConnectionReasonRunes {
		return out, domain.Invalid("reason", "too long")
	}
	var row store.Connection
	switch {
	case strings.TrimSpace(in.Slug) != "":
		r, e := s.read().GetConnectionBySlug(ctx, store.GetConnectionBySlugParams{CompanyID: companyID, Slug: strings.TrimSpace(in.Slug)})
		if e != nil || r.Identity != string(domain.ConnIdentityCompany) {
			return out, notFound("connection")
		}
		row = r
	case strings.TrimSpace(in.URL) != "":
		u, e := s.connectionURL(in.URL)
		if e != nil {
			return out, e
		}
		if existing, ok := s.connectionByURL(ctx, companyID, u); ok {
			row = existing
			break
		}
		name := strings.TrimSpace(in.Name)
		if name == "" {
			return out, domain.Invalid("name", "required (nome da conexão proposta)")
		}
		e = s.withTx(ctx, func(t *txc) error {
			var e error
			row, e = s.createConnectionTx(t, actor, companyID, nil, CreateConnectionInput{Name: name, URL: u, Identity: domain.ConnIdentityCompany})
			return e
		})
		if e != nil {
			return out, e
		}
	default:
		return out, domain.Invalid("slug", "informe a conexão (slug) ou nome e url para propor uma")
	}

	view := s.connectionView(row)
	if domain.ConnectionStatus(row.Status) == domain.ConnConnected {
		return RequestConnectionResult{Connection: view, Connected: true}, nil
	}
	// já há um gate pendente para esta conexão: devolve-o
	if row.GateID != nil {
		if g, e := loadGateView(ctx, s.read(), companyID, *row.GateID); e == nil && g.Status == domain.GatePending {
			return RequestConnectionResult{Connection: view, Gate: &g}, nil
		}
	}
	body := "A persona precisa de acesso à conexão **" + row.Name + "** (`" + row.Url + "`)."
	if r := strings.TrimSpace(in.Reason); r != "" {
		body += "\n\nMotivo: " + r
	}
	body += "\n\nPara aprovar, autorize a conexão em Integrações › Conexões; o gate é aprovado sozinho quando a autorização termina."
	gv, err := s.RequestGate(ctx, actor, companyID, RequestGateInput{Kind: domain.GateConnection, Title: "Conectar " + row.Name, Body: body,
		TaskID: in.TaskID, RunID: in.RunID, connectionID: &row.ID})
	if err != nil {
		return out, err
	}
	fresh, e := s.read().GetConnectionByID(ctx, store.GetConnectionByIDParams{CompanyID: companyID, ID: row.ID})
	if e == nil {
		view = s.connectionView(fresh)
	}
	return RequestConnectionResult{Connection: view, Gate: &gv}, nil
}

// connectionByURL acha uma conexão da empresa (identidade empresa) para o mesmo endereço.
func (s *Service) connectionByURL(ctx context.Context, companyID domain.ID, u string) (store.Connection, bool) {
	rows, err := s.read().ListConnections(ctx, store.ListConnectionsParams{CompanyID: companyID})
	if err != nil {
		return store.Connection{}, false
	}
	for _, r := range rows {
		if r.Identity == string(domain.ConnIdentityCompany) && strings.TrimRight(r.Url, "/") == strings.TrimRight(u, "/") {
			return r, true
		}
	}
	return store.Connection{}, false
}

// connectionGateDecided roda dentro do DecideGate de um gate connection: aprovar só vale com a conexão autorizada.
func (t *txc) connectionGateCheck(g store.Gate, decision domain.GateStatus) error {
	if decision != domain.GateApproved {
		return nil
	}
	c, err := t.q.GetConnectionByGate(t.ctx, store.GetConnectionByGateParams{CompanyID: g.CompanyID, GateID: &g.ID})
	if err != nil {
		if isNoRows(err) {
			return nil
		}
		return fmt.Errorf("get connection by gate: %w", err)
	}
	if domain.ConnectionStatus(c.Status) != domain.ConnConnected {
		return domain.Invalid("decision", "autorize a conexão "+c.Name+" antes de aprovar este gate")
	}
	return nil
}
