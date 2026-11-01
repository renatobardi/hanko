package service

import (
	"fmt"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/export"
	"github.com/renatobardi/hanko/internal/secretref"
)

// Planejamento puro do merge do import em empresa existente (ADR-0057): decide, item a item, o que fazer com cada
// workspace, projeto, assinatura, persona, fluxo, skill e integração do arquivo. Sem banco, então vale também como
// motor da prévia e do instalador de pacotes de time (#24).

// Tipos de item do merge, na ordem em que aparecem na prévia.
const (
	KindWorkspaces    = "workspaces"
	KindProjects      = "projects"
	KindSubscriptions = "subscriptions"
	KindSkills        = "skills"
	KindPersonas      = "personas"
	KindFlows         = "flows"
	KindIntegrations  = "integrations"
)

var mergeKinds = []string{KindWorkspaces, KindProjects, KindSubscriptions, KindSkills, KindPersonas, KindFlows, KindIntegrations}

// ImportItem é um item do arquivo e o que acontece com ele (prévia e resultado).
type ImportItem struct {
	Kind   string
	Key    string // slug (nome, em assinaturas) no arquivo
	Name   string
	Action domain.ItemAction
	// Target é a chave final no destino: o novo slug quando renomeado, a chave existente quando pulado ou substituído.
	Target string
}

// existingKeys são as chaves que a empresa de destino já tem, por tipo.
type existingKeys map[string]map[string]bool

type mergeDecision struct {
	Action domain.ItemAction
	Target string
}

type mergePlan struct {
	dec   map[string]map[string]mergeDecision // tipo → chave no arquivo → decisão
	items []ImportItem
}

func (m mergePlan) decision(kind, key string) mergeDecision { return m.dec[kind][key] }

// docItems devolve (chave, nome) dos itens do arquivo de um tipo, na ordem do arquivo.
func docItems(doc export.Document, kind string) (keys, names []string) {
	add := func(k, n string) {
		keys = append(keys, k)
		names = append(names, n)
	}
	switch kind {
	case KindWorkspaces:
		for _, x := range doc.Workspaces {
			add(x.Slug, x.Name)
		}
	case KindProjects:
		for _, x := range doc.Projects {
			add(x.Slug, x.Name)
		}
	case KindSubscriptions:
		for _, x := range doc.Subscriptions {
			add(strings.TrimSpace(x.Name), strings.TrimSpace(x.Name))
		}
	case KindSkills:
		for _, x := range doc.Skills {
			add(x.Slug, x.Name)
		}
	case KindPersonas:
		for _, x := range doc.Personas {
			add(x.Slug, x.Name)
		}
	case KindFlows:
		for _, x := range doc.Flows {
			add(x.Slug, x.Name)
		}
	case KindIntegrations:
		for _, x := range doc.Integrations {
			add(x.Slug, x.Name)
		}
	}
	return keys, names
}

// planMerge aplica a ação padrão def e as exceções resolve ("tipo/chave" → ação) aos conflitos. Itens sem conflito
// são create. Exceção para tipo ou chave que o arquivo não tem é erro (quase sempre um erro de digitação).
func planMerge(doc export.Document, ex existingKeys, def domain.ConflictAction, resolve map[string]domain.ConflictAction) (mergePlan, error) {
	if def == "" {
		def = domain.ConflictSkip
	}
	if !def.Valid() {
		return mergePlan{}, domain.Invalid("on_conflict", "want skip, rename or replace")
	}
	known := map[string]map[string]bool{}
	for _, kind := range mergeKinds {
		keys, _ := docItems(doc, kind)
		known[kind] = map[string]bool{}
		for _, k := range keys {
			known[kind][k] = true
		}
	}
	for rk, act := range resolve {
		kind, key, _ := strings.Cut(rk, "/")
		if !act.Valid() {
			return mergePlan{}, domain.Invalid("resolve", fmt.Sprintf("%q: unknown action %q", rk, act))
		}
		if known[kind] == nil {
			return mergePlan{}, domain.Invalid("resolve", fmt.Sprintf("%q: unknown kind %q (want one of %v)", rk, kind, mergeKinds))
		}
		if !known[kind][key] {
			return mergePlan{}, domain.Invalid("resolve", fmt.Sprintf("%q is not in the file", rk))
		}
	}

	plan := mergePlan{dec: map[string]map[string]mergeDecision{}}
	for _, kind := range mergeKinds {
		keys, names := docItems(doc, kind)
		plan.dec[kind] = map[string]mergeDecision{}
		// nomes ocupados: os do destino, os do arquivo e os que já foram atribuídos por renomeação
		taken := map[string]bool{}
		for k := range ex[kind] {
			taken[k] = true
		}
		for _, k := range keys {
			taken[k] = true
		}
		for i, key := range keys {
			d := mergeDecision{Action: domain.ItemCreate, Target: key}
			if ex[kind][key] {
				act := def
				if r, ok := resolve[kind+"/"+key]; ok {
					act = r
				}
				d.Action = act.ItemOf()
				if act == domain.ConflictRename {
					d.Target = domain.RenameTo(key, kind == KindSubscriptions, func(s string) bool { return taken[s] })
					taken[d.Target] = true
				}
			}
			plan.dec[kind][key] = d
			plan.items = append(plan.items, ImportItem{Kind: kind, Key: key, Name: names[i], Action: d.Action, Target: d.Target})
		}
	}
	return plan, nil
}

