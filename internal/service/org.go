package service

import (
	"context"
	"fmt"
	"sort"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// OrgNode is a persona in the chart with the people that report to it.
type OrgNode struct {
	Persona      domain.Persona
	Depth        int // 0 = top of the chart
	OpenTasks    int
	PendingGates int // gates this persona is waiting on
	Children     []*OrgNode
}

// OrgChart is the reports_to forest of a company. Delegation follows it: a persona hands
// work to itself or to the personas below it.
type OrgChart struct {
	Roots []*OrgNode
	Total int
	Depth int // levels, counting the top (0 when empty)
}

// OrgChart builds the tree from reports_to. Personas whose superior no longer exists are roots.
// Siblings are ordered by hiring date.
func (s *Service) OrgChart(ctx context.Context, companyID domain.ID) (OrgChart, error) {
	q := s.read()
	if _, err := q.GetCompanyByID(ctx, companyID); err != nil {
		return OrgChart{}, mapErr(err, "get company")
	}
	rows, err := q.ListPersonas(ctx, companyID)
	if err != nil {
		return OrgChart{}, fmt.Errorf("list personas: %w", err)
	}
	open := map[domain.ID]int{}
	tasks, err := s.ListTasks(ctx, companyID, TaskFilter{Limit: 500})
	if err != nil {
		return OrgChart{}, err
	}
	for _, t := range tasks {
		if t.PersonaID != nil && t.Status.Open() {
			open[*t.PersonaID]++
		}
	}
	pending := map[domain.ID]int{}
	gates, err := s.ListGates(ctx, companyID, GateFilter{Statuses: []domain.GateStatus{domain.GatePending}, Limit: 500})
	if err != nil {
		return OrgChart{}, err
	}
	for _, g := range gates {
		if g.PersonaID != nil {
			pending[*g.PersonaID]++
		}
	}

	nodes := make(map[domain.ID]*OrgNode, len(rows))
	order := make([]*OrgNode, 0, len(rows))
	for _, r := range rows {
		p := personaFromRow(r)
		n := &OrgNode{Persona: p, OpenTasks: open[p.ID], PendingGates: pending[p.ID]}
		nodes[p.ID] = n
		order = append(order, n)
	}
	sort.SliceStable(order, func(i, j int) bool { return order[i].Persona.CreatedAt.Before(order[j].Persona.CreatedAt) })
	chart := OrgChart{Total: len(order)}
	for _, n := range order {
		if up := n.Persona.ReportsTo; up != nil {
			if parent, ok := nodes[*up]; ok && parent != n {
				parent.Children = append(parent.Children, n)
				continue
			}
		}
		chart.Roots = append(chart.Roots, n)
	}
	var walk func(n *OrgNode, d int)
	walk = func(n *OrgNode, d int) {
		n.Depth = d
		chart.Depth = max(chart.Depth, d+1)
		for _, c := range n.Children {
			walk(c, d+1)
		}
	}
	for _, r := range chart.Roots {
		walk(r, 0)
	}
	return chart, nil
}

// orgParents loads reports_to of every persona of the company.
func (t *txc) orgParents(companyID domain.ID) (domain.OrgParents, error) {
	rows, err := t.q.ListPersonas(t.ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list personas: %w", err)
	}
	m := make(domain.OrgParents, len(rows))
	for _, r := range rows {
		m[r.ID] = r.ReportsTo
	}
	return m, nil
}

// checkDelegation enforces the chart on work handed out by a persona: it may create or assign
// tasks to itself or to personas below it. Humans and the system assign to anyone.
func (t *txc) checkDelegation(actor domain.Actor, companyID domain.ID, target *domain.ID) error {
	if actor.Type != domain.ActorPersona || actor.ID == nil || target == nil {
		return nil
	}
	parents, err := t.orgParents(companyID)
	if err != nil {
		return err
	}
	if !parents.CanDelegateTo(*actor.ID, *target) {
		return fmt.Errorf("delegate: a persona only assigns work to itself or to personas below it in the org chart: %w", domain.ErrNotAllowed)
	}
	return nil
}

// reviewRequired reports whether the actor is a persona hired with require_review.
func (t *txc) reviewRequired(actor domain.Actor, companyID domain.ID) (bool, error) {
	if actor.Type != domain.ActorPersona || actor.ID == nil {
		return false, nil
	}
	p, err := t.q.GetPersonaByID(t.ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: *actor.ID})
	if isNoRows(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get actor persona: %w", err)
	}
	return p.RequireReview, nil
}

// checkTaskOwnership limits what a persona may change: tasks it owns, tasks owned by personas
// below it in the chart, and unowned tasks. Humans and the system change any task.
func (t *txc) checkTaskOwnership(actor domain.Actor, companyID domain.ID, owner *domain.ID) error {
	if actor.Type != domain.ActorPersona || actor.ID == nil || owner == nil || *owner == *actor.ID {
		return nil
	}
	parents, err := t.orgParents(companyID)
	if err != nil {
		return err
	}
	if !parents.IsDescendant(*actor.ID, *owner) {
		return fmt.Errorf("update task: a persona changes only tasks it owns, or that belong to personas below it: %w", domain.ErrNotAllowed)
	}
	return nil
}
