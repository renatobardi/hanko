package domain

import "fmt"

// Metas em árvore (ADR-0042 §C): pai opcional, sem ciclo, no máximo MaxGoalDepth níveis. O progresso de uma meta
// com submetas é o agregado delas.

// MaxGoalDepth é o número máximo de níveis da árvore de metas (a raiz é o nível 1).
const MaxGoalDepth = 4

// goalWalkCap protege os laços contra dados corrompidos (ciclo que o banco deveria ter impedido).
const goalWalkCap = 64

// GoalNode é o que a árvore precisa saber de uma meta.
type GoalNode struct {
	ID       ID
	ParentID *ID
	Status   GoalStatus
	Start    float64
	Target   float64
	Current  float64
}

// GoalTree é a floresta de metas de uma empresa.
type GoalTree struct {
	nodes    map[ID]GoalNode
	children map[ID][]ID
	order    []ID
}

// NewGoalTree monta a árvore; a ordem dos filhos é a ordem de nodes.
func NewGoalTree(nodes []GoalNode) GoalTree {
	t := GoalTree{nodes: make(map[ID]GoalNode, len(nodes)), children: map[ID][]ID{}}
	for _, n := range nodes {
		t.nodes[n.ID] = n
		t.order = append(t.order, n.ID)
	}
	for _, n := range nodes {
		if n.ParentID != nil {
			if _, ok := t.nodes[*n.ParentID]; ok {
				t.children[*n.ParentID] = append(t.children[*n.ParentID], n.ID)
			}
		}
	}
	return t
}

// Has diz se a meta existe na árvore.
func (t GoalTree) Has(id ID) bool { _, ok := t.nodes[id]; return ok }

// Children devolve os filhos diretos.
func (t GoalTree) Children(id ID) []ID { return t.children[id] }

// Parent devolve o pai de id.
func (t GoalTree) Parent(id ID) (ID, bool) {
	n, ok := t.nodes[id]
	if !ok || n.ParentID == nil {
		return ID{}, false
	}
	if _, ok := t.nodes[*n.ParentID]; !ok {
		return ID{}, false
	}
	return *n.ParentID, true
}

// Path é a cadeia da raiz até id (inclusive), na ordem raiz → meta.
func (t GoalTree) Path(id ID) []ID {
	var rev []ID
	cur, ok := id, t.Has(id)
	for i := 0; ok && i < goalWalkCap; i++ {
		rev = append(rev, cur)
		cur, ok = t.Parent(cur)
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

// Depth é o nível de id: 1 para uma raiz.
func (t GoalTree) Depth(id ID) int { return len(t.Path(id)) }

// Height é quantos níveis a subárvore de id ocupa, contando o próprio id: 1 para uma folha.
func (t GoalTree) Height(id ID) int { return t.height(id, 0) }

func (t GoalTree) height(id ID, depth int) int {
	if depth >= goalWalkCap {
		return 1
	}
	h := 0
	for _, c := range t.children[id] {
		if ch := t.height(c, depth+1); ch > h {
			h = ch
		}
	}
	return h + 1
}

// InSubtree diz se candidate é id ou descende de id.
func (t GoalTree) InSubtree(id, candidate ID) bool {
	for _, p := range t.Path(candidate) {
		if p == id {
			return true
		}
	}
	return false
}

// CheckParent confere se id pode ficar sob parent (nil = virar raiz). id pode ainda não existir na árvore
// (meta nova): então só vale a profundidade do pai.
func (t GoalTree) CheckParent(id ID, parent *ID) error {
	if parent == nil {
		return nil
	}
	if !t.Has(*parent) {
		return Invalid("parent_id", "goal not found in company")
	}
	if *parent == id || t.InSubtree(id, *parent) {
		return Invalid("parent_id", "a meta não pode ser ancestral de si mesma")
	}
	below := 0 // níveis que a subárvore de id traz consigo
	if t.Has(id) {
		below = t.Height(id) - 1
	}
	if t.Depth(*parent)+1+below > MaxGoalDepth {
		return Invalid("parent_id", fmt.Sprintf("a árvore de metas tem no máximo %d níveis", MaxGoalDepth))
	}
	return nil
}

// Counted diz se a meta entra no agregado do pai: tudo menos as abandonadas.
func (n GoalNode) counted() bool { return n.Status != GoalAbandoned }

// countedChildren são os filhos que entram no agregado.
func (t GoalTree) countedChildren(id ID) []ID {
	var out []ID
	for _, c := range t.children[id] {
		if t.nodes[c].counted() {
			out = append(out, c)
		}
	}
	return out
}

// HasRollup diz se o progresso de id é o agregado das submetas (tem ao menos uma não abandonada).
func (t GoalTree) HasRollup(id ID) bool { return len(t.countedChildren(id)) > 0 }

// Progress é o progresso de 0 a 1: o da própria meta se ela não tem submetas contadas; senão a média simples do
// progresso das submetas (cada uma já agregada).
func (t GoalTree) Progress(id ID) float64 { return t.progress(id, 0) }

func (t GoalTree) progress(id ID, depth int) float64 {
	n, ok := t.nodes[id]
	if !ok {
		return 0
	}
	kids := t.countedChildren(id)
	if len(kids) == 0 || depth >= goalWalkCap {
		return GoalProgress(n.Start, n.Target, n.Current)
	}
	var sum float64
	for _, c := range kids {
		sum += t.progress(c, depth+1)
	}
	return sum / float64(len(kids))
}

// Complete diz se a meta-pai está concluída pelas submetas: tem submetas contadas e todas estão atingidas.
func (t GoalTree) Complete(id ID) bool {
	kids := t.countedChildren(id)
	if len(kids) == 0 {
		return false
	}
	for _, c := range kids {
		if t.nodes[c].Status != GoalAchieved {
			return false
		}
	}
	return true
}
