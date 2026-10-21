package domain

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Coordenação de tasks (ADR-0040): dependências bloqueantes, árvore pai/filho, pausa herdada e política de
// revisão por camada. Tudo aqui é regra pura, sem infraestrutura: o service e os testes de propriedade a usam.

const (
	// MaxTaskDependencies é o teto de dependências de uma task.
	MaxTaskDependencies = 50
	// MaxTaskDepth é a profundidade máxima da árvore (a raiz é o nível 1).
	MaxTaskDepth = 10
	// MaxHoldReason e MaxOverrideReason limitam os motivos digitados por pessoas.
	MaxHoldReason     = 500
	MaxOverrideReason = 500
)

var (
	// ErrTaskHeld: a task (ou um ancestral) está pausada; nenhuma run abre. Vale como ErrInvalid: o worker de
	// wake trata como "nada a fazer" e o retomar enfileira o wake de novo.
	ErrTaskHeld = fmt.Errorf("task is paused: %w", ErrInvalid)
	// ErrTaskBlocked: a task tem dependência aberta; nenhuma run abre até todas fecharem.
	ErrTaskBlocked = fmt.Errorf("task is blocked by open dependencies: %w", ErrInvalid)
	// ErrChildrenOpen: o pai não conclui com subtask aberta (override humano com motivo).
	ErrChildrenOpen = fmt.Errorf("task has open subtasks: %w", ErrInvalid)
	// ErrDependencyCycle: a dependência fecharia um ciclo.
	ErrDependencyCycle = fmt.Errorf("dependency cycle: %w", ErrConflict)
)

// ---------- dependências ----------

// DepEdges é o grafo de dependências: edges[a] lista as tasks de que a depende.
type DepEdges map[ID][]ID

// Add acrescenta a aresta a → b (sem checar nada).
func (e DepEdges) Add(a, b ID) { e[a] = append(e[a], b) }

// Reaches diz se, seguindo as dependências, from alcança to (from == to conta como alcançar).
func (e DepEdges) Reaches(from, to ID) bool {
	if from == to {
		return true
	}
	seen := map[ID]bool{from: true}
	stack := []ID{from}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, nx := range e[cur] {
			if nx == to {
				return true
			}
			if !seen[nx] {
				seen[nx] = true
				stack = append(stack, nx)
			}
		}
	}
	return false
}

// WouldCycle diz se fazer task depender de dependsOn fecharia um ciclo: dependsOn já alcança task.
func WouldCycle(edges DepEdges, task, dependsOn ID) bool {
	return task == dependsOn || edges.Reaches(dependsOn, task)
}

// HasCycle diz se o grafo tem ciclo (algoritmo de Kahn).
func HasCycle(edges DepEdges) bool {
	indeg := map[ID]int{}
	rev := map[ID][]ID{}
	for a, deps := range edges {
		if _, ok := indeg[a]; !ok {
			indeg[a] = 0
		}
		for _, b := range deps {
			if _, ok := indeg[b]; !ok {
				indeg[b] = 0
			}
			indeg[a]++
			rev[b] = append(rev[b], a)
		}
	}
	var queue []ID
	for id, d := range indeg {
		if d == 0 {
			queue = append(queue, id)
		}
	}
	done := 0
	for len(queue) > 0 {
		cur := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		done++
		for _, dep := range rev[cur] {
			indeg[dep]--
			if indeg[dep] == 0 {
				queue = append(queue, dep)
			}
		}
	}
	return done != len(indeg)
}

// OpenDependencies conta as dependências de task cuja task ainda está aberta. Uma task sem estado conhecido
// conta como aberta (o lado seguro).
func OpenDependencies(edges DepEdges, status map[ID]TaskStatus, task ID) int {
	n := 0
	for _, d := range edges[task] {
		if st, ok := status[d]; !ok || st.Open() {
			n++
		}
	}
	return n
}

// Blocked é o estado derivado "bloqueada": ao menos uma dependência aberta.
func Blocked(openDeps int) bool { return openDeps > 0 }

