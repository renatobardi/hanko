package domain

import (
	"errors"
	"math/rand"
	"testing"
)

func ids(n int) []ID {
	out := make([]ID, n)
	for i := range out {
		out[i] = NewID()
	}
	return out
}

func TestWouldCycle(t *testing.T) {
	n := ids(4)
	e := DepEdges{}
	e.Add(n[0], n[1]) // 0 depende de 1
	e.Add(n[1], n[2]) // 1 depende de 2
	tests := []struct {
		name       string
		task, dep  ID
		wantCycle  bool
		wantAdding bool // o grafo ganha ciclo se a aresta entrar
	}{
		{"self", n[0], n[0], true, true},
		{"direto", n[1], n[0], true, true},
		{"transitivo", n[2], n[0], true, true},
		{"paralelo ok", n[0], n[2], false, false},
		{"nó novo ok", n[3], n[0], false, false},
		{"raiz depende do novo ok", n[2], n[3], false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := WouldCycle(e, tt.task, tt.dep); got != tt.wantCycle {
				t.Errorf("WouldCycle = %v, want %v", got, tt.wantCycle)
			}
			c := DepEdges{}
			for a, ds := range e {
				c[a] = append([]ID(nil), ds...)
			}
			c.Add(tt.task, tt.dep)
			if got := HasCycle(c); got != tt.wantAdding {
				t.Errorf("HasCycle depois da aresta = %v, want %v", got, tt.wantAdding)
			}
		})
	}
	if HasCycle(e) {
		t.Error("o grafo base é acíclico")
	}
}

// Propriedade: um grafo montado só com arestas aceitas por WouldCycle nunca tem ciclo, e uma aresta recusada é
// exatamente uma que fecharia ciclo. Muitas sementes, grafos pequenos (onde a colisão é frequente) e médios.
func TestPropertyRandomGraphNeverCycles(t *testing.T) {
	for seed := int64(1); seed <= 200; seed++ {
		rng := rand.New(rand.NewSource(seed))
		nodes := ids(3 + rng.Intn(18))
		e := DepEdges{}
		accepted, refused := 0, 0
		for i := 0; i < 400; i++ {
			a, b := nodes[rng.Intn(len(nodes))], nodes[rng.Intn(len(nodes))]
			would := WouldCycle(e, a, b)
			probe := DepEdges{}
			for k, v := range e {
				probe[k] = append([]ID(nil), v...)
			}
			probe.Add(a, b)
			if would != HasCycle(probe) {
				t.Fatalf("seed %d: WouldCycle=%v mas HasCycle(com a aresta)=%v", seed, would, HasCycle(probe))
			}
			if would {
				refused++
				continue
			}
			e.Add(a, b)
			accepted++
			if HasCycle(e) {
				t.Fatalf("seed %d: ciclo depois de aceitar %v → %v", seed, a, b)
			}
		}
		if accepted == 0 || refused == 0 && len(nodes) < 8 {
			t.Fatalf("seed %d: cenário degenerado (aceitas=%d recusadas=%d)", seed, accepted, refused)
		}
	}
}

