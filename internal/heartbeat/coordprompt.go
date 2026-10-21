package heartbeat

import (
	"fmt"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// coordinationSection é o bloco "Coordenação" do briefing (ADR-0040): onde a task está na árvore, o que a bloqueia,
// as subtasks e a política de revisão que vale. Vazio quando não há nada a dizer.
func coordinationSection(rc service.RunContext) string {
	rel := rc.Relations
	var b strings.Builder
	if rel != nil {
		if n := len(rel.Ancestors); n > 0 {
			parts := make([]string, n)
			for i, a := range rel.Ancestors {
				parts[i] = fmt.Sprintf("%s (%s)", a.Key, clip(a.Title, 80))
			}
			fmt.Fprintf(&b, "- Faz parte de: %s\n", strings.Join(parts, " › "))
		}
		if len(rel.DependsOn) > 0 {
			b.WriteString("- Depende de:\n")
			for _, d := range rel.DependsOn {
				state := "aberta"
				if d.Closed {
					state = "fechada"
					if d.Status == domain.TaskCancelled {
						state = "cancelada, sem entrega"
					}
				}
				fmt.Fprintf(&b, "  - %s %s [%s]\n", d.Key, clip(d.Title, 80), state)
			}
		}
		if len(rel.Dependents) > 0 {
			b.WriteString("- Outras tasks esperam por esta:\n")
			for _, d := range rel.Dependents {
				fmt.Fprintf(&b, "  - %s %s [%s]\n", d.Key, clip(d.Title, 80), d.Status)
			}
		}
		if len(rel.Children) > 0 {
			open := 0
			for _, c := range rel.Children {
				if !c.Closed {
					open++
				}
			}
			fmt.Fprintf(&b, "- Subtasks (%d abertas de %d):\n", open, len(rel.Children))
			for _, c := range rel.Children {
				who := c.PersonaName
				if who == "" {
					who = "sem responsável"
				}
				fmt.Fprintf(&b, "  - %s %s [%s] · %s\n", c.Key, clip(c.Title, 80), c.Status, who)
			}
			if open > 0 {
				b.WriteString("  Esta task só pode ser concluída quando as subtasks fecharem; você é acordada de novo quando a última fechar.\n")
			}
		}
	}
	if p := rc.ReviewPolicy; p != nil && p.Required && p.RequiredSource != domain.ReviewLayerPersona {
		fmt.Fprintf(&b, "- Revisão: o fechamento desta task passa por revisão (definido na %s).\n", layerLabel(p.RequiredSource))
	}
	if b.Len() == 0 {
		return ""
	}
	return "## Coordenação\n\n" + b.String() + "\n"
}

func layerLabel(l domain.ReviewLayer) string {
	switch l {
	case domain.ReviewLayerTask:
		return "própria task"
	case domain.ReviewLayerProject:
		return "política do projeto"
	}
	return "sua configuração"
}
