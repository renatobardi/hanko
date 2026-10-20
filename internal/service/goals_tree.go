package service

import (
	"context"
	"fmt"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Metas em árvore (ADR-0042 §C). O banco recusa ciclo e profundidade (trigger goals_parent_guard); aqui a regra de
// domínio dá a mensagem certa, calcula o progresso agregado e propaga "atingida" pela cadeia até a raiz.

// GoalRef identifica uma meta numa cadeia ou lista de filhos.
type GoalRef struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
}

// goalForest é a árvore de metas de uma empresa mais o nome de cada nó.
type goalForest struct {
	tree domain.GoalTree
	refs map[domain.ID]GoalRef
}

func loadGoalForest(ctx context.Context, q *store.Queries, companyID domain.ID) (goalForest, error) {
	rows, err := q.ListGoalNodes(ctx, companyID)
	if err != nil {
		return goalForest{}, fmt.Errorf("list goal nodes: %w", err)
	}
	nodes := make([]domain.GoalNode, len(rows))
	refs := make(map[domain.ID]GoalRef, len(rows))
	for i, r := range rows {
		nodes[i] = domain.GoalNode{ID: r.ID, ParentID: r.ParentID, Status: domain.GoalStatus(r.Status),
			Start: r.StartValue, Target: r.TargetValue, Current: r.CurrentValue}
		refs[r.ID] = GoalRef{Slug: r.Slug, Title: r.Title}
	}
	return goalForest{tree: domain.NewGoalTree(nodes), refs: refs}, nil
}

// ancestors são as metas da raiz até o pai de id (sem o próprio id).
func (f goalForest) ancestors(id domain.ID) []GoalRef {
	path := f.tree.Path(id)
	if len(path) <= 1 {
		return nil
	}
	out := make([]GoalRef, 0, len(path)-1)
	for _, p := range path[:len(path)-1] {
		out = append(out, f.refs[p])
	}
	return out
}

func (f goalForest) children(id domain.ID) []GoalRef {
	kids := f.tree.Children(id)
	if len(kids) == 0 {
		return nil
	}
	out := make([]GoalRef, len(kids))
	for i, k := range kids {
		out[i] = f.refs[k]
	}
	return out
}

// apply preenche a parte de árvore da view: progresso agregado, cadeia, filhos e profundidade.
func (f goalForest) apply(v GoalView) GoalView {
	if !f.tree.Has(v.ID) {
		return v
	}
	v.Depth = f.tree.Depth(v.ID)
	v.Path = f.ancestors(v.ID)
	v.Children = f.children(v.ID)
	v.Rollup = f.tree.HasRollup(v.ID)
	if v.Rollup {
		v.ProgressPercent = f.tree.Progress(v.ID) * 100
	}
	if n := len(v.Path); n > 0 {
		v.ParentSlug = v.Path[n-1].Slug
	}
	return v
}

// checkParent confere o pai escolhido para a meta id (id pode ainda não existir): existe, não está abandonado,
// sem ciclo e dentro do limite de níveis.
func (f goalForest) checkParent(id domain.ID, parent *domain.ID, statusOf func(domain.ID) domain.GoalStatus) error {
	if parent == nil {
		return nil
	}
	if err := f.tree.CheckParent(id, parent); err != nil {
		return err
	}
	if statusOf != nil && statusOf(*parent) == domain.GoalAbandoned {
		return domain.Invalid("parent_id", "a meta-pai está abandonada")
	}
	return nil
}

// mapGoalErr traduz as violações do trigger e da FK de metas para erros de domínio.
func mapGoalErr(err error) error {
	switch constraintOf(err) {
	case "goals_parent_no_cycle":
		return domain.Invalid("parent_id", "a meta não pode ser ancestral de si mesma")
	case "goals_parent_depth":
		return domain.Invalid("parent_id", fmt.Sprintf("a árvore de metas tem no máximo %d níveis", domain.MaxGoalDepth))
	case "goals_parent_fk", "goals_parent_not_self":
		return domain.Invalid("parent_id", "goal not found in company")
	}
	return err
}

// syncGoalChain reavalia, de start até a raiz, as metas que têm submetas: ativa com todas as submetas contadas
// atingidas vira atingida; atingida cujo agregado caiu volta a ativa. Abandonada nunca muda aqui. Cada mudança grava o
// fato na mesma transação. Chamar depois de qualquer mudança que afete uma submeta (progresso, status, pai, criação).
func (t *txc) syncGoalChain(actor domain.Actor, companyID, start domain.ID) error {
	seen := map[domain.ID]bool{}
	cur := start
	for i := 0; i < 2*domain.MaxGoalDepth && !seen[cur]; i++ {
		seen[cur] = true
		forest, err := loadGoalForest(t.ctx, t.q, companyID)
		if err != nil {
			return err
		}
		if forest.tree.HasRollup(cur) {
			g, err := t.q.GetGoalByID(t.ctx, store.GetGoalByIDParams{CompanyID: companyID, ID: cur})
			if err != nil {
				return mapErr(err, "get goal")
			}
			complete := forest.tree.Complete(cur)
			switch {
			case complete && g.Status == string(domain.GoalActive):
				if err := t.q.SetGoalStatus(t.ctx, store.SetGoalStatusParams{CompanyID: companyID, ID: cur, Status: string(domain.GoalAchieved), AchievedAt: &t.now, Now: t.now}); err != nil {
					return fmt.Errorf("achieve parent goal: %w", err)
				}
				if err := t.activity(actor, companyID, "goal.achieved", "goal", cur, map[string]any{
					"slug": g.Slug, "title": g.Title, "via": "subgoals"}, ""); err != nil {
					return err
				}
			case !complete && g.Status == string(domain.GoalAchieved):
				if err := t.q.SetGoalStatus(t.ctx, store.SetGoalStatusParams{CompanyID: companyID, ID: cur, Status: string(domain.GoalActive), AchievedAt: nil, Now: t.now}); err != nil {
					return fmt.Errorf("reopen parent goal: %w", err)
				}
				if err := t.activity(actor, companyID, "goal.updated", "goal", cur, map[string]any{"status": string(domain.GoalActive), "via": "subgoals"}, ""); err != nil {
					return err
				}
			}
		}
		parent, ok := forest.tree.Parent(cur)
		if !ok {
			return nil
		}
		cur = parent
	}
	return nil
}
