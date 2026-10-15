package uitest

import (
	"context"
	"sort"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// descendant reports whether id sits below ancestor in the chart. Caller holds f.mu.
func (f *Fake) descendant(ancestor, id domain.ID) bool {
	parents := map[domain.ID]*domain.ID{}
	for _, p := range f.Personas {
		parents[p.ID] = p.ReportsTo
	}
	cur := parents[id]
	for i := 0; cur != nil && i < 64; i++ {
		if *cur == ancestor {
			return true
		}
		cur = parents[*cur]
	}
	return false
}

func has[T comparable](list []T, v T) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (f *Fake) ListGates(_ context.Context, _ domain.ID, fl service.GateFilter) ([]service.GateView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ListGates"); err != nil {
		return nil, err
	}
	var out []service.GateView
	for _, g := range f.Gates {
		if len(fl.Statuses) > 0 && !has(fl.Statuses, g.Status) {
			continue
		}
		if len(fl.Kinds) > 0 && !has(fl.Kinds, g.Kind) {
			continue
		}
		if fl.TaskID != nil && (g.TaskID == nil || *g.TaskID != *fl.TaskID) {
			continue
		}
		if fl.PersonaID != nil && (g.PersonaID == nil || *g.PersonaID != *fl.PersonaID) {
			continue
		}
		out = append(out, g)
	}
	// pending first (oldest first), then the decided, newest first
	sort.SliceStable(out, func(i, j int) bool {
		pi, pj := out[i].Status == domain.GatePending, out[j].Status == domain.GatePending
		switch {
		case pi && pj:
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		case pi != pj:
			return pi
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if fl.Limit > 0 && len(out) > fl.Limit {
		out = out[:fl.Limit]
	}
	return out, nil
}

func (f *Fake) GetGate(_ context.Context, _, id domain.ID) (service.GateView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("GetGate"); err != nil {
		return service.GateView{}, err
	}
	for _, g := range f.Gates {
		if g.ID == id {
			return g, nil
		}
	}
	return service.GateView{}, domain.ErrNotFound
}

// DecideGate mirrors the service rules that the pages depend on.
func (f *Fake) DecideGate(_ context.Context, actor domain.Actor, _, id domain.ID, in service.DecideGateInput) (service.GateView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("DecideGate")
	if err := f.fail("DecideGate"); err != nil {
		return service.GateView{}, err
	}
	if actor.Type != domain.ActorHuman {
		return service.GateView{}, domain.ErrNotAllowed
	}
	if !in.Decision.Decision() {
		return service.GateView{}, domain.Invalid("decision", "unknown decision")
	}
	for i, g := range f.Gates {
		if g.ID != id {
			continue
		}
		if g.Status != domain.GatePending {
			return service.GateView{}, domain.ErrConflict
		}
		now := time.Now().UTC()
		g.Status, g.DecisionNote, g.DecidedAt = in.Decision, in.Note, &now
		a := actor
		g.DecidedBy = &a
		f.Gates[i] = g
		if g.TaskID != nil {
			for ti, t := range f.Tasks {
				if t.ID != *g.TaskID {
					continue
				}
				switch {
				case in.Decision == domain.GateRejected:
					t.Status = domain.TaskTodo
				case in.Decision == domain.GateApproved && g.Kind == domain.GateTaskReview:
					t.Status = domain.TaskDone
				default:
					t.Status = domain.TaskInProgress
				}
				t.UpdatedAt = now
				f.Tasks[ti] = t
			}
		}
		if g.PersonaID != nil {
			for pi, p := range f.Personas {
				if p.ID == *g.PersonaID && p.Status == domain.PersonaAwaitingGate {
					p.Status = domain.PersonaIdle
					f.Personas[pi] = p
				}
			}
		}
		return g, nil
	}
	return service.GateView{}, domain.ErrNotFound
}

func (f *Fake) Inbox(ctx context.Context, companyID domain.ID) (service.Inbox, error) {
	if err := f.fail("Inbox"); err != nil {
		return service.Inbox{}, err
	}
	var in service.Inbox
	gates, _ := f.ListGates(ctx, companyID, service.GateFilter{Statuses: []domain.GateStatus{domain.GatePending}})
	in.Gates = gates
	f.mu.Lock()
	defer f.mu.Unlock()
	withGate := map[domain.ID]bool{}
	for _, g := range gates {
		if g.TaskID != nil {
			withGate[*g.TaskID] = true
		}
	}
	for _, t := range f.Tasks {
		if t.Status == domain.TaskBlocked && !withGate[t.ID] {
			in.BlockedTasks = append(in.BlockedTasks, t)
		}
	}
	for _, r := range f.Runs {
		if r.Status == domain.RunFailed || r.Status == domain.RunExpired {
			in.FailedRuns = append(in.FailedRuns, r)
		}
	}
	for _, p := range f.Personas {
		if p.Status == domain.PersonaError {
			in.ErrorPersonas = append(in.ErrorPersonas, service.PersonaAlert{Persona: p, PersonaHealth: f.Health[p.ID]})
		}
	}
	in.Alerts = append(in.Alerts, f.Alerts...)
	for _, s := range f.Subs {
		if s.Blocked {
			in.GuardedSubscriptions = append(in.GuardedSubscriptions, s)
		}
	}
	return in, nil
}

func (f *Fake) OrgChart(_ context.Context, _ domain.ID) (service.OrgChart, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("OrgChart"); err != nil {
		return service.OrgChart{}, err
	}
	open, pending := map[domain.ID]int{}, map[domain.ID]int{}
	for _, t := range f.Tasks {
		if t.PersonaID != nil && t.Status.Open() {
			open[*t.PersonaID]++
		}
	}
	for _, g := range f.Gates {
		if g.PersonaID != nil && g.Status == domain.GatePending {
			pending[*g.PersonaID]++
		}
	}
	nodes := map[domain.ID]*service.OrgNode{}
	var order []*service.OrgNode
	for _, p := range f.Personas {
		n := &service.OrgNode{Persona: p, OpenTasks: open[p.ID], PendingGates: pending[p.ID]}
		nodes[p.ID] = n
		order = append(order, n)
	}
	ch := service.OrgChart{Total: len(order)}
	for _, n := range order {
		if up := n.Persona.ReportsTo; up != nil {
			if parent, ok := nodes[*up]; ok && parent != n {
				parent.Children = append(parent.Children, n)
				continue
			}
		}
		ch.Roots = append(ch.Roots, n)
	}
	var walk func(n *service.OrgNode, d int)
	walk = func(n *service.OrgNode, d int) {
		n.Depth = d
		ch.Depth = max(ch.Depth, d+1)
		for _, c := range n.Children {
			walk(c, d+1)
		}
	}
	for _, r := range ch.Roots {
		walk(r, 0)
	}
	return ch, nil
}
