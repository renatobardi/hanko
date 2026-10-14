package ui

import (
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Geometry of the chart (SVG user units, 1 unit = 1 px).
const (
	orgCardW = 232
	orgCardH = 84
	orgGapX  = 28
	orgGapY  = 52
	orgPad   = 24
)

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

func orgStatusClass(s domain.PersonaStatus) string {
	switch s {
	case domain.PersonaRunning:
		return "run"
	case domain.PersonaError:
		return "error"
	case domain.PersonaPaused, domain.PersonaOff:
		return "paused"
	case domain.PersonaAwaitingGate:
		return "gate"
	}
	return "idle"
}

// layoutOrg places the chart: leaves take consecutive columns, a parent is centred above its
// children, each depth is a row. Everything is computed here so the template only draws.
func layoutOrg(chart service.OrgChart, base string) views.OrgVM {
	vm := views.OrgVM{Total: chart.Total, HireHref: base + "/personas/nova"}
	if chart.Total == 0 {
		return vm
	}
	col := 0
	var place func(n *service.OrgNode) int // returns the index of the node in vm.Nodes
	place = func(n *service.OrgNode) int {
		kids := make([]int, 0, len(n.Children))
		for _, c := range n.Children {
			kids = append(kids, place(c))
		}
		var cx int
		if len(kids) == 0 {
			cx = orgPad + col*(orgCardW+orgGapX) + orgCardW/2
			col++
		} else {
			cx = (vm.Nodes[kids[0]].X + vm.Nodes[kids[len(kids)-1]].X + orgCardW) / 2
		}
		p := n.Persona
		status := personaBadge(p.Status).Label
		meta := ""
		switch n.OpenTasks {
		case 0:
		case 1:
			meta = " · 1 task"
		default:
			meta = fmt.Sprintf(" · %d tasks", n.OpenTasks)
		}
		node := views.OrgNodeVM{
			X: cx - orgCardW/2, Y: orgPad + n.Depth*(orgCardH+orgGapY), W: orgCardW, H: orgCardH,
			Name: clip(p.Name, 22), Title: clip(p.Title, 28), Meta: meta, Status: status, StatusClass: orgStatusClass(p.Status),
			Href: base + "/personas/" + p.Slug, Initials: initialsOf(p.Name),
			Gate: p.Status == domain.PersonaAwaitingGate || n.PendingGates > 0, Review: p.RequireReview,
			Label: fmt.Sprintf("%s, %s, %s", p.Name, p.Title, status),
		}
		vm.Nodes = append(vm.Nodes, node)
		idx := len(vm.Nodes) - 1
		for _, k := range kids {
			c := vm.Nodes[k]
			px, py := node.X+orgCardW/2, node.Y+orgCardH
			cx2, cy := c.X+orgCardW/2, c.Y
			mid := py + orgGapY/2
			vm.Edges = append(vm.Edges, views.OrgEdgeVM{D: fmt.Sprintf("M%d %d V%d H%d V%d", px, py, mid, cx2, cy)})
		}
		return idx
	}
	for _, r := range chart.Roots {
		place(r)
	}
	vm.Width = orgPad*2 + col*orgCardW + (col-1)*orgGapX
	vm.Height = orgPad*2 + chart.Depth*orgCardH + (chart.Depth-1)*orgGapY
	return vm
}

func (s *server) handleOrg(w http.ResponseWriter, r *http.Request, co domain.Company) {
	chart, err := s.be.OrgChart(r.Context(), co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	base := "/c/" + co.Slug
	vm := layoutOrg(chart, base)
	switch chart.Total {
	case 0:
		vm.Subtitle = "Quem reporta a quem. A delegação de tasks segue a árvore."
	case 1:
		vm.Subtitle = "1 persona · quem reporta a quem. A delegação de tasks segue a árvore."
	default:
		vm.Subtitle = fmt.Sprintf("%d personas em %d níveis · quem reporta a quem. A delegação de tasks segue a árvore.", chart.Total, chart.Depth)
	}
	sh := s.shell(co, "organograma", "Organograma", views.Crumb{Label: "Organograma"})
	s.render(w, r, http.StatusOK, views.OrgPage(sh, vm))
}

// reportsOptions lists who a persona can report to: the owner, or any persona that is not the
// persona itself nor below it (that would close a loop; the service rejects it as well).
func reportsOptions(personas []domain.Persona, self *domain.Persona, sel *domain.ID) []views.Option {
	parents := map[domain.ID]*domain.ID{}
	for _, p := range personas {
		parents[p.ID] = p.ReportsTo
	}
	below := func(ancestor, id domain.ID) bool {
		cur := parents[id]
		for i := 0; cur != nil && i < 256; i++ {
			if *cur == ancestor {
				return true
			}
			cur = parents[*cur]
		}
		return false
	}
	out := []views.Option{{Value: "", Label: "Você (dono)", Selected: sel == nil}}
	for _, p := range personas {
		if self != nil && (p.ID == self.ID || below(self.ID, p.ID)) {
			continue
		}
		out = append(out, views.Option{Value: p.ID.String(), Label: p.Name, Selected: sel != nil && *sel == p.ID})
	}
	return out
}