// tally conta os itens por ação.
func (m mergePlan) tally() map[string]int {
	out := map[string]int{}
	for _, it := range m.items {
		out[string(it.Action)]++
	}
	return out
}

// written conta, por tipo, os itens que o merge grava (tudo menos skip).
func (m mergePlan) written() map[string]int {
	out := map[string]int{}
	for _, it := range m.items {
		if it.Action != domain.ItemSkip {
			out[it.Kind]++
		}
	}
	return out
}

// createPlan é a prévia de uma empresa nova: tudo create.
func createPlan(doc export.Document) []ImportItem {
	var out []ImportItem
	for _, kind := range mergeKinds {
		keys, names := docItems(doc, kind)
		for i, k := range keys {
			out = append(out, ImportItem{Kind: kind, Key: k, Name: names[i], Action: domain.ItemCreate, Target: k})
		}
	}
	return out
}

// ImportWarning é um aviso da prévia. Message é um formato pt-BR (com %d para Count) que a UI e a CLI traduzem.
type ImportWarning struct {
	Code    string
	Message string
	Count   int
}

// importWarnings monta os avisos da prévia. existing: o destino é uma empresa que já existe (só configuração entra).
func importWarnings(doc export.Document, p importPlan, active, existing bool) []ImportWarning {
	var out []ImportWarning
	add := func(code, msg string, n int) {
		if n > 0 {
			out = append(out, ImportWarning{Code: code, Message: msg, Count: n})
		}
	}
	add("workspace_paths_cleared", "%d workspace(s) chegam sem caminho (o caminho do arquivo não vale nesta instalação); ajuste depois", len(p.clearedPaths))
	refs := map[string]bool{}
	for _, c := range p.personaCfg {
		for _, r := range secretref.Find(string(c)) {
			refs[r.String()] = true
		}
	}
	add("secret_refs_to_create", "%d segredo(s) ${secret:…} citados nas personas precisam ser criados na empresa de destino", len(refs))
	add("integrations_disabled", "%d integração(ões) entram desligadas e sem segredos; ligue depois de configurar", len(doc.Integrations))
	if !active {
		add("personas_paused", "%d persona(s) entram pausadas até alguém liberar", len(doc.Personas))
		flows := 0
		for _, f := range doc.Flows {
			if f.Enabled {
				flows++
			}
		}
		add("flows_disabled", "%d fluxo(s) entram desligados", flows)
	}
	if existing {
		ext := len(doc.ExternalAgents) + len(doc.ExternalHosts) + len(doc.RepoPairings) + len(doc.AlertSettings)
		add("external_not_merged", "%d item(ns) de configuração externa (agentes, hosts, emparelhamentos, limites de alerta) não entram numa empresa existente; só numa empresa nova", ext)
		hist := len(doc.Tasks) + len(doc.Runs) + len(doc.Artifacts)
		add("history_not_merged", "%d item(ns) de histórico (tasks, execuções, artefatos) não entram numa empresa existente; só a configuração", hist)
	}
	return out
}
