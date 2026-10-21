package domain

import (
	"errors"
	"math"
	"testing"
)

// node monta uma meta de 0 a 100 no valor cur.
func gnode(id ID, parent *ID, st GoalStatus, cur float64) GoalNode {
	return GoalNode{ID: id, ParentID: parent, Status: st, Start: 0, Target: 100, Current: cur}
}

func TestGoalTreePathDepthHeight(t *testing.T) {
	a, b, c, d := NewID(), NewID(), NewID(), NewID()
	tr := NewGoalTree([]GoalNode{gnode(a, nil, GoalActive, 0), gnode(b, &a, GoalActive, 0), gnode(c, &b, GoalActive, 0), gnode(d, &a, GoalActive, 0)})
	if got := tr.Path(c); len(got) != 3 || got[0] != a || got[1] != b || got[2] != c {
		t.Errorf("path: %v", got)
	}
	if tr.Depth(a) != 1 || tr.Depth(c) != 3 || tr.Depth(ID{}) != 0 {
		t.Errorf("depth: %d %d", tr.Depth(a), tr.Depth(c))
	}
	if tr.Height(a) != 3 || tr.Height(d) != 1 || tr.Height(b) != 2 {
		t.Errorf("height: %d %d %d", tr.Height(a), tr.Height(d), tr.Height(b))
	}
	if !tr.Has(a) || tr.Has(NewID()) {
		t.Error("has")
	}
	if len(tr.Children(a)) != 2 {
		t.Errorf("filhos de a: %v", tr.Children(a))
	}
	if p, ok := tr.Parent(c); !ok || p != b {
		t.Error("parent")
	}
	if _, ok := tr.Parent(a); ok {
		t.Error("raiz não tem pai")
	}
	if !tr.InSubtree(a, c) || tr.InSubtree(d, c) {
		t.Error("subtree")
	}
}

func TestGoalTreeCheckParent(t *testing.T) {
	ids := make([]ID, 5)
	for i := range ids {
		ids[i] = NewID()
	}
	// cadeia de 4 níveis: 0 → 1 → 2 → 3; ids[4] solta
	nodes := []GoalNode{gnode(ids[0], nil, GoalActive, 0), gnode(ids[1], &ids[0], GoalActive, 0),
		gnode(ids[2], &ids[1], GoalActive, 0), gnode(ids[3], &ids[2], GoalActive, 0), gnode(ids[4], nil, GoalActive, 0)}
	tr := NewGoalTree(nodes)
	cases := []struct {
		name   string
		id     ID
		parent *ID
		ok     bool
	}{
		{"virar raiz", ids[3], nil, true},
		{"sob a raiz", ids[4], &ids[0], true},
		{"nível 5 recusado", ids[4], &ids[3], false},
		{"ela mesma", ids[1], &ids[1], false},
		{"descendente (ciclo)", ids[0], &ids[3], false},
		{"inexistente", ids[4], idPtr(NewID()), false},
		{"subárvore pesada não cabe", ids[1], &ids[4], true}, // 4 → 1 → 2 → 3 = 4 níveis
		{"meta nova (ainda fora da árvore)", NewID(), &ids[2], true},
		{"meta nova sob o último nível", NewID(), &ids[3], false},
	}
	for _, c := range cases {
		err := tr.CheckParent(c.id, c.parent)
		if (err == nil) != c.ok {
			t.Errorf("%s: err=%v, ok=%v", c.name, err, c.ok)
		}
		if err != nil && !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: erro deveria ser de validação: %v", c.name, err)
		}
	}
	// uma subárvore de 2 níveis sob o nível 3 estoura
	tr2 := NewGoalTree(append(nodes[:4:4], gnode(ids[4], nil, GoalActive, 0), gnode(NewID(), &ids[4], GoalActive, 0)))
	if err := tr2.CheckParent(ids[4], &ids[2]); err == nil {
		t.Error("subárvore de 2 níveis sob o nível 3 passaria de 4")
	}
}

func idPtr(id ID) *ID { return &id }

func TestGoalTreeProgress(t *testing.T) {
	root, a, b, c, aband := NewID(), NewID(), NewID(), NewID(), NewID()
	tr := NewGoalTree([]GoalNode{
		gnode(root, nil, GoalActive, 0),
		gnode(a, &root, GoalAchieved, 100),
		gnode(b, &root, GoalActive, 50),
		gnode(c, &b, GoalActive, 20), // b agrega c: 0,2 (o 50 de b deixa de valer)
		gnode(aband, &root, GoalAbandoned, 0),
	})
	approx := func(got, want float64) {
		t.Helper()
		if math.Abs(got-want) > 1e-9 {
			t.Errorf("got %v, want %v", got, want)
		}
	}
	approx(tr.Progress(c), 0.2)
	approx(tr.Progress(b), 0.2)
	approx(tr.Progress(root), (1+0.2)/2) // abandonada não conta
	approx(tr.Progress(a), 1)
	approx(tr.Progress(NewID()), 0)
	if !tr.HasRollup(root) || tr.HasRollup(c) {
		t.Error("rollup")
	}
	if tr.Complete(root) || tr.Complete(c) {
		t.Error("não completa")
	}

	// todos os filhos contados atingidos → completa; se só restam abandonados, vale o próprio valor
	p, k1, k2 := NewID(), NewID(), NewID()
	tr = NewGoalTree([]GoalNode{gnode(p, nil, GoalActive, 30), gnode(k1, &p, GoalAchieved, 100), gnode(k2, &p, GoalAbandoned, 0)})
	if !tr.Complete(p) {
		t.Error("pai completo")
	}
	tr = NewGoalTree([]GoalNode{gnode(p, nil, GoalActive, 30), gnode(k2, &p, GoalAbandoned, 0)})
	approx(tr.Progress(p), 0.3)
	if tr.HasRollup(p) || tr.Complete(p) {
		t.Error("só abandonadas: sem rollup")
	}
}

func TestGoalTreeCorruptCycleDoesNotHang(t *testing.T) {
	a, b := NewID(), NewID()
	tr := NewGoalTree([]GoalNode{gnode(a, &b, GoalActive, 0), gnode(b, &a, GoalActive, 0)})
	if n := len(tr.Path(a)); n == 0 || n > goalWalkCap {
		t.Errorf("path de ciclo corrompido: %d", n)
	}
	_ = tr.Height(a)
	_ = tr.Progress(a)
}