// Propriedade: destravar em cascata. Em qualquer DAG, fechar uma task livre (sem dependência aberta) libera
// exatamente as tasks que passaram a ficar livres por causa dela, e fechando só tasks livres todo o grafo termina
// (nunca trava).
func TestPropertyCascadeUnblock(t *testing.T) {
	for seed := int64(1); seed <= 150; seed++ {
		rng := rand.New(rand.NewSource(seed))
		nodes := ids(2 + rng.Intn(25))
		e := DepEdges{}
		for i := 0; i < 3*len(nodes); i++ {
			a, b := nodes[rng.Intn(len(nodes))], nodes[rng.Intn(len(nodes))]
			if !WouldCycle(e, a, b) {
				e.Add(a, b)
			}
		}
		status := map[ID]TaskStatus{}
		for _, n := range nodes {
			status[n] = TaskTodo
		}
		free := func() map[ID]bool {
			out := map[ID]bool{}
			for _, n := range nodes {
				if status[n].Open() && OpenDependencies(e, status, n) == 0 {
					out[n] = true
				}
			}
			return out
		}
		closed := 0
		for closed < len(nodes) {
			before := free()
			if len(before) == 0 {
				t.Fatalf("seed %d: grafo travou com %d tasks abertas", seed, len(nodes)-closed)
			}
			var pool []ID
			for n := range before {
				pool = append(pool, n)
			}
			pick := pool[rng.Intn(len(pool))]
			if rng.Intn(4) == 0 {
				status[pick] = TaskCancelled // cancelar também libera
			} else {
				status[pick] = TaskDone
			}
			closed++
			after := free()
			want := map[ID]bool{}
			for n := range after {
				if !before[n] {
					want[n] = true
				}
			}
			got := ReleasedBy(e, status, pick)
			if len(got) != len(want) {
				t.Fatalf("seed %d: ReleasedBy=%d tasks, esperado %d", seed, len(got), len(want))
			}
			for _, g := range got {
				if !want[g] {
					t.Fatalf("seed %d: %v liberada sem ter ficado livre", seed, g)
				}
			}
		}
	}
}

func TestReleasedByNeedsEveryDependencyClosed(t *testing.T) {
	n := ids(3)
	e := DepEdges{}
	e.Add(n[2], n[0])
	e.Add(n[2], n[1])
	status := map[ID]TaskStatus{n[0]: TaskDone, n[1]: TaskTodo, n[2]: TaskTodo}
	if got := ReleasedBy(e, status, n[0]); len(got) != 0 {
		t.Fatalf("ainda há dependência aberta, nada liberado: %v", got)
	}
	status[n[1]] = TaskCancelled
	if got := ReleasedBy(e, status, n[1]); len(got) != 1 || got[0] != n[2] {
		t.Fatalf("a última dependência (cancelada) libera: %v", got)
	}
	status[n[2]] = TaskDone
	if got := ReleasedBy(e, status, n[1]); len(got) != 0 {
		t.Fatalf("task fechada não é liberada: %v", got)
	}
}