// ReleasedBy lista as tasks que ficam livres quando closed fecha: dependem dela e não têm mais nenhuma
// dependência aberta (closed já deve constar como fechada em status). Só tasks abertas entram. A ordem é
// estável (por id) para os testes.
func ReleasedBy(edges DepEdges, status map[ID]TaskStatus, closed ID) []ID {
	var out []ID
	for a, deps := range edges {
		if st, ok := status[a]; !ok || !st.Open() {
			continue
		}
		dependsOnClosed := false
		for _, d := range deps {
			if d == closed {
				dependsOnClosed = true
				break
			}
		}
		if dependsOnClosed && OpenDependencies(edges, status, a) == 0 {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// TaskRef identifica uma task em listas curtas (dependências, filhos, ancestrais).
type TaskRef struct {
	ID        ID         `json:"id"`
	Key       string     `json:"key"`
	Title     string     `json:"title"`
	Status    TaskStatus `json:"status"`
	PersonaID *ID        `json:"persona_id,omitempty"`
}

// CycleMessage descreve a cadeia que fecharia o ciclo ("HNK-3 → HNK-2 → HNK-1 → HNK-3").
func CycleMessage(keys []string) string { return strings.Join(keys, " → ") }

// ---------- pausa ----------

// TaskHold é a pausa efetiva de uma task: a gravada nela ou herdada do ancestral mais próximo.
type TaskHold struct {
	Reason string    `json:"reason"`
	By     Actor     `json:"by"`
	At     time.Time `json:"at"`
	// OriginID/OriginKey são a task onde a pausa foi gravada (a raiz da pausa).
	OriginID  ID     `json:"origin_id"`
	OriginKey string `json:"origin_key"`
	// Inherited é verdadeiro quando a pausa vem de um ancestral, não da própria task.
	Inherited bool `json:"inherited"`
}

// ---------- política de revisão ----------

// ReviewLayer é a camada de onde uma decisão de revisão veio.
type ReviewLayer string

const (
	ReviewLayerTask    ReviewLayer = "task"
	ReviewLayerProject ReviewLayer = "project"
	ReviewLayerPersona ReviewLayer = "persona"
)

// Valid diz se é uma camada conhecida.
func (l ReviewLayer) Valid() bool {
	return l == ReviewLayerTask || l == ReviewLayerProject || l == ReviewLayerPersona
}

// ReviewerMode diz o que uma camada decide sobre o revisor automático.
type ReviewerMode string

const (
	ReviewerInherit ReviewerMode = "inherit" // não opina: vale a próxima camada
	ReviewerNone    ReviewerMode = "none"    // sem revisor automático: o gate humano abre direto
	ReviewerCustom  ReviewerMode = "custom"  // persona revisora ou harness+modelo desta camada
)

// Valid diz se é um modo conhecido.
func (m ReviewerMode) Valid() bool {
	return m == ReviewerInherit || m == ReviewerNone || m == ReviewerCustom
}

// ReviewOverride é a sobreposição de uma camada (task ou projeto). O zero value não opina em nada.
type ReviewOverride struct {
	// RequireReview nil = herda; true/false força.
	RequireReview *bool        `json:"require_review,omitempty"`
	ReviewerMode  ReviewerMode `json:"reviewer_mode"`
	// Para ReviewerCustom: uma persona revisora OU um harness (+ modelo). Os dois são exclusivos.
	ReviewerPersonaID *ID    `json:"reviewer_persona_id,omitempty"`
	ReviewerAgent     string `json:"reviewer_agent,omitempty"`
	ReviewerModel     string `json:"reviewer_model,omitempty"`
}

// Empty diz se a camada não opina em nada.
func (o ReviewOverride) Empty() bool {
	return o.RequireReview == nil && (o.ReviewerMode == "" || o.ReviewerMode == ReviewerInherit)
}

// customReviewer diz se o modo custom aponta para algum revisor.
func (o ReviewOverride) customReviewer() bool {
	return o.ReviewerMode == ReviewerCustom && HasReviewer(o.ReviewerPersonaID, o.ReviewerAgent)
}

// Validate confere a coerência da sobreposição.
func (o ReviewOverride) Validate() error {
	if o.ReviewerMode != "" && !o.ReviewerMode.Valid() {
		return Invalid("reviewer_mode", "deve ser inherit, none ou custom")
	}
	if o.ReviewerPersonaID != nil && o.ReviewerAgent != "" {
		return Invalid("reviewer_agent", "revisor persona e revisor harness são exclusivos")
	}
	switch o.ReviewerAgent {
	case "", "claude", "codex", "fake":
	default:
		return Invalid("reviewer_agent", "deve ser claude, codex ou fake")
	}
	if o.ReviewerMode == ReviewerCustom && !HasReviewer(o.ReviewerPersonaID, o.ReviewerAgent) {
		return Invalid("reviewer_mode", "custom exige uma persona revisora ou um harness")
	}
	if o.ReviewerMode != ReviewerCustom && (o.ReviewerPersonaID != nil || o.ReviewerAgent != "" || o.ReviewerModel != "") {
		return Invalid("reviewer_mode", "persona/harness revisores só valem no modo custom")
	}
	return nil
}

// PersonaReview é a política da persona (a base da precedência).
type PersonaReview struct {
	RequireReview     bool
	ReviewerPersonaID *ID
	ReviewerAgent     string
	ReviewerModel     string
}

// ReviewPolicy é a política efetiva e a origem de cada decisão.
type ReviewPolicy struct {
	Required       bool        `json:"required"`
	RequiredSource ReviewLayer `json:"required_source"`
	// HasReviewer: há revisão automática antes do gate; os campos abaixo dizem quem revisa.
	HasReviewer       bool        `json:"has_reviewer"`
	ReviewerSource    ReviewLayer `json:"reviewer_source"`
	ReviewerPersonaID *ID         `json:"reviewer_persona_id,omitempty"`
	ReviewerAgent     string      `json:"reviewer_agent,omitempty"`
	ReviewerModel     string      `json:"reviewer_model,omitempty"`
}

// ResolveReviewPolicy aplica a precedência task > projeto > persona, campo a campo: a obrigatoriedade da
// revisão e o revisor são resolvidos separadamente, cada um pela primeira camada que opina.
func ResolveReviewPolicy(task, project ReviewOverride, persona PersonaReview) ReviewPolicy {
	var out ReviewPolicy
	switch {
	case task.RequireReview != nil:
		out.Required, out.RequiredSource = *task.RequireReview, ReviewLayerTask
	case project.RequireReview != nil:
		out.Required, out.RequiredSource = *project.RequireReview, ReviewLayerProject
	default:
		out.Required, out.RequiredSource = persona.RequireReview, ReviewLayerPersona
	}
	pickCustom := func(o ReviewOverride, layer ReviewLayer) {
		out.ReviewerSource = layer
		out.HasReviewer = true
		out.ReviewerPersonaID, out.ReviewerAgent, out.ReviewerModel = o.ReviewerPersonaID, o.ReviewerAgent, o.ReviewerModel
	}
	switch {
	case task.ReviewerMode == ReviewerNone:
		out.ReviewerSource = ReviewLayerTask
	case task.customReviewer():
		pickCustom(task, ReviewLayerTask)
	case project.ReviewerMode == ReviewerNone:
		out.ReviewerSource = ReviewLayerProject
	case project.customReviewer():
		pickCustom(project, ReviewLayerProject)
	default:
		out.ReviewerSource = ReviewLayerPersona
		if HasReviewer(persona.ReviewerPersonaID, persona.ReviewerAgent) {
			out.HasReviewer = true
			out.ReviewerPersonaID, out.ReviewerAgent, out.ReviewerModel = persona.ReviewerPersonaID, persona.ReviewerAgent, persona.ReviewerModel
		}
	}
	return out
}

// RunPolicyOrigin é o que a run guarda da política que valeu nela.
type RunPolicyOrigin struct {
	ReviewRequired       bool        `json:"review_required"`
	ReviewRequiredSource ReviewLayer `json:"review_required_source"`
	ReviewerSource       ReviewLayer `json:"reviewer_source"`
}