func TestResolveReviewPolicyPrecedence(t *testing.T) {
	rv, other := NewID(), NewID()
	yes, no := true, false
	persona := PersonaReview{RequireReview: true, ReviewerAgent: "codex", ReviewerModel: "m1"}
	tests := []struct {
		name          string
		task, project ReviewOverride
		persona       PersonaReview
		want          ReviewPolicy
	}{
		{"só a persona", ReviewOverride{}, ReviewOverride{}, persona,
			ReviewPolicy{Required: true, RequiredSource: ReviewLayerPersona, HasReviewer: true, ReviewerSource: ReviewLayerPersona, ReviewerAgent: "codex", ReviewerModel: "m1"}},
		{"persona sem nada", ReviewOverride{}, ReviewOverride{}, PersonaReview{},
			ReviewPolicy{RequiredSource: ReviewLayerPersona, ReviewerSource: ReviewLayerPersona}},
		{"projeto exige", ReviewOverride{}, ReviewOverride{RequireReview: &yes}, PersonaReview{},
			ReviewPolicy{Required: true, RequiredSource: ReviewLayerProject, ReviewerSource: ReviewLayerPersona}},
		{"task dispensa o que o projeto exige", ReviewOverride{RequireReview: &no}, ReviewOverride{RequireReview: &yes}, persona,
			ReviewPolicy{Required: false, RequiredSource: ReviewLayerTask, HasReviewer: true, ReviewerSource: ReviewLayerPersona, ReviewerAgent: "codex", ReviewerModel: "m1"}},
		{"projeto troca o revisor", ReviewOverride{}, ReviewOverride{ReviewerMode: ReviewerCustom, ReviewerPersonaID: &rv}, persona,
			ReviewPolicy{Required: true, RequiredSource: ReviewLayerPersona, HasReviewer: true, ReviewerSource: ReviewLayerProject, ReviewerPersonaID: &rv}},
		{"task vence o projeto no revisor", ReviewOverride{ReviewerMode: ReviewerCustom, ReviewerAgent: "claude", ReviewerModel: "m9"},
			ReviewOverride{ReviewerMode: ReviewerCustom, ReviewerPersonaID: &other}, persona,
			ReviewPolicy{Required: true, RequiredSource: ReviewLayerPersona, HasReviewer: true, ReviewerSource: ReviewLayerTask, ReviewerAgent: "claude", ReviewerModel: "m9"}},
		{"task sem revisor", ReviewOverride{ReviewerMode: ReviewerNone}, ReviewOverride{ReviewerMode: ReviewerCustom, ReviewerPersonaID: &other}, persona,
			ReviewPolicy{Required: true, RequiredSource: ReviewLayerPersona, ReviewerSource: ReviewLayerTask}},
		{"projeto sem revisor", ReviewOverride{}, ReviewOverride{ReviewerMode: ReviewerNone}, persona,
			ReviewPolicy{Required: true, RequiredSource: ReviewLayerPersona, ReviewerSource: ReviewLayerProject}},
		{"custom vazio cai na próxima camada", ReviewOverride{ReviewerMode: ReviewerCustom}, ReviewOverride{}, persona,
			ReviewPolicy{Required: true, RequiredSource: ReviewLayerPersona, HasReviewer: true, ReviewerSource: ReviewLayerPersona, ReviewerAgent: "codex", ReviewerModel: "m1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveReviewPolicy(tt.task, tt.project, tt.persona)
			if got.Required != tt.want.Required || got.RequiredSource != tt.want.RequiredSource || got.HasReviewer != tt.want.HasReviewer ||
				got.ReviewerSource != tt.want.ReviewerSource || got.ReviewerAgent != tt.want.ReviewerAgent || got.ReviewerModel != tt.want.ReviewerModel ||
				(got.ReviewerPersonaID == nil) != (tt.want.ReviewerPersonaID == nil) ||
				(got.ReviewerPersonaID != nil && *got.ReviewerPersonaID != *tt.want.ReviewerPersonaID) {
				t.Errorf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestReviewOverrideValidate(t *testing.T) {
	p := NewID()
	tests := []struct {
		name string
		o    ReviewOverride
		ok   bool
	}{
		{"zero", ReviewOverride{}, true},
		{"inherit", ReviewOverride{ReviewerMode: ReviewerInherit}, true},
		{"none", ReviewOverride{ReviewerMode: ReviewerNone}, true},
		{"custom persona", ReviewOverride{ReviewerMode: ReviewerCustom, ReviewerPersonaID: &p}, true},
		{"custom harness", ReviewOverride{ReviewerMode: ReviewerCustom, ReviewerAgent: "codex"}, true},
		{"custom vazio", ReviewOverride{ReviewerMode: ReviewerCustom}, false},
		{"persona e harness", ReviewOverride{ReviewerMode: ReviewerCustom, ReviewerPersonaID: &p, ReviewerAgent: "codex"}, false},
		{"modo desconhecido", ReviewOverride{ReviewerMode: "x"}, false},
		{"harness desconhecido", ReviewOverride{ReviewerMode: ReviewerCustom, ReviewerAgent: "outro"}, false},
		{"revisor solto sem custom", ReviewOverride{ReviewerMode: ReviewerNone, ReviewerAgent: "codex"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.o.Validate()
			if (err == nil) != tt.ok {
				t.Errorf("Validate = %v, ok esperado %v", err, tt.ok)
			}
			if err != nil && !errors.Is(err, ErrInvalid) {
				t.Errorf("erro deveria ser ErrInvalid: %v", err)
			}
		})
	}
}

func TestCoordinationErrorsWrapSentinels(t *testing.T) {
	for _, e := range []error{ErrTaskHeld, ErrTaskBlocked, ErrChildrenOpen} {
		if !errors.Is(e, ErrInvalid) {
			t.Errorf("%v deveria valer como ErrInvalid (o worker trata como nada a fazer)", e)
		}
	}
	if !errors.Is(ErrDependencyCycle, ErrConflict) {
		t.Error("ciclo é conflito")
	}
}
